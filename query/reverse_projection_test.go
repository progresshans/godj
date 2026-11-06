package query_test

import (
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"testing"
)

func TestOneToOneProjectionRoutesUseTraversalNamesAndPhysicalKeys(t *testing.T) {
	owner := ir.ModelIdentity{AppLabel: "app", ModelName: "owner"}
	child := ir.ModelIdentity{AppLabel: "app", ModelName: "child"}
	other := ir.ModelIdentity{AppLabel: "app", ModelName: "other"}
	ownerPK := query.NewFieldRef("id", "owner_key", query.FieldInteger, false)
	childPK := query.NewFieldRef("id", "child_key", query.FieldInteger, false)
	fk := query.NewFieldRef("owner", "owner_id", query.FieldInteger, false)
	reverse := func(identity ir.ModelIdentity, table, name string, cardinality ir.RelationCardinality) query.RelationPath {
		t.Helper()
		path, err := query.NewReverseRelationPath(identity, table, "owner", "owner_id", owner, "owners", "owner_key", name, false, childPK, cardinality)
		if err != nil {
			t.Fatal(err)
		}
		return path
	}
	project := func(path query.RelationPath, key query.FieldRef, columns ...query.FieldRef) query.RelationProjection {
		t.Helper()
		p, err := query.NewRelationProjection(path.Hops(), key, columns)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	one := project(reverse(child, "children", "child", ir.RelationOneToOne), childPK, childPK, fk)
	two := project(reverse(other, "others", "other", ir.RelationOneToOne), childPK, childPK, fk)
	if !one.TerminalHop().Optional() || one.TerminalHop().Nullable() {
		t.Fatal("required physical FK lost optional reverse traversal")
	}
	base := query.NewPlan("owners", []query.FieldRef{ownerPK})
	plan, err := base.WithRelationProjections(two, one, one)
	if err != nil {
		t.Fatal(err)
	}
	got := plan.RelationProjections()
	if len(got) != 2 || !got[0].Equal(one) || !got[1].Equal(two) {
		t.Fatal("different reverse accessors sharing a physical FK name coalesced or misordered")
	}
	forward, err := query.NewForwardRelationPath(child, "children", "owner", "owner_id", owner, "owners", "owner_key", false, ownerPK, ir.RelationOneToOne)
	if err != nil {
		t.Fatal(err)
	}
	hops := append(one.Path().Hops(), forward.Hops()...)
	nested, err := query.NewRelationProjection(hops, ownerPK, []query.FieldRef{ownerPK})
	if err != nil {
		t.Fatal(err)
	}
	hops[0] = two.TerminalHop()
	if !nested.Path().Hops()[0].Equal(one.TerminalHop()) {
		t.Fatal("projection retained caller route slice")
	}
	if _, err := base.WithRelationProjections(nested); err == nil {
		t.Fatal("nested projection omitted selected parent")
	}
	if _, err := base.WithRelationProjections(nested, one); err != nil {
		t.Fatal(err)
	}
	reverseAgain := append(nested.Path().Hops(), two.Path().Hops()...)
	tail, err := query.NewRelationProjection(reverseAgain, childPK, []query.FieldRef{childPK, fk})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := base.WithRelationProjections(tail, nested, one); err != nil {
		t.Fatal("mixed traversal failed", err)
	}
	for name, route := range map[string][]query.RelationHop{"disconnected": append(two.Path().Hops(), forward.Hops()...), "collection": reverse(child, "children", "children", ir.RelationOneToMany).Hops()} {
		t.Run(name, func(t *testing.T) {
			if _, err := query.NewRelationProjection(route, childPK, []query.FieldRef{childPK, fk}); err == nil {
				t.Fatal("invalid selection route accepted")
			}
		})
	}
	if _, err := query.NewRelationProjection(one.Path().Hops(), childPK, []query.FieldRef{childPK}); err == nil {
		t.Fatal("reverse projection omitted its child FK")
	}
	conflict := project(reverse(other, "others", "child", ir.RelationOneToOne), childPK, childPK, fk)
	if _, err := base.WithRelationProjections(one, conflict); err == nil {
		t.Fatal("same accessor with conflicting declaration accepted")
	}
}
