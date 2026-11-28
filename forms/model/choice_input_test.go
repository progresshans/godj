package model_test

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"

	helpdesk "github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

func scalarChoiceInputModel(t *testing.T) ir.Model {
	t.Helper()
	definition, err := schema.Build(schema.Definition{AppLabel: "choice_inputs", Models: []schema.Model{{Name: "draft", GoName: "Draft", Fields: []schema.Field{
		schema.IntegerField("reviewer", "Reviewer", schema.Nullable(), schema.Blank(), schema.Default(int64(2))),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	return definition.Models[0]
}

func TestChoiceInputSeparatesSubmittedModelDefaultsFromDisplayInitial(t *testing.T) {
	for _, item := range []struct {
		name      string
		raw       []string
		present   bool
		initial   map[string]forms.Value
		required  bool
		noDefault bool
		keys      []int64
		valid     bool
		display   []int64
	}{
		{name: "new_model_default", keys: []int64{2}, valid: true, display: []int64{99}},
		{name: "current_zero", initial: map[string]forms.Value{"reviewer": forms.Integer(0)}, keys: []int64{0}, valid: true, display: []int64{0}},
		{name: "current_negative", initial: map[string]forms.Value{"reviewer": forms.Integer(-4)}, keys: []int64{-4}, valid: true, display: []int64{-4}},
		{name: "current_null", initial: map[string]forms.Value{"reviewer": forms.Null()}, valid: true},
		{name: "explicit_empty", raw: []string{""}, present: true, initial: map[string]forms.Value{"reviewer": forms.Integer(7)}, valid: true, display: []int64{7}},
		{name: "present_empty_slice", raw: []string{}, present: true, valid: true, display: []int64{99}},
		{name: "required_omission", required: true, valid: true, display: []int64{99}},
		{name: "no_ir_default", initial: map[string]forms.Value{"reviewer": forms.Integer(7)}, noDefault: true, valid: true, display: []int64{7}},
		{name: "integer_alias", raw: []string{" +0_7 "}, present: true, keys: []int64{7}, valid: true, display: []int64{99}},
		{name: "unicode_alias", raw: []string{"٧"}, present: true, keys: []int64{7}, valid: true, display: []int64{99}},
		{name: "malformed", raw: []string{"private-invalid-key"}, present: true, display: []int64{99}},
		{name: "repeated", raw: []string{"7", "7"}, present: true, display: []int64{99}},
	} {
		t.Run(item.name, func(t *testing.T) {
			// An explicitly overridden scalar ModelChoice may retain an IR
			// integer default. Canonical ForeignKey defaults are unsupported.
			metadata := scalarChoiceInputModel(t)
			if item.noDefault {
				for index := range metadata.Fields {
					if metadata.Fields[index].Name == "reviewer" {
						metadata.Fields[index].Default = nil
					}
				}
			}
			fieldCalls, crossCalls := 0, 0
			field, err := forms.ModelChoiceField("reviewer", forms.WithRequired(item.required), forms.WithNullable(), forms.WithDefault(forms.Integer(99)),
				forms.WithValidators(forms.FieldValidatorFunc(func(forms.Value) validation.Errors { fieldCalls++; return validation.Errors{} })))
			if err != nil {
				t.Fatal(err)
			}
			spec, err := forms.NewSpec([]forms.Field{field}, forms.CrossValidatorFunc(func(forms.Values) validation.Errors { crossCalls++; return validation.Errors{} }))
			if err != nil {
				t.Fatal(err)
			}
			raw := map[string][]string{}
			if item.present {
				raw["reviewer"] = item.raw
			}
			data := forms.NewData(raw)
			input, err := formmodel.PrepareChoiceInput(metadata, spec, data, item.initial)
			if err != nil || !input.Initialized() {
				t.Fatal(err)
			}
			keys, valid, err := input.ChoiceKeys("reviewer")
			if err != nil || valid != item.valid || !slices.Equal(keys, item.keys) {
				t.Fatal("submitted resolution changed model default/current/raw semantics", keys, valid, err)
			}
			initial, valid, err := input.InitialChoiceKeys("reviewer")
			if err != nil || !valid || !slices.Equal(initial, item.display) || fieldCalls != 0 || crossCalls != 0 {
				t.Fatal("display initial acquired model candidate/raw input or ran validation", initial, err)
			}
			if item.valid {
				choices := make([]forms.Choice, len(keys))
				for index, key := range keys {
					choices[index] = forms.Choice{Value: forms.Integer(key), Label: "Currently resolved"}
				}
				boundSpec, err := spec.WithModelChoices("reviewer", choices...)
				if err != nil {
					t.Fatal(err)
				}
				bound, err := formmodel.Bind(t.Context(), metadata, boundSpec, data, item.initial, formmodel.PostClean{})
				if err != nil || bound.Form().Valid() != !item.required || crossCalls != 1 {
					t.Fatal("resolved keys differ from final model binding or validation repeated", err)
				}
				if !item.required {
					candidate, _ := bound.Candidate().Get("reviewer")
					want := forms.Null()
					if len(keys) == 1 {
						want = forms.Integer(keys[0])
					}
					if !candidate.Equal(want) {
						t.Fatal("binding retained a key outside the prepared lookup")
					}
				}
			}
			if text := fmt.Sprintf("%+v %#v", input, input); strings.Contains(text, "private-invalid-key") || !strings.Contains(text, "redacted") {
				t.Fatal("choice lookup exposed raw input through formatting")
			}
		})
	}
}

func TestChoiceInputMultipleClearsWritableInitialAndPreservesReadonlyKeys(t *testing.T) {
	for _, item := range []struct {
		name  string
		raw   []string
		keys  []int64
		valid bool
	}{
		{"omitted", nil, nil, true},
		{"distinct_canonical", []string{"7", "7", "0", "-2", "9223372036854775807"}, []int64{7, 0, -2, math.MaxInt64}, true},
		{"alias", []string{"+07"}, nil, false},
		{"explicit_blank", []string{""}, nil, false},
	} {
		t.Run(item.name, func(t *testing.T) {
			metadata := (helpdesk.TicketDescriptor{}).Metadata()
			spec, err := formmodel.NewSpecForFields(metadata, []string{"labels"}, formmodel.OverrideField("labels", formmodel.WithRequired(false)))
			if err != nil {
				t.Fatal(err)
			}
			initial := map[string]forms.Value{"labels": forms.Integers(2, 5)}
			input, err := formmodel.PrepareChoiceInput(metadata, spec, forms.NewData(map[string][]string{"labels": item.raw}), initial)
			if err != nil {
				t.Fatal(err)
			}
			initial["labels"] = forms.Integers(999)
			keys, valid, err := input.ChoiceKeys("labels")
			if err != nil || valid != item.valid || !slices.Equal(keys, item.keys) {
				t.Fatal("multiple selection retained current keys or changed canonical spelling", keys, valid, err)
			}
			server, valid, err := input.InitialChoiceKeys("labels")
			if err != nil || !valid || !slices.Equal(server, []int64{2, 5}) {
				t.Fatal("read-only/display selection used raw or borrowed initial", server, err)
			}
			server[0] = 999
			again, _, err := input.InitialChoiceKeys("labels")
			if err != nil || !slices.Equal(again, []int64{2, 5}) {
				t.Fatal("initial key accessor leaked collection storage", err)
			}
		})
	}
}

func TestChoiceInputOwnsMetadataAndRejectsUnselectedOrInvalidInitial(t *testing.T) {
	metadata := scalarChoiceInputModel(t)
	field, err := forms.ModelChoiceField("reviewer", forms.WithRequired(false), forms.WithNullable())
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	input, err := formmodel.PrepareChoiceInput(metadata, spec, forms.Data{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for index := range metadata.Fields {
		if metadata.Fields[index].Name == "reviewer" {
			metadata.Fields[index].Default.Integer = 999
		}
	}
	keys, valid, err := input.ChoiceKeys("reviewer")
	if err != nil || !valid || !slices.Equal(keys, []int64{2}) {
		t.Fatal("caller model mutation changed prepared defaults", err)
	}
	for _, name := range []string{"unknown", "id", "author", "title"} {
		if _, _, err := input.ChoiceKeys(name); err == nil {
			t.Fatal("unselected field acquired key lookup", name)
		}
		if _, _, err := input.InitialChoiceKeys(name); err == nil {
			t.Fatal("unselected field acquired display lookup", name)
		}
	}
	zero := formmodel.ChoiceInput{}
	if _, _, err := zero.ChoiceKeys("reviewer"); err == nil || zero.Initialized() {
		t.Fatal("zero lookup accepted")
	}
	if _, _, err := zero.InitialChoiceKeys("reviewer"); err == nil {
		t.Fatal("zero display lookup accepted")
	}
	if invalid, err := formmodel.PrepareChoiceInput(metadata, spec, forms.Data{}, map[string]forms.Value{"reviewer": forms.String("private-invalid")}); err == nil || invalid.Initialized() {
		t.Fatal("invalid server model initial accepted")
	}
}
