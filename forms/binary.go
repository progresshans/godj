package forms

import (
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/binaryvalue"
	"github.com/progresshans/godj/internal/uuidinput"
	"github.com/progresshans/godj/validation"
)

func Binary(value binaryvalue.Value) Value { return textValue(ValueBinary, value.Data) }
func (value Value) AsBinary() (binaryvalue.Value, bool) {
	if value.kind != ValueBinary {
		return binaryvalue.Value{}, false
	}
	decoded := binaryvalue.Value{Data: value.text()}
	return decoded, decoded.Valid()
}
func (values Values) Binary(name string) (binaryvalue.Value, bool) {
	value, ok := values.Get(name)
	if !ok {
		return binaryvalue.Value{}, false
	}
	return value.AsBinary()
}

// BinaryField cleans a base64 text input to owned bytes. Empty optional input
// becomes empty bytes, even for nullable storage; it does not mean SQL NULL.
func BinaryField(name string, options ...FieldOption) (Field, error) {
	config := fieldConfig{label: name, required: true, widget: TextInput}
	for _, option := range options {
		if option == nil {
			return Field{}, &ConfigError{Path: "fields." + name, Code: "nil_option"}
		}
		option.apply(&config)
	}
	return makeField(name, FieldBinary, config)
}

func cleanBinary(raw string) (Value, validation.Code) {
	raw = uuidinput.TrimSpace(raw)
	if !utf8.ValidString(raw) {
		return Null(), "invalid_utf8"
	}
	if strings.ContainsRune(raw, 0) {
		return Null(), "null_characters_not_allowed"
	}
	value, err := binaryvalue.Parse(raw)
	if err != nil {
		return Null(), "invalid"
	}
	return Binary(value), ""
}
