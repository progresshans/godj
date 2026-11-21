package serializers

import (
	"strconv"
	"unicode/utf8"

	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/internal/decimalinput"
	"github.com/progresshans/godj/validation"
)

// Decimal snapshots a finite model value. A field gives it a fixed output
// scale; standalone values encode as canonical strings.
func Decimal(value decimal.Decimal) Value {
	if !value.Valid() {
		return Value{}
	}
	return Value{kind: ValueDecimal, string: value.String(), valid: true}
}
func (value Value) AsDecimal() (decimal.Decimal, bool) {
	if !value.valid || value.kind != ValueDecimal {
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
	return makeField(name, FieldDecimal, fieldConfig{required: true, decimalDigits: maxDigits, decimalPlaces: decimalPlaces}, options)
}
func (field Field) DecimalPrecision() (int, int, bool) {
	return field.decimalDigits, field.decimalPlaces, field.kind == FieldDecimal
}

func decimalOutput(value decimal.Decimal, digits, places int) (Value, error) {
	if !value.Fits(digits, places) {
		return Value{}, invalidValue("decimal", "value exceeds field precision or scale")
	}
	text, err := value.Fixed(places)
	if err != nil {
		return Value{}, invalidValue("decimal", "invalid decimal output")
	}
	return Value{kind: ValueDecimal, string: text, valid: true}, nil
}

func cleanDecimalValue(field Field, value Value) (Value, validation.Errors) {
	if value.kind == ValueDecimal {
		number, ok := value.AsDecimal()
		if !ok {
			return Value{}, oneViolation(field.name, "invalid")
		}
		result, err := decimalOutput(number, field.decimalDigits, field.decimalPlaces)
		if err != nil {
			return Value{}, oneViolation(field.name, "invalid")
		}
		return result, validation.NewErrors()
	}
	var parsed decimalinput.Parsed
	var err error
	switch value.kind {
	case ValueString:
		raw := decimalinput.TrimSpace(value.string)
		if raw == "" && field.nullable {
			return Null(), validation.NewErrors()
		}
		if utf8.RuneCountInString(raw) > 1000 {
			return Value{}, oneViolation(field.name, "max_string_length")
		}
		parsed, err = decimalinput.Parse(raw)
	case ValueNumber:
		parsed, err = decimalinput.JSONNumber(value.string)
		if err == nil && parsed.TextLength() > 1000 {
			return Value{}, oneViolation(field.name, "max_string_length")
		}
	case ValueInteger:
		parsed, err = decimalinput.Parse(strconv.FormatInt(value.integer, 10))
	case ValueFloat:
		number, ok := value.AsFloat()
		if !ok {
			return Value{}, oneViolation(field.name, "invalid")
		}
		parsed, err = decimalinput.Float(number)
	default:
		return Value{}, oneViolation(field.name, "invalid")
	}
	if err != nil {
		return Value{}, oneViolation(field.name, "invalid")
	}
	if code := parsed.Precision(field.decimalDigits, field.decimalPlaces, false); code != "" {
		return Value{}, oneViolation(field.name, validation.Code(code))
	}
	number, err := parsed.Value()
	if err != nil {
		return Value{}, oneViolation(field.name, "invalid")
	}
	result, err := decimalOutput(number, field.decimalDigits, field.decimalPlaces)
	if err != nil {
		return Value{}, oneViolation(field.name, "invalid")
	}
	return result, validation.NewErrors()
}
