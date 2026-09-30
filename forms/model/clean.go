package model

import (
	"fmt"
	"slices"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

// CleanFunc returns explicit changes to the model candidate and input errors.
// It must be pure and safe for concurrent use. The candidate is immutable;
// returning no changes leaves it intact. Form.Cleaned is never rewritten.
type CleanFunc func(forms.Values) (forms.Values, validation.Errors)

// PostClean runs after model field cleaning, including when fields failed.
// Fields declares every scalar the Clean callback may change, including fields
// excluded from the form. Such changes enter Input for the application's typed
// persistence adapter; declaration is not permission to perform a database
// write. Primary keys and collections cannot be changed here.
// Validators are read-only checks of the resulting candidate, even on errors.
type PostClean struct {
	Fields     []string
	Clean      CleanFunc
	Validators []Validator
}

func (clean PostClean) Clone() PostClean {
	clean.Fields = slices.Clone(clean.Fields)
	clean.Validators = slices.Clone(clean.Validators)
	return clean
}

func (clean PostClean) Empty() bool {
	return clean.Clean == nil && len(clean.Fields) == 0 && len(clean.Validators) == 0
}

func (clean PostClean) validate(model ir.Model) error {
	dimensions, err := ir.ImageDimensionOwners(model)
	if err != nil {
		return &Error{Path: "model", Code: "invalid_image_dimensions"}
	}
	if clean.Clean == nil && len(clean.Fields) != 0 {
		return &Error{Path: "post_clean.clean", Code: "nil"}
	}
	byName := make(map[string]ir.Field, len(model.Fields))
	for _, field := range model.Fields {
		byName[field.Name] = field
	}
	seen := make(map[string]bool, len(clean.Fields))
	for _, name := range clean.Fields {
		field, found := byName[name]
		if !found || field.PrimaryKey || field.Kind.IsFile() || dimensions[name] != "" {
			return &Error{Path: "post_clean.fields." + name, Code: "non_writable_scalar"}
		}
		if seen[name] {
			return &Error{Path: "post_clean.fields." + name, Code: "duplicate"}
		}
		seen[name] = true
	}
	for i, validator := range clean.Validators {
		if nilValidator(validator) {
			return &Error{Path: fmt.Sprintf("post_clean.validators[%d]", i), Code: "nil"}
		}
	}
	return nil
}

func (clean PostClean) apply(bound BoundForm) (BoundForm, error) {
	if clean.Clean != nil {
		changes, failures := clean.Clean(bound.candidate)
		candidate := make(map[string]forms.Value, len(bound.model.Fields))
		for _, entry := range bound.candidate.All() {
			candidate[entry.Name()] = entry.Value()
		}
		for _, entry := range changes.All() {
			if !slices.Contains(clean.Fields, entry.Name()) {
				return BoundForm{}, &Error{Path: "post_clean.changes." + entry.Name(), Code: "undeclared_field"}
			}
			var field ir.Field
			for _, declared := range bound.model.Fields {
				if declared.Name == entry.Name() {
					field = declared
					break
				}
			}
			if !cleanValueMatches(field, entry.Value()) {
				return BoundForm{}, &Error{Path: "post_clean.changes." + entry.Name(), Code: "type_mismatch"}
			}
			candidate[entry.Name()] = entry.Value()
			bound.changed = append(bound.changed, entry.Name())
		}
		bound.candidate = forms.NewValues(candidate)
		var err error
		bound, err = bound.WithErrors(failures)
		if err != nil {
			return BoundForm{}, err
		}
	}
	for _, validator := range clean.Validators {
		var err error
		bound, err = bound.WithErrors(validator.ValidateModel(bound.candidate))
		if err != nil {
			return BoundForm{}, err
		}
	}
	return bound, nil
}

// A clean result must have the declared representation, but field validation
// is not repeated. Email grammar, choices, blank and other field policies ran
// before Clean; the eventual storage checks remain the writer's responsibility.
func cleanValueMatches(field ir.Field, value forms.Value) bool {
	if value.IsNull() {
		return true
	}
	switch field.Kind {
	case ir.FieldChar, ir.FieldEmail, ir.FieldText:
		return value.Kind() == forms.ValueString
	case ir.FieldInteger, ir.FieldForeignKey:
		return value.Kind() == forms.ValueInteger
	case ir.FieldBoolean:
		return value.Kind() == forms.ValueBoolean
	case ir.FieldDate:
		return value.Kind() == forms.ValueDate
	case ir.FieldDateTime:
		return value.Kind() == forms.ValueDateTime
	case ir.FieldTime:
		return value.Kind() == forms.ValueTime
	case ir.FieldDuration:
		return value.Kind() == forms.ValueDuration
	case ir.FieldFloat:
		return value.Kind() == forms.ValueFloat
	case ir.FieldDecimal:
		return value.Kind() == forms.ValueDecimal
	case ir.FieldUUID:
		return value.Kind() == forms.ValueUUID
	case ir.FieldJSON:
		return value.Kind() == forms.ValueJSON
	default:
		return false
	}
}
