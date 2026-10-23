package forms

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/validation"
)

// SlugField validates a case-preserving slug after optional whitespace
// trimming. Standalone forms have no implicit length limit; model projection
// supplies the IR limit. Unicode mode never normalizes or transliterates text.
func SlugField(name string, options ...FieldOption) (Field, error) {
	config := fieldConfig{label: name, required: true, widget: TextInput, trimWhitespace: true}
	for _, option := range options {
		if option == nil {
			return Field{}, &ConfigError{Path: "fields." + name, Code: "nil_option"}
		}
		option.apply(&config)
	}
	return makeField(name, FieldSlug, config)
}

func WithAllowUnicode(allow bool) FieldOption {
	return fieldOption(func(config *fieldConfig) { config.allowUnicode, config.hasAllowUnicode = allow, true })
}

func (field Field) AllowUnicode() bool { return field.allowUnicode }

func slugFormErrors(field Field, value string) validation.Errors {
	if value == "" {
		return validation.Errors{}
	}
	name := validation.Field(field.name)
	var failures []validation.Violation
	if !validation.ValidSlug(value, field.allowUnicode) {
		failures = append(failures, validation.New(name, "invalid"))
	}
	if length := utf8.RuneCountInString(value); field.maxLength > 0 && length > field.maxLength {
		failures = append(failures, validation.New(name, "max_length", validation.NewParam("limit_value", strconv.Itoa(field.maxLength)), validation.NewParam("show_value", strconv.Itoa(length))))
	}
	if strings.ContainsRune(value, 0) {
		failures = append(failures, validation.New(name, "null_characters_not_allowed"))
	}
	return validation.NewErrors(failures...)
}
