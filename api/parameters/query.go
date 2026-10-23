// Package parameters binds bounded URL query parsing, typed DTO assignment and
// OpenAPI parameter declarations. It performs no I/O. Authentication, error
// presentation and request/DB lifetime remain the handler's responsibility.
package parameters

import (
	"fmt"
	"net/url"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/validation"
)

// Parameter binds one input to a DTO setter. Setters run in declaration order
// only after every parameter has passed validation. They must be pure, safe for
// concurrent requests and must not retain the DTO pointer. New never calls them.
type Parameter[T any] struct {
	metadata openapi.Parameter
	parse    func(string, bool) (func(*T), string)
	err      error
}

func Field[T, V any](name string, input Input[V], assign func(*T, V), description string) Parameter[T] {
	parameter := Parameter[T]{metadata: openapi.Parameter{
		Name: name, In: "query", Description: description, Schema: input.schema,
		Required: input.required, AllowEmptyValue: input.empty,
	}, err: input.err}
	if input.decode == nil || assign == nil {
		parameter.err = configError("input or DTO setter is zero or invalid", nil)
		return parameter
	}
	parameter.parse = func(raw string, present bool) (func(*T), string) {
		value, code := input.decode(raw, present)
		if code != "" {
			return nil, code
		}
		return func(result *T) { assign(result, value) }, ""
	}
	return parameter
}

// Query is an immutable prepared declaration. Copies may be shared concurrently
// subject to the setter contract. Each successful Parse owns its resulting DTO.
type Query[T any] struct {
	parameters []Parameter[T]
	names      map[string]bool
	maxBytes   int
}

// New snapshots up to 128 parameters and requires an explicit raw-query budget
// of 1 through 1 MiB. Duplicate names and invalid declarations fail before use.
func New[T any](maximumBytes int, parameters ...Parameter[T]) (Query[T], error) {
	if maximumBytes < 1 || maximumBytes > 1<<20 || len(parameters) > 128 {
		return Query[T]{}, configError("query byte or parameter count limit is invalid", nil)
	}
	query := Query[T]{parameters: append([]Parameter[T](nil), parameters...), names: make(map[string]bool, len(parameters)), maxBytes: maximumBytes}
	for index, parameter := range query.parameters {
		invalid := func(detail string, cause error) (Query[T], error) {
			return Query[T]{}, &api.Error{Code: api.FailureInvalidConfig, Field: fmt.Sprintf("parameters[%d]", index), Detail: detail, Cause: cause}
		}
		if parameter.err != nil {
			return invalid("query parameter is invalid", parameter.err)
		}
		if parameter.parse == nil {
			return invalid("query parameter is zero or invalid", nil)
		}
		if err := openapi.ValidateParameter(parameter.metadata); err != nil {
			return invalid("query parameter metadata is invalid", err)
		}
		if query.names[parameter.metadata.Name] {
			return invalid("query parameter name is duplicated", nil)
		}
		query.names[parameter.metadata.Name] = true
	}
	return query, nil
}

func (q Query[T]) MaxBytes() int { return q.maxBytes }

// Parameters returns a detached slice; the schemas inside it are immutable.
func (q Query[T]) Parameters() []openapi.Parameter {
	parameters := make([]openapi.Parameter, len(q.parameters))
	for index, parameter := range q.parameters {
		parameters[index] = parameter.metadata
	}
	return parameters
}

// Parse uses net/url query decoding, including '+' spaces, ignored empty '&'
// segments and rejection of unescaped semicolons. Unknown/duplicate names and
// malformed URL encoding are whole-input errors. Scalar errors follow declaration
// order and return the first violation, without values in diagnostics. Missing
// required values use "required"; all other invalid input uses "invalid".
// On failure it returns a zero DTO and never calls a setter. A non-nil Go error
// means a zero/unprepared Query; ordinary client input returns validation.Errors.
func (q Query[T]) Parse(raw string) (T, validation.Errors, error) {
	var zero T
	if q.maxBytes < 1 || q.names == nil {
		return zero, validation.Errors{}, configError("query is zero or unprepared", nil)
	}
	invalid := func(field validation.Field, code string) (T, validation.Errors, error) {
		return zero, validation.NewErrors(validation.New(field, validation.Code(code))), nil
	}
	if len(raw) > q.maxBytes {
		return invalid(validation.NonField, "invalid")
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return invalid(validation.NonField, "invalid")
	}
	for name, entries := range values {
		if !q.names[name] || len(entries) != 1 {
			return invalid(validation.NonField, "invalid")
		}
	}
	assignments := make([]func(*T), len(q.parameters))
	for index, parameter := range q.parameters {
		entries, present := values[parameter.metadata.Name]
		value := ""
		if present {
			value = entries[0]
		}
		assign, code := parameter.parse(value, present)
		if code != "" {
			return invalid(validation.Field(parameter.metadata.Name), code)
		}
		assignments[index] = assign
	}
	var result T
	for _, assign := range assignments {
		assign(&result)
	}
	return result, validation.Errors{}, nil
}

func configError(detail string, cause error) error {
	return &api.Error{Code: api.FailureInvalidConfig, Field: "parameters", Detail: detail, Cause: cause}
}
