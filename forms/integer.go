package forms

import (
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/progresshans/godj/validation"
)

// cleanInteger follows the decimal string behavior of Django 6.1's integer
// form field (sign, decimal digits, integer underscores, and a zero-only
// fractional suffix), bounded to signed int64 as its BigIntegerField form is.
// It never allocates a big integer or rounds through floating point. Unicode
// decimal digits use Go's Unicode tables; JSON has its separate strict parser.
func cleanInteger(raw string) (Value, validation.Code) {
	if !utf8.ValidString(raw) {
		return Null(), "invalid"
	}
	if raw == "" {
		return Null(), ""
	}
	fraction := strings.TrimRightFunc(raw, integerRegexWhitespace)
	if point := strings.LastIndexByte(fraction, '.'); point >= 0 {
		for _, character := range fraction[point+1:] {
			if character != '0' {
				return Null(), "invalid"
			}
		}
		raw = fraction[:point]
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Null(), "invalid"
	}
	negative := false
	if len(raw) != 0 && (raw[0] == '+' || raw[0] == '-') {
		negative = raw[0] == '-'
		raw = raw[1:]
	}
	limit := uint64(math.MaxInt64)
	if negative {
		limit++
	}
	var value uint64
	previousDigit, overflow := false, false
	for _, character := range raw {
		if character == '_' {
			if !previousDigit {
				return Null(), "invalid"
			}
			previousDigit = false
			continue
		}
		digit, ok := decimalDigit(character)
		if !ok {
			return Null(), "invalid"
		}
		previousDigit = true
		if value > (limit-uint64(digit))/10 {
			overflow = true
		} else if !overflow {
			value = value*10 + uint64(digit)
		}
	}
	if !previousDigit {
		return Null(), "invalid"
	}
	if overflow {
		if negative {
			return Null(), "min_value"
		}
		return Null(), "max_value"
	}
	if negative {
		if value == uint64(math.MaxInt64)+1 {
			return Integer(math.MinInt64), ""
		}
		return Integer(-int64(value)), ""
	}
	return Integer(int64(value)), ""
}

func integerRegexWhitespace(value rune) bool {
	return unicode.IsSpace(value) || value >= 0x1c && value <= 0x1f
}

func decimalDigit(value rune) (uint8, bool) {
	if value >= '0' && value <= '9' {
		return uint8(value - '0'), true
	}
	for _, group := range unicode.Digit.R16 {
		if uint32(value) >= uint32(group.Lo) && uint32(value) <= uint32(group.Hi) &&
			(uint32(value)-uint32(group.Lo))%uint32(group.Stride) == 0 {
			return uint8(((uint32(value) - uint32(group.Lo)) / uint32(group.Stride)) % 10), true
		}
	}
	for _, group := range unicode.Digit.R32 {
		if uint32(value) >= group.Lo && uint32(value) <= group.Hi && (uint32(value)-group.Lo)%group.Stride == 0 {
			return uint8(((uint32(value) - group.Lo) / group.Stride) % 10), true
		}
	}
	return 0, false
}
