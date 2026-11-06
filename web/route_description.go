package web

import "strings"

// ValidateRouteDeclarations uses the same compiler as application routing to
// check names, methods, paths and conflicting route languages. It does not
// execute handlers. Installed namespace membership and collisions with routes
// outside this set remain the responsibility of application construction.
func ValidateRouteDeclarations(routes []Route) error {
	_, err := compileRouter(nil, routes)
	return err
}

// RoutePathDescription is a detached description of the router's accepted
// path grammar. Template uses {name} placeholders; parameters retain their kinds.
type RoutePathDescription struct {
	Template   string
	Parameters []RouteParameterDescription
}

// RouteParameterKind identifies a closed converter validated by the compiler.
type RouteParameterKind string

const (
	RouteParameterInt64  RouteParameterKind = "int64"
	RouteParameterString RouteParameterKind = "str"
)

// RouteParameterDescription owns the name, kind and maximum decoded UTF-8 byte
// count of one segment. Limits are not configurable through this detached value.
type RouteParameterDescription struct {
	Name     string
	Kind     RouteParameterKind
	MaxBytes int
}

// DescribeRoutePath uses the same compiler as routing and reverse, so consumers
// do not need to parse the closed int64/str route declaration grammar.
func DescribeRoutePath(path string) (RoutePathDescription, error) {
	pattern, err := compileRoutePath(path)
	if err != nil {
		return RoutePathDescription{}, err
	}
	if pattern == nil {
		return RoutePathDescription{Template: path}, nil
	}
	parts := make([]string, len(pattern.segments))
	parameters := make([]RouteParameterDescription, 0, len(pattern.parameters))
	for i, segment := range pattern.segments {
		parts[i] = segment.literal
		if segment.kind != routeParameterInvalid {
			parts[i] = "{" + segment.parameterName + "}"
			description := RouteParameterDescription{Name: segment.parameterName, Kind: RouteParameterInt64, MaxBytes: 19}
			if segment.kind == routeParameterString {
				description.Kind, description.MaxBytes = RouteParameterString, MaximumStringParameterBytes
			}
			parameters = append(parameters, description)
		}
	}
	return RoutePathDescription{
		Template: strings.Join(parts, "/"), Parameters: parameters,
	}, nil
}
