package worker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/api"
	apisessionauth "github.com/progresshans/godj/api/sessionauth"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/adminapp"
	"github.com/progresshans/godj/examples/article/apiapp"
	"github.com/progresshans/godj/examples/article/articleapp"
	"github.com/progresshans/godj/examples/article/webapp"
	"github.com/progresshans/godj/migrations"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/web"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

const (
	workerAdminBasePath      = "/admin"
	workerPrincipalID        = "article-development-admin"
	workerPrincipalProbePath = "/__godj_system_state_worker/principal/"
	workerMaximumSessions    = 256
	workerAuditCapacity      = 1024

	credentialTable = "godj_system_credential"
	sessionTable    = "godj_system_session"
	auditTable      = "godj_system_audit"
	articleTable    = "godj_conformance_article"
	articleModel    = "godj_conformance.article"
)

type observedBackend struct {
	*sqlite.Backend
	inserts        atomic.Int64
	updates        atomic.Int64
	deletes        atomic.Int64
	articleInserts atomic.Int64
	auditInserts   atomic.Int64
	auditFailures  atomic.Int64
	failNextAudit  atomic.Bool
}

func (backend *observedBackend) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	backend.observeInsert(plan.Table())
	if backend.consumeAuditFailure(plan.Table()) {
		return 0, errors.New("injected audit insert failure")
	}
	return backend.Backend.Insert(ctx, plan)
}

func (backend *observedBackend) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	backend.updates.Add(1)
	return backend.Backend.Update(ctx, plan)
}

func (backend *observedBackend) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	backend.deletes.Add(1)
	return backend.Backend.Delete(ctx, plan)
}

func (backend *observedBackend) Atomic(ctx context.Context, callback func(db.Session) error) error {
	return backend.Backend.Atomic(ctx, func(session db.Session) error {
		return callback(&observedSession{Session: session, backend: backend})
	})
}

func (backend *observedBackend) CoordinatedAtomic(
	ctx context.Context,
	callback func(db.Session) error,
) error {
	return backend.Backend.CoordinatedAtomic(ctx, func(session db.Session) error {
		return callback(&observedSession{Session: session, backend: backend})
	})
}

func (backend *observedBackend) observeInsert(table string) {
	backend.inserts.Add(1)
	switch table {
	case articleTable:
		backend.articleInserts.Add(1)
	case auditTable:
		backend.auditInserts.Add(1)
	}
}

func (backend *observedBackend) consumeAuditFailure(table string) bool {
	if table != auditTable || !backend.failNextAudit.CompareAndSwap(true, false) {
		return false
	}
	backend.auditFailures.Add(1)
	return true
}

type observedSession struct {
	db.Session
	backend *observedBackend
}

func (session *observedSession) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	session.backend.observeInsert(plan.Table())
	if session.backend.consumeAuditFailure(plan.Table()) {
		return 0, errors.New("injected audit insert failure")
	}
	return session.Session.Insert(ctx, plan)
}

func (session *observedSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	session.backend.updates.Add(1)
	return session.Session.Update(ctx, plan)
}

func (session *observedSession) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	session.backend.deletes.Add(1)
	return session.Session.Delete(ctx, plan)
}

type workerSite struct {
	backend *observedBackend
	runtime *systemstate.Runtime
	webAuth *websessionauth.Runtime
	app     *web.Application
}

func openWorkerSite(ctx context.Context, request Request) (*workerSite, error) {
	backend, err := openObservedBackend(ctx, request.Database)
	if err != nil {
		return nil, err
	}
	site, err := composeWorkerSite(ctx, backend, request.Username, request.Password)
	if err != nil {
		_ = backend.Close()
		return nil, err
	}
	return site, nil
}

func openObservedBackend(ctx context.Context, database string) (*observedBackend, error) {
	dsn, err := sqliteDataSource(database)
	if err != nil {
		return nil, err
	}
	backend, openErr := sqlite.Open(ctx, dsn)
	if openErr != nil {
		return nil, fail(errorDatabase)
	}
	return &observedBackend{Backend: backend}, nil
}

func sqliteDataSource(database string) (string, error) {
	trimmed := strings.TrimSpace(database)
	if trimmed == "" || strings.ContainsRune(trimmed, 0) || trimmed == ":memory:" ||
		strings.Contains(strings.ToLower(trimmed), "mode=memory") {
		return "", fail(errorInvalidRequest)
	}
	if strings.HasPrefix(trimmed, "file:") {
		return trimmed, nil
	}
	absolute, err := filepath.Abs(trimmed)
	if err != nil {
		return "", fail(errorInvalidRequest)
	}
	return "file:" + filepath.ToSlash(absolute) + "?mode=rwc", nil
}

func composeWorkerSite(
	ctx context.Context,
	backend *observedBackend,
	username, password string,
) (*workerSite, error) {
	projectSettings, err := settings.New(settings.Definition{
		ProjectName: "article_example",
		InstalledApps: []apps.Config{{
			Name:  "github.com/progresshans/godj/examples/article/models",
			Label: apiapp.Namespace,
		}},
	})
	if err != nil {
		return nil, fail(errorApplication)
	}
	hasher, err := auth.NewDefaultPBKDF2()
	if err != nil {
		return nil, fail(errorApplication)
	}
	runtime, err := systemstate.Open(ctx, backend, systemstate.BootstrapConfig{
		Username:    username,
		Password:    password,
		PrincipalID: workerPrincipalID,
		Active:      true,
		Permissions: []auth.Permission{
			admin.DefaultAccessPermission,
			articleapp.ArticleViewPermission,
			articleapp.ArticleAddPermission,
			articleapp.ArticleChangePermission,
			articleapp.ArticleDeletePermission,
		},
		PasswordHasher: hasher,
		MaxSessions:    workerMaximumSessions,
		AuditCapacity:  workerAuditCapacity,
	})
	if err != nil {
		return nil, fail(errorApplication)
	}
	adminService, err := adminapp.NewDurableService(runtime, runtime)
	if err != nil {
		return nil, fail(errorApplication)
	}
	builder := admin.NewBuilder(projectSettings.Apps())
	if err := adminapp.RegisterArticle(builder, adminService); err != nil {
		return nil, fail(errorApplication)
	}
	registry, err := builder.Build()
	if err != nil {
		return nil, fail(errorApplication)
	}
	manager, err := sessions.NewManager(runtime.SessionStore(), sessions.Config{})
	if err != nil {
		return nil, fail(errorApplication)
	}
	allowedNext, err := admin.SiteAllowedNextPaths(registry, workerAdminBasePath)
	if err != nil {
		return nil, fail(errorApplication)
	}
	webAuth, err := websessionauth.New(websessionauth.Config{
		Sessions:         manager,
		Authenticator:    runtime.Authenticator(),
		Authorizer:       auth.PrincipalAuthorizer{},
		SessionCookie:    websessionauth.CookieConfig{Path: "/", AllowInsecure: true},
		CSRFCookie:       websessionauth.CookieConfig{Path: "/", AllowInsecure: true},
		LoginPath:        workerAdminBasePath + "/login/",
		FallbackPath:     workerAdminBasePath + "/",
		AllowedNextPaths: allowedNext,
	})
	if err != nil {
		return nil, fail(errorApplication)
	}
	adminSite, err := admin.NewSite(admin.SiteConfig{
		Apps:      projectSettings.Apps(),
		Namespace: apiapp.Namespace,
		BasePath:  workerAdminBasePath,
		Registry:  registry,
		Auth:      webAuth,
	})
	if err != nil {
		return nil, fail(errorApplication)
	}
	apiAuth, err := apisessionauth.New(webAuth)
	if err != nil {
		return nil, fail(errorApplication)
	}
	articleAPI, err := apiapp.New(runtime, apiAuth)
	if err != nil {
		return nil, fail(errorApplication)
	}
	middleware, err := apiapp.Middleware()
	if err != nil {
		return nil, fail(errorApplication)
	}
	routes := append(adminSite.Routes(), articleAPI.Routes()...)
	routes = append(routes, web.Route{
		Name:    apiapp.Namespace + ":worker-principal",
		Method:  http.MethodGet,
		Path:    workerPrincipalProbePath,
		Handler: webAuth.Require(articleapp.ArticleViewPermission, principalProbeHandler),
	})
	application, err := webapp.NewComposedApplication(runtime, routes, middleware)
	if err != nil {
		return nil, fail(errorApplication)
	}
	return &workerSite{backend: backend, runtime: runtime, webAuth: webAuth, app: application}, nil
}

type principalProbe struct {
	Authenticated bool     `json:"authenticated"`
	Active        bool     `json:"active"`
	Permission    bool     `json:"permission"`
	PrincipalID   string   `json:"principal_id"`
	Permissions   []string `json:"permissions"`
}

func principalProbeHandler(_ *web.Request, principal auth.Principal) (web.Response, error) {
	permissions := principal.Permissions()
	encodedPermissions := make([]string, len(permissions))
	for index := range permissions {
		encodedPermissions[index] = string(permissions[index])
	}
	payload, err := json.Marshal(principalProbe{
		Authenticated: principal.Authenticated(),
		Active:        principal.Active(),
		Permission:    principal.Has(articleapp.ArticleViewPermission),
		PrincipalID:   principal.ID(),
		Permissions:   encodedPermissions,
	})
	if err != nil {
		return web.Response{}, fail(errorProtocol)
	}
	header := make(http.Header)
	header.Set("Content-Type", api.JSONContentType)
	response, err := web.NewResponse(http.StatusOK, header, payload)
	if err != nil {
		return web.Response{}, fail(errorProtocol)
	}
	return response, nil
}

func migrateArticleAndSystem(ctx context.Context, request Request, backend *observedBackend) (bool, error) {
	root := strings.TrimSpace(request.RepositoryRoot)
	if root == "" {
		root = findRepositoryRoot()
	}
	document, err := os.ReadFile(filepath.Join(root, "examples", "article", "testdata", "postgres", "0001_initial.godj.json"))
	if err != nil {
		return false, fail(errorMigration)
	}
	loaded, report, err := migrationdefinition.Load(
		migrationdefinition.Source{
			SourceID: "examples/article/testdata/postgres/0001_initial.godj.json",
			Document: document,
		},
		systemstate.InitialDefinitionSource(),
	)
	if err != nil || report.DocumentsReceived != 2 || report.HeadersValidated != 2 ||
		report.OperationsDecoded != 4 || report.PlannerConstruction != 1 ||
		report.DefinitionsPublished != 2 || report.DefinitionSetsPublished != 1 {
		return false, fail(errorMigration)
	}
	state, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
	if err != nil {
		return false, fail(errorMigration)
	}
	_, articleApplied := state.Schema(apiapp.Namespace)
	_, systemApplied := state.Schema(systemstate.InitialMigrationKey().App)
	return articleApplied && systemApplied, nil
}

func migrateSystemOnly(ctx context.Context, backend *observedBackend) error {
	loaded, report, err := migrationdefinition.Load(systemstate.InitialDefinitionSource())
	if err != nil || report.DocumentsReceived != 1 || report.HeadersValidated != 1 ||
		report.OperationsDecoded != 3 || report.PlannerConstruction != 1 ||
		report.DefinitionsPublished != 1 || report.DefinitionSetsPublished != 1 {
		return fail(errorMigration)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		return fail(errorMigration)
	}
	return nil
}

func findRepositoryRoot() string {
	directory, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "."
		}
		directory = parent
	}
}

func countRows(ctx context.Context, queryer db.Queryer, table string) (int, error) {
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	plan, err := query.NewPlan(table, []query.FieldRef{id}).WithLimit(1 << 16)
	if err != nil {
		return 0, fail(errorPersistence)
	}
	rows, err := queryer.Query(ctx, plan)
	if err != nil || rows == nil {
		if rows != nil {
			_ = rows.Close()
		}
		return 0, fail(errorPersistence)
	}
	count := 0
	for rows.Next() {
		var ignored int64
		if err := rows.Scan(&ignored); err != nil {
			_ = rows.Close()
			return 0, fail(errorPersistence)
		}
		count++
	}
	iterationErr := rows.Err()
	closeErr := rows.Close()
	if iterationErr != nil || closeErr != nil {
		return 0, fail(errorPersistence)
	}
	return count, nil
}

func populateCounts(ctx context.Context, response Response, queryer db.Queryer) (Response, error) {
	var err error
	if response.CredentialRows, err = countRows(ctx, queryer, credentialTable); err != nil {
		return response, err
	}
	if response.SessionRows, err = countRows(ctx, queryer, sessionTable); err != nil {
		return response, err
	}
	if response.AuditRows, err = countRows(ctx, queryer, auditTable); err != nil {
		return response, err
	}
	if response.ArticleRows, err = countRows(ctx, queryer, articleTable); err != nil {
		return response, err
	}
	return response, nil
}

type httpHarness struct {
	server *httptest.Server
	client *http.Client
	jar    http.CookieJar
	base   *url.URL
}

func newHTTPHarness(application *web.Application, cookies CookieBundle) (*httpHarness, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fail(errorHTTP)
	}
	server := httptest.NewServer(application)
	parsed, err := url.Parse(server.URL)
	if err != nil {
		server.Close()
		return nil, fail(errorHTTP)
	}
	seed := make([]*http.Cookie, 0, 2)
	if cookies.Session != "" {
		seed = append(seed, &http.Cookie{Name: websessionauth.DefaultSessionCookieName, Value: cookies.Session, Path: "/", HttpOnly: true})
	}
	if cookies.CSRF != "" {
		seed = append(seed, &http.Cookie{Name: websessionauth.DefaultCSRFCookieName, Value: cookies.CSRF, Path: "/", HttpOnly: true})
	}
	jar.SetCookies(parsed, seed)
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &httpHarness{server: server, client: client, jar: jar, base: parsed}, nil
}

func (harness *httpHarness) close() {
	if harness == nil {
		return
	}
	harness.client.CloseIdleConnections()
	harness.server.Close()
}

func (harness *httpHarness) cookies() CookieBundle {
	if harness == nil {
		return CookieBundle{}
	}
	result := CookieBundle{}
	for _, cookie := range harness.jar.Cookies(harness.base) {
		switch cookie.Name {
		case websessionauth.DefaultSessionCookieName:
			result.Session = cookie.Value
		case websessionauth.DefaultCSRFCookieName:
			result.CSRF = cookie.Value
		}
	}
	return result
}

type httpResult struct {
	status  int
	header  http.Header
	body    []byte
	cookies []*http.Cookie
}

func (harness *httpHarness) do(
	ctx context.Context,
	method, path, accept, contentType, body, csrf string,
) (httpResult, error) {
	request, err := http.NewRequestWithContext(ctx, method, harness.server.URL+path, strings.NewReader(body))
	if err != nil {
		return httpResult{}, fail(errorHTTP)
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
	response, err := harness.client.Do(request)
	if err != nil {
		return httpResult{}, fail(errorHTTP)
	}
	payload, readErr := ioReadAllBounded(response.Body, 1<<20)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		return httpResult{}, fail(errorHTTP)
	}
	return httpResult{
		status:  response.StatusCode,
		header:  response.Header.Clone(),
		body:    payload,
		cookies: response.Cookies(),
	}, nil
}

func ioReadAllBounded(reader interface{ Read([]byte) (int, error) }, maximum int64) ([]byte, error) {
	payload, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil || int64(len(payload)) > maximum {
		return nil, fail(errorHTTP)
	}
	return payload, nil
}
