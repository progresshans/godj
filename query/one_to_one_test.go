package query_test

import (
	"testing"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestOneToOneCardinalitySurvivesPathsChainsAndProjection(t *testing.T) {
	source := ir.ModelIdentity{AppLabel: "reports", ModelName: "report"}
	target := ir.ModelIdentity{AppLabel: "tickets", ModelName: "ticket"}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	fk := query.NewFieldRef("ticket", "ticket_id", query.FieldInteger, true)
	one, err := query.NewForwardRelationPath(source, "reports_report", "ticket", "ticket_id", target, "tickets_ticket", "id", true, id, ir.RelationOneToOne)
	if err != nil {
		t.Fatal(err)
	}
	many, err := query.NewForwardRelationPath(source, "reports_report", "ticket", "ticket_id", target, "tickets_ticket", "id", true, id, ir.RelationManyToOne)
	if err != nil || one.Equal(many) {
		t.Fatal("AST erased cardinality", err)
	}
	chain, err := query.NewForwardRelationChain(one.Hops(), id, query.RelationTerminalRelatedField)
	if err != nil || !chain.Equal(one) || chain.Hops()[0].Cardinality() != ir.RelationOneToOne {
		t.Fatal("chain lost cardinality", err)
	}
	projection, err := query.NewForwardRelationProjection(source, "reports_report", fk, target, "tickets_ticket", id, []query.FieldRef{id}, ir.RelationOneToOne)
	if err != nil || !projection.Path().Equal(one) {
		t.Fatal("projection uses a different relation meaning", err)
	}
	nullPath, err := query.NewForwardRelationIsNullPath(source, "reports_report", fk, target, "tickets_ticket", "id", ir.RelationOneToOne)
	if err != nil || nullPath.Validate() != nil || nullPath.Hops()[0].Cardinality() != ir.RelationOneToOne {
		t.Fatal("source-key path lost cardinality", err)
	}
	reverse, err := query.NewReverseRelationPath(source, "reports_report", "ticket", "ticket_id", target, "tickets_ticket", "id", "report", true, id, ir.RelationOneToOne)
	if err != nil || reverse.Validate() != nil || reverse.Hops()[0].Cardinality() != ir.RelationOneToOne {
		t.Fatal("reverse path lost cardinality", err)
	}
	for _, unsupported := range []ir.RelationCardinality{"", ir.RelationOneToMany, "many_to_many"} {
		if _, err := query.NewForwardRelationPath(source, "reports_report", "ticket", "ticket_id", target, "tickets_ticket", "id", true, id, unsupported); err == nil {
			t.Fatal("invalid forward cardinality accepted", unsupported)
		}
	}
}
