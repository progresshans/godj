package querytest

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// GroupCompiler owns the backend-independent alias, argument and group
// cardinality invariants. Native result/codec behavior belongs to the generated
// grouped consumer, which runs the same cases against both actual backends.
func GroupCompiler(t *testing.T, compile func(query.Plan) (string, []any, error), postgres bool) {
	t.Helper()
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	rank := query.NewFieldRef("rank", "rank", query.FieldInteger, true)
	note := query.NewFieldRef("note", "note", query.FieldString, true)
	enabled := query.NewFieldRef("enabled", "enabled", query.FieldBoolean, false)
	base := query.NewPlan("godj_groups", []query.FieldRef{id, rank, note, enabled})
	filter, err := query.NewExpression(query.NewCondition(enabled, query.LookupExact, query.Boolean(true)))
	if err != nil {
		t.Fatal(err)
	}
	opened, err := query.CountResult(query.FieldResult(id))
	if err != nil {
		t.Fatal(err)
	}
	opened, err = opened.WithFilter(filter)
	if err != nil {
		t.Fatal(err)
	}
	distinct, err := query.CountResult(query.FieldResult(note))
	if err != nil {
		t.Fatal(err)
	}
	distinct, err = distinct.WithDistinct()
	if err != nil {
		t.Fatal(err)
	}
	minimum := query.MinResult(note)
	shape, err := query.NewGroupedResult([]query.ResultExpression{query.FieldResult(rank)}, []query.ResultExpression{opened, distinct, minimum})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Conditions(t, base, query.NewCondition(id, query.LookupGreaterThan, query.Integer(9))).WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	having, err := query.NewGroupExpression(opened, query.LookupGreaterThanOrEqual, query.Integer(2))
	if err != nil {
		t.Fatal(err)
	}
	key, err := query.NewGroupExpression(query.FieldResult(rank), query.LookupExact, query.Integer(1))
	if err != nil {
		t.Fatal(err)
	}
	key, err = query.NotGroupExpression(key)
	if err != nil {
		t.Fatal(err)
	}
	aggregate, err := query.NewGroupExpression(minimum, query.LookupExact, query.String("'hostile? $7"))
	if err != nil {
		t.Fatal(err)
	}
	aggregate, err = query.NotGroupExpression(aggregate)
	if err != nil {
		t.Fatal(err)
	}
	having, err = query.AndGroupExpressions(having, key, aggregate)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = plan.WithGroupHaving(having)
	if err != nil {
		t.Fatal(err)
	}
	order, err := query.NewGroupOrdering(opened, query.Descending, query.NullsLast)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = plan.WithGroupOrderings(order)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = plan.WithLimit(0)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = plan.WithOffset(2)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = plan.WithGroupMode(query.GroupPage)
	if err != nil {
		t.Fatal(err)
	}
	statement, args, err := compile(plan)
	if err != nil {
		t.Fatal(err)
	}
	want := []any{true, int64(9), int64(2), int64(1), "'hostile? $7", int64(0), int64(2)}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("parameter traversal %v, want %v", args, want)
	}
	for _, fragment := range []string{`COUNT("t0"."id") FILTER (WHERE `, `COUNT(DISTINCT "t0"."note")`, " GROUP BY 1", `"c0" IS NOT NULL`, `LEFT OUTER JOIN "godj_page"`, `"godj_page"."c1" DESC NULLS LAST`} {
		if !strings.Contains(statement, fragment) {
			t.Fatalf("missing %q: %s", fragment, statement)
		}
	}
	if strings.Contains(statement, "hostile") || strings.Contains(statement, `"c3" IS NOT NULL`) || strings.Count(statement, " FILTER ") != 1 {
		t.Fatal("unsafe value rendering, nullable aggregate NOT, or duplicate aggregate", statement)
	}
	if postgres {
		for i := 1; i <= len(want); i++ {
			if strings.Count(statement, fmt.Sprint("$", i)) != 1 {
				t.Fatal("placeholder traversal", statement)
			}
		}
	} else if strings.Count(statement, "?") != len(want) || !strings.Contains(statement, `FROM "main"."godj_groups"`) {
		t.Fatal("SQLite parameter traversal or CTE source shadow", statement)
	}
	count, err := plan.WithGroupMode(query.GroupCount)
	if err != nil {
		t.Fatal(err)
	}
	statement, args, err = compile(count)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(statement, "SELECT COUNT(*) FROM (") || !strings.Contains(statement, "GROUP BY 1") || !reflect.DeepEqual(args, want) {
		t.Fatal("count must count sliced groups", statement, args)
	}

	t.Run("conditional relation keeps absent roots", func(t *testing.T) {
		fk := query.NewFieldRef("group", "group_id", query.FieldInteger, true)
		region := query.NewFieldRef("region", "region", query.FieldString, true)
		path, err := query.NewForwardRelationPath(ir.ModelIdentity{AppLabel: "app", ModelName: "item"}, "items", "group", "group_id", ir.ModelIdentity{AppLabel: "app", ModelName: "group"}, "groups", "id", true, region, ir.RelationManyToOne)
		if err != nil {
			t.Fatal(err)
		}
		condition, err := query.NewExpression(query.NewRelatedCondition(path, query.LookupExact, query.String("east")))
		if err != nil {
			t.Fatal(err)
		}
		value, err := query.CountResult(query.FieldResult(id))
		if err != nil {
			t.Fatal(err)
		}
		value, err = value.WithFilter(condition)
		if err != nil {
			t.Fatal(err)
		}
		shape, err := query.NewGroupedResult([]query.ResultExpression{query.FieldResult(rank)}, []query.ResultExpression{value})
		if err != nil {
			t.Fatal(err)
		}
		plan, err := query.NewPlan("items", []query.FieldRef{id, rank, fk}).WithResultShape(shape)
		if err != nil {
			t.Fatal(err)
		}
		statement, args, err := compile(plan)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(statement, "LEFT OUTER JOIN") != 1 || strings.Contains(statement, "INNER JOIN") || !reflect.DeepEqual(args, []any{"east"}) {
			t.Fatal("conditional forward join pruned groups", statement, args)
		}
	})
}
