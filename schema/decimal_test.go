package schema_test

import (
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
	"testing"
)

func TestDecimalSchemaCanonicalDefaultsPrecisionAndOwnership(t *testing.T) {
	build := func(field schema.Field) (ir.Schema, error) {
		return schema.Build(schema.Definition{AppLabel: "costs", Models: []schema.Model{{Name: "record", GoName: "Record", Fields: []schema.Field{field}}}})
	}
	for _, value := range []decimal.Decimal{{}, {Coefficient: "-0"}, {Coefficient: "0012300", Exponent: -4}} {
		input := schema.DecimalField("cost", "Cost", 5, 2, schema.Nullable(), schema.Default(value))
		model, err := build(input)
		if err != nil {
			t.Fatal(err)
		}
		field := model.Models[0].Fields[1]
		if field.Kind != ir.FieldDecimal || field.Default == nil || field.Default.Kind != ir.ScalarDecimal || field.Default.Decimal != value.String() {
			t.Fatal("default lost canonical decimal")
		}
		input.Decimal.MaxDigits = 6
		if field.Decimal.MaxDigits != 5 {
			t.Fatal("Build borrowed caller precision")
		}
		clone := field.Clone()
		clone.Decimal.DecimalPlaces = 1
		if field.Decimal.DecimalPlaces != 2 || field.Equal(clone) {
			t.Fatal("clone or equality lost precision")
		}
		for _, mutate := range []func(*ir.Field){
			func(f *ir.Field) { f.Decimal = nil }, func(f *ir.Field) { f.Decimal.MaxDigits = 0 },
			func(f *ir.Field) { f.Decimal.MaxDigits = 1001 }, func(f *ir.Field) { f.Decimal.DecimalPlaces = -1 },
			func(f *ir.Field) { f.Decimal.DecimalPlaces = 6 }, func(f *ir.Field) { f.PrimaryKey = true },
			func(f *ir.Field) { f.MaxLength = 1 }, func(f *ir.Field) { f.Default.Decimal = "1.230" },
			func(f *ir.Field) { f.Default.Decimal = "1000" }, func(f *ir.Field) { f.Default.Decimal = "1.001" },
			func(f *ir.Field) { f.Default.FloatBits = "0000000000000000" },
		} {
			bad := model.Clone()
			mutate(&bad.Models[0].Fields[1])
			if _, err := ir.Normalize(bad); err == nil {
				t.Fatal("invalid decimal metadata accepted")
			}
		}
	}
	plain, err := build(schema.IntegerField("count", "Count"))
	if err != nil {
		t.Fatal(err)
	}
	plain.Models[0].Fields[1].Decimal = &ir.DecimalSpec{MaxDigits: 5, DecimalPlaces: 2}
	if _, err := ir.Normalize(plain); err == nil {
		t.Fatal("inactive decimal precision accepted")
	}
	for _, value := range []decimal.Decimal{{Coefficient: "NaN"}, {Coefficient: "1000"}, {Coefficient: "1", Exponent: -3}} {
		if _, err := build(schema.DecimalField("cost", "Cost", 5, 2, schema.Default(value))); err == nil {
			t.Fatal("invalid decimal default accepted")
		}
	}
}
