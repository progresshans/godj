package sessionauth_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

const (
	loginPath     = "/admin/login/"
	indexPath     = "/admin/"
	protectedPath = "/admin/protected/"
	deniedPath    = "/admin/denied/"
	logoutPath    = "/admin/logout/"
)

func TestAnonymousCSRFLoginRotationPermissionAndLogoutFlow(t *testing.T) {
	harness := newHarness(t, true)
	defer harness.Close()

	response := harness.Do(t, http.MethodGet, protectedPath, nil)
	if response.StatusCode != http.StatusFound {
		t.Fatalf("anonymous protected status=%d", response.StatusCode)
	}
	if location := response.Header.Get("Location"); location != "/admin/login/?next=%2Fadmin%2Fprotected%2F" {
		t.Fatalf("unsafe anonymous redirect: %q", location)
	}
	closeBody(t, response)
	if writes := harness.store.Writes(); writes != 0 {
		t.Fatalf("anonymous protected GET wrote %d sessions", writes)
	}

	response = harness.Do(t, http.MethodGet, loginPath, nil)
	preLoginToken := readBody(t, response)
	if response.StatusCode != http.StatusOK || len(preLoginToken) != 128 {
		t.Fatalf("login GET status=%d token length=%d", response.StatusCode, len(preLoginToken))
	}
	if writes := harness.store.Writes(); writes != 0 {
		t.Fatalf("anonymous CSRF GET wrote %d sessions", writes)
	}
	csrfCookie := namedResponseCookie(t, response.Cookies(), sessionauth.DefaultCSRFCookieName)
	if !csrfCookie.HttpOnly || csrfCookie.Secure || csrfCookie.SameSite != http.SameSiteLaxMode || csrfCookie.MaxAge < 1 || csrfCookie.Expires.IsZero() {
		t.Fatal("unexpected CSRF cookie policy")
	}
	response = harness.Do(t, http.MethodGet, loginPath, nil)
	secondMaskedToken := readBody(t, response)
	if secondMaskedToken == preLoginToken || secondMaskedToken[len(secondMaskedToken)-43:] == preLoginToken[len(preLoginToken)-43:] {
		t.Fatal("repeated CSRF rendering exposed a stable unmasked token suffix")
	}
	if writes := harness.store.Writes(); writes != 0 {
		t.Fatalf("repeated anonymous CSRF GET wrote %d sessions", writes)
	}
	missingTokenHeaders := http.Header{
		"X-Test-Username": []string{"admin"},
		"X-Test-Password": []string{"correct"},
	}
	response = harness.Do(t, http.MethodPost, loginPath, missingTokenHeaders)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("missing CSRF status=%d", response.StatusCode)
	}
	closeBody(t, response)
	oversizedTokenHeaders := missingTokenHeaders.Clone()
	oversizedTokenHeaders.Set("X-Test-Form-Token", strings.Repeat("A", 257))
	response = harness.Do(t, http.MethodPost, loginPath, oversizedTokenHeaders)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("oversized CSRF status=%d", response.StatusCode)
	}
	closeBody(t, response)
	if writes := harness.store.Writes(); writes != 0 {
		t.Fatalf("missing/oversized CSRF wrote %d sessions", writes)
	}
	duplicateRequest, err := http.NewRequest(http.MethodPost, harness.server.URL+loginPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	duplicateRequest.Header.Set("Cookie", sessionauth.DefaultCSRFCookieName+"="+csrfCookie.Value+"; "+sessionauth.DefaultCSRFCookieName+"="+csrfCookie.Value)
	duplicateRequest.Header.Set("X-Test-Form-Token", preLoginToken)
	duplicateRequest.Header.Set("X-Test-Username", "admin")
	duplicateRequest.Header.Set("X-Test-Password", "correct")
	rawClient := harness.server.Client()
	rawClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err = rawClient.Do(duplicateRequest)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("duplicate CSRF cookie status=%d", response.StatusCode)
	}
	closeBody(t, response)
	if writes := harness.store.Writes(); writes != 0 {
		t.Fatalf("duplicate CSRF cookie wrote %d sessions", writes)
	}
	duplicateFormHeaders := missingTokenHeaders.Clone()
	duplicateFormHeaders["X-Test-Form-Token"] = []string{preLoginToken, preLoginToken}
	response = harness.Do(t, http.MethodPost, loginPath, duplicateFormHeaders)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("duplicate CSRF form field status=%d", response.StatusCode)
	}
	closeBody(t, response)
	if writes := harness.store.Writes(); writes != 0 {
		t.Fatalf("duplicate CSRF form field wrote %d sessions", writes)
	}
	ambiguousSourceHeaders := missingTokenHeaders.Clone()
	ambiguousSourceHeaders["X-Test-Form-Token"] = []string{""}
	ambiguousSourceHeaders[sessionauth.DefaultCSRFHeader] = []string{preLoginToken}
	response = harness.Do(t, http.MethodPost, loginPath, ambiguousSourceHeaders)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("empty-form plus header CSRF status=%d", response.StatusCode)
	}
	closeBody(t, response)
	if writes := harness.store.Writes(); writes != 0 {
		t.Fatalf("ambiguous CSRF sources wrote %d sessions", writes)
	}
	// Model a sibling subdomain that can inject a parent-domain cookie and a
	// syntactically matching double-submit token but cannot know this runtime's
	// HMAC key. Secret equality alone must not be sufficient.
	forgedSecret := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	forgedTokenBytes := make([]byte, 96)
	for index := 0; index < 32; index++ {
		forgedTokenBytes[index] = 12
		forgedTokenBytes[32+index] = 12
	}
	forgedRequest, err := http.NewRequest(http.MethodPost, harness.server.URL+loginPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	forgedRequest.Header.Set("Cookie", sessionauth.DefaultCSRFCookieName+"="+forgedSecret)
	forgedRequest.Header.Set("X-Test-Form-Token", base64.RawURLEncoding.EncodeToString(forgedTokenBytes))
	forgedRequest.Header.Set("X-Test-Username", "admin")
	forgedRequest.Header.Set("X-Test-Password", "correct")
	response, err = rawClient.Do(forgedRequest)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("sibling-injected CSRF pair status=%d", response.StatusCode)
	}
	closeBody(t, response)
	if writes := harness.store.Writes(); writes != 0 {
		t.Fatalf("sibling-injected CSRF pair wrote %d sessions", writes)
	}

	invalidHeaders := http.Header{
		"X-Test-Form-Token": []string{preLoginToken},
		"X-Test-Username":   []string{"admin"},
		"X-Test-Password":   []string{"wrong"},
	}
	response = harness.Do(t, http.MethodPost, loginPath, invalidHeaders)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid login status=%d", response.StatusCode)
	}
	closeBody(t, response)
	if writes := harness.store.Writes(); writes != 0 {
		t.Fatalf("invalid login wrote %d sessions", writes)
	}

	wrongCSRFHeaders := invalidHeaders.Clone()
	wrongCSRFHeaders.Set("X-Test-Form-Token", strings.Repeat("A", 128))
	wrongCSRFHeaders.Set("X-Test-Password", "correct")
	response = harness.Do(t, http.MethodPost, loginPath, wrongCSRFHeaders)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong CSRF status=%d", response.StatusCode)
	}
	closeBody(t, response)
	if writes := harness.store.Writes(); writes != 0 {
		t.Fatalf("wrong CSRF wrote %d sessions", writes)
	}

	validHeaders := invalidHeaders.Clone()
	validHeaders.Set("X-Test-Password", "correct")
	validHeaders.Set("X-Test-Next", "https://attacker.invalid/")
	response = harness.Do(t, http.MethodPost, loginPath, validHeaders)
	if response.StatusCode != http.StatusFound || response.Header.Get("Location") != indexPath {
		t.Fatalf("valid login redirect status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	loginCookies := response.Cookies()
	firstSessionCookie := namedResponseCookie(t, loginCookies, sessionauth.DefaultSessionCookieName)
	rotatedCSRFCookie := namedResponseCookie(t, loginCookies, sessionauth.DefaultCSRFCookieName)
	if !firstSessionCookie.HttpOnly || firstSessionCookie.SameSite != http.SameSiteLaxMode || firstSessionCookie.MaxAge < 1 ||
		firstSessionCookie.Value == "" || rotatedCSRFCookie.Value == csrfCookie.Value {
		t.Fatal("login did not publish normalized rotated bearer cookies")
	}
	remaining := time.Until(firstSessionCookie.Expires)
	if remaining < 119*time.Minute || remaining > 121*time.Minute {
		t.Fatalf("session cookie lifetime=%v, want configured two-hour cap", remaining)
	}
	closeBody(t, response)
	if writes := harness.store.Writes(); writes != 1 {
		t.Fatalf("first valid login writes=%d, want 1", writes)
	}

	// The browser now presents the rotated CSRF cookie. The pre-login form token
	// must no longer verify, and credential/session state must remain unchanged.
	writesBeforeReplay := harness.store.Writes()
	replayHeaders := validHeaders.Clone()
	replayHeaders.Set("X-Test-Form-Token", preLoginToken)
	response = harness.Do(t, http.MethodPost, loginPath, replayHeaders)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("pre-login token replay status=%d", response.StatusCode)
	}
	closeBody(t, response)
	if writes := harness.store.Writes(); writes != writesBeforeReplay {
		t.Fatalf("replayed CSRF changed session writes from %d to %d", writesBeforeReplay, writes)
	}

	response = harness.Do(t, http.MethodGet, deniedPath, nil)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("missing permission status=%d", response.StatusCode)
	}
	closeBody(t, response)

	response = harness.Do(t, http.MethodGet, protectedPath, nil)
	protectedBody := readBody(t, response)
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(protectedBody, "operator\n") {
		t.Fatalf("protected response status=%d or body shape is invalid", response.StatusCode)
	}
	postLoginToken := strings.TrimPrefix(protectedBody, "operator\n")
	if len(postLoginToken) != 128 {
		t.Fatalf("post-login token length=%d", len(postLoginToken))
	}

	// A second valid login rotates an already-authenticated session instead of
	// retaining the fixation-prone ID.
	secondLoginHeaders := http.Header{
		sessionauth.DefaultCSRFHeader: []string{postLoginToken},
		"X-Test-Username":             []string{"admin"},
		"X-Test-Password":             []string{"correct"},
		"X-Test-Next":                 []string{protectedPath + "?id=1"},
	}
	response = harness.Do(t, http.MethodPost, loginPath, secondLoginHeaders)
	if response.StatusCode != http.StatusFound || response.Header.Get("Location") != protectedPath+"?id=1" {
		t.Fatalf("second login redirect status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	secondSessionCookie := namedResponseCookie(t, response.Cookies(), sessionauth.DefaultSessionCookieName)
	if secondSessionCookie.Value == firstSessionCookie.Value {
		t.Fatal("authenticated login retained the old session identifier")
	}
	closeBody(t, response)

	response = harness.Do(t, http.MethodGet, protectedPath, nil)
	protectedBody = readBody(t, response)
	postRotationToken := strings.TrimPrefix(protectedBody, "operator\n")
	logoutHeaders := http.Header{sessionauth.DefaultCSRFHeader: []string{postRotationToken}}
	response = harness.Do(t, http.MethodPost, logoutPath, logoutHeaders)
	if response.StatusCode != http.StatusFound {
		t.Fatalf("logout status=%d", response.StatusCode)
	}
	deletedSession := namedResponseCookie(t, response.Cookies(), sessionauth.DefaultSessionCookieName)
	deletedCSRF := namedResponseCookie(t, response.Cookies(), sessionauth.DefaultCSRFCookieName)
	if deletedSession.MaxAge >= 0 || deletedCSRF.MaxAge >= 0 || !deletedSession.Expires.Before(time.Now()) {
		t.Fatal("logout did not emit deletion cookies")
	}
	closeBody(t, response)

	response = harness.Do(t, http.MethodGet, protectedPath, nil)
	if response.StatusCode != http.StatusFound {
		t.Fatalf("post-logout protected status=%d", response.StatusCode)
	}
	closeBody(t, response)
}

func TestInactiveCredentialIsUniformAndWritesNoSession(t *testing.T) {
	harness := newHarness(t, false)
	defer harness.Close()
	response := harness.Do(t, http.MethodGet, loginPath, nil)
	token := readBody(t, response)
	headers := http.Header{
		"X-Test-Form-Token": []string{token},
		"X-Test-Username":   []string{"admin"},
		"X-Test-Password":   []string{"correct"},
	}
	response = harness.Do(t, http.MethodPost, loginPath, headers)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("inactive login status=%d", response.StatusCode)
	}
	closeBody(t, response)
	if writes := harness.store.Writes(); writes != 0 {
		t.Fatalf("inactive login wrote %d sessions", writes)
	}
}

func TestSafeNextFailsClosed(t *testing.T) {
	harness := newHarness(t, true)
	defer harness.Close()
	for _, candidate := range []string{
		"", "https://attacker.invalid/", "//attacker.invalid/", "/unknown/", "/admin/%2e%2e/", "/admin/\r\nX: y", strings.Repeat("x", 3000),
	} {
		if got := harness.runtime.SafeNext(candidate); got != indexPath {
			t.Fatalf("SafeNext(%q)=%q", candidate, got)
		}
	}
	for _, candidate := range []string{indexPath, protectedPath, protectedPath + "?id=1&q=go"} {
		if got := harness.runtime.SafeNext(candidate); got != candidate {
			t.Fatalf("SafeNext(%q)=%q", candidate, got)
		}
	}
}

func TestConcurrentAnonymousCSRFTokensDoNotWriteSessions(t *testing.T) {
	harness := newHarness(t, true)
	defer harness.Close()
	const workers = 32
	var wait sync.WaitGroup
	errorsFound := make(chan error, workers)
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			request, err := http.NewRequest(http.MethodGet, harness.server.URL+loginPath, nil)
			if err != nil {
				errorsFound <- err
				return
			}
			response, err := harness.server.Client().Do(request)
			if err != nil {
				errorsFound <- err
				return
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil || response.StatusCode != http.StatusOK || len(body) != 128 {
				errorsFound <- errors.New("anonymous CSRF response was invalid")
			}
		}()
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatal(err)
		}
	}
	if writes := harness.store.Writes(); writes != 0 {
		t.Fatalf("concurrent anonymous CSRF wrote %d sessions", writes)
	}
}

func TestCSRFFailureDiagnosticDoesNotExposeEntropyCause(t *testing.T) {
	t.Parallel()
	memory, err := sessions.NewMemoryStore(4)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := sessions.NewManager(memory, sessions.Config{})
	if err != nil {
		t.Fatal(err)
	}
	permission, _ := auth.NewPermission("article.article.view")
	principal, _ := auth.NewPrincipal(auth.PrincipalConfig{ID: "operator", Active: true, Permissions: []auth.Permission{permission}})
	credential, _ := auth.NewCredential("admin", "encoded-admin", principal)
	authenticator, err := auth.NewMemoryAuthenticator([]auth.Credential{credential}, plainHasher{})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := sessionauth.New(sessionauth.Config{
		Sessions:         manager,
		Authenticator:    authenticator,
		Authorizer:       auth.PrincipalAuthorizer{},
		FallbackPath:     indexPath,
		AllowedNextPaths: []string{indexPath},
		Random: io.MultiReader(
			bytes.NewReader(bytes.Repeat([]byte{11}, 32)),
			errorReader{err: errors.New("SHOULD_NOT_ESCAPE")},
		),
	})
	if err != nil {
		t.Fatal(err)
	}
	configured, err := settings.New(settings.Definition{
		ProjectName: "entropy_test",
		InstalledApps: []apps.Config{{
			Name:  "github.com/progresshans/godj/web/sessionauth_test",
			Label: "admin",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	observed := make(chan error, 1)
	application, err := web.NewApplication(web.Config{
		Settings: configured,
		Routes: []web.Route{{
			Name:   "admin:entropy",
			Method: http.MethodGet,
			Path:   loginPath,
			Handler: func(request *web.Request) (web.Response, error) {
				_, err := runtime.CSRFToken(request)
				observed <- err
				return statusResponse(http.StatusOK, "observed\n")
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	application.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://example.test"+loginPath, nil))
	entropyErr := <-observed
	if !errors.Is(entropyErr, &sessionauth.Error{Code: sessionauth.CodeEntropy}) {
		t.Fatalf("expected entropy error, got %v", entropyErr)
	}
	if strings.Contains(entropyErr.Error(), "SHOULD_NOT_ESCAPE") {
		t.Fatalf("entropy cause leaked: %v", entropyErr)
	}
}

type harness struct {
	runtime *sessionauth.Runtime
	store   *countingStore
	server  *httptest.Server
	client  *http.Client
}

func newHarness(t *testing.T, active bool) *harness {
	t.Helper()
	memory, err := sessions.NewMemoryStore(128)
	if err != nil {
		t.Fatal(err)
	}
	store := &countingStore{Store: memory}
	manager, err := sessions.NewManager(store, sessions.Config{})
	if err != nil {
		t.Fatal(err)
	}
	viewPermission, err := auth.NewPermission("article.article.view")
	if err != nil {
		t.Fatal(err)
	}
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "operator", Active: active, Permissions: []auth.Permission{viewPermission}})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := auth.NewCredential("admin", "encoded-admin", principal)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := auth.NewMemoryAuthenticator([]auth.Credential{credential}, plainHasher{})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := sessionauth.New(sessionauth.Config{
		Sessions:         manager,
		Authenticator:    authenticator,
		Authorizer:       auth.PrincipalAuthorizer{},
		SessionCookie:    sessionauth.CookieConfig{AllowInsecure: true, Lifetime: 2 * time.Hour},
		CSRFCookie:       sessionauth.CookieConfig{AllowInsecure: true},
		LoginPath:        loginPath,
		FallbackPath:     indexPath,
		AllowedNextPaths: []string{indexPath, loginPath, protectedPath, deniedPath, logoutPath},
	})
	if err != nil {
		t.Fatalf("runtime: %v (cause: %v)", err, errors.Unwrap(err))
	}
	configured, err := settings.New(settings.Definition{
		ProjectName: "sessionauth_test",
		InstalledApps: []apps.Config{{
			Name:  "github.com/progresshans/godj/web/sessionauth_test",
			Label: "admin",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	deletePermission, _ := auth.NewPermission("article.article.delete")
	loginGet := func(request *web.Request) (web.Response, error) {
		if err := runtime.VerifyCSRF(request, nil); err != nil {
			return web.Response{}, err
		}
		token, err := runtime.CSRFToken(request)
		if err != nil {
			return web.Response{}, err
		}
		response, err := web.HTML(http.StatusOK, []byte(token.Value()))
		if err != nil {
			return web.Response{}, err
		}
		return token.Apply(response)
	}
	loginPost := func(request *web.Request) (web.Response, error) {
		httpRequest := request.HTTP()
		if err := runtime.VerifyCSRF(request, httpRequest.Header.Values("X-Test-Form-Token")); err != nil {
			return statusResponse(http.StatusForbidden, "Forbidden\n")
		}
		result, err := runtime.Login(request, httpRequest.Header.Get("X-Test-Username"), httpRequest.Header.Get("X-Test-Password"))
		if errors.Is(err, auth.ErrInvalidCredentials) {
			return statusResponse(http.StatusUnauthorized, "Unauthorized\n")
		}
		if err != nil {
			return web.Response{}, err
		}
		header := make(http.Header)
		header.Set("Location", runtime.SafeNext(httpRequest.Header.Get("X-Test-Next")))
		response, err := web.NewResponse(http.StatusFound, header, []byte("Found\n"))
		if err != nil {
			return web.Response{}, err
		}
		return result.Apply(response)
	}
	protected := runtime.Require(viewPermission, func(request *web.Request, principal auth.Principal) (web.Response, error) {
		token, err := runtime.CSRFToken(request)
		if err != nil {
			return web.Response{}, err
		}
		response, err := web.HTML(http.StatusOK, []byte(principal.ID()+"\n"+token.Value()))
		if err != nil {
			return web.Response{}, err
		}
		return token.Apply(response)
	})
	denied := runtime.Require(deletePermission, func(*web.Request, auth.Principal) (web.Response, error) {
		return statusResponse(http.StatusOK, "unexpected\n")
	})
	logout := runtime.Require(viewPermission, func(request *web.Request, _ auth.Principal) (web.Response, error) {
		if err := runtime.VerifyCSRF(request, nil); err != nil {
			return statusResponse(http.StatusForbidden, "Forbidden\n")
		}
		change, err := runtime.Logout(request)
		if err != nil {
			return web.Response{}, err
		}
		header := make(http.Header)
		header.Set("Location", loginPath)
		response, err := web.NewResponse(http.StatusFound, header, []byte("Found\n"))
		if err != nil {
			return web.Response{}, err
		}
		return change.Apply(response)
	})
	application, err := web.NewApplication(web.Config{
		Settings: configured,
		Routes: []web.Route{
			{Name: "admin:login-get", Method: http.MethodGet, Path: loginPath, Handler: loginGet},
			{Name: "admin:login-post", Method: http.MethodPost, Path: loginPath, Handler: loginPost},
			{Name: "admin:protected", Method: http.MethodGet, Path: protectedPath, Handler: protected},
			{Name: "admin:denied", Method: http.MethodGet, Path: deniedPath, Handler: denied},
			{Name: "admin:logout", Method: http.MethodPost, Path: logoutPath, Handler: logout},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(application)
	jar, err := cookiejar.New(nil)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	client := server.Client()
	client.Jar = jar
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &harness{runtime: runtime, store: store, server: server, client: client}
}

func (h *harness) Close() { h.server.Close() }

func (h *harness) Do(t *testing.T, method, path string, header http.Header) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, h.server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if header != nil {
		request.Header = header.Clone()
	}
	response, err := h.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

type countingStore struct {
	sessions.Store
	writes atomic.Int64
}

func (s *countingStore) Create(ctx context.Context, record sessions.Record) (bool, error) {
	created, err := s.Store.Create(ctx, record)
	if err == nil && created {
		s.writes.Add(1)
	}
	return created, err
}

func (s *countingStore) Touch(ctx context.Context, id sessions.ID, accessedAt, idleExpiresAt time.Time) (sessions.Record, bool, error) {
	record, found, err := s.Store.Touch(ctx, id, accessedAt, idleExpiresAt)
	if err == nil && found {
		s.writes.Add(1)
	}
	return record, found, err
}

func (s *countingStore) Rotate(ctx context.Context, old sessions.ID, replacement sessions.Record) (bool, error) {
	rotated, err := s.Store.Rotate(ctx, old, replacement)
	if err == nil && rotated {
		s.writes.Add(1)
	}
	return rotated, err
}

func (s *countingStore) Delete(ctx context.Context, id sessions.ID) error {
	err := s.Store.Delete(ctx, id)
	if err == nil {
		s.writes.Add(1)
	}
	return err
}

func (s *countingStore) Writes() int64 { return s.writes.Load() }

type plainHasher struct{}

func (plainHasher) Hash(context.Context, string) (string, error) { return "encoded-dummy", nil }
func (plainHasher) Verify(_ context.Context, password, encoded string) (bool, error) {
	return password == "correct" && encoded == "encoded-admin", nil
}
func (plainHasher) ValidateEncoded(encoded string) error {
	if encoded != "encoded-admin" && encoded != "encoded-dummy" {
		return &auth.Error{Code: auth.CodeInvalidHash, Detail: "invalid test hash"}
	}
	return nil
}

type errorReader struct{ err error }

func (reader errorReader) Read([]byte) (int, error) { return 0, reader.err }

func statusResponse(status int, body string) (web.Response, error) {
	header := make(http.Header)
	header.Set("Content-Type", "text/plain; charset=utf-8")
	return web.NewResponse(status, header, []byte(body))
}

func readBody(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func closeBody(t *testing.T, response *http.Response) {
	t.Helper()
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
}

func namedResponseCookie(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("response cookie %q not found", name)
	return nil
}
