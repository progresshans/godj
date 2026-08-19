package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

func TestSQLiteRelationMigrationCapabilities(t *testing.T) {
	t.Parallel()
	got := (&Backend{}).RelationMigrationCapabilities()
	want := migrationbackend.RelationMigrationCapabilities{
		CreateModelForeignKeys:            true,
		AddNullableForeignKey:             false,
		AddRequiredForeignKeyToEmptyTable: false,
		RemoveForeignKeyByTableRemake:     false,
	}
	if got != want {
		t.Fatalf("RelationMigrationCapabilities() = %+v, want %+v", got, want)
	}
}

func TestSQLiteRelationForeignKeysOffUsesSQLiteCapability(t *testing.T) {
	assertSQLiteRelationCapabilityFeature(t, sqliteRelationForeignKeysCapabilityError(0), "sqlite_relation_migration")
}

func TestSQLiteRelationCreateModelRoundTripUsesOneFencedTransaction(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-create-round-trip")
	if err != nil {
		t.Fatalf("OpenMemory(): %v", err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("Close(): %v", err)
		}
	})

	target, source, sourceField := sqliteRelationTestModels()
	targetKey := target.Fields[0]
	applyIntent := migrationbackend.RelationMigrationIntent{Operations: []migrationbackend.RelationMigrationOperation{
		{
			OperationIndex: 0,
			Kind:           migrationbackend.RelationMigrationCreateModel,
			After:          target,
		},
		{
			OperationIndex: 1,
			Kind:           migrationbackend.RelationMigrationCreateModel,
			After:          source,
			Targets: []migrationbackend.RelationMigrationTarget{{
				SourceField: sourceField,
				TargetModel: target,
				TargetKey:   targetKey,
			}},
		},
	}}
	transition := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_relation"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}

	session := openSQLiteRelationSession(t, backend)
	if records, err := session.ReadAppliedMigrations(ctx); err != nil || len(records) != 0 {
		t.Fatalf("fresh relation snapshot = (%v, %v), want empty", records, err)
	}
	transaction, err := session.BeginRelationFencedMigration(ctx, transition, applyIntent)
	if err != nil {
		t.Fatalf("BeginRelationFencedMigration(apply): %v", err)
	}

	// The backend owns a deep sealed copy after Begin. Mutating every caller
	// alias must not affect the exact operations executed from independent
	// snapshots.
	executeTarget := target.Clone()
	executeSource := source.Clone()
	executeSourceField := executeSource.Fields[2].Clone()
	applyIntent.Operations[0].After.DBTable = "mutated_target"
	applyIntent.Operations[1].After.Fields[2].Column = "mutated_source_id"
	applyIntent.Operations[1].Targets[0].TargetModel.Fields[0].Column = "mutated_key"
	if err := transaction.CreateModel(ctx, executeTarget); err != nil {
		t.Fatalf("CreateModel(target): %v", err)
	}
	if err := transaction.CreateModel(ctx, executeSource); err != nil {
		t.Fatalf("CreateModel(source): %v", err)
	}
	concreteTransaction := transaction.(*sqliteRevisionFencedTransaction)
	if _, err := concreteTransaction.connection.ExecContext(
		ctx,
		`INSERT INTO "news_article" ("title", "author_id") VALUES ('pinned-orphan', 999)`,
	); err == nil {
		t.Fatal("orphan relation insert succeeded on the exact pinned migration connection")
	}
	if err := transaction.RecordApplied(ctx, "news", "0001_relation"); err != nil {
		t.Fatalf("RecordApplied(): %v", err)
	}
	outcome, err := transaction.CommitFenced(ctx)
	if err != nil || outcome.Durability != migrationbackend.CommitCommitted {
		t.Fatalf("CommitFenced(apply) = (%+v, %v)", outcome, err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatalf("Close(apply session): %v", err)
	}

	var createSQL string
	if err := backend.database.QueryRowContext(
		ctx,
		`SELECT "sql" FROM main.sqlite_schema WHERE "type" = 'table' AND "name" = ?`,
		source.DBTable,
	).Scan(&createSQL); err != nil {
		t.Fatalf("read source CREATE TABLE: %v", err)
	}
	wantConstraint := `FOREIGN KEY ("author_id") REFERENCES "news_author" ("id") ON DELETE NO ACTION`
	if !strings.Contains(createSQL, wantConstraint) {
		t.Fatalf("source CREATE TABLE = %q, want constraint %q", createSQL, wantConstraint)
	}
	if _, err := backend.ExecContext(ctx, `INSERT INTO "news_article" ("title", "author_id") VALUES ('orphan', 999)`); err == nil {
		t.Fatal("orphan relation insert succeeded with pinned foreign_keys enforcement")
	}
	if _, err := backend.ExecContext(ctx, `INSERT INTO "news_author" ("name") VALUES ('Ada')`); err != nil {
		t.Fatalf("insert target row: %v", err)
	}
	if _, err := backend.ExecContext(ctx, `INSERT INTO "news_article" ("title", "author_id") VALUES ('valid', 1)`); err != nil {
		t.Fatalf("insert valid relation row: %v", err)
	}
	if _, err := backend.ExecContext(ctx, `DELETE FROM "news_author" WHERE "id" = 1`); err == nil {
		t.Fatal("NO ACTION target delete succeeded while child exists")
	}
	if _, err := backend.ExecContext(ctx, `DELETE FROM "news_article"`); err != nil {
		t.Fatalf("clear child rows: %v", err)
	}

	unapplyIntent := migrationbackend.RelationMigrationIntent{Operations: []migrationbackend.RelationMigrationOperation{
		{
			OperationIndex: 1,
			Kind:           migrationbackend.RelationMigrationDeleteModel,
			Before:         executeSource,
			Targets: []migrationbackend.RelationMigrationTarget{{
				SourceField: executeSourceField,
				TargetModel: executeTarget,
				TargetKey:   executeTarget.Fields[0],
			}},
		},
		{
			OperationIndex: 0,
			Kind:           migrationbackend.RelationMigrationDeleteModel,
			Before:         executeTarget,
		},
	}}
	unapplyTransition := transition
	unapplyTransition.Kind = migrationbackend.HistoryTransitionUnapply
	session = openSQLiteRelationSession(t, backend)
	records, err := session.ReadAppliedMigrations(ctx)
	if err != nil || !reflect.DeepEqual(records, []migrationbackend.AppliedMigration{transition.Migration}) {
		t.Fatalf("unapply snapshot = (%v, %v)", records, err)
	}
	transaction, err = session.BeginRelationFencedMigration(ctx, unapplyTransition, unapplyIntent)
	if err != nil {
		t.Fatalf("BeginRelationFencedMigration(unapply): %v", err)
	}
	if err := transaction.DeleteModel(ctx, executeSource); err != nil {
		t.Fatalf("DeleteModel(child): %v", err)
	}
	if err := transaction.DeleteModel(ctx, executeTarget); err != nil {
		t.Fatalf("DeleteModel(target): %v", err)
	}
	if err := transaction.RecordUnapplied(ctx, "news", "0001_relation"); err != nil {
		t.Fatalf("RecordUnapplied(): %v", err)
	}
	outcome, err = transaction.CommitFenced(ctx)
	if err != nil || outcome.Durability != migrationbackend.CommitCommitted {
		t.Fatalf("CommitFenced(unapply) = (%+v, %v)", outcome, err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatalf("Close(unapply session): %v", err)
	}
	for _, table := range []string{executeSource.DBTable, executeTarget.DBTable} {
		var count int
		if err := backend.database.QueryRowContext(
			ctx,
			`SELECT COUNT(*) FROM main.sqlite_schema WHERE "type" = 'table' AND "name" = ?`,
			table,
		).Scan(&count); err != nil || count != 0 {
			t.Fatalf("final table %q count = (%d, %v), want 0", table, count, err)
		}
	}
	snapshot, err := readAtomicMigrationRevisionSnapshot(ctx, backend)
	if err != nil {
		t.Fatalf("read final revision snapshot: %v", err)
	}
	if len(snapshot.records) != 0 || snapshot.token.revision != 2 {
		t.Fatalf("final revision snapshot = %+v", snapshot)
	}
}

func TestSQLiteRelationDirectPortSurvivesCloseReopenApplyUnapplyReapply(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "relation-direct-port.sqlite")
	open := func() *Backend {
		t.Helper()
		backend, err := Open(ctx, "file:"+filepath.ToSlash(path)+"?mode=rwc")
		if err != nil {
			t.Fatalf("Open(%s): %v", path, err)
		}
		return backend
	}
	target, source, sourceField := sqliteRelationTestModels()
	migration := migrationbackend.AppliedMigration{App: "news", Name: "0001_relation"}

	apply := func(backend *Backend) {
		t.Helper()
		session := openSQLiteRelationSession(t, backend)
		if _, err := session.ReadAppliedMigrations(ctx); err != nil {
			t.Fatal(err)
		}
		transaction, err := session.BeginRelationFencedMigration(ctx, migrationbackend.HistoryTransition{
			Migration: migration,
			Kind:      migrationbackend.HistoryTransitionApply,
		}, sqliteRelationApplyIntent(target, source, sourceField))
		if err != nil {
			t.Fatal(err)
		}
		if err := transaction.CreateModel(ctx, target); err != nil {
			t.Fatal(err)
		}
		if err := transaction.CreateModel(ctx, source); err != nil {
			t.Fatal(err)
		}
		if err := transaction.RecordApplied(ctx, migration.App, migration.Name); err != nil {
			t.Fatal(err)
		}
		if outcome, err := transaction.CommitFenced(ctx); err != nil || outcome.Durability != migrationbackend.CommitCommitted {
			t.Fatalf("CommitFenced(apply) = (%+v, %v)", outcome, err)
		}
		if err := session.Close(ctx); err != nil {
			t.Fatal(err)
		}
	}
	unapply := func(backend *Backend) {
		t.Helper()
		session := openSQLiteRelationSession(t, backend)
		records, err := session.ReadAppliedMigrations(ctx)
		if err != nil || !reflect.DeepEqual(records, []migrationbackend.AppliedMigration{migration}) {
			t.Fatalf("ReadAppliedMigrations(unapply) = (%v, %v)", records, err)
		}
		transaction, err := session.BeginRelationFencedMigration(ctx, migrationbackend.HistoryTransition{
			Migration: migration,
			Kind:      migrationbackend.HistoryTransitionUnapply,
		}, sqliteRelationUnapplyIntent(target, source, sourceField))
		if err != nil {
			t.Fatal(err)
		}
		if err := transaction.DeleteModel(ctx, source); err != nil {
			t.Fatal(err)
		}
		if err := transaction.DeleteModel(ctx, target); err != nil {
			t.Fatal(err)
		}
		if err := transaction.RecordUnapplied(ctx, migration.App, migration.Name); err != nil {
			t.Fatal(err)
		}
		if outcome, err := transaction.CommitFenced(ctx); err != nil || outcome.Durability != migrationbackend.CommitCommitted {
			t.Fatalf("CommitFenced(unapply) = (%+v, %v)", outcome, err)
		}
		if err := session.Close(ctx); err != nil {
			t.Fatal(err)
		}
	}

	backend := open()
	apply(backend)
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	backend = open()
	unapply(backend)
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	backend = open()
	apply(backend)
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	backend = open()
	defer func() {
		if err := backend.Close(); err != nil {
			t.Errorf("final Close(): %v", err)
		}
	}()
	snapshot, err := readAtomicMigrationRevisionSnapshot(ctx, backend)
	if err != nil || snapshot.token.revision != 3 ||
		!reflect.DeepEqual(snapshot.records, []migrationbackend.AppliedMigration{migration}) {
		t.Fatalf("reopened final snapshot = (%+v, %v)", snapshot, err)
	}
	if !sqliteRelationTestTableExists(t, backend, target.DBTable) || !sqliteRelationTestTableExists(t, backend, source.DBTable) {
		t.Fatal("reopened reapply did not preserve both relation tables")
	}
}

func TestSQLiteRelationBeginFaultsCleanUpBeforePublication(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		contains    string
		setup       func(*testing.T, context.Context, *Backend, *ir.Model, *ir.Model, *ir.Field) migrationbackend.RelationMigrationIntent
		checkpoints []sqliteRelationBeginCheckpoint
		begun       bool
	}{
		{name: "pragma_set", method: "exec", contains: "PRAGMA foreign_keys = ON"},
		{name: "pragma_read", method: "query_row", contains: "PRAGMA foreign_keys", checkpoints: []sqliteRelationBeginCheckpoint{sqliteRelationCheckpointForeignKeysSet}},
		{name: "begin_immediate", method: "exec", contains: "BEGIN IMMEDIATE", checkpoints: []sqliteRelationBeginCheckpoint{sqliteRelationCheckpointForeignKeysSet, sqliteRelationCheckpointForeignKeysRead}},
		{name: "catalog", method: "query", contains: "FROM main.sqlite_schema", begun: true, checkpoints: []sqliteRelationBeginCheckpoint{sqliteRelationCheckpointForeignKeysSet, sqliteRelationCheckpointForeignKeysRead, sqliteRelationCheckpointTransactionBegun}},
		{
			name:     "physical_preflight",
			method:   "query",
			contains: `PRAGMA main.table_xinfo("news_author")`,
			begun:    true,
			checkpoints: []sqliteRelationBeginCheckpoint{
				sqliteRelationCheckpointForeignKeysSet,
				sqliteRelationCheckpointForeignKeysRead,
				sqliteRelationCheckpointTransactionBegun,
			},
			setup: func(t *testing.T, ctx context.Context, backend *Backend, target, source *ir.Model, sourceField *ir.Field) migrationbackend.RelationMigrationIntent {
				t.Helper()
				statement, err := compileMigrationCreateModel(*target)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := backend.ExecContext(ctx, statement); err != nil {
					t.Fatal(err)
				}
				sourceField.Relation.Target.AppLabel = "accounts"
				source.Fields[2] = sourceField.Clone()
				return migrationbackend.RelationMigrationIntent{Operations: []migrationbackend.RelationMigrationOperation{{
					OperationIndex: 0,
					Kind:           migrationbackend.RelationMigrationCreateModel,
					After:          source.Clone(),
					Targets: []migrationbackend.RelationMigrationTarget{{
						SourceField: sourceField.Clone(),
						TargetModel: target.Clone(),
						TargetKey:   target.Fields[0].Clone(),
					}},
				}}}
			},
		},
		{
			name:     "revision_claim",
			method:   "exec",
			contains: `CREATE TABLE "godj_migration_revision"`,
			begun:    true,
			checkpoints: []sqliteRelationBeginCheckpoint{
				sqliteRelationCheckpointForeignKeysSet,
				sqliteRelationCheckpointForeignKeysRead,
				sqliteRelationCheckpointTransactionBegun,
				sqliteRelationCheckpointPhysicalPreflightComplete,
				sqliteRelationCheckpointRevisionClaimStarting,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			backend, err := OpenMemory(ctx, "relation-begin-fault-"+test.name)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = backend.Close() })
			target, source, sourceField := sqliteRelationTestModels()
			intent := sqliteRelationApplyIntent(target, source, sourceField)
			if test.setup != nil {
				intent = test.setup(t, ctx, backend, &target, &source, &sourceField)
			}
			session := openSQLiteRelationSession(t, backend)
			if _, err := session.ReadAppliedMigrations(ctx); err != nil {
				t.Fatal(err)
			}
			concrete := session.(*sqliteRevisionFencedSession)
			var checkpoints []sqliteRelationBeginCheckpoint
			concrete.relationBeginCheckpoint = func(checkpoint sqliteRelationBeginCheckpoint) {
				checkpoints = append(checkpoints, checkpoint)
			}
			fault := &sqliteRelationBeginFaultConnection{
				method:    test.method,
				contains:  test.contains,
				remaining: 1,
			}
			concrete.relationConnectionHook = func(connection migrationPinnedConnection) migrationPinnedConnection {
				fault.migrationPinnedConnection = connection
				return fault
			}
			transaction, err := session.BeginRelationFencedMigration(ctx, migrationbackend.HistoryTransition{
				Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_relation"},
				Kind:      migrationbackend.HistoryTransitionApply,
			}, intent)
			if transaction != nil || err == nil || fault.remaining != 0 {
				t.Fatalf("BeginRelationFencedMigration() = (%v, %v), remaining fault=%d", transaction, err, fault.remaining)
			}
			if !reflect.DeepEqual(checkpoints, test.checkpoints) {
				t.Fatalf("begin checkpoints = %v, want %v", checkpoints, test.checkpoints)
			}
			wantClose, wantRaw := 1, 0
			if test.name == "begin_immediate" {
				wantClose, wantRaw = 0, 1
			}
			if fault.closeCalls != wantClose || fault.rawCalls != wantRaw ||
				test.begun && fault.rollbackCalls != 1 || !test.begun && fault.rollbackCalls != 0 {
				t.Fatalf("cleanup calls = close:%d raw:%d rollback:%d, want close:%d raw:%d begun=%t", fault.closeCalls, fault.rawCalls, fault.rollbackCalls, wantClose, wantRaw, test.begun)
			}
			if concrete.active != nil || concrete.state != revisionSessionPoisoned {
				t.Fatalf("failed session = active:%v state:%d", concrete.active, concrete.state)
			}
			if err := session.Close(ctx); err != nil {
				t.Fatalf("Close(poisoned session): %v", err)
			}
			if sqliteRelationTestTableExists(t, backend, source.DBTable) || test.setup == nil && sqliteRelationTestTableExists(t, backend, target.DBTable) {
				t.Fatal("Begin fault published relation DDL")
			}
			snapshot, snapshotErr := readAtomicMigrationRevisionSnapshot(ctx, backend)
			if snapshotErr != nil || snapshot.token.initialized || len(snapshot.records) != 0 {
				t.Fatalf("history after Begin fault = (%+v, %v)", snapshot, snapshotErr)
			}
		})
	}

	t.Run("acquire_connection", func(t *testing.T) {
		ctx := context.Background()
		backend, err := OpenMemory(ctx, "relation-begin-fault-acquire")
		if err != nil {
			t.Fatal(err)
		}
		session := openSQLiteRelationSession(t, backend)
		if _, err := session.ReadAppliedMigrations(ctx); err != nil {
			t.Fatal(err)
		}
		if err := backend.Close(); err != nil {
			t.Fatal(err)
		}
		target, source, sourceField := sqliteRelationTestModels()
		transaction, err := session.BeginRelationFencedMigration(ctx, migrationbackend.HistoryTransition{
			Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_relation"},
			Kind:      migrationbackend.HistoryTransitionApply,
		}, sqliteRelationApplyIntent(target, source, sourceField))
		concrete := session.(*sqliteRevisionFencedSession)
		if transaction != nil || err == nil || concrete.active != nil || concrete.state != revisionSessionPoisoned {
			t.Fatalf("closed-backend Begin = transaction:%v error:%v active:%v state:%d", transaction, err, concrete.active, concrete.state)
		}
		if err := session.Close(ctx); err != nil {
			t.Fatalf("Close(acquire-failed session): %v", err)
		}
	})

	t.Run("pre_begin_close_failure_joins_primary", func(t *testing.T) {
		ctx := context.Background()
		backend, err := OpenMemory(ctx, "relation-begin-close-cleanup-fault")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = backend.Close() }()
		session := openSQLiteRelationSession(t, backend)
		if _, err := session.ReadAppliedMigrations(ctx); err != nil {
			t.Fatal(err)
		}
		primary := errors.New("relation pragma primary fault")
		cleanup := errors.New("relation connection close cleanup fault")
		fault := &sqliteRelationBeginFaultConnection{
			method:    "exec",
			contains:  "PRAGMA foreign_keys = ON",
			remaining: 1,
			faultErr:  primary,
			closeErr:  cleanup,
		}
		concrete := session.(*sqliteRevisionFencedSession)
		concrete.relationConnectionHook = func(connection migrationPinnedConnection) migrationPinnedConnection {
			fault.migrationPinnedConnection = connection
			return fault
		}
		target, source, sourceField := sqliteRelationTestModels()
		transaction, err := session.BeginRelationFencedMigration(ctx, migrationbackend.HistoryTransition{
			Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_relation"},
			Kind:      migrationbackend.HistoryTransitionApply,
		}, sqliteRelationApplyIntent(target, source, sourceField))
		if transaction != nil || !errors.Is(err, primary) || !errors.Is(err, cleanup) ||
			fault.closeCalls != 1 || fault.rawCalls != 1 {
			t.Fatalf("close-cleanup Begin = transaction:%v error:%v close:%d raw:%d", transaction, err, fault.closeCalls, fault.rawCalls)
		}
		if err := session.Close(ctx); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("post_begin_rollback_failure_joins_primary", func(t *testing.T) {
		ctx := context.Background()
		backend, err := OpenMemory(ctx, "relation-begin-rollback-cleanup-fault")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = backend.Close() }()
		session := openSQLiteRelationSession(t, backend)
		if _, err := session.ReadAppliedMigrations(ctx); err != nil {
			t.Fatal(err)
		}
		primary := errors.New("relation catalog primary fault")
		cleanup := errors.New("relation rollback cleanup fault")
		fault := &sqliteRelationBeginFaultConnection{
			method:      "query",
			contains:    "FROM main.sqlite_schema",
			remaining:   1,
			faultErr:    primary,
			rollbackErr: cleanup,
		}
		concrete := session.(*sqliteRevisionFencedSession)
		concrete.relationConnectionHook = func(connection migrationPinnedConnection) migrationPinnedConnection {
			fault.migrationPinnedConnection = connection
			return fault
		}
		target, source, sourceField := sqliteRelationTestModels()
		transaction, err := session.BeginRelationFencedMigration(ctx, migrationbackend.HistoryTransition{
			Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_relation"},
			Kind:      migrationbackend.HistoryTransitionApply,
		}, sqliteRelationApplyIntent(target, source, sourceField))
		if transaction != nil || !errors.Is(err, primary) || !errors.Is(err, cleanup) ||
			fault.rollbackCalls != 1 || fault.rawCalls != 1 {
			t.Fatalf("rollback-cleanup Begin = transaction:%v error:%v rollback:%d raw:%d", transaction, err, fault.rollbackCalls, fault.rawCalls)
		}
		if err := session.Close(ctx); err != nil {
			t.Fatal(err)
		}
	})
}

func TestSQLiteRelationCreateModelRequiresExactCursorAndRecorderExhaustion(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-cursor")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	target, source, sourceField := sqliteRelationTestModels()
	intent := sqliteRelationApplyIntent(target, source, sourceField)
	transition := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_relation"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}
	session := openSQLiteRelationSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	transaction, err := session.BeginRelationFencedMigration(ctx, transition, intent)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.CreateModel(ctx, source); err == nil {
		t.Fatal("reordered CreateModel succeeded")
	}
	if err := transaction.CreateModel(ctx, target); err == nil {
		t.Fatal("relation transaction retried after mismatch")
	}
	outcome, err := transaction.CommitFenced(ctx)
	if err == nil || outcome.Durability != migrationbackend.CommitRolledBack {
		t.Fatalf("failed cursor CommitFenced() = (%+v, %v)", outcome, err)
	}
	if sqliteRelationTestTableExists(t, backend, target.DBTable) || sqliteRelationTestTableExists(t, backend, source.DBTable) {
		t.Fatal("cursor mismatch published relation tables")
	}
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}

	backend2, err := OpenMemory(ctx, "relation-recorder-exhaustion")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend2.Close() })
	session = openSQLiteRelationSession(t, backend2)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	transaction, err = session.BeginRelationFencedMigration(ctx, transition, intent)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.CreateModel(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := transaction.RecordApplied(ctx, "news", "0001_relation"); err == nil {
		t.Fatal("RecordApplied before cursor exhaustion succeeded")
	}
	outcome, err = transaction.CommitFenced(ctx)
	if err == nil || outcome.Durability != migrationbackend.CommitRolledBack {
		t.Fatalf("unexhausted CommitFenced() = (%+v, %v)", outcome, err)
	}
	if sqliteRelationTestTableExists(t, backend2, target.DBTable) {
		t.Fatal("unexhausted relation transaction preserved partial DDL")
	}
}

func TestSQLiteRelationMixedScalarFieldRoundTripAndReapply(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-mixed-scalar-round-trip")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })

	target, source, sourceField := sqliteRelationTestModels()
	extra := ir.Field{Name: "published", GoName: "Published", Column: "published", Kind: ir.FieldBoolean}
	sourceAfterAdd := source.Clone()
	sourceAfterAdd.Fields = append(sourceAfterAdd.Fields, extra)
	applyIntent := migrationbackend.RelationMigrationIntent{Operations: []migrationbackend.RelationMigrationOperation{
		{OperationIndex: 0, Kind: migrationbackend.RelationMigrationCreateModel, After: target},
		{
			OperationIndex: 1,
			Kind:           migrationbackend.RelationMigrationCreateModel,
			After:          source,
			Targets: []migrationbackend.RelationMigrationTarget{{
				SourceField: sourceField,
				TargetModel: target,
				TargetKey:   target.Fields[0],
			}},
		},
		{OperationIndex: 2, Kind: migrationbackend.RelationMigrationAddField, Before: source, After: sourceAfterAdd},
	}}
	unapplyIntent := migrationbackend.RelationMigrationIntent{Operations: []migrationbackend.RelationMigrationOperation{
		{OperationIndex: 2, Kind: migrationbackend.RelationMigrationRemoveField, Before: sourceAfterAdd, After: source},
		{
			OperationIndex: 1,
			Kind:           migrationbackend.RelationMigrationDeleteModel,
			Before:         source,
			Targets: []migrationbackend.RelationMigrationTarget{{
				SourceField: sourceField,
				TargetModel: target,
				TargetKey:   target.Fields[0],
			}},
		},
		{OperationIndex: 0, Kind: migrationbackend.RelationMigrationDeleteModel, Before: target},
	}}
	migration := migrationbackend.AppliedMigration{App: "news", Name: "0001_mixed_relation"}

	apply := func() {
		t.Helper()
		session := openSQLiteRelationSession(t, backend)
		if _, err := session.ReadAppliedMigrations(ctx); err != nil {
			t.Fatal(err)
		}
		transaction, err := session.BeginRelationFencedMigration(ctx, migrationbackend.HistoryTransition{
			Migration: migration,
			Kind:      migrationbackend.HistoryTransitionApply,
		}, applyIntent)
		if err != nil {
			t.Fatalf("BeginRelationFencedMigration(apply): %v", err)
		}
		if err := transaction.CreateModel(ctx, target); err != nil {
			t.Fatal(err)
		}
		if err := transaction.CreateModel(ctx, source); err != nil {
			t.Fatal(err)
		}
		if err := transaction.AddField(ctx, source, extra); err != nil {
			t.Fatalf("AddField(created relation table): %v", err)
		}
		if err := transaction.RecordApplied(ctx, migration.App, migration.Name); err != nil {
			t.Fatal(err)
		}
		outcome, err := transaction.CommitFenced(ctx)
		if err != nil || outcome.Durability != migrationbackend.CommitCommitted {
			t.Fatalf("CommitFenced(apply) = (%+v, %v)", outcome, err)
		}
		if err := session.Close(ctx); err != nil {
			t.Fatal(err)
		}
	}
	unapply := func() {
		t.Helper()
		session := openSQLiteRelationSession(t, backend)
		if _, err := session.ReadAppliedMigrations(ctx); err != nil {
			t.Fatal(err)
		}
		transaction, err := session.BeginRelationFencedMigration(ctx, migrationbackend.HistoryTransition{
			Migration: migration,
			Kind:      migrationbackend.HistoryTransitionUnapply,
		}, unapplyIntent)
		if err != nil {
			t.Fatalf("BeginRelationFencedMigration(unapply): %v", err)
		}
		if err := transaction.RemoveField(ctx, sourceAfterAdd, extra); err != nil {
			t.Fatalf("RemoveField(relation table): %v", err)
		}
		if err := transaction.DeleteModel(ctx, source); err != nil {
			t.Fatal(err)
		}
		if err := transaction.DeleteModel(ctx, target); err != nil {
			t.Fatal(err)
		}
		if err := transaction.RecordUnapplied(ctx, migration.App, migration.Name); err != nil {
			t.Fatal(err)
		}
		outcome, err := transaction.CommitFenced(ctx)
		if err != nil || outcome.Durability != migrationbackend.CommitCommitted {
			t.Fatalf("CommitFenced(unapply) = (%+v, %v)", outcome, err)
		}
		if err := session.Close(ctx); err != nil {
			t.Fatal(err)
		}
	}

	apply()
	if _, err := backend.ExecContext(ctx, `INSERT INTO "news_author" ("name") VALUES ('Ada')`); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.ExecContext(ctx, `INSERT INTO "news_article" ("title", "author_id", "published") VALUES ('sealed', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.ExecContext(ctx, `DELETE FROM "news_article"`); err != nil {
		t.Fatal(err)
	}
	unapply()
	apply()
	if !sqliteRelationTestTableExists(t, backend, target.DBTable) || !sqliteRelationTestTableExists(t, backend, source.DBTable) {
		t.Fatal("reapply did not restore the complete mixed relation step")
	}
}

func TestSQLiteRelationIntentStaticBoundaryIsClosedAndDeterministic(t *testing.T) {
	target, source, sourceField := sqliteRelationTestModels()
	transition := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_relation"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}
	valid := sqliteRelationApplyIntent(target, source, sourceField)

	t.Run("scalar_only_uses_sqlite_feature", func(t *testing.T) {
		_, err := validateAndSealSQLiteRelationIntent(transition, migrationbackend.RelationMigrationIntent{Operations: []migrationbackend.RelationMigrationOperation{{
			OperationIndex: 0,
			Kind:           migrationbackend.RelationMigrationCreateModel,
			After:          target,
		}}})
		assertSQLiteRelationCapabilityFeature(t, err, "sqlite_relation_migration")
	})

	t.Run("target_bearing_add_is_unsupported", func(t *testing.T) {
		before := target.Clone()
		relationField := sourceField.Clone()
		relationField.Relation.Target.ModelName = target.Name
		after := before.Clone()
		after.Fields = append(after.Fields, relationField)
		_, err := validateAndSealSQLiteRelationIntent(transition, migrationbackend.RelationMigrationIntent{Operations: []migrationbackend.RelationMigrationOperation{{
			OperationIndex: 0,
			Kind:           migrationbackend.RelationMigrationAddField,
			Before:         before,
			After:          after,
			Targets: []migrationbackend.RelationMigrationTarget{{
				SourceField: relationField,
				TargetModel: target,
				TargetKey:   target.Fields[0],
			}},
		}}})
		assertSQLiteRelationCapabilityFeature(t, err, "sqlite_relation_migration")
	})

	t.Run("wrong_direction_is_integrity_before_session", func(t *testing.T) {
		single := migrationbackend.RelationMigrationIntent{Operations: []migrationbackend.RelationMigrationOperation{valid.Operations[1]}}
		single.Operations[0].OperationIndex = 0
		wrongDirection := transition
		wrongDirection.Kind = migrationbackend.HistoryTransitionUnapply
		_, err := validateAndSealSQLiteRelationIntent(wrongDirection, single)
		if err == nil || migrationbackend.IsCapabilityError(err) || !strings.Contains(err.Error(), "does not match history transition") {
			t.Fatalf("wrong-direction validation error = %v", err)
		}
	})

	t.Run("duplicate_target_metadata_is_integrity", func(t *testing.T) {
		forged := cloneSQLiteRelationIntent(valid)
		forged.Operations[1].Targets = append(forged.Operations[1].Targets, forged.Operations[1].Targets[0])
		_, err := validateAndSealSQLiteRelationIntent(transition, forged)
		if err == nil || !strings.Contains(err.Error(), "exact field order") {
			t.Fatalf("duplicate target validation error = %v", err)
		}
	})

	t.Run("non_nil_empty_zero_sentinel_is_rejected_before_clone", func(t *testing.T) {
		forged := cloneSQLiteRelationIntent(valid)
		forged.Operations[0].Before = ir.Model{Fields: []ir.Field{}}
		_, err := validateAndSealSQLiteRelationIntent(transition, forged)
		if err == nil || !strings.Contains(err.Error(), "non-zero Before") {
			t.Fatalf("non-nil empty zero-sentinel validation error = %v", err)
		}
	})

	t.Run("discontinuous_scalar_delta_is_integrity", func(t *testing.T) {
		extra := ir.Field{Name: "published", GoName: "Published", Column: "published", Kind: ir.FieldBoolean}
		before := source.Clone()
		before.Fields[1].MaxLength++
		after := before.Clone()
		after.Fields = append(after.Fields, extra)
		forged := cloneSQLiteRelationIntent(valid)
		forged.Operations = append(forged.Operations, migrationbackend.RelationMigrationOperation{
			OperationIndex: 2,
			Kind:           migrationbackend.RelationMigrationAddField,
			Before:         before,
			After:          after,
		})
		_, err := validateAndSealSQLiteRelationIntent(transition, forged)
		if err == nil || !strings.Contains(err.Error(), "discontinuous") {
			t.Fatalf("discontinuous validation error = %v", err)
		}
	})

	t.Run("external_relation_target_requires_nested_authority", func(t *testing.T) {
		external := source.Clone()
		field := sourceField.Clone()
		field.Relation.Target = ir.ModelIdentity{AppLabel: "accounts", ModelName: external.Name}
		child := ir.Model{
			Name: "comment", GoName: "Comment", DBTable: "news_comment",
			Fields: []ir.Field{
				{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
				field,
			},
		}
		_, err := validateAndSealSQLiteRelationIntent(transition, migrationbackend.RelationMigrationIntent{Operations: []migrationbackend.RelationMigrationOperation{{
			OperationIndex: 0,
			Kind:           migrationbackend.RelationMigrationCreateModel,
			After:          child,
			Targets: []migrationbackend.RelationMigrationTarget{{
				SourceField: field,
				TargetModel: external,
				TargetKey:   external.Fields[0],
			}},
		}}})
		assertSQLiteRelationCapabilityFeature(t, err, "sqlite_relation_migration")
	})

	t.Run("cross_app_same_model_name_uses_full_identity", func(t *testing.T) {
		localTarget := target.Clone()
		localTarget.DBTable = "news_local_author"
		externalTarget := target.Clone()
		externalTarget.DBTable = "accounts_author"
		field := sourceField.Clone()
		field.Relation.Target.AppLabel = "accounts"
		crossAppSource := source.Clone()
		crossAppSource.Fields[2] = field
		intent := migrationbackend.RelationMigrationIntent{Operations: []migrationbackend.RelationMigrationOperation{
			{OperationIndex: 0, Kind: migrationbackend.RelationMigrationCreateModel, After: localTarget},
			{
				OperationIndex: 1,
				Kind:           migrationbackend.RelationMigrationCreateModel,
				After:          crossAppSource,
				Targets: []migrationbackend.RelationMigrationTarget{{
					SourceField: field,
					TargetModel: externalTarget,
					TargetKey:   externalTarget.Fields[0],
				}},
			},
		}}
		if _, err := validateAndSealSQLiteRelationIntent(transition, intent); err != nil {
			t.Fatalf("cross-app same-name relation rejected: %v", err)
		}
		externalTarget.DBTable = localTarget.DBTable
		intent.Operations[1].Targets[0].TargetModel = externalTarget
		_, err := validateAndSealSQLiteRelationIntent(transition, intent)
		if err == nil || !strings.Contains(err.Error(), "collides with local model") {
			t.Fatalf("cross-app table alias validation error = %v", err)
		}
	})

	t.Run("resource_limit_precedes_clone", func(t *testing.T) {
		forged := migrationbackend.RelationMigrationIntent{Operations: make([]migrationbackend.RelationMigrationOperation, sqliteRelationMaxOperations+1)}
		_, err := validateAndSealSQLiteRelationIntent(transition, forged)
		if err == nil || !strings.Contains(err.Error(), "maximum") {
			t.Fatalf("resource validation error = %v", err)
		}
	})
}

func TestSQLiteRelationLaterTargetFieldCannotCollideWithRegisteredReverseBeforeClaim(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-later-reverse-collision")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	target, source, sourceField := sqliteRelationTestModels()
	targetAfter := target.Clone()
	targetAfter.Fields = append(targetAfter.Fields, ir.Field{
		Name: "articles", GoName: "Articles", Column: "articles", Kind: ir.FieldBoolean,
	})
	intent := sqliteRelationApplyIntent(target, source, sourceField)
	intent.Operations = append(intent.Operations, migrationbackend.RelationMigrationOperation{
		OperationIndex: 2,
		Kind:           migrationbackend.RelationMigrationAddField,
		Before:         target,
		After:          targetAfter,
	})

	session := openSQLiteRelationSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	concrete := session.(*sqliteRevisionFencedSession)
	var checkpoints []sqliteRelationBeginCheckpoint
	concrete.relationBeginCheckpoint = func(checkpoint sqliteRelationBeginCheckpoint) {
		checkpoints = append(checkpoints, checkpoint)
	}
	transaction, err := session.BeginRelationFencedMigration(ctx, migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_reverse_collision"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}, intent)
	if transaction != nil || err == nil || !strings.Contains(err.Error(), "collides with reverse name") {
		t.Fatalf("BeginRelationFencedMigration() = (%v, %v), want ordered reverse collision", transaction, err)
	}
	if len(checkpoints) != 0 || concrete.active != nil || concrete.state != revisionSessionReady {
		t.Fatalf("reverse collision crossed connection boundary: checkpoints=%v active=%v state=%d", checkpoints, concrete.active, concrete.state)
	}
	if sqliteRelationTestTableExists(t, backend, target.DBTable) || sqliteRelationTestTableExists(t, backend, source.DBTable) {
		t.Fatal("reverse collision published relation tables")
	}
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteRelationInitialReverseCollisionCannotBeRemovedBeforeDelete(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-initial-reverse-collision")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	migration := migrationbackend.AppliedMigration{App: "news", Name: "0001_initial_reverse_collision"}
	seedSQLiteMigrationHistory(t, ctx, backend, migration)
	if _, err := backend.ExecContext(ctx, `CREATE TABLE "reverse_collision_marker" ("id" INTEGER)`); err != nil {
		t.Fatal(err)
	}

	targetAfter, source, sourceField := sqliteRelationTestModels()
	targetBefore := targetAfter.Clone()
	targetBefore.Fields = append(targetBefore.Fields, ir.Field{
		Name: "articles", GoName: "Articles", Column: "articles", Kind: ir.FieldBoolean,
	})
	intent := migrationbackend.RelationMigrationIntent{Operations: []migrationbackend.RelationMigrationOperation{
		{
			OperationIndex: 1,
			Kind:           migrationbackend.RelationMigrationRemoveField,
			Before:         targetBefore,
			After:          targetAfter,
		},
		{
			OperationIndex: 0,
			Kind:           migrationbackend.RelationMigrationDeleteModel,
			Before:         source,
			Targets: []migrationbackend.RelationMigrationTarget{{
				SourceField: sourceField,
				TargetModel: targetAfter,
				TargetKey:   targetAfter.Fields[0],
			}},
		},
	}}
	session := openSQLiteRelationSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	concrete := session.(*sqliteRevisionFencedSession)
	var checkpoints []sqliteRelationBeginCheckpoint
	concrete.relationBeginCheckpoint = func(checkpoint sqliteRelationBeginCheckpoint) {
		checkpoints = append(checkpoints, checkpoint)
	}
	connectionCalls := 0
	concrete.relationConnectionHook = func(connection migrationPinnedConnection) migrationPinnedConnection {
		connectionCalls++
		return connection
	}
	transaction, err := session.BeginRelationFencedMigration(ctx, migrationbackend.HistoryTransition{
		Migration: migration,
		Kind:      migrationbackend.HistoryTransitionUnapply,
	}, intent)
	if transaction != nil || err == nil || !strings.Contains(err.Error(), "collides with reverse name") {
		t.Fatalf("BeginRelationFencedMigration() = (%v, %v), want initial reverse collision", transaction, err)
	}
	if connectionCalls != 0 || len(checkpoints) != 0 || concrete.active != nil || concrete.state != revisionSessionReady {
		t.Fatalf(
			"initial reverse collision crossed connection boundary: connections=%d checkpoints=%v active=%v state=%d",
			connectionCalls,
			checkpoints,
			concrete.active,
			concrete.state,
		)
	}
	if !sqliteRelationTestTableExists(t, backend, "reverse_collision_marker") {
		t.Fatal("initial reverse collision changed the marker schema")
	}
	snapshot, err := readAtomicMigrationRevisionSnapshot(ctx, backend)
	if err != nil || !reflect.DeepEqual(snapshot.records, []migrationbackend.AppliedMigration{migration}) {
		t.Fatalf("history after initial reverse collision = (%+v, %v)", snapshot, err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteRelationIdentifierFoldingMatchesSQLiteASCIIOnly(t *testing.T) {
	if sqliteRelationIdentifierKey("News_Article") != sqliteRelationIdentifierKey("news_article") {
		t.Fatal("ASCII identifier case was not folded")
	}
	if sqliteRelationIdentifierKey("K") == sqliteRelationIdentifierKey("k") {
		t.Fatal("non-ASCII Kelvin sign was incorrectly folded to ASCII k")
	}
}

func TestSQLiteRelationNonASCIITempDecoyDoesNotShadowASCIIIdentifier(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-nonascii-decoy")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	if _, err := backend.ExecContext(ctx, `CREATE TEMP TABLE "K" ("id" INTEGER)`); err != nil {
		t.Fatal(err)
	}
	target, source, sourceField := sqliteRelationTestModels()
	source.DBTable = "k"
	session := openSQLiteRelationSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	transaction, err := session.BeginRelationFencedMigration(ctx, migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_nonascii"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}, sqliteRelationApplyIntent(target, source, sourceField))
	if err != nil {
		t.Fatalf("non-ASCII TEMP decoy falsely shadowed ASCII table: %v", err)
	}
	if err := transaction.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteRelationUnrelatedLegacyForeignKeyCycleDoesNotBlock(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-unrelated-cycle")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	for _, statement := range []string{
		`CREATE TABLE "legacy_a" ("id" INTEGER PRIMARY KEY, "b_id" INTEGER, FOREIGN KEY ("b_id") REFERENCES "legacy_b" ("id"))`,
		`CREATE TABLE "legacy_b" ("id" INTEGER PRIMARY KEY, "a_id" INTEGER, FOREIGN KEY ("a_id") REFERENCES "legacy_a" ("id"))`,
		`CREATE TABLE "reference_decoy" ("references" "news_article", "note" TEXT DEFAULT 'REFERENCES news_article' /* REFERENCES news_article */)`,
		`CREATE VIEW "literal_decoy" AS SELECT 'unrelated_literal' AS "value"`,
	} {
		if _, err := backend.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	target, source, sourceField := sqliteRelationTestModels()
	session := openSQLiteRelationSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	transaction, err := session.BeginRelationFencedMigration(ctx, migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_relation"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}, sqliteRelationApplyIntent(target, source, sourceField))
	if err != nil {
		t.Fatalf("unrelated legacy cycle blocked relation Begin: %v", err)
	}
	if err := transaction.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteRelationExternalTargetWithHarmlessSchemaObjects(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-external-target")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })

	target, source, sourceField := sqliteRelationTestModels()
	sourceField.Relation.Target.AppLabel = "accounts"
	source.Fields[2] = sourceField
	targetSQL, err := compileMigrationCreateModel(target)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		targetSQL,
		`CREATE TABLE "external_audit" ("id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT)`,
		`CREATE INDEX "external_author_name" ON "news_author" ("name")`,
		`CREATE TRIGGER "news_article" AFTER INSERT ON "news_author" BEGIN INSERT INTO "external_audit" ("id") VALUES (NULL); END`,
	} {
		if _, err := backend.ExecContext(ctx, statement); err != nil {
			t.Fatalf("setup %q: %v", statement, err)
		}
	}

	intent := migrationbackend.RelationMigrationIntent{Operations: []migrationbackend.RelationMigrationOperation{{
		OperationIndex: 0,
		Kind:           migrationbackend.RelationMigrationCreateModel,
		After:          source,
		Targets: []migrationbackend.RelationMigrationTarget{{
			SourceField: sourceField,
			TargetModel: target,
			TargetKey:   target.Fields[0],
		}},
	}}}
	transition := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_external_relation"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}
	session := openSQLiteRelationSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	transaction, err := session.BeginRelationFencedMigration(ctx, transition, intent)
	if err != nil {
		t.Fatalf("BeginRelationFencedMigration(): %v", err)
	}
	if err := transaction.CreateModel(ctx, source); err != nil {
		t.Fatal(err)
	}
	if err := transaction.RecordApplied(ctx, transition.Migration.App, transition.Migration.Name); err != nil {
		t.Fatal(err)
	}
	outcome, err := transaction.CommitFenced(ctx)
	if err != nil || outcome.Durability != migrationbackend.CommitCommitted {
		t.Fatalf("CommitFenced() = (%+v, %v)", outcome, err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteRelationCompilerAlwaysUsesNoAction(t *testing.T) {
	target, source, sourceField := sqliteRelationTestModels()
	sourceField.Nullable = true
	sourceField.Relation.OnDelete = ir.DeleteSetNull
	source.Fields[2] = sourceField
	statement, err := compileSQLiteRelationCreateModel(source, []migrationbackend.RelationMigrationTarget{{
		SourceField: sourceField,
		TargetModel: target,
		TargetKey:   target.Fields[0],
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(statement, `ON DELETE NO ACTION`) || strings.Contains(statement, `ON DELETE SET NULL`) {
		t.Fatalf("relation CREATE SQL = %q, want exact NO ACTION enforcement", statement)
	}
}

func TestSQLiteRelationBeginCheckpointOrderAndPostClaimCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	backend, err := OpenMemory(ctx, "relation-begin-checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	target, source, sourceField := sqliteRelationTestModels()
	session := openSQLiteRelationSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	concrete := session.(*sqliteRevisionFencedSession)
	var checkpoints []sqliteRelationBeginCheckpoint
	concrete.relationBeginCheckpoint = func(checkpoint sqliteRelationBeginCheckpoint) {
		checkpoints = append(checkpoints, checkpoint)
		if checkpoint == sqliteRelationCheckpointRevisionClaimed {
			cancel()
		}
	}
	transaction, err := session.BeginRelationFencedMigration(ctx, migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_cancel_after_claim"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}, sqliteRelationApplyIntent(target, source, sourceField))
	if transaction != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("BeginRelationFencedMigration() = (%v, %v), want nil/context.Canceled", transaction, err)
	}
	want := []sqliteRelationBeginCheckpoint{
		sqliteRelationCheckpointForeignKeysSet,
		sqliteRelationCheckpointForeignKeysRead,
		sqliteRelationCheckpointTransactionBegun,
		sqliteRelationCheckpointPhysicalPreflightComplete,
		sqliteRelationCheckpointRevisionClaimStarting,
		sqliteRelationCheckpointRevisionClaimed,
	}
	if !reflect.DeepEqual(checkpoints, want) {
		t.Fatalf("begin checkpoints = %v, want %v", checkpoints, want)
	}
	if concrete.active != nil || concrete.state != revisionSessionPoisoned {
		t.Fatalf("canceled session = active:%v state:%d, want nil/poisoned", concrete.active, concrete.state)
	}
	if sqliteRelationTestTableExists(t, backend, migrationRevisionTable) {
		t.Fatal("post-claim cancellation published revision metadata")
	}
}

func TestSQLiteRelationStaticUnsupportedDoesNotConsumeReadySession(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-static-zero-io")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	target, source, sourceField := sqliteRelationTestModels()
	session := openSQLiteRelationSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	concrete := session.(*sqliteRevisionFencedSession)
	var checkpoints []sqliteRelationBeginCheckpoint
	concrete.relationBeginCheckpoint = func(checkpoint sqliteRelationBeginCheckpoint) {
		checkpoints = append(checkpoints, checkpoint)
	}
	transition := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_relation"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}
	transaction, err := session.BeginRelationFencedMigration(ctx, transition, migrationbackend.RelationMigrationIntent{Operations: []migrationbackend.RelationMigrationOperation{{
		OperationIndex: 0,
		Kind:           migrationbackend.RelationMigrationCreateModel,
		After:          target,
	}}})
	if transaction != nil {
		t.Fatalf("scalar-only Begin returned transaction %v", transaction)
	}
	assertSQLiteRelationCapabilityFeature(t, err, "sqlite_relation_migration")
	if len(checkpoints) != 0 || concrete.state != revisionSessionReady || concrete.active != nil {
		t.Fatalf("static rejection checkpoints/state/active = %v/%d/%v, want empty/ready/nil", checkpoints, concrete.state, concrete.active)
	}
	transaction, err = session.BeginRelationFencedMigration(ctx, transition, sqliteRelationApplyIntent(target, source, sourceField))
	if err != nil {
		t.Fatalf("valid Begin after static rejection: %v", err)
	}
	if err := transaction.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteRelationPhysicalPreflightStopsBeforeRevisionClaim(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, context.Context, *Backend)
	}{
		{
			name: "mixed_case_temp_control_shadow",
			setup: func(t *testing.T, ctx context.Context, backend *Backend) {
				if _, err := backend.ExecContext(ctx, `CREATE TEMP TABLE "GODJ_MIGRATION_REVISION" ("revision" INTEGER)`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "mixed_case_temp_recorder_shadow",
			setup: func(t *testing.T, ctx context.Context, backend *Backend) {
				if _, err := backend.ExecContext(ctx, `CREATE TEMP TABLE "GODJ_MIGRATIONS" ("app" TEXT, "name" TEXT)`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "mixed_case_temp_source_shadow",
			setup: func(t *testing.T, ctx context.Context, backend *Backend) {
				if _, err := backend.ExecContext(ctx, `CREATE TEMP TABLE "NEWS_ARTICLE" ("id" INTEGER)`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "mixed_case_main_control_table_alias",
			setup: func(t *testing.T, ctx context.Context, backend *Backend) {
				if _, err := backend.ExecContext(ctx, `CREATE TABLE "GoDj_Migrations" ("app" TEXT)`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "mixed_case_main_control_view_alias",
			setup: func(t *testing.T, ctx context.Context, backend *Backend) {
				if _, err := backend.ExecContext(ctx, `CREATE VIEW "GoDj_Migration_Revision" AS SELECT 1 AS "revision"`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "mixed_case_main_control_index_alias",
			setup: func(t *testing.T, ctx context.Context, backend *Backend) {
				if _, err := backend.ExecContext(ctx, `CREATE TABLE "index_owner" ("value" INTEGER)`); err != nil {
					t.Fatal(err)
				}
				if _, err := backend.ExecContext(ctx, `CREATE INDEX "GoDj_Migrations" ON "index_owner" ("value")`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "dangling_child_references_future_source",
			setup: func(t *testing.T, ctx context.Context, backend *Backend) {
				if _, err := backend.ExecContext(ctx, `CREATE TABLE "dangling_child" ("article_id" INTEGER, FOREIGN KEY ("article_id") REFERENCES "news_article" ("id"))`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "commented_dangling_child_references_future_source",
			setup: func(t *testing.T, ctx context.Context, backend *Backend) {
				if _, err := backend.ExecContext(ctx, `CREATE TABLE "commented_dangling_child" (`+
					`"article_id" INTEGER, FOREIGN KEY ("article_id") `+
					`REFERENCES /* bounded decoy */ "news_article" ("id"))`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "view_references_future_source",
			setup: func(t *testing.T, ctx context.Context, backend *Backend) {
				if _, err := backend.ExecContext(ctx, `CREATE VIEW "future_article_view" AS SELECT * FROM "news_article"`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "single_quoted_view_references_future_source",
			setup: func(t *testing.T, ctx context.Context, backend *Backend) {
				if _, err := backend.ExecContext(ctx, `CREATE VIEW "single_quoted_future_article_view" AS SELECT * FROM 'news_article'`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "comma_join_single_quoted_view_references_future_source",
			setup: func(t *testing.T, ctx context.Context, backend *Backend) {
				if _, err := backend.ExecContext(ctx, `CREATE TABLE "view_other" ("id" INTEGER)`); err != nil {
					t.Fatal(err)
				}
				if _, err := backend.ExecContext(ctx, `CREATE VIEW "comma_future_article_view" AS `+
					`SELECT 1 FROM "view_other", 'news_article'`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "single_quoted_schema_view_references_future_source",
			setup: func(t *testing.T, ctx context.Context, backend *Backend) {
				if _, err := backend.ExecContext(ctx, `CREATE VIEW "schema_future_article_view" AS `+
					`SELECT * FROM 'main'.'news_article'`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "join_on_then_comma_single_quoted_view_references_future_source",
			setup: func(t *testing.T, ctx context.Context, backend *Backend) {
				for _, statement := range []string{
					`CREATE TABLE "join_left" ("id" INTEGER)`,
					`CREATE TABLE "join_right" ("id" INTEGER)`,
				} {
					if _, err := backend.ExecContext(ctx, statement); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := backend.ExecContext(ctx, `CREATE VIEW "join_comma_future_article_view" AS `+
					`SELECT 1 FROM "join_left" JOIN "join_right" ON 1 = 1, 'news_article'`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "parenthesized_single_quoted_view_references_future_source",
			setup: func(t *testing.T, ctx context.Context, backend *Backend) {
				if _, err := backend.ExecContext(ctx, `CREATE VIEW "parenthesized_future_article_view" AS `+
					`SELECT * FROM ('news_article')`); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			backend, err := OpenMemory(ctx, "relation-preflight-"+test.name)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = backend.Close() })
			target, source, sourceField := sqliteRelationTestModels()
			session := openSQLiteRelationSession(t, backend)
			if _, err := session.ReadAppliedMigrations(ctx); err != nil {
				t.Fatal(err)
			}
			test.setup(t, ctx, backend)
			concrete := session.(*sqliteRevisionFencedSession)
			var checkpoints []sqliteRelationBeginCheckpoint
			concrete.relationBeginCheckpoint = func(checkpoint sqliteRelationBeginCheckpoint) {
				checkpoints = append(checkpoints, checkpoint)
			}
			transaction, err := session.BeginRelationFencedMigration(ctx, migrationbackend.HistoryTransition{
				Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_relation"},
				Kind:      migrationbackend.HistoryTransitionApply,
			}, sqliteRelationApplyIntent(target, source, sourceField))
			if transaction != nil || err == nil {
				t.Fatalf("BeginRelationFencedMigration() = (%v, %v), want physical preflight failure", transaction, err)
			}
			for _, checkpoint := range checkpoints {
				if checkpoint == sqliteRelationCheckpointRevisionClaimStarting || checkpoint == sqliteRelationCheckpointRevisionClaimed {
					t.Fatalf("physical preflight reached revision claim: checkpoints=%v", checkpoints)
				}
			}
			if concrete.active != nil || concrete.state != revisionSessionPoisoned {
				t.Fatalf("failed session = active:%v state:%d, want nil/poisoned", concrete.active, concrete.state)
			}
		})
	}
}

func TestSQLiteRelationExternalTargetRequiresExactAutoIncrementShape(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-external-target-autoincrement")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	target, source, sourceField := sqliteRelationTestModels()
	sourceField.Relation.Target.AppLabel = "accounts"
	source.Fields[2] = sourceField
	if _, err := backend.ExecContext(ctx, `CREATE TABLE "news_author" ("id" INTEGER NOT NULL PRIMARY KEY, "name" VARCHAR(120) NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	session := openSQLiteRelationSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	concrete := session.(*sqliteRevisionFencedSession)
	var checkpoints []sqliteRelationBeginCheckpoint
	concrete.relationBeginCheckpoint = func(checkpoint sqliteRelationBeginCheckpoint) {
		checkpoints = append(checkpoints, checkpoint)
	}
	transaction, err := session.BeginRelationFencedMigration(ctx, migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_external"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}, migrationbackend.RelationMigrationIntent{Operations: []migrationbackend.RelationMigrationOperation{{
		OperationIndex: 0,
		Kind:           migrationbackend.RelationMigrationCreateModel,
		After:          source,
		Targets: []migrationbackend.RelationMigrationTarget{{
			SourceField: sourceField,
			TargetModel: target,
			TargetKey:   target.Fields[0],
		}},
	}}})
	if transaction != nil || err == nil || !strings.Contains(err.Error(), "canonical declaration") {
		t.Fatalf("BeginRelationFencedMigration() = (%v, %v), want AUTOINCREMENT drift failure", transaction, err)
	}
	assertSQLiteRelationNoClaimCheckpoint(t, checkpoints)
}

func TestSQLiteRelationCreateRejectsOrphanSequenceBeforeClaim(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-orphan-sequence")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	if _, err := backend.ExecContext(ctx, `CREATE TABLE "sequence_carrier" ("id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.ExecContext(ctx, `INSERT INTO main.sqlite_sequence ("name", "seq") VALUES ('news_author', 7)`); err != nil {
		t.Fatal(err)
	}
	target, source, sourceField := sqliteRelationTestModels()
	session := openSQLiteRelationSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	concrete := session.(*sqliteRevisionFencedSession)
	var checkpoints []sqliteRelationBeginCheckpoint
	concrete.relationBeginCheckpoint = func(checkpoint sqliteRelationBeginCheckpoint) {
		checkpoints = append(checkpoints, checkpoint)
	}
	transaction, err := session.BeginRelationFencedMigration(ctx, migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_relation"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}, sqliteRelationApplyIntent(target, source, sourceField))
	if transaction != nil || err == nil || !strings.Contains(err.Error(), "orphan row") {
		t.Fatalf("BeginRelationFencedMigration() = (%v, %v), want orphan sequence failure", transaction, err)
	}
	assertSQLiteRelationNoClaimCheckpoint(t, checkpoints)
}

func TestSQLiteRelationNonEmptyScalarAddFailsBeforeClaim(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-nonempty-scalar-add")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	profile := ir.Model{
		Name: "profile", GoName: "Profile", DBTable: "news_profile",
		Fields: []ir.Field{
			{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
			{Name: "name", GoName: "Name", Column: "name", Kind: ir.FieldChar, MaxLength: 80},
		},
	}
	profileSQL, err := compileMigrationCreateModel(profile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.ExecContext(ctx, profileSQL); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.ExecContext(ctx, `INSERT INTO "news_profile" ("name") VALUES ('occupied')`); err != nil {
		t.Fatal(err)
	}
	extra := ir.Field{Name: "active", GoName: "Active", Column: "active", Kind: ir.FieldBoolean}
	profileAfter := profile.Clone()
	profileAfter.Fields = append(profileAfter.Fields, extra)
	target, source, sourceField := sqliteRelationTestModels()
	intent := sqliteRelationApplyIntent(target, source, sourceField)
	intent.Operations = append([]migrationbackend.RelationMigrationOperation{{
		OperationIndex: 0,
		Kind:           migrationbackend.RelationMigrationAddField,
		Before:         profile,
		After:          profileAfter,
	}}, intent.Operations...)
	intent.Operations[1].OperationIndex = 1
	intent.Operations[2].OperationIndex = 2

	session := openSQLiteRelationSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	concrete := session.(*sqliteRevisionFencedSession)
	var checkpoints []sqliteRelationBeginCheckpoint
	concrete.relationBeginCheckpoint = func(checkpoint sqliteRelationBeginCheckpoint) {
		checkpoints = append(checkpoints, checkpoint)
	}
	transaction, err := session.BeginRelationFencedMigration(ctx, migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_relation"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}, intent)
	if transaction != nil || err == nil {
		t.Fatalf("BeginRelationFencedMigration() = (%v, %v), want nonempty AddField failure", transaction, err)
	}
	assertSQLiteRelationCapabilityFeature(t, err, "sqlite_add_field")
	assertSQLiteRelationNoClaimCheckpoint(t, checkpoints)
	var columns int
	if err := backend.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('news_profile') WHERE "name" = 'active'`).Scan(&columns); err != nil || columns != 0 {
		t.Fatalf("active column count = (%d, %v), want 0", columns, err)
	}
}

func TestSQLiteRelationDeletePreflightRejectsUnsealedSchemaHazards(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, context.Context, *Backend, ir.Model, ir.Model)
	}{
		{
			name: "inbound_foreign_key",
			setup: func(t *testing.T, ctx context.Context, backend *Backend, target, _ ir.Model) {
				if _, err := backend.ExecContext(ctx, `CREATE TABLE "outside_child" ("author_id" INTEGER, FOREIGN KEY ("author_id") REFERENCES "news_author" ("id"))`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "user_index",
			setup: func(t *testing.T, ctx context.Context, backend *Backend, _, source ir.Model) {
				if _, err := backend.ExecContext(ctx, `CREATE INDEX "article_title_custom" ON "news_article" ("title")`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "user_trigger",
			setup: func(t *testing.T, ctx context.Context, backend *Backend, _, source ir.Model) {
				if _, err := backend.ExecContext(ctx, `CREATE TABLE "delete_audit" ("id" INTEGER)`); err != nil {
					t.Fatal(err)
				}
				if _, err := backend.ExecContext(ctx, `CREATE TRIGGER "article_delete_audit" AFTER DELETE ON "news_article" BEGIN INSERT INTO "delete_audit" ("id") VALUES (1); END`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "dependent_view",
			setup: func(t *testing.T, ctx context.Context, backend *Backend, _, source ir.Model) {
				if _, err := backend.ExecContext(ctx, `CREATE VIEW "article_view" AS SELECT "title" FROM "news_article"`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "trigger_when_subquery",
			setup: func(t *testing.T, ctx context.Context, backend *Backend, _, source ir.Model) {
				if _, err := backend.ExecContext(ctx, `CREATE TABLE "trigger_owner" ("id" INTEGER)`); err != nil {
					t.Fatal(err)
				}
				if _, err := backend.ExecContext(ctx, `CREATE TRIGGER "article_when_dependency" AFTER INSERT ON "trigger_owner" `+
					`WHEN EXISTS (SELECT 1 FROM "news_article") BEGIN SELECT 1; END`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "trigger_single_quoted_dml_target",
			setup: func(t *testing.T, ctx context.Context, backend *Backend, _, source ir.Model) {
				if _, err := backend.ExecContext(ctx, `CREATE TABLE "trigger_owner_literal" ("id" INTEGER)`); err != nil {
					t.Fatal(err)
				}
				if _, err := backend.ExecContext(ctx, `CREATE TRIGGER "article_literal_dependency" AFTER INSERT ON "trigger_owner_literal" `+
					`BEGIN INSERT INTO 'news_article' ("title", "author_id") VALUES ('literal', 1); END`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "trigger_when_comma_join_single_quoted_target",
			setup: func(t *testing.T, ctx context.Context, backend *Backend, _, source ir.Model) {
				for _, statement := range []string{
					`CREATE TABLE "trigger_owner_comma" ("id" INTEGER)`,
					`CREATE TABLE "trigger_other" ("id" INTEGER)`,
				} {
					if _, err := backend.ExecContext(ctx, statement); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := backend.ExecContext(ctx, `CREATE TRIGGER "article_comma_dependency" `+
					`AFTER INSERT ON "trigger_owner_comma" `+
					`WHEN EXISTS (SELECT 1 FROM "trigger_other", 'news_article') BEGIN SELECT 1; END`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "trigger_update_or_ignore_single_quoted_target",
			setup: func(t *testing.T, ctx context.Context, backend *Backend, _, source ir.Model) {
				if _, err := backend.ExecContext(ctx, `CREATE TABLE "trigger_owner_update" ("id" INTEGER)`); err != nil {
					t.Fatal(err)
				}
				if _, err := backend.ExecContext(ctx, `CREATE TRIGGER "article_update_dependency" `+
					`AFTER INSERT ON "trigger_owner_update" BEGIN `+
					`UPDATE OR IGNORE 'news_article' SET "title" = "title"; END`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "trigger_unquoted_unicode_owner_with_touched_body",
			setup: func(t *testing.T, ctx context.Context, backend *Backend, _, source ir.Model) {
				if _, err := backend.ExecContext(ctx, `CREATE TABLE café ("id" INTEGER)`); err != nil {
					t.Fatal(err)
				}
				if _, err := backend.ExecContext(ctx, `CREATE TRIGGER "unicode_owner_dependency" `+
					`AFTER INSERT ON café BEGIN `+
					`SELECT * FROM "news_article"; SELECT * FROM "café"; END`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "virtual_table_content_dependency",
			setup: func(t *testing.T, ctx context.Context, backend *Backend, _, source ir.Model) {
				if _, err := backend.ExecContext(ctx, `CREATE  VIRTUAL TABLE "article_search" USING fts5("title", content='news_article')`); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			backend, err := OpenMemory(ctx, "relation-delete-hazard-"+test.name)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = backend.Close() })
			target, source, sourceField := sqliteRelationTestModels()
			migration := migrationbackend.AppliedMigration{App: "news", Name: "0001_relation"}
			seedSQLiteRelationPhysicalSchemaAndHistory(t, ctx, backend, migration, target, source, sourceField)
			test.setup(t, ctx, backend, target, source)

			session := openSQLiteRelationSession(t, backend)
			if _, err := session.ReadAppliedMigrations(ctx); err != nil {
				t.Fatal(err)
			}
			concrete := session.(*sqliteRevisionFencedSession)
			var checkpoints []sqliteRelationBeginCheckpoint
			concrete.relationBeginCheckpoint = func(checkpoint sqliteRelationBeginCheckpoint) {
				checkpoints = append(checkpoints, checkpoint)
			}
			transaction, err := session.BeginRelationFencedMigration(ctx, migrationbackend.HistoryTransition{
				Migration: migration,
				Kind:      migrationbackend.HistoryTransitionUnapply,
			}, sqliteRelationUnapplyIntent(target, source, sourceField))
			if transaction != nil || err == nil {
				t.Fatalf("BeginRelationFencedMigration() = (%v, %v), want closed preflight failure", transaction, err)
			}
			assertSQLiteRelationNoClaimCheckpoint(t, checkpoints)
			if !sqliteRelationTestTableExists(t, backend, target.DBTable) || !sqliteRelationTestTableExists(t, backend, source.DBTable) {
				t.Fatal("failed destructive preflight changed relation tables")
			}
		})
	}
}

func TestSQLiteRelationControlInboundForeignKeyFailsBeforeClaimWithoutCascade(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-control-inbound")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	bootstrap := migrationbackend.AppliedMigration{App: "bootstrap", Name: "0001"}
	seedSQLiteMigrationHistory(t, ctx, backend, bootstrap)
	if _, err := backend.ExecContext(ctx, `CREATE TABLE "control_child" (`+
		`"app" VARCHAR(255) NOT NULL, "name" VARCHAR(255) NOT NULL, `+
		`FOREIGN KEY ("app", "name") REFERENCES "godj_migrations" ("app", "name") ON DELETE CASCADE)`); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.ExecContext(ctx, `INSERT INTO "control_child" ("app", "name") VALUES (?, ?)`, bootstrap.App, bootstrap.Name); err != nil {
		t.Fatal(err)
	}

	target, source, sourceField := sqliteRelationTestModels()
	session := openSQLiteRelationSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	concrete := session.(*sqliteRevisionFencedSession)
	var checkpoints []sqliteRelationBeginCheckpoint
	concrete.relationBeginCheckpoint = func(checkpoint sqliteRelationBeginCheckpoint) {
		checkpoints = append(checkpoints, checkpoint)
	}
	transaction, err := session.BeginRelationFencedMigration(ctx, migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_relation"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}, sqliteRelationApplyIntent(target, source, sourceField))
	if transaction != nil || err == nil {
		t.Fatalf("BeginRelationFencedMigration() = (%v, %v), want control-inbound failure", transaction, err)
	}
	assertSQLiteRelationNoClaimCheckpoint(t, checkpoints)
	var rows int
	if err := backend.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM "control_child"`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("control child rows = (%d, %v), want unchanged 1", rows, err)
	}
	snapshot, err := readAtomicMigrationRevisionSnapshot(ctx, backend)
	if err != nil || !reflect.DeepEqual(snapshot.records, []migrationbackend.AppliedMigration{bootstrap}) {
		t.Fatalf("history after control-inbound failure = (%+v, %v)", snapshot, err)
	}
}

func TestSQLiteRelationForeignKeyCheckRunsBeforeRecorder(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-fk-check-before-recorder")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	target, source, sourceField := sqliteRelationTestModels()
	transition := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_relation"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}
	session := openSQLiteRelationSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	transaction, err := session.BeginRelationFencedMigration(ctx, transition, sqliteRelationApplyIntent(target, source, sourceField))
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.CreateModel(ctx, target); err != nil {
		t.Fatal(err)
	}
	fault := &migrationSQLFault{method: "query", contains: "foreign_key_check", code: 5, remaining: 1}
	installMigrationTransactionFault(transaction, fault)
	if err := transaction.CreateModel(ctx, source); err == nil {
		t.Fatal("last operation succeeded despite foreign_key_check fault")
	}
	if fault.remainingCount() != 0 {
		t.Fatal("foreign_key_check fault was not reached by the last operation")
	}
	if err := transaction.RecordApplied(ctx, transition.Migration.App, transition.Migration.Name); err == nil {
		t.Fatal("recorder ran after last-operation foreign_key_check failure")
	}
	outcome, err := transaction.CommitFenced(ctx)
	if err == nil || outcome.Durability != migrationbackend.CommitRolledBack {
		t.Fatalf("CommitFenced() = (%+v, %v), want rolled back failure", outcome, err)
	}
	if sqliteRelationTestTableExists(t, backend, target.DBTable) || sqliteRelationTestTableExists(t, backend, source.DBTable) {
		t.Fatal("foreign_key_check failure published relation DDL")
	}
}

func TestSQLiteRelationPhysicalValidationCachesRepeatedQueries(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-physical-cache")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })

	var columns strings.Builder
	columns.WriteString(`"id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT`)
	for index := 0; index < 64; index++ {
		fmt.Fprintf(&columns, `, "field_%d" BOOLEAN NOT NULL`, index)
	}
	if _, err := backend.ExecContext(ctx, `CREATE TABLE "cache_parent" (`+columns.String()+`)`); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 64; index++ {
		statement := fmt.Sprintf(`CREATE TABLE "cache_child_%d" (`+
			`"id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, "parent_id" INTEGER, `+
			`FOREIGN KEY ("parent_id") REFERENCES "cache_parent" ("id"))`, index)
		if _, err := backend.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}

	counting := &countingRelationSQLExecutor{migrationSQLExecutor: backend.database}
	cache := newSQLiteRelationPhysicalValidationCache()
	for index := 0; index < 128; index++ {
		if err := cache.assertAutoKey(ctx, counting, "cache_parent", "id"); err != nil {
			t.Fatal(err)
		}
	}
	if counting.queryCalls != 1 {
		t.Fatalf("shared AutoKey validation QueryContext calls = %d, want 1", counting.queryCalls)
	}

	catalog, err := loadSQLiteRelationCatalog(ctx, backend.database)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := buildSQLiteRelationPhysicalGraph(catalog)
	if err != nil {
		t.Fatal(err)
	}
	counting.queryCalls = 0
	dependencyIndex, err := buildSQLiteRelationRemoveDependencyIndex(
		ctx,
		counting,
		catalog,
		graph,
		map[string]struct{}{sqliteRelationIdentifierKey("cache_parent"): {}},
	)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 64; index++ {
		if owner, referenced := dependencyIndex.owner("cache_parent", fmt.Sprintf("field_%d", index)); referenced {
			t.Fatalf("unrelated removed field_%d reported inbound owner %q", index, owner)
		}
	}
	if owner, referenced := dependencyIndex.owner("cache_parent", "id"); !referenced || owner != "cache_child_0" {
		t.Fatalf("referenced parent id owner = (%q, %t), want cache_child_0", owner, referenced)
	}
	if counting.queryCalls != 64 || dependencyIndex.ownerVisits != 64 || dependencyIndex.foreignKeyVisits != 64 {
		t.Fatalf(
			"64x64 Remove dependency work = queries:%d owners:%d foreign-keys:%d, want 64/64/64",
			counting.queryCalls,
			dependencyIndex.ownerVisits,
			dependencyIndex.foreignKeyVisits,
		)
	}
}

func TestSQLiteRelationScalarRemoveRejectsExternalInboundColumnBeforeClaim(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-remove-inbound-column")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })

	target, source, sourceField := sqliteRelationTestModels()
	migration := migrationbackend.AppliedMigration{App: "news", Name: "0001_relation_with_profile_field"}
	seedSQLiteRelationPhysicalSchemaAndHistory(t, ctx, backend, migration, target, source, sourceField)
	profileAfter := ir.Model{
		Name: "profile", GoName: "Profile", DBTable: "news_profile",
		Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
	}
	profileBefore := profileAfter.Clone()
	profileBefore.Fields = append(profileBefore.Fields, ir.Field{
		Name: "code", GoName: "Code", Column: "code", Kind: ir.FieldChar, MaxLength: 40,
	})
	profileSQL, err := compileMigrationCreateModel(profileBefore)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		profileSQL,
		`CREATE TABLE "outside_profile_child" (` +
			`"id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, ` +
			`"profile_code" VARCHAR(40), ` +
			`FOREIGN KEY ("profile_code") REFERENCES "news_profile" ("code"))`,
	} {
		if _, err := backend.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed scalar RemoveField dependency %q: %v", statement, err)
		}
	}

	intent := migrationbackend.RelationMigrationIntent{Operations: []migrationbackend.RelationMigrationOperation{
		{
			OperationIndex: 2,
			Kind:           migrationbackend.RelationMigrationDeleteModel,
			Before:         source,
			Targets: []migrationbackend.RelationMigrationTarget{{
				SourceField: sourceField,
				TargetModel: target,
				TargetKey:   target.Fields[0],
			}},
		},
		{OperationIndex: 1, Kind: migrationbackend.RelationMigrationDeleteModel, Before: target},
		{OperationIndex: 0, Kind: migrationbackend.RelationMigrationRemoveField, Before: profileBefore, After: profileAfter},
	}}
	session := openSQLiteRelationSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	concrete := session.(*sqliteRevisionFencedSession)
	var checkpoints []sqliteRelationBeginCheckpoint
	concrete.relationBeginCheckpoint = func(checkpoint sqliteRelationBeginCheckpoint) {
		checkpoints = append(checkpoints, checkpoint)
	}
	transaction, err := session.BeginRelationFencedMigration(ctx, migrationbackend.HistoryTransition{
		Migration: migration,
		Kind:      migrationbackend.HistoryTransitionUnapply,
	}, intent)
	if transaction != nil || err == nil {
		t.Fatalf("BeginRelationFencedMigration() = (%v, %v), want inbound removed-column failure", transaction, err)
	}
	assertSQLiteRelationCapabilityFeature(t, err, "sqlite_drop_column")
	assertSQLiteRelationNoClaimCheckpoint(t, checkpoints)
	if concrete.state != revisionSessionPoisoned || concrete.active != nil {
		t.Fatalf("session after removed-column preflight = (state:%d active:%v), want poisoned without active transaction", concrete.state, concrete.active)
	}
	if !sqliteRelationTestTableExists(t, backend, target.DBTable) ||
		!sqliteRelationTestTableExists(t, backend, source.DBTable) ||
		!sqliteRelationTestTableExists(t, backend, profileBefore.DBTable) {
		t.Fatal("removed-column preflight failure changed a sealed table")
	}
	var codeColumns int
	if err := backend.database.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM pragma_table_info('news_profile') WHERE "name" = 'code'`,
	).Scan(&codeColumns); err != nil || codeColumns != 1 {
		t.Fatalf("profile code columns after failure = (%d, %v), want unchanged 1", codeColumns, err)
	}
	snapshot, err := readAtomicMigrationRevisionSnapshot(ctx, backend)
	if err != nil || !reflect.DeepEqual(snapshot.records, []migrationbackend.AppliedMigration{migration}) {
		t.Fatalf("history after removed-column preflight = (%+v, %v)", snapshot, err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteRelationTargetLookupUsesSealedTableIndexAtOperationLimit(t *testing.T) {
	target, _, sourceField := sqliteRelationTestModels()
	sourceField.Relation.Target.AppLabel = "accounts"
	intent := migrationbackend.RelationMigrationIntent{
		Operations: make([]migrationbackend.RelationMigrationOperation, sqliteRelationMaxOperations),
	}
	models := make([]ir.Model, sqliteRelationMaxOperations)
	longSuffix := strings.Repeat("x", 1_024)
	for index := range intent.Operations {
		field := sourceField.Clone()
		field.Relation.Reverse.Name = fmt.Sprintf("sources_%04d", index)
		model := ir.Model{
			Name:    fmt.Sprintf("source_%04d", index),
			GoName:  fmt.Sprintf("Source%d", index),
			DBTable: fmt.Sprintf("news_source_%04d_%s", index, longSuffix),
			Fields: []ir.Field{
				{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
				field,
			},
		}
		models[index] = model
		intent.Operations[index] = migrationbackend.RelationMigrationOperation{
			OperationIndex: index,
			Kind:           migrationbackend.RelationMigrationCreateModel,
			After:          model,
			Targets: []migrationbackend.RelationMigrationTarget{{
				SourceField: field,
				TargetModel: target,
				TargetKey:   target.Fields[0],
			}},
		}
	}
	seal, err := validateAndSealSQLiteRelationIntent(migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_large_relation"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}, intent)
	if err != nil {
		t.Fatalf("validateAndSealSQLiteRelationIntent(max operations): %v", err)
	}
	if len(seal.targetOperationByTable) != sqliteRelationMaxOperations {
		t.Fatalf("sealed target table index size = %d, want %d", len(seal.targetOperationByTable), sqliteRelationMaxOperations)
	}
	for index := range models {
		targets, known := sqliteRelationTargetsForModel(&seal, models[index])
		if !known || len(targets) != 1 || !reflect.DeepEqual(targets[0].TargetModel, target) {
			t.Fatalf("indexed targets[%d] = (%#v, %t)", index, targets, known)
		}
	}
}

func TestSQLiteRelationReverseCollisionCheckBoundsLongModelNameAtFieldLimit(t *testing.T) {
	fields := make([]ir.Field, sqliteRelationMaxFields)
	fields[0] = ir.Field{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}
	for index := 1; index < len(fields); index++ {
		fields[index] = ir.Field{
			Name:   fmt.Sprintf("field_%04d", index),
			GoName: fmt.Sprintf("Field%d", index),
			Column: fmt.Sprintf("field_%04d", index),
			Kind:   ir.FieldBoolean,
		}
	}
	wide := ir.Model{
		Name:    strings.Repeat("a", sqliteRelationMaxStringBytes-1),
		GoName:  "WideModel",
		DBTable: "wide_model",
		Fields:  fields,
	}
	reverseIndex := newSQLiteRelationReverseOwnerIndex()
	reverseApp := reverseIndex.app("news")
	if _, exists := reverseApp.register(
		wide.Name,
		"reserved_reverse",
		sqliteRelationReverseOwner{model: "source", field: "field"},
	); exists {
		t.Fatal("fresh reverse owner index reported duplicate registration")
	}
	reverseApp.modelLookups = 0
	if field, owner, collision := reverseApp.firstFieldCollision(wide.Name, wide.Fields); collision {
		t.Fatalf("unexpected long-model reverse collision = field:%#v owner:%#v", field, owner)
	}
	if reverseApp.modelLookups != 1 {
		t.Fatalf("long model reverse outer-map lookups = %d, want exactly 1 for all %d fields", reverseApp.modelLookups, len(wide.Fields))
	}
	target, source, sourceField := sqliteRelationTestModels()
	intent := sqliteRelationApplyIntent(target, source, sourceField)
	intent.Operations = append([]migrationbackend.RelationMigrationOperation{{
		OperationIndex: 0,
		Kind:           migrationbackend.RelationMigrationCreateModel,
		After:          wide,
	}}, intent.Operations...)
	intent.Operations[1].OperationIndex = 1
	intent.Operations[2].OperationIndex = 2
	seal, err := validateAndSealSQLiteRelationIntent(migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_wide_relation"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}, intent)
	if err != nil {
		t.Fatalf("validateAndSealSQLiteRelationIntent(long model at field limit): %v", err)
	}
	if len(seal.intent.Operations) != 3 || seal.intent.Operations[0].After.Name != wide.Name {
		t.Fatal("long-name boundary intent was not sealed exactly")
	}
}

func TestSQLiteRelationReverseCollisionIndexSelectsLongTransitionAppOnce(t *testing.T) {
	longApp := strings.Repeat("a", sqliteRelationMaxStringBytes-1)
	reverseIndex := newSQLiteRelationReverseOwnerIndex()
	transitionOwners := reverseIndex.app(longApp)
	for index := 0; index < sqliteRelationMaxOperations; index++ {
		model := fmt.Sprintf("model_%04d", index)
		if _, _, collision := transitionOwners.firstFieldCollision(model, nil); collision {
			t.Fatalf("unexpected reverse collision for %q", model)
		}
	}
	if reverseIndex.appLookups != 1 {
		t.Fatalf("long transition app outer-map lookups = %d, want exactly 1 for %d operations", reverseIndex.appLookups, sqliteRelationMaxOperations)
	}
	if transitionOwners.modelLookups != sqliteRelationMaxOperations {
		t.Fatalf("short model lookups = %d, want %d", transitionOwners.modelLookups, sqliteRelationMaxOperations)
	}

	operations := make([]migrationbackend.RelationMigrationOperation, sqliteRelationMaxOperations)
	for index := 0; index < len(operations)-2; index++ {
		operations[index] = migrationbackend.RelationMigrationOperation{
			OperationIndex: index,
			Kind:           migrationbackend.RelationMigrationCreateModel,
			After: ir.Model{
				Name:    fmt.Sprintf("scalar_%04d", index),
				GoName:  fmt.Sprintf("Scalar%d", index),
				DBTable: fmt.Sprintf("scalar_%04d", index),
				Fields: []ir.Field{{
					Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true,
				}},
			},
		}
	}
	target, source, sourceField := sqliteRelationTestModels()
	sourceField.Relation.Target.AppLabel = longApp
	source.Fields[2] = sourceField
	operations[len(operations)-2] = migrationbackend.RelationMigrationOperation{
		OperationIndex: len(operations) - 2,
		Kind:           migrationbackend.RelationMigrationCreateModel,
		After:          target,
	}
	operations[len(operations)-1] = migrationbackend.RelationMigrationOperation{
		OperationIndex: len(operations) - 1,
		Kind:           migrationbackend.RelationMigrationCreateModel,
		After:          source,
		Targets: []migrationbackend.RelationMigrationTarget{{
			SourceField: sourceField,
			TargetModel: target,
			TargetKey:   target.Fields[0],
		}},
	}
	seal, err := validateAndSealSQLiteRelationIntent(migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: longApp, Name: "0001_relation"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}, migrationbackend.RelationMigrationIntent{Operations: operations})
	if err != nil {
		t.Fatalf("validateAndSealSQLiteRelationIntent(long transition app at operation limit): %v", err)
	}
	if len(seal.intent.Operations) != sqliteRelationMaxOperations {
		t.Fatalf("sealed operation count = %d, want %d", len(seal.intent.Operations), sqliteRelationMaxOperations)
	}
}

func TestSQLiteRelationResourceScanBoundsTransitionIdentityBeforeClone(t *testing.T) {
	target, source, sourceField := sqliteRelationTestModels()
	transition := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{
			App:  strings.Repeat("a", sqliteRelationMaxStringBytes+1),
			Name: "0001_relation",
		},
		Kind: migrationbackend.HistoryTransitionApply,
	}
	_, err := validateAndSealSQLiteRelationIntent(transition, sqliteRelationApplyIntent(target, source, sourceField))
	if err == nil || !strings.Contains(err.Error(), "transition.migration.app") || !strings.Contains(err.Error(), "maximum") {
		t.Fatalf("oversized transition app error = %v", err)
	}
}

func TestSQLiteRelationReverseOwnersRetainLongSourceStructurallyAtTargetLimit(t *testing.T) {
	target, _, baseField := sqliteRelationTestModels()
	fields := make([]ir.Field, sqliteRelationMaxFields)
	targets := make([]migrationbackend.RelationMigrationTarget, sqliteRelationMaxFields-1)
	fields[0] = ir.Field{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}
	for index := 1; index < len(fields); index++ {
		field := baseField.Clone()
		field.Name = fmt.Sprintf("target_%04d", index)
		field.GoName = fmt.Sprintf("Target%d", index)
		field.Column = fmt.Sprintf("target_%04d_id", index)
		field.Relation.Reverse.Name = fmt.Sprintf("sources_%04d", index)
		fields[index] = field
		targets[index-1] = migrationbackend.RelationMigrationTarget{
			SourceField: field,
			TargetModel: target,
			TargetKey:   target.Fields[0],
		}
	}
	longSourceName := strings.Repeat("s", sqliteRelationMaxStringBytes-1)
	source := ir.Model{
		Name:    longSourceName,
		GoName:  "WideRelationSource",
		DBTable: "wide_relation_source",
		Fields:  fields,
	}
	intent := migrationbackend.RelationMigrationIntent{Operations: []migrationbackend.RelationMigrationOperation{
		{
			OperationIndex: 0,
			Kind:           migrationbackend.RelationMigrationCreateModel,
			After:          target,
		},
		{
			OperationIndex: 1,
			Kind:           migrationbackend.RelationMigrationCreateModel,
			After:          source,
			Targets:        targets,
		},
	}}
	seal, err := validateAndSealSQLiteRelationIntent(migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_wide_relation_source"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}, intent)
	if err != nil {
		t.Fatalf("validateAndSealSQLiteRelationIntent(long source at target limit): %v", err)
	}
	if len(seal.intent.Operations[1].Targets) != sqliteRelationMaxFields-1 || seal.intent.Operations[1].After.Name != longSourceName {
		t.Fatal("long-source relation owners were not sealed at the target limit")
	}
}

func TestSQLiteRelationCatalogJoinsRowsCloseFailure(t *testing.T) {
	closeErr := errors.New("catalog rows close failure")
	backend := openMigrationHistoryFaultBackend(t, historyFault{
		rows:     [][]driver.Value{{"too", "few"}},
		closeErr: closeErr,
	})
	_, err := loadSQLiteRelationCatalog(context.Background(), backend.database)
	if err == nil || !errors.Is(err, closeErr) || !strings.Contains(err.Error(), "expected 2 destination arguments") {
		t.Fatalf("loadSQLiteRelationCatalog() error = %v, want scan primary joined with rows.Close cause", err)
	}
}

func sqliteRelationTestModels() (ir.Model, ir.Model, ir.Field) {
	target := ir.Model{
		Name: "author", GoName: "Author", DBTable: "news_author",
		Fields: []ir.Field{
			{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
			{Name: "name", GoName: "Name", Column: "name", Kind: ir.FieldChar, MaxLength: 120},
		},
	}
	sourceField := ir.Field{
		Name: "author", GoName: "Author", Column: "author_id", Kind: ir.FieldForeignKey,
		Relation: &ir.ForeignKeyRelation{
			Target:      ir.ModelIdentity{AppLabel: "news", ModelName: "author"},
			Cardinality: ir.RelationManyToOne,
			Reverse:     ir.ReverseRelation{Name: "articles"},
			OnDelete:    ir.DeleteProtect,
		},
	}
	source := ir.Model{
		Name: "article", GoName: "Article", DBTable: "news_article",
		Fields: []ir.Field{
			{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
			{Name: "title", GoName: "Title", Column: "title", Kind: ir.FieldChar, MaxLength: 200},
			sourceField,
		},
	}
	return target, source, sourceField
}

func sqliteRelationApplyIntent(target, source ir.Model, sourceField ir.Field) migrationbackend.RelationMigrationIntent {
	return migrationbackend.RelationMigrationIntent{Operations: []migrationbackend.RelationMigrationOperation{
		{OperationIndex: 0, Kind: migrationbackend.RelationMigrationCreateModel, After: target},
		{
			OperationIndex: 1,
			Kind:           migrationbackend.RelationMigrationCreateModel,
			After:          source,
			Targets: []migrationbackend.RelationMigrationTarget{{
				SourceField: sourceField,
				TargetModel: target,
				TargetKey:   target.Fields[0],
			}},
		},
	}}
}

func sqliteRelationUnapplyIntent(target, source ir.Model, sourceField ir.Field) migrationbackend.RelationMigrationIntent {
	return migrationbackend.RelationMigrationIntent{Operations: []migrationbackend.RelationMigrationOperation{
		{
			OperationIndex: 1,
			Kind:           migrationbackend.RelationMigrationDeleteModel,
			Before:         source,
			Targets: []migrationbackend.RelationMigrationTarget{{
				SourceField: sourceField,
				TargetModel: target,
				TargetKey:   target.Fields[0],
			}},
		},
		{OperationIndex: 0, Kind: migrationbackend.RelationMigrationDeleteModel, Before: target},
	}}
}

func seedSQLiteRelationPhysicalSchemaAndHistory(
	t *testing.T,
	ctx context.Context,
	backend *Backend,
	migration migrationbackend.AppliedMigration,
	target,
	source ir.Model,
	sourceField ir.Field,
) {
	t.Helper()
	targetSQL, err := compileMigrationCreateModel(target)
	if err != nil {
		t.Fatal(err)
	}
	sourceSQL, err := compileSQLiteRelationCreateModel(source, []migrationbackend.RelationMigrationTarget{{
		SourceField: sourceField,
		TargetModel: target,
		TargetKey:   target.Fields[0],
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{targetSQL, sourceSQL} {
		if _, err := backend.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed relation schema %q: %v", statement, err)
		}
	}
	seedSQLiteMigrationHistory(t, ctx, backend, migration)
}

func seedSQLiteMigrationHistory(
	t *testing.T,
	ctx context.Context,
	backend *Backend,
	migration migrationbackend.AppliedMigration,
) {
	t.Helper()
	session, err := backend.OpenRevisionFencedSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	transaction, err := session.BeginFencedMigration(ctx, migrationbackend.HistoryTransition{
		Migration: migration,
		Kind:      migrationbackend.HistoryTransitionApply,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.RecordApplied(ctx, migration.App, migration.Name); err != nil {
		t.Fatal(err)
	}
	outcome, err := transaction.CommitFenced(ctx)
	if err != nil || outcome.Durability != migrationbackend.CommitCommitted {
		t.Fatalf("seed history CommitFenced() = (%+v, %v)", outcome, err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func openSQLiteRelationSession(t *testing.T, backend *Backend) migrationbackend.RelationRevisionFencedSession {
	t.Helper()
	raw, err := backend.OpenRevisionFencedSession(context.Background())
	if err != nil {
		t.Fatalf("OpenRevisionFencedSession(): %v", err)
	}
	session, ok := raw.(migrationbackend.RelationRevisionFencedSession)
	if !ok {
		t.Fatalf("SQLite session type %T does not implement RelationRevisionFencedSession", raw)
	}
	return session
}

func sqliteRelationTestTableExists(t *testing.T, backend *Backend, table string) bool {
	t.Helper()
	var count int
	if err := backend.database.QueryRowContext(
		context.Background(),
		`SELECT COUNT(*) FROM main.sqlite_schema WHERE "type" = 'table' AND "name" = ?`,
		table,
	).Scan(&count); err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("inspect table %q: %v", table, err)
	}
	return count == 1
}

func assertSQLiteRelationCapabilityFeature(t *testing.T, err error, feature string) {
	t.Helper()
	var capability *migrationbackend.CapabilityError
	if !errors.As(err, &capability) || capability.Feature != feature {
		t.Fatalf("capability error = %#v (%v), want feature %q", capability, err, feature)
	}
}

func assertSQLiteRelationNoClaimCheckpoint(t *testing.T, checkpoints []sqliteRelationBeginCheckpoint) {
	t.Helper()
	for _, checkpoint := range checkpoints {
		if checkpoint == sqliteRelationCheckpointRevisionClaimStarting || checkpoint == sqliteRelationCheckpointRevisionClaimed {
			t.Fatalf("preflight reached revision claim: checkpoints=%v", checkpoints)
		}
	}
}

type countingRelationSQLExecutor struct {
	migrationSQLExecutor
	queryCalls int
}

func (executor *countingRelationSQLExecutor) QueryContext(
	ctx context.Context,
	statement string,
	arguments ...any,
) (*sql.Rows, error) {
	executor.queryCalls++
	return executor.migrationSQLExecutor.QueryContext(ctx, statement, arguments...)
}

type sqliteRelationBeginFaultConnection struct {
	migrationPinnedConnection
	method        string
	contains      string
	remaining     int
	closeCalls    int
	rawCalls      int
	rollbackCalls int
	faultErr      error
	rollbackErr   error
	closeErr      error
}

func (connection *sqliteRelationBeginFaultConnection) inject(method, statement string) error {
	if connection.remaining == 0 || connection.method != method || !strings.Contains(statement, connection.contains) {
		return nil
	}
	connection.remaining--
	if connection.faultErr != nil {
		return connection.faultErr
	}
	return errors.New("injected relation Begin fault")
}

func (connection *sqliteRelationBeginFaultConnection) ExecContext(
	ctx context.Context,
	statement string,
	arguments ...any,
) (sql.Result, error) {
	if statement == "ROLLBACK" {
		connection.rollbackCalls++
		if connection.rollbackErr != nil {
			return nil, connection.rollbackErr
		}
	}
	if err := connection.inject("exec", statement); err != nil {
		return nil, err
	}
	return connection.migrationPinnedConnection.ExecContext(ctx, statement, arguments...)
}

func (connection *sqliteRelationBeginFaultConnection) QueryContext(
	ctx context.Context,
	statement string,
	arguments ...any,
) (*sql.Rows, error) {
	if err := connection.inject("query", statement); err != nil {
		return nil, err
	}
	return connection.migrationPinnedConnection.QueryContext(ctx, statement, arguments...)
}

func (connection *sqliteRelationBeginFaultConnection) QueryRowContext(
	ctx context.Context,
	statement string,
	arguments ...any,
) *sql.Row {
	if err := connection.inject("query_row", statement); err != nil {
		return connection.migrationPinnedConnection.QueryRowContext(ctx, `SELECT * FROM "__godj_relation_begin_fault__"`)
	}
	return connection.migrationPinnedConnection.QueryRowContext(ctx, statement, arguments...)
}

func (connection *sqliteRelationBeginFaultConnection) Close() error {
	connection.closeCalls++
	if connection.closeErr != nil {
		return connection.closeErr
	}
	return connection.migrationPinnedConnection.Close()
}

func (connection *sqliteRelationBeginFaultConnection) Raw(callback func(any) error) error {
	connection.rawCalls++
	return connection.migrationPinnedConnection.Raw(callback)
}
