package serializers

import (
	"strconv"
	"unicode/utf8"

	"github.com/progresshans/godj/internal/unicode16"
	"github.com/progresshans/godj/validation"
)

// SlugField validates submitted strings with the pinned Django slug grammar.
// Output and omission defaults preserve text without grammar checks or edits.
func SlugField(name string, options ...FieldOption) (Field, error) {
	return makeField(name, FieldSlug, fieldConfig{required: true, trimWhitespace: true}, options)
}

func WithAllowUnicode(allow bool) FieldOption {
	return fieldOption(func(config *fieldConfig) { config.allowUnicode, config.allowUnicodeSet = allow, true })
}

func (field Field) AllowUnicode() bool { return field.allowUnicode }

func cleanSlugValue(field Field, value Value) (Value, validation.Errors) {
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
	// Django uses an absolute end anchor. In particular, untrimmed ASCII
	// input cannot retain the trailing LF accepted by DRF 3.18's "$" regex.
	if !validation.ValidSlug(text, field.allowUnicode) {
		failures = append(failures, validation.New(name, "invalid"))
	}
	if len(failures) != 0 {
		return Value{}, validation.NewErrors(failures...)
	}
	return String(text), validation.Errors{}
}
