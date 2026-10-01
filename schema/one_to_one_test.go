package schema_test

import (
	"reflect"
	"testing"

	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestOneToOneDeclarationOwnsUniquenessAndReverseMeaning(t *testing.T) {
	for name, reverse := range map[string]schema.ReverseRelation{
		"default": {}, "explicit": schema.RelatedName("service_report"), "hidden": schema.NoReverse(),
	} {
		t.Run(name, func(t *testing.T) {
			input := schema.Definition{AppLabel: "reports", Models: []schema.Model{{Name: "report", GoName: "Report", Fields: []schema.Field{
				schema.OneToOne("ticket", "TicketID", schema.Target("tickets", "ticket"), reverse, schema.Protect),
			}}}}
			built, err := schema.Build(input)
			if err != nil {
				t.Fatal(err)
			}
			field := built.Models[0].Fields[1]
			wantReverse := reverse
			if name == "default" {
				wantReverse.Name = "report"
			}
			if field.Kind != ir.FieldForeignKey || !field.Unique || field.Column != "ticket_id" ||
				field.Relation.Cardinality != ir.RelationOneToOne || field.Relation.Reverse != wantReverse {
				t.Fatalf("one-to-one metadata: %+v, relation %+v", field, field.Relation)
			}
			if input.Models[0].Fields[0].Relation.Reverse != reverse {
				t.Fatal("normalization changed declaration ownership")
			}
			implicit := built.Clone()
			implicit.Models[0].Fields[1].Unique = false
			normalized, err := ir.Normalize(implicit)
			if err != nil || !reflect.DeepEqual(normalized, built) || implicit.Models[0].Fields[1].Unique {
				t.Fatal("one-to-one uniqueness can be removed or normalization mutated its input", err)
			}
			ordinary := built.Clone()
			ordinary.Models[0].Fields[1].Relation.Cardinality = ir.RelationManyToOne
			oneHash, err := ir.Hash(built)
			if err != nil {
				t.Fatal(err)
			}
			fkHash, err := ir.Hash(ordinary)
			if err != nil || oneHash == fkHash {
				t.Fatal("schema hash erased one-to-one cardinality", err)
			}
			if ordinary.Models[0].Fields[1].Relation.Reverse != wantReverse {
				t.Fatal("clone borrowed relation metadata")
			}
		})
	}
}

func TestOneToOneNullabilityAndInvalidFieldShapes(t *testing.T) {
	base := schema.OneToOne("ticket", "TicketID", schema.Target("tickets", "ticket"), schema.RelatedName("report"), schema.SetNull, schema.Nullable())
	build := func(field schema.Field) error {
		_, err := schema.Build(schema.Definition{AppLabel: "reports", Models: []schema.Model{{Name: "report", GoName: "Report", Fields: []schema.Field{field}}}})
		return err
	}
	if err := build(base); err != nil {
		t.Fatal(err)
	}
	base.Nullable = false
	if err := build(base); err == nil {
		t.Fatal("required one-to-one accepted SET_NULL")
	}
	base.Nullable = true
	base.Relation.Cardinality = ir.RelationOneToMany
	if err := build(base); err == nil {
		t.Fatal("source column accepted one-to-many cardinality")
	}
}
