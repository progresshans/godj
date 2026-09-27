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

// Runtime is an immutable API policy adapter around the accepted Web session
// runtime. Its zero value is invalid.
type Runtime struct {
	runtime *websessionauth.Runtime
	inspect bool
}

var _ api.AlternativeAuthentication = (*Runtime)(nil)
var _ api.PrincipalAuthentication = (*Runtime)(nil)
var _ api.CSRFAuthentication = (*Runtime)(nil)

type Option interface{ apply(*Runtime) }
type option func(*Runtime)

func (o option) apply(runtime *Runtime) { o(runtime) }

// WithReadOnlyResolution uses InspectPrincipal for credential admission. It
// neither refreshes nor cleans up server sessions. Use it when a final mutation
// owner couples all session effects to a transaction. CSRF checks and safe
// response tokens remain enabled; this option never changes authorization.
func WithReadOnlyResolution() Option {
	return option(func(runtime *Runtime) { runtime.inspect = true })
}

func New(runtime *websessionauth.Runtime, options ...Option) (*Runtime, error) {
	if runtime == nil {
		return nil, &Error{Code: CodeInvalidConfig, Field: "runtime", Detail: "session-auth runtime is nil"}
	}
	if runtime.CSRFHeader() == "" {
		return nil, &Error{Code: CodeInvalidConfig, Field: "csrf_header", Detail: "session-auth runtime has no CSRF header"}
	}
	prepared := &Runtime{runtime: runtime}
	for _, option := range options {
		if option == nil {
			return nil, &Error{Code: CodeInvalidConfig, Field: "option", Detail: "session authentication option is nil"}
		}
		option.apply(prepared)
	}
	return prepared, nil
}

type permissionMode uint8

const (
	allPermissions permissionMode = iota
	anyPermission
	onlyAuthenticated
)

func (r *Runtime) RequireAuthenticated(handler api.AuthenticatedHandler) (web.Handler, error) {
	return r.protect(onlyAuthenticated, "", handler)
}

// RequireCSRF admits anonymous or signed-in browsers without resolving a
// principal. Session credential cookies, including expired/malformed ones, are
// not consulted. The application handler owns proof/account-specific admission.
func (r *Runtime) RequireCSRF(handler web.Handler) (web.Handler, error) {
	if r == nil || r.runtime == nil || r.runtime.CSRFHeader() == "" {
		return nil, &Error{Code: CodeInvalidConfig, Field: "runtime", Detail: "API session runtime is nil or uninitialized"}
	}
	if handler == nil {
		return nil, &Error{Code: CodeInvalidConfig, Field: "handler", Detail: "CSRF API handler is nil"}
	}
	return func(request *web.Request) (web.Response, error) {
		if err := r.runtime.VerifyCSRF(request, nil); err != nil {
			if classified, ok := err.(*websessionauth.Error); ok && classified != nil && classified.Code == websessionauth.CodeCSRFRejected && classified.Cause == nil {
				return api.ErrorResponse(http.StatusForbidden, api.CodeCSRFRejected, validation.NewErrors())
			}
			return web.Response{}, err
		}
		response, err := handler(request)
		if err != nil || !safeMethod(request.Method()) {
			return response, err
		}
		return r.applySafeToken(request, response)
	}, nil
}

// Require resolves an authenticated principal, checks unsafe-method CSRF,
// applies every explicit permission, and only then invokes application parsing
// or persistence. Expected denial responses are JSON 403 without redirects or
// WWW-Authenticate.
func (r *Runtime) Require(permission auth.Permission, handler api.AuthenticatedHandler, additional ...auth.Permission) (web.Handler, error) {
	return r.protect(allPermissions, permission, handler, additional...)
}

// RequireAny accepts the first explicitly granted permission whose deny overlay
// allows the request. Credential/CSRF checks run once; errors never fall back.
func (r *Runtime) RequireAny(permission auth.Permission, handler api.AuthenticatedHandler, alternatives ...auth.Permission) (web.Handler, error) {
	return r.protect(anyPermission, permission, handler, alternatives...)
}

func (r *Runtime) protect(mode permissionMode, permission auth.Permission, handler api.AuthenticatedHandler, additional ...auth.Permission) (web.Handler, error) {
	if r == nil || r.runtime == nil {
		return nil, &Error{Code: CodeInvalidConfig, Field: "runtime", Detail: "API session runtime is nil or uninitialized"}
	}
	if r.runtime.CSRFHeader() == "" {
		return nil, &Error{Code: CodeInvalidConfig, Field: "csrf_header", Detail: "session-auth runtime has no CSRF header"}
	}
	var permissions []auth.Permission
	if mode != onlyAuthenticated {
		var err error
		permissions, err = auth.RequiredPermissions(permission, additional...)
		if err != nil {
			return nil, &Error{Code: CodeInvalidConfig, Field: "permission", Detail: "permission is invalid"}
		}
	}
	if handler == nil {
		return nil, &Error{Code: CodeInvalidConfig, Field: "handler", Detail: "authenticated API handler is nil"}
	}

	return func(request *web.Request) (web.Response, error) {
		resolve := r.runtime.Principal
		if r.inspect {
			resolve = r.runtime.InspectPrincipal
		}
		principal, err := resolve(request)
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
		granted := false
		for _, required := range permissions {
			allowed, err := r.runtime.Authorized(request.Context(), principal, required)
			if err != nil {
				return web.Response{}, err
			}
			if mode == anyPermission {
				if allowed {
					granted = true
					break
				}
			} else if !allowed {
				return api.ErrorResponse(http.StatusForbidden, api.CodePermissionDenied, validation.NewErrors())
			}
		}
		if mode == anyPermission && !granted {
			return api.ErrorResponse(http.StatusForbidden, api.CodePermissionDenied, validation.NewErrors())
		}
		response, err := handler(request, principal)
		if err != nil || !safeMethod(request.Method()) {
			return response, err
		}
		return r.applySafeToken(request, response)
	}, nil
}

func safeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}

// applySafeToken shares the same transport publication for both admissions.
func (r *Runtime) applySafeToken(request *web.Request, response web.Response) (web.Response, error) {
	token, err := r.runtime.CSRFToken(request)
	if err != nil {
		return web.Response{}, err
	}
	header := response.Header()
	if header == nil {
		header = make(http.Header)
	}
	header.Set(r.runtime.CSRFHeader(), token.Value())
	response, err = response.WithHeaders(header)
	if err != nil {
		return web.Response{}, &Error{Code: CodeResponse, Field: "csrf_header", Detail: "CSRF response header could not be applied", Cause: err}
	}
	response, err = token.Apply(response)
	if err != nil {
		return web.Response{}, &Error{Code: CodeResponse, Field: "csrf_cookie", Detail: "CSRF response cookie could not be applied", Cause: err}
	}
	return response, nil
}
