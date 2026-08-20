package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

func TestSQLiteRevisionFenceBootstrapReopenAddFieldAndReverse(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "lifecycle.sqlite")
	backend := openLifecycleFileBackend(t, path, "")
	session := openLifecycleSession(t, backend)
	if records, err := session.ReadAppliedMigrations(ctx); err != nil || len(records) != 0 {
		t.Fatalf("fresh snapshot = (%v, %v), want empty", records, err)
	}
	if sqliteTableExists(t, backend, migrationRevisionTable) || sqliteTableExists(t, backend, migrationRecorderTable) {
		t.Fatal("read-only fresh snapshot created metadata or recorder")
	}

	initial := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_initial"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}
	initialModel := migrationTestModel(false)
	transaction := beginLifecycleTransaction(t, session, initial, createModelMigrationIntent(initialModel))
	if err := transaction.CreateModel(ctx, initialModel); err != nil {
		t.Fatalf("CreateModel(): %v", err)
	}
	if err := transaction.RecordApplied(ctx, "news", "0001_initial"); err != nil {
		t.Fatalf("RecordApplied(): %v", err)
	}
	outcome, err := transaction.CommitFenced(ctx)
	if err != nil || outcome.Durability != migrationbackend.CommitCommitted {
		t.Fatalf("initial CommitFenced() = (%+v, %v)", outcome, err)
	}
	initialShape, err := readAtomicMigrationRevisionSnapshot(ctx, backend)
	if err != nil {
		t.Fatalf("read initial revision: %v", err)
	}
	if initialShape.token.revision != 1 || len(initialShape.records) != 1 || !initialShape.token.initialized {
		t.Fatalf("initial revision shape = %+v", initialShape)
	}
	if initialShape.token.epoch == ([migrationRevisionEpochSize]byte{}) {
		t.Fatal("bootstrap epoch is all zero")
	}
	if err := session.Close(ctx); err != nil {
		t.Fatalf("close initial session: %v", err)
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("close initial backend: %v", err)
	}

	backend = openLifecycleFileBackend(t, path, "")
	session = openLifecycleSession(t, backend)
	records, err := session.ReadAppliedMigrations(ctx)
	wantRecords := []migrationbackend.AppliedMigration{{App: "news", Name: "0001_initial"}}
	if err != nil || !reflect.DeepEqual(records, wantRecords) {
		t.Fatalf("reopened records = (%v, %v), want %v", records, err, wantRecords)
	}

	falseDefault := &ir.ScalarDefault{Kind: ir.ScalarBoolean, Boolean: false}
	field := ir.Field{
		Name: "featured", GoName: "Featured", Column: "featured",
		Kind: ir.FieldBoolean, Default: falseDefault,
	}
	addTransition := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0002_featured"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}
	beforeAdd := migrationTestModel(false)
	afterAdd := beforeAdd.Clone()
	afterAdd.Fields = append(afterAdd.Fields, field.Clone())
	transaction = beginLifecycleTransaction(t, session, addTransition, addFieldMigrationIntent(beforeAdd, afterAdd))
	if err := transaction.AddField(ctx, migrationTestModel(false), field); err != nil {
		t.Fatalf("AddField(Boolean(false)): %v", err)
	}
	if err := transaction.RecordApplied(ctx, "news", "0002_featured"); err != nil {
		t.Fatalf("record AddField migration: %v", err)
	}
	if outcome, err := transaction.CommitFenced(ctx); err != nil || outcome.Durability != migrationbackend.CommitCommitted {
		t.Fatalf("AddField CommitFenced() = (%+v, %v)", outcome, err)
	}
	assertSQLiteColumns(t, backend, "godj_migration_article", "id", "title", "published", "featured")
	assertSQLiteColumnHasNoPersistentDefault(t, backend, "godj_migration_article", "featured")
	if _, err := backend.ExecContext(
		ctx,
		`INSERT INTO "godj_migration_article" ("title", "published") VALUES ('missing-featured', 0)`,
	); err == nil {
		t.Fatal("raw insert without featured succeeded; physical default leaked")
	}

	reverseTransition := migrationbackend.HistoryTransition{
		Migration: addTransition.Migration,
		Kind:      migrationbackend.HistoryTransitionUnapply,
	}
	modelWithField := migrationTestModel(false)
	modelWithField.Fields = append(modelWithField.Fields, field)
	transaction = beginLifecycleTransaction(t, session, reverseTransition, removeFieldMigrationIntent(modelWithField, migrationTestModel(false)))
	if err := transaction.RemoveField(ctx, modelWithField, field); err != nil {
		t.Fatalf("RemoveField(): %v", err)
	}
	if err := transaction.RecordUnapplied(ctx, "news", "0002_featured"); err != nil {
		t.Fatalf("RecordUnapplied(): %v", err)
	}
	if outcome, err := transaction.CommitFenced(ctx); err != nil || outcome.Durability != migrationbackend.CommitCommitted {
		t.Fatalf("reverse CommitFenced() = (%+v, %v)", outcome, err)
	}
	finalShape, err := readAtomicMigrationRevisionSnapshot(ctx, backend)
	if err != nil {
		t.Fatalf("read final revision: %v", err)
	}
	if finalShape.token.revision != 3 || finalShape.token.epoch != initialShape.token.epoch ||
		!reflect.DeepEqual(finalShape.records, wantRecords) {
		t.Fatalf("final revision shape = %+v", finalShape)
	}
	assertSQLiteColumns(t, backend, "godj_migration_article", "id", "title", "published")
	if err := session.Close(ctx); err != nil {
		t.Fatalf("close reopened session: %v", err)
	}
}

func TestSQLiteMigrationHistoryFingerprintV1Goldens(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		records []migrationbackend.AppliedMigration
		want    string
	}{
		{name: "empty", want: "af5570f5a1810b7af78caf4bc70a660f0df51e42baf91d4de5b2328de0e83dfc"},
		{
			name:    "alpha",
			records: []migrationbackend.AppliedMigration{{App: "alpha", Name: "0001"}},
			want:    "d082f0a0b67b8c2b5c7efc208270dd2e17c6a346d9b2fa0e572e6396dedff40e",
		},
		{
			name:    "utf8_byte_length",
			records: []migrationbackend.AppliedMigration{{App: "legacy", Name: "ä"}},
			want:    "35e542d7c4bce2ba60aa694f4301300cb1835e58ca14efe54a781ea7ae03e45c",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fingerprint := fingerprintMigrationHistory(test.records)
			got := hex.EncodeToString(fingerprint[:])
			if got != test.want {
				t.Fatalf("fingerprint = %s, want %s", got, test.want)
			}
		})
	}
}

func TestSQLiteRevisionFenceRequiresAdoptionForExistingRecorder(t *testing.T) {
	for _, withRecord := range []bool{false, true} {
		withRecord := withRecord
		t.Run(fmt.Sprintf("record=%t", withRecord), func(t *testing.T) {
			ctx := context.Background()
			backend := openMigrationTestBackend(t)
			if _, err := backend.ExecContext(ctx, createMigrationRecorderTableSQL); err != nil {
				t.Fatalf("create recorder: %v", err)
			}
			if withRecord {
				if _, err := backend.ExecContext(ctx, `INSERT INTO "godj_migrations" VALUES ('legacy', '0001')`); err != nil {
					t.Fatalf("seed recorder: %v", err)
				}
			}
			session := openLifecycleSession(t, backend)
			_, err := session.ReadAppliedMigrations(ctx)
			assertRevisionFenceKind(t, err, migrationbackend.RevisionFenceFailureAdoptionRequired)
			if sqliteTableExists(t, backend, migrationRevisionTable) {
				t.Fatal("adoption-required snapshot created revision metadata")
			}
			if err := session.Close(ctx); err != nil {
				t.Fatalf("Close(): %v", err)
			}
		})
	}
}

func TestSQLiteRevisionSessionRequiresSealedIntentEvenWhenEmpty(t *testing.T) {
	ctx := context.Background()
	backend := openMigrationTestBackend(t)
	session := openLifecycleSession(t, backend)
	if records, err := session.ReadAppliedMigrations(ctx); err != nil || len(records) != 0 {
		t.Fatalf("fresh snapshot = (%v, %v), want empty", records, err)
	}
	transition := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}

	transaction, err := session.BeginMigration(ctx, transition, migrationbackend.MigrationIntent{})
	if transaction != nil || err == nil || !strings.Contains(err.Error(), "intent operations are missing") {
		t.Fatalf("BeginMigration(missing intent) = (%v, %v), want nil missing-intent error", transaction, err)
	}

	transaction, err = session.BeginMigration(ctx, transition, emptySQLiteMigrationIntent())
	if err != nil || transaction == nil {
		t.Fatalf("BeginMigration(empty sealed intent) = (%v, %v), want transaction", transaction, err)
	}
	if err := transaction.Rollback(ctx); err != nil {
		t.Fatalf("Rollback(empty sealed intent): %v", err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatalf("Close(): %v", err)
	}
}

func TestSQLiteRevisionSessionStateMachine(t *testing.T) {
	ctx := context.Background()
	transition := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}

	t.Run("exact_one_snapshot_and_idempotent_close", func(t *testing.T) {
		backend := openMigrationTestBackend(t)
		session := openLifecycleSession(t, backend)
		if records, err := session.ReadAppliedMigrations(ctx); err != nil || len(records) != 0 {
			t.Fatalf("first snapshot = (%v, %v)", records, err)
		}
		if stats := backend.database.Stats(); stats.InUse != 0 {
			t.Fatalf("snapshot pinned %d connections", stats.InUse)
		}
		if _, err := session.ReadAppliedMigrations(ctx); err == nil {
			t.Fatal("second snapshot succeeded")
		}
		if err := session.Close(ctx); err != nil {
			t.Fatalf("Close(): %v", err)
		}
		if err := session.Close(ctx); err != nil {
			t.Fatalf("second Close(): %v", err)
		}
	})

	t.Run("begin_before_snapshot_and_closed_reuse", func(t *testing.T) {
		backend := openMigrationTestBackend(t)
		session := openLifecycleSession(t, backend)
		if _, err := session.BeginMigration(ctx, transition, emptySQLiteMigrationIntent()); err == nil {
			t.Fatal("BeginMigration before snapshot succeeded")
		}
		if sqliteTableExists(t, backend, migrationRevisionTable) || sqliteTableExists(t, backend, migrationRecorderTable) {
			t.Fatal("begin-before-snapshot mutated history")
		}
		if err := session.Close(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := session.ReadAppliedMigrations(ctx); err == nil {
			t.Fatal("closed session snapshot succeeded")
		}
		if _, err := session.BeginMigration(ctx, transition, emptySQLiteMigrationIntent()); err == nil {
			t.Fatal("closed session begin succeeded")
		}
	})

	t.Run("second_active_begin_and_poisoned_reuse", func(t *testing.T) {
		backend := openMigrationTestBackend(t)
		session := openLifecycleSession(t, backend)
		if _, err := session.ReadAppliedMigrations(ctx); err != nil {
			t.Fatal(err)
		}
		transaction := beginLifecycleTransaction(t, session, transition)
		if _, err := session.BeginMigration(ctx, transition, emptySQLiteMigrationIntent()); err == nil {
			t.Fatal("second active transaction began")
		}
		if err := transaction.Rollback(ctx); err != nil {
			t.Fatalf("Rollback(): %v", err)
		}
		if _, err := session.BeginMigration(ctx, transition, emptySQLiteMigrationIntent()); err == nil {
			t.Fatal("poisoned session begin succeeded")
		}
		if _, err := session.ReadAppliedMigrations(ctx); err == nil {
			t.Fatal("poisoned session snapshot succeeded")
		}
		if err := session.Close(ctx); err != nil {
			t.Fatal(err)
		}
		if sqliteTableExists(t, backend, migrationRevisionTable) || sqliteTableExists(t, backend, migrationRecorderTable) {
			t.Fatal("rolled-back active transaction mutated history")
		}
		if stats := backend.database.Stats(); stats.InUse != 0 {
			t.Fatalf("database in-use connections = %d, want 0", stats.InUse)
		}
	})

	t.Run("concurrent_operation_and_rollback", func(t *testing.T) {
		backend := openMigrationTestBackend(t)
		session := openLifecycleSession(t, backend)
		if _, err := session.ReadAppliedMigrations(ctx); err != nil {
			t.Fatal(err)
		}
		transaction := beginLifecycleTransaction(t, session, transition)
		concrete := transaction.(*sqliteRevisionFencedTransaction)
		start := make(chan struct{})
		errorsSeen := make(chan error, 33)
		var group sync.WaitGroup
		for range 32 {
			group.Add(1)
			go func() {
				defer group.Done()
				<-start
				_, err := concrete.TableExists(ctx, "sqlite_schema")
				if err != nil && !strings.Contains(err.Error(), "already complete") {
					errorsSeen <- err
				}
			}()
		}
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			errorsSeen <- transaction.Rollback(ctx)
		}()
		close(start)
		group.Wait()
		close(errorsSeen)
		for err := range errorsSeen {
			if err != nil {
				t.Fatalf("concurrent operation/rollback: %v", err)
			}
		}
		if err := session.Close(ctx); err != nil {
			t.Fatal(err)
		}
		if stats := backend.database.Stats(); stats.InUse != 0 {
			t.Fatalf("database in-use connections = %d, want 0", stats.InUse)
		}
	})

	t.Run("concurrent_commit_and_rollback", func(t *testing.T) {
		backend := openMigrationTestBackend(t)
		session := openLifecycleSession(t, backend)
		if _, err := session.ReadAppliedMigrations(ctx); err != nil {
			t.Fatal(err)
		}
		transaction := beginLifecycleTransaction(t, session, transition)
		if err := transaction.RecordApplied(ctx, "news", "0001"); err != nil {
			t.Fatal(err)
		}
		type commitResult struct {
			outcome migrationbackend.CommitOutcome
			err     error
		}
		start := make(chan struct{})
		commitDone := make(chan commitResult, 1)
		rollbackDone := make(chan error, 1)
		go func() {
			<-start
			outcome, err := transaction.CommitFenced(ctx)
			commitDone <- commitResult{outcome: outcome, err: err}
		}()
		go func() {
			<-start
			rollbackDone <- transaction.Rollback(ctx)
		}()
		close(start)
		commit := <-commitDone
		if err := <-rollbackDone; err != nil {
			t.Fatalf("concurrent Rollback(): %v", err)
		}
		switch commit.outcome.Durability {
		case migrationbackend.CommitCommitted:
			if commit.err != nil {
				t.Fatalf("committed with error: %v", commit.err)
			}
			shape, err := readAtomicMigrationRevisionSnapshot(ctx, backend)
			if err != nil || shape.token.revision != 1 || len(shape.records) != 1 {
				t.Fatalf("committed shape = (%+v, %v)", shape, err)
			}
		case migrationbackend.CommitUnknown:
			if commit.err == nil {
				t.Fatal("terminal race returned unknown without an error")
			}
			if sqliteTableExists(t, backend, migrationRevisionTable) || sqliteTableExists(t, backend, migrationRecorderTable) {
				t.Fatal("rollback winner left durable history")
			}
		default:
			t.Fatalf("terminal race durability = %d, want committed or unknown", commit.outcome.Durability)
		}
		if err := session.Close(ctx); err != nil {
			t.Fatal(err)
		}
		if stats := backend.database.Stats(); stats.InUse != 0 {
			t.Fatalf("database in-use connections = %d, want 0", stats.InUse)
		}
	})

	t.Run("concurrent_close_is_idempotent", func(t *testing.T) {
		backend := openMigrationTestBackend(t)
		session := openLifecycleSession(t, backend)
		if _, err := session.ReadAppliedMigrations(ctx); err != nil {
			t.Fatal(err)
		}
		_ = beginLifecycleTransaction(t, session, transition)
		start := make(chan struct{})
		errorsSeen := make(chan error, 16)
		var group sync.WaitGroup
		for range 16 {
			group.Add(1)
			go func() {
				defer group.Done()
				<-start
				errorsSeen <- session.Close(ctx)
			}()
		}
		close(start)
		group.Wait()
		close(errorsSeen)
		for err := range errorsSeen {
			if err != nil {
				t.Fatalf("concurrent Close(): %v", err)
			}
		}
		if stats := backend.database.Stats(); stats.InUse != 0 {
			t.Fatalf("database in-use connections = %d, want 0", stats.InUse)
		}
	})

	t.Run("concurrent_close_waiter_honors_its_context", func(t *testing.T) {
		backend := openMigrationTestBackend(t)
		session := openLifecycleSession(t, backend)
		if _, err := session.ReadAppliedMigrations(ctx); err != nil {
			t.Fatal(err)
		}
		transaction := beginLifecycleTransaction(t, session, transition)
		concrete := transaction.(*sqliteRevisionFencedTransaction)
		entered := make(chan struct{})
		release := make(chan struct{})
		concrete.mu.Lock()
		concrete.connection = &blockingRollbackPinnedConnection{
			migrationPinnedConnection: concrete.connection,
			entered:                   entered,
			release:                   release,
		}
		concrete.mu.Unlock()

		firstDone := make(chan error, 1)
		firstCtx, firstCancel := context.WithTimeout(ctx, 2*time.Second)
		defer firstCancel()
		go func() { firstDone <- session.Close(firstCtx) }()
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("first Close() did not enter rollback")
		}

		waiterCtx, waiterCancel := context.WithCancel(ctx)
		waiterCancel()
		started := time.Now()
		waiterErr := session.Close(waiterCtx)
		if !errors.Is(waiterErr, context.Canceled) {
			t.Fatalf("second Close() error = %v, want context.Canceled", waiterErr)
		}
		if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
			t.Fatalf("second Close() waited %s for the first cleanup", elapsed)
		}

		close(release)
		if err := <-firstDone; err != nil {
			t.Fatalf("first Close(): %v", err)
		}
		if stats := backend.database.Stats(); stats.InUse != 0 {
			t.Fatalf("database in-use connections = %d, want 0", stats.InUse)
		}
	})

	t.Run("close_and_begin_do_not_leak", func(t *testing.T) {
		type beginResult struct {
			transaction migrationbackend.RevisionFencedTransaction
			err         error
		}
		for iteration := range 16 {
			path := filepath.Join(t.TempDir(), fmt.Sprintf("close-begin-%d.sqlite", iteration))
			backend := openLifecycleFileBackend(t, path, "&_busy_timeout=1")
			session := openLifecycleSession(t, backend)
			if _, err := session.ReadAppliedMigrations(ctx); err != nil {
				t.Fatal(err)
			}
			start := make(chan struct{})
			closeDone := make(chan error, 1)
			beginDone := make(chan beginResult, 1)
			go func() {
				<-start
				closeDone <- session.Close(ctx)
			}()
			go func() {
				<-start
				transaction, err := session.BeginMigration(ctx, transition, emptySQLiteMigrationIntent())
				beginDone <- beginResult{transaction: transaction, err: err}
			}()
			close(start)
			if err := <-closeDone; err != nil {
				t.Fatalf("iteration %d Close(): %v", iteration, err)
			}
			begin := <-beginDone
			if begin.err == nil {
				if err := begin.transaction.Rollback(ctx); err != nil {
					t.Fatalf("iteration %d terminal Rollback(): %v", iteration, err)
				}
			}
			if stats := backend.database.Stats(); stats.InUse != 0 {
				t.Fatalf("iteration %d in-use connections = %d, want 0", iteration, stats.InUse)
			}
			if sqliteTableExists(t, backend, migrationRevisionTable) || sqliteTableExists(t, backend, migrationRecorderTable) {
				t.Fatalf("iteration %d Close/Begin race mutated history", iteration)
			}
		}
	})
}

func TestSQLiteRevisionFencedDeclaredTransitionIntegrity(t *testing.T) {
	ctx := context.Background()
	transition := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}
	tests := []struct {
		name   string
		record func(migrationbackend.RevisionFencedTransaction) error
	}{
		{name: "zero_recorder_calls"},
		{name: "wrong_identity", record: func(transaction migrationbackend.RevisionFencedTransaction) error {
			return transaction.RecordApplied(ctx, "news", "wrong")
		}},
		{name: "opposite_direction", record: func(transaction migrationbackend.RevisionFencedTransaction) error {
			return transaction.RecordUnapplied(ctx, "news", "0001")
		}},
		{name: "duplicate_recorder_call", record: func(transaction migrationbackend.RevisionFencedTransaction) error {
			if err := transaction.RecordApplied(ctx, "news", "0001"); err != nil {
				return err
			}
			return transaction.RecordApplied(ctx, "news", "0001")
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			backend := openMigrationTestBackend(t)
			session := openLifecycleSession(t, backend)
			if _, err := session.ReadAppliedMigrations(ctx); err != nil {
				t.Fatal(err)
			}
			transaction := beginLifecycleTransaction(t, session, transition, createModelMigrationIntent(migrationTestModel(false)))
			if err := transaction.CreateModel(ctx, migrationTestModel(false)); err != nil {
				t.Fatal(err)
			}
			if test.record != nil {
				err := test.record(transaction)
				assertRevisionFenceKind(t, err, migrationbackend.RevisionFenceFailureIntegrity)
			}
			outcome, err := transaction.CommitFenced(ctx)
			if outcome.Durability != migrationbackend.CommitRolledBack {
				t.Fatalf("CommitFenced durability = %d, want rolled back", outcome.Durability)
			}
			assertRevisionFenceKind(t, err, migrationbackend.RevisionFenceFailureIntegrity)
			for _, table := range []string{migrationRevisionTable, migrationRecorderTable, "godj_migration_article"} {
				if sqliteTableExists(t, backend, table) {
					t.Fatalf("invalid declared transition preserved table %q", table)
				}
			}
			if _, beginErr := session.BeginMigration(ctx, transition, emptySQLiteMigrationIntent()); beginErr == nil {
				t.Fatal("session remained reusable after transition integrity failure")
			}
			if err := session.Close(ctx); err != nil {
				t.Fatal(err)
			}
			if stats := backend.database.Stats(); stats.InUse != 0 {
				t.Fatalf("database in-use connections = %d, want 0", stats.InUse)
			}
		})
	}

	t.Run("apply_already_present", func(t *testing.T) {
		backend := openMigrationTestBackend(t)
		session := openLifecycleSession(t, backend)
		if _, err := session.ReadAppliedMigrations(ctx); err != nil {
			t.Fatal(err)
		}
		transaction := beginLifecycleTransaction(t, session, transition)
		if err := transaction.RecordApplied(ctx, "news", "0001"); err != nil {
			t.Fatal(err)
		}
		if outcome, err := transaction.CommitFenced(ctx); err != nil || outcome.Durability != migrationbackend.CommitCommitted {
			t.Fatalf("bootstrap commit = (%+v, %v)", outcome, err)
		}
		before, err := readAtomicMigrationRevisionSnapshot(ctx, backend)
		if err != nil {
			t.Fatal(err)
		}
		_, err = session.BeginMigration(ctx, transition, emptySQLiteMigrationIntent())
		assertRevisionFenceKind(t, err, migrationbackend.RevisionFenceFailureIntegrity)
		after, err := readAtomicMigrationRevisionSnapshot(ctx, backend)
		if err != nil || !equalMigrationRevisionToken(before.token, after.token) || !equalAppliedMigrations(before.records, after.records) {
			t.Fatalf("already-present attempt changed durable history: before=%+v after=%+v err=%v", before, after, err)
		}
		if stats := backend.database.Stats(); stats.InUse != 0 {
			t.Fatalf("database in-use connections = %d, want 0", stats.InUse)
		}
	})

	t.Run("unapply_missing", func(t *testing.T) {
		backend := openMigrationTestBackend(t)
		session := openLifecycleSession(t, backend)
		if _, err := session.ReadAppliedMigrations(ctx); err != nil {
			t.Fatal(err)
		}
		_, err := session.BeginMigration(ctx, migrationbackend.HistoryTransition{
			Migration: transition.Migration,
			Kind:      migrationbackend.HistoryTransitionUnapply,
		}, emptySQLiteMigrationIntent())

		assertRevisionFenceKind(t, err, migrationbackend.RevisionFenceFailureIntegrity)
		if sqliteTableExists(t, backend, migrationRevisionTable) || sqliteTableExists(t, backend, migrationRecorderTable) {
			t.Fatal("missing unapply mutated fresh history")
		}
		if stats := backend.database.Stats(); stats.InUse != 0 {
			t.Fatalf("database in-use connections = %d, want 0", stats.InUse)
		}
	})
}

func TestSQLiteRevisionMetadataCorruptionFailsClosed(t *testing.T) {
	validHash := fingerprintMigrationHistory(nil)
	validEpoch := make([]byte, migrationRevisionEpochSize)
	for index := range validEpoch {
		validEpoch[index] = byte(index + 1)
	}
	type corruptionCase struct {
		name  string
		setup func(*testing.T, *Backend)
	}
	createReadyTables := func(t *testing.T, backend *Backend) {
		t.Helper()
		if _, err := backend.ExecContext(context.Background(), createMigrationRecorderTableSQL); err != nil {
			t.Fatalf("create recorder: %v", err)
		}
		if _, err := backend.ExecContext(context.Background(), createMigrationRevisionTableSQL); err != nil {
			t.Fatalf("create metadata: %v", err)
		}
		if _, err := backend.ExecContext(context.Background(), `PRAGMA ignore_check_constraints = ON`); err != nil {
			t.Fatalf("disable checks for corruption fixture: %v", err)
		}
	}
	insert := func(t *testing.T, backend *Backend, singleton, format, epoch, revision, hash any) {
		t.Helper()
		if _, err := backend.ExecContext(
			context.Background(),
			`INSERT INTO "godj_migration_revision" VALUES (?, ?, ?, ?, ?)`,
			singleton, format, epoch, revision, hash,
		); err != nil {
			t.Fatalf("insert corrupt metadata: %v", err)
		}
	}
	cases := []corruptionCase{
		{
			name: "view_object",
			setup: func(t *testing.T, backend *Backend) {
				if _, err := backend.ExecContext(context.Background(), createMigrationRecorderTableSQL); err != nil {
					t.Fatal(err)
				}
				if _, err := backend.ExecContext(context.Background(), `CREATE VIEW "godj_migration_revision" AS SELECT 1 AS singleton`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "metadata_without_recorder",
			setup: func(t *testing.T, backend *Backend) {
				if _, err := backend.ExecContext(context.Background(), createMigrationRevisionTableSQL); err != nil {
					t.Fatal(err)
				}
				insert(t, backend, 1, 1, validEpoch, 0, validHash[:])
			},
		},
		{
			name: "missing_column",
			setup: func(t *testing.T, backend *Backend) {
				if _, err := backend.ExecContext(context.Background(), createMigrationRecorderTableSQL); err != nil {
					t.Fatal(err)
				}
				if _, err := backend.ExecContext(context.Background(), `CREATE TABLE "godj_migration_revision" ("singleton" INTEGER PRIMARY KEY)`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "extra_column",
			setup: func(t *testing.T, backend *Backend) {
				if _, err := backend.ExecContext(context.Background(), createMigrationRecorderTableSQL); err != nil {
					t.Fatal(err)
				}
				if _, err := backend.ExecContext(context.Background(), `CREATE TABLE "godj_migration_revision" (`+
					`"singleton" INTEGER NOT NULL PRIMARY KEY, "format_version" INTEGER NOT NULL, `+
					`"epoch" BLOB NOT NULL, "revision" INTEGER NOT NULL, "history_fingerprint" BLOB NOT NULL, "extra" TEXT)`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "wrong_declared_type",
			setup: func(t *testing.T, backend *Backend) {
				if _, err := backend.ExecContext(context.Background(), createMigrationRecorderTableSQL); err != nil {
					t.Fatal(err)
				}
				if _, err := backend.ExecContext(context.Background(), `CREATE TABLE "godj_migration_revision" (`+
					`"singleton" INTEGER NOT NULL PRIMARY KEY, "format_version" TEXT NOT NULL, `+
					`"epoch" BLOB NOT NULL, "revision" INTEGER NOT NULL, "history_fingerprint" BLOB NOT NULL)`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "renamed_column",
			setup: func(t *testing.T, backend *Backend) {
				if _, err := backend.ExecContext(context.Background(), createMigrationRecorderTableSQL); err != nil {
					t.Fatal(err)
				}
				if _, err := backend.ExecContext(context.Background(), `CREATE TABLE "godj_migration_revision" (`+
					`"singleton" INTEGER NOT NULL PRIMARY KEY, "format_version" INTEGER NOT NULL, `+
					`"database_epoch" BLOB NOT NULL, "revision" INTEGER NOT NULL, "history_fingerprint" BLOB NOT NULL)`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "nullable_column",
			setup: func(t *testing.T, backend *Backend) {
				if _, err := backend.ExecContext(context.Background(), createMigrationRecorderTableSQL); err != nil {
					t.Fatal(err)
				}
				if _, err := backend.ExecContext(context.Background(), `CREATE TABLE "godj_migration_revision" (`+
					`"singleton" INTEGER NOT NULL PRIMARY KEY, "format_version" INTEGER NOT NULL, `+
					`"epoch" BLOB, "revision" INTEGER NOT NULL, "history_fingerprint" BLOB NOT NULL)`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "wrong_primary_key",
			setup: func(t *testing.T, backend *Backend) {
				if _, err := backend.ExecContext(context.Background(), createMigrationRecorderTableSQL); err != nil {
					t.Fatal(err)
				}
				if _, err := backend.ExecContext(context.Background(), `CREATE TABLE "godj_migration_revision" (`+
					`"singleton" INTEGER NOT NULL, "format_version" INTEGER NOT NULL, `+
					`"epoch" BLOB NOT NULL, "revision" INTEGER NOT NULL, "history_fingerprint" BLOB NOT NULL)`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:  "zero_rows",
			setup: func(t *testing.T, backend *Backend) { createReadyTables(t, backend) },
		},
		{
			name: "multiple_rows",
			setup: func(t *testing.T, backend *Backend) {
				createReadyTables(t, backend)
				insert(t, backend, 1, 1, validEpoch, 0, validHash[:])
				insert(t, backend, 2, 1, validEpoch, 0, validHash[:])
			},
		},
		{
			name: "singleton",
			setup: func(t *testing.T, backend *Backend) {
				createReadyTables(t, backend)
				insert(t, backend, 2, 1, validEpoch, 0, validHash[:])
			},
		},
		{
			name: "format_version",
			setup: func(t *testing.T, backend *Backend) {
				createReadyTables(t, backend)
				insert(t, backend, 1, 2, validEpoch, 0, validHash[:])
			},
		},
		{
			name: "format_type",
			setup: func(t *testing.T, backend *Backend) {
				createReadyTables(t, backend)
				insert(t, backend, 1, "bad", validEpoch, 0, validHash[:])
			},
		},
		{
			name: "epoch_type",
			setup: func(t *testing.T, backend *Backend) {
				createReadyTables(t, backend)
				insert(t, backend, 1, 1, strings.Repeat("e", 16), 0, validHash[:])
			},
		},
		{
			name: "epoch_short",
			setup: func(t *testing.T, backend *Backend) {
				createReadyTables(t, backend)
				insert(t, backend, 1, 1, make([]byte, 15), 0, validHash[:])
			},
		},
		{
			name: "epoch_long",
			setup: func(t *testing.T, backend *Backend) {
				createReadyTables(t, backend)
				insert(t, backend, 1, 1, make([]byte, 17), 0, validHash[:])
			},
		},
		{
			name: "revision_type",
			setup: func(t *testing.T, backend *Backend) {
				createReadyTables(t, backend)
				insert(t, backend, 1, 1, validEpoch, "bad", validHash[:])
			},
		},
		{
			name: "revision_negative",
			setup: func(t *testing.T, backend *Backend) {
				createReadyTables(t, backend)
				insert(t, backend, 1, 1, validEpoch, -1, validHash[:])
			},
		},
		{
			name: "fingerprint_type",
			setup: func(t *testing.T, backend *Backend) {
				createReadyTables(t, backend)
				insert(t, backend, 1, 1, validEpoch, 0, strings.Repeat("h", 32))
			},
		},
		{
			name: "fingerprint_short",
			setup: func(t *testing.T, backend *Backend) {
				createReadyTables(t, backend)
				insert(t, backend, 1, 1, validEpoch, 0, make([]byte, 31))
			},
		},
		{
			name: "fingerprint_long",
			setup: func(t *testing.T, backend *Backend) {
				createReadyTables(t, backend)
				insert(t, backend, 1, 1, validEpoch, 0, make([]byte, 33))
			},
		},
		{
			name: "fingerprint_mismatch",
			setup: func(t *testing.T, backend *Backend) {
				createReadyTables(t, backend)
				if _, err := backend.ExecContext(context.Background(), `INSERT INTO "godj_migrations" VALUES ('unknown', '0001')`); err != nil {
					t.Fatal(err)
				}
				insert(t, backend, 1, 1, validEpoch, 0, validHash[:])
			},
		},
	}
	for _, test := range cases {
		test := test
		t.Run(test.name, func(t *testing.T) {
			backend := openMigrationTestBackend(t)
			test.setup(t, backend)
			session := openLifecycleSession(t, backend)
			_, err := session.ReadAppliedMigrations(context.Background())
			assertRevisionFenceKind(t, err, migrationbackend.RevisionFenceFailureIntegrity)
			if err := session.Close(context.Background()); err != nil {
				t.Fatalf("Close(): %v", err)
			}
		})
	}
}

func TestSQLiteRevisionRecorderCorruptionFailsClosed(t *testing.T) {
	validEpoch := make([]byte, migrationRevisionEpochSize)
	validHash := fingerprintMigrationHistory(nil)
	type corruptionCase struct {
		name  string
		setup func(*testing.T, *Backend)
	}
	createMetadata := func(t *testing.T, backend *Backend, hash [32]byte) {
		t.Helper()
		if _, err := backend.ExecContext(context.Background(), createMigrationRevisionTableSQL); err != nil {
			t.Fatal(err)
		}
		if _, err := backend.ExecContext(
			context.Background(),
			`INSERT INTO "godj_migration_revision" VALUES (1, 1, ?, 0, ?)`,
			validEpoch,
			hash[:],
		); err != nil {
			t.Fatal(err)
		}
	}
	cases := []corruptionCase{
		{
			name: "view_object",
			setup: func(t *testing.T, backend *Backend) {
				if _, err := backend.ExecContext(context.Background(), `CREATE VIEW "godj_migrations" AS SELECT 'news' AS app, '0001' AS name`); err != nil {
					t.Fatal(err)
				}
				createMetadata(t, backend, validHash)
			},
		},
		{
			name: "missing_column",
			setup: func(t *testing.T, backend *Backend) {
				if _, err := backend.ExecContext(context.Background(), `CREATE TABLE "godj_migrations" ("app" VARCHAR(255) NOT NULL PRIMARY KEY)`); err != nil {
					t.Fatal(err)
				}
				createMetadata(t, backend, validHash)
			},
		},
		{
			name: "extra_column",
			setup: func(t *testing.T, backend *Backend) {
				if _, err := backend.ExecContext(context.Background(), `CREATE TABLE "godj_migrations" (`+
					`"app" VARCHAR(255) NOT NULL, "name" VARCHAR(255) NOT NULL, "extra" TEXT, PRIMARY KEY ("app", "name"))`); err != nil {
					t.Fatal(err)
				}
				createMetadata(t, backend, validHash)
			},
		},
		{
			name: "renamed_column",
			setup: func(t *testing.T, backend *Backend) {
				if _, err := backend.ExecContext(context.Background(), `CREATE TABLE "godj_migrations" (`+
					`"application" VARCHAR(255) NOT NULL, "name" VARCHAR(255) NOT NULL, PRIMARY KEY ("application", "name"))`); err != nil {
					t.Fatal(err)
				}
				createMetadata(t, backend, validHash)
			},
		},
		{
			name: "wrong_declared_type",
			setup: func(t *testing.T, backend *Backend) {
				if _, err := backend.ExecContext(context.Background(), `CREATE TABLE "godj_migrations" (`+
					`"app" TEXT NOT NULL, "name" VARCHAR(255) NOT NULL, PRIMARY KEY ("app", "name"))`); err != nil {
					t.Fatal(err)
				}
				createMetadata(t, backend, validHash)
			},
		},
		{
			name: "nullable_column",
			setup: func(t *testing.T, backend *Backend) {
				if _, err := backend.ExecContext(context.Background(), `CREATE TABLE "godj_migrations" (`+
					`"app" VARCHAR(255), "name" VARCHAR(255) NOT NULL, PRIMARY KEY ("app", "name"))`); err != nil {
					t.Fatal(err)
				}
				createMetadata(t, backend, validHash)
			},
		},
		{
			name: "wrong_primary_key",
			setup: func(t *testing.T, backend *Backend) {
				if _, err := backend.ExecContext(context.Background(), `CREATE TABLE "godj_migrations" (`+
					`"app" VARCHAR(255) NOT NULL, "name" VARCHAR(255) NOT NULL)`); err != nil {
					t.Fatal(err)
				}
				createMetadata(t, backend, validHash)
			},
		},
		{
			name: "collation_changes_identity",
			setup: func(t *testing.T, backend *Backend) {
				if _, err := backend.ExecContext(context.Background(), `CREATE TABLE "godj_migrations" (`+
					`"app" VARCHAR(255) COLLATE NOCASE NOT NULL, "name" VARCHAR(255) NOT NULL, PRIMARY KEY ("app", "name"))`); err != nil {
					t.Fatal(err)
				}
				createMetadata(t, backend, validHash)
			},
		},
		{
			name: "empty_identity",
			setup: func(t *testing.T, backend *Backend) {
				if _, err := backend.ExecContext(context.Background(), createMigrationRecorderTableSQL); err != nil {
					t.Fatal(err)
				}
				if _, err := backend.ExecContext(context.Background(), `INSERT INTO "godj_migrations" VALUES ('', '0001')`); err != nil {
					t.Fatal(err)
				}
				blankHash := fingerprintMigrationHistory([]migrationbackend.AppliedMigration{{Name: "0001"}})
				createMetadata(t, backend, blankHash)
			},
		},
	}
	for _, test := range cases {
		test := test
		t.Run(test.name, func(t *testing.T) {
			backend := openMigrationTestBackend(t)
			test.setup(t, backend)
			session := openLifecycleSession(t, backend)
			_, err := session.ReadAppliedMigrations(context.Background())
			assertRevisionFenceKind(t, err, migrationbackend.RevisionFenceFailureIntegrity)
			if err := session.Close(context.Background()); err != nil {
				t.Fatalf("Close(): %v", err)
			}
		})
	}
}

func TestSQLiteRevisionFenceRejectsOverflowBeforeMutation(t *testing.T) {
	ctx := context.Background()
	backend := openMigrationTestBackend(t)
	if _, err := backend.ExecContext(ctx, createMigrationRecorderTableSQL); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.ExecContext(ctx, createMigrationRevisionTableSQL); err != nil {
		t.Fatal(err)
	}
	hash := fingerprintMigrationHistory(nil)
	if _, err := backend.ExecContext(
		ctx,
		`INSERT INTO "godj_migration_revision" VALUES (1, 1, ?, ?, ?)`,
		make([]byte, migrationRevisionEpochSize), int64(math.MaxInt64), hash[:],
	); err != nil {
		t.Fatal(err)
	}
	session := openLifecycleSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatalf("ReadAppliedMigrations(): %v", err)
	}
	_, err := session.BeginMigration(ctx, migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}, emptySQLiteMigrationIntent())

	assertRevisionFenceKind(t, err, migrationbackend.RevisionFenceFailureIntegrity)
	if sqliteTableExists(t, backend, "overflow_domain") {
		t.Fatal("overflow attempt created domain table")
	}
}

func TestSQLiteRevisionFenceSameTokenContentionThenStale(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "contenders.sqlite")
	left := openLifecycleFileBackend(t, path, "&_busy_timeout=1")
	busy := openLifecycleFileBackend(t, path, "&_busy_timeout=1")
	stale := openLifecycleFileBackend(t, path, "&_busy_timeout=1")
	leftSession := openLifecycleSession(t, left)
	busySession := openLifecycleSession(t, busy)
	staleSession := openLifecycleSession(t, stale)
	for name, session := range map[string]migrationbackend.RevisionFencedSession{
		"left": leftSession, "busy": busySession, "stale": staleSession,
	} {
		if _, err := session.ReadAppliedMigrations(ctx); err != nil {
			t.Fatalf("%s snapshot: %v", name, err)
		}
	}
	transition := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}
	leftTransaction := beginLifecycleTransaction(t, leftSession, transition, createModelMigrationIntent(migrationTestModel(false)))
	if _, err := busySession.BeginMigration(ctx, transition, emptySQLiteMigrationIntent()); err == nil {
		t.Fatal("same-token contender unexpectedly began")
	} else {
		assertRevisionFenceKind(t, err, migrationbackend.RevisionFenceFailureContended)
	}
	if err := leftTransaction.CreateModel(ctx, migrationTestModel(false)); err != nil {
		t.Fatal(err)
	}
	if err := leftTransaction.RecordApplied(ctx, "news", "0001"); err != nil {
		t.Fatal(err)
	}
	if outcome, err := leftTransaction.CommitFenced(ctx); err != nil || outcome.Durability != migrationbackend.CommitCommitted {
		t.Fatalf("winner commit = (%+v, %v)", outcome, err)
	}
	if _, err := staleSession.BeginMigration(ctx, transition, emptySQLiteMigrationIntent()); err == nil {
		t.Fatal("old snapshot began after winner commit")
	} else {
		assertRevisionFenceKind(t, err, migrationbackend.RevisionFenceFailureStale)
	}
	shape, err := readAtomicMigrationRevisionSnapshot(ctx, left)
	if err != nil || shape.token.revision != 1 || len(shape.records) != 1 {
		t.Fatalf("winner durable shape = (%+v, %v)", shape, err)
	}
	for _, session := range []migrationbackend.RevisionFencedSession{leftSession, busySession, staleSession} {
		if err := session.Close(ctx); err != nil {
			t.Fatalf("Close(): %v", err)
		}
	}
}

func TestSQLiteRevisionFenceRejectsFingerprintABAWithHigherRevision(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aba.sqlite")
	backend := openLifecycleFileBackend(t, path, "&_busy_timeout=1")
	active := openLifecycleSession(t, backend)
	if _, err := active.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	first := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}
	transaction := beginLifecycleTransaction(t, active, first)
	if err := transaction.RecordApplied(ctx, "news", "0001"); err != nil {
		t.Fatal(err)
	}
	if outcome, err := transaction.CommitFenced(ctx); err != nil || outcome.Durability != migrationbackend.CommitCommitted {
		t.Fatalf("first commit = (%+v, %v)", outcome, err)
	}
	stale := openLifecycleSession(t, backend)
	if _, err := stale.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	before, err := readAtomicMigrationRevisionSnapshot(ctx, backend)
	if err != nil {
		t.Fatal(err)
	}
	second := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0002"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}
	transaction = beginLifecycleTransaction(t, active, second)
	if err := transaction.RecordApplied(ctx, "news", "0002"); err != nil {
		t.Fatal(err)
	}
	if outcome, err := transaction.CommitFenced(ctx); err != nil || outcome.Durability != migrationbackend.CommitCommitted {
		t.Fatalf("ABA apply = (%+v, %v)", outcome, err)
	}
	transaction = beginLifecycleTransaction(t, active, migrationbackend.HistoryTransition{
		Migration: second.Migration,
		Kind:      migrationbackend.HistoryTransitionUnapply,
	})
	if err := transaction.RecordUnapplied(ctx, "news", "0002"); err != nil {
		t.Fatal(err)
	}
	if outcome, err := transaction.CommitFenced(ctx); err != nil || outcome.Durability != migrationbackend.CommitCommitted {
		t.Fatalf("ABA unapply = (%+v, %v)", outcome, err)
	}
	after, err := readAtomicMigrationRevisionSnapshot(ctx, backend)
	if err != nil {
		t.Fatal(err)
	}
	if !equalAppliedMigrations(before.records, after.records) || before.token.fingerprint != after.token.fingerprint {
		t.Fatalf("fixture did not restore identical identity fingerprint: before=%+v after=%+v", before, after)
	}
	if before.token.revision != 1 || after.token.revision != 3 {
		t.Fatalf("ABA revisions = %d -> %d, want 1 -> 3", before.token.revision, after.token.revision)
	}
	if _, err := stale.BeginMigration(ctx, second, emptySQLiteMigrationIntent()); err == nil {
		t.Fatal("stale pre-ABA token began after identities returned to the same fingerprint")
	} else {
		assertRevisionFenceKind(t, err, migrationbackend.RevisionFenceFailureStale)
	}
	final, err := readAtomicMigrationRevisionSnapshot(ctx, backend)
	if err != nil || !equalMigrationRevisionToken(after.token, final.token) || !equalAppliedMigrations(after.records, final.records) {
		t.Fatalf("stale ABA attempt mutated history: after=%+v final=%+v err=%v", after, final, err)
	}
}

const (
	revisionFenceProcessHelperEnv = "GODJ_SQLITE_REVISION_FENCE_PROCESS_HELPER"
	revisionFenceProcessPathEnv   = "GODJ_SQLITE_REVISION_FENCE_PROCESS_PATH"
	revisionFenceProcessDirEnv    = "GODJ_SQLITE_REVISION_FENCE_PROCESS_DIR"
	revisionFenceProcessIDEnv     = "GODJ_SQLITE_REVISION_FENCE_PROCESS_ID"
)

func TestSQLiteRevisionFenceTwoProcessSingleWinnerAndReopen(t *testing.T) {
	if os.Getenv(revisionFenceProcessHelperEnv) != "" {
		t.Skip("parent-only process coordinator")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable(): %v", err)
	}
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "two-process.sqlite")
	startPath := filepath.Join(directory, "start")
	releasePath := filepath.Join(directory, "release")

	processCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	type childProcess struct {
		id     string
		cmd    *exec.Cmd
		output bytes.Buffer
	}
	children := []*childProcess{{id: "left"}, {id: "right"}}
	for _, child := range children {
		child.cmd = exec.CommandContext(
			processCtx,
			executable,
			"-test.run=^TestSQLiteRevisionFenceProcessHelper$",
			"-test.count=1",
		)
		child.cmd.Env = append(
			os.Environ(),
			revisionFenceProcessHelperEnv+"=1",
			revisionFenceProcessPathEnv+"="+databasePath,
			revisionFenceProcessDirEnv+"="+directory,
			revisionFenceProcessIDEnv+"="+child.id,
		)
		child.cmd.Stdout = &child.output
		child.cmd.Stderr = &child.output
		if err := child.cmd.Start(); err != nil {
			t.Fatalf("start %s helper: %v", child.id, err)
		}
		defer func(child *childProcess) {
			if child.cmd.Process != nil {
				_ = child.cmd.Process.Kill()
			}
		}(child)
	}
	waitForRevisionFenceProcessCondition(t, 15*time.Second, func() bool {
		return revisionFenceProcessMarkerExists(directory, "ready-left") &&
			revisionFenceProcessMarkerExists(directory, "ready-right")
	}, "both helpers to take the same fresh snapshot")
	writeRevisionFenceProcessMarker(t, startPath, "start")
	defer func() {
		_ = os.WriteFile(releasePath, []byte("release"), 0o600)
	}()
	waitForRevisionFenceProcessCondition(t, 15*time.Second, func() bool {
		claims := 0
		contended := 0
		for _, child := range children {
			if revisionFenceProcessMarkerExists(directory, "claimed-"+child.id) {
				claims++
			}
			if string(readRevisionFenceProcessMarker(directory, "result-"+child.id)) == "contended" {
				contended++
			}
		}
		return claims == 1 && contended == 1
	}, "one helper claim and one BUSY/LOCKED contender before release")
	writeRevisionFenceProcessMarker(t, releasePath, "release")
	for _, child := range children {
		if err := child.cmd.Wait(); err != nil {
			t.Fatalf("%s helper failed: %v\n%s", child.id, err, child.output.String())
		}
	}
	results := map[string]int{}
	for _, child := range children {
		result := string(readRevisionFenceProcessMarker(directory, "result-"+child.id))
		results[result]++
	}
	if results["committed"] != 1 || results["contended"] != 1 || len(results) != 2 {
		t.Fatalf("two-process results = %v, want one committed and one contended", results)
	}

	ctx := context.Background()
	backend := openLifecycleFileBackend(t, databasePath, "&_busy_timeout=1")
	session := openLifecycleSession(t, backend)
	records, err := session.ReadAppliedMigrations(ctx)
	want := []migrationbackend.AppliedMigration{{App: "news", Name: "0001"}}
	if err != nil || !reflect.DeepEqual(records, want) {
		t.Fatalf("reopened process winner history = (%v, %v), want %v", records, err, want)
	}
	shape, err := readAtomicMigrationRevisionSnapshot(ctx, backend)
	if err != nil || shape.token.revision != 1 {
		t.Fatalf("reopened process winner revision = (%+v, %v)", shape, err)
	}
	second := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0002"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}
	transaction := beginLifecycleTransaction(t, session, second)
	if err := transaction.RecordApplied(ctx, "news", "0002"); err != nil {
		t.Fatal(err)
	}
	if outcome, err := transaction.CommitFenced(ctx); err != nil || outcome.Durability != migrationbackend.CommitCommitted {
		t.Fatalf("reopened second commit = (%+v, %v)", outcome, err)
	}
	shape, err = readAtomicMigrationRevisionSnapshot(ctx, backend)
	if err != nil || shape.token.revision != 2 || len(shape.records) != 2 {
		t.Fatalf("reopened successor = (%+v, %v)", shape, err)
	}
}

func TestSQLiteRevisionFenceProcessHelper(t *testing.T) {
	if os.Getenv(revisionFenceProcessHelperEnv) == "" {
		return
	}
	databasePath := os.Getenv(revisionFenceProcessPathEnv)
	directory := os.Getenv(revisionFenceProcessDirEnv)
	id := os.Getenv(revisionFenceProcessIDEnv)
	if databasePath == "" || directory == "" || id == "" {
		t.Fatal("process helper environment is incomplete")
	}
	ctx := context.Background()
	backend := openLifecycleFileBackend(t, databasePath, "&_busy_timeout=1")
	session := openLifecycleSession(t, backend)
	if records, err := session.ReadAppliedMigrations(ctx); err != nil || len(records) != 0 {
		t.Fatalf("process %s initial snapshot = (%v, %v)", id, records, err)
	}
	writeRevisionFenceProcessMarker(t, filepath.Join(directory, "ready-"+id), "ready")
	waitForRevisionFenceProcessCondition(t, 20*time.Second, func() bool {
		return revisionFenceProcessMarkerExists(directory, "start")
	}, "parent process start barrier")
	transition := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}
	transaction, err := session.BeginMigration(ctx, transition, emptySQLiteMigrationIntent())
	if err != nil {
		assertRevisionFenceKind(t, err, migrationbackend.RevisionFenceFailureContended)
		if closeErr := session.Close(ctx); closeErr != nil {
			t.Fatalf("process %s close after contention: %v", id, closeErr)
		}
		writeRevisionFenceProcessMarker(t, filepath.Join(directory, "result-"+id), "contended")
		return
	}
	writeRevisionFenceProcessMarker(t, filepath.Join(directory, "claimed-"+id), "claimed")
	waitForRevisionFenceProcessCondition(t, 20*time.Second, func() bool {
		return revisionFenceProcessMarkerExists(directory, "release")
	}, "parent process release barrier")
	if err := transaction.RecordApplied(ctx, "news", "0001"); err != nil {
		t.Fatalf("process %s RecordApplied(): %v", id, err)
	}
	if outcome, err := transaction.CommitFenced(ctx); err != nil || outcome.Durability != migrationbackend.CommitCommitted {
		t.Fatalf("process %s CommitFenced() = (%+v, %v)", id, outcome, err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatalf("process %s Close(): %v", id, err)
	}
	writeRevisionFenceProcessMarker(t, filepath.Join(directory, "result-"+id), "committed")
}

func waitForRevisionFenceProcessCondition(t *testing.T, timeout time.Duration, condition func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}

func revisionFenceProcessMarkerExists(directory, name string) bool {
	_, err := os.Stat(filepath.Join(directory, name))
	return err == nil
}

func readRevisionFenceProcessMarker(directory, name string) []byte {
	content, _ := os.ReadFile(filepath.Join(directory, name))
	return content
}

func writeRevisionFenceProcessMarker(t *testing.T, path, value string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatalf("write process marker %s: %v", filepath.Base(path), err)
	}
}

func TestSQLiteLegacyAndFencedWritersCannotCrossCutover(t *testing.T) {
	t.Run("fenced_wins", func(t *testing.T) {
		ctx := context.Background()
		path := filepath.Join(t.TempDir(), "fenced-wins.sqlite")
		fenced := openLifecycleFileBackend(t, path, "&_busy_timeout=1")
		legacy := openLifecycleFileBackend(t, path, "&_busy_timeout=1")
		session := openLifecycleSession(t, fenced)
		if _, err := session.ReadAppliedMigrations(ctx); err != nil {
			t.Fatal(err)
		}
		transition := migrationbackend.HistoryTransition{
			Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001"},
			Kind:      migrationbackend.HistoryTransitionApply,
		}
		transaction := beginLifecycleTransaction(t, session, transition)
		if _, err := legacy.BeginMigration(ctx); err == nil {
			t.Fatal("legacy writer crossed active fenced bootstrap")
		} else {
			assertRevisionFenceKind(t, err, migrationbackend.RevisionFenceFailureContended)
		}
		if err := transaction.RecordApplied(ctx, "news", "0001"); err != nil {
			t.Fatal(err)
		}
		if outcome, err := transaction.CommitFenced(ctx); err != nil || outcome.Durability != migrationbackend.CommitCommitted {
			t.Fatalf("fenced commit = (%+v, %v)", outcome, err)
		}
		if _, err := legacy.BeginMigration(ctx); err == nil || !migrationbackend.IsCapabilityError(err) {
			t.Fatalf("legacy writer after cutover error = %v, want capability", err)
		}
	})

	t.Run("legacy_wins", func(t *testing.T) {
		ctx := context.Background()
		path := filepath.Join(t.TempDir(), "legacy-wins.sqlite")
		fenced := openLifecycleFileBackend(t, path, "&_busy_timeout=1")
		legacy := openLifecycleFileBackend(t, path, "&_busy_timeout=1")
		session := openLifecycleSession(t, fenced)
		if _, err := session.ReadAppliedMigrations(ctx); err != nil {
			t.Fatal(err)
		}
		legacyTransaction, err := legacy.BeginMigration(ctx)
		if err != nil {
			t.Fatalf("legacy begin before cutover: %v", err)
		}
		transition := migrationbackend.HistoryTransition{
			Migration: migrationbackend.AppliedMigration{App: "legacy", Name: "0001"},
			Kind:      migrationbackend.HistoryTransitionApply,
		}
		if _, err := session.BeginMigration(ctx, transition, emptySQLiteMigrationIntent()); err == nil {
			t.Fatal("fenced writer crossed active legacy transaction")
		} else {
			assertRevisionFenceKind(t, err, migrationbackend.RevisionFenceFailureContended)
		}
		if err := legacyTransaction.RecordApplied(ctx, "legacy", "0001"); err != nil {
			t.Fatal(err)
		}
		if err := legacyTransaction.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		freshSession := openLifecycleSession(t, fenced)
		_, err = freshSession.ReadAppliedMigrations(ctx)
		assertRevisionFenceKind(t, err, migrationbackend.RevisionFenceFailureAdoptionRequired)
	})
}

func TestSQLiteRevisionFencedCommitOutcomes(t *testing.T) {
	t.Run("committed_with_close_error", func(t *testing.T) {
		ctx := context.Background()
		backend := openMigrationTestBackend(t)
		session := openLifecycleSession(t, backend)
		if _, err := session.ReadAppliedMigrations(ctx); err != nil {
			t.Fatal(err)
		}
		transition := migrationbackend.HistoryTransition{
			Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001"},
			Kind:      migrationbackend.HistoryTransitionApply,
		}
		transaction := beginLifecycleTransaction(t, session, transition)
		concrete := transaction.(*sqliteRevisionFencedTransaction)
		closeFailure := errors.New("close failure")
		concrete.connection = &closeErrorPinnedConnection{migrationPinnedConnection: concrete.connection, err: closeFailure}
		if err := transaction.RecordApplied(ctx, "news", "0001"); err != nil {
			t.Fatal(err)
		}
		outcome, err := transaction.CommitFenced(ctx)
		if outcome.Durability != migrationbackend.CommitCommitted || !errors.Is(err, closeFailure) {
			t.Fatalf("CommitFenced() = (%+v, %v), want committed/close failure", outcome, err)
		}
		shape, snapshotErr := readAtomicMigrationRevisionSnapshot(ctx, backend)
		if snapshotErr != nil || shape.token.revision != 1 {
			t.Fatalf("committed shape = (%+v, %v)", shape, snapshotErr)
		}
	})

	t.Run("committed_close_failure_discards_unreleased_connection", func(t *testing.T) {
		ctx := context.Background()
		path := filepath.Join(t.TempDir(), "commit-close.sqlite")
		backend := openLifecycleFileBackend(t, path, "")
		session := openLifecycleSession(t, backend)
		if _, err := session.ReadAppliedMigrations(ctx); err != nil {
			t.Fatal(err)
		}
		transition := migrationbackend.HistoryTransition{
			Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001"},
			Kind:      migrationbackend.HistoryTransitionApply,
		}
		transaction := beginLifecycleTransaction(t, session, transition)
		concrete := transaction.(*sqliteRevisionFencedTransaction)
		closeFailure := errors.New("close without release")
		concrete.connection = &closeWithoutReleasePinnedConnection{
			migrationPinnedConnection: concrete.connection,
			err:                       closeFailure,
		}
		if err := transaction.RecordApplied(ctx, "news", "0001"); err != nil {
			t.Fatal(err)
		}
		outcome, err := transaction.CommitFenced(ctx)
		if outcome.Durability != migrationbackend.CommitCommitted || !errors.Is(err, closeFailure) {
			t.Fatalf("CommitFenced() = (%+v, %v), want committed/close failure", outcome, err)
		}
		if stats := backend.database.Stats(); stats.InUse != 0 {
			t.Fatalf("database in-use connections = %d, want 0", stats.InUse)
		}
		shape, snapshotErr := readAtomicMigrationRevisionSnapshot(ctx, backend)
		if snapshotErr != nil || shape.token.revision != 1 || len(shape.records) != 1 {
			t.Fatalf("committed shape after discard = (%+v, %v)", shape, snapshotErr)
		}
	})

	t.Run("rolled_back_after_literal_commit_failure", func(t *testing.T) {
		ctx := context.Background()
		path := filepath.Join(t.TempDir(), "commit-failure.sqlite")
		backend := openLifecycleFileBackend(t, path, "&_pragma=foreign_keys(1)")
		for _, statement := range []string{
			`CREATE TABLE "parent" ("id" INTEGER PRIMARY KEY)`,
			`CREATE TABLE "child" ("parent_id" INTEGER, FOREIGN KEY ("parent_id") REFERENCES "parent" ("id") DEFERRABLE INITIALLY DEFERRED)`,
		} {
			if _, err := backend.ExecContext(ctx, statement); err != nil {
				t.Fatal(err)
			}
		}
		session := openLifecycleSession(t, backend)
		if _, err := session.ReadAppliedMigrations(ctx); err != nil {
			t.Fatal(err)
		}
		transition := migrationbackend.HistoryTransition{
			Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001"},
			Kind:      migrationbackend.HistoryTransitionApply,
		}
		transaction := beginLifecycleTransaction(t, session, transition)
		concrete := transaction.(*sqliteRevisionFencedTransaction)
		if _, err := concrete.connection.ExecContext(ctx, `INSERT INTO "child" VALUES (404)`); err != nil {
			t.Fatal(err)
		}
		if err := transaction.RecordApplied(ctx, "news", "0001"); err != nil {
			t.Fatal(err)
		}
		outcome, err := transaction.CommitFenced(ctx)
		if err == nil || outcome.Durability != migrationbackend.CommitRolledBack {
			t.Fatalf("CommitFenced() = (%+v, %v), want rolled back error", outcome, err)
		}
		if sqliteTableExists(t, backend, migrationRevisionTable) || sqliteTableExists(t, backend, migrationRecorderTable) {
			t.Fatal("failed COMMIT preserved bootstrap metadata or recorder")
		}
		var rows int
		if err := backend.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM "child"`).Scan(&rows); err != nil || rows != 0 {
			t.Fatalf("child rows = %d err=%v", rows, err)
		}
	})

	t.Run("unknown_after_external_commit", func(t *testing.T) {
		ctx := context.Background()
		backend := openLifecycleFileBackend(t, filepath.Join(t.TempDir(), "unknown.sqlite"), "")
		session := openLifecycleSession(t, backend)
		if _, err := session.ReadAppliedMigrations(ctx); err != nil {
			t.Fatal(err)
		}
		transition := migrationbackend.HistoryTransition{
			Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001"},
			Kind:      migrationbackend.HistoryTransitionApply,
		}
		transaction := beginLifecycleTransaction(t, session, transition)
		concrete := transaction.(*sqliteRevisionFencedTransaction)
		if err := transaction.RecordApplied(ctx, "news", "0001"); err != nil {
			t.Fatal(err)
		}
		if _, err := concrete.connection.ExecContext(ctx, "COMMIT"); err != nil {
			t.Fatalf("external commit fixture: %v", err)
		}
		outcome, err := transaction.CommitFenced(ctx)
		if err == nil || outcome.Durability != migrationbackend.CommitUnknown {
			t.Fatalf("CommitFenced() = (%+v, %v), want unknown", outcome, err)
		}
		if stats := backend.database.Stats(); stats.InUse != 0 {
			t.Fatalf("database in-use connections = %d, want 0", stats.InUse)
		}
		shape, snapshotErr := readAtomicMigrationRevisionSnapshot(ctx, backend)
		if snapshotErr != nil || shape.token.revision != 1 || len(shape.records) != 1 {
			t.Fatalf("unknown durable observation = (%+v, %v)", shape, snapshotErr)
		}
	})

	t.Run("rollback_close_failure_discards_unreleased_connection", func(t *testing.T) {
		ctx := context.Background()
		path := filepath.Join(t.TempDir(), "rollback-close.sqlite")
		backend := openLifecycleFileBackend(t, path, "")
		session := openLifecycleSession(t, backend)
		if _, err := session.ReadAppliedMigrations(ctx); err != nil {
			t.Fatal(err)
		}
		transition := migrationbackend.HistoryTransition{
			Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001"},
			Kind:      migrationbackend.HistoryTransitionApply,
		}
		transaction := beginLifecycleTransaction(t, session, transition, createModelMigrationIntent(migrationTestModel(false)))
		if err := transaction.CreateModel(ctx, migrationTestModel(false)); err != nil {
			t.Fatal(err)
		}
		concrete := transaction.(*sqliteRevisionFencedTransaction)
		closeFailure := errors.New("rollback close without release")
		concrete.connection = &closeWithoutReleasePinnedConnection{
			migrationPinnedConnection: concrete.connection,
			err:                       closeFailure,
		}
		err := transaction.Rollback(ctx)
		if !errors.Is(err, closeFailure) {
			t.Fatalf("Rollback() error = %v, want close failure", err)
		}
		if stats := backend.database.Stats(); stats.InUse != 0 {
			t.Fatalf("database in-use connections = %d, want 0", stats.InUse)
		}
		if sqliteTableExists(t, backend, migrationRevisionTable) || sqliteTableExists(t, backend, "godj_migration_article") {
			t.Fatal("rollback close failure preserved uncommitted mutation")
		}
	})
}

func TestSQLiteRevisionSessionCloseRollsBackAbandonedTransactionWithCanceledContext(t *testing.T) {
	ctx := context.Background()
	backend := openMigrationTestBackend(t)
	session := openLifecycleSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	transition := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}
	transaction := beginLifecycleTransaction(t, session, transition, createModelMigrationIntent(migrationTestModel(false)))
	if err := transaction.CreateModel(ctx, migrationTestModel(false)); err != nil {
		t.Fatal(err)
	}
	if err := transaction.RecordApplied(ctx, "news", "0001"); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := session.Close(canceled); err != nil {
		t.Fatalf("Close(canceled): %v", err)
	}
	if sqliteTableExists(t, backend, migrationRevisionTable) ||
		sqliteTableExists(t, backend, migrationRecorderTable) ||
		sqliteTableExists(t, backend, "godj_migration_article") {
		t.Fatal("abandoned transaction survived session Close")
	}
	if stats := backend.database.Stats(); stats.InUse != 0 {
		t.Fatalf("database in-use connections = %d, want 0", stats.InUse)
	}
	// A fresh lifecycle proves the only pooled connection was released.
	fresh := openLifecycleSession(t, backend)
	if records, err := fresh.ReadAppliedMigrations(ctx); err != nil || len(records) != 0 {
		t.Fatalf("fresh snapshot after cleanup = (%v, %v)", records, err)
	}
}

func TestSQLiteManualConnectionCloseLeaksAndDiscardRollsBack(t *testing.T) {
	t.Run("normal_close_leaks", func(t *testing.T) {
		ctx := context.Background()
		backend := openMigrationTestBackend(t)
		connection, err := backend.database.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
			t.Fatal(err)
		}
		if _, err := connection.ExecContext(ctx, `CREATE TABLE "leaked_manual_transaction" ("id" INTEGER)`); err != nil {
			t.Fatal(err)
		}
		if err := connection.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := backend.database.ExecContext(ctx, "COMMIT"); err != nil {
			t.Fatalf("next borrower could not commit leaked transaction: %v", err)
		}
		if !sqliteTableExists(t, backend, "leaked_manual_transaction") {
			t.Fatal("fixture did not prove that normal Close returned an open transaction")
		}
	})

	t.Run("bad_conn_discard_rolls_back", func(t *testing.T) {
		ctx := context.Background()
		backend := openMigrationTestBackend(t)
		connection, err := backend.database.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
			t.Fatal(err)
		}
		if _, err := connection.ExecContext(ctx, `CREATE TABLE "discarded_manual_transaction" ("id" INTEGER)`); err != nil {
			t.Fatal(err)
		}
		if err := discardMigrationConnection(connection); err != nil {
			t.Fatalf("discardMigrationConnection(): %v", err)
		}
		if sqliteTableExists(t, backend, "discarded_manual_transaction") {
			t.Fatal("discarded physical connection preserved uncommitted DDL")
		}
		if stats := backend.database.Stats(); stats.InUse != 0 {
			t.Fatalf("database in-use connections = %d, want 0", stats.InUse)
		}
	})
}

func TestSQLiteRevisionIOClassifiesBusyAndLockedAtLiveCallSites(t *testing.T) {
	ctx := context.Background()
	transition := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}

	t.Run("atomic_snapshot", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "snapshot.sqlite")
		locker := openLifecycleFileBackend(t, path, "&_busy_timeout=1")
		reader := openLifecycleFileBackend(t, path, "&_busy_timeout=1")
		connection, err := locker.database.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := connection.ExecContext(ctx, "BEGIN EXCLUSIVE"); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := rollbackAndReleasePinnedMigration(ctx, connection); err != nil {
				t.Errorf("release snapshot lock: %v", err)
			}
		}()
		session := openLifecycleSession(t, reader)
		_, err = session.ReadAppliedMigrations(ctx)
		assertRevisionFenceKind(t, err, migrationbackend.RevisionFenceFailureContended)
		if err := session.Close(ctx); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("metadata_and_history_reads", func(t *testing.T) {
		backend := openMigrationTestBackend(t)
		bootstrapSQLiteRevisionHistory(t, backend, transition)
		tests := []struct {
			name     string
			contains string
			code     int
		}{
			{name: "metadata_shape", contains: `PRAGMA table_info("godj_migration_revision")`, code: 5 | 0x200},
			{name: "history", contains: `FROM "godj_migrations"`, code: 6 | 0x300},
			{name: "metadata_row", contains: `FROM "godj_migration_revision"`, code: 5},
		}
		for _, test := range tests {
			test := test
			t.Run(test.name, func(t *testing.T) {
				readTransaction, err := backend.database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				fault := &migrationSQLFault{method: "query", contains: test.contains, code: test.code, remaining: 1}
				executor := &faultingMigrationSQLExecutor{migrationSQLExecutor: readTransaction, fault: fault}
				_, err = inspectMigrationRevisionSnapshot(ctx, executor)
				assertRevisionFenceKind(t, err, migrationbackend.RevisionFenceFailureContended)
				if rollbackErr := readTransaction.Rollback(); rollbackErr != nil {
					t.Fatalf("rollback read fixture: %v", rollbackErr)
				}
				if fault.remainingCount() != 0 {
					t.Fatal("fault did not pass through the intended read call-site")
				}
			})
		}
	})

	t.Run("revision_claim", func(t *testing.T) {
		backend := openMigrationTestBackend(t)
		bootstrapSQLiteRevisionHistory(t, backend, transition)
		before, err := readAtomicMigrationRevisionSnapshot(ctx, backend)
		if err != nil {
			t.Fatal(err)
		}
		second := migrationbackend.HistoryTransition{
			Migration: migrationbackend.AppliedMigration{App: "news", Name: "0002"},
			Kind:      migrationbackend.HistoryTransitionApply,
		}
		successorRecords, err := migrationHistorySuccessor(before.records, second)
		if err != nil {
			t.Fatal(err)
		}
		successorToken := before.token
		successorToken.revision++
		successorToken.fingerprint = fingerprintMigrationHistory(successorRecords)
		connection, err := backend.database.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
			t.Fatal(err)
		}
		fault := &migrationSQLFault{method: "exec", contains: `UPDATE "godj_migration_revision"`, code: 6, remaining: 1}
		pinned := &faultingMigrationPinnedConnection{migrationPinnedConnection: connection, fault: fault}
		candidate := &sqliteRevisionFencedTransaction{
			connection:       pinned,
			transition:       second,
			expectedRecords:  cloneAppliedMigrations(before.records),
			successorRecords: cloneAppliedMigrations(successorRecords),
			expectedToken:    before.token,
			successorToken:   successorToken,
		}
		err = candidate.claimRevision(ctx)
		assertRevisionFenceKind(t, err, migrationbackend.RevisionFenceFailureContended)
		if err := candidate.rollbackWithoutSession(ctx); err != nil {
			t.Fatalf("rollback failed claim: %v", err)
		}
		after, err := readAtomicMigrationRevisionSnapshot(ctx, backend)
		if err != nil || !equalMigrationRevisionToken(before.token, after.token) || !equalAppliedMigrations(before.records, after.records) {
			t.Fatalf("failed claim mutated history: before=%+v after=%+v err=%v", before, after, err)
		}
	})

	for _, test := range []struct {
		name        string
		method      string
		contains    string
		code        int
		prepare     func(*testing.T, migrationbackend.RevisionFencedTransaction)
		invoke      func(migrationbackend.RevisionFencedTransaction) (migrationbackend.CommitOutcome, error)
		wantOutcome migrationbackend.CommitDurability
	}{
		{
			name:   "domain",
			method: "exec", contains: `CREATE TABLE "godj_migration_article"`, code: 5,
			invoke: func(transaction migrationbackend.RevisionFencedTransaction) (migrationbackend.CommitOutcome, error) {
				err := transaction.CreateModel(ctx, migrationTestModel(false))
				if err == nil {
					return migrationbackend.CommitOutcome{}, errors.New("domain fault was not injected")
				}
				assertRevisionFenceKind(t, err, migrationbackend.RevisionFenceFailureContended)
				return transaction.CommitFenced(ctx)
			},
			wantOutcome: migrationbackend.CommitRolledBack,
		},
		{
			name:   "recorder",
			method: "exec", contains: `INSERT INTO "godj_migrations"`, code: 6,
			prepare: func(t *testing.T, transaction migrationbackend.RevisionFencedTransaction) {
				if err := transaction.CreateModel(ctx, migrationTestModel(false)); err != nil {
					t.Fatal(err)
				}
			},
			invoke: func(transaction migrationbackend.RevisionFencedTransaction) (migrationbackend.CommitOutcome, error) {
				err := transaction.RecordApplied(ctx, "news", "0001")
				if err == nil {
					return migrationbackend.CommitOutcome{}, errors.New("recorder fault was not injected")
				}
				assertRevisionFenceKind(t, err, migrationbackend.RevisionFenceFailureContended)
				return transaction.CommitFenced(ctx)
			},
			wantOutcome: migrationbackend.CommitRolledBack,
		},
		{
			name:   "final_verification",
			method: "query", contains: `FROM "godj_migrations"`, code: 5 | 0x200,
			prepare: func(t *testing.T, transaction migrationbackend.RevisionFencedTransaction) {
				if err := transaction.RecordApplied(ctx, "news", "0001"); err != nil {
					t.Fatal(err)
				}
			},
			invoke: func(transaction migrationbackend.RevisionFencedTransaction) (migrationbackend.CommitOutcome, error) {
				return transaction.CommitFenced(ctx)
			},
			wantOutcome: migrationbackend.CommitRolledBack,
		},
		{
			name:   "commit",
			method: "exec", contains: "COMMIT", code: 6 | 0x300,
			prepare: func(t *testing.T, transaction migrationbackend.RevisionFencedTransaction) {
				if err := transaction.RecordApplied(ctx, "news", "0001"); err != nil {
					t.Fatal(err)
				}
			},
			invoke: func(transaction migrationbackend.RevisionFencedTransaction) (migrationbackend.CommitOutcome, error) {
				return transaction.CommitFenced(ctx)
			},
			wantOutcome: migrationbackend.CommitRolledBack,
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), test.name+".sqlite")
			backend := openLifecycleFileBackend(t, path, "")
			session := openLifecycleSession(t, backend)
			if _, err := session.ReadAppliedMigrations(ctx); err != nil {
				t.Fatal(err)
			}
			intent := emptySQLiteMigrationIntent()
			if test.name == "domain" || test.name == "recorder" {
				intent = createModelMigrationIntent(migrationTestModel(false))
			}
			transaction := beginLifecycleTransaction(t, session, transition, intent)
			if test.prepare != nil {
				test.prepare(t, transaction)
			}
			fault := &migrationSQLFault{method: test.method, contains: test.contains, code: test.code, remaining: 1}
			installMigrationTransactionFault(transaction, fault)
			outcome, err := test.invoke(transaction)
			if outcome.Durability != test.wantOutcome {
				t.Fatalf("outcome = %+v, want durability %d", outcome, test.wantOutcome)
			}
			assertRevisionFenceKind(t, err, migrationbackend.RevisionFenceFailureContended)
			if fault.remainingCount() != 0 {
				t.Fatal("fault did not pass through the intended transaction call-site")
			}
			for _, table := range []string{migrationRevisionTable, migrationRecorderTable, "godj_migration_article"} {
				if sqliteTableExists(t, backend, table) {
					t.Fatalf("%s fault preserved table %q", test.name, table)
				}
			}
			if stats := backend.database.Stats(); stats.InUse != 0 {
				t.Fatalf("database in-use connections = %d, want 0", stats.InUse)
			}
		})
	}

	t.Run("rollback_discard", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "rollback.sqlite")
		backend := openLifecycleFileBackend(t, path, "")
		session := openLifecycleSession(t, backend)
		if _, err := session.ReadAppliedMigrations(ctx); err != nil {
			t.Fatal(err)
		}
		transaction := beginLifecycleTransaction(t, session, transition, createModelMigrationIntent(migrationTestModel(false)))
		if err := transaction.CreateModel(ctx, migrationTestModel(false)); err != nil {
			t.Fatal(err)
		}
		fault := &migrationSQLFault{method: "exec", contains: "ROLLBACK", code: 5, remaining: 1}
		installMigrationTransactionFault(transaction, fault)
		err := transaction.Rollback(ctx)
		assertRevisionFenceKind(t, err, migrationbackend.RevisionFenceFailureContended)
		if fault.remainingCount() != 0 {
			t.Fatal("rollback fault was not injected")
		}
		if stats := backend.database.Stats(); stats.InUse != 0 {
			t.Fatalf("database in-use connections = %d, want 0", stats.InUse)
		}
		if sqliteTableExists(t, backend, migrationRevisionTable) || sqliteTableExists(t, backend, "godj_migration_article") {
			t.Fatal("discard after failed rollback preserved uncommitted mutation")
		}
	})
}

func TestSQLiteRevisionIOClassifiesBusyAndLockedAtEveryStage(t *testing.T) {
	stages := []string{
		"begin", "metadata", "history", "claim", "domain", "recorder", "final verification", "commit", "rollback",
	}
	for _, stage := range stages {
		stage := stage
		t.Run(stage, func(t *testing.T) {
			for _, code := range []int{5, 6, 5 | 0x200, 6 | 0x300} {
				err := classifyRevisionIO(stage, codedRevisionSQLiteError{code: code})
				assertRevisionFenceKind(t, err, migrationbackend.RevisionFenceFailureContended)
				integrityErr := classifyRevisionIntegrityIO(stage, codedRevisionSQLiteError{code: code})
				assertRevisionFenceKind(t, integrityErr, migrationbackend.RevisionFenceFailureContended)
			}
		})
	}
}

type closeErrorPinnedConnection struct {
	migrationPinnedConnection
	err error
}

type closeWithoutReleasePinnedConnection struct {
	migrationPinnedConnection
	err error
}

type blockingRollbackPinnedConnection struct {
	migrationPinnedConnection
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (connection *blockingRollbackPinnedConnection) ExecContext(
	ctx context.Context,
	statement string,
	args ...any,
) (sql.Result, error) {
	if statement == "ROLLBACK" {
		connection.once.Do(func() { close(connection.entered) })
		select {
		case <-connection.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return connection.migrationPinnedConnection.ExecContext(ctx, statement, args...)
}

func (connection *closeWithoutReleasePinnedConnection) Close() error {
	return connection.err
}

func (connection *closeErrorPinnedConnection) Close() error {
	return errors.Join(connection.migrationPinnedConnection.Close(), connection.err)
}

type codedRevisionSQLiteError struct{ code int }

func (err codedRevisionSQLiteError) Error() string { return fmt.Sprintf("SQLite code %d", err.code) }
func (err codedRevisionSQLiteError) Code() int     { return err.code }

type migrationSQLFault struct {
	mu        sync.Mutex
	method    string
	contains  string
	code      int
	remaining int
}

func (fault *migrationSQLFault) inject(method, statement string) error {
	fault.mu.Lock()
	defer fault.mu.Unlock()
	if fault.remaining == 0 || fault.method != method || !strings.Contains(statement, fault.contains) {
		return nil
	}
	fault.remaining--
	return codedRevisionSQLiteError{code: fault.code}
}

func (fault *migrationSQLFault) remainingCount() int {
	fault.mu.Lock()
	defer fault.mu.Unlock()
	return fault.remaining
}

type faultingMigrationSQLExecutor struct {
	migrationSQLExecutor
	fault *migrationSQLFault
}

func (executor *faultingMigrationSQLExecutor) ExecContext(
	ctx context.Context,
	statement string,
	args ...any,
) (sql.Result, error) {
	if err := executor.fault.inject("exec", statement); err != nil {
		return nil, err
	}
	return executor.migrationSQLExecutor.ExecContext(ctx, statement, args...)
}

func (executor *faultingMigrationSQLExecutor) QueryContext(
	ctx context.Context,
	statement string,
	args ...any,
) (*sql.Rows, error) {
	if err := executor.fault.inject("query", statement); err != nil {
		return nil, err
	}
	return executor.migrationSQLExecutor.QueryContext(ctx, statement, args...)
}

type faultingMigrationPinnedConnection struct {
	migrationPinnedConnection
	fault *migrationSQLFault
}

func (connection *faultingMigrationPinnedConnection) ExecContext(
	ctx context.Context,
	statement string,
	args ...any,
) (sql.Result, error) {
	if err := connection.fault.inject("exec", statement); err != nil {
		return nil, err
	}
	return connection.migrationPinnedConnection.ExecContext(ctx, statement, args...)
}

func (connection *faultingMigrationPinnedConnection) QueryContext(
	ctx context.Context,
	statement string,
	args ...any,
) (*sql.Rows, error) {
	if err := connection.fault.inject("query", statement); err != nil {
		return nil, err
	}
	return connection.migrationPinnedConnection.QueryContext(ctx, statement, args...)
}

func installMigrationTransactionFault(
	transaction migrationbackend.RevisionFencedTransaction,
	fault *migrationSQLFault,
) {
	concrete := transaction.(*sqliteRevisionFencedTransaction)
	concrete.mu.Lock()
	concrete.connection = &faultingMigrationPinnedConnection{
		migrationPinnedConnection: concrete.connection,
		fault:                     fault,
	}
	concrete.mu.Unlock()
}

func bootstrapSQLiteRevisionHistory(
	t *testing.T,
	backend *Backend,
	transition migrationbackend.HistoryTransition,
) {
	t.Helper()
	ctx := context.Background()
	session := openLifecycleSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	transaction := beginLifecycleTransaction(t, session, transition)
	if err := transaction.RecordApplied(ctx, transition.Migration.App, transition.Migration.Name); err != nil {
		t.Fatal(err)
	}
	if outcome, err := transaction.CommitFenced(ctx); err != nil || outcome.Durability != migrationbackend.CommitCommitted {
		t.Fatalf("bootstrap revision history = (%+v, %v)", outcome, err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func openLifecycleFileBackend(t *testing.T, path, query string) *Backend {
	t.Helper()
	backend, err := Open(context.Background(), "file:"+path+"?mode=rwc"+query)
	if err != nil {
		t.Fatalf("Open(%s): %v", path, err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("Close(%s): %v", path, err)
		}
	})
	return backend
}

func openLifecycleSession(t *testing.T, backend *Backend) migrationbackend.RevisionFencedSession {
	t.Helper()
	session, err := backend.OpenRevisionFencedSession(context.Background())
	if err != nil {
		t.Fatalf("OpenRevisionFencedSession(): %v", err)
	}
	return session
}

func beginLifecycleTransaction(
	t *testing.T,
	session migrationbackend.RevisionFencedSession,
	transition migrationbackend.HistoryTransition,
	intents ...migrationbackend.MigrationIntent,
) migrationbackend.RevisionFencedTransaction {
	t.Helper()
	intent := emptySQLiteMigrationIntent()
	if len(intents) > 1 {
		t.Fatalf("begin lifecycle transaction received %d intents, want at most 1", len(intents))
	}
	if len(intents) == 1 {
		intent = intents[0]
	}
	transaction, err := session.BeginMigration(context.Background(), transition, intent)
	if err != nil {
		t.Fatalf("BeginMigration(%+v): %v", transition, err)
	}
	return transaction
}

func emptySQLiteMigrationIntent() migrationbackend.MigrationIntent {
	return migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{}}
}

func createModelMigrationIntent(model ir.Model) migrationbackend.MigrationIntent {
	return migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
		OperationIndex: 0,
		Kind:           migrationbackend.MigrationCreateModel,
		After:          model.Clone(),
	}}}
}

func addFieldMigrationIntent(before, after ir.Model) migrationbackend.MigrationIntent {
	return migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
		OperationIndex: 0,
		Kind:           migrationbackend.MigrationAddField,
		Before:         before.Clone(),
		After:          after.Clone(),
	}}}
}

func removeFieldMigrationIntent(before, after ir.Model) migrationbackend.MigrationIntent {
	return migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
		OperationIndex: 0,
		Kind:           migrationbackend.MigrationRemoveField,
		Before:         before.Clone(),
		After:          after.Clone(),
	}}}
}

func assertRevisionFenceKind(t *testing.T, err error, want migrationbackend.RevisionFenceFailureKind) {
	t.Helper()
	var fenceError *migrationbackend.RevisionFenceError
	if !errors.As(err, &fenceError) || fenceError == nil {
		t.Fatalf("error = %#v, want RevisionFenceError kind %d", err, want)
	}
	if fenceError.Kind != want {
		t.Fatalf("fence error kind = %d, want %d: %v", fenceError.Kind, want, err)
	}
}
