package model

import (
	"slices"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/schema/ir"
)

// Definition is a reusable, database-independent model form projection.
// Schema IR supplies stored fields; extra inputs are command data and cannot
// shadow any model field, even a field excluded from this projection.
type Definition struct {
	Fields      []string
	Overrides   []Override
	ExtraFields []forms.Field
	Validators  []forms.CrossValidator
}

func (definition Definition) Clone() Definition {
	definition.Fields = slices.Clone(definition.Fields)
	definition.Overrides = slices.Clone(definition.Overrides)
	definition.ExtraFields = slices.Clone(definition.ExtraFields)
	definition.Validators = slices.Clone(definition.Validators)
	return definition
}

func (definition Definition) Spec(model ir.Model) (forms.Spec, error) {
	projected, err := NewSpecForFields(model, definition.Fields, definition.Overrides...)
	if err != nil {
		return forms.Spec{}, err
	}
	names := make(map[string]bool, len(model.Fields)+len(model.ManyToMany))
	for _, field := range model.Fields {
		names[field.Name] = true
	}
	for _, field := range model.ManyToMany {
		names[field.Name] = true
	}
	fields := projected.Fields()
	for _, extra := range definition.ExtraFields {
		if names[extra.Name()] {
			return forms.Spec{}, &Error{Path: extra.Name(), Code: "shadows_model"}
		}
		fields = append(fields, extra)
	}
	return forms.NewSpec(fields, definition.Validators...)
}
