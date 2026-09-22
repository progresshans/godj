package query_test

import (
	"errors"
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

func TestPrefetchOwnerSelectionRejectsGlobalSliceAndForeignRoot(t *testing.T) {
	base, path := prefetchOwnerPlan(t)
	limited, err := base.WithLimit(1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = limited.ForPrefetchOwners(path, []int64{1}); !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) {
		t.Fatal("global LIMIT was mistaken for a per-owner limit", err)
	}
	foreign := query.NewPlan("other_labels", base.SourceFields())
	if _, err = foreign.ForPrefetchOwners(path, []int64{1}); err == nil {
		t.Fatal("foreign owner projection accepted")
	}
}
