package model_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

func blankModel(t *testing.T, fields ...schema.Field) ir.Model {
	t.Helper()
	definition, err := schema.Build(schema.Definition{AppLabel: "postclean", Models: []schema.Model{{Name: "contact", GoName: "Contact", Fields: fields}}})
	if err != nil {
		t.Fatal(err)
	}
	return definition.Models[0]
}

func TestModelBindSeparatesBlankNullAndInputOverride(t *testing.T) {
	for _, nullable := range []bool{false, true} {
		for _, blank := range []bool{false, true} {
			for _, optional := range []bool{false, true} {
				t.Run(fmt.Sprintf("null_%t/blank_%t/optional_%t", nullable, blank, optional), func(t *testing.T) {
					var options []schema.FieldOption
					if nullable {
						options = append(options, schema.Nullable())
					}
					if blank {
						options = append(options, schema.Blank())
					}
					metadata := blankModel(t, schema.IntegerField("counter", "Counter", options...))
					definition := formmodel.Definition{}
					if optional {
						definition.Overrides = []formmodel.Override{formmodel.OverrideField("counter", formmodel.WithRequired(false))}
					}
					bound, err := definition.Bind(t.Context(), metadata, forms.NewData(map[string][]string{"counter": {""}}), nil)
					if err != nil {
						t.Fatal(err)
					}
					want := ""
					if !blank && !optional {
						want = "required"
					}
					errors := bound.Form().Errors().All()
					if want != "" {
						if len(errors) != 1 || string(errors[0].Code()) != want || bound.Form().Valid() {
							t.Fatalf("errors %v, want %s", errors, want)
						}
						if _, err := bound.Input(); err == nil {
							t.Fatal("invalid model became writable input")
						}
					} else if !bound.Form().Valid() || len(errors) != 0 {
						t.Fatal("unexpected model error", errors)
					}
					values, err := bound.ValidationValues()
					if err != nil {
						t.Fatal(err)
					}
					_, selected := values["counter"]
					if selected != blank {
						t.Fatal("model validation exclusion ignored field errors or the optional override")
					}
				})
			}
		}
	}
}

func TestModelBindPreservesOmittedDefaultsWithoutChangingCleanedData(t *testing.T) {
	metadata := blankModel(t,
		schema.CharField("title", "Title", 40, schema.Blank(), schema.Default("default")),
		schema.IntegerField("counter", "Counter", schema.Blank(), schema.Default(int64(7))),
		schema.BooleanField("enabled", "Enabled", schema.Default(true)),
	)
	for _, existing := range []bool{false, true} {
		for _, explicit := range []bool{false, true} {
			t.Run(fmt.Sprintf("existing_%t/explicit_%t", existing, explicit), func(t *testing.T) {
				var initial map[string]forms.Value
				if existing {
					initial = map[string]forms.Value{"title": forms.String("current"), "counter": forms.Integer(11), "enabled": forms.Boolean(true)}
				}
				raw := map[string][]string{}
				if explicit {
					raw["title"] = []string{""}
					raw["counter"] = []string{""}
				}
				bound, err := (formmodel.Definition{}).Bind(t.Context(), metadata, forms.NewData(raw), initial)
				if err != nil {
					t.Fatal(err)
				}
				if text, ok := bound.Form().Cleaned().String("title"); !ok || text != "" {
					t.Fatal("model default entered form cleaned data")
				}
				if enabled, ok := bound.Candidate().Boolean("enabled"); !ok || enabled {
					t.Fatal("omitted checkbox retained true default")
				}
				if explicit {
					counter, present := bound.Candidate().Get("counter")
					if !bound.Form().Valid() || !present || !counter.IsNull() {
						t.Fatal("explicit empty integer did not follow blank field validation")
					}
					if title, _ := bound.Candidate().String("title"); title != "" {
						t.Fatal("explicit empty text kept its default")
					}
					return
				}
				input, err := bound.Input()
				if err != nil {
					t.Fatal(err)
				}
				wantTitle, wantCounter := "default", int64(7)
				if existing {
					wantTitle, wantCounter = "current", 11
				}
				if title, _ := input.String("title"); title != wantTitle {
					t.Fatal("omitted title lost current/default")
				}
				if number, _ := input.Integer("counter"); number != wantCounter {
					t.Fatal("omitted integer lost current/default")
				}
				if _, present := bound.Form().Submitted().Get("title"); present {
					t.Fatal("omission was converted to explicit empty input")
				}
			})
		}
	}
}

func TestModelBindRevalidatesEmailAndChoicesAndRunsCleanAfterErrors(t *testing.T) {
	metadata := blankModel(t, schema.CharField("key", "Key", 8), schema.EmailField("address", "Address", schema.Blank()), schema.IntegerField("counter", "Counter", schema.Default(int64(1))))
	for i := range metadata.Fields {
		if metadata.Fields[i].Name == "address" {
			metadata.Fields[i].Choices = []ir.Choice{{Value: ir.Scalar{Kind: ir.ScalarString, String: "legacy"}, Label: "Legacy"}, {Value: ir.Scalar{Kind: ir.ScalarString, String: "a@example.com"}, Label: "A"}}
		}
	}
	for _, override := range []bool{false, true} {
		for _, address := range []string{"legacy", "other@example.com", "a@example.com", ""} {
			t.Run(fmt.Sprintf("override_%t/%s", override, address), func(t *testing.T) {
				spec, err := formmodel.NewSpec(metadata)
				if err != nil {
					t.Fatal(err)
				}
				if override {
					fields := spec.Fields()
					for i, field := range fields {
						if field.Name() == "address" {
							fields[i], err = forms.CharField("address", forms.WithRequired(false))
							if err != nil {
								t.Fatal(err)
							}
						}
					}
					spec, err = forms.NewSpec(fields)
					if err != nil {
						t.Fatal(err)
					}
				}
				calls := 0
				validator := formmodel.ValidatorFunc(func(candidate forms.Values) validation.Errors {
					calls++
					if counter, ok := candidate.Integer("counter"); !ok || counter != 1 {
						t.Fatal("failed form field replaced model default")
					}
					return validation.NewErrors(validation.New(validation.NonField, "semantic"))
				})
				bound, err := formmodel.Bind(t.Context(), metadata, spec, forms.NewData(map[string][]string{"key": {"new"}, "address": {address}, "counter": {"bad"}}), nil, formmodel.PostClean{Validators: []formmodel.Validator{validator}})
				if err != nil || calls != 1 {
					t.Fatal("model clean did not run after field failure", err)
				}
				codes := map[validation.Field][]validation.Code{}
				for _, violation := range bound.Form().Errors().All() {
					codes[violation.Field()] = append(codes[violation.Field()], violation.Code())
				}
				want := map[validation.Field][]validation.Code{"counter": {"invalid"}, validation.NonField: {"semantic"}}
				if address == "legacy" {
					want["address"] = []validation.Code{"invalid"}
				}
				if address == "other@example.com" {
					want["address"] = []validation.Code{"invalid_choice"}
				}
				if !reflect.DeepEqual(codes, want) {
					t.Fatalf("codes %v want %v", codes, want)
				}
				values, err := bound.ValidationValues()
				if err != nil {
					t.Fatal(err)
				}
				if _, present := values["key"]; !present {
					t.Fatal("unrelated field error prevented valid unique candidate")
				}
				if _, present := values["counter"]; present {
					t.Fatal("failed model field entered database validation")
				}
			})
		}
	}
}

func TestModelBindSnapshotsCandidateAndRecomputesExclusions(t *testing.T) {
	metadata := blankModel(t, schema.CharField("key", "Key", 20), schema.EmailField("address", "Address", schema.Blank()))
	definition := formmodel.Definition{Fields: []string{"key"}}
	initial := map[string]forms.Value{"address": forms.String("legacy-malformed")}
	bound, err := definition.Bind(t.Context(), metadata, forms.NewData(map[string][]string{"key": {"private"}}), initial)
	if err != nil || !bound.Form().Valid() {
		t.Fatal(err)
	}
	initial["address"] = forms.String("changed")
	metadata.Fields = nil
	before, err := bound.ValidationValues()
	if err != nil {
		t.Fatal(err)
	}
	rejected, err := bound.WithErrors(validation.NewErrors(validation.New("key", validation.CodeUnique)))
	if err != nil {
		t.Fatal(err)
	}
	after, err := rejected.ValidationValues()
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 1 || len(after) != 0 || !bound.Form().Valid() || rejected.Form().Valid() {
		t.Fatal("validation snapshots alias or exclusions are stale")
	}
	if address, _ := rejected.Candidate().String("address"); address != "legacy-malformed" {
		t.Fatal("candidate aliases or revalidates excluded legacy data")
	}
	input, err := bound.Input()
	if err != nil {
		t.Fatal(err)
	}
	if len(input.All()) != 1 {
		t.Fatal("excluded field entered persistence input")
	}
	for _, value := range []any{bound, rejected, bound.Candidate(), bound.Form().Submitted()} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%w"} {
			if text := fmt.Sprintf(format, value); strings.Contains(text, "private") || strings.Contains(text, "legacy-malformed") {
				t.Fatal("candidate leaked through formatting")
			}
		}
	}
}

func TestModelBindRejectsBadConfigurationBeforeCallbacks(t *testing.T) {
	metadata := blankModel(t, schema.IntegerField("counter", "Counter"), schema.EmailField("address", "Address"))
	calls := 0
	field, err := forms.CharField("counter", forms.WithValidators(forms.FieldValidatorFunc(func(forms.Value) validation.Errors { calls++; return validation.Errors{} })))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := formmodel.Bind(t.Context(), metadata, spec, forms.NewData(map[string][]string{"counter": {"x"}}), nil, formmodel.PostClean{}); err == nil || calls != 0 {
		t.Fatal("incompatible model input invoked validator")
	}
	proper, err := formmodel.NewSpecForFields(metadata, []string{"counter"}, formmodel.OverrideField("counter", formmodel.WithValidators(forms.FieldValidatorFunc(func(forms.Value) validation.Errors { calls++; return validation.Errors{} }))))
	if err != nil {
		t.Fatal(err)
	}
	var nilValidator formmodel.ValidatorFunc
	if _, err := formmodel.Bind(t.Context(), metadata, proper, forms.NewData(map[string][]string{"counter": {"1"}}), nil, formmodel.PostClean{Validators: []formmodel.Validator{nilValidator}}); err == nil || calls != 0 {
		t.Fatal("typed nil model validator reached callbacks")
	}
	for _, initial := range []map[string]forms.Value{{"address": forms.Integer(3)}, {"unknown": forms.String("x")}, {"address": forms.String("\xff")}} {
		if _, err := formmodel.Bind(t.Context(), metadata, proper, forms.NewData(map[string][]string{"counter": {"1"}}), initial, formmodel.PostClean{}); err == nil || calls != 0 {
			t.Fatal("invalid initial snapshot reached callbacks")
		}
	}
	if _, err := (formmodel.BoundForm{}).Input(); err == nil {
		t.Fatal("zero model form accepted")
	}
	if _, err := (formmodel.BoundForm{}).ValidationValues(); err == nil {
		t.Fatal("zero model form yielded validation input")
	}
}

func TestModelBindCollectionOmissionClearsInitialAndBlankOwnsRequired(t *testing.T) {
	definition, err := schema.Build(schema.Definition{AppLabel: "posts", Models: []schema.Model{{Name: "post", GoName: "Post", Fields: []schema.Field{schema.CharField("title", "Title", 40)}, ManyToMany: []schema.ManyToManyField{schema.ManyToMany("labels", "Labels", schema.Target("labels", "label"), schema.RelatedName("posts"))}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, blank := range []bool{false, true} {
		t.Run(fmt.Sprint(blank), func(t *testing.T) {
			metadata := definition.Models[0].Clone()
			metadata.ManyToMany[0].Blank = blank
			spec, err := formmodel.NewSpecForFields(metadata, []string{"labels"})
			if err != nil {
				t.Fatal(err)
			}
			spec, err = spec.WithModelChoices("labels", forms.Choice{Value: forms.Integer(7), Label: "Visible"})
			if err != nil {
				t.Fatal(err)
			}
			if spec.Fields()[0].Required() == blank {
				t.Fatal("collection input ignored Blank policy")
			}
			bound, err := formmodel.Bind(t.Context(), metadata, spec, forms.NewData(nil), map[string]forms.Value{"labels": forms.Integers(7)}, formmodel.PostClean{})
			if err != nil || bound.Form().Valid() != blank {
				t.Fatal("collection required policy changed", err)
			}
			if blank {
				input, err := bound.Input()
				if err != nil {
					t.Fatal(err)
				}
				value, ok := input.Get("labels")
				if !ok {
					t.Fatal("collection disappeared")
				}
				keys, ok := value.AsIntegers()
				if !ok || len(keys) != 0 {
					t.Fatal("omitted select-multiple retained old membership")
				}
			}
			values, err := bound.ValidationValues()
			if err != nil || len(values) != 0 {
				t.Fatal("collection became a scalar DB uniqueness value", err)
			}
		})
	}
}
