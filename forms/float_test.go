package forms_test

import (
	"math"
	"reflect"
	"strconv"
	"testing"

	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/internal/floattest"
	"github.com/progresshans/godj/schema"
)

func TestFloatModelFormsAgainstPinnedDjango(t *testing.T) {
	definition, err := schema.Build(schema.Definition{AppLabel: "numbers", Models: []schema.Model{{Name: "entry", GoName: "Entry", Fields: []schema.Field{schema.FloatField("effort", "Effort", schema.Nullable())}}}})
	if err != nil {
		t.Fatal(err)
	}
	for index, observation := range floattest.Load(t).Form {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			spec, err := formmodel.NewSpec(definition.Models[0], formmodel.OverrideField("effort", formmodel.WithRequired(observation.Required)))
			if err != nil {
				t.Fatal(err)
			}
			if spec.Fields()[0].Widget() != forms.NumberInput || observation.Widget != "NumberInput" || observation.WidgetAttrs["step"] != "any" {
				t.Fatal("float widget or step changed")
			}
			data := map[string][]string{}
			for key, value := range observation.Input {
				data[key] = []string{value}
			}
			for name, initial := range map[string]forms.Value{"null": forms.Null(), "zero": forms.Float(0), "negative_zero": forms.Float(math.Copysign(0, -1)), "same": forms.Float(1.5)} {
				bound, err := spec.Bind(forms.NewData(data), map[string]forms.Value{"effort": initial})
				if err != nil {
					t.Fatal(err)
				}
				codes := map[string][]string{}
				for _, v := range bound.Errors().All() {
					codes[string(v.Field())] = append(codes[string(v.Field())], string(v.Code()))
				}
				if bound.Valid() != observation.Valid || !reflect.DeepEqual(codes, observation.Errors) {
					t.Fatalf("input %q: errors=%v want=%v", observation.Input, codes, observation.Errors)
				}
				got, present := bound.Cleaned().Get("effort")
				want, exists := observation.Cleaned["effort"]
				if present != exists {
					t.Fatal("invalid input leaked cleaned value")
				}
				if exists {
					if want == nil {
						if !got.IsNull() {
							t.Fatal("empty input lost NULL")
						}
					} else {
						value, ok := got.AsFloat()
						if !ok || math.Float64bits(value) != math.Float64bits(want.Float(t)) {
							t.Fatalf("float bits differ: %016x want %s", math.Float64bits(value), want.Bits)
						}
					}
				}
				changed, exists := observation.Changed[name]
				if !exists || (len(bound.Changed()) == 1) != changed {
					t.Fatalf("changed from %s differs", name)
				}
			}
		})
	}
}
func TestFloatFormDefaultsAndAdmission(t *testing.T) {
	for _, value := range []forms.Value{forms.Null(), forms.Float(0), forms.Float(math.Copysign(0, -1)), forms.Float(math.SmallestNonzeroFloat64)} {
		field, err := forms.FloatField("effort", forms.WithNullable(), forms.WithRequired(false), forms.WithDefault(value))
		if err != nil {
			t.Fatal(err)
		}
		spec, err := forms.NewSpec([]forms.Field{field})
		if err != nil {
			t.Fatal(err)
		}
		initial, err := spec.Unbound(nil)
		got, _ := initial.Initial().Get("effort")
		if err != nil || !got.Equal(value) {
			t.Fatal("default lost")
		}
		bound, err := spec.Bind(forms.NewData(nil), nil)
		got, _ = bound.Cleaned().Get("effort")
		if err != nil || !bound.Valid() || !got.IsNull() {
			t.Fatal("bound omission used initial default")
		}
		bound, err = spec.Bind(forms.NewData(map[string][]string{"effort": {"1", "2"}}), nil)
		if err != nil || bound.Valid() {
			t.Fatal("repeated float accepted")
		}
	}
	for _, value := range []forms.Value{forms.Float(math.NaN()), forms.Float(math.Inf(1)), forms.Float(math.Inf(-1)), forms.Integer(1), forms.String("1.5")} {
		if _, err := forms.FloatField("effort", forms.WithDefault(value)); err == nil {
			t.Fatal("invalid Float default accepted")
		}
	}
}
