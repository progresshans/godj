package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

type sqliteRelationRemakeFixture struct {
	backend    *Backend
	target     ir.Model
	before     ir.Model
	after      ir.Model
	removed    ir.Field
	transition migrationbackend.HistoryTransition
	intent     migrationbackend.MigrationIntent
}

type sqliteRelationRemakeDurableSnapshot struct {
	revision migrationRevisionSnapshot
	schema   []string
	rows     []string
	sequence []string
}

type sqliteRelationRemakeBusyError struct {
	stage string
}

func (e *sqliteRelationRemakeBusyError) Error() string {
	return "injected SQLITE_BUSY during " + e.stage
}

func (*sqliteRelationRemakeBusyError) Code() int {
	return 5 // SQLITE_BUSY.
}

func TestSQLiteRelationRemakeRejectsClosedShapeHazardsBeforeClaim(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T, sqliteRelationRemakeFixture)
		detail  string
	}{
		{
			name: "any inbound foreign key",
			prepare: func(t *testing.T, fixture sqliteRelationRemakeFixture) {
				mustSQLiteRelationRemakeExec(t, fixture.backend,
					`CREATE TABLE "outside_child" ("id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, `+
						`"article_id" INTEGER NOT NULL, FOREIGN KEY ("article_id") REFERENCES "news_article" ("id") ON DELETE NO ACTION)`)
			},
			detail: "inbound foreign key",
		},
		{
			name: "user index",
			prepare: func(t *testing.T, fixture sqliteRelationRemakeFixture) {
				mustSQLiteRelationRemakeExec(t, fixture.backend, `CREATE INDEX "article_title_idx" ON "news_article" ("title")`)
			},
			detail: "index",
		},
		{
			name: "user trigger",
			prepare: func(t *testing.T, fixture sqliteRelationRemakeFixture) {
				mustSQLiteRelationRemakeExec(t, fixture.backend, `CREATE TABLE "audit" ("id" INTEGER)`)
				mustSQLiteRelationRemakeExec(t, fixture.backend,
					`CREATE TRIGGER "article_audit" AFTER UPDATE ON "news_article" BEGIN INSERT INTO "audit" ("id") VALUES (1); END`)
			},
			detail: "trigger",
		},
		{
			name: "referencing view",
			prepare: func(t *testing.T, fixture sqliteRelationRemakeFixture) {
				mustSQLiteRelationRemakeExec(t, fixture.backend, `CREATE VIEW "article_view" AS SELECT "title" FROM "news_article"`)
			},
			detail: "view",
		},
		{
			name: "generated column",
			prepare: func(t *testing.T, fixture sqliteRelationRemakeFixture) {
				mustSQLiteRelationRemakeExec(t, fixture.backend,
					`ALTER TABLE "news_article" ADD COLUMN "derived" TEXT GENERATED ALWAYS AS ("title") VIRTUAL`)
			},
			detail: "columns",
		},
		{
			name: "unsupported table option",
			prepare: func(t *testing.T, fixture sqliteRelationRemakeFixture) {
				mustSQLiteRelationRemakeExec(t, fixture.backend, `PRAGMA writable_schema = ON`)
				mustSQLiteRelationRemakeExec(t, fixture.backend,
					`UPDATE main.sqlite_schema SET "sql" = "sql" || ' STRICT' WHERE "type"='table' AND "name"='news_article'`)
				mustSQLiteRelationRemakeExec(t, fixture.backend, `PRAGMA writable_schema = OFF`)
			},
			detail: "canonical declaration",
		},
		{
			name: "case variant sequence authority",
			prepare: func(t *testing.T, fixture sqliteRelationRemakeFixture) {
				mustSQLiteRelationRemakeExec(t, fixture.backend,
					`UPDATE main.sqlite_sequence SET "name"='NeWs_ArTiClE' WHERE "name"='news_article'`)
			},
			detail: "does not exactly match",
		},
		{
			name: "non integral sequence authority",
			prepare: func(t *testing.T, fixture sqliteRelationRemakeFixture) {
				mustSQLiteRelationRemakeExec(t, fixture.backend,
					`UPDATE main.sqlite_sequence SET "seq"='invalid' WHERE "name"='news_article'`)
			},
			detail: "sequence catalog",
		},
		{
			name: "negative sequence authority",
			prepare: func(t *testing.T, fixture sqliteRelationRemakeFixture) {
				mustSQLiteRelationRemakeExec(t, fixture.backend,
					`UPDATE main.sqlite_sequence SET "seq"=-1 WHERE "name"='news_article'`)
			},
			detail: "sequence catalog",
		},
		{
			name: "deterministic temporary collision",
			prepare: func(t *testing.T, fixture sqliteRelationRemakeFixture) {
				temporary := sqliteRelationRemakeTemporary(
					fixture.transition,
					0,
					fixture.after.DBTable,
					fixture.removed.Column,
				)
				mustSQLiteRelationRemakeExec(t, fixture.backend, `CREATE TEMP TABLE `+mustQuoteSQLiteRelationRemake(t, temporary)+` ("id" INTEGER)`)
			},
			detail: "temporary table",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := prepareSQLiteRelationRemakeFixture(t)
			test.prepare(t, fixture)
			before := readSQLiteRelationRemakeDurableSnapshot(t, fixture.backend)
			session := openSQLiteRelationSession(t, fixture.backend)
			if _, err := session.ReadAppliedMigrations(context.Background()); err != nil {
				t.Fatalf("ReadAppliedMigrations(hazard): %v", err)
			}
			concrete := session.(*sqliteRevisionFencedSession)
			checkpoints := make([]sqliteRelationBeginCheckpoint, 0)
			concrete.relationBeginCheckpoint = func(checkpoint sqliteRelationBeginCheckpoint) {
				checkpoints = append(checkpoints, checkpoint)
			}
			transaction, err := session.BeginMigration(
				context.Background(),
				fixture.transition,
				fixture.intent,
			)
			if transaction != nil || err == nil || !strings.Contains(err.Error(), test.detail) {
				t.Fatalf("BeginMigration(hazard) = (%v, %v), want %q", transaction, err, test.detail)
			}
			assertSQLiteRelationCapabilityFeature(t, err, "sqlite_relation_migration")
			assertSQLiteRelationNoClaimCheckpoint(t, checkpoints)
			if err := session.Close(context.Background()); err != nil {
				t.Fatalf("Close(hazard session): %v", err)
			}
			after := readSQLiteRelationRemakeDurableSnapshot(t, fixture.backend)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("preclaim hazard changed durable state:\nbefore=%+v\nafter=%+v", before, after)
			}
		})
	}
}

func TestSQLiteRelationRemakePreservesRowsHighWaterAndRemainingForeignKey(t *testing.T) {
	fixture := prepareSQLiteRelationRemakeFixture(t)
	ctx := context.Background()
	session := openSQLiteRelationSession(t, fixture.backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatalf("ReadAppliedMigrations(success): %v", err)
	}
	transaction, err := session.BeginMigration(ctx, fixture.transition, fixture.intent)
	if err != nil {
		t.Fatalf("BeginMigration(success): %v", err)
	}
	// Mutation of the caller-visible aliases after Begin cannot change the
	// physical plan sealed from the exact transition tuple and snapshots.
	fixture.intent.Operations[0].Before.DBTable = "mutated_source"
	fixture.intent.Operations[0].Targets[0].TargetModel.DBTable = "mutated_target"
	if err := transaction.RemoveField(ctx, fixture.after.Clone(), fixture.removed.Clone()); err != nil {
		t.Fatalf("RemoveField(success): %v", err)
	}
	if err := transaction.RecordUnapplied(ctx, fixture.transition.Migration.App, fixture.transition.Migration.Name); err != nil {
		t.Fatalf("RecordUnapplied(success): %v", err)
	}
	outcome, err := transaction.CommitFenced(ctx)
	if err != nil || outcome.Durability != migrationbackend.CommitCommitted {
		t.Fatalf("CommitFenced(success)=(%+v,%v)", outcome, err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatalf("Close(success): %v", err)
	}

	rows, err := fixture.backend.database.QueryContext(
		ctx,
		`SELECT "id", "title", "author_id" FROM "news_article" ORDER BY "id"`,
	)
	if err != nil {
		t.Fatalf("read retained remake rows: %v", err)
	}
	wantRows := []string{"3\x00one\x005", "8\x00two\x005"}
	gotRows := make([]string, 0, len(wantRows))
	for rows.Next() {
		var id, author int64
		var title string
		if err := rows.Scan(&id, &title, &author); err != nil {
			_ = rows.Close()
			t.Fatalf("scan retained remake row: %v", err)
		}
		gotRows = append(gotRows, fmt.Sprintf("%d\x00%s\x00%d", id, title, author))
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		t.Fatalf("iterate retained remake rows: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close retained remake rows: %v", err)
	}
	if !reflect.DeepEqual(gotRows, wantRows) {
		t.Fatalf("retained remake rows=%v, want %v", gotRows, wantRows)
	}
	if sqliteRelationTestColumnExists(t, fixture.backend, "news_article", "editor_id") {
		t.Fatal("remade source retained removed editor column")
	}
	foreignKeys, err := readSQLiteRelationForeignKeys(ctx, fixture.backend.database, "news_article", 1)
	if err != nil || len(foreignKeys) != 1 || foreignKeys[0].from != "author_id" ||
		foreignKeys[0].table != "news_author" || foreignKeys[0].to != "id" ||
		foreignKeys[0].onUpdate != "NO ACTION" || foreignKeys[0].onDelete != "NO ACTION" {
		t.Fatalf("remaining remake foreign key=(%+v,%v)", foreignKeys, err)
	}
	var sequence int64
	if err := fixture.backend.database.QueryRowContext(
		ctx,
		`SELECT "seq" FROM main.sqlite_sequence WHERE "name"='news_article'`,
	).Scan(&sequence); err != nil || sequence != 100 {
		t.Fatalf("remake sequence=(%d,%v), want 100", sequence, err)
	}
	mustSQLiteRelationRemakeExec(t, fixture.backend,
		`INSERT INTO "news_article" ("title", "author_id") VALUES ('after', 5)`)
	var next int64
	if err := fixture.backend.database.QueryRowContext(
		ctx,
		`SELECT "id" FROM "news_article" WHERE "title"='after'`,
	).Scan(&next); err != nil || next != 101 {
		t.Fatalf("post-remake AutoField=(%d,%v), want 101", next, err)
	}
	temporary := sqliteRelationRemakeTemporary(
		fixture.transition,
		0,
		fixture.after.DBTable,
		fixture.removed.Column,
	)
	if sqliteRelationTestTableExists(t, fixture.backend, temporary) {
		t.Fatalf("successful remake leaked temporary table %q", temporary)
	}
}

func TestSQLiteRelationRemakeMutationFaultsRollbackWithoutRetryOrTemporaryLeak(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		contains string
	}{
		{name: "create", method: "exec", contains: `CREATE TABLE "main"."__godj_relation_`},
		{name: "copy", method: "exec", contains: `INSERT INTO "main"."__godj_relation_`},
		{name: "drop", method: "exec", contains: `DROP TABLE "main"."news_article"`},
		{name: "rename", method: "exec", contains: `ALTER TABLE "main"."__godj_relation_`},
		{name: "sequence clear", method: "exec", contains: `DELETE FROM "main"."sqlite_sequence"`},
		{name: "sequence restore", method: "exec", contains: `INSERT INTO "main"."sqlite_sequence"`},
		{name: "final foreign key check", method: "query", contains: `foreign_key_check`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := prepareSQLiteRelationRemakeFixture(t)
			before := readSQLiteRelationRemakeDurableSnapshot(t, fixture.backend)
			session := openSQLiteRelationSession(t, fixture.backend)
			if _, err := session.ReadAppliedMigrations(context.Background()); err != nil {
				t.Fatalf("ReadAppliedMigrations(fault): %v", err)
			}
			concrete := session.(*sqliteRevisionFencedSession)
			cause := errors.New("injected relation remake " + test.name + " fault")
			var fault *sqliteNullableRelationAddFaultConnection
			concrete.relationConnectionHook = func(connection migrationPinnedConnection) migrationPinnedConnection {
				fault = &sqliteNullableRelationAddFaultConnection{
					migrationPinnedConnection: connection,
					method:                    test.method,
					contains:                  test.contains,
					faultErr:                  cause,
					remaining:                 1,
				}
				return fault
			}
			transaction, err := session.BeginMigration(
				context.Background(),
				fixture.transition,
				fixture.intent,
			)
			if err != nil {
				t.Fatalf("BeginMigration(fault): %v", err)
			}
			firstErr := transaction.RemoveField(context.Background(), fixture.after.Clone(), fixture.removed.Clone())
			if !errors.Is(firstErr, cause) {
				t.Fatalf("RemoveField(%s) error=%v, want cause", test.name, firstErr)
			}
			var capability *migrationbackend.CapabilityError
			if errors.As(firstErr, &capability) {
				t.Fatalf("post-mutation fault leaked CapabilityError: %#v", capability)
			}
			secondErr := transaction.RemoveField(context.Background(), fixture.after.Clone(), fixture.removed.Clone())
			if !errors.Is(secondErr, cause) || fault.remaining != 0 {
				t.Fatalf("sticky no-retry error=(%v, remaining=%d)", secondErr, fault.remaining)
			}
			if err := transaction.Rollback(context.Background()); err != nil {
				t.Fatalf("Rollback(%s): %v", test.name, err)
			}
			if fault.rollbackCalls != 1 {
				t.Fatalf("Rollback(%s) calls=%d, want 1", test.name, fault.rollbackCalls)
			}
			if err := session.Close(context.Background()); err != nil {
				t.Fatalf("Close(%s session): %v", test.name, err)
			}
			after := readSQLiteRelationRemakeDurableSnapshot(t, fixture.backend)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("%s fault changed durable state:\nbefore=%+v\nafter=%+v", test.name, before, after)
			}
			temporary := sqliteRelationRemakeTemporary(
				fixture.transition,
				0,
				fixture.after.DBTable,
				fixture.removed.Column,
			)
			if sqliteRelationTestTableExists(t, fixture.backend, temporary) {
				t.Fatalf("%s fault leaked temporary table %q", test.name, temporary)
			}
		})
	}
}

func TestSQLiteLoadedRelationRemakeBusyFaultsStayOwnedByOriginalAddField(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		contains string
	}{
		{name: "create", method: "exec", contains: `CREATE TABLE "main"."__godj_relation_`},
		{name: "copy", method: "exec", contains: `INSERT INTO "main"."__godj_relation_`},
		{name: "drop", method: "exec", contains: `DROP TABLE "main"."news_article"`},
		{name: "rename", method: "exec", contains: `ALTER TABLE "main"."__godj_relation_`},
		{name: "sequence clear", method: "exec", contains: `DELETE FROM "main"."sqlite_sequence"`},
		{name: "sequence restore", method: "exec", contains: `INSERT INTO "main"."sqlite_sequence"`},
		{name: "final foreign key check", method: "query", contains: "foreign_key_check"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "loaded-remake-busy.sqlite")
			loaded := loadSQLiteLoadedNullableRelationAddSet(t, "remake-busy-"+test.name)
			database := openSQLiteLoadedRelationTaxonomyBackend(t, path)
			seedState, err := (migrations.Executor{Backend: database}).Migrate(
				ctx,
				loaded,
				migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{
					App: "news", Name: "0002_article",
				})),
			)

			if err != nil {
				t.Fatalf("Migrate(relation remake seed): %v", err)
			}
			assertSQLiteLoadedNullableRelationAddSeedState(t, seedState)
			if _, err := database.ExecContext(ctx, `INSERT INTO "news_author" ("name") VALUES ('Ada'), ('Grace')`); err != nil {
				t.Fatalf("insert relation remake authors: %v", err)
			}
			if _, err := database.ExecContext(ctx, `INSERT INTO "news_article" ("title", "author_id") VALUES ('first', 1), ('second', 2)`); err != nil {
				t.Fatalf("insert relation remake articles: %v", err)
			}
			appliedState, err := (migrations.Executor{Backend: database}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
			if err != nil {
				t.Fatalf("Migrate(relation remake Add): %v", err)
			}
			if err := database.Close(); err != nil {
				t.Fatalf("close relation remake seed: %v", err)
			}
			before := readSQLiteNullableRelationAddSnapshot(t, path)

			database = openSQLiteLoadedRelationTaxonomyBackend(t, path)
			cause := &sqliteRelationRemakeBusyError{stage: test.name}
			connectionFault := &sqliteRelationBeginFaultConnection{
				method: test.method, contains: test.contains, remaining: 1, faultErr: cause,
			}
			probe := &sqliteLoadedRelationTaxonomyBackend{Backend: database, fault: connectionFault}
			state, err := (migrations.Executor{Backend: probe}).Migrate(
				ctx,
				loaded,
				migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{
					App: "news", Name: "0002_article",
				})),
			)

			var migrationError *migrations.Error
			if !errors.As(err, &migrationError) || migrationError == nil ||
				migrationError.Category != migrations.CategoryExecution ||
				migrationError.Code != migrations.CodeOperationFailed ||
				migrationError.Direction != migrations.DirectionBackward ||
				migrationError.App != "news" || migrationError.Migration != "0003_editor" ||
				migrationError.OperationIndex != 0 || migrationError.Operation != "AddField" ||
				migrationError.RollbackCause != nil || !errors.Is(err, cause) {
				t.Fatalf("%s remake taxonomy = %#v (%v), want Execution/OperationFailed backward news.0003_editor operation[0]=AddField", test.name, migrationError, err)
			}
			if migrationbackend.IsRevisionFenceError(migrationError.Cause) ||
				migrationbackend.IsCapabilityError(migrationError.Cause) {
				t.Fatalf("%s remake SQLITE_BUSY escaped execution ownership: %#v", test.name, migrationError.Cause)
			}
			if !state.Equal(appliedState) {
				t.Fatalf("%s remake returned state differs from exact pre-step state", test.name)
			}
			wantCheckpoints := []sqliteRelationBeginCheckpoint{
				sqliteRelationCheckpointForeignKeysSet,
				sqliteRelationCheckpointForeignKeysRead,
				sqliteRelationCheckpointTransactionBegun,
				sqliteRelationCheckpointPhysicalPreflightComplete,
				sqliteRelationCheckpointRevisionClaimStarting,
				sqliteRelationCheckpointRevisionClaimed,
			}
			if !reflect.DeepEqual(probe.checkpoints, wantCheckpoints) {
				t.Fatalf("%s remake checkpoints=%v, want %v", test.name, probe.checkpoints, wantCheckpoints)
			}
			if connectionFault.remaining != 0 || connectionFault.closeCalls != 1 ||
				connectionFault.rawCalls != 0 || connectionFault.rollbackCalls != 1 ||
				probe.transactionRollbackCalls != 1 {
				t.Fatalf(
					"%s remake cleanup = remaining:%d close:%d raw:%d connection rollback:%d transaction rollback:%d",
					test.name,
					connectionFault.remaining,
					connectionFault.closeCalls,
					connectionFault.rawCalls,
					connectionFault.rollbackCalls,
					probe.transactionRollbackCalls,
				)
			}
			if err := database.Close(); err != nil {
				t.Fatalf("close relation remake fault database: %v", err)
			}
			after := readSQLiteNullableRelationAddSnapshot(t, path)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("%s remake fault changed reopened durable snapshot:\nbefore=%+v\nafter=%+v", test.name, before, after)
			}
		})
	}
}

func TestSQLiteRelationRemakeTemporaryNameUsesVersionedTransitionTuple(t *testing.T) {
	transition := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "blog", Name: "0002_editor"},
		Kind:      migrationbackend.HistoryTransitionUnapply,
	}
	base := sqliteRelationRemakeTemporary(transition, 7, "news_article", "editor_id")
	if len(base) != len("__godj_relation_")+32 || !strings.HasPrefix(base, "__godj_relation_") {
		t.Fatalf("temporary name=%q, want prefix plus 32 lowercase hex", base)
	}
	variants := []string{
		sqliteRelationRemakeTemporary(transition, 8, "news_article", "editor_id"),
		sqliteRelationRemakeTemporary(transition, 7, "news_article", "editor_i"),
		sqliteRelationRemakeTemporary(transition, 7, "news_articlee", "ditor_id"),
	}
	transition.Migration.Name = "0003_editor"
	variants = append(variants, sqliteRelationRemakeTemporary(transition, 7, "news_article", "editor_id"))
	for _, variant := range variants {
		if variant == base {
			t.Fatalf("versioned length-prefixed tuple collided: %q", variant)
		}
	}
}

func prepareSQLiteRelationRemakeFixture(t *testing.T) sqliteRelationRemakeFixture {
	t.Helper()
	ctx := context.Background()
	name := "relation-remake-" + strings.NewReplacer("/", "-", " ", "-").Replace(t.Name())
	database, err := OpenMemory(ctx, name)
	if err != nil {
		t.Fatalf("OpenMemory(remake): %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	target, before, author := sqliteRelationTestModels()
	initial := migrationbackend.AppliedMigration{App: "news", Name: "0001_initial"}
	seedSQLiteRelationPhysicalSchemaAndHistory(t, ctx, database, initial, target, before, author)
	mustSQLiteRelationRemakeExec(t, database, `INSERT INTO "news_author" ("id", "name") VALUES (5, 'Ada')`)
	mustSQLiteRelationRemakeExec(t, database,
		`INSERT INTO "news_article" ("id", "title", "author_id") VALUES (3, 'one', 5), (8, 'two', 5)`)
	mustSQLiteRelationRemakeExec(t, database,
		`UPDATE main.sqlite_sequence SET "seq"=100 WHERE "name"='news_article'`)
	removed := author.Clone()
	removed.Name = "editor"
	removed.GoName = "Editor"
	removed.Column = "editor_id"
	removed.Nullable = true
	removed.Relation.Reverse.Name = "edited_articles"
	after := before.Clone()
	after.Fields = append(after.Fields, removed)
	apply := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0002_editor"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}
	addIntent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
		OperationIndex: 0,
		Kind:           migrationbackend.MigrationAddField,
		Before:         before,
		After:          after,
		Targets: []migrationbackend.MigrationTarget{{
			SourceField: removed,
			TargetModel: target,
			TargetKey:   target.Fields[0],
		}},
	}}}
	session := openSQLiteRelationSession(t, database)
	if records, err := session.ReadAppliedMigrations(ctx); err != nil ||
		!reflect.DeepEqual(records, []migrationbackend.AppliedMigration{initial}) {
		t.Fatalf("ReadAppliedMigrations(prepare remake)=(%v,%v)", records, err)
	}
	transaction, err := session.BeginMigration(ctx, apply, addIntent)
	if err != nil {
		t.Fatalf("BeginMigration(prepare Add): %v", err)
	}
	if err := transaction.AddField(ctx, before.Clone(), removed.Clone()); err != nil {
		t.Fatalf("AddField(prepare remake): %v", err)
	}
	if err := transaction.RecordApplied(ctx, apply.Migration.App, apply.Migration.Name); err != nil {
		t.Fatalf("RecordApplied(prepare remake): %v", err)
	}
	outcome, err := transaction.CommitFenced(ctx)
	if err != nil || outcome.Durability != migrationbackend.CommitCommitted {
		t.Fatalf("CommitFenced(prepare remake)=(%+v,%v)", outcome, err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatalf("Close(prepare remake): %v", err)
	}
	mustSQLiteRelationRemakeExec(t, database, `UPDATE "news_article" SET "editor_id"=5 WHERE "id"=8`)
	remove := apply
	remove.Kind = migrationbackend.HistoryTransitionUnapply
	return sqliteRelationRemakeFixture{
		backend:    database,
		target:     target,
		before:     before,
		after:      after,
		removed:    removed,
		transition: remove,
		intent: migrationbackend.MigrationIntent{
			Operations: []migrationbackend.MigrationOperation{{
				OperationIndex: 0,
				Kind:           migrationbackend.MigrationRemoveField,
				Before:         after,
				After:          before,
				Targets: []migrationbackend.MigrationTarget{{
					SourceField: removed,
					TargetModel: target,
					TargetKey:   target.Fields[0],
				}},
			}},
		},
	}
}

func readSQLiteRelationRemakeDurableSnapshot(t *testing.T, backend *Backend) sqliteRelationRemakeDurableSnapshot {
	t.Helper()
	ctx := context.Background()
	revision, err := readAtomicMigrationRevisionSnapshot(ctx, backend)
	if err != nil {
		t.Fatalf("read remake revision snapshot: %v", err)
	}
	snapshot := sqliteRelationRemakeDurableSnapshot{revision: revision}
	readRows := func(statement string, scan func(*sql.Rows) string) []string {
		t.Helper()
		rows, err := backend.database.QueryContext(ctx, statement)
		if err != nil {
			t.Fatalf("read remake snapshot with %q: %v", statement, err)
		}
		defer rows.Close()
		values := make([]string, 0)
		for rows.Next() {
			values = append(values, scan(rows))
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate remake snapshot with %q: %v", statement, err)
		}
		return values
	}
	snapshot.schema = readRows(
		`SELECT "type", "name", "tbl_name", COALESCE("sql", '') FROM main.sqlite_schema ORDER BY "type", "name", "tbl_name", "sql"`,
		func(rows *sql.Rows) string {
			var kind, name, owner, statement string
			if err := rows.Scan(&kind, &name, &owner, &statement); err != nil {
				t.Fatalf("scan remake schema: %v", err)
			}
			return fmt.Sprintf("%s\x00%s\x00%s\x00%s", kind, name, owner, statement)
		},
	)
	snapshot.rows = readRows(
		`SELECT "id", "title", "author_id", "editor_id" FROM "news_article" ORDER BY "id"`,
		func(rows *sql.Rows) string {
			var id, author int64
			var title string
			var editor sql.NullInt64
			if err := rows.Scan(&id, &title, &author, &editor); err != nil {
				t.Fatalf("scan remake rows: %v", err)
			}
			return fmt.Sprintf("%d\x00%s\x00%d\x00%t\x00%d", id, title, author, editor.Valid, editor.Int64)
		},
	)
	snapshot.sequence = readRows(
		`SELECT typeof("name"), CAST("name" AS TEXT), typeof("seq"), CAST("seq" AS TEXT) FROM main.sqlite_sequence ORDER BY "name"`,
		func(rows *sql.Rows) string {
			var nameType, name, sequenceType, sequence string
			if err := rows.Scan(&nameType, &name, &sequenceType, &sequence); err != nil {
				t.Fatalf("scan remake sequence: %v", err)
			}
			return fmt.Sprintf("%s\x00%s\x00%s\x00%s", nameType, name, sequenceType, sequence)
		},
	)
	return snapshot
}

func mustSQLiteRelationRemakeExec(t *testing.T, backend *Backend, statement string) {
	t.Helper()
	if _, err := backend.ExecContext(context.Background(), statement); err != nil {
		t.Fatalf("execute remake fixture %q: %v", statement, err)
	}
}

func mustQuoteSQLiteRelationRemake(t *testing.T, identifier string) string {
	t.Helper()
	quoted, err := quoteIdentifier(identifier)
	if err != nil {
		t.Fatalf("quote remake identifier %q: %v", identifier, err)
	}
	return quoted
}
