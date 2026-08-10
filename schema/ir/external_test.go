package ir_test

import (
	"testing"

	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestExternalRelationPublicSurface(t *testing.T) {
	t.Parallel()

	var target schema.ModelTarget = ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}
	var reverse schema.ReverseRelation = ir.ReverseRelation{Name: "posts"}
	var policy schema.DeletePolicy = ir.DeleteProtect
	field := schema.ForeignKey("author", "AuthorID", target, reverse, policy)
	if field.Kind != ir.FieldForeignKey || field.Relation == nil {
		t.Fatalf("ForeignKey() = %#v", field)
	}
	if ir.FormatVersion != 2 || ir.RelationFormatVersion != 3 ||
		ir.RelationManyToOne != "many_to_one" || ir.RelationOneToMany != "one_to_many" ||
		ir.DeleteProtect != "protect" || ir.DeleteSetNull != "set_null" {
		t.Fatal("public relation version/token constants drifted")
	}
}
