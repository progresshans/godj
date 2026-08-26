package sessionauth

import (
	"context"
	"errors"
	"math"
	"net/http"
	"time"

	"github.com/progresshans/godj/web"
)

type ResponseChange struct {
	cookies []http.Cookie
}

// Apply derives a response carrying this change's Set-Cookie headers.
func (change ResponseChange) Apply(response web.Response) (web.Response, error) {
	if len(change.cookies) == 0 {
		return response, nil
	}
	header := response.Header()
	for _, cookie := range change.cookies {
		header.Add("Set-Cookie", cookie.String())
	}
	updated, err := web.NewResponse(response.Status(), header, response.Body())
	if err != nil {
		return web.Response{}, &Error{Code: CodeResponse, Detail: "response cookie application failed", Cause: err}
	}
	return updated, nil
}

func (ResponseChange) String() string   { return "sessionauth.ResponseChange{redacted}" }
func (ResponseChange) GoString() string { return "sessionauth.ResponseChange{redacted}" }

func (r *Runtime) request(request *web.Request) (*http.Request, error) {
	if request == nil {
		return nil, &Error{Code: CodeInvalidRequest, Detail: "web request is nil or outside its borrowed lifetime"}
	}
	httpRequest := request.HTTP()
	if httpRequest == nil || httpRequest.Context() == nil || httpRequest.URL == nil {
		return nil, &Error{Code: CodeInvalidRequest, Detail: "web request is nil or outside its borrowed lifetime"}
	}
	if err := httpRequest.Context().Err(); err != nil {
		return nil, err
	}
	if r == nil || r.sessions == nil || r.authenticator == nil || r.authorizer == nil || r.random == nil || r.clock == nil || !r.csrfKeyRing.Valid() {
		return nil, &Error{Code: CodeInvalidConfig, Detail: "session-auth runtime is nil or uninitialized"}
	}
	return httpRequest, nil
}

func (r *Runtime) namedCookie(request *http.Request, name string) (string, bool, error) {
	total := 0
	for _, header := range request.Header.Values("Cookie") {
		total += len(header)
		if total > r.limits.MaxCookieHeaderBytes {
			return "", false, &Error{Code: CodeInvalidRequest, Field: "cookie", Detail: "cookie header exceeds the configured resource limit"}
		}
	}
	cookies := request.CookiesNamed(name)
	if len(cookies) == 0 {
		return "", false, nil
	}
	if len(cookies) != 1 || cookies[0] == nil || len(cookies[0].Value) > r.limits.MaxCookieValueBytes {
		return "", false, &Error{Code: CodeInvalidRequest, Field: "cookie", Detail: "cookie is duplicated or outside the configured resource limit"}
	}
	return cookies[0].Value, true, nil
}

func (r *Runtime) sessionResponseCookie(value string, expires time.Time) http.Cookie {
	if r.sessionCookie.Lifetime > 0 {
		configuredExpiry := r.now().Add(r.sessionCookie.Lifetime)
		if configuredExpiry.Before(expires) {
			expires = configuredExpiry
		}
	}
	return r.responseCookie(r.sessionCookie, value, expires)
}

func (r *Runtime) csrfResponseCookie(value string) http.Cookie {
	now := r.now()
	return r.responseCookie(r.csrfCookie, value, now.Add(r.csrfCookie.Lifetime))
}

func (r *Runtime) responseCookie(config CookieConfig, value string, expires time.Time) http.Cookie {
	now := r.now()
	maxAge := int64(math.Ceil(expires.Sub(now).Seconds()))
	if maxAge < 1 {
		maxAge = 1
	}
	if maxAge > int64(math.MaxInt) {
		maxAge = int64(math.MaxInt)
	}
	return http.Cookie{
		Name:     config.Name,
		Value:    value,
		Path:     config.Path,
		Domain:   config.Domain,
		Expires:  expires.Round(0).UTC(),
		MaxAge:   int(maxAge),
		Secure:   config.Secure,
		HttpOnly: true,
		SameSite: config.SameSite,
	}
}

func (r *Runtime) deletionCookie(config CookieConfig) http.Cookie {
	return http.Cookie{
		Name:     config.Name,
		Value:    "",
		Path:     config.Path,
		Domain:   config.Domain,
		Expires:  time.Unix(1, 0).UTC(),
		MaxAge:   -1,
		Secure:   config.Secure,
		HttpOnly: true,
		SameSite: config.SameSite,
	}
}

func (r *Runtime) now() time.Time {
	r.clockMu.Lock()
	value := r.clock().Round(0).UTC()
	r.clockMu.Unlock()
	if value.IsZero() {
		return time.Unix(1, 0).UTC()
	}
	return value
}

func sessionFailure(detail string, cause error) error {
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return cause
	}
	return &Error{Code: CodeSession, Detail: detail, Cause: cause}
}

func authenticationFailure(detail string, cause error) error {
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return cause
	}
	return &Error{Code: CodeAuthentication, Detail: detail, Cause: cause}
}
