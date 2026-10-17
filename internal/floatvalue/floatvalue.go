package floatvalue

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ErrInvalid = errors.New("invalid binary64 value")
var ErrOverflow = errors.New("integer exceeds binary64 range")

func CanonicalBits(value float64) uint64 {
	if math.IsNaN(value) {
		return 0x7ff8000000000000
	}
	return math.Float64bits(value)
}
func Bits(value float64) string {
	const hex = "0123456789abcdef"
	bits := CanonicalBits(value)
	var text [16]byte
	for i := len(text) - 1; i >= 0; i-- {
		text[i] = hex[bits&15]
		bits >>= 4
	}
	return string(text[:])
}
func FromBits(text string) (float64, error) {
	if len(text) != 16 {
		return 0, ErrInvalid
	}
	bits, err := strconv.ParseUint(text, 16, 64)
	if err != nil {
		return 0, ErrInvalid
	}
	value := math.Float64frombits(bits)
	if Bits(value) != text {
		return 0, ErrInvalid
	}
	return value, nil
}
func Finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
func JSON(value float64) (string, error) {
	if !Finite(value) {
		return "", ErrInvalid
	}
	text := strconv.FormatFloat(value, 'g', -1, 64)
	if !strings.ContainsAny(text, ".eE") {
		text += ".0"
	}
	return text, nil
}

// ParseInput models decimal string conversion, independently of field-specific
// emptiness, finite-value admission, or transport resource limits.
func ParseInput(raw string) (float64, error) {
	if !utf8.ValidString(raw) {
		return 0, ErrInvalid
	}
	raw = strings.TrimSpace(raw)
	var normalized strings.Builder
	normalized.Grow(len(raw))
	for _, character := range raw {
		if character < utf8.RuneSelf {
			normalized.WriteByte(byte(character))
			continue
		}
		digit, ok := decimalDigit(character)
		if !ok {
			return 0, ErrInvalid
		}
		normalized.WriteByte('0' + digit)
	}
	text := normalized.String()
	unsigned := text
	negative := false
	if len(unsigned) > 0 && (unsigned[0] == '+' || unsigned[0] == '-') {
		negative = unsigned[0] == '-'
		unsigned = unsigned[1:]
	}
	switch strings.ToLower(unsigned) {
	case "nan":
		value := math.Float64frombits(0x7ff8000000000000)
		if negative {
			value = math.Copysign(value, -1)
		}
		return value, nil
	case "inf", "infinity":
		if negative {
			return math.Inf(-1), nil
		}
		return math.Inf(1), nil
	}
	index := 0
	if len(text) > 0 && (text[0] == '+' || text[0] == '-') {
		index++
	}
	before, valid := digits(text, &index)
	if !valid {
		return 0, ErrInvalid
	}
	after := false
	if index < len(text) && text[index] == '.' {
		index++
		after, valid = digits(text, &index)
	}
	if !valid || !before && !after {
		return 0, ErrInvalid
	}
	if index < len(text) && (text[index] == 'e' || text[index] == 'E') {
		index++
		if index < len(text) && (text[index] == '+' || text[index] == '-') {
			index++
		}
		exponent, ok := digits(text, &index)
		if !ok || !exponent {
			return 0, ErrInvalid
		}
	}
	if index != len(text) {
		return 0, ErrInvalid
	}
	value, err := strconv.ParseFloat(strings.ReplaceAll(text, "_", ""), 64)
	if err != nil && !(errors.Is(err, strconv.ErrRange) && math.IsInf(value, 0)) {
		return 0, ErrInvalid
	}
	return value, nil
}
func digits(text string, index *int) (seen, valid bool) {
	for *index < len(text) {
		c := text[*index]
		if c >= '0' && c <= '9' {
			seen = true
			*index++
			continue
		}
		if c == '_' {
			if !seen || *index+1 >= len(text) || text[*index+1] < '0' || text[*index+1] > '9' {
				return seen, false
			}
			*index++
			continue
		}
		break
	}
	return seen, true
}
func decimalDigit(value rune) (byte, bool) {
	for _, r := range unicode.Digit.R16 {
		if uint32(value) >= uint32(r.Lo) && uint32(value) <= uint32(r.Hi) && (uint32(value)-uint32(r.Lo))%uint32(r.Stride) == 0 {
			return byte(((uint32(value) - uint32(r.Lo)) / uint32(r.Stride)) % 10), true
		}
	}
	for _, r := range unicode.Digit.R32 {
		if uint32(value) >= r.Lo && uint32(value) <= r.Hi && (uint32(value)-r.Lo)%r.Stride == 0 {
			return byte(((uint32(value) - r.Lo) / r.Stride) % 10), true
		}
	}
	return 0, false
}

// JSONNumber receives a syntax-checked JSON number from the closed transport
// value. Bare integer tokens follow Python int-to-float conversion, including
// integer -0 becoming positive zero and integer overflow remaining an error.
func JSONNumber(raw string) (float64, error) {
	integer := !strings.ContainsAny(raw, ".eE")
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		if errors.Is(err, strconv.ErrRange) && math.IsInf(value, 0) && integer {
			return 0, ErrOverflow
		}
		return 0, ErrInvalid
	}
	if !Finite(value) {
		return 0, ErrInvalid
	}
	if integer && value == 0 {
		return 0, nil
	}
	return value, nil
}
