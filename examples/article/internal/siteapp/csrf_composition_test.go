package siteapp

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/articleapp"
	"github.com/progresshans/godj/migrations"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/systemstate"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

var siteAppCSRFTokenPattern = regexp.MustCompile(`name="csrfmiddlewaretoken" value="([A-Za-z0-9_-]{128})"`)

func TestSharedCSRFKeyRingComposesAcrossTwoArticleSiteRuntimes(t *testing.T) {
	ring, err := websessionauth.NewCSRFKeyRing(bytes.Repeat([]byte{0x41}, 32))
	if err != nil {
		t.Fatalf("NewCSRFKeyRing(): %v", err)
	}
	fixture := newSiteAppCSRFFixture(t, ring)
	fixture.login(t)

	apiToken := fixture.apiToken(t, fixture.firstURL)
	created := fixture.request(
		t,
		http.MethodPost,
		fixture.secondURL+"/api/articles/",
		api.JSONContentType,
		`{"title":"Shared API key ring","published":true}`,
		apiToken,
	)
	if created.status != http.StatusCreated || !strings.Contains(created.body, `"title":"Shared API key ring"`) {
		t.Fatalf("cross-Runtime API create = status %d body %q", created.status, created.body)
	}

	adminPage := fixture.request(t, http.MethodGet, fixture.firstURL+"/admin/articles/add/", "", "", "")
	adminToken := siteAppCSRFTokenPattern.FindStringSubmatch(adminPage.body)
	if adminPage.status != http.StatusOK || len(adminToken) != 2 {
		t.Fatalf("first Runtime Admin add page = status %d token-parts %d body-bytes %d", adminPage.status, len(adminToken), len(adminPage.body))
	}
	values := url.Values{
		"csrfmiddlewaretoken": {adminToken[1]},
		"title":               {"Shared Admin key ring"},
		"summary":             {"verified by the other Runtime"},
	}
	adminCreated := fixture.request(
		t,
		http.MethodPost,
		fixture.secondURL+"/admin/articles/add/",
		"application/x-www-form-urlencoded",
		values.Encode(),
		"",
	)
	if adminCreated.status != http.StatusFound || !strings.HasPrefix(adminCreated.header.Get("Location"), "/admin/articles/") {
		t.Fatalf(
			"cross-Runtime Admin create = status %d location %q body-bytes %d",
			adminCreated.status,
			adminCreated.header.Get("Location"),
			len(adminCreated.body),
		)
	}
	fixture.assertArticles(t, []string{"Shared API key ring", "Shared Admin key ring"})
}

func TestZeroCSRFKeyRingKeepsArticleSiteKeysProcessLocal(t *testing.T) {
	fixture := newSiteAppCSRFFixture(t, websessionauth.CSRFKeyRing{})
	fixture.login(t)
	token := fixture.apiToken(t, fixture.firstURL)

	rejected := fixture.request(
		t,
		http.MethodPost,
		fixture.secondURL+"/api/articles/",
		api.JSONContentType,
		`{"title":"Rejected by other Runtime"}`,
		token,
	)
	if rejected.status != http.StatusForbidden || rejected.body != `{"code":"csrf_rejected","errors":[]}` {
		t.Fatalf("zero-ring cross-Runtime API create = status %d body %q", rejected.status, rejected.body)
	}
	fixture.assertArticles(t, nil)

	accepted := fixture.request(
		t,
		http.MethodPost,
		fixture.firstURL+"/api/articles/",
		api.JSONContentType,
		`{"title":"Accepted by issuing Runtime"}`,
		token,
	)
	if accepted.status != http.StatusCreated {
		t.Fatalf("zero-ring issuing-Runtime API create = status %d body %q", accepted.status, accepted.body)
	}
	fixture.assertArticles(t, []string{"Accepted by issuing Runtime"})
}

type siteAppCSRFFixture struct {
	client       *http.Client
	firstURL     string
	secondURL    string
	firstBackend *sqlite.Backend
	username     string
	password     string
}

func newSiteAppCSRFFixture(t *testing.T, ring websessionauth.CSRFKeyRing) siteAppCSRFFixture {
	t.Helper()
	// A full repository race run schedules several CPU-intensive password
	// profile tests at once. Keep this fixture bounded while allowing both
	// Runtime startup verifications to finish under that instrumented load.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	databasePath := filepath.Join(t.TempDir(), "siteapp-csrf.sqlite3")
	dataSourceName := "file:" + filepath.ToSlash(databasePath) + "?mode=rwc&_busy_timeout=5000"
	firstBackend := openSiteAppCSRFBackend(t, ctx, dataSourceName)
	secondBackend := openSiteAppCSRFBackend(t, ctx, dataSourceName)
	migrateSiteAppCSRFSchema(t, ctx, firstBackend)

	const (
		username = "csrf-composition-admin"
		password = "csrf-composition-password-marker"
	)
	firstConfig := NewConfig(firstBackend, username, password)
	secondConfig := NewConfig(secondBackend, username, password)
	if ring.Valid() {
		firstConfig = firstConfig.WithCSRFKeyRing(ring)
		secondConfig = secondConfig.WithCSRFKeyRing(ring)
	}
	first, err := New(ctx, firstConfig)
	if err != nil {
		t.Fatalf("New(first Article site): %v", err)
	}
	second, err := New(ctx, secondConfig)
	if err != nil {
		t.Fatalf("New(second Article site): %v", err)
	}
	firstServer := newSiteAppTestServer(t, first)
	secondServer := newSiteAppTestServer(t, second)
	firstAddress, err := url.Parse(firstServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	secondAddress, err := url.Parse(secondServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	if firstAddress.Hostname() != secondAddress.Hostname() {
		t.Fatalf("test servers use different cookie hosts: %q/%q", firstAddress.Hostname(), secondAddress.Hostname())
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return siteAppCSRFFixture{
		client: &http.Client{
			Jar:     jar,
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		firstURL:     firstServer.URL,
		secondURL:    secondServer.URL,
		firstBackend: firstBackend,
		username:     username,
		password:     password,
	}
}

func (fixture siteAppCSRFFixture) login(t *testing.T) {
	t.Helper()
	page := fixture.request(t, http.MethodGet, fixture.firstURL+"/admin/login/", "", "", "")
	token := siteAppCSRFTokenPattern.FindStringSubmatch(page.body)
	if page.status != http.StatusOK || len(token) != 2 {
		t.Fatalf("first Runtime login page = status %d token-parts %d body-bytes %d", page.status, len(token), len(page.body))
	}
	values := url.Values{
		"csrfmiddlewaretoken": {token[1]},
		"username":            {fixture.username},
		"password":            {fixture.password},
		"next":                {"/admin/articles/"},
	}
	response := fixture.request(
		t,
		http.MethodPost,
		fixture.firstURL+"/admin/login/",
		"application/x-www-form-urlencoded",
		values.Encode(),
		"",
	)
	if response.status != http.StatusFound || response.header.Get("Location") != "/admin/articles/" {
		t.Fatalf("first Runtime login = status %d location %q body-bytes %d", response.status, response.header.Get("Location"), len(response.body))
	}
}

func (fixture siteAppCSRFFixture) apiToken(t *testing.T, baseURL string) string {
	t.Helper()
	response := fixture.request(t, http.MethodGet, baseURL+"/api/articles/", "", "", "")
	token := response.header.Get(websessionauth.DefaultCSRFHeader)
	if response.status != http.StatusOK || len(token) != 128 {
		t.Fatalf("authenticated API token = status %d token-length %d body-bytes %d", response.status, len(token), len(response.body))
	}
	return token
}

type siteAppHTTPResult struct {
	status int
	header http.Header
	body   string
}

func (fixture siteAppCSRFFixture) request(
	t *testing.T,
	method, target, contentType, body, csrf string,
) siteAppHTTPResult {
	t.Helper()
	request, err := http.NewRequest(method, target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(target, fixture.firstURL+"/api/") || strings.HasPrefix(target, fixture.secondURL+"/api/") {
		request.Header.Set("Accept", api.JSONContentType)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if csrf != "" {
		request.Header.Set(websessionauth.DefaultCSRFHeader, csrf)
	}
	response, err := fixture.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Errorf("close Article site response: %v", err)
		}
	}()
	encoded, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return siteAppHTTPResult{status: response.StatusCode, header: response.Header.Clone(), body: string(encoded)}
}

func (fixture siteAppCSRFFixture) assertArticles(t *testing.T, wantTitles []string) {
	t.Helper()
	repository, err := articleapp.NewRepository(fixture.firstBackend)
	if err != nil {
		t.Fatalf("NewRepository(): %v", err)
	}
	page, err := repository.List(context.Background(), articleapp.ListOptions{Limit: 10})
	if err != nil || page.Total != int64(len(wantTitles)) || len(page.Articles) != len(wantTitles) {
		t.Fatalf("Article rows = (%#v, %v), want titles %v", page, err, wantTitles)
	}
	for index, title := range wantTitles {
		if page.Articles[index].Title != title {
			t.Fatalf("Article %d title = %q, want %q", index, page.Articles[index].Title, title)
		}
	}
}

func openSiteAppCSRFBackend(t *testing.T, ctx context.Context, dataSourceName string) *sqlite.Backend {
	t.Helper()
	backend, err := sqlite.Open(ctx, dataSourceName)
	if err != nil {
		t.Fatalf("sqlite.Open(%q): %v", dataSourceName, err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("close Article site CSRF backend: %v", err)
		}
	})
	return backend
}

func newSiteAppTestServer(t *testing.T, application http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(application)
	t.Cleanup(server.Close)
	return server
}

func migrateSiteAppCSRFSchema(t *testing.T, ctx context.Context, backend *sqlite.Backend) {
	t.Helper()
	repository := siteAppRepositoryRoot(t)
	document, err := os.ReadFile(filepath.Join(
		repository,
		"examples",
		"article",
		"migrations",
		"0001_initial.godj.json",
	))
	if err != nil {
		t.Fatalf("read Article definition: %v", err)
	}
	loaded, _, err := migrationdefinition.Load(
		migrationdefinition.Source{
			SourceID: "examples/article/migrations/0001_initial.godj.json",
			Document: document,
		},
		systemstate.InitialDefinitionSource(),
	)
	if err != nil {
		t.Fatalf("load Article and system-state definitions: %v", err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatalf("migrate Article and system-state definitions: %v", err)
	}
}

func siteAppRepositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("cannot locate repository root from Article site CSRF test")
		}
		directory = parent
	}
}
