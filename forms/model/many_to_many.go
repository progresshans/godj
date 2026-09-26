package model

import (
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/schema/ir"
)

func projectManyToMany(field ir.ManyToManyField, override overrideConfig) (forms.Field, error) {
	if field.Target.AppLabel == "" || field.Target.ModelName == "" || (field.Symmetry != ir.ManyToManyDirected && field.Symmetry != ir.ManyToManySymmetrical) {
		return forms.Field{}, &Error{Path: "fields." + field.Name, Code: "invalid_many_to_many"}
	}
	label := field.GoName
	if label == "" {
		label = field.Name
	}
	if override.hasLabel {
		label = override.label
	}
	options := []forms.FieldOption{forms.WithLabel(label)}
	if override.hasRequired {
		options = append(options, forms.WithRequired(override.required))
	}
	if override.hasWidget {
		options = append(options, forms.WithWidget(override.widget))
	}
	if len(override.validators) > 0 {
		options = append(options, forms.WithValidators(override.validators...))
	}
	result, err := forms.ModelMultipleChoiceField(field.Name, options...)
	if err != nil {
		return forms.Field{}, &Error{Path: "fields." + field.Name, Code: errorCode(err)}
	}
	return result, nil
}
