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

// Route binds one exact uppercase HTTP method and one clean static path to a
// namespaced route name and handler.
type Route struct {
	Name    string
	Method  string
	Path    string
	Handler Handler
}

// Config is the immutable Application startup input.
type Config struct {
	Settings         settings.Settings
	Routes           []Route
	Middleware       []Middleware
	Logger           *slog.Logger
	MaxResponseBytes int64
}
