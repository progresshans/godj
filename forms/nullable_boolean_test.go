package forms_test

import (
	"strconv"
	"testing"

	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/internal/nullablebooleantest"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/validation"
)

func TestNullableBooleanModelFormsAgainstPinnedDjangoWidgetObservations(t *testing.T) {
	definition, err := schema.Build(schema.Definition{AppLabel: "reviews", Models: []schema.Model{{Name: "review", GoName: "Review", Fields: []schema.Field{schema.BooleanField("flag", "Flag", schema.Nullable())}}}})
	if err != nil {
		t.Fatal(err)
	}
	for index, observation := range nullablebooleantest.Load(t).Form {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			spec, err := formmodel.NewSpec(definition.Models[0], formmodel.OverrideField("flag", formmodel.WithRequired(observation.Required)))
			if err != nil {
				t.Fatal(err)
			}
			if field := spec.Fields()[0]; field.Widget() != forms.NullBooleanSelect || !field.Nullable() {
				t.Fatal("model lost three-state widget")
			}
			data := make(map[string][]string, len(observation.Input))
			for name, raw := range observation.Input {
				data[name] = []string{raw}
			}
			for name, initial := range map[string]forms.Value{"null": forms.Null(), "false": forms.Boolean(false), "true": forms.Boolean(true)} {
				bound, err := spec.Bind(forms.NewData(data), map[string]forms.Value{"flag": initial})
				if err != nil {
					t.Fatal(err)
				}
				if !observation.Valid || len(observation.Errors) != 0 || !bound.Valid() || !bound.Errors().Empty() {
					t.Fatalf("widget cleaning errors: %v", bound.Errors().All())
				}
				expected, exists := observation.Cleaned["flag"]
				actual, present := bound.Cleaned().Get("flag")
				if !exists || !present {
					t.Fatal("cleaned field absent")
				}
				if expected == nil {
					if !actual.IsNull() {
						t.Fatal("unknown was converted to false")
					}
				} else if value, valid := actual.AsBoolean(); !valid || value != *expected {
					t.Fatal("known Boolean was converted to null or inverted")
				}
				changed, exists := observation.Changed[name]
				if !exists || (len(bound.Changed()) == 1) != changed {
					t.Fatalf("changed from %s differs: %v", name, bound.Changed())
				}
			}
		})
	}
}

func TestNullableBooleanDefaultsValidationAndRepeatedInput(t *testing.T) {
	for _, initial := range []forms.Value{forms.Null(), forms.Boolean(false), forms.Boolean(true)} {
		field, err := forms.BooleanField("flag", forms.WithNullable(), forms.WithDefault(initial))
		if err != nil {
			t.Fatal(err)
		}
		spec, err := forms.NewSpec([]forms.Field{field})
		if err != nil {
			t.Fatal(err)
		}
		unbound, err := spec.Unbound(nil)
		if err != nil {
			t.Fatal(err)
		}
		actual, _ := unbound.Initial().Get("flag")
		if !actual.Equal(initial) {
			t.Fatal("initial null/false/true default changed")
		}
		bound, err := spec.Bind(forms.NewData(nil), nil)
		cleaned, _ := bound.Cleaned().Get("flag")
		if err != nil || !bound.Valid() || !cleaned.IsNull() {
			t.Fatal("a default replaced omitted bound input")
		}
		bound, err = spec.Bind(forms.NewData(map[string][]string{"flag": {"true", "false"}}), nil)
		if err != nil || bound.Valid() || bound.Errors().Len() != 1 {
			t.Fatal("ambiguous repeated Boolean accepted")
		}
	}
	field, err := forms.BooleanField("flag", forms.WithNullable(), forms.WithValidators(forms.FieldValidatorFunc(func(value forms.Value) validation.Errors {
		if value.IsNull() {
			return validation.NewErrors(validation.New("flag", "required"))
		}
		return validation.NewErrors()
	})))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := spec.Bind(forms.NewData(nil), nil)
	if err != nil || bound.Valid() {
		t.Fatal("nullable cleaning bypassed application validator")
	}
	bound, err = spec.Bind(forms.NewData(map[string][]string{"flag": {"false"}}), nil)
	if err != nil || !bound.Valid() {
		t.Fatal("validator treated false as unknown")
	}
	if _, err := forms.BooleanField("flag", forms.WithWidget(forms.NullBooleanSelect)); err == nil {
		t.Fatal("nonnullable field accepted unknown widget state")
	}
}
