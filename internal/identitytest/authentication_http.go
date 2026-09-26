package identitytest

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	apisession "github.com/progresshans/godj/api/sessionauth"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

type identityHTTP struct {
	server *httptest.Server
	client *http.Client
	store  *sessions.MemoryStore
}

func newIdentityHTTP(t *testing.T, authenticator auth.CredentialAuthenticator) *identityHTTP {
	t.Helper()
	store, err := sessions.NewMemoryStore(32)
	if err != nil {
		t.Fatal(err)
	}
	result := newIdentityHTTPWithStore(t, authenticator, store)
	result.store = store
	return result
}

func newIdentityHTTPWithStore(t *testing.T, authenticator auth.CredentialAuthenticator, store sessions.Store) *identityHTTP {
	t.Helper()
	manager, err := sessions.NewManager(store, sessions.Config{})
	if err != nil {
		t.Fatal(err)
	}
	config := sessionauth.Config{Sessions: manager, Authenticator: authenticator, Authorizer: auth.PrincipalAuthorizer{},
		SessionCookie: sessionauth.CookieConfig{AllowInsecure: true}, CSRFCookie: sessionauth.CookieConfig{AllowInsecure: true},
		LoginPath: "/login/", FallbackPath: "/", AllowedNextPaths: []string{"/", "/login/", "/view/", "/change/", "/deny/"}}
	runtime, err := sessionauth.New(config)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := apisession.New(runtime)
	if err != nil {
		t.Fatal(err)
	}
	handler := func(_ *web.Request, principal auth.Principal) (web.Response, error) {
		return web.NewResponse(200, nil, []byte(principal.ID()))
	}
	view, err := adapter.Require("helpdesk.ticket.view", handler)
	if err != nil {
		t.Fatal(err)
	}
	change, err := adapter.Require("helpdesk.ticket.change", handler)
	if err != nil {
		t.Fatal(err)
	}
	config.Authorizer = denyIdentityAuthorization{}
	deniedRuntime, err := sessionauth.New(config)
	if err != nil {
		t.Fatal(err)
	}
	deniedAdapter, err := apisession.New(deniedRuntime)
	if err != nil {
		t.Fatal(err)
	}
	deny, err := deniedAdapter.Require("helpdesk.ticket.change", handler)
	if err != nil {
		t.Fatal(err)
	}
	configured, err := settings.New(settings.Definition{ProjectName: "identity_http", InstalledApps: []apps.Config{{Name: "github.com/progresshans/godj/internal/identitytest", Label: "identityprobe"}}})
	if err != nil {
		t.Fatal(err)
	}
	application, err := web.NewApplication(web.Config{Settings: configured, Routes: []web.Route{
		{Name: "identityprobe:csrf", Method: "GET", Path: "/login/", Handler: func(request *web.Request) (web.Response, error) {
			token, err := runtime.CSRFToken(request)
			if err != nil {
				return web.Response{}, err
			}
			response, err := web.NewResponse(200, nil, []byte(token.Value()))
			if err != nil {
				return web.Response{}, err
			}
			return token.Apply(response)
		}},
		{Name: "identityprobe:login", Method: "POST", Path: "/login/", Handler: func(request *web.Request) (web.Response, error) {
			if err := runtime.VerifyCSRF(request, nil); err != nil {
				return web.NewResponse(403, nil, nil)
			}
			result, err := runtime.Login(request, request.HTTP().Header.Get("X-Test-Username"), request.HTTP().Header.Get("X-Test-Password"))
			if errors.Is(err, auth.ErrInvalidCredentials) {
				return web.NewResponse(401, nil, nil)
			}
			if err != nil {
				return web.Response{}, err
			}
			response, err := web.NewResponse(200, nil, []byte(result.Principal().ID()))
			if err != nil {
				return web.Response{}, err
			}
			return result.Apply(response)
		}},
		{Name: "identityprobe:view", Method: "GET", Path: "/view/", Handler: view},
		{Name: "identityprobe:change", Method: "GET", Path: "/change/", Handler: change},
		{Name: "identityprobe:deny", Method: "GET", Path: "/deny/", Handler: deny},
	}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(application)
	t.Cleanup(server.Close)
	result := &identityHTTP{server: server}
	result.client = result.newClient(t)
	return result
}

type denyIdentityAuthorization struct{}

func (denyIdentityAuthorization) Allowed(context.Context, auth.Principal, auth.Permission) (bool, error) {
	return false, nil
}
func (h *identityHTTP) newClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := *h.server.Client()
	client.Jar = jar
	client.Timeout = 5 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client
}
func (h *identityHTTP) request(t *testing.T, client *http.Client, method, path string, header http.Header) (*http.Response, string) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, h.server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if header != nil {
		request.Header = header.Clone()
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	return response, string(body)
}
func (h *identityHTTP) login(t *testing.T, client *http.Client, username, password string, want int) sessions.ID {
	t.Helper()
	response, token := h.request(t, client, "GET", "/login/", nil)
	if response.StatusCode != 200 {
		t.Fatal("CSRF endpoint failed")
	}
	response, _ = h.request(t, client, "POST", "/login/", http.Header{"X-Test-Username": {username}, "X-Test-Password": {password}, sessionauth.DefaultCSRFHeader: {token}})
	if response.StatusCode != want {
		t.Fatal("stored login HTTP status", response.StatusCode, want)
	}
	var id sessions.ID
	for _, cookie := range response.Cookies() {
		if cookie.Name == sessionauth.DefaultSessionCookieName {
			var err error
			id, err = sessions.ParseID(cookie.Value)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if want != 200 && id.Valid() {
		t.Fatal("failed login created session cookie")
	}
	return id
}
func (h *identityHTTP) expect(t *testing.T, client *http.Client, path string, want int, secret string) {
	t.Helper()
	response, body := h.request(t, client, "GET", path, nil)
	if response.StatusCode != want {
		t.Fatal("current stored authorization HTTP status", path, response.StatusCode, want)
	}
	if strings.Contains(body, secret) || strings.Contains(body, "encoded_password") {
		t.Fatal("HTTP response exposed stored credential")
	}
}

func runAuthenticationHTTP(t *testing.T, backend DirectoryBackend, directory *identity.Directory, hasher auth.PasswordHasher, user models.User, grant models.GroupPermissionsLink, permission models.Permission, encoded string, joined time.Time) {
	t.Helper()
	ctx := t.Context()
	authenticator, err := identity.NewAuthenticator(ctx, directory, hasher)
	if err != nil {
		t.Fatal(err)
	}
	h := newIdentityHTTP(t, authenticator)
	bobHash, err := hasher.Hash(ctx, "bob password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := models.UserObjects.Create(ctx, backend, models.NewUserCreate("other-member", "bob", bobHash, joined)); err != nil {
		t.Fatal(err)
	}
	id := h.login(t, h.client, "member", "original password", 200)
	bob := h.newClient(t)
	h.login(t, bob, "bob", "bob password", 200)
	h.login(t, h.newClient(t), "bob", "original password", 401)
	h.expect(t, h.client, "/view/", 200, encoded)
	h.expect(t, bob, "/view/", 403, bobHash)
	if _, err := models.GroupPermissionsLinkObjects.Delete(ctx, backend, &grant); err != nil {
		t.Fatal(err)
	}
	h.expect(t, h.client, "/view/", 403, encoded)
	if _, err := models.UserPermissionsLinkObjects.Create(ctx, backend, models.NewUserPermissionsLinkCreate(user.ID, permission.ID)); err != nil {
		t.Fatal(err)
	}
	h.expect(t, h.client, "/view/", 200, encoded)
	update := func(patch models.UserPatch) {
		t.Helper()
		if _, err := models.UserObjects.Update(ctx, backend, user, patch); err != nil {
			t.Fatal(err)
		}
	}
	update(models.UserPatch{}.WithSuperuser(true))
	h.expect(t, h.client, "/change/", 200, encoded)
	h.expect(t, h.client, "/deny/", 403, encoded)
	update(models.UserPatch{}.WithSuperuser(false).WithStaff(true))
	h.expect(t, h.client, "/change/", 403, encoded)
	h.expect(t, h.client, "/view/", 200, encoded)
	update(models.UserPatch{}.WithUsername("renamed"))
	h.expect(t, h.client, "/view/", 200, encoded)
	h.login(t, h.newClient(t), "member", "original password", 401)
	replacement, err := hasher.Hash(ctx, "replacement password")
	if err != nil {
		t.Fatal(err)
	}
	update(models.UserPatch{}.WithEncodedPassword(replacement))
	h.expect(t, h.client, "/view/", 403, replacement)
	if _, found, err := h.store.Load(ctx, id); err != nil || found {
		t.Fatal("stale credential session survived", err)
	}
	h.login(t, h.client, "renamed", "original password", 401)
	id = h.login(t, h.client, "renamed", "replacement password", 200)
	h.expect(t, h.client, "/view/", 200, replacement)
	update(models.UserPatch{}.WithActive(false).WithSuperuser(true))
	h.expect(t, h.client, "/view/", 403, replacement)
	if _, found, err := h.store.Load(ctx, id); err != nil || found {
		t.Fatal("inactive account retained session", err)
	}
	update(models.UserPatch{}.WithActive(true))
	h.expect(t, h.client, "/view/", 403, replacement)
}
