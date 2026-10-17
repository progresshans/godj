package input

import (
	"context"
	"errors"
	"math"
	"strconv"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

const maximumListItems = 1024
const maximumListDiagnostics = 1 << 14

// ListConfig separates the whole request budget from each compact object's
// encoding budget. MinItems may be zero; MaxItems must be between 1 and 1024.
// Zero parser/item limit fields use the existing parser/serializer defaults.
type ListConfig struct {
	Parser             api.ParserConfig
	ItemLimits         serializers.Limits
	MinItems, MaxItems int
}

// ListValidator checks one fully bound row. It must be pure, concurrency-safe,
// and must not mutate or retain the DTO or its pointers/slices, perform I/O, or
// commit a write. Return diagnostics for the row's fields; the list owns the
// outer "index" parameter. A field's existing "index" becomes "item_index".
// A non-nil error remains internal and stops evaluation of the remaining rows.
type ListValidator[T any] func(context.Context, T) (validation.Errors, error)

// ListBody binds an array through the same immutable Body and Spec. Every row
// owns its successful values. A valid row's pure setters/validator may run even
// if another row is invalid, but no failed request returns any DTO rows.
type ListBody[T any] struct {
	body             Body[T]
	parser           api.Parser
	itemLimits       serializers.Limits
	minimum, maximum int
	validate         ListValidator[T]
	valid            bool
}

// NewList prepares an array binding with at most one additional row validator.
// Construction evaluates no DTO, setter, validator or request. One body parser
// owns the array's complete byte/depth/value budget and JSON-field grammar.
func NewList[T any](body Body[T], config ListConfig, validators ...ListValidator[T]) (ListBody[T], error) {
	if err := body.check(serializers.ModeFull); err != nil {
		return ListBody[T]{}, err
	}
	if config.MinItems < 0 || config.MaxItems < 1 || config.MaxItems < config.MinItems || config.MaxItems > maximumListItems {
		return ListBody[T]{}, configError("list.count", "list bounds require 0 <= minimum <= maximum and 1 <= maximum <= 1024")
	}
	if len(validators) > 1 || len(validators) == 1 && validators[0] == nil {
		return ListBody[T]{}, configError("list.validator", "provide at most one non-nil row validator")
	}
	parser, err := api.NewParser(config.Parser)
	if err != nil {
		return ListBody[T]{}, err
	}
	// Validate item limits through the existing writer. No second limit
	// normalizer or JSON serializer is introduced for collection inputs.
	object, err := serializers.NewObject()
	if err != nil {
		return ListBody[T]{}, err
	}
	if _, err := serializers.Encode(object.Value(), config.ItemLimits); err != nil {
		return ListBody[T]{}, configError("list.item_limits", "item limits cannot encode an empty object")
	}
	result := ListBody[T]{body: body, parser: parser, itemLimits: config.ItemLimits, minimum: config.MinItems, maximum: config.MaxItems, valid: true}
	if len(validators) == 1 {
		result.validate = validators[0]
	}
	return result, nil
}

// Bounds returns this prepared list's inclusive row count. A zero ListBody
// returns zeroes; Schema, Bind and Parse reject that unprepared value.
func (list ListBody[T]) Bounds() (minimum, maximum int) { return list.minimum, list.maximum }

// Schema uses this exact item's full/partial schema and count bounds.
func (list ListBody[T]) Schema(mode serializers.Mode) (openapi.Schema, error) {
	if !list.valid {
		return openapi.Schema{}, configError("list", "typed list is zero or unprepared")
	}
	item, err := list.body.Schema(mode)
	if err != nil {
		return openapi.Schema{}, err
	}
	return openapi.ArrayRange(item, list.minimum, list.maximum)
}

// Parse reads one borrowed array request. Media, malformed JSON, duplicate
// names, JSON-field admission, cancellation and reader failures use api.Parser.
func (list ListBody[T]) Parse(request *web.Request, mode serializers.Mode) ([]T, validation.Errors, error) {
	if !list.valid {
		return nil, validation.Errors{}, configError("list", "typed list is zero or unprepared")
	}
	if err := list.body.check(mode); err != nil {
		return nil, validation.Errors{}, err
	}
	objects, err := list.parser.ParseListFor(request, list.body.spec)
	if err != nil {
		return nil, validation.Errors{}, err
	}
	return list.Bind(request.Context(), objects, mode)
}

// Bind validates compact item budgets, binds rows, and runs the optional pure
// validator in input order. Client diagnostics do not skip other rows; a row's
// failed budget/Spec validation skips its setters and application validator.
// Diagnostics own their original zero-based index. More than 16384 violations
// becomes one too_many_errors rejection with the complete count, never a
// truncated prefix presented as the complete result.
func (list ListBody[T]) Bind(ctx context.Context, objects []serializers.Object, mode serializers.Mode) ([]T, validation.Errors, error) {
	if !list.valid {
		return nil, validation.Errors{}, configError("list", "typed list is zero or unprepared")
	}
	if err := list.body.check(mode); err != nil {
		return nil, validation.Errors{}, err
	}
	if ctx == nil {
		return nil, validation.Errors{}, configError("context", "context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, validation.Errors{}, err
	}
	if len(objects) < list.minimum || len(objects) > list.maximum {
		return nil, validation.NewErrors(validation.New(validation.NonField, "invalid_count", validation.NewParam("min", strconv.Itoa(list.minimum)), validation.NewParam("max", strconv.Itoa(list.maximum)))), nil
	}
	result := make([]T, len(objects))
	var failures indexedDiagnostics
	for index, object := range objects {
		if err := ctx.Err(); err != nil {
			return nil, validation.Errors{}, err
		}
		_, err := serializers.Encode(object.Value(), list.itemLimits)
		if cancelled := ctx.Err(); cancelled != nil {
			return nil, validation.Errors{}, errors.Join(cancelled, err)
		}
		if err != nil {
			if !errors.Is(err, &serializers.Error{Code: serializers.CodeResourceLimit}) {
				return nil, validation.Errors{}, err
			}
			if err := failures.add(ctx, index, validation.NewErrors(validation.New(validation.NonField, "limit_exceeded"))); err != nil {
				return nil, validation.Errors{}, err
			}
			continue
		}
		value, diagnostics, err := list.body.Bind(ctx, object, mode)
		if err != nil {
			return nil, validation.Errors{}, err
		}
		if !diagnostics.Empty() {
			if err := failures.add(ctx, index, diagnostics); err != nil {
				return nil, validation.Errors{}, err
			}
			continue
		}
		if list.validate != nil {
			diagnostics, err = list.validate(ctx, value)
			if cancelled := ctx.Err(); cancelled != nil {
				return nil, validation.Errors{}, errors.Join(cancelled, err)
			}
			if err != nil {
				return nil, validation.Errors{}, &listValidationFailure{cause: err}
			}
			if err := failures.add(ctx, index, diagnostics); err != nil {
				return nil, validation.Errors{}, err
			}
		}
		result[index] = value
	}
	if err := ctx.Err(); err != nil {
		return nil, validation.Errors{}, err
	}
	if failures.count != 0 {
		return nil, failures.result(), nil
	}
	return result, validation.Errors{}, nil
}

type indexedDiagnostics struct {
	count int
	items []validation.Violation
}

func (failures *indexedDiagnostics) add(ctx context.Context, index int, diagnostics validation.Errors) error {
	if diagnostics.Len() > math.MaxInt-failures.count {
		return configError("list.validator", "diagnostic count exceeds the supported range")
	}
	// The count is complete even when retaining every diagnostic would exceed
	// the bound. Field validation may itself report an integer-list item index;
	// give that position its own name before adding the outer row position.
	for position := 0; position < diagnostics.Len(); position++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		violation, _ := diagnostics.At(position)
		parameters := violation.Params()
		// Even discarded overflow diagnostics must be representable. Otherwise
		// an invalid callback result could turn into a public count summary.
		text := []serializers.Value{serializers.String(string(violation.Field())), serializers.String(string(violation.Code()))}
		for _, parameter := range parameters {
			text = append(text, serializers.String(parameter.Key()), serializers.String(parameter.Value()))
		}
		if _, err := serializers.NewList(text...); err != nil {
			return configError("list.validator", "row diagnostics contain invalid JSON text")
		}
		indexed, itemIndexed := false, false
		for _, parameter := range parameters {
			if parameter.Key() == "index" {
				if indexed {
					return configError("list.validator", "field diagnostics repeat an index parameter")
				}
				indexed = true
			}
			if parameter.Key() == "item_index" {
				if itemIndexed {
					return configError("list.validator", "field diagnostics repeat an item_index parameter")
				}
				itemIndexed = true
			}
		}
		if indexed && itemIndexed {
			return configError("list.validator", "field diagnostics contain ambiguous item indexes")
		}
		for slot, parameter := range parameters {
			if parameter.Key() == "index" {
				parameters[slot] = validation.NewParam("item_index", parameter.Value())
			}
		}
		failures.count++
		if failures.count > maximumListDiagnostics {
			failures.items = nil
			continue
		}
		parameters = append(parameters, validation.NewParam("index", strconv.Itoa(index)))
		failures.items = append(failures.items, validation.New(violation.Field(), violation.Code(), parameters...))
	}
	return nil
}

func (failures indexedDiagnostics) result() validation.Errors {
	if failures.count > maximumListDiagnostics {
		return validation.NewErrors(validation.New(validation.NonField, "too_many_errors", validation.NewParam("count", strconv.Itoa(failures.count))))
	}
	return validation.NewErrors(failures.items...)
}

// A row validator's direct api.Error must not masquerade as a closed parser's
// expected 400/413/415. Preserve its cause only for internal inspection.
type listValidationFailure struct{ cause error }

func (*listValidationFailure) Error() string         { return "api input: row validation failed" }
func (listValidationFailure) GoString() string       { return "api input: row validation failed" }
func (failure *listValidationFailure) Unwrap() error { return failure.cause }
