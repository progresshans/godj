package postgres

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/progresshans/godj/conformance/choicesproduct"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
)

func TestPostgresChoicesHistoryPreservesHeapRowsAndPhysicalChecks(t *testing.T) {
	url := postgresIntegrationURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	schema := postgresMigrationIntegrationSchema(t, ctx, url)
	backend := openPostgresMigrationIntegrationBackend(t, ctx, url, schema)
	sources, err := choicesproduct.Sources()
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	executor := migrations.Executor{Backend: backend}
	migrate := func(name string) migrations.ProjectState {
		t.Helper()
		state, err := executor.Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{App: "choices", Name: name})))
		if err != nil {
			t.Fatalf("migrate %s: %v", name, err)
		}
		return state
	}
	initial := migrate("0001_initial")
	category, entry := `"`+schema+`"."choices_category"`, `"`+schema+`"."choices_entry"`
	for _, sql := range []string{`INSERT INTO ` + category + ` (name) VALUES ('retired')`, `INSERT INTO ` + entry + ` (status,priority,category_id) VALUES ('legacy',99,1)`} {
		if _, err := backend.database.ExecContext(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	heapIdentity := func() string {
		t.Helper()
		var value string
		if err := backend.database.QueryRowContext(ctx, `SELECT oid::text || ':' || relfilenode::text FROM pg_catalog.pg_class WHERE oid=$1::regclass`, entry).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	beforeHeap := heapIdentity()
	second := migrate("0002_choices")
	third := migrate("0003_labels")
	if second.Equal(initial) || third.Equal(second) {
		t.Fatal("history lost choices metadata")
	}
	if restored := migrate("0001_initial"); !restored.Equal(initial) {
		t.Fatal("metadata reversal lost exact historical state")
	}
	if heapIdentity() != beforeHeap {
		t.Fatal("metadata choices replaced or rewrote the physical table")
	}
	migrate("0004_mixed")
	var status, name string
	var priority int64
	var notes *string
	if err := backend.database.QueryRowContext(ctx, `SELECT e.status,e.priority,c.name,e.notes FROM `+entry+` e JOIN `+category+` c ON c.id=e.category_id`).Scan(&status, &priority, &name, &notes); err != nil || status != "legacy" || priority != 99 || name != "retired" || notes != nil {
		t.Fatalf("choices changed existing data: %s %d %s %v", status, priority, name, err)
	}
	if _, err := backend.database.ExecContext(ctx, `INSERT INTO `+entry+` (status,priority,category_id) VALUES ('outside',-9223372036854775808,1)`); err != nil {
		t.Fatalf("choices leaked a DB constraint: %v", err)
	}
	if _, err := backend.database.ExecContext(ctx, `INSERT INTO `+entry+` (status,priority,category_id) VALUES ('open',0,999)`); err == nil {
		t.Fatal("choices disabled the physical FK")
	}
	migrate("0003_labels")
	readHistory := func() any {
		t.Helper()
		session, err := backend.OpenRevisionFencedSession(ctx)
		if err != nil {
			t.Fatal(err)
		}
		records, err := session.ReadAppliedMigrations(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := session.Close(ctx); err != nil {
			t.Fatal(err)
		}
		return records
	}
	beforeHistory := readHistory()
	if _, err := backend.database.ExecContext(ctx, `ALTER TABLE `+entry+` ADD COLUMN unexpected TEXT`); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{App: "choices", Name: "0002_choices"}))); err == nil {
		t.Fatal("metadata-only migration ignored physical drift")
	}
	if !reflect.DeepEqual(beforeHistory, readHistory()) {
		t.Fatal("rejected physical drift changed recorded history")
	}
}
