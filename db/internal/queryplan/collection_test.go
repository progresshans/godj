package queryplan_test

import (
	"errors"
	"testing"

	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestCollectionIdentityConflictsCannotHideInNegationOrEmptyPlans(t *testing.T) {
	owner := ir.ModelIdentity{AppLabel: "app", ModelName: "owner"}
	child := ir.ModelIdentity{AppLabel: "app", ModelName: "child"}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	name := query.NewFieldRef("name", "name", query.FieldString, false)
	raw, err := query.NewReverseRelationPath(child, "children", "owner", "owner_id", owner, "owners", "id", "children", false, name, ir.RelationOneToMany)
	if err != nil {
		t.Fatal(err)
	}
	var predicates []query.Expression
	for _, childKey := range []query.FieldRef{id, query.NewFieldRef("other_id", "other_id", query.FieldInteger, false)} {
		path, err := query.NewRelationChain(raw.Hops(), []query.FieldRef{id, childKey}, name, query.RelationTerminalRelatedField)
		if err != nil {
			t.Fatal(err)
		}
		predicate, err := query.NewExpression(query.NewRelatedCondition(path, query.LookupExact, query.String("selected")))
		if err != nil {
			t.Fatal(err)
		}
		predicates = append(predicates, predicate)
	}
	for _, negate := range []bool{false, true} {
		second := predicates[1]
		if negate {
			second, err = query.NotExpression(second)
			if err != nil {
				t.Fatal(err)
			}
		}
		expression, err := query.AndExpressions(predicates[0], second)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := query.NewPlan("owners", []query.FieldRef{id}).WithWhere(expression)
		if err != nil {
			t.Fatal(err)
		}
		empty, err := plan.WithLimit(0)
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []query.Plan{plan, empty} {
			if _, err := queryplan.PrepareJoins(source, "test"); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
				t.Fatal("conflicting row identity accepted", negate, err)
			}
			if sql, args, err := sqlite.Compile(source); sql != "" || args != nil || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
				t.Fatal("invalid collection published SQL", negate, sql, args, err)
			}
		}
	}
}

func TestSQLiteCollectionExistenceOwnsItsJoinBudget(t *testing.T) {
	node := ir.ModelIdentity{AppLabel: "app", ModelName: "node"}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	parent := query.NewFieldRef("parent", "parent_id", query.FieldInteger, true)
	name := query.NewFieldRef("name", "name", query.FieldString, false)
	raw, err := query.NewReverseRelationPath(node, "nodes", "parent", "parent_id", node, "nodes", "id", "children", true, name, ir.RelationOneToMany)
	if err != nil {
		t.Fatal(err)
	}
	for _, depth := range []int{63, 64} {
		hops, keys := make([]query.RelationHop, depth), make([]query.FieldRef, depth+1)
		for i := range hops {
			hops[i] = raw.Hops()[0]
			keys[i] = id
		}
		keys[depth] = id
		path, err := query.NewRelationChain(hops, keys, name, query.RelationTerminalRelatedField)
		if err != nil {
			t.Fatal(err)
		}
		leaf, err := query.NewExpression(query.NewRelatedCondition(path, query.LookupExact, query.String("selected")))
		if err != nil {
			t.Fatal(err)
		}
		negative, err := query.NotExpression(leaf)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := query.NewPlan("nodes", []query.FieldRef{id, parent, name}).WithWhere(negative)
		if err != nil {
			t.Fatal(err)
		}
		prepared, err := queryplan.PrepareJoins(plan, "test")
		if err != nil || len(prepared.Keys) != 0 || len(prepared.Exists) != 1 {
			t.Fatal("negative collection leaked outer joins", err)
		}
		// The outer query has no joins; a zero-row result must still validate
		// the independently bounded EXISTS query before returning success.
		empty, err := plan.WithLimit(0)
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []query.Plan{plan, empty} {
			sql, args, err := sqlite.Compile(source)
			if depth == 64 {
				if sql != "" || args != nil || !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) {
					t.Fatal("collection bypassed SQLite join limit", err)
				}
			} else if err != nil || sql == "" {
				t.Fatal("supported collection depth rejected", err)
			}
		}
	}
}
