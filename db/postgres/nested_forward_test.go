package postgres

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/progresshans/godj/internal/querytest"
	"github.com/progresshans/godj/query"
)

func TestPostgreSQLNestedForwardPathsMatchDjango(t *testing.T) {
	databaseURL := postgresIntegrationURL(t)
	ctx := t.Context()
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(redactConnectionError(err))
	}
	schema := fmt.Sprintf("godj_nested_reference_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		_ = admin.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
		if err := admin.Close(ctx); err != nil {
			t.Error(redactConnectionError(err))
		}
	})
	post := quoted + `."nested_reference_post"`
	// Fixture instants are UTC; the separate administrative connection must
	// not inherit the developer machine's default time zone.
	if _, err := admin.Exec(ctx, "SET TIME ZONE 'UTC'"); err != nil {
		t.Fatal(err)
	}
	querytest.CreateNestedFixture(t, func(name string) string { return quoted + "." + pgx.Identifier{name}.Sanitize() }, "TIMESTAMP(6) WITH TIME ZONE", func(statement string) error { _, err := admin.Exec(ctx, statement); return err })

	backend, err := Open(ctx, Config{URL: databaseURL, Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	// Trace the production physical connection profile without retaining SQL
	// or credentials. Empty-result cases must avoid all connection I/O.
	config, err := currentConnectionConfig(databaseURL)
	if err != nil {
		t.Fatal("prepare traced PostgreSQL connection")
	}
	trace := &membershipTrace{table: post}
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
	querytest.CheckNestedReference(t, backend, func(plan query.Plan) (string, []any, error) { return compilePlan(schema, plan) }, func() (uint64, uint64) { return trace.reads.Load(), trace.total.Load() })
}
