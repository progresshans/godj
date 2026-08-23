package article_test

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/adminapp"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

const (
	articleAdminBasePath = "/admin"
	articleAdminUsername = "staff"
	articleAdminPassword = "correct horse battery staple"
)

var articleAdminCSRFPattern = regexp.MustCompile(`name="csrfmiddlewaretoken" value="([A-Za-z0-9_-]{128})"`)

func TestArticleAdminSiteSQLiteUserFlow(t *testing.T) {
	runArticleAdminSiteUserFlow(t, newArticleAdminSiteFixture(t))
}

func runArticleAdminSiteUserFlow(t *testing.T, fixture articleAdminSiteFixture) {
	t.Helper()

	// The first protected request preserves only an allowlisted local next URI.
	anonymous := fixture.request(t, http.MethodGet, articleAdminBasePath+"/articles/", nil)
	anonymous.requireStatus(t, http.StatusFound)
	loginLocation := anonymous.header.Get("Location")
	parsedLogin, err := url.Parse(loginLocation)
	if err != nil {
		t.Fatal(err)
	}
	if parsedLogin.Path != articleAdminBasePath+"/login/" || parsedLogin.Query().Get("next") != articleAdminBasePath+"/articles/" {
		t.Fatalf("anonymous redirect = %q", loginLocation)
	}

	login := fixture.request(t, http.MethodGet, loginLocation, nil)
	login.requireStatus(t, http.StatusOK)
	login.requireMarkers(t, `data-admin-view="login"`)
	oversizedPassword := strings.Repeat("x", 1025)
	oversizedLogin := fixture.request(t, http.MethodPost, articleAdminBasePath+"/login/", url.Values{
		"csrfmiddlewaretoken": {login.csrfToken(t)},
		"username":            {articleAdminUsername},
		"password":            {oversizedPassword},
		"next":                {articleAdminBasePath + "/articles/"},
	})
	oversizedLogin.requireStatus(t, http.StatusOK)
	oversizedLogin.requireMarkers(t, `data-login-error="invalid_credentials"`)
	if strings.Contains(oversizedLogin.body, oversizedPassword) {
		t.Fatal("oversized password was reflected in the login response")
	}
	stillAnonymous := fixture.request(t, http.MethodGet, articleAdminBasePath+"/articles/", nil)
	stillAnonymous.requireStatus(t, http.StatusFound)
	loginToken := oversizedLogin.csrfToken(t)
	loginResult := fixture.request(t, http.MethodPost, articleAdminBasePath+"/login/", url.Values{
		"csrfmiddlewaretoken": {loginToken},
		"username":            {articleAdminUsername},
		"password":            {articleAdminPassword},
		"next":                {articleAdminBasePath + "/articles/"},
	})
	loginResult.requireRedirect(t, articleAdminBasePath+"/articles/")

	list := fixture.request(t, http.MethodGet, articleAdminBasePath+"/articles/", nil)
	list.requireStatus(t, http.StatusOK)
	list.requireMarkers(t,
		`data-admin-view="list"`,
		`data-result-count="2"`,
		`data-page="1"`,
		`data-page-count="2"`,
		`data-object-id="1"`,
		`data-action="publish"`,
	)
	list.rejectMarkers(t, `data-object-id="2"`)

	search := fixture.request(t, http.MethodGet, articleAdminBasePath+"/articles/?q=Rust", nil)
	search.requireStatus(t, http.StatusOK)
	search.requireMarkers(t,
		`data-admin-view="list"`,
		`data-result-count="1"`,
		`data-page="1"`,
		`data-page-count="1"`,
		`data-object-id="2"`,
	)
	search.rejectMarkers(t, `data-object-id="1"`)

	secondPage := fixture.request(t, http.MethodGet, articleAdminBasePath+"/articles/?p=2", nil)
	secondPage.requireStatus(t, http.StatusOK)
	secondPage.requireMarkers(t,
		`data-admin-view="list"`,
		`data-result-count="2"`,
		`data-page="2"`,
		`data-page-count="2"`,
		`data-object-id="2"`,
	)
	secondPage.rejectMarkers(t, `data-object-id="1"`)

	// A 201-rune edit is rejected while preserving submitted text. Neither the
	// Article row nor the semantic audit log may change.
	beforeInvalid, found, err := fixture.service.Get(context.Background(), 2)
	if err != nil || !found {
		t.Fatalf("Get(2) before invalid edit = %#v, %t, %v", beforeInvalid, found, err)
	}
	change := fixture.request(t, http.MethodGet, articleAdminBasePath+"/articles/change/?id=2", nil)
	change.requireStatus(t, http.StatusOK)
	tooLongTitle := strings.Repeat("한", 201)
	if got := utf8.RuneCountInString(tooLongTitle); got != 201 {
		t.Fatalf("too-long title rune count = %d", got)
	}
	invalidEdit := fixture.request(t, http.MethodPost, articleAdminBasePath+"/articles/change/?id=2", url.Values{
		"csrfmiddlewaretoken": {change.csrfToken(t)},
		"title":               {tooLongTitle},
		"published":           {"on"},
		"summary":             {"sticky <summary>"},
	})
	invalidEdit.requireStatus(t, http.StatusOK)
	invalidEdit.requireMarkers(t,
		`data-admin-view="form"`,
		`data-field-name="title"`,
		`data-error-field="title" data-error-code="max_length"`,
		`max_length (limit=200, actual=201)`,
		`data-field-name="summary"`,
		`sticky &lt;summary&gt;`,
		tooLongTitle,
	)
	afterInvalid, found, err := fixture.service.Get(context.Background(), 2)
	if err != nil || !found {
		t.Fatalf("Get(2) after invalid edit = %#v, %t, %v", afterInvalid, found, err)
	}
	assertArticleAdminArticleEqual(t, afterInvalid, beforeInvalid)
	if got := fixture.audit.Len(); got != 0 {
		t.Fatalf("audit length after invalid edit = %d, want 0", got)
	}

	// Max length is measured in runes: 200 multibyte runes create successfully.
	multibyteTitle := strings.Repeat("界", 200)
	if got := utf8.RuneCountInString(multibyteTitle); got != 200 || len(multibyteTitle) <= 200 {
		t.Fatalf("multibyte title = %d runes, %d bytes", got, len(multibyteTitle))
	}
	add := fixture.request(t, http.MethodGet, articleAdminBasePath+"/articles/add/", nil)
	add.requireStatus(t, http.StatusOK)
	invalidControl := fixture.request(t, http.MethodPost, articleAdminBasePath+"/articles/add/", url.Values{
		"csrfmiddlewaretoken": {add.csrfToken(t)},
		"title":               {"bad\x01title"},
		"summary":             {"must remain sticky"},
	})
	invalidControl.requireStatus(t, http.StatusOK)
	invalidControl.requireMarkers(t,
		`data-error-field="title" data-error-code="invalid_control_character"`,
		`must remain sticky`,
	)
	if _, found, err := fixture.service.Get(context.Background(), 3); err != nil || found {
		t.Fatalf("invalid-control add created row 3: found=%t error=%v", found, err)
	}
	if got := fixture.audit.Len(); got != 0 {
		t.Fatalf("audit length after invalid-control add = %d, want 0", got)
	}
	addResult := fixture.request(t, http.MethodPost, articleAdminBasePath+"/articles/add/", url.Values{
		"csrfmiddlewaretoken": {invalidControl.csrfToken(t)},
		"title":               {multibyteTitle},
		"summary":             {"created through Admin"},
	})
	addResult.requireNoticeRedirect(t, "added", "")
	created, found, err := fixture.service.Get(context.Background(), 3)
	if err != nil || !found || created.Title != multibyteTitle || created.Published {
		t.Fatalf("created Article = %#v, %t, %v", created, found, err)
	}

	// The same exact limit accepts 200 ASCII runes on update.
	asciiTitle := strings.Repeat("a", 200)
	if got := utf8.RuneCountInString(asciiTitle); got != 200 || len(asciiTitle) != 200 {
		t.Fatalf("ASCII title = %d runes, %d bytes", got, len(asciiTitle))
	}
	createdChange := fixture.request(t, http.MethodGet, articleAdminBasePath+"/articles/change/?id=3", nil)
	createdChange.requireStatus(t, http.StatusOK)
	changeResult := fixture.request(t, http.MethodPost, articleAdminBasePath+"/articles/change/?id=3", url.Values{
		"csrfmiddlewaretoken": {createdChange.csrfToken(t)},
		"title":               {asciiTitle},
		"published":           {"on"},
		"summary":             {"changed through Admin"},
	})
	changeResult.requireNoticeRedirect(t, "changed", "")
	updated, found, err := fixture.service.Get(context.Background(), 3)
	if err != nil || !found || updated.Title != asciiTitle || !updated.Published || updated.Summary == nil || *updated.Summary != "changed through Admin" {
		t.Fatalf("updated Article = %#v, %t, %v", updated, found, err)
	}

	deletePage := fixture.request(t, http.MethodGet, articleAdminBasePath+"/articles/delete/?id=3", nil)
	deletePage.requireStatus(t, http.StatusOK)
	deletePage.requireMarkers(t, `data-admin-view="delete"`)
	deleteResult := fixture.request(t, http.MethodPost, articleAdminBasePath+"/articles/delete/?id=3", url.Values{
		"csrfmiddlewaretoken": {deletePage.csrfToken(t)},
		"confirm":             {"yes"},
	})
	deleteResult.requireNoticeRedirect(t, "deleted", "")
	if deleted, found, err := fixture.service.Get(context.Background(), 3); err != nil || found {
		t.Fatalf("Get(3) after delete = %#v, %t, %v", deleted, found, err)
	}

	history := fixture.request(t, http.MethodGet, articleAdminBasePath+"/articles/history/?id=3", nil)
	history.requireStatus(t, http.StatusOK)
	history.requireMarkers(t,
		`data-admin-view="history"`,
		`data-object-id="3"`,
		`data-sequence="1" data-action="add" data-actor="staff-1"`,
		`data-sequence="2" data-action="change" data-actor="staff-1"`,
		`data-sequence="3" data-action="delete" data-actor="staff-1"`,
	)
	history.requireOrdered(t, `data-sequence="1"`, `data-sequence="2"`, `data-sequence="3"`)

	// Publishing changes only the selected surviving row and records only that
	// selected object. The unselected false row stays false.
	actionResult := fixture.request(t, http.MethodPost, articleAdminBasePath+"/articles/action/publish/", url.Values{
		"csrfmiddlewaretoken": {history.csrfToken(t)},
		"selected":            {"2"},
	})
	actionResult.requireNoticeRedirect(t, "published", "1")
	publishedNotice := fixture.request(t, http.MethodGet, actionResult.header.Get("Location"), nil)
	publishedNotice.requireStatus(t, http.StatusOK)
	publishedNotice.requireMarkers(t, `data-admin-message="published" data-affected="1"`, `1 object(s) published.`)
	selected, selectedFound, err := fixture.service.Get(context.Background(), 2)
	if err != nil || !selectedFound || !selected.Published {
		t.Fatalf("selected Article after publish = %#v, %t, %v", selected, selectedFound, err)
	}
	unselected, unselectedFound, err := fixture.service.Get(context.Background(), 1)
	if err != nil || !unselectedFound || unselected.Published {
		t.Fatalf("unselected Article after publish = %#v, %t, %v", unselected, unselectedFound, err)
	}
	entries := fixture.audit.All()
	if len(entries) != 4 || entries[3].Action != admin.ActionPublish || entries[3].ObjectID != 2 ||
		len(entries[3].ChangedFields) != 1 || entries[3].ChangedFields[0] != "published" {
		t.Fatalf("audit entries after publish = %#v", entries)
	}

	logoutResult := fixture.request(t, http.MethodPost, articleAdminBasePath+"/logout/", url.Values{
		"csrfmiddlewaretoken": {publishedNotice.csrfToken(t)},
	})
	logoutResult.requireRedirect(t, articleAdminBasePath+"/login/")
	afterLogout := fixture.request(t, http.MethodGet, articleAdminBasePath+"/articles/", nil)
	afterLogout.requireStatus(t, http.StatusFound)
	afterLogoutURL, err := url.Parse(afterLogout.header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if afterLogoutURL.Path != articleAdminBasePath+"/login/" || afterLogoutURL.Query().Get("next") != articleAdminBasePath+"/articles/" {
		t.Fatalf("post-logout redirect = %q", afterLogout.header.Get("Location"))
	}
}

type articleAdminSiteFixture struct {
	server  *httptest.Server
	client  *http.Client
	service adminapp.Service
	audit   *admin.AuditLog
}

func newArticleAdminSiteFixture(t *testing.T) articleAdminSiteFixture {
	t.Helper()
	ctx := context.Background()
	backend, err := sqlite.OpenMemory(ctx, "article-admin-site-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	for _, statement := range []string{
		`CREATE TABLE "godj_conformance_article" (
  "id" INTEGER NOT NULL PRIMARY KEY,
  "title" VARCHAR(200) NOT NULL,
  "published" BOOLEAN NOT NULL,
  "summary" VARCHAR(200) NULL
)`,
		`INSERT INTO "godj_conformance_article" ("id", "title", "published", "summary") VALUES
  (1, 'Go Alpha', FALSE, NULL),
  (2, 'Rust Beta', FALSE, 'Second row')`,
	} {
		if _, err := backend.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}

	return newArticleAdminSiteFixtureWithBackend(t, backend)
}

func newArticleAdminSiteFixtureWithBackend(t *testing.T, backend adminapp.Backend) articleAdminSiteFixture {
	t.Helper()
	ctx := context.Background()
	audit, err := admin.NewAuditLog(64)
	if err != nil {
		t.Fatal(err)
	}
	service, err := adminapp.NewService(backend, audit)
	if err != nil {
		t.Fatal(err)
	}
	projectSettings, err := settings.New(settings.Definition{
		ProjectName: "article_admin_integration",
		InstalledApps: []apps.Config{{
			Name:  "github.com/progresshans/godj/examples/article/models",
			Label: "godj_conformance",
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

	store, err := sessions.NewMemoryStore(16)
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
	encodedPassword, err := hasher.Hash(ctx, articleAdminPassword)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{
		ID:     "staff-1",
		Active: true,
		Permissions: []auth.Permission{
			admin.DefaultAccessPermission,
			adminapp.ArticleViewPermission,
			adminapp.ArticleAddPermission,
			adminapp.ArticleChangePermission,
			adminapp.ArticleDeletePermission,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := auth.NewCredential(articleAdminUsername, encodedPassword, principal)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := auth.NewMemoryAuthenticator([]auth.Credential{credential}, hasher)
	if err != nil {
		t.Fatal(err)
	}
	allowedNext, err := admin.SiteAllowedNextPaths(registry, articleAdminBasePath)
	if err != nil {
		t.Fatal(err)
	}
	authRuntime, err := sessionauth.New(sessionauth.Config{
		Sessions:         manager,
		Authenticator:    authenticator,
		Authorizer:       auth.PrincipalAuthorizer{},
		SessionCookie:    sessionauth.CookieConfig{Path: "/", AllowInsecure: true},
		CSRFCookie:       sessionauth.CookieConfig{Path: "/", AllowInsecure: true},
		LoginPath:        articleAdminBasePath + "/login/",
		FallbackPath:     articleAdminBasePath + "/",
		AllowedNextPaths: allowedNext,
	})
	if err != nil {
		t.Fatal(err)
	}
	site, err := admin.NewSite(admin.SiteConfig{
		Apps:      projectSettings.Apps(),
		Namespace: "godj_conformance",
		BasePath:  articleAdminBasePath,
		Registry:  registry,
		Auth:      authRuntime,
		PageSize:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	application, err := web.NewApplication(web.Config{
		Settings: projectSettings,
		Routes:   site.Routes(),
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(application)
	t.Cleanup(server.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Jar:     jar,
		Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return articleAdminSiteFixture{server: server, client: client, service: service, audit: audit}
}

type articleAdminHTTPResult struct {
	status int
	header http.Header
	body   string
}

func (fixture articleAdminSiteFixture) request(t *testing.T, method, requestURI string, values url.Values) articleAdminHTTPResult {
	t.Helper()
	var body io.Reader
	if values != nil {
		body = strings.NewReader(values.Encode())
	}
	request, err := http.NewRequest(method, fixture.server.URL+requestURI, body)
	if err != nil {
		t.Fatal(err)
	}
	if values != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	response, err := fixture.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return articleAdminHTTPResult{status: response.StatusCode, header: response.Header.Clone(), body: string(payload)}
}

func (result articleAdminHTTPResult) requireStatus(t *testing.T, want int) {
	t.Helper()
	if result.status != want {
		t.Fatalf("status = %d, want %d; body=%q", result.status, want, result.body)
	}
	if want == http.StatusOK && result.header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q", result.header.Get("Content-Type"))
	}
}

func (result articleAdminHTTPResult) requireRedirect(t *testing.T, location string) {
	t.Helper()
	result.requireStatus(t, http.StatusFound)
	if got := result.header.Get("Location"); got != location {
		t.Fatalf("Location = %q, want %q", got, location)
	}
}

func (result articleAdminHTTPResult) requireNoticeRedirect(t *testing.T, notice, count string) {
	t.Helper()
	result.requireStatus(t, http.StatusFound)
	location := result.header.Get("Location")
	parsed, err := url.Parse(location)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if parsed.Path != articleAdminBasePath+"/articles/" || query.Get("notice") != notice || query.Get("count") != count || query.Get("sig") == "" {
		t.Fatalf("signed notice redirect = %q, want notice=%q count=%q", location, notice, count)
	}
}

func (result articleAdminHTTPResult) requireMarkers(t *testing.T, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if !strings.Contains(result.body, marker) {
			t.Fatalf("semantic marker %q missing from body %q", marker, result.body)
		}
	}
}

func (result articleAdminHTTPResult) rejectMarkers(t *testing.T, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if strings.Contains(result.body, marker) {
			t.Fatalf("unexpected semantic marker %q in body", marker)
		}
	}
}

func (result articleAdminHTTPResult) requireOrdered(t *testing.T, markers ...string) {
	t.Helper()
	position := -1
	for _, marker := range markers {
		next := strings.Index(result.body, marker)
		if next < 0 || next <= position {
			t.Fatalf("marker %q is missing or out of order", marker)
		}
		position = next
	}
}

func (result articleAdminHTTPResult) csrfToken(t *testing.T) string {
	t.Helper()
	match := articleAdminCSRFPattern.FindStringSubmatch(result.body)
	if len(match) != 2 {
		t.Fatal("CSRF form token is missing")
	}
	return match[1]
}

func assertArticleAdminArticleEqual(t *testing.T, got, want adminapp.Article) {
	t.Helper()
	if got.ID != want.ID || got.Title != want.Title || got.Published != want.Published || !articleAdminOptionalStringEqual(got.Summary, want.Summary) {
		t.Fatalf("Article = %#v, want %#v", got, want)
	}
}

func articleAdminOptionalStringEqual(left, right *string) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}
