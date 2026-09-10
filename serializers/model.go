package serializers

import (
	"unicode/utf8"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// ModelField explicitly selects a field for a JSON representation. Storage
// kind, nullability, length and default come from Schema IR; exposure and API
// required/empty policy remain application choices. Auto keys are read-only.
type ModelField struct {
	Name       string
	ReadOnly   bool
	Optional   bool
	AllowEmpty bool
}

// ModelEncoder binds an immutable serializer allowlist to owned model metadata.
// The caller supplies only a model value when encoding each row. The reader
// receives a detached field, so it cannot change subsequent projections.
type ModelEncoder[M any] struct {
	spec   Spec
	fields []ir.Field
	read   func(M, ir.Field) (query.Value, bool)
}

func NewModelEncoder[M any](spec Spec, model ir.Model, read func(M, ir.Field) (query.Value, bool)) (ModelEncoder[M], error) {
	if !spec.valid || read == nil {
		return ModelEncoder[M]{}, invalidConfig("model", "invalid projection")
	}
	byName := make(map[string]ir.Field, len(model.Fields))
	for _, field := range model.Fields {
		if _, duplicate := byName[field.Name]; duplicate {
			return ModelEncoder[M]{}, invalidConfig("model", "duplicate field")
		}
		byName[field.Name] = field
	}
	fields := make([]ir.Field, len(spec.fields))
	for index, field := range spec.fields {
		metadata, found := byName[field.name]
		if !found {
			return ModelEncoder[M]{}, invalidConfig("model."+field.name, "unknown field")
		}
		fields[index] = metadata.Clone()
	}
	return ModelEncoder[M]{spec: spec, fields: fields, read: read}, nil
}

// Encode validates output types, nullability and lengths without applying
// input trimming/defaults or retaining the model and its mutable pointers.
func (encoder ModelEncoder[M]) Encode(value M) (Value, error) {
	if encoder.read == nil {
		return Value{}, invalidConfig("model", "invalid projection")
	}
	members := make([]Member, 0, len(encoder.fields))
	for index, metadata := range encoder.fields {
		field := encoder.spec.fields[index]
		scalar, found := encoder.read(value, metadata.Clone())
		if !found {
			return Value{}, invalidValue(field.name, "missing model value")
		}
		var converted Value
		switch scalar.Kind() {
		case query.ValueNull:
			converted = Null()
		case query.ValueString:
			text, _ := scalar.String()
			converted = String(text)
		case query.ValueBoolean:
			boolean, _ := scalar.Boolean()
			converted = Boolean(boolean)
		case query.ValueInteger:
			integer, _ := scalar.Integer()
			converted = Integer(integer)
		default:
			return Value{}, invalidValue(field.name, "invalid scalar")
		}
		if !valueMatchesField(converted, field.kind, field.nullable) {
			return Value{}, invalidValue(field.name, "model value type mismatch")
		}
		if converted.kind == ValueString && field.maxLength > 0 && utf8.RuneCountInString(converted.string) > field.maxLength {
			return Value{}, invalidValue(field.name, "model value exceeds maximum length")
		}
		members = append(members, MemberOf(field.name, converted))
	}
	object, err := NewObject(members...)
	if err != nil {
		return Value{}, err
	}
	return object.Value(), nil
}

// FromModel creates a serializer from an explicit allowlist. It never discovers
// or exposes new fields automatically, accesses model values, or performs I/O.
func FromModel(model ir.Model, selected ...ModelField) (Spec, error) {
	byName := make(map[string]ir.Field, len(model.Fields))
	for _, field := range model.Fields {
		if _, exists := byName[field.Name]; exists {
			return Spec{}, invalidConfig("model."+field.Name, "duplicate model field")
		}
		byName[field.Name] = field
	}
	fields := make([]Field, 0, len(selected))
	for _, selection := range selected {
		field, found := byName[selection.Name]
		if !found {
			return Spec{}, invalidConfig("model."+selection.Name, "unknown model field")
		}
		options := []FieldOption{}
		if selection.ReadOnly || field.PrimaryKey {
			options = append(options, WithReadOnly())
		}
		if selection.Optional {
			options = append(options, WithRequired(false))
		}
		if field.Nullable {
			options = append(options, WithNullable())
		}
		if selection.AllowEmpty {
			options = append(options, WithAllowEmpty())
		}
		if field.Default != nil && !selection.ReadOnly && !field.PrimaryKey {
			switch field.Default.Kind {
			case ir.ScalarString:
				options = append(options, WithDefault(String(field.Default.String)))
			case ir.ScalarBoolean:
				options = append(options, WithDefault(Boolean(field.Default.Boolean)))
			case ir.ScalarInteger:
				options = append(options, WithDefault(Integer(field.Default.Integer)))
			default:
				return Spec{}, invalidConfig("model."+field.Name, "unsupported model default")
			}
		}
		var projected Field
		var err error
		switch field.Kind {
		case ir.FieldChar:
			options = append(options, WithMaxLength(field.MaxLength))
			projected, err = StringField(field.Name, options...)
		case ir.FieldBoolean:
			projected, err = BooleanField(field.Name, options...)
		case ir.FieldAuto, ir.FieldForeignKey:
			projected, err = IntegerField(field.Name, options...)
		default:
			return Spec{}, invalidConfig("model."+field.Name, "unsupported model field")
		}
		if err != nil {
			return Spec{}, err
		}
		fields = append(fields, projected)
	}
	return NewSpec(fields)
}
