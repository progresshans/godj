package serializers

import (
	"strconv"
	"unicode/utf8"

	"github.com/progresshans/godj/internal/unicode16"
	"github.com/progresshans/godj/validation"
)

// URLField validates a complete URL after the explicit whitespace policy.
// JSON input does not infer a scheme. Output/defaults retain the supplied
// string and do not re-run input validation or normalization.
func URLField(name string, options ...FieldOption) (Field, error) {
	return makeField(name, FieldURL, fieldConfig{required: true, trimWhitespace: true}, options)
}

func cleanURLValue(field Field, value Value) (Value, validation.Errors) {
	text := value.string
	if field.trimWhitespace {
		text = unicode16.TrimSpace(text)
	}
	if text == "" {
		if !field.allowEmpty {
			return Value{}, oneViolation(field.name, CodeBlank)
		}
		return String(""), validation.Errors{}
	}
	name := validation.Field(field.name)
	var failures []validation.Violation
	if field.maxLength > 0 && utf8.RuneCountInString(text) > field.maxLength {
		failures = append(failures, validation.New(name, CodeMaxLength, validation.NewParam("max_length", strconv.Itoa(field.maxLength))))
	}
	if !validation.ValidURL(text) {
		failures = append(failures, validation.New(name, "invalid"))
	}
	if len(failures) != 0 {
		return Value{}, validation.NewErrors(failures...)
	}
	return String(text), validation.Errors{}
}
