package serializers

import (
	"errors"
	"math"
	"unicode/utf8"

	"github.com/progresshans/godj/internal/floatvalue"
	"github.com/progresshans/godj/validation"
)

// Float is a finite binary64 JSON number. Invalid model output remains an
// invalid Value; non-finite input is rejected before application persistence.
func Float(value float64) Value {
	if !floatvalue.Finite(value) {
		return Value{}
	}
	return Value{kind: ValueFloat, integer: int64(math.Float64bits(value)), valid: true}
}
func (value Value) AsFloat() (float64, bool) {
	if !value.valid || value.kind != ValueFloat {
		return 0, false
	}
	return math.Float64frombits(uint64(value.integer)), true
}
func (values Values) Float(name string) (float64, bool) {
	value, ok := values.Get(name)
	if !ok {
		return 0, false
	}
	return value.AsFloat()
}
func FloatField(name string, options ...FieldOption) (Field, error) {
	return makeField(name, FieldFloat, fieldConfig{required: true}, options)
}
func cleanFloatValue(field Field, value Value) (Value, validation.Errors) {
	var number float64
	var err error
	switch value.kind {
	case ValueFloat:
		return value, validation.NewErrors()
	case ValueInteger:
		number = float64(value.integer)
	case ValueBoolean:
		if value.boolean {
			number = 1
		}
	case ValueNumber:
		number, err = floatvalue.JSONNumber(value.string)
	case ValueString:
		if utf8.RuneCountInString(value.string) > 1000 {
			return Value{}, oneViolation(field.name, "max_string_length")
		}
		number, err = floatvalue.ParseInput(value.string)
	default:
		return Value{}, oneViolation(field.name, CodeFloat)
	}
	if errors.Is(err, floatvalue.ErrOverflow) {
		return Value{}, oneViolation(field.name, "overflow")
	}
	if err != nil || !floatvalue.Finite(number) {
		return Value{}, oneViolation(field.name, CodeFloat)
	}
	return Float(number), validation.NewErrors()
}
