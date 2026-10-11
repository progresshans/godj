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

	"example.com/godj-project-bundle/project"
	"example.com/godj-project-bundle/records"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

type computedReference struct {
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

func seedComputed(t *testing.T, backend db.Mutator, inputs []map[string]any) {
	t.Helper()
	for _, input := range inputs {
		integer := func(raw any) int64 {
			value, err := strconv.ParseInt(raw.(map[string]any)["text"].(string), 10, 64)
			check(t, err)
			return value
		}
		create := records.NewMeasureCreate(input["code"].(string), input["enabled"].(bool), integer(input["amount"]))
		if value := input["group_key"]; value != nil {
			create = create.WithGroupKey(value.(string))
		}
		if value := input["other"]; value != nil {
			create = create.WithOther(integer(value))
		}
		if value := input["score"]; value != nil {
			number, err := strconv.ParseFloat(value.(map[string]any)["text"].(string), 64)
			check(t, err)
			create = create.WithScore(number)
		}
		if value := input["price"]; value != nil {
			number, err := decimal.Parse(value.(map[string]any)["text"].(string))
			check(t, err)
			create = create.WithPrice(number)
		}
		if value := input["elapsed"]; value != nil {
			micros, err := strconv.ParseInt(value.(map[string]any)["microseconds"].(string), 10, 64)
			check(t, err)
			create = create.WithElapsed(duration.FromMicroseconds(micros))
		}
		_, err := records.MeasureObjects.Create(t.Context(), backend, create)
		check(t, err)
	}
}

func readComputedReference(t *testing.T, vendor string) computedReference {
	t.Helper()
	data, err := os.ReadFile("django61-" + vendor + ".json")
	check(t, err)
	var fixture computedReference
	check(t, json.Unmarshal(data, &fixture))
	return fixture
}

func TestComputedReference(t *testing.T) {
	withComputedBackends(t, func(t *testing.T, backend computedBackend, vendor string, _ computedPhysicalWriter) {
		fixture := readComputedReference(t, vendor)
		for _, record := range fixture.Cases {
			t.Run(record.Name, func(t *testing.T) {
				var actual any
				var actionErr error
				rollback := errors.New("computed reference owned rollback")
				err := backend.Atomic(t.Context(), func(session db.Session) error {
					seedComputed(t, session, fixture.Input)
					actual, actionErr = runComputedReference(t, session, record.Name)
					return rollback
				})
				if !errors.Is(err, rollback) {
					t.Fatal("reference did not confirm rollback", err)
				}
				remaining, err := records.MeasureObjects.Using(backend).Count(t.Context())
				check(t, err)
				if remaining != 0 {
					t.Fatal("computed reference left stored rows", remaining)
				}
				if record.Error != nil {
					if actionErr == nil {
						t.Fatalf("native reference error became success: %#v", actual)
					}
					if record.Error.SQLState != nil {
						var native *pgconn.PgError
						if !errors.As(actionErr, &native) || native.Code != *record.Error.SQLState {
							t.Fatal("lost native SQLSTATE", actionErr)
						}
					}
					return
				}
				check(t, actionErr)
				got, err := canonicalComputed(actual)
				check(t, err)
				want, err := canonicalComputed(record.Result)
				check(t, err)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("computed reference differs: %#v\nwant %#v", got, want)
				}
			})
		}
	})
}

func priorityDynamic() orm.DynamicExpression {
	f := orm.DynamicF("other")
	return orm.DynamicCase(f, orm.DynamicWhen(f.IsNull(true), int64(0)),
		orm.DynamicWhen(orm.DynamicOr(f.Exact(int64(-1)), f.Exact(int64(0))), f.Add(int64(1))))
}
func nullableDynamic() orm.DynamicExpression {
	return orm.DynamicCase(orm.DynamicF("amount").Add(orm.DynamicF("other")),
		orm.DynamicWhen(orm.DynamicF("enabled").Exact(false), orm.DynamicNull(query.FieldInteger)))
}

type namedComputed struct {
	name  string
	value orm.DynamicExpression
}

func selectComputed(ctx context.Context, source project.RecordsMeasureQuery, values ...namedComputed) (any, error) {
	inputs := make([]orm.DynamicExpression, len(values))
	for index, value := range values {
		inputs[index] = value.value
	}
	projection, err := source.ProjectExpressions(inputs...)
	if err != nil {
		return nil, err
	}
	rows, err := project.SelectRecordsMeasureInto(ctx, source, projection)
	if err != nil {
		return nil, err
	}
	result := make([]any, len(rows))
	for index, row := range rows {
		mapped := map[string]any{}
		for column, value := range row {
			mapped[values[column].name] = value
		}
		result[index] = mapped
	}
	return result, nil
}

func runComputedReference(t *testing.T, backend db.Session, name string) (any, error) {
	ctx := t.Context()
	api, err := project.Using(backend)
	if err != nil {
		return nil, err
	}
	f := records.MeasureFields
	source := api.RecordsMeasure.OrderBy(f.ID.Asc())
	amount, other := orm.DynamicF("amount"), orm.DynamicF("other")
	var expression orm.DynamicExpression
	switch name {
	case "priority_preview_boundaries":
		for _, row := range []struct {
			code  string
			value int64
		}{{"minimum", math.MinInt64}, {"maximum", math.MaxInt64}, {"low", -1}, {"urgent", 1}} {
			if _, err := records.MeasureObjects.Create(ctx, backend, records.NewMeasureCreate(row.code, true, 0).WithOther(row.value)); err != nil {
				return nil, err
			}
		}
		return selectComputed(ctx, source, namedComputed{"code", orm.DynamicF("code")}, namedComputed{"other", other}, namedComputed{"derived", priorityDynamic()})
	case "first_matching_branch":
		expression = orm.DynamicCase(int64(-1), orm.DynamicWhen(amount.GreaterThanOrEqual(int64(0)), int64(11)), orm.DynamicWhen(amount.GreaterThan(int64(0)), int64(22)))
	case "no_branches_default":
		expression = orm.DynamicCase(amount)
	case "nullable_branch":
		expression = orm.DynamicCase(amount, orm.DynamicWhen(orm.DynamicF("enabled").Exact(false), orm.DynamicNull(query.FieldInteger)))
	case "nullable_condition_falls_through":
		expression = orm.DynamicCase(int64(-10), orm.DynamicWhen(other.GreaterThan(int64(0)), amount.Add(int64(3))))
	case "unselected_row_overflow":
		expression = orm.DynamicCase(amount, orm.DynamicWhen(orm.DynamicF("code").Exact("never"), amount.Add(int64(math.MaxInt64))))
	case "unselected_constant_zero_division":
		expression = orm.DynamicCase(amount, orm.DynamicWhen(orm.DynamicF("code").Exact("never"), orm.DynamicValue(int64(1)).Divide(int64(0))))
	case "selected_zero_division":
		expression = orm.DynamicCase(amount, orm.DynamicWhen(orm.DynamicF("enabled").Exact(true), amount.Divide(int64(0))))
	case "not_computed_null":
		expression = nullableDynamic()
		value, err := source.Expression(expression)
		if err != nil {
			return nil, err
		}
		source = source.Filter(orm.Not(value.Exact(int64(7))))
	case "mixed_or_computed_and_field":
		expression = amount.Add(other)
		value, err := source.Expression(expression)
		if err != nil {
			return nil, err
		}
		source = source.Filter(orm.Or(value.GreaterThan(int64(5)), f.Enabled.Exact(false)))
	case "conditional_aggregate":
		value, err := source.Expression(nullableDynamic())
		if err != nil {
			return nil, err
		}
		return project.AggregateRecordsMeasureInto(ctx, source, orm.Aggregate4(value.Sum(), value.Avg(), orm.Count(value), value.Max(), func(sum, avg query.Value, count int64, max query.Value) map[string]any {
			return map[string]any{"total": sum, "average": avg, "present": query.Integer(count), "maximum": max}
		}))
	case "conditional_group_key":
		key, err := source.Expression(orm.DynamicCase(int64(0), orm.DynamicWhen(orm.DynamicF("enabled").Exact(true), int64(1))))
		if err != nil {
			return nil, err
		}
		value, err := source.Expression(amount.Add(int64(1)))
		if err != nil {
			return nil, err
		}
		grouped, err := project.GroupRecordsMeasureBy(source.OrderBy(), orm.Project1(key, func(v query.Value) query.Value { return v }),
			orm.Aggregate2(orm.CountRows[records.Measure](), value.Sum(), func(count int64, amount query.Value) map[string]any {
				return map[string]any{"total": query.Integer(count), "amount": amount}
			}),
			func(key query.Value, value map[string]any) map[string]any { value["bucket"] = key; return value })
		if err != nil {
			return nil, err
		}
		rows, err := grouped.OrderBy(orm.GroupKey(key).Asc()).All(ctx)
		if err != nil {
			return nil, err
		}
		result := make([]any, len(rows))
		for index, row := range rows {
			result[index] = row
		}
		return result, nil
	case "conditional_group_having":
		grouped, err := source.OrderBy().GroupValues([]string{"group_key"}, []orm.DynamicAggregateInput{{Kind: query.ResultSum, Expression: nullableDynamic()}})
		if err != nil {
			return nil, err
		}
		rows, err := grouped.HavingDynamic(orm.GroupLookupInput{Column: 1, Lookup: query.LookupGreaterThan, Value: int64(0)}).OrderByDynamic(orm.GroupOrderInput{Column: 0, Direction: query.Ascending}).All(ctx)
		if err != nil {
			return nil, err
		}
		result := make([]any, len(rows))
		for index, row := range rows {
			result[index] = map[string]any{"group_key": row.Keys[0], "total": row.Aggregates[0]}
		}
		return result, nil
	case "conditional_distinct_order":
		expression = priorityDynamic()
		value, err := source.Expression(expression)
		if err != nil {
			return nil, err
		}
		return selectComputed(ctx, source.OrderBy(value.Asc()).Distinct(), namedComputed{"derived", expression})
	case "conditional_sliced_sum":
		value, err := source.Expression(priorityDynamic())
		if err != nil {
			return nil, err
		}
		source, err = source.Limit(3)
		if err != nil {
			return nil, err
		}
		return project.AggregateRecordsMeasureInto(ctx, source, orm.Aggregate1(value.Sum(), func(v query.Value) map[string]any { return map[string]any{"total": v} }))
	case "conditional_decimal_copy":
		zero, err := decimal.Parse("0.00")
		if err != nil {
			return nil, err
		}
		expression = orm.DynamicCase(orm.DynamicF("price"), orm.DynamicWhen(orm.DynamicF("price").IsNull(true), zero))
	case "conditional_duration_copy":
		expression = orm.DynamicCase(orm.DynamicF("elapsed"), orm.DynamicWhen(orm.DynamicF("elapsed").IsNull(true), duration.FromMicroseconds(7)))
	case "conditional_mixed_types":
		expression = orm.DynamicCase(amount, orm.DynamicWhen(orm.DynamicF("enabled").Exact(true), "a"))
	case "conditional_update_original_rows":
		matched, err := source.UpdateDynamic(ctx, orm.DynamicUpdateInput{Field: "other", Value: priorityDynamic()})
		if err != nil {
			return nil, err
		}
		rows, err := selectComputed(ctx, source, namedComputed{"code", orm.DynamicF("code")}, namedComputed{"amount", amount}, namedComputed{"other", other})
		if err != nil {
			return nil, err
		}
		return map[string]any{"matched": query.Integer(matched), "rows": rows}, nil
	case "float_scalar_nan":
		if _, err := source.Filter(f.Code.Exact("a")).Update(ctx, orm.Assign(f.Score, math.Inf(1))); err != nil {
			return nil, err
		}
		if _, err := source.Filter(f.Code.Exact("b")).Update(ctx, orm.Assign(f.Score, math.Inf(-1))); err != nil {
			return nil, err
		}
		expression = orm.DynamicF("score").Subtract(orm.DynamicF("score"))
	default:
		return nil, fmt.Errorf("unowned computed reference %s", name)
	}
	return selectComputed(ctx, source, namedComputed{"code", orm.DynamicF("code")}, namedComputed{"derived", expression})
}

func canonicalComputed(raw any) (any, error) {
	if value, ok := raw.(query.Value); ok {
		switch value.Kind() {
		case query.ValueNull:
			return nil, nil
		case query.ValueInteger:
			n, _ := value.Integer()
			return map[string]any{"type": "int", "text": strconv.FormatInt(n, 10)}, nil
		case query.ValueFloat:
			n, _ := value.Float()
			return computedFloat(n), nil
		case query.ValueString:
			s, _ := value.String()
			return s, nil
		case query.ValueBoolean:
			b, _ := value.Boolean()
			return b, nil
		case query.ValueDecimal:
			n, _ := value.Decimal()
			return map[string]any{"type": "Decimal", "text": n.String()}, nil
		case query.ValueDuration:
			n, _ := value.Duration()
			micros, err := n.TotalMicroseconds()
			return map[string]any{"type": "timedelta", "microseconds": strconv.FormatInt(micros, 10)}, err
		default:
			return nil, fmt.Errorf("unowned computed scalar kind %s", value.Kind())
		}
	}
	switch value := raw.(type) {
	case nil, string, bool:
		return raw, nil
	case []any:
		out := make([]any, len(value))
		for i, child := range value {
			v, err := canonicalComputed(child)
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil
	case map[string]any:
		if kind, ok := value["type"].(string); ok {
			switch kind {
			case "float":
				n, err := strconv.ParseFloat(value["text"].(string), 64)
				if err != nil {
					return nil, err
				}
				return computedFloat(n), nil
			case "Decimal":
				n, err := decimal.Parse(value["text"].(string))
				if err != nil {
					return nil, err
				}
				return map[string]any{"type": kind, "text": n.String()}, nil
			case "int", "timedelta":
				return value, nil
			}
		}
		out := map[string]any{}
		for key, child := range value {
			v, err := canonicalComputed(child)
			if err != nil {
				return nil, err
			}
			out[key] = v
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unowned computed result %T", raw)
	}
}
func computedFloat(value float64) any {
	text := strconv.FormatUint(math.Float64bits(value), 16)
	if math.IsNaN(value) {
		text = "NaN"
	}
	return map[string]any{"type": "float", "bits": text}
}
