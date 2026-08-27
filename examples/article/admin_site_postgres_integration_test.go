package article_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/db/postgres"
	articlemodels "github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/migrations"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
)

func TestArticleAdminSitePostgresUserFlow(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL"))
	if databaseURL == "" {
		if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
			t.Fatal("GODJ_REQUIRE_POSTGRES=1 requires GODJ_TEST_POSTGRES_URL")
		}
		t.Skip("GODJ_TEST_POSTGRES_URL is not configured; Article Admin PostgreSQL E2E was not run")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	adminConnection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect Article Admin PostgreSQL database: %v", articlePostgresRedactedConnectionError(err))
	}
	schema := fmt.Sprintf("godj_article_admin_%d", time.Now().UnixNano())
	quotedSchema := `"` + schema + `"`
	if _, err := adminConnection.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		_ = adminConnection.Close(ctx)
		t.Fatalf("create Article Admin PostgreSQL schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		if _, err := adminConnection.Exec(cleanupCtx, "DROP SCHEMA "+quotedSchema+" CASCADE"); err != nil {
			t.Errorf("drop Article Admin PostgreSQL schema: %v", err)
		}
		if err := adminConnection.Close(cleanupCtx); err != nil {
			t.Errorf("close Article Admin PostgreSQL admin connection: %v", err)
		}
	})

	loaded, _, err := migrationdefinition.Load(migrationdefinition.Source{
		SourceID: "migrations/0001_initial.godj.json",
		Document: append([]byte(nil), articlePostgresInitialDefinition...),
	})
	if err != nil {
		t.Fatalf("load Article Admin PostgreSQL migration definition: %v", err)
	}
	backend, err := postgres.Open(ctx, postgres.Config{URL: databaseURL, Schema: schema})
	if err != nil {
		t.Fatalf("open Article Admin PostgreSQL backend: %v", err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("close Article Admin PostgreSQL backend: %v", err)
		}
	})
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatalf("migrate Article Admin PostgreSQL fixture: %v", err)
	}
	first, err := articlemodels.ArticleObjects.Create(
		ctx,
		backend,
		articlemodels.NewArticleCreate("Go Alpha").WithSummaryNull(),
	)
	if err != nil {
		t.Fatalf("seed first Article Admin PostgreSQL row: %v", err)
	}
	second, err := articlemodels.ArticleObjects.Create(
		ctx,
		backend,
		articlemodels.NewArticleCreate("Rust Beta").WithSummary("Second row"),
	)
	if err != nil {
		t.Fatalf("seed second Article Admin PostgreSQL row: %v", err)
	}
	if first.ID != 1 || second.ID != 2 || first.Published || second.Published {
		t.Fatalf("Article Admin PostgreSQL seed rows = %#v/%#v", first, second)
	}

	runArticleAdminSiteUserFlow(t, newArticleAdminSiteFixtureWithBackend(t, backend))
}
