package query_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestGroupedResultOwnsSelectionPredicatesAndOrdering(t *testing.T) {
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	key := query.NewFieldRef("rank", "rank", query.FieldInteger, true)
	active := query.NewFieldRef("active", "active", query.FieldBoolean, false)
	filter, err := query.NewExpression(query.NewCondition(active, query.LookupExact, query.Boolean(true)))
	if err != nil {
		t.Fatal(err)
	}
	count, err := query.CountResult(query.FieldResult(id))
	if err != nil {
		t.Fatal(err)
	}
	conditional, err := count.WithFilter(filter)
	if err != nil {
		t.Fatal(err)
	}
	distinct, err := count.WithDistinct()
	if err != nil {
		t.Fatal(err)
	}
	keys := []query.ResultExpression{query.FieldResult(key)}
	values := []query.ResultExpression{query.CountAllResult(), conditional, distinct}
	shape, err := query.NewGroupedResult(keys, values)
	if err != nil {
		t.Fatal(err)
	}
	keys[0], values[0] = query.FieldResult(id), count
	shape.GroupKeys()[0] = query.FieldResult(id)
	shape.GroupAggregates()[0] = count
	if !shape.GroupKeys()[0].Equal(query.FieldResult(key)) || !shape.GroupAggregates()[0].Equal(query.CountAllResult()) {
		t.Fatal("selection aliases caller storage")
	}
	if count.Distinct() || count.Equal(distinct) || count.Equal(conditional) {
		t.Fatal("aggregate refinements changed their parent or lost identity")
	}
	if _, filtered := count.Filter(); filtered {
		t.Fatal("filter mutated its source expression")
	}
	base := query.NewPlan("items", []query.FieldRef{id, key, active})
	plan, err := base.WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	having, err := query.NewGroupExpression(conditional, query.LookupGreaterThanOrEqual, query.Integer(1))
	if err != nil {
		t.Fatal(err)
	}
	nullKey, err := query.NewGroupExpression(query.FieldResult(key), query.LookupIsNull, query.Boolean(true))
	if err != nil {
		t.Fatal(err)
	}
	combined, err := query.OrGroupExpressions(having, nullKey)
	if err != nil {
		t.Fatal(err)
	}
	combined.Children()[0] = nullKey
	if !combined.Children()[0].Equal(having) {
		t.Fatal("HAVING children alias")
	}
	derived, err := plan.WithGroupHaving(combined)
	if err != nil {
		t.Fatal(err)
	}
	order, err := query.NewGroupOrdering(conditional, query.Descending, query.NullsLast)
	if err != nil {
		t.Fatal(err)
	}
	orders := []query.GroupOrdering{order}
	derived, err = derived.WithGroupOrderings(orders...)
	if err != nil {
		t.Fatal(err)
	}
	orders[0] = query.GroupOrdering{}
	derived.ResultShape().GroupOrderings()[0] = query.GroupOrdering{}
	if !derived.ResultShape().GroupOrderings()[0].Equal(order) {
		t.Fatal("ordering aliases")
	}
	if _, ok := plan.ResultShape().GroupHaving(); ok || len(plan.ResultShape().GroupOrderings()) != 0 || base.ResultShape().Kind() != query.ResultModel {
		t.Fatal("refinement mutated a parent")
	}
	page, err := derived.WithGroupMode(query.GroupPage)
	if err != nil {
		t.Fatal(err)
	}
	if derived.Equal(page) || derived.ResultShape().GroupMode() != query.GroupRows || page.ResultShape().GroupMode() != query.GroupPage {
		t.Fatal("mode is not independent plan state")
	}
}

func TestGroupedResultRejectsUnselectedAndForeignMetadata(t *testing.T) {
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	key := query.NewFieldRef("rank", "rank", query.FieldInteger, true)
	count := query.CountAllResult()
	shape, err := query.NewGroupedResult([]query.ResultExpression{query.FieldResult(key)}, []query.ResultExpression{count})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := query.NewPlan("items", []query.FieldRef{id, key}).WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []query.ResultExpression{query.FieldResult(id), query.MaxResult(id), query.FieldResult(query.NewFieldRef("rank", "rank", query.FieldInteger, false))} {
		having, err := query.NewGroupExpression(value, query.LookupGreaterThan, query.Integer(0))
		if err != nil {
			t.Fatal(err)
		}
		failed, err := plan.WithGroupHaving(having)
		if err == nil || !failed.Equal(query.Plan{}) {
			t.Fatal("unselected HAVING produced a plan", err)
		}
		order, err := query.NewGroupOrdering(value, query.Ascending, query.NullsFirst)
		if err != nil {
			t.Fatal(err)
		}
		failed, err = plan.WithGroupOrderings(order)
		if err == nil || !failed.Equal(query.Plan{}) {
			t.Fatal("unselected ordering produced a plan", err)
		}
	}
	for _, foreign := range []query.FieldRef{
		query.NewFieldRef("rank", "different", query.FieldInteger, true),
		query.NewFieldRef("rank", "rank", query.FieldInteger, false),
	} {
		filter, err := query.NewExpression(query.NewCondition(foreign, query.LookupExact, query.Integer(1)))
		if err != nil {
			t.Fatal(err)
		}
		filtered, err := query.MinResult(id).WithFilter(filter)
		if err != nil {
			t.Fatal(err)
		}
		invalid, err := query.NewGroupedResult(shape.GroupKeys(), []query.ResultExpression{filtered})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := query.NewPlan("items", []query.FieldRef{id, key}).WithResultShape(invalid); err == nil {
			t.Fatal("filter bypassed source metadata")
		}
	}
	root := ir.ModelIdentity{AppLabel: "app", ModelName: "item"}
	target := ir.ModelIdentity{AppLabel: "app", ModelName: "group"}
	fk := query.NewFieldRef("group", "group_id", query.FieldInteger, true)
	path, err := query.NewForwardRelationPath(root, "items", "group", "group_id", target, "groups", "id", true, key, ir.RelationManyToOne)
	if err != nil {
		t.Fatal(err)
	}
	related, err := query.RelatedFieldResult(path)
	if err != nil {
		t.Fatal(err)
	}
	relatedCount, err := query.CountResult(related)
	if err != nil {
		t.Fatal(err)
	}
	relatedShape, err := query.NewGroupedResult([]query.ResultExpression{related}, []query.ResultExpression{relatedCount})
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []query.Plan{query.NewPlan("other_items", []query.FieldRef{id, fk}), query.NewPlan("items", []query.FieldRef{id}), query.NewPlan("items", []query.FieldRef{id, query.NewFieldRef("group", "group_id", query.FieldInteger, false)})} {
		if _, err := source.WithResultShape(relatedShape); err == nil {
			t.Fatal("related grouping bypassed root authority")
		}
	}
	if _, err := query.NewPlan("items", []query.FieldRef{id, fk}).WithResultShape(relatedShape); err != nil {
		t.Fatal(err)
	}
}

func TestGroupedResultBoundsAndUnsupportedShapes(t *testing.T) {
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	key := query.FieldResult(id)
	count := query.CountAllResult()
	for _, test := range []struct {
		name         string
		keys, values []query.ResultExpression
	}{
		{"no keys", nil, []query.ResultExpression{count}}, {"no aggregate", []query.ResultExpression{key}, nil},
		{"duplicate key", []query.ResultExpression{key, key}, []query.ResultExpression{count}},
		{"duplicate aggregate", []query.ResultExpression{key}, []query.ResultExpression{count, count}},
		{"zero key", []query.ResultExpression{{}}, []query.ResultExpression{count}},
		{"scalar aggregate", []query.ResultExpression{key}, []query.ResultExpression{key}},
		{"json key", []query.ResultExpression{query.FieldResult(query.NewFieldRef("data", "data", query.FieldJSON, true))}, []query.ResultExpression{count}},
	} {
		t.Run(test.name, func(t *testing.T) {
			shape, err := query.NewGroupedResult(test.keys, test.values)
			if err == nil || !shape.Equal(query.ResultShape{}) {
				t.Fatal("invalid shape escaped", err)
			}
		})
	}
	keys, values := make([]query.ResultExpression, query.MaxGroupKeys), make([]query.ResultExpression, query.MaxAggregateExpressions)
	for i := range keys {
		keys[i] = query.FieldResult(query.NewFieldRef(fmt.Sprint("key", i), fmt.Sprint("k", i), query.FieldInteger, false))
	}
	for i := range values {
		values[i] = query.MinResult(query.NewFieldRef(fmt.Sprint("value", i), fmt.Sprint("v", i), query.FieldInteger, false))
	}
	if _, err := query.NewGroupedResult(keys, values); err != nil {
		t.Fatal("valid boundary", err)
	}
	if _, err := query.NewGroupedResult(append(keys, key), values); err == nil {
		t.Fatal("key bound bypassed")
	}
	if _, err := query.NewGroupedResult(keys, append(values, count)); err == nil {
		t.Fatal("aggregate bound bypassed")
	}
	for _, value := range []query.ResultExpression{count, query.MinResult(id), query.MaxResult(id), key} {
		if _, err := value.WithDistinct(); err == nil {
			t.Fatal("unsupported DISTINCT")
		}
	}
	filter, _ := query.NewExpression(query.NewCondition(id, query.LookupExact, query.Integer(1)))
	if _, err := count.WithFilter(filter); !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) {
		t.Fatal("COUNT(*) FILTER", err)
	}
	if _, err := query.MinResult(id).WithFilter(query.Expression{}); err == nil {
		t.Fatal("zero filter")
	}
	leaf, err := query.NewGroupExpression(count, query.LookupGreaterThan, query.Integer(1))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		lookup query.Lookup
		value  query.Value
	}{{query.LookupGreaterThan, query.String("1")}, {query.LookupExact, query.Null()}, {query.LookupIsNull, query.Integer(1)}, {query.LookupIContains, query.String("1")}} {
		if _, err := query.NewGroupExpression(count, test.lookup, test.value); err == nil {
			t.Fatal("invalid HAVING accepted", test)
		}
	}
	deep := leaf
	for i := 1; i < 64; i++ {
		deep, err = query.NotGroupExpression(deep)
		if err != nil {
			t.Fatal("valid depth", i, err)
		}
	}
	if _, err := query.NotGroupExpression(deep); err == nil {
		t.Fatal("depth bound bypassed")
	}
	wide := make([]query.GroupExpression, 1022)
	for i := range wide {
		wide[i] = leaf
	}
	if _, err := query.AndGroupExpressions(leaf, leaf, wide[:1021]...); err != nil {
		t.Fatal("valid node bound", err)
	}
	if _, err := query.AndGroupExpressions(leaf, leaf, wide...); err == nil {
		t.Fatal("node bound bypassed")
	}
	if _, err := query.NotGroupExpression(query.GroupExpression{}); err == nil {
		t.Fatal("zero predicate")
	}
}

func TestGroupedSourceAndPageZeroLimitHaveExplicitMeaning(t *testing.T) {
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	rank := query.NewFieldRef("rank", "rank", query.FieldInteger, true)
	base := query.NewPlan("items", []query.FieldRef{id, rank})
	shape, err := query.NewGroupedResult([]query.ResultExpression{query.FieldResult(rank)}, []query.ResultExpression{query.CountAllResult()})
	if err != nil {
		t.Fatal(err)
	}
	limited, _ := base.WithLimit(1)
	offset, _ := base.WithOffset(0)
	for _, source := range []query.Plan{limited, offset, base.WithOrderings(query.NewOrdering(id, query.Ascending))} {
		if _, err := source.WithResultShape(shape); !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) {
			t.Fatal("unsupported source state", err)
		}
	}
	plan, err := base.WithOrderings(query.NewOrdering(rank, query.Descending)).WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Orderings()) != 0 || len(plan.ResultShape().GroupOrderings()) != 1 {
		t.Fatal("group key ordering was not converted")
	}
	plan, err = plan.WithLimit(0)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.EmptyResult() {
		t.Fatal("zero row limit is not empty")
	}
	page, err := plan.WithGroupMode(query.GroupPage)
	if err != nil {
		t.Fatal(err)
	}
	if page.EmptyResult() {
		t.Fatal("zero page size erased total group count")
	}
	count, err := plan.WithGroupMode(query.GroupCount)
	if err != nil {
		t.Fatal(err)
	}
	if !count.EmptyResult() {
		t.Fatal("count of zero group slice is not empty")
	}
	projection, _ := query.NewProjectionResult(query.FieldResult(id))
	if _, err := plan.WithResultShape(projection); err == nil {
		t.Fatal("group shape replaced by row projection")
	}
	if _, err := page.WithGroupMode(0); err == nil {
		t.Fatal("invalid group mode")
	}
	if _, err := base.WithGroupHaving(query.GroupExpression{}); err == nil {
		t.Fatal("HAVING accepted without groups")
	}
}
