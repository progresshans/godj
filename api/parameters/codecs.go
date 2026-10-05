package parameters

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/api/openapi"
)

// Codec is a closed scalar conversion and its schema. Its zero value is invalid.
// Constructors defer declaration errors to New; callers cannot replace either
// half of the binding with an unrelated decoder or schema.
type Codec[T any] struct {
	decode func(string) (T, bool)
	schema func(*T) (openapi.Schema, error)
	empty  bool
}

// CanonicalInt64 accepts only strconv.FormatInt's decimal spelling, within the
// inclusive bounds: no plus sign, leading zeroes, negative zero or whitespace.
func CanonicalInt64(minimum, maximum int64) Codec[int64] {
	return integer(minimum, maximum, openapi.CanonicalDecimal)
}

// DigitsInt64 accepts nonempty ASCII digits, including leading zeroes. Its
// inclusive bounds must be nonnegative and fit int64.
func DigitsInt64(minimum, maximum int64) Codec[int64] {
	return integer(minimum, maximum, openapi.UnsignedDigits)
}

func integer(minimum, maximum int64, grammar openapi.QueryIntegerGrammar) Codec[int64] {
	return Codec[int64]{
		schema: func(fallback *int64) (openapi.Schema, error) {
			return openapi.QueryInteger(minimum, maximum, grammar, fallback)
		},
		decode: func(raw string) (int64, bool) {
			if grammar == openapi.UnsignedDigits {
				for index := range len(raw) {
					if raw[index] < '0' || raw[index] > '9' {
						return 0, false
					}
				}
			}
			value, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || value < minimum || value > maximum || grammar == openapi.CanonicalDecimal && strconv.FormatInt(value, 10) != raw {
				return 0, false
			}
			return value, true
		},
	}
}

// String accepts untrimmed UTF-8 without NUL, within the decoded byte limit.
// URL '+' decodes to a space; an encoded plus remains a literal plus.
func String(maximumBytes int, allowEmpty bool) Codec[string] {
	return Codec[string]{
		empty: allowEmpty,
		schema: func(fallback *string) (openapi.Schema, error) {
			return openapi.QueryString(maximumBytes, allowEmpty, fallback)
		},
		decode: func(raw string) (string, bool) {
			if len(raw) > maximumBytes || !allowEmpty && raw == "" || !utf8.ValidString(raw) || strings.ContainsRune(raw, 0) {
				return "", false
			}
			// Do not retain the complete request's backing string in a DTO.
			return strings.Clone(raw), true
		},
	}
}
