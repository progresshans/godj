package schema_test

import (
	"math"
	"testing"

	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestIntegerFieldNormalizesFullRangeDefaultsAndNullability(t *testing.T) {
	for _, value := range []int64{math.MinInt64, -1, 0, 1, math.MaxInt64} {
		for _, nullable := range []bool{false, true} {
			options := []schema.FieldOption{schema.Default(value)}
			if nullable {
				options = append(options, schema.Nullable())
			}
			input := schema.Definition{AppLabel: "numbers", Models: []schema.Model{{Name: "counter", GoName: "Counter", Fields: []schema.Field{
				schema.IntegerField("value", "Value", options...),
			}}}}
			normalized, err := schema.Build(input)
			if err != nil {
				t.Fatal(err)
			}
			field := normalized.Models[0].Fields[1]
			if field.Kind != ir.FieldInteger || field.PrimaryKey || field.Nullable != nullable || field.Column != "value" ||
				field.Default == nil || field.Default.Kind != ir.ScalarInteger || field.Default.Integer != value {
				t.Fatalf("integer metadata = %+v", field)
			}
			input.Models[0].Fields[0].Default.Integer = 19
			if field.Default.Integer != value {
				t.Fatal("normalized default aliases the declaration")
			}
			before, err := ir.Hash(normalized)
			if err != nil {
				t.Fatal(err)
			}
			after, err := ir.Normalize(normalized)
			if err != nil {
				t.Fatal(err)
			}
			hash, err := ir.Hash(after)
			if err != nil || before != hash {
				t.Fatal("integer normalization is not idempotent")
			}
		}
	}
}

func TestIntegerFieldRejectsNonScalarOrAutomaticMetadata(t *testing.T) {
	for _, invalid := range []schema.Field{
		schema.IntegerField("value", "Value", schema.Default("1")),
		schema.IntegerField("value", "Value", schema.Default(false)),
		{Name: "value", GoName: "Value", Kind: ir.FieldInteger, MaxLength: 10},
		{Name: "value", GoName: "Value", Kind: ir.FieldInteger, Relation: &ir.ForeignKeyRelation{}},
		{Name: "value", GoName: "Value", Kind: ir.FieldInteger, Default: &ir.Scalar{Kind: ir.ScalarInteger, Boolean: true}},
	} {
		if _, err := schema.Build(schema.Definition{AppLabel: "numbers", Models: []schema.Model{{Name: "counter", GoName: "Counter", Fields: []schema.Field{invalid}}}}); err == nil {
			t.Fatalf("invalid integer metadata accepted: %+v", invalid)
		}
	}
	if _, err := ir.Normalize(ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "numbers", Models: []ir.Model{{
		Name: "counter", GoName: "Counter", Fields: []ir.Field{{Name: "value", GoName: "Value", Kind: ir.FieldInteger, PrimaryKey: true}},
	}}}); err == nil {
		t.Fatal("ordinary integer primary key was accepted")
	}
}
