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
	Definition     formmodel.Definition
	RelatedChoices []RelatedChoices
}

func prepareModelForm(model ir.Model, config FormConfig) (forms.Spec, error) {
	projected, err := config.Definition.Spec(model)
	if err != nil {
		if failure, ok := err.(*formmodel.Error); ok && failure.Code == "shadows_model" {
			return forms.Spec{}, &ConfigError{Path: "model.form." + failure.Path, Code: failure.Code}
		}
		return forms.Spec{}, err
	}
	fields := projected.Fields()
	for _, field := range fields {
		if field.Name() == "csrfmiddlewaretoken" || field.Name() == "expected_revision" {
			return forms.Spec{}, &ConfigError{Path: "model.form." + field.Name(), Code: "reserved"}
		}
	}
	return projected, nil
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
