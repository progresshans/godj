// Package endpoint prepares typed HTTP handlers and OpenAPI operations from
// the same input, output, explicit admission and response declarations.
// Application reads, transactions and the timing of output preparation remain
// in the typed handler. No request or DTO is evaluated at construction.
package endpoint

import (
	"errors"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

// Status is a response status and its public description. Success declarations
// use the exact Output representation; Errors use the framework's JSON error envelope.
// Authentication and internal-error responses remain owned by their profiles.
type Status struct {
	Code        int
	Description string
}

type Config[I, O any] struct {
	Route                web.Route
	Summary, Description string
	Admission            Admission
	Input                Input[I]
	Output               output.Output[O]
	Success              []Status
	Errors               []Status
	// ErrorLimits bounds the complete public error envelope. Zero fields use
	// serializer defaults. It applies to input diagnostics and direct Reject.
	ErrorLimits serializers.Limits
	// SummarizeValidationErrors replaces an oversized validation_error with one
	// too_many_errors diagnostic containing the original count, using standard
	// envelope limits. Other encoding errors and internal causes stay errors.
	SummarizeValidationErrors bool
	Handle                    func(*web.Request, auth.Principal, I) (output.Prepared[O], error)
}

// Endpoint retains immutable startup snapshots. It can be shared concurrently
// subject to the input setter, output getter and application handler contracts.
type Endpoint struct {
	operation openapi.Operation
	input     *inputDefinition
	output    output.Declaration
	valid     bool
}

// New validates declarations before asking the adapter to wrap the handler.
// Route.Handler must be nil: the typed handler is the single execution source.
// Input validation precedes Handle and every error discards a prepared success.
// Handle owns the operation's completion boundary: a nil error confirms its
// prepared result. A later request cancellation cannot overturn a confirmed
// commit. The handler must report cancellation or uncertain outcomes as errors.
// Input-owned 400/413/415 statuses are added automatically; Errors may give those
// statuses application descriptions and declare other expected client errors.
func New[I, O any](authentication api.Authentication, config Config[I, O]) (Endpoint, error) {
	if config.Handle == nil || config.Route.Handler != nil {
		return Endpoint{}, configError("handler", "a typed handler and an unbound route are required", nil)
	}
	if config.Input.err != nil {
		return Endpoint{}, configError("input", "input declaration is invalid", config.Input.err)
	}
	if config.Input.read == nil || config.Input.definition == nil || config.Input.depth < 1 || config.Input.depth > maximumInputDepth || config.Input.stages < 1 || config.Input.stages > maximumInputStages {
		return Endpoint{}, configError("input", "input is zero or unprepared", nil)
	}
	if len(config.Success) == 0 || len(config.Success)+len(config.Errors) > 32 {
		return Endpoint{}, configError("responses", "response count is outside the supported range", nil)
	}
	policy := errorPolicy{limits: config.ErrorLimits, summarize: config.SummarizeValidationErrors}
	if _, err := api.ErrorResponseWithLimits(http.StatusBadRequest, api.CodeValidationError, validation.Errors{}, policy.limits); err != nil {
		return Endpoint{}, configError("error_limits", "error limits cannot encode the empty error envelope", err)
	}
	declaration := config.Output.Declaration()
	definitions, err := output.Components(declaration)
	if err != nil {
		return Endpoint{}, configError("output", "output declaration is invalid", err)
	}
	inputDefinitions, err := inputSchemas(config.Input.definition)
	if err != nil {
		return Endpoint{}, err
	}
	definitions = append(definitions, inputDefinitions...)
	if err := validatePathBindings(config.Route.Path, config.Input.definition.paths); err != nil {
		return Endpoint{}, err
	}
	operation := openapi.Operation{Route: config.Route, Summary: config.Summary, Description: config.Description,
		Parameters: slices.Clone(config.Input.definition.parameters), RequestBody: cloneBody(config.Input.definition.body)}
	if err := config.Admission.describe(&operation); err != nil {
		return Endpoint{}, err
	}
	success, failures := make(map[int]bool, len(config.Success)), make(map[int]bool)
	for index, status := range config.Success {
		if success[status.Code] {
			return Endpoint{}, invalidIndex(index, "success statuses must be distinct")
		}
		response := openapi.Response{Status: status.Code, Description: status.Description}
		if config.Output.IsNoContent() {
			if status.Code != http.StatusNoContent {
				return Endpoint{}, invalidIndex(index, "no-content success requires status 204")
			}
		} else {
			if status.Code < 200 || status.Code >= 300 || status.Code == http.StatusNoContent || status.Code == http.StatusResetContent {
				return Endpoint{}, invalidIndex(index, "JSON success requires a JSON-compatible 2xx status")
			}
			response.ContentType, response.Schema = api.JSONContentType, config.Output.Schema()
		}
		success[status.Code] = true
		operation.Responses = append(operation.Responses, response)
	}
	errorSchema, err := openapi.Ref(openapi.ErrorSchemaName)
	if err != nil {
		return Endpoint{}, err
	}
	errorDescriptions := make(map[int]string, len(config.Input.failures)+len(config.Errors))
	for _, code := range config.Input.failures {
		errorDescriptions[code] = http.StatusText(code)
	}
	declaredErrors := make(map[int]bool, len(config.Errors))
	for index, status := range config.Errors {
		if status.Code < 400 || status.Code >= 500 || declaredErrors[status.Code] {
			return Endpoint{}, invalidIndex(len(config.Success)+index, "error must be a distinct client-error status")
		}
		declaredErrors[status.Code] = true
		errorDescriptions[status.Code] = status.Description
	}
	codes := make([]int, 0, len(errorDescriptions))
	for code := range errorDescriptions {
		codes = append(codes, code)
	}
	sort.Ints(codes)
	for _, code := range codes {
		failures[code] = true
		operation.Responses = append(operation.Responses, openapi.Response{Status: code, Description: errorDescriptions[code], ContentType: api.JSONContentType, Schema: errorSchema})
	}
	// Reuse the existing route, profile, permission, schema-graph and operation
	// checks without evaluating any runtime handler or DTO. The application
	// document still checks conflicts across all its endpoints and manual routes.
	operation.Route.Handler = func(*web.Request) (web.Response, error) { return web.Response{}, nil }
	if _, err := openapi.New(openapi.Config{Title: "Prepared endpoint", Version: "1", Authentication: authentication, Operations: []openapi.Operation{operation}, Schemas: definitions}); err != nil {
		return Endpoint{}, err
	}
	reader, encoder, handle := config.Input.read, config.Output, config.Handle
	execute := func(request *web.Request, actor auth.Principal) (web.Response, error) {
		if request == nil || request.HTTP() == nil || request.HTTP().URL == nil {
			return web.Response{}, configError("request", "request is nil or outside its borrowed lifetime", nil)
		}
		ctx := request.Context()
		if err := ctx.Err(); err != nil {
			return web.Response{}, err
		}
		value, diagnostics, err := reader(request, actor)
		if cancelled := ctx.Err(); cancelled != nil {
			return web.Response{}, errors.Join(cancelled, err)
		}
		if err != nil {
			response, handled, responseErr := expectedInputError(err, policy)
			if !handled {
				return web.Response{}, err
			}
			if responseErr != nil {
				return web.Response{}, responseErr
			}
			if !failures[response.Status()] {
				return web.Response{}, responseError("input returned an undeclared error status", nil)
			}
			return response, nil
		}
		if !diagnostics.Empty() {
			if !failures[http.StatusBadRequest] {
				return web.Response{}, responseError("input returned undeclared validation errors", nil)
			}
			return policy.response(http.StatusBadRequest, api.CodeValidationError, diagnostics)
		}
		prepared, err := handle(request, actor, value)
		if err != nil {
			if cancelled := ctx.Err(); cancelled != nil {
				return web.Response{}, errors.Join(cancelled, err)
			}
			failure, expected := err.(*rejection)
			if !expected || failure == nil {
				return web.Response{}, err
			}
			if !failures[failure.status] {
				return web.Response{}, responseError("handler returned an undeclared error status", nil)
			}
			return policy.response(failure.status, failure.code, failure.diagnostics)
		}
		response, err := encoder.Response(prepared)
		if err != nil {
			return web.Response{}, err
		}
		if !success[response.Status()] || !encoder.IsNoContent() && !jsonContentType(response.Header()) {
			return web.Response{}, responseError("handler returned an undeclared status or representation", nil)
		}
		return response, nil
	}
	protected, err := config.Admission.wrap(authentication, execute)
	if err != nil {
		return Endpoint{}, err
	}
	if protected == nil {
		return Endpoint{}, configError("admission", "authentication returned a nil handler", nil)
	}
	operation.Route.Handler = protected
	return Endpoint{operation: operation, input: config.Input.definition, output: declaration, valid: true}, nil
}

// Operation returns a detached description with the actual protected handler.
func (endpoint Endpoint) Operation() (openapi.Operation, error) {
	if !endpoint.valid {
		return openapi.Operation{}, configError("endpoint", "endpoint is zero or invalid", nil)
	}
	operation := endpoint.operation
	operation.Parameters = slices.Clone(operation.Parameters)
	operation.Responses = slices.Clone(operation.Responses)
	operation.RequestBody = cloneBody(operation.RequestBody)
	operation.AdditionalPermissions = slices.Clone(operation.AdditionalPermissions)
	operation.AlternativePermissions = slices.Clone(operation.AlternativePermissions)
	return operation, nil
}

// Collect returns operations and their complete schema declarations. Sharing
// the same Input or named output Shape shares its component identity. Extra
// outputs allow an application to combine manual operations with typed ones;
// unrelated declarations with the same name remain configuration errors.
func Collect(endpoints []Endpoint, extraOutputs ...output.Declaration) ([]openapi.Operation, []openapi.NamedSchema, error) {
	operations := make([]openapi.Operation, 0, len(endpoints))
	outputs := slices.Clone(extraOutputs)
	var inputs []*inputDefinition
	for _, endpoint := range endpoints {
		operation, err := endpoint.Operation()
		if err != nil {
			return nil, nil, err
		}
		operations = append(operations, operation)
		outputs = append(outputs, endpoint.output)
		inputs = append(inputs, endpoint.input)
	}
	definitions, err := output.Components(outputs...)
	if err != nil {
		return nil, nil, err
	}
	inputDefinitions, err := inputSchemas(inputs...)
	if err != nil {
		return nil, nil, err
	}
	definitions = append(definitions, inputDefinitions...)
	// ValidateSchema checks duplicate component names and complete references.
	for _, definition := range definitions {
		if err := openapi.ValidateSchema(definition.Schema, definitions...); err != nil {
			return nil, nil, err
		}
	}
	return operations, definitions, nil
}

func cloneBody(body *openapi.RequestBody) *openapi.RequestBody {
	if body == nil {
		return nil
	}
	copy := *body
	return &copy
}

func jsonContentType(header http.Header) bool {
	var values []string
	for name, entries := range header {
		if strings.EqualFold(name, "Content-Type") {
			values = append(values, entries...)
		}
	}
	return len(values) == 1 && values[0] == api.JSONContentType
}
