package main

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sync"
	"time"

	ab "example.com/godj-openapi-client/articlebearer"
	as "example.com/godj-openapi-client/articlesession"
	hs "example.com/godj-openapi-client/helpdesksession"
)

const (
	sessionCookieName = "godj_session"
	csrfCookieName    = "godj_csrf"
	csrfHeaderName    = "X-Godj-Csrftoken"
)

type bearerSource struct{ token string }

func (source bearerSource) BearerAuth(context.Context, ab.OperationName) (ab.BearerAuth, error) {
	return ab.BearerAuth{Token: source.token}, nil
}

// The jar only captures response cookies. It is deliberately not assigned to
// http.Client.Jar: generated security methods own each outgoing cookie once.
type sessionState struct {
	jar       http.CookieJar
	cookieURL *url.URL
	mu        sync.Mutex
	masked    string
	invalid   bool
}

func (state *sessionState) cookie(name string) string {
	for _, cookie := range state.jar.Cookies(state.cookieURL) {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

func (state *sessionState) header() string {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.invalid {
		return "invalid-csrf-probe"
	}
	return state.masked
}

func (state *sessionState) setMasked(value string) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.masked = value
}

func (state *sessionState) setInvalid(value bool) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.invalid = value
}

func (state *sessionState) ready(masked string) bool {
	cookie := state.cookie(csrfCookieName)
	if cookie == "" || masked == "" || cookie == masked {
		return false
	}
	state.setMasked(masked)
	return true
}

type articleSessionSource struct{ *sessionState }

func (source articleSessionSource) SessionAuth(context.Context, as.OperationName) (as.SessionAuth, error) {
	return as.SessionAuth{APIKey: source.cookie(sessionCookieName)}, nil
}

func (source articleSessionSource) CsrfCookie(context.Context, as.OperationName) (as.CsrfCookie, error) {
	return as.CsrfCookie{APIKey: source.cookie(csrfCookieName)}, nil
}

func (source articleSessionSource) CsrfHeader(context.Context, as.OperationName) (as.CsrfHeader, error) {
	return as.CsrfHeader{APIKey: source.header()}, nil
}

type helpdeskSessionSource struct{ *sessionState }

func (source helpdeskSessionSource) SessionAuth(context.Context, hs.OperationName) (hs.SessionAuth, error) {
	return hs.SessionAuth{APIKey: source.cookie(sessionCookieName)}, nil
}

func (source helpdeskSessionSource) CsrfCookie(context.Context, hs.OperationName) (hs.CsrfCookie, error) {
	return hs.CsrfCookie{APIKey: source.cookie(csrfCookieName)}, nil
}

func (source helpdeskSessionSource) CsrfHeader(context.Context, hs.OperationName) (hs.CsrfHeader, error) {
	return hs.CsrfHeader{APIKey: source.header()}, nil
}

type observedTransport struct {
	base    *http.Transport
	state   *sessionState
	mu      sync.Mutex
	status  int
	csrfSet bool
}

func (transport *observedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if transport.state != nil {
		counts := make(map[string]int)
		for _, cookie := range request.Cookies() {
			counts[cookie.Name]++
		}
		if counts[sessionCookieName] != 1 || counts[csrfCookieName] > 1 || request.Header.Get("Authorization") != "" {
			return nil, fail("session credential transport")
		}
		if request.Method != http.MethodGet && request.Method != http.MethodHead && request.Method != http.MethodOptions {
			if counts[csrfCookieName] != 1 || len(request.Header.Values(csrfHeaderName)) != 1 {
				return nil, fail("csrf credential transport")
			}
		}
	}
	response, err := transport.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	transport.mu.Lock()
	transport.status = response.StatusCode
	transport.mu.Unlock()
	if transport.state != nil {
		cookies := response.Cookies()
		transport.state.jar.SetCookies(request.URL, cookies)
		for _, cookie := range cookies {
			if cookie.Name == csrfCookieName && cookie.Value != "" && cookie.HttpOnly {
				transport.mu.Lock()
				transport.csrfSet = true
				transport.mu.Unlock()
			}
		}
	}
	return response, nil
}

func (transport *observedTransport) lastStatus() int {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return transport.status
}

func (transport *observedTransport) capturedCSRF() bool {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return transport.csrfSet
}

func newHTTPClient() (*http.Client, *observedTransport) {
	transport := &observedTransport{base: http.DefaultTransport.(*http.Transport).Clone()}
	transport.base.Proxy = nil
	return &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, transport
}

func newSessionClient(target endpoint, session string, path string) (*http.Client, *observedTransport, *sessionState, error) {
	address, err := url.Parse(target.URL)
	if err != nil {
		return nil, nil, nil, fail("session setup")
	}
	address.Path = path
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, nil, nil, fail("session cookie setup")
	}
	jar.SetCookies(address, []*http.Cookie{{Name: sessionCookieName, Value: session, Path: "/"}})
	state := &sessionState{jar: jar, cookieURL: address}
	client, transport := newHTTPClient()
	transport.state = state
	return client, transport, state, nil
}
