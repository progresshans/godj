package protocol

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"time"
)

type ValueType string

const (
	ValueNull     ValueType = "null"
	ValueBool     ValueType = "bool"
	ValueInt      ValueType = "int"
	ValueString   ValueType = "string"
	ValueDecimal  ValueType = "decimal"
	ValueDatetime ValueType = "datetime"
	ValueUUID     ValueType = "uuid"
	ValueBytes    ValueType = "bytes"
	ValuePK       ValueType = "pk"
	ValueList     ValueType = "list"
	ValueObject   ValueType = "object"
)

// Value is the recursive, typed value used by normalized observations. Its
// JSON representation is a tagged union; callers should construct values with
// the constructors below or decode them from JSON and then call Validate.
type Value struct {
	Type     ValueType
	Bool     *bool
	Text     *string
	Encoding string
	Nested   *Value
	Items    []Value
	Fields   []NamedValue
}

type NamedValue struct {
	Name  string `json:"name"`
	Value Value  `json:"value"`
}

func Null() Value {
	return Value{Type: ValueNull}
}

func Boolean(value bool) Value {
	return Value{Type: ValueBool, Bool: &value}
}

func Integer(value string) Value {
	return Value{Type: ValueInt, Text: &value}
}

func String(value string) Value {
	return Value{Type: ValueString, Text: &value}
}

func Decimal(value string) Value {
	return Value{Type: ValueDecimal, Text: &value}
}

func Datetime(value string) Value {
	return Value{Type: ValueDatetime, Text: &value}
}

func UUID(value string) Value {
	return Value{Type: ValueUUID, Text: &value}
}

func Bytes(value []byte) Value {
	encoded := base64.StdEncoding.EncodeToString(value)
	return Value{Type: ValueBytes, Text: &encoded, Encoding: "base64"}
}

func PrimaryKey(value Value) Value {
	return Value{Type: ValuePK, Nested: &value}
}

func List(items ...Value) Value {
	if items == nil {
		items = []Value{}
	}
	return Value{Type: ValueList, Items: items}
}

// Object constructs an object from a Go map and sorts fields by name. This is
// the only place where map iteration enters the value model.
func Object(fields map[string]Value) Value {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	named := make([]NamedValue, 0, len(names))
	for _, name := range names {
		named = append(named, NamedValue{Name: name, Value: fields[name]})
	}
	return Value{Type: ValueObject, Fields: named}
}

var (
	canonicalInteger = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)
	canonicalDecimal = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`)
	canonicalUUID    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

func (v Value) Validate() error {
	scalarText := func(kind ValueType, validate func(string) error) error {
		if v.Text == nil {
			return fmt.Errorf("%s value is missing", kind)
		}
		if validate != nil {
			if err := validate(*v.Text); err != nil {
				return err
			}
		}
		return v.validateUnused(kind, false, true, false, false, false)
	}

	switch v.Type {
	case ValueNull:
		return v.validateUnused(ValueNull, false, false, false, false, false)
	case ValueBool:
		if v.Bool == nil {
			return fmt.Errorf("bool value is missing")
		}
		return v.validateUnused(ValueBool, true, false, false, false, false)
	case ValueInt:
		return scalarText(ValueInt, func(value string) error {
			if !canonicalInteger.MatchString(value) || value == "-0" {
				return fmt.Errorf("int value %q is not canonical base-10", value)
			}
			return nil
		})
	case ValueString:
		return scalarText(ValueString, nil)
	case ValueDecimal:
		return scalarText(ValueDecimal, func(value string) error {
			if !canonicalDecimal.MatchString(value) {
				return fmt.Errorf("decimal value %q is not canonical base-10", value)
			}
			return nil
		})
	case ValueDatetime:
		return scalarText(ValueDatetime, func(value string) error {
			if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
				return fmt.Errorf("datetime value %q must be RFC3339 with an offset: %w", value, err)
			}
			return nil
		})
	case ValueUUID:
		return scalarText(ValueUUID, func(value string) error {
			if !canonicalUUID.MatchString(value) {
				return fmt.Errorf("uuid value %q is not canonical lowercase UUID text", value)
			}
			return nil
		})
	case ValueBytes:
		if v.Encoding != "base64" {
			return fmt.Errorf("bytes encoding must be base64")
		}
		if v.Text == nil {
			return fmt.Errorf("bytes value is missing")
		}
		decoded, err := base64.StdEncoding.DecodeString(*v.Text)
		if err != nil {
			return fmt.Errorf("bytes value is not base64: %w", err)
		}
		if base64.StdEncoding.EncodeToString(decoded) != *v.Text {
			return fmt.Errorf("bytes value is not canonical padded base64")
		}
		if err := v.validateUnused(ValueBytes, false, true, true, false, false); err != nil {
			return err
		}
		return nil
	case ValuePK:
		if v.Nested == nil {
			return fmt.Errorf("pk value is missing")
		}
		if err := v.validateUnused(ValuePK, false, false, false, true, false); err != nil {
			return err
		}
		if !v.Nested.isNonNullScalar() {
			return fmt.Errorf("pk must contain one non-null scalar value")
		}
		if err := v.Nested.Validate(); err != nil {
			return fmt.Errorf("pk value: %w", err)
		}
		return nil
	case ValueList:
		if v.Items == nil {
			return fmt.Errorf("list items are missing")
		}
		if err := v.validateUnused(ValueList, false, false, false, false, true); err != nil {
			return err
		}
		for index := range v.Items {
			if err := v.Items[index].Validate(); err != nil {
				return fmt.Errorf("list item %d: %w", index, err)
			}
		}
		return nil
	case ValueObject:
		if v.Fields == nil {
			return fmt.Errorf("object fields are missing")
		}
		if err := v.validateUnused(ValueObject, false, false, false, false, false); err != nil {
			return err
		}
		previous := ""
		for index := range v.Fields {
			field := v.Fields[index]
			if field.Name == "" {
				return fmt.Errorf("object field %d has an empty name", index)
			}
			if index > 0 && field.Name <= previous {
				return fmt.Errorf("object fields must be sorted and unique: %q follows %q", field.Name, previous)
			}
			if err := field.Value.Validate(); err != nil {
				return fmt.Errorf("object field %q: %w", field.Name, err)
			}
			previous = field.Name
		}
		return nil
	default:
		return fmt.Errorf("unknown value type %q", v.Type)
	}
}

func (v Value) validateUnused(kind ValueType, allowBool, allowText, allowEncoding, allowNested, allowItems bool) error {
	if !allowBool && v.Bool != nil {
		return fmt.Errorf("%s cannot contain bool storage", kind)
	}
	if !allowText && v.Text != nil {
		return fmt.Errorf("%s cannot contain text storage", kind)
	}
	if !allowEncoding && v.Encoding != "" {
		return fmt.Errorf("%s cannot contain encoding", kind)
	}
	if !allowNested && v.Nested != nil {
		return fmt.Errorf("%s cannot contain nested value", kind)
	}
	if !allowItems && v.Items != nil {
		return fmt.Errorf("%s cannot contain items", kind)
	}
	if kind != ValueObject && v.Fields != nil {
		return fmt.Errorf("%s cannot contain fields", kind)
	}
	return nil
}

func (v Value) isNonNullScalar() bool {
	switch v.Type {
	case ValueBool, ValueInt, ValueString, ValueDecimal, ValueDatetime, ValueUUID, ValueBytes:
		return true
	default:
		return false
	}
}

func (v Value) MarshalJSON() ([]byte, error) {
	if err := v.Validate(); err != nil {
		return nil, err
	}
	switch v.Type {
	case ValueNull:
		return json.Marshal(struct {
			Type ValueType `json:"type"`
		}{Type: v.Type})
	case ValueBool:
		return json.Marshal(struct {
			Type  ValueType `json:"type"`
			Value bool      `json:"value"`
		}{Type: v.Type, Value: *v.Bool})
	case ValueInt, ValueString, ValueDecimal, ValueDatetime, ValueUUID:
		return json.Marshal(struct {
			Type  ValueType `json:"type"`
			Value string    `json:"value"`
		}{Type: v.Type, Value: *v.Text})
	case ValueBytes:
		return json.Marshal(struct {
			Type     ValueType `json:"type"`
			Encoding string    `json:"encoding"`
			Value    string    `json:"value"`
		}{Type: v.Type, Encoding: v.Encoding, Value: *v.Text})
	case ValuePK:
		return json.Marshal(struct {
			Type  ValueType `json:"type"`
			Value Value     `json:"value"`
		}{Type: v.Type, Value: *v.Nested})
	case ValueList:
		return json.Marshal(struct {
			Type  ValueType `json:"type"`
			Items []Value   `json:"items"`
		}{Type: v.Type, Items: v.Items})
	case ValueObject:
		return json.Marshal(struct {
			Type   ValueType    `json:"type"`
			Fields []NamedValue `json:"fields"`
		}{Type: v.Type, Fields: v.Fields})
	default:
		return nil, fmt.Errorf("unknown value type %q", v.Type)
	}
}

func (v *Value) UnmarshalJSON(data []byte) error {
	if v == nil {
		return fmt.Errorf("cannot unmarshal value into nil receiver")
	}
	var discriminator struct {
		Type ValueType `json:"type"`
	}
	if err := json.Unmarshal(data, &discriminator); err != nil {
		return err
	}
	if discriminator.Type == "" {
		return fmt.Errorf("value type is missing")
	}

	var decoded Value
	switch discriminator.Type {
	case ValueNull:
		var wire struct {
			Type ValueType `json:"type"`
		}
		if err := decodeStrictBytes(data, &wire); err != nil {
			return err
		}
		decoded = Null()
	case ValueBool:
		var wire struct {
			Type  ValueType `json:"type"`
			Value *bool     `json:"value"`
		}
		if err := decodeStrictBytes(data, &wire); err != nil {
			return err
		}
		decoded = Value{Type: wire.Type, Bool: wire.Value}
	case ValueInt, ValueString, ValueDecimal, ValueDatetime, ValueUUID:
		var wire struct {
			Type  ValueType `json:"type"`
			Value *string   `json:"value"`
		}
		if err := decodeStrictBytes(data, &wire); err != nil {
			return err
		}
		decoded = Value{Type: wire.Type, Text: wire.Value}
	case ValueBytes:
		var wire struct {
			Type     ValueType `json:"type"`
			Encoding string    `json:"encoding"`
			Value    *string   `json:"value"`
		}
		if err := decodeStrictBytes(data, &wire); err != nil {
			return err
		}
		decoded = Value{Type: wire.Type, Encoding: wire.Encoding, Text: wire.Value}
	case ValuePK:
		var wire struct {
			Type  ValueType `json:"type"`
			Value *Value    `json:"value"`
		}
		if err := decodeStrictBytes(data, &wire); err != nil {
			return err
		}
		decoded = Value{Type: wire.Type, Nested: wire.Value}
	case ValueList:
		var wire struct {
			Type  ValueType `json:"type"`
			Items *[]Value  `json:"items"`
		}
		if err := decodeStrictBytes(data, &wire); err != nil {
			return err
		}
		decoded.Type = wire.Type
		if wire.Items != nil {
			decoded.Items = *wire.Items
		}
	case ValueObject:
		var wire struct {
			Type   ValueType     `json:"type"`
			Fields *[]NamedValue `json:"fields"`
		}
		if err := decodeStrictBytes(data, &wire); err != nil {
			return err
		}
		decoded.Type = wire.Type
		if wire.Fields != nil {
			decoded.Fields = *wire.Fields
		}
	default:
		return fmt.Errorf("unknown value type %q", discriminator.Type)
	}

	if err := decoded.Validate(); err != nil {
		return err
	}
	*v = decoded
	return nil
}

func decodeStrictBytes(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return fmt.Errorf("invalid trailing JSON: %w", err)
	}
	return nil
}
