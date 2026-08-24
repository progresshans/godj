package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/webapp"
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

func TestPublicationConfigForServeIsExplicitPairedAndLoopbackOnly(t *testing.T) {
	const password = "publication-secret-marker"
	tests := []struct {
		name        string
		listen      string
		environment map[string]string
		wantEnabled bool
		wantUser    string
		wantPass    string
		wantError   string
	}{
		{
			name:   "credentials absent preserves non-loopback public mode",
			listen: "0.0.0.0:8000",
		},
		{
			name:   "IPv4 loopback",
			listen: "127.0.0.1:0",
			environment: map[string]string{
				articleAdminUsernameEnv: " admin ",
				articleAdminPasswordEnv: password,
			},
			wantEnabled: true,
			wantUser:    "admin",
			wantPass:    password,
		},
		{
			name:   "IPv6 loopback",
			listen: "[::1]:8000",
			environment: map[string]string{
				articleAdminUsernameEnv: "admin",
				articleAdminPasswordEnv: "  password with intentional edges  ",
			},
			wantEnabled: true,
			wantUser:    "admin",
			wantPass:    "  password with intentional edges  ",
		},
		{
			name:   "username only",
			listen: defaultListenAddress,
			environment: map[string]string{
				articleAdminUsernameEnv: "admin",
			},
			wantError: "must be configured together",
		},
		{
			name:   "password only",
			listen: defaultListenAddress,
			environment: map[string]string{
				articleAdminPasswordEnv: password,
			},
			wantError: "must be configured together",
		},
		{
			name:   "blank username",
			listen: defaultListenAddress,
			environment: map[string]string{
				articleAdminUsernameEnv: " ",
				articleAdminPasswordEnv: password,
			},
			wantError: articleAdminUsernameEnv + " is empty",
		},
		{
			name:   "blank password",
			listen: defaultListenAddress,
			environment: map[string]string{
				articleAdminUsernameEnv: "admin",
				articleAdminPasswordEnv: "\t",
			},
			wantError: articleAdminPasswordEnv + " is empty",
		},
		{
			name:   "wildcard",
			listen: "0.0.0.0:8000",
			environment: map[string]string{
				articleAdminUsernameEnv: "admin",
				articleAdminPasswordEnv: password,
			},
			wantError: "requires a loopback listen address",
		},
		{
			name:   "non-loopback IP",
			listen: "192.0.2.10:8000",
			environment: map[string]string{
				articleAdminUsernameEnv: "admin",
				articleAdminPasswordEnv: password,
			},
			wantError: "requires a loopback listen address",
		},
		{
			name:   "arbitrary hostname",
			listen: "localhost:8000",
			environment: map[string]string{
				articleAdminUsernameEnv: "admin",
				articleAdminPasswordEnv: password,
			},
			wantError: "requires a loopback listen address",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lookup := func(name string) (string, bool) {
				value, configured := test.environment[name]
				return value, configured
			}
			got, err := publicationConfigForServe(serveConfig{listenAddress: test.listen}, lookup)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("publicationConfigForServe() error = %v, want containing %q", err, test.wantError)
				}
				if strings.Contains(err.Error(), password) {
					t.Fatalf("publicationConfigForServe() exposed password: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.authenticated != test.wantEnabled || got.username != test.wantUser || got.password != test.wantPass {
				t.Fatalf("publication config = %#v", got)
			}
		})
	}
	if _, err := publicationConfigForServe(serveConfig{}, nil); err == nil {
		t.Fatal("publicationConfigForServe(nil lookup) error = nil")
	}
}

func TestApplicationForServePreservesPublicOnlyDefault(t *testing.T) {
	backend := newArticleSiteTestBackend(t, "article-site-default")
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("close default Article site backend: %v", err)
		}
	})
	application, err := applicationForServe(context.Background(), backend, publicationConfig{})
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

func TestRunRejectsAuthenticatedNonLoopbackBeforeBackendOrListener(t *testing.T) {
	clearArticleDatabaseEnvironment(t)
	const secret = "non-loopback-secret-marker"
	t.Setenv(articleSQLiteDatabaseEnv, "file:never-opened.sqlite3")
	t.Setenv(articleAdminUsernameEnv, "admin")
	t.Setenv(articleAdminPasswordEnv, secret)
	var backendCalls atomic.Int32
	var listenCalls atomic.Int32
	openBackend := func(context.Context, databaseConfig) (articleBackend, error) {
		backendCalls.Add(1)
		return nil, nil
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
	if backendCalls.Load() != 0 || listenCalls.Load() != 0 {
		t.Fatalf("non-loopback startup side effects = backend %d listener %d", backendCalls.Load(), listenCalls.Load())
	}
	combined := err.Error() + stdout.String() + stderr.String()
	if strings.Contains(combined, secret) {
		t.Fatalf("non-loopback startup exposed password in %q", combined)
	}
}

func TestRunPublishesOptInAdminAndAPIAndCancelsCleanly(t *testing.T) {
	clearArticleDatabaseEnvironment(t)
	const (
		username = "development-admin"
		password = "publication-smoke-secret"
	)
	t.Setenv(articleSQLiteDatabaseEnv, "file:ignored-by-test-opener")
	t.Setenv(articleAdminUsernameEnv, username)
	t.Setenv(articleAdminPasswordEnv, password)

	backend := newArticleSiteTestBackend(t, "article-site-publication")
	t.Cleanup(func() { _ = backend.Close() })
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
	loginRequest, err := http.NewRequest(http.MethodPost, baseURL+"/admin/login/", strings.NewReader(values.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	loginRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginResponse, err := client.Do(loginRequest)
	if err != nil {
		t.Fatal(err)
	}
	loginBody := articleSiteReadBody(t, loginResponse)
	if loginResponse.StatusCode != http.StatusFound || loginResponse.Header.Get("Location") != "/admin/articles/" || loginBody != "Found\n" {
		t.Fatalf("Admin login = status %d location %q body %q", loginResponse.StatusCode, loginResponse.Header.Get("Location"), loginBody)
	}
	var sessionSecret string
	var csrfSecret string
	for _, cookie := range loginResponse.Cookies() {
		switch cookie.Name {
		case websessionauth.DefaultSessionCookieName:
			sessionSecret = cookie.Value
		case websessionauth.DefaultCSRFCookieName:
			csrfSecret = cookie.Value
		}
	}
	if sessionSecret == "" || csrfSecret == "" || csrfSecret == preLoginToken {
		t.Fatal("Admin login did not publish independent session and rotated CSRF cookies")
	}
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

func TestApplicationForServeRedactsRejectedPassword(t *testing.T) {
	backend := newArticleSiteTestBackend(t, "article-site-password-redaction")
	t.Cleanup(func() { _ = backend.Close() })
	const marker = "rejected-password-secret-marker"
	_, err := applicationForServe(context.Background(), backend, publicationConfig{
		authenticated: true,
		username:      "admin",
		password:      marker + strings.Repeat("x", 1100),
	})
	if err == nil {
		t.Fatal("oversized configured password error = nil")
	}
	if strings.Contains(err.Error(), marker) {
		t.Fatalf("applicationForServe() exposed rejected password: %v", err)
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
	openBackend := func(ctx context.Context, config databaseConfig) (articleBackend, error) {
		backend, err := openArticleBackend(ctx, config)
		if err != nil {
			return nil, err
		}
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
	if _, err := backend.ExecContext(context.Background(), `CREATE TABLE "godj_conformance_article" (
  "id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
  "title" VARCHAR(200) NOT NULL,
  "published" BOOLEAN NOT NULL,
  "summary" VARCHAR(200) NULL
)`); err != nil {
		_ = backend.Close()
		t.Fatal(err)
	}
	return backend
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
		articleAdminUsernameEnv,
		articleAdminPasswordEnv,
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
