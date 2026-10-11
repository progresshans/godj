package model_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/binaryvalue"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/internal/binarytest"
)

func TestBinaryModelFormsAgainstPinnedDjango(t *testing.T) {
	reference, inputs := binarytest.Load(t, "sqlite"), binarytest.Inputs(t)
	for _, profile := range []string{"required", "short", "blank", "nullable", "nullable_required", "hidden", "default"} {
		t.Run(profile, func(t *testing.T) {
			model := binarytest.Model(t, profile)
			spec, err := formmodel.NewSpec(model)
			if err != nil {
				t.Fatal(err)
			}
			names := []string{}
			for _, field := range spec.Fields() {
				names = append(names, field.Name())
			}
			if !slices.Equal(names, reference.Profiles[profile].FormFields) {
				t.Fatal("non-editable fields entered automatic model form", names)
			}
			count := 0
			for _, input := range inputs {
				// A form submits strings. Non-string Python objects are exercised at
				// the JSON boundary; nil/omission represent missing text here.
				var text *string
				if input.Present && json.Unmarshal(input.Value, &text) != nil {
					continue
				}
				count++
				t.Run(input.Name, func(t *testing.T) {
					data := map[string][]string{"title": {"input"}, "internal_note": {"attacker"}}
					if input.Present {
						raw := ""
						if text != nil {
							raw = *text
						}
						data["payload"] = []string{raw}
					}
					initial := map[string]forms.Value{"payload": forms.Binary(binaryvalue.Value{Data: "keep"}), "internal_note": forms.String("server")}
					bound, err := (formmodel.Definition{}).Bind(t.Context(), model, forms.NewData(data), initial)
					if err != nil {
						t.Fatal(err)
					}
					wanted := reference.Profiles[profile].Cases[input.Name].ModelForm
					codes := []string{}
					for _, failure := range bound.Form().Errors().All() {
						if failure.Field() != "payload" {
							t.Fatal("unexpected field failure", failure.Field())
						}
						codes = append(codes, string(failure.Code()))
					}
					if bound.Form().Valid() != wanted.Valid || !slices.Equal(codes, wanted.Errors["payload"]) {
						t.Fatalf("valid=%v codes=%v want valid=%v codes=%v", bound.Form().Valid(), codes, wanted.Valid, wanted.Errors["payload"])
					}
					secret, _ := bound.Candidate().Get("internal_note")
					if raw, ok := secret.AsString(); !ok || raw != wanted.InternalNote {
						t.Fatal("non-editable scalar was overwritten")
					}
					if _, exists := bound.Form().Cleaned().Get("internal_note"); exists {
						t.Fatal("server field entered cleaned form")
					}
					if wanted.Valid {
						value, exists := bound.Candidate().Get("payload")
						got, ok := value.AsBinary()
						want := wanted.Candidate.Binary(t)
						if !exists || want == nil || !ok || got != *want {
							t.Fatal("binary candidate differs from native")
						}
					} else if _, err := bound.Input(); err == nil {
						t.Fatal("invalid base64 published persistable input")
					}
					if value, _ := initial["payload"].AsBinary(); value.Data != "keep" {
						t.Fatal("binding mutated caller-owned initial value")
					}
				})
			}
			if count != 32 {
				t.Fatal("text input coverage changed", count)
			}
		})
	}
}

func TestNonEditableModelFieldsCannotBeSelectedOrBound(t *testing.T) {
	model := binarytest.Model(t, "hidden")
	for _, name := range []string{"payload", "internal_note"} {
		if _, err := formmodel.NewSpecForFields(model, []string{"title", name}); err == nil {
			t.Fatal("explicit selection lifted non-editable policy", name)
		}
		if _, err := formmodel.NewSpec(model, formmodel.OverrideField(name, formmodel.WithRequired(false))); err == nil {
			t.Fatal("override lifted non-editable policy", name)
		}
		var field forms.Field
		var err error
		if name == "payload" {
			field, err = forms.BinaryField(name, forms.WithRequired(false))
		} else {
			field, err = forms.CharField(name)
		}
		if err != nil {
			t.Fatal(err)
		}
		spec, err := forms.NewSpec([]forms.Field{field})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := formmodel.Bind(t.Context(), model, spec, forms.NewData(map[string][]string{name: {"attacker"}}), nil, formmodel.PostClean{}); err == nil {
			t.Fatal("custom form bound a non-editable model field", name)
		}
	}
}

func TestServerOwnedInitialValuesValidateRepresentationWithoutGrantingInput(t *testing.T) {
	model := binarytest.Model(t, "hidden")
	for index := range model.Fields {
		if model.Fields[index].Name == "payload" {
			model.Fields[index].MaxLength = 4
		}
	}
	initial := map[string]forms.Value{
		"id":            forms.Integer(7),
		"payload":       forms.Binary(binaryvalue.Value{Data: "\x00\xfflegacy"}),
		"internal_note": forms.String("server"),
	}
	if err := formmodel.ValidateInitialValues(model, initial); err != nil {
		t.Fatal("existing server bytes were treated as editable input", err)
	}
	for _, bad := range []map[string]forms.Value{
		{"payload": forms.String("AP8=")},
		{"payload": forms.Binary(binaryvalue.Value{Data: strings.Repeat("x", binaryvalue.MaxBytes+1)})},
		{"internal_note": forms.Integer(1)},
		{"internal_note": forms.String("bad\x00text")},
		{"unknown": forms.String("value")},
	} {
		if err := formmodel.ValidateInitialValues(model, bad); err == nil {
			t.Fatal("malformed stored initial accepted")
		}
	}
	for _, name := range []string{"payload", "internal_note"} {
		if _, err := formmodel.NewSpecForFields(model, []string{name}); err == nil {
			t.Fatal("stored-value validation changed input policy", name)
		}
	}
}
