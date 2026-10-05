package output

import (
	"context"
	"math"
	"slices"

	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/serializers"
)

func String() Shape[string] {
	return Shape[string]{node: &node{schema: openapi.String()}, project: func(value string) (serializers.Projection, error) {
		return serializers.ValueProjection(serializers.String(value)), nil
	}}
}

func Boolean() Shape[bool] {
	return Shape[bool]{node: &node{schema: openapi.Boolean()}, project: func(value bool) (serializers.Projection, error) {
		return serializers.ValueProjection(serializers.Boolean(value)), nil
	}}
}

func Int64() Shape[int64] { return Int64Range(math.MinInt64, math.MaxInt64) }

func Int64Range(minimum, maximum int64) Shape[int64] {
	schema, err := openapi.IntegerRange(minimum, maximum)
	return Shape[int64]{node: &node{schema: schema, err: err}, project: func(value int64) (serializers.Projection, error) {
		if value < minimum || value > maximum {
			return serializers.Projection{}, invalidValue("integer", "output is outside the declared range")
		}
		return serializers.ValueProjection(serializers.Integer(value)), nil
	}}
}

// Nullable maps nil to JSON null and a present pointer to the original shape.
// It does not omit object fields or change requiredness.
func Nullable[T any](shape Shape[T]) Shape[*T] {
	schema, err := openapi.Nullable(schemaOf(shape.node))
	return Shape[*T]{node: &node{schema: schema, err: err, children: []*node{shape.node}}, project: func(value *T) (serializers.Projection, error) {
		if value == nil {
			return serializers.ValueProjection(serializers.Null()), nil
		}
		return shape.project(*value)
	}}
}

// Array always emits an array, including [] for a nil slice. Bounds are
// inclusive; the response's shared resource limits apply independently.
func Array[T any](items Shape[T], minimum, maximum int) Shape[[]T] {
	schema, err := openapi.ArrayRange(schemaOf(items.node), minimum, maximum)
	return Shape[[]T]{node: &node{schema: schema, err: err, children: []*node{items.node}}, project: func(values []T) (serializers.Projection, error) {
		if len(values) < minimum || len(values) > maximum {
			return serializers.Projection{}, invalidValue("array", "output length is outside the declared range")
		}
		return serializers.ArrayProjection(len(values), func(_ context.Context, index int) (serializers.Projection, error) {
			return items.project(values[index])
		}), nil
	}}
}

// Property connects one required output member to a typed, pure getter.
// Nil, duplicate and invalid declarations are rejected by New before reads.
type Property[T any] struct {
	name    string
	node    *node
	project func(T) (serializers.Projection, error)
}

func Field[T, V any](name string, shape Shape[V], get func(T) V) Property[T] {
	property := Property[T]{name: name, node: shape.node}
	if get != nil && shape.project != nil {
		property.project = func(value T) (serializers.Projection, error) { return shape.project(get(value)) }
	}
	return property
}

// Object emits exactly its declared fields in declaration order. Properties
// are copied and cannot be changed by later slice mutations. Each getter is
// read once when its field is reached; no getter runs during preparation.
func Object[T any](properties ...Property[T]) Shape[T] {
	properties = slices.Clone(properties)
	n := &node{children: make([]*node, len(properties))}
	schemas := make([]openapi.Property, len(properties))
	for index, property := range properties {
		n.children[index] = property.node
		schemas[index] = openapi.Property{Name: property.name, Schema: schemaOf(property.node), Required: true}
		if property.project == nil {
			n.err = configError("object field has a nil getter or invalid shape", nil)
		}
	}
	schema, err := openapi.Object(schemas...)
	n.schema = schema
	if err != nil {
		n.err = err
	}
	return Shape[T]{node: n, project: func(value T) (serializers.Projection, error) {
		return serializers.ObjectProjection(len(properties), func(_ context.Context, index int) (string, serializers.Projection, error) {
			property := properties[index]
			child, err := property.project(value)
			return property.name, child, err
		}), nil
	}}
}

// Named binds a component identity to the same output declaration. Reuse this
// returned shape wherever that component is needed; free references cannot be
// supplied to an unrelated output codec.
func Named[T any](name string, shape Shape[T]) Shape[T] {
	ref, err := openapi.Ref(name)
	return Shape[T]{node: &node{name: name, schema: ref, definition: schemaOf(shape.node), err: err, children: []*node{shape.node}}, project: shape.project}
}

// Model derives both schema and output from the encoder's own immutable spec.
// Existing input choices/defaults/trimming do not become output validation;
// model nullability, lengths, exact values and computed allowlists are retained.
func Model[M any](encoder serializers.ModelEncoder[M]) Shape[M] {
	schema, err := openapi.ModelResponseSchema(encoder.Spec())
	return Shape[M]{node: &node{schema: schema, err: err}, project: func(value M) (serializers.Projection, error) {
		return encoder.Project(value), nil
	}}
}
