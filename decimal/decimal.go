// Package decimal represents finite decimal values without binary floating-point
// conversion. Declared field precision and transport grammar remain separate.
package decimal

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const MaxDigits = 1000
const MaxAdjustedExponent = 1000
const MaxInputBytes = 4096

var ErrInvalid = errors.New("invalid finite decimal")
var ErrRange = errors.New("decimal exceeds the supported precision or exponent range")
var ErrScale = errors.New("decimal cannot be represented at the requested scale without rounding")

// Decimal is an exact, comparable coefficient times ten to Exponent. Components
// contain no mutable storage. The zero value is positive zero; "-0" preserves a
// negative zero before a backend applies its native numeric zero semantics.
// New, Parse and Canonical normalize insignificant coefficient zeros. Invalid
// literals are errors at query, mutation, schema and serialization boundaries.
type Decimal struct {
	Coefficient string
	Exponent    int32
}

func New(coefficient string, exponent int32) (Decimal, error) {
	return (Decimal{Coefficient: coefficient, Exponent: exponent}).Canonical()
}

func (value Decimal) Canonical() (Decimal, error) {
	text := value.Coefficient
	if text == "" {
		if value.Exponent != 0 {
			return Decimal{}, ErrInvalid
		}
		return Decimal{}, nil
	}
	if len(text) > MaxInputBytes {
		return Decimal{}, ErrRange
	}
	negative := text[0] == '-'
	if negative {
		text = text[1:]
	}
	if text == "" {
		return Decimal{}, ErrInvalid
	}
	for _, c := range []byte(text) {
		if c < '0' || c > '9' {
			return Decimal{}, ErrInvalid
		}
	}
	text = strings.TrimLeft(text, "0")
	if text == "" {
		if negative {
			return Decimal{Coefficient: "-0"}, nil
		}
		return Decimal{}, nil
	}
	adjusted := int64(value.Exponent) + int64(len(text)) - 1
	if adjusted < -MaxAdjustedExponent || adjusted > MaxAdjustedExponent {
		return Decimal{}, ErrRange
	}
	digits := strings.TrimRight(text, "0")
	if len(digits) > MaxDigits {
		return Decimal{}, ErrRange
	}
	exponent := adjusted - int64(len(digits)) + 1
	if negative {
		digits = "-" + digits
	}
	return Decimal{Coefficient: digits, Exponent: int32(exponent)}, nil
}

func (value Decimal) Valid() bool {
	_, err := value.Canonical()
	return err == nil
}

// Parse accepts finite ASCII decimal/exponent text and returns a canonical
// value. Whitespace, Unicode digits and underscores belong to Form/API input.
func Parse(raw string) (Decimal, error) {
	if len(raw) == 0 {
		return Decimal{}, ErrInvalid
	}
	if len(raw) > MaxInputBytes {
		return Decimal{}, ErrRange
	}
	index := 0
	negative := raw[0] == '-'
	if negative || raw[0] == '+' {
		index++
	}
	start := index
	for index < len(raw) && raw[index] >= '0' && raw[index] <= '9' {
		index++
	}
	coefficient := raw[start:index]
	fraction := 0
	if index < len(raw) && raw[index] == '.' {
		index++
		start = index
		for index < len(raw) && raw[index] >= '0' && raw[index] <= '9' {
			index++
		}
		fraction = index - start
		coefficient += raw[start:index]
	}
	if coefficient == "" {
		return Decimal{}, ErrInvalid
	}
	var exponent int64
	if index < len(raw) && (raw[index] == 'e' || raw[index] == 'E') {
		index++
		start = index
		if index < len(raw) && (raw[index] == '+' || raw[index] == '-') {
			index++
		}
		digitStart := index
		for index < len(raw) && raw[index] >= '0' && raw[index] <= '9' {
			index++
		}
		if index == digitStart || index != len(raw) {
			return Decimal{}, ErrInvalid
		}
		parsed, err := strconv.ParseInt(raw[start:index], 10, 32)
		if err != nil {
			return Decimal{}, ErrRange
		}
		exponent = parsed
	}
	if index != len(raw) {
		return Decimal{}, ErrInvalid
	}
	exponent -= int64(fraction)
	if negative {
		coefficient = "-" + coefficient
	}
	if exponent < -1<<31 || exponent > 1<<31-1 {
		return Decimal{}, ErrRange
	}
	return New(coefficient, int32(exponent))
}

func (value Decimal) String() string {
	canonical, err := value.Canonical()
	if err != nil {
		return "invalid decimal"
	}
	negative, digits := canonical.parts()
	text := digits
	if canonical.Exponent >= 0 {
		text += strings.Repeat("0", int(canonical.Exponent))
	} else if position := len(digits) + int(canonical.Exponent); position > 0 {
		text = digits[:position] + "." + digits[position:]
	} else {
		text = "0." + strings.Repeat("0", -position) + digits
	}
	if negative {
		text = "-" + text
	}
	return text
}

func (value Decimal) parts() (bool, string) {
	if value.Coefficient == "" {
		return false, "0"
	}
	if value.Coefficient[0] == '-' {
		return true, value.Coefficient[1:]
	}
	return false, value.Coefficient
}

// Fits applies the declared storage domain to the numeric value, without
// rounding or confusing submitted trailing zeros with stored precision.
func (value Decimal) Fits(maxDigits, decimalPlaces int) bool {
	if maxDigits < 1 || maxDigits > MaxDigits || decimalPlaces < 0 || decimalPlaces > maxDigits {
		return false
	}
	canonical, err := value.Canonical()
	if err != nil {
		return false
	}
	_, digits := canonical.parts()
	if digits == "0" {
		return true
	}
	whole := max(0, len(digits)+int(canonical.Exponent))
	places := max(0, -int(canonical.Exponent))
	return whole <= maxDigits-decimalPlaces && places <= decimalPlaces
}

// Fixed formats a value at a declared scale. It never rounds an excess digit.
func (value Decimal) Fixed(decimalPlaces int) (string, error) {
	if decimalPlaces < 0 || decimalPlaces > MaxDigits {
		return "", ErrRange
	}
	canonical, err := value.Canonical()
	if err != nil {
		return "", err
	}
	if int64(canonical.Exponent) < -int64(decimalPlaces) {
		return "", ErrScale
	}
	text := canonical.String()
	if decimalPlaces == 0 {
		return text, nil
	}
	_, tail, present := strings.Cut(text, ".")
	if !present {
		text += "."
	}
	return text + strings.Repeat("0", decimalPlaces-len(tail)), nil
}

// Compare uses numeric equality, including positive and negative zero.
func (value Decimal) Compare(other Decimal) (int, error) {
	left, err := value.Canonical()
	if err != nil {
		return 0, err
	}
	right, err := other.Canonical()
	if err != nil {
		return 0, err
	}
	ln, ld := left.parts()
	rn, rd := right.parts()
	if ld == "0" && rd == "0" {
		return 0, nil
	}
	if ld == "0" {
		if rn {
			return 1, nil
		}
		return -1, nil
	}
	if rd == "0" {
		if ln {
			return -1, nil
		}
		return 1, nil
	}
	if ln != rn {
		if ln {
			return -1, nil
		}
		return 1, nil
	}
	le, re := len(ld)+int(left.Exponent), len(rd)+int(right.Exponent)
	comparison := 0
	if le < re {
		comparison = -1
	} else if le > re {
		comparison = 1
	} else {
		comparison = strings.Compare(ld, rd)
	}
	if ln {
		comparison = -comparison
	}
	return comparison, nil
}

func (value Decimal) Equal(other Decimal) bool {
	comparison, err := value.Compare(other)
	return err == nil && comparison == 0
}
func (value Decimal) MarshalText() ([]byte, error) {
	if !value.Valid() {
		return nil, ErrInvalid
	}
	return []byte(value.String()), nil
}
func (value *Decimal) UnmarshalText(raw []byte) error {
	if value == nil {
		return ErrInvalid
	}
	parsed, err := Parse(string(raw))
	if err != nil {
		return err
	}
	*value = parsed
	return nil
}
func (value Decimal) MarshalJSON() ([]byte, error) {
	text, err := value.MarshalText()
	if err != nil {
		return nil, err
	}
	return json.Marshal(string(text))
}
func (value *Decimal) UnmarshalJSON(raw []byte) error {
	if value == nil {
		return ErrInvalid
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return fmt.Errorf("%w: expected a decimal string", ErrInvalid)
	}
	return value.UnmarshalText([]byte(text))
}
