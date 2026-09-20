package forms

import (
	"strconv"
	"strings"

	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/internal/decimalinput"
	"github.com/progresshans/godj/validation"
)

func Decimal(value decimal.Decimal) Value {
	if !value.Valid() {
		return Value{kind: ValueKind(255)}
	}
	return Value{kind: ValueDecimal, string: value.String()}
}
func (value Value) AsDecimal() (decimal.Decimal, bool) {
	if value.kind != ValueDecimal {
		return decimal.Decimal{}, false
	}
	number, err := decimal.Parse(value.string)
	return number, err == nil
}
func (values Values) Decimal(name string) (decimal.Decimal, bool) {
	value, ok := values.Get(name)
	if !ok {
		return decimal.Decimal{}, false
	}
	return value.AsDecimal()
}

func DecimalField(name string, maxDigits, decimalPlaces int, options ...FieldOption) (Field, error) {
	config := fieldConfig{label: name, required: true, widget: NumberInput, decimalDigits: maxDigits, decimalPlaces: decimalPlaces}
	for _, option := range options {
		if option == nil {
			return Field{}, &ConfigError{Path: "fields." + name, Code: "nil_option"}
		}
		option.apply(&config)
	}
	return makeField(name, FieldDecimal, config)
}
func (field Field) DecimalPrecision() (int, int, bool) {
	return field.decimalDigits, field.decimalPlaces, field.kind == FieldDecimal
}
func (field Field) NumberStep() string {
	if field.widget != NumberInput {
		return ""
	}
	if field.kind == FieldFloat {
		return "any"
	}
	if field.kind != FieldDecimal {
		return ""
	}
	if field.decimalPlaces == 0 {
		return "1"
	}
	if field.decimalPlaces > 6 {
		return "1e-" + strconv.Itoa(field.decimalPlaces)
	}
	return "0." + strings.Repeat("0", field.decimalPlaces-1) + "1"
}
func cleanDecimal(field Field, raw string) (Value, validation.Code) {
	if raw == "" {
		return Null(), ""
	}
	parsed, err := decimalinput.Parse(raw)
	if err != nil {
		return Null(), "invalid"
	}
	if code := parsed.Precision(field.decimalDigits, field.decimalPlaces, true); code != "" {
		return Null(), validation.Code(code)
	}
	value, err := parsed.Value()
	if err != nil {
		return Null(), "invalid"
	}
	return Decimal(value), ""
}
func changedDecimal(raw string, initial Value) bool {
	if raw == "" {
		return !initial.IsNull()
	}
	parsed, err := decimalinput.Parse(raw)
	if err != nil {
		return true
	}
	value, err := parsed.Value()
	if err != nil {
		return true
	}
	return !Decimal(value).Equal(initial)
}
