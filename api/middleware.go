package api

import (
	pathpkg "path"
	"strings"

	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

// Representation converts only lower Web 404/405 responses inside one
// canonical subtree. Status and sorted Allow are retained; non-API responses
// keep the lower Web plain-text representation.
func Representation(prefix string) (web.Middleware, error) {
	if !validAPIPrefix(prefix) {
		return nil, &Error{Code: FailureInvalidConfig, Field: "prefix", Detail: "API prefix must be a clean absolute path ending in slash"}
	}
	return func(next web.Handler) web.Handler {
		return func(request *web.Request) (web.Response, error) {
			if next == nil {
				return web.Response{}, &Error{Code: FailureInvalidConfig, Field: "handler", Detail: "API representation downstream is nil"}
			}
			response, err := next(request)
			if err != nil || request == nil || !strings.HasPrefix(request.Path(), prefix) {
				return response, err
			}
			routingCode, routingFailure := web.RoutingError(response)
			if !routingFailure {
				return response, nil
			}
			var code ResponseCode
			switch routingCode {
			case web.CodeRouteNotFound:
				code = CodeNotFound
			case web.CodeMethodNotAllowed:
				code = CodeMethodNotAllowed
			default:
				return response, nil
			}
			converted, err := ErrorResponse(response.Status(), code, validation.NewErrors())
			if err != nil {
				return web.Response{}, err
			}
			header := converted.Header()
			if allow := response.Header().Get("Allow"); allow != "" {
				header.Set("Allow", allow)
			}
			return web.NewResponse(converted.Status(), header, converted.Body())
		}
	}, nil
}

func validAPIPrefix(value string) bool {
	if value == "/" || !strings.HasPrefix(value, "/") || !strings.HasSuffix(value, "/") ||
		strings.ContainsAny(value, "?#\\") {
		return false
	}
	clean := strings.TrimSuffix(value, "/")
	return pathpkg.Clean(clean) == clean
}
