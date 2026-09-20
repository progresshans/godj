package consumer_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"example.com/godj-json/models"
	"example.com/godj-json/project"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

//go:embed projection_sqlite_reference.json
var projectionSQLiteReference []byte

//go:embed projection_postgres_reference.json
var projectionPostgresReference []byte

type projectionObservation struct {
	Label, JSON string
	Missing     bool
	RootNull    bool `json:"root_null"`
}
type projectionCase struct {
	Name, Exception string
	Path            []any
	Rows            []projectionObservation
}
type projectedJSON struct {
	Label           string
	Whole, Selected *jsonvalue.Value
}

func projectionExpected(t *testing.T, name string, row projectionObservation, native bool) (*jsonvalue.Value, bool) {
	t.Helper()
	missing, raw, deviation := row.Missing, row.JSON, false
	if name == "numeric_key" {
		switch row.Label {
		case "numeric_key":
			missing, raw, deviation = false, `"zero"`, true
		case "root_array":
			missing, deviation = true, true
		}
	}
	if native && (name == "index_zero" || name == "numeric_key") && slices.Contains([]string{"json_null", "scalar_string", "scalar_one", "scalar_false"}, row.Label) {
		missing, deviation = true, true
	}
	if !native {
		if name == "a" {
			if corrected, ok := map[string]string{"key_string_null": `"null"`, "key_string_false": `"false"`, "key_string_true": `"true"`, "key_string_one": `"1"`, "key_string_object": `"{}"`, "key_string_array": `"[1]"`, "huge": `340282366920938463463374607431768211455`}[row.Label]; ok {
				raw, deviation = corrected, true
			}
		}
		if name == "empty_key" && row.Label == "nul_key" || name == "nul_key" && row.Label == "empty_key" {
			missing, deviation = true, true
		}
		if name == "nul_key" && row.Label == "empty_and_nul" {
			raw, deviation = `"nul"`, true
		}
	}
	if missing {
		return nil, deviation
	}
	value := document(t, raw)
	return &value, deviation
}

func verifyJSONProjection(t *testing.T, backend jsonBackend, native bool) {
	t.Helper()
	raw, count := projectionSQLiteReference, 32
	if native {
		raw, count = projectionPostgresReference, 30
	}
	var reference struct {
		Samples []struct {
			Label string
			Raw   *string
		}
		Projections []projectionCase
	}
	if err := json.Unmarshal(raw, &reference); err != nil {
		t.Fatal(err)
	}
	if len(reference.Samples) != count || len(reference.Projections) != 8 {
		t.Fatal("projection reference inventory changed")
	}
	names := make([]string, 0, count)
	for _, sample := range reference.Samples {
		name := "projection_" + sample.Label
		names = append(names, name)
		value := jsonvalue.Null()
		if sample.Raw != nil {
			value = document(t, *sample.Raw)
		}
		input := models.NewRecordCreate(name, value)
		if sample.Raw != nil {
			input = input.WithPayload(value)
		}
		if _, err := models.RecordObjects.Create(t.Context(), backend, input); err != nil {
			t.Fatal(err)
		}
	}
	base := models.RecordObjects.Using(backend).Filter(models.RecordFields.Label.In(names...)).OrderBy(models.RecordFields.ID.Asc())
	deviations, rejected := 0, 0
	for _, tc := range reference.Projections {
		segments := make([]query.JSONPathSegment, len(tc.Path))
		for i, segment := range tc.Path {
			switch s := segment.(type) {
			case string:
				segments[i] = query.JSONKey(s)
			case float64:
				if s != 0 {
					t.Fatal("unexpected reference index")
				}
				segments[i] = query.JSONIndex(0)
			default:
				t.Fatal("invalid reference path")
			}
		}
		field := models.RecordFields.Payload.At(segments...)
		projection := orm.Project3(models.RecordFields.Label, models.RecordFields.Payload, field, func(label string, whole, selected *jsonvalue.Value) projectedJSON {
			return projectedJSON{label, whole, selected}
		})
		if native && tc.Name == "nul_key" {
			if tc.Exception != "DataError" || tc.Rows != nil {
				t.Fatal("native NUL projection reference changed")
			}
			rejected++
			empty, err := base.Limit(0)
			if err != nil {
				t.Fatal(err)
			}
			for _, source := range []orm.QuerySet[models.Record]{base, empty, base.Filter(models.RecordFields.ID.In())} {
				if rows, err := orm.SelectInto(t.Context(), source, projection); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidValue}) || rows != nil {
					t.Fatal("NUL projection bypassed preflight", err)
				}
			}
			continue
		}
		if tc.Exception != "" || len(tc.Rows) != count {
			t.Fatal("unexpected projection reference result", tc.Name, tc.Exception)
		}
		rows, err := orm.SelectInto(t.Context(), base, projection)
		if err != nil || len(rows) != count {
			t.Fatal(tc.Name, err)
		}
		required := orm.Project1(models.RecordFields.Required.At(segments...), func(value *jsonvalue.Value) *jsonvalue.Value { return value })
		requiredRows, err := orm.SelectInto(t.Context(), base, required)
		if err != nil || len(requiredRows) != count {
			t.Fatal("required source path projection", err)
		}
		for i, referenceRow := range tc.Rows {
			want, deviation := projectionExpected(t, tc.Name, referenceRow, native)
			if deviation {
				deviations++
			}
			row := rows[i]
			if strings.TrimPrefix(row.Label, "projection_") != referenceRow.Label || (row.Whole == nil) != referenceRow.RootNull {
				t.Fatal("projection row/source identity changed", tc.Name, row.Label)
			}
			for _, value := range []*jsonvalue.Value{row.Selected, requiredRows[i]} {
				if value == nil && want == nil {
					continue
				}
				if value == nil || want == nil || *value != *want {
					t.Fatalf("projection %s %s: got %v want %v", tc.Name, referenceRow.Label, value, want)
				}
			}
		}
	}
	if native && (deviations != 10 || rejected != 1) || !native && (deviations != 12 || rejected != 0) {
		t.Fatal("projection deviation/exception inventory changed", deviations, rejected)
	}
	verifyJSONProjectionExecution(t, backend, native, base)
}

func verifyJSONProjectionExecution(t *testing.T, backend jsonBackend, native bool, base orm.QuerySet[models.Record]) {
	t.Helper()
	segments := []query.JSONPathSegment{query.JSONKey("a")}
	path := models.RecordFields.Payload.At(segments...)
	projection := orm.Project2(models.RecordFields.Label, path, func(label string, value *jsonvalue.Value) projectedJSON {
		return projectedJSON{Label: label, Selected: value}
	})
	segments[0] = query.JSONKey("changed")
	// Each SELECT path contributes a parameter before WHERE paths/values and
	// pagination. Multiple selections share source metadata, not destinations.
	multiple := orm.Project4(models.RecordFields.Label, path, models.RecordFields.Payload.At(query.JSONKey("a"), query.JSONKey("b"), query.JSONIndex(0)), models.RecordFields.Payload, func(label string, one, two, whole *jsonvalue.Value) []*jsonvalue.Value {
		return []*jsonvalue.Value{one, two, whole}
	})
	source := base.Filter(orm.Or(path.Exact(jsonvalue.Null()), models.RecordFields.Label.Exact("projection_nested")))
	source, err := source.Offset(1)
	if err != nil {
		t.Fatal(err)
	}
	source, err = source.Limit(1)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := orm.SelectInto(t.Context(), source, multiple)
	if err != nil || len(rows) != 1 || len(rows[0]) != 3 || rows[0][0] == nil || rows[0][1] == nil || rows[0][2] == nil || rows[0][0].Text != `{"b":[null,false]}` || *rows[0][1] != jsonvalue.Null() || rows[0][2].Text != `{"a":{"b":[null,false]}}` {
		t.Fatal("projection/WHERE/pagination parameter order", rows, err)
	}
	*rows[0][0] = document(t, `"changed"`)
	again, err := orm.SelectInto(t.Context(), source, multiple)
	if err != nil || len(again) != 1 || again[0][0].Text != `{"b":[null,false]}` || again[0][0] == again[0][1] || rows[0][0] == again[0][0] {
		t.Fatal("projection destinations or caller results alias", err)
	}
	// Projection evaluates its own selected row shape even when the source's
	// model cache is warm. It must not extract paths from cached Go models.
	original := document(t, `{"a":1}`)
	warmRecord, err := models.RecordObjects.Create(t.Context(), backend, models.NewRecordCreate("projection_warm", original).WithPayload(original))
	if err != nil {
		t.Fatal(err)
	}
	warm := models.RecordObjects.Using(backend).Filter(models.RecordFields.ID.Exact(warmRecord.ID))
	if cached, err := warm.All(t.Context()); err != nil || len(cached) != 1 {
		t.Fatal(err)
	}
	if _, err := models.RecordObjects.Update(t.Context(), backend, warmRecord, models.RecordPatch{}.WithPayload(document(t, `{"a":7}`))); err != nil {
		t.Fatal(err)
	}
	got, err := orm.SelectInto(t.Context(), warm, projection)
	if err != nil || len(got) != 1 || got[0].Selected == nil || got[0].Selected.Text != "7" {
		t.Fatal("warm source projection reused cached model", err)
	}
	got[0].Selected.Text = "changed"
	got, err = orm.SelectInto(t.Context(), warm, projection)
	if err != nil || got[0].Selected.Text != "7" {
		t.Fatal("projection result mutated source", err)
	}
	if cached, err := warm.All(t.Context()); err != nil || len(cached) != 1 || cached[0].Payload == nil || *cached[0].Payload != original {
		t.Fatal("projection replaced source model cache", err)
	}
	// DISTINCT compares selected JSON values in the database's own domain.
	distinctBase := models.RecordObjects.Using(backend).Filter(models.RecordFields.Label.In("projection_sql_null", "projection_empty_object", "projection_key_null", "projection_key_string_null", "projection_key_one", "projection_key_float")).Distinct()
	values, err := orm.SelectInto(t.Context(), distinctBase, orm.Project1(path, func(value *jsonvalue.Value) *jsonvalue.Value { return value }))
	wantCount := 5
	if native {
		wantCount = 4
	}
	if err != nil || len(values) != wantCount {
		t.Fatal("distinct JSON path domain", len(values), err)
	}
	counts := map[string]int{}
	for _, value := range values {
		if value == nil {
			counts["missing"]++
		} else {
			counts[value.Text]++
		}
	}
	if counts["missing"] != 1 || counts["null"] != 1 || counts[`"null"`] != 1 || counts["1"]+counts["1.0"] != wantCount-3 {
		t.Fatal("distinct lost SQL/JSON null or numeric domain", counts)
	}
	if !native && (counts["1"] != 1 || counts["1.0"] != 1) {
		t.Fatal("SQLite path spelling collapsed", counts)
	}
	if _, err := orm.SelectInto(t.Context(), distinctBase.OrderBy(models.RecordFields.ID.Asc()), orm.Project1(path, func(value *jsonvalue.Value) *jsonvalue.Value { return value })); !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) {
		t.Fatal("DISTINCT ordered by an unselected field", err)
	}
	var before uint64
	if !native {
		before = backend.(*sqlite.Backend).QueryCount()
	}
	for _, invalid := range []orm.Projection[models.Record, *jsonvalue.Value]{orm.Project1(models.RecordFields.Payload.At(), func(v *jsonvalue.Value) *jsonvalue.Value { return v }), orm.Project1(orm.JSONPathField[models.Record]{}, func(v *jsonvalue.Value) *jsonvalue.Value { return v }), orm.Project1(path, (func(*jsonvalue.Value) *jsonvalue.Value)(nil))} {
		if result, err := orm.SelectInto(t.Context(), base, invalid); err == nil || result != nil {
			t.Fatal("invalid projection accepted")
		}
	}
	duplicate := orm.Project2(path, models.RecordFields.Payload.At(query.JSONKey("a")), func(a, b *jsonvalue.Value) int { return 1 })
	if _, err := orm.SelectInto(t.Context(), base, duplicate); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatal("duplicate path selection accepted", err)
	}
	for _, empty := range []orm.QuerySet[models.Record]{base.Filter(models.RecordFields.ID.In()), func() orm.QuerySet[models.Record] {
		q, err := base.Limit(0)
		if err != nil {
			t.Fatal(err)
		}
		return q
	}()} {
		if result, err := orm.SelectInto(t.Context(), empty, projection); err != nil || result == nil || len(result) != 0 {
			t.Fatal("empty projection result", err)
		}
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := orm.SelectInto(canceled, base, projection); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled projection", err)
	}
	if !native && backend.(*sqlite.Backend).QueryCount() != before {
		t.Fatal("invalid/empty/canceled projection performed I/O")
	}
	relations, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	relatedProjection := orm.Project1(relations.ModelsLink.Record.Payload.At(query.JSONKey("a")), func(v *jsonvalue.Value) *jsonvalue.Value { return v })
	if _, err := orm.SelectInto(t.Context(), models.LinkObjects.Using(backend), relatedProjection); !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) {
		t.Fatal("related projection widened silently", err)
	}
	if !native {
		verifyJSONProjectionFailure(t, backend.(*sqlite.Backend))
	}
}

func verifyJSONProjectionFailure(t *testing.T, backend *sqlite.Backend) {
	t.Helper()
	value := document(t, `{"a":1}`)
	good, err := models.RecordObjects.Create(t.Context(), backend, models.NewRecordCreate("projection_before_bad", value).WithPayload(value))
	if err != nil {
		t.Fatal(err)
	}
	bad, err := models.RecordObjects.Create(t.Context(), backend, models.NewRecordCreate("projection_bad", value).WithPayload(value))
	if err != nil {
		t.Fatal(err)
	}
	// A foreign writer can satisfy SQLite JSON_VALID while violating GoDj's
	// duplicate-key boundary. No prefix of a failed projection is published.
	table := (models.RecordDescriptor{}).Metadata().DBTable
	if _, err := backend.ExecContext(t.Context(), `UPDATE "`+table+`" SET payload=? WHERE id=?`, `{"a":1,"a":2}`, bad.ID); err != nil {
		t.Fatal(err)
	}
	source := models.RecordObjects.Using(backend).Filter(models.RecordFields.ID.In(good.ID, bad.ID)).OrderBy(models.RecordFields.ID.Asc())
	projection := orm.Project1(models.RecordFields.Payload.At(query.JSONKey("a")), func(v *jsonvalue.Value) *jsonvalue.Value { return v })
	if rows, err := orm.SelectInto(t.Context(), source, projection); err == nil || rows != nil {
		t.Fatal("failed projection published partial rows")
	}
	if _, err := backend.ExecContext(t.Context(), `UPDATE "`+table+`" SET payload=? WHERE id=?`, `{"a":2}`, bad.ID); err != nil {
		t.Fatal(err)
	}
	rows, err := orm.SelectInto(t.Context(), source, projection)
	if err != nil || len(rows) != 2 || rows[0] == nil || rows[1] == nil || rows[0].Text != "1" || rows[1].Text != "2" {
		t.Fatal("projection could not retry after failed scan", err)
	}
}
