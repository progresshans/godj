package accountstoretest

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"reflect"
	"testing"

	apisession "github.com/progresshans/godj/api/sessionauth"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

func selectedAuthenticationHTTP(t *testing.T, open Open) {
	backend, reopen := open(t)
	f := provisionSelfPassword(t, backend)
	row := f.target
	// Begin with a group-only grant. Another account uses a different password
	// and has no membership, despite sharing this selected domain and server.
	for _, link := range accountNativeSnapshot(t, backend).direct {
		if link.SourceID == row.ID {
			if _, err := models.UserPermissionsLinkObjects.Delete(t.Context(), backend, &link); err != nil {
				t.Fatal(err)
			}
		}
	}
	group, err := models.GroupObjects.Create(t.Context(), backend, models.NewGroupCreate("HTTP readers"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := models.UserGroupsLinkObjects.Create(t.Context(), backend, models.NewUserGroupsLinkCreate(row.ID, group.ID)); err != nil {
		t.Fatal(err)
	}
	grant, err := models.GroupPermissionsLinkObjects.Create(t.Context(), backend, models.NewGroupPermissionsLinkCreate(group.ID, f.view.ID))
	if err != nil {
		t.Fatal(err)
	}
	bobHash, err := f.base.hasher.Hash(t.Context(), "other private password")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := models.UserObjects.Create(t.Context(), backend, models.NewUserCreate("other-http-principal", "other-http-user", bobHash, nativeLoginInstant))
	if err != nil {
		t.Fatal(err)
	}
	h := newUserReferenceHTTPConfigured(t, f.runtime, f.clock, 204, func(runtime *sessionauth.Runtime) []web.Route {
		adapter, err := apisession.New(runtime)
		if err != nil {
			t.Fatal(err)
		}
		denied, err := sessionauth.New(sessionauth.Config{Sessions: f.runtime.Sessions(), Authenticator: f.runtime.Authenticator(), LoginPersistence: f.runtime.LoginPersistence(), Authorizer: denyAccountPermission{}, Clock: f.clock,
			LoginPath: "/login/", FallbackPath: "/api_view/", AllowedNextPaths: []string{"/api_view/"},
			SessionCookie: sessionauth.CookieConfig{AllowInsecure: true}, CSRFCookie: sessionauth.CookieConfig{AllowInsecure: true}})
		if err != nil {
			t.Fatal(err)
		}
		deniedAdapter, err := apisession.New(denied)
		if err != nil {
			t.Fatal(err)
		}
		var routes []web.Route
		for _, route := range []struct {
			name       string
			permission auth.Permission
			adapter    *apisession.Runtime
		}{{"api_view", "helpdesk.ticket.view", adapter}, {"api_change", "helpdesk.ticket.change", adapter}, {"api_deny", "helpdesk.ticket.change", deniedAdapter}} {
			handler, err := route.adapter.Require(route.permission, func(_ *web.Request, principal auth.Principal) (web.Response, error) {
				return web.NewResponse(200, nil, []byte(principal.ID()))
			})
			if err != nil {
				t.Fatal(err)
			}
			routes = append(routes, web.Route{Name: "userreference:" + route.name, Method: http.MethodGet, Path: "/" + route.name + "/", Handler: handler})
		}
		return routes
	})
	t.Cleanup(h.close)
	newBrowser := func() *userReferenceHTTP {
		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatal(err)
		}
		client, browser := *h.client, *h
		client.Jar, browser.client = jar, &client
		return &browser
	}
	login := func(t *testing.T, browser *userReferenceHTTP, target *models.User, raw string, want int, grants ...auth.Permission) sessions.Record {
		t.Helper()
		before := userReferenceSnapshot(t, reopen(t))
		entropy := f.entropy.count()
		response, id := browser.loginAttempt(t, target.Username, raw, false, want)
		after := userReferenceSnapshot(t, reopen(t))
		if want != 204 {
			if id.Valid() || f.entropy.count() != entropy {
				t.Fatal("failed authentication allocated or published a bearer")
			}
			for _, cookie := range response.Cookies() {
				if cookie.Name == sessionauth.DefaultSessionCookieName {
					t.Fatal("failed authentication published a session cookie")
				}
			}
			assertUserReferenceState(t, before, after, f.actor.Principal())
			return sessions.Record{}
		}
		credential := authenticationExpected(t, f, *target, grants...)
		record := loginExpectedRecord(t, credential, sessions.Record{}, "create", f.entropy.one(t, entropy), f.clock())
		if id != record.ID() {
			t.Fatal("HTTP cookie did not contain the independently observed bearer")
		}
		accountSessionRecord(t, f.runtime.SessionStore(), record, true)
		assertUserReferenceState(t, loginExpectedState(t, before, after, credential, *target, sessions.Record{}, record, f.clock()), after, f.actor.Principal())
		at := f.clock()
		target.LastLogin = &at
		return record
	}
	expect := func(t *testing.T, browser *userReferenceHTTP, path string, want int, removed sessions.Record) {
		t.Helper()
		before := userReferenceSnapshot(t, reopen(t))
		browser.expect(t, path, want)
		if removed.ID().Valid() {
			loginRemoveRecord(t, &before.account, loginNamespace(t, f.credential), removed.ID())
			accountSessionRecord(t, f.runtime.SessionStore(), removed, false)
		}
		// The fixed clock means ordinary access changes no native payload.
		// Rejection may remove only the current stale browser record.
		assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
	}
	update := func(t *testing.T, next models.User, patch models.UserPatch) {
		t.Helper()
		before := userReferenceSnapshot(t, reopen(t))
		if _, err := models.UserObjects.Patch(t.Context(), backend, row, patch); err != nil {
			t.Fatal(err)
		}
		userReferenceReplace(&before.account, next)
		assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
		row = next
	}
	var current sessions.Record
	if !t.Run("login", func(t *testing.T) {
		current = login(t, h, &row, password, 204, "helpdesk.ticket.view")
		expect(t, h, "/api_view/", 200, sessions.Record{})
	}) {
		return
	}
	if !t.Run("cross_account", func(t *testing.T) {
		other := newBrowser()
		login(t, other, &bob, "other private password", 204)
		login(t, newBrowser(), &bob, password, 401)
		expect(t, other, "/api_view/", 403, sessions.Record{})
	}) {
		return
	}
	if !t.Run("group_revocation", func(t *testing.T) {
		if _, err := models.GroupPermissionsLinkObjects.Delete(t.Context(), backend, &grant); err != nil {
			t.Fatal(err)
		}
		expect(t, h, "/api_view/", 403, sessions.Record{})
	}) {
		return
	}
	if !t.Run("direct_restoration", func(t *testing.T) {
		if _, err := models.UserPermissionsLinkObjects.Create(t.Context(), backend, models.NewUserPermissionsLinkCreate(row.ID, f.view.ID)); err != nil {
			t.Fatal(err)
		}
		expect(t, h, "/api_view/", 200, sessions.Record{})
	}) {
		return
	}
	if !t.Run("superuser_overlay", func(t *testing.T) {
		next := row
		next.Superuser = true
		update(t, next, models.UserPatch{}.WithSuperuser(true))
		expect(t, h, "/api_change/", 200, sessions.Record{})
		expect(t, h, "/api_deny/", 403, sessions.Record{})
	}) {
		return
	}
	if !t.Run("staff_only", func(t *testing.T) {
		next := row
		next.Superuser, next.Staff = false, true
		update(t, next, models.UserPatch{}.WithSuperuser(false).WithStaff(true))
		expect(t, h, "/api_change/", 403, sessions.Record{})
		expect(t, h, "/api_view/", 200, sessions.Record{})
	}) {
		return
	}
	if !t.Run("rename", func(t *testing.T) {
		next, previous := row, row
		next.Username = "renamed-member"
		update(t, next, models.UserPatch{}.WithUsername(next.Username))
		expect(t, h, "/api_view/", 200, sessions.Record{})
		login(t, newBrowser(), &previous, password, 401)
	}) {
		return
	}
	if !t.Run("password_replaced", func(t *testing.T) {
		next := row
		var err error
		next.EncodedPassword, err = f.base.hasher.Hash(t.Context(), "replacement private password")
		if err != nil || next.EncodedPassword == row.EncodedPassword {
			t.Fatal("HTTP fixture password did not change", err)
		}
		update(t, next, models.UserPatch{}.WithEncodedPassword(next.EncodedPassword))
		expect(t, h, "/api_view/", 403, current)
		login(t, newBrowser(), &row, password, 401)
	}) {
		return
	}
	if !t.Run("relogin", func(t *testing.T) {
		current = login(t, h, &row, "replacement private password", 204, "helpdesk.ticket.view")
		expect(t, h, "/api_view/", 200, sessions.Record{})
	}) {
		return
	}
	if !t.Run("inactive", func(t *testing.T) {
		next := row
		next.Active, next.Superuser = false, true
		update(t, next, models.UserPatch{}.WithActive(false).WithSuperuser(true))
		expect(t, h, "/api_view/", 403, current)
	}) {
		return
	}
	t.Run("reactivation", func(t *testing.T) {
		next := row
		next.Active = true
		update(t, next, models.UserPatch{}.WithActive(true))
		address, err := url.Parse(h.server.URL)
		if err != nil {
			t.Fatal(err)
		}
		h.client.Jar.SetCookies(address, []*http.Cookie{{Name: sessionauth.DefaultSessionCookieName, Value: current.ID().Encoded(), Path: "/"}})
		expect(t, h, "/api_view/", 403, sessions.Record{})
		if stored, found, err := f.runtime.SessionStore().Load(t.Context(), current.ID()); err != nil || found || !reflect.DeepEqual(stored, sessions.Record{}) {
			t.Fatal("reactivation revived a deleted browser session", err)
		}
	})
}
