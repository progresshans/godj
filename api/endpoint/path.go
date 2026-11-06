package endpoint

import (
	"strings"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

type pathBinding struct {
	name string
	kind web.RouteParameterKind
}

// PathInt64 reads the router's already parsed, non-negative int64 segment.
// New verifies its name and converter against the same route compiler used by
// HTTP dispatch and OpenAPI. Resource-specific rules, such as positive keys or
// object existence, belong to a subsequent Resolve stage or the handler.
func PathInt64(name string) Input[int64] {
	return Input[int64]{
		definition: &inputDefinition{paths: []pathBinding{{name: name, kind: web.RouteParameterInt64}}}, depth: 1, stages: 1,
		read: func(request *web.Request, _ auth.Principal) (int64, validation.Errors, error) {
			value, ok := request.Int64Parameter(name)
			if !ok {
				return 0, validation.Errors{}, configError("input.path", "request has no matching integer path binding", nil)
			}
			return value, validation.Errors{}, nil
		},
	}
}

// PathString reads one already decoded router segment without trimming or
// decoding again. The returned string owns its bounded bytes independently of
// the borrowed request. New checks the route's str converter and parameter name.
func PathString(name string) Input[string] {
	return Input[string]{
		definition: &inputDefinition{paths: []pathBinding{{name: name, kind: web.RouteParameterString}}}, depth: 1, stages: 1,
		read: func(request *web.Request, _ auth.Principal) (string, validation.Errors, error) {
			value, ok := request.StringParameter(name)
			if !ok {
				return "", validation.Errors{}, configError("input.path", "request has no matching string path binding", nil)
			}
			return strings.Clone(value), validation.Errors{}, nil
		},
	}
}

func validatePathBindings(path string, bindings []pathBinding) error {
	if len(bindings) == 0 {
		return nil
	}
	description, err := web.DescribeRoutePath(path)
	if err != nil {
		return err
	}
	if len(description.Parameters) != len(bindings) {
		return configError("input.path", "typed path input must bind every route parameter exactly once", nil)
	}
	declared := make(map[string]web.RouteParameterKind, len(description.Parameters))
	for _, parameter := range description.Parameters {
		declared[parameter.Name] = parameter.Kind
	}
	seen := make(map[string]bool, len(bindings))
	for _, binding := range bindings {
		if seen[binding.name] || declared[binding.name] != binding.kind {
			return configError("input.path", "path input is duplicated, unknown or bound to another converter", nil)
		}
		seen[binding.name] = true
	}
	return nil
}
