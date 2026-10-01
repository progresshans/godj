package postgres

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/progresshans/godj/internal/querytest"
	"github.com/progresshans/godj/query"
)

func TestPostgreSQLPrefetchOwnerWindowsMatchDjango(t *testing.T) {
	url := postgresIntegrationURL(t)
	schema := postgresMigrationIntegrationSchema(t, t.Context(), url)
	backend := openPostgresMigrationIntegrationBackend(t, t.Context(), url, schema)
	admin, err := pgx.Connect(t.Context(), url)
	if err != nil {
		t.Fatal(redactConnectionError(err))
	}
	t.Cleanup(func() {
		if err := admin.Close(context.Background()); err != nil {
			t.Error(redactConnectionError(err))
		}
	})
	querytest.CreatePrefetchWindowFixture(t, func(name string) string { return pgx.Identifier{schema, name}.Sanitize() }, "JSONB", func(statement string) error {
		_, err := admin.Exec(t.Context(), statement)
		return err
	})
	config, err := currentConnectionConfig(url)
	if err != nil {
		t.Fatal("prepare traced PostgreSQL connection")
	}
	trace := &membershipTrace{table: pgx.Identifier{schema}.Sanitize() + "."}
	config.Tracer = trace
	if err := backend.database.Close(); err != nil {
		t.Fatal(err)
	}
	backend.database = stdlib.OpenDB(*config,
		stdlib.OptionAfterConnect(validateAndCloseInvalidPostgresPhysicalSession),
		stdlib.OptionResetSession(func(ctx context.Context, connection *pgx.Conn) error {
			if err := validateCurrentPostgresPhysicalSession(ctx, connection); err != nil {
				return errors.Join(driver.ErrBadConn, err)
			}
			return nil
		}),
	)
	querytest.CheckPrefetchWindows(t, backend, func(plan query.Plan) (string, []any, error) { return compilePlan(schema, plan) }, func() (uint64, uint64) { return trace.reads.Load(), trace.total.Load() })
}
