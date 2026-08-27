//go:build darwin || linux

package projectmigrateproduct_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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
	"syscall"
	"testing"
	"time"

	"github.com/progresshans/godj/api"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

const authenticatedRestartMaximumResponseBytes = 1 << 20

var (
	authenticatedRestartCSRFPattern       = regexp.MustCompile(`name="csrfmiddlewaretoken" value="([A-Za-z0-9_-]{128})"`)
	authenticatedRestartMaskedCSRFPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{128}$`)
)

func TestGlobalMigrateAuthenticatedArticleRestartDurability(t *testing.T) {
	repository := repositoryRoot(t)
	descriptor := filepath.Join(repository, "examples", "article", "godj.toml")
	databaseDirectory := t.TempDir()
	databasePath := filepath.Join(databaseDirectory, "authenticated-restart.sqlite3")
	workspaceBase := newWorkspaceBase(t)
	globalBinary := buildGlobalGodj(t, repository)
	expectedCatalog := expectedArticleCatalog(t, repository)

	const username = "authenticated-restart-admin"
	password := fmt.Sprintf("authenticated-restart-password-%d-%d-7Vq", os.Getpid(), time.Now().UnixNano())
	values := environmentMap(articleEnvironment(t, databasePath, workspaceBase))
	values[articleAdminUsernameEnv] = username
	values[articleAdminPasswordEnv] = password
	environment := sortedEnvironment(values)
	sensitive := []string{password}
	outputCanaries := []string{username, databasePath}

	migration := runMigrate(t, globalBinary, repository, descriptor, environment)
	assertMigrateSuccess(t, migration, expectedCatalog, append(append([]string(nil), sensitive...), outputCanaries...)...)
	assertWorkspaceEmpty(t, workspaceBase)
	assertLatestDatabase(t, databasePath, expectedCatalog, "")
	authenticatedRestartAssertMigratedSystemStateEmpty(t, authenticatedRestartInspectDatabase(t, databasePath))
	authenticatedRestartAssertArtifactsExcludeSensitive(t, databaseDirectory, sensitive)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal("create authenticated restart cookie jar")
	}
	client := authenticatedRestartHTTPClient(jar)
	t.Cleanup(client.CloseIdleConnections)

	var phaseAState authenticatedRestartPhaseAState
	phaseA := authenticatedRestartRunServer(
		t,
		globalBinary,
		repository,
		descriptor,
		reserveLoopbackAddress(t, ""),
		environment,
		&sensitive,
		outputCanaries,
		func(baseURL string) error {
			var phaseErr error
			phaseAState, phaseErr = authenticatedRestartExercisePhaseA(
				client,
				jar,
				baseURL,
				username,
				password,
				&sensitive,
			)
			return phaseErr
		},
	)
	assertWorkspaceEmpty(t, workspaceBase)
	phaseASnapshot := authenticatedRestartInspectDatabase(t, databasePath)
	authenticatedRestartAssertPhaseAState(t, phaseASnapshot, username, password, phaseAState, sensitive)
	authenticatedRestartAssertArtifactsExcludeSensitive(t, databaseDirectory, sensitive)

	phaseB := authenticatedRestartRunServer(
		t,
		globalBinary,
		repository,
		descriptor,
		reserveLoopbackAddress(t, ""),
		environment,
		&sensitive,
		outputCanaries,
		func(baseURL string) error {
			return authenticatedRestartExercisePhaseB(client, jar, baseURL, phaseAState, &sensitive)
		},
	)
	assertWorkspaceEmpty(t, workspaceBase)
	if phaseA.PID == phaseB.PID || phaseA.PID == os.Getpid() || phaseB.PID == os.Getpid() {
		t.Fatalf("authenticated restart did not use two distinct global children: phase_a=%d phase_b=%d test=%d", phaseA.PID, phaseB.PID, os.Getpid())
	}
	phaseBSnapshot := authenticatedRestartInspectDatabase(t, databasePath)
	authenticatedRestartAssertCredentialUnchanged(t, phaseASnapshot.Credential, phaseBSnapshot.Credential)
	authenticatedRestartAssertPhaseBState(t, phaseBSnapshot, username, password, phaseAState, sensitive)
	authenticatedRestartAssertArtifactsExcludeSensitive(t, databaseDirectory, sensitive)
}

type authenticatedRestartPhaseAState struct {
	SessionCookie string
	CSRFCookie    string
	StaleCSRF     string
}

type authenticatedRestartLoginState struct {
	SessionCookie string
	CSRFCookie    string
}

type authenticatedRestartPhaseResult struct {
	PID           int
	ProcessGroups []int
}

type authenticatedRestartHTTPResult struct {
	Status      int
	ContentType string
	Header      http.Header
	Cookies     []*http.Cookie
	Body        []byte
}

type authenticatedRestartArticle struct {
	ID        int64   `json:"id"`
	Title     string  `json:"title"`
	Published bool    `json:"published"`
	Summary   *string `json:"summary"`
}

type authenticatedRestartArticlePage struct {
	Count    int64                         `json:"count"`
	Next     *string                       `json:"next"`
	Previous *string                       `json:"previous"`
	Results  []authenticatedRestartArticle `json:"results"`
}

type authenticatedRestartPersistedArticle struct {
	ID        int64
	Title     string
	Published bool
	Summary   sql.NullString
}

type authenticatedRestartSessionRow struct {
	Digest  string
	Payload string
}

type authenticatedRestartAuditRow struct {
	Sequence      int64
	ActorID       string
	Model         string
	ObjectID      string
	Action        string
	ChangedFields string
	DisplayLabel  string
}

type authenticatedRestartCredentialRow struct {
	ID               int64
	PrincipalID      string
	Username         string
	EncodedPassword  string
	Active           bool
	Permissions      string
	DefinitionDigest string
}

type authenticatedRestartDatabaseSnapshot struct {
	Articles   []authenticatedRestartPersistedArticle
	Sessions   []authenticatedRestartSessionRow
	Audits     []authenticatedRestartAuditRow
	Credential []authenticatedRestartCredentialRow
}

func authenticatedRestartExercisePhaseA(
	client *http.Client,
	jar http.CookieJar,
	baseURL, username, password string,
	sensitive *[]string,
) (authenticatedRestartPhaseAState, error) {
	login, err := authenticatedRestartLogin(client, jar, baseURL, username, password, sensitive)
	if err != nil {
		return authenticatedRestartPhaseAState{}, err
	}

	safe, err := authenticatedRestartRequest(client, http.MethodGet, baseURL+"/api/articles/", api.JSONContentType, "", "", "")
	if err != nil {
		return authenticatedRestartPhaseAState{}, fmt.Errorf("phase A authenticated API read: %w", err)
	}
	page, err := authenticatedRestartDecodePage(safe)
	if err != nil || safe.Status != http.StatusOK || safe.ContentType != api.JSONContentType || page.Count != 0 || len(page.Results) != 0 {
		return authenticatedRestartPhaseAState{}, fmt.Errorf("phase A initial API shape status/json/count/results=%d/%t/%d/%d", safe.Status, err == nil, page.Count, len(page.Results))
	}
	csrf := safe.Header.Get(websessionauth.DefaultCSRFHeader)
	if !authenticatedRestartMaskedCSRFPattern.MatchString(csrf) {
		return authenticatedRestartPhaseAState{}, errors.New("phase A API omitted a bounded masked CSRF token")
	}
	*sensitive = append(*sensitive, csrf)

	created, err := authenticatedRestartRequest(
		client,
		http.MethodPost,
		baseURL+"/api/articles/",
		api.JSONContentType,
		api.JSONContentType,
		`{"title":"API before restart","published":true,"summary":"api-created"}`,
		csrf,
	)
	if err != nil {
		return authenticatedRestartPhaseAState{}, fmt.Errorf("phase A API create: %w", err)
	}
	if err := authenticatedRestartRequireArticle(created, http.StatusCreated, authenticatedRestartArticle{
		ID: 1, Title: "API before restart", Published: true, Summary: authenticatedRestartString("api-created"),
	}); err != nil || created.Header.Get("Location") != "/api/articles/1/" {
		return authenticatedRestartPhaseAState{}, fmt.Errorf("phase A API create shape/location=%t/%t", err == nil, created.Header.Get("Location") == "/api/articles/1/")
	}

	updated, err := authenticatedRestartRequest(
		client,
		http.MethodPut,
		baseURL+"/api/articles/1/",
		api.JSONContentType,
		api.JSONContentType,
		`{"title":"API durable","published":true,"summary":"api-updated"}`,
		csrf,
	)
	if err != nil {
		return authenticatedRestartPhaseAState{}, fmt.Errorf("phase A API update: %w", err)
	}
	if err := authenticatedRestartRequireArticle(updated, http.StatusOK, authenticatedRestartArticle{
		ID: 1, Title: "API durable", Published: true, Summary: authenticatedRestartString("api-updated"),
	}); err != nil {
		return authenticatedRestartPhaseAState{}, fmt.Errorf("phase A API update shape: %w", err)
	}

	disposable, err := authenticatedRestartRequest(
		client,
		http.MethodPost,
		baseURL+"/api/articles/",
		api.JSONContentType,
		api.JSONContentType,
		`{"title":"API disposable","published":false}`,
		csrf,
	)
	if err != nil {
		return authenticatedRestartPhaseAState{}, fmt.Errorf("phase A second API create: %w", err)
	}
	if err := authenticatedRestartRequireArticle(disposable, http.StatusCreated, authenticatedRestartArticle{ID: 2, Title: "API disposable"}); err != nil {
		return authenticatedRestartPhaseAState{}, fmt.Errorf("phase A second API create shape: %w", err)
	}
	deleted, err := authenticatedRestartRequest(client, http.MethodDelete, baseURL+"/api/articles/2/", api.JSONContentType, "", "", csrf)
	if err != nil {
		return authenticatedRestartPhaseAState{}, fmt.Errorf("phase A API delete: %w", err)
	}
	if deleted.Status != http.StatusNoContent || len(deleted.Body) != 0 {
		return authenticatedRestartPhaseAState{}, fmt.Errorf("phase A API delete status/body-bytes=%d/%d", deleted.Status, len(deleted.Body))
	}

	if err := authenticatedRestartAdminCreate(
		client,
		baseURL,
		"Admin before restart",
		"admin-created",
		false,
		sensitive,
	); err != nil {
		return authenticatedRestartPhaseAState{}, fmt.Errorf("phase A Admin create durable row: %w", err)
	}
	if err := authenticatedRestartAdminUpdate(
		client,
		baseURL,
		3,
		"Admin durable",
		"admin-updated",
		true,
		sensitive,
	); err != nil {
		return authenticatedRestartPhaseAState{}, fmt.Errorf("phase A Admin update durable row: %w", err)
	}
	if err := authenticatedRestartAdminCreate(
		client,
		baseURL,
		"Admin disposable",
		"delete-me",
		false,
		sensitive,
	); err != nil {
		return authenticatedRestartPhaseAState{}, fmt.Errorf("phase A Admin create disposable row: %w", err)
	}
	if err := authenticatedRestartAdminDelete(client, baseURL, 4, sensitive); err != nil {
		return authenticatedRestartPhaseAState{}, fmt.Errorf("phase A Admin delete: %w", err)
	}

	if err := authenticatedRestartRequireHistory(
		client,
		baseURL,
		3,
		[]string{
			`data-sequence="1" data-action="add" data-actor="article-development-admin"`,
			`data-sequence="2" data-action="change" data-actor="article-development-admin"`,
		},
		sensitive,
	); err != nil {
		return authenticatedRestartPhaseAState{}, fmt.Errorf("phase A durable-row Admin history: %w", err)
	}
	if err := authenticatedRestartRequireHistory(
		client,
		baseURL,
		4,
		[]string{
			`data-sequence="3" data-action="add" data-actor="article-development-admin"`,
			`data-sequence="4" data-action="delete" data-actor="article-development-admin"`,
		},
		sensitive,
	); err != nil {
		return authenticatedRestartPhaseAState{}, fmt.Errorf("phase A deleted-row Admin history: %w", err)
	}

	if err := authenticatedRestartRequireAPIDetail(client, baseURL, authenticatedRestartArticle{
		ID: 1, Title: "API durable", Published: true, Summary: authenticatedRestartString("api-updated"),
	}); err != nil {
		return authenticatedRestartPhaseAState{}, fmt.Errorf("phase A API durable detail: %w", err)
	}
	if err := authenticatedRestartRequireAPIDetail(client, baseURL, authenticatedRestartArticle{
		ID: 3, Title: "Admin durable", Published: true, Summary: authenticatedRestartString("admin-updated"),
	}); err != nil {
		return authenticatedRestartPhaseAState{}, fmt.Errorf("phase A Admin durable detail through API: %w", err)
	}
	listed, err := authenticatedRestartRequest(client, http.MethodGet, baseURL+"/api/articles/", api.JSONContentType, "", "", "")
	if err != nil {
		return authenticatedRestartPhaseAState{}, fmt.Errorf("phase A final API list: %w", err)
	}
	finalPage, err := authenticatedRestartDecodePage(listed)
	if err != nil || listed.Status != http.StatusOK || finalPage.Count != 2 || len(finalPage.Results) != 2 ||
		finalPage.Results[0].ID != 1 || finalPage.Results[1].ID != 3 {
		return authenticatedRestartPhaseAState{}, fmt.Errorf("phase A final API list shape status/json/count/results=%d/%t/%d/%d", listed.Status, err == nil, finalPage.Count, len(finalPage.Results))
	}
	if token := listed.Header.Get(websessionauth.DefaultCSRFHeader); token != "" {
		*sensitive = append(*sensitive, token)
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return authenticatedRestartPhaseAState{}, errors.New("parse phase A base URL after CRUD")
	}
	if authenticatedRestartJarCookie(jar, parsed, websessionauth.DefaultSessionCookieName) != login.SessionCookie {
		return authenticatedRestartPhaseAState{}, errors.New("phase A browser session cookie changed after CRUD")
	}
	if authenticatedRestartJarCookie(jar, parsed, websessionauth.DefaultCSRFCookieName) != login.CSRFCookie {
		return authenticatedRestartPhaseAState{}, errors.New("phase A browser CSRF cookie changed after CRUD")
	}
	return authenticatedRestartPhaseAState{
		SessionCookie: login.SessionCookie,
		CSRFCookie:    login.CSRFCookie,
		StaleCSRF:     csrf,
	}, nil
}

func authenticatedRestartExercisePhaseB(
	client *http.Client,
	jar http.CookieJar,
	baseURL string,
	phaseA authenticatedRestartPhaseAState,
	sensitive *[]string,
) error {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return errors.New("parse phase B base URL")
	}
	if authenticatedRestartJarCookie(jar, parsed, websessionauth.DefaultSessionCookieName) != phaseA.SessionCookie {
		return errors.New("phase B browser did not carry the byte-identical phase A session cookie")
	}
	if authenticatedRestartJarCookie(jar, parsed, websessionauth.DefaultCSRFCookieName) != phaseA.CSRFCookie {
		return errors.New("phase B browser did not carry the byte-identical phase A CSRF cookie")
	}
	stale, err := authenticatedRestartRequest(
		client,
		http.MethodPost,
		baseURL+"/api/articles/",
		api.JSONContentType,
		api.JSONContentType,
		`{"title":"Must not persist"}`,
		phaseA.StaleCSRF,
	)
	if err != nil {
		return fmt.Errorf("phase B stale-CSRF API write: %w", err)
	}
	if stale.Status != http.StatusForbidden || !bytes.Equal(stale.Body, []byte(`{"code":"csrf_rejected","errors":[]}`)) {
		return fmt.Errorf("phase B stale-CSRF status/body-shape=%d/%t", stale.Status, bytes.Equal(stale.Body, []byte(`{"code":"csrf_rejected","errors":[]}`)))
	}
	if authenticatedRestartJarCookie(jar, parsed, websessionauth.DefaultCSRFCookieName) != phaseA.CSRFCookie {
		return errors.New("phase B stale-CSRF rejection changed the carried phase A CSRF cookie")
	}

	safe, err := authenticatedRestartRequest(client, http.MethodGet, baseURL+"/api/articles/", api.JSONContentType, "", "", "")
	if err != nil {
		return fmt.Errorf("phase B durable-session API read: %w", err)
	}
	page, err := authenticatedRestartDecodePage(safe)
	if err != nil || safe.Status != http.StatusOK || page.Count != 2 || len(page.Results) != 2 ||
		page.Results[0].ID != 1 || page.Results[1].ID != 3 {
		return fmt.Errorf("phase B durable API list shape status/json/count/results=%d/%t/%d/%d", safe.Status, err == nil, page.Count, len(page.Results))
	}
	freshCSRF := safe.Header.Get(websessionauth.DefaultCSRFHeader)
	if !authenticatedRestartMaskedCSRFPattern.MatchString(freshCSRF) || freshCSRF == phaseA.StaleCSRF {
		return errors.New("phase B safe API read did not publish a distinct bounded CSRF token")
	}
	*sensitive = append(*sensitive, freshCSRF)
	if current := authenticatedRestartJarCookie(jar, parsed, websessionauth.DefaultSessionCookieName); current != phaseA.SessionCookie {
		return errors.New("phase B durable session cookie was not byte-identical after safe read")
	}
	if current := authenticatedRestartJarCookie(jar, parsed, websessionauth.DefaultCSRFCookieName); current != phaseA.CSRFCookie {
		return errors.New("phase B safe read changed the carried phase A CSRF cookie")
	}

	if err := authenticatedRestartRequireHistory(
		client,
		baseURL,
		3,
		[]string{
			`data-sequence="1" data-action="add" data-actor="article-development-admin"`,
			`data-sequence="2" data-action="change" data-actor="article-development-admin"`,
			"Admin durable",
		},
		sensitive,
	); err != nil {
		return fmt.Errorf("phase B restarted Admin history: %w", err)
	}
	if err := authenticatedRestartAdminUpdate(
		client,
		baseURL,
		3,
		"Admin after restart",
		"admin-restarted",
		true,
		sensitive,
	); err != nil {
		return fmt.Errorf("phase B Admin update: %w", err)
	}

	created, err := authenticatedRestartRequest(
		client,
		http.MethodPost,
		baseURL+"/api/articles/",
		api.JSONContentType,
		api.JSONContentType,
		`{"title":"API after restart","published":false,"summary":"session survived"}`,
		freshCSRF,
	)
	if err != nil {
		return fmt.Errorf("phase B API create: %w", err)
	}
	if err := authenticatedRestartRequireArticle(created, http.StatusCreated, authenticatedRestartArticle{
		ID: 5, Title: "API after restart", Summary: authenticatedRestartString("session survived"),
	}); err != nil || created.Header.Get("Location") != "/api/articles/5/" {
		return fmt.Errorf("phase B API create shape/location=%t/%t", err == nil, created.Header.Get("Location") == "/api/articles/5/")
	}
	for _, want := range []authenticatedRestartArticle{
		{ID: 1, Title: "API durable", Published: true, Summary: authenticatedRestartString("api-updated")},
		{ID: 3, Title: "Admin after restart", Published: true, Summary: authenticatedRestartString("admin-restarted")},
		{ID: 5, Title: "API after restart", Summary: authenticatedRestartString("session survived")},
	} {
		if err := authenticatedRestartRequireAPIDetail(client, baseURL, want); err != nil {
			return fmt.Errorf("phase B API detail id=%d: %w", want.ID, err)
		}
	}
	if err := authenticatedRestartRequireHistory(
		client,
		baseURL,
		3,
		[]string{
			`data-sequence="1" data-action="add" data-actor="article-development-admin"`,
			`data-sequence="2" data-action="change" data-actor="article-development-admin"`,
			`data-sequence="5" data-action="change" data-actor="article-development-admin"`,
			"Admin after restart",
		},
		sensitive,
	); err != nil {
		return fmt.Errorf("phase B continued Admin history: %w", err)
	}
	if authenticatedRestartJarCookie(jar, parsed, websessionauth.DefaultSessionCookieName) != phaseA.SessionCookie {
		return errors.New("phase B session cookie changed after authenticated writes")
	}
	return nil
}

func authenticatedRestartLogin(
	client *http.Client,
	jar http.CookieJar,
	baseURL, username, password string,
	sensitive *[]string,
) (authenticatedRestartLoginState, error) {
	loginPage, err := authenticatedRestartRequest(client, http.MethodGet, baseURL+"/admin/login/", "", "", "", "")
	if err != nil {
		return authenticatedRestartLoginState{}, fmt.Errorf("phase A Admin login page: %w", err)
	}
	if loginPage.Status != http.StatusOK || loginPage.ContentType != "text/html; charset=utf-8" {
		return authenticatedRestartLoginState{}, fmt.Errorf("phase A Admin login page status/content-type=%d/%t", loginPage.Status, loginPage.ContentType == "text/html; charset=utf-8")
	}
	preLoginToken, err := authenticatedRestartResponseCSRF(loginPage, sensitive)
	if err != nil {
		return authenticatedRestartLoginState{}, err
	}
	preLoginCSRF, err := authenticatedRestartUniqueCookie(loginPage.Cookies, websessionauth.DefaultCSRFCookieName)
	if err != nil {
		return authenticatedRestartLoginState{}, err
	}
	if !preLoginCSRF.HttpOnly || preLoginCSRF.Path != "/" || preLoginCSRF.Value == "" || preLoginCSRF.Value == preLoginToken {
		return authenticatedRestartLoginState{}, errors.New("phase A Admin login page published an unsafe CSRF cookie")
	}
	*sensitive = append(*sensitive, preLoginCSRF.Value)

	form := url.Values{
		"csrfmiddlewaretoken": {preLoginToken},
		"username":            {username},
		"password":            {password},
		"next":                {"/admin/articles/"},
	}
	login, err := authenticatedRestartRequest(
		client,
		http.MethodPost,
		baseURL+"/admin/login/",
		"",
		"application/x-www-form-urlencoded",
		form.Encode(),
		"",
	)
	if err != nil {
		return authenticatedRestartLoginState{}, fmt.Errorf("phase A Admin login: %w", err)
	}
	if login.Status != http.StatusFound || login.Header.Get("Location") != "/admin/articles/" || !bytes.Equal(login.Body, []byte("Found\n")) {
		return authenticatedRestartLoginState{}, fmt.Errorf("phase A Admin login status/location/body-shape=%d/%t/%t", login.Status, login.Header.Get("Location") == "/admin/articles/", bytes.Equal(login.Body, []byte("Found\n")))
	}
	sessionCookie, err := authenticatedRestartUniqueCookie(login.Cookies, websessionauth.DefaultSessionCookieName)
	if err != nil {
		return authenticatedRestartLoginState{}, err
	}
	rotatedCSRF, err := authenticatedRestartUniqueCookie(login.Cookies, websessionauth.DefaultCSRFCookieName)
	if err != nil {
		return authenticatedRestartLoginState{}, err
	}
	if !sessionCookie.HttpOnly || sessionCookie.Path != "/" || sessionCookie.Value == "" ||
		!rotatedCSRF.HttpOnly || rotatedCSRF.Path != "/" || rotatedCSRF.Value == "" ||
		rotatedCSRF.Value == preLoginCSRF.Value || rotatedCSRF.Value == preLoginToken {
		return authenticatedRestartLoginState{}, errors.New("phase A Admin login did not rotate independent session and CSRF cookies")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return authenticatedRestartLoginState{}, errors.New("parse phase A login base URL")
	}
	if authenticatedRestartJarCookie(jar, parsed, websessionauth.DefaultSessionCookieName) != sessionCookie.Value ||
		authenticatedRestartJarCookie(jar, parsed, websessionauth.DefaultCSRFCookieName) != rotatedCSRF.Value {
		return authenticatedRestartLoginState{}, errors.New("phase A browser did not retain rotated login cookies")
	}
	*sensitive = append(*sensitive, sessionCookie.Value, rotatedCSRF.Value)
	return authenticatedRestartLoginState{
		SessionCookie: sessionCookie.Value,
		CSRFCookie:    rotatedCSRF.Value,
	}, nil
}

func authenticatedRestartAdminCreate(
	client *http.Client,
	baseURL, title, summary string,
	published bool,
	sensitive *[]string,
) error {
	page, err := authenticatedRestartRequest(client, http.MethodGet, baseURL+"/admin/articles/add/", "", "", "", "")
	if err != nil {
		return err
	}
	csrf, err := authenticatedRestartResponseCSRF(page, sensitive)
	if err != nil || page.Status != http.StatusOK || page.ContentType != "text/html; charset=utf-8" {
		return fmt.Errorf("Admin add page status/content-type/token=%d/%t/%t", page.Status, page.ContentType == "text/html; charset=utf-8", err == nil)
	}
	form := url.Values{
		"csrfmiddlewaretoken": {csrf},
		"title":               {title},
		"summary":             {summary},
	}
	if published {
		form.Set("published", "on")
	}
	result, err := authenticatedRestartRequest(
		client,
		http.MethodPost,
		baseURL+"/admin/articles/add/",
		"",
		"application/x-www-form-urlencoded",
		form.Encode(),
		"",
	)
	if err != nil {
		return err
	}
	return authenticatedRestartRequireNoticeRedirect(result, "added")
}

func authenticatedRestartAdminUpdate(
	client *http.Client,
	baseURL string,
	id int64,
	title, summary string,
	published bool,
	sensitive *[]string,
) error {
	target := fmt.Sprintf("%s/admin/articles/change/?id=%d", baseURL, id)
	page, err := authenticatedRestartRequest(client, http.MethodGet, target, "", "", "", "")
	if err != nil {
		return err
	}
	csrf, err := authenticatedRestartResponseCSRF(page, sensitive)
	if err != nil || page.Status != http.StatusOK || page.ContentType != "text/html; charset=utf-8" {
		return fmt.Errorf("Admin change page status/content-type/token=%d/%t/%t", page.Status, page.ContentType == "text/html; charset=utf-8", err == nil)
	}
	form := url.Values{
		"csrfmiddlewaretoken": {csrf},
		"title":               {title},
		"summary":             {summary},
	}
	if published {
		form.Set("published", "on")
	}
	result, err := authenticatedRestartRequest(
		client,
		http.MethodPost,
		target,
		"",
		"application/x-www-form-urlencoded",
		form.Encode(),
		"",
	)
	if err != nil {
		return err
	}
	return authenticatedRestartRequireNoticeRedirect(result, "changed")
}

func authenticatedRestartAdminDelete(
	client *http.Client,
	baseURL string,
	id int64,
	sensitive *[]string,
) error {
	target := fmt.Sprintf("%s/admin/articles/delete/?id=%d", baseURL, id)
	page, err := authenticatedRestartRequest(client, http.MethodGet, target, "", "", "", "")
	if err != nil {
		return err
	}
	csrf, err := authenticatedRestartResponseCSRF(page, sensitive)
	if err != nil || page.Status != http.StatusOK || page.ContentType != "text/html; charset=utf-8" ||
		!bytes.Contains(page.Body, []byte(`data-admin-view="delete"`)) {
		return fmt.Errorf("Admin delete page status/content-type/token/view=%d/%t/%t/%t", page.Status, page.ContentType == "text/html; charset=utf-8", err == nil, bytes.Contains(page.Body, []byte(`data-admin-view="delete"`)))
	}
	form := url.Values{"csrfmiddlewaretoken": {csrf}, "confirm": {"yes"}}
	result, err := authenticatedRestartRequest(
		client,
		http.MethodPost,
		target,
		"",
		"application/x-www-form-urlencoded",
		form.Encode(),
		"",
	)
	if err != nil {
		return err
	}
	return authenticatedRestartRequireNoticeRedirect(result, "deleted")
}

func authenticatedRestartRequireHistory(
	client *http.Client,
	baseURL string,
	id int64,
	markers []string,
	sensitive *[]string,
) error {
	target := fmt.Sprintf("%s/admin/articles/history/?id=%d", baseURL, id)
	response, err := authenticatedRestartRequest(client, http.MethodGet, target, "", "", "", "")
	if err != nil {
		return err
	}
	if _, err := authenticatedRestartResponseCSRF(response, sensitive); err != nil ||
		response.Status != http.StatusOK || response.ContentType != "text/html; charset=utf-8" ||
		!bytes.Contains(response.Body, []byte(`data-admin-view="history"`)) {
		return fmt.Errorf("Admin history status/content-type/token/view=%d/%t/%t/%t", response.Status, response.ContentType == "text/html; charset=utf-8", err == nil, bytes.Contains(response.Body, []byte(`data-admin-view="history"`)))
	}
	position := 0
	for _, marker := range markers {
		relative := bytes.Index(response.Body[position:], []byte(marker))
		if relative < 0 {
			return errors.New("Admin history omitted or reordered a required semantic marker")
		}
		position += relative + len(marker)
	}
	return nil
}

func authenticatedRestartRequireAPIDetail(
	client *http.Client,
	baseURL string,
	want authenticatedRestartArticle,
) error {
	response, err := authenticatedRestartRequest(
		client,
		http.MethodGet,
		fmt.Sprintf("%s/api/articles/%d/", baseURL, want.ID),
		api.JSONContentType,
		"",
		"",
		"",
	)
	if err != nil {
		return err
	}
	return authenticatedRestartRequireArticle(response, http.StatusOK, want)
}

func authenticatedRestartRequireArticle(
	response authenticatedRestartHTTPResult,
	wantStatus int,
	want authenticatedRestartArticle,
) error {
	if response.Status != wantStatus || response.ContentType != api.JSONContentType {
		return fmt.Errorf("Article response status/content-type=%d/%t", response.Status, response.ContentType == api.JSONContentType)
	}
	decoder := json.NewDecoder(bytes.NewReader(response.Body))
	decoder.DisallowUnknownFields()
	var got authenticatedRestartArticle
	if err := decoder.Decode(&got); err != nil {
		return errors.New("decode Article response")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("Article response has trailing JSON")
	}
	if got.ID != want.ID || got.Title != want.Title || got.Published != want.Published ||
		!authenticatedRestartOptionalStringEqual(got.Summary, want.Summary) {
		return errors.New("Article response semantic fields differ")
	}
	return nil
}

func authenticatedRestartDecodePage(response authenticatedRestartHTTPResult) (authenticatedRestartArticlePage, error) {
	decoder := json.NewDecoder(bytes.NewReader(response.Body))
	decoder.DisallowUnknownFields()
	var page authenticatedRestartArticlePage
	if err := decoder.Decode(&page); err != nil {
		return authenticatedRestartArticlePage{}, errors.New("decode Article API page")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return authenticatedRestartArticlePage{}, errors.New("Article API page has trailing JSON")
	}
	return page, nil
}

func authenticatedRestartRequireNoticeRedirect(response authenticatedRestartHTTPResult, notice string) error {
	if response.Status != http.StatusFound || !bytes.Equal(response.Body, []byte("Found\n")) {
		return fmt.Errorf("Admin mutation status/body-shape=%d/%t", response.Status, bytes.Equal(response.Body, []byte("Found\n")))
	}
	location, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		return errors.New("parse Admin mutation redirect")
	}
	query := location.Query()
	if location.Path != "/admin/articles/" || query.Get("notice") != notice || query.Get("count") != "" || query.Get("sig") == "" {
		return fmt.Errorf("Admin mutation redirect path/notice/count/signature=%t/%t/%t/%t", location.Path == "/admin/articles/", query.Get("notice") == notice, query.Get("count") == "", query.Get("sig") != "")
	}
	return nil
}

func authenticatedRestartResponseCSRF(response authenticatedRestartHTTPResult, sensitive *[]string) (string, error) {
	match := authenticatedRestartCSRFPattern.FindSubmatch(response.Body)
	if len(match) != 2 {
		return "", errors.New("Admin response omitted a bounded masked CSRF token")
	}
	token := string(match[1])
	*sensitive = append(*sensitive, token)
	return token, nil
}

func authenticatedRestartUniqueCookie(cookies []*http.Cookie, name string) (*http.Cookie, error) {
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

func authenticatedRestartJarCookie(jar http.CookieJar, target *url.URL, name string) string {
	for _, cookie := range jar.Cookies(target) {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

func authenticatedRestartHTTPClient(jar http.CookieJar) *http.Client {
	return &http.Client{
		Jar:     jar,
		Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func authenticatedRestartRequest(
	client *http.Client,
	method, target, accept, contentType, body, csrf string,
) (authenticatedRestartHTTPResult, error) {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request, err := http.NewRequest(method, target, reader)
	if err != nil {
		return authenticatedRestartHTTPResult{}, errors.New("construct authenticated restart request")
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
		return authenticatedRestartHTTPResult{}, fmt.Errorf("perform authenticated restart request: %w", err)
	}
	payload, readErr := io.ReadAll(io.LimitReader(response.Body, authenticatedRestartMaximumResponseBytes+1))
	closeErr := response.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return authenticatedRestartHTTPResult{}, errors.New("read authenticated restart response")
	}
	if len(payload) > authenticatedRestartMaximumResponseBytes {
		return authenticatedRestartHTTPResult{}, errors.New("authenticated restart response exceeded one mebibyte")
	}
	return authenticatedRestartHTTPResult{
		Status:      response.StatusCode,
		ContentType: response.Header.Get("Content-Type"),
		Header:      response.Header.Clone(),
		Cookies:     response.Cookies(),
		Body:        payload,
	}, nil
}

func authenticatedRestartRunServer(
	t *testing.T,
	globalBinary, repository, descriptor, expectedAddress string,
	environment []string,
	sensitive *[]string,
	outputCanaries []string,
	exercise func(string) error,
) authenticatedRestartPhaseResult {
	t.Helper()
	if exercise == nil {
		t.Fatal("authenticated restart exercise is nil")
	}
	stdout := newReadinessOutput()
	stderr := &boundedOutput{maximum: maximumCommandOutput}
	command := exec.Command(globalBinary, "runserver", "--project", descriptor, "--addr", expectedAddress)
	command.Dir = repository
	command.Env = append([]string(nil), environment...)
	command.Stdout = stdout
	command.Stderr = stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.WaitDelay = 2 * time.Second
	if err := command.Start(); err != nil {
		t.Fatal("start authenticated global runserver")
	}
	result := authenticatedRestartPhaseResult{PID: command.Process.Pid}
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	finished := false
	defer func() {
		if !finished {
			_ = interruptAndWait(command, waited, 20*time.Second, result.ProcessGroups...)
		}
	}()

	readyTimer := time.NewTimer(commandTimeout)
	defer readyTimer.Stop()
	select {
	case address := <-stdout.ready:
		if address != expectedAddress || authenticatedRestartValidateAddress(address) != nil {
			t.Fatal("authenticated runserver published an invalid readiness address")
		}
		groups, err := ownedProcessGroups(command.Process.Pid)
		if err != nil || len(groups) < 2 {
			t.Fatalf("capture authenticated runserver process ownership: groups=%d error=%t", len(groups), err != nil)
		}
		result.ProcessGroups = groups
	case waitErr := <-waited:
		finished = true
		authenticatedRestartAssertOutputExcludesSensitive(t, stdout.String(), stderr.String(), *sensitive, outputCanaries)
		t.Fatalf("authenticated global runserver exited before readiness: error=%t stdout-bytes=%d stderr-bytes=%d", waitErr != nil, len(stdout.String()), len(stderr.String()))
	case <-readyTimer.C:
		cleanup := interruptAndWait(command, waited, 20*time.Second, result.ProcessGroups...)
		finished = true
		authenticatedRestartAssertOutputExcludesSensitive(t, stdout.String(), stderr.String(), *sensitive, outputCanaries)
		t.Fatalf("authenticated global runserver readiness timed out: cleanup-failed=%t stdout-bytes=%d stderr-bytes=%d", cleanup.failed(), len(stdout.String()), len(stderr.String()))
	}

	if err := authenticatedRestartWaitForHTTP(expectedAddress); err != nil {
		cleanup := interruptAndWait(command, waited, 20*time.Second, result.ProcessGroups...)
		finished = true
		authenticatedRestartAssertOutputExcludesSensitive(t, stdout.String(), stderr.String(), *sensitive, outputCanaries)
		t.Fatalf("authenticated global runserver HTTP readiness failed: cleanup-failed=%t", cleanup.failed())
	}
	exerciseErr := exercise("http://" + expectedAddress)
	cleanup := interruptAndWait(command, waited, 20*time.Second, result.ProcessGroups...)
	finished = true
	authenticatedRestartAssertOutputExcludesSensitive(t, stdout.String(), stderr.String(), *sensitive, outputCanaries)
	if cleanup.failed() || len(cleanup.ProcessGroups) < 2 {
		t.Fatalf("authenticated global runserver cleanup failed: failed=%t groups=%d", cleanup.failed(), len(cleanup.ProcessGroups))
	}
	if stderr.Truncated() || stderr.String() != "" {
		t.Fatalf("authenticated global runserver stderr shape bytes=%d truncated=%t", len(stderr.String()), stderr.Truncated())
	}
	wantReadiness := articleReadinessPrefix + expectedAddress + "\n"
	if stdout.Truncated() || stdout.String() != wantReadiness {
		t.Fatalf("authenticated global runserver stdout shape bytes=%d want-bytes=%d truncated=%t", len(stdout.String()), len(wantReadiness), stdout.Truncated())
	}
	if exerciseErr != nil {
		t.Fatalf("exercise authenticated global Article runtime: %v", exerciseErr)
	}
	return result
}

func authenticatedRestartValidateAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil || port == "0" {
		return errors.New("invalid listener address")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("listener is not loopback")
	}
	return nil
}

func authenticatedRestartWaitForHTTP(address string) error {
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(5 * time.Second)
	for {
		response, err := client.Get("http://" + address + articleListPath)
		if err == nil {
			_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, authenticatedRestartMaximumResponseBytes+1))
			closeErr := response.Body.Close()
			if response.StatusCode == http.StatusOK && readErr == nil && closeErr == nil {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return errors.New("authenticated runserver HTTP readiness timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func authenticatedRestartInspectDatabase(t *testing.T, databasePath string) authenticatedRestartDatabaseSnapshot {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal("open authenticated restart SQLite inspection")
	}
	database.SetMaxOpenConns(1)
	defer func() {
		if err := database.Close(); err != nil {
			t.Error("close authenticated restart SQLite inspection")
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := database.PingContext(ctx); err != nil {
		t.Fatal("ping authenticated restart SQLite inspection")
	}

	var snapshot authenticatedRestartDatabaseSnapshot
	articleRows, err := database.QueryContext(ctx, `SELECT "id", "title", "published", "summary" FROM "godj_conformance_article" ORDER BY "id"`)
	if err != nil {
		t.Fatal("query authenticated restart Articles")
	}
	for articleRows.Next() {
		var row authenticatedRestartPersistedArticle
		if err := articleRows.Scan(&row.ID, &row.Title, &row.Published, &row.Summary); err != nil {
			_ = articleRows.Close()
			t.Fatal("scan authenticated restart Article")
		}
		snapshot.Articles = append(snapshot.Articles, row)
	}
	if err := errors.Join(articleRows.Err(), articleRows.Close()); err != nil {
		t.Fatal("finish authenticated restart Article rows")
	}

	sessionRows, err := database.QueryContext(ctx, `SELECT "digest", "payload" FROM "godj_system_session" ORDER BY "id"`)
	if err != nil {
		t.Fatal("query authenticated restart sessions")
	}
	for sessionRows.Next() {
		var row authenticatedRestartSessionRow
		if err := sessionRows.Scan(&row.Digest, &row.Payload); err != nil {
			_ = sessionRows.Close()
			t.Fatal("scan authenticated restart session")
		}
		snapshot.Sessions = append(snapshot.Sessions, row)
	}
	if err := errors.Join(sessionRows.Err(), sessionRows.Close()); err != nil {
		t.Fatal("finish authenticated restart session rows")
	}

	auditRows, err := database.QueryContext(ctx, `SELECT "id", "actor_id", "model", "object_id", "action", "changed_fields", "display_label" FROM "godj_system_audit" ORDER BY "id"`)
	if err != nil {
		t.Fatal("query authenticated restart audit")
	}
	for auditRows.Next() {
		var row authenticatedRestartAuditRow
		if err := auditRows.Scan(&row.Sequence, &row.ActorID, &row.Model, &row.ObjectID, &row.Action, &row.ChangedFields, &row.DisplayLabel); err != nil {
			_ = auditRows.Close()
			t.Fatal("scan authenticated restart audit")
		}
		snapshot.Audits = append(snapshot.Audits, row)
	}
	if err := errors.Join(auditRows.Err(), auditRows.Close()); err != nil {
		t.Fatal("finish authenticated restart audit rows")
	}

	credentialRows, err := database.QueryContext(ctx, `SELECT "id", "principal_id", "username", "encoded_password", "active", "permissions", "definition_digest" FROM "godj_system_credential" ORDER BY "id"`)
	if err != nil {
		t.Fatal("query authenticated restart credential")
	}
	for credentialRows.Next() {
		var row authenticatedRestartCredentialRow
		if err := credentialRows.Scan(
			&row.ID,
			&row.PrincipalID,
			&row.Username,
			&row.EncodedPassword,
			&row.Active,
			&row.Permissions,
			&row.DefinitionDigest,
		); err != nil {
			_ = credentialRows.Close()
			t.Fatal("scan authenticated restart credential")
		}
		snapshot.Credential = append(snapshot.Credential, row)
	}
	if err := errors.Join(credentialRows.Err(), credentialRows.Close()); err != nil {
		t.Fatal("finish authenticated restart credential rows")
	}
	return snapshot
}

func authenticatedRestartAssertPhaseAState(
	t *testing.T,
	snapshot authenticatedRestartDatabaseSnapshot,
	username, password string,
	phase authenticatedRestartPhaseAState,
	sensitive []string,
) {
	t.Helper()
	authenticatedRestartAssertArticles(t, snapshot.Articles, []authenticatedRestartPersistedArticle{
		{ID: 1, Title: "API durable", Published: true, Summary: sql.NullString{String: "api-updated", Valid: true}},
		{ID: 3, Title: "Admin durable", Published: true, Summary: sql.NullString{String: "admin-updated", Valid: true}},
	})
	authenticatedRestartAssertCredential(t, snapshot.Credential, username, password)
	authenticatedRestartAssertSession(t, snapshot.Sessions, phase.SessionCookie, sensitive)
	authenticatedRestartAssertAudit(t, snapshot.Audits, []authenticatedRestartAuditRow{
		{Sequence: 1, ObjectID: "3", Action: "add", DisplayLabel: "Admin before restart"},
		{Sequence: 2, ObjectID: "3", Action: "change", DisplayLabel: "Admin durable"},
		{Sequence: 3, ObjectID: "4", Action: "add", DisplayLabel: "Admin disposable"},
		{Sequence: 4, ObjectID: "4", Action: "delete", DisplayLabel: "Admin disposable"},
	})
}

func authenticatedRestartAssertMigratedSystemStateEmpty(
	t *testing.T,
	snapshot authenticatedRestartDatabaseSnapshot,
) {
	t.Helper()
	if len(snapshot.Articles) != 0 || len(snapshot.Sessions) != 0 || len(snapshot.Audits) != 0 || len(snapshot.Credential) != 0 {
		t.Fatalf(
			"authenticated migrate populated runtime state: articles=%d sessions=%d audits=%d credentials=%d",
			len(snapshot.Articles),
			len(snapshot.Sessions),
			len(snapshot.Audits),
			len(snapshot.Credential),
		)
	}
}

func authenticatedRestartAssertCredentialUnchanged(
	t *testing.T,
	before, after []authenticatedRestartCredentialRow,
) {
	t.Helper()
	if len(before) != 1 || len(after) != 1 || before[0] != after[0] {
		t.Fatalf(
			"authenticated restart credential changed across processes: before-count=%d after-count=%d exact=%t",
			len(before),
			len(after),
			len(before) == 1 && len(after) == 1 && before[0] == after[0],
		)
	}
}

func authenticatedRestartAssertPhaseBState(
	t *testing.T,
	snapshot authenticatedRestartDatabaseSnapshot,
	username, password string,
	phase authenticatedRestartPhaseAState,
	sensitive []string,
) {
	t.Helper()
	authenticatedRestartAssertArticles(t, snapshot.Articles, []authenticatedRestartPersistedArticle{
		{ID: 1, Title: "API durable", Published: true, Summary: sql.NullString{String: "api-updated", Valid: true}},
		{ID: 3, Title: "Admin after restart", Published: true, Summary: sql.NullString{String: "admin-restarted", Valid: true}},
		{ID: 5, Title: "API after restart", Summary: sql.NullString{String: "session survived", Valid: true}},
	})
	authenticatedRestartAssertCredential(t, snapshot.Credential, username, password)
	authenticatedRestartAssertSession(t, snapshot.Sessions, phase.SessionCookie, sensitive)
	authenticatedRestartAssertAudit(t, snapshot.Audits, []authenticatedRestartAuditRow{
		{Sequence: 1, ObjectID: "3", Action: "add", DisplayLabel: "Admin before restart"},
		{Sequence: 2, ObjectID: "3", Action: "change", DisplayLabel: "Admin durable"},
		{Sequence: 3, ObjectID: "4", Action: "add", DisplayLabel: "Admin disposable"},
		{Sequence: 4, ObjectID: "4", Action: "delete", DisplayLabel: "Admin disposable"},
		{Sequence: 5, ObjectID: "3", Action: "change", DisplayLabel: "Admin after restart"},
	})
}

func authenticatedRestartAssertArticles(
	t *testing.T,
	got, want []authenticatedRestartPersistedArticle,
) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("authenticated restart Article row count = %d, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("authenticated restart Article row %d semantic fields differ", index)
		}
	}
}

func authenticatedRestartAssertCredential(
	t *testing.T,
	rows []authenticatedRestartCredentialRow,
	username, password string,
) {
	t.Helper()
	if len(rows) != 1 || rows[0].ID <= 0 || rows[0].PrincipalID != "article-development-admin" || rows[0].Username != username ||
		rows[0].EncodedPassword == "" || rows[0].EncodedPassword == password || strings.Contains(rows[0].EncodedPassword, password) ||
		!rows[0].Active || rows[0].Permissions == "" || !strings.HasPrefix(rows[0].DefinitionDigest, "sha256:") {
		t.Fatalf(
			"authenticated restart credential semantic shape = count:%d id:%t principal:%t username:%t encoded:%t raw-absent:%t active:%t permissions:%t digest:%t",
			len(rows),
			len(rows) == 1 && rows[0].ID > 0,
			len(rows) == 1 && rows[0].PrincipalID == "article-development-admin",
			len(rows) == 1 && rows[0].Username == username,
			len(rows) == 1 && rows[0].EncodedPassword != "",
			len(rows) == 1 && !strings.Contains(rows[0].EncodedPassword, password),
			len(rows) == 1 && rows[0].Active,
			len(rows) == 1 && rows[0].Permissions != "",
			len(rows) == 1 && strings.HasPrefix(rows[0].DefinitionDigest, "sha256:"),
		)
	}
}

func authenticatedRestartAssertSession(
	t *testing.T,
	rows []authenticatedRestartSessionRow,
	rawSession string,
	sensitive []string,
) {
	t.Helper()
	wantDigest := authenticatedRestartSessionDigest(rawSession)
	if len(rows) != 1 || rows[0].Digest != wantDigest || !strings.HasPrefix(rows[0].Payload, "v1.") {
		t.Fatalf("authenticated restart session semantic shape = count:%d digest:%t payload-prefix:%t", len(rows), len(rows) == 1 && rows[0].Digest == wantDigest, len(rows) == 1 && strings.HasPrefix(rows[0].Payload, "v1."))
	}
	stored := rows[0].Digest + rows[0].Payload
	for _, value := range sensitive {
		if value != "" && strings.Contains(stored, value) {
			t.Fatal("authenticated restart session row stored a raw secret")
		}
	}
}

func authenticatedRestartAssertAudit(
	t *testing.T,
	got, want []authenticatedRestartAuditRow,
) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("authenticated restart audit row count = %d, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index].Sequence != want[index].Sequence ||
			got[index].ActorID != "article-development-admin" ||
			got[index].Model != "godj_conformance.article" ||
			got[index].ObjectID != want[index].ObjectID ||
			got[index].Action != want[index].Action ||
			!strings.HasPrefix(got[index].ChangedFields, "v1.") ||
			got[index].DisplayLabel != want[index].DisplayLabel {
			t.Fatalf("authenticated restart audit row %d semantic fields differ", index)
		}
	}
}

func authenticatedRestartSessionDigest(raw string) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte("godj.systemstate.session.v1\x00"))
	_, _ = digest.Write([]byte(raw))
	return hex.EncodeToString(digest.Sum(nil))
}

func authenticatedRestartAssertOutputExcludesSensitive(
	t *testing.T,
	stdout, stderr string,
	sensitive, outputCanaries []string,
) {
	t.Helper()
	combined := stdout + stderr
	for _, value := range append(append([]string(nil), sensitive...), outputCanaries...) {
		if value != "" && strings.Contains(combined, value) {
			t.Fatal("authenticated global runserver output exposed a raw secret")
		}
	}
}

func authenticatedRestartAssertArtifactsExcludeSensitive(
	t *testing.T,
	directory string,
	sensitive []string,
) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal("read authenticated restart artifact directory")
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		payload, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatal("read authenticated restart database artifact")
		}
		for _, value := range sensitive {
			if value != "" && bytes.Contains(payload, []byte(value)) {
				t.Fatal("authenticated restart database artifact stored a raw secret")
			}
		}
	}
}

func authenticatedRestartString(value string) *string {
	return &value
}

func authenticatedRestartOptionalStringEqual(left, right *string) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}
