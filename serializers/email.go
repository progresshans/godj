package serializers

import (
	"strconv"
	"unicode/utf8"

	"github.com/progresshans/godj/internal/unicode16"
	"github.com/progresshans/godj/validation"
)

// EmailField validates normalized input with pinned DRF/Django email syntax.
// Like DRF it has no implicit serializer max_length; the grammar still rejects
// more than 320 characters. Model projection supplies the declared length.
// The existing JSON value boundary rejects NUL before field cleaning.
// Output remains a string and does not re-run input grammar or trimming.
func EmailField(name string, options ...FieldOption) (Field, error) {
	return makeField(name, FieldEmail, fieldConfig{required: true, trimWhitespace: true}, options)
}

func cleanEmailValue(field Field, value Value) (Value, validation.Errors) {
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
	if !validation.ValidEmail(text) {
		failures = append(failures, validation.New(name, "invalid"))
	}
	if len(failures) != 0 {
		return Value{}, validation.NewErrors(failures...)
	}
	return String(text), validation.Errors{}
}
