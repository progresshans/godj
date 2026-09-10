package article_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/bearerauth"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/apiapp"
	"github.com/progresshans/godj/examples/article/articleapp"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

const (
	articleAPIBearerDigestDomain = "godj/article-api-test/bearer/v1:"
	articleAPIFullBearer         = "gdj-article-api-full-bearer-A1b2C3d4"
	articleAPIViewerBearer       = "gdj-article-api-viewer-bearer-E5f6G7h8"
	articleAPIInvalidBearer      = "gdj-article-api-invalid-bearer-I9j0K1l2"
	articleAPIMalformedBearer    = "ab=c"
)

func TestArticleAPIBearerSQLiteUserFlow(t *testing.T) {
	ctx := context.Background()
	backend, err := sqlite.OpenMemory(ctx, "article-api-bearer-e2e-"+strings.ReplaceAll(t.Name(), "/", "_"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("close Article API Bearer SQLite backend: %v", err)
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

	runArticleAPIBearerUserFlow(t, backend)
}

func runArticleAPIBearerUserFlow(t *testing.T, backend articleapp.Backend) {
	t.Helper()
	fixture := newArticleAPIBearerFixture(t, backend)
	seedArticleAPIFlow(t, fixture.repository)

	if got := articleAPIArticleCount(t, fixture.repository); got != 5 {
		t.Fatalf("initial Article count = %d, want 5", got)
	}

	listed := fixture.request(t, articleAPIBearerRequest{
		method:        http.MethodGet,
		target:        apiapp.ListPath,
		authorization: "Bearer " + articleAPIFullBearer,
	})
	listed.requireJSON(t, http.StatusOK, `{"count":5,"next":"/api/articles/?page=2","previous":null,"results":[{"id":1,"title":"Go Alpha","published":true,"summary":null},{"id":2,"title":"Rust Beta","published":false,"summary":"second"}]}`)
	listed.requireChallenge(t, "")

	// Bearer mutations deliberately omit both the CSRF header and CSRF cookie.
	created := fixture.request(t, articleAPIBearerRequest{
		method:        http.MethodPost,
		target:        apiapp.ListPath,
		contentType:   api.JSONContentType,
		body:          `{"title":"  Bearer Created  ","published":true,"summary":"from bearer"}`,
		authorization: "Bearer " + articleAPIFullBearer,
	})
	created.requireJSON(t, http.StatusCreated, `{"id":6,"title":"Bearer Created","published":true,"summary":"from bearer"}`)
	created.requireChallenge(t, "")
	if got := created.header.Get("Location"); got != "/api/articles/6/" {
		t.Fatal("Bearer create Location was not the expected relative Article path")
	}
	if got := articleAPIArticleCount(t, fixture.repository); got != 6 {
		t.Fatalf("valid Bearer create Article count = %d, want 6", got)
	}

	detail := fixture.request(t, articleAPIBearerRequest{
		method:        http.MethodGet,
		target:        "/api/articles/6/",
		authorization: "Bearer " + articleAPIFullBearer,
	})
	detail.requireJSON(t, http.StatusOK, `{"id":6,"title":"Bearer Created","published":true,"summary":"from bearer"}`)

	replaced := fixture.request(t, articleAPIBearerRequest{
		method:        http.MethodPut,
		target:        "/api/articles/6/",
		contentType:   api.JSONContentType,
		body:          `{"title":"Bearer Replaced","summary":null}`,
		authorization: "Bearer " + articleAPIFullBearer,
	})
	replaced.requireJSON(t, http.StatusOK, `{"id":6,"title":"Bearer Replaced","published":false,"summary":null}`)

	patched := fixture.request(t, articleAPIBearerRequest{
		method:        http.MethodPatch,
		target:        "/api/articles/6/",
		contentType:   api.JSONContentType,
		body:          `{"published":true,"summary":"patched"}`,
		authorization: "Bearer " + articleAPIFullBearer,
	})
	patched.requireJSON(t, http.StatusOK, `{"id":6,"title":"Bearer Replaced","published":true,"summary":"patched"}`)
	article := articleAPIGet(t, fixture.repository, 6)
	if article.Title != "Bearer Replaced" || !article.Published || article.Summary == nil || *article.Summary != "patched" {
		t.Fatal("valid Bearer PUT/PATCH did not persist the expected Article state")
	}

	beforeDenied := articleAPIArticleCount(t, fixture.repository)
	permissionDenied := fixture.request(t, articleAPIBearerRequest{
		method:        http.MethodDelete,
		target:        "/api/articles/6/",
		authorization: "Bearer " + articleAPIViewerBearer,
	})
	permissionDenied.requireJSON(t, http.StatusForbidden, `{"code":"permission_denied","errors":[]}`)
	permissionDenied.requireChallenge(t, `Bearer error="insufficient_scope"`)
	if got := articleAPIArticleCount(t, fixture.repository); got != beforeDenied {
		t.Fatalf("permission denial changed Article count from %d to %d", beforeDenied, got)
	}

	// The client carries a live Manager-backed session cookie. A valid token in
	// query/body is still ignored when the explicit Bearer profile is missing.
	beforeMissing := articleAPIArticleCount(t, fixture.repository)
	missing := fixture.request(t, articleAPIBearerRequest{
		method:      http.MethodPost,
		target:      apiapp.ListPath + "?access_token=" + url.QueryEscape(articleAPIFullBearer),
		contentType: api.JSONContentType,
		body:        `{"title":"must not exist","access_token":"` + articleAPIFullBearer + `"}`,
	})
	missing.requireJSON(t, http.StatusUnauthorized, `{"code":"not_authenticated","errors":[]}`)
	missing.requireChallenge(t, "Bearer")
	if got := articleAPIArticleCount(t, fixture.repository); got != beforeMissing {
		t.Fatalf("missing Bearer request changed Article count from %d to %d", beforeMissing, got)
	}
	fixture.requireLiveSession(t)

	beforeMalformed := articleAPIArticleCount(t, fixture.repository)
	malformed := fixture.request(t, articleAPIBearerRequest{
		method:        http.MethodPost,
		target:        apiapp.ListPath,
		contentType:   api.JSONContentType,
		body:          `{"title":"must not exist"}`,
		authorization: "Bearer " + articleAPIMalformedBearer,
	})
	malformed.requireJSON(t, http.StatusBadRequest, `{"code":"not_authenticated","errors":[]}`)
	malformed.requireChallenge(t, `Bearer error="invalid_request"`)
	if got := articleAPIArticleCount(t, fixture.repository); got != beforeMalformed {
		t.Fatalf("malformed Bearer request changed Article count from %d to %d", beforeMalformed, got)
	}

	beforeInvalid := articleAPIArticleCount(t, fixture.repository)
	invalid := fixture.request(t, articleAPIBearerRequest{
		method:        http.MethodPost,
		target:        apiapp.ListPath + "?access_token=" + url.QueryEscape(articleAPIFullBearer),
		contentType:   api.JSONContentType,
		body:          `{"title":"must not exist","access_token":"` + articleAPIFullBearer + `"}`,
		authorization: "Bearer " + articleAPIInvalidBearer,
	})
	invalid.requireJSON(t, http.StatusUnauthorized, `{"code":"not_authenticated","errors":[]}`)
	invalid.requireChallenge(t, `Bearer error="invalid_token"`)
	if got := articleAPIArticleCount(t, fixture.repository); got != beforeInvalid {
		t.Fatalf("invalid Bearer request changed Article count from %d to %d", beforeInvalid, got)
	}
	fixture.requireLiveSession(t)

	deleted := fixture.request(t, articleAPIBearerRequest{
		method:        http.MethodDelete,
		target:        "/api/articles/6/",
		authorization: "Bearer " + articleAPIFullBearer,
	})
	deleted.requireNoContent(t)
	deleted.requireChallenge(t, "")
	if _, found, err := fixture.repository.Get(context.Background(), 6); err != nil || found {
		t.Fatalf("Article 6 after Bearer delete: found=%t error=%v", found, err)
	}
	if got := articleAPIArticleCount(t, fixture.repository); got != 5 {
		t.Fatalf("valid Bearer delete Article count = %d, want 5", got)
	}

	if got := fixture.verifier.Calls(); got != 8 {
		t.Fatalf("Bearer verifier calls = %d, want 8", got)
	}
	fixture.requireSecretsAbsent(t)
}

type articleAPIBearerVerifier struct {
	principals map[[sha256.Size]byte]auth.Principal
	calls      atomic.Uint64
}

func newArticleAPIBearerVerifier(full auth.Principal, viewer auth.Principal) *articleAPIBearerVerifier {
	return &articleAPIBearerVerifier{principals: map[[sha256.Size]byte]auth.Principal{
		articleAPIBearerDigest(articleAPIFullBearer):   full,
		articleAPIBearerDigest(articleAPIViewerBearer): viewer,
	}}
}

func (verifier *articleAPIBearerVerifier) Verify(ctx context.Context, token bearerauth.Token) (auth.Principal, error) {
	if err := ctx.Err(); err != nil {
		return auth.Principal{}, err
	}
	verifier.calls.Add(1)
	digest := articleAPIBearerDigest(token.Encoded())
	principal, found := verifier.principals[digest]
	if !found {
		return auth.Principal{}, auth.ErrInvalidCredentials
	}
	return principal, nil
}

func (verifier *articleAPIBearerVerifier) Calls() uint64 {
	if verifier == nil {
		return 0
	}
	return verifier.calls.Load()
}

func articleAPIBearerDigest(raw string) [sha256.Size]byte {
	digest := sha256.New()
	_, _ = io.WriteString(digest, articleAPIBearerDigestDomain)
	_, _ = io.WriteString(digest, raw)
	var result [sha256.Size]byte
	copy(result[:], digest.Sum(nil))
	return result
}

type articleAPIBearerFixture struct {
	server       *httptest.Server
	client       *http.Client
	repository   articleapp.Repository
	verifier     *articleAPIBearerVerifier
	logs         *articleAPISafeBuffer
	sessionStore *sessions.Manager
	sessionID    sessions.ID
}

func newArticleAPIBearerFixture(t *testing.T, backend articleapp.Backend) articleAPIBearerFixture {
	t.Helper()
	repository, err := articleapp.NewRepository(backend)
	if err != nil {
		t.Fatal(err)
	}
	fullPrincipal := articleAPIPrincipal(t, "bearer-staff-1", true)
	viewerPrincipal := articleAPIPrincipal(t, "bearer-viewer-1", false)
	verifier := newArticleAPIBearerVerifier(fullPrincipal, viewerPrincipal)
	runtime, err := bearerauth.New(bearerauth.Config{
		Verifier:   verifier,
		Authorizer: auth.PrincipalAuthorizer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	articleAPI, err := apiapp.New(backend, runtime)
	if err != nil {
		t.Fatal(err)
	}
	middleware, err := apiapp.Middleware()
	if err != nil {
		t.Fatal(err)
	}
	projectSettings, err := settings.New(settings.Definition{
		ProjectName: "article_api_bearer_e2e",
		InstalledApps: []apps.Config{{
			Name:  "github.com/progresshans/godj/examples/article/models",
			Label: apiapp.Namespace,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	logs := &articleAPISafeBuffer{}
	application, err := web.NewApplication(web.Config{
		Settings:   projectSettings,
		Routes:     articleAPI.Routes(),
		Middleware: middleware,
		Logger:     slog.New(slog.NewTextHandler(logs, nil)),
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

	store, err := sessions.NewMemoryStore(4)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := sessions.NewManager(store, sessions.Config{})
	if err != nil {
		t.Fatal(err)
	}
	record, err := manager.Create(context.Background(), map[string]string{
		"_godj_principal_id": fullPrincipal.ID(),
	})
	if err != nil {
		t.Fatal(err)
	}
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(serverURL, []*http.Cookie{{
		Name:     websessionauth.DefaultSessionCookieName,
		Value:    record.ID().Encoded(),
		Path:     "/",
		HttpOnly: true,
	}})

	return articleAPIBearerFixture{
		server:       server,
		client:       client,
		repository:   repository,
		verifier:     verifier,
		logs:         logs,
		sessionStore: manager,
		sessionID:    record.ID(),
	}
}

type articleAPIBearerRequest struct {
	method        string
	target        string
	contentType   string
	body          string
	authorization string
}

func (fixture articleAPIBearerFixture) request(t *testing.T, input articleAPIBearerRequest) articleAPIBearerResult {
	t.Helper()
	request, err := http.NewRequest(input.method, fixture.server.URL+input.target, strings.NewReader(input.body))
	if err != nil {
		t.Fatal("construct Article API Bearer request")
	}
	request.Header.Set("Accept", api.JSONContentType)
	if input.contentType != "" {
		request.Header.Set("Content-Type", input.contentType)
	}
	if input.authorization != "" {
		request.Header.Set("Authorization", input.authorization)
	}
	response, err := fixture.client.Do(request)
	if err != nil {
		t.Fatal("execute Article API Bearer request")
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal("read Article API Bearer response")
	}
	result := articleAPIBearerResult{
		status:  response.StatusCode,
		header:  response.Header.Clone(),
		body:    string(payload),
		cookies: response.Cookies(),
	}
	result.requireProfileIsolation(t)
	result.requireSecretsAbsent(t)
	if articleAPIBearerContainsSecret(fixture.logs.String()) {
		t.Fatal("raw Bearer credential escaped into the application log")
	}
	return result
}

func (fixture articleAPIBearerFixture) requireLiveSession(t *testing.T) {
	t.Helper()
	if fixture.sessionStore == nil || !fixture.sessionID.Valid() {
		t.Fatal("Bearer E2E session sentinel is not initialized")
	}
	if _, found, err := fixture.sessionStore.Load(context.Background(), fixture.sessionID); err != nil || !found {
		t.Fatalf("Manager-backed session cookie stopped being valid: found=%t error=%v", found, err)
	}
}

func (fixture articleAPIBearerFixture) requireSecretsAbsent(t *testing.T) {
	t.Helper()
	if fixture.verifier == nil || len(fixture.verifier.principals) != 2 {
		t.Fatal("Bearer verifier did not retain exactly two digest-keyed principals")
	}
	for digest := range fixture.verifier.principals {
		if articleAPIBearerContainsSecret(string(digest[:])) {
			t.Fatal("raw Bearer credential was retained as verifier storage")
		}
	}
	if articleAPIBearerContainsSecret(fixture.logs.String()) {
		t.Fatal("raw Bearer credential escaped into the application log")
	}
	page, err := fixture.repository.List(context.Background(), articleapp.ListOptions{Limit: articleapp.MaximumPageSize})
	if err != nil {
		t.Fatal("inspect Article persistence for Bearer credential leakage")
	}
	for _, article := range page.Articles {
		if articleAPIBearerContainsSecret(article.Title) ||
			article.Summary != nil && articleAPIBearerContainsSecret(*article.Summary) {
			t.Fatal("raw Bearer credential escaped into Article persistence")
		}
	}
}

type articleAPIBearerResult struct {
	status  int
	header  http.Header
	body    string
	cookies []*http.Cookie
}

func (result articleAPIBearerResult) requireJSON(t *testing.T, status int, body string) {
	t.Helper()
	if result.status != status || result.header.Get("Content-Type") != api.JSONContentType || result.body != body {
		actualDigest := sha256.Sum256([]byte(result.body))
		wantDigest := sha256.Sum256([]byte(body))
		t.Fatalf(
			"Article API Bearer response mismatch: status=%d content_type_match=%t body_bytes=%d body_sha256=%x want_status=%d want_body_bytes=%d want_body_sha256=%x",
			result.status,
			result.header.Get("Content-Type") == api.JSONContentType,
			len(result.body),
			actualDigest,
			status,
			len(body),
			wantDigest,
		)
	}
}

func (result articleAPIBearerResult) requireNoContent(t *testing.T) {
	t.Helper()
	if result.status != http.StatusNoContent || result.header.Get("Content-Type") != "" || result.body != "" {
		t.Fatalf(
			"Article API Bearer no-content mismatch: status=%d content_type_empty=%t body_bytes=%d",
			result.status,
			result.header.Get("Content-Type") == "",
			len(result.body),
		)
	}
}

func (result articleAPIBearerResult) requireChallenge(t *testing.T, want string) {
	t.Helper()
	if result.header.Get("WWW-Authenticate") != want {
		t.Fatal("Article API Bearer challenge did not match the fixed profile value")
	}
}

func (result articleAPIBearerResult) requireProfileIsolation(t *testing.T) {
	t.Helper()
	if len(result.cookies) != 0 || len(result.header.Values("Set-Cookie")) != 0 {
		t.Fatal("Article API Bearer response published cookie state")
	}
	if result.header.Get(websessionauth.DefaultCSRFHeader) != "" {
		t.Fatal("Article API Bearer response published a CSRF header")
	}
}

func (result articleAPIBearerResult) requireSecretsAbsent(t *testing.T) {
	t.Helper()
	if articleAPIBearerContainsSecret(result.body) {
		t.Fatal("raw Bearer credential escaped into the response body")
	}
	for _, values := range result.header {
		for _, value := range values {
			if articleAPIBearerContainsSecret(value) {
				t.Fatal("raw Bearer credential escaped into a response header")
			}
		}
	}
}

func articleAPIBearerContainsSecret(value string) bool {
	return strings.Contains(value, articleAPIFullBearer) ||
		strings.Contains(value, articleAPIViewerBearer) ||
		strings.Contains(value, articleAPIInvalidBearer) ||
		strings.Contains(value, articleAPIMalformedBearer)
}

type articleAPISafeBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *articleAPISafeBuffer) Write(payload []byte) (int, error) {
	if buffer == nil {
		return 0, errors.New("nil Article API log buffer")
	}
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(payload)
}

func (buffer *articleAPISafeBuffer) String() string {
	if buffer == nil {
		return ""
	}
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}
