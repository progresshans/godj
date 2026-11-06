package accountstoretest

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/conformance/accountdeletefixture/access"
	"github.com/progresshans/godj/conformance/accountdeletefixture/people"
	"github.com/progresshans/godj/conformance/accountdeletefixture/project"
	"github.com/progresshans/godj/conformance/accountdeletefixture/tickets"
	identityadmin "github.com/progresshans/godj/identity/admin"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
	websession "github.com/progresshans/godj/web/sessionauth"
)

func accountDeletionAdmin(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionDeletion(t, backend)
	tree := deletionTreeFixture(t, backend, f, "admin")
	check := func(t *testing.T, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10_000})
	check(t, err)
	clock := func() time.Time { return joined }
	config := accountmodel.RuntimeConfig[people.Member, access.Team, access.Grant]{
		Sessions: sessions.Config{Clock: clock},
		Accounts: accountmodel.AccountManagementConfig[people.Member]{
			ReadFields: []string{"contact", "region"}, CreateFields: []string{"contact", "region"}, EditFields: []string{"region"},
			Permissions:      accountmodel.AccountPermissions{View: "account.view", Add: "account.add", Change: "account.change", Delete: "account.delete"},
			GroupPermissions: deletionConfig().Group, PermissionPermissions: deletionConfig().Permission, Now: clock,
		},
		Catalog: accountmodel.CatalogConfig[access.Team, access.Grant]{
			Group: accountmodel.CatalogModelConfig[access.Team]{ReadFields: []string{"title"}, WriteFields: []string{"title"},
				Permissions: accountmodel.CatalogPermissions{View: "catalog.view_group", Add: "catalog.add_group", Change: "catalog.change_group", Delete: "catalog.delete_group"}},
			Permission: accountmodel.CatalogModelConfig[access.Grant]{ReadFields: []string{"key", "title"}, WriteFields: []string{"key", "title"},
				Permissions: accountmodel.CatalogPermissions{View: "catalog.view_permission", Add: "catalog.add_permission", Change: "catalog.change_permission", Delete: "catalog.delete_permission"}},
			AccountPermissions: deletionConfig().Account, Now: clock,
		},
	}
	runtime, err := project.NewAccountRuntime(t.Context(), backend, hasher, auth.PrincipalAuthorizer{}, config)
	check(t, err)
	configured, err := settings.New(settings.Definition{ProjectName: "deletion_admin", InstalledApps: append(project.InstalledApps(), apps.Config{Name: "github.com/progresshans/godj/internal/accountstoretest", Label: "deleteadmin"})})
	check(t, err)
	builder := admin.NewBuilder(configured.Apps())
	check(t, identityadmin.RegisterAccount(builder, runtime.Accounts(), identityadmin.AccountRegistration{Slug: "accounts"}))
	registry, err := builder.Build()
	check(t, err)
	paths, err := admin.SiteAllowedNextPaths(registry, "/admin")
	check(t, err)
	sessionAuth, err := websession.New(websession.Config{Sessions: runtime.Sessions(), Authenticator: runtime.Authenticator(), Authorizer: auth.PrincipalAuthorizer{}, LoginPersistence: runtime.LoginPersistence(), Clock: clock,
		SessionCookie: websession.CookieConfig{Path: "/", AllowInsecure: true}, CSRFCookie: websession.CookieConfig{Path: "/", AllowInsecure: true}, LoginPath: "/admin/login/", FallbackPath: "/admin/", AllowedNextPaths: paths})
	check(t, err)
	site, err := admin.NewSite(admin.SiteConfig{Apps: configured.Apps(), Namespace: "deleteadmin", Registry: registry, Auth: sessionAuth})
	check(t, err)
	application, err := web.NewApplication(web.Config{Settings: configured, Routes: site.Routes()})
	check(t, err)
	server := httptest.NewServer(application)
	t.Cleanup(server.Close)
	jar, err := cookiejar.New(nil)
	check(t, err)
	client := &http.Client{Jar: jar, Transport: server.Client().Transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	t.Cleanup(client.CloseIdleConnections)
	call := func(t *testing.T, method, path string, values url.Values, want int) (http.Header, string) {
		t.Helper()
		request, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, strings.NewReader(values.Encode()))
		check(t, err)
		if method == "POST" {
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.Header.Set("Origin", server.URL)
		}
		response, err := client.Do(request)
		check(t, err)
		body, readErr := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
		check(t, readErr)
		check(t, response.Body.Close())
		if response.StatusCode != want || len(body) > 1<<20 || strings.Contains(string(body), password) || strings.Contains(string(body), f.secret) {
			t.Fatal("selected Admin deletion response or privacy boundary", method, path, response.StatusCode, want)
		}
		return response.Header.Clone(), string(body)
	}
	pattern := regexp.MustCompile(`name="csrfmiddlewaretoken" value="([A-Za-z0-9_-]{128})"`)
	token := func(t *testing.T, page string) string {
		t.Helper()
		match := pattern.FindStringSubmatch(page)
		if len(match) != 2 {
			t.Fatal("selected Admin deletion omitted actual CSRF state")
		}
		return match[1]
	}
	_, page := call(t, "GET", "/admin/login/", nil, 200)
	call(t, "POST", "/admin/login/", url.Values{"username": {f.actorRow.Contact}, "password": {password}, "csrfmiddlewaretoken": {token(t, page)}}, 302)
	guard, err := tickets.TicketObjects.Create(t.Context(), reopen(t), tickets.NewTicketCreate("protect Admin descendant", tree.child.ID))
	check(t, err)
	path := "/admin/accounts/delete/?id=" + strconv.FormatInt(tree.root.ID, 10)
	for _, mode := range []string{"csrf", "stale", "protected"} {
		if !t.Run(mode, func(t *testing.T) {
			_, page := call(t, "GET", path, nil, 200)
			data := url.Values{"confirm": {"yes"}, "expected_revision": {"1"}, "csrfmiddlewaretoken": {token(t, page)}}
			want := 200
			if mode == "csrf" {
				data.Del("csrfmiddlewaretoken")
				want = 403
			} else if mode == "stale" {
				data.Set("expected_revision", "2")
				want = 409
			}
			before := deletionSnapshot(t, reopen(t))
			header, body := call(t, "POST", path, data, want)
			if header.Get("Location") != "" || mode == "protected" && !strings.Contains(body, "protected related objects") {
				t.Fatal("rejected Admin deletion published success or lost protection")
			}
			requireDeletionUnchanged(t, reopen(t), before)
		}) {
			t.FailNow()
		}
	}
	count, err := tickets.TicketObjects.Delete(t.Context(), reopen(t), &guard)
	check(t, err)
	if count != 1 {
		t.Fatal("Admin protection lost its actual host guard")
	}
	if !t.Run("commit", func(t *testing.T) {
		_, page := call(t, "GET", path, nil, 200)
		before := deletionSnapshot(t, reopen(t))
		header, _ := call(t, "POST", path, url.Values{"confirm": {"yes"}, "expected_revision": {"1"}, "csrfmiddlewaretoken": {token(t, page)}}, 302)
		if header.Get("Location") != "/admin/accounts/" {
			t.Fatal("confirmed Admin deletion lost its safe redirect")
		}
		call(t, "GET", path, nil, 404)
		after := deletionSnapshot(t, reopen(t))
		assertDeletionTreeState(t, reopen(t), f, tree, before, after)
		if value, found, err := runtime.Credentials().CredentialByID(t.Context(), tree.root.Subject); err != nil || found || value.Principal().Authenticated() {
			t.Fatal("Admin deletion retained a credential", err)
		}
	}) {
		t.FailNow()
	}
}
