package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

func TestSiteStartupPathPlanAndDuplicateRouteRejection(t *testing.T) {
	registry := siteTestRegistry(t, newSiteModelState())
	paths, err := SiteAllowedNextPaths(registry, "")
	if err != nil {
		t.Fatalf("SiteAllowedNextPaths() error = %v", err)
	}
	wantPaths := []string{
		"/admin/",
		"/admin/articles/",
		"/admin/articles/add/",
		"/admin/articles/change/",
		"/admin/articles/delete/",
		"/admin/articles/history/",
	}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("SiteAllowedNextPaths() = %v, want %v", paths, wantPaths)
	}
	if sessionauth.DefaultLimits().MaxAllowedNextPaths < 1+5*MaximumRegistryModels {
		t.Fatalf("default allowed-next limit %d cannot compose the maximum Admin registry", sessionauth.DefaultLimits().MaxAllowedNextPaths)
	}
	if _, err := SiteAllowedNextPaths(Registry{}, "/admin"); errorCode(err) != "invalid" {
		t.Fatalf("SiteAllowedNextPaths(zero registry) error = %v", err)
	}
	for _, basePath := range []string{"/", "admin", "/admin/", "/admin//nested", "/admin?debug"} {
		if _, err := SiteAllowedNextPaths(registry, basePath); errorCode(err) != "invalid" {
			t.Fatalf("SiteAllowedNextPaths(basePath=%q) error = %v", basePath, err)
		}
	}

	installed := mustApps(t)
	runtime, _ := siteTestRuntime(t, registry, paths, "/admin/login/", "/admin/", siteTestIdentities(t))
	site, err := NewSite(SiteConfig{
		Apps: installed, Namespace: "godj_conformance", Registry: registry, Auth: runtime, PageSize: 2,
	})
	if err != nil {
		t.Fatalf("NewSite() error = %v", err)
	}
	routes := site.Routes()
	if len(routes) != 13 {
		t.Fatalf("Routes() count = %d, want 13", len(routes))
	}
	seenNames := make(map[string]struct{}, len(routes))
	seenMethodPaths := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		if _, duplicate := seenNames[route.Name]; duplicate {
			t.Fatalf("duplicate route name %q", route.Name)
		}
		seenNames[route.Name] = struct{}{}
		key := route.Method + " " + route.Path
		if _, duplicate := seenMethodPaths[key]; duplicate {
			t.Fatalf("duplicate route method/path %q", key)
		}
		seenMethodPaths[key] = struct{}{}
	}

	tests := []struct {
		name   string
		config SiteConfig
		code   string
		path   string
	}{
		{name: "missing namespace", config: SiteConfig{Apps: installed, Namespace: "missing", Registry: registry, Auth: runtime}, code: "not_installed", path: "site.namespace"},
		{name: "missing auth", config: SiteConfig{Apps: installed, Namespace: "godj_conformance", Registry: registry}, code: "missing", path: "site.auth"},
		{name: "zero registry", config: SiteConfig{Apps: installed, Namespace: "godj_conformance", Registry: Registry{}, Auth: runtime}, code: "invalid", path: "site.registry"},
		{name: "invalid page size", config: SiteConfig{Apps: installed, Namespace: "godj_conformance", Registry: registry, Auth: runtime, PageSize: MaximumListLimit + 1}, code: "invalid", path: "site.page_size"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewSite(test.config)
			var configErr *ConfigError
			if !errors.As(err, &configErr) || configErr.Code != test.code || configErr.Path != test.path {
				t.Fatalf("NewSite() error = %v, want %s/%s", err, test.path, test.code)
			}
		})
	}

	mismatchedRuntime, _ := siteTestRuntime(t, registry, []string{"/admin/"}, "/admin/login/", "/admin/", siteTestIdentities(t))
	if _, err := NewSite(SiteConfig{Apps: installed, Namespace: "godj_conformance", Registry: registry, Auth: mismatchedRuntime}); errorCode(err) != "mismatch" {
		t.Fatalf("NewSite(missing allowed paths) error = %v", err)
	}
	extraPathRuntime, _ := siteTestRuntime(t, registry, append(append([]string(nil), paths...), "/other/"), "/admin/login/", "/admin/", siteTestIdentities(t))
	if _, err := NewSite(SiteConfig{Apps: installed, Namespace: "godj_conformance", Registry: registry, Auth: extraPathRuntime}); errorCode(err) != "mismatch" {
		t.Fatalf("NewSite(extra allowed path) error = %v", err)
	}
	wrongRedirectRuntime, _ := siteTestRuntime(t, registry, paths, "/other/login/", "/admin/", siteTestIdentities(t))
	if _, err := NewSite(SiteConfig{Apps: installed, Namespace: "godj_conformance", Registry: registry, Auth: wrongRedirectRuntime}); errorCode(err) != "mismatch" {
		t.Fatalf("NewSite(mismatched redirect paths) error = %v", err)
	}
	narrowCookieRuntime, _ := siteTestRuntimeWithCookiePaths(
		t, paths, "/admin/login/", "/admin/", siteTestIdentities(t), "/admin/articles/", "/admin/",
	)
	if _, err := NewSite(SiteConfig{Apps: installed, Namespace: "godj_conformance", Registry: registry, Auth: narrowCookieRuntime}); errorCode(err) != "mismatch" {
		t.Fatalf("NewSite(mismatched cookie paths) error = %v", err)
	}

	duplicateRegistry := Registry{
		models:     append(append([]registeredModel(nil), registry.models...), registry.models[0]),
		byIdentity: cloneIndex(registry.byIdentity),
		bySlug:     cloneIndex(registry.bySlug),
	}
	duplicateRegistry.models[1].slug = "login"
	duplicateRegistry.byIdentity["godj_conformance.duplicate"] = 1
	duplicateRegistry.bySlug["login"] = 1
	duplicatePaths, err := SiteAllowedNextPaths(duplicateRegistry, "/admin")
	if err != nil {
		t.Fatal(err)
	}
	duplicateRuntime, _ := siteTestRuntime(t, duplicateRegistry, duplicatePaths, "/admin/login/", "/admin/", siteTestIdentities(t))
	if _, err := NewSite(SiteConfig{Apps: installed, Namespace: "godj_conformance", Registry: duplicateRegistry, Auth: duplicateRuntime}); errorCode(err) != "duplicate" {
		t.Fatalf("NewSite(duplicate routes) error = %v", err)
	}
}

func TestSiteApplicationAuthenticationAuthorizationListAndRotation(t *testing.T) {
	harness := newSiteApplicationHarness(t, 2)
	client := newSiteHTTPClient(harness.application)

	response := client.do(http.MethodGet, "/admin/articles/", nil)
	if response.Code != http.StatusFound || response.Header().Get("Location") != "/admin/login/?next=%2Fadmin%2Farticles%2F" {
		t.Fatalf("anonymous list response = %d location %q", response.Code, response.Header().Get("Location"))
	}
	if response.Header().Get("Content-Security-Policy") != "frame-ancestors 'none'" || response.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("anonymous redirect framing headers = %#v", response.Header())
	}
	loginPage := client.do(http.MethodGet, response.Header().Get("Location"), nil)
	if loginPage.Code != http.StatusOK || !strings.Contains(loginPage.Body.String(), `form method="post" action="/admin/login/"`) {
		t.Fatalf("login form does not use the query-free POST path: %d %q", loginPage.Code, loginPage.Body.String())
	}
	if loginPage.Header().Get("Content-Security-Policy") != "frame-ancestors 'none'" || loginPage.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("rendered login framing headers = %#v", loginPage.Header())
	}

	oldCSRF := client.login(t, "admin", "secret", "/admin/articles/?p=2")
	if got := client.cookieValue(sessionauth.DefaultSessionCookieName); got == "" {
		t.Fatal("successful login did not set a session cookie")
	}
	if got := client.cookieValue(sessionauth.DefaultCSRFCookieName); got == "" || got == oldCSRF {
		t.Fatal("successful login did not rotate the pre-login CSRF secret")
	}

	response = client.do(http.MethodGet, "/admin/articles/?p=2", nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `data-page="2"`) ||
		!strings.Contains(response.Body.String(), `data-object-id="3"`) || strings.Contains(response.Body.String(), `data-object-id="1"`) {
		t.Fatalf("page 2 response = %d %q", response.Code, response.Body.String())
	}
	if got := harness.state.lastListRequest(); got.Offset != 2 || got.Limit != 2 {
		t.Fatalf("page 2 ListRequest = %#v", got)
	}

	response = client.do(http.MethodGet, "/admin/articles/?p=invalid", nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `data-query-error="invalid_page"`) || !strings.Contains(response.Body.String(), `data-page="1"`) {
		t.Fatalf("invalid page response = %d %q", response.Code, response.Body.String())
	}
	if got := harness.state.lastListRequest(); got.Offset != 0 || got.Limit != 2 {
		t.Fatalf("invalid-page fallback ListRequest = %#v", got)
	}
	response = client.do(http.MethodGet, "/admin/articles/?p=999", nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `data-query-error="invalid_page"`) ||
		!strings.Contains(response.Body.String(), `data-page="1"`) || !strings.Contains(response.Body.String(), `data-object-id="1"`) {
		t.Fatalf("out-of-range page response = %d %q", response.Code, response.Body.String())
	}
	if got := harness.state.lastListRequest(); got.Offset != 0 || got.Limit != 2 {
		t.Fatalf("out-of-range fallback ListRequest = %#v", got)
	}
	response = client.do(http.MethodGet, "/admin/articles/?p=2147483648", nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `data-query-error="invalid_page"`) ||
		!strings.Contains(response.Body.String(), `data-page="1"`) {
		t.Fatalf("bounded page response = %d %q", response.Code, response.Body.String())
	}
	if got := harness.state.lastListRequest(); got.Offset != 0 || got.Limit != 2 {
		t.Fatalf("bounded-page fallback ListRequest = %#v", got)
	}

	oldSession := client.cookieValue(sessionauth.DefaultSessionCookieName)
	oldCookies := client.detachedCookies()
	currentToken := siteCSRFToken(t, response.Body.String())
	response = client.do(http.MethodPost, "/admin/login/", url.Values{
		"csrfmiddlewaretoken": {currentToken}, "username": {"admin"}, "password": {"secret"}, "next": {"/admin/"},
	})
	if response.Code != http.StatusFound || client.cookieValue(sessionauth.DefaultSessionCookieName) == oldSession {
		t.Fatalf("re-login response = %d, session was not rotated", response.Code)
	}
	stale := &siteHTTPClient{application: harness.application, cookies: oldCookies}
	response = stale.do(http.MethodGet, "/admin/", nil)
	if response.Code != http.StatusFound || !strings.HasPrefix(response.Header().Get("Location"), "/admin/login/") {
		t.Fatalf("rotated stale session response = %d location %q", response.Code, response.Header().Get("Location"))
	}

	nonAdmin := newSiteHTTPClient(harness.application)
	response = nonAdmin.do(http.MethodGet, "/admin/login/?next=%2Fadmin%2F", nil)
	nonAdminToken := siteCSRFToken(t, response.Body.String())
	response = nonAdmin.do(http.MethodPost, "/admin/login/", url.Values{
		"csrfmiddlewaretoken": {nonAdminToken}, "username": {"nonadmin"}, "password": {"secret"}, "next": {"/admin/"},
	})
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `data-login-error="invalid_credentials"`) ||
		nonAdmin.cookieValue(sessionauth.DefaultSessionCookieName) != "" {
		t.Fatalf("non-admin login response = %d session %q body %q", response.Code, nonAdmin.cookieValue(sessionauth.DefaultSessionCookieName), response.Body.String())
	}

	modelDenied := newSiteHTTPClient(harness.application)
	modelDenied.login(t, "modeldenied", "secret", "/admin/")
	response = modelDenied.do(http.MethodGet, "/admin/", nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "No permitted models.") {
		t.Fatalf("model-denied index response = %d %q", response.Code, response.Body.String())
	}
	response = modelDenied.do(http.MethodGet, "/admin/articles/", nil)
	if response.Code != http.StatusForbidden {
		t.Fatalf("model-denied list status = %d", response.Code)
	}

	viewer := newSiteHTTPClient(harness.application)
	viewer.login(t, "viewer", "secret", "/admin/articles/")
	response = viewer.do(http.MethodGet, "/admin/articles/", nil)
	body := response.Body.String()
	if response.Code != http.StatusOK || strings.Contains(body, ">Add Article<") || strings.Contains(body, ">Change</a>") ||
		strings.Contains(body, ">Delete</a>") || strings.Contains(body, `data-action="publish"`) {
		t.Fatalf("permission-gated list UI = %d %q", response.Code, body)
	}

	logoutPage := client.do(http.MethodGet, "/admin/articles/", nil)
	harness.store.resetCounts()
	response = client.do(http.MethodPost, "/admin/logout/", url.Values{
		"csrfmiddlewaretoken": {siteCSRFToken(t, logoutPage.Body.String())},
	})
	if response.Code != http.StatusFound || response.Header().Get("Location") != "/admin/login/" {
		t.Fatalf("logout response = %d location %q", response.Code, response.Header().Get("Location"))
	}
	if counts := harness.store.counts(); counts.deletes != 1 {
		t.Fatalf("logout session-store counts = %#v, want one delete", counts)
	}
}

func TestSiteRecoversFromAmbiguousSessionCookiesOnLoginAndLogout(t *testing.T) {
	t.Run("login creates a fresh session", func(t *testing.T) {
		harness := newSiteApplicationHarness(t, 2)
		client := newSiteHTTPClient(harness.application)
		login := client.do(http.MethodGet, "/admin/login/", nil)
		response := siteServeWithAdditionalCookies(
			harness.application,
			client.detachedCookies(),
			[]*http.Cookie{
				{Name: sessionauth.DefaultSessionCookieName, Value: "forged-one", Path: "/"},
				{Name: sessionauth.DefaultSessionCookieName, Value: "forged-two", Path: "/"},
			},
			http.MethodPost,
			"/admin/login/",
			url.Values{
				"csrfmiddlewaretoken": {siteCSRFToken(t, login.Body.String())},
				"username":            {"admin"},
				"password":            {"secret"},
				"next":                {"/admin/"},
			},
		)
		if response.Code != http.StatusFound || response.Header().Get("Location") != "/admin/" {
			t.Fatalf("ambiguous-cookie login = %d location %q body %q", response.Code, response.Header().Get("Location"), response.Body.String())
		}
		if cookie := siteResponseCookie(response, sessionauth.DefaultSessionCookieName); cookie == nil || cookie.Value == "" || cookie.MaxAge < 1 {
			t.Fatalf("ambiguous-cookie login session cookie = %#v", cookie)
		}
		if counts := harness.store.counts(); counts.creates != 1 || counts.loads != 0 || counts.rotates != 0 {
			t.Fatalf("ambiguous-cookie login store counts = %#v", counts)
		}
	})

	t.Run("logout flushes valid duplicate and returns deletion cookies", func(t *testing.T) {
		harness := newSiteApplicationHarness(t, 2)
		client := newSiteHTTPClient(harness.application)
		client.login(t, "admin", "secret", "/admin/")
		validID, err := sessions.ParseID(client.cookieValue(sessionauth.DefaultSessionCookieName))
		if err != nil {
			t.Fatal(err)
		}
		index := client.do(http.MethodGet, "/admin/", nil)
		harness.store.resetCounts()
		response := siteServeWithAdditionalCookies(
			harness.application,
			client.detachedCookies(),
			[]*http.Cookie{{Name: sessionauth.DefaultSessionCookieName, Value: "forged", Path: "/"}},
			http.MethodPost,
			"/admin/logout/",
			url.Values{"csrfmiddlewaretoken": {siteCSRFToken(t, index.Body.String())}},
		)
		if response.Code != http.StatusFound || response.Header().Get("Location") != "/admin/login/" {
			t.Fatalf("ambiguous-cookie logout = %d location %q body %q", response.Code, response.Header().Get("Location"), response.Body.String())
		}
		for _, name := range []string{sessionauth.DefaultSessionCookieName, sessionauth.DefaultCSRFCookieName} {
			cookie := siteResponseCookie(response, name)
			if cookie == nil || cookie.MaxAge >= 0 {
				t.Fatalf("ambiguous-cookie logout deletion %q = %#v", name, cookie)
			}
		}
		if counts := harness.store.counts(); counts.deletes != 1 || counts.loads != 0 {
			t.Fatalf("ambiguous-cookie logout store counts = %#v, want one delete and zero loads", counts)
		}
		if _, found, err := harness.store.Store.Load(context.Background(), validID); err != nil || found {
			t.Fatalf("valid duplicate session survived logout: found=%v err=%v", found, err)
		}
	})
}

func TestSiteMutationBoundaryFormsActionsAndHistoryAfterDeletion(t *testing.T) {
	harness := newSiteApplicationHarness(t, 2)
	client := newSiteHTTPClient(harness.application)
	client.login(t, "admin", "secret", "/admin/articles/")
	list := client.do(http.MethodGet, "/admin/articles/", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d", list.Code)
	}
	token := siteCSRFToken(t, list.Body.String())

	harness.store.resetCounts()
	before := harness.state.mutationCounts()
	response := client.do(http.MethodPost, "/admin/articles/add/", url.Values{
		"csrfmiddlewaretoken": {"invalid"}, "title": {"Must not persist"}, "published": {"on"}, "summary": {""},
	})
	if response.Code != http.StatusForbidden {
		t.Fatalf("invalid-CSRF add status = %d body %q", response.Code, response.Body.String())
	}
	if counts := harness.store.counts(); counts != (siteStoreCounts{}) {
		t.Fatalf("CSRF rejection touched session store: %#v", counts)
	}
	if got := harness.state.mutationCounts(); got != before {
		t.Fatalf("CSRF rejection mutated model: got %#v, before %#v", got, before)
	}

	response = client.do(http.MethodPost, "/admin/articles/add/", url.Values{
		"csrfmiddlewaretoken": {token}, "title": {"First raw value", "Second raw value"}, "published": {"on"}, "summary": {""},
	})
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `data-error-code="multiple"`) ||
		!strings.Contains(response.Body.String(), `value="First raw value"`) {
		t.Fatalf("duplicate scalar form response = %d %q", response.Code, response.Body.String())
	}
	if got := harness.state.mutationCounts(); got != before {
		t.Fatalf("duplicate scalar form mutated model: got %#v, before %#v", got, before)
	}

	response = client.do(http.MethodPost, "/admin/articles/add/", url.Values{
		"csrfmiddlewaretoken": {token}, "title": {"bad\x00title"}, "published": {"on"}, "summary": {""},
	})
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `data-error-code="null_characters_not_allowed"`) ||
		!strings.Contains(response.Body.String(), `value="bad�title"`) || strings.Contains(response.Body.String(), "\x00") {
		t.Fatalf("NUL form response = %d %q", response.Code, response.Body.String())
	}
	if got := harness.state.mutationCounts(); got != before {
		t.Fatalf("NUL form mutated model: got %#v, before %#v", got, before)
	}

	response = client.do(http.MethodPost, "/admin/articles/action/publish/", url.Values{
		"csrfmiddlewaretoken": {token}, "selected": {"3", "1", "3"},
	})
	if response.Code != http.StatusFound || !siteSignedNoticeLocation(response.Header().Get("Location"), "published", "2") {
		t.Fatalf("action response = %d location %q body %q", response.Code, response.Header().Get("Location"), response.Body.String())
	}
	if got := harness.state.lastActionSelection(); !reflect.DeepEqual(got, []int64{1, 3}) {
		t.Fatalf("action selected IDs = %v, want [1 3]", got)
	}

	response = client.do(http.MethodPost, "/admin/articles/delete/?id=1", url.Values{
		"csrfmiddlewaretoken": {token}, "confirm": {"yes"},
	})
	if response.Code != http.StatusFound || !siteSignedNoticeLocation(response.Header().Get("Location"), "deleted", "") {
		t.Fatalf("delete response = %d location %q body %q", response.Code, response.Header().Get("Location"), response.Body.String())
	}
	response = client.do(http.MethodGet, "/admin/articles/change/?id=1", nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("deleted object change status = %d", response.Code)
	}
	response = client.do(http.MethodGet, "/admin/articles/history/?id=1", nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `data-action="delete"`) ||
		!strings.Contains(response.Body.String(), "First article") {
		t.Fatalf("deleted object history response = %d %q", response.Code, response.Body.String())
	}
}

func TestSiteUsesRuntimeDenyOverlayForRoutesAndVisibleOperations(t *testing.T) {
	harness := newSiteApplicationHarnessWithAuthorizer(t, 2, siteDenyAuthorizer{denied: mustPermission(t, "articles.change")})
	client := newSiteHTTPClient(harness.application)
	client.login(t, "admin", "secret", "/admin/articles/")
	response := client.do(http.MethodGet, "/admin/articles/", nil)
	body := response.Body.String()
	if response.Code != http.StatusOK || strings.Contains(body, ">Change</a>") || strings.Contains(body, `data-action="publish"`) {
		t.Fatalf("authorizer-denied operations remain visible: %d %q", response.Code, body)
	}
	response = client.do(http.MethodGet, "/admin/articles/change/?id=1", nil)
	if response.Code != http.StatusForbidden {
		t.Fatalf("authorizer-denied change status = %d", response.Code)
	}
}

func TestSiteRoutesAreDetachedAndConcurrentUseIsStable(t *testing.T) {
	harness := newSiteApplicationHarness(t, 2)
	routes := harness.site.Routes()
	originalName, originalPath := routes[0].Name, routes[0].Path
	routes[0].Name = "forged:name"
	routes[0].Path = "/forged/"
	routes[0].Handler = nil
	again := harness.site.Routes()
	if again[0].Name != originalName || again[0].Path != originalPath || again[0].Handler == nil {
		t.Fatalf("Routes() aliased Site storage: %#v", again[0])
	}

	client := newSiteHTTPClient(harness.application)
	client.login(t, "admin", "secret", "/admin/articles/")
	cookies := client.detachedCookies()
	const goroutines = 24
	errorsFound := make(chan error, goroutines)
	var wait sync.WaitGroup
	for index := 0; index < goroutines; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			response := siteServeWithCookies(harness.application, cookies, http.MethodGet, "/admin/articles/?p=invalid", nil)
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `data-query-error="invalid_page"`) {
				errorsFound <- fmt.Errorf("response = %d %q", response.Code, response.Body.String())
				return
			}
			errorsFound <- nil
		}()
	}
	wait.Wait()
	for index := 0; index < goroutines; index++ {
		if err := <-errorsFound; err != nil {
			t.Fatal(err)
		}
	}
}

type siteApplicationHarness struct {
	site        *Site
	application *web.Application
	store       *siteCountingStore
	state       *siteModelState
}

func newSiteApplicationHarness(t *testing.T, pageSize int) siteApplicationHarness {
	return newSiteApplicationHarnessWithAuthorizer(t, pageSize, auth.PrincipalAuthorizer{})
}

func newSiteApplicationHarnessWithAuthorizer(t *testing.T, pageSize int, authorizer auth.Authorizer) siteApplicationHarness {
	t.Helper()
	state := newSiteModelState()
	registry := siteTestRegistry(t, state)
	installed := mustApps(t)
	allowed, err := SiteAllowedNextPaths(registry, "/admin")
	if err != nil {
		t.Fatalf("SiteAllowedNextPaths() error = %v", err)
	}
	runtime, store := siteTestRuntimeConfigured(t, allowed, "/admin/login/", "/admin/", siteTestIdentities(t), "/", "/", authorizer)
	site, err := NewSite(SiteConfig{
		Apps: installed, Namespace: "godj_conformance", Registry: registry, Auth: runtime, PageSize: pageSize,
	})
	if err != nil {
		t.Fatalf("NewSite() error = %v", err)
	}
	configured, err := settings.New(settings.Definition{
		ProjectName:   "admin_site_test",
		InstalledApps: installed.All(),
	})
	if err != nil {
		t.Fatalf("settings.New() error = %v", err)
	}
	application, err := web.NewApplication(web.Config{Settings: configured, Routes: site.Routes()})
	if err != nil {
		t.Fatalf("web.NewApplication() error = %v", err)
	}
	return siteApplicationHarness{site: site, application: application, store: store, state: state}
}

type siteIdentity struct {
	username  string
	principal auth.Principal
}

func siteTestIdentities(t *testing.T) []siteIdentity {
	t.Helper()
	access := mustPermission(t, string(DefaultAccessPermission))
	view := mustPermission(t, "articles.view")
	add := mustPermission(t, "articles.add")
	change := mustPermission(t, "articles.change")
	deletePermission := mustPermission(t, "articles.delete")
	return []siteIdentity{
		{username: "admin", principal: sitePrincipal(t, "admin-principal", access, view, add, change, deletePermission)},
		{username: "nonadmin", principal: sitePrincipal(t, "nonadmin-principal", view)},
		{username: "modeldenied", principal: sitePrincipal(t, "model-denied-principal", access)},
		{username: "viewer", principal: sitePrincipal(t, "viewer-principal", access, view)},
	}
}

func sitePrincipal(t *testing.T, id string, permissions ...auth.Permission) auth.Principal {
	t.Helper()
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: id, Active: true, Permissions: permissions})
	if err != nil {
		t.Fatalf("auth.NewPrincipal() error = %v", err)
	}
	return principal
}

func siteTestRuntime(
	t *testing.T,
	_ Registry,
	allowed []string,
	loginPath, fallbackPath string,
	identities []siteIdentity,
) (*sessionauth.Runtime, *siteCountingStore) {
	return siteTestRuntimeWithCookiePaths(t, allowed, loginPath, fallbackPath, identities, "/", "/")
}

func siteTestRuntimeWithCookiePaths(
	t *testing.T,
	allowed []string,
	loginPath, fallbackPath string,
	identities []siteIdentity,
	sessionPath, csrfPath string,
) (*sessionauth.Runtime, *siteCountingStore) {
	return siteTestRuntimeConfigured(t, allowed, loginPath, fallbackPath, identities, sessionPath, csrfPath, auth.PrincipalAuthorizer{})
}

func siteTestRuntimeConfigured(
	t *testing.T,
	allowed []string,
	loginPath, fallbackPath string,
	identities []siteIdentity,
	sessionPath, csrfPath string,
	authorizer auth.Authorizer,
) (*sessionauth.Runtime, *siteCountingStore) {
	t.Helper()
	memory, err := sessions.NewMemoryStore(64)
	if err != nil {
		t.Fatalf("sessions.NewMemoryStore() error = %v", err)
	}
	store := &siteCountingStore{Store: memory}
	manager, err := sessions.NewManager(store, sessions.Config{})
	if err != nil {
		t.Fatalf("sessions.NewManager() error = %v", err)
	}
	credentials := make([]auth.Credential, len(identities))
	for index, identity := range identities {
		credentials[index], err = auth.NewCredential(identity.username, "plain:secret", identity.principal)
		if err != nil {
			t.Fatalf("auth.NewCredential(%q) error = %v", identity.username, err)
		}
	}
	authenticator, err := auth.NewMemoryAuthenticator(credentials, sitePlainHasher{})
	if err != nil {
		t.Fatalf("auth.NewMemoryAuthenticator() error = %v", err)
	}
	runtime, err := sessionauth.New(sessionauth.Config{
		Sessions:         manager,
		Authenticator:    authenticator,
		Authorizer:       authorizer,
		SessionCookie:    sessionauth.CookieConfig{Path: sessionPath, AllowInsecure: true, Lifetime: 2 * time.Hour},
		CSRFCookie:       sessionauth.CookieConfig{Path: csrfPath, AllowInsecure: true},
		LoginPath:        loginPath,
		FallbackPath:     fallbackPath,
		AllowedNextPaths: append([]string(nil), allowed...),
	})
	if err != nil {
		t.Fatalf("sessionauth.New() error = %v", err)
	}
	return runtime, store
}

type siteDenyAuthorizer struct{ denied auth.Permission }

func (authorizer siteDenyAuthorizer) Allowed(ctx context.Context, _ auth.Principal, permission auth.Permission) (bool, error) {
	if ctx == nil {
		return false, errors.New("nil context")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return permission != authorizer.denied, nil
}

type sitePlainHasher struct{}

func (sitePlainHasher) Hash(ctx context.Context, password string) (string, error) {
	if ctx == nil {
		return "", errors.New("nil context")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "plain:" + password, nil
}

func (sitePlainHasher) Verify(ctx context.Context, password, encoded string) (bool, error) {
	if ctx == nil {
		return false, errors.New("nil context")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return encoded == "plain:"+password, nil
}

func (sitePlainHasher) ValidateEncoded(encoded string) error {
	if !strings.HasPrefix(encoded, "plain:") {
		return errors.New("invalid test hash")
	}
	return nil
}

type siteStoreCounts struct {
	loads   int
	creates int
	touches int
	rotates int
	deletes int
}

type siteCountingStore struct {
	sessions.Store
	mu sync.Mutex
	siteStoreCounts
}

func (store *siteCountingStore) Load(ctx context.Context, id sessions.ID) (sessions.Record, bool, error) {
	store.mu.Lock()
	store.loads++
	store.mu.Unlock()
	return store.Store.Load(ctx, id)
}

func (store *siteCountingStore) Create(ctx context.Context, record sessions.Record) (bool, error) {
	store.mu.Lock()
	store.creates++
	store.mu.Unlock()
	return store.Store.Create(ctx, record)
}

func (store *siteCountingStore) Touch(ctx context.Context, id sessions.ID, accessedAt, idleExpiresAt time.Time) (sessions.Record, bool, error) {
	store.mu.Lock()
	store.touches++
	store.mu.Unlock()
	return store.Store.Touch(ctx, id, accessedAt, idleExpiresAt)
}

func (store *siteCountingStore) Rotate(ctx context.Context, id sessions.ID, replacement sessions.Record) (sessions.Record, bool, error) {
	store.mu.Lock()
	store.rotates++
	store.mu.Unlock()
	return store.Store.Rotate(ctx, id, replacement)
}

func (store *siteCountingStore) Delete(ctx context.Context, id sessions.ID) error {
	store.mu.Lock()
	store.deletes++
	store.mu.Unlock()
	return store.Store.Delete(ctx, id)
}

func (store *siteCountingStore) resetCounts() {
	store.mu.Lock()
	store.siteStoreCounts = siteStoreCounts{}
	store.mu.Unlock()
}

func (store *siteCountingStore) counts() siteStoreCounts {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.siteStoreCounts
}

type siteMutationCounts struct {
	creates int
	updates int
	deletes int
	actions int
}

type siteModelState struct {
	mu           sync.Mutex
	articles     map[int64]registryArticle
	nextID       int64
	lastList     ListRequest
	mutations    siteMutationCounts
	lastSelected []int64
	history      map[int64][]AuditEntry
	nextSequence uint64
}

func newSiteModelState() *siteModelState {
	summary := "Third summary"
	return &siteModelState{
		articles: map[int64]registryArticle{
			1: {id: 1, title: "First article"},
			2: {id: 2, title: "Second article", published: true},
			3: {id: 3, title: "Third article", summary: &summary},
		},
		nextID:       4,
		history:      make(map[int64][]AuditEntry),
		nextSequence: 1,
	}
}

func siteTestRegistry(t *testing.T, state *siteModelState) Registry {
	t.Helper()
	config := validRegistryConfig(t)
	config.List = func(ctx context.Context, request ListRequest) (Page[registryArticle], error) {
		if err := ctx.Err(); err != nil {
			return Page[registryArticle]{}, err
		}
		state.mu.Lock()
		defer state.mu.Unlock()
		state.lastList = request
		ids := make([]int64, 0, len(state.articles))
		for id, article := range state.articles {
			if request.Search != "" && !strings.Contains(strings.ToLower(article.title), strings.ToLower(request.Search)) {
				continue
			}
			ids = append(ids, id)
		}
		sort.Slice(ids, func(left, right int) bool { return ids[left] < ids[right] })
		total := int64(len(ids))
		if request.Offset >= len(ids) {
			return Page[registryArticle]{Total: total, Offset: request.Offset, Limit: request.Limit}, nil
		}
		end := request.Offset + request.Limit
		if end > len(ids) {
			end = len(ids)
		}
		items := make([]registryArticle, 0, end-request.Offset)
		for _, id := range ids[request.Offset:end] {
			items = append(items, cloneSiteArticle(state.articles[id]))
		}
		return Page[registryArticle]{Items: items, Total: total, Offset: request.Offset, Limit: request.Limit}, nil
	}
	config.Get = func(ctx context.Context, id int64) (registryArticle, bool, error) {
		if err := ctx.Err(); err != nil {
			return registryArticle{}, false, err
		}
		state.mu.Lock()
		defer state.mu.Unlock()
		article, found := state.articles[id]
		return cloneSiteArticle(article), found, nil
	}
	config.Create = func(ctx context.Context, _ auth.Principal, values forms.Values) (registryArticle, error) {
		if err := ctx.Err(); err != nil {
			return registryArticle{}, err
		}
		article := siteArticleFromValues(values)
		state.mu.Lock()
		defer state.mu.Unlock()
		article.id = state.nextID
		state.nextID++
		state.articles[article.id] = cloneSiteArticle(article)
		state.mutations.creates++
		return cloneSiteArticle(article), nil
	}
	config.Update = func(ctx context.Context, _ auth.Principal, id int64, values forms.Values) (registryArticle, []string, error) {
		if err := ctx.Err(); err != nil {
			return registryArticle{}, nil, err
		}
		article := siteArticleFromValues(values)
		article.id = id
		state.mu.Lock()
		defer state.mu.Unlock()
		state.articles[id] = cloneSiteArticle(article)
		state.mutations.updates++
		return cloneSiteArticle(article), []string{"title", "published", "summary"}, nil
	}
	config.Delete = func(ctx context.Context, principal auth.Principal, id int64) (registryArticle, error) {
		if err := ctx.Err(); err != nil {
			return registryArticle{}, err
		}
		state.mu.Lock()
		defer state.mu.Unlock()
		article := state.articles[id]
		delete(state.articles, id)
		state.mutations.deletes++
		state.appendHistoryLocked(principal.ID(), article, ActionDelete, nil)
		return cloneSiteArticle(article), nil
	}
	config.History = func(ctx context.Context, id int64, request HistoryRequest) ([]AuditEntry, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		state.mu.Lock()
		defer state.mu.Unlock()
		entries := state.history[id]
		if len(entries) > request.Limit {
			entries = entries[len(entries)-request.Limit:]
		}
		result := make([]AuditEntry, len(entries))
		for index := range entries {
			result[index] = entries[index].Clone()
		}
		return result, nil
	}
	config.Actions[0].Run = func(ctx context.Context, principal auth.Principal, ids []int64) (ActionResult, error) {
		if err := ctx.Err(); err != nil {
			return ActionResult{}, err
		}
		state.mu.Lock()
		defer state.mu.Unlock()
		state.lastSelected = append([]int64(nil), ids...)
		matched := make([]int64, 0, len(ids))
		for _, id := range ids {
			article, found := state.articles[id]
			if !found {
				continue
			}
			article.published = true
			state.articles[id] = article
			state.appendHistoryLocked(principal.ID(), article, ActionPublish, []string{"published"})
			matched = append(matched, id)
		}
		state.mutations.actions++
		return ActionResult{MatchedIDs: matched}, nil
	}
	builder := NewBuilder(mustApps(t))
	if err := RegisterModel(builder, config); err != nil {
		t.Fatalf("RegisterModel() error = %v", err)
	}
	registry, err := builder.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return registry
}

func (state *siteModelState) appendHistoryLocked(actor string, article registryArticle, action Action, changed []string) {
	entry := AuditEntry{
		Sequence:      state.nextSequence,
		ActorID:       actor,
		Model:         "godj_conformance.article",
		ObjectID:      article.id,
		Action:        action,
		ChangedFields: append([]string(nil), changed...),
		DisplayLabel:  article.title,
	}
	state.nextSequence++
	state.history[article.id] = append(state.history[article.id], entry)
}

func (state *siteModelState) lastListRequest() ListRequest {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.lastList
}

func (state *siteModelState) mutationCounts() siteMutationCounts {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.mutations
}

func (state *siteModelState) lastActionSelection() []int64 {
	state.mu.Lock()
	defer state.mu.Unlock()
	return append([]int64(nil), state.lastSelected...)
}

func siteArticleFromValues(values forms.Values) registryArticle {
	title, _ := values.String("title")
	published, _ := values.Boolean("published")
	var summary *string
	if value, ok := values.Get("summary"); ok && !value.IsNull() {
		text, _ := value.AsString()
		summary = &text
	}
	return registryArticle{title: title, published: published, summary: summary}
}

func cloneSiteArticle(article registryArticle) registryArticle {
	if article.summary != nil {
		value := *article.summary
		article.summary = &value
	}
	return article
}

type siteHTTPClient struct {
	application *web.Application
	cookies     map[string]*http.Cookie
}

func newSiteHTTPClient(application *web.Application) *siteHTTPClient {
	return &siteHTTPClient{application: application, cookies: make(map[string]*http.Cookie)}
}

func (client *siteHTTPClient) do(method, target string, values url.Values) *httptest.ResponseRecorder {
	response := siteServeWithCookies(client.application, client.cookies, method, target, values)
	for _, cookie := range response.Result().Cookies() {
		if cookie.MaxAge < 0 {
			delete(client.cookies, cookie.Name)
			continue
		}
		clone := *cookie
		client.cookies[cookie.Name] = &clone
	}
	return response
}

func (client *siteHTTPClient) login(t *testing.T, username, password, next string) string {
	t.Helper()
	response := client.do(http.MethodGet, "/admin/login/?next="+url.QueryEscape(next), nil)
	if response.Code != http.StatusOK {
		t.Fatalf("login GET status = %d body %q", response.Code, response.Body.String())
	}
	token := siteCSRFToken(t, response.Body.String())
	oldCSRF := client.cookieValue(sessionauth.DefaultCSRFCookieName)
	response = client.do(http.MethodPost, "/admin/login/", url.Values{
		"csrfmiddlewaretoken": {token}, "username": {username}, "password": {password}, "next": {next},
	})
	if response.Code != http.StatusFound || response.Header().Get("Location") != next {
		t.Fatalf("login POST = %d location %q body %q", response.Code, response.Header().Get("Location"), response.Body.String())
	}
	return oldCSRF
}

func (client *siteHTTPClient) cookieValue(name string) string {
	if cookie := client.cookies[name]; cookie != nil {
		return cookie.Value
	}
	return ""
}

func (client *siteHTTPClient) detachedCookies() map[string]*http.Cookie {
	result := make(map[string]*http.Cookie, len(client.cookies))
	for name, cookie := range client.cookies {
		clone := *cookie
		result[name] = &clone
	}
	return result
}

func siteServeWithCookies(
	application *web.Application,
	cookies map[string]*http.Cookie,
	method, target string,
	values url.Values,
) *httptest.ResponseRecorder {
	return siteServeWithAdditionalCookies(application, cookies, nil, method, target, values)
}

func siteServeWithAdditionalCookies(
	application *web.Application,
	cookies map[string]*http.Cookie,
	additional []*http.Cookie,
	method, target string,
	values url.Values,
) *httptest.ResponseRecorder {
	var body *strings.Reader
	if values == nil {
		body = strings.NewReader("")
	} else {
		body = strings.NewReader(values.Encode())
	}
	request := httptest.NewRequest(method, "http://example.test"+target, body)
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	for _, cookie := range additional {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	application.ServeHTTP(response, request)
	return response
}

func siteResponseCookie(response *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

func siteCSRFToken(t *testing.T, body string) string {
	t.Helper()
	const marker = `name="csrfmiddlewaretoken" value="`
	start := strings.Index(body, marker)
	if start < 0 {
		t.Fatalf("response contains no CSRF token: %q", body)
	}
	remaining := body[start+len(marker):]
	end := strings.IndexByte(remaining, '"')
	if end < 0 {
		t.Fatalf("response contains malformed CSRF token: %q", body)
	}
	return remaining[:end]
}

func siteSignedNoticeLocation(location, notice, count string) bool {
	parsed, err := url.Parse(location)
	if err != nil || parsed.Path != "/admin/articles/" {
		return false
	}
	query := parsed.Query()
	if query.Get("notice") != notice || query.Get("count") != count || len(query["notice"]) != 1 || len(query["sig"]) != 1 || len(query.Get("sig")) != 43 {
		return false
	}
	wantKeys := 2
	if count != "" {
		wantKeys++
	}
	return len(query) == wantKeys
}
