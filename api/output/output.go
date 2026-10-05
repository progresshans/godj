// Package output binds explicit Go getters to JSON output and OpenAPI schemas.
// Shapes are immutable declarations; preparation performs no I/O and never
// reads a DTO. There is no struct reflection or arbitrary encoder/schema pair.
package output

import (
	"context"
	"fmt"
	"net/http"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/web"
)

// Shape declares an exact Go output type. Its zero value is invalid. Staged
// declaration errors are returned by New, including errors in nested shapes.
type Shape[T any] struct {
	node    *node
	project func(T) (serializers.Projection, error)
}

type node struct {
	schema     openapi.Schema
	children   []*node
	name       string
	definition openapi.Schema
	err        error
}

func schemaOf(node *node) openapi.Schema {
	if node == nil {
		return openapi.Schema{}
	}
	return node.schema
}

// Output is a prepared declaration and one whole-response resource budget. It
// may be shared concurrently when application getters are concurrency-safe.
// Source values and mutable fields must remain stable until Encode/JSON returns.
type Output[T any] struct {
	shape  Shape[T]
	limits serializers.Limits
}

func New[T any](shape Shape[T], limits serializers.Limits) (Output[T], error) {
	if shape.project == nil || shape.node == nil {
		return Output[T]{}, configError("shape is zero or invalid", nil)
	}
	resolved, err := limits.Resolve()
	if err != nil {
		return Output[T]{}, configError("response limits are invalid", err)
	}
	output := Output[T]{shape: shape, limits: resolved}
	if _, err := Components(output.Declaration()); err != nil {
		return Output[T]{}, err
	}
	return output, nil
}

// Schema returns the root schema, including its named reference when declared.
// Components collects the matching definitions for the document.
func (o Output[T]) Schema() openapi.Schema { return schemaOf(o.shape.node) }

func (o Output[T]) Encode(ctx context.Context, value T) ([]byte, error) {
	if o.shape.project == nil || o.shape.node == nil {
		return nil, configError("output is zero or invalid", nil)
	}
	if ctx == nil {
		return nil, responseError("output requires a context", nil)
	}
	if err := ctx.Err(); err != nil {
		return nil, responseError("output was cancelled", err)
	}
	projection, err := o.shape.project(value)
	if err != nil {
		return nil, responseError("output value is invalid", err)
	}
	body, err := serializers.EncodeProjection(ctx, projection, o.limits)
	if err != nil {
		return nil, responseError("output projection failed", err)
	}
	return body, nil
}

// JSON creates a complete response only after projection, validation and
// encoding succeed. Authorization, reads and transaction ownership stay with
// the handler; write handlers must validate output before committing a write.
func (o Output[T]) JSON(ctx context.Context, status int, value T) (web.Response, error) {
	body, err := o.Encode(ctx, value)
	if err != nil {
		return web.Response{}, err
	}
	header := make(http.Header)
	header.Set("Content-Type", api.JSONContentType)
	response, err := web.NewResponse(status, header, body)
	if err != nil {
		return web.Response{}, responseError("HTTP response is invalid", err)
	}
	if err := ctx.Err(); err != nil {
		return web.Response{}, responseError("output was cancelled", err)
	}
	return response, nil
}

// Declaration describes a prepared Output independent of its Go value type.
// Its fields are private; the zero declaration is invalid.
type Declaration struct{ node *node }

func (o Output[T]) Declaration() Declaration { return Declaration{node: o.shape.node} }

// Components returns detached definitions for one or more prepared outputs.
// Reusing the same named Shape shares its identity. Independently naming two
// declarations identically is an error, even if their schemas happen to match.
// Inline/named schema budgets, cycles and references use OpenAPI's validator.
func Components(outputs ...Declaration) ([]openapi.NamedSchema, error) {
	var definitions []openapi.NamedSchema
	visited := make(map[*node]bool)
	identities := make(map[string]*node)
	var visit func(*node) error
	visit = func(current *node) error {
		if current == nil {
			return configError("nested shape is zero or invalid", nil)
		}
		if visited[current] {
			return nil
		}
		if len(visited) >= 4096 {
			return configError("output declarations exceed 4096 nodes", nil)
		}
		visited[current] = true
		if current.err != nil {
			return configError("output declaration is invalid", current.err)
		}
		if current.name != "" {
			if previous := identities[current.name]; previous != nil && previous != current {
				return configError(fmt.Sprintf("component %q has multiple output identities", current.name), nil)
			}
			identities[current.name] = current
			definitions = append(definitions, openapi.NamedSchema{Name: current.name, Schema: current.definition})
		}
		for _, child := range current.children {
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	for _, output := range outputs {
		if err := visit(output.node); err != nil {
			return nil, err
		}
	}
	for _, output := range outputs {
		if err := openapi.ValidateSchema(output.node.schema, definitions...); err != nil {
			return nil, configError("output schema graph is invalid", err)
		}
	}
	return definitions, nil
}

func configError(detail string, cause error) error {
	return &api.Error{Code: api.FailureInvalidConfig, Field: "output", Detail: detail, Cause: cause}
}

func responseError(detail string, cause error) error {
	return &api.Error{Code: api.FailureInvalidResponse, Field: "output", Detail: detail, Cause: cause}
}

func invalidValue(field, detail string) error {
	return &serializers.Error{Code: serializers.CodeInvalidValue, Field: field, Detail: detail}
}
