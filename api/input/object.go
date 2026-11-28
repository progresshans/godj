package input

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
)

// ObjectProperty places an existing prepared Body inside a required object
// member. Each child keeps its own model provenance, field policy and typed
// binding. No model or relation write is inferred from the object structure.
type ObjectProperty[T any] struct {
	name          string
	decoder       serializers.ObjectDecoder
	full, partial openapi.Schema
	prepare       func(context.Context, serializers.Object, serializers.Mode) (func(*T) error, validation.Errors, error)
	err           error
}

// ObjectField binds a required child object to one pure, concurrency-safe DTO
// setter. All child field validation and conversions finish before any setter
// in the whole envelope runs. Construction never evaluates a DTO or callback.
func ObjectField[T, V any](name string, body Body[V], assign func(*T, V)) ObjectProperty[T] {
	property := ObjectProperty[T]{name: name, decoder: body.decoder}
	if err := body.check(serializers.ModeFull); err != nil {
		property.err = err
		return property
	}
	if assign == nil {
		property.err = configError("objects", "object setter is nil")
		return property
	}
	property.full, property.partial = body.full, body.partial
	property.prepare = func(ctx context.Context, object serializers.Object, mode serializers.Mode) (func(*T) error, validation.Errors, error) {
		build, diagnostics, err := body.prepareInput(ctx, object, mode)
		if err != nil {
			return nil, validation.Errors{}, err
		}
		if !diagnostics.Empty() {
			return nil, objectDiagnostics(name, body.nested, diagnostics), nil
		}
		return func(destination *T) error {
			value, err := build()
			if canceled := ctx.Err(); canceled != nil {
				return canceled
			}
			if err != nil {
				return err
			}
			assign(destination, value)
			return nil
		}, validation.Errors{}, nil
	}
	return property
}

// Object prepares a closed envelope of required object members. Full/partial
// mode controls the fields inside each child; structural envelope members are
// required in both modes. Null is not an object. Only config owns request-byte
// and aggregate JSON budgets; child single-body parsers are not invoked.
// Diagnostics use JSON Pointer paths, in child declaration order followed by
// lexically ordered unknown envelope members. No input values enter diagnostics.
func Object[T any](config api.ParserConfig, properties ...ObjectProperty[T]) (Body[T], error) {
	if len(properties) == 0 || len(properties) >= 4096 {
		return Body[T]{}, configError("objects", "object envelope must have a bounded nonempty declaration")
	}
	properties = slices.Clone(properties)
	decoders := make([]serializers.ObjectDecoderField, len(properties))
	full, partial := make([]openapi.Property, len(properties)), make([]openapi.Property, len(properties))
	names := make(map[string]bool, len(properties))
	for index, property := range properties {
		if property.err != nil {
			return Body[T]{}, property.err
		}
		if property.prepare == nil {
			return Body[T]{}, configError(fmt.Sprintf("objects[%d]", index), "object binding is zero or unprepared")
		}
		decoders[index] = serializers.ObjectDecoderField{Name: property.name, Decoder: property.decoder}
		full[index] = openapi.Property{Name: property.name, Schema: property.full, Required: true}
		partial[index] = openapi.Property{Name: property.name, Schema: property.partial, Required: true}
		names[property.name] = true
	}
	decoder, err := serializers.ComposeObjectDecoder(decoders...)
	if err != nil {
		return Body[T]{}, configError("objects", "object declarations are invalid or exceed their bounds")
	}
	parser, err := api.NewParser(config)
	if err != nil {
		return Body[T]{}, err
	}
	fullSchema, err := openapi.Object(full...)
	if err != nil {
		return Body[T]{}, err
	}
	partialSchema, err := openapi.Object(partial...)
	if err != nil {
		return Body[T]{}, err
	}
	body := Body[T]{parser: parser, decoder: decoder, full: fullSchema, partial: partialSchema, nested: true, valid: true}
	body.prepare = func(ctx context.Context, object serializers.Object, mode serializers.Mode) (preparedInput[T], validation.Errors, error) {
		if !object.Valid() {
			return nil, validation.Errors{}, configError("object", "input object is zero or invalid")
		}
		assignments := make([]func(*T) error, len(properties))
		var failures []validation.Errors
		for index, property := range properties {
			if err := ctx.Err(); err != nil {
				return nil, validation.Errors{}, err
			}
			value, present := object.Get(property.name)
			if !present {
				failures = append(failures, validation.NewErrors(validation.New(validation.Field("/"+property.name), serializers.CodeRequired)))
				continue
			}
			child, valid := value.AsObject()
			if !valid {
				failures = append(failures, validation.NewErrors(validation.New(validation.Field("/"+property.name), serializers.CodeType)))
				continue
			}
			assign, diagnostics, err := property.prepare(ctx, child, mode)
			if err != nil {
				return nil, validation.Errors{}, err
			}
			failures = append(failures, diagnostics)
			assignments[index] = assign
		}
		var unknown []string
		for _, member := range object.Members() {
			if !names[member.Name()] {
				unknown = append(unknown, member.Name())
			}
		}
		sort.Strings(unknown)
		for _, name := range unknown {
			failures = append(failures, validation.NewErrors(validation.New(validation.Field("/"+pointerToken(name)), serializers.CodeUnknown)))
		}
		diagnostics := validation.Join(failures...)
		if !diagnostics.Empty() {
			return nil, diagnostics, nil
		}
		return func() (T, error) {
			var result, zero T
			for _, assign := range assignments {
				if err := ctx.Err(); err != nil {
					return zero, err
				}
				if err := assign(&result); err != nil {
					return zero, err
				}
			}
			return result, nil
		}, validation.Errors{}, nil
	}
	return body, nil
}

func objectDiagnostics(name string, nested bool, source validation.Errors) validation.Errors {
	items := source.All()
	for index, item := range items {
		field := string(item.Field())
		if !nested {
			field = "/" + pointerToken(field)
		}
		items[index] = validation.New(validation.Field("/"+name+field), item.Code(), item.Params()...)
	}
	return validation.NewErrors(items...)
}

func pointerToken(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}
