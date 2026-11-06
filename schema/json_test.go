package schema_test

import (
	"testing"

	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestJSONModelDefaultIdentityAndOwnership(t *testing.T) {
	value := jsonvalue.Value{Text: ` {"b":[true,null],"a":340282366920938463463374607431768211455} `}
	declaration := schema.JSONField("payload", "Payload", schema.Nullable(), schema.Default(value))
	value.Text = `"changed"`
	definition, err := schema.Build(schema.Definition{AppLabel: "jsonref", Models: []schema.Model{{Name: "record", GoName: "Record", Fields: []schema.Field{
		declaration, schema.JSONField("document_null", "DocumentNull", schema.Default(jsonvalue.Null())),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	field := definition.Models[0].Fields[1]
	want := `{"a":340282366920938463463374607431768211455,"b":[true,null]}`
	if field.Kind != ir.FieldJSON || !field.Nullable || field.Default == nil || field.Default.Kind != ir.ScalarJSON || field.Default.JSON != want {
		t.Fatal("JSON default lost precision, ownership, or type")
	}
	null := definition.Models[0].Fields[2]
	if null.Nullable || null.Default == nil || null.Default.JSON != "null" {
		t.Fatal("JSON null incorrectly required SQL nullability")
	}
	clone := definition.Clone()
	clone.Models[0].Fields[1].Default.JSON = `[]`
	if field.Default.JSON != want || field.Equal(clone.Models[0].Fields[1]) {
		t.Fatal("JSON metadata shared mutable default state")
	}
	before, err := ir.Hash(definition)
	if err != nil {
		t.Fatal(err)
	}
	after, err := ir.Hash(clone)
	if err != nil || before == after {
		t.Fatal("JSON omitted from schema identity", err)
	}
	for _, mutate := range []func(*ir.Field){
		func(f *ir.Field) { f.PrimaryKey = true },
		func(f *ir.Field) { f.MaxLength = 100 },
		func(f *ir.Field) { f.Decimal = &ir.DecimalSpec{MaxDigits: 5, DecimalPlaces: 2} },
		func(f *ir.Field) { f.Default = &ir.Scalar{Kind: ir.ScalarString, String: "null"} },
		func(f *ir.Field) { f.Default.JSON = "" },
		func(f *ir.Field) { f.Default.JSON = `{"a":1,"a":2}` },
		func(f *ir.Field) { f.Default.JSON = ` {"a":1}` },
		func(f *ir.Field) { f.Default.JSON = `{"b":2,"a":1}` },
		func(f *ir.Field) { f.Default.Boolean = true },
		func(f *ir.Field) { f.Default.UUID = "00000000-0000-0000-0000-000000000000" },
		func(f *ir.Field) { f.Default.Decimal = "0" },
		func(f *ir.Field) { f.Choices = []ir.Choice{schema.Choice("a", "A")} },
		func(f *ir.Field) { f.Kind = ir.FieldText; f.Default.Kind = ir.ScalarString },
	} {
		invalid := definition.Clone()
		mutate(&invalid.Models[0].Fields[1])
		if _, err := ir.Normalize(invalid); err == nil {
			t.Fatal("invalid or mixed JSON metadata accepted")
		}
	}
}

func TestJSONDeclarationRejectsInvalidAndUntypedDefaults(t *testing.T) {
	for _, field := range []schema.Field{
		schema.JSONField("payload", "Payload", schema.Default(jsonvalue.Value{})),
		schema.JSONField("payload", "Payload", schema.Default(jsonvalue.Value{Text: `"\ud800"`})),
		schema.JSONField("payload", "Payload", schema.Default(`{}`)),
	} {
		if _, err := schema.Build(schema.Definition{AppLabel: "jsonref", Models: []schema.Model{{Name: "record", GoName: "Record", Fields: []schema.Field{field}}}}); err == nil {
			t.Fatal("invalid JSON default silently became a string or null")
		}
	}
}
