package web

import (
	"context"
	"net/http"
	"sync/atomic"

	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/settings"
)

var inactiveRequestContext = func() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}()

// Request is borrowed for exactly one synchronous handler and middleware
// invocation. Context is authoritative for all request-scoped I/O.
type Request struct {
	httpRequest *http.Request
	settings    settings.Settings
	reverse     func(string) (string, error)
	active      atomic.Bool
	nextCalls   []atomic.Uint32
}

func newRequest(
	request *http.Request,
	configured settings.Settings,
	reverse func(string) (string, error),
	middlewareCount int,
) *Request {
	result := &Request{
		httpRequest: request,
		settings:    configured,
		reverse:     reverse,
		nextCalls:   make([]atomic.Uint32, middlewareCount),
	}
	result.active.Store(true)
	return result
}

// Settings returns the immutable project settings snapshot while the Request
// is active.
func (r *Request) Settings() settings.Settings {
	if r == nil || !r.active.Load() {
		return settings.Settings{}
	}
	return r.settings
}

// Apps returns the immutable installed-app registry while the Request is
// active.
func (r *Request) Apps() apps.Registry {
	return r.Settings().Apps()
}

// Reverse resolves a namespaced static route while the Request is active.
func (r *Request) Reverse(name string) (string, error) {
	if r == nil || !r.active.Load() || r.reverse == nil {
		return "", &Error{Code: CodeInvalidRequest, Field: "reverse", Detail: "request is outside its borrowed lifetime"}
	}
	return r.reverse(name)
}

// Context returns the underlying request context while the Request is active.
// An inactive Request returns an already-canceled context so accidental late
// I/O fails closed.
func (r *Request) Context() context.Context {
	if r == nil || !r.active.Load() || r.httpRequest == nil || r.httpRequest.Context() == nil {
		return inactiveRequestContext
	}
	return r.httpRequest.Context()
}

// Method returns the exact request method while the Request is active.
func (r *Request) Method() string {
	if r == nil || !r.active.Load() || r.httpRequest == nil {
		return ""
	}
	return r.httpRequest.Method
}

// Path returns the decoded request URL path while the Request is active.
func (r *Request) Path() string {
	if r == nil || !r.active.Load() || r.httpRequest == nil || r.httpRequest.URL == nil {
		return ""
	}
	return r.httpRequest.URL.Path
}

// HTTP returns the borrowed standard-library request. It returns nil after the
// synchronous handler chain has completed.
func (r *Request) HTTP() *http.Request {
	if r == nil || !r.active.Load() {
		return nil
	}
	return r.httpRequest
}

func (r *Request) claimNext(index int) bool {
	if r == nil || !r.active.Load() || index < 0 || index >= len(r.nextCalls) {
		return false
	}
	return r.nextCalls[index].Add(1) == 1
}

func (r *Request) middlewareViolated() bool {
	if r == nil {
		return true
	}
	for index := range r.nextCalls {
		if r.nextCalls[index].Load() > 1 {
			return true
		}
	}
	return false
}

func (r *Request) release() {
	if r != nil {
		r.active.Store(false)
	}
}
