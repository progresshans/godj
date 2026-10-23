package model_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/internal/slugtest"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/validation"
)

func TestSlugFormsAgainstPinnedDjango(t *testing.T) {
	reference, inputs := slugtest.Load(t, "sqlite")
	for _, profile := range []string{"form", "form_unicode", "form_optional", "form_untrimmed", "model", "model_unicode", "model_optional", "model_nullable", "model_short"} {
		observation := reference.Forms[profile]
		t.Run(profile, func(t *testing.T) {
			var spec forms.Spec
			var err error
			if strings.HasPrefix(profile, "form") {
				field, createErr := forms.SlugField("address", forms.WithRequired(observation.Required), forms.WithAllowUnicode(observation.Unicode), forms.WithTrimWhitespace(observation.Strip))
				if createErr != nil {
					t.Fatal(createErr)
				}
				spec, err = forms.NewSpec([]forms.Field{field})
			} else {
				options := []schema.FieldOption{schema.AllowUnicode(observation.Unicode)}
				if profile == "model_nullable" {
					options = append(options, schema.Nullable())
				}
				if profile == "model_short" {
					options = append(options, schema.MaxLength(12))
				}
				definition, buildErr := schema.Build(schema.Definition{AppLabel: "slug_reference", Models: []schema.Model{{Name: "contact", GoName: "Contact", Fields: []schema.Field{schema.SlugField("address", "Address", options...)}}}})
				if buildErr != nil {
					t.Fatal(buildErr)
				}
				spec, err = formmodel.NewSpec(definition.Models[0], formmodel.OverrideField("address", formmodel.WithRequired(observation.Required)))
			}
			if err != nil {
				t.Fatal(err)
			}
			field := spec.Fields()[0]
			if field.Kind() != forms.FieldSlug || field.Widget() != forms.TextInput || observation.Widget != "TextInput" || field.MaxLength() != observation.Maximum || field.AllowUnicode() != observation.Unicode || field.TrimWhitespace() != observation.Strip {
				t.Fatal("slug projection lost its kind, widget or declared length")
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
							t.Fatal("slug diagnostic used a fixed field name")
						}
						codes = append(codes, string(violation.Code()))
					}
					if !reflect.DeepEqual(codes, wanted.Codes) {
						t.Fatalf("codes %v want %v", codes, wanted.Codes)
					}
					value, exists := bound.Cleaned().Get("address")
					if len(codes) > 0 {
						if exists || bound.Valid() {
							t.Fatal("invalid slug published cleaned data")
						}
						return
					}
					if !exists || !bound.Valid() {
						t.Fatal("valid slug lost cleaned data")
					}
					if wanted.Value == nil {
						if !value.IsNull() {
							t.Fatal("nullable empty slug became a string")
						}
						return
					}
					if got, ok := value.AsString(); !ok || got != *wanted.Value {
						t.Fatal("slug trimming or original case changed")
					}
				})
			}
		})
	}
}

func TestSlugModelCandidatesAndChoicesAgainstPinnedDjango(t *testing.T) {
	reference, _ := slugtest.Load(t, "sqlite")
	submissions := map[string]*string{"valid": new("  New_Name-2  "), "unicode": new("  읽기-쉬운_주소  "), "invalid": new("bad/value"), "empty": new(""), "omitted": nil}
	for _, profile := range []string{"ascii", "unicode"} {
		metadata := blankModel(t, schema.SlugField("address", "Address", schema.Nullable(), schema.Blank(), schema.AllowUnicode(profile == "unicode")))
		for name, submitted := range submissions {
			t.Run(profile+"/"+name, func(t *testing.T) {
				data := map[string][]string{}
				if submitted != nil {
					data["address"] = []string{*submitted}
				}
				initial := map[string]forms.Value{"address": forms.String("old")}
				bound, err := (formmodel.Definition{}).Bind(t.Context(), metadata, forms.NewData(data), initial)
				if err != nil {
					t.Fatal(err)
				}
				assertSlugCandidate(t, bound, reference.ModelForms[profile][name], true)
				if value, _ := initial["address"].AsString(); value != "old" {
					t.Fatal("bind mutated initial model state")
				}
			})
		}
	}
	chosen := blankModel(t, schema.SlugField("address", "Address", schema.Choices(schema.Choice("Old Slug!", "Legacy"), schema.Choice("current-name", "Current"))))
	for raw, observation := range reference.ModelChoices {
		t.Run("choice/"+raw, func(t *testing.T) {
			bound, err := (formmodel.Definition{}).Bind(t.Context(), chosen, forms.NewData(map[string][]string{"address": {raw}}), nil)
			if err != nil {
				t.Fatal(err)
			}
			assertSlugCandidate(t, bound, observation, false)
		})
	}
	metadata := blankModel(t, schema.SlugField("address", "Address", schema.Nullable(), schema.Blank()))
	char, err := forms.CharField("address")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{char})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	bound, err := formmodel.Bind(t.Context(), metadata, spec, forms.NewData(map[string][]string{"address": {"bad/value"}}), nil, formmodel.PostClean{Validators: []formmodel.Validator{formmodel.ValidatorFunc(func(candidate forms.Values) validation.Errors {
		calls++
		value, _ := candidate.Get("address")
		if got, _ := value.AsString(); got != "bad/value" {
			t.Fatal("model clean lost the submitted candidate")
		}
		return validation.NewErrors()
	})}})
	if err != nil || calls != 1 || bound.Form().Valid() || bound.Form().Errors().ByField("address").Empty() {
		t.Fatal("custom Char form bypassed model slug policy", err)
	}
	if _, err := bound.Input(); err == nil {
		t.Fatal("invalid slug became persistable input")
	}
}

func assertSlugCandidate(t *testing.T, bound formmodel.BoundForm, observation slugtest.ModelForm, checkCleaned bool) {
	t.Helper()
	codes := []string{}
	for _, failure := range bound.Form().Errors().ByField("address").All() {
		codes = append(codes, string(failure.Code()))
	}
	if bound.Form().Valid() != observation.Valid || !reflect.DeepEqual(codes, observation.Errors) {
		t.Fatalf("model Slug errors differ from native: %v want %v", codes, observation.Errors)
	}
	assertValue := func(value forms.Value, present bool, expected *string) {
		t.Helper()
		if expected == nil {
			if present && !value.IsNull() {
				t.Fatal("null Slug candidate became text")
			}
		} else if text, ok := value.AsString(); !present || !ok || text != *expected {
			t.Fatal("Slug model candidate differs from native")
		}
	}
	value, present := bound.Candidate().Get("address")
	assertValue(value, present, observation.Candidate)
	if checkCleaned {
		value, present = bound.Form().Cleaned().Get("address")
		assertValue(value, present, observation.Cleaned)
	}
}
