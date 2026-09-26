package serializers

import (
	"slices"
	"strings"
	"unicode/utf8"
)

// Choice describes one accepted scalar input and its presentation label.
type Choice struct {
	Value Value
	Label string
}

// WithChoices snapshots ordered string or integer choices. JSON scalar types
// stay strict and string choices preserve whitespace. Defaults are absence
// values, not submitted input, and are not restricted by membership.
func WithChoices(choices ...Choice) FieldOption {
	owned := append([]Choice{}, choices...)
	return fieldOption(func(config *fieldConfig) { config.choices = slices.Clone(owned) })
}

// Choices returns a detached copy, or nil for an unrestricted field.
func (field Field) Choices() []Choice { return slices.Clone(field.choices) }

func configureChoices(name string, kind FieldKind, config *fieldConfig) error {
	if config.choices == nil {
		return nil
	}
	invalid := func(detail string) error { return invalidConfig("fields."+name+".choices", detail) }
	if len(config.choices) == 0 || (kind != FieldString && kind != FieldInteger) {
		return invalid("choices require a nonempty list for a string or integer field")
	}
	if kind == FieldString {
		if config.trimWhitespaceSet && config.trimWhitespace {
			return invalid("choice input must preserve whitespace")
		}
		config.trimWhitespace = false
	}
	stringsSeen, integersSeen := make(map[string]bool), make(map[int64]bool)
	for _, choice := range config.choices {
		if !choice.Value.validValue() || !valueMatchesField(choice.Value, kind, false) {
			return invalid("choice value must match the field type")
		}
		if !utf8.ValidString(choice.Label) || strings.ContainsRune(choice.Label, 0) {
			return invalid("choice label must be valid text without NUL")
		}
		if kind == FieldString {
			value := choice.Value.string
			if stringsSeen[value] {
				return invalid("choice value is duplicated")
			}
			stringsSeen[value] = true
			if config.maxLength > 0 && utf8.RuneCountInString(value) > config.maxLength {
				return invalid("choice value exceeds maximum length")
			}
			if value == "" {
				config.allowEmpty = true
			}
		} else {
			if integersSeen[choice.Value.integer] {
				return invalid("choice value is duplicated")
			}
			integersSeen[choice.Value.integer] = true
		}
	}
	return nil
}

func (field Field) acceptsChoice(value Value) bool {
	if field.kind == FieldString && field.allowEmpty && value.kind == ValueString && value.string == "" {
		return true
	}
	for _, choice := range field.choices {
		if choice.Value.kind == value.kind && (value.kind == ValueString && choice.Value.string == value.string ||
			value.kind == ValueInteger && choice.Value.integer == value.integer) {
			return true
		}
	}
	return false
}
