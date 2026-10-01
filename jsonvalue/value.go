// Package jsonvalue represents owned JSON documents without floating-point
// conversion. SQL NULL, field configuration, and backend capabilities remain
// separate from the JSON null value.
package jsonvalue

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/progresshans/godj/internal/wirejson"
)

const (
	MaxDocumentBytes = 1 << 20
	MaxDepth         = 64
	MaxValues        = 1 << 16
	MaxObjectMembers = 1 << 14
	MaxArrayItems    = 1 << 14
	MaxStringBytes   = 1 << 20
	MaxNumberBytes   = 4096
)

var ErrInvalid = errors.New("invalid JSON value")
var ErrLimit = errors.New("JSON value exceeds supported limits")

// Value contains no mutable storage. Text is a JSON document, not a Go string
// to be implicitly encoded as a JSON string. Literals are validated and
// canonicalized at schema, query, mutation, and representation boundaries.
// The zero value is invalid; Null() is the JSON null value. A nullable model
// uses a nil *Value for SQL NULL, separately from a pointer to Null().
type Value struct {
	Text string
}

func Null() Value { return Value{Text: "null"} }

// Parse accepts exactly one bounded JSON value. Object member order and
// insignificant whitespace are normalized deterministically. Number tokens
// retain all digits and their original integer/fraction/exponent spelling.
func Parse(document []byte) (Value, error) {
	decoded, err := decode(document)
	if err != nil {
		return Value{}, err
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		return Value{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return Value{Text: string(canonical)}, nil
}

func (value Value) Canonical() (Value, error) { return Parse([]byte(value.Text)) }
func (value Value) Valid() bool {
	_, err := value.Canonical()
	return err == nil
}

// Bytes returns an owned copy. Text/String are immutable Go strings; neither
// exposes a retained input buffer or a mutable decoded tree.
func (value Value) Bytes() []byte  { return []byte(value.Text) }
func (value Value) String() string { return value.Text }

// Decode returns a fresh tree containing only nil, bool, string, json.Number,
// []any, and map[string]any. Callers own every mutable container. A JSON string
// containing JSON syntax remains a string and is not decoded a second time.
func (value Value) Decode() (any, error) { return decode([]byte(value.Text)) }

func decode(document []byte) (any, error) {
	decoded, err := wirejson.DecodeValue(document, wirejson.Limits{
		Bytes: MaxDocumentBytes, ValueDepth: MaxDepth, Values: MaxValues,
		ObjectKeys: MaxObjectMembers, ArrayValues: MaxArrayItems,
		StringBytes: MaxStringBytes, KeyBytes: MaxStringBytes,
	})
	if err != nil {
		if errors.Is(err, wirejson.ErrResource) || errors.Is(err, wirejson.ErrDepth) {
			return nil, fmt.Errorf("%w: %w", ErrLimit, err)
		}
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	// Bound the canonical result before allocating a document-sized encoding.
	// Escaping can grow a valid input, and a numeric token has its own limit.
	if !measure(wirejson.NewSizer(MaxDocumentBytes), decoded) {
		return nil, ErrLimit
	}
	return decoded, nil
}

func measure(size *wirejson.Sizer, value any) bool {
	switch value := value.(type) {
	case nil:
		return size.Literal("null")
	case bool:
		return size.Boolean(value)
	case string:
		return size.String(value)
	case json.Number:
		return len(value.String()) <= MaxNumberBytes && size.Literal(value.String())
	case []any:
		if !size.Add(2) {
			return false
		}
		for index, child := range value {
			if index > 0 && !size.Add(1) || !measure(size, child) {
				return false
			}
		}
		return true
	case map[string]any:
		if !size.Add(2) {
			return false
		}
		index := 0
		for key, child := range value {
			if index > 0 && !size.Add(1) || !size.String(key) || !size.Add(1) || !measure(size, child) {
				return false
			}
			index++
		}
		return true
	default:
		return false
	}
}
