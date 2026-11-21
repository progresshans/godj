// Package binaryvalue represents owned bytes independently of a database
// driver, encoded input, or file storage. Empty bytes are distinct from SQL NULL.
package binaryvalue

import (
	"encoding/base64"
	"errors"
	"strings"
)

const MaxBytes = 1 << 20

// MaxEncodedBytes bounds padded base64 before decoding or allocating a buffer.
const MaxEncodedBytes = (MaxBytes + 2) / 3 * 4

var ErrInvalid = errors.New("invalid binary value")
var ErrLimit = errors.New("binary value exceeds supported limits")

// Value contains the original bytes in an immutable Go string. Data may contain
// NUL and invalid UTF-8; it is never text to be implicitly stored or JSON encoded.
// The zero value is valid empty bytes. A nullable model uses a nil *Value for
// SQL NULL. Boundary validation also covers values constructed with a literal.
type Value struct{ Data string }

// FromBytes snapshots the input; later caller mutations cannot change the value.
func FromBytes(data []byte) (Value, error) {
	if len(data) > MaxBytes {
		return Value{}, ErrLimit
	}
	return Value{Data: string(data)}, nil
}

// Parse accepts standard padded base64, including nonzero unused pad bits as
// Django's strict base64 decoder does. It rejects whitespace, alternate alphabets,
// missing/excess padding and trailing data instead of ignoring those bytes.
func Parse(text string) (Value, error) {
	if len(text) > MaxEncodedBytes {
		return Value{}, ErrLimit
	}
	if len(text)%4 != 0 || strings.ContainsAny(text, "\r\n") {
		return Value{}, ErrInvalid
	}
	data, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		return Value{}, ErrInvalid
	}
	return FromBytes(data)
}

func (value Value) Canonical() (Value, error) {
	if len(value.Data) > MaxBytes {
		return Value{}, ErrLimit
	}
	return value, nil
}

func (value Value) Valid() bool { return len(value.Data) <= MaxBytes }

// Bytes returns a fresh non-nil slice even for empty bytes. Drivers must not
// interpret the empty value as the separate SQL NULL state.
func (value Value) Bytes() []byte { return []byte(value.Data) }

// Base64 returns canonical padded base64 for a value already validated by its
// enclosing boundary. It is a transport representation, not the stored value.
func (value Value) Base64() string { return base64.StdEncoding.EncodeToString([]byte(value.Data)) }

// Compare orders bytes lexicographically as unsigned octets on both backends.
func (value Value) Compare(other Value) int { return strings.Compare(value.Data, other.Data) }
