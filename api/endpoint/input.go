package endpoint

import (
	"net/http"
	"slices"

	bodyinput "github.com/progresshans/godj/api/input"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/api/parameters"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

// Input binds a closed runtime reader and its actual request declaration.
// Its zero value is invalid; construction errors are reported by New.
type Input[T any] struct {
	read       func(*web.Request, auth.Principal) (T, validation.Errors, error)
	depth      int
	stages     int
	definition *inputDefinition
	failures   []int
	jsonBody   *jsonBodySource[T]
	err        error
}

// Only a bare JSONBody retains this source. Wrappers clear it so deriving an
// array can never silently discard a query policy or application preparation.
type jsonBodySource[T any] struct {
	body bodyinput.Body[T]
	mode serializers.Mode
}

type inputDefinition struct {
	parameters []openapi.Parameter
	body       *openapi.RequestBody
	schemas    []openapi.NamedSchema
	children   []*inputDefinition
	paths      []pathBinding
	query      bool
	noQuery    bool
}

func Query[T any](query parameters.Query[T]) Input[T] {
	if query.MaxBytes() == 0 {
		return Input[T]{err: configError("input", "query is zero or unprepared", nil)}
	}
	return Input[T]{
		definition: &inputDefinition{parameters: query.Parameters(), query: true},
		depth:      1, stages: 1,
		failures: []int{http.StatusBadRequest},
		read: func(request *web.Request, _ auth.Principal) (T, validation.Errors, error) {
			return query.Parse(request.HTTP().URL.RawQuery)
		},
	}
}

// JSONBody gives this exact full/partial Body schema an explicit component
// name. Reuse the Input value to share that component between endpoints.
// Body parsing does not inspect query parameters; NoQuery adds that policy.
func JSONBody[T any](name string, body bodyinput.Body[T], mode serializers.Mode, description string) Input[T] {
	schema, err := body.Schema(mode)
	if err != nil {
		return Input[T]{err: err}
	}
	ref, err := openapi.Ref(name)
	if err != nil {
		return Input[T]{err: err}
	}
	return Input[T]{
		depth: 1, stages: 1,
		jsonBody: &jsonBodySource[T]{body: body, mode: mode},
		definition: &inputDefinition{
			body:    &openapi.RequestBody{Schema: ref, Required: true, Description: description},
			schemas: []openapi.NamedSchema{{Name: name, Schema: schema}},
		},
		failures: []int{http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType},
		read: func(request *web.Request, _ auth.Principal) (T, validation.Errors, error) {
			return body.Parse(request, mode)
		},
	}
}

// JSONListBody derives an array from a bare JSONBody declaration. The single
// and array inputs share the exact named item component. Reuse that JSONBody
// value; independent declarations with the same name still conflict. Add
// NoQuery, Sequence or Resolve around the resulting array, not its source.
// The list reads the body once with config's whole-request and item budgets.
func JSONListBody[T any](item Input[T], config bodyinput.ListConfig, description string, validators ...bodyinput.ListValidator[T]) Input[[]T] {
	if item.err != nil {
		return Input[[]T]{err: item.err}
	}
	if item.jsonBody == nil || item.definition == nil || item.definition.body == nil || item.read == nil {
		return Input[[]T]{err: configError("input", "JSONListBody requires a bare JSONBody declaration", nil)}
	}
	list, err := bodyinput.NewList(item.jsonBody.body, config, validators...)
	if err != nil {
		return Input[[]T]{err: err}
	}
	minimum, maximum := list.Bounds()
	schema, err := openapi.ArrayRange(item.definition.body.Schema, minimum, maximum)
	if err != nil {
		return Input[[]T]{err: err}
	}
	return Input[[]T]{
		depth: 1, stages: 1,
		definition: &inputDefinition{
			body:     &openapi.RequestBody{Schema: schema, Required: true, Description: description},
			children: []*inputDefinition{item.definition},
		},
		failures: slices.Clone(item.failures),
		read: func(request *web.Request, _ auth.Principal) ([]T, validation.Errors, error) {
			return list.Parse(request, item.jsonBody.mode)
		},
	}
}

// NoInput has no body or parameter declaration and performs no request I/O.
// The application can still use its router-owned path parameters in the handler.
func NoInput() Input[struct{}] {
	return Input[struct{}]{definition: &inputDefinition{}, depth: 1, stages: 1, read: func(*web.Request, auth.Principal) (struct{}, validation.Errors, error) {
		return struct{}{}, validation.Errors{}, nil
	}}
}

// NoQuery rejects any nonempty raw query before the wrapped reader runs. It
// deliberately does not normalize empty '&' segments or perform URL decoding.
// It cannot be combined with declared query parameters.
func NoQuery[T any](input Input[T]) Input[T] {
	if input.err != nil {
		return input
	}
	if input.definition == nil || input.read == nil || len(input.definition.parameters) != 0 {
		return Input[T]{err: configError("input", "NoQuery requires an input without query parameters", nil)}
	}
	result := input
	result.jsonBody = nil
	result.depth++
	result.stages++
	if result.depth > maximumInputDepth {
		return Input[T]{err: configError("input", "input pipeline exceeds 64 stages", nil)}
	}
	if result.stages > maximumInputStages {
		return Input[T]{err: configError("input", "input pipeline exceeds 4096 execution stages", nil)}
	}
	definition := *input.definition
	definition.schemas, definition.children, definition.noQuery = nil, []*inputDefinition{input.definition}, true
	result.definition = &definition
	result.failures = slices.Clone(input.failures)
	if !slices.Contains(result.failures, http.StatusBadRequest) {
		result.failures = append(result.failures, http.StatusBadRequest)
	}
	result.read = func(request *web.Request, actor auth.Principal) (T, validation.Errors, error) {
		if request.HTTP().URL.RawQuery != "" {
			var zero T
			return zero, validation.NewErrors(validation.New(validation.NonField, "invalid")), nil
		}
		return input.read(request, actor)
	}
	return result
}
