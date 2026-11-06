package forms

import (
	"math"

	"github.com/progresshans/godj/internal/floatvalue"
	"github.com/progresshans/godj/validation"
)

// Float stores a finite binary64 initial or cleaned value. NaN and infinity
// produce an invalid value, never NULL.
func Float(value float64) Value {
	if !floatvalue.Finite(value) {
		return Value{kind: ValueKind(255)}
	}
	return Value{kind: ValueFloat, integer: int64(math.Float64bits(value))}
}
func (value Value) AsFloat() (float64, bool) {
	if value.kind != ValueFloat {
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

// FloatField accepts decimal/exponent input in a NumberInput with step=any.
func FloatField(name string, options ...FieldOption) (Field, error) {
	config := fieldConfig{label: name, required: true, widget: NumberInput}
	for _, option := range options {
		if option == nil {
			return Field{}, &ConfigError{Path: "fields." + name, Code: "nil_option"}
		}
		option.apply(&config)
	}
	return makeField(name, FieldFloat, config)
}
func cleanFloat(raw string) (Value, validation.Code) {
	if raw == "" {
		return Null(), ""
	}
	value, err := floatvalue.ParseInput(raw)
	if err != nil || !floatvalue.Finite(value) {
		return Null(), "invalid"
	}
	return Float(value), ""
}
