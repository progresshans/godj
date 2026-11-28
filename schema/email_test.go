package schema_test

import (
	"testing"

	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestEmailSchemaRetainsDistinctInputMeaningWithStringStorage(t *testing.T) {
	build := func(field schema.Field) (ir.Schema, error) {
		return schema.Build(schema.Definition{AppLabel: "contacts", Models: []schema.Model{{Name: "contact", GoName: "Contact", Fields: []schema.Field{field}}}})
	}
	email, err := build(schema.EmailField("address", "Address", schema.Nullable(), schema.Unique(), schema.Default("legacy")))
	if err != nil {
		t.Fatal(err)
	}
	field := email.Models[0].Fields[1]
	if field.Kind != ir.FieldEmail || field.MaxLength != 254 || !field.Nullable || !field.Unique || field.Default.String != "legacy" {
		t.Fatal("email semantics or declared defaults changed")
	}
	previous := field.Clone()
	previous.Kind = ir.FieldChar
	for _, pair := range [][2]ir.Field{{previous, field}, {field, previous}} {
		kind, err := ir.ClassifyFieldChange(pair[0], pair[1])
		if err != nil || kind != ir.ChangeStringSemantics {
			t.Fatalf("bounded semantic change: %v %v", kind, err)
		}
	}
	for _, mutate := range []func(*ir.Field){
		func(f *ir.Field) { f.MaxLength++ }, func(f *ir.Field) { f.Nullable = false }, func(f *ir.Field) { f.Unique = false }, func(f *ir.Field) { f.Column = "other" }, func(f *ir.Field) { f.Default.String = "different" }, func(f *ir.Field) { f.Choices = []ir.Choice{schema.Choice("legacy", "Old")} },
	} {
		changed := field.Clone()
		mutate(&changed)
		if _, err := ir.ClassifyFieldChange(previous, changed); err == nil {
			t.Fatal("email kind transition hid another field change")
		}
	}
	char := email.Clone()
	char.Models[0].Fields[1].Kind = ir.FieldChar
	charHash, _ := ir.Hash(char)
	emailHash, _ := ir.Hash(email)
	if charHash == emailHash || field.Equal(previous) {
		t.Fatal("historical metadata lost email kind")
	}
	for _, invalid := range []schema.Field{schema.EmailField("address", "Address", schema.MaxLength(0)), schema.EmailField("address", "Address", schema.MaxLength(-1)), schema.EmailField("address", "Address", schema.Default(true)), schema.EmailField("address", "Address", schema.MaxLength(2), schema.Default("abc"))} {
		if _, err := build(invalid); err == nil {
			t.Fatal("invalid email storage declaration accepted")
		}
	}
}
