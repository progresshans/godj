package api

import (
	"strings"

	"github.com/progresshans/godj/web"
)

// JSONPolicy is the immutable configuration of JSON negotiation and routing
// errors for one API subtree. Its zero value installs neither policy.
// Applications use the same value for Middleware and API documentation.
type JSONPolicy struct {
	prefix     string
	middleware []web.Middleware
}

// NewJSONPolicy prepares the existing JSON negotiation and routing-error
// middleware in their required outermost-first order. It performs no I/O.
func NewJSONPolicy(prefix string) (JSONPolicy, error) {
	if _, err := web.DescribeRoutePrefix(prefix, prefix); err != nil {
		return JSONPolicy{}, &Error{Code: FailureInvalidConfig, Field: "prefix", Detail: "JSON policy requires a canonical static subtree", Cause: err}
	}
	negotiation, err := JSONNegotiation(prefix)
	if err != nil {
		return JSONPolicy{}, err
	}
	representation, err := Representation(prefix)
	if err != nil {
		return JSONPolicy{}, err
	}
	return JSONPolicy{prefix: prefix, middleware: []web.Middleware{negotiation, representation}}, nil
}

// Middleware returns a detached slice for web.Config.Middleware. Application
// construction must install this chain to apply the policy to routing failures
// as well as matched handlers.
func (p JSONPolicy) Middleware() []web.Middleware {
	return append([]web.Middleware(nil), p.middleware...)
}

// NegotiatesJSON reports whether this policy rejects unacceptable Accept
// headers at path. It uses the same subtree matching rule as the middleware.
func (p JSONPolicy) NegotiatesJSON(path string) bool {
	return p.prefix != "" && strings.HasPrefix(path, p.prefix)
}

// RouteNegotiation describes a route's complete accepted path language. A
// policy that covers only some values of a path parameter cannot be represented
// as one operation's negotiation behavior and is rejected explicitly.
func (p JSONPolicy) RouteNegotiation(routePath string) (bool, error) {
	if p.prefix == "" {
		return false, nil
	}
	coverage, err := web.DescribeRoutePrefix(routePath, p.prefix)
	if err != nil {
		return false, &Error{Code: FailureInvalidConfig, Field: "json_policy", Detail: "route coverage could not be described", Cause: err}
	}
	if coverage.Some && !coverage.All {
		return false, &Error{Code: FailureInvalidConfig, Field: "json_policy", Detail: "JSON policy covers only part of the operation's accepted paths"}
	}
	return coverage.All, nil
}
