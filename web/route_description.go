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
// path grammar. Template uses {name} placeholders; all parameters are int64.
type RoutePathDescription struct {
	Template   string
	Parameters []string
}

// DescribeRoutePath uses the same compiler as routing and reverse, so consumers
// do not need to reimplement the closed <int64:name> grammar.
func DescribeRoutePath(path string) (RoutePathDescription, error) {
	pattern, err := compileRoutePath(path)
	if err != nil {
		return RoutePathDescription{}, err
	}
	if pattern == nil {
		return RoutePathDescription{Template: path}, nil
	}
	parts := make([]string, len(pattern.segments))
	for i, segment := range pattern.segments {
		parts[i] = segment.literal
		if segment.kind == routeParameterInt64 {
			parts[i] = "{" + segment.parameterName + "}"
		}
	}
	return RoutePathDescription{
		Template: strings.Join(parts, "/"), Parameters: append([]string(nil), pattern.parameters...),
	}, nil
}
