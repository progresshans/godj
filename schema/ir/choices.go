package ir

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// ValidateChoices validates the closed choice metadata before a consumer
// creates its own immutable snapshot. It does not validate stored model rows.
func ValidateChoices(field Field) error {
	return validateChoices(field, "field.choices")
}

func validateChoices(field Field, path string) error {
	if field.Choices == nil {
		return nil
	}
	if len(field.Choices) == 0 {
		return validation(path, "empty", "an explicit choice list must not be empty")
	}
	var kind ScalarKind
	switch field.Kind {
	case FieldChar, FieldText:
		kind = ScalarString
	case FieldInteger:
		kind = ScalarInteger
	default:
		return validation(path, "unsupported", "choices currently require a string or IntegerField")
	}
	seen := make(map[Scalar]struct{}, len(field.Choices))
	for index, choice := range field.Choices {
		itemPath := fmt.Sprintf("%s[%d]", path, index)
		if err := validateScalar(choice.Value, itemPath+".value"); err != nil {
			return err
		}
		if choice.Value.Kind != kind {
			return validation(itemPath+".value", "type_mismatch", "choice value must match the field kind")
		}
		if _, duplicate := seen[choice.Value]; duplicate {
			return validation(itemPath+".value", "duplicate", "choice values must be unique")
		}
		seen[choice.Value] = struct{}{}
		if !utf8.ValidString(choice.Label) || strings.ContainsRune(choice.Label, 0) {
			return validation(itemPath+".label", "invalid", "choice label must be valid text without NUL")
		}
		if choice.Value.Kind == ScalarString && strings.ContainsRune(choice.Value.String, 0) {
			return validation(itemPath+".value", "invalid", "choice value must not contain NUL")
		}
		if field.Kind == FieldChar && utf8.RuneCountInString(choice.Value.String) > field.MaxLength {
			return validation(itemPath+".value", "max_length", "choice value exceeds the field length")
		}
	}
	return nil
}

// ChoiceLabel reports a configured label without replacing an unknown value.
// Renderers decide how to display existing rows outside the current choices.
func (field Field) ChoiceLabel(value Scalar) (string, bool) {
	for _, choice := range field.Choices {
		if choice.Value == value {
			return choice.Label, true
		}
	}
	return "", false
}
