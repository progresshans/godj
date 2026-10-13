package consumer

import (
	"encoding/json"
	"os"
	"testing"

	"example.com/godj-project-bundle/project"
	"example.com/godj-project-bundle/records"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

type relatedInput struct {
	Groups []struct {
		Key   string
		Limit int64
	}
	Data []struct {
		Key     string
		Amount  *int64
		Score   *float64
		Price   *string
		Elapsed *int64
		Flag    bool
		Group   *string
	}
	Measures []struct {
		Code    string
		Amount  int64
		Other   *int64
		Enabled bool
		Datum   *string
	}
	Details []struct {
		Measure string
		Bonus   int64
		Enabled bool
	}
}

type relatedSeed struct {
	groups   map[string]records.Group
	data     map[string]records.Datum
	measures map[string]records.Measure
	details  map[string]records.Detail
}

func seedRelated(t *testing.T, backend db.Mutator) relatedSeed {
	t.Helper()
	data, err := os.ReadFile("input.json")
	check(t, err)
	var input relatedInput
	check(t, json.Unmarshal(data, &input))
	result := relatedSeed{groups: map[string]records.Group{}, data: map[string]records.Datum{}, measures: map[string]records.Measure{}, details: map[string]records.Detail{}}
	for _, row := range input.Groups {
		value, err := records.GroupObjects.Create(t.Context(), backend, records.NewGroupCreate(row.Limit))
		check(t, err)
		result.groups[row.Key] = value
	}
	for _, row := range input.Data {
		input := records.NewDatumCreate(row.Key, row.Flag)
		if row.Amount != nil {
			input = input.WithAmount(*row.Amount)
		}
		if row.Score != nil {
			input = input.WithScore(*row.Score)
		}
		if row.Price != nil {
			value, err := decimal.Parse(*row.Price)
			check(t, err)
			input = input.WithPrice(value)
		}
		if row.Elapsed != nil {
			input = input.WithElapsed(duration.FromMicroseconds(*row.Elapsed))
		}
		if row.Group != nil {
			input = input.WithGroupID(result.groups[*row.Group].ID)
		}
		value, err := records.DatumObjects.Create(t.Context(), backend, input)
		check(t, err)
		result.data[row.Key] = value
	}
	for _, row := range input.Measures {
		input := records.NewMeasureCreate(row.Code, row.Amount, row.Enabled)
		if row.Other != nil {
			input = input.WithOther(*row.Other)
		}
		if row.Datum != nil {
			input = input.WithDatumID(result.data[*row.Datum].ID)
		}
		value, err := records.MeasureObjects.Create(t.Context(), backend, input)
		check(t, err)
		result.measures[row.Code] = value
	}
	for _, row := range input.Details {
		value, err := records.DetailObjects.Create(t.Context(), backend, records.NewDetailCreate(result.measures[row.Measure].ID, row.Bonus, row.Enabled))
		check(t, err)
		result.details[row.Measure] = value
	}
	return result
}

func relatedValue(t *testing.T, value query.Value) any {
	t.Helper()
	switch value.Kind() {
	case query.ValueNull:
		return nil
	case query.ValueInteger:
		n, _ := value.Integer()
		return n
	case query.ValueFloat:
		n, _ := value.Float()
		return n
	case query.ValueString:
		n, _ := value.String()
		return n
	case query.ValueBoolean:
		n, _ := value.Boolean()
		return n
	case query.ValueDecimal:
		n, _ := value.Decimal()
		return n.String()
	case query.ValueDuration:
		n, _ := value.Duration()
		micros, err := n.TotalMicroseconds()
		check(t, err)
		return micros
	default:
		t.Fatalf("unowned related reference value: %s", value.Kind())
		return nil
	}
}

func observeRelated(t *testing.T, reader db.Session, name string) any {
	t.Helper()
	api, err := project.UsingSession(reader)
	check(t, err)
	base := api.RecordsMeasure.OrderBy(records.MeasureFields.ID.Asc())
	amount, target := orm.DynamicF("amount"), orm.DynamicF("datum__amount")
	added := amount.Add(target)
	equal := amount.Add(int64(1)).Exact(target.Multiply(int64(2)))
	projectRows := func(source project.RecordsMeasureQuery, names []string, inputs ...orm.DynamicExpression) []map[string]any {
		selection, err := source.ProjectExpressions(inputs...)
		check(t, err)
		values, err := project.SelectRecordsMeasureInto(t.Context(), source, selection)
		check(t, err)
		rows := make([]map[string]any, 0, len(values))
		for _, values := range values {
			row := map[string]any{}
			for index, value := range values {
				row[names[index]] = relatedValue(t, value)
			}
			rows = append(rows, row)
		}
		return rows
	}
	rows := func(value orm.DynamicExpression) any {
		return projectRows(base, []string{"code", "value"}, orm.DynamicF("code"), value)
	}
	codes := func(input orm.DynamicPredicate) any {
		predicate, err := base.ExpressionPredicate(input)
		check(t, err)
		values, err := project.SelectRecordsMeasureInto(t.Context(), base.Filter(predicate), orm.Project1(records.MeasureFields.Code, func(value string) string { return value }))
		check(t, err)
		return values
	}
	switch name {
	case "forward_add":
		return rows(added)
	case "forward_chain_add":
		return rows(amount.Add(orm.DynamicF("datum__group__limit")))
	case "rhs_exact":
		return codes(equal)
	case "rhs_ordered":
		return codes(amount.Add(int64(1)).LessThan(target.Multiply(int64(2))))
	case "rhs_not":
		return codes(orm.DynamicNot(equal))
	case "related_both_sides":
		return codes(target.LessThan(orm.DynamicF("detail__bonus")))
	case "predicate_or_missing":
		predicate, err := base.ExpressionPredicate(equal)
		check(t, err)
		relations, err := project.BindRelations()
		check(t, err)
		values, err := project.SelectRecordsMeasureInto(t.Context(), base.Filter(orm.Or(predicate, relations.RecordsMeasure.Datum.IsNull(true))), orm.Project1(records.MeasureFields.Code, func(value string) string { return value }))
		check(t, err)
		return values
	case "case_rhs_related":
		return rows(orm.DynamicCase("other", orm.DynamicWhen(equal, "same")))
	case "case_related_predicate":
		// A direct relation lookup inside CASE exercises the field-lookup path,
		// while the chosen result still uses the common related scalar operand.
		relations, err := project.BindRelations()
		check(t, err)
		value := orm.Case(orm.F(records.MeasureFields.Amount), orm.When(relations.RecordsMeasure.Datum.Flag.Exact(true), relations.RecordsMeasure.Datum.Amount))
		values, err := project.SelectRecordsMeasureInto(t.Context(), base, orm.Project2(records.MeasureFields.Code, value, func(code string, value orm.Optional[int64]) map[string]any {
			var raw any
			if number, present := value.Get(); present {
				raw = number
			}
			return map[string]any{"code": code, "value": raw}
		}))
		check(t, err)
		return values
	case "reverse_add":
		return rows(amount.Add(orm.DynamicF("detail__bonus")))
	case "mixed_route":
		return rows(orm.DynamicF("detail__measure__datum__amount"))
	case "related_group":
		key, err := base.Expression(target)
		check(t, err)
		value, err := base.Expression(added)
		check(t, err)
		grouped, err := project.GroupRecordsMeasureBy(base.OrderBy(), orm.Project1(key, func(key query.Value) query.Value { return key }),
			orm.Aggregate3(value.Sum(), value.Avg(), orm.Count(value), func(sum, avg query.Value, count int64) map[string]any {
				return map[string]any{"sum": relatedValue(t, sum), "avg": relatedValue(t, avg), "count": count}
			}), func(key query.Value, row map[string]any) map[string]any {
				row["key"] = relatedValue(t, key)
				return row
			})
		check(t, err)
		values, err := grouped.OrderBy(orm.GroupKey(key).Asc().NullsFirst()).All(t.Context())
		check(t, err)
		return values
	case "filtered_count":
		predicate, err := base.ExpressionPredicate(equal)
		check(t, err)
		field := records.MeasureFields.Enabled
		grouped, err := project.GroupRecordsMeasureBy(base.OrderBy(), orm.Project1(field, func(value bool) bool { return value }),
			orm.Aggregate1(orm.Count(records.MeasureFields.ID).Where(predicate), func(value int64) int64 { return value }),
			func(enabled bool, count int64) map[string]any {
				return map[string]any{"enabled": enabled, "count": count}
			})
		check(t, err)
		values, err := grouped.OrderBy(orm.GroupKey(field).Asc()).All(t.Context())
		check(t, err)
		return values
	case "order_slice":
		value, err := base.Expression(added)
		check(t, err)
		source, err := base.OrderBy(value.Desc().NullsLast(), records.MeasureFields.ID.Asc()).Offset(1)
		check(t, err)
		source, err = source.Limit(3)
		check(t, err)
		return projectRows(source, []string{"code", "value"}, orm.DynamicF("code"), added)
	case "distinct_computed":
		value, err := base.Expression(added)
		check(t, err)
		values, err := project.SelectRecordsMeasureInto(t.Context(), base.OrderBy(value.Asc().NullsFirst()).Distinct(), orm.Project1(value, func(value query.Value) any { return relatedValue(t, value) }))
		check(t, err)
		return values
	case "aggregate_computed":
		value, err := base.Expression(added)
		check(t, err)
		row, err := orm.AggregateInto(t.Context(), records.MeasureObjects.Using(reader), orm.Aggregate3(value.Sum(), value.Avg(), orm.Count(value), func(sum, avg query.Value, count int64) map[string]any {
			return map[string]any{"sum": relatedValue(t, sum), "avg": relatedValue(t, avg), "count": count}
		}))
		check(t, err)
		return row
	case "related_codecs":
		return projectRows(base, []string{"code", "price", "elapsed", "score", "flag"}, orm.DynamicF("code"), orm.DynamicF("datum__price"), orm.DynamicF("datum__elapsed"), orm.DynamicF("datum__score"), orm.DynamicF("datum__flag"))
	default:
		t.Fatal("unknown related observation", name)
		return nil
	}
}
