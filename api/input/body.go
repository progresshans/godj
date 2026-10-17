// Package input connects an immutable serializer Spec to typed request DTOs
// and its OpenAPI request schemas. The Spec owns input meaning; handlers own
// authentication, error presentation, relationships and persistence.
package input

import (
	"context"
	"fmt"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

// Presence distinguishes an effective value from absence. In full mode a
// serializer default is present; partial omission remains absent. With a
// Nullable codec, Get returns (nil, true) for model null and (nil, false) for
// absence. A non-null zero value is still present. The zero Presence is absent.
type Presence[T any] struct {
	value   T
	present bool
}

func (value Presence[T]) Get() (T, bool) { return value.value, value.present }

// Property binds one writable serializer field to a typed DTO setter. Setters
// must be pure, safe for concurrent requests, and must not retain the DTO pointer.
// New never calls them. Bind calls them in Spec order, including absent fields,
// only after all validation and value conversions succeed.
type Property[T any] struct {
	name    string
	matches func(serializers.Field) bool
	decode  func(serializers.Value, bool) (func(*T), bool)
}

func Field[T, V any](name string, codec Codec[V], assign func(*T, Presence[V])) Property[T] {
	property := Property[T]{name: name}
	if codec.accepts == nil || codec.read == nil || assign == nil {
		return property
	}
	property.matches = func(field serializers.Field) bool {
		return codec.accepts(field.Kind()) && codec.nullable == field.Nullable()
	}
	property.decode = func(value serializers.Value, present bool) (func(*T), bool) {
		var result Presence[V]
		if present {
			decoded, ok := codec.read(value)
			if !ok {
				return nil, false
			}
			result = Presence[V]{value: decoded, present: true}
		}
		return func(destination *T) { assign(destination, result) }, true
	}
	return property
}

// Body is an immutable prepared binding. Copies share only immutable
// declarations; each successful request owns its DTO, pointers and list slices.
// Scalar value objects containing strings can share immutable text. The zero
// Body is invalid. Reuse is concurrent subject to the setter contract.
type Body[T any] struct {
	spec          serializers.Spec
	parser        api.Parser
	properties    []Property[T]
	full, partial openapi.Schema
	valid         bool
}

// New requires exactly one compatible binding for each writable Spec field.
// Read-only fields stay in the Spec for validation but cannot have DTO setters.
// Parser limits and both full/partial schemas are prepared before use. For model
// inputs, construct the Spec with serializers.FromModel so Schema IR owns meaning.
func New[T any](spec serializers.Spec, config api.ParserConfig, properties ...Property[T]) (Body[T], error) {
	fields := spec.Fields()
	if len(fields) == 0 {
		return Body[T]{}, configError("spec", "serializer spec is zero or invalid")
	}
	byName := make(map[string]Property[T], len(properties))
	metadata := make(map[string]serializers.Field, len(fields))
	for _, field := range fields {
		metadata[field.Name()] = field
	}
	for index, property := range properties {
		field, known := metadata[property.name]
		_, duplicate := byName[property.name]
		if !known || field.ReadOnly() || duplicate || property.matches == nil || property.decode == nil || !property.matches(field) {
			return Body[T]{}, configError(fmt.Sprintf("properties[%d]", index), "field binding is unknown, duplicated, read-only or incompatible")
		}
		if value, present := field.Default(); present {
			if _, ok := property.decode(value, true); !ok {
				return Body[T]{}, configError(fmt.Sprintf("properties[%d]", index), "serializer default cannot be transferred to the declared type")
			}
		}
		byName[property.name] = property
	}
	body := Body[T]{spec: spec, properties: make([]Property[T], 0, len(properties))}
	for _, field := range fields {
		if field.ReadOnly() {
			continue
		}
		property, present := byName[field.Name()]
		if !present {
			return Body[T]{}, configError("fields."+field.Name(), "writable field has no typed binding")
		}
		body.properties = append(body.properties, property)
	}
	var err error
	if body.parser, err = api.NewParser(config); err != nil {
		return Body[T]{}, err
	}
	if body.full, err = openapi.RequestSchema(spec, serializers.ModeFull); err != nil {
		return Body[T]{}, err
	}
	if body.partial, err = openapi.RequestSchema(spec, serializers.ModePartial); err != nil {
		return Body[T]{}, err
	}
	body.valid = true
	return body, nil
}

// Schema is the existing RequestSchema projection of this exact Spec and mode.
// It does not infer shape or validation from the DTO's Go struct.
func (body Body[T]) Schema(mode serializers.Mode) (openapi.Schema, error) {
	if err := body.check(mode); err != nil {
		return openapi.Schema{}, err
	}
	if mode == serializers.ModeFull {
		return body.full, nil
	}
	return body.partial, nil
}

// Parse reads one borrowed request body through api.Parser and then binds it.
// It must be called after the handler's required authentication/authorization.
// Parser failures and cancellation remain Go errors; client field failures are
// validation.Errors. No failure publishes a partial DTO. Body read ownership,
// media grammar and whole-document budgets remain those of api.Parser.
func (body Body[T]) Parse(request *web.Request, mode serializers.Mode) (T, validation.Errors, error) {
	var zero T
	if err := body.check(mode); err != nil {
		return zero, validation.Errors{}, err
	}
	object, err := body.parser.ParseObjectFor(request, body.spec)
	if err != nil {
		return zero, validation.Errors{}, err
	}
	return body.Bind(request.Context(), object, mode)
}

// Bind performs pure Spec validation and typed transfer without reading a body.
// Full defaults, partial omissions, read-only fields, unknown-field ordering and
// normalization are delegated unchanged to Spec.Bind. Validation failure calls
// no setter. Cancellation is checked before/after validation, between transfers
// and setters, and before return; a late cancellation discards the DTO but cannot
// undo effects from a setter that violated the pure-callback contract.
func (body Body[T]) Bind(ctx context.Context, object serializers.Object, mode serializers.Mode) (T, validation.Errors, error) {
	var zero T
	if err := body.check(mode); err != nil {
		return zero, validation.Errors{}, err
	}
	if ctx == nil {
		return zero, validation.Errors{}, configError("context", "context is nil")
	}
	if err := ctx.Err(); err != nil {
		return zero, validation.Errors{}, err
	}
	bound, err := body.spec.Bind(object, mode)
	if cancelled := ctx.Err(); cancelled != nil {
		return zero, validation.Errors{}, cancelled
	}
	if err != nil {
		return zero, validation.Errors{}, err
	}
	if !bound.Valid() {
		return zero, bound.Errors(), nil
	}
	values := bound.Values()
	assignments := make([]func(*T), len(body.properties))
	for index, property := range body.properties {
		if err := ctx.Err(); err != nil {
			return zero, validation.Errors{}, err
		}
		value, present := values.Get(property.name)
		assign, ok := property.decode(value, present)
		if !ok {
			return zero, validation.Errors{}, configError("fields."+property.name, "validated value does not match its declared type")
		}
		assignments[index] = assign
	}
	var result T
	for _, assign := range assignments {
		if err := ctx.Err(); err != nil {
			return zero, validation.Errors{}, err
		}
		assign(&result)
	}
	if err := ctx.Err(); err != nil {
		return zero, validation.Errors{}, err
	}
	return result, validation.Errors{}, nil
}

func (body Body[T]) check(mode serializers.Mode) error {
	if !body.valid {
		return configError("body", "typed body is zero or unprepared")
	}
	if mode != serializers.ModeFull && mode != serializers.ModePartial {
		return configError("mode", "serializer mode is unsupported")
	}
	return nil
}

func configError(field, detail string) error {
	return &api.Error{Code: api.FailureInvalidConfig, Field: "input." + field, Detail: detail}
}
