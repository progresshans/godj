package model_test

import (
	"reflect"
	"testing"

	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/internal/emailtest"
	"github.com/progresshans/godj/schema"
)

func TestEmailFormsAgainstPinnedDjango(t *testing.T) {
	reference, inputs := emailtest.Load(t, "sqlite")
	for _, profile := range []string{"form", "form_optional", "model", "model_optional", "model_nullable", "model_short"} {
		observation := reference.Forms[profile]
		t.Run(profile, func(t *testing.T) {
			var spec forms.Spec
			var err error
			if profile == "form" || profile == "form_optional" {
				field, createErr := forms.EmailField("address", forms.WithRequired(observation.Required))
				if createErr != nil {
					t.Fatal(createErr)
				}
				spec, err = forms.NewSpec([]forms.Field{field})
			} else {
				options := []schema.FieldOption{}
				if profile == "model_nullable" {
					options = append(options, schema.Nullable())
				}
				if profile == "model_short" {
					options = append(options, schema.MaxLength(12))
				}
				definition, buildErr := schema.Build(schema.Definition{AppLabel: "email_reference", Models: []schema.Model{{Name: "contact", GoName: "Contact", Fields: []schema.Field{schema.EmailField("address", "Address", options...)}}}})
				if buildErr != nil {
					t.Fatal(buildErr)
				}
				spec, err = formmodel.NewSpec(definition.Models[0], formmodel.OverrideField("address", formmodel.WithRequired(observation.Required)))
			}
			if err != nil {
				t.Fatal(err)
			}
			field := spec.Fields()[0]
			if field.Kind() != forms.FieldEmail || field.Widget() != forms.EmailInput || observation.Widget != "EmailInput" || field.MaxLength() != observation.Maximum {
				t.Fatal("email projection lost its kind, widget or declared length")
			}
			for _, input := range inputs {
				t.Run(input.Name, func(t *testing.T) {
					wanted, present := observation.Cases[input.Name]
					if !present {
						t.Fatal("native input observation is missing")
					}
					data := map[string][]string{}
					if input.Value != nil {
						data["address"] = []string{*input.Value}
					}
					bound, err := spec.Bind(t.Context(), forms.NewData(data), nil)
					if err != nil {
						t.Fatal(err)
					}
					codes := []string{}
					for _, violation := range bound.Errors().All() {
						if violation.Field() != "address" {
							t.Fatal("email diagnostic used a fixed field name")
						}
						codes = append(codes, string(violation.Code()))
					}
					if !reflect.DeepEqual(codes, wanted.Codes) {
						t.Fatalf("codes %v want %v", codes, wanted.Codes)
					}
					value, exists := bound.Cleaned().Get("address")
					if len(codes) > 0 {
						if exists || bound.Valid() {
							t.Fatal("invalid email published cleaned data")
						}
						return
					}
					if !exists || !bound.Valid() {
						t.Fatal("valid email lost cleaned data")
					}
					if wanted.Value == nil {
						if !value.IsNull() {
							t.Fatal("nullable empty email became a string")
						}
						return
					}
					if got, ok := value.AsString(); !ok || got != *wanted.Value {
						t.Fatal("email trimming or original case changed")
					}
				})
			}
		})
	}
}
