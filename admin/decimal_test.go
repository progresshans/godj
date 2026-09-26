package admin

import (
	"testing"

	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/templates"
)

func TestDecimalAdminTypedSnapshotPrecisionAndRevalidation(t *testing.T) {
	definition, err := schema.Build(schema.Definition{AppLabel: "costs", Models: []schema.Model{{Name: "record", GoName: "Record", Fields: []schema.Field{schema.DecimalField("cost", "Cost", 5, 2, schema.Nullable())}}}})
	if err != nil {
		t.Fatal(err)
	}
	metadata := definition.Models[0]
	projector, err := NewModelProjector(metadata, func(v decimal.Decimal, field ir.Field) (query.Value, bool) {
		field.Decimal.MaxDigits = 1000
		return query.Decimal(v), true
	}, "cost")
	if err != nil {
		t.Fatal(err)
	}
	for _, number := range []decimal.Decimal{{}, {Coefficient: "-0"}, {Coefficient: "15", Exponent: -1}} {
		object, err := projector.Project(number, 1, "Cost")
		if err != nil {
			t.Fatal(err)
		}
		value, _ := object.Values().Member("cost")
		got, ok := value.AsString()
		want, _ := number.Fixed(2)
		if !ok || got != want {
			t.Fatal("admin snapshot lost scale/zero sign")
		}
		if !initialMatchesSnapshot(forms.Decimal(number), value) {
			t.Fatal("Decimal form initial did not match its fixed-scale snapshot")
		}
		for _, wrong := range []templates.Value{templates.String("2.00"), templates.Integer(0), templates.Null(), templates.String("NaN")} {
			if initialMatchesSnapshot(forms.Decimal(number), wrong) {
				t.Fatal("Decimal initial accepted a mismatched snapshot")
			}
		}
	}
	for _, number := range []decimal.Decimal{{Coefficient: "NaN"}, {Coefficient: "1", Exponent: -3}, {Coefficient: "1", Exponent: 3}} {
		if _, err := projector.Project(number, 1, "Cost"); err == nil {
			t.Fatal("invalid snapshot accepted")
		}
	}
	field := metadata.Fields[1]
	for _, text := range []string{"1.5", "1.500", "1000.00", "NaN"} {
		if validSnapshotValue(templates.String(text), field, 1) {
			t.Fatal("noncanonical or overflowing snapshot admitted")
		}
	}
	wrong, err := NewModelProjector(metadata, func(decimal.Decimal, ir.Field) (query.Value, bool) { return query.String("1.50"), true }, "cost")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrong.Project(decimal.Decimal{}, 1, "Cost"); err == nil {
		t.Fatal("string bypassed typed Decimal snapshot")
	}
	formField, err := forms.DecimalField("cost", 4, 4, forms.WithNullable(), forms.WithRequired(false))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{formField})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := spec.Bind(forms.NewData(map[string][]string{"cost": {"-0.0"}}), nil)
	if err != nil || !bound.Valid() {
		t.Fatal("zero decimal form rejected", err)
	}
	validated, err := validateBoundForm(bound, spec, spec.Fields())
	if err != nil {
		t.Fatal("canonical revalidation lost input scale needed for zero whole digits", err)
	}
	value, ok := validated.Decimal("cost")
	if !ok || value.Coefficient != "-0" {
		t.Fatal("revalidation lost negative zero")
	}
	if text, _ := renderedFieldValue(spec.Fields()[0], bound, map[string][]string{"cost": {"-0.0"}}); text != "-0.0" {
		t.Fatal("bound form lost submitted decimal")
	}
}
