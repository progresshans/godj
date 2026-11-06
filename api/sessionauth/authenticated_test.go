package sessionauth_test

import (
	"context"
	"github.com/progresshans/godj/api"
	apisession "github.com/progresshans/godj/api/sessionauth"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
	websession "github.com/progresshans/godj/web/sessionauth"
	"net/http/httptest"
	"testing"
)

func TestRequireAuthenticatedSessionKeepsCSRFWithoutModelAuthorization(t *testing.T) {
	var config websession.Config
	h := newAPIAuthHarness(t, func(c *websession.Config) {
		p, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "operator", Active: true})
		if err != nil {
			t.Fatal(err)
		}
		c.Authenticator = fixedAuthenticator{principal: p}
		c.Authorizer = requirementsAuthorizer(func(context.Context, auth.Principal, auth.Permission) (bool, error) {
			t.Fatal("auth-only invoked model authorization")
			return false, nil
		})
		config = *c
	})
	runtime, err := websession.New(config)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := apisession.New(runtime, nil); got != nil || err == nil {
		t.Fatal("nil option accepted")
	}
	adapter, err := apisession.New(runtime, apisession.WithReadOnlyResolution())
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	handler := func(_ *web.Request, p auth.Principal) (web.Response, error) {
		calls++
		if p.Staff() || len(p.Permissions()) != 0 {
			t.Fatal("not ordinary")
		}
		return api.NoContent()
	}
	if got, err := adapter.Require("", handler); got != nil || err == nil {
		t.Fatal("empty permission accepted")
	}
	if got, err := adapter.RequireAuthenticated(nil); got != nil || err == nil {
		t.Fatal("nil handler accepted")
	}
	protected, err := adapter.RequireAuthenticated(handler)
	if err != nil {
		t.Fatal(err)
	}
	configured, err := settings.New(settings.Definition{ProjectName: "account_auth", InstalledApps: []apps.Config{{Name: "example.test/account", Label: "account"}}})
	if err != nil {
		t.Fatal(err)
	}
	app, err := web.NewApplication(web.Config{Settings: configured, Routes: []web.Route{{Name: "account:get", Method: "GET", Path: "/", Handler: protected}, {Name: "account:post", Method: "POST", Path: "/", Handler: protected}}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "http://example.test/", nil)
	request.AddCookie(h.sessionCookie)
	writer := httptest.NewRecorder()
	app.ServeHTTP(writer, request)
	response := writer.Result()
	response.Body.Close()
	if response.StatusCode != 204 || response.Header.Get(websession.DefaultCSRFHeader) == "" || calls != 1 {
		t.Fatal("safe auth-only admission")
	}
	csrf := namedCookie(t, response.Cookies(), websession.DefaultCSRFCookieName)
	for _, mode := range []string{"allowed", "csrf", "anonymous"} {
		request = httptest.NewRequest("POST", "http://example.test/", nil)
		if mode != "anonymous" {
			request.AddCookie(h.sessionCookie)
		}
		request.AddCookie(csrf)
		if mode != "csrf" {
			request.Header.Set(websession.DefaultCSRFHeader, response.Header.Get(websession.DefaultCSRFHeader))
		}
		before := calls
		writer = httptest.NewRecorder()
		app.ServeHTTP(writer, request)
		want := 403
		count := 0
		if mode == "allowed" {
			want, count = 204, 1
		}
		if writer.Code != want || calls-before != count || writer.Header().Get("Location") != "" || writer.Header().Get("WWW-Authenticate") != "" {
			t.Fatal("session auth-only admission", mode, writer.Code)
		}
	}
}
