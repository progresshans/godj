// Package web provides GoDj's minimal synchronous HTTP runtime. It owns
// settings-to-router startup, request/response lifetime, middleware ordering,
// and graceful server shutdown while leaving application representation and
// database access to app code.
package web

import (
	"log/slog"

	"github.com/progresshans/godj/settings"
)

const DefaultMaxResponseBytes int64 = 1 << 20

// Handler synchronously handles one borrowed request and returns one fully
// buffered response. The request and its raw HTTP request must not be retained
// after Handler returns.
type Handler func(*Request) (Response, error)

// Middleware wraps a synchronous Handler. The first declared middleware is
// outermost, and each middleware may invoke its downstream Handler at most
// once per request.
type Middleware func(Handler) Handler

// Route binds one exact uppercase HTTP method and one clean path to a
// namespaced route name and handler. Path may contain the closed
// <int64:name> segment converter; all other path syntax is static.
type Route struct {
	Name    string
	Method  string
	Path    string
	Handler Handler
}

// ReverseArgument is one closed, typed argument for parameterized route
// reversal. Its representation is intentionally private so callers cannot
// introduce converter kinds that the router did not validate at startup.
type ReverseArgument struct {
	name         string
	kind         routeParameterKind
	integerValue int64
}

// Int64Argument constructs a named signed 64-bit route argument. Negative
// values and invalid names are rejected by ReverseWith with a structured
// error, before any request I/O.
func Int64Argument(name string, value int64) ReverseArgument {
	return ReverseArgument{name: name, kind: routeParameterInt64, integerValue: value}
}

// Config is the immutable Application startup input.
type Config struct {
	Settings         settings.Settings
	Routes           []Route
	Middleware       []Middleware
	Logger           *slog.Logger
	MaxResponseBytes int64
}
