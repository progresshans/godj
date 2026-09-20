package serializers

import (
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/validation"
)

// JSON snapshots a model JSON document, including a literal JSON null. Null()
// instead denotes the serializer's nullable input/model value. Both encode as
// JSON null, while defaults and writes retain their different model meaning.
// Invalid documents and the API's forbidden NUL values remain invalid Values.
func JSON(document jsonvalue.Value) Value {
	canonical, err := document.Canonical()
	if err != nil {
		return Value{}
	}
	if _, err := decodeDocument(canonical.Bytes(), jsonFieldLimits(), nil, true); err != nil {
		return Value{}
	}
	return Value{kind: ValueJSON, string: canonical.Text, valid: true}
}

func (value Value) AsJSON() (jsonvalue.Value, bool) {
	if !value.valid || value.kind != ValueJSON {
		return jsonvalue.Value{}, false
	}
	// The private immutable text was checked before this value was published.
	return jsonvalue.Value{Text: value.string}, true
}

func (values Values) JSON(name string) (jsonvalue.Value, bool) {
	value, ok := values.Get(name)
	if !ok {
		return jsonvalue.Value{}, false
	}
	return value.AsJSON()
}

func JSONField(name string, options ...FieldOption) (Field, error) {
	return makeField(name, FieldJSON, fieldConfig{required: true}, options)
}

// DecodeObject keeps the ordinary named envelope and extends only declared
// JSON payloads to arbitrary JSON object names. The single decoder budget spans
// the envelope, all JSON fields and every nested value before encapsulation.
func (spec Spec) DecodeObject(document []byte, limits Limits) (Object, error) {
	if !spec.valid {
		return Object{}, invalidConfig("spec", "serializer spec is zero or invalid")
	}
	fields := make(map[string]bool)
	for _, field := range spec.fields {
		if field.kind == FieldJSON {
			fields[field.name] = true
		}
	}
	return decodeDeclaredObject(document, limits, fields)
}

func jsonFieldLimits() Limits {
	return Limits{MaxDocumentBytes: jsonvalue.MaxDocumentBytes, MaxDepth: jsonvalue.MaxDepth,
		MaxValues: jsonvalue.MaxValues, MaxObjectMembers: jsonvalue.MaxObjectMembers,
		MaxArrayItems: jsonvalue.MaxArrayItems, MaxStringBytes: jsonvalue.MaxStringBytes,
		MaxNumberBytes: jsonvalue.MaxNumberBytes}
}

func cleanJSONValue(field Field, value Value) (Value, validation.Errors) {
	converted, err := jsonDocumentValue(value)
	if err != nil {
		return Value{}, oneViolation(field.name, "invalid")
	}
	return converted, validation.NewErrors()
}

func jsonDocumentValue(value Value) (Value, error) {
	if !value.validValue() {
		return Value{}, invalidValue("json", "JSON field contains an unsupported value")
	}
	if value.kind == ValueJSON {
		return value, nil
	}
	encoded, err := Encode(value, jsonFieldLimits())
	if err != nil {
		return Value{}, err
	}
	// Encoding bounds depth and total nodes before the type walk, even for
	// deeply nested values assembled by a Go caller.
	if !rawJSONValue(value) {
		return Value{}, invalidValue("json", "JSON field contains an unsupported value")
	}
	document, err := jsonvalue.Parse(encoded)
	if err != nil {
		return Value{}, err
	}
	// Encode checked the API's NUL/Unicode rules and budget above. The model
	// parser additionally canonicalized object order and bounded escaped size.
	return Value{kind: ValueJSON, string: document.Text, valid: true}, nil
}

func rawJSONValue(value Value) bool {
	if !value.validValue() {
		return false
	}
	switch value.kind {
	case ValueNull, ValueBoolean, ValueInteger, ValueNumber, ValueFloat, ValueString, ValueJSON:
		return true
	case ValueList:
		for _, child := range value.list {
			if !rawJSONValue(child) {
				return false
			}
		}
		return true
	case ValueObject:
		for _, member := range value.object.members {
			if !rawJSONValue(member.value) {
				return false
			}
		}
		return true
	default:
		return false
	}
}
