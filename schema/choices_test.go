package schema_test

import (
	"testing"

	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestChoicesKeepOwnedOrderAndHistoricalIdentity(t *testing.T) {
	choices := []ir.Choice{schema.Choice("open", "열림"), schema.Choice("closed", "닫힘")}
	option := schema.Choices(choices...)
	choices[0].Label = "caller changed option input"
	definition := schema.Definition{AppLabel: "tickets", Models: []schema.Model{{Name: "ticket", GoName: "Ticket", Fields: []schema.Field{
		schema.CharField("status", "Status", 12, option, schema.Default("open")),
		schema.IntegerField("priority", "Priority", schema.Nullable(), schema.Choices(schema.Choice(int64(-1), "Low"), schema.Choice(int64(0), "Normal"))),
	}}}}
	normalized, err := schema.Build(definition)
	if err != nil {
		t.Fatal(err)
	}
	field := normalized.Models[0].Fields[1]
	definition.Models[0].Fields[0].Choices[0].Label = "caller changed field input"
	if label, found := field.ChoiceLabel(ir.Scalar{Kind: ir.ScalarString, String: "open"}); !found || label != "열림" {
		t.Fatal("choice snapshot retained caller storage")
	}
	if _, found := field.ChoiceLabel(ir.Scalar{Kind: ir.ScalarString, String: "retired"}); found {
		t.Fatal("unknown stored value acquired a label")
	}
	before, err := ir.Hash(normalized)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*ir.Field){
		func(f *ir.Field) { f.Choices[0].Label = "Updated label" },
		func(f *ir.Field) { f.Choices[0], f.Choices[1] = f.Choices[1], f.Choices[0] },
		func(f *ir.Field) { f.Choices[0].Value.String = "pending" },
	} {
		copy := normalized.Clone()
		mutate(&copy.Models[0].Fields[1])
		after, err := ir.Hash(copy)
		if err != nil || before == after || field.Equal(copy.Models[0].Fields[1]) {
			t.Fatalf("choice identity did not change: %v", err)
		}
		if label, _ := field.ChoiceLabel(ir.Scalar{Kind: ir.ScalarString, String: "open"}); label != "열림" {
			t.Fatal("clone mutation changed original choices")
		}
	}
	if !field.Equal(field.Clone()) {
		t.Fatal("metadata equality depends on owned pointer identity")
	}
}

func TestChoicesRejectAmbiguousOrUnsupportedDeclarations(t *testing.T) {
	for _, field := range []schema.Field{
		schema.CharField("value", "Value", 4, schema.Choices()),
		schema.CharField("value", "Value", 4, schema.Choices(schema.Choice("a", "First"), schema.Choice("a", "Second"))),
		schema.CharField("value", "Value", 4, schema.Choices(schema.Choice("large", "Too long"))),
		schema.CharField("value", "Value", 4, schema.Choices(schema.Choice(int64(1), "Wrong kind"))),
		schema.IntegerField("value", "Value", schema.Choices(schema.Choice("1", "Wrong kind"))),
		schema.BooleanField("value", "Value", schema.Choices(schema.Choice(int64(1), "Unsupported field"))),
		schema.TextField("value", "Value", schema.Choices(schema.Choice("a\x00", "NUL value"))),
		schema.TextField("value", "Value", schema.Choices(schema.Choice("a", "NUL\x00label"))),
		schema.TextField("value", "Value", schema.Choices(ir.Choice{Value: ir.Scalar{Kind: ir.ScalarString, String: "a", Integer: 1}, Label: "Mixed scalar"})),
	} {
		if _, err := schema.Build(schema.Definition{AppLabel: "tickets", Models: []schema.Model{{Name: "ticket", GoName: "Ticket", Fields: []schema.Field{field}}}}); err == nil {
			t.Fatal("invalid choices declaration accepted")
		}
	}
}
