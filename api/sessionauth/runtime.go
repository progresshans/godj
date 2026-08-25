// Package sessionauth adapts the Web session runtime to JSON API semantics.
// Typed principals remain explicit arguments and are never hidden in context.
package sessionauth

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

type ErrorCode string

const (
	CodeInvalidConfig ErrorCode = "invalid_config"
	CodeResponse      ErrorCode = "response_failure"
)

type Error struct {
	Code   ErrorCode
	Field  string
	Detail string
	Cause  error `json:"-"`
}

func (e *Error) Error() string {
	if e == nil {
		return "api/sessionauth: <nil>"
	}
	if e.Field == "" {
		return fmt.Sprintf("api/sessionauth: %s: %s", e.Code, e.Detail)
	}
	return fmt.Sprintf("api/sessionauth: %s: %s: %s", e.Code, e.Field, e.Detail)
}

// GoString keeps diagnostic %#v formatting on the same framework-owned,
// secret-free surface as Error while Unwrap retains Cause for errors.Is/As.
func (e Error) GoString() string { return (&e).Error() }

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *Error) Is(target error) bool {
	want, ok := target.(*Error)
	if !ok || e == nil || want == nil {
		return false
	}
	return (want.Code == "" || e.Code == want.Code) &&
		(want.Field == "" || e.Field == want.Field)
}

type AuthenticatedHandler func(*web.Request, auth.Principal) (web.Response, error)

// Runtime is an immutable API policy adapter around the accepted Web session
// runtime. Its zero value is invalid.
type Runtime struct {
	runtime *websessionauth.Runtime
}

func New(runtime *websessionauth.Runtime) (*Runtime, error) {
	if runtime == nil || runtime.CSRFHeader() == "" {
		return nil, &Error{Code: CodeInvalidConfig, Field: "runtime", Detail: "session-auth runtime is nil or invalid"}
	}
	return &Runtime{runtime: runtime}, nil
}

// Require resolves an authenticated principal, checks unsafe-method CSRF,
// applies one explicit permission, and only then invokes application parsing
// or persistence. Expected denial responses are JSON 403 without redirects or
// WWW-Authenticate.
func (r *Runtime) Require(permission auth.Permission, handler AuthenticatedHandler) web.Handler {
	return func(request *web.Request) (web.Response, error) {
		if r == nil || r.runtime == nil {
			return web.Response{}, &Error{Code: CodeInvalidConfig, Field: "runtime", Detail: "API session runtime is nil or invalid"}
		}
		if handler == nil {
			return web.Response{}, &Error{Code: CodeInvalidConfig, Field: "handler", Detail: "authenticated API handler is nil"}
		}
		principal, err := r.runtime.Principal(request)
		if err != nil {
			return web.Response{}, err
		}
		if !principal.Authenticated() {
			return api.ErrorResponse(http.StatusForbidden, api.CodeNotAuthenticated, validation.NewErrors())
		}
		if !safeMethod(request.Method()) {
			if err := r.runtime.VerifyCSRF(request, nil); err != nil {
				if errors.Is(err, &websessionauth.Error{Code: websessionauth.CodeCSRFRejected}) {
					return api.ErrorResponse(http.StatusForbidden, api.CodeCSRFRejected, validation.NewErrors())
				}
				return web.Response{}, err
			}
		}
		allowed, err := r.runtime.Authorized(request.Context(), principal, permission)
		if err != nil {
			return web.Response{}, err
		}
		if !allowed {
			return api.ErrorResponse(http.StatusForbidden, api.CodePermissionDenied, validation.NewErrors())
		}
		response, err := handler(request, principal)
		if err != nil || !safeMethod(request.Method()) {
			return response, err
		}
		token, err := r.runtime.CSRFToken(request)
		if err != nil {
			return web.Response{}, err
		}
		header := response.Header()
		if header == nil {
			header = make(http.Header)
		}
		header.Set(r.runtime.CSRFHeader(), token.Value())
		response, err = web.NewResponse(response.Status(), header, response.Body())
		if err != nil {
			return web.Response{}, &Error{Code: CodeResponse, Field: "csrf_header", Detail: "CSRF response header could not be applied", Cause: err}
		}
		response, err = token.Apply(response)
		if err != nil {
			return web.Response{}, &Error{Code: CodeResponse, Field: "csrf_cookie", Detail: "CSRF response cookie could not be applied", Cause: err}
		}
		return response, nil
	}
}

func safeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}
