package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"strconv"
	"testing"

	"example.com/godj-project-bundle/records"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/query"
)

type annotationReference struct {
	Input []map[string]any
	Cases []struct {
		Name   string
		Result any
		Error  *struct {
			Type, Message string
			SQLState      *string `json:"sqlstate"`
		}
	}
}
type annotationReadProbe struct {
	db.Session
	queries int
}

func (p *annotationReadProbe) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	p.queries++
	return p.Session.Query(ctx, plan)
}
func (p *annotationReadProbe) ValidateSession(ctx context.Context) error {
	if session, ok := p.Session.(db.SessionValidator); ok {
		return session.ValidateSession(ctx)
	}
	return ctx.Err()
}

func TestNamedAnnotationReference(t *testing.T) {
	withAnnotationBackends(t, func(t *testing.T, backend annotationBackend, vendor string, _ annotationPhysicalWriter) {
		data, err := os.ReadFile("django61-" + vendor + ".json")
		check(t, err)
		var fixture annotationReference
		check(t, json.Unmarshal(data, &fixture))
		if len(fixture.Cases) != 77 {
			t.Fatal("annotation inventory differs")
		}
		seedAnnotationReference(t, backend, fixture.Input)
		before := annotationStorage(t, backend)
		for _, record := range fixture.Cases {
			t.Run(record.Name, func(t *testing.T) {
				var actual any
				var failure error
				queries := 0
				rollback := errors.New("annotation reference rollback")
				err := backend.Atomic(t.Context(), func(session db.Session) error {
					probe := &annotationReadProbe{Session: session}
					actual, failure = runAnnotationReference(t, probe, session, record.Name)
					queries = probe.queries
					return rollback
				})
				if !errors.Is(err, rollback) {
					t.Fatal("annotation reference rollback failed", err)
				}
				if !reflect.DeepEqual(before, annotationStorage(t, backend)) {
					t.Fatal("annotation comparison changed complete stored rows or relations")
				}
				// These are explicit existing Go contracts, not unexecuted cases.
				// Names reserve source metadata even after projection; numeric
				// domains need explicit conversion and SQLite cannot publish an
				// integer outside the generated Go int64 domain.
				boundary := record.Name == "projection_excluded_field_shadow" || record.Name == "alias_name_1" || record.Name == "alias_name_part__name" || record.Name == "mixed_integer_float"
				if boundary {
					var typed *query.Error
					if record.Error != nil || record.Result == nil || actual != nil || !errors.As(failure, &typed) || typed.Code != query.CodeInvalidPlan || queries != 0 {
						t.Fatal("declared Go annotation boundary differs", record.Name, actual, failure, queries)
					}
					return
				}
				if record.Name == "integer_constant_overflow" && vendor == "sqlite" {
					if record.Error != nil || record.Result == nil || actual != nil || !errors.Is(failure, &query.Error{Category: query.CategoryField, Code: query.CodeInvalidValue}) || queries != 1 {
						t.Fatal("SQLite annotation escaped int64 result domain", actual, failure, queries)
					}
					return
				}
				if record.Error != nil {
					if actual != nil || failure == nil {
						t.Fatal("annotation error differs", actual, failure, record.Error)
					}
					if record.Error.SQLState != nil {
						var native *pgconn.PgError
						if !errors.As(failure, &native) || native.Code != *record.Error.SQLState || queries != 1 {
							t.Fatal("annotation native SQLSTATE differs", failure, record.Error, queries)
						}
					} else {
						var typed *query.Error
						if !errors.As(failure, &typed) || queries != 0 {
							t.Fatal("annotation configuration error reached I/O", failure, queries)
						}
					}
					return
				}
				check(t, failure)
				got, err := canonicalAnnotation(actual)
				check(t, err)
				want, err := canonicalAnnotation(record.Result)
				check(t, err)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("annotation %s differs:\n%#v\nwant %#v", record.Name, got, want)
				}
			})
		}
	})
}

func annotationStorage(t *testing.T, backend db.Queryer) any {
	t.Helper()
	a, err := records.CategoryObjects.Using(backend).OrderBy(records.CategoryFields.ID.Asc()).All(t.Context())
	check(t, err)
	b, err := records.TagObjects.Using(backend).OrderBy(records.TagFields.ID.Asc()).All(t.Context())
	check(t, err)
	c, err := records.MeasureObjects.Using(backend).OrderBy(records.MeasureFields.ID.Asc()).All(t.Context())
	check(t, err)
	d, err := records.MeasureTagObjects.Using(backend).OrderBy(records.MeasureTagFields.ID.Asc()).All(t.Context())
	check(t, err)
	return []any{a, b, c, d}
}

func seedAnnotationReference(t *testing.T, backend db.Session, inputs []map[string]any) {
	t.Helper()
	var categories, tags []int64
	for _, name := range []string{"alpha", "beta", "empty"} {
		row, err := records.CategoryObjects.Create(t.Context(), backend, records.NewCategoryCreate(name))
		check(t, err)
		categories = append(categories, row.ID)
	}
	for _, name := range []string{"red", "blue", "green"} {
		row, err := records.TagObjects.Create(t.Context(), backend, records.NewTagCreate(name))
		check(t, err)
		tags = append(tags, row.ID)
	}
	integer := func(raw any) int64 {
		n, err := strconv.ParseInt(raw.(map[string]any)["text"].(string), 10, 64)
		check(t, err)
		return n
	}
	ids := map[string]int64{}
	for _, input := range inputs {
		create := records.NewMeasureCreate(input["code"].(string), input["enabled"].(bool), integer(input["amount"]))
		if value := input["group_key"]; value != nil {
			create = create.WithGroupKey(value.(string))
		}
		if value := input["other"]; value != nil {
			create = create.WithOther(integer(value))
		}
		if value := input["score"]; value != nil {
			n, err := strconv.ParseFloat(value.(map[string]any)["text"].(string), 64)
			check(t, err)
			create = create.WithScore(n)
		}
		if value := input["price"]; value != nil {
			n, err := decimal.Parse(value.(map[string]any)["text"].(string))
			check(t, err)
			create = create.WithPrice(n)
		}
		if value := input["elapsed"]; value != nil {
			n, err := strconv.ParseInt(value.(map[string]any)["microseconds"].(string), 10, 64)
			check(t, err)
			create = create.WithElapsed(duration.FromMicroseconds(n))
		}
		if value := input["document"]; value != nil {
			data, err := json.Marshal(annotationJSONInput(value))
			check(t, err)
			document, err := jsonvalue.Parse(data)
			check(t, err)
			create = create.WithDocument(document)
		}
		if value := input["category_id"]; value != nil {
			create = create.WithCategoryID(categories[integer(value)-1])
		}
		row, err := records.MeasureObjects.Create(t.Context(), backend, create)
		check(t, err)
		ids[row.Code] = row.ID
	}
	for _, links := range []struct {
		code string
		tags []int
	}{{"a", []int{0, 1}}, {"b", []int{0}}, {"c", []int{1}}, {"e", []int{2}}} {
		for _, tag := range links.tags {
			_, err := records.MeasureTagObjects.Create(t.Context(), backend, records.NewMeasureTagCreate(ids[links.code], tags[tag]))
			check(t, err)
		}
	}
}

func annotationJSONInput(raw any) any {
	switch value := raw.(type) {
	case map[string]any:
		if kind, ok := value["type"].(string); ok && (kind == "int" || kind == "float") {
			return json.Number(value["text"].(string))
		}
		result := map[string]any{}
		for key, item := range value {
			result[key] = annotationJSONInput(item)
		}
		return result
	case []any:
		result := make([]any, len(value))
		for index, item := range value {
			result[index] = annotationJSONInput(item)
		}
		return result
	default:
		return raw
	}
}

func canonicalAnnotation(raw any) (any, error) {
	if value, ok := raw.(query.Value); ok {
		switch value.Kind() {
		case query.ValueNull:
			return nil, nil
		case query.ValueInteger:
			n, _ := value.Integer()
			return canonicalAnnotation(n)
		case query.ValueFloat:
			n, _ := value.Float()
			return canonicalAnnotation(n)
		case query.ValueString:
			n, _ := value.String()
			return n, nil
		case query.ValueBoolean:
			n, _ := value.Boolean()
			return n, nil
		case query.ValueDecimal:
			n, _ := value.Decimal()
			return map[string]any{"type": "Decimal", "text": n.String()}, nil
		case query.ValueDuration:
			n, _ := value.Duration()
			micros, err := n.TotalMicroseconds()
			return map[string]any{"type": "timedelta", "microseconds": strconv.FormatInt(micros, 10)}, err
		case query.ValueJSON:
			n, _ := value.JSON()
			decoded, err := n.Decode()
			if err != nil {
				return nil, err
			}
			return canonicalAnnotation(decoded)
		default:
			return nil, fmt.Errorf("unexpected annotation kind %s", value.Kind())
		}
	}
	switch value := raw.(type) {
	case nil, string, bool:
		return value, nil
	case int64:
		return map[string]any{"type": "int", "text": strconv.FormatInt(value, 10)}, nil
	case json.Number:
		n, err := strconv.ParseInt(string(value), 10, 64)
		if err != nil {
			return nil, err
		}
		return canonicalAnnotation(n)
	case float64:
		return map[string]any{"type": "float", "bits": strconv.FormatUint(math.Float64bits(value), 16)}, nil
	case []any:
		result := make([]any, len(value))
		for index, item := range value {
			converted, err := canonicalAnnotation(item)
			if err != nil {
				return nil, err
			}
			result[index] = converted
		}
		return result, nil
	case map[string]any:
		if kind, ok := value["type"].(string); ok {
			switch kind {
			case "int", "timedelta":
				return value, nil
			case "float":
				n, err := strconv.ParseFloat(value["text"].(string), 64)
				if err != nil {
					return nil, err
				}
				return canonicalAnnotation(n)
			case "Decimal":
				n, err := decimal.Parse(value["text"].(string))
				if err != nil {
					return nil, err
				}
				return map[string]any{"type": kind, "text": n.String()}, nil
			}
		}
		result := map[string]any{}
		for key, item := range value {
			converted, err := canonicalAnnotation(item)
			if err != nil {
				return nil, err
			}
			result[key] = converted
		}
		return result, nil
	default:
		return nil, fmt.Errorf("unexpected annotation value %T", raw)
	}
}
