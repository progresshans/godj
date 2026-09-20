package postgres

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/internal/decimaltest"
	"github.com/progresshans/godj/migrations"
	mb "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

func TestPostgresDecimalPrecisionKeepsCachedReadersUsable(t *testing.T) {
	ctx := t.Context()
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, ctx, url)
	writer := openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
	loaded, before, _ := decimaltest.PrecisionHistory(t)
	executor := migrations.Executor{Backend: writer}
	initial := migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{App: "decimalref", Name: "0001_initial"}))
	if _, err := executor.Migrate(ctx, loaded, initial); err != nil {
		t.Fatal(err)
	}
	table, err := quoteTable(namespace, before.DBTable)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.database.ExecContext(ctx, "INSERT INTO "+table+` (value) VALUES (1.50),(10.50)`); err != nil {
		t.Fatal(err)
	}
	// A separate pool cannot rely on process-local notification from the
	// migration owner. Keep its one physical connection and warmed cache alive.
	reader := openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
	reader.database.SetMaxOpenConns(1)
	reader.database.SetMaxIdleConns(1)
	var pid int
	if err := reader.database.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	read := func(source db.Queryer, digits, places int, projection bool) {
		t.Helper()
		id := query.NewFieldRef("id", "id", query.FieldInteger, false)
		cost := query.NewDecimalFieldRef("value", "value", true, digits, places)
		plan := query.NewPlan(before.DBTable, []query.FieldRef{id, cost}).WithOrderings(query.NewOrdering(cost, query.Ascending))
		if projection {
			shape, err := query.NewProjectionResult(query.FieldResult(cost))
			if err != nil {
				t.Fatal(err)
			}
			plan, err = plan.WithDistinct().WithResultShape(shape)
			if err != nil {
				t.Fatal(err)
			}
		}
		rows, err := source.Query(ctx, plan)
		if err != nil {
			t.Fatal("cached Decimal result descriptor survived precision change", err)
		}
		defer func() {
			if err := rows.Close(); err != nil {
				t.Error(err)
			}
		}()
		var values []decimal.Decimal
		for rows.Next() {
			var key int64
			scanner := orm.NewNullableDecimalScanner(digits, places)
			if projection {
				err = rows.Scan(&scanner)
			} else {
				err = rows.Scan(&key, &scanner)
			}
			if err != nil || !scanner.Valid {
				t.Fatal("cached Decimal scanner lost value", err)
			}
			values = append(values, scanner.Decimal)
		}
		want := []decimal.Decimal{{Coefficient: "15", Exponent: -1}, {Coefficient: "105", Exponent: -1}}
		if err := rows.Err(); err != nil || !reflect.DeepEqual(values, want) {
			t.Fatal("Decimal result changed exact value or numeric order", err)
		}
	}
	for _, state := range []struct {
		target         migrations.LifecycleRequest
		digits, places int
	}{
		{initial, 5, 2}, {migrations.LatestLifecycleRequest(), 7, 3}, {initial, 5, 2}, {migrations.LatestLifecycleRequest(), 7, 3},
	} {
		if _, err := executor.Migrate(ctx, loaded, state.target); err != nil {
			t.Fatal(err)
		}
		for _, projection := range []bool{false, true} {
			read(reader, state.digits, state.places, projection)
			if err := reader.Atomic(ctx, func(session db.Session) error { read(session, state.digits, state.places, projection); return nil }); err != nil {
				t.Fatal(err)
			}
		}
		var currentPID int
		if err := reader.database.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&currentPID); err != nil || currentPID != pid {
			t.Fatal("cache regression replaced the physical reader", err)
		}
	}
}

func TestPostgresDecimalPrecisionSQLKeepsMixedOperationSlots(t *testing.T) {
	statements, err := NewMigrationSQLRenderer(MigrationSQLConfig{Schema: "product_schema"}).RenderForwardMigrationSQL(t.Context(), decimaltest.MixedPrecisionSQLRequest(t))
	want := []string{`ALTER TABLE "product_schema"."decimalref_cost" ALTER COLUMN "value" TYPE NUMERIC(7,3)`, `ALTER TABLE "product_schema"."decimalref_cost" ADD COLUMN "note" TEXT NULL`, ""}
	if err != nil || !reflect.DeepEqual(statements, want) {
		t.Fatalf("mixed SQL slots: %v %v", statements, err)
	}
}

func TestPostgresDecimalPrecisionScanFailures(t *testing.T) {
	_, before, after := decimaltest.PrecisionHistory(t)
	schema := postgresMigrationSchema{namespace: "public"}
	decimaltest.ScanFailures(t, "1.50", func(ctx context.Context, database *sql.DB) error {
		return schema.validateDecimalValues(ctx, database, before, before.Fields[1], after.Fields[1])
	})
}

func TestPostgresDecimalPrecisionRejectsNaNAndRetainsCatalogLocks(t *testing.T) {
	ctx := t.Context()
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, ctx, url)
	backend := openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
	loaded, before, after := decimaltest.PrecisionHistory(t)
	executor := migrations.Executor{Backend: backend}
	initial := migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{App: "decimalref", Name: "0001_initial"}))
	if _, err := executor.Migrate(ctx, loaded, initial); err != nil {
		t.Fatal(err)
	}
	table, err := quoteTable(namespace, before.DBTable)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.database.ExecContext(ctx, "INSERT INTO "+table+` (value) VALUES ('NaN'::numeric)`); err != nil {
		t.Fatal(err)
	}
	held := postgresMigrationIntegrationSession(t, ctx, backend)
	history, err := held.ReadAppliedMigrations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err == nil {
		t.Fatal("precision change adopted native NaN")
	}
	var raw string
	if err := backend.database.QueryRowContext(ctx, "SELECT value::text FROM "+table).Scan(&raw); err != nil || raw != "NaN" {
		t.Fatal("failed precision change modified NaN", err)
	}
	// The pre-failure private token and exact old typmod must remain usable.
	tx, err := held.BeginMigration(ctx, mb.HistoryTransition{Kind: mb.HistoryTransitionApply, Migration: mb.AppliedMigration{App: "decimalref", Name: "0002_precision"}}, mb.MigrationIntent{Operations: []mb.MigrationOperation{{OperationIndex: 0, Kind: mb.MigrationAlterField, Before: before, After: after}}})
	if err != nil {
		t.Fatal("failed precision scan changed revision or typmod", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	check := postgresMigrationIntegrationSession(t, ctx, backend)
	current, err := check.ReadAppliedMigrations(ctx)
	if err != nil || !reflect.DeepEqual(current, history) {
		t.Fatal("NaN rejection changed history", err)
	}
	if _, err := backend.database.ExecContext(ctx, "UPDATE "+table+` SET value=1.50`); err != nil {
		t.Fatal(err)
	}
	writer, err := backend.database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.ExecContext(ctx, "UPDATE "+table+` SET value=1.50`); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err == nil {
		t.Fatal("precision scan bypassed competing writer's lock")
	}
	if err := writer.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	if err := backend.database.QueryRowContext(ctx, "SELECT value::text FROM "+table).Scan(&raw); err != nil || raw != "1.500" {
		t.Fatal("native typmod failed to preserve exact value", err)
	}
	if _, err := executor.Migrate(ctx, loaded, initial); err != nil {
		t.Fatal(err)
	}
	if err := backend.database.QueryRowContext(ctx, "SELECT value::text FROM "+table).Scan(&raw); err != nil || raw != "1.50" {
		t.Fatal("native reverse typmod or value incorrect", err)
	}
}
