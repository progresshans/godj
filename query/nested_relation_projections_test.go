package query_test

import (
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"reflect"
	"testing"
)

func TestNestedRelationProjectionRoutesOwnPrefixesAndRepeatedModelOccurrences(t *testing.T) {
	root := ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}
	person := ir.ModelIdentity{AppLabel: "people", ModelName: "person"}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	author := query.NewFieldRef("author", "author_id", query.FieldInteger, false)
	manager := query.NewFieldRef("manager", "manager_id", query.FieldInteger, true)
	name := query.NewFieldRef("name", "name", query.FieldString, false)
	makeDirect := func(source ir.ModelIdentity, table string, key query.FieldRef, columns []query.FieldRef) query.RelationProjection {
		p, err := query.NewForwardRelationProjection(source, table, key, person, "people_person", id, columns)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	columns := []query.FieldRef{id, name, manager}
	direct := makeDirect(root, "blog_post", author, columns)
	self := makeDirect(person, "people_person", manager, columns)
	hops := append(direct.Path().Hops(), self.Path().Hops()...)
	child, err := query.NewForwardChainProjection(hops, id, columns)
	if err != nil {
		t.Fatal(err)
	}
	grandchild, err := query.NewForwardChainProjection(append(hops, self.TerminalHop()), id, columns)
	if err != nil {
		t.Fatal(err)
	}
	base := query.NewPlan("blog_post", []query.FieldRef{id, author})
	plan, err := base.WithRelationProjections(grandchild, child, direct, child)
	if err != nil {
		t.Fatal(err)
	}
	ordered := plan.RelationProjections()
	if len(ordered) != 3 {
		t.Fatal("repeated physical FK occurrences collapsed")
	}
	for i, p := range ordered {
		if len(p.Path().Hops()) != i+1 {
			t.Fatal("parent must precede its descendant")
		}
	}
	if child.Equal(grandchild) || !child.TerminalHop().Equal(grandchild.TerminalHop()) {
		t.Fatal("route equality confused a repeated physical edge")
	}
	permuted, err := base.WithRelationProjections(direct, child, grandchild)
	if err != nil || !plan.Equal(permuted) {
		t.Fatal("canonical route ordering lost", err)
	}
	hops[0] = query.RelationHop{}
	columns[0] = name
	returned := child.Path().Hops()
	returned[0] = query.RelationHop{}
	if !plan.Equal(permuted) || !plan.WithoutRelationProjections().Equal(base) || len(base.RelationProjections()) != 0 {
		t.Fatal("route aliases or source mutated")
	}
	if !reflect.DeepEqual(child.TargetColumns(), []query.FieldRef{id, name, manager}) {
		t.Fatal("constructor retained column slice")
	}
	// A child must have every parent and an exact FK field declaration on that
	// parent's projection. Logical names alone cannot authorize physical metadata.
	changed := makeDirect(root, "blog_post", query.NewFieldRef("author", "other_id", query.FieldInteger, false), []query.FieldRef{id, name, manager})
	missingKey := makeDirect(root, "blog_post", author, []query.FieldRef{id, name})
	nullableKey := makeDirect(root, "blog_post", author, []query.FieldRef{id, name, query.NewFieldRef("manager", "manager_id", query.FieldInteger, false)})
	for label, input := range map[string][]query.RelationProjection{
		"missing direct parent": {child}, "missing middle parent": {direct, grandchild}, "different prefix metadata": {changed, child}, "parent FK absent": {missingKey, child}, "parent FK nullable changed": {nullableKey, child},
	} {
		t.Run(label, func(t *testing.T) {
			p, err := base.WithRelationProjections(input...)
			if err == nil || !p.Equal(query.Plan{}) {
				t.Fatalf("partial/accepted graph=%v", err)
			}
		})
	}
}
