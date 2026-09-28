package model_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/validation"
)

func TestModelCleanSeparatesCandidateInputAndExcludedValidation(t *testing.T) {
	metadata := blankModel(t, schema.CharField("code", "Code", 12, schema.Unique()), schema.EmailField("email", "Email"), schema.IntegerField("counter", "Counter", schema.Default(int64(1))), schema.CharField("hidden", "Hidden", 24, schema.Default("initial"), schema.Unique()))
	for _, invalid := range []bool{false, true} {
		name := "valid"
		if invalid {
			name = "field_failure"
		}
		t.Run(name, func(t *testing.T) {
			raw := map[string][]string{"code": {"mixed"}, "email": {"a@example.com"}, "counter": {"2"}, "hidden": {"forged"}}
			if invalid {
				raw["counter"] = []string{"bad"}
			}
			calls := 0
			output := map[string]forms.Value{"code": forms.String("MIXED"), "email": forms.String("invalid-after-clean"), "counter": forms.Integer(7), "hidden": forms.String("server-change")}
			definition := formmodel.Definition{Fields: []string{"code", "email", "counter"}, PostClean: formmodel.PostClean{
				Fields: []string{"code", "email", "counter", "hidden"},
				Clean: func(candidate forms.Values) (forms.Values, validation.Errors) {
					calls++
					counter, _ := candidate.Integer("counter")
					want := int64(2)
					if invalid {
						want = 1
					}
					hidden, _ := candidate.String("hidden")
					if counter != want || hidden != "initial" {
						t.Fatal("clean did not receive default or excluded initial candidate")
					}
					return forms.NewValues(output), validation.Errors{}
				},
				Validators: []formmodel.Validator{formmodel.ValidatorFunc(func(candidate forms.Values) validation.Errors {
					if code, _ := candidate.String("code"); code != "MIXED" {
						t.Fatal("validator ran before model clean")
					}
					return validation.Errors{}
				})},
			}}
			bound, err := definition.Bind(metadata, forms.NewData(raw), nil)
			if err != nil || calls != 1 {
				t.Fatal("bind", err, calls)
			}
			output["hidden"] = forms.String("mutated-output")
			raw["code"][0] = "mutated-submission"
			if got, _ := bound.Form().Cleaned().String("code"); got != "mixed" {
				t.Fatal("clean rewrote form cleaned data")
			}
			if _, found := bound.Form().Cleaned().Get("hidden"); found {
				t.Fatal("clean published excluded form field")
			}
			if value, _ := bound.Form().Submitted().Get("code"); !reflect.DeepEqual(value, []string{"mixed"}) {
				t.Fatal("original submission was rewritten")
			}
			if hidden, _ := bound.Candidate().String("hidden"); hidden != "server-change" {
				t.Fatal("candidate aliases output")
			}
			var seen []map[string]query.Value
			check := func(_ context.Context, values map[string]query.Value) (validation.Errors, error) {
				seen = append(seen, values)
				return validation.Errors{}, nil
			}
			failures, err := bound.CheckDatabase(t.Context(), formmodel.DatabaseChecks{UniqueFields: check, Constraints: check})
			if err != nil || !failures.Empty() || len(seen) != 2 {
				t.Fatal("database phases", err)
			}
			for _, values := range seen {
				if code, ok := values["code"].String(); !ok || code != "MIXED" {
					t.Fatal("DB checks missed model clean change")
				}
				if _, found := values["hidden"]; found {
					t.Fatal("clean changed validation exclusion")
				}
				_, found := values["counter"]
				if found == invalid {
					t.Fatal("field repair erased earlier failure exclusion")
				}
			}
			input, err := bound.Input()
			if invalid {
				if err == nil || bound.Form().Errors().ByField("counter").Empty() {
					t.Fatal("clean repaired a field error implicitly")
				}
			} else {
				if err != nil || !bound.Form().Valid() {
					t.Fatal("field validators were rerun after clean", err)
				}
				if hidden, _ := input.String("hidden"); hidden != "server-change" {
					t.Fatal("explicit excluded change lost before persistence")
				}
				if email, _ := input.String("email"); email != "invalid-after-clean" {
					t.Fatal("changed email lost before persistence")
				}
			}
		})
	}
}

func TestModelCleanRetainsChangesWithErrorsAndUnchangedFieldsStayPrivate(t *testing.T) {
	metadata := blankModel(t, schema.CharField("code", "Code", 12), schema.CharField("hidden", "Hidden", 24, schema.Default("initial")))
	for _, mode := range []string{"no_change", "field_error", "nonfield_error"} {
		t.Run(mode, func(t *testing.T) {
			definition := formmodel.Definition{Fields: []string{"code"}, PostClean: formmodel.PostClean{Fields: []string{"code", "hidden"}, Clean: func(forms.Values) (forms.Values, validation.Errors) {
				if mode == "no_change" {
					return forms.Values{}, validation.Errors{}
				}
				field := validation.NonField
				if mode == "field_error" {
					field = "code"
				}
				return forms.NewValues(map[string]forms.Value{"code": forms.String("changed"), "hidden": forms.String("hidden-change")}), validation.NewErrors(validation.New(field, "policy"))
			}}}
			bound, err := definition.Bind(metadata, forms.NewData(map[string][]string{"code": {"original"}}), nil)
			if err != nil {
				t.Fatal(err)
			}
			input, err := bound.Input()
			if mode == "no_change" {
				if err != nil {
					t.Fatal(err)
				}
				if _, found := input.Get("hidden"); found {
					t.Fatal("unchanged excluded value became writable")
				}
			} else {
				if err == nil {
					t.Fatal("invalid candidate allowed persistence")
				}
				if got, _ := bound.Candidate().String("hidden"); got != "hidden-change" {
					t.Fatal("model error discarded candidate change")
				}
			}
		})
	}
}

func TestModelCleanRejectsInvalidOwnershipBeforeCallbacks(t *testing.T) {
	metadata := blankModel(t, schema.CharField("code", "Code", 12), schema.IntegerField("counter", "Counter"))
	for _, mode := range []string{"nil_clean", "primary_key", "unknown", "duplicate", "nil_validator"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			hook := formmodel.PostClean{Fields: []string{"code"}, Clean: func(forms.Values) (forms.Values, validation.Errors) {
				calls++
				return forms.Values{}, validation.Errors{}
			}}
			switch mode {
			case "nil_clean":
				hook.Clean = nil
			case "primary_key":
				hook.Fields = []string{"id"}
			case "unknown":
				hook.Fields = []string{"private-secret"}
			case "duplicate":
				hook.Fields = []string{"code", "code"}
			case "nil_validator":
				var nilCheck formmodel.ValidatorFunc
				hook.Validators = []formmodel.Validator{nilCheck}
			}
			definition := formmodel.Definition{PostClean: hook}
			if _, err := definition.Bind(metadata, forms.NewData(nil), nil); err == nil || calls != 0 {
				t.Fatal("invalid ownership reached callback", err, calls)
			}
		})
	}
	for _, mode := range []string{"undeclared", "wrong_type", "unknown", "primary_key", "command_input"} {
		t.Run(mode, func(t *testing.T) {
			changes := map[string]forms.Value{"code": forms.String("secret-value")}
			switch mode {
			case "undeclared":
				changes["counter"] = forms.Integer(1)
			case "wrong_type":
				changes["code"] = forms.Integer(1)
			case "unknown":
				changes["hidden"] = forms.String("secret-value")
			case "primary_key":
				changes["id"] = forms.Integer(1)
			case "command_input":
				changes["confirmation"] = forms.String("secret-value")
			}
			after := 0
			definition := formmodel.Definition{PostClean: formmodel.PostClean{Fields: []string{"code"}, Clean: func(forms.Values) (forms.Values, validation.Errors) {
				return forms.NewValues(changes), validation.Errors{}
			}, Validators: []formmodel.Validator{formmodel.ValidatorFunc(func(forms.Values) validation.Errors { after++; return validation.Errors{} })}}}
			bound, err := definition.Bind(metadata, forms.NewData(map[string][]string{"code": {"ok"}, "counter": {"1"}}), nil)
			if err == nil || bound.Form().Bound() || after != 0 || strings.Contains(err.Error(), "secret-value") {
				t.Fatal("invalid clean result published partial state", err)
			}
		})
	}
}

func TestModelCleanDefinitionClonesAndConcurrentBindsOwnChanges(t *testing.T) {
	metadata := blankModel(t, schema.CharField("code", "Code", 12), schema.CharField("hidden", "Hidden", 24, schema.Default("initial")))
	definition := formmodel.Definition{Fields: []string{"code"}, PostClean: formmodel.PostClean{Fields: []string{"code", "hidden"}, Clean: func(values forms.Values) (forms.Values, validation.Errors) {
		code, _ := values.String("code")
		return forms.NewValues(map[string]forms.Value{"code": forms.String(strings.ToUpper(code)), "hidden": forms.String(code)}), validation.Errors{}
	}, Validators: []formmodel.Validator{formmodel.ValidatorFunc(func(forms.Values) validation.Errors { return validation.Errors{} })}}}
	clone := definition.Clone()
	clone.PostClean.Fields[0] = "id"
	clone.PostClean.Validators[0] = nil
	for _, value := range []string{"alpha", "beta", "gamma", "delta"} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			bound, err := definition.Bind(metadata, forms.NewData(map[string][]string{"code": {value}}), nil)
			if err != nil {
				t.Fatal(err)
			}
			input, err := bound.Input()
			if err != nil {
				t.Fatal(err)
			}
			if got, _ := input.String("hidden"); got != value {
				t.Fatal("concurrent model candidate aliased")
			}
		})
	}
}
