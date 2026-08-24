package serializers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"unicode/utf8"
)

const (
	DefaultMaxDocumentBytes = 1 << 20
	DefaultMaxDepth         = 16
	DefaultMaxValues        = 4096
	DefaultMaxObjectMembers = 1024
	DefaultMaxArrayItems    = 1024
	DefaultMaxStringBytes   = 64 << 10

	hardMaxDocumentBytes = 8 << 20
	hardMaxDepth         = 64
	hardMaxValues        = 1 << 16
	hardMaxObjectMembers = 1 << 14
	hardMaxArrayItems    = 1 << 14
	hardMaxStringBytes   = 1 << 20
)

// Limits bounds both decoding and deterministic encoding. Zero fields select
// defaults; negative or over-hard-cap fields fail closed as invalid config.
type Limits struct {
	MaxDocumentBytes int
	MaxDepth         int
	MaxValues        int
	MaxObjectMembers int
	MaxArrayItems    int
	MaxStringBytes   int
}

func DefaultLimits() Limits {
	return Limits{
		MaxDocumentBytes: DefaultMaxDocumentBytes,
		MaxDepth:         DefaultMaxDepth,
		MaxValues:        DefaultMaxValues,
		MaxObjectMembers: DefaultMaxObjectMembers,
		MaxArrayItems:    DefaultMaxArrayItems,
		MaxStringBytes:   DefaultMaxStringBytes,
	}
}

// DecodeObject decodes exactly one top-level JSON object. It rejects duplicate
// members, trailing data, floating-point values, noncanonical integers, raw
// invalid UTF-8, and every configured resource overflow.
func DecodeObject(document []byte, limits Limits) (Object, error) {
	resolved, err := resolveLimits(limits)
	if err != nil {
		return Object{}, err
	}
	if len(document) == 0 {
		return Object{}, invalidDocument("document", "JSON document is empty", nil)
	}
	if len(document) > resolved.MaxDocumentBytes {
		return Object{}, resourceLimit("document", "JSON document exceeds the configured byte limit")
	}
	if !utf8.Valid(document) {
		return Object{}, invalidDocument("document", "JSON document is not valid UTF-8", nil)
	}
	if err := rejectUnpairedSurrogateEscapes(document); err != nil {
		return Object{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	budget := decodeBudget{limits: resolved}
	value, err := decodeJSONValue(decoder, &budget, 1)
	if err != nil {
		return Object{}, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return Object{}, invalidDocument("document", "JSON document contains trailing data", nil)
		}
		return Object{}, invalidDocument("document", "JSON document contains malformed trailing data", err)
	}
	object, ok := value.AsObject()
	if !ok {
		return Object{}, invalidDocument("document", "top-level JSON value must be an object", nil)
	}
	return object, nil
}

func rejectUnpairedSurrogateEscapes(document []byte) error {
	insideString := false
	for index := 0; index < len(document); index++ {
		switch document[index] {
		case '"':
			insideString = !insideString
		case '\\':
			if !insideString || index+1 >= len(document) {
				continue
			}
			if document[index+1] != 'u' {
				index++
				continue
			}
			value, ok := decodeHexQuad(document[index+2:])
			if !ok {
				continue
			}
			index += 5
			switch {
			case value >= 0xd800 && value <= 0xdbff:
				if index+6 >= len(document) || document[index+1] != '\\' || document[index+2] != 'u' {
					return invalidDocument("document.string", "JSON string contains an unpaired UTF-16 surrogate", nil)
				}
				low, valid := decodeHexQuad(document[index+3:])
				if !valid || low < 0xdc00 || low > 0xdfff {
					return invalidDocument("document.string", "JSON string contains an unpaired UTF-16 surrogate", nil)
				}
				index += 6
			case value >= 0xdc00 && value <= 0xdfff:
				return invalidDocument("document.string", "JSON string contains an unpaired UTF-16 surrogate", nil)
			}
		}
	}
	return nil
}

func decodeHexQuad(value []byte) (uint16, bool) {
	if len(value) < 4 {
		return 0, false
	}
	var result uint16
	for index := 0; index < 4; index++ {
		digit, ok := jsonHexValue(value[index])
		if !ok {
			return 0, false
		}
		result = result<<4 | uint16(digit)
	}
	return result, true
}

func jsonHexValue(value byte) (byte, bool) {
	switch {
	case value >= '0' && value <= '9':
		return value - '0', true
	case value >= 'a' && value <= 'f':
		return value - 'a' + 10, true
	case value >= 'A' && value <= 'F':
		return value - 'A' + 10, true
	default:
		return 0, false
	}
}

type decodeBudget struct {
	limits Limits
	values int
}

func (b *decodeBudget) consumeValue(depth int) error {
	if depth > b.limits.MaxDepth {
		return resourceLimit("document.depth", "JSON nesting exceeds the configured depth limit")
	}
	b.values++
	if b.values > b.limits.MaxValues {
		return resourceLimit("document.values", "JSON value count exceeds the configured limit")
	}
	return nil
}

func decodeJSONValue(decoder *json.Decoder, budget *decodeBudget, depth int) (Value, error) {
	if err := budget.consumeValue(depth); err != nil {
		return Value{}, err
	}
	token, err := decoder.Token()
	if err != nil {
		return Value{}, invalidDocument("document", "JSON value is malformed or incomplete", err)
	}
	switch typed := token.(type) {
	case nil:
		return Null(), nil
	case string:
		if err := validateJSONString(typed, budget.limits, "document.string"); err != nil {
			return Value{}, err
		}
		return String(typed), nil
	case bool:
		return Boolean(typed), nil
	case json.Number:
		integer, err := strconv.ParseInt(typed.String(), 10, 64)
		if err != nil || strconv.FormatInt(integer, 10) != typed.String() {
			return Value{}, invalidDocument("document.number", "JSON number must be a canonical signed 64-bit integer", err)
		}
		return Integer(integer), nil
	case json.Delim:
		switch typed {
		case '{':
			return decodeJSONObject(decoder, budget, depth)
		case '[':
			return decodeJSONArray(decoder, budget, depth)
		default:
			return Value{}, invalidDocument("document", "JSON contains an unexpected closing delimiter", nil)
		}
	default:
		return Value{}, invalidDocument("document", "JSON contains an unsupported value", nil)
	}
}

func decodeJSONObject(decoder *json.Decoder, budget *decodeBudget, depth int) (Value, error) {
	members := make([]Member, 0)
	seen := make(map[string]struct{})
	for decoder.More() {
		if len(members) >= budget.limits.MaxObjectMembers {
			return Value{}, resourceLimit("document.object", "JSON object member count exceeds the configured limit")
		}
		token, err := decoder.Token()
		if err != nil {
			return Value{}, invalidDocument("document.object", "JSON object member name is malformed", err)
		}
		name, ok := token.(string)
		if !ok {
			return Value{}, invalidDocument("document.object", "JSON object member name must be a string", nil)
		}
		if err := validateJSONString(name, budget.limits, "document.object.name"); err != nil {
			return Value{}, err
		}
		if _, duplicate := seen[name]; duplicate {
			return Value{}, invalidDocument("document.object."+name, "JSON object member is duplicated", nil)
		}
		seen[name] = struct{}{}
		value, err := decodeJSONValue(decoder, budget, depth+1)
		if err != nil {
			return Value{}, err
		}
		members = append(members, MemberOf(name, value))
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return Value{}, invalidDocument("document.object", "JSON object is incomplete", err)
	}
	object, err := NewObject(members...)
	if err != nil {
		return Value{}, invalidDocument("document.object", "JSON object contains an invalid member", err)
	}
	return object.Value(), nil
}

func decodeJSONArray(decoder *json.Decoder, budget *decodeBudget, depth int) (Value, error) {
	values := make([]Value, 0)
	for decoder.More() {
		if len(values) >= budget.limits.MaxArrayItems {
			return Value{}, resourceLimit("document.array", "JSON array item count exceeds the configured limit")
		}
		value, err := decodeJSONValue(decoder, budget, depth+1)
		if err != nil {
			return Value{}, err
		}
		values = append(values, value)
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim(']') {
		return Value{}, invalidDocument("document.array", "JSON array is incomplete", err)
	}
	list, err := NewList(values...)
	if err != nil {
		return Value{}, invalidDocument("document.array", "JSON array contains an invalid value", err)
	}
	return list, nil
}

func validateJSONString(value string, limits Limits, field string) error {
	if !utf8.ValidString(value) {
		return invalidDocument(field, "JSON string is not valid UTF-8", nil)
	}
	for index := 0; index < len(value); index++ {
		if value[index] == 0 {
			return invalidDocument(field, "JSON string contains NUL", nil)
		}
	}
	if len(value) > limits.MaxStringBytes {
		return resourceLimit(field, "JSON string exceeds the configured byte limit")
	}
	return nil
}

// Encode renders one immutable Value without insignificant whitespace while
// preserving object declaration order.
func Encode(value Value, limits Limits) ([]byte, error) {
	resolved, err := resolveLimits(limits)
	if err != nil {
		return nil, err
	}
	if !value.validValue() {
		return nil, invalidValue("value", "value is zero or invalid")
	}
	state := encodeState{limits: resolved}
	if err := state.appendValue(value, 1); err != nil {
		return nil, err
	}
	return append([]byte(nil), state.document...), nil
}

// EncodeObject renders one ordered Object.
func EncodeObject(object Object, limits Limits) ([]byte, error) {
	return Encode(object.Value(), limits)
}

type encodeState struct {
	limits   Limits
	values   int
	document []byte
}

func (s *encodeState) appendValue(value Value, depth int) error {
	if depth > s.limits.MaxDepth {
		return resourceLimit("value.depth", "JSON nesting exceeds the configured depth limit")
	}
	s.values++
	if s.values > s.limits.MaxValues {
		return resourceLimit("value.values", "JSON value count exceeds the configured limit")
	}
	switch value.kind {
	case ValueNull:
		return s.appendBytes([]byte("null"))
	case ValueBoolean:
		if value.boolean {
			return s.appendBytes([]byte("true"))
		}
		return s.appendBytes([]byte("false"))
	case ValueInteger:
		return s.appendBytes(strconv.AppendInt(nil, value.integer, 10))
	case ValueString:
		if err := validateJSONString(value.string, s.limits, "value.string"); err != nil {
			return err
		}
		encoded, err := encodeJSONString(value.string)
		if err != nil {
			return invalidValueCause("value.string", "string could not be encoded", err)
		}
		return s.appendBytes(encoded)
	case ValueList:
		if len(value.list) > s.limits.MaxArrayItems {
			return resourceLimit("value.array", "JSON array item count exceeds the configured limit")
		}
		if err := s.appendBytes([]byte{'['}); err != nil {
			return err
		}
		for index := range value.list {
			if index > 0 {
				if err := s.appendBytes([]byte{','}); err != nil {
					return err
				}
			}
			if err := s.appendValue(value.list[index], depth+1); err != nil {
				return err
			}
		}
		return s.appendBytes([]byte{']'})
	case ValueObject:
		if !value.object.validObject() {
			return invalidValue("value.object", "object is invalid")
		}
		if len(value.object.members) > s.limits.MaxObjectMembers {
			return resourceLimit("value.object", "JSON object member count exceeds the configured limit")
		}
		if err := s.appendBytes([]byte{'{'}); err != nil {
			return err
		}
		for index := range value.object.members {
			if index > 0 {
				if err := s.appendBytes([]byte{','}); err != nil {
					return err
				}
			}
			member := value.object.members[index]
			if err := validateJSONString(member.name, s.limits, "value.object.name"); err != nil {
				return err
			}
			encodedName, err := encodeJSONString(member.name)
			if err != nil {
				return invalidValueCause("value.object.name", "object member name could not be encoded", err)
			}
			if err := s.appendBytes(encodedName); err != nil {
				return err
			}
			if err := s.appendBytes([]byte{':'}); err != nil {
				return err
			}
			if err := s.appendValue(member.value, depth+1); err != nil {
				return err
			}
		}
		return s.appendBytes([]byte{'}'})
	default:
		return invalidValue("value", "value kind is unsupported")
	}
}

func (s *encodeState) appendBytes(value []byte) error {
	if len(value) > s.limits.MaxDocumentBytes-len(s.document) {
		return resourceLimit("value.document", "encoded JSON exceeds the configured byte limit")
	}
	s.document = append(s.document, value...)
	return nil
}

func encodeJSONString(value string) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	encoded := buffer.Bytes()
	if len(encoded) == 0 || encoded[len(encoded)-1] != '\n' {
		return nil, errors.New("JSON string encoder omitted its terminator")
	}
	return append([]byte(nil), encoded[:len(encoded)-1]...), nil
}

func resolveLimits(limits Limits) (Limits, error) {
	defaults := DefaultLimits()
	resolved := limits
	if resolved.MaxDocumentBytes == 0 {
		resolved.MaxDocumentBytes = defaults.MaxDocumentBytes
	}
	if resolved.MaxDepth == 0 {
		resolved.MaxDepth = defaults.MaxDepth
	}
	if resolved.MaxValues == 0 {
		resolved.MaxValues = defaults.MaxValues
	}
	if resolved.MaxObjectMembers == 0 {
		resolved.MaxObjectMembers = defaults.MaxObjectMembers
	}
	if resolved.MaxArrayItems == 0 {
		resolved.MaxArrayItems = defaults.MaxArrayItems
	}
	if resolved.MaxStringBytes == 0 {
		resolved.MaxStringBytes = defaults.MaxStringBytes
	}
	if resolved.MaxDocumentBytes < 1 || resolved.MaxDocumentBytes > hardMaxDocumentBytes ||
		resolved.MaxDepth < 1 || resolved.MaxDepth > hardMaxDepth ||
		resolved.MaxValues < 1 || resolved.MaxValues > hardMaxValues ||
		resolved.MaxObjectMembers < 1 || resolved.MaxObjectMembers > hardMaxObjectMembers ||
		resolved.MaxArrayItems < 1 || resolved.MaxArrayItems > hardMaxArrayItems ||
		resolved.MaxStringBytes < 1 || resolved.MaxStringBytes > hardMaxStringBytes {
		return Limits{}, &Error{Code: CodeInvalidConfig, Field: "limits", Detail: "JSON limits are outside the supported range"}
	}
	return resolved, nil
}

func invalidDocument(field, detail string, cause error) error {
	return &Error{Code: CodeInvalidDocument, Field: field, Detail: detail, Cause: cause}
}

func resourceLimit(field, detail string) error {
	return &Error{Code: CodeResourceLimit, Field: field, Detail: detail}
}
