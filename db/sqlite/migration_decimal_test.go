package sqlite

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/internal/decimalstorage"
	"github.com/progresshans/godj/internal/decimaltest"
	"github.com/progresshans/godj/migrations"
)

func TestSQLiteDecimalPrecisionSQLKeepsMixedOperationSlots(t *testing.T) {
	statements, err := NewMigrationSQLRenderer().RenderForwardMigrationSQL(t.Context(), decimaltest.MixedPrecisionSQLRequest(t))
	want := []string{"", `ALTER TABLE "decimalref_cost" ADD COLUMN "note" TEXT NULL`, ""}
	if err != nil || !reflect.DeepEqual(statements, want) {
		t.Fatalf("mixed SQL slots: %v %v", statements, err)
	}
}

func TestSQLiteDecimalPrecisionScanFailures(t *testing.T) {
	_, before, after := decimaltest.PrecisionHistory(t)
	key, err := decimalstorage.Encode(decimal.Decimal{Coefficient: "15", Exponent: -1})
	if err != nil {
		t.Fatal(err)
	}
	decimaltest.ScanFailures(t, key, func(ctx context.Context, database *sql.DB) error {
		return validateSQLiteDecimalValues(ctx, database, before, before.Fields[1], after.Fields[1])
	})
}

func TestSQLiteDecimalPrecisionRejectsCorruptionAndPreservesPhysicalStorage(t *testing.T) {
	ctx := t.Context()
	backend := openMigrationTestBackend(t)
	loaded, _, _ := decimaltest.PrecisionHistory(t)
	executor := migrations.Executor{Backend: backend}
	initial := migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{App: "decimalref", Name: "0001_initial"}))
	if _, err := executor.Migrate(ctx, loaded, initial); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.ExecContext(ctx, `INSERT INTO decimalref_cost (value) VALUES (NULL)`); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := backend.database.QueryRowContext(ctx, "PRAGMA schema_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	history, err := readAtomicMigrationRevisionSnapshot(ctx, backend)
	if err != nil {
		t.Fatal(err)
	}
	key := func(raw string) []byte {
		t.Helper()
		value, err := decimal.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		key, err := decimalstorage.Encode(value)
		if err != nil {
			t.Fatal(err)
		}
		return key
	}
	for _, raw := range []any{"1.5", int64(1), float64(1.5), []byte("1.5"), []byte{2}, key("1000"), key("1.001")} {
		if _, err := backend.ExecContext(ctx, `UPDATE decimalref_cost SET value=?`, raw); err != nil {
			t.Fatal(err)
		}
		if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err == nil {
			t.Fatal("widening adopted invalid historical Decimal storage")
		}
		var stored any
		if err := backend.database.QueryRowContext(ctx, `SELECT value FROM decimalref_cost`).Scan(&stored); err != nil || !reflect.DeepEqual(stored, raw) {
			t.Fatal("failed precision change modified foreign value", err)
		}
		current, err := readAtomicMigrationRevisionSnapshot(ctx, backend)
		if err != nil || !reflect.DeepEqual(current, history) {
			t.Fatal("invalid Decimal advanced private revision/history", err)
		}
	}
	valid := key("1.5")
	if _, err := backend.ExecContext(ctx, `UPDATE decimalref_cost SET value=?`, valid); err != nil {
		t.Fatal(err)
	}
	for _, target := range []migrations.LifecycleRequest{migrations.LatestLifecycleRequest(), initial} {
		if _, err := executor.Migrate(ctx, loaded, target); err != nil {
			t.Fatal(err)
		}
		var currentVersion int
		var stored []byte
		if err := backend.database.QueryRowContext(ctx, "PRAGMA schema_version").Scan(&currentVersion); err != nil || currentVersion != version {
			t.Fatal("precision-only change performed physical DDL", err)
		}
		if err := backend.database.QueryRowContext(ctx, `SELECT value FROM decimalref_cost`).Scan(&stored); err != nil || !reflect.DeepEqual(stored, valid) {
			t.Fatal("precision-only change rewrote the exact storage key", err)
		}
	}
}
