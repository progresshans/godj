package article_test

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/api"
	apisessionauth "github.com/progresshans/godj/api/sessionauth"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/adminapp"
	"github.com/progresshans/godj/examples/article/apiapp"
	"github.com/progresshans/godj/examples/article/articleapp"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

const (
	articleAPIViewerUsername = "viewer"
	articleAPIViewerPassword = "view-only password"
)

func TestArticleAPIAdminSessionSQLiteUserFlow(t *testing.T) {
	ctx := context.Background()
	backend, err := sqlite.OpenMemory(ctx, "article-api-e2e-"+strings.ReplaceAll(t.Name(), "/", "_"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("close Article API SQLite backend: %v", err)
		}
	})
	if _, err := backend.ExecContext(ctx, `CREATE TABLE "godj_conformance_article" (
  "id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
  "title" VARCHAR(200) NOT NULL,
  "published" BOOLEAN NOT NULL,
  "summary" VARCHAR(200) NULL
)`); err != nil {
		t.Fatal(err)
	}

	runArticleAPIAdminSessionUserFlow(t, backend)
}

func runArticleAPIAdminSessionUserFlow(t *testing.T, backend articleapp.Backend) {
	t.Helper()
	fixture := newArticleAPIAdminSessionFixture(t, backend)
	seedArticleAPIFlow(t, fixture.repository)

	if got := articleAPIArticleCount(t, fixture.repository); got != 5 {
		t.Fatalf("initial Article count = %d, want 5", got)
	}
	anonymous := fixture.request(t, fixture.client, http.MethodGet, apiapp.ListPath, "", "", "")
	anonymous.requireJSON(t, http.StatusForbidden, `{"code":"not_authenticated","errors":[]}`)
	if anonymous.header.Get("Location") != "" || anonymous.header.Get("WWW-Authenticate") != "" {
		t.Fatal("anonymous API denial exposed browser-auth headers")
	}
	if got := articleAPIArticleCount(t, fixture.repository); got != 5 {
		t.Fatalf("anonymous denial changed Article count to %d", got)
	}

	login := fixture.login(t, fixture.client, articleAdminUsername, articleAdminPassword)
	if login.sessionCookie == "" || login.csrfCookie == "" || login.preLoginCSRF == "" {
		t.Fatal("Admin login did not publish complete session and CSRF state")
	}

	// The login response rotated the CSRF secret. Replaying the masked token
	// rendered before login must fail before parsing or persistence.
	beforeReplay := articleAPIArticleCount(t, fixture.repository)
	replay := fixture.request(
		t,
		fixture.client,
		http.MethodPost,
		apiapp.ListPath,
		api.JSONContentType,
		`{"title":"must not exist"}`,
		login.preLoginCSRF,
	)
	replay.requireJSON(t, http.StatusForbidden, `{"code":"csrf_rejected","errors":[]}`)
	if replay.header.Get("Location") != "" || replay.header.Get("WWW-Authenticate") != "" {
		t.Fatal("CSRF denial exposed browser-auth headers")
	}
	if got := articleAPIArticleCount(t, fixture.repository); got != beforeReplay {
		t.Fatalf("pre-login CSRF replay changed Article count from %d to %d", beforeReplay, got)
	}

	filtered := fixture.request(
		t,
		fixture.client,
		http.MethodGet,
		apiapp.ListPath+"?search=go&published=true&ordering=-id&page=1",
		"",
		"",
		"",
	)
	filtered.requireJSON(t, http.StatusOK, `{"count":3,"next":"/api/articles/?search=go&published=true&ordering=-id&page=2","previous":null,"results":[{"id":5,"title":"Go Omega","published":true,"summary":"fifth"},{"id":3,"title":"Go Gamma","published":true,"summary":"third"}]}`)
	freshCSRF := filtered.header.Get(websessionauth.DefaultCSRFHeader)
	if len(freshCSRF) != 128 || freshCSRF == login.preLoginCSRF || freshCSRF == login.csrfCookie {
		t.Fatalf("fresh API CSRF token has unsafe shape or reused pre-login/raw bytes: length=%d", len(freshCSRF))
	}
	if got := articleAPICookieValue(t, fixture.client.Jar, fixture.server.URL, websessionauth.DefaultCSRFCookieName); got != login.csrfCookie {
		t.Fatal("safe API request unexpectedly replaced the rotated CSRF cookie")
	}
	if got := articleAPICookieValue(t, fixture.client.Jar, fixture.server.URL, websessionauth.DefaultSessionCookieName); got != login.sessionCookie {
		t.Fatal("safe API request unexpectedly replaced the rotated session cookie")
	}

	secondPage := fixture.request(
		t,
		fixture.client,
		http.MethodGet,
		apiapp.ListPath+"?search=go&published=true&ordering=-id&page=2",
		"",
		"",
		"",
	)
	secondPage.requireJSON(t, http.StatusOK, `{"count":3,"next":null,"previous":"/api/articles/?search=go&published=true&ordering=-id","results":[{"id":1,"title":"Go Alpha","published":true,"summary":null}]}`)

	created := fixture.request(
		t,
		fixture.client,
		http.MethodPost,
		apiapp.ListPath,
		api.JSONContentType,
		`{"title":"  API Created  ","published":true,"summary":"from API"}`,
		freshCSRF,
	)
	created.requireJSON(t, http.StatusCreated, `{"id":6,"title":"API Created","published":true,"summary":"from API"}`)
	if got := created.header.Get("Location"); got != "/api/articles/6/" || strings.Contains(got, fixture.server.URL) {
		t.Fatalf("create Location = %q", got)
	}
	if got := articleAPIArticleCount(t, fixture.repository); got != 6 {
		t.Fatalf("valid create Article count = %d, want 6", got)
	}
	articleAPIRequireArticle(t, fixture.repository, articleapp.Article{
		ID:        6,
		Title:     "API Created",
		Published: true,
		Summary:   articleAPIString("from API"),
	})

	detail := fixture.request(t, fixture.client, http.MethodGet, "/api/articles/6/", "", "", "")
	detail.requireJSON(t, http.StatusOK, `{"id":6,"title":"API Created","published":true,"summary":"from API"}`)

	beforeInvalidPUT := articleAPIGet(t, fixture.repository, 6)
	invalidPUT := fixture.request(
		t,
		fixture.client,
		http.MethodPut,
		"/api/articles/6/",
		api.JSONContentType,
		`{"published":false,"summary":null}`,
		freshCSRF,
	)
	invalidPUT.requireJSON(t, http.StatusBadRequest, `{"code":"validation_error","errors":[{"field":"title","code":"required","params":[]}]}`)
	articleAPIRequireArticle(t, fixture.repository, beforeInvalidPUT)

	validPUT := fixture.request(
		t,
		fixture.client,
		http.MethodPut,
		"/api/articles/6/",
		api.JSONContentType,
		`{"title":"Replaced","summary":null}`,
		freshCSRF,
	)
	validPUT.requireJSON(t, http.StatusOK, `{"id":6,"title":"Replaced","published":false,"summary":null}`)
	articleAPIRequireArticle(t, fixture.repository, articleapp.Article{ID: 6, Title: "Replaced"})

	beforeInvalidPatch := articleAPIGet(t, fixture.repository, 6)
	invalidPatch := fixture.request(
		t,
		fixture.client,
		http.MethodPatch,
		"/api/articles/6/",
		api.JSONContentType,
		`{"title":null}`,
		freshCSRF,
	)
	invalidPatch.requireJSON(t, http.StatusBadRequest, `{"code":"validation_error","errors":[{"field":"title","code":"null","params":[]}]}`)
	articleAPIRequireArticle(t, fixture.repository, beforeInvalidPatch)

	validPatch := fixture.request(
		t,
		fixture.client,
		http.MethodPatch,
		"/api/articles/6/",
		api.JSONContentType,
		`{"published":true,"summary":""}`,
		freshCSRF,
	)
	validPatch.requireJSON(t, http.StatusOK, `{"id":6,"title":"Replaced","published":true,"summary":""}`)
	articleAPIRequireArticle(t, fixture.repository, articleapp.Article{
		ID:        6,
		Title:     "Replaced",
		Published: true,
		Summary:   articleAPIString(""),
	})

	// A second real browser session can authenticate through Admin and view the
	// API, but its narrower principal must not gain delete permission.
	viewer := fixture.newClient(t)
	fixture.login(t, viewer, articleAPIViewerUsername, articleAPIViewerPassword)
	viewerSafe := fixture.request(t, viewer, http.MethodGet, "/api/articles/6/", "", "", "")
	viewerSafe.requireJSON(t, http.StatusOK, `{"id":6,"title":"Replaced","published":true,"summary":""}`)
	viewerCSRF := viewerSafe.header.Get(websessionauth.DefaultCSRFHeader)
	if len(viewerCSRF) != 128 {
		t.Fatalf("viewer fresh CSRF token length = %d", len(viewerCSRF))
	}
	beforePermissionDenied := articleAPIGet(t, fixture.repository, 6)
	permissionDenied := fixture.request(t, viewer, http.MethodDelete, "/api/articles/6/", "", "", viewerCSRF)
	permissionDenied.requireJSON(t, http.StatusForbidden, `{"code":"permission_denied","errors":[]}`)
	if permissionDenied.header.Get("Location") != "" || permissionDenied.header.Get("WWW-Authenticate") != "" {
		t.Fatal("permission denial exposed browser-auth headers")
	}
	articleAPIRequireArticle(t, fixture.repository, beforePermissionDenied)
	if got := articleAPIArticleCount(t, fixture.repository); got != 6 {
		t.Fatalf("permission denial changed Article count to %d", got)
	}

	deleted := fixture.request(t, fixture.client, http.MethodDelete, "/api/articles/6/", "", "", freshCSRF)
	deleted.requireNoContent(t)
	if _, found, err := fixture.repository.Get(context.Background(), 6); err != nil || found {
		t.Fatalf("Article 6 after delete: found=%t error=%v", found, err)
	}
	if got := articleAPIArticleCount(t, fixture.repository); got != 5 {
		t.Fatalf("valid delete Article count = %d, want 5", got)
	}
	repeated := fixture.request(t, fixture.client, http.MethodDelete, "/api/articles/6/", "", "", freshCSRF)
	repeated.requireJSON(t, http.StatusNotFound, `{"code":"not_found","errors":[]}`)
	if got := articleAPIArticleCount(t, fixture.repository); got != 5 {
		t.Fatalf("repeated delete changed Article count to %d", got)
	}
}

type articleAPIAdminSessionFixture struct {
	server     *httptest.Server
	client     *http.Client
	repository articleapp.Repository
	sessions   *sessions.Manager
}

func newArticleAPIAdminSessionFixture(t *testing.T, backend articleapp.Backend) articleAPIAdminSessionFixture {
	t.Helper()
	ctx := context.Background()
	repository, err := articleapp.NewRepository(backend)
	if err != nil {
		t.Fatal(err)
	}
	audit, err := admin.NewAuditLog(64)
	if err != nil {
		t.Fatal(err)
	}
	service, err := adminapp.NewService(backend, audit)
	if err != nil {
		t.Fatal(err)
	}
	projectSettings, err := settings.New(settings.Definition{
		ProjectName: "article_api_e2e",
		InstalledApps: []apps.Config{{
			Name:  "github.com/progresshans/godj/examples/article/models",
			Label: apiapp.Namespace,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	builder := admin.NewBuilder(projectSettings.Apps())
	if err := adminapp.RegisterArticle(builder, service); err != nil {
		t.Fatal(err)
	}
	registry, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}

	store, err := sessions.NewMemoryStore(32)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := sessions.NewManager(store, sessions.Config{})
	if err != nil {
		t.Fatal(err)
	}
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	fullPrincipal := articleAPIPrincipal(t, "staff-1", true)
	viewerPrincipal := articleAPIPrincipal(t, "viewer-1", false)
	credentials := []auth.Credential{
		articleAPICredential(t, ctx, hasher, articleAdminUsername, articleAdminPassword, fullPrincipal),
		articleAPICredential(t, ctx, hasher, articleAPIViewerUsername, articleAPIViewerPassword, viewerPrincipal),
	}
	authenticator, err := auth.NewMemoryAuthenticator(credentials, hasher)
	if err != nil {
		t.Fatal(err)
	}
	allowedNext, err := admin.SiteAllowedNextPaths(registry, articleAdminBasePath)
	if err != nil {
		t.Fatal(err)
	}
	webRuntime, err := websessionauth.New(websessionauth.Config{
		Sessions:         manager,
		Authenticator:    authenticator,
		Authorizer:       auth.PrincipalAuthorizer{},
		SessionCookie:    websessionauth.CookieConfig{Path: "/", AllowInsecure: true},
		CSRFCookie:       websessionauth.CookieConfig{Path: "/", AllowInsecure: true},
		LoginPath:        articleAdminBasePath + "/login/",
		FallbackPath:     articleAdminBasePath + "/",
		AllowedNextPaths: allowedNext,
	})
	if err != nil {
		t.Fatal(err)
	}
	adminSite, err := admin.NewSite(admin.SiteConfig{
		Apps:      projectSettings.Apps(),
		Namespace: apiapp.Namespace,
		BasePath:  articleAdminBasePath,
		Registry:  registry,
		Auth:      webRuntime,
		PageSize:  2,
	})
	if err != nil {
		t.Fatal(err)
	}
	apiRuntime, err := apisessionauth.New(webRuntime)
	if err != nil {
		t.Fatal(err)
	}
	articleAPI, err := apiapp.New(backend, apiRuntime)
	if err != nil {
		t.Fatal(err)
	}
	middleware, err := apiapp.Middleware()
	if err != nil {
		t.Fatal(err)
	}
	routes := append(adminSite.Routes(), articleAPI.Routes()...)
	application, err := web.NewApplication(web.Config{
		Settings:   projectSettings,
		Routes:     routes,
		Middleware: middleware,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(application)
	t.Cleanup(server.Close)
	fixture := articleAPIAdminSessionFixture{server: server, repository: repository, sessions: manager}
	fixture.client = fixture.newClient(t)
	return fixture
}

func articleAPIPrincipal(t *testing.T, id string, full bool) auth.Principal {
	t.Helper()
	permissions := []auth.Permission{admin.DefaultAccessPermission, articleapp.ArticleViewPermission}
	if full {
		permissions = append(permissions,
			articleapp.ArticleAddPermission,
			articleapp.ArticleChangePermission,
			articleapp.ArticleDeletePermission,
		)
	}
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: id, Active: true, Permissions: permissions})
	if err != nil {
		t.Fatal(err)
	}
	return principal
}

func articleAPICredential(
	t *testing.T,
	ctx context.Context,
	hasher auth.PasswordHasher,
	username string,
	password string,
	principal auth.Principal,
) auth.Credential {
	t.Helper()
	encoded, err := hasher.Hash(ctx, password)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := auth.NewCredential(username, encoded, principal)
	if err != nil {
		t.Fatal(err)
	}
	return credential
}

func (fixture articleAPIAdminSessionFixture) newClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{
		Jar:     jar,
		Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

type articleAPILoginState struct {
	preLoginCSRF  string
	sessionCookie string
	csrfCookie    string
}

func (fixture articleAPIAdminSessionFixture) login(
	t *testing.T,
	client *http.Client,
	username string,
	password string,
) articleAPILoginState {
	t.Helper()
	preLoginSession, err := fixture.sessions.Create(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	serverURL, err := url.Parse(fixture.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client.Jar.SetCookies(serverURL, []*http.Cookie{{
		Name:  websessionauth.DefaultSessionCookieName,
		Value: preLoginSession.ID().Encoded(),
		Path:  "/",
	}})
	loginPage := fixture.request(t, client, http.MethodGet, articleAdminBasePath+"/login/", "", "", "")
	if loginPage.status != http.StatusOK || loginPage.header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("Admin login page = status %d content-type %q body %q", loginPage.status, loginPage.header.Get("Content-Type"), loginPage.body)
	}
	preLoginToken := loginPage.adminCSRFToken(t)
	preLoginResponseCookie := loginPage.namedCookie(t, websessionauth.DefaultCSRFCookieName)
	preLoginCookie := articleAPICookieValue(t, client.Jar, fixture.server.URL, websessionauth.DefaultCSRFCookieName)
	if !preLoginResponseCookie.HttpOnly || preLoginResponseCookie.Value == "" ||
		preLoginCookie != preLoginResponseCookie.Value || preLoginCookie == preLoginToken {
		t.Fatal("Admin login page did not publish an independent HttpOnly CSRF secret")
	}
	values := url.Values{
		"csrfmiddlewaretoken": {preLoginToken},
		"username":            {username},
		"password":            {password},
		"next":                {articleAdminBasePath + "/articles/"},
	}
	result := fixture.request(
		t,
		client,
		http.MethodPost,
		articleAdminBasePath+"/login/",
		"application/x-www-form-urlencoded",
		values.Encode(),
		"",
	)
	if result.status != http.StatusFound || result.header.Get("Location") != articleAdminBasePath+"/articles/" {
		t.Fatalf("Admin login = status %d location %q body %q", result.status, result.header.Get("Location"), result.body)
	}
	sessionCookie := result.namedCookie(t, websessionauth.DefaultSessionCookieName)
	csrfCookie := result.namedCookie(t, websessionauth.DefaultCSRFCookieName)
	if !sessionCookie.HttpOnly || sessionCookie.Value == "" || sessionCookie.Value == preLoginSession.ID().Encoded() ||
		!csrfCookie.HttpOnly || csrfCookie.Value == "" || csrfCookie.Value == preLoginCookie {
		t.Fatal("Admin login did not rotate the session and CSRF cookies")
	}
	if _, found, err := fixture.sessions.Load(context.Background(), preLoginSession.ID()); err != nil || found {
		t.Fatalf("pre-login server session survived rotation: found=%t error=%v", found, err)
	}
	if got := articleAPICookieValue(t, client.Jar, fixture.server.URL, websessionauth.DefaultSessionCookieName); got != sessionCookie.Value {
		t.Fatal("browser session cookie was not updated to the login response")
	}
	if got := articleAPICookieValue(t, client.Jar, fixture.server.URL, websessionauth.DefaultCSRFCookieName); got != csrfCookie.Value {
		t.Fatal("browser CSRF cookie was not updated to the rotated login response")
	}
	return articleAPILoginState{
		preLoginCSRF:  preLoginToken,
		sessionCookie: sessionCookie.Value,
		csrfCookie:    csrfCookie.Value,
	}
}

type articleAPIHTTPResult struct {
	status  int
	header  http.Header
	body    string
	cookies []*http.Cookie
}

func (fixture articleAPIAdminSessionFixture) request(
	t *testing.T,
	client *http.Client,
	method string,
	target string,
	contentType string,
	body string,
	csrf string,
) articleAPIHTTPResult {
	t.Helper()
	request, err := http.NewRequest(method, fixture.server.URL+target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(target, "/api/") {
		request.Header.Set("Accept", api.JSONContentType)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if csrf != "" {
		request.Header.Set(websessionauth.DefaultCSRFHeader, csrf)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return articleAPIHTTPResult{
		status:  response.StatusCode,
		header:  response.Header.Clone(),
		body:    string(payload),
		cookies: response.Cookies(),
	}
}

func (result articleAPIHTTPResult) requireJSON(t *testing.T, status int, body string) {
	t.Helper()
	if result.status != status || result.header.Get("Content-Type") != api.JSONContentType || result.body != body {
		t.Fatalf(
			"API response = status %d content-type %q body %q, want %d %q %q",
			result.status,
			result.header.Get("Content-Type"),
			result.body,
			status,
			api.JSONContentType,
			body,
		)
	}
}

func (result articleAPIHTTPResult) requireNoContent(t *testing.T) {
	t.Helper()
	if result.status != http.StatusNoContent || result.header.Get("Content-Type") != "" || result.body != "" {
		t.Fatalf("no-content response = status %d content-type %q body %q", result.status, result.header.Get("Content-Type"), result.body)
	}
}

func (result articleAPIHTTPResult) adminCSRFToken(t *testing.T) string {
	t.Helper()
	match := articleAdminCSRFPattern.FindStringSubmatch(result.body)
	if len(match) != 2 {
		t.Fatal("Admin CSRF form token is missing")
	}
	return match[1]
}

func (result articleAPIHTTPResult) namedCookie(t *testing.T, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range result.cookies {
		if cookie != nil && cookie.Name == name {
			clone := *cookie
			return &clone
		}
	}
	t.Fatalf("response cookie %q is missing", name)
	return nil
}

func articleAPICookieValue(t *testing.T, jar http.CookieJar, rawURL string, name string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	for _, cookie := range jar.Cookies(parsed) {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

func seedArticleAPIFlow(t *testing.T, repository articleapp.Repository) {
	t.Helper()
	inputs := []articleapp.Input{
		{Title: "Go Alpha", Published: true},
		{Title: "Rust Beta", Summary: articleAPIString("second")},
		{Title: "Go Gamma", Published: true, Summary: articleAPIString("third")},
		{Title: "Go Draft", Summary: articleAPIString("fourth")},
		{Title: "Go Omega", Published: true, Summary: articleAPIString("fifth")},
	}
	for index, input := range inputs {
		created, err := repository.Create(context.Background(), input)
		if err != nil {
			t.Fatalf("seed Article %d: %v", index+1, err)
		}
		if created.ID != int64(index+1) {
			t.Fatalf("seed Article ID = %d, want %d", created.ID, index+1)
		}
	}
}

func articleAPIGet(t *testing.T, repository articleapp.Repository, id int64) articleapp.Article {
	t.Helper()
	article, found, err := repository.Get(context.Background(), id)
	if err != nil || !found {
		t.Fatalf("get Article %d: found=%t error=%v", id, found, err)
	}
	return article
}

func articleAPIRequireArticle(t *testing.T, repository articleapp.Repository, want articleapp.Article) {
	t.Helper()
	got := articleAPIGet(t, repository, want.ID)
	if got.ID != want.ID || got.Title != want.Title || got.Published != want.Published || !articleAPIStringPointersEqual(got.Summary, want.Summary) {
		t.Fatalf("Article %d = %#v, want %#v", want.ID, got, want)
	}
}

func articleAPIArticleCount(t *testing.T, repository articleapp.Repository) int64 {
	t.Helper()
	page, err := repository.List(context.Background(), articleapp.ListOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	return page.Total
}

func articleAPIString(value string) *string { return &value }

func articleAPIStringPointersEqual(left *string, right *string) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}
