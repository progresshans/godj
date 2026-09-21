package forms

import (
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/validation"
)

// Choice pairs a stored string or integer with its presentation label.
type Choice struct {
	Value Value
	Label string
}

// InputValue is the exact HTML submission representation of a valid choice.
func (choice Choice) InputValue() string {
	if choice.Value.kind == ValueInteger {
		return strconv.FormatInt(choice.Value.integer, 10)
	}
	return choice.Value.string
}

// WithChoices snapshots an ordered, nonempty choice list. Choice fields use
// Select unless a compatible widget is explicitly supplied. Submitted values
// are matched before any scalar conversion; whitespace is significant.
func WithChoices(choices ...Choice) FieldOption {
	owned := append([]Choice{}, choices...)
	return fieldOption(func(config *fieldConfig) { config.choices = slices.Clone(owned) })
}

// Choices returns a detached copy in declaration order. An empty ModelChoice
// list permits no nonempty input; ordinary fields with nil choices are unrestricted.
func (field Field) Choices() []Choice { return slices.Clone(field.choices) }

func validateChoices(name string, kind FieldKind, config fieldConfig) error {
	if config.choices == nil && !config.modelChoice {
		return nil
	}
	invalid := func(code string) error { return &ConfigError{Path: "fields." + name + ".choices", Code: code} }
	if len(config.choices) == 0 && !config.modelChoice || (kind != FieldChar && kind != FieldInteger) {
		return invalid("unsupported")
	}
	if config.modelChoice && (kind != FieldInteger || config.widget != Select) {
		return invalid("unsupported_model_choice")
	}
	seen := make(map[Value]struct{}, len(config.choices))
	for _, choice := range config.choices {
		if !validValueForField(choice.Value, kind, false) {
			return invalid("type_mismatch")
		}
		if !utf8.ValidString(choice.Label) || strings.ContainsRune(choice.Label, 0) ||
			kind == FieldChar && (!utf8.ValidString(choice.Value.string) || strings.ContainsRune(choice.Value.string, 0)) {
			return invalid("invalid_text")
		}
		if kind == FieldChar && config.maxLength > 0 && utf8.RuneCountInString(choice.Value.string) > config.maxLength {
			return invalid("max_length")
		}
		if _, duplicate := seen[choice.Value]; duplicate {
			return invalid("duplicate")
		}
		seen[choice.Value] = struct{}{}
	}
	return nil
}

func cleanChoice(field Field, raw string) (Value, validation.Code) {
	if raw == "" {
		if field.required {
			return Null(), "required"
		}
		if field.kind == FieldChar {
			return field.emptyValue, ""
		}
		return Null(), ""
	}
	for _, choice := range field.choices {
		if raw == choice.InputValue() {
			return choice.Value, ""
		}
	}
	return Null(), "invalid_choice"
}
