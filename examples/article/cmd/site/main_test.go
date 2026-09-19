package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/internal/operatorconfig"
	"github.com/progresshans/godj/examples/article/webapp"
	"github.com/progresshans/godj/migrations"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/systemstate"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

var siteLoginCSRFPattern = regexp.MustCompile(`name="csrfmiddlewaretoken" value="([A-Za-z0-9_-]{128})"`)

const (
	articleSiteOperationTimeout = 30 * time.Second
	articleSiteShutdownTimeout  = 10 * time.Second
)

func TestParseServeConfig(t *testing.T) {
	config, err := parseServeConfig([]string{"serve"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if config.listenAddress != defaultListenAddress || config.database != "" || config.databaseSpecified {
		t.Fatalf("config = %#v", config)
	}
	config, err = parseServeConfig([]string{"serve", "--listen", "127.0.0.1:0"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if config.listenAddress != "127.0.0.1:0" || config.database != "" || config.databaseSpecified {
		t.Fatalf("runserver config = %#v", config)
	}
	config, err = parseServeConfig([]string{"serve", "--database", "file:article.sqlite3"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if config.listenAddress != defaultListenAddress || config.database != "file:article.sqlite3" || !config.databaseSpecified {
		t.Fatalf("explicit config = %#v", config)
	}
}

func TestParseServeConfigRejectsInvalidArguments(t *testing.T) {
	tests := [][]string{
		nil,
		{"run", "--database", "article.sqlite3"},
		{"serve", "--database", "article.sqlite3", "unexpected"},
		{"serve", "--listen", " ", "--database", "article.sqlite3"},
		{"serve", "--database", " "},
	}
	for _, arguments := range tests {
		if _, err := parseServeConfig(arguments, &bytes.Buffer{}); err == nil {
			t.Errorf("parseServeConfig(%q) error = nil", arguments)
		}
	}
}

func TestDatabaseConfigForServeSelectsStrictEnvironment(t *testing.T) {
	tests := []struct {
		name        string
		config      serveConfig
		environment map[string]string
		want        databaseConfig
		wantError   string
	}{
		{
			name:        "sqlite environment",
			environment: map[string]string{articleSQLiteDatabaseEnv: " file:article.sqlite3 "},
			want:        databaseConfig{kind: databaseKindSQLite, sqliteDatabase: "file:article.sqlite3"},
		},
		{
			name: "postgres environment",
			environment: map[string]string{
				articlePostgresURLEnv:    " postgres://article:secret@127.0.0.1/article ",
				articlePostgresSchemaEnv: " article_runtime ",
			},
			want: databaseConfig{
				kind:           databaseKindPostgres,
				postgresURL:    "postgres://article:secret@127.0.0.1/article",
				postgresSchema: "article_runtime",
			},
		},
		{
			name:   "direct sqlite compatibility",
			config: serveConfig{database: "article.sqlite3", databaseSpecified: true},
			want:   databaseConfig{kind: databaseKindSQLite, sqliteDatabase: "article.sqlite3"},
		},
		{
			name:      "missing configuration",
			wantError: "configure " + articleSQLiteDatabaseEnv,
		},
		{
			name:        "empty sqlite",
			environment: map[string]string{articleSQLiteDatabaseEnv: " "},
			wantError:   articleSQLiteDatabaseEnv + " is empty",
		},
		{
			name:        "postgres URL without schema",
			environment: map[string]string{articlePostgresURLEnv: "postgres://article:secret@127.0.0.1/article"},
			wantError:   articlePostgresSchemaEnv + " is required",
		},
		{
			name:        "postgres schema without URL",
			environment: map[string]string{articlePostgresSchemaEnv: "article_runtime"},
			wantError:   articlePostgresURLEnv + " is required",
		},
		{
			name: "backend conflict",
			environment: map[string]string{
				articleSQLiteDatabaseEnv: "article.sqlite3",
				articlePostgresURLEnv:    "postgres://article:secret@127.0.0.1/article",
				articlePostgresSchemaEnv: "article_runtime",
			},
			wantError: "SQLite and PostgreSQL environment are mutually exclusive",
		},
		{
			name:   "direct and environment conflict",
			config: serveConfig{database: "article.sqlite3", databaseSpecified: true},
			environment: map[string]string{
				articlePostgresURLEnv:    "postgres://article:secret@127.0.0.1/article",
				articlePostgresSchemaEnv: "article_runtime",
			},
			wantError: "--database and database environment are mutually exclusive",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lookup := func(name string) (string, bool) {
				value, exists := test.environment[name]
				return value, exists
			}
			got, err := databaseConfigForServe(test.config, lookup)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("databaseConfigForServe() error = %v, want containing %q", err, test.wantError)
				}
				if strings.Contains(err.Error(), "secret") {
					t.Fatalf("databaseConfigForServe() exposed PostgreSQL URL secret: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("databaseConfigForServe() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestLoopbackListenAddressIsIPLiteralOnly(t *testing.T) {
	tests := []struct {
		address string
		want    bool
	}{
		{address: "127.0.0.1:0", want: true},
		{address: "[::1]:8000", want: true},
		{address: "0.0.0.0:8000"},
		{address: "192.0.2.10:8000"},
		{address: "localhost:8000"},
		{address: ":8000"},
		{address: "not-an-address"},
	}
	for _, test := range tests {
		if got := loopbackListenAddress(test.address); got != test.want {
			t.Errorf("loopbackListenAddress(%q) = %t, want %t", test.address, got, test.want)
		}
	}
}

func TestApplicationForServePreservesPublicOnlyDefault(t *testing.T) {
	backend := newArticleSiteTestBackend(t, "article-site-default")
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("close default Article site backend: %v", err)
		}
	})
	explicitlyMigrateArticleSiteSystemState(t, backend)
	application, err := applicationForServe(context.Background(), backend, false)
	if err != nil {
		t.Fatal(err)
	}
	if path, err := application.Reverse(webapp.ArticleListRoute); err != nil || path != webapp.ArticleListPath {
		t.Fatalf("legacy Article route reverse = %q, %v", path, err)
	}
	server := httptest.NewServer(application)
	t.Cleanup(server.Close)

	public := articleSiteGET(t, server.Client(), server.URL+webapp.ArticleListPath, "")
	if public.status != http.StatusOK || public.contentType != "text/html; charset=utf-8" {
		t.Fatalf("default public route = status %d content-type %q body %q", public.status, public.contentType, public.body)
	}
	for _, path := range []string{"/admin/", "/api/articles/"} {
		missing := articleSiteGET(t, server.Client(), server.URL+path, "")
		if missing.status != http.StatusNotFound || missing.contentType != "text/plain; charset=utf-8" || missing.body != "Not Found\n" {
			t.Fatalf("default route %s = status %d content-type %q body %q", path, missing.status, missing.contentType, missing.body)
		}
	}
}

func TestApplicationForServeRequiresExplicitSystemMigrationWithoutCreatingSchema(t *testing.T) {
	ctx := context.Background()
	backend := newArticleSiteTestBackend(t, "article-site-system-migration-gate")
	t.Cleanup(func() { _ = backend.Close() })
	application, err := applicationForServe(ctx, backend, true)
	if application != nil || !errors.Is(err, &systemstate.Error{
		Code:  systemstate.CodeSchemaUnavailable,
		Field: "migration_history",
	}) {
		t.Fatalf("applicationForServe(missing system migration) = (%v,%#v)", application, err)
	}
	history, historyErr := backend.ReadAppliedMigrations(ctx)
	if historyErr != nil || len(history) != 0 {
		t.Fatalf("migration history after rejected startup = (%+v,%v)", history, historyErr)
	}
	rows, queryErr := backend.Query(ctx, query.NewPlan(
		"godj_system_credential",
		[]query.FieldRef{query.NewFieldRef("id", "id", query.FieldInteger, false)},
	))
	if rows != nil {
		_ = rows.Close()
	}
	if !errors.Is(queryErr, &query.Error{Category: query.CategoryBackend, Code: query.CodeMissingTable}) {
		t.Fatalf("credential table after rejected startup error = %#v", queryErr)
	}
}

func TestApplicationForServeExplicitSystemMigrationPersistsSessionAcrossReopenAndRotatesCSRFKey(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "article-site-restart.sqlite3")
	dataSourceName := "file:" + filepath.ToSlash(databasePath) + "?mode=rwc"
	const (
		username = "restart-admin"
		password = "restart-password-secret-marker"
	)
	firstBackend, err := sqlite.Open(ctx, dataSourceName)
	if err != nil {
		t.Fatalf("open first Article site backend: %v", err)
	}
	createArticleSiteArticleTable(t, firstBackend)
	explicitlyMigrateArticleSiteSystemState(t, firstBackend)
	provisionArticleSiteOperator(t, ctx, firstBackend, username, password)
	firstApplication, err := applicationForServe(ctx, firstBackend, true)
	if err != nil {
		_ = firstBackend.Close()
		t.Fatalf("applicationForServe(first process): %v", err)
	}
	firstServer := httptest.NewServer(firstApplication)
	jar, err := cookiejar.New(nil)
	if err != nil {
		firstServer.Close()
		_ = firstBackend.Close()
		t.Fatal(err)
	}
	client := &http.Client{
		Jar:     jar,
		Timeout: articleSiteOperationTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	login := articleSiteLogin(t, client, firstServer.URL, username, password)
	firstSafe := articleSiteGET(t, client, firstServer.URL+"/api/articles/", api.JSONContentType)
	if firstSafe.status != http.StatusOK || firstSafe.body != `{"count":0,"next":null,"previous":null,"results":[]}` {
		t.Fatalf("first-process authenticated API = status %d body %q", firstSafe.status, firstSafe.body)
	}
	staleCSRF := firstSafe.header.Get(websessionauth.DefaultCSRFHeader)
	if len(staleCSRF) != 128 {
		t.Fatalf("first-process API CSRF token length = %d", len(staleCSRF))
	}
	created := articleSiteJSON(
		t,
		client,
		http.MethodPost,
		firstServer.URL+"/api/articles/",
		`{"title":"Before restart","published":true}`,
		staleCSRF,
	)
	if created.status != http.StatusCreated || created.body != `{"id":1,"title":"Before restart","published":true,"summary":null}` {
		t.Fatalf("first-process API create = status %d body %q", created.status, created.body)
	}
	if got := articleSiteTableRowCount(t, ctx, firstBackend, "godj_system_audit"); got != 0 {
		t.Fatalf("API write synthesized %d Admin audit rows", got)
	}
	firstServer.Close()
	if err := firstBackend.Close(); err != nil {
		t.Fatalf("close first Article site backend: %v", err)
	}

	secondBackend, err := sqlite.Open(ctx, dataSourceName)
	if err != nil {
		t.Fatalf("reopen Article site backend: %v", err)
	}
	t.Cleanup(func() { _ = secondBackend.Close() })
	secondApplication, err := applicationForServe(ctx, secondBackend, true)
	if err != nil {
		t.Fatalf("applicationForServe(reopened process): %v", err)
	}
	secondServer := httptest.NewServer(secondApplication)
	t.Cleanup(secondServer.Close)
	restartedSafe := articleSiteGET(t, client, secondServer.URL+"/api/articles/", api.JSONContentType)
	if restartedSafe.status != http.StatusOK || restartedSafe.body != `{"count":1,"next":null,"previous":null,"results":[{"id":1,"title":"Before restart","published":true,"summary":null}]}` {
		t.Fatalf("restarted authenticated API = status %d body %q", restartedSafe.status, restartedSafe.body)
	}
	if got := articleSiteCookieValue(t, jar, secondServer.URL, websessionauth.DefaultSessionCookieName); got != login.sessionCookie {
		t.Fatalf("restarted session cookie changed: got length %d want length %d", len(got), len(login.sessionCookie))
	}
	freshCSRF := restartedSafe.header.Get(websessionauth.DefaultCSRFHeader)
	if len(freshCSRF) != 128 || freshCSRF == staleCSRF {
		t.Fatalf("restarted API CSRF token has unsafe shape: length=%d equal-stale=%t", len(freshCSRF), freshCSRF == staleCSRF)
	}
	staleAttempt := articleSiteJSON(
		t,
		client,
		http.MethodPost,
		secondServer.URL+"/api/articles/",
		`{"title":"Must not persist"}`,
		staleCSRF,
	)
	if staleAttempt.status != http.StatusForbidden || staleAttempt.body != `{"code":"csrf_rejected","errors":[]}` {
		t.Fatalf("restarted stale-CSRF API write = status %d body %q", staleAttempt.status, staleAttempt.body)
	}
	freshAttempt := articleSiteJSON(
		t,
		client,
		http.MethodPost,
		secondServer.URL+"/api/articles/",
		`{"title":"After restart"}`,
		freshCSRF,
	)
	if freshAttempt.status != http.StatusCreated || freshAttempt.body != `{"id":2,"title":"After restart","published":false,"summary":null}` {
		t.Fatalf("restarted fresh-CSRF API write = status %d body %q", freshAttempt.status, freshAttempt.body)
	}
	if got := articleSiteTableRowCount(t, ctx, secondBackend, "godj_conformance_article"); got != 2 {
		t.Fatalf("Article rows after rejected and accepted restart writes = %d, want 2", got)
	}
	if got := articleSiteTableRowCount(t, ctx, secondBackend, "godj_system_audit"); got != 0 {
		t.Fatalf("restarted API writes synthesized %d Admin audit rows", got)
	}
	addPage := articleSiteGET(t, client, secondServer.URL+"/admin/articles/add/", "")
	addToken := siteLoginCSRFPattern.FindStringSubmatch(addPage.body)
	if addPage.status != http.StatusOK || len(addToken) != 2 {
		t.Fatalf("restarted Admin add page = status %d token-parts %d body %q", addPage.status, len(addToken), addPage.body)
	}
	addValues := url.Values{
		"csrfmiddlewaretoken": {addToken[1]},
		"title":               {"Admin audited"},
		"summary":             {"durable history"},
	}
	addRequest, err := http.NewRequest(
		http.MethodPost,
		secondServer.URL+"/admin/articles/add/",
		strings.NewReader(addValues.Encode()),
	)
	if err != nil {
		t.Fatal(err)
	}
	addRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	addResponse, err := client.Do(addRequest)
	if err != nil {
		t.Fatal(err)
	}
	addBody := articleSiteReadBody(t, addResponse)
	if addResponse.StatusCode != http.StatusFound || !strings.HasPrefix(addResponse.Header.Get("Location"), "/admin/articles/") {
		t.Fatalf("restarted Admin add = status %d location %q body %q", addResponse.StatusCode, addResponse.Header.Get("Location"), addBody)
	}
	if got := articleSiteTableRowCount(t, ctx, secondBackend, "godj_conformance_article"); got != 3 {
		t.Fatalf("Article rows after durable Admin write = %d, want 3", got)
	}
	if got := articleSiteTableRowCount(t, ctx, secondBackend, "godj_system_audit"); got != 1 {
		t.Fatalf("durable Admin audit rows = %d, want 1", got)
	}
	logoutPage := articleSiteGET(t, client, secondServer.URL+"/admin/articles/", "")
	logoutToken := siteLoginCSRFPattern.FindStringSubmatch(logoutPage.body)
	if logoutPage.status != http.StatusOK || len(logoutToken) != 2 {
		t.Fatalf("process-B logout page = status %d token-parts %d body %q", logoutPage.status, len(logoutToken), logoutPage.body)
	}
	copiedPreLogoutSession := login.sessionCookie
	logoutValues := url.Values{"csrfmiddlewaretoken": {logoutToken[1]}}
	logoutRequest, err := http.NewRequest(
		http.MethodPost,
		secondServer.URL+"/admin/logout/",
		strings.NewReader(logoutValues.Encode()),
	)
	if err != nil {
		t.Fatal(err)
	}
	logoutRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	logoutResponse, err := client.Do(logoutRequest)
	if err != nil {
		t.Fatal(err)
	}
	logoutBody := articleSiteReadBody(t, logoutResponse)
	if logoutResponse.StatusCode != http.StatusFound || logoutResponse.Header.Get("Location") != "/admin/login/" || logoutBody != "Found\n" {
		t.Fatalf("process-B logout = status %d location %q body %q", logoutResponse.StatusCode, logoutResponse.Header.Get("Location"), logoutBody)
	}
	if got := articleSiteCookieValue(t, jar, secondServer.URL, websessionauth.DefaultSessionCookieName); got != "" {
		t.Fatalf("process-B logout retained browser session cookie of length %d", len(got))
	}
	if got := articleSiteTableRowCount(t, ctx, secondBackend, "godj_system_session"); got != 0 {
		t.Fatalf("durable session rows after process-B logout = %d, want 0", got)
	}
	secondServer.Close()
	if err := secondBackend.Close(); err != nil {
		t.Fatalf("close process-B Article site backend: %v", err)
	}

	thirdBackend, err := sqlite.Open(ctx, dataSourceName)
	if err != nil {
		t.Fatalf("open process-C Article site backend: %v", err)
	}
	t.Cleanup(func() { _ = thirdBackend.Close() })
	thirdApplication, err := applicationForServe(ctx, thirdBackend, true)
	if err != nil {
		t.Fatalf("applicationForServe(process C): %v", err)
	}
	thirdServer := httptest.NewServer(thirdApplication)
	t.Cleanup(thirdServer.Close)
	copiedJar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	thirdURL, err := url.Parse(thirdServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	copiedJar.SetCookies(thirdURL, []*http.Cookie{{
		Name:     websessionauth.DefaultSessionCookieName,
		Value:    copiedPreLogoutSession,
		Path:     "/",
		HttpOnly: true,
	}})
	copiedClient := &http.Client{
		Jar:     copiedJar,
		Timeout: articleSiteOperationTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	adminAfterLogout := articleSiteGET(t, copiedClient, thirdServer.URL+"/admin/articles/", "")
	if adminAfterLogout.status != http.StatusFound || !strings.HasPrefix(adminAfterLogout.header.Get("Location"), "/admin/login/?next=") {
		t.Fatalf("process-C copied-cookie Admin = status %d location %q body %q", adminAfterLogout.status, adminAfterLogout.header.Get("Location"), adminAfterLogout.body)
	}
	apiAfterLogout := articleSiteGET(t, copiedClient, thirdServer.URL+"/api/articles/", api.JSONContentType)
	if apiAfterLogout.status != http.StatusForbidden || apiAfterLogout.body != `{"code":"not_authenticated","errors":[]}` {
		t.Fatalf("process-C copied-cookie API = status %d body %q", apiAfterLogout.status, apiAfterLogout.body)
	}
	if got := articleSiteTableRowCount(t, ctx, thirdBackend, "godj_system_session"); got != 0 {
		t.Fatalf("process-C durable session rows = %d, want 0", got)
	}
	if got := articleSiteTableRowCount(t, ctx, thirdBackend, "godj_system_audit"); got != 1 {
		t.Fatalf("process-C durable Admin audit rows = %d, want 1", got)
	}
}

func TestRunRejectsProvisionedAuthenticatedNonLoopbackBeforeListener(t *testing.T) {
	clearArticleDatabaseEnvironment(t)
	t.Setenv(articleSQLiteDatabaseEnv, "file:opened-by-test-adapter.sqlite3")
	backend := newArticleSiteTestBackend(t, "article-site-non-loopback")
	explicitlyMigrateArticleSiteSystemState(t, backend)
	provisionArticleSiteOperator(t, context.Background(), backend, "admin", "non-loopback-secret-marker")
	var backendCalls atomic.Int32
	var listenCalls atomic.Int32
	openBackend := func(context.Context, databaseConfig) (articleBackend, error) {
		backendCalls.Add(1)
		return backend, nil
	}
	listen := func(string, string) (net.Listener, error) {
		listenCalls.Add(1)
		return nil, nil
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runWithListener(
		context.Background(),
		[]string{"serve", "--listen", "0.0.0.0:8000"},
		&stdout,
		&stderr,
		listen,
		openBackend,
	)
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("non-loopback authenticated startup error = %v", err)
	}
	if backendCalls.Load() != 1 || listenCalls.Load() != 0 {
		t.Fatalf("non-loopback startup side effects = backend %d listener %d", backendCalls.Load(), listenCalls.Load())
	}
}

func TestRunPublishesOptInAdminAndAPIAndCancelsCleanly(t *testing.T) {
	clearArticleDatabaseEnvironment(t)
	const (
		username = "development-admin"
		password = "publication-smoke-secret"
	)
	t.Setenv(articleSQLiteDatabaseEnv, "file:ignored-by-test-opener")

	backend := newArticleSiteTestBackend(t, "article-site-publication")
	t.Cleanup(func() { _ = backend.Close() })
	explicitlyMigrateArticleSiteSystemState(t, backend)
	provisionArticleSiteOperator(t, context.Background(), backend, username, password)
	var opened atomic.Int32
	openBackend := func(context.Context, databaseConfig) (articleBackend, error) {
		opened.Add(1)
		return backend, nil
	}
	listenerReady := make(chan net.Listener, 1)
	listen := func(network, _ string) (net.Listener, error) {
		listener, err := net.Listen(network, "127.0.0.1:0")
		if err == nil {
			listenerReady <- listener
		}
		return listener, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	runResult := make(chan error, 1)
	go func() {
		runResult <- runWithListener(
			ctx,
			[]string{"serve", "--listen", "127.0.0.1:0"},
			&stdout,
			&stderr,
			listen,
			openBackend,
		)
	}()
	var listener net.Listener
	select {
	case listener = <-listenerReady:
	case err := <-runResult:
		t.Fatalf("authenticated Article site stopped before listen: %v", err)
	case <-time.After(articleSiteOperationTimeout):
		t.Fatal("timed out waiting for authenticated Article site listener")
	}
	baseURL := "http://" + listener.Addr().String()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Jar:     jar,
		Timeout: articleSiteOperationTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	public := articleSiteGET(t, client, baseURL+webapp.ArticleListPath, "")
	if public.status != http.StatusOK || public.contentType != "text/html; charset=utf-8" {
		t.Fatalf("composed public route = status %d content-type %q body %q", public.status, public.contentType, public.body)
	}
	anonymous := articleSiteGET(t, client, baseURL+"/api/articles/", api.JSONContentType)
	if anonymous.status != http.StatusForbidden || anonymous.contentType != api.JSONContentType || anonymous.body != `{"code":"not_authenticated","errors":[]}` {
		t.Fatalf("anonymous API = status %d content-type %q body %q", anonymous.status, anonymous.contentType, anonymous.body)
	}
	login := articleSiteLogin(t, client, baseURL, username, password)
	preLoginToken := login.preLoginToken
	sessionSecret := login.sessionCookie
	csrfSecret := login.csrfCookie
	authenticated := articleSiteGET(t, client, baseURL+"/api/articles/", api.JSONContentType)
	if authenticated.status != http.StatusOK || authenticated.contentType != api.JSONContentType || authenticated.body != `{"count":0,"next":null,"previous":null,"results":[]}` {
		t.Fatalf("authenticated API = status %d content-type %q body %q", authenticated.status, authenticated.contentType, authenticated.body)
	}
	freshToken := authenticated.header.Get(websessionauth.DefaultCSRFHeader)
	if len(freshToken) != 128 || freshToken == preLoginToken || freshToken == csrfSecret {
		t.Fatalf("authenticated API CSRF response token has unsafe shape: length=%d", len(freshToken))
	}

	cancel()
	select {
	case err := <-runResult:
		if err != nil {
			t.Fatalf("authenticated Article site cancellation error = %v", err)
		}
	case <-time.After(articleSiteShutdownTimeout):
		t.Fatal("authenticated Article site did not stop after cancellation")
	}
	if got := opened.Load(); got != 1 {
		t.Fatalf("authenticated Article site backend opens = %d, want 1", got)
	}
	combinedOutput := stdout.String() + stderr.String()
	for _, secret := range []string{password, sessionSecret, csrfSecret, preLoginToken, freshToken} {
		if strings.Contains(combinedOutput, secret) {
			t.Fatalf("Article site output exposed authentication secret in %q", combinedOutput)
		}
	}
	if !strings.Contains(stdout.String(), "article site listening on http://") || stderr.Len() != 0 {
		t.Fatalf("Article site output = stdout %q stderr %q", stdout.String(), stderr.String())
	}
}

func TestArticleSiteProductionSourceHasNoRawOperatorCredentialEnvironment(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"GODJ_ARTICLE_ADMIN_USERNAME",
		"GODJ_ARTICLE_ADMIN_PASSWORD",
		"publicationConfig",
	} {
		if bytes.Contains(source, []byte(forbidden)) {
			t.Fatalf("Article site production source retains forbidden raw-credential environment surface %q", forbidden)
		}
	}
}

func TestOpenArticleBackendPostgresDiagnosticDoesNotExposeURLSecret(t *testing.T) {
	const secret = "runtime-password"
	_, err := openArticleBackend(context.Background(), databaseConfig{
		kind:           databaseKindPostgres,
		postgresURL:    "https://article:" + secret + "@example.invalid/article",
		postgresSchema: "article_runtime",
	})
	if err == nil {
		t.Fatal("openArticleBackend() error = nil")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("openArticleBackend() exposed PostgreSQL URL secret: %v", err)
	}
}

func TestRunRejectsNilContextBeforeSideEffects(t *testing.T) {
	if err := run(nil, []string{"serve"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("run(nil) error = nil")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := run(canceled, []string{"serve"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("run(canceled) error = nil")
	}
}

func TestRunClosesBackendAndListenerOnceWhenContextCancelsBeforeServeOwnership(t *testing.T) {
	clearArticleDatabaseEnvironment(t)
	t.Setenv(articleSQLiteDatabaseEnv, "file:site-cancel-window?mode=memory&cache=shared")
	ctx, cancel := context.WithCancel(context.Background())
	var backendCloses atomic.Int32
	backend := newArticleSiteTestBackend(t, "site-cancel-window")
	explicitlyMigrateArticleSiteSystemState(t, backend)
	openBackend := func(context.Context, databaseConfig) (articleBackend, error) {
		return &countingArticleBackend{articleBackend: backend, closes: &backendCloses}, nil
	}
	var listener *countingListener
	listen := func(network, address string) (net.Listener, error) {
		opened, err := net.Listen(network, "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		listener = &countingListener{Listener: opened}
		return listener, nil
	}
	stdout := cancelWriter{cancel: cancel}
	err := runWithListener(
		ctx,
		[]string{"serve", "--listen", "127.0.0.1:0"},
		stdout,
		&bytes.Buffer{},
		listen,
		openBackend,
	)
	if err != nil {
		t.Fatalf("runWithListener() error = %v", err)
	}
	if listener == nil {
		t.Fatal("listener was not created")
	}
	if closes := listener.closes.Load(); closes != 1 {
		t.Fatalf("listener Close() calls = %d, want 1", closes)
	}
	if closes := backendCloses.Load(); closes != 1 {
		t.Fatalf("backend Close() calls = %d, want 1", closes)
	}
}

type countingArticleBackend struct {
	articleBackend
	closes *atomic.Int32
}

func newArticleSiteTestBackend(t *testing.T, name string) *sqlite.Backend {
	t.Helper()
	backend, err := sqlite.OpenMemory(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	createArticleSiteArticleTable(t, backend)
	return backend
}

func createArticleSiteArticleTable(t *testing.T, backend *sqlite.Backend) {
	t.Helper()
	if _, err := backend.ExecContext(context.Background(), `CREATE TABLE "godj_conformance_article" (
  "id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
  "title" VARCHAR(200) NOT NULL,
  "published" BOOLEAN NOT NULL,
  "summary" VARCHAR(200) NULL
)`); err != nil {
		t.Fatal(err)
	}
}

func explicitlyMigrateArticleSiteSystemState(t *testing.T, backend *sqlite.Backend) {
	t.Helper()
	ctx := context.Background()
	loaded, _, err := migrationdefinition.Load(systemstate.InitialDefinitionSource())
	if err != nil {
		t.Fatalf("load Article site system definition: %v", err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(
		ctx,
		loaded,
		migrations.LatestLifecycleRequest(),
	); err != nil {
		t.Fatalf("explicitly migrate Article site system definition: %v", err)
	}
	history, err := backend.ReadAppliedMigrations(ctx)
	want := systemstate.InitialMigrationKey()
	if err != nil || len(history) != 1 || history[0].App != want.App || history[0].Name != want.Name {
		t.Fatalf("Article site system migration history = (%+v,%v), want %s.%s", history, err, want.App, want.Name)
	}
}

func provisionArticleSiteOperator(
	t *testing.T,
	ctx context.Context,
	backend systemstate.Backend,
	username, password string,
) {
	t.Helper()
	policy, err := operatorconfig.CredentialPolicy()
	if err != nil {
		t.Fatalf("Article operator policy: %v", err)
	}
	if err := systemstate.ProvisionOperator(ctx, backend, systemstate.ProvisionOperatorConfig{
		Username:         username,
		Password:         password,
		CredentialPolicy: policy,
	}); err != nil {
		t.Fatalf("ProvisionOperator(): %v", err)
	}
}

type articleSiteLoginState struct {
	preLoginToken string
	sessionCookie string
	csrfCookie    string
}

func articleSiteLogin(
	t *testing.T,
	client *http.Client,
	baseURL, username, password string,
) articleSiteLoginState {
	t.Helper()
	loginPage := articleSiteGET(t, client, baseURL+"/admin/login/", "")
	if loginPage.status != http.StatusOK || loginPage.contentType != "text/html; charset=utf-8" {
		t.Fatalf("Admin login page = status %d content-type %q body %q", loginPage.status, loginPage.contentType, loginPage.body)
	}
	match := siteLoginCSRFPattern.FindStringSubmatch(loginPage.body)
	if len(match) != 2 {
		t.Fatal("Admin login CSRF token is missing")
	}
	preLoginToken := match[1]
	values := url.Values{
		"csrfmiddlewaretoken": {preLoginToken},
		"username":            {username},
		"password":            {password},
		"next":                {"/admin/articles/"},
	}
	request, err := http.NewRequest(
		http.MethodPost,
		baseURL+"/admin/login/",
		strings.NewReader(values.Encode()),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body := articleSiteReadBody(t, response)
	if response.StatusCode != http.StatusFound || response.Header.Get("Location") != "/admin/articles/" || body != "Found\n" {
		t.Fatalf("Admin login = status %d location %q body %q", response.StatusCode, response.Header.Get("Location"), body)
	}
	result := articleSiteLoginState{preLoginToken: preLoginToken}
	for _, cookie := range response.Cookies() {
		switch cookie.Name {
		case websessionauth.DefaultSessionCookieName:
			result.sessionCookie = cookie.Value
		case websessionauth.DefaultCSRFCookieName:
			result.csrfCookie = cookie.Value
		}
	}
	if result.sessionCookie == "" || result.csrfCookie == "" || result.csrfCookie == preLoginToken {
		t.Fatal("Admin login did not publish independent session and rotated CSRF cookies")
	}
	return result
}

func articleSiteJSON(
	t *testing.T,
	client *http.Client,
	method, target, body, csrf string,
) articleSiteHTTPResult {
	t.Helper()
	request, err := http.NewRequest(method, target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept", api.JSONContentType)
	if body != "" {
		request.Header.Set("Content-Type", api.JSONContentType)
	}
	if csrf != "" {
		request.Header.Set(websessionauth.DefaultCSRFHeader, csrf)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return articleSiteHTTPResult{
		status:      response.StatusCode,
		contentType: response.Header.Get("Content-Type"),
		header:      response.Header.Clone(),
		body:        articleSiteReadBody(t, response),
	}
}

func articleSiteCookieValue(t *testing.T, jar http.CookieJar, target, name string) string {
	t.Helper()
	parsed, err := url.Parse(target)
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

func articleSiteTableRowCount(t *testing.T, ctx context.Context, backend *sqlite.Backend, table string) int {
	t.Helper()
	identifier := query.NewFieldRef("id", "id", query.FieldInteger, false)
	rows, err := backend.Query(ctx, query.NewPlan(table, []query.FieldRef{identifier}))
	if err != nil {
		t.Fatalf("query Article site table %q: %v", table, err)
	}
	count := 0
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			t.Fatalf("scan Article site table %q row: %v", table, err)
		}
		count++
	}
	iterationErr := rows.Err()
	closeErr := rows.Close()
	if iterationErr != nil || closeErr != nil {
		t.Fatalf("read Article site table %q rows: %v", table, errors.Join(iterationErr, closeErr))
	}
	return count
}

type articleSiteHTTPResult struct {
	status      int
	contentType string
	header      http.Header
	body        string
}

func articleSiteGET(t *testing.T, client *http.Client, target string, accept string) articleSiteHTTPResult {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	if accept != "" {
		request.Header.Set("Accept", accept)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return articleSiteHTTPResult{
		status:      response.StatusCode,
		contentType: response.Header.Get("Content-Type"),
		header:      response.Header.Clone(),
		body:        articleSiteReadBody(t, response),
	}
}

func articleSiteReadBody(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

func (backend *countingArticleBackend) Close() error {
	backend.closes.Add(1)
	return backend.articleBackend.Close()
}

func clearArticleDatabaseEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		articleSQLiteDatabaseEnv,
		articlePostgresURLEnv,
		articlePostgresSchemaEnv,
	} {
		value, configured := os.LookupEnv(name)
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if configured {
				if err := os.Setenv(name, value); err != nil {
					t.Errorf("restore %s: %v", name, err)
				}
				return
			}
			if err := os.Unsetenv(name); err != nil {
				t.Errorf("unset %s: %v", name, err)
			}
		})
	}
}

type cancelWriter struct {
	cancel context.CancelFunc
}

func (w cancelWriter) Write(payload []byte) (int, error) {
	w.cancel()
	return len(payload), nil
}

type countingListener struct {
	net.Listener
	closes atomic.Int32
}

func (l *countingListener) Close() error {
	l.closes.Add(1)
	return l.Listener.Close()
}
