package forms_test

import (
	"reflect"
	"strconv"
	"testing"

	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/internal/decimaltest"
	"github.com/progresshans/godj/schema"
)

func TestDecimalModelFormsAgainstPinnedDjango(t *testing.T) {
	model, err := schema.Build(schema.Definition{AppLabel: "costs", Models: []schema.Model{{Name: "record", GoName: "Record", Fields: []schema.Field{schema.DecimalField("cost", "Cost", 12, 2, schema.Nullable())}}}})
	if err != nil {
		t.Fatal(err)
	}
	exceptions := 0
	for index, observed := range decimaltest.Load(t).Form {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			spec, err := formmodel.NewSpec(model.Models[0], formmodel.OverrideField("cost", formmodel.WithRequired(observed.Required)))
			if err != nil {
				t.Fatal(err)
			}
			field := spec.Fields()[0]
			if field.Widget() != forms.NumberInput || observed.Widget != "NumberInput" || field.NumberStep() != observed.WidgetAttrs["step"] {
				t.Fatal("decimal widget/step differs")
			}
			data := map[string][]string{}
			for key, value := range observed.Input {
				data[key] = []string{value}
			}
			for name, initial := range map[string]forms.Value{"null": forms.Null(), "zero": forms.Decimal(decimal.Decimal{}), "negative_zero": forms.Decimal(decimal.Decimal{Coefficient: "-0"}), "same": forms.Decimal(decimal.Decimal{Coefficient: "15", Exponent: -1})} {
				bound, err := spec.Bind(forms.NewData(data), map[string]forms.Value{"cost": initial})
				if err != nil {
					t.Fatal(err)
				}
				codes := map[string][]string{}
				for _, v := range bound.Errors().All() {
					codes[string(v.Field())] = append(codes[string(v.Field())], string(v.Code()))
				}
				if bound.Valid() != observed.Valid || !reflect.DeepEqual(codes, observed.Errors) {
					t.Fatalf("input %q: %v want %v", observed.Input, codes, observed.Errors)
				}
				got, present := bound.Cleaned().Get("cost")
				want, exists := observed.Cleaned["cost"]
				if present != exists {
					t.Fatal("invalid input leaked cleaned value")
				}
				if exists {
					if want == nil {
						if !got.IsNull() {
							t.Fatal("empty lost NULL")
						}
					} else {
						value, ok := got.AsDecimal()
						if !ok || value != want.Decimal(t) {
							t.Fatal("decimal value or zero sign changed")
						}
					}
				}
				changed, exists := observed.Changed[name]
				if !exists {
					t.Fatal("changed observation missing")
				}
				wantChanged := true
				if changed.Exception != "" {
					if observed.Input["cost"] != "sNaN" || name == "null" || changed.Exception != "InvalidOperation" {
						t.Fatal("unnamed changed exception")
					}
					exceptions++
				} else {
					if changed.Value == nil {
						t.Fatal("changed value missing")
					}
					wantChanged = *changed.Value
				}
				if (len(bound.Changed()) == 1) != wantChanged {
					t.Fatalf("input %q changed from %s differs", observed.Input, name)
				}
			}
		})
	}
	if exceptions != 6 {
		t.Fatalf("sNaN deviation selectors %d want 6", exceptions)
	}
}

func TestDecimalFormPrecisionProfilesAndInitialBounds(t *testing.T) {
	supported, unbounded := 0, 0
	for index, row := range decimaltest.Load(t).Precision {
		if row.MaxDigits == nil || row.DecimalPlaces == nil {
			if row.MaxDigits != nil || row.DecimalPlaces != nil {
				t.Fatal("unexpected precision profile")
			}
			unbounded++
			continue
		}
		supported++
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			field, err := forms.DecimalField("cost", *row.MaxDigits, *row.DecimalPlaces, forms.WithNullable(), forms.WithRequired(false))
			if err != nil {
				t.Fatal(err)
			}
			spec, err := forms.NewSpec([]forms.Field{field})
			if err != nil {
				t.Fatal(err)
			}
			bound, err := spec.Bind(forms.NewData(map[string][]string{"cost": {row.Input}}), nil)
			if err != nil {
				t.Fatal(err)
			}
			codes := []string{}
			for _, v := range bound.Errors().All() {
				codes = append(codes, string(v.Code()))
			}
			if !reflect.DeepEqual(codes, row.Form.Codes) {
				t.Fatalf("%d/%d %q: %v want %v", *row.MaxDigits, *row.DecimalPlaces, row.Input, codes, row.Form.Codes)
			}
			if row.Form.Value != nil {
				value, ok := bound.Cleaned().Decimal("cost")
				if !ok || value != row.Form.Value.Decimal(t) {
					t.Fatal("precision value changed")
				}
			}
		})
	}
	if supported != 72 || unbounded != 18 {
		t.Fatal("precision coverage changed")
	}
	for _, precision := range [][2]int{{0, 0}, {1001, 2}, {5, -1}, {5, 6}} {
		if _, err := forms.DecimalField("cost", precision[0], precision[1]); err == nil {
			t.Fatal("invalid precision accepted")
		}
	}
	for _, value := range []forms.Value{forms.String("1.2"), forms.Float(1.2), forms.Decimal(decimal.Decimal{Coefficient: "NaN"}), forms.Decimal(decimal.Decimal{Coefficient: "1", Exponent: -3})} {
		if _, err := forms.DecimalField("cost", 5, 2, forms.WithDefault(value)); err == nil {
			t.Fatal("invalid default accepted")
		}
	}
	field, err := forms.DecimalField("cost", 5, 2, forms.WithNullable(), forms.WithRequired(false))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := spec.Unbound(map[string]forms.Value{"cost": forms.Decimal(decimal.Decimal{Coefficient: "1", Exponent: 3})}); err == nil {
		t.Fatal("out-of-field initial accepted")
	}
	bound, err := spec.Bind(forms.NewData(map[string][]string{"cost": {"1.500"}}), map[string]forms.Value{"cost": forms.Decimal(decimal.Decimal{Coefficient: "15", Exponent: -1})})
	if err != nil || bound.Valid() || len(bound.Changed()) != 0 {
		t.Fatal("precision error altered numeric changed semantics")
	}
	if field.NumberStep() != "0.01" {
		t.Fatal("decimal step")
	}
	for _, scale := range []int{0, 6, 7, 1000} {
		f, err := forms.DecimalField("cost", 1000, scale)
		if err != nil {
			t.Fatal(err)
		}
		want := map[int]string{0: "1", 6: "0.000001", 7: "1e-7", 1000: "1e-1000"}[scale]
		if f.NumberStep() != want {
			t.Fatal("step exponent boundary")
		}
	}
}
