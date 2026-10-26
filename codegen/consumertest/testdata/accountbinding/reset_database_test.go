package consumer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/settings/database"
)

func resetCheck(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// A reconnect uses the exact configuration and a new real native connection.
// This is a runtime/connection restart; it does not claim process coverage.
func withResetDatabases(t *testing.T, schemas []ir.Schema, run func(*testing.T, database.Backend, func() database.Backend)) {
	open := func(t *testing.T, config database.Config) {
		reopen := func() database.Backend {
			backend, err := database.Open(t.Context(), config)
			resetCheck(t, err)
			t.Cleanup(func() { resetCheck(t, backend.Close()) })
			return backend
		}
		backend := reopen()
		migrateAccountDatabase(t, backend, schemas...)
		run(t, backend, reopen)
	}
	t.Run("sqlite", func(t *testing.T) {
		config, err := database.SQLite(filepath.Join(t.TempDir(), "selected-reset.sqlite3"))
		resetCheck(t, err)
		open(t, config)
	})
	url := strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL"))
	if url == "" {
		if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
			t.Fatal("required selected reset PostgreSQL connection absent")
		}
		return
	}
	t.Run("postgres", func(t *testing.T) {
		connection, err := pgx.Connect(t.Context(), url)
		resetCheck(t, err)
		schema := fmt.Sprintf("godj_selected_reset_%d_%d", os.Getpid(), time.Now().UnixNano())
		quoted := pgx.Identifier{schema}.Sanitize()
		_, err = connection.Exec(t.Context(), "CREATE SCHEMA "+quoted)
		resetCheck(t, err)
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_, err := connection.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE")
			resetCheck(t, err)
			resetCheck(t, connection.Close(ctx))
		})
		config, err := database.PostgreSQL(url, schema)
		resetCheck(t, err)
		open(t, config)
	})
}
