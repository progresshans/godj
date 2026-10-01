package serializers

import (
	"github.com/progresshans/godj/internal/floatvalue"
	"strconv"
)

// Number constructs an exact JSON number. Canonical signed int64 tokens use
// ValueInteger; other valid tokens retain their spelling as ValueNumber. This
// neither evaluates exponents nor silently converts a decimal to float64.
func Number(raw string) (Value, error) {
	if len(raw) > hardMaxNumberBytes {
		return Value{}, resourceLimit("value.number", "JSON number exceeds the hard byte limit")
	}
	if !validNumber(raw) {
		return Value{}, invalidValue("value.number", "expected one JSON number without whitespace")
	}
	if integer, err := strconv.ParseInt(raw, 10, 64); err == nil && strconv.FormatInt(integer, 10) == raw {
		return Integer(integer), nil
	}
	return Value{kind: ValueNumber, string: raw, valid: true}, nil
}

// AsNumber returns the exact input token or a typed number's canonical output.
// AsInteger remains strict and performs no decimal or exponent coercion.
func (v Value) AsNumber() (string, bool) {
	if !v.valid {
		return "", false
	}
	if v.kind == ValueFloat {
		value, _ := v.AsFloat()
		text, err := floatvalue.JSON(value)
		return text, err == nil
	}
	if v.kind == ValueInteger {
		return strconv.FormatInt(v.integer, 10), true
	}
	return v.string, v.kind == ValueNumber
}

func validNumber(raw string) bool {
	if raw == "" {
		return false
	}
	index := 0
	if raw[index] == '-' {
		index++
		if index == len(raw) {
			return false
		}
	}
	if raw[index] == '0' {
		index++
	} else {
		if raw[index] < '1' || raw[index] > '9' {
			return false
		}
		for index < len(raw) && raw[index] >= '0' && raw[index] <= '9' {
			index++
		}
	}
	if index < len(raw) && raw[index] == '.' {
		index++
		start := index
		for index < len(raw) && raw[index] >= '0' && raw[index] <= '9' {
			index++
		}
		if index == start {
			return false
		}
	}
	if index < len(raw) && (raw[index] == 'e' || raw[index] == 'E') {
		index++
		if index < len(raw) && (raw[index] == '+' || raw[index] == '-') {
			index++
		}
		start := index
		for index < len(raw) && raw[index] >= '0' && raw[index] <= '9' {
			index++
		}
		if index == start {
			return false
		}
	}
	return index == len(raw)
}
