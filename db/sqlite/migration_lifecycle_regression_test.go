package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

func TestSQLiteExecutorCompetingCommitStopsTailAndReturnsOwnDurablePrefix(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "between-steps.sqlite")
	left, right := openLifecycleFileBackend(t, path, ""), openLifecycleFileBackend(t, path, "")
	loaded, keys := sqliteLifecycleRegressionDefinitions(t)
	probe := &sqliteLifecycleInterleaver{RevisionFencedBackend: left}
	probe.beforeBegin = func(ctx context.Context, attempt int) error {
		if attempt != 2 {
			return nil
		}
		_, err := (migrations.Executor{Backend: right}).Migrate(ctx, loaded,
			migrations.TargetedLifecycleRequest(migrations.NamedTarget(keys[3])))
		return err
	}
	state, err := (migrations.Executor{Backend: probe}).Migrate(ctx, loaded,
		migrations.TargetedLifecycleRequest(migrations.NamedTarget(keys[2])))
	var failure *migrations.Error
	if !errors.As(err, &failure) || failure.Category != migrations.CategoryConflict || failure.Code != migrations.CodeStaleHistoryRevision {
		t.Fatalf("competing commit error = %v, want stale history conflict", err)
	}
	if probe.begins != 2 {
		t.Fatalf("begin attempts = %d, want failed second attempt without retry/tail", probe.begins)
	}
	for _, model := range []struct {
		app, name           string
		committed, returned bool
	}{
		{"plan", "first", true, true}, {"plan", "second", false, false},
		{"plan", "tail", false, false}, {"other", "competitor", true, false},
	} {
		_, returned := state.Model(model.app, model.name)
		if returned != model.returned || sqliteTableExists(t, right, model.app+"_"+model.name) != model.committed {
			t.Fatalf("%s.%s returned/durable state differs from the own committed prefix", model.app, model.name)
		}
	}
	snapshot, err := readAtomicMigrationRevisionSnapshot(ctx, right)
	wantRecords := []migrationbackend.AppliedMigration{{App: keys[3].App, Name: keys[3].Name}, {App: keys[0].App, Name: keys[0].Name}}
	if err != nil || snapshot.token.revision != 2 || !reflect.DeepEqual(snapshot.records, wantRecords) {
		t.Fatalf("durable competing histories = (%+v, %v), want both commits exactly once", snapshot, err)
	}
}

func TestSQLiteExecutorRejectsRecorderCorruptionAfterValidTransition(t *testing.T) {
	ctx := t.Context()
	database := openMigrationTestBackend(t)
	loaded, keys := sqliteLifecycleRegressionDefinitions(t)
	probe := &sqliteLifecycleInterleaver{RevisionFencedBackend: database, corruptSuccessor: true}
	request := migrations.TargetedLifecycleRequest(migrations.NamedTarget(keys[0]))
	state, err := (migrations.Executor{Backend: probe}).Migrate(ctx, loaded, request)
	var failure *migrations.Error
	if !errors.As(err, &failure) || failure.Category != migrations.CategoryHistory || failure.Code != migrations.CodeHistoryRevisionIntegrity {
		t.Fatalf("corrupt successor error = %v, want revision integrity failure", err)
	}
	if len(state.Apps()) != 0 || probe.begins != 1 {
		t.Fatalf("failed commit published state=%v or retried begins=%d", state.Apps(), probe.begins)
	}
	for _, table := range []string{migrationRevisionTable, migrationRecorderTable, "plan_first"} {
		if sqliteTableExists(t, database, table) {
			t.Fatalf("corrupt successor preserved table %q", table)
		}
	}
	if inUse := database.database.Stats().InUse; inUse != 0 {
		t.Fatalf("corrupt successor leaked %d pinned connections", inUse)
	}
	// A fresh public lifecycle proves rollback released the transaction and did
	// not turn the valid definition into an unrecoverable partial publication.
	state, err = (migrations.Executor{Backend: database}).Migrate(ctx, loaded, request)
	if err != nil {
		t.Fatalf("fresh lifecycle after rejected successor: %v", err)
	}
	if _, exists := state.Model("plan", "first"); !exists {
		t.Fatal("fresh lifecycle did not publish its committed model")
	}
}

// The interleaver delegates every operation to the real backend. It changes
// timing at one public boundary; it does not implement a second fence model.
type sqliteLifecycleInterleaver struct {
	migrationbackend.RevisionFencedBackend
	migrationbackend.RevisionFencedSession
	beforeBegin      func(context.Context, int) error
	corruptSuccessor bool
	begins           int
}

func (probe *sqliteLifecycleInterleaver) OpenRevisionFencedSession(ctx context.Context) (migrationbackend.RevisionFencedSession, error) {
	session, err := probe.RevisionFencedBackend.OpenRevisionFencedSession(ctx)
	if err != nil {
		return nil, err
	}
	probe.RevisionFencedSession = session
	return probe, nil
}

func (probe *sqliteLifecycleInterleaver) BeginMigration(ctx context.Context, transition migrationbackend.HistoryTransition, intent migrationbackend.MigrationIntent) (migrationbackend.RevisionFencedTransaction, error) {
	probe.begins++
	if probe.beforeBegin != nil {
		if err := probe.beforeBegin(ctx, probe.begins); err != nil {
			return nil, err
		}
	}
	transaction, err := probe.RevisionFencedSession.BeginMigration(ctx, transition, intent)
	if err == nil && probe.corruptSuccessor {
		return sqliteCorruptSuccessorTransaction{transaction}, nil
	}
	return transaction, err
}

type sqliteCorruptSuccessorTransaction struct {
	migrationbackend.RevisionFencedTransaction
}

func (transaction sqliteCorruptSuccessorTransaction) CommitFenced(ctx context.Context) (migrationbackend.CommitOutcome, error) {
	actual := transaction.RevisionFencedTransaction.(*sqliteRevisionFencedTransaction)
	// RecordApplied already succeeded through Executor; change its persisted
	// identity without changing the expected successor so final verification
	// must detect the mismatch before COMMIT.
	if _, err := actual.connection.ExecContext(ctx, `UPDATE "godj_migrations" SET "name" = 'unexpected_successor'`); err != nil {
		return migrationbackend.CommitOutcome{Durability: migrationbackend.CommitUnknown}, err
	}
	return actual.CommitFenced(ctx)
}

func sqliteLifecycleRegressionDefinitions(t *testing.T) (migrations.LoadedDefinitionSet, []migrations.MigrationKey) {
	t.Helper()
	var sources []definition.Source
	var keys []migrations.MigrationKey
	for index, name := range []string{"first", "second", "tail", "competitor"} {
		app := "plan"
		if index == 3 {
			app = "other"
		}
		migration := migrations.Migration{App: app, Name: fmt.Sprintf("%04d_%s", index+1, name), Operations: []migrations.Operation{
			migrations.CreateModel{AppLabel: app, Model: ir.Model{Name: name, GoName: "Model_" + name, DBTable: app + "_" + name,
				Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}}}},
		}}
		if index == 1 || index == 2 {
			migration.Dependencies = []migrations.MigrationKey{keys[index-1]}
		}
		document, err := definition.Encode(definition.Producer{Name: "sqlite-regression", Version: "1"}, migration)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, definition.Source{SourceID: migration.Name + ".json", Document: document})
		keys = append(keys, migration.Key())
	}
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	return loaded, keys
}
