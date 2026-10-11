package sessionauth_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/progresshans/godj/api"
	apisession "github.com/progresshans/godj/api/sessionauth"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
	websession "github.com/progresshans/godj/web/sessionauth"
)

type csrfOnlyGuard struct {
	sessions.Store
	t *testing.T
}

func (g csrfOnlyGuard) Load(context.Context, sessions.ID) (sessions.Record, bool, error) {
	g.t.Fatal("CSRF-only loaded session")
	return sessions.Record{}, false, nil
}
func (g csrfOnlyGuard) Access(context.Context, sessions.ID, sessions.AccessPolicy) (sessions.Record, sessions.AccessStatus, error) {
	g.t.Fatal("CSRF-only accessed session")
	return sessions.Record{}, 0, nil
}
func (g csrfOnlyGuard) Create(context.Context, sessions.Record) (bool, error) {
	g.t.Fatal("CSRF-only created session")
	return false, nil
}
func (g csrfOnlyGuard) Delete(context.Context, sessions.ID) error {
	g.t.Fatal("CSRF-only deleted session")
	return nil
}
func (g csrfOnlyGuard) Authenticate(context.Context, string, string) (auth.Credential, error) {
	g.t.Fatal("CSRF-only authenticated")
	return auth.Credential{}, nil
}
func (g csrfOnlyGuard) Resolve(context.Context, string) (auth.Credential, error) {
	g.t.Fatal("CSRF-only resolved credential")
	return auth.Credential{}, nil
}
func (g csrfOnlyGuard) Allowed(context.Context, auth.Principal, auth.Permission) (bool, error) {
	g.t.Fatal("CSRF-only authorized")
	return false, nil
}

func TestCSRFOnlyAdmitsWithoutSessionAccessAndChecksOriginBeforeHandler(t *testing.T) {
	guard := csrfOnlyGuard{t: t}
	manager, err := sessions.NewManager(guard, sessions.Config{})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := websession.New(websession.Config{Sessions: manager, Authenticator: guard, Authorizer: guard, LoginPath: "/login/", FallbackPath: "/", AllowedNextPaths: []string{"/"}})
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := apisession.New(runtime)
	if err != nil {
		t.Fatal(err)
	}
	configured, err := settings.New(settings.Definition{ProjectName: "public_csrf", InstalledApps: []apps.Config{{Name: "public", Label: "public"}}})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	protected, err := adapter.RequireCSRF(func(*web.Request) (web.Response, error) { calls++; return api.NoContent() })
	if err != nil {
		t.Fatal(err)
	}
	routes := []web.Route{}
	for _, method := range []string{"GET", "HEAD", "OPTIONS", "TRACE", "POST", "PUT", "PATCH", "DELETE"} {
		routes = append(routes, web.Route{Name: "public:" + method, Method: method, Path: "/", Handler: protected})
	}
	app, err := web.NewApplication(web.Config{Settings: configured, Routes: routes})
	if err != nil {
		t.Fatal(err)
	}
	send := func(method string, cookies []*http.Cookie, header http.Header) *http.Response {
		t.Helper()
		request := httptest.NewRequest(method, "https://example.test/", strings.NewReader("body must not be parsed by adapter"))
		request.Header = header.Clone()
		if request.Header == nil {
			request.Header = make(http.Header)
		}
		for _, c := range cookies {
			request.AddCookie(c)
		}
		writer := httptest.NewRecorder()
		app.ServeHTTP(writer, request)
		return writer.Result()
	}
	safe := send("GET", nil, nil)
	defer safe.Body.Close()
	if safe.StatusCode != 204 || calls != 1 {
		t.Fatal("anonymous bootstrap refused")
	}
	token := safe.Header.Get(runtime.CSRFHeader())
	cookie := namedCookie(t, safe.Cookies(), websession.DefaultCSRFCookieName)
	if !cookie.HttpOnly || cookie.Value == token || len(token) != 128 {
		t.Fatal("wrong CSRF transport")
	}
	for _, c := range safe.Cookies() {
		if c.Name == websession.DefaultSessionCookieName {
			t.Fatal("anonymous bootstrap minted login session")
		}
	}
	for _, mode := range []string{"anonymous", "malformed_session", "duplicate_session", "missing_cookie", "missing_token", "duplicate_cookie", "duplicate_header", "foreign_origin", "same_site_sibling", "cross_site_fetch", "safe_foreign"} {
		t.Run(mode, func(t *testing.T) {
			cookies := []*http.Cookie{cookie}
			header := http.Header{runtime.CSRFHeader(): []string{token}}
			method := "POST"
			want := 204
			switch mode {
			case "malformed_session":
				cookies = append(cookies, &http.Cookie{Name: websession.DefaultSessionCookieName, Value: "broken"})
			case "duplicate_session":
				cookies = append(cookies, &http.Cookie{Name: websession.DefaultSessionCookieName, Value: "broken"}, &http.Cookie{Name: websession.DefaultSessionCookieName, Value: "also-broken"})
			case "missing_cookie":
				cookies = nil
				want = 403
			case "missing_token":
				header = nil
				want = 403
			case "duplicate_cookie":
				cookies = append(cookies, cookie)
				want = 403
			case "duplicate_header":
				header.Add(runtime.CSRFHeader(), token)
				want = 403
			case "foreign_origin":
				header.Set("Origin", "https://attacker.test")
				want = 403
			case "same_site_sibling":
				header.Set("Origin", "https://sibling.example.test")
				header.Set("Sec-Fetch-Site", "same-site")
				want = 403
			case "cross_site_fetch":
				header.Set("Sec-Fetch-Site", "cross-site")
				want = 403
			case "safe_foreign":
				method = "GET"
				header.Set("Origin", "https://attacker.test")
			}
			before := calls
			got := send(method, cookies, header)
			defer got.Body.Close()
			delta := 0
			if want == 204 {
				delta = 1
			}
			if got.StatusCode != want || calls != before+delta || got.Header.Get("Location") != "" || got.Header.Get("WWW-Authenticate") != "" {
				t.Fatal("admission boundary", got.StatusCode, calls-before)
			}
			if want == 403 {
				body, _ := io.ReadAll(got.Body)
				if !strings.Contains(string(body), `"code":"csrf_rejected"`) {
					t.Fatal("incorrect refusal")
				}
			}
			if method != "GET" && (len(got.Cookies()) != 0 || got.Header.Get(runtime.CSRFHeader()) != "") {
				t.Fatal("unsafe response published CSRF state")
			}
		})
	}
	for _, method := range []string{"HEAD", "OPTIONS", "TRACE", "PUT", "PATCH", "DELETE"} {
		got := send(method, []*http.Cookie{cookie}, http.Header{runtime.CSRFHeader(): []string{token}})
		got.Body.Close()
		if got.StatusCode != 204 {
			t.Fatal("method refused", method)
		}
	}
	for _, bad := range []*apisession.Runtime{nil, {}} {
		if handler, err := bad.RequireCSRF(func(*web.Request) (web.Response, error) { return api.NoContent() }); handler != nil || err == nil {
			t.Fatal("invalid runtime published handler")
		}
	}
	if handler, err := adapter.RequireCSRF(nil); handler != nil || err == nil {
		t.Fatal("nil handler accepted")
	}
	if handler, err := adapter.Require("", func(*web.Request, auth.Principal) (web.Response, error) { return api.NoContent() }); handler != nil || err == nil {
		t.Fatal("empty permission became anonymous")
	}
}

func TestCSRFOnlyDoesNotPublishTokenAfterHandlerFailure(t *testing.T) {
	h := newAPIAuthHarness(t)
	failure := errors.New("private execution error")
	handler, err := h.adapter.RequireCSRF(func(*web.Request) (web.Response, error) { return web.Response{}, failure })
	if err != nil {
		t.Fatal(err)
	}
	configured, err := settings.New(settings.Definition{ProjectName: "public_error", InstalledApps: []apps.Config{{Name: "public", Label: "public"}}})
	if err != nil {
		t.Fatal(err)
	}
	app, err := web.NewApplication(web.Config{Settings: configured, Routes: []web.Route{{Name: "public:error", Method: "GET", Path: "/", Handler: handler}}})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest("GET", "https://example.test/", nil))
	if response.Code != 500 || len(response.Result().Cookies()) != 0 || response.Header().Get(websession.DefaultCSRFHeader) != "" || strings.Contains(response.Body.String(), failure.Error()) {
		t.Fatal("failed handler exposed token or cause")
	}
}
