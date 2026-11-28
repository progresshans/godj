package identitytest

import (
	"net/http"
	"net/http/httptest"
	"testing"

	apisession "github.com/progresshans/godj/api/sessionauth"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

func verifyTransitionHTTP(t *testing.T, runtime *systemstate.Runtime, id sessions.ID, principalID string) {
	t.Helper()
	manager, err := sessions.NewManager(runtime.SessionStore(), sessions.Config{})
	if err != nil {
		t.Fatal(err)
	}
	authentication, err := sessionauth.New(sessionauth.Config{Sessions: manager, Authenticator: runtime.Authenticator(), Authorizer: auth.PrincipalAuthorizer{}, SessionCookie: sessionauth.CookieConfig{AllowInsecure: true}, CSRFCookie: sessionauth.CookieConfig{AllowInsecure: true}, LoginPath: "/login/", FallbackPath: "/", AllowedNextPaths: []string{"/", "/api/"}})
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := apisession.New(authentication)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := adapter.Require("helpdesk.ticket.view", func(_ *web.Request, principal auth.Principal) (web.Response, error) {
		return web.NewResponse(http.StatusOK, nil, []byte(principal.ID()))
	})
	if err != nil {
		t.Fatal(err)
	}
	configured, err := settings.New(settings.Definition{ProjectName: "adoption_probe", InstalledApps: []apps.Config{{Name: "github.com/progresshans/godj/internal/identitytest", Label: "identityprobe"}}})
	if err != nil {
		t.Fatal(err)
	}
	application, err := web.NewApplication(web.Config{Settings: configured, Routes: []web.Route{{Name: "identityprobe:api", Method: http.MethodGet, Path: "/api/", Handler: handler}}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://example.test/api/", nil)
	request.AddCookie(&http.Cookie{Name: sessionauth.DefaultSessionCookieName, Value: id.Encoded()})
	response := httptest.NewRecorder()
	application.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != principalID {
		t.Fatal("pre-adoption session failed actual identity-backed API", response.Code)
	}
}
