package serializers

import (
	"strconv"

	"github.com/progresshans/godj/validation"
)

// Integers snapshots an ordered integer list without converting keys through
// floating point. Empty input is an empty JSON array, never null.
func Integers(values ...int64) Value {
	items := make([]Value, len(values))
	for index, value := range values {
		items[index] = Integer(value)
	}
	return Value{kind: ValueList, list: items, valid: true}
}

// AsIntegers returns an independent list only when every child is an integer.
func (value Value) AsIntegers() ([]int64, bool) {
	if !value.valid || value.kind != ValueList {
		return nil, false
	}
	keys := make([]int64, len(value.list))
	for index, child := range value.list {
		key, ok := child.AsInteger()
		if !ok {
			return nil, false
		}
		keys[index] = key
	}
	return keys, true
}

// IntegerListField preserves order and duplicates. Membership, positive-key
// policy and replacement semantics belong to the application transaction.
// Presence can be required independently of an empty, non-null array.
func IntegerListField(name string, options ...FieldOption) (Field, error) {
	return makeField(name, FieldIntegerList, fieldConfig{required: true}, options)
}

func cleanIntegerList(field Field, value Value) (Value, validation.Errors) {
	if value.kind != ValueList {
		return Value{}, oneViolation(field.name, CodeType)
	}
	var failures []validation.Violation
	for index, child := range value.list {
		if child.kind == ValueInteger {
			continue
		}
		code := CodeType
		if child.kind == ValueNull {
			code = CodeNull
		}
		failures = append(failures, validation.New(validation.Field(field.name), code, validation.NewParam("index", strconv.Itoa(index))))
	}
	if len(failures) != 0 {
		return Value{}, validation.NewErrors(failures...)
	}
	return value, validation.NewErrors()
}
