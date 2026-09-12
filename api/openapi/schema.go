// Package openapi describes the supported HTTP API surface using immutable
// OpenAPI schemas. Application and parser policies remain runtime boundaries.
package openapi

import (
	"fmt"
	"math"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/serializers"
)

// Schema is an immutable JSON Schema value. Its zero value is invalid.
// Construction uses the supported schema vocabulary rather than arbitrary maps.
// Ref constructs a local component reference; document construction checks its
// target and rejects recursive schema graphs rather than expanding references.
type Schema struct {
	value serializers.Value
}

// Value returns the immutable JSON representation of the schema.
func (s Schema) Value() serializers.Value { return s.value }

// String describes a JSON string without application-specific normalization.
func String() Schema { return schemaPrimitive("string") }

// Boolean describes a JSON boolean.
func Boolean() Schema { return schemaPrimitive("boolean") }

// Integer describes the signed 64-bit integer range used by serializers.Value.
// JSON Schema cannot restrict the lexical spelling of an integer in JSON text.
func Integer() Schema {
	// All names and values in this primitive are statically valid.
	object, _ := serializers.NewObject(
		serializers.MemberOf("type", serializers.String("integer")),
		serializers.MemberOf("format", serializers.String("int64")),
		serializers.MemberOf("minimum", serializers.Integer(math.MinInt64)),
		serializers.MemberOf("maximum", serializers.Integer(math.MaxInt64)),
	)
	return Schema{value: object.Value()}
}

func schemaPrimitive(kind string) Schema {
	// This helper is called only with the fixed JSON type names above and below.
	object, _ := serializers.NewObject(serializers.MemberOf("type", serializers.String(kind)))
	return Schema{value: object.Value()}
}

// Nullable accepts either the original schema or null. A separate branch keeps
// enum and other constraints from accidentally continuing to reject null.
func Nullable(schema Schema) (Schema, error) {
	if !schemaValid(schema) {
		return Schema{}, schemaConfigError("nullable", "schema is zero or invalid")
	}
	choices, err := serializers.NewList(schema.value, schemaPrimitive("null").value)
	if err != nil {
		return Schema{}, schemaConfigError("nullable", "nullable alternatives are invalid")
	}
	return schemaObject(serializers.MemberOf("anyOf", choices))
}

// Property declares one named member of a closed JSON object. Required controls
// presence independently of whether the member's schema accepts null.
type Property struct {
	Name     string
	Schema   Schema
	Required bool
}

// Object describes exactly the declared properties, preserving their order.
// Names must be unique, nonempty valid JSON object member names.
func Object(properties ...Property) (Schema, error) {
	members := make([]serializers.Member, 0, len(properties))
	required := make([]serializers.Value, 0, len(properties))
	names := make(map[string]struct{}, len(properties))
	for index, property := range properties {
		field := fmt.Sprintf("properties[%d]", index)
		if !schemaValid(property.Schema) {
			return Schema{}, schemaConfigError(field, "property schema is zero or invalid")
		}
		if _, exists := names[property.Name]; exists {
			return Schema{}, schemaConfigError(field, "property name is duplicated")
		}
		names[property.Name] = struct{}{}
		members = append(members, serializers.MemberOf(property.Name, property.Schema.value))
		if property.Required {
			required = append(required, serializers.String(property.Name))
		}
	}
	propertyObject, err := serializers.NewObject(members...)
	if err != nil {
		return Schema{}, schemaConfigError("properties", "property names must be nonempty valid JSON member names")
	}
	result := []serializers.Member{
		serializers.MemberOf("type", serializers.String("object")),
		serializers.MemberOf("properties", propertyObject.Value()),
		serializers.MemberOf("additionalProperties", serializers.Boolean(false)),
	}
	if len(required) != 0 {
		values, err := serializers.NewList(required...)
		if err != nil {
			return Schema{}, schemaConfigError("required", "required property names are invalid")
		}
		result = append(result, serializers.MemberOf("required", values))
	}
	return schemaObject(result...)
}

// Array describes a JSON array whose elements use one schema.
func Array(items Schema) (Schema, error) {
	if !schemaValid(items) {
		return Schema{}, schemaConfigError("items", "item schema is zero or invalid")
	}
	return schemaObject(
		serializers.MemberOf("type", serializers.String("array")),
		serializers.MemberOf("items", items.value),
	)
}

// EnumStrings describes a nonempty set of distinct JSON strings.
func EnumStrings(values ...string) (Schema, error) {
	if len(values) == 0 {
		return Schema{}, schemaConfigError("enum", "enum must contain at least one string")
	}
	seen := make(map[string]struct{}, len(values))
	choices := make([]serializers.Value, len(values))
	for index, value := range values {
		if _, exists := seen[value]; exists {
			return Schema{}, schemaConfigError(fmt.Sprintf("enum[%d]", index), "enum string is duplicated")
		}
		seen[value] = struct{}{}
		choices[index] = serializers.String(value)
	}
	list, err := serializers.NewList(choices...)
	if err != nil {
		return Schema{}, schemaConfigError("enum", "enum contains an invalid JSON string")
	}
	return schemaObject(
		serializers.MemberOf("type", serializers.String("string")),
		serializers.MemberOf("enum", list),
	)
}

// RequestSchema projects the writable fields and absence semantics used by
// Spec.Bind. Read-only fields are excluded and additional properties are denied.
// Full mode documents defaults; partial mode neither requires nor defaults any
// omitted field. Parser limits and application validation are separate policies.
//
// Strings cleaned before validation carry x-godj-normalization metadata. Its
// allowEmptyAfterTrim and maxLengthAfterTrim describe checks after Go TrimSpace;
// they are not standard JSON Schema assertions on the unnormalized input.
func RequestSchema(spec serializers.Spec, mode serializers.Mode) (Schema, error) {
	fields := spec.Fields()
	if len(fields) == 0 {
		return Schema{}, schemaConfigError("spec", "serializer spec is zero or invalid")
	}
	if mode != serializers.ModeFull && mode != serializers.ModePartial {
		return Schema{}, schemaConfigError("mode", "serializer mode is unsupported")
	}
	properties := make([]Property, 0, len(fields))
	for _, field := range fields {
		if field.ReadOnly() {
			continue
		}
		projected, err := schemaFieldType(field)
		if err != nil {
			return Schema{}, err
		}
		var annotations []serializers.Member
		if field.Kind() == serializers.FieldString {
			if field.TrimWhitespace() {
				normalization := []serializers.Member{
					serializers.MemberOf("trimWhitespace", serializers.Boolean(true)),
					serializers.MemberOf("allowEmptyAfterTrim", serializers.Boolean(field.AllowEmpty())),
				}
				if field.MaxLength() > 0 {
					normalization = append(normalization, serializers.MemberOf("maxLengthAfterTrim", serializers.Integer(int64(field.MaxLength()))))
				}
				policy, err := serializers.NewObject(normalization...)
				if err != nil {
					return Schema{}, schemaConfigError("normalization", "string normalization metadata is invalid")
				}
				annotations = append(annotations, serializers.MemberOf("x-godj-normalization", policy.Value()))
			} else {
				if !field.AllowEmpty() {
					annotations = append(annotations, serializers.MemberOf("minLength", serializers.Integer(1)))
				}
				if field.MaxLength() > 0 {
					annotations = append(annotations, serializers.MemberOf("maxLength", serializers.Integer(int64(field.MaxLength()))))
				}
			}
		}
		defaultValue, hasDefault := field.Default()
		if mode == serializers.ModeFull && hasDefault {
			annotations = append(annotations, serializers.MemberOf("default", defaultValue))
		}
		projected, err = schemaAnnotate(projected, annotations...)
		if err != nil {
			return Schema{}, err
		}
		properties = append(properties, Property{
			Name:     field.Name(),
			Schema:   projected,
			Required: mode == serializers.ModeFull && field.Required() && !hasDefault,
		})
	}
	return Object(properties...)
}

// ModelResponseSchema describes the complete allowlist emitted by ModelEncoder.
// Every selected field is present, including read-only and nullable fields.
// String length bounds describe the encoder's untrimmed Unicode character
// count. Input defaults, trimming and blank policy are not projected. Arbitrary
// application JSON is not validated merely by attaching this schema.
func ModelResponseSchema(spec serializers.Spec) (Schema, error) {
	fields := spec.Fields()
	if len(fields) == 0 {
		return Schema{}, schemaConfigError("spec", "serializer spec is zero or invalid")
	}
	properties := make([]Property, 0, len(fields))
	for _, field := range fields {
		projected, err := schemaFieldType(field)
		if err != nil {
			return Schema{}, err
		}
		var annotations []serializers.Member
		if field.ReadOnly() {
			annotations = append(annotations, serializers.MemberOf("readOnly", serializers.Boolean(true)))
		}
		if field.Kind() == serializers.FieldString && field.MaxLength() > 0 {
			annotations = append(annotations, serializers.MemberOf("maxLength", serializers.Integer(int64(field.MaxLength()))))
		}
		if len(annotations) != 0 {
			projected, err = schemaAnnotate(projected, annotations...)
			if err != nil {
				return Schema{}, err
			}
		}
		properties = append(properties, Property{Name: field.Name(), Schema: projected, Required: true})
	}
	return Object(properties...)
}

func schemaFieldType(field serializers.Field) (Schema, error) {
	var schema Schema
	switch field.Kind() {
	case serializers.FieldString:
		schema = String()
	case serializers.FieldBoolean:
		schema = Boolean()
	case serializers.FieldInteger:
		schema = Integer()
	default:
		return Schema{}, schemaConfigError("field", "serializer field kind is unsupported")
	}
	if field.Nullable() {
		return Nullable(schema)
	}
	return schema, nil
}

func schemaAnnotate(schema Schema, annotations ...serializers.Member) (Schema, error) {
	object, ok := schema.value.AsObject()
	if !ok {
		return Schema{}, schemaConfigError("annotation", "schema is zero or invalid")
	}
	return schemaObject(append(object.Members(), annotations...)...)
}

func schemaObject(members ...serializers.Member) (Schema, error) {
	object, err := serializers.NewObject(members...)
	if err != nil {
		return Schema{}, schemaConfigError("value", "schema JSON is invalid")
	}
	return Schema{value: object.Value()}, nil
}

func schemaValid(schema Schema) bool {
	object, ok := schema.value.AsObject()
	return ok && object.Valid()
}

func schemaConfigError(field, detail string) error {
	return &api.Error{Code: api.FailureInvalidConfig, Field: "schema." + field, Detail: detail}
}
