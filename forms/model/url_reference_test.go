package model_test

import (
	"reflect"
	"testing"

	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/internal/urltest"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/validation"
)

func TestURLFormsAgainstPinnedDjango(t *testing.T) {
	reference, inputs := urltest.Load(t, "sqlite")
	for _, profile := range []string{"form", "form_optional", "form_http", "model", "model_optional", "model_nullable", "model_short"} {
		observation := reference.Forms[profile]
		t.Run(profile, func(t *testing.T) {
			var spec forms.Spec
			var err error
			if profile == "form" || profile == "form_optional" || profile == "form_http" {
				field, createErr := forms.URLField("address", forms.WithRequired(observation.Required), forms.WithAssumeScheme(observation.Scheme))
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
					options = append(options, schema.MaxLength(24))
				}
				definition, buildErr := schema.Build(schema.Definition{AppLabel: "url_reference", Models: []schema.Model{{Name: "contact", GoName: "Contact", Fields: []schema.Field{schema.URLField("address", "Address", options...)}}}})
				if buildErr != nil {
					t.Fatal(buildErr)
				}
				spec, err = formmodel.NewSpec(definition.Models[0], formmodel.OverrideField("address", formmodel.WithRequired(observation.Required)))
			}
			if err != nil {
				t.Fatal(err)
			}
			field := spec.Fields()[0]
			if field.Kind() != forms.FieldURL || field.Widget() != forms.URLInput || observation.Widget != "URLInput" || field.MaxLength() != observation.Maximum {
				t.Fatal("url projection lost its kind, widget or declared length")
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
							t.Fatal("url diagnostic used a fixed field name")
						}
						codes = append(codes, string(violation.Code()))
					}
					if !reflect.DeepEqual(codes, wanted.Codes) {
						t.Fatalf("codes %v want %v", codes, wanted.Codes)
					}
					value, exists := bound.Cleaned().Get("address")
					if len(codes) > 0 {
						if exists || bound.Valid() {
							t.Fatal("invalid url published cleaned data")
						}
						return
					}
					if !exists || !bound.Valid() {
						t.Fatal("valid url lost cleaned data")
					}
					if wanted.Value == nil {
						if !value.IsNull() {
							t.Fatal("nullable empty url became a string")
						}
						return
					}
					if got, ok := value.AsString(); !ok || got != *wanted.Value {
						t.Fatal("url trimming or original case changed")
					}
				})
			}
		})
	}
}

func TestURLModelCandidatesAndChoicesAgainstPinnedDjango(t *testing.T) {
	reference, _ := urltest.Load(t, "sqlite")
	metadata := blankModel(t, schema.URLField("address", "Address", schema.Nullable(), schema.Blank()))
	for name, submitted := range map[string]*string{"bare": new("new.example.com/path"), "relative": new("//new.example.com"), "invalid": new("javascript://example.com"), "empty": new(""), "omitted": nil} {
		t.Run(name, func(t *testing.T) {
			data := map[string][]string{}
			if submitted != nil {
				data["address"] = []string{*submitted}
			}
			initial := map[string]forms.Value{"address": forms.String("https://old.example.com")}
			bound, err := (formmodel.Definition{}).Bind(t.Context(), metadata, forms.NewData(data), initial)
			if err != nil {
				t.Fatal(err)
			}
			assertURLCandidate(t, bound, reference.ModelForms[name], true)
			if value, _ := initial["address"].AsString(); value != "https://old.example.com" {
				t.Fatal("bind mutated the original model state")
			}
		})
	}
	chosen := blankModel(t, schema.URLField("address", "Address", schema.Choices(schema.Choice("legacy", "Legacy"), schema.Choice("https://new.example.com", "Current"))))
	for raw, observation := range reference.ModelChoices {
		t.Run("choice/"+raw, func(t *testing.T) {
			bound, err := (formmodel.Definition{}).Bind(t.Context(), chosen, forms.NewData(map[string][]string{"address": {raw}}), nil)
			if err != nil {
				t.Fatal(err)
			}
			assertURLCandidate(t, bound, observation, false)
		})
	}
	// Replacing the input widget/type does not bypass the model's URL policy.
	char, err := forms.CharField("address")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{char})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	bound, err := formmodel.Bind(t.Context(), metadata, spec, forms.NewData(map[string][]string{"address": {"legacy"}}), nil, formmodel.PostClean{Validators: []formmodel.Validator{formmodel.ValidatorFunc(func(candidate forms.Values) validation.Errors {
		calls++
		value, _ := candidate.Get("address")
		if got, _ := value.AsString(); got != "legacy" {
			t.Fatal("model clean lost the invalid candidate")
		}
		return validation.NewErrors()
	})}})
	if err != nil || calls != 1 || bound.Form().Valid() || bound.Form().Errors().ByField("address").Empty() {
		t.Fatal("model URL policy was bypassed", err)
	}
	if _, err := bound.Input(); err == nil {
		t.Fatal("invalid candidate became persistable input")
	}
}

func assertURLCandidate(t *testing.T, bound formmodel.BoundForm, observation urltest.ModelForm, checkCleaned bool) {
	t.Helper()
	codes := []string{}
	for _, failure := range bound.Form().Errors().ByField("address").All() {
		codes = append(codes, string(failure.Code()))
	}
	if bound.Form().Valid() != observation.Valid || !reflect.DeepEqual(codes, observation.Errors) {
		t.Fatalf("model URL errors differ from native: %v want %v", codes, observation.Errors)
	}
	assertValue := func(value forms.Value, present bool, expected *string) {
		t.Helper()
		if expected == nil {
			if present && !value.IsNull() {
				t.Fatal("null URL candidate became text")
			}
		} else if text, ok := value.AsString(); !present || !ok || text != *expected {
			t.Fatal("URL model candidate differs from native")
		}
	}
	value, present := bound.Candidate().Get("address")
	assertValue(value, present, observation.Candidate)
	if checkCleaned {
		value, present = bound.Form().Cleaned().Get("address")
		assertValue(value, present, observation.Cleaned)
	}
}
