package api

import (
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/web"
)

// AuthenticatedHandler is an application handler that receives the principal
// resolved by one explicit API authentication profile.
type AuthenticatedHandler func(*web.Request, auth.Principal) (web.Response, error)

// Authentication constructs a handler protected by one explicit permission.
// Construction failures are returned before the caller publishes any route;
// successful construction must return a non-nil handler.
type Authentication interface {
	Require(auth.Permission, AuthenticatedHandler) (web.Handler, error)
}
