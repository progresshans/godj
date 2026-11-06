package wirejson

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
)

// Uint accepts only the canonical unsigned integer spelling of a JSON number.
func Uint(value any, maximum uint64) (uint64, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	return ParseUint(number.String(), maximum)
}

func ParseUint(text string, maximum uint64) (uint64, bool) {
	if text == "" || len(text) > 1 && text[0] == '0' {
		return 0, false
	}
	for _, character := range text {
		if character < '0' || character > '9' {
			return 0, false
		}
	}
	value, err := strconv.ParseUint(text, 10, 64)
	return value, err == nil && value <= maximum
}

func HasKeys[V any](object map[string]V, keys ...string) bool {
	if len(object) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, exists := object[key]; !exists {
			return false
		}
	}
	return true
}

// DecodeCanonical decodes a document already preflighted for protocol-specific
// resource bounds. Exact re-encoding rejects unknown fields, duplicate
// keys, lossy strings, alternate number spellings, and noncanonical whitespace.
func DecodeCanonical(document []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("trailing JSON value")
		}
		return err
	}
	canonical, err := json.Marshal(target)
	if err != nil {
		return err
	}
	if !bytes.Equal(canonical, document) {
		return errors.New("non-canonical JSON document")
	}
	return nil
}
