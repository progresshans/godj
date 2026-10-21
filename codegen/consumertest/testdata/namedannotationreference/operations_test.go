package consumer

import (
	"errors"
	"math"
	"strings"
	"testing"

	"example.com/godj-project-bundle/project"
	"example.com/godj-project-bundle/records"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

// This adapter only spells the public Go operations for the independent
// observer's ordered calls. It never computes an expected query value in Go.
type annotationSelection struct {
	model project.RecordsMeasureQuery
	named project.RecordsMeasureNamedQuery
	bound bool
	err   error
}

func (q annotationSelection) annotate(values ...orm.NamedExpression) annotationSelection {
	if q.err != nil {
		return q
	}
	if q.bound {
		q.named, q.err = q.named.Annotate(values...)
	} else {
		q.named, q.err = q.model.Annotate(values...)
		q.bound = true
	}
	return q
}
func (q annotationSelection) alias(value orm.NamedExpression) annotationSelection {
	if q.err != nil {
		return q
	}
	if q.bound {
		q.named, q.err = q.named.Alias(value)
	} else {
		q.named, q.err = q.model.Alias(value)
		q.bound = true
	}
	return q
}
func (q annotationSelection) filter(input orm.DynamicPredicate) annotationSelection {
	if q.err != nil {
		return q
	}
	var predicate orm.Predicate[records.Measure]
	if q.bound {
		predicate, q.err = q.named.ExpressionPredicate(input)
		q.named = q.named.Filter(predicate)
	} else {
		predicate, q.err = q.model.ExpressionPredicate(input)
		q.model = q.model.Filter(predicate)
	}
	return q
}
func (q annotationSelection) order(values ...orm.Ordering[records.Measure]) annotationSelection {
	if q.bound {
		q.named = q.named.OrderBy(values...)
	} else {
		q.model = q.model.OrderBy(values...)
	}
	return q
}
func (q annotationSelection) limit(n int) annotationSelection {
	if q.err != nil {
		return q
	}
	if q.bound {
		q.named, q.err = q.named.Limit(n)
	} else {
		q.model, q.err = q.model.Limit(n)
	}
	return q
}
func (q annotationSelection) values(t *testing.T, names ...string) (any, error) {
	if q.err != nil {
		return nil, q.err
	}
	inputs := make([]orm.DynamicExpression, len(names))
	for i, name := range names {
		inputs[i] = orm.DynamicF(name)
	}
	var projection orm.Projection[records.Measure, []query.Value]
	var selected orm.ResultQuery[records.Measure, []query.Value]
	var err error
	if q.bound {
		projection, err = q.named.ProjectExpressions(inputs...)
		if err == nil {
			selected, err = project.SelectRecordsMeasureNamedQuery(q.named, projection)
		}
	} else {
		projection, err = q.model.ProjectExpressions(inputs...)
		if err == nil {
			selected, err = project.SelectRecordsMeasureQuery(q.model, projection)
		}
	}
	if err != nil {
		return nil, err
	}
	return annotationResultValues(t, selected, names...)
}
func annotationResultValues(t *testing.T, selected orm.ResultQuery[records.Measure, []query.Value], names ...string) (any, error) {
	rows, err := selected.All(t.Context())
	if err != nil {
		return nil, err
	}
	result := make([]any, len(rows))
	for i, row := range rows {
		value := map[string]any{}
		for j, name := range names {
			value[name] = row[j]
		}
		result[i] = value
	}
	return result, nil
}
func (q annotationSelection) modelValues(t *testing.T, names ...string) (any, error) {
	if q.err != nil {
		return nil, q.err
	}
	rows, err := q.named.All(t.Context())
	if err != nil {
		return nil, err
	}
	result := make([]any, len(rows))
	for i, row := range rows {
		value := map[string]any{}
		for _, name := range names {
			switch name {
			case "code":
				value[name] = row.Value.Code
			case "amount":
				value[name] = query.Integer(row.Value.Amount)
			default:
				column, err := q.named.Column(name)
				if err != nil {
					return nil, err
				}
				value[name], err = column.Get(row)
				if err != nil {
					return nil, err
				}
			}
		}
		result[i] = value
	}
	return result, nil
}

func runAnnotationReference(t *testing.T, reader db.Session, writer db.Session, name string) (any, error) {
	api, err := project.UsingSession(reader)
	if err != nil {
		return nil, err
	}
	f := records.MeasureFields
	ctx := t.Context()
	base := annotationSelection{model: api.RecordsMeasure.OrderBy(f.ID.Asc())}
	amount, other := orm.DynamicF("amount"), orm.DynamicF("other")
	arithmetic := func() annotationSelection {
		return base.annotate(orm.Named("adjusted", amount.Add(1)), orm.Named("doubled", amount.Multiply(2)))
	}
	derived := func(value orm.DynamicExpression) annotationSelection {
		return base.annotate(orm.Named("derived", value))
	}
	switch name {
	case "scalar_model_and_projection":
		return arithmetic().modelValues(t, "code", "amount", "adjusted", "doubled")
	case "scalar_values_keyword":
		return base.annotate(orm.Named("adjusted", amount.Add(1))).values(t, "code", "adjusted")
	case "same_call_dependency":
		return base.annotate(orm.Named("first", amount.Add(1)), orm.Named("second", orm.DynamicF("first").Multiply(2))).values(t, "code", "first", "second")
	case "same_call_forward_reference":
		return base.annotate(orm.Named("second", orm.DynamicF("first").Multiply(2)), orm.Named("first", amount.Add(1))).values(t, "code", "first", "second")
	case "rebind_prior_alias":
		return base.annotate(orm.Named("adjusted", amount.Add(1))).annotate(orm.Named("adjusted", orm.DynamicF("adjusted").Add(1))).values(t, "code", "adjusted")
	case "dependent_alias_before_rebind":
		return base.annotate(orm.Named("first", amount.Add(1))).annotate(orm.Named("second", orm.DynamicF("first").Multiply(2))).annotate(orm.Named("first", orm.DynamicValue(100))).values(t, "code", "first", "second")
	case "field_shadow":
		return base.annotate(orm.Named("amount", amount.Add(1))).values(t, "code", "amount")
	case "fk_attname_shadow":
		return base.annotate(orm.Named("category_id", amount)).values(t, "code", "category_id")
	case "projection_excluded_field_shadow":
		selected, err := project.SelectRecordsMeasureQuery(base.model, orm.Project1(f.Code, func(s string) string { return s }))
		if err != nil {
			return nil, err
		}
		_, err = orm.AnnotateDynamic(selected, orm.Named("amount", amount.Add(1)))
		return nil, err
	case "unknown_reference":
		return derived(orm.DynamicF("absent")).values(t, "code", "derived")
	case "cycle_reference":
		return base.annotate(orm.Named("first", orm.DynamicF("second")), orm.Named("second", orm.DynamicF("first"))).values(t, "code", "first", "second")
	case "missing_output_null":
		return derived(orm.DynamicValue(nil)).values(t, "code", "derived")
	case "typed_output_null":
		return derived(orm.DynamicNull(query.FieldInteger)).values(t, "code", "derived")
	case "nullable_arithmetic":
		return derived(amount.Add(other)).values(t, "code", "derived")
	case "integer_divide_by_zero":
		return derived(amount.Divide(other)).values(t, "code", "derived")
	case "integer_remainder_by_zero":
		return derived(amount.Remainder(other)).values(t, "code", "derived")
	case "integer_negative_division":
		return base.filter(orm.DynamicF("code").Exact("b")).annotate(orm.Named("derived", amount.Divide(2))).values(t, "code", "derived")
	case "integer_negative_remainder":
		return base.filter(orm.DynamicF("code").Exact("b")).annotate(orm.Named("derived", amount.Remainder(2))).values(t, "code", "derived")
	case "integer_constant_overflow", "integer_overflow_empty_native", "integer_overflow_empty_folded":
		source := base
		if name == "integer_overflow_empty_native" {
			source = source.filter(orm.DynamicF("id").LessThan(0))
		}
		if name == "integer_overflow_empty_folded" {
			source.model = source.model.Filter(f.ID.In())
		}
		return source.annotate(orm.Named("derived", orm.DynamicValue(int64(math.MaxInt64)).Add(1))).values(t, "code", "derived")
	case "mixed_integer_float":
		return derived(amount.Add(orm.DynamicF("score"))).values(t, "code", "derived")
	case "decimal_arithmetic":
		two, err := decimal.Parse("2")
		if err != nil {
			return nil, err
		}
		return derived(orm.DynamicF("price").Multiply(two)).values(t, "code", "derived")
	case "duration_arithmetic":
		return derived(orm.DynamicF("elapsed").Add(duration.FromMicroseconds(1))).values(t, "code", "derived")
	case "field_copy_domains":
		return base.annotate(orm.Named("text", orm.DynamicF("code")), orm.Named("truth", orm.DynamicF("enabled")), orm.Named("exact", orm.DynamicF("price")), orm.Named("span", orm.DynamicF("elapsed")), orm.Named("payload", orm.DynamicF("document"))).values(t, "code", "text", "truth", "exact", "span", "payload")
	case "forward_optional_field":
		return base.annotate(orm.Named("category_name", orm.DynamicF("category__name"))).values(t, "code", "category_name")
	case "scalar_filter":
		return arithmetic().filter(orm.DynamicF("adjusted").GreaterThanOrEqual(4)).values(t, "code", "adjusted")
	case "scalar_not_nullable_filter":
		return derived(other.Add(1)).filter(orm.DynamicNot(orm.DynamicF("derived").GreaterThan(1))).values(t, "code", "derived")
	case "scalar_compare_aliases":
		return arithmetic().filter(orm.DynamicF("adjusted").LessThan(orm.DynamicF("doubled"))).values(t, "code", "adjusted", "doubled")
	case "scalar_order_nulls_first", "scalar_order_nulls_last":
		source := derived(other.Add(1))
		if source.err != nil {
			return nil, source.err
		}
		column, err := source.named.Column("derived")
		if err != nil {
			return nil, err
		}
		order := column.Desc().NullsLast()
		if name == "scalar_order_nulls_first" {
			order = column.Desc().NullsFirst()
		}
		return source.order(order, f.ID.Asc()).values(t, "code", "derived")
	case "annotate_before_slice":
		return arithmetic().limit(2).values(t, "code", "adjusted")
	case "annotate_after_slice":
		return base.limit(2).annotate(orm.Named("adjusted", amount.Add(1))).values(t, "code", "adjusted")
	case "filter_after_slice":
		return arithmetic().limit(2).filter(orm.DynamicF("adjusted").GreaterThan(0)).values(t, "code", "adjusted")
	case "order_after_slice":
		source := arithmetic().limit(2)
		if source.err != nil {
			return nil, source.err
		}
		column, err := source.named.Column("adjusted")
		if err != nil {
			return nil, err
		}
		return source.order(column.Asc()).values(t, "code", "adjusted")
	case "count_scalar_annotation":
		source := arithmetic()
		if source.err != nil {
			return nil, source.err
		}
		return source.named.Count(ctx)
	case "exists_scalar_annotation":
		source := arithmetic().filter(orm.DynamicF("adjusted").GreaterThan(0))
		if source.err != nil {
			return nil, source.err
		}
		return source.named.Exists(ctx)
	case "alias_filter_not_selected":
		return base.alias(orm.Named("hidden", amount.Add(1))).filter(orm.DynamicF("hidden").GreaterThan(4)).values(t, "code")
	case "alias_select_rejected":
		return base.alias(orm.Named("hidden", amount.Add(1))).values(t, "code", "hidden")
	case "alias_promoted_to_annotation":
		return base.alias(orm.Named("hidden", amount.Add(1))).annotate(orm.Named("visible", orm.DynamicF("hidden"))).values(t, "code", "visible")
	case "sum_computed_operand", "avg_computed_operand", "sum_distinct_computed_operand", "sum_filtered_computed_operand":
		kind := query.ResultSum
		if name == "avg_computed_operand" {
			kind = query.ResultAvg
		}
		value, err := base.model.AggregateExpression(kind, amount.Multiply(2))
		if err != nil {
			return nil, err
		}
		if name == "sum_distinct_computed_operand" {
			value = value.Distinct()
		}
		if name == "sum_filtered_computed_operand" {
			value = value.Where(f.Enabled.Exact(true))
		}
		selected, err := project.AggregateRecordsMeasureQuery(base.model, orm.Aggregate1(value, func(v query.Value) any { return map[string]any{"value": v} }))
		if err != nil {
			return nil, err
		}
		return selected.One(ctx)
	case "aggregate_annotation_alias", "aggregate_sliced_annotation_alias", "terminal_average_of_annotation":
		source, column, kind := arithmetic(), "adjusted", query.ResultSum
		if name == "aggregate_sliced_annotation_alias" {
			source = source.limit(2)
		}
		if name == "terminal_average_of_annotation" {
			source = base.annotate(orm.Named("tag_count", orm.DynamicF("tags__id")).Count())
			column, kind = "tag_count", query.ResultAvg
		}
		if source.err != nil {
			return nil, source.err
		}
		projection, err := source.named.ProjectExpressions(orm.DynamicF(column))
		if err != nil {
			return nil, err
		}
		selected, err := project.SelectRecordsMeasureNamedQuery(source.named, projection)
		if err != nil {
			return nil, err
		}
		stage, err := orm.Derive(selected)
		if err != nil {
			return nil, err
		}
		value, err := stage.Expression(orm.DynamicColumn(0))
		if err != nil {
			return nil, err
		}
		aggregate := value.Sum()
		if kind == query.ResultAvg {
			aggregate = value.Avg()
		}
		reduced, err := orm.AggregateResults(stage, orm.Aggregate1(aggregate, func(v query.Value) any { return map[string]any{"value": v} }))
		if err != nil {
			return nil, err
		}
		return reduced.One(ctx)
	case "distinct_projection_with_annotation", "distinct_projection_order_expands_selection":
		source := base.model
		if name == "distinct_projection_with_annotation" {
			source = source.OrderBy()
		}
		selected, err := project.SelectRecordsMeasureQuery(source, orm.Project1(f.GroupKey, func(s *string) *string { return s }))
		if err != nil {
			return nil, err
		}
		named, err := orm.AnnotateDynamic(selected, orm.Named("adjusted", amount.Add(1)))
		if err != nil {
			return nil, err
		}
		named = named.Distinct()
		if name == "distinct_projection_with_annotation" {
			value, err := named.Column("adjusted")
			if err != nil {
				return nil, err
			}
			named = named.OrderBy(f.GroupKey.Asc(), value.Asc())
		}
		projection, err := named.ProjectExpressions(orm.DynamicF("group_key"), orm.DynamicF("adjusted"))
		if err != nil {
			return nil, err
		}
		result, err := orm.SelectNamed(named, projection)
		if err != nil {
			return nil, err
		}
		return annotationResultValues(t, result, "group_key", "adjusted")
	case "group_then_computed_aggregate", "group_then_computed_having", "computed_key_then_group", "group_key_retained_when_hidden":
		return annotationGroupedReference(t, base, name)
	case "row_aggregate_annotation":
		return base.annotate(orm.Named("total", amount).Sum(), orm.Named("mean", amount).Avg()).modelValues(t, "code", "total", "mean")
	case "related_count_annotation":
		return base.annotate(orm.Named("tag_count", orm.DynamicF("tags__id")).Count()).modelValues(t, "code", "tag_count")
	case "related_count_having":
		return base.annotate(orm.Named("tag_count", orm.DynamicF("tags__id")).Count()).filter(orm.DynamicF("tag_count").GreaterThanOrEqual(2)).values(t, "code", "tag_count")
	case "related_count_mixed_or":
		return base.annotate(orm.Named("tag_count", orm.DynamicF("tags__id")).Count()).filter(orm.DynamicOr(orm.DynamicF("tag_count").GreaterThanOrEqual(2), orm.DynamicF("code").Exact("d"))).values(t, "code", "tag_count")
	case "filter_then_related_annotation":
		return base.filter(orm.DynamicF("tags__name").Exact("red")).annotate(orm.Named("tag_count", orm.DynamicF("tags__id")).Count()).values(t, "code", "tag_count")
	case "related_annotation_then_filter":
		return base.annotate(orm.Named("tag_count", orm.DynamicF("tags__id")).Count()).filter(orm.DynamicF("tags__name").Exact("red")).values(t, "code", "tag_count")
	case "related_annotate_then_slice":
		return base.annotate(orm.Named("tag_count", orm.DynamicF("tags__id")).Count()).limit(2).values(t, "code", "tag_count")
	case "related_slice_then_annotate":
		return base.limit(2).annotate(orm.Named("tag_count", orm.DynamicF("tags__id")).Count()).values(t, "code", "tag_count")
	case "nested_aggregate_annotation_rejected":
		return base.annotate(orm.Named("tag_count", orm.DynamicF("tags__id")).Count()).annotate(orm.Named("mean", orm.DynamicF("tag_count")).Avg()).values(t, "code", "mean")
	case "reverse_count_and_sum", "multi_join_count_multiplicity", "multi_join_distinct_count", "reverse_count_filter_before", "reverse_count_filter_after":
		return annotationReverseReference(t, api, name)
	case "aggregate_empty_default":
		source := base.model.Filter(f.ID.In())
		value, err := source.AggregateExpression(query.ResultSum, amount.Add(1))
		if err != nil {
			return nil, err
		}
		selected, err := project.AggregateRecordsMeasureQuery(source, orm.Aggregate1(value, func(v query.Value) query.Value { return v }))
		if err != nil {
			return nil, err
		}
		stage, err := orm.Derive(selected)
		if err != nil {
			return nil, err
		}
		projection, err := stage.Project(orm.DynamicCase(orm.DynamicColumn(0), orm.DynamicWhen(orm.DynamicColumn(0).IsNull(true), 0)))
		if err != nil {
			return nil, err
		}
		result, err := orm.SelectResults(stage, projection)
		if err != nil {
			return nil, err
		}
		rows, err := result.All(ctx)
		if err != nil {
			return nil, err
		}
		if len(rows) != 1 || len(rows[0]) != 1 {
			return nil, errors.New("empty aggregate did not return one result")
		}
		return map[string]any{"value": rows[0][0]}, nil
	case "empty_annotation":
		base.model = base.model.Filter(f.ID.In())
		return base.annotate(orm.Named("derived", amount.Add(1))).values(t, "code", "derived")
	case "annotation_expression_sql_literals":
		return derived(orm.DynamicValue("quote ' ; and %")).values(t, "code", "derived")
	case "cache_owned_by_annotation_query":
		source := arithmetic()
		first, err := source.modelValues(t, "code", "adjusted")
		if err != nil {
			return nil, err
		}
		if _, err := records.MeasureObjects.Using(writer).Filter(f.Code.Exact("a")).Update(ctx, orm.Assign(f.Amount, int64(100))); err != nil {
			return nil, err
		}
		cached, err := source.modelValues(t, "code", "adjusted")
		if err != nil {
			return nil, err
		}
		source.named = source.named.Fresh()
		fresh, err := source.modelValues(t, "code", "adjusted")
		if err != nil {
			return nil, err
		}
		return map[string]any{"first": first, "cached": cached, "fresh": fresh}, nil
	case "immutable_annotation_branches":
		source := arithmetic()
		first := source.filter(orm.DynamicF("adjusted").GreaterThan(0))
		second := source.annotate(orm.Named("adjusted", orm.DynamicF("adjusted").Add(10)))
		original, err := source.values(t, "code", "adjusted")
		if err != nil {
			return nil, err
		}
		filtered, err := first.values(t, "code", "adjusted")
		if err != nil {
			return nil, err
		}
		rebound, err := second.values(t, "code", "adjusted")
		if err != nil {
			return nil, err
		}
		return map[string]any{"original": original, "filtered": filtered, "rebound": rebound}, nil
	}
	if strings.HasPrefix(name, "alias_name_") {
		alias := strings.TrimPrefix(name, "alias_name_")
		return base.annotate(orm.Named(alias, amount.Add(1))).values(t, "code", alias)
	}
	return nil, errors.New("unimplemented independent annotation case: " + name)
}

func annotationGroupedReference(t *testing.T, base annotationSelection, name string) (any, error) {
	f := records.MeasureFields
	source := base.model.OrderBy()
	var selected orm.ResultQuery[records.Measure, []query.Value]
	var err error
	key := "group_key"
	if name == "computed_key_then_group" {
		named, err := source.Annotate(orm.Named("key", orm.DynamicF("amount").Remainder(2)))
		if err != nil {
			return nil, err
		}
		projection, err := named.ProjectExpressions(orm.DynamicF("key"))
		if err != nil {
			return nil, err
		}
		selected, err = project.SelectRecordsMeasureNamedQuery(named, projection)
		key = "key"
	} else {
		projection, e := source.ProjectExpressions(orm.DynamicF("group_key"))
		if e != nil {
			return nil, e
		}
		selected, err = project.SelectRecordsMeasureQuery(source, projection)
	}
	if err != nil {
		return nil, err
	}
	definition := orm.Named("total", orm.DynamicF("amount")).Sum()
	if name == "computed_key_then_group" {
		definition = orm.Named("total", orm.DynamicF("id")).Count()
	}
	named, err := orm.AnnotateDynamic(selected, definition)
	if err != nil {
		return nil, err
	}
	names := []string{key, "total"}
	if name == "group_then_computed_aggregate" || name == "group_then_computed_having" {
		named, err = orm.AnnotateDynamic(named, orm.Named("doubled", orm.DynamicF("total").Multiply(2)))
		if err != nil {
			return nil, err
		}
		names = append(names, "doubled")
	}
	if name == "group_then_computed_having" {
		predicate, err := named.Predicate(orm.DynamicF("doubled").GreaterThanOrEqual(6))
		if err != nil {
			return nil, err
		}
		named = named.Filter(predicate)
	}
	if key == "group_key" {
		named = named.OrderBy(f.GroupKey.Asc())
	} else {
		column, err := named.Column(key)
		if err != nil {
			return nil, err
		}
		named = named.OrderBy(column.Asc())
	}
	if name == "group_key_retained_when_hidden" {
		names = []string{"total"}
	}
	inputs := make([]orm.DynamicExpression, len(names))
	for i, name := range names {
		inputs[i] = orm.DynamicF(name)
	}
	projection, err := named.ProjectExpressions(inputs...)
	if err != nil {
		return nil, err
	}
	result, err := orm.SelectNamed(named, projection)
	if err != nil {
		return nil, err
	}
	return annotationResultValues(t, result, names...)
}

func annotationReverseReference(t *testing.T, api project.Models, name string) (any, error) {
	base := api.RecordsCategory.OrderBy(records.CategoryFields.ID.Asc())
	count := orm.Named("total", orm.DynamicF("measures__id")).Count()
	names := []string{"name", "total"}
	if name == "reverse_count_filter_before" {
		predicate, err := base.ExpressionPredicate(orm.DynamicF("measures__enabled").Exact(true))
		if err != nil {
			return nil, err
		}
		base = base.Filter(predicate)
	}
	values := []orm.NamedExpression{count}
	if name == "reverse_count_and_sum" {
		values = append(values, orm.Named("cost", orm.DynamicF("measures__price")).Sum())
		names = append(names, "cost")
	}
	if name == "multi_join_count_multiplicity" || name == "multi_join_distinct_count" {
		items := orm.Named("items", orm.DynamicF("measures__id")).Count()
		tags := orm.Named("tags", orm.DynamicF("measures__tags__id")).Count()
		if name == "multi_join_distinct_count" {
			items, tags = items.Distinct(), tags.Distinct()
		}
		values = []orm.NamedExpression{items, tags}
		names = []string{"name", "items", "tags"}
	}
	named, err := base.Annotate(values...)
	if err != nil {
		return nil, err
	}
	if name == "reverse_count_filter_after" {
		predicate, err := named.ExpressionPredicate(orm.DynamicF("measures__enabled").Exact(true))
		if err != nil {
			return nil, err
		}
		named = named.Filter(predicate)
	}
	rows, err := named.All(t.Context())
	if err != nil {
		return nil, err
	}
	result := make([]any, len(rows))
	for i, row := range rows {
		value := map[string]any{"name": row.Value.Name}
		for _, name := range names[1:] {
			column, err := named.Column(name)
			if err != nil {
				return nil, err
			}
			value[name], err = column.Get(row)
			if err != nil {
				return nil, err
			}
		}
		result[i] = value
	}
	return result, nil
}
