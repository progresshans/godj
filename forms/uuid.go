package forms

import (
	"github.com/progresshans/godj/internal/uuidinput"
	"github.com/progresshans/godj/uuid"
	"github.com/progresshans/godj/validation"
)

func UUID(value uuid.UUID) Value { return Value{kind: ValueUUID, string: value.String()} }
func (value Value) AsUUID() (uuid.UUID, bool) {
	if value.kind != ValueUUID {
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

// UUIDField preserves the submitted spelling and cleans it to one UUID value.
func UUIDField(name string, options ...FieldOption) (Field, error) {
	config := fieldConfig{label: name, required: true, widget: TextInput}
	for _, option := range options {
		if option == nil {
			return Field{}, &ConfigError{Path: "fields." + name, Code: "nil_option"}
		}
		option.apply(&config)
	}
	return makeField(name, FieldUUID, config)
}
func cleanUUID(raw string) (Value, validation.Code) {
	raw = uuidinput.TrimSpace(raw)
	if raw == "" {
		return Null(), ""
	}
	value, err := uuidinput.Parse(raw)
	if err != nil {
		return Null(), "invalid"
	}
	return UUID(value), ""
}
