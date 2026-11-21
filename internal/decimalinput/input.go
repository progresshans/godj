// Package decimalinput retains submitted decimal precision until validation.
// The pinned Django/DRF observations are in internal/decimaltest/testdata.
package decimalinput

import (
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/progresshans/godj/decimal"
)

// Parsed owns a finite decimal's original digits and exponent. Leading zeros
// do not count as significant digits, but submitted trailing zeros do.
type Parsed struct {
	digits   string
	exponent int64
	negative bool
}

// TrimSpace follows Python str.strip, including the ASCII information separators.
func TrimSpace(raw string) string {
	return strings.TrimFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f })
}

func Parse(raw string) (Parsed, error) {
	if len(raw) > decimal.MaxInputBytes || !utf8.ValidString(raw) {
		return Parsed{}, decimal.ErrInvalid
	}
	var normalized strings.Builder
	normalized.Grow(len(raw))
	for _, character := range TrimSpace(raw) {
		if character == '_' {
			continue
		}
		if character < utf8.RuneSelf {
			normalized.WriteByte(byte(character))
			continue
		}
		digit, ok := decimalDigit(character)
		if !ok {
			return Parsed{}, decimal.ErrInvalid
		}
		normalized.WriteByte('0' + digit)
	}
	text := normalized.String()
	if text == "" {
		return Parsed{}, decimal.ErrInvalid
	}
	index := 0
	negative := text[0] == '-'
	if negative || text[0] == '+' {
		index++
	}
	start := index
	for index < len(text) && text[index] >= '0' && text[index] <= '9' {
		index++
	}
	digits := text[start:index]
	fraction := 0
	if index < len(text) && text[index] == '.' {
		index++
		start = index
		for index < len(text) && text[index] >= '0' && text[index] <= '9' {
			index++
		}
		fraction = index - start
		digits += text[start:index]
	}
	if digits == "" {
		return Parsed{}, decimal.ErrInvalid
	}
	var exponent int64
	if index < len(text) && (text[index] == 'e' || text[index] == 'E') {
		index++
		start = index
		if index < len(text) && (text[index] == '+' || text[index] == '-') {
			index++
		}
		digitStart := index
		for index < len(text) && text[index] >= '0' && text[index] <= '9' {
			index++
		}
		if index == digitStart || index != len(text) {
			return Parsed{}, decimal.ErrInvalid
		}
		var err error
		exponent, err = strconv.ParseInt(text[start:index], 10, 64)
		if err != nil {
			return Parsed{}, decimal.ErrInvalid
		}
	}
	if index != len(text) {
		return Parsed{}, decimal.ErrInvalid
	}
	// Check the pinned decimal constructor's finite exponent domain before
	// subtraction or digit arithmetic. These checks never expand powers of ten.
	const maxExponent int64 = 999999999999999999
	const minExponent int64 = -1999999999999999997
	if exponent < minExponent+int64(fraction) || exponent > maxExponent+int64(fraction) {
		return Parsed{}, decimal.ErrInvalid
	}
	exponent -= int64(fraction)
	digits = strings.TrimLeft(digits, "0")
	if digits == "" {
		digits = "0"
	}
	if exponent+int64(len(digits))-1 > maxExponent {
		return Parsed{}, decimal.ErrInvalid
	}
	return Parsed{digits: digits, exponent: exponent, negative: negative}, nil
}

// JSONNumber receives a syntax-checked JSON token. Bare integer -0 follows
// Python int's positive zero; decimal/exponent tokens preserve their zero sign.
func JSONNumber(raw string) (Parsed, error) {
	value, err := Parse(raw)
	if err == nil && value.digits == "0" && !strings.ContainsAny(raw, ".eE") {
		value.negative = false
	}
	return value, err
}

// Float is an explicitly typed binary64 input, following DRF's str(float)
// conversion. JSON ingress uses JSONNumber and never takes this route.
func Float(value float64) (Parsed, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return Parsed{}, decimal.ErrInvalid
	}
	text := strconv.FormatFloat(value, 'g', -1, 64)
	parsed, err := Parse(text)
	if err != nil {
		return Parsed{}, err
	}
	adjusted := parsed.exponent + int64(len(parsed.digits)) - 1
	if adjusted >= -4 && adjusted < 16 {
		text = strconv.FormatFloat(value, 'f', -1, 64)
		if !strings.Contains(text, ".") {
			text += ".0"
		}
	}
	return Parse(text)
}

// Precision preserves Django Form's zero/positive-exponent exception, while
// the serializer counts that exponent as DRF does. Error precedence is fixed.
func (value Parsed) Precision(maxDigits, places int, form bool) string {
	if value.digits == "" || maxDigits < 1 || maxDigits > decimal.MaxDigits || places < 0 || places > maxDigits {
		return "invalid"
	}
	digits, decimals := int64(len(value.digits)), int64(0)
	if value.exponent >= 0 {
		if !form || value.digits != "0" {
			digits += value.exponent
		}
	} else {
		decimals = -value.exponent
		digits = max(digits, decimals)
	}
	if digits > int64(maxDigits) {
		return "max_digits"
	}
	if decimals > int64(places) {
		return "max_decimal_places"
	}
	if digits-decimals > int64(maxDigits-places) {
		return "max_whole_digits"
	}
	return ""
}

func (value Parsed) Value() (decimal.Decimal, error) {
	if value.digits == "" {
		return decimal.Decimal{}, decimal.ErrInvalid
	}
	if value.digits == "0" {
		if value.negative {
			return decimal.Decimal{Coefficient: "-0"}, nil
		}
		return decimal.Decimal{}, nil
	}
	if value.exponent < -1<<31 || value.exponent > 1<<31-1 {
		return decimal.Decimal{}, decimal.ErrRange
	}
	digits := value.digits
	if value.negative {
		digits = "-" + digits
	}
	return decimal.New(digits, int32(value.exponent))
}

// TextLength measures Python Decimal's string form without allocating large
// expansions. DRF applies its string guard to that form for JSON Decimal input.
func (value Parsed) TextLength() int64 {
	if value.digits == "" {
		return 0
	}
	digits := int64(len(value.digits))
	adjusted := digits + value.exponent - 1
	length := digits
	if value.exponent > 0 || adjusted < -6 {
		if digits > 1 {
			length++
		}
		length += 1 + int64(len(strconv.FormatInt(adjusted, 10)))
		if adjusted >= 0 {
			length++
		}
	} else if value.exponent < 0 {
		if digits+value.exponent > 0 {
			length++
		} else {
			length = 2 - value.exponent
		}
	}
	if value.negative {
		length++
	}
	return length
}

func decimalDigit(value rune) (byte, bool) {
	for _, span := range unicode.Digit.R16 {
		if uint32(value) >= uint32(span.Lo) && uint32(value) <= uint32(span.Hi) && (uint32(value)-uint32(span.Lo))%uint32(span.Stride) == 0 {
			return byte(((uint32(value) - uint32(span.Lo)) / uint32(span.Stride)) % 10), true
		}
	}
	for _, span := range unicode.Digit.R32 {
		if uint32(value) >= span.Lo && uint32(value) <= span.Hi && (uint32(value)-span.Lo)%span.Stride == 0 {
			return byte(((uint32(value) - span.Lo) / span.Stride) % 10), true
		}
	}
	return 0, false
}
