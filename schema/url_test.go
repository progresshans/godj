package schema_test

import (
	"testing"

	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestURLSchemaRetainsDistinctInputMeaningWithStringStorage(t *testing.T) {
	build := func(field schema.Field) (ir.Schema, error) {
		return schema.Build(schema.Definition{AppLabel: "contacts", Models: []schema.Model{{Name: "contact", GoName: "Contact", Fields: []schema.Field{field}}}})
	}
	url, err := build(schema.URLField("address", "Address", schema.Nullable(), schema.Unique(), schema.Default("legacy")))
	if err != nil {
		t.Fatal(err)
	}
	field := url.Models[0].Fields[1]
	if field.Kind != ir.FieldURL || field.MaxLength != 200 || !field.Nullable || !field.Unique || field.Default.String != "legacy" {
		t.Fatal("url semantics or declared defaults changed")
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
			t.Fatal("url kind transition hid another field change")
		}
	}
	email := field.Clone()
	email.Kind = ir.FieldEmail
	for _, pair := range [][2]ir.Field{{email, field}, {field, email}} {
		if kind, err := ir.ClassifyFieldChange(pair[0], pair[1]); err != nil || kind != ir.ChangeStringSemantics {
			t.Fatal("Email/URL metadata transition changed storage", err)
		}
	}
	char := url.Clone()
	char.Models[0].Fields[1].Kind = ir.FieldChar
	charHash, _ := ir.Hash(char)
	urlHash, _ := ir.Hash(url)
	if charHash == urlHash || field.Equal(previous) {
		t.Fatal("historical metadata lost url kind")
	}
	for _, invalid := range []schema.Field{schema.URLField("address", "Address", schema.MaxLength(0)), schema.URLField("address", "Address", schema.MaxLength(-1)), schema.URLField("address", "Address", schema.Default(true)), schema.URLField("address", "Address", schema.MaxLength(2), schema.Default("abc"))} {
		if _, err := build(invalid); err == nil {
			t.Fatal("invalid url storage declaration accepted")
		}
	}
}
