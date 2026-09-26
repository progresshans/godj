package schema_test

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestFloatDefaultsPreserveBitsAndRejectMixedPayloads(t *testing.T) {
	build := func(field schema.Field) (ir.Schema, error) {
		return schema.Build(schema.Definition{AppLabel: "numbers", Models: []schema.Model{{Name: "record", GoName: "Record", Fields: []schema.Field{field}}}})
	}
	cases := []struct {
		value float64
		bits  string
	}{{0, "0000000000000000"}, {math.Copysign(0, -1), "8000000000000000"}, {math.SmallestNonzeroFloat64, "0000000000000001"}, {math.MaxFloat64, "7fefffffffffffff"}, {-math.MaxFloat64, "ffefffffffffffff"}, {math.NaN(), "7ff8000000000000"}, {math.Inf(1), "7ff0000000000000"}, {math.Inf(-1), "fff0000000000000"}}
	for _, test := range cases {
		model, err := build(schema.FloatField("amount", "Amount", schema.Nullable(), schema.Default(test.value)))
		if err != nil {
			t.Fatal(err)
		}
		field := model.Models[0].Fields[1]
		if field.Kind != ir.FieldFloat || field.Default.Kind != ir.ScalarFloat || field.Default.FloatBits != test.bits {
			t.Fatal("Float default bits lost")
		}
		encoded, err := ir.CanonicalJSON(model)
		if err != nil || !json.Valid(encoded) {
			t.Fatal("non-standard default JSON")
		}
		for _, bad := range []ir.Scalar{
			{Kind: ir.ScalarFloat, FloatBits: test.bits, String: "x"}, {Kind: ir.ScalarFloat, FloatBits: test.bits, Integer: 1},
			{Kind: ir.ScalarFloat, FloatBits: test.bits, Boolean: true}, {Kind: ir.ScalarFloat, FloatBits: test.bits, Date: "2000-01-01"},
			{Kind: ir.ScalarFloat, FloatBits: test.bits, Time: "00:00:00"}, {Kind: ir.ScalarFloat, FloatBits: test.bits, DateTime: "2000-01-01T00:00:00.000000Z"},
			{Kind: ir.ScalarFloat, FloatBits: test.bits, Duration: "00:00:00"},
		} {
			altered := model.Clone()
			altered.Models[0].Fields[1].Default = &bad
			if _, err := ir.Normalize(altered); err == nil {
				t.Fatal("mixed float payload accepted")
			}
		}
	}
	for _, field := range []schema.Field{schema.FloatField("amount", "Amount", schema.Default(int64(1))), schema.FloatField("amount", "Amount", schema.Default("1.5"))} {
		if _, err := build(field); err == nil {
			t.Fatal("implicit Float default coercion")
		}
	}
	model, err := build(schema.IntegerField("amount", "Amount", schema.Default(int64(0))))
	if err != nil {
		t.Fatal(err)
	}
	model.Models[0].Fields[1].Default.FloatBits = "0000000000000000"
	if _, err := ir.Normalize(model); err == nil {
		t.Fatal("inactive float payload accepted")
	}
}
