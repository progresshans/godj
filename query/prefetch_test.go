package query_test

import (
	"reflect"
	"testing"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func prefetchOwnerPlan(t *testing.T) (query.Plan, query.RelationPath) {
	t.Helper()
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	name := query.NewFieldRef("name", "name", query.FieldString, false)
	owner := query.NewFieldRef("owner", "owner_id", query.FieldInteger, false)
	through := ir.ModelIdentity{AppLabel: "app", ModelName: "link"}
	target := ir.ModelIdentity{AppLabel: "app", ModelName: "label"}
	raw, err := query.NewReverseRelationPath(through, "links", "label", "label_id", target, "labels", "id", "links", false, owner, ir.RelationOneToMany)
	if err != nil {
		t.Fatal(err)
	}
	path, err := query.NewRelationChain(raw.Hops(), []query.FieldRef{id, id}, owner, query.RelationTerminalRelatedField)
	if err != nil {
		t.Fatal(err)
	}
	return query.NewPlan("labels", []query.FieldRef{id, name}), path
}

func TestPrefetchOwnerSelectionKeepsGroupingAndMembershipScopes(t *testing.T) {
	base, path := prefetchOwnerPlan(t)
	p, err := base.WithConditions(query.NewRelatedCondition(path, query.LookupExact, query.Integer(1)))
	if err != nil {
		t.Fatal(err)
	}
	p, err = p.WithConditions(query.NewRelatedCondition(path, query.LookupExact, query.Integer(2)))
	if err != nil {
		t.Fatal(err)
	}
	original := p
	owners := []int64{1, 2}
	selected, err := p.ForPrefetchOwners(path, owners)
	if err != nil {
		t.Fatal(err)
	}
	owners[0] = 999
	if !p.Equal(original) || selected.ResultShape().Kind() != query.ResultPrefetch || !reflect.DeepEqual(base.SourceFields(), selected.SourceFields()) {
		t.Fatal("prefetch changed the source query or its field universe")
	}
	projection, _ := selected.ResultShape().Expressions()[0].RelationPath()
	if projection.Hops()[0].FilterScope() != 0 {
		t.Fatal("grouping did not retain the first intermediary occurrence")
	}
	var scopes []uint32
	for _, condition := range selected.Conditions() {
		path, _ := condition.RelationPath()
		scopes = append(scopes, path.Hops()[0].FilterScope())
		if values, list := condition.Values(); list {
			if !values[0].Equal(query.Integer(1)) || !values[1].Equal(query.Integer(2)) {
				t.Fatal("owner membership aliases caller input")
			}
		}
	}
	if !reflect.DeepEqual(scopes, []uint32{0, 1, 1, 0}) {
		t.Fatal("custom query scopes or membership provenance were lost", scopes)
	}
	if _, err := base.WithResultShape(selected.ResultShape()); err == nil {
		t.Fatal("owner result accepted without its required membership predicate")
	}
}

func TestPrefetchOwnerSelectionDoesNotReuseNegatedSubquery(t *testing.T) {
	base, path := prefetchOwnerPlan(t)
	expression, err := query.NewExpression(query.NewRelatedCondition(path, query.LookupExact, query.Integer(2)))
	if err != nil {
		t.Fatal(err)
	}
	expression, err = query.NotExpression(expression)
	if err != nil {
		t.Fatal(err)
	}
	base, err = base.WithWhere(expression)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := base.ForPrefetchOwners(path, []int64{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	projection, _ := selected.ResultShape().Expressions()[0].RelationPath()
	if projection.Hops()[0].FilterScope() != 1 {
		t.Fatal("prefetch reused an occurrence owned by NOT EXISTS")
	}
}

func TestPrefetchOwnerSliceUsesLastMembershipAndLateGrouping(t *testing.T) {
	base, path := prefetchOwnerPlan(t)
	base, err := base.WithConditions(query.NewRelatedCondition(path, query.LookupExact, query.Integer(1)))
	if err != nil {
		t.Fatal(err)
	}
	base, err = base.WithConditions(query.NewRelatedCondition(path, query.LookupExact, query.Integer(2)))
	if err != nil {
		t.Fatal(err)
	}
	limited, err := base.WithLimit(2)
	if err != nil {
		t.Fatal(err)
	}
	limited, err = limited.WithOffset(1)
	if err != nil {
		t.Fatal(err)
	}
	keys := []int64{1, 2}
	selected, err := limited.ForPrefetchOwners(path, keys)
	if err != nil {
		t.Fatal(err)
	}
	keys[0] = 900
	window, ok := selected.PrefetchWindow()
	if !ok {
		t.Fatal("global slice was not partitioned")
	}
	partition, _ := window.Partition().RelationPath()
	grouping, keysCopy, late := window.OwnerFilter()
	groupPath, _ := grouping.RelationPath()
	if !late || partition.Hops()[0].FilterScope() != 1 || groupPath.Hops()[0].FilterScope() != 0 || !keysCopy[0].Equal(query.Integer(1)) {
		t.Fatal("slice lost membership, grouping, or key ownership")
	}
	keysCopy[0] = query.Integer(900)
	_, preserved, _ := window.OwnerFilter()
	if !preserved[0].Equal(query.Integer(1)) {
		t.Fatal("window exposes mutable keys")
	}
	if len(selected.Conditions()) != 3 {
		t.Fatal("grouping boundary was pushed before the window")
	}
	if !selected.Equal(selected) {
		t.Fatal("immutable window identity changed")
	}
	if _, exists := limited.PrefetchWindow(); exists {
		t.Fatal("source plan mutated")
	}
	projection, err := query.NewProjectionResult(query.FieldResult(base.SourceFields()[0]))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := selected.WithResultShape(projection); err == nil {
		t.Fatal("window lost model owner projection")
	}
	if _, err := base.WithResultShape(selected.ResultShape()); err == nil {
		t.Fatal("unanchored window shape transplanted")
	}
	if _, err := selected.ForPrefetchOwners(path, []int64{2}); err == nil {
		t.Fatal("prefetch window rebound")
	}
	foreign := query.NewPlan("other_labels", base.SourceFields())
	if _, err = foreign.ForPrefetchOwners(path, []int64{1}); err == nil {
		t.Fatal("foreign owner projection accepted")
	}
	// Full grouping membership remains valid without any slice.
	plain, err := base.ForPrefetchOwners(path, []int64{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := plain.PrefetchWindow(); ok {
		t.Fatal("unsliced plan gained a window")
	}
	if _, err := plain.WithLimit(1); err == nil {
		t.Fatal("late pagination silently became a global limit")
	}
}

func TestPrefetchForeignKeySliceValidatesSourceAndProtectsRows(t *testing.T) {
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	owner := query.NewFieldRef("owner", "owner_id", query.FieldInteger, true)
	base := query.NewPlan("links", []query.FieldRef{id, owner})
	base, err := base.WithLimit(0)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := base.ForPrefetchForeignKey(owner, []int64{0, 2})
	if err != nil {
		t.Fatal(err)
	}
	window, ok := plan.PrefetchWindow()
	if !ok || !window.Partition().Equal(query.FieldResult(owner)) || !plan.EmptyResult() {
		t.Fatal("reverse owner slice changed shape")
	}
	if _, _, late := window.OwnerFilter(); late {
		t.Fatal("root foreign-key partition gained a late filter")
	}
	for _, field := range []query.FieldRef{query.NewFieldRef("owner", "owner_id", query.FieldInteger, false), query.NewFieldRef("foreign", "foreign", query.FieldInteger, true), query.NewFieldRef("owner", "owner_id", query.FieldString, true)} {
		if _, err := base.ForPrefetchForeignKey(field, nil); err == nil {
			t.Fatal("foreign or mistyped partition accepted")
		}
	}
	aggregate, err := query.NewAggregateResult(query.CountAllResult())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.WithResultShape(aggregate); err == nil {
		t.Fatal("partitioned model replaced by a scalar aggregate")
	}
}
