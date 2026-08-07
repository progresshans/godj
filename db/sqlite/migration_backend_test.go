package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

func TestSQLiteMigrationRoundTripPreservesRowsAndRecorder(t *testing.T) {
	ctx := context.Background()
	backend := openMigrationTestBackend(t)
	executor := migrations.Executor{Backend: backend}
	initial, summary := migrationTestMigrations()

	state0 := migrations.EmptyProjectState()
	state1, err := executor.Apply(ctx, state0, initial)
	if err != nil {
		t.Fatalf("apply initial migration: %v", err)
	}
	if _, err := backend.ExecContext(ctx, `INSERT INTO "godj_migration_article" ("title", "published") VALUES (?, ?)`, "first", false); err != nil {
		t.Fatalf("insert row before AddField: %v", err)
	}

	state2, err := executor.Apply(ctx, state1, summary)
	if err != nil {
		t.Fatalf("apply summary migration: %v", err)
	}
	assertSQLiteColumns(t, backend, "godj_migration_article", "id", "title", "published", "summary")
	var firstSummary sql.NullString
	if err := backend.database.QueryRowContext(ctx, `SELECT "summary" FROM "godj_migration_article" WHERE "title" = ?`, "first").Scan(&firstSummary); err != nil {
		t.Fatalf("read existing row after AddField: %v", err)
	}
	if firstSummary.Valid {
		t.Fatalf("existing row summary = %q, want NULL", firstSummary.String)
	}
	if _, err := backend.ExecContext(ctx, `INSERT INTO "godj_migration_article" ("title", "published", "summary") VALUES (?, ?, ?)`, "second", true, "kept until reverse"); err != nil {
		t.Fatalf("insert row after AddField: %v", err)
	}
	assertMigrationRecords(t, backend, "news.0001_initial", "news.0002_summary")

	state1Again, err := executor.Unapply(ctx, state2, summary)
	if err != nil {
		t.Fatalf("unapply summary migration: %v", err)
	}
	if !state1Again.Equal(state1) {
		t.Fatal("summary reverse state does not equal initial state")
	}
	assertSQLiteColumns(t, backend, "godj_migration_article", "id", "title", "published")
	assertArticleRows(t, backend,
		articleMigrationRow{id: 1, title: "first", published: false},
		articleMigrationRow{id: 2, title: "second", published: true},
	)
	assertMigrationRecords(t, backend, "news.0001_initial")

	state0Again, err := executor.Unapply(ctx, state1Again, initial)
	if err != nil {
		t.Fatalf("unapply initial migration: %v", err)
	}
	if !state0Again.Equal(state0) {
		t.Fatal("initial reverse state is not empty")
	}
	if sqliteTableExists(t, backend, "godj_migration_article") {
		t.Fatal("article table still exists after reversing CreateModel")
	}
	assertMigrationRecords(t, backend)
}

func TestSQLiteMigrationAutoFieldDoesNotReuseDeletedMaximumID(t *testing.T) {
	ctx := context.Background()
	backend := openMigrationTestBackend(t)
	initial, _ := migrationTestMigrations()
	if _, err := (migrations.Executor{Backend: backend}).Apply(ctx, migrations.EmptyProjectState(), initial); err != nil {
		t.Fatalf("apply initial migration: %v", err)
	}
	first, err := backend.ExecContext(ctx, `INSERT INTO "godj_migration_article" ("title", "published") VALUES ('first', 0)`)
	if err != nil {
		t.Fatalf("insert first row: %v", err)
	}
	firstID, err := first.LastInsertId()
	if err != nil {
		t.Fatalf("read first ID: %v", err)
	}
	if _, err := backend.ExecContext(ctx, `DELETE FROM "godj_migration_article" WHERE "id" = ?`, firstID); err != nil {
		t.Fatalf("delete maximum ID: %v", err)
	}
	second, err := backend.ExecContext(ctx, `INSERT INTO "godj_migration_article" ("title", "published") VALUES ('second', 0)`)
	if err != nil {
		t.Fatalf("insert second row: %v", err)
	}
	secondID, err := second.LastInsertId()
	if err != nil {
		t.Fatalf("read second ID: %v", err)
	}
	if secondID != firstID+1 {
		t.Fatalf("ID after deleting maximum = %d, want %d", secondID, firstID+1)
	}
}

func TestSQLiteMigrationTransactionObservesUncommittedCreateModel(t *testing.T) {
	ctx := context.Background()
	backend := openMigrationTestBackend(t)
	transactionInterface, err := backend.BeginMigration(ctx)
	if err != nil {
		t.Fatalf("begin migration: %v", err)
	}
	transaction, ok := transactionInterface.(*migrationTransaction)
	if !ok {
		t.Fatalf("BeginMigration() transaction = %T, want *migrationTransaction", transactionInterface)
	}
	model := migrationTestModel(false)
	if err := transaction.CreateModel(ctx, model); err != nil {
		t.Fatalf("CreateModel() error = %v", err)
	}
	exists, err := transaction.TableExists(ctx, model.DBTable)
	if err != nil {
		t.Fatalf("TableExists() error = %v", err)
	}
	if !exists {
		t.Fatal("TableExists() = false for uncommitted CreateModel")
	}
	if err := transaction.Rollback(ctx); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if sqliteTableExists(t, backend, model.DBTable) {
		t.Fatal("rolled-back CreateModel remained visible")
	}
}

func TestSQLiteMigrationOperationFailureRollsBackAndConnectionRecovers(t *testing.T) {
	ctx := context.Background()
	backend := openMigrationTestBackend(t)
	if _, err := backend.ExecContext(ctx, `CREATE TABLE "godj_migration_conflict" ("sentinel" INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create conflicting table: %v", err)
	}

	conflict := migrationTestModel(false)
	conflict.Name = "conflict"
	conflict.GoName = "Conflict"
	conflict.DBTable = "godj_migration_conflict"
	migration := migrations.Migration{
		App:  "news",
		Name: "0001_operation_failure",
		Operations: []migrations.Operation{
			migrations.CreateModel{AppLabel: "news", Model: migrationTestModel(false)},
			migrations.CreateModel{AppLabel: "news", Model: conflict},
		},
	}
	state0 := migrations.EmptyProjectState()
	stateAfter, err := (migrations.Executor{Backend: backend}).Apply(ctx, state0, migration)
	assertSQLiteMigrationError(t, err, migrations.CategoryExecution, migrations.CodeOperationFailed, 1, "CreateModel")
	if !stateAfter.Equal(state0) {
		t.Fatal("operation failure changed returned state")
	}
	if sqliteTableExists(t, backend, "godj_migration_article") {
		t.Fatal("first CreateModel survived rollback")
	}
	if !sqliteTableExists(t, backend, "godj_migration_conflict") {
		t.Fatal("pre-existing conflicting table was changed")
	}
	if sqliteTableExists(t, backend, migrationRecorderTable) {
		t.Fatal("recorder table was created before recorder phase")
	}

	// MaxOpenConns(1) makes this query and the next migration fail if the
	// pinned migration connection was not returned to the pool.
	var value int
	if err := backend.database.QueryRowContext(ctx, "SELECT 1").Scan(&value); err != nil || value != 1 {
		t.Fatalf("query after rollback: value=%d err=%v", value, err)
	}
	initial, _ := migrationTestMigrations()
	if _, err := (migrations.Executor{Backend: backend}).Apply(ctx, state0, initial); err != nil {
		t.Fatalf("apply migration after operation rollback: %v", err)
	}
}

func TestSQLiteMigrationRecorderFailureRollsBackSchema(t *testing.T) {
	ctx := context.Background()
	backend := openMigrationTestBackend(t)
	if _, err := backend.ExecContext(ctx, createMigrationRecorderTableSQL); err != nil {
		t.Fatalf("create recorder table: %v", err)
	}
	if _, err := backend.ExecContext(ctx, `INSERT INTO "godj_migrations" ("app", "name") VALUES (?, ?)`, "news", "0001_initial"); err != nil {
		t.Fatalf("seed duplicate recorder row: %v", err)
	}

	initial, _ := migrationTestMigrations()
	state0 := migrations.EmptyProjectState()
	stateAfter, err := (migrations.Executor{Backend: backend}).Apply(ctx, state0, initial)
	assertSQLiteMigrationError(t, err, migrations.CategoryRecorder, migrations.CodeRecordFailed, migrations.NoOperation, "")
	if !stateAfter.Equal(state0) {
		t.Fatal("recorder failure changed returned state")
	}
	if sqliteTableExists(t, backend, "godj_migration_article") {
		t.Fatal("CreateModel survived recorder rollback")
	}
	assertMigrationRecords(t, backend, "news.0001_initial")
}

func TestSQLiteMigrationReverseRecorderFailureRestoresDroppedColumn(t *testing.T) {
	ctx := context.Background()
	backend := openMigrationTestBackend(t)
	executor := migrations.Executor{Backend: backend}
	initial, summary := migrationTestMigrations()
	state1, err := executor.Apply(ctx, migrations.EmptyProjectState(), initial)
	if err != nil {
		t.Fatalf("apply initial migration: %v", err)
	}
	state2, err := executor.Apply(ctx, state1, summary)
	if err != nil {
		t.Fatalf("apply summary migration: %v", err)
	}
	if _, err := backend.ExecContext(ctx, `INSERT INTO "godj_migration_article" ("title", "published", "summary") VALUES ('kept', 1, 'still here')`); err != nil {
		t.Fatalf("seed summary row: %v", err)
	}
	if _, err := backend.ExecContext(ctx, `CREATE TRIGGER "block_summary_unrecord" BEFORE DELETE ON "godj_migrations"
		WHEN OLD."app" = 'news' AND OLD."name" = '0002_summary'
		BEGIN SELECT RAISE(ABORT, 'forced recorder failure'); END`); err != nil {
		t.Fatalf("create recorder failure trigger: %v", err)
	}

	stateAfter, err := executor.Unapply(ctx, state2, summary)
	assertSQLiteMigrationError(t, err, migrations.CategoryRecorder, migrations.CodeRecordFailed, migrations.NoOperation, "")
	if !stateAfter.Equal(state2) {
		t.Fatal("reverse recorder failure changed returned state")
	}
	assertSQLiteColumns(t, backend, "godj_migration_article", "id", "title", "published", "summary")
	var summaryValue string
	if err := backend.database.QueryRowContext(ctx, `SELECT "summary" FROM "godj_migration_article" WHERE "title" = 'kept'`).Scan(&summaryValue); err != nil {
		t.Fatalf("read restored summary value: %v", err)
	}
	if summaryValue != "still here" {
		t.Fatalf("restored summary = %q, want %q", summaryValue, "still here")
	}
	assertMigrationRecords(t, backend, "news.0001_initial", "news.0002_summary")

	if _, err := backend.ExecContext(ctx, `DROP TRIGGER "block_summary_unrecord"`); err != nil {
		t.Fatalf("drop recorder failure trigger: %v", err)
	}
	if _, err := executor.Unapply(ctx, state2, summary); err != nil {
		t.Fatalf("unapply after recorder recovery: %v", err)
	}
}

func TestSQLiteMigrationCommitFailureRollsBackAndConnectionRecovers(t *testing.T) {
	ctx := context.Background()
	backend := openMigrationTestBackend(t)
	if _, err := backend.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	if _, err := backend.ExecContext(ctx, `CREATE TABLE "migration_parent" ("id" INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create parent table: %v", err)
	}
	if _, err := backend.ExecContext(ctx, `CREATE TABLE "migration_child" (`+
		`"parent_id" INTEGER, `+
		`FOREIGN KEY ("parent_id") REFERENCES "migration_parent" ("id") DEFERRABLE INITIALLY DEFERRED)`); err != nil {
		t.Fatalf("create child table: %v", err)
	}

	transactionInterface, err := backend.BeginMigration(ctx)
	if err != nil {
		t.Fatalf("begin migration: %v", err)
	}
	transaction, ok := transactionInterface.(*migrationTransaction)
	if !ok {
		t.Fatalf("BeginMigration() transaction = %T, want *migrationTransaction", transactionInterface)
	}
	if _, err := transaction.transaction.ExecContext(ctx, `INSERT INTO "migration_child" ("parent_id") VALUES (404)`); err != nil {
		t.Fatalf("insert deferred foreign-key violation: %v", err)
	}
	if err := transaction.RecordApplied(ctx, "news", "0001_commit_failure"); err != nil {
		t.Fatalf("record migration before commit: %v", err)
	}
	if err := transaction.Commit(ctx); err == nil {
		t.Fatal("Commit() error = nil, want deferred foreign-key failure")
	}
	// This mirrors Executor's best-effort rollback after a failed commit. The
	// modernc driver has already resolved the failed sql.Tx, so it is a no-op.
	if err := transaction.Rollback(ctx); err != nil {
		t.Fatalf("Rollback() after failed Commit(): %v", err)
	}

	var childRows int
	if err := backend.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM "migration_child"`).Scan(&childRows); err != nil {
		t.Fatalf("query child table after failed commit: %v", err)
	}
	if childRows != 0 {
		t.Fatalf("child rows after failed commit = %d, want 0", childRows)
	}
	if sqliteTableExists(t, backend, migrationRecorderTable) {
		t.Fatal("recorder table survived failed commit")
	}

	// A fresh transaction proves the only pooled connection is usable again.
	cleanTransaction, err := backend.BeginMigration(ctx)
	if err != nil {
		t.Fatalf("begin migration after failed commit: %v", err)
	}
	if err := cleanTransaction.RecordApplied(ctx, "news", "0001_recovered"); err != nil {
		t.Fatalf("record migration after failed commit: %v", err)
	}
	if err := cleanTransaction.Commit(ctx); err != nil {
		t.Fatalf("commit migration after recovery: %v", err)
	}
	assertMigrationRecords(t, backend, "news.0001_recovered")
}

func TestSQLiteMigrationDropColumnRejectsDependenciesWithoutRebuild(t *testing.T) {
	tests := []struct {
		name    string
		create  string
		cleanup string
	}{
		{
			name:    "index",
			create:  `CREATE INDEX "article_summary_idx" ON "godj_migration_article" ("summary")`,
			cleanup: `DROP INDEX "article_summary_idx"`,
		},
		{
			name:    "partial index predicate",
			create:  `CREATE INDEX "article_title_partial_idx" ON "godj_migration_article" ("title") WHERE "summary" IS NOT NULL`,
			cleanup: `DROP INDEX "article_title_partial_idx"`,
		},
		{
			name:    "trigger",
			create:  `CREATE TRIGGER "article_summary_trigger" AFTER UPDATE OF "summary" ON "godj_migration_article" BEGIN SELECT 1; END`,
			cleanup: `DROP TRIGGER "article_summary_trigger"`,
		},
		{
			name:    "trigger select star from another table",
			create:  `CREATE TRIGGER "recorder_article_star_trigger" AFTER INSERT ON "godj_migrations" BEGIN SELECT * FROM "godj_migration_article"; END`,
			cleanup: `DROP TRIGGER "recorder_article_star_trigger"`,
		},
		{
			name:    "view",
			create:  `CREATE VIEW "article_summary_view" AS SELECT "summary" FROM "godj_migration_article"`,
			cleanup: `DROP VIEW "article_summary_view"`,
		},
		{
			name:    "view select star with quoted alias",
			create:  `CREATE VIEW "article_star_view" AS SELECT "ArticleAlias".* FROM "godj_migration_article" AS "ArticleAlias"`,
			cleanup: `DROP VIEW "article_star_view"`,
		},
		{
			name: "foreign key",
			create: `CREATE TABLE "article_summary_reference" (` +
				`"summary" VARCHAR(200), ` +
				`FOREIGN KEY ("summary") REFERENCES "godj_migration_article" ("summary"))`,
			cleanup: `DROP TABLE "article_summary_reference"`,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			backend := openMigrationTestBackend(t)
			executor := migrations.Executor{Backend: backend}
			initial, summary := migrationTestMigrations()
			state1, err := executor.Apply(ctx, migrations.EmptyProjectState(), initial)
			if err != nil {
				t.Fatalf("apply initial migration: %v", err)
			}
			state2, err := executor.Apply(ctx, state1, summary)
			if err != nil {
				t.Fatalf("apply summary migration: %v", err)
			}
			if _, err := backend.ExecContext(ctx, `INSERT INTO "godj_migration_article" ("title", "published", "summary") VALUES ('kept', 1, 'value')`); err != nil {
				t.Fatalf("seed row: %v", err)
			}
			if _, err := backend.ExecContext(ctx, test.create); err != nil {
				t.Fatalf("create dependency: %v", err)
			}
			beforeSQL := sqliteTableSQL(t, backend, "godj_migration_article")

			stateAfter, err := executor.Unapply(ctx, state2, summary)
			assertSQLiteMigrationError(t, err, migrations.CategoryCapability, migrations.CodeUnsupported, 0, "AddField")
			if !migrationbackend.IsCapabilityError(err) {
				t.Fatalf("Unapply() error = %v, want capability error", err)
			}
			if !stateAfter.Equal(state2) {
				t.Fatal("capability failure changed returned state")
			}
			if afterSQL := sqliteTableSQL(t, backend, "godj_migration_article"); afterSQL != beforeSQL {
				t.Fatalf("table SQL changed after rejected drop:\n before: %s\n  after: %s", beforeSQL, afterSQL)
			}
			assertSQLiteColumns(t, backend, "godj_migration_article", "id", "title", "published", "summary")
			var summaryValue string
			if err := backend.database.QueryRowContext(ctx, `SELECT "summary" FROM "godj_migration_article" WHERE "title" = 'kept'`).Scan(&summaryValue); err != nil || summaryValue != "value" {
				t.Fatalf("row after rejected drop: summary=%q err=%v", summaryValue, err)
			}
			assertMigrationRecords(t, backend, "news.0001_initial", "news.0002_summary")

			if _, err := backend.ExecContext(ctx, test.cleanup); err != nil {
				t.Fatalf("remove dependency: %v", err)
			}
			if _, err := executor.Unapply(ctx, state2, summary); err != nil {
				t.Fatalf("unapply after dependency cleanup: %v", err)
			}
		})
	}
}

func TestSQLiteMigrationDropColumnClassifiesTableDefinitionDependencies(t *testing.T) {
	tests := []struct {
		name       string
		definition string
	}{
		{
			name: "check constraint",
			definition: `CREATE TABLE "drop_dependency" (` +
				`"id" INTEGER PRIMARY KEY, "summary" TEXT, CHECK ("summary" <> 'bad'))`,
		},
		{
			name: "generated column",
			definition: `CREATE TABLE "drop_dependency" (` +
				`"id" INTEGER PRIMARY KEY, "summary" TEXT, ` +
				`"summary_length" INTEGER GENERATED ALWAYS AS (length("summary")) STORED)`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			backend := openMigrationTestBackend(t)
			if _, err := backend.ExecContext(ctx, test.definition); err != nil {
				t.Fatalf("create dependency table: %v", err)
			}
			transaction, err := backend.BeginMigration(ctx)
			if err != nil {
				t.Fatalf("begin migration: %v", err)
			}
			field := ir.Field{Name: "summary", GoName: "Summary", Column: "summary", Kind: ir.FieldChar, Nullable: true, MaxLength: 200}
			err = transaction.RemoveField(ctx, ir.Model{Name: "article", GoName: "Article", DBTable: "drop_dependency"}, field)
			if !migrationbackend.IsCapabilityError(err) {
				t.Fatalf("RemoveField() error = %v, want capability error", err)
			}
			if err := transaction.Rollback(ctx); err != nil {
				t.Fatalf("rollback rejected RemoveField: %v", err)
			}
			assertSQLiteColumns(t, backend, "drop_dependency", "id", "summary")
		})
	}
}

func TestSQLiteMigrationRejectsNonNullableAddField(t *testing.T) {
	ctx := context.Background()
	backend := openMigrationTestBackend(t)
	initial, _ := migrationTestMigrations()
	state1, err := (migrations.Executor{Backend: backend}).Apply(ctx, migrations.EmptyProjectState(), initial)
	if err != nil {
		t.Fatalf("apply initial migration: %v", err)
	}
	model, exists := state1.Model("news", "article")
	if !exists {
		t.Fatal("initial model is missing")
	}
	transaction, err := backend.BeginMigration(ctx)
	if err != nil {
		t.Fatalf("begin migration: %v", err)
	}
	field := ir.Field{Name: "required", GoName: "Required", Column: "required", Kind: ir.FieldChar, MaxLength: 20}
	err = transaction.AddField(ctx, model, field)
	if !migrationbackend.IsCapabilityError(err) {
		t.Fatalf("AddField() error = %v, want capability error", err)
	}
	if err := transaction.Rollback(ctx); err != nil {
		t.Fatalf("rollback rejected AddField: %v", err)
	}
	assertSQLiteColumns(t, backend, "godj_migration_article", "id", "title", "published")
}

func TestSQLiteMigrationRejectsNullableAddFieldDefaultWithoutBackfill(t *testing.T) {
	ctx := context.Background()
	backend := openMigrationTestBackend(t)
	executor := migrations.Executor{Backend: backend}
	initial, _ := migrationTestMigrations()
	state1, err := executor.Apply(ctx, migrations.EmptyProjectState(), initial)
	if err != nil {
		t.Fatalf("apply initial migration: %v", err)
	}
	defaultValue := &ir.ScalarDefault{Kind: ir.ScalarString, String: "backfilled"}
	migration := migrations.Migration{
		App:  "news",
		Name: "0002_summary_default",
		Operations: []migrations.Operation{migrations.AddField{
			AppLabel:  "news",
			ModelName: "article",
			Field: ir.Field{
				Name: "summary", GoName: "Summary", Column: "summary",
				Kind: ir.FieldChar, Nullable: true, MaxLength: 200, Default: defaultValue,
			},
		}},
	}
	after, err := executor.Apply(ctx, state1, migration)
	assertSQLiteMigrationError(t, err, migrations.CategoryCapability, migrations.CodeUnsupported, 0, "AddField")
	if !migrationbackend.IsCapabilityError(err) {
		t.Fatalf("Apply() error = %v, want capability error", err)
	}
	if !after.Equal(state1) {
		t.Fatal("unsupported default AddField changed returned state")
	}
	assertSQLiteColumns(t, backend, "godj_migration_article", "id", "title", "published")
	assertMigrationRecords(t, backend, "news.0001_initial")
}

func TestSQLiteMigrationHonorsCanceledContextAndReleasesConnection(t *testing.T) {
	backend := openMigrationTestBackend(t)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := backend.BeginMigration(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("BeginMigration(canceled) error = %v, want context.Canceled", err)
	}

	transaction, err := backend.BeginMigration(context.Background())
	if err != nil {
		t.Fatalf("begin migration: %v", err)
	}
	err = transaction.CreateModel(canceled, migrationTestModel(false))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CreateModel(canceled) error = %v, want context.Canceled", err)
	}
	if err := transaction.Rollback(context.Background()); err != nil {
		t.Fatalf("rollback canceled operation: %v", err)
	}
	if sqliteTableExists(t, backend, "godj_migration_article") {
		t.Fatal("canceled CreateModel created a table")
	}

	initial, _ := migrationTestMigrations()
	if _, err := (migrations.Executor{Backend: backend}).Apply(context.Background(), migrations.EmptyProjectState(), initial); err != nil {
		t.Fatalf("apply after canceled transaction: %v", err)
	}
}

func TestSQLiteMigrationCloseConcurrentWithTerminalOperationDoesNotHang(t *testing.T) {
	for _, terminal := range []string{"commit", "rollback"} {
		terminal := terminal
		t.Run(terminal, func(t *testing.T) {
			ctx := context.Background()
			backend := openMigrationTestBackend(t)
			transaction, err := backend.BeginMigration(ctx)
			if err != nil {
				t.Fatalf("BeginMigration() error = %v", err)
			}
			if err := transaction.CreateModel(ctx, migrationTestModel(false)); err != nil {
				t.Fatalf("CreateModel() error = %v", err)
			}

			start := make(chan struct{})
			results := make(chan struct {
				operation string
				err       error
			}, 2)
			go func() {
				<-start
				results <- struct {
					operation string
					err       error
				}{operation: "close", err: backend.Close()}
			}()
			go func() {
				<-start
				var terminalErr error
				if terminal == "commit" {
					terminalErr = transaction.Commit(ctx)
				} else {
					terminalErr = transaction.Rollback(ctx)
				}
				results <- struct {
					operation string
					err       error
				}{operation: terminal, err: terminalErr}
			}()
			close(start)

			for range 2 {
				select {
				case result := <-results:
					if result.err != nil {
						t.Errorf("%s error = %v", result.operation, result.err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("concurrent Backend.Close and migration terminal operation hung")
				}
			}
		})
	}
}

type articleMigrationRow struct {
	id        int64
	title     string
	published bool
}

func migrationTestMigrations() (migrations.Migration, migrations.Migration) {
	return migrations.Migration{
			App:  "news",
			Name: "0001_initial",
			Operations: []migrations.Operation{
				migrations.CreateModel{AppLabel: "news", Model: migrationTestModel(false)},
			},
		}, migrations.Migration{
			App:  "news",
			Name: "0002_summary",
			Operations: []migrations.Operation{
				migrations.AddField{
					AppLabel:  "news",
					ModelName: "article",
					Field: ir.Field{
						Name:      "summary",
						GoName:    "Summary",
						Column:    "summary",
						Kind:      ir.FieldChar,
						Nullable:  true,
						MaxLength: 200,
					},
				},
			},
		}
}

func openMigrationTestBackend(t *testing.T) *Backend {
	t.Helper()
	backend, err := OpenMemory(context.Background(), "migration-"+t.Name())
	if err != nil {
		t.Fatalf("OpenMemory(): %v", err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("Close(): %v", err)
		}
	})
	return backend
}

func assertSQLiteColumns(t *testing.T, backend *Backend, table string, want ...string) {
	t.Helper()
	quotedTable, err := quoteIdentifier(table)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := backend.database.QueryContext(context.Background(), "PRAGMA table_info("+quotedTable+")")
	if err != nil {
		t.Fatalf("inspect columns for %s: %v", table, err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var sequence, notNull, primaryKey int
		var name, declaredType string
		var defaultValue sql.NullString
		if err := rows.Scan(&sequence, &name, &declaredType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan column for %s: %v", table, err)
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate columns for %s: %v", table, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("columns for %s = %v, want %v", table, got, want)
	}
}

func assertArticleRows(t *testing.T, backend *Backend, want ...articleMigrationRow) {
	t.Helper()
	rows, err := backend.database.QueryContext(context.Background(), `SELECT "id", "title", "published" FROM "godj_migration_article" ORDER BY "id"`)
	if err != nil {
		t.Fatalf("query article rows: %v", err)
	}
	defer rows.Close()
	var got []articleMigrationRow
	for rows.Next() {
		var row articleMigrationRow
		if err := rows.Scan(&row.id, &row.title, &row.published); err != nil {
			t.Fatalf("scan article row: %v", err)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate article rows: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("article rows = %#v, want %#v", got, want)
	}
}

func assertMigrationRecords(t *testing.T, backend *Backend, want ...string) {
	t.Helper()
	rows, err := backend.database.QueryContext(context.Background(), `SELECT "app", "name" FROM "godj_migrations" ORDER BY "app", "name"`)
	if err != nil {
		t.Fatalf("query migration records: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var app, name string
		if err := rows.Scan(&app, &name); err != nil {
			t.Fatalf("scan migration record: %v", err)
		}
		got = append(got, app+"."+name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate migration records: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("migration records = %v, want %v", got, want)
	}
}

func sqliteTableExists(t *testing.T, backend *Backend, table string) bool {
	t.Helper()
	var count int
	if err := backend.database.QueryRowContext(
		context.Background(),
		`SELECT COUNT(*) FROM "sqlite_schema" WHERE "type" = 'table' AND "name" = ?`,
		table,
	).Scan(&count); err != nil {
		t.Fatalf("check table %s: %v", table, err)
	}
	return count == 1
}

func sqliteTableSQL(t *testing.T, backend *Backend, table string) string {
	t.Helper()
	var statement string
	if err := backend.database.QueryRowContext(
		context.Background(),
		`SELECT "sql" FROM "sqlite_schema" WHERE "type" = 'table' AND "name" = ?`,
		table,
	).Scan(&statement); err != nil {
		t.Fatalf("read table SQL for %s: %v", table, err)
	}
	return statement
}

func assertSQLiteMigrationError(t *testing.T, err error, category migrations.ErrorCategory, code migrations.ErrorCode, operationIndex int, operation string) {
	t.Helper()
	var migrationError *migrations.Error
	if !errors.As(err, &migrationError) {
		t.Fatalf("error = %#v, want *migrations.Error", err)
	}
	if migrationError.Category != category || migrationError.Code != code || migrationError.OperationIndex != operationIndex || migrationError.Operation != operation {
		t.Fatalf(
			"migration error = %s, want category=%s code=%s operation[%d]=%s",
			fmt.Sprintf("%#v", migrationError),
			category,
			code,
			operationIndex,
			operation,
		)
	}
}
