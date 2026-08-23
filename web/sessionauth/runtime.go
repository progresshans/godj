package sessionauth

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/web"
)

const principalSessionKey = "_godj_principal_id"

type AuthenticatedHandler func(*web.Request, auth.Principal) (web.Response, error)

// Principal resolves one active typed principal. A missing, malformed, stale,
// inactive or unknown session is anonymous; no-session requests do not write a
// session. Valid active loads advance the Manager's idle expiry.
func (r *Runtime) Principal(request *web.Request) (auth.Principal, error) {
	httpRequest, err := r.request(request)
	if err != nil {
		return auth.Principal{}, err
	}
	encoded, found, cookieErr := r.namedCookie(httpRequest, r.sessionCookie.Name)
	if cookieErr != nil || !found {
		return auth.Anonymous(), nil
	}
	id, err := sessions.ParseID(encoded)
	if err != nil {
		return auth.Anonymous(), nil
	}
	record, found, err := r.sessions.Load(httpRequest.Context(), id)
	if err != nil {
		return auth.Principal{}, sessionFailure("session load failed", err)
	}
	if !found {
		return auth.Anonymous(), nil
	}
	principalID, found := record.Value(principalSessionKey)
	if !found || principalID == "" {
		return auth.Anonymous(), nil
	}
	principal, err := r.authenticator.Resolve(httpRequest.Context(), principalID)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		if flushErr := r.sessions.Flush(httpRequest.Context(), id); flushErr != nil {
			return auth.Principal{}, sessionFailure("invalid principal session flush failed", flushErr)
		}
		return auth.Anonymous(), nil
	}
	if err != nil {
		return auth.Principal{}, authenticationFailure("principal resolution failed", err)
	}
	if !principal.Authenticated() {
		return auth.Anonymous(), nil
	}
	return principal, nil
}

func (r *Runtime) Optional(handler AuthenticatedHandler) web.Handler {
	return func(request *web.Request) (web.Response, error) {
		if handler == nil {
			return web.Response{}, &Error{Code: CodeInvalidConfig, Field: "handler", Detail: "authenticated handler is nil"}
		}
		principal, err := r.Principal(request)
		if err != nil {
			return web.Response{}, err
		}
		return handler(request, principal)
	}
}

// Require redirects an anonymous request to LoginPath and returns a stable 403
// for an authenticated principal without permission. The typed principal is
// passed explicitly and is never stored in context or web.Request.
func (r *Runtime) Require(permission auth.Permission, handler AuthenticatedHandler) web.Handler {
	return func(request *web.Request) (web.Response, error) {
		if handler == nil {
			return web.Response{}, &Error{Code: CodeInvalidConfig, Field: "handler", Detail: "authenticated handler is nil"}
		}
		principal, err := r.Principal(request)
		if err != nil {
			return web.Response{}, err
		}
		if !principal.Authenticated() {
			next := r.fallbackPath
			if httpRequest := request.HTTP(); httpRequest != nil && httpRequest.URL != nil {
				next = r.SafeNext(httpRequest.URL.RequestURI())
			}
			return redirectResponse(r.loginPath + "?next=" + url.QueryEscape(next))
		}
		allowed, err := r.authorizer.Allowed(request.Context(), principal, permission)
		if err != nil {
			return web.Response{}, &Error{Code: CodeAuthorization, Detail: "permission evaluation failed", Cause: err}
		}
		if !allowed {
			return forbiddenResponse()
		}
		return handler(request, principal)
	}
}

type LoginResult struct {
	principal auth.Principal
	change    ResponseChange
}

func (result LoginResult) Principal() auth.Principal { return result.principal }
func (result LoginResult) Apply(response web.Response) (web.Response, error) {
	return result.change.Apply(response)
}
func (LoginResult) String() string   { return "sessionauth.LoginResult{redacted}" }
func (LoginResult) GoString() string { return "sessionauth.LoginResult{redacted}" }

// Login verifies credentials without session writes on failure. On success it
// creates or rotates the server session and always rotates the independent CSRF
// cookie secret. Callers verify the login POST's pre-login CSRF token first.
func (r *Runtime) Login(request *web.Request, username, password string) (LoginResult, error) {
	httpRequest, err := r.request(request)
	if err != nil {
		return LoginResult{}, err
	}
	principal, err := r.authenticator.Authenticate(httpRequest.Context(), username, password)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		return LoginResult{}, auth.ErrInvalidCredentials
	}
	if err != nil {
		return LoginResult{}, authenticationFailure("credential verification failed", err)
	}
	if !principal.Authenticated() {
		return LoginResult{}, auth.ErrInvalidCredentials
	}
	csrfSecret, err := r.newCSRFSecret()
	if err != nil {
		return LoginResult{}, err
	}
	var record sessions.Record
	encoded, cookieFound, cookieErr := r.namedCookie(httpRequest, r.sessionCookie.Name)
	if cookieErr != nil {
		return LoginResult{}, cookieErr
	}
	if cookieFound {
		if id, parseErr := sessions.ParseID(encoded); parseErr == nil {
			loaded, found, loadErr := r.sessions.Load(httpRequest.Context(), id)
			if loadErr != nil {
				return LoginResult{}, sessionFailure("session load before login failed", loadErr)
			}
			if found {
				loaded, deriveErr := loaded.WithValue(principalSessionKey, principal.ID())
				if deriveErr != nil {
					return LoginResult{}, sessionFailure("session authentication state is invalid", deriveErr)
				}
				record, err = r.sessions.Rotate(httpRequest.Context(), loaded)
				if err != nil {
					return LoginResult{}, sessionFailure("session rotation failed", err)
				}
			}
		}
	}
	if !record.ID().Valid() {
		record, err = r.sessions.Create(httpRequest.Context(), map[string]string{principalSessionKey: principal.ID()})
		if err != nil {
			return LoginResult{}, sessionFailure("session creation failed", err)
		}
	}
	change := ResponseChange{cookies: []http.Cookie{
		r.sessionResponseCookie(record.ID().Encoded(), record.AbsoluteExpiresAt()),
		r.csrfResponseCookie(csrfSecret),
	}}
	return LoginResult{principal: principal, change: change}, nil
}

// Logout flushes all server-side values for the presented session and deletes
// both bearer cookies. It is idempotent for a missing or malformed cookie.
func (r *Runtime) Logout(request *web.Request) (ResponseChange, error) {
	httpRequest, err := r.request(request)
	if err != nil {
		return ResponseChange{}, err
	}
	encoded, found, cookieErr := r.namedCookie(httpRequest, r.sessionCookie.Name)
	if cookieErr != nil {
		return ResponseChange{}, cookieErr
	}
	if found {
		if id, parseErr := sessions.ParseID(encoded); parseErr == nil {
			if err := r.sessions.Flush(httpRequest.Context(), id); err != nil {
				return ResponseChange{}, sessionFailure("session flush failed", err)
			}
		}
	}
	return ResponseChange{cookies: []http.Cookie{
		r.deletionCookie(r.sessionCookie),
		r.deletionCookie(r.csrfCookie),
	}}, nil
}

// SafeNext returns raw only when it is a bounded local URI whose exact path is
// registered in the runtime allowlist. Everything else uses FallbackPath.
func (r *Runtime) SafeNext(raw string) string {
	if r == nil || !validLocalRequestURI(raw, r.allowedNextPaths, r.limits.MaxNextBytes) {
		if r == nil {
			return defaultFallbackPath
		}
		return r.fallbackPath
	}
	return raw
}

func redirectResponse(location string) (web.Response, error) {
	header := make(http.Header)
	header.Set("Content-Type", "text/plain; charset=utf-8")
	header.Set("Location", location)
	return web.NewResponse(http.StatusFound, header, []byte("Found\n"))
}

func forbiddenResponse() (web.Response, error) {
	header := make(http.Header)
	header.Set("Content-Type", "text/plain; charset=utf-8")
	return web.NewResponse(http.StatusForbidden, header, []byte("Forbidden\n"))
}
