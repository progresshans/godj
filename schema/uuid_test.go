package schema_test

import (
	"strings"
	"testing"

	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/uuid"
)

func TestUUIDModelDefaultIdentityAndOwnership(t *testing.T) {
	definition, err := schema.Build(schema.Definition{AppLabel: "uuidref", Models: []schema.Model{{Name: "record", GoName: "Record", Fields: []schema.Field{
		schema.UUIDField("reference", "Reference", schema.Nullable(), schema.Default(uuid.UUID{})),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	field := definition.Models[0].Fields[1]
	if field.Kind != ir.FieldUUID || !field.Nullable || field.MaxLength != 0 || field.Default == nil || field.Default.Kind != ir.ScalarUUID || field.Default.UUID != "00000000-0000-0000-0000-000000000000" {
		t.Fatal("UUID default or nullable identity lost")
	}
	clone := definition.Clone()
	clone.Models[0].Fields[1].Default.UUID = "00000000-0000-0000-0000-000000000001"
	if field.Default.UUID != "00000000-0000-0000-0000-000000000000" || field.Equal(clone.Models[0].Fields[1]) {
		t.Fatal("UUID metadata shares caller ownership")
	}
	before, err := ir.Hash(definition)
	if err != nil {
		t.Fatal(err)
	}
	after, err := ir.Hash(clone)
	if err != nil || before == after {
		t.Fatal("UUID default was omitted from schema identity", err)
	}
	for _, mutate := range []func(*ir.Field){
		func(f *ir.Field) { f.PrimaryKey = true },
		func(f *ir.Field) { f.MaxLength = 32 },
		func(f *ir.Field) { f.Decimal = &ir.DecimalSpec{MaxDigits: 5, DecimalPlaces: 2} },
		func(f *ir.Field) { f.Default = &ir.Scalar{Kind: ir.ScalarString, String: field.Default.UUID} },
		func(f *ir.Field) { f.Default.UUID = strings.Repeat("0", 32) },
		func(f *ir.Field) { f.Default.UUID = "FFFFFFFF-FFFF-FFFF-FFFF-FFFFFFFFFFFF" },
		func(f *ir.Field) { f.Default.UUID = "" },
		func(f *ir.Field) { f.Default.Boolean = true },
		func(f *ir.Field) { f.Default.Decimal = "0" },
		func(f *ir.Field) { f.Choices = []ir.Choice{schema.Choice("a", "A")} },
		func(f *ir.Field) { f.Kind = ir.FieldChar; f.MaxLength = 36; f.Default.Kind = ir.ScalarString },
	} {
		invalid := definition.Clone()
		mutate(&invalid.Models[0].Fields[1])
		if _, err := ir.Normalize(invalid); err == nil {
			t.Fatal("invalid or mixed UUID metadata accepted")
		}
	}
}
