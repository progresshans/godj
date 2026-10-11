package endpoint

import (
	"errors"
	"slices"
	"strings"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

const maximumInputDepth = 64
const maximumInputStages = 4096

// Pair retains both exact Go types in a sequential input. Neither field is
// published to the handler unless all stages finish successfully.
type Pair[A, B any] struct {
	First  A
	Second B
}

// Sequence reads first, then second. It supports one query parser, one body,
// independently named headers and router values. Duplicate sources and conflicting
// query policies are configuration errors, not repeated body reads or silently
// ignored inputs. Pipelines have a maximum depth of 64 and 4096 total reader
// invocations, counting repeated shared stages each time they would run.
func Sequence[A, B any](first Input[A], second Input[B]) Input[Pair[A, B]] {
	if first.err != nil {
		return Input[Pair[A, B]]{err: first.err}
	}
	if second.err != nil {
		return Input[Pair[A, B]]{err: second.err}
	}
	if first.definition == nil || second.definition == nil || first.read == nil || second.read == nil {
		return Input[Pair[A, B]]{err: configError("input", "sequence contains an unprepared input", nil)}
	}
	depth := 1 + max(first.depth, second.depth)
	if depth > maximumInputDepth {
		return Input[Pair[A, B]]{err: configError("input", "input pipeline exceeds 64 stages", nil)}
	}
	stages := 1 + first.stages + second.stages
	if stages > maximumInputStages {
		return Input[Pair[A, B]]{err: configError("input", "input pipeline exceeds 4096 execution stages", nil)}
	}
	left, right := first.definition, second.definition
	if left.query && right.query || left.body != nil && right.body != nil {
		return Input[Pair[A, B]]{err: configError("input", "sequence must not repeat a query parser or JSON body", nil)}
	}
	headers := make(map[string]bool)
	for _, parameter := range left.parameters {
		if parameter.In == "header" {
			headers[strings.ToLower(parameter.Name)] = true
		}
	}
	for _, parameter := range right.parameters {
		if parameter.In == "header" && headers[strings.ToLower(parameter.Name)] {
			return Input[Pair[A, B]]{err: configError("input", "sequence must not read the same header twice", nil)}
		}
	}
	definition := &inputDefinition{
		parameters: append(slices.Clone(left.parameters), right.parameters...),
		body:       left.body, paths: append(slices.Clone(left.paths), right.paths...),
		query: left.query || right.query, noQuery: left.noQuery || right.noQuery,
		children: []*inputDefinition{left, right},
	}
	if definition.body == nil {
		definition.body = right.body
	}
	if definition.noQuery && hasQueryParameters(definition.parameters) {
		return Input[Pair[A, B]]{err: configError("input", "NoQuery conflicts with declared query parameters", nil)}
	}
	failures := slices.Clone(first.failures)
	for _, status := range second.failures {
		if !slices.Contains(failures, status) {
			failures = append(failures, status)
		}
	}
	return Input[Pair[A, B]]{definition: definition, failures: failures, depth: depth, stages: stages,
		read: func(request *web.Request, actor auth.Principal) (Pair[A, B], validation.Errors, error) {
			var zero Pair[A, B]
			left, diagnostics, err := first.read(request, actor)
			if cancelled := request.Context().Err(); cancelled != nil {
				return zero, validation.Errors{}, errors.Join(cancelled, err)
			}
			if err != nil || !diagnostics.Empty() {
				return zero, diagnostics, err
			}
			right, diagnostics, err := second.read(request, actor)
			if cancelled := request.Context().Err(); cancelled != nil {
				return zero, validation.Errors{}, errors.Join(cancelled, err)
			}
			if err != nil || !diagnostics.Empty() {
				return zero, diagnostics, err
			}
			return Pair[A, B]{First: left, Second: right}, validation.Errors{}, nil
		},
	}
}

// Resolve runs explicit application validation or a read-only lookup after
// the input's closed validation. The input's wire declaration stays unchanged;
// only the successfully prepared application value changes type. The callback
// gets the admitted actor and borrowed request, must be concurrency-safe, and
// must neither consume the body nor retain the request. It must not commit a
// write: the final handler owns that outcome. Return Reject directly for an
// expected client failure declared in Config.Errors. Errors skip later stages.
func Resolve[A, B any](input Input[A], prepare func(*web.Request, auth.Principal, A) (B, error)) Input[B] {
	if input.err != nil {
		return Input[B]{err: input.err}
	}
	if input.definition == nil || input.read == nil || prepare == nil {
		return Input[B]{err: configError("input", "resolution requires an input and preparation callback", nil)}
	}
	if input.depth >= maximumInputDepth {
		return Input[B]{err: configError("input", "input pipeline exceeds 64 stages", nil)}
	}
	if input.stages >= maximumInputStages {
		return Input[B]{err: configError("input", "input pipeline exceeds 4096 execution stages", nil)}
	}
	return Input[B]{definition: input.definition, failures: slices.Clone(input.failures), depth: input.depth + 1, stages: input.stages + 1,
		read: func(request *web.Request, actor auth.Principal) (B, validation.Errors, error) {
			var zero B
			value, diagnostics, err := input.read(request, actor)
			if cancelled := request.Context().Err(); cancelled != nil {
				return zero, validation.Errors{}, errors.Join(cancelled, err)
			}
			if err != nil || !diagnostics.Empty() {
				return zero, diagnostics, err
			}
			prepared, err := prepare(request, actor, value)
			if cancelled := request.Context().Err(); cancelled != nil {
				return zero, validation.Errors{}, errors.Join(cancelled, err)
			}
			if err != nil {
				if rejected, expected := err.(*rejection); expected && rejected != nil {
					return zero, validation.Errors{}, err
				}
				return zero, validation.Errors{}, &resolutionFailure{cause: err}
			}
			return prepared, validation.Errors{}, nil
		},
	}
}

// Traverse source identity instead of the combined metadata. Independently
// composed pipelines can reuse one Body without redefining its named component.
func inputSchemas(inputs ...*inputDefinition) ([]openapi.NamedSchema, error) {
	stack := slices.Clone(inputs)
	var schemas []openapi.NamedSchema
	seen := make(map[*inputDefinition]bool)
	for len(stack) != 0 {
		current := stack[0]
		stack = stack[1:]
		if current == nil {
			return nil, configError("input", "input definition is absent", nil)
		}
		if seen[current] {
			continue
		}
		if len(seen) >= 4096 {
			return nil, configError("input", "input declarations exceed 4096 nodes", nil)
		}
		seen[current] = true
		schemas = append(schemas, current.schemas...)
		stack = append(stack, current.children...)
	}
	return schemas, nil
}

func expectedInputError(err error, policy errorPolicy) (web.Response, bool, error) {
	if failure, expected := err.(*rejection); expected && failure != nil {
		response, err := policy.response(failure.status, failure.code, failure.diagnostics)
		return response, true, err
	}
	return api.RequestErrorResponse(err)
}

// Keep callback failures distinct from a closed parser's direct API Error.
// A callback must use Reject for an expected client outcome, while callers
// can still inspect every original internal cause with errors.Is/As.
type resolutionFailure struct{ cause error }

func (*resolutionFailure) Error() string         { return "api endpoint: input preparation failed" }
func (resolutionFailure) GoString() string       { return "api endpoint: input preparation failed" }
func (failure *resolutionFailure) Unwrap() error { return failure.cause }
