package serializers

import (
	"maps"
	"unicode/utf8"
)

// ComputedField adds an explicitly typed, read-only representation value. Read
// must derive only from its supplied snapshot, without I/O or retained state.
// A missing value is an execution error, never an omitted response member.
type ComputedField[M any] struct {
	Field Field
	Read  func(M) (Value, bool)
}

// WithComputed returns an independent projection with the additional fields
// appended in declaration order. It does not change storage metadata or input
// cleaning. Stored/relation names cannot be replaced, even when not selected.
func (encoder ModelEncoder[M]) WithComputed(fields ...ComputedField[M]) (ModelEncoder[M], error) {
	if encoder.read == nil || !encoder.spec.valid {
		return ModelEncoder[M]{}, invalidConfig("model", "invalid projection")
	}
	result := encoder
	result.computed = maps.Clone(encoder.computed)
	if result.computed == nil {
		result.computed = make(map[string]func(M) (Value, bool))
	}
	selected := encoder.spec.Fields()
	for _, computed := range fields {
		field := computed.Field
		if !field.valid || !field.readOnly || computed.Read == nil {
			return ModelEncoder[M]{}, invalidConfig("model.computed", "a read-only field and value reader are required")
		}
		if _, exists := encoder.modelNames[field.name]; exists {
			return ModelEncoder[M]{}, invalidConfig("model."+field.name, "computed field conflicts with storage metadata")
		}
		if _, exists := result.computed[field.name]; exists {
			return ModelEncoder[M]{}, invalidConfig("model."+field.name, "computed field is duplicated")
		}
		selected = append(selected, field)
		result.computed[field.name] = computed.Read
	}
	var err error
	result.spec, err = NewSpec(selected)
	if err != nil {
		return ModelEncoder[M]{}, err
	}
	return result, nil
}

// Spec is the exact immutable representation declaration, including computed
// fields. Consumers such as OpenAPI derive their output contract from it.
func (encoder ModelEncoder[M]) Spec() Spec { return encoder.spec }

func computedOutputValue(field Field, value Value) (Value, error) {
	value, err := outputValue(field, value)
	if err != nil {
		return Value{}, err
	}
	if field.kind == FieldDecimal && !value.IsNull() {
		decimal, ok := value.AsDecimal()
		if !ok {
			return Value{}, invalidValue(field.name, "invalid decimal representation value")
		}
		value, err = decimalOutput(decimal, field.decimalDigits, field.decimalPlaces)
		if err != nil {
			return Value{}, invalidValue(field.name, "decimal representation value exceeds field precision or scale")
		}
	}
	return value, nil
}

func outputValue(field Field, value Value) (Value, error) {
	if !value.validValue() || !valueMatchesField(value, field.kind, field.nullable) {
		return Value{}, invalidValue(field.name, "representation value type mismatch")
	}
	if value.kind == ValueString && field.maxLength > 0 && utf8.RuneCountInString(value.string) > field.maxLength {
		return Value{}, invalidValue(field.name, "representation value exceeds maximum length")
	}
	return value, nil
}
