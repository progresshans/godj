package model

import (
	"fmt"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/schema/ir"
)

// ChoiceInput owns the pure lookup preparation for one model form. It retains
// detached model/field metadata and initial values alongside immutable raw
// input. Its keys are requests for current resolution, never authorization.
type ChoiceInput struct {
	fields    map[string]forms.Field
	metadata  map[string]ir.Field
	candidate map[string]forms.Value
	initial   map[string]forms.Value
	data      forms.Data
	valid     bool
}

// PrepareChoiceInput prepares selected fields and current/model-default values
// once, using the same model binding preparation as Bind. It performs no I/O,
// field cleaning or field/cross-field/model validation. Initial must be the
// server-owned initial values that will also be passed to the eventual Bind.
// A command form with no model uses Field.ModelChoiceKeys and Field.Default
// directly; it must not manufacture model metadata for this preparation.
func PrepareChoiceInput(model ir.Model, spec forms.Spec, data forms.Data, initial map[string]forms.Value) (ChoiceInput, error) {
	binding, formInitial, err := prepareModelBinding(model.Clone(), spec.Fields(), initial, PostClean{})
	if err != nil {
		return ChoiceInput{}, err
	}
	fields := make(map[string]forms.Field, len(binding.fields))
	for _, field := range binding.fields {
		fields[field.Name()] = field
	}
	return ChoiceInput{fields: fields, metadata: binding.byName, candidate: binding.candidate, initial: formInitial, data: data, valid: true}, nil
}

func (input ChoiceInput) Initialized() bool { return input.valid }

// ChoiceKeys extracts the submitted keys with the field's normal syntax. An
// omitted optional scalar input with a supported IR default retains its current model
// candidate, including an explicit current NULL, instead of a form display
// default. Required omission, explicit empty input and multiple-choice omission
// never acquire retained keys. A false second result preserves malformed raw
// input for ordinary Bind diagnostics; no validators have run.
func (input ChoiceInput) ChoiceKeys(fieldName string) ([]int64, bool, error) {
	field, err := input.field(fieldName)
	if err != nil {
		return nil, false, err
	}
	return modelChoiceInputKeys(field, input.metadata[fieldName], input.data, input.candidate[fieldName])
}

// InitialChoiceKeys returns the server display selection for an unbound or
// read-only form: the explicitly supplied initial value, or Field.Default when
// that value is absent. This matches Spec.Unbound and read-only row display;
// an overridden form display default can differ from the IR model default that
// ChoiceKeys must resolve for an omitted optional POST. Submitted input is
// never consulted. All returned key slices are detached.
func (input ChoiceInput) InitialChoiceKeys(fieldName string) ([]int64, bool, error) {
	field, err := input.field(fieldName)
	if err != nil {
		return nil, false, err
	}
	return modelChoiceInitialKeys(field, input.initial)
}

func (input ChoiceInput) field(name string) (forms.Field, error) {
	if !input.valid {
		return forms.Field{}, &Error{Path: "choice_input", Code: "uninitialized"}
	}
	field := input.fields[name]
	_, parent := field.InlineParent()
	if !field.ModelChoice() || parent {
		return forms.Field{}, &Error{Path: "choice_input." + name, Code: "not_selected_relation"}
	}
	return field, nil
}

func modelChoiceInputKeys(field forms.Field, metadata ir.Field, data forms.Data, retained forms.Value) ([]int64, bool, error) {
	keys, valid, err := field.ModelChoiceKeys(data)
	if err != nil || !valid || len(keys) != 0 || field.Required() || field.Kind() != forms.FieldInteger || !retainsOmittedDefault(metadata, field, data, forms.Null()) {
		return keys, valid, err
	}
	return modelChoiceValueKeys(field, retained)
}

func modelChoiceInitialKeys(field forms.Field, initial map[string]forms.Value) ([]int64, bool, error) {
	value, present := initial[field.Name()]
	if !present {
		value, _ = field.Default()
	}
	return modelChoiceValueKeys(field, value)
}

func modelChoiceValueKeys(field forms.Field, value forms.Value) ([]int64, bool, error) {
	if value.IsNull() {
		return nil, true, nil
	}
	if field.Kind() == forms.FieldIntegerList {
		if keys, valid := value.AsIntegers(); valid {
			return keys, true, nil
		}
	} else if key, valid := value.AsInteger(); valid {
		return []int64{key}, true, nil
	}
	return nil, false, &Error{Path: "choices." + field.Name(), Code: "invalid_initial_value"}
}

func (ChoiceInput) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "model.ChoiceInput{redacted}")
}
