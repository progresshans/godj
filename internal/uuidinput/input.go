// Package uuidinput implements the pinned public UUID input grammar. Model
// values and stored UUIDs use the stricter uuid package instead.
package uuidinput

import (
	"math/big"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/progresshans/godj/uuid"
)

// TrimSpace follows the Form's Python str.strip boundary. The UUID serializer
// does not apply it before parsing; whitespace can affect the required length.
func TrimSpace(raw string) string {
	return strings.TrimFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f })
}

// Parse follows UUID(hex=...) spelling conversion, including its public
// aliases. Every transformation is linear; numeric work starts only after
// checking the fixed 32-code-point boundary.
func Parse(raw string) (uuid.UUID, error) {
	if !utf8.ValidString(raw) {
		return uuid.UUID{}, uuid.ErrInvalid
	}
	raw = strings.ReplaceAll(strings.ReplaceAll(raw, "urn:", ""), "uuid:", "")
	raw = strings.ReplaceAll(strings.Trim(raw, "{}"), "-", "")
	if utf8.RuneCountInString(raw) != 32 {
		return uuid.UUID{}, uuid.ErrInvalid
	}
	var normalized strings.Builder
	for _, character := range raw {
		if digit, ok := decimalDigit(character); ok {
			normalized.WriteByte('0' + digit)
		} else if unicode.IsSpace(character) {
			normalized.WriteByte(' ')
		} else if character < utf8.RuneSelf {
			normalized.WriteByte(byte(character))
		} else {
			return uuid.UUID{}, uuid.ErrInvalid
		}
	}
	text := strings.TrimSpace(normalized.String())
	text = strings.TrimPrefix(text, "+")
	prefix := strings.HasPrefix(text, "0x") || strings.HasPrefix(text, "0X")
	if prefix {
		text = text[2:]
		text = strings.TrimPrefix(text, "_")
	}
	var digits strings.Builder
	previousDigit := false
	for _, character := range text {
		if character == '_' {
			if !previousDigit {
				return uuid.UUID{}, uuid.ErrInvalid
			}
			previousDigit = false
			continue
		}
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f' || character >= 'A' && character <= 'F') {
			return uuid.UUID{}, uuid.ErrInvalid
		}
		digits.WriteByte(byte(character))
		previousDigit = true
	}
	if !previousDigit || digits.Len() > 32 {
		return uuid.UUID{}, uuid.ErrInvalid
	}
	return uuid.Parse(strings.Repeat("0", 32-digits.Len()) + digits.String())
}

// Integer accepts exact JSON integer tokens in the unsigned 128-bit range.
// Decimal points and exponents retain their floating-token meaning and fail.
// -0 is the one valid signed spelling; zero remains a present UUID value.
func Integer(raw string) (uuid.UUID, error) {
	if raw == "-0" {
		return uuid.UUID{}, nil
	}
	if raw == "" || len(raw) > 39 || len(raw) > 1 && raw[0] == '0' {
		return uuid.UUID{}, uuid.ErrInvalid
	}
	for _, digit := range raw {
		if digit < '0' || digit > '9' {
			return uuid.UUID{}, uuid.ErrInvalid
		}
	}
	number, ok := new(big.Int).SetString(raw, 10)
	if !ok || number.BitLen() > 128 {
		return uuid.UUID{}, uuid.ErrInvalid
	}
	var value uuid.UUID
	number.FillBytes(value[:])
	return value, nil
}

// decimalZeroes pins Python 3.14's Unicode 16.0 decimal repertoire. The
// independently observed public model/Form/serializer values live in
// internal/uuidtest/testdata/django61.json; Go currently ships Unicode 15.
var decimalZeroes = [...]rune{
	0x30, 0x660, 0x6f0, 0x7c0, 0x966, 0x9e6, 0xa66, 0xae6,
	0xb66, 0xbe6, 0xc66, 0xce6, 0xd66, 0xde6, 0xe50, 0xed0,
	0xf20, 0x1040, 0x1090, 0x17e0, 0x1810, 0x1946, 0x19d0, 0x1a80,
	0x1a90, 0x1b50, 0x1bb0, 0x1c40, 0x1c50, 0xa620, 0xa8d0, 0xa900,
	0xa9d0, 0xa9f0, 0xaa50, 0xabf0, 0xff10, 0x104a0, 0x10d30, 0x10d40,
	0x11066, 0x110f0, 0x11136, 0x111d0, 0x112f0, 0x11450, 0x114d0, 0x11650,
	0x116c0, 0x116d0, 0x116da, 0x11730, 0x118e0, 0x11950, 0x11bf0, 0x11c50,
	0x11d50, 0x11da0, 0x11f50, 0x16130, 0x16a60, 0x16ac0, 0x16b50, 0x16d70,
	0x1ccf0, 0x1d7ce, 0x1d7d8, 0x1d7e2, 0x1d7ec, 0x1d7f6, 0x1e140, 0x1e2f0,
	0x1e4f0, 0x1e5f1, 0x1e950, 0x1fbf0,
}

func decimalDigit(value rune) (byte, bool) {
	for _, zero := range decimalZeroes {
		if value < zero {
			break
		}
		if value <= zero+9 {
			return byte(value - zero), true
		}
	}
	return 0, false
}
