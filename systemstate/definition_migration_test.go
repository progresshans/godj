package systemstate

import (
	"context"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestInitialDefinitionExplicitSQLiteMigrationReopenAndNoOp(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "godj-system.sqlite3")
	dataSourceName := "file:" + filepath.ToSlash(databasePath) + "?mode=rwc"
	loaded, _ := loadInitialDefinition(t)

	firstDatabase, err := sqlite.Open(ctx, dataSourceName)
	if err != nil {
		t.Fatalf("open first SQLite system migration backend: %v", err)
	}
	firstBackend := &countingMigrationBackend{delegate: firstDatabase}
	firstState, err := (migrations.Executor{Backend: firstBackend}).Migrate(
		ctx,
		loaded,
		migrations.LatestLifecycleRequest(),
	)
	if err != nil {
		_ = firstDatabase.Close()
		t.Fatalf("explicitly migrate initial system definition: %v", err)
	}
	if got := firstBackend.beginCalls.Load(); got != 1 {
		_ = firstDatabase.Close()
		t.Fatalf("first system migration transactions = %d, want 1", got)
	}
	assertInitialMigrationState(t, firstState)
	assertInitialPhysicalTables(t, ctx, firstDatabase)
	firstHistory, err := firstDatabase.ReadAppliedMigrations(ctx)
	if err != nil {
		_ = firstDatabase.Close()
		t.Fatalf("read first SQLite system migration history: %v", err)
	}
	assertInitialMigrationHistory(t, firstHistory)
	if err := firstDatabase.Close(); err != nil {
		t.Fatalf("close first SQLite system migration backend: %v", err)
	}

	secondDatabase, err := sqlite.Open(ctx, dataSourceName)
	if err != nil {
		t.Fatalf("reopen SQLite system migration backend: %v", err)
	}
	t.Cleanup(func() {
		if err := secondDatabase.Close(); err != nil {
			t.Errorf("close reopened SQLite system migration backend: %v", err)
		}
	})
	reopenedHistory, err := secondDatabase.ReadAppliedMigrations(ctx)
	if err != nil {
		t.Fatalf("read reopened SQLite system migration history: %v", err)
	}
	assertInitialMigrationHistory(t, reopenedHistory)

	secondLoaded, _ := loadInitialDefinition(t)
	secondBackend := &countingMigrationBackend{delegate: secondDatabase}
	secondState, err := (migrations.Executor{Backend: secondBackend}).Migrate(
		ctx,
		secondLoaded,
		migrations.LatestLifecycleRequest(),
	)
	if err != nil {
		t.Fatalf("repeat explicit system migration after reopen: %v", err)
	}
	if got := secondBackend.beginCalls.Load(); got != 0 {
		t.Fatalf("reopened no-op system migration transactions = %d, want 0", got)
	}
	assertInitialMigrationState(t, secondState)
	assertInitialPhysicalTables(t, ctx, secondDatabase)
	if !firstState.Equal(secondState) {
		t.Fatalf("reopened no-op state differs from first migrated state")
	}
	finalHistory, err := secondDatabase.ReadAppliedMigrations(ctx)
	if err != nil {
		t.Fatalf("read final SQLite system migration history: %v", err)
	}
	assertInitialMigrationHistory(t, finalHistory)
}

func assertInitialPhysicalTables(t *testing.T, ctx context.Context, backend *sqlite.Backend) {
	t.Helper()
	for _, model := range initialDefinitionModels() {
		fields := make([]query.FieldRef, len(model.Fields))
		for index, field := range model.Fields {
			var kind query.FieldKind
			switch field.Kind {
			case ir.FieldAuto:
				kind = query.FieldInteger
			case ir.FieldChar:
				kind = query.FieldString
			case ir.FieldBoolean:
				kind = query.FieldBoolean
			default:
				t.Fatalf("unsupported system field kind %q", field.Kind)
			}
			fields[index] = query.NewFieldRef(field.Name, field.Column, kind, field.Nullable)
		}
		rows, err := backend.Query(ctx, query.NewPlan(model.DBTable, fields))
		if err != nil {
			t.Fatalf("query empty migrated table %q: %v", model.DBTable, err)
		}
		if rows.Next() {
			_ = rows.Close()
			t.Fatalf("newly migrated table %q unexpectedly contains a row", model.DBTable)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			t.Fatalf("iterate empty migrated table %q: %v", model.DBTable, err)
		}
		if err := rows.Close(); err != nil {
			t.Fatalf("close empty migrated table %q rows: %v", model.DBTable, err)
		}
	}
}

func assertInitialMigrationState(t *testing.T, state migrations.ProjectState) {
	t.Helper()
	schema, exists := state.Schema(initialMigrationApp)
	if !exists {
		t.Fatalf("migrated state is missing app %q", initialMigrationApp)
	}
	if got, want := schema.Models, initialDefinitionModels(); !reflect.DeepEqual(got, want) {
		t.Fatalf("migrated system models =\n%#v\nwant\n%#v", got, want)
	}
}

func assertInitialMigrationHistory(t *testing.T, history []migrationbackend.AppliedMigration) {
	t.Helper()
	want := []migrationbackend.AppliedMigration{{App: initialMigrationApp, Name: initialMigrationName}}
	if !reflect.DeepEqual(history, want) {
		t.Fatalf("system migration history = %+v, want %+v", history, want)
	}
}

type countingMigrationBackend struct {
	delegate   *sqlite.Backend
	beginCalls atomic.Int64
}

func (backend *countingMigrationBackend) MigrationCapabilities() migrationbackend.MigrationCapabilities {
	return backend.delegate.MigrationCapabilities()
}

func (backend *countingMigrationBackend) OpenRevisionFencedSession(
	ctx context.Context,
) (migrationbackend.RevisionFencedSession, error) {
	session, err := backend.delegate.OpenRevisionFencedSession(ctx)
	if err != nil {
		return nil, err
	}
	return &countingMigrationSession{
		RevisionFencedSession: session,
		beginCalls:            &backend.beginCalls,
	}, nil
}

type countingMigrationSession struct {
	migrationbackend.RevisionFencedSession
	beginCalls *atomic.Int64
}

func (session *countingMigrationSession) BeginMigration(
	ctx context.Context,
	transition migrationbackend.HistoryTransition,
	intent migrationbackend.MigrationIntent,
) (migrationbackend.RevisionFencedTransaction, error) {
	session.beginCalls.Add(1)
	return session.RevisionFencedSession.BeginMigration(ctx, transition, intent)
}
