package article_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/migrations"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
)

func TestArticleAPIBearerPostgresUserFlow(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL"))
	if databaseURL == "" {
		if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
			t.Fatal("GODJ_REQUIRE_POSTGRES=1 requires GODJ_TEST_POSTGRES_URL")
		}
		t.Skip("GODJ_TEST_POSTGRES_URL is not configured; Article API Bearer PostgreSQL E2E was not run")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	adminConnection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect Article API Bearer PostgreSQL database: %v", articlePostgresRedactedConnectionError(err))
	}
	schema := fmt.Sprintf("godj_article_api_bearer_%d", time.Now().UnixNano())
	quotedSchema := `"` + schema + `"`
	if _, err := adminConnection.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		_ = adminConnection.Close(ctx)
		t.Fatalf("create Article API Bearer PostgreSQL schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		if _, err := adminConnection.Exec(cleanupCtx, "DROP SCHEMA "+quotedSchema+" CASCADE"); err != nil {
			t.Errorf("drop Article API Bearer PostgreSQL schema: %v", err)
		}
		if err := adminConnection.Close(cleanupCtx); err != nil {
			t.Errorf("close Article API Bearer PostgreSQL admin connection: %v", err)
		}
	})

	if got := fmt.Sprintf("%x", sha256.Sum256(articlePostgresInitialDefinition)); got != articleAPIPostgresDefinitionSHA256 {
		t.Fatalf("Article API Bearer PostgreSQL migration fixture SHA-256 = %s, want %s", got, articleAPIPostgresDefinitionSHA256)
	}
	loaded, report, err := migrationdefinition.Load(migrationdefinition.Source{
		SourceID: "migrations/0001_initial.godj.json",
		Document: append([]byte(nil), articlePostgresInitialDefinition...),
	})
	if err != nil {
		t.Fatalf("load Article API Bearer PostgreSQL migration definition: %v", err)
	}
	if report.DocumentsReceived != 1 || report.HeadersValidated != 1 || report.OperationsDecoded != 1 ||
		report.PlannerConstruction != 1 || report.DefinitionsPublished != 1 || report.DefinitionSetsPublished != 1 {
		t.Fatalf("Article API Bearer PostgreSQL definition load report = %+v", report)
	}
	backend, err := postgres.Open(ctx, postgres.Config{URL: databaseURL, Schema: schema})
	if err != nil {
		t.Fatalf("open Article API Bearer PostgreSQL backend: %v", err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("close Article API Bearer PostgreSQL backend: %v", err)
		}
	})
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatalf("migrate Article API Bearer PostgreSQL fixture: %v", err)
	}

	runArticleAPIBearerUserFlow(t, backend)
}
