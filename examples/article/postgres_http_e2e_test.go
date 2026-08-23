package article_test

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/db/postgres"
	articlemodels "github.com/progresshans/godj/examples/article/models"
	articleproject "github.com/progresshans/godj/examples/article/project"
	"github.com/progresshans/godj/examples/article/webapp"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
)

//go:embed testdata/postgres/0001_initial.godj.json
var articlePostgresInitialDefinition []byte

func TestArticlePostgresMigrationGeneratedCRUDAndHTTP(t *testing.T) {
	databaseURL := os.Getenv("GODJ_TEST_POSTGRES_URL")
	if strings.TrimSpace(databaseURL) == "" {
		if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
			t.Fatal("GODJ_REQUIRE_POSTGRES=1 requires GODJ_TEST_POSTGRES_URL")
		}
		t.Skip("GODJ_TEST_POSTGRES_URL is not configured; Article PostgreSQL E2E was not run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect Article PostgreSQL E2E database: %v", articlePostgresRedactedConnectionError(err))
	}
	schema := fmt.Sprintf("godj_article_pg_%d", time.Now().UnixNano())
	quotedSchema := `"` + schema + `"`
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("create isolated Article PostgreSQL schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+quotedSchema+" CASCADE"); err != nil {
			t.Errorf("drop isolated Article PostgreSQL schema: %v", err)
		}
		if err := admin.Close(cleanupCtx); err != nil {
			t.Errorf("close Article PostgreSQL admin connection: %v", err)
		}
	})

	loaded, report, err := migrationdefinition.Load(migrationdefinition.Source{
		SourceID: "testdata/postgres/0001_initial.godj.json",
		Document: append([]byte(nil), articlePostgresInitialDefinition...),
	})
	if err != nil {
		t.Fatalf("load Article PostgreSQL migration definition: %v", err)
	}
	if report.DocumentsReceived != 1 || report.HeadersValidated != 1 || report.OperationsDecoded != 1 ||
		report.PlannerConstruction != 1 || report.DefinitionsPublished != 1 || report.DefinitionSetsPublished != 1 {
		t.Fatalf("Article PostgreSQL definition load report = %+v", report)
	}

	backend, err := postgres.Open(ctx, postgres.Config{URL: databaseURL, Schema: schema})
	if err != nil {
		t.Fatalf("open Article PostgreSQL migration backend: %v", err)
	}
	state, err := (migrations.Executor{Backend: backend}).Migrate(
		ctx,
		loaded,
		migrations.LatestLifecycleRequest(),
	)
	if err != nil {
		_ = backend.Close()
		t.Fatalf("migrate Article PostgreSQL fixture: %v", err)
	}
	physicalModel, exists := state.Model("godj_conformance", "article")
	if !exists || !reflect.DeepEqual(physicalModel, (articlemodels.ArticleDescriptor{}).Metadata()) {
		_ = backend.Close()
		t.Fatalf("migrated Article state = %+v, exists=%t", physicalModel, exists)
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("close Article PostgreSQL backend after migration: %v", err)
	}

	backend, err = postgres.Open(ctx, postgres.Config{URL: databaseURL, Schema: schema})
	if err != nil {
		t.Fatalf("reopen migrated Article PostgreSQL backend: %v", err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("close reopened Article PostgreSQL backend: %v", err)
		}
	})
	history, err := backend.ReadAppliedMigrations(ctx)
	if err != nil {
		t.Fatalf("read reopened Article PostgreSQL migration history: %v", err)
	}
	wantHistory := []migrationbackend.AppliedMigration{{App: "godj_conformance", Name: "0001_initial"}}
	if !reflect.DeepEqual(history, wantHistory) {
		t.Fatalf("reopened Article PostgreSQL history = %v, want %v", history, wantHistory)
	}

	nullArticle, err := articlemodels.ArticleObjects.Create(
		ctx,
		backend,
		articlemodels.NewArticleCreate("<script>alert(1)</script>").
			WithPublished(true).
			WithSummaryNull(),
	)
	if err != nil {
		t.Fatalf("create escaped/NULL Article through generated Manager: %v", err)
	}
	emptyArticle, err := articlemodels.ArticleObjects.Create(
		ctx,
		backend,
		articlemodels.NewArticleCreate("Before Save").WithSummary(""),
	)
	if err != nil {
		t.Fatalf("create empty-summary Article through generated Manager: %v", err)
	}
	if nullArticle.ID <= 0 || emptyArticle.ID <= nullArticle.ID || nullArticle.Summary != nil ||
		emptyArticle.Summary == nil || *emptyArticle.Summary != "" {
		t.Fatalf("created Article values = null:%+v empty:%+v", nullArticle, emptyArticle)
	}
	emptyArticle.Title = "Saved Empty Summary"
	emptyArticle.Published = true
	if err := articlemodels.ArticleObjects.Save(ctx, backend, &emptyArticle); err != nil {
		t.Fatalf("save updated Article through generated Manager: %v", err)
	}

	bound, err := articleproject.Using(backend)
	if err != nil {
		t.Fatalf("bind reopened PostgreSQL backend to generated Article project: %v", err)
	}
	ordered, err := bound.ModelsArticle.
		OrderBy(articlemodels.ArticleFields.ID.Asc()).
		All(ctx)
	if err != nil {
		t.Fatalf("read ordered Articles through generated project facade: %v", err)
	}
	if len(ordered) != 2 {
		t.Fatalf("ordered Article count = %d, want 2", len(ordered))
	}
	first, err := ordered[0].Unwrap()
	if err != nil {
		t.Fatalf("unwrap first ordered Article: %v", err)
	}
	second, err := ordered[1].Unwrap()
	if err != nil {
		t.Fatalf("unwrap second ordered Article: %v", err)
	}
	if first.ID != nullArticle.ID || second.ID != emptyArticle.ID || first.ID >= second.ID ||
		first.Title != "<script>alert(1)</script>" || first.Summary != nil ||
		second.Title != "Saved Empty Summary" || !second.Published || second.Summary == nil || *second.Summary != "" {
		t.Fatalf("ordered Article values = first:%+v second:%+v", first, second)
	}

	application, err := webapp.NewApplication(backend)
	if err != nil {
		t.Fatalf("build Article PostgreSQL web application: %v", err)
	}
	server := httptest.NewServer(application)
	t.Cleanup(server.Close)
	client := &http.Client{Timeout: 5 * time.Second}
	body := articlePostgresHTTPBody(t, client, server.URL+webapp.ArticleListPath)
	if strings.Contains(body, "<script>") || !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatalf("PostgreSQL Article title was not HTML-escaped: %q", body)
	}
	nullElement := articlePostgresElement(t, body, strconv.FormatInt(nullArticle.ID, 10))
	if !strings.Contains(nullElement, `class="summary summary-null"`) {
		t.Fatalf("PostgreSQL NULL summary representation = %q", nullElement)
	}
	emptyElement := articlePostgresElement(t, body, strconv.FormatInt(emptyArticle.ID, 10))
	if !strings.Contains(emptyElement, `<p class="summary"></p>`) || strings.Contains(emptyElement, "summary-null") ||
		!strings.Contains(emptyElement, "Saved Empty Summary") {
		t.Fatalf("PostgreSQL empty summary/save representation = %q", emptyElement)
	}
	if strings.Index(body, `data-article-id="`+strconv.FormatInt(nullArticle.ID, 10)+`"`) >
		strings.Index(body, `data-article-id="`+strconv.FormatInt(emptyArticle.ID, 10)+`"`) {
		t.Fatalf("PostgreSQL HTTP Articles are not ordered by primary key: %q", body)
	}
}

func articlePostgresHTTPBody(t *testing.T, client *http.Client, address string) string {
	t.Helper()
	response, err := client.Get(address)
	if err != nil {
		t.Fatalf("GET Article PostgreSQL page: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read Article PostgreSQL page: %v", err)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("Article PostgreSQL response = status %d Content-Type %q body=%q", response.StatusCode, response.Header.Get("Content-Type"), body)
	}
	return string(body)
}

func articlePostgresElement(t *testing.T, body, id string) string {
	t.Helper()
	start := strings.Index(body, `data-article-id="`+id+`"`)
	if start < 0 {
		t.Fatalf("PostgreSQL Article %s is missing from %q", id, body)
	}
	endOffset := strings.Index(body[start:], "</li>")
	if endOffset < 0 {
		t.Fatalf("PostgreSQL Article %s has no closing list item", id)
	}
	return body[start : start+endOffset]
}

func articlePostgresRedactedConnectionError(err error) error {
	var structured interface{ SQLState() string }
	if errors.As(err, &structured) {
		return fmt.Errorf("PostgreSQL SQLSTATE %s", structured.SQLState())
	}
	return errors.New("PostgreSQL connection failed")
}
