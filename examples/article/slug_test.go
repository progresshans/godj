package article_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/apiapp"
	"github.com/progresshans/godj/examples/article/articleapp"
	articlemodels "github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/examples/article/webapp"
	"github.com/progresshans/godj/migrations"
	mb "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

type articleSlugBackend interface {
	articleapp.Backend
	mb.RevisionFencedBackend
	Close() error
}

func TestArticleSlugSQLiteUserFlow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "slug.sqlite")
	var database *sqlite.Backend
	runArticleSlugUserFlow(t, func(ctx context.Context) (articleSlugBackend, error) {
		var err error
		database, err = sqlite.Open(ctx, path)
		return database, err
	}, func(ctx context.Context, statement string) error {
		_, err := database.ExecContext(ctx, strings.ReplaceAll(statement, "ARTICLE_TABLE", `"godj_conformance_article"`))
		return err
	})
}

func TestArticleSlugPostgresUserFlow(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL"))
	if databaseURL == "" {
		if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
			t.Fatal("required PostgreSQL connection is absent")
		}
		t.Skip("GODJ_TEST_POSTGRES_URL is not configured")
	}
	connection, err := pgx.Connect(t.Context(), databaseURL)
	if err != nil {
		t.Fatal("connect Article slug PostgreSQL fixture")
	}
	namespace := fmt.Sprintf("godj_article_slug_%d_%d", os.Getpid(), time.Now().UnixNano())
	quoted := pgx.Identifier{namespace}.Sanitize()
	if _, err := connection.Exec(t.Context(), "CREATE SCHEMA "+quoted); err != nil {
		_ = connection.Close(context.Background())
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if _, err := connection.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
		if err := connection.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	runArticleSlugUserFlow(t, func(ctx context.Context) (articleSlugBackend, error) {
		return postgres.Open(ctx, postgres.Config{URL: databaseURL, Schema: namespace})
	}, func(ctx context.Context, statement string) error {
		table := pgx.Identifier{namespace, "godj_conformance_article"}.Sanitize()
		_, err := connection.Exec(ctx, strings.ReplaceAll(statement, "ARTICLE_TABLE", table))
		return err
	})
}

func runArticleSlugUserFlow(t *testing.T, open func(context.Context) (articleSlugBackend, error), execute func(context.Context, string) error) {
	t.Helper()
	ctx := t.Context()
	backend, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if backend != nil {
			if err := backend.Close(); err != nil {
				t.Error(err)
			}
		}
	})
	loaded, _, err := definition.Load(articleCurrentDefinitionSources()...)
	if err != nil {
		t.Fatal(err)
	}
	previous := migrations.MigrationKey{App: "godj_conformance", Name: "0002_alter_article_summary"}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(previous))); err != nil {
		t.Fatal(err)
	}
	if err := execute(ctx, `INSERT INTO ARTICLE_TABLE (title,published,summary) VALUES ('Legacy article',TRUE,'Preserved before slug migration')`); err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	fixture := newArticleAPIAdminSessionFixture(t, backend)
	legacyPage, err := fixture.repository.List(ctx, articleapp.ListOptions{})
	if err != nil || len(legacyPage.Articles) != 1 || legacyPage.Articles[0].Slug != nil || legacyPage.Articles[0].Summary == nil || *legacyPage.Articles[0].Summary != "Preserved before slug migration" {
		t.Fatal("slug addition did not preserve the previous Article", err)
	}
	legacy := legacyPage.Articles[0]
	public, err := webapp.NewApplication(backend)
	if err != nil {
		t.Fatal(err)
	}
	publicServer := httptest.NewServer(public)
	t.Cleanup(publicServer.Close)
	publicRequest := func(method, path string) articleAPIHTTPResult {
		t.Helper()
		request, err := http.NewRequestWithContext(ctx, method, publicServer.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := publicServer.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		if err := errors.Join(readErr, response.Body.Close()); err != nil {
			t.Fatal(err)
		}
		return articleAPIHTTPResult{status: response.StatusCode, header: response.Header, body: string(body)}
	}
	decode := func(response articleAPIHTTPResult, status int) articleapp.Article {
		t.Helper()
		if response.status != status {
			t.Fatalf("slug HTTP status %d, want %d: %s", response.status, status, response.body)
		}
		var value articleapp.Article
		if err := json.Unmarshal([]byte(response.body), &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	assertStored := func(expected articleapp.Article) {
		t.Helper()
		actual, found, err := fixture.repository.Get(ctx, expected.ID)
		if err != nil || !found || !reflect.DeepEqual(actual, expected) {
			t.Fatal("rejected request changed Article data", actual, expected, err)
		}
	}
	login := fixture.login(t, fixture.client, articleAdminUsername, articleAdminPassword)
	response := fixture.request(t, fixture.client, http.MethodGet, apiapp.ListPath, "", "", "")
	csrf := response.header.Get(websessionauth.DefaultCSRFHeader)
	if len(csrf) != 128 || csrf == login.preLoginCSRF {
		t.Fatal("slug flow lacks rotated session CSRF")
	}
	post := func(body string) articleAPIHTTPResult {
		return fixture.request(t, fixture.client, http.MethodPost, apiapp.ListPath, api.JSONContentType, body, csrf)
	}
	created := decode(post(`{"title":"<script>Readable article</script>","published":true,"summary":"<img src=x onerror=bad>","slug":"  읽기-쉬운_주소  "}`), http.StatusCreated)
	if created.Slug == nil || *created.Slug != "읽기-쉬운_주소" {
		t.Fatal("slug input lost trimming or Unicode")
	}
	assertStored(created)
	detail := fmt.Sprintf("/api/articles/%d/", created.ID)
	patch := func(body string) articleAPIHTTPResult {
		return fixture.request(t, fixture.client, http.MethodPatch, detail, api.JSONContentType, body, csrf)
	}
	t.Run("input_and_duplicate_rejection", func(t *testing.T) {
		count := articleAPIArticleCount(t, fixture.repository)
		for _, value := range []string{"invalid slug", "a/b", "bad?query", "e\u0301", strings.Repeat("한", 51)} {
			body, _ := json.Marshal(map[string]string{"slug": value})
			response := patch(string(body))
			if response.status != http.StatusBadRequest || !strings.Contains(response.body, `"field":"slug"`) {
				t.Fatal("invalid slug accepted", response.status, response.body)
			}
			assertStored(created)
		}
		duplicate := post(`{"title":"Duplicate","slug":"읽기-쉬운_주소"}`)
		duplicate.requireJSON(t, http.StatusBadRequest, `{"code":"validation_error","errors":[{"field":"slug","code":"unique","params":[]}]}`)
		if articleAPIArticleCount(t, fixture.repository) != count {
			t.Fatal("duplicate slug created a row")
		}
		response := fixture.request(t, fixture.client, http.MethodPatch, fmt.Sprintf("/api/articles/%d/", legacy.ID), api.JSONContentType, `{"title":"Lost","slug":"읽기-쉬운_주소"}`, csrf)
		response.requireJSON(t, http.StatusBadRequest, `{"code":"validation_error","errors":[{"field":"slug","code":"unique","params":[]}]}`)
		assertStored(legacy)
	})
	t.Run("presence_and_exact_case", func(t *testing.T) {
		created = decode(fixture.request(t, fixture.client, http.MethodPut, detail, api.JSONContentType, `{"title":"<script>Readable article</script>","published":true}`, csrf), http.StatusOK)
		if created.Slug == nil || *created.Slug != "읽기-쉬운_주소" {
			t.Fatal("PUT omission erased slug")
		}
		created = decode(patch(`{"summary":"<img src=x onerror=bad>"}`), http.StatusOK)
		if created.Slug == nil || *created.Slug != "읽기-쉬운_주소" {
			t.Fatal("PATCH omission erased slug")
		}
		created = decode(patch(`{"slug":null}`), http.StatusOK)
		if created.Slug != nil {
			t.Fatal("explicit null did not clear slug")
		}
		created = decode(patch(`{"slug":"Mixed_읽기-주소"}`), http.StatusOK)
		if created.Slug == nil || *created.Slug != "Mixed_읽기-주소" {
			t.Fatal("slug was case folded or generated")
		}
		assertStored(created)
	})
	t.Run("authorization_and_csrf", func(t *testing.T) {
		denied := fixture.request(t, fixture.client, http.MethodPatch, detail, api.JSONContentType, `{"slug":"not-saved"}`, login.preLoginCSRF)
		denied.requireJSON(t, http.StatusForbidden, `{"code":"csrf_rejected","errors":[]}`)
		viewer := fixture.newClient(t)
		fixture.login(t, viewer, articleAPIViewerUsername, articleAPIViewerPassword)
		read := fixture.request(t, viewer, http.MethodGet, detail, "", "", "")
		if read.status != http.StatusOK {
			t.Fatal("viewer cannot read Article")
		}
		denied = fixture.request(t, viewer, http.MethodPatch, detail, api.JSONContentType, `{broken`, read.header.Get(websessionauth.DefaultCSRFHeader))
		if denied.status != http.StatusForbidden || strings.Contains(denied.body, "invalid_json") {
			t.Fatal("slug update parsed before permission", denied.status, denied.body)
		}
		assertStored(created)
	})
	t.Run("public_addresses", func(t *testing.T) {
		address, err := public.ReverseWith(webapp.ArticleSlugRoute, web.StringArgument("slug", *created.Slug))
		if err != nil || !strings.Contains(address, "%") || strings.Contains(address, "읽기") {
			t.Fatal("Unicode slug URL is not escaped", address, err)
		}
		page := publicRequest(http.MethodGet, address)
		if page.status != http.StatusOK || !strings.Contains(page.body, html.EscapeString(created.Title)) || strings.Contains(page.body, "<script>") || strings.Contains(page.body, "<img ") || !strings.Contains(page.body, `href="`+address+`"`) {
			t.Fatal("public slug rendering lost escaped data/canonical address", page.status, page.body)
		}
		head := publicRequest(http.MethodHead, address)
		if head.status != http.StatusOK || head.body != "" {
			t.Fatal("slug HEAD returned a body")
		}
		for _, path := range []string{strings.Replace(address, "Mixed_", "mixed_", 1), "/articles/by-slug/bad%252Fslug/", "/articles/by-slug/a%2Fb/", "/articles/by-slug/missing/"} {
			if response := publicRequest(http.MethodGet, path); response.status != http.StatusNotFound {
				t.Fatal("slug route folded or decoded unsafe input", path, response.status)
			}
		}
		draft := decode(post(`{"title":"Private draft","slug":"draft-article"}`), http.StatusCreated)
		for _, path := range []string{"/articles/by-slug/draft-article/", fmt.Sprintf("/articles/%d/", draft.ID)} {
			if response := publicRequest(http.MethodGet, path); response.status != http.StatusNotFound || strings.Contains(response.body, draft.Title) {
				t.Fatal("public detail exposed a draft")
			}
		}
		legacyURL := fmt.Sprintf("/articles/%d/", legacy.ID)
		if page := publicRequest(http.MethodGet, legacyURL); page.status != http.StatusOK || !strings.Contains(page.body, legacy.Title) {
			t.Fatal("pre-migration Article lost its fallback address")
		}
	})
	t.Run("admin_save_and_rejection", func(t *testing.T) {
		path := fmt.Sprintf("/admin/articles/change/?id=%d", created.ID)
		get := fixture.request(t, fixture.client, http.MethodGet, path, "", "", "")
		if get.status != http.StatusOK || !strings.Contains(get.body, `name="slug"`) {
			t.Fatal("Admin omitted the model slug")
		}
		form := url.Values{"csrfmiddlewaretoken": {get.adminCSRFToken(t)}, "title": {created.Title}, "published": {"on"}, "summary": {*created.Summary}, "slug": {"  Admin_주소  "}}
		response := fixture.request(t, fixture.client, http.MethodPost, path, "application/x-www-form-urlencoded", form.Encode(), "")
		if response.status != http.StatusFound {
			t.Fatal("Admin slug save failed", response.status, response.body)
		}
		created = articleAPIGet(t, fixture.repository, created.ID)
		if created.Slug == nil || *created.Slug != "Admin_주소" {
			t.Fatal("Admin lost the cleaned Unicode slug")
		}
		form.Set("slug", "draft-article")
		response = fixture.request(t, fixture.client, http.MethodPost, path, "application/x-www-form-urlencoded", form.Encode(), "")
		if response.status != http.StatusOK || !strings.Contains(response.body, `data-error-field="slug"`) || !strings.Contains(response.body, `data-error-code="unique"`) || !strings.Contains(response.body, `value="draft-article"`) {
			t.Fatal("Admin did not render duplicate slug input", response.status, response.body)
		}
		assertStored(created)
		form.Set("slug", "<script>bad</script>")
		response = fixture.request(t, fixture.client, http.MethodPost, path, "application/x-www-form-urlencoded", form.Encode(), "")
		if response.status != http.StatusOK || !strings.Contains(response.body, `data-error-code="invalid"`) || strings.Contains(response.body, "<script>bad") {
			t.Fatal("Admin invalid slug was accepted or rendered as markup")
		}
		assertStored(created)
	})
	t.Run("rollback_and_legacy_output", func(t *testing.T) {
		failure := errors.New("rollback after slug DML")
		repository := fixture.repository.WithMutationHook(func(context.Context, db.Session, articleapp.MutationResult) error { return failure })
		if _, _, err := repository.Patch(ctx, created.ID, (articleapp.Patch{}).WithSlug("Rolled_back")); !errors.Is(err, failure) {
			t.Fatal("slug update lost callback rollback", err)
		}
		assertStored(created)
		stored, found, err := articlemodels.ArticleObjects.Using(backend).Filter(articlemodels.ArticleFields.ID.Exact(created.ID)).OrderBy(articlemodels.ArticleFields.ID.Asc()).First(ctx)
		if err != nil || !found {
			t.Fatal(err)
		}
		if _, err := articlemodels.ArticleObjects.Update(ctx, backend, stored, (articlemodels.ArticlePatch{}).WithSlug("<script>/legacy?")); err != nil {
			t.Fatal("ordinary ORM revalidated slug grammar", err)
		}
		created = decode(fixture.request(t, fixture.client, http.MethodGet, detail, "", "", ""), http.StatusOK)
		if created.Slug == nil || *created.Slug != "<script>/legacy?" {
			t.Fatal("response rewrote existing slug")
		}
		page := publicRequest(http.MethodGet, fmt.Sprintf("/articles/%d/", created.ID))
		if page.status != http.StatusOK || strings.Contains(page.body, "by-slug/") || strings.Contains(page.body, "<script>/legacy?") {
			t.Fatal("invalid stored slug became an unsafe public address")
		}
		list := publicRequest(http.MethodGet, "/articles/")
		if list.status != http.StatusOK || !strings.Contains(list.body, fmt.Sprintf(`href="/articles/%d/"`, created.ID)) || strings.Contains(list.body, `href="/articles/by-slug/draft-article/"`) {
			t.Fatal("list failed safe address selection")
		}
	})
	t.Run("storage_failure_classification", func(t *testing.T) {
		for _, operation := range []string{"create", "patch"} {
			for _, mode := range []string{"constraint", "rollback_failure", "cancellation"} {
				t.Run(operation+"/"+mode, func(t *testing.T) {
					count := articleAPIArticleCount(t, fixture.repository)
					requestContext, cancel := context.WithCancel(ctx)
					defer cancel()
					rollbackFailure := errors.New("injected rollback boundary failure")
					wrapper := &articleSlugConflictBackend{Backend: backend, slug: "Collision_" + operation + "_" + mode}
					if mode == "rollback_failure" {
						wrapper.finish = rollbackFailure
					}
					if mode == "cancellation" {
						wrapper.cancel = cancel
					}
					repository, err := articleapp.NewRepository(wrapper)
					if err != nil {
						t.Fatal(err)
					}
					if operation == "create" {
						_, err = repository.Create(requestContext, articleapp.Input{Title: "Must roll back", Slug: &wrapper.slug})
					} else {
						_, _, err = repository.Patch(requestContext, created.ID, (articleapp.Patch{}).WithSlug(wrapper.slug))
					}
					failures, rejected := validation.Rejected(err)
					var conflict *query.Error
					if err == nil || !errors.As(err, &conflict) || conflict.Code != query.CodeUniqueConstraint || wrapper.injections != 1 {
						t.Fatal("physical slug conflict was not exercised", err, wrapper.injections)
					}
					if mode == "constraint" {
						if !rejected || failures.ByField(validation.NonField).Len() != 1 {
							t.Fatal("confirmed storage conflict lost safe diagnostics", err)
						}
					} else if rejected || mode == "rollback_failure" && !errors.Is(err, rollbackFailure) || mode == "cancellation" && !errors.Is(err, context.Canceled) {
						t.Fatal("uncertain or canceled storage error became an input rejection", err)
					}
					if articleAPIArticleCount(t, fixture.repository) != count {
						t.Fatal("failed write retained transaction-local conflicting insert")
					}
					assertStored(created)
				})
			}
		}
	})
	fixture.server.Close()
	publicServer.Close()
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	backend = nil
	backend, err = open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := articleapp.NewRepository(backend)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []articleapp.Article{legacy, created} {
		got, found, err := reopened.Get(ctx, want.ID)
		if err != nil || !found || !reflect.DeepEqual(got, want) {
			t.Fatal("fresh connection lost committed slug state", got, want, err)
		}
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal("reopened slug history lost no-op", err)
	}
}

// Inject a real conflicting row after the advisory read and before DML. Both
// writes use the borrowed transaction, so the post-failure row count also
// verifies the backend rollback. finish models an additional owner failure;
// it must prevent presentation of the otherwise direct input rejection.
type articleSlugConflictBackend struct {
	articleapp.Backend
	slug       string
	finish     error
	cancel     context.CancelFunc
	injections int
}

func (backend *articleSlugConflictBackend) Atomic(ctx context.Context, callback func(db.Session) error) error {
	err := backend.Backend.Atomic(ctx, func(session db.Session) error {
		err := callback(articleSlugConflictSession{Session: session, owner: backend})
		if backend.cancel != nil {
			backend.cancel()
		}
		return err
	})
	if backend.finish != nil {
		return errors.Join(err, backend.finish)
	}
	return err
}

type articleSlugConflictSession struct {
	db.Session
	owner *articleSlugConflictBackend
}

func (session articleSlugConflictSession) inject(ctx context.Context) error {
	session.owner.injections++
	_, err := articlemodels.ArticleObjects.Create(ctx, session.Session, articlemodels.NewArticleCreate("Transaction-local collision").WithSlug(session.owner.slug))
	return err
}

func (session articleSlugConflictSession) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	if err := session.inject(ctx); err != nil {
		return 0, err
	}
	return session.Session.Insert(ctx, plan)
}

func (session articleSlugConflictSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	if err := session.inject(ctx); err != nil {
		return 0, err
	}
	return session.Session.Update(ctx, plan)
}
