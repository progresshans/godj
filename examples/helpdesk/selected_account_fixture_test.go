package helpdesk_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	accountaudit "github.com/progresshans/godj/account/audit"
	auditmodels "github.com/progresshans/godj/account/audit/models"
	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/accounts"
	"github.com/progresshans/godj/examples/helpdesk/migrationdefs"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/examples/helpdesk/project"
	identitymodels "github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/settings/database"
	"github.com/progresshans/godj/web"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

type selectedHelpdeskBackend interface {
	database.Backend
	helpdesk.SelectedBackend
}

type selectedHelpdeskFixture struct {
	backend           selectedHelpdeskBackend
	open              func(context.Context) (selectedHelpdeskBackend, error)
	runtime           *accounts.Runtime
	credential        auth.Credential
	account           identitymodels.User
	permissions       map[auth.Permission]int64
	ownership         domain.Receipt
	category, outside models.Category
	ticket, private   models.Ticket
	label             models.Label
	link              models.TicketLabel
	report            models.ServiceReport
}

const selectedHelpdeskPassword = "Quartz-river-compass-73!"

func TestSelectedAccountHelpdeskSQLiteScopeLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "selected.sqlite3") + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	open := func(ctx context.Context) (selectedHelpdeskBackend, error) { return sqlite.Open(ctx, path) }
	backend, err := open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	runSelectedHelpdeskScope(t, newSelectedHelpdeskFixture(t, backend, open))
}

func TestSelectedAccountHelpdeskPostgresScopeLifecycle(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL"))
	if url == "" {
		if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
			t.Fatal("GODJ_REQUIRE_POSTGRES=1 requires GODJ_TEST_POSTGRES_URL")
		}
		t.Skip("PostgreSQL environment is unavailable")
	}
	ctx, cancel := context.WithTimeout(t.Context(), helpdeskConsumerTimeout)
	defer cancel()
	connection, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal("connect selected Helpdesk PostgreSQL fixture failed")
	}
	schema := fmt.Sprintf("godj_selected_helpdesk_%d_%d", os.Getpid(), time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := connection.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		_ = connection.Close(ctx)
		t.Fatal("create selected Helpdesk PostgreSQL fixture failed")
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		_, drop := connection.Exec(cleanup, "DROP SCHEMA "+quoted+" CASCADE")
		if errors.Join(drop, connection.Close(cleanup)) != nil {
			t.Error("clean selected Helpdesk PostgreSQL fixture failed")
		}
	})
	open := func(ctx context.Context) (selectedHelpdeskBackend, error) {
		return postgres.Open(ctx, postgres.Config{URL: url, Schema: schema})
	}
	backend, err := open(ctx)
	if err != nil {
		t.Fatal("open selected Helpdesk PostgreSQL backend failed")
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error("close selected Helpdesk PostgreSQL backend failed")
		}
	})
	runSelectedHelpdeskScope(t, newSelectedHelpdeskFixture(t, backend, open))
}

func newSelectedHelpdeskFixture(t *testing.T, backend selectedHelpdeskBackend, open func(context.Context) (selectedHelpdeskBackend, error)) *selectedHelpdeskFixture {
	t.Helper()
	ctx := t.Context()
	loaded, _, err := definition.Load(append(migrationdefs.MigrationSources(), helpdesk.MigrationSources()...)...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10000})
	if err != nil {
		t.Fatal(err)
	}
	firstConfig, err := accounts.FirstAccountConfig()
	if err != nil {
		t.Fatal(err)
	}
	first, err := project.NewFirstAccount(backend, hasher, firstConfig)
	if err != nil {
		t.Fatal(err)
	}
	input, err := first.Manager().Input(map[string]query.Value{"username": query.String("scope-bootstrap"), "email": query.String("")})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := first.Prepare(ctx, input, selectedHelpdeskPassword)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := first.Commit(ctx, prepared)
	if err != nil {
		t.Fatal(err)
	}
	config, err := accounts.Policy()
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := project.NewAccountRuntime(ctx, backend, hasher, auth.PrincipalAuthorizer{}, config)
	if err != nil {
		t.Fatal(err)
	}
	startup, err := runtime.InspectStartup(ctx)
	if err != nil || !startup.Ready() || startup.Ownership().OperationID() != initial.Ownership.OperationID() || startup.SessionRecords() != 0 || startup.AuditEvents() != 1 {
		t.Fatal("selected account startup", err)
	}
	bootstrap, err := runtime.Authenticator().Authenticate(ctx, "scope-bootstrap", selectedHelpdeskPassword)
	if err != nil {
		t.Fatal(err)
	}
	f := &selectedHelpdeskFixture{backend: backend, open: open, runtime: runtime, ownership: initial.Ownership, permissions: make(map[auth.Permission]int64)}
	grants := append([]auth.Permission{"godj.admin.access"}, helpdesk.Permissions()...)
	var keys []int64
	catalog := runtime.Catalog().Permissions()
	for _, permission := range grants {
		value, err := catalog.Manager().Input(map[string]query.Value{"code": query.String(string(permission)), "name": query.String(string(permission))})
		if err != nil {
			t.Fatal(err)
		}
		created, err := catalog.Create(ctx, bootstrap.Principal(), value)
		if err != nil {
			t.Fatal(err)
		}
		f.permissions[permission] = created.ID
		keys = append(keys, created.ID)
	}
	creator := runtime.Accounts().Creator()
	input, err = creator.Manager().Input(map[string]query.Value{"username": query.String("scope-operator"), "email": query.String(""), "active": query.Boolean(true), "staff": query.Boolean(true), "superuser": query.Boolean(false)})
	if err != nil {
		t.Fatal(err)
	}
	input, err = input.WithCollections(map[string][]int64{"permissions": keys})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := creator.Prepare(ctx, bootstrap.Principal(), input, selectedHelpdeskPassword)
	if err != nil {
		t.Fatal(err)
	}
	created, err := creator.Commit(ctx, bootstrap.Principal(), pending)
	if err != nil {
		t.Fatal(err)
	}
	f.account, err = identitymodels.UserObjects.Using(backend).Filter(identitymodels.UserFields.ID.Exact(created.ID)).Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f.credential, err = runtime.Authenticator().Authenticate(ctx, "scope-operator", selectedHelpdeskPassword)
	if err != nil {
		t.Fatal(err)
	}
	actor := f.credential.Principal()
	actual := actor.Permissions()
	slices.Sort(actual)
	slices.Sort(grants)
	if _, bound := actor.CredentialDomain(); !bound || !actor.Staff() || actor.Superuser() || !slices.Equal(actual, grants) {
		t.Fatal("ordinary selected actor lost domain or explicit grants")
	}
	f.category, err = models.CategoryObjects.Create(ctx, backend, models.NewCategoryCreate("Selected scope"))
	if err != nil {
		t.Fatal(err)
	}
	f.outside, err = models.CategoryObjects.Create(ctx, backend, models.NewCategoryCreate("private outside category"))
	if err != nil {
		t.Fatal(err)
	}
	f.ticket, err = models.TicketObjects.Create(ctx, backend, models.NewTicketCreate("selected visible ticket", f.category.ID))
	if err != nil {
		t.Fatal(err)
	}
	f.private, err = models.TicketObjects.Create(ctx, backend, models.NewTicketCreate("private outside ticket", f.outside.ID))
	if err != nil {
		t.Fatal(err)
	}
	f.label, err = models.LabelObjects.Create(ctx, backend, models.NewLabelCreate("selected visible label", f.category.ID))
	if err != nil {
		t.Fatal(err)
	}
	f.link, err = models.TicketLabelObjects.Create(ctx, backend, models.NewTicketLabelCreate(f.ticket.ID, f.label.ID))
	if err != nil {
		t.Fatal(err)
	}
	f.report, err = models.ServiceReportObjects.Create(ctx, backend, models.NewServiceReportCreate(f.ticket.ID, "selected visible report"))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// The transport intentionally keeps an earlier *real* credential. Native
// authorization must reject revoked grants even when admission has approved it.
// Production session/login persistence is exercised by the browser and SDK.
type selectedHelpdeskEarlierAuthenticator struct {
	auth.LoginInputProvider
	credential auth.Credential
}

func (a selectedHelpdeskEarlierAuthenticator) Resolve(_ context.Context, id string) (auth.Credential, error) {
	if id != a.credential.Principal().ID() {
		return auth.Credential{}, auth.ErrInvalidCredentials
	}
	return a.credential, nil
}
func (selectedHelpdeskEarlierAuthenticator) Authenticate(context.Context, string, string) (auth.Credential, error) {
	return auth.Credential{}, auth.ErrInvalidCredentials
}

type selectedHelpdeskAdmission struct{ actor auth.Principal }

func (selectedHelpdeskAdmission) DescribeAuthentication() (api.AuthenticationDescription, error) {
	return api.AuthenticationDescription{Kind: api.AuthenticationBearer}, nil
}
func (a selectedHelpdeskAdmission) Require(_ auth.Permission, next api.AuthenticatedHandler, _ ...auth.Permission) (web.Handler, error) {
	return func(r *web.Request) (web.Response, error) { return next(r, a.actor) }, nil
}

func (f *selectedHelpdeskFixture) client(t *testing.T, backend helpdesk.SelectedBackend, overlay auth.Authorizer) *helpdeskClient {
	t.Helper()
	application, err := helpdesk.NewSelected(backend, f.runtime.Credentials(), overlay, f.category.ID)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := application.AdminRegistry(helpdesk.AdminConfig{})
	if err != nil {
		t.Fatal(err)
	}
	configured, err := settings.New(settings.Definition{ProjectName: "selected-helpdesk", InstalledApps: helpdesk.InstalledApps()})
	if err != nil {
		t.Fatal(err)
	}
	store, err := sessions.NewMemoryStore(4)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := sessions.NewManager(store, sessions.Config{})
	if err != nil {
		t.Fatal(err)
	}
	record, err := manager.Create(t.Context(), map[string]string{auth.SessionPrincipalIDKey: f.credential.Principal().ID(), auth.SessionCredentialStampKey: f.credential.SessionStamp()})
	if err != nil {
		t.Fatal(err)
	}
	pages := []string{helpdesk.TicketEditorPath, helpdesk.TicketSummaryPath, helpdesk.ServiceReportStatusPath, helpdesk.LabelUsagePath, helpdesk.CostPreviewPath, helpdesk.DeadlinePreviewPath, helpdesk.DeadlineCalendarPath, helpdesk.ServicePreviewPath}
	allowed, err := admin.SiteAllowedNextPaths(registry, "/admin")
	if err != nil {
		t.Fatal(err)
	}
	webAuth, err := websessionauth.New(websessionauth.Config{Sessions: manager, Authenticator: selectedHelpdeskEarlierAuthenticator{LoginInputProvider: f.runtime.Authenticator(), credential: f.credential}, Authorizer: auth.PrincipalAuthorizer{}, SessionCookie: websessionauth.CookieConfig{Path: "/", AllowInsecure: true}, CSRFCookie: websessionauth.CookieConfig{Path: "/", AllowInsecure: true}, LoginPath: "/admin/login/", FallbackPath: "/admin/", AllowedNextPaths: append(allowed, pages...)})
	if err != nil {
		t.Fatal(err)
	}
	site, err := admin.NewSite(admin.SiteConfig{Apps: configured.Apps(), Namespace: "helpdesk", Registry: registry, Auth: webAuth, AdditionalNextPaths: pages, RenderLimits: helpdesk.AdminRenderLimits()})
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := application.API(helpdesk.APIConfig{Authentication: selectedHelpdeskAdmission{f.credential.Principal()}})
	if err != nil {
		t.Fatal(err)
	}
	routes := append(site.Routes(), adapter.Routes()...)
	editor, err := application.TicketEditor(helpdesk.TicketEditorConfig{Auth: webAuth})
	if err != nil {
		t.Fatal(err)
	}
	routes = append(routes, editor.Routes()...)
	summary, err := application.TicketSummary(webAuth)
	if err != nil {
		t.Fatal(err)
	}
	routes = append(routes, summary.Routes()...)
	status, err := application.ServiceReportStatus(webAuth)
	if err != nil {
		t.Fatal(err)
	}
	routes = append(routes, status.Routes()...)
	usage, err := application.LabelUsage(webAuth)
	if err != nil {
		t.Fatal(err)
	}
	routes = append(routes, usage.Routes()...)
	cost, err := application.CostPreview(webAuth)
	if err != nil {
		t.Fatal(err)
	}
	routes = append(routes, cost.Routes()...)
	deadline, err := application.DeadlinePreview(webAuth)
	if err != nil {
		t.Fatal(err)
	}
	routes = append(routes, deadline.Routes()...)
	calendar, err := application.DeadlineCalendar(webAuth)
	if err != nil {
		t.Fatal(err)
	}
	routes = append(routes, calendar.Routes()...)
	service, err := application.ServicePreview(webAuth)
	if err != nil {
		t.Fatal(err)
	}
	routes = append(routes, service.Routes()...)
	routes = append(routes, web.Route{Name: "helpdesk:test-csrf", Method: "GET", Path: "/_test/csrf/", Handler: func(request *web.Request) (web.Response, error) {
		token, err := webAuth.CSRFToken(request)
		if err != nil {
			return web.Response{}, err
		}
		response, err := web.NewResponse(http.StatusOK, nil, []byte(token.Value()))
		if err != nil {
			return web.Response{}, err
		}
		return token.Apply(response)
	}})
	webApp, err := web.NewApplication(web.Config{Settings: configured, Routes: routes, MaxResponseBytes: helpdesk.TicketEditorMaxResponseBytes})
	if err != nil {
		t.Fatal(err)
	}
	client := &helpdeskClient{t: t, application: webApp, api: adapter, cookies: map[string]*http.Cookie{websessionauth.DefaultSessionCookieName: {Name: websessionauth.DefaultSessionCookieName, Value: record.ID().Encoded()}}}
	response := client.requestContext(t.Context(), "GET", "/_test/csrf/", "", false)
	if response.Code != http.StatusOK {
		t.Fatal("selected test CSRF setup", response.Code)
	}
	client.csrf = response.Body.String()
	return client
}

// These writes model an independent account authority changing after request
// admission. They do not call the stale transport or the business service.
func (f *selectedHelpdeskFixture) revoke(t *testing.T, permissions ...auth.Permission) func() {
	t.Helper()
	for _, permission := range permissions {
		link, err := identitymodels.UserPermissionsLinkObjects.Using(f.backend).Filter(identitymodels.UserPermissionsLinkFields.SourceID.Exact(f.account.ID), identitymodels.UserPermissionsLinkFields.TargetID.Exact(f.permissions[permission])).Get(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if deleted, err := identitymodels.UserPermissionsLinkObjects.Delete(t.Context(), f.backend, &link); err != nil || deleted != 1 {
			t.Fatal("revoke selected grant", err)
		}
	}
	return func() {
		for _, permission := range permissions {
			if _, err := identitymodels.UserPermissionsLinkObjects.Create(t.Context(), f.backend, identitymodels.NewUserPermissionsLinkCreate(f.account.ID, f.permissions[permission])); err != nil {
				t.Error("restore selected grant", err)
			}
		}
	}
}

type selectedHelpdeskState struct {
	tickets []models.Ticket
	labels  []models.Label
	reports []models.ServiceReport
	links   []models.TicketLabel
	audits  []auditmodels.Event
}

func (f *selectedHelpdeskFixture) state(t *testing.T) selectedHelpdeskState {
	t.Helper()
	value, err := db.ReadSnapshotResult(t.Context(), f.backend, func(ctx context.Context, reader db.Queryer) (selectedHelpdeskState, error) {
		var result selectedHelpdeskState
		var err error
		if result.tickets, err = models.TicketObjects.Using(reader).OrderBy(models.TicketFields.ID.Asc()).All(ctx); err != nil {
			return result, err
		}
		if result.labels, err = models.LabelObjects.Using(reader).OrderBy(models.LabelFields.ID.Asc()).All(ctx); err != nil {
			return result, err
		}
		if result.reports, err = models.ServiceReportObjects.Using(reader).OrderBy(models.ServiceReportFields.ID.Asc()).All(ctx); err != nil {
			return result, err
		}
		if result.links, err = models.TicketLabelObjects.Using(reader).OrderBy(models.TicketLabelFields.ID.Asc()).All(ctx); err != nil {
			return result, err
		}
		result.audits, err = auditmodels.EventObjects.Using(reader).OrderBy(auditmodels.EventFields.ID.Asc()).All(ctx)
		return result, err
	})
	if err != nil {
		t.Fatal("read complete independent business state", err)
	}
	return value
}
func (f *selectedHelpdeskFixture) unchanged(t *testing.T, before selectedHelpdeskState) {
	t.Helper()
	if !reflect.DeepEqual(before, f.state(t)) {
		t.Fatal("rejected request changed business rows, relations or audit")
	}
}
func (f *selectedHelpdeskFixture) history(t *testing.T, actor auth.Principal, model string, id int64) []accountaudit.Entry {
	t.Helper()
	namespace, bound := actor.CredentialDomain()
	if !bound {
		t.Fatal("audit actor lost domain")
	}
	entries, err := db.ReadSnapshotResult(t.Context(), f.backend, func(ctx context.Context, reader db.Queryer) ([]accountaudit.Entry, error) {
		return accountaudit.HistoryIn(ctx, reader, namespace, model, id, 32)
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Domain() != namespace || entry.ActorID() != actor.ID() || entry.Model() != model || entry.ObjectID() != id {
			t.Fatal("audit crossed selected actor, domain or target")
		}
	}
	return entries
}
