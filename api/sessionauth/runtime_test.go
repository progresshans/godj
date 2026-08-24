package sessionauth_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/api"
	apisessionauth "github.com/progresshans/godj/api/sessionauth"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

func TestRequireUsesJSON403CSRFPermissionAndSafeTokenHeader(t *testing.T) {
	harness := newAPIAuthHarness(t)

	anonymous := harness.request(t, http.MethodGet, "/api/articles/", false, nil, "")
	assertJSONCode(t, anonymous, http.StatusForbidden, api.CodeNotAuthenticated)
	if anonymous.Header.Get("Location") != "" || anonymous.Header.Get("WWW-Authenticate") != "" {
		t.Fatalf("anonymous headers = %#v", anonymous.Header)
	}
	if harness.calls.Load() != 0 {
		t.Fatalf("anonymous handler calls = %d", harness.calls.Load())
	}

	denied := harness.request(t, http.MethodDelete, "/api/denied/", true, nil, "placeholder")
	assertJSONCode(t, denied, http.StatusForbidden, api.CodeCSRFRejected)
	if harness.calls.Load() != 0 {
		t.Fatalf("CSRF-denied handler calls = %d", harness.calls.Load())
	}

	safe := harness.request(t, http.MethodGet, "/api/articles/", true, nil, "")
	if safe.StatusCode != http.StatusOK || safe.Header.Get("Content-Type") != api.JSONContentType {
		t.Fatalf("safe response = %d %#v", safe.StatusCode, safe.Header)
	}
	token := safe.Header.Get(websessionauth.DefaultCSRFHeader)
	if len(token) != 128 {
		t.Fatalf("masked token length = %d", len(token))
	}
	csrfCookie := namedCookie(t, safe.Cookies(), websessionauth.DefaultCSRFCookieName)
	if !csrfCookie.HttpOnly || csrfCookie.Value == "" || csrfCookie.Value == token {
		t.Fatalf("CSRF cookie = %#v token=%q", csrfCookie, token)
	}
	if harness.calls.Load() != 1 {
		t.Fatalf("safe handler calls = %d", harness.calls.Load())
	}

	missingCSRF := harness.request(t, http.MethodPost, "/api/articles/", true, csrfCookie, "")
	assertJSONCode(t, missingCSRF, http.StatusForbidden, api.CodeCSRFRejected)
	if harness.mutations.Load() != 0 {
		t.Fatalf("missing-CSRF mutations = %d", harness.mutations.Load())
	}

	valid := harness.request(t, http.MethodPost, "/api/articles/", true, csrfCookie, token)
	if valid.StatusCode != http.StatusOK {
		t.Fatalf("valid unsafe response = %d %q", valid.StatusCode, readBody(t, valid))
	}
	if harness.mutations.Load() != 1 || harness.calls.Load() != 2 {
		t.Fatalf("valid unsafe calls/mutations = %d/%d", harness.calls.Load(), harness.mutations.Load())
	}

	denied = harness.request(t, http.MethodDelete, "/api/denied/", true, csrfCookie, token)
	assertJSONCode(t, denied, http.StatusForbidden, api.CodePermissionDenied)
	if harness.mutations.Load() != 1 || harness.calls.Load() != 2 {
		t.Fatalf("permission denial changed application state: calls/mutations=%d/%d", harness.calls.Load(), harness.mutations.Load())
	}
}

func TestNewRejectsNilOrUninitializedRuntime(t *testing.T) {
	if _, err := apisessionauth.New(nil); !errors.Is(err, &apisessionauth.Error{Code: apisessionauth.CodeInvalidConfig}) {
		t.Fatalf("nil runtime error = %v", err)
	}
	if _, err := apisessionauth.New(&websessionauth.Runtime{}); !errors.Is(err, &apisessionauth.Error{Code: apisessionauth.CodeInvalidConfig}) {
		t.Fatalf("zero runtime error = %v", err)
	}
}

func TestRequireAddsSafeTokenToValidNilHeaderResponse(t *testing.T) {
	harness := newAPIAuthHarness(t)
	response := harness.request(t, http.MethodGet, "/api/nil-header/", true, nil, "")
	if response.StatusCode != http.StatusOK || len(response.Header.Get(websessionauth.DefaultCSRFHeader)) != 128 {
		body := readBody(t, response)
		t.Fatalf("response = %d headers %#v body %q", response.StatusCode, response.Header, body)
	}
	csrfCookie := namedCookie(t, response.Cookies(), websessionauth.DefaultCSRFCookieName)
	if !csrfCookie.HttpOnly || csrfCookie.Value == "" {
		t.Fatalf("CSRF cookie = %#v", csrfCookie)
	}
}

type apiAuthHarness struct {
	application   *web.Application
	sessionCookie *http.Cookie
	calls         atomic.Int64
	mutations     atomic.Int64
}

func newAPIAuthHarness(t *testing.T) *apiAuthHarness {
	t.Helper()
	store, err := sessions.NewMemoryStore(16)
	if err != nil {
		t.Fatal(err)
	}
	fixedTime := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	manager, err := sessions.NewManager(store, sessions.Config{
		Clock:  func() time.Time { return fixedTime },
		Random: bytes.NewReader(bytes.Repeat([]byte{1}, 512)),
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := auth.NewPermission("articles.view")
	if err != nil {
		t.Fatal(err)
	}
	deletePermission, err := auth.NewPermission("articles.delete")
	if err != nil {
		t.Fatal(err)
	}
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{
		ID:          "operator",
		Active:      true,
		Permissions: []auth.Permission{view},
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := manager.Create(context.Background(), map[string]string{"_godj_principal_id": principal.ID()})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := websessionauth.New(websessionauth.Config{
		Sessions:         manager,
		Authenticator:    fixedAuthenticator{principal: principal},
		Authorizer:       auth.PrincipalAuthorizer{},
		SessionCookie:    websessionauth.CookieConfig{AllowInsecure: true},
		CSRFCookie:       websessionauth.CookieConfig{AllowInsecure: true},
		FallbackPath:     "/api/articles/",
		AllowedNextPaths: []string{"/api/articles/"},
		Random:           bytes.NewReader(bytes.Repeat([]byte{2}, 4096)),
		Clock:            func() time.Time { return fixedTime },
	})
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := apisessionauth.New(runtime)
	if err != nil {
		t.Fatal(err)
	}
	configured, err := settings.New(settings.Definition{
		ProjectName: "api_session_test",
		InstalledApps: []apps.Config{{
			Name:  "example.test/api",
			Label: "test",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	harness := &apiAuthHarness{
		sessionCookie: &http.Cookie{Name: websessionauth.DefaultSessionCookieName, Value: record.ID().Encoded(), Path: "/"},
	}
	articleObject, err := serializers.NewObject(serializers.MemberOf("ok", serializers.Boolean(true)))
	if err != nil {
		t.Fatal(err)
	}
	allowedHandler := adapter.Require(view, func(_ *web.Request, resolved auth.Principal) (web.Response, error) {
		if resolved.ID() != principal.ID() {
			return web.Response{}, errors.New("wrong principal")
		}
		harness.calls.Add(1)
		return api.JSON(http.StatusOK, articleObject.Value())
	})
	mutatingHandler := adapter.Require(view, func(_ *web.Request, resolved auth.Principal) (web.Response, error) {
		if resolved.ID() != principal.ID() {
			return web.Response{}, errors.New("wrong principal")
		}
		harness.calls.Add(1)
		harness.mutations.Add(1)
		return api.JSON(http.StatusOK, articleObject.Value())
	})
	deniedHandler := adapter.Require(deletePermission, func(*web.Request, auth.Principal) (web.Response, error) {
		harness.calls.Add(1)
		harness.mutations.Add(1)
		return api.JSON(http.StatusOK, articleObject.Value())
	})
	nilHeaderHandler := adapter.Require(view, func(*web.Request, auth.Principal) (web.Response, error) {
		return web.NewResponse(http.StatusOK, nil, nil)
	})
	harness.application, err = web.NewApplication(web.Config{
		Settings: configured,
		Routes: []web.Route{
			{Name: "test:list", Method: http.MethodGet, Path: "/api/articles/", Handler: allowedHandler},
			{Name: "test:create", Method: http.MethodPost, Path: "/api/articles/", Handler: mutatingHandler},
			{Name: "test:delete", Method: http.MethodDelete, Path: "/api/denied/", Handler: deniedHandler},
			{Name: "test:nil-header", Method: http.MethodGet, Path: "/api/nil-header/", Handler: nilHeaderHandler},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return harness
}

func (h *apiAuthHarness) request(t *testing.T, method, path string, authenticated bool, csrfCookie *http.Cookie, token string) *http.Response {
	t.Helper()
	request := httptest.NewRequest(method, "http://example.test"+path, nil)
	if authenticated {
		request.AddCookie(h.sessionCookie)
	}
	if csrfCookie != nil {
		request.AddCookie(csrfCookie)
	}
	if token != "" {
		request.Header.Set(websessionauth.DefaultCSRFHeader, token)
	}
	recorder := httptest.NewRecorder()
	h.application.ServeHTTP(recorder, request)
	return recorder.Result()
}

type fixedAuthenticator struct {
	principal auth.Principal
}

func (f fixedAuthenticator) Authenticate(context.Context, string, string) (auth.Principal, error) {
	return f.principal, nil
}

func (f fixedAuthenticator) Resolve(_ context.Context, id string) (auth.Principal, error) {
	if id != f.principal.ID() {
		return auth.Principal{}, auth.ErrInvalidCredentials
	}
	return f.principal, nil
}

func assertJSONCode(t *testing.T, response *http.Response, status int, code api.ResponseCode) {
	t.Helper()
	body := readBody(t, response)
	if response.StatusCode != status || response.Header.Get("Content-Type") != api.JSONContentType ||
		!strings.Contains(body, `"code":"`+string(code)+`"`) {
		t.Fatalf("response = status %d headers %#v body %q", response.StatusCode, response.Header, body)
	}
}

func readBody(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func namedCookie(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == name {
			clone := *cookie
			return &clone
		}
	}
	t.Fatalf("cookie %q missing from %#v", name, cookies)
	return nil
}
