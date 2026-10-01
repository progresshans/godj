package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/uniquetest"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

func TestSQLiteUniqueSQLGroupsPreservePhysicalAndMetadataOperations(t *testing.T) {
	unique, _ := uniquetest.Model(t, "char")
	plain := unique.Clone()
	plain.Fields[1].Unique = false
	added := unique.Clone()
	extra := ir.Field{Name: "external", GoName: "External", Column: "external", Kind: ir.FieldUUID, Nullable: true, Unique: true}
	added.Fields = append(added.Fields, extra)
	choices := added.Clone()
	choices.Fields[1].Choices = []ir.Choice{{Value: ir.Scalar{Kind: ir.ScalarString, String: "x"}, Label: "X"}}
	initial := migrations.Migration{App: "uniqueref", Name: "0001_mixed", Operations: []migrations.Operation{
		migrations.CreateModel{AppLabel: "uniqueref", Model: plain},
		migrations.AlterField{AppLabel: "uniqueref", ModelName: plain.Name, Before: plain.Fields[1], After: unique.Fields[1]},
		migrations.AddField{AppLabel: "uniqueref", ModelName: plain.Name, Field: extra},
		migrations.AlterField{AppLabel: "uniqueref", ModelName: plain.Name, Before: added.Fields[1], After: choices.Fields[1]},
	}}
	loaded := sqliteUniqueHistory(t, initial)
	flat, err := migrations.RenderMigrationSQL(t.Context(), loaded, initial.Key(), NewMigrationSQLRenderer())
	if err != nil || len(flat) != 4 || !strings.HasPrefix(flat[0], "CREATE TABLE ") ||
		!strings.HasPrefix(flat[1], `CREATE UNIQUE INDEX "main".`) || !strings.HasPrefix(flat[2], "ALTER TABLE ") ||
		!strings.HasPrefix(flat[3], `CREATE UNIQUE INDEX "main".`) || flat[1] == flat[3] {
		t.Fatalf("incomplete or reordered unique SQL: %#v %v", flat, err)
	}
	for _, pair := range [][2]ir.Model{{plain, unique}, {unique, plain}} {
		groups, err := NewMigrationSQLRenderer().RenderForwardMigrationSQL(t.Context(), migrationbackend.ForwardMigrationSQLRequest{App: "uniqueref", Name: "0002_unique", Intent: migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{Kind: migrationbackend.MigrationAlterField, Before: pair[0], After: pair[1]}}}})
		if err != nil || len(groups) != 1 || len(groups[0]) != 1 {
			t.Fatal("missing physical unique alteration", groups, err)
		}
		if !pair[1].Fields[1].Unique && !strings.HasPrefix(groups[0][0], `DROP INDEX "main".`) {
			t.Fatal("unique removal did not drop its owned index")
		}
	}
	groups, err := compileSQLiteCreateModelStatements(added, nil)
	if err != nil || len(groups) != 3 {
		t.Fatal("CreateModel discarded declared unique columns", groups, err)
	}
	database := openMigrationTestBackend(t)
	if !database.MigrationCapabilities().UniqueConstraints {
		t.Fatal("implemented uniqueness unavailable")
	}
	tx, err := database.BeginMigration(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range []func() error{
		func() error { return tx.CreateModel(t.Context(), unique) },
		func() error { return tx.AddField(t.Context(), plain, extra) },
		func() error { return tx.RemoveField(t.Context(), unique, unique.Fields[1]) },
		func() error { return tx.DeleteModel(t.Context(), unique) },
	} {
		if err := call(); !migrationbackend.IsCapabilityError(err) {
			t.Fatal("unsealed legacy editor admitted unique ownership", err)
		}
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	var objects int
	if err := database.database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_schema`).Scan(&objects); err != nil || objects != 0 {
		t.Fatal("unsupported editor mutated schema", objects, err)
	}
}

func TestSQLiteUniqueNamesAndDeclaredNamespaceCollisions(t *testing.T) {
	names := make(map[string]bool)
	for _, pair := range [][2]string{{"a_b", "c"}, {"a", "b_c"}, {"a", "c"}, {"b", "c"}} {
		name, err := sqliteUniqueIndexName(pair[0], pair[1])
		again, againErr := sqliteUniqueIndexName(pair[0], pair[1])
		if err != nil || againErr != nil || name != again || len(name) != 56 || names[name] {
			t.Fatal("unique index identity is ambiguous", name, err)
		}
		names[name] = true
	}
	model, _ := uniquetest.Model(t, "char")
	collision := model.Clone()
	collision.Name, collision.GoName = "collision", "Collision"
	var err error
	collision.DBTable, err = sqliteUniqueIndexName(model.DBTable, model.Fields[1].Column)
	if err != nil {
		t.Fatal(err)
	}
	collision.Fields[1].Unique = false
	request := migrationbackend.ForwardMigrationSQLRequest{App: "uniqueref", Name: "0001_collision", Intent: migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{
		{Kind: migrationbackend.MigrationCreateModel, After: model},
		{OperationIndex: 1, Kind: migrationbackend.MigrationCreateModel, After: collision},
	}}}
	before := request.Intent.Clone()
	if groups, err := NewMigrationSQLRenderer().RenderForwardMigrationSQL(t.Context(), request); err == nil || groups != nil {
		t.Fatal("table/index namespace collision accepted")
	}
	if !reflect.DeepEqual(before, request.Intent) {
		t.Fatal("collision validation mutated caller intent")
	}
	for _, name := range []string{"", "broken\x00name"} {
		if _, err := sqliteUniqueIndexName(name, "value"); err == nil {
			t.Fatal("invalid table name accepted")
		}
	}
}

func TestSQLiteIntentSealIncludesUnique(t *testing.T) {
	model, _, _ := sqliteRelationTestModels()
	intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{Kind: migrationbackend.MigrationCreateModel, After: model}}}
	seal, err := validateAndSealSQLiteRelationIntent(migrationbackend.HistoryTransition{Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_initial"}, Kind: migrationbackend.HistoryTransitionApply}, intent)
	if err != nil {
		t.Fatal(err)
	}
	seal.intent.Operations[0].After.Fields[1].Unique = true
	if err := verifySQLiteRelationIntentSeal(&seal); err == nil {
		t.Fatal("unique mutation did not invalidate the intent seal")
	}
}

type sqliteUniqueBusyError struct{}

func (sqliteUniqueBusyError) Error() string { return "injected late SQLite contention" }
func (sqliteUniqueBusyError) Code() int     { return 5 }

type sqliteUniqueStatementFault struct {
	migrationSQLExecutor
	calls, failAt int
	failure       error
}

func (executor *sqliteUniqueStatementFault) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	executor.calls++
	if executor.calls == executor.failAt {
		return nil, executor.failure
	}
	return nil, nil
}

func TestSQLiteUniqueStatementStreamKeepsLateFailureExecutionOwnership(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		cause := sqliteUniqueBusyError{}
		executor := &sqliteUniqueStatementFault{failAt: failAt, failure: cause}
		err := classifyRevisionIO("DDL group", executeSQLiteMigrationStatements(t.Context(), executor, []string{"CREATE TABLE example (id INTEGER)", "CREATE UNIQUE INDEX example_id ON example (id)"}))
		if !errors.Is(err, cause) || executor.calls != failAt {
			t.Fatal("DDL failure lost cause or continued execution", err)
		}
		if migrationbackend.IsRevisionFenceError(err) != (failAt == 1) {
			t.Fatal("late DDL contention was reclassified as preclaim fencing", err)
		}
	}
}
