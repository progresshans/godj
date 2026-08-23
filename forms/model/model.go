// Package model projects normalized Schema IR metadata into structural forms.
// It intentionally provides no reflection, dynamic assignment, or autosave.
package model

import (
	"fmt"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/schema/ir"
)

// Error reports an invalid model projection at startup.
type Error struct {
	Path string
	Code string
}

func (e *Error) Error() string {
	return fmt.Sprintf("forms/model: %s: %s", e.Path, e.Code)
}

// OverrideOption is a closed structural override. Storage metadata such as
// kind, nullability, default, and max length cannot be overridden here.
type OverrideOption interface {
	apply(*overrideConfig)
}

type overrideOption func(*overrideConfig)

func (option overrideOption) apply(config *overrideConfig) { option(config) }

func WithLabel(label string) OverrideOption {
	return overrideOption(func(config *overrideConfig) {
		config.label = label
		config.hasLabel = true
	})
}

func WithRequired(required bool) OverrideOption {
	return overrideOption(func(config *overrideConfig) {
		config.required = required
		config.hasRequired = true
	})
}

func WithValidators(validators ...forms.FieldValidator) OverrideOption {
	detached := append([]forms.FieldValidator(nil), validators...)
	return overrideOption(func(config *overrideConfig) {
		config.validators = append(config.validators, detached...)
	})
}

type overrideConfig struct {
	label       string
	hasLabel    bool
	required    bool
	hasRequired bool
	validators  []forms.FieldValidator
}

// Override identifies one existing IR field and presentation/validation-only
// customizations for its projected form field.
type Override struct {
	name   string
	config overrideConfig
	err    error
}

func OverrideField(name string, options ...OverrideOption) Override {
	config := overrideConfig{}
	for index, option := range options {
		if option == nil {
			return Override{name: name, err: &Error{
				Path: fmt.Sprintf("overrides.%s.options[%d]", name, index),
				Code: "nil",
			}}
		}
		option.apply(&config)
	}
	return Override{name: name, config: config}
}

// NewSpec projects editable Char and Boolean fields in exact IR declaration
// order. An Auto primary key is non-editable; every other unsupported kind is
// rejected rather than silently omitted.
func NewSpec(model ir.Model, overrides ...Override) (forms.Spec, error) {
	overrideByName := make(map[string]overrideConfig, len(overrides))
	for index, override := range overrides {
		if override.err != nil {
			return forms.Spec{}, override.err
		}
		if override.name == "" {
			return forms.Spec{}, &Error{Path: fmt.Sprintf("overrides[%d]", index), Code: "invalid_name"}
		}
		if _, exists := overrideByName[override.name]; exists {
			return forms.Spec{}, &Error{Path: "overrides." + override.name, Code: "duplicate"}
		}
		overrideByName[override.name] = cloneOverride(override.config)
	}

	known := make(map[string]struct{}, len(model.Fields))
	fields := make([]forms.Field, 0, len(model.Fields))
	for index, field := range model.Fields {
		path := fmt.Sprintf("fields[%d]", index)
		if field.Name == "" {
			return forms.Spec{}, &Error{Path: path + ".name", Code: "invalid"}
		}
		if _, duplicate := known[field.Name]; duplicate {
			return forms.Spec{}, &Error{Path: path + ".name", Code: "duplicate"}
		}
		known[field.Name] = struct{}{}
		override := overrideByName[field.Name]
		if field.Kind == ir.FieldAuto && field.PrimaryKey {
			if _, configured := overrideByName[field.Name]; configured {
				return forms.Spec{}, &Error{Path: "overrides." + field.Name, Code: "non_editable"}
			}
			continue
		}
		projected, err := projectField(field, override)
		if err != nil {
			return forms.Spec{}, &Error{Path: path + "." + field.Name, Code: errorCode(err)}
		}
		fields = append(fields, projected)
	}
	for name := range overrideByName {
		if _, ok := known[name]; !ok {
			return forms.Spec{}, &Error{Path: "overrides." + name, Code: "unknown_field"}
		}
	}
	if len(fields) == 0 {
		return forms.Spec{}, &Error{Path: "fields", Code: "no_editable_fields"}
	}
	spec, err := forms.NewSpec(fields)
	if err != nil {
		return forms.Spec{}, &Error{Path: "fields", Code: errorCode(err)}
	}
	return spec, nil
}

func projectField(field ir.Field, override overrideConfig) (forms.Field, error) {
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
	if len(override.validators) != 0 {
		options = append(options, forms.WithValidators(override.validators...))
	}
	switch field.Kind {
	case ir.FieldChar:
		if field.PrimaryKey || field.MaxLength <= 0 {
			return forms.Field{}, &Error{Code: "invalid_char_metadata"}
		}
		options = append(options, forms.WithRequired(!field.Nullable), forms.WithMaxLength(field.MaxLength))
		if override.hasRequired {
			options = append(options, forms.WithRequired(override.required))
		}
		if field.Nullable {
			options = append(options, forms.WithNullable())
		}
		if field.Default != nil {
			if field.Default.Kind != ir.ScalarString {
				return forms.Field{}, &Error{Code: "default_type_mismatch"}
			}
			options = append(options, forms.WithDefault(forms.String(field.Default.String)))
		}
		return forms.CharField(field.Name, options...)
	case ir.FieldBoolean:
		if field.PrimaryKey || field.Nullable || field.MaxLength != 0 {
			return forms.Field{}, &Error{Code: "invalid_boolean_metadata"}
		}
		options = append(options, forms.WithRequired(false))
		if override.hasRequired {
			options = append(options, forms.WithRequired(override.required))
		}
		if field.Default != nil {
			if field.Default.Kind != ir.ScalarBoolean {
				return forms.Field{}, &Error{Code: "default_type_mismatch"}
			}
			options = append(options, forms.WithDefault(forms.Boolean(field.Default.Boolean)))
		}
		return forms.BooleanField(field.Name, options...)
	default:
		return forms.Field{}, &Error{Code: "unsupported_kind"}
	}
}

func cloneOverride(config overrideConfig) overrideConfig {
	clone := config
	clone.validators = append([]forms.FieldValidator(nil), config.validators...)
	return clone
}

func errorCode(err error) string {
	if typed, ok := err.(*Error); ok && typed.Code != "" {
		return typed.Code
	}
	if typed, ok := err.(*forms.ConfigError); ok && typed.Code != "" {
		return typed.Code
	}
	return "invalid"
}
