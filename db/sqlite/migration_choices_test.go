package sqlite

import (
	"context"
	"reflect"
	"testing"

	"github.com/progresshans/godj/conformance/choicesproduct"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestSQLiteChoicesHistoryChangesWithoutDDLAndPreservesPhysicalChecks(t *testing.T) {
	ctx := context.Background()
	backend := openMigrationTestBackend(t)
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
	for _, sql := range []string{`INSERT INTO choices_category (name) VALUES ('retired')`, `INSERT INTO choices_entry (status,priority,category_id) VALUES ('legacy',99,1)`} {
		if _, err := backend.ExecContext(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	var schemaVersion int
	if err := backend.database.QueryRowContext(ctx, "PRAGMA schema_version").Scan(&schemaVersion); err != nil {
		t.Fatal(err)
	}
	initialRevision, err := readAtomicMigrationRevisionSnapshot(ctx, backend)
	if err != nil {
		t.Fatal(err)
	}
	second := migrate("0002_choices")
	third := migrate("0003_labels")
	if second.Equal(initial) || third.Equal(second) {
		t.Fatal("history lost choice metadata")
	}
	reverted := migrate("0001_initial")
	if !reverted.Equal(initial) {
		t.Fatal("metadata rollback did not restore initial state")
	}
	var afterVersion int
	if err := backend.database.QueryRowContext(ctx, "PRAGMA schema_version").Scan(&afterVersion); err != nil || afterVersion != schemaVersion {
		t.Fatalf("metadata changes executed DDL: %d -> %d (%v)", schemaVersion, afterVersion, err)
	}
	afterRevision, err := readAtomicMigrationRevisionSnapshot(ctx, backend)
	if err != nil {
		t.Fatal(err)
	}
	if afterRevision.token.revision != initialRevision.token.revision+4 || !reflect.DeepEqual(afterRevision.records, initialRevision.records) {
		t.Fatal("metadata transitions did not advance fenced history exactly once per migration")
	}
	migrate("0004_mixed")
	var status, category string
	var priority int64
	var notes *string
	if err := backend.database.QueryRowContext(ctx, `SELECT e.status,e.priority,c.name,e.notes FROM choices_entry e JOIN choices_category c ON c.id=e.category_id`).Scan(&status, &priority, &category, &notes); err != nil || status != "legacy" || priority != 99 || category != "retired" || notes != nil {
		t.Fatalf("choices changed existing rows: %s %d %s %v", status, priority, category, err)
	}
	if _, err := backend.ExecContext(ctx, `INSERT INTO choices_entry (status,priority,category_id) VALUES ('outside',-9223372036854775808,1)`); err != nil {
		t.Fatalf("choices leaked a database value constraint: %v", err)
	}
	if _, err := backend.ExecContext(ctx, `INSERT INTO choices_entry (status,priority,category_id) VALUES ('open',0,999)`); err == nil {
		t.Fatal("choices disabled the physical foreign key")
	}
	migrate("0003_labels")
	// A metadata-only transition still requires the exact physical preimage.
	if _, err := backend.ExecContext(ctx, `ALTER TABLE choices_entry ADD COLUMN unexpected TEXT`); err != nil {
		t.Fatal(err)
	}
	beforeFailure, err := readAtomicMigrationRevisionSnapshot(ctx, backend)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{App: "choices", Name: "0002_choices"}))); err == nil {
		t.Fatal("physical drift was ignored for metadata-only migration")
	}
	afterFailure, err := readAtomicMigrationRevisionSnapshot(ctx, backend)
	if err != nil {
		t.Fatal(err)
	}
	if beforeFailure.token != afterFailure.token || !reflect.DeepEqual(beforeFailure.records, afterFailure.records) {
		t.Fatal("rejected physical drift changed durable history")
	}
}

func TestSQLiteIntentSealOwnsChoicesAndCompleteScalarPayload(t *testing.T) {
	model := migrationTestModel(false)
	model.Fields[1].Choices = []ir.Choice{schema.Choice("open", "Open"), schema.Choice("closed", "Closed")}
	model.Fields = append(model.Fields, ir.Field{Name: "at", GoName: "At", Column: "at", Kind: ir.FieldDateTime, Default: &ir.Scalar{Kind: ir.ScalarDateTime, DateTime: "2026-09-19T00:00:00.000000Z"}})
	for _, mutate := range []func(*ir.Model){
		func(model *ir.Model) { model.Fields[1].Choices[0].Label = "Tampered" },
		func(model *ir.Model) { model.Fields[1].Choices[0].Value.String = "pending" },
		func(model *ir.Model) {
			model.Fields[1].Choices[0], model.Fields[1].Choices[1] = model.Fields[1].Choices[1], model.Fields[1].Choices[0]
		},
		func(model *ir.Model) {
			model.Fields[len(model.Fields)-1].Default.DateTime = "2026-09-20T00:00:00.000000Z"
		},
	} {
		seal, err := validateAndSealSQLiteRelationIntent(migrationbackend.HistoryTransition{Kind: migrationbackend.HistoryTransitionApply, Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_initial"}}, createModelMigrationIntent(model))
		if err != nil {
			t.Fatal(err)
		}
		mutate(&seal.intent.Operations[0].After)
		if err := verifySQLiteRelationIntentSeal(&seal); err == nil {
			t.Fatal("modified scalar metadata retained migration execution authority")
		}
	}
}
