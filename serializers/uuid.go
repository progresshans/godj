package serializers

import (
	"strconv"

	"github.com/progresshans/godj/internal/uuidinput"
	"github.com/progresshans/godj/uuid"
	"github.com/progresshans/godj/validation"
)

// UUID snapshots a value and emits its canonical lowercase hyphenated string.
func UUID(value uuid.UUID) Value { return Value{kind: ValueUUID, string: value.String(), valid: true} }
func (value Value) AsUUID() (uuid.UUID, bool) {
	if !value.valid || value.kind != ValueUUID {
		return uuid.UUID{}, false
	}
	identifier, err := uuid.Parse(value.string)
	return identifier, err == nil
}
func (values Values) UUID(name string) (uuid.UUID, bool) {
	value, ok := values.Get(name)
	if !ok {
		return uuid.UUID{}, false
	}
	return value.AsUUID()
}
func UUIDField(name string, options ...FieldOption) (Field, error) {
	return makeField(name, FieldUUID, fieldConfig{required: true}, options)
}
func cleanUUIDValue(field Field, value Value) (Value, validation.Errors) {
	if value.kind == ValueUUID {
		return value, validation.NewErrors()
	}
	var identifier uuid.UUID
	var err error
	switch value.kind {
	case ValueString:
		identifier, err = uuidinput.Parse(value.string)
	case ValueInteger:
		identifier, err = uuidinput.Integer(strconv.FormatInt(value.integer, 10))
	case ValueNumber:
		identifier, err = uuidinput.Integer(value.string)
	case ValueBoolean:
		if value.boolean {
			identifier[15] = 1
		}
	default:
		return Value{}, oneViolation(field.name, "invalid")
	}
	if err != nil {
		return Value{}, oneViolation(field.name, "invalid")
	}
	return UUID(identifier), validation.NewErrors()
}
