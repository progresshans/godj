package api

import (
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/web"
)

// AuthenticatedHandler is an application handler that receives the principal
// resolved by one explicit API authentication profile.
type AuthenticatedHandler func(*web.Request, auth.Principal) (web.Response, error)

// Authentication constructs a handler protected by every declared permission.
// Credentials and CSRF are evaluated once; authorization checks the primary
// and additional permissions in declaration order before the handler runs.
// Implementations retain a detached, validated permission snapshot.
// Construction failures are returned before the caller publishes any route;
// successful construction must return a non-nil handler.
type Authentication interface {
	Require(auth.Permission, AuthenticatedHandler, ...auth.Permission) (web.Handler, error)
}

// PrincipalAuthentication admits an active authenticated principal without a
// model-permission requirement. This is explicit: an empty permission passed
// to Require is still an error. The transport's credential and CSRF checks run
// before the handler; no permission overlay can grant anonymous access.
type PrincipalAuthentication interface {
	Authentication
	RequireAuthenticated(AuthenticatedHandler) (web.Handler, error)
}

// AlternativeAuthentication additionally supports a nonempty disjunction. The
// credential and CSRF boundary is evaluated once. Each candidate uses its deny
// overlay; a definite denial permits the next candidate, but an execution error
// ends the request without consulting alternatives or invoking the handler.
type AlternativeAuthentication interface {
	Authentication
	RequireAny(auth.Permission, AuthenticatedHandler, ...auth.Permission) (web.Handler, error)
}

// AuthenticationKind names the accepted, mutually exclusive API profiles.
type AuthenticationKind uint8

const (
	AuthenticationSession AuthenticationKind = iota + 1
	AuthenticationBearer
)

// AuthenticationDescription contains public transport names only. It must not
// contain credentials, tokens, cookie values, verifier settings, or key material.
type AuthenticationDescription struct {
	Kind              AuthenticationKind
	SessionCookieName string
	CSRFCookieName    string
	CSRFHeader        string
}

// AuthenticationDescriber is an optional documentation capability. Adapters
// without it remain usable as Authentication, but cannot publish a document
// that would otherwise have to guess their authentication behavior.
type AuthenticationDescriber interface {
	DescribeAuthentication() (AuthenticationDescription, error)
}
