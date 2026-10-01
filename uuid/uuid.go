// Package uuid provides a copyable UUID value independent of a database driver.
// Every 128-bit pattern is valid; the zero UUID is distinct from a nullable
// field's missing value. Generation policy belongs to the caller.
package uuid

import (
	"bytes"
	"encoding/hex"
	"errors"
)

// UUID stores the canonical big-endian bytes. Assignments and method results
// copy the value; it owns no mutable slice or pointer.
type UUID [16]byte

var ErrInvalid = errors.New("invalid UUID")

// Parse accepts 32 hexadecimal digits or the 8-4-4-4-12 hyphenated spelling.
// ASCII hexadecimal letters are case-insensitive. Form/API compatibility
// conversion is a separate input boundary from this model-value parser.
func Parse(text string) (UUID, error) {
	var digits [32]byte
	switch len(text) {
	case 32:
		copy(digits[:], text)
	case 36:
		position := 0
		for index := range len(text) {
			if index == 8 || index == 13 || index == 18 || index == 23 {
				if text[index] != '-' {
					return UUID{}, ErrInvalid
				}
				continue
			}
			digits[position] = text[index]
			position++
		}
	default:
		return UUID{}, ErrInvalid
	}
	var value UUID
	if _, err := hex.Decode(value[:], digits[:]); err != nil {
		return UUID{}, ErrInvalid
	}
	return value, nil
}

// FromBytes copies exactly 16 canonical bytes, retaining none of the input.
func FromBytes(data []byte) (UUID, error) {
	if len(data) != 16 {
		return UUID{}, ErrInvalid
	}
	var value UUID
	copy(value[:], data)
	return value, nil
}

func (value UUID) Bytes() [16]byte { return [16]byte(value) }

// Hex returns the lowercase 32-digit spelling used by SQLite storage.
func (value UUID) Hex() string {
	var result [32]byte
	hex.Encode(result[:], value[:])
	return string(result[:])
}

// String returns the canonical lowercase, hyphenated UUID spelling.
func (value UUID) String() string {
	var result [36]byte
	hex.Encode(result[0:8], value[0:4])
	result[8] = '-'
	hex.Encode(result[9:13], value[4:6])
	result[13] = '-'
	hex.Encode(result[14:18], value[6:8])
	result[18] = '-'
	hex.Encode(result[19:23], value[8:10])
	result[23] = '-'
	hex.Encode(result[24:36], value[10:16])
	return string(result[:])
}

// Compare orders all UUIDs as unsigned 128-bit values.
func (value UUID) Compare(other UUID) int { return bytes.Compare(value[:], other[:]) }
