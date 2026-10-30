package schema_test

import (
	"testing"

	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestManyToManyDeclarationKeepsColumnlessThroughAndDirectedSelf(t *testing.T) {
	labels := schema.ManyToMany("labels", "Labels", schema.Target("labels", "label"), schema.RelatedName("tickets"), schema.Through(schema.Target("links", "ticket_label"), "ticket", "label"))
	follows := schema.ManyToMany("follows", "Follows", schema.Target("helpdesk", "ticket"), schema.RelatedName("followers"), schema.Directed())
	input := schema.Definition{AppLabel: "helpdesk", Models: []schema.Model{{Name: "ticket", GoName: "Ticket", Fields: []schema.Field{schema.TextField("title", "Title")}, ManyToMany: []ir.ManyToManyField{labels, follows}}}}
	value, err := schema.Build(input)
	if err != nil {
		t.Fatal(err)
	}
	model := value.Models[0]
	if len(model.Fields) != 2 || len(model.ManyToMany) != 2 || model.ManyToMany[1].Symmetry != ir.ManyToManyDirected || *model.ManyToMany[0].Through != *labels.Through {
		t.Fatal("declaration lost collection meaning")
	}
	labels.Through.SourceField = "mutated"
	if model.ManyToMany[0].Through.SourceField != "ticket" {
		t.Fatal("Build retained caller through pointer")
	}
}
