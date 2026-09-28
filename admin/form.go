package admin

import (
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
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
	model.modelValidators = model.createModelValidators
	model.revisionField = ""
	return model
}

func (model registeredModel) bind(data forms.Data, initial map[string]forms.Value) (formmodel.BoundForm, error) {
	return formmodel.Bind(model.model, model.form, data, initial, model.modelValidators...)
}

func validateModelBoundData(model ir.Model, data forms.Data, spec forms.Spec, initial map[string]forms.Value, validators []formmodel.Validator) (forms.Values, error) {
	bound, err := formmodel.Bind(model, spec, data, initial, validators...)
	if err != nil {
		return forms.Values{}, &ConfigError{Path: "form", Code: "model_validation_failed", Cause: err}
	}
	if !bound.Form().Valid() {
		return forms.Values{}, validation.Reject(bound.Form().Errors(), nil)
	}
	return bound.Input()
}

// Initial comes from the same typed object as Snapshot, never from a submitted
// form. Selected values are rendered; additional model values remain private
// to the candidate. A model validator must receive a complete stored snapshot.
func modelInitialValues(model ir.Model, spec forms.Spec, provided map[string]forms.Value, id int64, revisionField string, revision int64, complete bool) (map[string]forms.Value, map[string]forms.Value, error) {
	selected := make(map[string]forms.Value, len(spec.Fields()))
	candidate := make(map[string]forms.Value, len(provided)+1)
	inputs := make(map[string]bool, len(spec.Fields()))
	for _, field := range spec.Fields() {
		inputs[field.Name()] = true
		value, found := provided[field.Name()]
		if !found {
			return nil, nil, &ConfigError{Path: "get.result.initial." + field.Name(), Code: "missing_value"}
		}
		selected[field.Name()] = value
	}
	known := make(map[string]bool, len(model.Fields)+len(model.ManyToMany))
	extra := make(map[string]forms.Value)
	var extraNames []string
	for _, field := range model.Fields {
		known[field.Name] = true
		value, found := provided[field.Name]
		if field.PrimaryKey {
			if found {
				if key, ok := value.AsInteger(); !ok || key != id {
					return nil, nil, &ConfigError{Path: "get.result.initial." + field.Name, Code: "primary_key_mismatch"}
				}
			}
			candidate[field.Name] = forms.Integer(id)
			continue
		}
		if field.Name == revisionField {
			if found {
				if version, valid := value.AsInteger(); !valid || version != revision {
					return nil, nil, &ConfigError{Path: "get.result.initial." + field.Name, Code: "revision_mismatch"}
				}
			}
			value, found = forms.Integer(revision), true
		}
		if complete && !found {
			return nil, nil, &ConfigError{Path: "get.result.initial." + field.Name, Code: "missing_model_value"}
		}
		if found && value.IsNull() && !field.Nullable {
			return nil, nil, &ConfigError{Path: "get.result.initial." + field.Name, Code: "nonnullable"}
		}
		if found {
			candidate[field.Name] = value
			if !inputs[field.Name] {
				extra[field.Name] = value
				extraNames = append(extraNames, field.Name)
			}
		}
	}
	for _, field := range model.ManyToMany {
		known[field.Name] = true
		if value, found := provided[field.Name]; found {
			candidate[field.Name] = value
			if !inputs[field.Name] {
				extra[field.Name] = value
				extraNames = append(extraNames, field.Name)
			}
		}
	}
	for name := range provided {
		if !known[name] {
			return nil, nil, &ConfigError{Path: "get.result.initial." + name, Code: "unknown_field"}
		}
	}
	if len(extraNames) > 0 {
		extraSpec, err := formmodel.NewSpecForFields(model, extraNames)
		if err != nil {
			return nil, nil, &ConfigError{Path: "get.result.initial", Code: "invalid_model", Cause: err}
		}
		if _, err := extraSpec.Unbound(extra); err != nil {
			return nil, nil, &ConfigError{Path: "get.result.initial", Code: "invalid_model_value", Cause: err}
		}
	}
	return selected, candidate, nil
}
