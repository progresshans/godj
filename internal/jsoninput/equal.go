// Package jsoninput owns JSON input comparisons without changing model storage
// or the database's equality rules.
package jsoninput

import (
	"encoding/json"
	"math/big"
	"strings"

	"github.com/progresshans/godj/jsonvalue"
)

// Equal compares form JSON values. Integer and floating tokens remain distinct,
// as do bool/number and positive/negative floating zero. Insignificant object
// order, floating scale and exponent spelling do not produce a false change.
// It never rounds through float64 or allocates an exponent-sized decimal.
func Equal(left, right jsonvalue.Value) bool {
	l, err := left.Decode()
	if err != nil {
		return false
	}
	r, err := right.Decode()
	return err == nil && equal(l, r)
}

func equal(left, right any) bool {
	switch left := left.(type) {
	case nil:
		return right == nil
	case bool:
		other, ok := right.(bool)
		return ok && left == other
	case string:
		other, ok := right.(string)
		return ok && left == other
	case json.Number:
		other, ok := right.(json.Number)
		if !ok {
			return false
		}
		l, lok := number(left.String())
		r, rok := number(other.String())
		return lok && rok && l == r
	case []any:
		other, ok := right.([]any)
		if !ok || len(left) != len(other) {
			return false
		}
		for index, value := range left {
			if !equal(value, other[index]) {
				return false
			}
		}
		return true
	case map[string]any:
		other, ok := right.(map[string]any)
		if !ok || len(left) != len(other) {
			return false
		}
		for key, value := range left {
			candidate, exists := other[key]
			if !exists || !equal(value, candidate) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

type numberKey struct {
	floating, negative    bool
	coefficient, exponent string
}

// Parse only tokens already checked by the bounded JSON value codec. Large
// exponent integers are bounded by token size, not by a machine int's range.
func number(raw string) (numberKey, bool) {
	key := numberKey{floating: strings.ContainsAny(raw, ".eE"), negative: strings.HasPrefix(raw, "-")}
	if key.negative {
		raw = raw[1:]
	}
	mantissa := raw
	var exponent big.Int
	if index := strings.IndexAny(raw, "eE"); index >= 0 {
		mantissa = raw[:index]
		if _, ok := exponent.SetString(raw[index+1:], 10); !ok {
			return numberKey{}, false
		}
	}
	whole, fraction, _ := strings.Cut(mantissa, ".")
	digits := strings.TrimLeft(whole+fraction, "0")
	if digits == "" {
		key.coefficient, key.exponent = "0", "0"
		key.negative = key.floating && key.negative
		return key, true
	}
	key.coefficient = strings.TrimRight(digits, "0")
	var adjustment big.Int
	adjustment.SetInt64(int64(len(digits) - len(key.coefficient) - len(fraction)))
	exponent.Add(&exponent, &adjustment)
	key.exponent = exponent.String()
	return key, true
}
