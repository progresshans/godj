//go:build darwin || linux

package restart_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/systemstate"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

const (
	articleReadinessPrefix = "article site listening on http://"
	maximumResponseBytes   = 1 << 20
	phaseReadinessTimeout  = 45 * time.Second
	phaseShutdownTimeout   = 20 * time.Second
	requestTimeout         = 5 * time.Second
)

var (
	adminCSRFPattern     = regexp.MustCompile(`name="csrfmiddlewaretoken" value="([A-Za-z0-9_-]{128})"`)
	maskedCSRFPattern    = regexp.MustCompile(`^[A-Za-z0-9_-]{128}$`)
	sessionDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type restartDatabaseBackend interface {
	systemstate.Backend
	migrationbackend.RevisionFencedBackend
	Close() error
}

type restartBackendFactory func(context.Context) (restartDatabaseBackend, error)

type restartDatabaseProfile struct {
	siteEnvironment map[string]string
	sensitive       []string
	open            restartBackendFactory
	assertArtifacts func(*testing.T, context.Context, []string)
}

// TestSystemStateSQLiteDistinctProcessRestartSentinel is the Unix SQLite
// product sentinel for the durable Article composition root.
func TestSystemStateSQLiteDistinctProcessRestartSentinel(t *testing.T) {
	temporary := t.TempDir()
	databasePath := filepath.Join(temporary, "article-system-restart.sqlite3")
	dataSourceName := "file:" + filepath.ToSlash(databasePath) + "?mode=rwc"
	open := func(ctx context.Context) (restartDatabaseBackend, error) {
		return sqlite.Open(ctx, dataSourceName)
	}
	runSystemStateDistinctProcessRestartSentinel(t, restartDatabaseProfile{
		siteEnvironment: map[string]string{articleSQLiteDatabaseEnv: dataSourceName},
		open:            open,
		assertArtifacts: func(t *testing.T, _ context.Context, sensitive []string) {
			assertSQLiteArtifactsExcludeSensitive(t, databasePath, sensitive)
		},
	})
}

// TestSystemStatePostgresDistinctProcessRestartSentinel runs the exact same
// clean A/B/C process lifecycle against one isolated PostgreSQL schema. The
// parent test connection is closed before migration and each child phase.
func TestSystemStatePostgresDistinctProcessRestartSentinel(t *testing.T) {
	databaseURL := restartPostgresTestURL(t)
	schema := createRestartPostgresSchema(t, databaseURL)
	open := func(ctx context.Context) (restartDatabaseBackend, error) {
		backend, err := postgres.Open(ctx, postgres.Config{URL: databaseURL, Schema: schema})
		if err != nil {
			return nil, restartPostgresSafeError(err)
		}
		return backend, nil
	}
	runSystemStateDistinctProcessRestartSentinel(t, restartDatabaseProfile{
		siteEnvironment: map[string]string{
			articlePostgresURLEnv:    databaseURL,
			articlePostgresSchemaEnv: schema,
		},
		sensitive: restartPostgresSensitiveValues(t, databaseURL),
		open:      open,
		assertArtifacts: func(t *testing.T, ctx context.Context, sensitive []string) {
			assertPostgresArtifactsExcludeSensitive(t, ctx, open, sensitive)
		},
	})
}

func runSystemStateDistinctProcessRestartSentinel(t *testing.T, profile restartDatabaseProfile) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	repository := restartRepositoryRoot(t)
	temporary := t.TempDir()
	password := fmt.Sprintf("restart-password-%d-%d-7Vq", os.Getpid(), time.Now().UnixNano())
	const username = "restart-system-admin"

	explicitlyMigrateRestartDatabase(t, ctx, repository, profile.open)
	binaryPath, buildOutput := buildRestartArticleSite(t, ctx, repository, temporary)
	sensitive := append([]string{password}, profile.sensitive...)
	assertBytesExcludeSensitive(t, "site build output", buildOutput, sensitive)
	assertFileExcludesSensitive(t, "site binary", binaryPath, sensitive)

	explicitEnvironment := make(map[string]string, len(profile.siteEnvironment)+2)
	for name, value := range profile.siteEnvironment {
		explicitEnvironment[name] = value
	}
	explicitEnvironment[articleAdminUsernameEnv] = username
	explicitEnvironment[articleAdminPasswordEnv] = password
	environment := restartEnvironment(os.Environ(), explicitEnvironment)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal("create A/B browser cookie jar")
	}
	client := newRestartHTTPClient(jar)
	defer client.CloseIdleConnections()

	var copiedPreLogoutSession string
	var phaseACSRFCookie string
	var staleCSRF string
	phaseA, err := runRestartSitePhase(ctx, binaryPath, repository, environment, &sensitive, func(baseURL string) error {
		login, err := loginRestartSite(client, jar, baseURL, username, password, &sensitive)
		if err != nil {
			return err
		}
		copiedPreLogoutSession = login.sessionCookie
		phaseACSRFCookie = login.csrfCookie

		safe, err := requestRestartSite(client, http.MethodGet, baseURL+"/api/articles/", api.JSONContentType, "", "", "")
		if err != nil {
			return fmt.Errorf("phase A authenticated API: %w", err)
		}
		page, err := decodeArticlePage(safe)
		if err != nil || safe.status != http.StatusOK || safe.contentType != api.JSONContentType || page.Count != 0 || len(page.Results) != 0 {
			return fmt.Errorf("phase A authenticated API shape status=%d count=%d results=%d", safe.status, page.Count, len(page.Results))
		}
		staleCSRF = safe.header.Get(websessionauth.DefaultCSRFHeader)
		if !maskedCSRFPattern.MatchString(staleCSRF) {
			return errors.New("phase A API omitted a current masked CSRF token")
		}
		sensitive = append(sensitive, staleCSRF)

		addPage, err := requestRestartSite(client, http.MethodGet, baseURL+"/admin/articles/add/", "", "", "", "")
		if err != nil {
			return fmt.Errorf("phase A Admin add page: %w", err)
		}
		addToken, err := responseCSRFToken(addPage)
		if err != nil || addPage.status != http.StatusOK {
			return fmt.Errorf("phase A Admin add page status=%d token=%t", addPage.status, err == nil)
		}
		sensitive = append(sensitive, addToken)
		form := url.Values{
			"csrfmiddlewaretoken": {addToken},
			"title":               {"Process A audited"},
			"summary":             {"durable history"},
		}
		created, err := requestRestartSite(
			client,
			http.MethodPost,
			baseURL+"/admin/articles/add/",
			"",
			"application/x-www-form-urlencoded",
			form.Encode(),
			"",
		)
		if err != nil {
			return fmt.Errorf("phase A Admin create: %w", err)
		}
		if created.status != http.StatusFound || !strings.HasPrefix(created.header.Get("Location"), "/admin/articles/") {
			return fmt.Errorf("phase A Admin create status/location=%d/%t", created.status, strings.HasPrefix(created.header.Get("Location"), "/admin/articles/"))
		}
		parsed, err := url.Parse(baseURL)
		if err != nil {
			return errors.New("parse phase A base URL after mutation")
		}
		if got := restartJarCookie(jar, parsed, websessionauth.DefaultCSRFCookieName); got != phaseACSRFCookie {
			return errors.New("phase A browser changed the rotated CSRF cookie secret")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	client.CloseIdleConnections()
	stateA := inspectRestartDatabase(t, ctx, profile.open)
	assertRestartDatabaseState(t, "phase A", stateA, 1, 1, 1)
	if stateA.session == nil || !sessionDigestPattern.MatchString(stateA.session.digest) ||
		!strings.HasPrefix(stateA.session.payload, "v1.") {
		t.Fatalf("phase A durable session row has invalid digest/payload shape")
	}
	if stateA.audit == nil || stateA.audit.actorID != "article-development-admin" ||
		stateA.audit.model != "godj_conformance.article" || stateA.audit.objectID != "1" ||
		stateA.audit.action != "add" || stateA.audit.displayLabel != "Process A audited" ||
		!strings.HasPrefix(stateA.audit.changedFields, "v1.") {
		t.Fatalf("phase A durable audit row has invalid semantic shape: %+v", stateA.audit)
	}
	profile.assertArtifacts(t, ctx, sensitive)
	assertFileExcludesSensitive(t, "site binary after phase A", binaryPath, sensitive)

	phaseB, err := runRestartSitePhase(ctx, binaryPath, repository, environment, &sensitive, func(baseURL string) error {
		parsed, err := url.Parse(baseURL)
		if err != nil {
			return errors.New("parse phase B base URL")
		}
		if got := restartJarCookie(jar, parsed, websessionauth.DefaultSessionCookieName); got != copiedPreLogoutSession {
			return errors.New("phase B browser did not carry the phase A session cookie")
		}
		if got := restartJarCookie(jar, parsed, websessionauth.DefaultCSRFCookieName); got != phaseACSRFCookie {
			return errors.New("phase B stale POST did not begin with the byte-exact phase A CSRF cookie secret")
		}

		stale, err := requestRestartSite(
			client,
			http.MethodPost,
			baseURL+"/api/articles/",
			api.JSONContentType,
			api.JSONContentType,
			`{"title":"Must not persist"}`,
			staleCSRF,
		)
		if err != nil {
			return fmt.Errorf("phase B stale-CSRF API write: %w", err)
		}
		if stale.status != http.StatusForbidden || string(stale.body) != `{"code":"csrf_rejected","errors":[]}` {
			return fmt.Errorf("phase B stale-CSRF result status/body-shape=%d/%t", stale.status, string(stale.body) == `{"code":"csrf_rejected","errors":[]}`)
		}
		if got := restartJarCookie(jar, parsed, websessionauth.DefaultCSRFCookieName); got != phaseACSRFCookie {
			return errors.New("phase B stale POST changed the phase A CSRF cookie secret")
		}

		safe, err := requestRestartSite(client, http.MethodGet, baseURL+"/api/articles/", api.JSONContentType, "", "", "")
		if err != nil {
			return fmt.Errorf("phase B safe API read: %w", err)
		}
		page, err := decodeArticlePage(safe)
		if err != nil || safe.status != http.StatusOK || page.Count != 1 || len(page.Results) != 1 || page.Results[0].ID != 1 {
			return fmt.Errorf("phase B post-rejection API delta status=%d count=%d results=%d", safe.status, page.Count, len(page.Results))
		}
		freshCSRF := safe.header.Get(websessionauth.DefaultCSRFHeader)
		if !maskedCSRFPattern.MatchString(freshCSRF) || freshCSRF == staleCSRF {
			return errors.New("phase B safe GET did not publish a distinct current CSRF token")
		}
		phaseBCSRFCookie := restartJarCookie(jar, parsed, websessionauth.DefaultCSRFCookieName)
		if phaseBCSRFCookie == "" || phaseBCSRFCookie != phaseACSRFCookie {
			return errors.New("phase B safe GET did not retain the byte-exact phase A CSRF cookie secret")
		}
		sensitive = append(sensitive, freshCSRF, phaseBCSRFCookie)

		created, err := requestRestartSite(
			client,
			http.MethodPost,
			baseURL+"/api/articles/",
			api.JSONContentType,
			api.JSONContentType,
			`{"title":"Process B API"}`,
			freshCSRF,
		)
		if err != nil {
			return fmt.Errorf("phase B fresh-CSRF API write: %w", err)
		}
		var article struct {
			ID int64 `json:"id"`
		}
		if json.Unmarshal(created.body, &article) != nil || created.status != http.StatusCreated || article.ID != 2 {
			return fmt.Errorf("phase B fresh-CSRF create status/id=%d/%d", created.status, article.ID)
		}
		confirmed, err := requestRestartSite(client, http.MethodGet, baseURL+"/api/articles/", api.JSONContentType, "", "", "")
		if err != nil {
			return fmt.Errorf("phase B confirmed API read: %w", err)
		}
		confirmedPage, err := decodeArticlePage(confirmed)
		if err != nil || confirmed.status != http.StatusOK || confirmedPage.Count != 2 || len(confirmedPage.Results) != 2 {
			return fmt.Errorf("phase B accepted-write delta status=%d count=%d results=%d", confirmed.status, confirmedPage.Count, len(confirmedPage.Results))
		}
		if token := confirmed.header.Get(websessionauth.DefaultCSRFHeader); token != "" {
			sensitive = append(sensitive, token)
		}

		history, err := requestRestartSite(client, http.MethodGet, baseURL+"/admin/articles/history/?id=1", "", "", "", "")
		if err != nil {
			return fmt.Errorf("phase B durable Admin history: %w", err)
		}
		body := string(history.body)
		for _, marker := range []string{
			`data-admin-view="history"`,
			`data-object-id="1"`,
			`data-sequence="1" data-action="add" data-actor="article-development-admin"`,
			"Process A audited",
		} {
			if !strings.Contains(body, marker) {
				return fmt.Errorf("phase B durable Admin history omitted required marker shape")
			}
		}
		logoutToken, err := responseCSRFToken(history)
		if err != nil || history.status != http.StatusOK {
			return fmt.Errorf("phase B Admin history/logout token status=%d token=%t", history.status, err == nil)
		}
		sensitive = append(sensitive, logoutToken)
		logoutForm := url.Values{"csrfmiddlewaretoken": {logoutToken}}
		logout, err := requestRestartSite(
			client,
			http.MethodPost,
			baseURL+"/admin/logout/",
			"",
			"application/x-www-form-urlencoded",
			logoutForm.Encode(),
			"",
		)
		if err != nil {
			return fmt.Errorf("phase B logout: %w", err)
		}
		if logout.status != http.StatusFound || logout.header.Get("Location") != "/admin/login/" || string(logout.body) != "Found\n" {
			return fmt.Errorf("phase B logout status/location/body-shape=%d/%t/%t", logout.status, logout.header.Get("Location") == "/admin/login/", string(logout.body) == "Found\n")
		}
		if got := restartJarCookie(jar, parsed, websessionauth.DefaultSessionCookieName); got != "" {
			return errors.New("phase B logout retained a browser session cookie")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	client.CloseIdleConnections()
	stateB := inspectRestartDatabase(t, ctx, profile.open)
	assertRestartDatabaseState(t, "phase B", stateB, 2, 0, 1)
	if stateB.audit == nil || stateA.audit == nil || *stateB.audit != *stateA.audit {
		t.Fatalf("phase B did not preserve the exact phase A audit row")
	}
	profile.assertArtifacts(t, ctx, sensitive)
	assertFileExcludesSensitive(t, "site binary after phase B", binaryPath, sensitive)

	phaseC, err := runRestartSitePhase(ctx, binaryPath, repository, environment, &sensitive, func(baseURL string) error {
		copiedJar, err := cookiejar.New(nil)
		if err != nil {
			return errors.New("create phase C copied-cookie jar")
		}
		parsed, err := url.Parse(baseURL)
		if err != nil {
			return errors.New("parse phase C base URL")
		}
		copiedJar.SetCookies(parsed, []*http.Cookie{{
			Name:     websessionauth.DefaultSessionCookieName,
			Value:    copiedPreLogoutSession,
			Path:     "/",
			HttpOnly: true,
		}})
		copiedClient := newRestartHTTPClient(copiedJar)
		defer copiedClient.CloseIdleConnections()

		adminResult, err := requestRestartSite(copiedClient, http.MethodGet, baseURL+"/admin/articles/", "", "", "", "")
		if err != nil {
			return fmt.Errorf("phase C copied-cookie Admin: %w", err)
		}
		if adminResult.status != http.StatusFound || !strings.HasPrefix(adminResult.header.Get("Location"), "/admin/login/?next=") {
			return fmt.Errorf("phase C copied-cookie Admin status/location=%d/%t", adminResult.status, strings.HasPrefix(adminResult.header.Get("Location"), "/admin/login/?next="))
		}
		apiResult, err := requestRestartSite(copiedClient, http.MethodGet, baseURL+"/api/articles/", api.JSONContentType, "", "", "")
		if err != nil {
			return fmt.Errorf("phase C copied-cookie API: %w", err)
		}
		if apiResult.status != http.StatusForbidden || string(apiResult.body) != `{"code":"not_authenticated","errors":[]}` {
			return fmt.Errorf("phase C copied-cookie API status/body-shape=%d/%t", apiResult.status, string(apiResult.body) == `{"code":"not_authenticated","errors":[]}`)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	stateC := inspectRestartDatabase(t, ctx, profile.open)
	assertRestartDatabaseState(t, "phase C", stateC, 2, 0, 1)
	if stateC.audit == nil || stateB.audit == nil || *stateC.audit != *stateB.audit {
		t.Fatalf("phase C did not preserve the exact durable audit row after logout")
	}
	profile.assertArtifacts(t, ctx, sensitive)
	assertFileExcludesSensitive(t, "site binary after phase C", binaryPath, sensitive)

	pids := map[int]bool{phaseA.pid: true, phaseB.pid: true, phaseC.pid: true}
	if len(pids) != 3 || phaseA.pid == os.Getpid() || phaseB.pid == os.Getpid() || phaseC.pid == os.Getpid() {
		t.Fatalf("A/B/C did not execute in three distinct child PIDs: %d %d %d (test PID %d)", phaseA.pid, phaseB.pid, phaseC.pid, os.Getpid())
	}
}

type restartLoginState struct {
	sessionCookie string
	csrfCookie    string
}

func loginRestartSite(
	client *http.Client,
	jar http.CookieJar,
	baseURL, username, password string,
	sensitive *[]string,
) (restartLoginState, error) {
	loginPage, err := requestRestartSite(client, http.MethodGet, baseURL+"/admin/login/", "", "", "", "")
	if err != nil {
		return restartLoginState{}, fmt.Errorf("phase A Admin login page: %w", err)
	}
	if loginPage.status != http.StatusOK || loginPage.contentType != "text/html; charset=utf-8" {
		return restartLoginState{}, fmt.Errorf("phase A Admin login page status/content-type=%d/%t", loginPage.status, loginPage.contentType == "text/html; charset=utf-8")
	}
	preLoginToken, err := responseCSRFToken(loginPage)
	if err != nil {
		return restartLoginState{}, err
	}
	preLoginCSRF, err := uniqueRestartCookie(loginPage.cookies, websessionauth.DefaultCSRFCookieName)
	if err != nil {
		return restartLoginState{}, err
	}
	if !preLoginCSRF.HttpOnly || preLoginCSRF.Path != "/" || preLoginCSRF.Value == "" || preLoginCSRF.Value == preLoginToken {
		return restartLoginState{}, errors.New("phase A Admin login published an unsafe CSRF cookie")
	}
	*sensitive = append(*sensitive, preLoginToken, preLoginCSRF.Value)

	form := url.Values{
		"csrfmiddlewaretoken": {preLoginToken},
		"username":            {username},
		"password":            {password},
		"next":                {"/admin/articles/"},
	}
	login, err := requestRestartSite(
		client,
		http.MethodPost,
		baseURL+"/admin/login/",
		"",
		"application/x-www-form-urlencoded",
		form.Encode(),
		"",
	)
	if err != nil {
		return restartLoginState{}, fmt.Errorf("phase A Admin login: %w", err)
	}
	if login.status != http.StatusFound || login.header.Get("Location") != "/admin/articles/" || string(login.body) != "Found\n" {
		return restartLoginState{}, fmt.Errorf("phase A Admin login status/location/body-shape=%d/%t/%t", login.status, login.header.Get("Location") == "/admin/articles/", string(login.body) == "Found\n")
	}
	sessionCookie, err := uniqueRestartCookie(login.cookies, websessionauth.DefaultSessionCookieName)
	if err != nil {
		return restartLoginState{}, err
	}
	rotatedCSRF, err := uniqueRestartCookie(login.cookies, websessionauth.DefaultCSRFCookieName)
	if err != nil {
		return restartLoginState{}, err
	}
	if !sessionCookie.HttpOnly || sessionCookie.Path != "/" || sessionCookie.Value == "" ||
		!rotatedCSRF.HttpOnly || rotatedCSRF.Path != "/" || rotatedCSRF.Value == "" ||
		rotatedCSRF.Value == preLoginCSRF.Value || rotatedCSRF.Value == preLoginToken {
		return restartLoginState{}, errors.New("phase A Admin login did not rotate independent session and CSRF cookies")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return restartLoginState{}, errors.New("parse phase A base URL")
	}
	if restartJarCookie(jar, parsed, websessionauth.DefaultSessionCookieName) != sessionCookie.Value ||
		restartJarCookie(jar, parsed, websessionauth.DefaultCSRFCookieName) != rotatedCSRF.Value {
		return restartLoginState{}, errors.New("phase A browser did not retain rotated login cookies")
	}
	*sensitive = append(*sensitive, sessionCookie.Value, rotatedCSRF.Value)
	return restartLoginState{sessionCookie: sessionCookie.Value, csrfCookie: rotatedCSRF.Value}, nil
}

type restartHTTPResult struct {
	status      int
	contentType string
	header      http.Header
	cookies     []*http.Cookie
	body        []byte
}

func newRestartHTTPClient(jar http.CookieJar) *http.Client {
	return &http.Client{
		Jar:     jar,
		Timeout: requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func requestRestartSite(
	client *http.Client,
	method, target, accept, contentType, body, csrf string,
) (restartHTTPResult, error) {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request, err := http.NewRequest(method, target, reader)
	if err != nil {
		return restartHTTPResult{}, errors.New("construct restart HTTP request")
	}
	if accept != "" {
		request.Header.Set("Accept", accept)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if csrf != "" {
		request.Header.Set(websessionauth.DefaultCSRFHeader, csrf)
	}
	response, err := client.Do(request)
	if err != nil {
		return restartHTTPResult{}, fmt.Errorf("perform restart HTTP request: %w", err)
	}
	payload, readErr := io.ReadAll(io.LimitReader(response.Body, maximumResponseBytes+1))
	closeErr := response.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return restartHTTPResult{}, errors.New("read restart HTTP response")
	}
	if len(payload) > maximumResponseBytes {
		return restartHTTPResult{}, errors.New("restart HTTP response exceeded one mebibyte")
	}
	return restartHTTPResult{
		status:      response.StatusCode,
		contentType: response.Header.Get("Content-Type"),
		header:      response.Header.Clone(),
		cookies:     response.Cookies(),
		body:        payload,
	}, nil
}

func responseCSRFToken(response restartHTTPResult) (string, error) {
	match := adminCSRFPattern.FindSubmatch(response.body)
	if len(match) != 2 {
		return "", errors.New("Admin response omitted the masked CSRF token")
	}
	return string(match[1]), nil
}

func uniqueRestartCookie(cookies []*http.Cookie, name string) (*http.Cookie, error) {
	var found *http.Cookie
	for _, cookie := range cookies {
		if cookie.Name != name {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("response published duplicate %s cookies", name)
		}
		clone := *cookie
		found = &clone
	}
	if found == nil {
		return nil, fmt.Errorf("response omitted the %s cookie", name)
	}
	return found, nil
}

func restartJarCookie(jar http.CookieJar, target *url.URL, name string) string {
	for _, cookie := range jar.Cookies(target) {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

type articlePage struct {
	Count   int64 `json:"count"`
	Results []struct {
		ID int64 `json:"id"`
	} `json:"results"`
}

func decodeArticlePage(response restartHTTPResult) (articlePage, error) {
	var page articlePage
	if err := json.Unmarshal(response.body, &page); err != nil {
		return articlePage{}, errors.New("decode Article API page")
	}
	return page, nil
}

type restartPhaseResult struct {
	pid     int
	address string
}

func runRestartSitePhase(
	ctx context.Context,
	binaryPath, repository string,
	environment []string,
	sensitive *[]string,
	exercise func(string) error,
) (restartPhaseResult, error) {
	if exercise == nil {
		return restartPhaseResult{}, errors.New("restart phase exercise is nil")
	}
	stdout := &synchronizedBuffer{}
	stderr := &synchronizedBuffer{}
	command := exec.Command(binaryPath, "serve", "--listen", "127.0.0.1:0")
	command.Dir = repository
	command.Env = append([]string(nil), environment...)
	command.Stdout = stdout
	command.Stderr = stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.WaitDelay = 2 * time.Second
	if err := command.Start(); err != nil {
		return restartPhaseResult{}, fmt.Errorf("start Article site child: %w", err)
	}
	result := restartPhaseResult{pid: command.Process.Pid}
	waited := make(chan error, 1)
	go func() {
		waited <- command.Wait()
	}()
	finished := false
	defer func() {
		if !finished {
			forceStopRestartProcess(command, waited)
		}
	}()

	address, err := awaitRestartReadiness(ctx, stdout, stderr, waited, sensitive)
	if err != nil {
		forceStopRestartProcess(command, waited)
		finished = true
		return result, err
	}
	result.address = address
	if err := waitForRestartHTTP(address); err != nil {
		forceStopRestartProcess(command, waited)
		finished = true
		return result, err
	}
	exerciseErr := exercise("http://" + address)
	shutdownErr := cleanStopRestartProcess(command, waited)
	finished = true
	stdoutBytes := stdout.Bytes()
	stderrBytes := stderr.Bytes()
	if err := bytesExcludeSensitive(stdoutBytes, stderrBytes, sensitive); err != nil {
		return result, err
	}
	readinessLine := articleReadinessPrefix + address + "\n"
	if string(stdoutBytes) != readinessLine || len(stderrBytes) != 0 {
		return result, fmt.Errorf("Article site child output shape stdout=%d stderr=%d", len(stdoutBytes), len(stderrBytes))
	}
	if shutdownErr != nil {
		return result, shutdownErr
	}
	if err := verifyRestartListenerClosed(address); err != nil {
		return result, err
	}
	if exerciseErr != nil {
		return result, exerciseErr
	}
	return result, nil
}

func awaitRestartReadiness(
	ctx context.Context,
	stdout, stderr *synchronizedBuffer,
	waited <-chan error,
	sensitive *[]string,
) (string, error) {
	timer := time.NewTimer(phaseReadinessTimeout)
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if address, ready, err := parseRestartReadiness(stdout.String()); ready || err != nil {
			return address, err
		}
		select {
		case waitErr := <-waited:
			if err := bytesExcludeSensitive(stdout.Bytes(), stderr.Bytes(), sensitive); err != nil {
				return "", err
			}
			return "", fmt.Errorf("Article site child exited before readiness: %v; stdout=%d stderr=%d", waitErr, len(stdout.Bytes()), len(stderr.Bytes()))
		case <-ctx.Done():
			return "", fmt.Errorf("Article site readiness context: %w", ctx.Err())
		case <-timer.C:
			return "", fmt.Errorf("Article site readiness timed out; stdout=%d stderr=%d", len(stdout.Bytes()), len(stderr.Bytes()))
		case <-ticker.C:
		}
	}
}

func parseRestartReadiness(output string) (string, bool, error) {
	newline := strings.IndexByte(output, '\n')
	if newline < 0 {
		return "", false, nil
	}
	line := output[:newline]
	if !strings.HasPrefix(line, articleReadinessPrefix) {
		return "", true, errors.New("Article site published an invalid readiness prefix")
	}
	address := strings.TrimPrefix(line, articleReadinessPrefix)
	host, port, err := net.SplitHostPort(address)
	if err != nil || port == "0" || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return "", true, errors.New("Article site published an invalid readiness address")
	}
	return address, true, nil
}

func waitForRestartHTTP(address string) error {
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(5 * time.Second)
	for {
		response, err := client.Get("http://" + address + "/articles/")
		if err == nil {
			_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, maximumResponseBytes+1))
			closeErr := response.Body.Close()
			if response.StatusCode == http.StatusOK && readErr == nil && closeErr == nil {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return errors.New("Article site HTTP readiness timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func cleanStopRestartProcess(command *exec.Cmd, waited <-chan error) error {
	if err := syscall.Kill(-command.Process.Pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("signal Article site process group: %w", err)
	}
	timer := time.NewTimer(phaseShutdownTimeout)
	defer timer.Stop()
	select {
	case err := <-waited:
		if err != nil {
			return fmt.Errorf("Article site child did not exit cleanly: %w", err)
		}
		return nil
	case <-timer.C:
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		<-waited
		return errors.New("Article site child graceful shutdown timed out")
	}
}

func forceStopRestartProcess(command *exec.Cmd, waited <-chan error) {
	if command == nil || command.Process == nil {
		return
	}
	_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
	}
}

func verifyRestartListenerClosed(address string) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		connection, dialErr := net.DialTimeout("tcp4", address, 100*time.Millisecond)
		if dialErr == nil {
			_ = connection.Close()
		} else {
			listener, listenErr := net.Listen("tcp4", address)
			if listenErr == nil {
				return listener.Close()
			}
		}
		if time.Now().After(deadline) {
			return errors.New("Article site listener remained reachable or unavailable for reuse after child exit")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

type synchronizedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *synchronizedBuffer) Write(payload []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(payload)
}

func (buffer *synchronizedBuffer) Bytes() []byte {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return append([]byte(nil), buffer.buffer.Bytes()...)
}

func (buffer *synchronizedBuffer) String() string {
	return string(buffer.Bytes())
}

func buildRestartArticleSite(t *testing.T, ctx context.Context, repository, temporary string) (string, []byte) {
	t.Helper()
	binaryPath := filepath.Join(temporary, "article-site")
	buildCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(
		buildCtx,
		"go",
		"build",
		"-buildvcs=false",
		"-mod=readonly",
		"-o", binaryPath,
		"./examples/article/cmd/site",
	)
	command.Dir = repository
	command.Env = restartEnvironment(os.Environ(), map[string]string{
		"GOTOOLCHAIN": "local",
		"GOWORK":      "off",
	})
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build Article site binary: %v; output bytes=%d", err, len(output))
	}
	info, err := os.Stat(binaryPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		t.Fatalf("Article site build artifact = (%v,%v)", info, err)
	}
	return binaryPath, output
}

func explicitlyMigrateRestartDatabase(t *testing.T, ctx context.Context, repository string, openBackend restartBackendFactory) {
	t.Helper()
	document, err := os.ReadFile(filepath.Join(repository, "examples", "article", "testdata", "postgres", "0001_initial.godj.json"))
	if err != nil {
		t.Fatalf("read Article migration definition: %v", err)
	}
	loaded, report, err := migrationdefinition.Load(
		migrationdefinition.Source{
			SourceID: "examples/article/testdata/postgres/0001_initial.godj.json",
			Document: document,
		},
		systemstate.InitialDefinitionSource(),
	)
	if err != nil {
		t.Fatalf("load Article and system migration definitions: %v", err)
	}
	if report.DocumentsReceived != 2 || report.HeadersValidated != 2 || report.OperationsDecoded != 4 ||
		report.PlannerConstruction != 1 || report.DefinitionsPublished != 2 || report.DefinitionSetsPublished != 1 {
		t.Fatalf("combined restart definition report = %+v", report)
	}
	backend, err := openBackend(ctx)
	if err != nil {
		t.Fatalf("open restart migration database: %v", err)
	}
	open := true
	defer func() {
		if open {
			_ = backend.Close()
		}
	}()
	state, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
	if err != nil {
		t.Fatalf("explicitly migrate Article and system definitions: %v", err)
	}
	for _, app := range []string{"godj_conformance", systemstate.InitialMigrationKey().App} {
		if _, found := state.Schema(app); !found {
			t.Fatalf("migrated restart state omitted app %q", app)
		}
	}
	assertRestartMigrationHistory(t, ctx, backend)
	if err := backend.Close(); err != nil {
		t.Fatalf("close restart migration database: %v", err)
	}
	open = false
}

type restartDatabaseState struct {
	credentials int
	sessions    int
	audits      int
	articles    int
	session     *restartSessionRow
	audit       *restartAuditRow
}

type restartSessionRow struct {
	id      int64
	digest  string
	payload string
}

type restartAuditRow struct {
	id            int64
	actorID       string
	model         string
	objectID      string
	action        string
	changedFields string
	displayLabel  string
}

func inspectRestartDatabase(t *testing.T, ctx context.Context, openBackend restartBackendFactory) restartDatabaseState {
	t.Helper()
	backend, err := openBackend(ctx)
	if err != nil {
		t.Fatalf("open restart database inspection handle: %v", err)
	}
	state := restartDatabaseState{
		credentials: restartTableCount(t, ctx, backend, "godj_system_credential"),
		sessions:    restartTableCount(t, ctx, backend, "godj_system_session"),
		audits:      restartTableCount(t, ctx, backend, "godj_system_audit"),
		articles:    restartTableCount(t, ctx, backend, "godj_conformance_article"),
		session:     readRestartSession(t, ctx, backend),
		audit:       readRestartAudit(t, ctx, backend),
	}
	assertRestartMigrationHistory(t, ctx, backend)
	if err := backend.Close(); err != nil {
		t.Fatalf("close restart database inspection handle: %v", err)
	}
	return state
}

func assertRestartDatabaseState(
	t *testing.T,
	phase string,
	state restartDatabaseState,
	articles, sessions, audits int,
) {
	t.Helper()
	if state.credentials != 1 || state.articles != articles || state.sessions != sessions || state.audits != audits {
		t.Fatalf(
			"%s database counts credential/article/session/audit = %d/%d/%d/%d, want 1/%d/%d/%d",
			phase,
			state.credentials,
			state.articles,
			state.sessions,
			state.audits,
			articles,
			sessions,
			audits,
		)
	}
}

func restartTableCount(t *testing.T, ctx context.Context, backend db.Queryer, table string) int {
	t.Helper()
	identifier := query.NewFieldRef("id", "id", query.FieldInteger, false)
	rows, err := backend.Query(ctx, query.NewPlan(table, []query.FieldRef{identifier}))
	if err != nil {
		t.Fatalf("query restart table %q: %v", table, err)
	}
	count := 0
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			t.Fatalf("scan restart table %q: %v", table, err)
		}
		count++
	}
	iterationErr := rows.Err()
	closeErr := rows.Close()
	if err := errors.Join(iterationErr, closeErr); err != nil {
		t.Fatalf("finish restart table %q: %v", table, err)
	}
	return count
}

func readRestartSession(t *testing.T, ctx context.Context, backend db.Queryer) *restartSessionRow {
	t.Helper()
	fields := []query.FieldRef{
		query.NewFieldRef("id", "id", query.FieldInteger, false),
		query.NewFieldRef("digest", "digest", query.FieldString, false),
		query.NewFieldRef("payload", "payload", query.FieldString, false),
	}
	rows, err := backend.Query(ctx, query.NewPlan("godj_system_session", fields))
	if err != nil {
		t.Fatalf("query restart session rows: %v", err)
	}
	var found *restartSessionRow
	for rows.Next() {
		if found != nil {
			_ = rows.Close()
			t.Fatal("restart session query returned more than one row")
		}
		var row restartSessionRow
		if err := rows.Scan(&row.id, &row.digest, &row.payload); err != nil {
			_ = rows.Close()
			t.Fatalf("scan restart session row: %v", err)
		}
		found = &row
	}
	iterationErr := rows.Err()
	closeErr := rows.Close()
	if err := errors.Join(iterationErr, closeErr); err != nil {
		t.Fatalf("finish restart session rows: %v", err)
	}
	return found
}

func readRestartAudit(t *testing.T, ctx context.Context, backend db.Queryer) *restartAuditRow {
	t.Helper()
	fields := []query.FieldRef{
		query.NewFieldRef("id", "id", query.FieldInteger, false),
		query.NewFieldRef("actor_id", "actor_id", query.FieldString, false),
		query.NewFieldRef("model", "model", query.FieldString, false),
		query.NewFieldRef("object_id", "object_id", query.FieldString, false),
		query.NewFieldRef("action", "action", query.FieldString, false),
		query.NewFieldRef("changed_fields", "changed_fields", query.FieldString, false),
		query.NewFieldRef("display_label", "display_label", query.FieldString, false),
	}
	rows, err := backend.Query(ctx, query.NewPlan("godj_system_audit", fields))
	if err != nil {
		t.Fatalf("query restart audit rows: %v", err)
	}
	var found *restartAuditRow
	for rows.Next() {
		if found != nil {
			_ = rows.Close()
			t.Fatal("restart audit query returned more than one row")
		}
		var row restartAuditRow
		if err := rows.Scan(
			&row.id,
			&row.actorID,
			&row.model,
			&row.objectID,
			&row.action,
			&row.changedFields,
			&row.displayLabel,
		); err != nil {
			_ = rows.Close()
			t.Fatalf("scan restart audit row: %v", err)
		}
		found = &row
	}
	iterationErr := rows.Err()
	closeErr := rows.Close()
	if err := errors.Join(iterationErr, closeErr); err != nil {
		t.Fatalf("finish restart audit rows: %v", err)
	}
	return found
}

func assertRestartMigrationHistory(t *testing.T, ctx context.Context, backend migrationbackend.AppliedMigrationReader) {
	t.Helper()
	history, err := backend.ReadAppliedMigrations(ctx)
	if err != nil {
		t.Fatalf("read restart migration history: %v", err)
	}
	want := map[migrations.MigrationKey]bool{
		{App: "godj_conformance", Name: "0001_initial"}: true,
		systemstate.InitialMigrationKey():               true,
	}
	if len(history) != len(want) {
		t.Fatalf("restart migration history has %d entries, want exactly two", len(history))
	}
	for _, applied := range history {
		key := migrations.MigrationKey{App: applied.App, Name: applied.Name}
		if !want[key] {
			t.Fatalf("restart migration history has unexpected key %+v", key)
		}
		delete(want, key)
	}
	if len(want) != 0 {
		t.Fatalf("restart migration history omitted %d required keys", len(want))
	}
}

func restartRepositoryRoot(t *testing.T) string {
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
			t.Fatal("cannot locate repository root from restart conformance test")
		}
		directory = parent
	}
}

func restartPostgresTestURL(t *testing.T) string {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv(postgresTestURLEnv))
	if databaseURL != "" {
		return databaseURL
	}
	if os.Getenv(postgresRequiredEnv) == "1" {
		t.Fatalf("%s=1 requires %s", postgresRequiredEnv, postgresTestURLEnv)
	}
	t.Skip("GODJ_TEST_POSTGRES_URL is not configured; system-state PostgreSQL distinct-process restart was not run")
	return ""
}

func createRestartPostgresSchema(t *testing.T, databaseURL string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect restart PostgreSQL database: %v", restartPostgresSafeError(err))
	}
	schema := fmt.Sprintf("godj_systemstate_restart_%d_%d", os.Getpid(), time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("create isolated restart PostgreSQL schema: %v", restartPostgresSafeError(err))
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		cleanup, err := pgx.Connect(cleanupCtx, databaseURL)
		if err != nil {
			t.Errorf("connect PostgreSQL schema cleanup: %v", restartPostgresSafeError(err))
			return
		}
		if _, err := cleanup.Exec(cleanupCtx, "DROP SCHEMA "+quotedSchema+" CASCADE"); err != nil {
			t.Errorf("drop isolated restart PostgreSQL schema: %v", restartPostgresSafeError(err))
		}
		if err := cleanup.Close(cleanupCtx); err != nil {
			t.Errorf("close PostgreSQL schema cleanup: %v", restartPostgresSafeError(err))
		}
	})
	if err := admin.Close(ctx); err != nil {
		t.Fatalf("close PostgreSQL schema creation handle: %v", restartPostgresSafeError(err))
	}
	return schema
}

func restartPostgresSensitiveValues(t *testing.T, databaseURL string) []string {
	t.Helper()
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal("parse restart PostgreSQL URL: database URL is invalid")
	}
	values := []string{databaseURL}
	// Very short substrings cannot be distinguished reliably from ordinary
	// binary/database bytes. Hosted PostgreSQL credentials exceed this bound;
	// all child success output is additionally constrained to one readiness line.
	if len(config.Password) >= 4 {
		values = append(values, config.Password)
	}
	return values
}

func restartPostgresSafeError(err error) error {
	if err == nil {
		return nil
	}
	var structured interface{ SQLState() string }
	if errors.As(err, &structured) {
		return fmt.Errorf("PostgreSQL SQLSTATE %s", structured.SQLState())
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("PostgreSQL operation timed out")
	}
	if errors.Is(err, context.Canceled) {
		return errors.New("PostgreSQL operation was canceled")
	}
	return errors.New("PostgreSQL operation failed")
}

func assertPostgresArtifactsExcludeSensitive(
	t *testing.T,
	ctx context.Context,
	openBackend restartBackendFactory,
	sensitive []string,
) {
	t.Helper()
	backend, err := openBackend(ctx)
	if err != nil {
		t.Fatalf("open PostgreSQL artifact inspection handle: %v", restartPostgresSafeError(err))
	}
	open := true
	defer func() {
		if open {
			_ = backend.Close()
		}
	}()
	columns := map[string][]string{
		"godj_system_credential": {
			"principal_id",
			"username",
			"encoded_password",
			"permissions",
			"definition_digest",
		},
		"godj_system_session": {"digest", "payload"},
		"godj_system_audit": {
			"actor_id",
			"model",
			"object_id",
			"action",
			"changed_fields",
			"display_label",
		},
	}
	for table, names := range columns {
		for _, name := range names {
			for _, value := range restartTextColumnValues(t, ctx, backend, table, name) {
				for _, secret := range sensitive {
					if secret != "" && strings.Contains(value, secret) {
						t.Fatal("PostgreSQL restart artifact exposed a sensitive value")
					}
				}
			}
		}
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("close PostgreSQL artifact inspection handle: %v", restartPostgresSafeError(err))
	}
	open = false
}

func restartTextColumnValues(
	t *testing.T,
	ctx context.Context,
	queryer db.Queryer,
	table, column string,
) []string {
	t.Helper()
	field := query.NewFieldRef(column, column, query.FieldString, false)
	plan, err := query.NewPlan(table, []query.FieldRef{field}).WithLimit(4)
	if err != nil {
		t.Fatalf("build PostgreSQL artifact query: %v", restartPostgresSafeError(err))
	}
	rows, err := queryer.Query(ctx, plan)
	if err != nil {
		if rows != nil {
			_ = rows.Close()
		}
		t.Fatalf("query PostgreSQL restart artifact: %v", restartPostgresSafeError(err))
	}
	if rows == nil {
		t.Fatal("query PostgreSQL restart artifact returned nil rows")
	}
	values := make([]string, 0, 4)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			_ = rows.Close()
			t.Fatalf("scan PostgreSQL restart artifact: %v", restartPostgresSafeError(err))
		}
		values = append(values, value)
		if len(values) > 4 {
			_ = rows.Close()
			t.Fatal("PostgreSQL restart artifact exceeded the bounded query")
		}
	}
	iterationErr := rows.Err()
	closeErr := rows.Close()
	if err := errors.Join(iterationErr, closeErr); err != nil {
		t.Fatalf("finish PostgreSQL restart artifact query: %v", restartPostgresSafeError(err))
	}
	return values
}

func assertBytesExcludeSensitive(t *testing.T, label string, payload []byte, sensitive []string) {
	t.Helper()
	for _, value := range sensitive {
		if value != "" && bytes.Contains(payload, []byte(value)) {
			t.Fatalf("%s exposed a sensitive value", label)
		}
	}
}

func bytesExcludeSensitive(stdout, stderr []byte, sensitive *[]string) error {
	if sensitive == nil {
		return nil
	}
	combined := append(append([]byte(nil), stdout...), stderr...)
	for _, value := range *sensitive {
		if value != "" && bytes.Contains(combined, []byte(value)) {
			return errors.New("Article site child output exposed a sensitive value")
		}
	}
	return nil
}

func assertFileExcludesSensitive(t *testing.T, label, path string, sensitive []string) {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s for sensitive-value scan: %v", label, err)
	}
	assertBytesExcludeSensitive(t, label, payload, sensitive)
}

func assertSQLiteArtifactsExcludeSensitive(t *testing.T, databasePath string, sensitive []string) {
	t.Helper()
	paths, err := filepath.Glob(databasePath + "*")
	if err != nil {
		t.Fatalf("enumerate SQLite artifacts: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("SQLite restart database artifact is missing")
	}
	for _, path := range paths {
		assertFileExcludesSensitive(t, "SQLite restart artifact", path, sensitive)
	}
}
