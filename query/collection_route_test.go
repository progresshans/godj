package query

import (
	"testing"

	"github.com/progresshans/godj/schema/ir"
)

func collectionRouteFixture(t *testing.T, terminal FieldRef) RelationPath {
	t.Helper()
	owner := ir.ModelIdentity{AppLabel: "app", ModelName: "owner"}
	link := ir.ModelIdentity{AppLabel: "app", ModelName: "link"}
	target := ir.ModelIdentity{AppLabel: "app", ModelName: "target"}
	id := NewFieldRef("id", "id", FieldInteger, false)
	reverse, err := NewReverseRelationPath(link, "links", "owner", "owner_id", owner, "owners", "id", "labels", false, id, ir.RelationOneToMany)
	if err != nil {
		t.Fatal(err)
	}
	forward, err := NewForwardRelationPath(link, "links", "target", "target_id", target, "targets", "id", true, terminal, ir.RelationManyToOne)
	if err != nil {
		t.Fatal(err)
	}
	path, err := NewRelationChain(append(reverse.Hops(), forward.Hops()...), []FieldRef{id, id, id}, terminal, RelationTerminalRelatedField)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCollectionRouteKeysAndFilterScopes(t *testing.T) {
	name := NewFieldRef("name", "name", FieldString, true)
	path := collectionRouteFixture(t, name)
	if path.SingleValued() || path.Validate() != nil {
		t.Fatal("collection path is invalid")
	}
	hops, keys := path.Hops(), path.PrimaryKeys()
	hops[0].sourceTable = "changed"
	keys[1] = NewFieldRef("wrong", "wrong", FieldInteger, false)
	if path.Hops()[0].SourceTable() != "links" || path.PrimaryKeys()[1].Column() != "id" {
		t.Fatal("route metadata aliases caller slices")
	}
	keys = path.PrimaryKeys()
	keys[2] = NewFieldRef("wrong", "wrong", FieldInteger, false)
	if _, err := NewRelationChain(path.Hops(), keys, name, RelationTerminalRelatedField); err == nil {
		t.Fatal("destination key disagrees with its ForeignKey target")
	}
	keys = path.PrimaryKeys()
	keys[0] = NewFieldRef("id", "id", FieldInteger, true)
	if _, err := NewRelationChain(path.Hops(), keys, name, RelationTerminalRelatedField); err == nil {
		t.Fatal("nullable row identity accepted")
	}
	if _, err := NewRelationChain(path.Hops()[1:], path.PrimaryKeys(), name, RelationTerminalRelatedField); err == nil {
		t.Fatal("misaligned keys accepted")
	}

	left, err := NewExpression(NewRelatedCondition(path, LookupExact, String("a")))
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewExpression(NewRelatedCondition(path, LookupExact, String("b")))
	if err != nil {
		t.Fatal(err)
	}
	base := NewPlan("owners", []FieldRef{path.PrimaryKeys()[0]})
	first, err := base.WithWhere(left)
	if err != nil {
		t.Fatal(err)
	}
	chained, err := first.WithWhere(right)
	if err != nil {
		t.Fatal(err)
	}
	for index, condition := range chained.Conditions() {
		got, _ := condition.RelationPath()
		for _, hop := range got.Hops() {
			if hop.FilterScope() != uint32(index) {
				t.Fatal("successive filters reused a collection join", index, hop.FilterScope())
			}
		}
	}
	and, err := AndExpressions(left, right)
	if err != nil {
		t.Fatal(err)
	}
	same, err := base.WithWhere(and)
	if err != nil {
		t.Fatal(err)
	}
	if chained.Equal(same) {
		t.Fatal("same and successive filter scopes collapsed")
	}
	for _, condition := range same.Conditions() {
		got, _ := condition.RelationPath()
		if got.Hops()[0].FilterScope() != 0 {
			t.Fatal("one filter split its join scope")
		}
	}
	again, err := first.WithWhere(right)
	if err != nil || !again.Equal(chained) {
		t.Fatal("derived plan changed its parent or predicate", err)
	}
	if path.Hops()[0].FilterScope() != 0 {
		t.Fatal("filter mutated its input path")
	}
	missingRoot := NewPlan("owners", []FieldRef{NewFieldRef("wrong", "wrong", FieldInteger, false)})
	if _, err := missingRoot.WithWhere(left); err == nil {
		t.Fatal("root identity outside selected metadata accepted")
	}
	membership, err := NewRelatedInCondition(path, []Value{String("a"), String("b")})
	if err != nil {
		t.Fatal(err)
	}
	expression, err := NewExpression(membership)
	if err != nil {
		t.Fatal(err)
	}
	not, err := NotExpression(expression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := base.WithWhere(not); err != nil {
		t.Fatal("keyed collection NOT rejected", err)
	}
}
