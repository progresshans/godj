package helpdesk_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/db/postgres"
)

func TestPublicHelpdeskPostgresConsumerAndPermissionMaintenance(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL"))
	if databaseURL == "" {
		if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
			t.Fatal("GODJ_REQUIRE_POSTGRES=1 requires GODJ_TEST_POSTGRES_URL")
		}
		t.Skip("GODJ_TEST_POSTGRES_URL is not configured; Helpdesk PostgreSQL consumer was not run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal("connect Helpdesk PostgreSQL fixture failed")
	}
	schema := fmt.Sprintf("godj_helpdesk_%d_%d", os.Getpid(), time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := connection.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		_ = connection.Close(ctx)
		t.Fatal("create Helpdesk PostgreSQL fixture schema failed")
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		if _, err := connection.Exec(cleanupCtx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error("drop Helpdesk PostgreSQL fixture schema failed")
		}
		if err := connection.Close(cleanupCtx); err != nil {
			t.Error("close Helpdesk PostgreSQL fixture connection failed")
		}
	})
	runPublicHelpdeskConsumer(t, ctx, func(ctx context.Context) (helpdeskBackend, error) {
		return postgres.Open(ctx, postgres.Config{URL: databaseURL, Schema: schema})
	})
}
