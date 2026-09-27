package admin

import (
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/schema/ir"
)

// FormConfig selects model fields from Schema IR and may append command-only
// inputs, such as password confirmation. ExtraFields cannot shadow any stored
// field, including a field excluded from this form. Persistence stays explicit.
type FormConfig struct {
	Fields         []string
	Overrides      []formmodel.Override
	ExtraFields    []forms.Field
	Validators     []forms.CrossValidator
	RelatedChoices []RelatedChoices
}

func prepareModelForm(model ir.Model, config FormConfig) (forms.Spec, error) {
	projected, err := formmodel.NewSpecForFields(model, config.Fields, config.Overrides...)
	if err != nil {
		return forms.Spec{}, err
	}
	modelNames := make(map[string]bool, len(model.Fields)+len(model.ManyToMany))
	for _, field := range model.Fields {
		modelNames[field.Name] = true
	}
	for _, field := range model.ManyToMany {
		modelNames[field.Name] = true
	}
	fields := projected.Fields()
	for _, extra := range config.ExtraFields {
		if modelNames[extra.Name()] {
			return forms.Spec{}, &ConfigError{Path: "model.form." + extra.Name(), Code: "shadows_model"}
		}
		fields = append(fields, extra)
	}
	for _, field := range fields {
		if field.Name() == "csrfmiddlewaretoken" || field.Name() == "expected_revision" {
			return forms.Spec{}, &ConfigError{Path: "model.form." + field.Name(), Code: "reserved"}
		}
	}
	return forms.NewSpec(fields, config.Validators...)
}

func additionalPermissions(primary auth.Permission, additional []auth.Permission) ([]auth.Permission, error) {
	permissions, err := auth.RequiredPermissions(primary, additional...)
	if err != nil {
		return nil, &ConfigError{Path: "model.permissions", Code: "invalid", Cause: err}
	}
	return permissions, nil
}

func (model registeredModel) forCreate() registeredModel {
	model.form, model.formFor, model.choicePermissions = model.createForm, model.createFormFor, model.createChoicePermissions
	model.revisionField = ""
	return model
}
