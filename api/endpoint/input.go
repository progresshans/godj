package endpoint

import (
	"net/http"
	"slices"

	bodyinput "github.com/progresshans/godj/api/input"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/api/parameters"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

// Input binds a closed runtime reader and its actual request declaration.
// Its zero value is invalid; construction errors are reported by New.
type Input[T any] struct {
	read       func(*web.Request) (T, validation.Errors, error)
	definition *inputDefinition
	failures   []int
	err        error
}

type inputDefinition struct {
	parameters []openapi.Parameter
	body       *openapi.RequestBody
	schemas    []openapi.NamedSchema
}

func Query[T any](query parameters.Query[T]) Input[T] {
	if query.MaxBytes() == 0 {
		return Input[T]{err: configError("input", "query is zero or unprepared", nil)}
	}
	return Input[T]{
		definition: &inputDefinition{parameters: query.Parameters()},
		failures:   []int{http.StatusBadRequest},
		read: func(request *web.Request) (T, validation.Errors, error) {
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
		definition: &inputDefinition{
			body:    &openapi.RequestBody{Schema: ref, Required: true, Description: description},
			schemas: []openapi.NamedSchema{{Name: name, Schema: schema}},
		},
		failures: []int{http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType},
		read: func(request *web.Request) (T, validation.Errors, error) {
			return body.Parse(request, mode)
		},
	}
}

// NoInput has no body or parameter declaration and performs no request I/O.
// The application can still use its router-owned path parameters in the handler.
func NoInput() Input[struct{}] {
	return Input[struct{}]{definition: &inputDefinition{}, read: func(*web.Request) (struct{}, validation.Errors, error) {
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
	result.failures = slices.Clone(input.failures)
	if !slices.Contains(result.failures, http.StatusBadRequest) {
		result.failures = append(result.failures, http.StatusBadRequest)
	}
	result.read = func(request *web.Request) (T, validation.Errors, error) {
		if request.HTTP().URL.RawQuery != "" {
			var zero T
			return zero, validation.NewErrors(validation.New(validation.NonField, "invalid")), nil
		}
		return input.read(request)
	}
	return result
}
