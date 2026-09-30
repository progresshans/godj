package forms

import (
	"strings"

	"github.com/progresshans/godj/internal/unicode16"
)

// EmailField cleans an email string without case folding or address rewriting.
// It defaults to EmailInput and 320 characters. Model projection supplies its
// own storage limit. Grammar, length and NUL diagnostics follow pinned Django.
func EmailField(name string, options ...FieldOption) (Field, error) {
	config := fieldConfig{label: name, required: true, widget: EmailInput, trimWhitespace: true, maxLength: 320}
	for _, option := range options {
		if option == nil {
			return Field{}, &ConfigError{Path: "fields." + name, Code: "nil_option"}
		}
		option.apply(&config)
	}
	return makeField(name, FieldEmail, config)
}

func stringFieldKind(kind FieldKind) bool {
	return kind == FieldChar || kind == FieldEmail || kind == FieldURL
}

func trimStringInput(kind FieldKind, value string) string {
	if kind == FieldEmail || kind == FieldURL {
		return unicode16.TrimSpace(value)
	}
	return strings.TrimSpace(value)
}
