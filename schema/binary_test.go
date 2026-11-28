package schema_test

import (
	"strings"
	"testing"

	"github.com/progresshans/godj/binaryvalue"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestBinaryModelDefaultOwnershipAndInputPolicy(t *testing.T) {
	definition, err := schema.Build(schema.Definition{AppLabel: "binaryref", Models: []schema.Model{{Name: "packet", GoName: "Packet", Fields: []schema.Field{
		schema.BinaryField("payload", "Payload", schema.Nullable(), schema.Default(binaryvalue.Value{Data: "\x00\xff"})),
		schema.BinaryField("input", "Input", schema.Editable(true), schema.MaxLength(4)),
		schema.CharField("secret", "Secret", 20, schema.Editable(false)),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	field := definition.Models[0].Fields[1]
	if field.Kind != ir.FieldBinary || !field.NonEditable || !field.Nullable || field.MaxLength != 0 || field.Default == nil || field.Default.Kind != ir.ScalarBinary || field.Default.Binary != "AP8=" || definition.Models[0].Fields[2].NonEditable || !definition.Models[0].Fields[3].NonEditable {
		t.Fatal("binary declaration lost type, default or input policy")
	}
	baseline, err := ir.Hash(definition)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*ir.Field){func(f *ir.Field) { f.Default.Binary = "" }, func(f *ir.Field) { f.NonEditable = false }, func(f *ir.Field) { f.MaxLength = 3 }} {
		clone := definition.Clone()
		mutate(&clone.Models[0].Fields[1])
		hash, err := ir.Hash(clone)
		if err != nil || hash == baseline || field.Equal(clone.Models[0].Fields[1]) || field.Default.Binary != "AP8=" {
			t.Fatal("binary identity or snapshot ownership lost", err)
		}
	}
	for _, mutate := range []func(*ir.Field){
		func(f *ir.Field) { f.PrimaryKey = true }, func(f *ir.Field) { f.MaxLength = -1 }, func(f *ir.Field) { f.MaxLength = binaryvalue.MaxBytes + 1 },
		func(f *ir.Field) { f.MaxLength = 1 }, func(f *ir.Field) { f.Decimal = &ir.DecimalSpec{MaxDigits: 2} },
		func(f *ir.Field) { f.Default = &ir.Scalar{Kind: ir.ScalarString, String: "AP8="} },
		func(f *ir.Field) { f.Default.Binary = "AP9=" }, func(f *ir.Field) { f.Default.Binary = "AP8" },
		func(f *ir.Field) { f.Default.Binary = "AP8=\n" }, func(f *ir.Field) { f.Default.String = "x" },
		func(f *ir.Field) { f.Default.JSON = "null" }, func(f *ir.Field) { f.Choices = []ir.Choice{schema.Choice("a", "A")} },
		func(f *ir.Field) { f.Kind = ir.FieldChar; f.MaxLength = 10; f.Default.Kind = ir.ScalarString },
	} {
		clone := definition.Clone()
		mutate(&clone.Models[0].Fields[1])
		if _, err := ir.Normalize(clone); err == nil {
			t.Fatal("invalid or mixed binary metadata accepted")
		}
	}
	for _, value := range []binaryvalue.Value{{}, {Data: strings.Repeat("x", binaryvalue.MaxBytes+1)}} {
		_, err := schema.Build(schema.Definition{AppLabel: "binaryref", Models: []schema.Model{{Name: "packet", GoName: "Packet", Fields: []schema.Field{schema.BinaryField("payload", "Payload", schema.Default(value))}}}})
		if (err == nil) != value.Valid() {
			t.Fatal("empty or invalid binary default became another value", err)
		}
	}
}
