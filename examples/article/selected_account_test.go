package article_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	auditmodels "github.com/progresshans/godj/account/audit/models"
	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/adminapp"
	"github.com/progresshans/godj/examples/article/apiapp"
	"github.com/progresshans/godj/examples/article/articleapp"
	"github.com/progresshans/godj/examples/article/internal/accounts"
	"github.com/progresshans/godj/examples/article/internal/accounttest"
	"github.com/progresshans/godj/examples/article/modeldef"
	articlemodels "github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/examples/article/project"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/settings/database"
	"github.com/progresshans/godj/web"
)

func TestSelectedAccountArticleSQLiteScopeLifecycle(t *testing.T) {
	backend, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "selected.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	runSelectedArticleScope(t, backend)
}

func TestSelectedAccountArticlePostgresScopeLifecycle(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL"))
	if url == "" {
		if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
			t.Fatal("GODJ_REQUIRE_POSTGRES=1 requires GODJ_TEST_POSTGRES_URL")
		}
		t.Skip("PostgreSQL environment is unavailable")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	connection, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(articlePostgresRedactedConnectionError(err))
	}
	schema := fmt.Sprintf("godj_selected_article_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := connection.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		_ = connection.Close(ctx)
		t.Fatal(articlePostgresRedactedConnectionError(err))
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		_, err := connection.Exec(cleanup, "DROP SCHEMA "+quoted+" CASCADE")
		if err := errors.Join(err, connection.Close(cleanup)); err != nil {
			t.Error(articlePostgresRedactedConnectionError(err))
		}
	})
	backend, err := postgres.Open(ctx, postgres.Config{URL: url, Schema: schema})
	if err != nil {
		t.Fatal(articlePostgresRedactedConnectionError(err))
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(articlePostgresRedactedConnectionError(err))
		}
	})
	runSelectedArticleScope(t, backend)
}

func runSelectedArticleScope(t *testing.T, backend database.Backend) {
	t.Helper()
	ctx := t.Context()
	sources := append(modeldef.MigrationSources(), articleCurrentDefinitionSources()...)
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	bootstrap, err := accounttest.Provision(ctx, backend, "scope-admin", "Compass-river-quartz-73!")
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := accounts.New(ctx, backend)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := runtime.Authenticator().Authenticate(ctx, "scope-admin", "Compass-river-quartz-73!")
	if err != nil {
		t.Fatal(err)
	}
	actor := credential.Principal()
	service, err := adminapp.NewSelectedService(backend, runtime.Credentials(), auth.PrincipalAuthorizer{})
	if err != nil {
		t.Fatal(err)
	}
	value, err := service.Create(ctx, actor, fullArticleInput(t, articlemodels.Article{Title: "Scoped Article"}))
	if err != nil || value.ID <= 0 {
		t.Fatal("initial selected Article", err)
	}
	history, err := service.HistoryLimited(ctx, actor, value.ID, 10)
	if err != nil || len(history) != 1 || history[0].ActorID != actor.ID() || history[0].DisplayLabel != "" {
		t.Fatal("account-domain Article audit", err)
	}
	baseline := selectedArticleCounts(t, backend)
	assertDenied := func(err error) {
		t.Helper()
		failure, ok := err.(*admin.OperationError)
		if !ok || failure.Code != admin.OperationDenied {
			t.Fatal("scope did not return confirmed denial", err)
		}
	}

	t.Run("current_authority_and_private_read_cleanup", func(t *testing.T) {
		row, err := models.UserObjects.Using(backend).Filter(models.UserFields.ID.Exact(bootstrap.Account.ID)).Get(ctx)
		if err != nil {
			t.Fatal(err)
		}
		inactive, err := models.UserObjects.Patch(ctx, backend, row, (models.UserPatch{}).WithActive(false).WithRevision(row.Revision+1))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := models.UserObjects.Patch(ctx, backend, inactive, (models.UserPatch{}).WithActive(true).WithRevision(inactive.Revision+1)); err != nil {
				t.Error("restore current actor after denial checks", err)
			}
		})
		page, err := service.List(ctx, actor, articleapp.ListOptions{})
		assertDenied(err)
		if page.Total != 0 || len(page.Articles) != 0 {
			t.Fatal("denial published a page")
		}
		_, _, err = service.Get(ctx, actor, value.ID)
		assertDenied(err)
		_, err = service.HistoryLimited(ctx, actor, value.ID, 10)
		assertDenied(err)
		_, err = service.Create(ctx, actor, fullArticleInput(t, articlemodels.Article{Title: "denied"}))
		assertDenied(err)
		_, _, err = service.Update(ctx, actor, value.ID, fullArticleInput(t, articlemodels.Article{Title: "denied"}))
		assertDenied(err)
		_, err = service.Delete(ctx, actor, value.ID)
		assertDenied(err)
		_, err = service.Publish(ctx, actor, []int64{value.ID})
		assertDenied(err)
		application := selectedArticleAPI(t, backend, runtime, actor)
		for _, request := range []struct{ method, path, body string }{
			{"GET", "/api/articles/", ""}, {"GET", fmt.Sprintf("/api/articles/%d/", value.ID), ""},
			{"POST", "/api/articles/", `{"title":"denied"}`},
			{"POST", apiapp.BulkCreatePath, `[{"title":"denied"}]`},
			{"PUT", fmt.Sprintf("/api/articles/%d/", value.ID), `{"title":"denied"}`},
			{"PATCH", fmt.Sprintf("/api/articles/%d/", value.ID), `{"title":"denied"}`},
			{"DELETE", fmt.Sprintf("/api/articles/%d/", value.ID), ""},
		} {
			if got := selectedArticleRequest(application, request.method, request.path, request.body); got.Code != 403 {
				t.Fatal("stale transport approval bypassed account scope", request.method, got.Code)
			}
		}
		if got := selectedArticleCounts(t, backend); got != baseline {
			t.Fatal("denied operations wrote durable state", got, baseline)
		}
		fault := errors.New("private snapshot cleanup failure")
		broken := &selectedArticleFaultBackend{Backend: backend, cleanup: fault}
		denied, err := adminapp.NewSelectedService(broken, runtime.Credentials(), auth.PrincipalAuthorizer{})
		if err != nil {
			t.Fatal(err)
		}
		if page, err := denied.List(ctx, actor, articleapp.ListOptions{}); !errors.Is(err, fault) || page.Total != 0 {
			t.Fatal("cleanup failure became a confirmed read denial", err)
		}
		if got := selectedArticleRequest(selectedArticleAPI(t, broken, runtime, actor), "GET", "/api/articles/", ""); got.Code != 500 {
			t.Fatal("API cleanup failure became permission denial", got.Code)
		}
	})

	t.Run("audit_rollback_and_unknown_commit", func(t *testing.T) {
		fault := errors.New("private audit failure")
		broken := &selectedArticleFaultBackend{Backend: backend, audit: fault}
		service, err := adminapp.NewSelectedService(broken, runtime.Credentials(), auth.PrincipalAuthorizer{})
		if err != nil {
			t.Fatal(err)
		}
		if result, err := service.Create(ctx, actor, fullArticleInput(t, articlemodels.Article{Title: "must roll back"})); !errors.Is(err, fault) || result.ID != 0 || broken.writes != 1 {
			t.Fatal("audit failure published or retried", err)
		}
		if selectedArticleCounts(t, backend) != baseline {
			t.Fatal("audit failure left partial state")
		}
		unknown := &selectedArticleFaultBackend{Backend: backend, unknown: true}
		service, err = adminapp.NewSelectedService(unknown, runtime.Credentials(), auth.PrincipalAuthorizer{})
		if err != nil {
			t.Fatal(err)
		}
		if result, err := service.Create(ctx, actor, fullArticleInput(t, articlemodels.Article{Title: "committed but unknown"})); !errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) || result.ID != 0 || unknown.writes != 1 {
			t.Fatal("uncertain commit published or retried", err)
		}
		want := baseline
		want[0]++
		want[1]++
		if selectedArticleCounts(t, backend) != want {
			t.Fatal("unknown injection did not follow actual atomic commit")
		}
		baseline = want
	})

	t.Run("old_domain_does_not_inherit_article_authority_or_history", func(t *testing.T) {
		change, err := domain.Prepare(ctx, bootstrap.Ownership.RoleFingerprint(), strings.Repeat("e", 64))
		if err != nil {
			t.Fatal(err)
		}
		if err := backend.CoordinatedAtomic(ctx, func(scope db.Session) error {
			_, err := domain.TransitionIn(ctx, scope, bootstrap.Ownership, change, time.Now())
			return err
		}); err != nil {
			t.Fatal(err)
		}
		_, err = service.List(ctx, actor, articleapp.ListOptions{})
		assertDenied(err)
		_, err = service.Create(ctx, actor, fullArticleInput(t, articlemodels.Article{Title: "old domain"}))
		assertDenied(err)
		if got := selectedArticleRequest(selectedArticleAPI(t, backend, runtime, actor), "POST", "/api/articles/", `{"title":"old domain"}`); got.Code != 403 {
			t.Fatal("API inherited old-domain actor", got.Code)
		}
		credential, err := runtime.Authenticator().Authenticate(ctx, "scope-admin", "Compass-river-quartz-73!")
		if err != nil {
			t.Fatal(err)
		}
		current := credential.Principal()
		if history, err := service.HistoryLimited(ctx, current, value.ID, 10); err != nil || len(history) != 0 {
			t.Fatal("new domain inherited historical audit", err)
		}
		if selectedArticleCounts(t, backend) != baseline {
			t.Fatal("domain denial changed Article or audit rows")
		}
	})
}

func selectedArticleCounts(t *testing.T, backend db.Queryer) [2]int64 {
	t.Helper()
	rows, err := articlemodels.ArticleObjects.Using(backend).Count(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	audits, err := auditmodels.EventObjects.Using(backend).Count(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return [2]int64{rows, audits}
}

// The transport deliberately supplies an earlier authenticated actor. These
// tests isolate the native owner from a fresh middleware permission check.
type selectedEarlierAuthentication struct{ actor auth.Principal }

func (selectedEarlierAuthentication) DescribeAuthentication() (api.AuthenticationDescription, error) {
	return api.AuthenticationDescription{Kind: api.AuthenticationBearer}, nil
}
func (a selectedEarlierAuthentication) Require(_ auth.Permission, next api.AuthenticatedHandler, _ ...auth.Permission) (web.Handler, error) {
	return func(request *web.Request) (web.Response, error) { return next(request, a.actor) }, nil
}
func selectedArticleAPI(t *testing.T, backend apiapp.SelectedBackend, runtime *accounts.Runtime, actor auth.Principal) *web.Application {
	t.Helper()
	adapter, err := apiapp.NewSelected(backend, runtime.Credentials(), auth.PrincipalAuthorizer{}, selectedEarlierAuthentication{actor})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := settings.New(settings.Definition{ProjectName: "selected-article", InstalledApps: project.InstalledApps()})
	if err != nil {
		t.Fatal(err)
	}
	application, err := web.NewApplication(web.Config{Settings: settings, Routes: adapter.Routes(), Middleware: adapter.Middleware()})
	if err != nil {
		t.Fatal(err)
	}
	return application
}
func selectedArticleRequest(application http.Handler, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Accept", "application/json")
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	result := httptest.NewRecorder()
	application.ServeHTTP(result, request)
	return result
}

type selectedArticleFaultBackend struct {
	database.Backend
	audit, cleanup error
	unknown        bool
	writes         int
}

func (b *selectedArticleFaultBackend) ReadSnapshot(ctx context.Context, run func(db.Queryer) error) error {
	err := b.Backend.ReadSnapshot(ctx, run)
	if b.cleanup == nil {
		return err
	}
	return errors.Join(err, b.cleanup)
}
func (b *selectedArticleFaultBackend) CoordinatedAtomic(ctx context.Context, run func(db.Session) error) error {
	b.writes++
	err := b.Backend.CoordinatedAtomic(ctx, func(scope db.Session) error {
		if b.audit != nil {
			return run(selectedArticleAuditFailure{Session: scope, failure: b.audit})
		}
		return run(scope)
	})
	if err == nil && b.unknown {
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown}
	}
	return err
}

type selectedArticleAuditFailure struct {
	db.Session
	failure error
}

func (s selectedArticleAuditFailure) ValidateSession(ctx context.Context) error {
	return s.Session.(db.SessionValidator).ValidateSession(ctx)
}
func (s selectedArticleAuditFailure) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	if plan.Table() == "godj_account_audit_event" {
		return 0, s.failure
	}
	return s.Session.Insert(ctx, plan)
}
