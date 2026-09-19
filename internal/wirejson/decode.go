// Package wirejson owns strict JSON framing and bounded traversal primitives.
// Command protocols retain their envelopes, limits, and public failure policy.
package wirejson

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

var (
	ErrResource = errors.New("JSON wire resource limit exceeded")
	ErrDepth    = errors.New("JSON wire nesting limit exceeded")
)

// Limits describes one protocol's allocation and traversal bounds. Bytes and
// at least one depth limit are required. Other zero limits are unused.
// ValueDepth counts edges from the root to every value, including scalars;
// Containers counts only nested arrays/objects, starting at one for the root.
// They are separate because the current protocols use both conventions.
type Limits struct {
	Bytes        int
	ValueDepth   int
	Containers   int
	Values       int
	ObjectKeys   int
	ArrayValues  int
	StringBytes  int
	KeyBytes     int
	RejectArrays bool
	RejectNull   bool
}

func (limits Limits) valid() bool {
	if limits.Bytes <= 0 || limits.ValueDepth == 0 && limits.Containers == 0 {
		return false
	}
	for _, value := range []int{limits.ValueDepth, limits.Containers, limits.Values, limits.ObjectKeys, limits.ArrayValues, limits.StringBytes, limits.KeyBytes} {
		if value < 0 {
			return false
		}
	}
	return true
}

// DecodeObject retains one bounded object. It rejects duplicate keys, trailing
// values, invalid UTF-8 and lossy Unicode escapes before publishing any result.
func DecodeObject(document []byte, limits Limits) (map[string]any, error) {
	value, err := decode(document, limits, true)
	if err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("JSON wire root is not an object")
	}
	return object, nil
}

// Scan checks the same framing and traversal bounds without retaining the
// value tree. Canonical typed decoders can therefore preflight a large document
// before allocating its schema-sized slices. Scan permits any root value.
func Scan(document []byte, limits Limits) error {
	_, err := decode(document, limits, false)
	return err
}

func decode(document []byte, limits Limits, retain bool) (any, error) {
	if !limits.valid() {
		return nil, errors.New("invalid JSON wire limits")
	}
	if len(document) > limits.Bytes {
		return nil, ErrResource
	}
	if !utf8.Valid(document) || !validSurrogateEscapes(document) {
		return nil, errors.New("invalid JSON wire framing")
	}
	parser := traversal{decoder: NewDecoder(document), limits: limits, retain: retain}
	value, err := parser.read(0)
	if err != nil {
		return nil, err
	}
	if _, err := parser.decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("trailing JSON wire value")
		}
		return nil, err
	}
	return value, nil
}

// NewDecoder preserves the lexical number spelling for closed schema parsers.
func NewDecoder(document []byte) *json.Decoder {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	return decoder
}

type traversal struct {
	decoder *json.Decoder
	limits  Limits
	values  int
	retain  bool
}

func (parser *traversal) read(depth int) (any, error) {
	limits := parser.limits
	if limits.ValueDepth > 0 && depth > limits.ValueDepth {
		return nil, ErrDepth
	}
	if limits.Values > 0 {
		if parser.values >= limits.Values {
			return nil, ErrResource
		}
		parser.values++
	}
	token, err := parser.decoder.Token()
	if err != nil {
		return nil, err
	}
	if token == nil && limits.RejectNull {
		return nil, errors.New("JSON null is not permitted")
	}
	if value, ok := token.(string); ok && limits.StringBytes > 0 && len(value) > limits.StringBytes {
		return nil, ErrResource
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return token, nil
	}
	if limits.Containers > 0 && depth+1 > limits.Containers {
		return nil, ErrDepth
	}
	switch delimiter {
	case '{':
		object := make(map[string]any)
		for parser.decoder.More() {
			if limits.ObjectKeys > 0 && len(object) >= limits.ObjectKeys {
				return nil, ErrResource
			}
			token, err := parser.decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := token.(string)
			if !ok {
				return nil, errors.New("JSON wire object key is not a string")
			}
			if limits.KeyBytes > 0 && len(key) > limits.KeyBytes {
				return nil, ErrResource
			}
			if _, exists := object[key]; exists {
				return nil, errors.New("duplicate JSON wire object key")
			}
			child, err := parser.read(depth + 1)
			if err != nil {
				return nil, err
			}
			if !parser.retain {
				child = nil
			}
			object[key] = child
		}
		if closing, err := parser.decoder.Token(); err != nil || closing != json.Delim('}') {
			return nil, errors.New("invalid JSON wire object close")
		}
		if !parser.retain {
			return nil, nil
		}
		return object, nil
	case '[':
		if limits.RejectArrays {
			return nil, errors.New("JSON wire arrays are not permitted")
		}
		array := make([]any, 0)
		for count := 0; parser.decoder.More(); count++ {
			if limits.ArrayValues > 0 && count >= limits.ArrayValues {
				return nil, ErrResource
			}
			child, err := parser.read(depth + 1)
			if err != nil {
				return nil, err
			}
			if parser.retain {
				array = append(array, child)
			}
		}
		if closing, err := parser.decoder.Token(); err != nil || closing != json.Delim(']') {
			return nil, errors.New("invalid JSON wire array close")
		}
		if !parser.retain {
			return nil, nil
		}
		return array, nil
	default:
		return nil, errors.New("invalid JSON wire delimiter")
	}
}

// encoding/json substitutes U+FFFD for unpaired UTF-16 surrogate escapes.
// Reject those escapes before decoding so distinct identity bytes cannot
// collapse onto a legitimate U+FFFD. The decoder owns all other JSON syntax.
func validSurrogateEscapes(document []byte) bool {
	inString := false
	for index := 0; index < len(document); index++ {
		switch document[index] {
		case '"':
			inString = !inString
		case '\\':
			if !inString || index+1 >= len(document) {
				continue
			}
			if document[index+1] != 'u' {
				index++
				continue
			}
			unit, ok := hexCodeUnit(document, index+2)
			if !ok {
				return true // The JSON decoder rejects incomplete/non-hex escapes.
			}
			switch {
			case unit >= 0xd800 && unit <= 0xdbff:
				if index+7 >= len(document) || document[index+6] != '\\' || document[index+7] != 'u' {
					return false
				}
				low, paired := hexCodeUnit(document, index+8)
				if !paired || low < 0xdc00 || low > 0xdfff {
					return false
				}
				index += 11
			case unit >= 0xdc00 && unit <= 0xdfff:
				return false
			default:
				index += 5
			}
		}
	}
	return true
}

func hexCodeUnit(document []byte, offset int) (uint16, bool) {
	if offset < 0 || offset > len(document)-4 {
		return 0, false
	}
	var value uint16
	for _, character := range document[offset : offset+4] {
		value <<= 4
		switch {
		case character >= '0' && character <= '9':
			value |= uint16(character - '0')
		case character >= 'a' && character <= 'f':
			value |= uint16(character-'a') + 10
		case character >= 'A' && character <= 'F':
			value |= uint16(character-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}
