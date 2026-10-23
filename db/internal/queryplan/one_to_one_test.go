package queryplan_test

import (
	"errors"
	"testing"

	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestOneToOneOppositeDirectionsCannotDisagreeAboutCardinality(t *testing.T) {
	model := ir.ModelIdentity{AppLabel: "nodes", ModelName: "node"}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	parent := query.NewFieldRef("parent", "parent_id", query.FieldInteger, true)
	reverse, err := query.NewReverseRelationPath(model, "nodes_node", "parent", "parent_id", model, "nodes_node", "id", "child", true, id, ir.RelationOneToOne)
	if err != nil {
		t.Fatal(err)
	}
	for _, cardinality := range []ir.RelationCardinality{ir.RelationManyToOne, ir.RelationOneToOne} {
		forward, err := query.NewForwardRelationIsNullPath(model, "nodes_node", parent, model, "nodes_node", "id", cardinality)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := query.NewPlan("nodes_node", []query.FieldRef{id, parent}).WithConditions(
			query.NewRelatedCondition(reverse, query.LookupExact, query.Integer(2)),
			query.NewRelatedCondition(forward, query.LookupIsNull, query.Boolean(false)),
		)
		if err != nil {
			t.Fatal(err)
		}
		statement, arguments, err := sqlite.Compile(plan)
		if cardinality == ir.RelationManyToOne {
			if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) || statement != "" || arguments != nil {
				t.Fatal("conflicting physical declaration published SQL", statement, arguments, err)
			}
		} else if err != nil || statement == "" {
			t.Fatal("consistent forward/reverse declaration rejected", err)
		}
	}
}
