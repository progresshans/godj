package serializers

import (
	"github.com/progresshans/godj/binaryvalue"
	"github.com/progresshans/godj/validation"
)

// Binary retains immutable bytes and emits canonical standard base64. Invalid
// literals remain invalid Values instead of becoming empty bytes or JSON null.
func Binary(value binaryvalue.Value) Value {
	if !value.Valid() {
		return Value{}
	}
	return Value{kind: ValueBinary, string: value.Data, valid: true}
}
func (value Value) AsBinary() (binaryvalue.Value, bool) {
	if !value.valid || value.kind != ValueBinary {
		return binaryvalue.Value{}, false
	}
	return binaryvalue.Value{Data: value.string}, true
}
func (values Values) Binary(name string) (binaryvalue.Value, bool) {
	value, ok := values.Get(name)
	if !ok {
		return binaryvalue.Value{}, false
	}
	return value.AsBinary()
}

// BinaryField requires explicit standard base64 JSON text. Required controls
// presence; empty bytes are a value. WithMaxLength limits decoded input bytes.
func BinaryField(name string, options ...FieldOption) (Field, error) {
	return makeField(name, FieldBinary, fieldConfig{required: true}, options)
}

func cleanBinaryValue(field Field, value Value) (Value, validation.Errors) {
	decoded, ok := value.AsBinary()
	if !ok {
		text, stringValue := value.AsString()
		if !stringValue {
			return Value{}, oneViolation(field.name, "invalid")
		}
		var err error
		decoded, err = binaryvalue.Parse(text)
		if err != nil {
			return Value{}, oneViolation(field.name, "invalid")
		}
	}
	if field.maxLength > 0 && len(decoded.Data) > field.maxLength {
		return Value{}, oneViolation(field.name, "max_length")
	}
	return Binary(decoded), validation.NewErrors()
}
