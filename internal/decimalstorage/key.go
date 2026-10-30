// Package decimalstorage owns the exact, field-independent SQLite order key.
// PostgreSQL uses its native NUMERIC codec instead of this representation.
package decimalstorage

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"

	"github.com/progresshans/godj/decimal"
)

var ErrInvalid = errors.New("invalid decimal order key")

// Encode preserves exact numeric order with SQLite's native BLOB comparison.
// Both signs of zero map to one numeric zero, matching native DB equality.
func Encode(value decimal.Decimal) ([]byte, error) {
	value, err := value.Canonical()
	if err != nil {
		return nil, err
	}
	if value.Coefficient == "" || value.Coefficient == "-0" {
		return []byte{1}, nil
	}
	negative := strings.HasPrefix(value.Coefficient, "-")
	digits := strings.TrimPrefix(value.Coefficient, "-")
	out := make([]byte, 6+len(digits))
	out[0] = 2
	adjusted := value.Exponent + int32(len(digits)) - 1
	binary.BigEndian.PutUint32(out[1:5], uint32(adjusted)^0x80000000)
	copy(out[5:], digits)
	if negative {
		out[0] = 0
		for i := 1; i < len(out); i++ {
			out[i] = ^out[i]
		}
	}
	return out, nil
}

// Decode rejects truncated, over-budget and noncanonical physical values. It
// copies byte storage and never guesses a Decimal from a native float/string.
func Decode(raw []byte) (decimal.Decimal, error) {
	if bytes.Equal(raw, []byte{1}) {
		return decimal.Decimal{}, nil
	}
	if len(raw) < 7 || len(raw) > decimal.MaxDigits+6 || raw[0] != 0 && raw[0] != 2 {
		return decimal.Decimal{}, ErrInvalid
	}
	data := bytes.Clone(raw)
	negative := data[0] == 0
	if negative {
		for i := 1; i < len(data); i++ {
			data[i] = ^data[i]
		}
	}
	if data[len(data)-1] != 0 {
		return decimal.Decimal{}, ErrInvalid
	}
	digits := string(data[5 : len(data)-1])
	if digits[0] == '0' || digits[len(digits)-1] == '0' {
		return decimal.Decimal{}, ErrInvalid
	}
	adjusted := int64(int32(binary.BigEndian.Uint32(data[1:5]) ^ 0x80000000))
	if adjusted < -decimal.MaxAdjustedExponent || adjusted > decimal.MaxAdjustedExponent {
		return decimal.Decimal{}, ErrInvalid
	}
	exponent := int32(adjusted - int64(len(digits)) + 1)
	if negative {
		digits = "-" + digits
	}
	value, err := decimal.New(digits, exponent)
	if err != nil {
		return decimal.Decimal{}, ErrInvalid
	}
	encoded, err := Encode(value)
	if err != nil || !bytes.Equal(encoded, raw) {
		return decimal.Decimal{}, ErrInvalid
	}
	return value, nil
}
