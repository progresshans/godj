package godj

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/bearerauth"
	apisessionauth "github.com/progresshans/godj/api/sessionauth"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/article/apiapp"
	"github.com/progresshans/godj/examples/article/articleapp"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

const gdj0047TokenDigestDomain = "godj/conformance/api-authentication/verifier/v1\x00"

type gdj0047VerificationRecord struct {
	principal auth.Principal
	err       error
}

// gdj0047DigestVerifier keeps only domain-separated SHA-256 lookup keys. Raw
// bearer material exists at the request boundary and is accessed exactly once,
// inside Verify, before the borrowed Token is released by bearerauth.Runtime.
type gdj0047DigestVerifier struct {
	records  map[[sha256.Size]byte]gdj0047VerificationRecord
	inspect  func(bearerauth.Token)
	calls    atomic.Int64
	success  atomic.Int64
	retain   atomic.Bool
	mu       sync.Mutex
	retained bearerauth.Token
}

func newGDJ0047DigestVerifier(records map[string]gdj0047VerificationRecord) *gdj0047DigestVerifier {
	byDigest := make(map[[sha256.Size]byte]gdj0047VerificationRecord, len(records))
	for label, record := range records {
		byDigest[gdj0047Digest(gdj0047RawToken(label))] = record
	}
	return &gdj0047DigestVerifier{records: byDigest}
}

func (verifier *gdj0047DigestVerifier) Verify(ctx context.Context, token bearerauth.Token) (auth.Principal, error) {
	verifier.calls.Add(1)
	if ctx == nil {
		return auth.Principal{}, errors.New("GDJ-0047 verifier context is nil")
	}
	if err := ctx.Err(); err != nil {
		return auth.Principal{}, err
	}
	if verifier.inspect != nil {
		verifier.inspect(token)
	}
	if verifier.retain.Load() {
		verifier.mu.Lock()
		verifier.retained = token
		verifier.mu.Unlock()
	}
	encoded := token.Encoded()
	record, found := verifier.records[gdj0047Digest(encoded)]
	if !found {
		return auth.Principal{}, auth.ErrInvalidCredentials
	}
	if record.err != nil {
		return auth.Principal{}, record.err
	}
	if record.principal.Authenticated() {
		verifier.success.Add(1)
	}
	return record.principal, nil
}

func gdj0047Digest(encoded string) [sha256.Size]byte {
	hash := sha256.New()
	_, _ = io.WriteString(hash, gdj0047TokenDigestDomain)
	_, _ = io.WriteString(hash, encoded)
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return digest
}

func gdj0047RawToken(label string) string {
	digest := sha256.Sum256([]byte("godj/conformance/api-authentication/raw/v1\x00" + label))
	return "gdj0047." + hex.EncodeToString(digest[:])
}

func gdj0047SizedToken(label string, size int) string {
	if size <= 0 {
		return ""
	}
	seed := gdj0047RawToken(label)
	repeated := strings.Repeat(seed, (size+len(seed)-1)/len(seed))
	return repeated[:size]
}

type gdj0047VerifierSnapshot struct {
	calls   int64
	success int64
}

func (verifier *gdj0047DigestVerifier) snapshot() gdj0047VerifierSnapshot {
	return gdj0047VerifierSnapshot{calls: verifier.calls.Load(), success: verifier.success.Load()}
}

func (verifier *gdj0047DigestVerifier) consumeRetainedReleased() bool {
	verifier.mu.Lock()
	defer verifier.mu.Unlock()
	released := verifier.retained.Encoded() == ""
	verifier.retained = bearerauth.Token{}
	return released
}

type gdj0047Authorizer struct {
	cause           error
	failPrincipalID string
	causes          map[string]error
	calls           atomic.Int64
}

func (authorizer *gdj0047Authorizer) Allowed(ctx context.Context, principal auth.Principal, permission auth.Permission) (bool, error) {
	authorizer.calls.Add(1)
	if cause := authorizer.causes[principal.ID()]; cause != nil {
		return false, cause
	}
	if authorizer.cause != nil && (authorizer.failPrincipalID == "" || authorizer.failPrincipalID == principal.ID()) {
		return false, authorizer.cause
	}
	return (auth.PrincipalAuthorizer{}).Allowed(ctx, principal, permission)
}

type gdj0047InvocationSnapshot struct {
	calls       int64
	principalID string
}

// gdj0047CountingAuthentication decorates one real authentication Runtime.
// It counts only invocations that crossed parsing, verification, and
// authorization and records the exact Principal passed to the Article handler.
type gdj0047CountingAuthentication struct {
	inner       api.Authentication
	calls       atomic.Int64
	mu          sync.Mutex
	principalID string
}

func (authentication *gdj0047CountingAuthentication) Require(
	permission auth.Permission,
	handler api.AuthenticatedHandler,
) (web.Handler, error) {
	return authentication.inner.Require(permission, func(request *web.Request, principal auth.Principal) (web.Response, error) {
		authentication.calls.Add(1)
		authentication.mu.Lock()
		authentication.principalID = principal.ID()
		authentication.mu.Unlock()
		return handler(request, principal)
	})
}

func (authentication *gdj0047CountingAuthentication) snapshot() gdj0047InvocationSnapshot {
	authentication.mu.Lock()
	defer authentication.mu.Unlock()
	return gdj0047InvocationSnapshot{calls: authentication.calls.Load(), principalID: authentication.principalID}
}

type gdj0047ProfileBackend struct {
	owner       *articleAPIObservedBackend
	atomicCalls atomic.Int64
}

func (backend *gdj0047ProfileBackend) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	return backend.owner.Query(ctx, plan)
}

func (backend *gdj0047ProfileBackend) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	return backend.owner.Insert(ctx, plan)
}

func (backend *gdj0047ProfileBackend) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	return backend.owner.Update(ctx, plan)
}

func (backend *gdj0047ProfileBackend) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	return backend.owner.Delete(ctx, plan)
}

func (backend *gdj0047ProfileBackend) Atomic(ctx context.Context, callback func(db.Session) error) error {
	backend.atomicCalls.Add(1)
	return backend.owner.Atomic(ctx, callback)
}

func (verifier *gdj0047DigestVerifier) addEncoded(encoded string, record gdj0047VerificationRecord) {
	verifier.records[gdj0047Digest(encoded)] = record
}

type gdj0047LockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *gdj0047LockedBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(data)
}

func (buffer *gdj0047LockedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

type gdj0047APIFixture struct {
	raw                   io.Closer
	observed              *articleAPIObservedBackend
	repository            articleapp.Repository
	verifier              *gdj0047DigestVerifier
	authorizer            *gdj0047Authorizer
	bearerRuntime         *bearerauth.Runtime
	sessionRuntime        *apisessionauth.Runtime
	bearerAuthentication  *gdj0047CountingAuthentication
	sessionAuthentication *gdj0047CountingAuthentication
	bearerBackend         *gdj0047ProfileBackend
	sessionBackend        *gdj0047ProfileBackend
	bearerAdapter         *apiapp.Application
	sessionAdapter        *apiapp.Application
	bearerApplication     *web.Application
	sessionApplication    *web.Application
	sessionCookie         *http.Cookie
	logs                  *gdj0047LockedBuffer
	artifacts             *gdj0047LockedBuffer
}

func newGDJ0047APIFixture(
	ctx context.Context,
	contractID string,
	records map[string]gdj0047VerificationRecord,
	authorizer *gdj0047Authorizer,
) (*gdj0047APIFixture, error) {
	raw, err := openEmptyArticleDatabase(ctx, contractID)
	if err != nil {
		return nil, err
	}
	fail := func(cause error) (*gdj0047APIFixture, error) {
		return nil, errors.Join(cause, raw.Close())
	}
	observed := &articleAPIObservedBackend{raw: raw}
	repository, err := articleapp.NewRepository(observed)
	if err != nil {
		return fail(fmt.Errorf("create GDJ-0047 Article repository: %w", err))
	}
	if authorizer == nil {
		authorizer = &gdj0047Authorizer{}
	}
	verifier := newGDJ0047DigestVerifier(records)
	bearerRuntime, err := bearerauth.New(bearerauth.Config{Verifier: verifier, Authorizer: authorizer})
	if err != nil {
		return fail(fmt.Errorf("create GDJ-0047 Bearer runtime: %w", err))
	}
	bearerAuthentication := &gdj0047CountingAuthentication{inner: bearerRuntime}
	bearerBackend := &gdj0047ProfileBackend{owner: observed}
	bearerAdapter, err := apiapp.New(bearerBackend, bearerAuthentication)
	if err != nil {
		return fail(fmt.Errorf("create GDJ-0047 Bearer Article adapter: %w", err))
	}

	sessionPrincipal, err := gdj0047Principal(
		"gdj0047-session-user",
		articleapp.ArticleViewPermission,
		articleapp.ArticleAddPermission,
		articleapp.ArticleChangePermission,
		articleapp.ArticleDeletePermission,
	)
	if err != nil {
		return fail(err)
	}
	store, err := sessions.NewMemoryStore(16)
	if err != nil {
		return fail(err)
	}
	fixedTime := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	manager, err := sessions.NewManager(store, sessions.Config{
		Clock:  func() time.Time { return fixedTime },
		Random: bytes.NewReader(articleAPIEntropy(8192, 47)),
	})
	if err != nil {
		return fail(err)
	}
	sessionCookie, err := articleAPISessionCookie(ctx, manager, sessionPrincipal)
	if err != nil {
		return fail(err)
	}
	webRuntime, err := websessionauth.New(websessionauth.Config{
		Sessions:      manager,
		Authenticator: articleAPIAuthenticator{principals: map[string]auth.Principal{"session": sessionPrincipal}},
		Authorizer:    auth.PrincipalAuthorizer{},
		SessionCookie: websessionauth.CookieConfig{Path: "/", AllowInsecure: true},
		CSRFCookie:    websessionauth.CookieConfig{Path: "/", AllowInsecure: true},
		LoginPath:     "/login/",
		FallbackPath:  apiapp.ListPath,
		Clock:         func() time.Time { return fixedTime },
		Random:        bytes.NewReader(articleAPIEntropy(1<<20, 53)),
	})
	if err != nil {
		return fail(err)
	}
	sessionRuntime, err := apisessionauth.New(webRuntime)
	if err != nil {
		return fail(err)
	}
	sessionAuthentication := &gdj0047CountingAuthentication{inner: sessionRuntime}
	sessionBackend := &gdj0047ProfileBackend{owner: observed}
	sessionAdapter, err := apiapp.New(sessionBackend, sessionAuthentication)
	if err != nil {
		return fail(fmt.Errorf("create GDJ-0047 session Article adapter: %w", err))
	}

	logs := &gdj0047LockedBuffer{}
	bearerApplication, err := gdj0047WebApplication(bearerAdapter.Routes(), logs)
	if err != nil {
		return fail(err)
	}
	sessionApplication, err := gdj0047WebApplication(sessionAdapter.Routes(), logs)
	if err != nil {
		return fail(err)
	}
	return &gdj0047APIFixture{
		raw: raw, observed: observed, repository: repository,
		verifier: verifier, authorizer: authorizer,
		bearerRuntime: bearerRuntime, sessionRuntime: sessionRuntime,
		bearerAuthentication: bearerAuthentication, sessionAuthentication: sessionAuthentication,
		bearerBackend: bearerBackend, sessionBackend: sessionBackend,
		bearerAdapter: bearerAdapter, sessionAdapter: sessionAdapter,
		bearerApplication: bearerApplication, sessionApplication: sessionApplication,
		sessionCookie: sessionCookie, logs: logs, artifacts: &gdj0047LockedBuffer{},
	}, nil
}

func gdj0047WebApplication(routes []web.Route, logs io.Writer) (*web.Application, error) {
	middleware, err := apiapp.Middleware()
	if err != nil {
		return nil, err
	}
	return gdj0047ConfiguredApplication(routes, middleware, logs)
}

func gdj0047ConfiguredApplication(routes []web.Route, middleware []web.Middleware, logs io.Writer) (*web.Application, error) {
	configured, err := settings.New(settings.Definition{
		ProjectName: "godj_api_authentication_conformance",
		InstalledApps: []apps.Config{{
			Name:  "github.com/progresshans/godj/examples/article/apiapp",
			Label: apiapp.Namespace,
		}},
	})
	if err != nil {
		return nil, err
	}
	return web.NewApplication(web.Config{
		Settings:   configured,
		Routes:     routes,
		Middleware: middleware,
		Logger:     slog.New(slog.NewTextHandler(logs, nil)),
	})
}

type gdj0047CapturedError struct {
	calls int64
	err   error
}

type gdj0047ErrorCapture struct {
	mu    sync.Mutex
	calls int64
	err   error
}

func (capture *gdj0047ErrorCapture) middleware(next web.Handler) web.Handler {
	return func(request *web.Request) (web.Response, error) {
		response, err := next(request)
		capture.mu.Lock()
		capture.calls++
		capture.err = err
		capture.mu.Unlock()
		return response, err
	}
}

func (capture *gdj0047ErrorCapture) snapshot() gdj0047CapturedError {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return gdj0047CapturedError{calls: capture.calls, err: capture.err}
}

func gdj0047ProtectedErrorApplication(
	runtime *bearerauth.Runtime,
	capture *gdj0047ErrorCapture,
	handlerCalls *atomic.Int64,
	logs io.Writer,
) (*web.Application, error) {
	protected, err := runtime.Require(articleapp.ArticleViewPermission, func(*web.Request, auth.Principal) (web.Response, error) {
		handlerCalls.Add(1)
		return api.NoContent()
	})
	if err != nil {
		return nil, err
	}
	return gdj0047ConfiguredApplication([]web.Route{{
		Name: apiapp.Namespace + ":gdj0047-error-probe", Method: http.MethodGet,
		Path: "/__godj0047_error_probe__/", Handler: protected,
	}}, []web.Middleware{capture.middleware}, logs)
}

func gdj0047RequireInvocation(
	before gdj0047InvocationSnapshot,
	after gdj0047InvocationSnapshot,
	wantCalls int64,
	wantPrincipalID string,
) error {
	if after.calls-before.calls != wantCalls {
		return fmt.Errorf("authenticated handler invocation delta %d; want %d", after.calls-before.calls, wantCalls)
	}
	if wantCalls > 0 && after.principalID != wantPrincipalID {
		return fmt.Errorf("authenticated handler principal %q; want %q", after.principalID, wantPrincipalID)
	}
	return nil
}

func gdj0047Principal(id string, permissions ...auth.Permission) (auth.Principal, error) {
	return auth.NewPrincipal(auth.PrincipalConfig{ID: id, Active: true, Permissions: permissions})
}

func (fixture *gdj0047APIFixture) close() error {
	if fixture == nil || fixture.raw == nil {
		return nil
	}
	return fixture.raw.Close()
}

func (fixture *gdj0047APIFixture) seed(ctx context.Context, rows ...articleAPISeed) error {
	for _, row := range rows {
		if row.id <= 0 || row.title == "" {
			return errors.New("seed GDJ-0047 Article row: invalid row")
		}
		backend := fixture.observed.raw
		if _, err := backend.ExecContext(
			ctx,
			`INSERT INTO "godj_conformance_article" ("id", "title", "published", "summary") VALUES (?, ?, ?, ?)`,
			row.id, row.title, row.published, row.summary,
		); err != nil {
			return fmt.Errorf("seed GDJ-0047 Article row %d: %w", row.id, err)
		}
	}
	return nil
}

func (fixture *gdj0047APIFixture) state(ctx context.Context) (protocol.Value, error) {
	page, err := fixture.repository.List(ctx, articleapp.ListOptions{Limit: articleapp.MaximumPageSize})
	if err != nil {
		return protocol.Value{}, err
	}
	rows := make([]protocol.Value, len(page.Articles))
	for index, article := range page.Articles {
		rows[index] = articleAPIArticleValue(article)
	}
	return protocol.List(rows...), nil
}

func (fixture *gdj0047APIFixture) count(ctx context.Context) (int64, error) {
	page, err := fixture.repository.List(ctx, articleAPIListCountOptions())
	if err != nil {
		return 0, err
	}
	return page.Total, nil
}

type gdj0047RequestOptions struct {
	header      http.Header
	body        string
	contentType string
	csrf        string
}

type gdj0047Client struct {
	fixture     *gdj0047APIFixture
	application *web.Application
	cookies     map[string]*http.Cookie
}

func (fixture *gdj0047APIFixture) bearerClient(cookies ...*http.Cookie) *gdj0047Client {
	return fixture.client(fixture.bearerApplication, cookies...)
}

func (fixture *gdj0047APIFixture) sessionClient(cookies ...*http.Cookie) *gdj0047Client {
	return fixture.client(fixture.sessionApplication, cookies...)
}

func (fixture *gdj0047APIFixture) client(application *web.Application, cookies ...*http.Cookie) *gdj0047Client {
	client := &gdj0047Client{fixture: fixture, application: application, cookies: make(map[string]*http.Cookie)}
	for _, cookie := range cookies {
		if cookie == nil {
			continue
		}
		clone := *cookie
		client.cookies[clone.Name] = &clone
	}
	return client
}

func (client *gdj0047Client) do(
	ctx context.Context,
	method string,
	target string,
	options gdj0047RequestOptions,
) (articleAPIHTTPResponse, error) {
	request := httptest.NewRequestWithContext(ctx, method, "http://conformance.example"+target, strings.NewReader(options.body))
	if options.header != nil {
		request.Header = options.header.Clone()
	}
	request.Header.Set("Accept", api.JSONContentType)
	if options.contentType != "" {
		request.Header.Set("Content-Type", options.contentType)
	}
	if options.csrf != "" {
		request.Header.Set(websessionauth.DefaultCSRFHeader, options.csrf)
	}
	names := make([]string, 0, len(client.cookies))
	for name := range client.cookies {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		request.AddCookie(client.cookies[name])
	}
	recorder := httptest.NewRecorder()
	client.application.ServeHTTP(recorder, request)
	response := recorder.Result()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return articleAPIHTTPResponse{}, err
	}
	for _, cookie := range response.Cookies() {
		clone := *cookie
		if clone.MaxAge < 0 {
			delete(client.cookies, clone.Name)
			continue
		}
		client.cookies[clone.Name] = &clone
	}
	_, _ = io.WriteString(client.fixture.artifacts, fmt.Sprint(response.Header))
	_, _ = client.fixture.artifacts.Write(body)
	return articleAPIHTTPResponse{status: response.StatusCode, header: response.Header.Clone(), body: body}, nil
}

func (client *gdj0047Client) authenticated(
	ctx context.Context,
	method string,
	target string,
	options gdj0047RequestOptions,
) (protocol.Value, articleAPIHTTPResponse, error) {
	before := client.fixture.verifier.snapshot()
	response, err := client.do(ctx, method, target, options)
	if err != nil {
		return protocol.Value{}, articleAPIHTTPResponse{}, err
	}
	after := client.fixture.verifier.snapshot()
	value, err := gdj0047AuthenticationResponse(response, after.success-before.success == 1)
	return value, response, err
}

func (client *gdj0047Client) csrf(ctx context.Context) (string, error) {
	response, err := client.do(ctx, http.MethodGet, apiapp.ListPath, gdj0047RequestOptions{})
	if err != nil {
		return "", err
	}
	if response.status != http.StatusOK {
		return "", fmt.Errorf("provision GDJ-0047 CSRF token: status %d", response.status)
	}
	token := response.header.Get(websessionauth.DefaultCSRFHeader)
	if token == "" {
		return "", errors.New("provision GDJ-0047 CSRF token: response token is missing")
	}
	return token, nil
}

func gdj0047Authorization(value string) http.Header {
	return http.Header{"Authorization": {value}}
}

func gdj0047Bearer(label string) http.Header {
	return gdj0047Authorization("Bearer " + gdj0047RawToken(label))
}

func gdj0047AuthenticationResponse(response articleAPIHTTPResponse, authenticated bool) (protocol.Value, error) {
	contentType := response.header.Get("Content-Type")
	if before, _, found := strings.Cut(contentType, ";"); found {
		contentType = before
	}
	errorCodes := protocol.Null()
	if response.status >= http.StatusBadRequest && strings.HasPrefix(contentType, api.JSONContentType) {
		codes, err := articleAPIErrorCodes(response.body)
		if err != nil {
			return protocol.Value{}, err
		}
		errorCodes = codes
	}
	return protocol.Object(map[string]protocol.Value{
		"authenticated":    protocol.Boolean(authenticated),
		"body_empty":       protocol.Boolean(len(response.body) == 0),
		"content_type":     articleAPIOptionalString(contentType),
		"csrf_header":      protocol.Boolean(response.header.Get(websessionauth.DefaultCSRFHeader) != ""),
		"error_codes":      errorCodes,
		"response_cookies": parameterRoutingInt(len(response.header.Values("Set-Cookie"))),
		"status":           parameterRoutingInt(response.status),
		"www_authenticate": articleAPIOptionalString(response.header.Get("WWW-Authenticate")),
	}), nil
}

func (fixture *gdj0047APIFixture) finalize(
	observation protocol.Observation,
	exactCredentialCanaries ...string,
) (protocol.Observation, error) {
	encoded, err := json.Marshal(observation)
	if err != nil {
		return protocol.Observation{}, err
	}
	visible := string(encoded) + fixture.artifacts.String() + fixture.logs.String()
	for index, canary := range exactCredentialCanaries {
		if canary == "" {
			return protocol.Observation{}, fmt.Errorf("GDJ-0047 credential canary %d is empty", index)
		}
		if strings.Contains(visible, canary) {
			return protocol.Observation{}, fmt.Errorf("GDJ-0047 credential canary %d escaped", index)
		}
	}
	return observation, nil
}
