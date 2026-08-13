package migrationrelation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
)

func TestSQLiteRelationCandidatePinnedForeignKeysBeforeBeginAndExactNoAction(t *testing.T) {
	ctx := context.Background()
	database, backend, _ := sqliteRelationOpenCandidate(t)
	defer database.Close()

	intent := relationBackendArticleCreateIntent()
	result, err := faultExecutePlan(ctx, backend, []faultExecutorStep{{
		Transition: relationBackendTransition{
			App: "blog", Name: "0001", Direction: relationBackendApply,
			FromRevision: 0, ToRevision: 1,
		},
		Intent: intent,
	}})
	if err != nil {
		t.Fatalf("faultExecutePlan(): %v", err)
	}
	if result.ConfirmedSteps != 1 || result.Outcome != migrationbackend.CommitCommitted {
		t.Fatalf("result = %+v, want one committed step", result)
	}
	trace := backend.sqliteRelationTraceSnapshot()
	if len(trace) < 3 || trace[0] != "PRAGMA foreign_keys = ON" ||
		trace[1] != "PRAGMA foreign_keys" || trace[2] != "BEGIN IMMEDIATE" {
		t.Fatalf("SQLite trace prefix = %#v, want exact pinned PRAGMA enable/read before BEGIN", trace)
	}

	foreignKeys := sqliteRelationForeignKeysFromDatabase(t, database, "article")
	if len(foreignKeys) != 1 {
		t.Fatalf("article foreign keys = %#v, want 1", foreignKeys)
	}
	if foreignKeys[0].OnDelete != "NO ACTION" || foreignKeys[0].OnUpdate != "NO ACTION" {
		t.Fatalf("article physical action = %+v, want exact NO ACTION", foreignKeys[0])
	}
}

func TestSQLiteRelationCandidateRejectsRevisionSuccessorOverflowBeforeBackendIO(t *testing.T) {
	database, backend, _ := sqliteRelationOpenCandidate(t)
	defer database.Close()
	intent := relationBackendArticleCreateIntent()
	before := sqliteRelationDumpState(t, database)
	_, err := faultExecutePlan(context.Background(), backend, []faultExecutorStep{{
		Transition: relationBackendTransition{
			App: intent.App, Name: intent.Name, Direction: relationBackendApply,
			FromRevision: math.MaxInt64, ToRevision: math.MinInt64,
		},
		Intent: intent,
	}})
	if !errors.Is(err, relationBackendErrIntent) {
		t.Fatalf("overflow transition error=%v, want transition rejection", err)
	}
	if backend.openCalls != 0 || backend.beginCalls != 0 || len(backend.sqliteRelationTraceSnapshot()) != 0 {
		t.Fatalf(
			"overflow transition reached SQLite backend open=%d begin=%d trace=%#v",
			backend.openCalls,
			backend.beginCalls,
			backend.sqliteRelationTraceSnapshot(),
		)
	}
	if after := sqliteRelationDumpState(t, database); after != before {
		t.Fatalf("overflow transition changed durable state\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestSQLiteRelationCandidateNullableNativeAddOnPopulatedTableAndRequiredReject(t *testing.T) {
	ctx := context.Background()
	t.Run("nullable_native_add", func(t *testing.T) {
		database, backend, _ := sqliteRelationOpenCandidate(t)
		defer database.Close()
		sqliteRelationApplyInitialArticle(t, backend)
		if _, err := database.ExecContext(ctx,
			`INSERT INTO "author" ("id", "name") VALUES (5, 'Ada');
			 INSERT INTO "article" ("id", "title", "author_id") VALUES (3, 'one', 5), (8, 'two', 5)`,
		); err != nil {
			t.Fatalf("seed populated relation tables: %v", err)
		}

		before := relationBackendArticleModel(false)
		after := relationBackendArticleModel(true)
		editor := after.Relations[1]
		intent := relationBackendStepIntent{
			App: "blog", Name: "0002",
			Changes: []relationBackendChange{{
				Kind: relationBackendAddField, Before: before, After: after, Relation: editor,
			}},
		}
		result, err := faultExecutePlan(ctx, backend, []faultExecutorStep{{
			Transition: relationBackendTransition{
				App: "blog", Name: "0002", Direction: relationBackendApply,
				FromRevision: 1, ToRevision: 2,
			},
			Intent: intent,
		}})
		if err != nil {
			t.Fatalf("nullable relation AddField: %v", err)
		}
		if result.ConfirmedSteps != 1 {
			t.Fatalf("confirmed steps = %d, want 1", result.ConfirmedSteps)
		}
		var nullEditors int
		if err := database.QueryRowContext(
			ctx, `SELECT COUNT(*) FROM "article" WHERE "editor_id" IS NULL`,
		).Scan(&nullEditors); err != nil {
			t.Fatalf("read nullable editors: %v", err)
		}
		if nullEditors != 2 {
			t.Fatalf("NULL editor rows = %d, want 2", nullEditors)
		}
		trace := strings.Join(backend.sqliteRelationTraceSnapshot(), "\n")
		if !strings.Contains(trace,
			`ALTER TABLE "main"."article" ADD COLUMN "editor_id" INTEGER NULL REFERENCES "author" ("id") ON DELETE NO ACTION`,
		) {
			t.Fatalf("trace lacks native nullable ADD:\n%s", trace)
		}
	})

	t.Run("required_empty_add_and_reverse", func(t *testing.T) {
		database, backend, _ := sqliteRelationOpenCandidate(t)
		defer database.Close()
		sqliteRelationApplyInitialArticle(t, backend)

		add := relationBackendRequiredAddIntent()
		result, err := faultExecutePlan(ctx, backend, []faultExecutorStep{{
			Transition: relationBackendTransition{
				App: add.App, Name: add.Name, Direction: relationBackendApply,
				FromRevision: 1, ToRevision: 2,
			},
			Intent: add,
		}})
		if err != nil {
			t.Fatalf("empty required relation AddField: %v", err)
		}
		if result.ConfirmedSteps != 1 || !sqliteRelationColumnExists(t, database, "article", "reviewer_id") {
			t.Fatalf("empty required AddField result=%+v reviewer_exists=%t", result, sqliteRelationColumnExists(t, database, "article", "reviewer_id"))
		}
		foreignKeys := sqliteRelationForeignKeysFromDatabase(t, database, "article")
		wantReviewer := sqliteRelationPhysicalForeignKey{
			SourceColumn: "reviewer_id", TargetTable: "author", TargetColumn: "id",
			OnUpdate: "NO ACTION", OnDelete: "NO ACTION",
		}
		if !sqliteRelationContainsForeignKey(foreignKeys, wantReviewer) {
			t.Fatalf("empty required AddField foreign keys = %#v, missing %+v", foreignKeys, wantReviewer)
		}

		change := add.Changes[0]
		remove := relationBackendStepIntent{
			App: add.App, Name: add.Name,
			Changes: []relationBackendChange{{
				Kind: relationBackendRemoveField, Before: change.After, After: change.Before,
				Relation: change.Relation,
			}},
		}
		result, err = faultExecutePlan(ctx, backend, []faultExecutorStep{{
			Transition: relationBackendTransition{
				App: remove.App, Name: remove.Name, Direction: relationBackendUnapply,
				FromRevision: 2, ToRevision: 3,
			},
			Intent: remove,
		}})
		if err != nil {
			t.Fatalf("reverse empty required relation AddField: %v", err)
		}
		if result.ConfirmedSteps != 1 || sqliteRelationColumnExists(t, database, "article", "reviewer_id") {
			t.Fatalf("reverse empty required AddField result=%+v reviewer_exists=%t", result, sqliteRelationColumnExists(t, database, "article", "reviewer_id"))
		}
		if sqliteRelationRevision(t, database) != 3 || sqliteRelationRecorderCount(t, database, add.App, add.Name) != 0 {
			t.Fatal("reverse empty required AddField lost revision/recorder semantics")
		}
	})

	t.Run("required_populated_reject", func(t *testing.T) {
		database, backend, _ := sqliteRelationOpenCandidate(t)
		defer database.Close()
		sqliteRelationApplyInitialArticle(t, backend)
		if _, err := database.ExecContext(ctx,
			`INSERT INTO "author" ("id", "name") VALUES (1, 'Ada');
			 INSERT INTO "article" ("id", "title", "author_id") VALUES (1, 'one', 1)`,
		); err != nil {
			t.Fatalf("seed populated relation tables: %v", err)
		}
		before := relationBackendArticleModel(false)
		after := before.relationBackendClone()
		reviewer := relationBackendRelation{
			Name: "reviewer", Column: "reviewer_id", TargetTable: "author", TargetColumn: "id",
			OnDelete: relationBackendProtect, Position: 4,
		}
		after.Relations = append(after.Relations, reviewer)
		intent := relationBackendStepIntent{
			App: "blog", Name: "0002_required",
			Changes: []relationBackendChange{{
				Kind: relationBackendAddField, Before: before, After: after, Relation: reviewer,
			}},
		}
		beforeState := sqliteRelationDumpState(t, database)
		traceStart := len(backend.sqliteRelationTraceSnapshot())
		beginStart, commitStart, rollbackStart := backend.beginCalls, backend.commitCalls, backend.rollbackCalls
		_, err := faultExecutePlan(ctx, backend, []faultExecutorStep{{
			Transition: relationBackendTransition{
				App: "blog", Name: "0002_required", Direction: relationBackendApply,
				FromRevision: 1, ToRevision: 2,
			},
			Intent: intent,
		}})
		if !errors.Is(err, sqliteRelationErrRequiredRows) {
			t.Fatalf("required relation AddField error = %v, want populated rejection", err)
		}
		wantTrace := []string{"PRAGMA foreign_keys = ON", "PRAGMA foreign_keys", "BEGIN IMMEDIATE"}
		if trace := backend.sqliteRelationTraceSnapshot()[traceStart:]; !relationBackendStringSlicesEqual(trace, wantTrace) {
			t.Fatalf("required populated rejection trace = %#v, want exact pinned preflight %#v", trace, wantTrace)
		}
		if sqliteRelationColumnExists(t, database, "article", "reviewer_id") {
			t.Fatal("required relation AddField mutated schema")
		}
		if afterState := sqliteRelationDumpState(t, database); afterState != beforeState {
			t.Fatalf("required populated rejection changed schema/revision\nbefore:\n%s\nafter:\n%s", beforeState, afterState)
		}
		if got := sqliteRelationRecorderCount(t, database, "blog", "0002_required"); got != 0 {
			t.Fatalf("required populated rejection recorder rows = %d, want 0", got)
		}
		if backend.beginCalls != beginStart+1 || backend.commitCalls != commitStart || backend.rollbackCalls != rollbackStart+1 {
			t.Fatalf(
				"required populated lifecycle begin=%d/%d commit=%d/%d rollback=%d/%d",
				backend.beginCalls, beginStart, backend.commitCalls, commitStart, backend.rollbackCalls, rollbackStart,
			)
		}
		var rows int
		if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM "article" WHERE "id" = 1 AND "author_id" = 1`).Scan(&rows); err != nil {
			t.Fatalf("read required rejection rows: %v", err)
		}
		if rows != 1 {
			t.Fatalf("required populated rejection preserved rows = %d, want 1", rows)
		}
	})
}

func TestSQLiteRelationCandidateBoundedRemakePreservesRowsSequenceAndRemainingForeignKeys(t *testing.T) {
	ctx := context.Background()
	database, backend, _ := sqliteRelationOpenCandidate(t)
	defer database.Close()
	sqliteRelationApplyInitialArticle(t, backend)
	if _, err := database.ExecContext(ctx,
		`INSERT INTO "author" ("id", "name") VALUES (5, 'Ada');
		 INSERT INTO "article" ("id", "title", "author_id") VALUES (3, 'one', 5), (8, 'two', 5)`,
	); err != nil {
		t.Fatalf("seed relation rows: %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE "sqlite_sequence" SET "seq" = 100 WHERE "name" = 'article'`); err != nil {
		t.Fatalf("raise article sequence high-water mark: %v", err)
	}

	before := relationBackendArticleModel(false)
	after := relationBackendArticleModel(true)
	editor := after.Relations[1]
	add := relationBackendStepIntent{App: "blog", Name: "0002", Changes: []relationBackendChange{{
		Kind: relationBackendAddField, Before: before, After: after, Relation: editor,
	}}}
	if _, err := faultExecutePlan(ctx, backend, []faultExecutorStep{{
		Transition: relationBackendTransition{App: "blog", Name: "0002", Direction: relationBackendApply, FromRevision: 1, ToRevision: 2},
		Intent:     add,
	}}); err != nil {
		t.Fatalf("add editor relation: %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE "article" SET "editor_id" = 5 WHERE "id" = 8`); err != nil {
		t.Fatalf("set editor relation: %v", err)
	}

	remove := relationBackendStepIntent{App: "blog", Name: "0002", Changes: []relationBackendChange{{
		Kind: relationBackendRemoveField, Before: after, After: before, Relation: editor,
	}}}
	result, err := faultExecutePlan(ctx, backend, []faultExecutorStep{{
		Transition: relationBackendTransition{App: "blog", Name: "0002", Direction: relationBackendUnapply, FromRevision: 2, ToRevision: 3},
		Intent:     remove,
	}})
	if err != nil {
		t.Fatalf("bounded relation remake: %v", err)
	}
	if result.ConfirmedSteps != 1 {
		t.Fatalf("confirmed steps = %d, want 1", result.ConfirmedSteps)
	}
	rows, err := database.QueryContext(ctx, `SELECT "id", "title", "author_id" FROM "article" ORDER BY "id"`)
	if err != nil {
		t.Fatalf("read remade retained values: %v", err)
	}
	defer rows.Close()
	type sqliteRelationRetainedRow struct {
		ID       int64
		Title    string
		AuthorID int64
	}
	var retained []sqliteRelationRetainedRow
	for rows.Next() {
		var row sqliteRelationRetainedRow
		if err := rows.Scan(&row.ID, &row.Title, &row.AuthorID); err != nil {
			t.Fatalf("scan remade retained values: %v", err)
		}
		retained = append(retained, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate remade retained values: %v", err)
	}
	wantRetained := []sqliteRelationRetainedRow{{ID: 3, Title: "one", AuthorID: 5}, {ID: 8, Title: "two", AuthorID: 5}}
	if len(retained) != len(wantRetained) || retained[0] != wantRetained[0] || retained[1] != wantRetained[1] {
		t.Fatalf("remade retained values = %#v, want %#v", retained, wantRetained)
	}
	if sqliteRelationColumnExists(t, database, "article", "editor_id") {
		t.Fatal("removed relation column still exists")
	}
	foreignKeys := sqliteRelationForeignKeysFromDatabase(t, database, "article")
	wantForeignKey := sqliteRelationPhysicalForeignKey{
		SourceColumn: "author_id", TargetTable: "author", TargetColumn: "id",
		OnUpdate: "NO ACTION", OnDelete: "NO ACTION",
	}
	if len(foreignKeys) != 1 || foreignKeys[0] != wantForeignKey {
		t.Fatalf("remaining foreign keys = %#v, want complete tuple %+v", foreignKeys, wantForeignKey)
	}
	var sequence int64
	if err := database.QueryRowContext(ctx,
		`SELECT "seq" FROM "sqlite_sequence" WHERE "name" = 'article'`,
	).Scan(&sequence); err != nil {
		t.Fatalf("read remade sqlite_sequence: %v", err)
	}
	if sequence != 100 {
		t.Fatalf("remade sqlite_sequence = %d, want preserved high-water 100", sequence)
	}
	if _, err := database.ExecContext(ctx,
		`INSERT INTO "article" ("title", "author_id") VALUES ('after-remake', 5)`,
	); err != nil {
		t.Fatalf("insert after high-water sequence remake: %v", err)
	}
	var nextID int64
	if err := database.QueryRowContext(ctx, `SELECT "id" FROM "article" WHERE "title" = 'after-remake'`).Scan(&nextID); err != nil {
		t.Fatalf("read post-remake auto id: %v", err)
	}
	if nextID != 101 {
		t.Fatalf("post-remake auto id = %d, want 101", nextID)
	}

	t.Run("actual_scalar_shape_interleaved_relation_survives_restart_and_remake", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "actual-shape.sqlite3")
		open := func(t *testing.T) (*sql.DB, *sqliteRelationBackend) {
			t.Helper()
			database, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatalf("open actual-shape database: %v", err)
			}
			database.SetMaxOpenConns(1)
			if err := sqliteRelationInitialize(context.Background(), database); err != nil {
				_ = database.Close()
				t.Fatalf("initialize actual-shape database: %v", err)
			}
			return database, &sqliteRelationBackend{database: database}
		}

		author := relationBackendAuthorModel()
		author.Table = "legacy_author"
		articleBefore := relationBackendModel{
			Table: "legacy_article",
			Columns: []relationBackendColumn{
				{Name: "id", Type: "INTEGER", NotNull: true, PrimaryKey: true, AutoIncrement: true, Position: 1},
				{Name: "title", Type: "VARCHAR", MaxLength: 200, NotNull: true, Position: 3},
				{Name: "published", Type: "BOOLEAN", NotNull: true, Position: 4},
				{Name: "summary", Type: "VARCHAR", MaxLength: 200, Nullable: true, Position: 5},
			},
			Relations: []relationBackendRelation{{
				Name: "author", Column: "author_id", TargetTable: "legacy_author", TargetColumn: "id",
				OnDelete: relationBackendProtect, Position: 2,
			}},
		}
		editor := relationBackendRelation{
			Name: "editor", Column: "editor_id", TargetTable: "legacy_author", TargetColumn: "id",
			Nullable: true, OnDelete: relationBackendSetNull, Position: 6,
		}
		articleAfter := articleBefore.relationBackendClone()
		articleAfter.Relations = append(articleAfter.Relations, editor)

		database, backend := open(t)
		authorSQL, err := sqliteRelationCompileCreateTable(author, author.Table)
		if err != nil {
			t.Fatalf("compile actual-shape author: %v", err)
		}
		sqliteRelationExec(t, database, authorSQL)
		articleSQL := sqliteRelationCompileClosedCreateTable(
			articleBefore,
			articleBefore.Table,
			func(relation relationBackendRelation) bool { return relation.Name == "author" },
		)
		sqliteRelationExec(t, database, articleSQL)
		sqliteRelationExec(t, database,
			`INSERT INTO "legacy_author" ("id", "name") VALUES (5, 'Ada')`)
		sqliteRelationExec(t, database,
			`INSERT INTO "legacy_article" ("id", "author_id", "title", "published", "summary") `+
				`VALUES (8, 5, 'guide', TRUE, NULL)`)

		add := relationBackendStepIntent{App: "blog", Name: "legacy_0002", Changes: []relationBackendChange{{
			Kind: relationBackendAddField, Before: articleBefore, After: articleAfter, Relation: editor,
		}}}
		if result, err := faultExecutePlan(ctx, backend, []faultExecutorStep{{
			Transition: relationBackendTransition{App: add.App, Name: add.Name, Direction: relationBackendApply, FromRevision: 0, ToRevision: 1},
			Intent:     add,
		}}); err != nil || result.ConfirmedSteps != 1 {
			t.Fatalf("actual-shape relation AddField result=%+v error=%v", result, err)
		}
		sqliteRelationExec(t, database, `UPDATE "legacy_article" SET "editor_id" = 5 WHERE "id" = 8`)
		sqliteRelationExec(t, database, `UPDATE "sqlite_sequence" SET "seq" = 40 WHERE "name" = 'legacy_article'`)
		if err := database.Close(); err != nil {
			t.Fatalf("close actual-shape database before restart: %v", err)
		}

		database, backend = open(t)
		defer database.Close()
		if got := sqliteRelationRevision(t, database); got != 1 {
			t.Fatalf("restarted actual-shape revision = %d, want 1", got)
		}
		remove := relationBackendStepIntent{App: add.App, Name: add.Name, Changes: []relationBackendChange{{
			Kind: relationBackendRemoveField, Before: articleAfter, After: articleBefore, Relation: editor,
		}}}
		if result, err := faultExecutePlan(ctx, backend, []faultExecutorStep{{
			Transition: relationBackendTransition{App: remove.App, Name: remove.Name, Direction: relationBackendUnapply, FromRevision: 1, ToRevision: 2},
			Intent:     remove,
		}}); err != nil || result.ConfirmedSteps != 1 {
			t.Fatalf("restarted actual-shape remake result=%+v error=%v", result, err)
		}

		rows, err := database.QueryContext(ctx, `PRAGMA main.table_xinfo("legacy_article")`)
		if err != nil {
			t.Fatalf("read actual-shape column order: %v", err)
		}
		var columns []string
		for rows.Next() {
			var cid, notNull, primaryKey, hidden int
			var name, dataType string
			var defaultValue sql.NullString
			if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey, &hidden); err != nil {
				_ = rows.Close()
				t.Fatalf("scan actual-shape column order: %v", err)
			}
			columns = append(columns, name)
		}
		if err := rows.Close(); err != nil {
			t.Fatalf("close actual-shape column order: %v", err)
		}
		wantColumns := []string{"id", "author_id", "title", "published", "summary"}
		if !relationBackendStringSlicesEqual(columns, wantColumns) {
			t.Fatalf("actual-shape retained order = %#v, want %#v", columns, wantColumns)
		}
		var id, authorID, published int64
		var title string
		var summary sql.NullString
		if err := database.QueryRowContext(ctx,
			`SELECT "id", "author_id", "title", "published", "summary" FROM "legacy_article"`,
		).Scan(&id, &authorID, &title, &published, &summary); err != nil {
			t.Fatalf("read actual-shape retained values: %v", err)
		}
		if id != 8 || authorID != 5 || title != "guide" || published != 1 || summary.Valid {
			t.Fatalf("actual-shape retained values id=%d author=%d title=%q published=%d summary=%#v", id, authorID, title, published, summary)
		}
		sqliteRelationExec(t, database,
			`INSERT INTO "legacy_article" ("author_id", "title", "published", "summary") `+
				`VALUES (5, 'after', FALSE, 'kept')`)
		var next int64
		if err := database.QueryRowContext(ctx,
			`SELECT "id" FROM "legacy_article" WHERE "title" = 'after'`,
		).Scan(&next); err != nil {
			t.Fatalf("read restarted actual-shape sequence: %v", err)
		}
		if next != 41 {
			t.Fatalf("restarted actual-shape next id = %d, want 41", next)
		}
	})
}

func TestSQLiteRelationCandidateRemakeNormalizesCaseVariantSequenceRowExactlyOnce(t *testing.T) {
	database, backend, _ := sqliteRelationOpenCandidate(t)
	defer database.Close()
	sqliteRelationApplyInitialArticle(t, backend)
	before := relationBackendArticleModel(false)
	after := relationBackendArticleModel(true)
	editor := after.Relations[1]
	add := relationBackendStepIntent{App: "blog", Name: "0002", Changes: []relationBackendChange{{
		Kind: relationBackendAddField, Before: before, After: after, Relation: editor,
	}}}
	if _, err := faultExecutePlan(context.Background(), backend, []faultExecutorStep{{
		Transition: relationBackendTransition{
			App: add.App, Name: add.Name, Direction: relationBackendApply,
			FromRevision: 1, ToRevision: 2,
		},
		Intent: add,
	}}); err != nil {
		t.Fatalf("prepare case-variant sequence remake: %v", err)
	}
	sqliteRelationExec(t, database, `INSERT INTO "author" ("id", "name") VALUES (5, 'Ada')`)
	sqliteRelationExec(t, database, `INSERT INTO "article" ("id", "title", "author_id") VALUES (8, 'two', 5)`)
	sqliteRelationExec(t, database, `UPDATE "sqlite_sequence" SET "name" = 'ArTiClE', "seq" = 77 WHERE "name" = 'article'`)
	remove := relationBackendStepIntent{App: add.App, Name: add.Name, Changes: []relationBackendChange{{
		Kind: relationBackendRemoveField, Before: after, After: before, Relation: editor,
	}}}
	result, err := faultExecutePlan(context.Background(), backend, []faultExecutorStep{{
		Transition: relationBackendTransition{
			App: remove.App, Name: remove.Name, Direction: relationBackendUnapply,
			FromRevision: 2, ToRevision: 3,
		},
		Intent: remove,
	}})
	if err != nil || result.ConfirmedSteps != 1 {
		t.Fatalf("case-variant sequence remake result=%+v error=%v", result, err)
	}
	var rows int
	var name string
	var sequence int64
	if err := database.QueryRowContext(
		context.Background(),
		`SELECT COUNT(*), MIN("name"), MIN("seq") FROM "sqlite_sequence" WHERE "name" COLLATE NOCASE = 'article'`,
	).Scan(&rows, &name, &sequence); err != nil {
		t.Fatalf("read normalized case-variant sequence: %v", err)
	}
	if rows != 1 || name != "article" || sequence != 77 {
		t.Fatalf("normalized sequence rows=%d name=%q seq=%d, want one exact article/77", rows, name, sequence)
	}
}

func TestSQLiteRelationCandidateRemakeRejectsInvalidSequenceBeforeDDLWithoutMutation(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T, *sql.DB)
	}{
		{
			name: "duplicate",
			prepare: func(t *testing.T, database *sql.DB) {
				sqliteRelationExec(t, database, `INSERT INTO "sqlite_sequence" ("name", "seq") VALUES ('article', 8)`)
			},
		},
		{
			name: "non-integer",
			prepare: func(t *testing.T, database *sql.DB) {
				sqliteRelationExec(t, database, `UPDATE "sqlite_sequence" SET "seq" = 'invalid' WHERE "name" = 'article'`)
			},
		},
		{
			name: "negative",
			prepare: func(t *testing.T, database *sql.DB) {
				sqliteRelationExec(t, database, `UPDATE "sqlite_sequence" SET "seq" = -1 WHERE "name" = 'article'`)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database, backend, _ := sqliteRelationOpenCandidate(t)
			defer database.Close()
			sqliteRelationApplyInitialArticle(t, backend)
			before := relationBackendArticleModel(false)
			after := relationBackendArticleModel(true)
			editor := after.Relations[1]
			add := relationBackendStepIntent{App: "blog", Name: "0002", Changes: []relationBackendChange{{
				Kind: relationBackendAddField, Before: before, After: after, Relation: editor,
			}}}
			if _, err := faultExecutePlan(context.Background(), backend, []faultExecutorStep{{
				Transition: relationBackendTransition{
					App: add.App, Name: add.Name, Direction: relationBackendApply,
					FromRevision: 1, ToRevision: 2,
				},
				Intent: add,
			}}); err != nil {
				t.Fatalf("prepare invalid sequence remake: %v", err)
			}
			sqliteRelationExec(t, database, `INSERT INTO "author" ("id", "name") VALUES (5, 'Ada')`)
			sqliteRelationExec(t, database, `INSERT INTO "article" ("id", "title", "author_id") VALUES (8, 'two', 5)`)
			test.prepare(t, database)
			beforeState := sqliteRelationDumpState(t, database)
			beforeSequence := sqliteRelationSequenceState(t, database, "article")
			traceStart := len(backend.sqliteRelationTraceSnapshot())
			remove := relationBackendStepIntent{App: add.App, Name: add.Name, Changes: []relationBackendChange{{
				Kind: relationBackendRemoveField, Before: after, After: before, Relation: editor,
			}}}
			_, err := faultExecutePlan(context.Background(), backend, []faultExecutorStep{{
				Transition: relationBackendTransition{
					App: remove.App, Name: remove.Name, Direction: relationBackendUnapply,
					FromRevision: 2, ToRevision: 3,
				},
				Intent: remove,
			}})
			if !errors.Is(err, sqliteRelationErrDrift) || !strings.Contains(err.Error(), "sqlite_sequence") {
				t.Fatalf("invalid sequence remake error=%v, want drift", err)
			}
			wantTrace := []string{"PRAGMA foreign_keys = ON", "PRAGMA foreign_keys", "BEGIN IMMEDIATE"}
			if trace := backend.sqliteRelationTraceSnapshot()[traceStart:]; !relationBackendStringSlicesEqual(trace, wantTrace) {
				t.Fatalf("invalid sequence remake trace=%#v, want %#v", trace, wantTrace)
			}
			if afterState := sqliteRelationDumpState(t, database); afterState != beforeState {
				t.Fatalf("invalid sequence rejection mutated schema/revision\nbefore:\n%s\nafter:\n%s", beforeState, afterState)
			}
			if afterSequence := sqliteRelationSequenceState(t, database, "article"); afterSequence != beforeSequence {
				t.Fatalf("invalid sequence rejection mutated sequence\nbefore:\n%s\nafter:\n%s", beforeSequence, afterSequence)
			}
			if sqliteRelationRevision(t, database) != 2 ||
				sqliteRelationRecorderCount(t, database, add.App, add.Name) != 1 ||
				!sqliteRelationColumnExists(t, database, "article", "editor_id") {
				t.Fatal("invalid sequence rejection crossed revision, recorder, or DDL boundary")
			}
		})
	}
}

func TestSQLiteRelationCandidateCreateModelApplyReverseOrderUnapplyReapply(t *testing.T) {
	ctx := context.Background()
	database, backend, _ := sqliteRelationOpenCandidate(t)
	defer database.Close()
	create := relationBackendArticleCreateIntent()
	apply := faultExecutorStep{
		Transition: relationBackendTransition{
			App: create.App, Name: create.Name, Direction: relationBackendApply,
			FromRevision: 0, ToRevision: 1,
		},
		Intent: create,
	}
	if result, err := faultExecutePlan(ctx, backend, []faultExecutorStep{apply}); err != nil || result.ConfirmedSteps != 1 {
		t.Fatalf("CreateModel apply result=%+v error=%v", result, err)
	}
	sqliteRelationExec(t, database, `INSERT INTO "author" ("id", "name") VALUES (5, 'Ada')`)
	sqliteRelationExec(t, database, `INSERT INTO "article" ("id", "title", "author_id") VALUES (8, 'two', 5)`)

	unapplyIntent := relationBackendStepIntent{
		App: create.App, Name: create.Name,
		Changes: []relationBackendChange{
			{Kind: relationBackendDeleteModel, Before: create.Changes[1].After},
			{Kind: relationBackendDeleteModel, Before: create.Changes[0].After},
		},
	}
	result, err := faultExecutePlan(ctx, backend, []faultExecutorStep{{
		Transition: relationBackendTransition{
			App: create.App, Name: create.Name, Direction: relationBackendUnapply,
			FromRevision: 1, ToRevision: 2,
		},
		Intent: unapplyIntent,
	}})
	if err != nil || result.ConfirmedSteps != 1 {
		t.Fatalf("CreateModel reverse-order unapply result=%+v error=%v", result, err)
	}
	if sqliteRelationTableExistsFromDatabase(t, database, "article") || sqliteRelationTableExistsFromDatabase(t, database, "author") {
		t.Fatal("reverse-order unapply retained article or author table")
	}
	if sqliteRelationRecorderCount(t, database, create.App, create.Name) != 0 || sqliteRelationRevision(t, database) != 2 {
		t.Fatal("reverse-order unapply recorder/revision mismatch")
	}

	apply.Transition.FromRevision = 2
	apply.Transition.ToRevision = 3
	result, err = faultExecutePlan(ctx, backend, []faultExecutorStep{apply})
	if err != nil || result.ConfirmedSteps != 1 {
		t.Fatalf("CreateModel reapply result=%+v error=%v", result, err)
	}
	if !sqliteRelationTableExistsFromDatabase(t, database, "author") || !sqliteRelationTableExistsFromDatabase(t, database, "article") {
		t.Fatal("CreateModel reapply did not restore tables")
	}
	if sqliteRelationRecorderCount(t, database, create.App, create.Name) != 1 || sqliteRelationRevision(t, database) != 3 {
		t.Fatal("CreateModel reapply recorder/revision mismatch")
	}
	var authorRows, articleRows int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM "author"`).Scan(&authorRows); err != nil {
		t.Fatalf("count reapplied authors: %v", err)
	}
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM "article"`).Scan(&articleRows); err != nil {
		t.Fatalf("count reapplied articles: %v", err)
	}
	if authorRows != 0 || articleRows != 0 {
		t.Fatalf("reapplied rows author=%d article=%d, want empty new tables", authorRows, articleRows)
	}
	wantForeignKey := sqliteRelationPhysicalForeignKey{
		SourceColumn: "author_id", TargetTable: "author", TargetColumn: "id",
		OnUpdate: "NO ACTION", OnDelete: "NO ACTION",
	}
	foreignKeys := sqliteRelationForeignKeysFromDatabase(t, database, "article")
	if len(foreignKeys) != 1 || foreignKeys[0] != wantForeignKey {
		t.Fatalf("reapplied foreign keys = %#v, want %+v", foreignKeys, wantForeignKey)
	}

	t.Run("ordered_effective_inbound_remove_then_target_delete", func(t *testing.T) {
		database, backend, _ := sqliteRelationOpenCandidate(t)
		defer database.Close()
		target := relationBackendAuthorModel()
		sourceBefore := relationBackendModel{
			Table: "audit_log",
			Columns: []relationBackendColumn{{
				Name: "id", Type: "INTEGER", NotNull: true, PrimaryKey: true, AutoIncrement: true, Position: 1,
			}},
			Relations: []relationBackendRelation{{
				Name: "author", Column: "author_id", TargetTable: "author", TargetColumn: "id",
				OnDelete: relationBackendProtect, Position: 2,
			}},
		}
		sourceAfter := sourceBefore.relationBackendClone()
		sourceAfter.Relations = nil
		targetSQL, err := sqliteRelationCompileCreateTable(target, target.Table)
		if err != nil {
			t.Fatalf("compile ordered-inbound target: %v", err)
		}
		sourceSQL, err := sqliteRelationCompileCreateTable(sourceBefore, sourceBefore.Table)
		if err != nil {
			t.Fatalf("compile ordered-inbound source: %v", err)
		}
		sqliteRelationExec(t, database, targetSQL)
		sqliteRelationExec(t, database, sourceSQL)
		sqliteRelationExec(t, database, `INSERT INTO "author" ("id", "name") VALUES (1, 'Ada')`)
		sqliteRelationExec(t, database, `INSERT INTO "audit_log" ("id", "author_id") VALUES (7, 1)`)
		intent := relationBackendStepIntent{
			App: "blog", Name: "remove_inbound_delete_target",
			Changes: []relationBackendChange{
				{Kind: relationBackendRemoveField, Before: sourceBefore, After: sourceAfter, Relation: sourceBefore.Relations[0]},
				{Kind: relationBackendDeleteModel, Before: target},
			},
		}
		result, err := faultExecutePlan(ctx, backend, []faultExecutorStep{{
			Transition: relationBackendTransition{App: intent.App, Name: intent.Name, Direction: relationBackendApply, FromRevision: 0, ToRevision: 1},
			Intent:     intent,
		}})
		if err != nil || result.ConfirmedSteps != 1 {
			t.Fatalf("ordered safe inbound lifecycle result=%+v error=%v", result, err)
		}
		if sqliteRelationTableExistsFromDatabase(t, database, "author") || sqliteRelationColumnExists(t, database, "audit_log", "author_id") {
			t.Fatal("ordered safe inbound lifecycle retained target or removed FK column")
		}
		var retainedID int64
		if err := database.QueryRowContext(ctx, `SELECT "id" FROM "audit_log"`).Scan(&retainedID); err != nil || retainedID != 7 {
			t.Fatalf("ordered safe inbound retained row id=%d error=%v", retainedID, err)
		}
	})

	t.Run("planned_source_then_target_delete_rejects_without_mutation", func(t *testing.T) {
		database, backend, _ := sqliteRelationOpenCandidate(t)
		defer database.Close()
		intent := relationBackendArticleCreateIntent()
		intent.Name = "unsafe_delete"
		intent.Changes = append(intent.Changes, relationBackendChange{
			Kind: relationBackendDeleteModel, Before: relationBackendAuthorModel(),
		})
		before := sqliteRelationDumpState(t, database)
		_, err := faultExecutePlan(ctx, backend, []faultExecutorStep{{
			Transition: relationBackendTransition{App: intent.App, Name: intent.Name, Direction: relationBackendApply, FromRevision: 0, ToRevision: 1},
			Intent:     intent,
		}})
		if !errors.Is(err, relationBackendErrIntent) {
			t.Fatalf("planned inbound target deletion error = %v, want invalid intent", err)
		}
		if len(backend.sqliteRelationTraceSnapshot()) != 0 || backend.openCalls != 0 || backend.beginCalls != 0 {
			t.Fatalf("unsafe planned deletion reached backend trace=%#v open=%d begin=%d", backend.sqliteRelationTraceSnapshot(), backend.openCalls, backend.beginCalls)
		}
		if after := sqliteRelationDumpState(t, database); after != before {
			t.Fatalf("unsafe planned deletion changed state\nbefore:\n%s\nafter:\n%s", before, after)
		}
	})
}

func TestSQLiteRelationCandidateDeleteModelRejectsExternalCatalogReferencesBeforeDDL(t *testing.T) {
	tests := []struct {
		name    string
		want    error
		prepare func(*testing.T, *sql.DB)
	}{
		{
			name: "external view",
			want: sqliteRelationErrView,
			prepare: func(t *testing.T, database *sql.DB) {
				sqliteRelationExec(t, database, `CREATE VIEW "article_read_model" AS SELECT "id" FROM "article"`)
			},
		},
		{
			name: "external trigger body",
			want: sqliteRelationErrTrigger,
			prepare: func(t *testing.T, database *sql.DB) {
				sqliteRelationExec(t, database, `CREATE TABLE "audit_log" ("id" INTEGER PRIMARY KEY)`)
				sqliteRelationExec(t, database, `CREATE TRIGGER "audit_reads_article" AFTER INSERT ON "audit_log" BEGIN SELECT "id" FROM "article"; END`)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database, backend, _ := sqliteRelationOpenCandidate(t)
			defer database.Close()
			sqliteRelationApplyInitialArticle(t, backend)
			test.prepare(t, database)
			before := sqliteRelationDumpState(t, database)
			traceStart := len(backend.sqliteRelationTraceSnapshot())
			create := relationBackendArticleCreateIntent()
			unapply := relationBackendStepIntent{
				App:  create.App,
				Name: create.Name,
				Changes: []relationBackendChange{
					{Kind: relationBackendDeleteModel, Before: create.Changes[1].After},
					{Kind: relationBackendDeleteModel, Before: create.Changes[0].After},
				},
			}
			_, err := faultExecutePlan(context.Background(), backend, []faultExecutorStep{{
				Transition: relationBackendTransition{
					App: unapply.App, Name: unapply.Name, Direction: relationBackendUnapply,
					FromRevision: 1, ToRevision: 2,
				},
				Intent: unapply,
			}})
			if !errors.Is(err, test.want) {
				t.Fatalf("DeleteModel catalog reference error=%v, want %v", err, test.want)
			}
			wantTrace := []string{"PRAGMA foreign_keys = ON", "PRAGMA foreign_keys", "BEGIN IMMEDIATE"}
			if trace := backend.sqliteRelationTraceSnapshot()[traceStart:]; !relationBackendStringSlicesEqual(trace, wantTrace) {
				t.Fatalf("DeleteModel catalog reference trace=%#v, want %#v", trace, wantTrace)
			}
			if after := sqliteRelationDumpState(t, database); after != before {
				t.Fatalf("DeleteModel catalog reference rejection mutated durable state\nbefore:\n%s\nafter:\n%s", before, after)
			}
			if sqliteRelationRevision(t, database) != 1 ||
				sqliteRelationRecorderCount(t, database, create.App, create.Name) != 1 ||
				!sqliteRelationTableExistsFromDatabase(t, database, "article") ||
				!sqliteRelationTableExistsFromDatabase(t, database, "author") {
				t.Fatal("DeleteModel catalog reference rejection crossed revision, recorder, or DDL boundary")
			}
		})
	}

	t.Run("orphan sqlite_sequence row rejects CreateModel before DDL", func(t *testing.T) {
		database, backend, _ := sqliteRelationOpenCandidate(t)
		defer database.Close()
		sqliteRelationExec(t, database, `CREATE TABLE "sequence_seed" ("id" INTEGER PRIMARY KEY AUTOINCREMENT)`)
		sqliteRelationExec(t, database, `INSERT INTO "sqlite_sequence" ("name", "seq") VALUES ('article', 99)`)
		before := sqliteRelationDumpState(t, database)
		intent := relationBackendArticleCreateIntent()
		_, err := faultExecutePlan(context.Background(), backend, []faultExecutorStep{{
			Transition: relationBackendTransition{
				App: intent.App, Name: intent.Name, Direction: relationBackendApply,
				FromRevision: 0, ToRevision: 1,
			},
			Intent: intent,
		}})
		if !errors.Is(err, sqliteRelationErrDrift) || !strings.Contains(err.Error(), "orphan sqlite_sequence") {
			t.Fatalf("orphan sequence CreateModel error=%v, want drift", err)
		}
		wantTrace := []string{"PRAGMA foreign_keys = ON", "PRAGMA foreign_keys", "BEGIN IMMEDIATE"}
		if trace := backend.sqliteRelationTraceSnapshot(); !relationBackendStringSlicesEqual(trace, wantTrace) {
			t.Fatalf("orphan sequence CreateModel trace=%#v, want %#v", trace, wantTrace)
		}
		if after := sqliteRelationDumpState(t, database); after != before {
			t.Fatalf("orphan sequence CreateModel rejection mutated durable state\nbefore:\n%s\nafter:\n%s", before, after)
		}
		if sqliteRelationRevision(t, database) != 0 ||
			sqliteRelationRecorderCount(t, database, intent.App, intent.Name) != 0 ||
			sqliteRelationTableExistsFromDatabase(t, database, "article") ||
			sqliteRelationTableExistsFromDatabase(t, database, "author") {
			t.Fatal("orphan sequence CreateModel rejection crossed revision, recorder, or DDL boundary")
		}
	})
}

func TestSQLiteRelationCandidateStaticCatalogRejectsExternalSQLBearingTableReference(t *testing.T) {
	catalog := sqliteRelationPhysicalCatalog{Objects: []sqliteRelationSchemaObject{
		{Schema: "main", Type: "table", Name: "article", Owner: "article", SQL: `CREATE TABLE "article" ("id" INTEGER PRIMARY KEY)`},
		{Schema: "main", Type: "table", Name: "article_fts", Owner: "article_fts", SQL: `CREATE VIRTUAL TABLE "article_fts" USING fts5("title", content='article')`},
	}}
	if err := sqliteRelationRejectStaticCatalogReferences(catalog, "article", make(map[string]struct{})); !errors.Is(err, sqliteRelationErrDrift) {
		t.Fatalf("external SQL-bearing table reference error=%v, want drift", err)
	}
	decoy := sqliteRelationPhysicalCatalog{Objects: []sqliteRelationSchemaObject{
		{Schema: "main", Type: "table", Name: "article_archive", Owner: "article_archive", SQL: `CREATE VIRTUAL TABLE "article_archive" USING fts5("title", content='article_archive')`},
	}}
	if err := sqliteRelationRejectStaticCatalogReferences(decoy, "article", make(map[string]struct{})); err != nil {
		t.Fatalf("longer external table identifier decoy rejected: %v", err)
	}
}

func TestSQLiteRelationCandidateCreateModelTargetMustPrecedeSourceOrExistPhysically(t *testing.T) {
	ctx := context.Background()
	t.Run("planned_target_before_source_succeeds", func(t *testing.T) {
		database, backend, _ := sqliteRelationOpenCandidate(t)
		defer database.Close()
		intent := relationBackendArticleCreateIntent()
		result, err := faultExecutePlan(ctx, backend, []faultExecutorStep{{
			Transition: relationBackendTransition{
				App: intent.App, Name: intent.Name, Direction: relationBackendApply,
				FromRevision: 0, ToRevision: 1,
			},
			Intent: intent,
		}})
		if err != nil || result.ConfirmedSteps != 1 {
			t.Fatalf("planned target-before-source result=%+v error=%v", result, err)
		}
	})

	tests := []struct {
		name    string
		intent  func() relationBackendStepIntent
		prepare func(*testing.T, *sql.DB)
	}{
		{
			name: "planned_target_after_source",
			intent: func() relationBackendStepIntent {
				intent := relationBackendArticleCreateIntent()
				intent.Changes[0], intent.Changes[1] = intent.Changes[1], intent.Changes[0]
				return intent
			},
		},
		{
			name: "missing_preexisting_target",
			intent: func() relationBackendStepIntent {
				return relationBackendStepIntent{
					App: "blog", Name: "source_only",
					Changes: []relationBackendChange{{
						Kind: relationBackendCreateModel, After: relationBackendArticleModel(false),
					}},
				}
			},
		},
		{
			name: "preexisting_target_wrong_pk",
			intent: func() relationBackendStepIntent {
				return relationBackendStepIntent{
					App: "blog", Name: "source_only",
					Changes: []relationBackendChange{{
						Kind: relationBackendCreateModel, After: relationBackendArticleModel(false),
					}},
				}
			},
			prepare: func(t *testing.T, database *sql.DB) {
				sqliteRelationExec(t, database, `CREATE TABLE "author" ("id" TEXT PRIMARY KEY, "name" TEXT NOT NULL)`)
			},
		},
		{
			name: "preexisting_target_deleted_before_source",
			intent: func() relationBackendStepIntent {
				return relationBackendStepIntent{
					App: "blog", Name: "deleted_target",
					Changes: []relationBackendChange{
						{Kind: relationBackendDeleteModel, Before: relationBackendAuthorModel()},
						{Kind: relationBackendCreateModel, After: relationBackendArticleModel(false)},
					},
				}
			},
			prepare: func(t *testing.T, database *sql.DB) {
				sqliteRelationExec(t, database, `CREATE TABLE "author" ("id" INTEGER PRIMARY KEY AUTOINCREMENT, "name" TEXT NOT NULL)`)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database, backend, _ := sqliteRelationOpenCandidate(t)
			defer database.Close()
			if test.prepare != nil {
				test.prepare(t, database)
			}
			intent := test.intent()
			before := sqliteRelationDumpState(t, database)
			traceStart := len(backend.sqliteRelationTraceSnapshot())
			_, err := faultExecutePlan(ctx, backend, []faultExecutorStep{{
				Transition: relationBackendTransition{
					App: intent.App, Name: intent.Name, Direction: relationBackendApply,
					FromRevision: 0, ToRevision: 1,
				},
				Intent: intent,
			}})
			wantError := error(sqliteRelationErrDrift)
			wantTrace := []string{"PRAGMA foreign_keys = ON", "PRAGMA foreign_keys", "BEGIN IMMEDIATE"}
			if test.name == "preexisting_target_deleted_before_source" {
				wantError = relationBackendErrIntent
				wantTrace = nil
			}
			if !errors.Is(err, wantError) {
				t.Fatalf("CreateModel target validation error = %v, want %v", err, wantError)
			}
			if trace := backend.sqliteRelationTraceSnapshot()[traceStart:]; !relationBackendStringSlicesEqual(trace, wantTrace) {
				t.Fatalf("CreateModel target validation trace = %#v, want %#v", trace, wantTrace)
			}
			if after := sqliteRelationDumpState(t, database); after != before {
				t.Fatalf("CreateModel target validation changed durable schema\nbefore:\n%s\nafter:\n%s", before, after)
			}
			if sqliteRelationRecorderCount(t, database, intent.App, intent.Name) != 0 || sqliteRelationRevision(t, database) != 0 {
				t.Fatal("CreateModel target validation changed recorder/revision")
			}
		})
	}

	t.Run("ordered_virtual_existing_table_still_checks_remake_hazards", func(t *testing.T) {
		database, backend, _ := sqliteRelationOpenCandidate(t)
		defer database.Close()
		sqliteRelationApplyInitialArticle(t, backend)
		before := relationBackendArticleModel(false)
		after := relationBackendArticleModel(true)
		editor := after.Relations[1]
		sqliteRelationExec(t, database, `CREATE TRIGGER "article_touch" AFTER UPDATE ON "article" BEGIN SELECT 1; END`)
		beforeState := sqliteRelationDumpState(t, database)
		traceStart := len(backend.sqliteRelationTraceSnapshot())
		intent := relationBackendStepIntent{App: "blog", Name: "0002", Changes: []relationBackendChange{
			{Kind: relationBackendAddField, Before: before, After: after, Relation: editor},
			{Kind: relationBackendRemoveField, Before: after, After: before, Relation: editor},
		}}
		_, err := faultExecutePlan(context.Background(), backend, []faultExecutorStep{{
			Transition: relationBackendTransition{App: "blog", Name: "0002", Direction: relationBackendApply, FromRevision: 1, ToRevision: 2},
			Intent:     intent,
		}})
		if !errors.Is(err, sqliteRelationErrTrigger) {
			t.Fatalf("ordered virtual remake preflight error = %v, want trigger rejection", err)
		}
		trace := backend.sqliteRelationTraceSnapshot()[traceStart:]
		wantTrace := []string{"PRAGMA foreign_keys = ON", "PRAGMA foreign_keys", "BEGIN IMMEDIATE"}
		if !relationBackendStringSlicesEqual(trace, wantTrace) {
			t.Fatalf("ordered virtual remake preflight trace = %#v, want exact pinned preflight %#v", trace, wantTrace)
		}
		if sqliteRelationRevision(t, database) != 1 || sqliteRelationRecorderCount(t, database, "blog", "0002") != 0 {
			t.Fatal("ordered virtual remake preflight changed revision or recorder")
		}
		if afterState := sqliteRelationDumpState(t, database); afterState != beforeState {
			t.Fatalf("ordered virtual remake preflight changed schema\nbefore:\n%s\nafter:\n%s", beforeState, afterState)
		}
	})
}

func TestSQLiteRelationCandidateCreateModelRejectsWholeMainNamespaceBeforeFirstDDL(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T, *sql.DB)
	}{
		{
			name: "table_case_fold",
			prepare: func(t *testing.T, database *sql.DB) {
				sqliteRelationExec(t, database, `CREATE TABLE "ArTiClE" ("id" INTEGER PRIMARY KEY)`)
			},
		},
		{
			name: "view_case_fold",
			prepare: func(t *testing.T, database *sql.DB) {
				sqliteRelationExec(t, database, `CREATE VIEW "ArTiClE" AS SELECT 1 AS "id"`)
			},
		},
		{
			name: "index_case_fold",
			prepare: func(t *testing.T, database *sql.DB) {
				sqliteRelationExec(t, database, `CREATE TABLE "legacy" ("id" INTEGER PRIMARY KEY)`)
				sqliteRelationExec(t, database, `CREATE INDEX "ArTiClE" ON "legacy" ("id")`)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database, backend, _ := sqliteRelationOpenCandidate(t)
			defer database.Close()
			test.prepare(t, database)
			intent := relationBackendArticleCreateIntent()
			before := sqliteRelationDumpState(t, database)
			result, err := faultExecutePlan(context.Background(), backend, []faultExecutorStep{{
				Transition: relationBackendTransition{
					App: intent.App, Name: intent.Name, Direction: relationBackendApply,
					FromRevision: 0, ToRevision: 1,
				},
				Intent: intent,
			}})
			if !errors.Is(err, sqliteRelationErrDrift) {
				t.Fatalf("main namespace collision result=%+v error=%v, want drift", result, err)
			}
			wantTrace := []string{"PRAGMA foreign_keys = ON", "PRAGMA foreign_keys", "BEGIN IMMEDIATE"}
			if trace := backend.sqliteRelationTraceSnapshot(); !relationBackendStringSlicesEqual(trace, wantTrace) {
				t.Fatalf("main namespace collision trace=%#v, want preflight only %#v", trace, wantTrace)
			}
			if backend.beginCalls != 1 || backend.rollbackCalls != 1 || backend.commitCalls != 0 {
				t.Fatalf(
					"main namespace collision lifecycle begin=%d rollback=%d commit=%d",
					backend.beginCalls,
					backend.rollbackCalls,
					backend.commitCalls,
				)
			}
			if sqliteRelationTableExistsFromDatabase(t, database, "author") {
				t.Fatal("later CreateModel namespace collision allowed the first planned table DDL")
			}
			if after := sqliteRelationDumpState(t, database); after != before {
				t.Fatalf("main namespace collision changed durable state\nbefore:\n%s\nafter:\n%s", before, after)
			}
			if sqliteRelationRevision(t, database) != 0 || sqliteRelationRecorderCount(t, database, intent.App, intent.Name) != 0 {
				t.Fatal("main namespace collision changed revision or recorder")
			}
		})
	}

	t.Run("trigger_name_may_share_table_name", func(t *testing.T) {
		database, backend, _ := sqliteRelationOpenCandidate(t)
		defer database.Close()
		sqliteRelationExec(t, database, `CREATE TABLE "audit_log" ("id" INTEGER PRIMARY KEY)`)
		sqliteRelationExec(t, database, `CREATE TRIGGER "article" AFTER INSERT ON "audit_log" BEGIN SELECT 1; END`)
		intent := relationBackendArticleCreateIntent()
		result, err := faultExecutePlan(context.Background(), backend, []faultExecutorStep{{
			Transition: relationBackendTransition{
				App: intent.App, Name: intent.Name, Direction: relationBackendApply,
				FromRevision: 0, ToRevision: 1,
			},
			Intent: intent,
		}})
		if err != nil || result.ConfirmedSteps != 1 || result.Outcome != migrationbackend.CommitCommitted {
			t.Fatalf("trigger namespace independence result=%+v error=%v", result, err)
		}
		if !sqliteRelationTableExistsFromDatabase(t, database, "author") ||
			!sqliteRelationTableExistsFromDatabase(t, database, "article") ||
			sqliteRelationRevision(t, database) != 1 ||
			sqliteRelationRecorderCount(t, database, intent.App, intent.Name) != 1 {
			t.Fatal("trigger namespace independence did not commit the complete CreateModel step")
		}
	})
}

func TestSQLiteRelationCandidateAddFieldTargetMissingOrWrongPKRejectsBeforeDDLRecorder(t *testing.T) {
	ctx := context.Background()
	t.Run("mixed_case_target_and_column_reject_before_backend_io", func(t *testing.T) {
		database, backend, _ := sqliteRelationOpenCandidate(t)
		defer database.Close()
		sqliteRelationApplyInitialArticle(t, backend)
		intent := relationBackendNullableAddIntent()
		intent.Name = "0002_mixed_target"
		intent.Changes[0].After.Relations[1].TargetTable = "AuThOr"
		intent.Changes[0].After.Relations[1].TargetColumn = "ID"
		intent.Changes[0].Relation = intent.Changes[0].After.Relations[1]
		before := sqliteRelationDumpState(t, database)
		traceStart := len(backend.sqliteRelationTraceSnapshot())
		openStart, beginStart := backend.openCalls, backend.beginCalls
		result, err := faultExecutePlan(ctx, backend, []faultExecutorStep{{
			Transition: relationBackendTransition{
				App: intent.App, Name: intent.Name, Direction: relationBackendApply,
				FromRevision: 1, ToRevision: 2,
			},
			Intent: intent,
		}})
		if !errors.Is(err, relationBackendErrIntent) {
			t.Fatalf("mixed-case target AddField result=%+v error=%v, want invalid intent", result, err)
		}
		if trace := backend.sqliteRelationTraceSnapshot()[traceStart:]; len(trace) != 0 ||
			backend.openCalls != openStart || backend.beginCalls != beginStart {
			t.Fatalf(
				"mixed-case target reached backend trace=%#v open=%d/%d begin=%d/%d",
				trace,
				backend.openCalls,
				openStart,
				backend.beginCalls,
				beginStart,
			)
		}
		if after := sqliteRelationDumpState(t, database); after != before ||
			sqliteRelationRevision(t, database) != 1 ||
			sqliteRelationRecorderCount(t, database, intent.App, intent.Name) != 0 {
			t.Fatal("mixed-case target rejection changed schema, revision, or recorder")
		}
	})

	tests := []struct {
		name    string
		prepare func(*testing.T, *sql.DB)
	}{
		{name: "missing", prepare: func(t *testing.T, database *sql.DB) {
			sqliteRelationExec(t, database, `DROP TABLE "author"`)
		}},
		{name: "wrong_non_pk", prepare: func(t *testing.T, database *sql.DB) {
			sqliteRelationExec(t, database, `PRAGMA foreign_keys = OFF`)
			sqliteRelationExec(t, database, `DROP TABLE "author"`)
			sqliteRelationExec(t, database, `CREATE TABLE "author" ("id" INTEGER NOT NULL, "name" TEXT NOT NULL)`)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database, backend, _ := sqliteRelationOpenCandidate(t)
			defer database.Close()
			sqliteRelationApplyInitialArticle(t, backend)
			test.prepare(t, database)
			intent := relationBackendNullableAddIntent()
			before := sqliteRelationDumpState(t, database)
			traceStart := len(backend.sqliteRelationTraceSnapshot())
			_, err := faultExecutePlan(ctx, backend, []faultExecutorStep{{
				Transition: relationBackendTransition{
					App: intent.App, Name: intent.Name, Direction: relationBackendApply,
					FromRevision: 1, ToRevision: 2,
				},
				Intent: intent,
			}})
			if !errors.Is(err, sqliteRelationErrDrift) {
				t.Fatalf("AddField target validation error = %v, want drift", err)
			}
			wantTrace := []string{"PRAGMA foreign_keys = ON", "PRAGMA foreign_keys", "BEGIN IMMEDIATE"}
			if trace := backend.sqliteRelationTraceSnapshot()[traceStart:]; !relationBackendStringSlicesEqual(trace, wantTrace) {
				t.Fatalf("AddField target validation trace = %#v, want %#v", trace, wantTrace)
			}
			if after := sqliteRelationDumpState(t, database); after != before {
				t.Fatalf("AddField target validation changed schema\nbefore:\n%s\nafter:\n%s", before, after)
			}
			if sqliteRelationColumnExists(t, database, "article", "editor_id") ||
				sqliteRelationRecorderCount(t, database, intent.App, intent.Name) != 0 ||
				sqliteRelationRevision(t, database) != 1 {
				t.Fatal("AddField target validation changed schema/recorder/revision")
			}
		})
	}
}

func TestSQLiteRelationCandidateControlCatalogFailsClosedBeforeRevisionAndRecorderWrites(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T, *sql.DB)
	}{
		{
			name: "revision_extra_column",
			prepare: func(t *testing.T, database *sql.DB) {
				sqliteRelationExec(t, database, `ALTER TABLE "`+sqliteRelationRevisionTable+`" ADD COLUMN "extra" TEXT`)
			},
		},
		{
			name: "recorder_extra_index",
			prepare: func(t *testing.T, database *sql.DB) {
				sqliteRelationExec(t, database, `CREATE INDEX "recorder_extra_idx" ON "`+sqliteRelationRecorderTable+`" ("name")`)
			},
		},
		{
			name: "main_trigger_owner_revision",
			prepare: func(t *testing.T, database *sql.DB) {
				sqliteRelationExec(t, database, `CREATE TRIGGER "revision_side_effect" BEFORE UPDATE ON "`+sqliteRelationRevisionTable+`" BEGIN SELECT 1; END`)
			},
		},
		{
			name: "main_external_trigger_body_recorder",
			prepare: func(t *testing.T, database *sql.DB) {
				sqliteRelationExec(t, database, `CREATE TABLE "audit_log" ("id" INTEGER PRIMARY KEY)`)
				sqliteRelationExec(t, database, `CREATE TRIGGER "audit_writes_recorder" AFTER INSERT ON "audit_log" BEGIN INSERT INTO "`+sqliteRelationRecorderTable+`" ("app", "name") VALUES ('evil', 'evil'); END`)
			},
		},
		{
			name: "temp_external_trigger_body_revision",
			prepare: func(t *testing.T, database *sql.DB) {
				sqliteRelationExec(t, database, `CREATE TABLE "audit_log" ("id" INTEGER PRIMARY KEY)`)
				sqliteRelationExec(t, database, `CREATE TEMP TRIGGER "audit_writes_revision" AFTER INSERT ON "audit_log" BEGIN UPDATE "`+sqliteRelationRevisionTable+`" SET "revision" = 999 WHERE "singleton" = 1; END`)
			},
		},
		{
			name: "temp_view_reads_recorder",
			prepare: func(t *testing.T, database *sql.DB) {
				sqliteRelationExec(t, database, `CREATE TEMP VIEW "recorder_leak" AS SELECT * FROM "`+sqliteRelationRecorderTable+`"`)
			},
		},
		{
			name: "user_table_inbound_fk_to_recorder",
			prepare: func(t *testing.T, database *sql.DB) {
				sqliteRelationExec(t, database,
					`CREATE TABLE "control_child" (`+
						`"app" TEXT NOT NULL, "name" TEXT NOT NULL, `+
						`FOREIGN KEY ("app", "name") REFERENCES "`+sqliteRelationRecorderTable+`" ("app", "name") ON DELETE CASCADE)`,
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database, backend, _ := sqliteRelationOpenCandidate(t)
			defer database.Close()
			test.prepare(t, database)
			intent := relationBackendArticleCreateIntent()
			before := sqliteRelationDumpState(t, database)
			result, err := faultExecutePlan(context.Background(), backend, []faultExecutorStep{{
				Transition: relationBackendTransition{
					App: intent.App, Name: intent.Name, Direction: relationBackendApply,
					FromRevision: 0, ToRevision: 1,
				},
				Intent: intent,
			}})
			if !errors.Is(err, sqliteRelationErrControlCatalog) {
				t.Fatalf("control catalog result=%+v error=%v, want closed-shape rejection", result, err)
			}
			wantTrace := []string{"PRAGMA foreign_keys = ON", "PRAGMA foreign_keys", "BEGIN IMMEDIATE"}
			if trace := backend.sqliteRelationTraceSnapshot(); !relationBackendStringSlicesEqual(trace, wantTrace) {
				t.Fatalf("control catalog trace=%#v, want no revision/DDL/recorder trace %#v", trace, wantTrace)
			}
			if backend.beginCalls != 1 || backend.rollbackCalls != 1 || backend.commitCalls != 0 ||
				sqliteRelationRevision(t, database) != 0 ||
				sqliteRelationRecorderCount(t, database, intent.App, intent.Name) != 0 ||
				sqliteRelationTableExistsFromDatabase(t, database, "author") ||
				sqliteRelationTableExistsFromDatabase(t, database, "article") {
				t.Fatal("control catalog rejection crossed revision, DDL, recorder, or commit boundary")
			}
			if after := sqliteRelationDumpState(t, database); after != before {
				t.Fatalf("control catalog rejection changed durable state\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})
	}

	t.Run("durable child inbound to recorder blocks actual unapply without cascade", func(t *testing.T) {
		database, backend, _ := sqliteRelationOpenCandidate(t)
		defer database.Close()
		sqliteRelationApplyInitialArticle(t, backend)
		sqliteRelationExec(t, database,
			`CREATE TABLE "control_child" (`+
				`"app" TEXT NOT NULL, "name" TEXT NOT NULL, `+
				`FOREIGN KEY ("app", "name") REFERENCES "`+sqliteRelationRecorderTable+`" ("app", "name") ON DELETE CASCADE)`,
		)
		sqliteRelationExec(t, database, `INSERT INTO "control_child" ("app", "name") VALUES ('blog', '0001')`)
		before := sqliteRelationDumpState(t, database)
		traceStart := len(backend.sqliteRelationTraceSnapshot())
		create := relationBackendArticleCreateIntent()
		unapply := relationBackendStepIntent{
			App:  create.App,
			Name: create.Name,
			Changes: []relationBackendChange{
				{Kind: relationBackendDeleteModel, Before: create.Changes[1].After},
				{Kind: relationBackendDeleteModel, Before: create.Changes[0].After},
			},
		}
		_, err := faultExecutePlan(context.Background(), backend, []faultExecutorStep{{
			Transition: relationBackendTransition{
				App: unapply.App, Name: unapply.Name, Direction: relationBackendUnapply,
				FromRevision: 1, ToRevision: 2,
			},
			Intent: unapply,
		}})
		if !errors.Is(err, sqliteRelationErrControlCatalog) {
			t.Fatalf("control-child unapply error=%v, want control inbound rejection", err)
		}
		wantTrace := []string{"PRAGMA foreign_keys = ON", "PRAGMA foreign_keys", "BEGIN IMMEDIATE"}
		if trace := backend.sqliteRelationTraceSnapshot()[traceStart:]; !relationBackendStringSlicesEqual(trace, wantTrace) {
			t.Fatalf("control-child unapply trace=%#v, want %#v", trace, wantTrace)
		}
		var childRows, recorderRows int
		if err := database.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM "control_child"`).Scan(&childRows); err != nil {
			t.Fatalf("count control children after rejected unapply: %v", err)
		}
		if err := database.QueryRowContext(
			context.Background(),
			`SELECT COUNT(*) FROM `+sqliteRelationQualifiedMain(sqliteRelationRecorderTable),
		).Scan(&recorderRows); err != nil {
			t.Fatalf("count recorder after rejected control-child unapply: %v", err)
		}
		if childRows != 1 || recorderRows != 1 || sqliteRelationRevision(t, database) != 1 ||
			!sqliteRelationTableExistsFromDatabase(t, database, "author") ||
			!sqliteRelationTableExistsFromDatabase(t, database, "article") {
			t.Fatalf(
				"control-child rejection state child=%d recorder=%d revision=%d author=%t article=%t",
				childRows,
				recorderRows,
				sqliteRelationRevision(t, database),
				sqliteRelationTableExistsFromDatabase(t, database, "author"),
				sqliteRelationTableExistsFromDatabase(t, database, "article"),
			)
		}
		if after := sqliteRelationDumpState(t, database); after != before {
			t.Fatalf("control-child unapply rejection mutated durable state\nbefore:\n%s\nafter:\n%s", before, after)
		}
	})

	t.Run("initialize_validates_before_insert_or_ignore_trigger", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "control-init.sqlite3")
		database, err := sql.Open("sqlite", "file:"+path)
		if err != nil {
			t.Fatalf("open control initialization fixture: %v", err)
		}
		defer database.Close()
		if err := sqliteRelationInitialize(context.Background(), database); err != nil {
			t.Fatalf("first control initialization: %v", err)
		}
		sqliteRelationExec(t, database, `CREATE TABLE "effects" ("count" INTEGER NOT NULL)`)
		sqliteRelationExec(t, database, `INSERT INTO "effects" ("count") VALUES (0)`)
		sqliteRelationExec(t, database, `CREATE TRIGGER "revision_insert_effect" BEFORE INSERT ON "`+sqliteRelationRevisionTable+`" BEGIN UPDATE "effects" SET "count" = "count" + 1; END`)
		if err := sqliteRelationInitialize(context.Background(), database); !errors.Is(err, sqliteRelationErrControlCatalog) {
			t.Fatalf("second control initialization error=%v, want trigger rejection", err)
		}
		var effects int
		if err := database.QueryRowContext(context.Background(), `SELECT "count" FROM "effects"`).Scan(&effects); err != nil {
			t.Fatalf("read initialization side effects: %v", err)
		}
		if effects != 0 || sqliteRelationRevision(t, database) != 0 {
			t.Fatalf("rejected initialization side effects=%d revision=%d, want zero", effects, sqliteRelationRevision(t, database))
		}
	})

	t.Run("initialize_rejects_preexisting_malformed_control_table", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "malformed-control-init.sqlite3")
		database, err := sql.Open("sqlite", "file:"+path)
		if err != nil {
			t.Fatalf("open malformed control initialization fixture: %v", err)
		}
		defer database.Close()
		sqliteRelationExec(t, database, `CREATE TABLE "`+sqliteRelationRevisionTable+`" (`+
			`"singleton" INTEGER NOT NULL PRIMARY KEY, "revision" INTEGER NOT NULL, "extra" TEXT)`)
		if err := sqliteRelationInitialize(context.Background(), database); !errors.Is(err, sqliteRelationErrControlCatalog) {
			t.Fatalf("malformed control initialization error=%v, want closed-shape rejection", err)
		}
		if sqliteRelationTableExistsFromDatabase(t, database, sqliteRelationRecorderTable) {
			t.Fatal("malformed control initialization committed the newly created recorder table")
		}
		var rows int
		if err := database.QueryRowContext(
			context.Background(),
			`SELECT COUNT(*) FROM "`+sqliteRelationRevisionTable+`"`,
		).Scan(&rows); err != nil {
			t.Fatalf("read malformed revision control rows: %v", err)
		}
		if rows != 0 {
			t.Fatalf("malformed control initialization inserted %d revision row(s), want 0", rows)
		}
	})

	t.Run("initialize_holds_immediate_writer_barrier_after_validation", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "control-init-barrier.sqlite3")
		first, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(25)")
		if err != nil {
			t.Fatalf("open first initialization handle: %v", err)
		}
		defer first.Close()
		if err := sqliteRelationInitialize(context.Background(), first); err != nil {
			t.Fatalf("first barrier initialization: %v", err)
		}
		second, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(25)")
		if err != nil {
			t.Fatalf("open second initialization handle: %v", err)
		}
		defer second.Close()
		barrierCause := errors.New("concurrent catalog mutation blocked")
		sqliteRelationInitializeAfterValidationHook = func() error {
			_, err := second.ExecContext(
				context.Background(),
				`CREATE TRIGGER "late_revision_effect" BEFORE INSERT ON "`+sqliteRelationRevisionTable+`" BEGIN SELECT 1; END`,
			)
			if err == nil {
				return errors.New("concurrent catalog mutation unexpectedly acquired writer lock")
			}
			return barrierCause
		}
		defer func() { sqliteRelationInitializeAfterValidationHook = nil }()
		if err := sqliteRelationInitialize(context.Background(), first); !errors.Is(err, barrierCause) {
			t.Fatalf("barrier initialization error=%v, want injected barrier cause", err)
		}
		var triggers int
		if err := first.QueryRowContext(
			context.Background(),
			`SELECT COUNT(*) FROM "main"."sqlite_schema" WHERE "type" = 'trigger' AND "name" = 'late_revision_effect'`,
		).Scan(&triggers); err != nil {
			t.Fatalf("inspect late initialization trigger: %v", err)
		}
		if triggers != 0 || sqliteRelationRevision(t, first) != 0 {
			t.Fatalf("initialization writer barrier trigger=%d revision=%d, want unchanged", triggers, sqliteRelationRevision(t, first))
		}
	})

	t.Run("durable revision and recorder integrity reject before claim without mutation", func(t *testing.T) {
		tests := []struct {
			name         string
			fromRevision int64
			want         error
			prepare      func(*testing.T, *sql.DB)
		}{
			{
				name: "negative revision", want: sqliteRelationErrRevision,
				prepare: func(t *testing.T, database *sql.DB) {
					sqliteRelationExec(t, database, `UPDATE "`+sqliteRelationRevisionTable+`" SET "revision" = -1 WHERE "singleton" = 1`)
				},
			},
			{
				name: "extra revision singleton row", want: sqliteRelationErrRevision,
				prepare: func(t *testing.T, database *sql.DB) {
					sqliteRelationExec(t, database, `PRAGMA ignore_check_constraints = ON`)
					sqliteRelationExec(t, database, `INSERT INTO "`+sqliteRelationRevisionTable+`" ("singleton", "revision") VALUES (2, 0)`)
					sqliteRelationExec(t, database, `PRAGMA ignore_check_constraints = OFF`)
				},
			},
			{
				name: "recorder count ahead of revision", want: sqliteRelationErrRecorder,
				prepare: func(t *testing.T, database *sql.DB) {
					sqliteRelationExec(t, database, `INSERT INTO "`+sqliteRelationRecorderTable+`" ("app", "name") VALUES ('blog', 'orphan')`)
				},
			},
			{
				name: "impossible revision recorder parity", fromRevision: 2, want: sqliteRelationErrRecorder,
				prepare: func(t *testing.T, database *sql.DB) {
					sqliteRelationExec(t, database, `UPDATE "`+sqliteRelationRevisionTable+`" SET "revision" = 2 WHERE "singleton" = 1`)
					sqliteRelationExec(t, database, `INSERT INTO "`+sqliteRelationRecorderTable+`" ("app", "name") VALUES ('blog', 'orphan')`)
				},
			},
			{
				name: "oversized recorder identity", fromRevision: 1, want: sqliteRelationErrRecorder,
				prepare: func(t *testing.T, database *sql.DB) {
					if _, err := database.ExecContext(
						context.Background(),
						`UPDATE "`+sqliteRelationRevisionTable+`" SET "revision" = 1 WHERE "singleton" = 1; `+
							`INSERT INTO "`+sqliteRelationRecorderTable+`" ("app", "name") VALUES (?, 'oversized')`,
						strings.Repeat("a", migrationdefinition.MaxSourceIDBytes+1),
					); err != nil {
						t.Fatalf("seed oversized recorder identity: %v", err)
					}
				},
			},
			{
				name: "invalid UTF-8 recorder identity", fromRevision: 1, want: sqliteRelationErrRecorder,
				prepare: func(t *testing.T, database *sql.DB) {
					invalid := string([]byte{0xff})
					if utf8.ValidString(invalid) {
						t.Fatal("invalid UTF-8 recorder fixture unexpectedly valid")
					}
					if _, err := database.ExecContext(
						context.Background(),
						`UPDATE "`+sqliteRelationRevisionTable+`" SET "revision" = 1 WHERE "singleton" = 1; `+
							`INSERT INTO "`+sqliteRelationRecorderTable+`" ("app", "name") VALUES (?, 'invalid_utf8')`,
						invalid,
					); err != nil {
						t.Fatalf("seed invalid UTF-8 recorder identity: %v", err)
					}
				},
			},
			{
				name: "BLOB recorder identity alias", fromRevision: 1, want: sqliteRelationErrRecorder,
				prepare: func(t *testing.T, database *sql.DB) {
					sqliteRelationExec(t, database, `UPDATE "`+sqliteRelationRevisionTable+`" SET "revision" = 1 WHERE "singleton" = 1`)
					sqliteRelationExec(t, database, `INSERT INTO "`+sqliteRelationRecorderTable+`" ("app", "name") VALUES (X'626c6f67', X'30303031')`)
				},
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				database, backend, _ := sqliteRelationOpenCandidate(t)
				defer database.Close()
				test.prepare(t, database)
				before := sqliteRelationDumpState(t, database)
				var recorderRows int
				if err := database.QueryRowContext(
					context.Background(),
					`SELECT COUNT(*) FROM `+sqliteRelationQualifiedMain(sqliteRelationRecorderTable),
				).Scan(&recorderRows); err != nil {
					t.Fatalf("count corrupt recorder fixture: %v", err)
				}
				intent := relationBackendArticleCreateIntent()
				_, err := faultExecutePlan(context.Background(), backend, []faultExecutorStep{{
					Transition: relationBackendTransition{
						App: intent.App, Name: intent.Name, Direction: relationBackendApply,
						FromRevision: test.fromRevision, ToRevision: test.fromRevision + 1,
					},
					Intent: intent,
				}})
				if !errors.Is(err, test.want) {
					t.Fatalf("corrupt durable state error=%v, want %v", err, test.want)
				}
				wantTrace := []string{"PRAGMA foreign_keys = ON", "PRAGMA foreign_keys", "BEGIN IMMEDIATE"}
				if trace := backend.sqliteRelationTraceSnapshot(); !relationBackendStringSlicesEqual(trace, wantTrace) {
					t.Fatalf("corrupt durable state trace=%#v, want %#v", trace, wantTrace)
				}
				if backend.beginCalls != 1 || backend.rollbackCalls != 1 || backend.commitCalls != 0 {
					t.Fatalf("corrupt durable state lifecycle begin=%d rollback=%d commit=%d", backend.beginCalls, backend.rollbackCalls, backend.commitCalls)
				}
				if after := sqliteRelationDumpState(t, database); after != before {
					t.Fatalf("corrupt durable state rejection mutated schema/revision\nbefore:\n%s\nafter:\n%s", before, after)
				}
				var afterRecorderRows int
				if err := database.QueryRowContext(
					context.Background(),
					`SELECT COUNT(*) FROM `+sqliteRelationQualifiedMain(sqliteRelationRecorderTable),
				).Scan(&afterRecorderRows); err != nil {
					t.Fatalf("count recorder after corrupt rejection: %v", err)
				}
				if afterRecorderRows != recorderRows ||
					sqliteRelationTableExistsFromDatabase(t, database, "author") ||
					sqliteRelationTableExistsFromDatabase(t, database, "article") {
					t.Fatal("corrupt durable state rejection changed recorder or user schema")
				}
			})
		}
	})

	t.Run("reinitialize rejects corrupt durable state without changing it", func(t *testing.T) {
		database, _, _ := sqliteRelationOpenCandidate(t)
		defer database.Close()
		sqliteRelationExec(t, database, `UPDATE "`+sqliteRelationRevisionTable+`" SET "revision" = 2 WHERE "singleton" = 1`)
		sqliteRelationExec(t, database, `INSERT INTO "`+sqliteRelationRecorderTable+`" ("app", "name") VALUES ('blog', 'orphan')`)
		before := sqliteRelationDumpState(t, database)
		if err := sqliteRelationInitialize(context.Background(), database); !errors.Is(err, sqliteRelationErrRecorder) {
			t.Fatalf("corrupt reinitialization error=%v, want recorder integrity rejection", err)
		}
		if after := sqliteRelationDumpState(t, database); after != before {
			t.Fatalf("corrupt reinitialization changed durable state\nbefore:\n%s\nafter:\n%s", before, after)
		}
		if sqliteRelationRecorderCount(t, database, "blog", "orphan") != 1 || sqliteRelationRevision(t, database) != 2 {
			t.Fatal("corrupt reinitialization did not preserve the preexisting corrupt snapshot")
		}
	})
}

func TestSQLiteRelationCandidatePhysicalPreflightRejectsHazardsBeforeDDLAndRecorder(t *testing.T) {
	t.Run("physical catalog accepts exact object bound and rejects one more", func(t *testing.T) {
		database, _, _ := sqliteRelationOpenCandidate(t)
		defer database.Close()
		connection, err := database.Conn(context.Background())
		if err != nil {
			t.Fatalf("pin exact physical catalog connection: %v", err)
		}
		defer connection.Close()
		baseline, err := sqliteRelationLoadPhysicalCatalog(
			context.Background(), connection, sqliteRelationDefaultPhysicalLimits,
		)
		if err != nil {
			t.Fatalf("load baseline physical catalog: %v", err)
		}
		limits := sqliteRelationDefaultPhysicalLimits
		limits.MaxObjects = len(baseline.Objects)
		if _, err := sqliteRelationLoadPhysicalCatalog(context.Background(), connection, limits); err != nil {
			t.Fatalf("exact physical object bound rejected: %v", err)
		}
		if _, err := connection.ExecContext(context.Background(), `CREATE TEMP VIEW "one_more_object" AS SELECT 1`); err != nil {
			t.Fatalf("create one-more temp catalog object: %v", err)
		}
		if _, err := sqliteRelationLoadPhysicalCatalog(context.Background(), connection, limits); !errors.Is(err, sqliteRelationErrDrift) {
			t.Fatalf("one-more physical object error = %v, want drift", err)
		}
	})

	t.Run("schema object scan has product-tied count and byte budgets", func(t *testing.T) {
		count, statementBytes := 0, 0
		for index := 0; index < profileMaxDocuments; index++ {
			if err := sqliteRelationConsumeSchemaObjectBudget(
				fmt.Sprintf("view_%04d", index),
				"SELECT 1",
				&count,
				&statementBytes,
			); err != nil {
				t.Fatalf("accepted physical schema budget item %d: %v", index, err)
			}
		}
		if err := sqliteRelationConsumeSchemaObjectBudget("overflow", "SELECT 1", &count, &statementBytes); !errors.Is(err, sqliteRelationErrDrift) {
			t.Fatalf("schema object count overflow error = %v, want drift", err)
		}

		count, statementBytes = 0, 0
		if err := sqliteRelationConsumeSchemaObjectBudget(
			"oversized_sql",
			strings.Repeat("x", (1<<20)+1),
			&count,
			&statementBytes,
		); !errors.Is(err, sqliteRelationErrDrift) {
			t.Fatalf("schema object statement overflow error = %v, want drift", err)
		}

		count, statementBytes = 0, (16<<20)-4
		if err := sqliteRelationConsumeSchemaObjectBudget("batch_overflow", "12345", &count, &statementBytes); !errors.Is(err, sqliteRelationErrDrift) {
			t.Fatalf("schema object batch overflow error = %v, want drift", err)
		}
	})

	t.Run("aggregate physical catalog and graph work budgets fail before revision", func(t *testing.T) {
		t.Run("aggregate table SQL", func(t *testing.T) {
			database, backend, _ := sqliteRelationOpenCandidate(t)
			defer database.Close()
			limits := sqliteRelationDefaultPhysicalLimits
			limits.MaxBatchBytes = 1
			backend.physicalLimits = &limits
			intent := relationBackendArticleCreateIntent()
			before := sqliteRelationDumpState(t, database)
			_, err := faultExecutePlan(context.Background(), backend, []faultExecutorStep{{
				Transition: relationBackendTransition{
					App: intent.App, Name: intent.Name, Direction: relationBackendApply,
					FromRevision: 0, ToRevision: 1,
				},
				Intent: intent,
			}})
			if !errors.Is(err, sqliteRelationErrDrift) {
				t.Fatalf("aggregate table SQL budget error=%v, want drift", err)
			}
			if backend.beginCalls != 1 || backend.rollbackCalls != 1 || backend.commitCalls != 0 ||
				sqliteRelationRevision(t, database) != 0 ||
				sqliteRelationRecorderCount(t, database, intent.App, intent.Name) != 0 ||
				sqliteRelationTableExistsFromDatabase(t, database, "author") ||
				sqliteRelationTableExistsFromDatabase(t, database, "article") {
				t.Fatal("aggregate physical table SQL rejection crossed revision, DDL, recorder, or commit boundary")
			}
			if after := sqliteRelationDumpState(t, database); after != before {
				t.Fatalf("aggregate physical SQL rejection changed state\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})

		t.Run("repeated intent graph work", func(t *testing.T) {
			database, backend, _ := sqliteRelationOpenCandidate(t)
			defer database.Close()
			limits := sqliteRelationDefaultPhysicalLimits
			limits.MaxGraphWork = 10
			backend.physicalLimits = &limits
			intent := relationBackendStepIntent{App: "blog", Name: "graph_work"}
			for index := 0; index < 3; index++ {
				intent.Changes = append(intent.Changes, relationBackendChange{
					Kind: relationBackendCreateModel,
					After: relationBackendModel{
						Table: fmt.Sprintf("work_%d", index),
						Columns: []relationBackendColumn{{
							Name: "id", Type: "INTEGER", NotNull: true, PrimaryKey: true, AutoIncrement: true, Position: 1,
						}},
					},
				})
			}
			before := sqliteRelationDumpState(t, database)
			_, err := faultExecutePlan(context.Background(), backend, []faultExecutorStep{{
				Transition: relationBackendTransition{
					App: intent.App, Name: intent.Name, Direction: relationBackendApply,
					FromRevision: 0, ToRevision: 1,
				},
				Intent: intent,
			}})
			if !errors.Is(err, sqliteRelationErrDrift) {
				t.Fatalf("repeated physical graph work error=%v, want drift", err)
			}
			if backend.beginCalls != 1 || backend.rollbackCalls != 1 || backend.commitCalls != 0 ||
				sqliteRelationRevision(t, database) != 0 ||
				sqliteRelationRecorderCount(t, database, intent.App, intent.Name) != 0 {
				t.Fatal("repeated physical graph work rejection crossed revision, recorder, or commit boundary")
			}
			for index := 0; index < 3; index++ {
				if sqliteRelationTableExistsFromDatabase(t, database, fmt.Sprintf("work_%d", index)) {
					t.Fatalf("repeated physical graph work created work_%d before rejection", index)
				}
			}
			if after := sqliteRelationDumpState(t, database); after != before {
				t.Fatalf("repeated physical graph work rejection changed state\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})

		t.Run("repeated remake catalog scan work", func(t *testing.T) {
			database, backend, _ := sqliteRelationOpenCandidate(t)
			defer database.Close()
			sqliteRelationApplyInitialArticle(t, backend)
			commitStart := backend.commitCalls
			beforeModel := relationBackendArticleModel(false)
			afterModel := relationBackendArticleModel(true)
			editor := afterModel.Relations[1]
			intent := relationBackendStepIntent{App: "blog", Name: "catalog_work", Changes: []relationBackendChange{
				{Kind: relationBackendAddField, Before: beforeModel, After: afterModel, Relation: editor},
				{Kind: relationBackendRemoveField, Before: afterModel, After: beforeModel, Relation: editor},
			}}
			limits := sqliteRelationDefaultPhysicalLimits
			limits.MaxCatalogWork = 1
			backend.physicalLimits = &limits
			before := sqliteRelationDumpState(t, database)
			_, err := faultExecutePlan(context.Background(), backend, []faultExecutorStep{{
				Transition: relationBackendTransition{
					App: intent.App, Name: intent.Name, Direction: relationBackendApply,
					FromRevision: 1, ToRevision: 2,
				},
				Intent: intent,
			}})
			if !errors.Is(err, sqliteRelationErrDrift) || !strings.Contains(err.Error(), "catalog scan work") {
				t.Fatalf("repeated catalog scan work error=%v, want drift", err)
			}
			initialRecorder := sqliteRelationRecorderCount(t, database, "blog", "0001")
			candidateRecorder := sqliteRelationRecorderCount(t, database, intent.App, intent.Name)
			editorExists := sqliteRelationColumnExists(t, database, "article", "editor_id")
			if backend.commitCalls != commitStart || sqliteRelationRevision(t, database) != 1 ||
				initialRecorder != 1 || candidateRecorder != 0 || editorExists {
				t.Fatalf(
					"catalog scan work rejection crossed boundary commit=%d revision=%d initial_recorder=%d candidate_recorder=%d editor=%t",
					backend.commitCalls-commitStart,
					sqliteRelationRevision(t, database),
					initialRecorder,
					candidateRecorder,
					editorExists,
				)
			}
			if after := sqliteRelationDumpState(t, database); after != before {
				t.Fatalf("catalog scan work rejection changed state\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})
	})

	tests := []struct {
		name    string
		prepare func(*testing.T, *sql.DB, relationBackendChange)
		want    error
	}{
		{name: "unmanaged_index", want: sqliteRelationErrIndex, prepare: func(t *testing.T, database *sql.DB, _ relationBackendChange) {
			sqliteRelationExec(t, database, `CREATE INDEX "article_title_idx" ON "article" ("title")`)
		}},
		{name: "trigger", want: sqliteRelationErrTrigger, prepare: func(t *testing.T, database *sql.DB, _ relationBackendChange) {
			sqliteRelationExec(t, database, `CREATE TRIGGER "article_touch" AFTER UPDATE ON "article" BEGIN SELECT 1; END`)
		}},
		{name: "temp_trigger", want: sqliteRelationErrTrigger, prepare: func(t *testing.T, database *sql.DB, _ relationBackendChange) {
			sqliteRelationExec(t, database, `CREATE TEMP TRIGGER "article_temp_touch" AFTER UPDATE ON "article" BEGIN SELECT 1; END`)
		}},
		{name: "external_trigger_body", want: sqliteRelationErrTrigger, prepare: func(t *testing.T, database *sql.DB, _ relationBackendChange) {
			sqliteRelationExec(t, database, `CREATE TABLE "audit_log" ("id" INTEGER PRIMARY KEY)`)
			sqliteRelationExec(t, database, `CREATE TRIGGER "audit_updates_article" AFTER INSERT ON "audit_log" BEGIN UPDATE "article" SET "title" = "title" WHERE "id" = NEW."id"; END`)
		}},
		{name: "external_temp_trigger_body", want: sqliteRelationErrTrigger, prepare: func(t *testing.T, database *sql.DB, _ relationBackendChange) {
			sqliteRelationExec(t, database, `CREATE TABLE "audit_log" ("id" INTEGER PRIMARY KEY)`)
			sqliteRelationExec(t, database, `CREATE TEMP TRIGGER "audit_temp_updates_article" AFTER INSERT ON "audit_log" BEGIN UPDATE "article" SET "title" = "title" WHERE "id" = NEW."id"; END`)
		}},
		{name: "view", want: sqliteRelationErrView, prepare: func(t *testing.T, database *sql.DB, _ relationBackendChange) {
			sqliteRelationExec(t, database, `CREATE VIEW "article_view" AS SELECT * FROM "article"`)
		}},
		{name: "temp_view", want: sqliteRelationErrView, prepare: func(t *testing.T, database *sql.DB, _ relationBackendChange) {
			sqliteRelationExec(t, database, `CREATE TEMP VIEW "article_temp_view" AS SELECT * FROM "article"`)
		}},
		{name: "inbound", want: sqliteRelationErrInbound, prepare: func(t *testing.T, database *sql.DB, _ relationBackendChange) {
			sqliteRelationExec(t, database, `CREATE TABLE "comment" ("id" INTEGER PRIMARY KEY, "article_id" INTEGER REFERENCES "ArTiClE" ("id") ON DELETE NO ACTION)`)
		}},
		{name: "temp_shadow_source_case_fold", want: sqliteRelationErrTempShadow, prepare: func(t *testing.T, database *sql.DB, _ relationBackendChange) {
			sqliteRelationExec(t, database, `CREATE TEMP TABLE "ArTiClE" ("id" INTEGER PRIMARY KEY)`)
		}},
		{name: "temp_shadow_target_case_fold", want: sqliteRelationErrTempShadow, prepare: func(t *testing.T, database *sql.DB, _ relationBackendChange) {
			sqliteRelationExec(t, database, `CREATE TEMP TABLE "AuThOr" ("id" INTEGER PRIMARY KEY)`)
		}},
		{name: "temp_collision", want: sqliteRelationErrTempCollision, prepare: func(t *testing.T, database *sql.DB, change relationBackendChange) {
			sqliteRelationExec(t, database, `CREATE TABLE `+sqliteRelationQuoteIdentifier(sqliteRelationTemporaryTable(change))+` ("id" INTEGER PRIMARY KEY)`)
		}},
		{name: "drift", want: sqliteRelationErrDrift, prepare: func(t *testing.T, database *sql.DB, _ relationBackendChange) {
			sqliteRelationExec(t, database, `ALTER TABLE "article" ADD COLUMN "unmanaged" TEXT`)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database, backend, _ := sqliteRelationOpenCandidate(t)
			defer database.Close()
			sqliteRelationApplyInitialArticle(t, backend)
			before := relationBackendArticleModel(false)
			after := relationBackendArticleModel(true)
			editor := after.Relations[1]
			add := relationBackendStepIntent{App: "blog", Name: "0002", Changes: []relationBackendChange{{
				Kind: relationBackendAddField, Before: before, After: after, Relation: editor,
			}}}
			if _, err := faultExecutePlan(context.Background(), backend, []faultExecutorStep{{
				Transition: relationBackendTransition{App: "blog", Name: "0002", Direction: relationBackendApply, FromRevision: 1, ToRevision: 2},
				Intent:     add,
			}}); err != nil {
				t.Fatalf("add editor relation: %v", err)
			}
			change := relationBackendChange{Kind: relationBackendRemoveField, Before: after, After: before, Relation: editor}
			test.prepare(t, database, change)
			beforeState := sqliteRelationDumpState(t, database)
			traceStart := len(backend.sqliteRelationTraceSnapshot())
			intent := relationBackendStepIntent{App: "blog", Name: "0002", Changes: []relationBackendChange{change}}
			_, err := faultExecutePlan(context.Background(), backend, []faultExecutorStep{{
				Transition: relationBackendTransition{App: "blog", Name: "0002", Direction: relationBackendUnapply, FromRevision: 2, ToRevision: 3},
				Intent:     intent,
			}})
			if !errors.Is(err, test.want) {
				t.Fatalf("physical preflight error = %v, want %v", err, test.want)
			}
			trace := backend.sqliteRelationTraceSnapshot()[traceStart:]
			wantTrace := []string{"PRAGMA foreign_keys = ON", "PRAGMA foreign_keys", "BEGIN IMMEDIATE"}
			if !relationBackendStringSlicesEqual(trace, wantTrace) {
				t.Fatalf("failed preflight trace = %#v, want exact pinned preflight %#v", trace, wantTrace)
			}
			if sqliteRelationRevision(t, database) != 2 || sqliteRelationRecorderCount(t, database, "blog", "0002") != 1 {
				t.Fatal("failed physical preflight changed revision or recorder")
			}
			if afterState := sqliteRelationDumpState(t, database); afterState != beforeState {
				t.Fatalf("failed physical preflight changed schema\nbefore:\n%s\nafter:\n%s", beforeState, afterState)
			}
		})
	}
}

func TestSQLiteRelationCandidateRemakeHazardIdentifierMatchingIgnoresLongerDecoys(t *testing.T) {
	database, backend, _ := sqliteRelationOpenCandidate(t)
	defer database.Close()
	sqliteRelationApplyInitialArticle(t, backend)
	before := relationBackendArticleModel(false)
	after := relationBackendArticleModel(true)
	editor := after.Relations[1]
	add := relationBackendStepIntent{App: "blog", Name: "0002", Changes: []relationBackendChange{{
		Kind: relationBackendAddField, Before: before, After: after, Relation: editor,
	}}}
	if result, err := faultExecutePlan(context.Background(), backend, []faultExecutorStep{{
		Transition: relationBackendTransition{
			App: add.App, Name: add.Name, Direction: relationBackendApply,
			FromRevision: 1, ToRevision: 2,
		},
		Intent: add,
	}}); err != nil || result.ConfirmedSteps != 1 {
		t.Fatalf("prepare identifier-boundary remake result=%+v error=%v", result, err)
	}
	sqliteRelationExec(t, database, `CREATE TABLE "article_archive" ("id" INTEGER PRIMARY KEY)`)
	sqliteRelationExec(t, database, `CREATE VIEW "article_archive_view" AS SELECT * FROM "article_archive"`)
	sqliteRelationExec(t, database, `CREATE TABLE "audit_log" ("id" INTEGER PRIMARY KEY)`)
	sqliteRelationExec(t, database, `CREATE TRIGGER "audit_updates_article_archive" AFTER INSERT ON "audit_log" BEGIN UPDATE "article_archive" SET "id" = "id" WHERE "id" = NEW."id"; END`)
	remove := relationBackendStepIntent{App: add.App, Name: add.Name, Changes: []relationBackendChange{{
		Kind: relationBackendRemoveField, Before: after, After: before, Relation: editor,
	}}}
	result, err := faultExecutePlan(context.Background(), backend, []faultExecutorStep{{
		Transition: relationBackendTransition{
			App: remove.App, Name: remove.Name, Direction: relationBackendUnapply,
			FromRevision: 2, ToRevision: 3,
		},
		Intent: remove,
	}})
	if err != nil || result.ConfirmedSteps != 1 || result.Outcome != migrationbackend.CommitCommitted {
		t.Fatalf("identifier-boundary remake result=%+v error=%v", result, err)
	}
	if sqliteRelationColumnExists(t, database, "article", "editor_id") ||
		sqliteRelationRevision(t, database) != 3 ||
		sqliteRelationRecorderCount(t, database, add.App, add.Name) != 0 {
		t.Fatal("identifier-boundary remake did not preserve unapply lifecycle semantics")
	}
}

func TestSQLiteRelationCandidateGeneratedColumnRejectsBeforeDDL(t *testing.T) {
	ctx := context.Background()
	database, backend, _ := sqliteRelationOpenCandidate(t)
	defer database.Close()
	sqliteRelationExec(t, database, `CREATE TABLE "author" (`+
		`"id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, "name" TEXT NOT NULL)`)
	sqliteRelationExec(t, database, `CREATE TABLE "generated_article" (`+
		`"id" INTEGER PRIMARY KEY AUTOINCREMENT, "title" TEXT NOT NULL, `+
		`"slug" TEXT GENERATED ALWAYS AS (lower("title")) STORED, `+
		`"author_id" INTEGER NOT NULL REFERENCES "author" ("id") ON DELETE NO ACTION, `+
		`"editor_id" INTEGER REFERENCES "author" ("id") ON DELETE NO ACTION)`)
	before := relationBackendModel{
		Table: "generated_article",
		Columns: []relationBackendColumn{
			{Name: "id", Type: "INTEGER", NotNull: true, PrimaryKey: true, AutoIncrement: true, Position: 1},
			{Name: "title", Type: "VARCHAR", MaxLength: 200, NotNull: true, Position: 2},
			{Name: "slug", Type: "VARCHAR", MaxLength: 200, Nullable: true, Position: 3},
		},
		Relations: []relationBackendRelation{
			{Name: "author", Column: "author_id", TargetTable: "author", TargetColumn: "id", OnDelete: relationBackendProtect, Position: 4},
			{Name: "editor", Column: "editor_id", TargetTable: "author", TargetColumn: "id", Nullable: true, OnDelete: relationBackendSetNull, Position: 5},
		},
	}
	after := before.relationBackendClone()
	after.Relations = after.Relations[:1]
	intent := relationBackendStepIntent{App: "blog", Name: "generated", Changes: []relationBackendChange{{
		Kind: relationBackendRemoveField, Before: before, After: after,
		Relation: before.Relations[1],
	}}}
	beforeState := sqliteRelationDumpState(t, database)
	traceStart := len(backend.sqliteRelationTraceSnapshot())
	_, err := faultExecutePlan(ctx, backend, []faultExecutorStep{{
		Transition: relationBackendTransition{App: "blog", Name: "generated", Direction: relationBackendApply, FromRevision: 0, ToRevision: 1},
		Intent:     intent,
	}})
	if !errors.Is(err, sqliteRelationErrGenerated) {
		t.Fatalf("generated-column preflight error = %v, want generated rejection", err)
	}
	wantTrace := []string{"PRAGMA foreign_keys = ON", "PRAGMA foreign_keys", "BEGIN IMMEDIATE"}
	if trace := backend.sqliteRelationTraceSnapshot()[traceStart:]; !relationBackendStringSlicesEqual(trace, wantTrace) {
		t.Fatalf("generated-column preflight trace = %#v, want exact pinned preflight %#v", trace, wantTrace)
	}
	if afterState := sqliteRelationDumpState(t, database); afterState != beforeState {
		t.Fatalf("generated-column preflight changed schema/revision\nbefore:\n%s\nafter:\n%s", beforeState, afterState)
	}
	if sqliteRelationRevision(t, database) != 0 || sqliteRelationRecorderCount(t, database, "blog", "generated") != 0 {
		t.Fatal("generated-column preflight changed revision or recorder")
	}
}

func TestSQLiteRelationCandidatePreBeginFaultsReleaseSingleConnectionWithoutMutation(t *testing.T) {
	stages := []faultStage{faultStagePragmaEnable, faultStagePragmaRead, faultStageBegin}
	for _, stage := range stages {
		t.Run(string(stage), func(t *testing.T) {
			database, backend, _ := sqliteRelationOpenCandidate(t)
			defer database.Close()
			before := sqliteRelationDumpState(t, database)
			cause := fmt.Errorf("%s resource sentinel", stage)
			backend.faults = faultNewPlan(stage, cause)
			intent := relationBackendArticleCreateIntent()
			_, err := relationBackendOpenStep(
				context.Background(), backend,
				relationBackendTransition{App: intent.App, Name: intent.Name, Direction: relationBackendApply, ToRevision: 1},
				intent,
			)
			if !errors.Is(err, cause) {
				t.Fatalf("pre-BEGIN fault error = %v, want cause %v", err, cause)
			}
			if backend.beginCalls != 0 || backend.rollbackCalls != 0 || backend.commitCalls != 0 {
				t.Fatalf("pre-BEGIN lifecycle begin=%d rollback=%d commit=%d, want all zero", backend.beginCalls, backend.rollbackCalls, backend.commitCalls)
			}
			if after := sqliteRelationDumpState(t, database); after != before {
				t.Fatalf("pre-BEGIN fault changed durable state\nbefore:\n%s\nafter:\n%s", before, after)
			}
			probeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var revision int64
			if err := database.QueryRowContext(
				probeCtx, `SELECT "revision" FROM "`+sqliteRelationRevisionTable+`" WHERE "singleton" = 1`,
			).Scan(&revision); err != nil {
				t.Fatalf("single-connection probe after pre-BEGIN fault: %v", err)
			}
			if revision != 0 || database.Stats().InUse != 0 {
				t.Fatalf("post-fault revision=%d in_use=%d, want 0/0", revision, database.Stats().InUse)
			}
		})
	}
}

func TestSQLiteRelationCandidateDirectSessionRejectsCanceledAndForgedTransitionBeforePin(t *testing.T) {
	tests := []struct {
		name       string
		context    func() (context.Context, context.CancelFunc)
		transition func(relationBackendStepIntent) relationBackendTransition
		want       error
	}{
		{
			name: "canceled",
			context: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, func() {}
			},
			transition: func(intent relationBackendStepIntent) relationBackendTransition {
				return relationBackendTransition{App: intent.App, Name: intent.Name, Direction: relationBackendApply, ToRevision: 1}
			},
			want: context.Canceled,
		},
		{
			name: "forged direction",
			context: func() (context.Context, context.CancelFunc) {
				return context.WithCancel(context.Background())
			},
			transition: func(intent relationBackendStepIntent) relationBackendTransition {
				return relationBackendTransition{App: intent.App, Name: intent.Name, Direction: 9, ToRevision: 1}
			},
			want: relationBackendErrIntent,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database, backend, _ := sqliteRelationOpenCandidate(t)
			defer database.Close()
			ctx, cancel := test.context()
			defer cancel()
			intent := relationBackendArticleCreateIntent()
			session := &sqliteRelationSession{backend: backend}
			_, err := session.BeginRelationFencedMigration(ctx, test.transition(intent), intent)
			if !errors.Is(err, test.want) {
				t.Fatalf("direct relation begin error = %v, want %v", err, test.want)
			}
			if trace := backend.sqliteRelationTraceSnapshot(); len(trace) != 0 || backend.beginCalls != 0 || database.Stats().InUse != 0 {
				t.Fatalf("direct rejected begin trace=%#v begin=%d in_use=%d, want zero pinned I/O", trace, backend.beginCalls, database.Stats().InUse)
			}
		})
	}
}

func TestSQLiteRelationCandidateCanceledPostBeginUsesDetachedBoundedRollback(t *testing.T) {
	database, backend, _ := sqliteRelationOpenCandidate(t)
	defer database.Close()
	rollbackCause := errors.New("rollback observation sentinel")
	backend.faults = faultNewPlan("", nil)
	backend.faults.rollbackCause = rollbackCause
	intent := relationBackendArticleCreateIntent()
	ctx, cancel := context.WithCancel(context.Background())
	opened, err := relationBackendOpenStep(
		ctx, backend,
		relationBackendTransition{App: intent.App, Name: intent.Name, Direction: relationBackendApply, ToRevision: 1},
		intent,
	)
	if err != nil {
		cancel()
		t.Fatalf("open post-BEGIN cancellation fixture: %v", err)
	}
	cancel()
	applyErr := opened.Transaction.ApplyRelationChange(ctx, intent.Changes[0])
	if !errors.Is(applyErr, context.Canceled) {
		t.Fatalf("canceled ApplyRelationChange error = %v, want context canceled", applyErr)
	}
	rollbackErr := opened.Transaction.RollbackRelation(ctx)
	joined := errors.Join(applyErr, rollbackErr, opened.Session.Close(context.Background()))
	if !errors.Is(joined, context.Canceled) || !errors.Is(joined, rollbackCause) {
		t.Fatalf("canceled cleanup error = %v, want context and rollback causes", joined)
	}
	if backend.beginCalls != 1 || backend.rollbackCalls != 1 || backend.commitCalls != 0 {
		t.Fatalf("canceled lifecycle begin=%d rollback=%d commit=%d", backend.beginCalls, backend.rollbackCalls, backend.commitCalls)
	}
	probeCtx, probeCancel := context.WithTimeout(context.Background(), time.Second)
	defer probeCancel()
	var revision int64
	if err := database.QueryRowContext(
		probeCtx, `SELECT "revision" FROM "`+sqliteRelationRevisionTable+`" WHERE "singleton" = 1`,
	).Scan(&revision); err != nil {
		t.Fatalf("reuse single connection after canceled rollback: %v", err)
	}
	if revision != 0 || sqliteRelationTableExistsFromDatabase(t, database, "author") ||
		sqliteRelationTableExistsFromDatabase(t, database, "article") || database.Stats().InUse != 0 {
		t.Fatalf("canceled rollback durable state revision=%d author=%t article=%t in_use=%d",
			revision,
			sqliteRelationTableExistsFromDatabase(t, database, "author"),
			sqliteRelationTableExistsFromDatabase(t, database, "article"),
			database.Stats().InUse,
		)
	}
}

func TestSQLiteRelationCandidateRawBeginAndRollbackFailuresDiscardPinnedConnection(t *testing.T) {
	t.Run("real busy begin failure", func(t *testing.T) {
		database, backend, _ := sqliteRelationOpenCandidate(t)
		defer database.Close()
		database.SetMaxOpenConns(2)
		blocker, err := database.Conn(context.Background())
		if err != nil {
			t.Fatalf("pin blocking connection: %v", err)
		}
		if _, err := blocker.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
			_ = blocker.Close()
			t.Fatalf("begin blocking transaction: %v", err)
		}
		intent := relationBackendArticleCreateIntent()
		_, err = relationBackendOpenStep(
			context.Background(), backend,
			relationBackendTransition{App: intent.App, Name: intent.Name, Direction: relationBackendApply, ToRevision: 1},
			intent,
		)
		if err == nil || backend.discardCalls != 1 {
			t.Fatalf("busy begin = error:%v discard:%d, want failure and confirmed discard", err, backend.discardCalls)
		}
		if _, rollbackErr := blocker.ExecContext(context.Background(), `ROLLBACK`); rollbackErr != nil {
			t.Fatalf("release blocking transaction: %v", rollbackErr)
		}
		if err := blocker.Close(); err != nil {
			t.Fatalf("close blocking connection: %v", err)
		}
		probeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		var revision int64
		if err := database.QueryRowContext(
			probeCtx,
			`SELECT "revision" FROM `+sqliteRelationQualifiedMain(sqliteRelationRevisionTable)+` WHERE "singleton" = 1`,
		).Scan(&revision); err != nil || revision != 0 {
			t.Fatalf("probe after busy discard = revision:%d error:%v", revision, err)
		}
	})

	t.Run("real rollback SQL failure", func(t *testing.T) {
		database, backend, _ := sqliteRelationOpenCandidate(t)
		defer database.Close()
		intent := relationBackendArticleCreateIntent()
		opened, err := relationBackendOpenStep(
			context.Background(), backend,
			relationBackendTransition{App: intent.App, Name: intent.Name, Direction: relationBackendApply, ToRevision: 1},
			intent,
		)
		if err != nil {
			t.Fatalf("open rollback-failure fixture: %v", err)
		}
		transaction := opened.Transaction.(*sqliteRelationTransaction)
		if err := transaction.ApplyRelationChange(context.Background(), intent.Changes[0]); err != nil {
			t.Fatalf("apply before external commit: %v", err)
		}
		if _, err := transaction.connection.ExecContext(context.Background(), `COMMIT`); err != nil {
			t.Fatalf("force external durability before rollback: %v", err)
		}
		rollbackErr := transaction.RollbackRelation(context.Background())
		if rollbackErr == nil || backend.discardCalls != 1 {
			t.Fatalf("real rollback SQL failure = error:%v discard:%d, want failure and confirmed discard", rollbackErr, backend.discardCalls)
		}
		if err := opened.Session.Close(context.Background()); err != nil {
			t.Fatalf("close rollback-failure session: %v", err)
		}
		probeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if !sqliteRelationTableExistsFromDatabase(t, database, "author") {
			t.Fatal("external commit durability was not observable after discarded connection")
		}
		var revision int64
		if err := database.QueryRowContext(
			probeCtx,
			`SELECT "revision" FROM `+sqliteRelationQualifiedMain(sqliteRelationRevisionTable)+` WHERE "singleton" = 1`,
		).Scan(&revision); err != nil || revision != 1 || database.Stats().InUse != 0 {
			t.Fatalf("probe after rollback failure = revision:%d in_use:%d error:%v", revision, database.Stats().InUse, err)
		}
	})
}

func TestSQLiteRelationCandidateCommittedCloseFailurePreservesOutcomeAndDiscardsConnection(t *testing.T) {
	database, backend, _ := sqliteRelationOpenCandidate(t)
	defer database.Close()
	closeCause := errors.New("close connection observation sentinel")
	backend.closeConnection = func(*sql.Conn) error { return closeCause }
	intent := relationBackendArticleCreateIntent()
	result, err := faultExecutePlan(context.Background(), backend, []faultExecutorStep{{
		Transition: relationBackendTransition{
			App: intent.App, Name: intent.Name, Direction: relationBackendApply,
			FromRevision: 0, ToRevision: 1,
		},
		Intent: intent,
	}})
	if !errors.Is(err, closeCause) {
		t.Fatalf("committed close failure error=%v, want close cause", err)
	}
	if result.ConfirmedSteps != 1 || result.Outcome != migrationbackend.CommitCommitted {
		t.Fatalf("committed close failure result=%+v, want confirmed committed outcome", result)
	}
	if backend.commitCalls != 1 || backend.rollbackCalls != 0 || backend.discardCalls != 1 {
		t.Fatalf(
			"committed close lifecycle commit=%d rollback=%d discard=%d",
			backend.commitCalls,
			backend.rollbackCalls,
			backend.discardCalls,
		)
	}
	if database.Stats().InUse != 0 {
		t.Fatalf("committed close failure retained %d in-use connection(s)", database.Stats().InUse)
	}
	if sqliteRelationRevision(t, database) != 1 ||
		sqliteRelationRecorderCount(t, database, intent.App, intent.Name) != 1 ||
		!sqliteRelationTableExistsFromDatabase(t, database, "author") ||
		!sqliteRelationTableExistsFromDatabase(t, database, "article") {
		t.Fatal("committed close failure lost durable successor state")
	}
}

func TestSQLiteRelationCandidateRawCommitErrorIsUnknownDiscardsAndNeverRetries(t *testing.T) {
	database, backend, _ := sqliteRelationOpenCandidate(t)
	defer database.Close()
	database.SetMaxOpenConns(1)
	commitCause := errors.New("raw COMMIT observation sentinel")
	backend.commitConnection = func(context.Context, *sql.Conn) (sql.Result, error) {
		return nil, commitCause
	}
	intent := relationBackendArticleCreateIntent()
	tail := relationBackendStepIntent{App: "blog", Name: "tail", Changes: []relationBackendChange{{
		Kind: relationBackendCreateModel,
		After: relationBackendModel{
			Table: "tail_table",
			Columns: []relationBackendColumn{{
				Name: "id", Type: "INTEGER", NotNull: true, PrimaryKey: true, AutoIncrement: true, Position: 1,
			}},
		},
	}}}
	result, err := faultExecutePlan(context.Background(), backend, []faultExecutorStep{
		{
			Transition: relationBackendTransition{
				App: intent.App, Name: intent.Name, Direction: relationBackendApply,
				FromRevision: 0, ToRevision: 1,
			},
			Intent: intent,
		},
		{
			Transition: relationBackendTransition{
				App: tail.App, Name: tail.Name, Direction: relationBackendApply,
				FromRevision: 1, ToRevision: 2,
			},
			Intent: tail,
		},
	})
	if !errors.Is(err, commitCause) {
		t.Fatalf("raw COMMIT error=%v, want cause identity", err)
	}
	var failure *faultCandidateError
	if !errors.As(err, &failure) || failure.Code != "commit_outcome_unknown" || failure.Reason != "unknown" {
		t.Fatalf("raw COMMIT structured error=%#v, want unknown outcome", err)
	}
	if result.Outcome != migrationbackend.CommitUnknown || result.ConfirmedSteps != 0 ||
		len(result.Attempts) != 2 || result.Attempts[0] != 1 || result.Attempts[1] != 0 {
		t.Fatalf("raw COMMIT result=%+v, want unknown/no retry/no tail", result)
	}
	if backend.commitCalls != 1 || backend.rollbackCalls != 0 || backend.discardCalls != 1 || database.Stats().InUse != 0 {
		t.Fatalf(
			"raw COMMIT lifecycle commit=%d rollback=%d discard=%d in_use=%d",
			backend.commitCalls,
			backend.rollbackCalls,
			backend.discardCalls,
			database.Stats().InUse,
		)
	}

	probeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var revision int64
	if err := database.QueryRowContext(
		probeCtx,
		`SELECT "revision" FROM `+sqliteRelationQualifiedMain(sqliteRelationRevisionTable)+` WHERE "singleton" = 1`,
	).Scan(&revision); err != nil {
		t.Fatalf("single-connection probe after raw COMMIT discard: %v", err)
	}
	if revision != 0 || sqliteRelationTableExistsFromDatabase(t, database, "author") ||
		sqliteRelationTableExistsFromDatabase(t, database, "article") ||
		sqliteRelationTableExistsFromDatabase(t, database, "tail_table") ||
		sqliteRelationRecorderCount(t, database, intent.App, intent.Name) != 0 {
		t.Fatalf(
			"raw COMMIT nondurable successor revision=%d author=%t article=%t tail=%t recorder=%d",
			revision,
			sqliteRelationTableExistsFromDatabase(t, database, "author"),
			sqliteRelationTableExistsFromDatabase(t, database, "article"),
			sqliteRelationTableExistsFromDatabase(t, database, "tail_table"),
			sqliteRelationRecorderCount(t, database, intent.App, intent.Name),
		)
	}
}

func TestSQLiteRelationCandidatePhysicalAndPlannedEdgesCannotCloseCycle(t *testing.T) {
	database, backend, _ := sqliteRelationOpenCandidate(t)
	defer database.Close()
	left := relationBackendModel{
		Table: "left_model",
		Columns: []relationBackendColumn{
			{Name: "id", Type: "INTEGER", NotNull: true, PrimaryKey: true, AutoIncrement: true, Position: 1},
		},
		Relations: []relationBackendRelation{{
			Name: "right", Column: "right_id", TargetTable: "right_model", TargetColumn: "id",
			Nullable: true, OnDelete: relationBackendSetNull, Position: 2,
		}},
	}
	rightBefore := relationBackendModel{
		Table: "right_model",
		Columns: []relationBackendColumn{
			{Name: "id", Type: "INTEGER", NotNull: true, PrimaryKey: true, AutoIncrement: true, Position: 1},
		},
	}
	for _, model := range []relationBackendModel{rightBefore, left} {
		statement, err := sqliteRelationCompileCreateTable(model, model.Table)
		if err != nil {
			t.Fatalf("compile physical cycle fixture %q: %v", model.Table, err)
		}
		if _, err := database.ExecContext(context.Background(), statement); err != nil {
			t.Fatalf("create physical cycle fixture %q: %v", model.Table, err)
		}
	}
	beforeState := sqliteRelationDumpState(t, database)
	leftRelation := relationBackendRelation{
		Name: "left", Column: "left_id", TargetTable: "left_model", TargetColumn: "id",
		Nullable: true, OnDelete: relationBackendSetNull, Position: 2,
	}
	rightAfter := rightBefore.relationBackendClone()
	rightAfter.Relations = []relationBackendRelation{leftRelation}
	intent := relationBackendStepIntent{
		App: "blog", Name: "physical_cycle",
		Changes: []relationBackendChange{{
			Kind: relationBackendAddField, Before: rightBefore, After: rightAfter, Relation: leftRelation,
		}},
	}
	_, err := faultExecutePlan(context.Background(), backend, []faultExecutorStep{{
		Transition: relationBackendTransition{App: intent.App, Name: intent.Name, Direction: relationBackendApply, ToRevision: 1},
		Intent:     intent,
	}})
	if !errors.Is(err, relationBackendErrCycle) {
		t.Fatalf("physical+planned cycle error = %v, want cycle", err)
	}
	if afterState := sqliteRelationDumpState(t, database); afterState != beforeState {
		t.Fatalf("physical cycle preflight mutated state\nbefore:\n%s\nafter:\n%s", beforeState, afterState)
	}
	if backend.beginCalls != 1 || backend.rollbackCalls != 1 || backend.commitCalls != 0 ||
		sqliteRelationRevision(t, database) != 0 || sqliteRelationColumnExists(t, database, "right_model", "left_id") {
		t.Fatalf(
			"physical cycle lifecycle begin=%d rollback=%d commit=%d revision=%d left_column=%t",
			backend.beginCalls, backend.rollbackCalls, backend.commitCalls,
			sqliteRelationRevision(t, database), sqliteRelationColumnExists(t, database, "right_model", "left_id"),
		)
	}
}

func TestSQLiteRelationCandidateSameTableCreateThenAddUsesOrderedVirtualStateAndFinalShape(t *testing.T) {
	ctx := context.Background()
	articleBefore := relationBackendModel{
		Table: "article",
		Columns: []relationBackendColumn{
			{Name: "id", Type: "INTEGER", NotNull: true, PrimaryKey: true, AutoIncrement: true, Position: 1},
			{Name: "title", Type: "VARCHAR", MaxLength: 200, NotNull: true, Position: 2},
		},
		Relations: []relationBackendRelation{},
	}
	authorRelation := relationBackendRelation{
		Name: "author", Column: "author_id", TargetTable: "author", TargetColumn: "id",
		OnDelete: relationBackendProtect, Position: 3,
	}
	articleAfter := articleBefore.relationBackendClone()
	articleAfter.Relations = []relationBackendRelation{authorRelation}
	addBefore := articleBefore.relationBackendClone()
	addBefore.Relations = nil
	intent := relationBackendStepIntent{
		App: "blog", Name: "same_table_chain",
		Changes: []relationBackendChange{
			{Kind: relationBackendCreateModel, After: relationBackendAuthorModel()},
			{Kind: relationBackendCreateModel, After: articleBefore},
			{Kind: relationBackendAddField, Before: addBefore, After: articleAfter, Relation: authorRelation},
		},
	}
	if err := relationBackendValidateIntent(intent); err != nil {
		t.Fatalf("same-table ordered intent validation: %v", err)
	}
	database, backend, _ := sqliteRelationOpenCandidate(t)
	defer database.Close()
	opened, err := relationBackendOpenStep(
		ctx, backend,
		relationBackendTransition{App: intent.App, Name: intent.Name, Direction: relationBackendApply, ToRevision: 1},
		intent,
	)
	if err != nil {
		t.Fatalf("open same-table ordered step: %v", err)
	}
	transaction := opened.Transaction.(*sqliteRelationTransaction)
	if err := transaction.ApplyRelationChange(ctx, intent.Changes[0]); err != nil {
		t.Fatalf("create planned target: %v", err)
	}
	if err := transaction.ApplyRelationChange(ctx, intent.Changes[1]); err != nil {
		t.Fatalf("create planned source: %v", err)
	}
	if err := sqliteRelationAssertModelShape(ctx, transaction.connection, articleBefore); err != nil {
		t.Fatalf("intermediate same-table source shape: %v", err)
	}
	if err := transaction.ApplyRelationChange(ctx, intent.Changes[2]); err != nil {
		t.Fatalf("add relation to planned source: %v", err)
	}
	if err := transaction.RecordRelationTransition(ctx); err != nil {
		t.Fatalf("record same-table final state: %v", err)
	}
	outcome, err := transaction.CommitRelationFenced(ctx)
	if err != nil || outcome.Durability != migrationbackend.CommitCommitted {
		t.Fatalf("commit same-table final state outcome=%+v error=%v", outcome, err)
	}
	if err := opened.Session.Close(ctx); err != nil {
		t.Fatalf("close same-table session: %v", err)
	}
	foreignKeys := sqliteRelationForeignKeysFromDatabase(t, database, "article")
	if len(foreignKeys) != 1 || foreignKeys[0].SourceColumn != "author_id" ||
		foreignKeys[0].TargetTable != "author" || sqliteRelationRevision(t, database) != 1 {
		t.Fatalf("same-table final foreign keys=%#v revision=%d", foreignKeys, sqliteRelationRevision(t, database))
	}

	t.Run("created source remake temp collision rejects before first DDL", func(t *testing.T) {
		database, backend, _ := sqliteRelationOpenCandidate(t)
		defer database.Close()
		chained := intent.relationBackendClone()
		chained.Name = "same_table_remake_collision"
		chained.Changes = append(chained.Changes, relationBackendChange{
			Kind: relationBackendRemoveField, Before: articleAfter, After: addBefore, Relation: authorRelation,
		})
		collision := sqliteRelationTemporaryTable(chained.Changes[3])
		sqliteRelationExec(t, database, `CREATE TABLE `+sqliteRelationQuoteIdentifier(collision)+` ("id" INTEGER PRIMARY KEY)`)
		before := sqliteRelationDumpState(t, database)
		_, err := faultExecutePlan(ctx, backend, []faultExecutorStep{{
			Transition: relationBackendTransition{
				App: chained.App, Name: chained.Name, Direction: relationBackendApply,
				FromRevision: 0, ToRevision: 1,
			},
			Intent: chained,
		}})
		if !errors.Is(err, sqliteRelationErrTempCollision) {
			t.Fatalf("created-source remake collision error=%v, want temp collision", err)
		}
		wantTrace := []string{"PRAGMA foreign_keys = ON", "PRAGMA foreign_keys", "BEGIN IMMEDIATE"}
		if trace := backend.sqliteRelationTraceSnapshot(); !relationBackendStringSlicesEqual(trace, wantTrace) {
			t.Fatalf("created-source remake collision trace=%#v, want preflight only %#v", trace, wantTrace)
		}
		if after := sqliteRelationDumpState(t, database); after != before {
			t.Fatalf("created-source remake collision changed durable state\nbefore:\n%s\nafter:\n%s", before, after)
		}
		if sqliteRelationRevision(t, database) != 0 ||
			sqliteRelationTableExistsFromDatabase(t, database, "author") ||
			sqliteRelationTableExistsFromDatabase(t, database, "article") {
			t.Fatal("created-source remake collision crossed revision or first-DDL boundary")
		}
	})

	t.Run("later_target_still_rejected", func(t *testing.T) {
		later := intent.relationBackendClone()
		later.Changes[0], later.Changes[1], later.Changes[2] = later.Changes[1], later.Changes[2], later.Changes[0]
		later.Name = "later_target"
		database, backend, _ := sqliteRelationOpenCandidate(t)
		defer database.Close()
		_, err := relationBackendOpenStep(
			ctx, backend,
			relationBackendTransition{App: later.App, Name: later.Name, Direction: relationBackendApply, ToRevision: 1},
			later,
		)
		if !errors.Is(err, sqliteRelationErrDrift) {
			t.Fatalf("later planned target error = %v, want drift", err)
		}
		if sqliteRelationRevision(t, database) != 0 || sqliteRelationTableExistsFromDatabase(t, database, "article") {
			t.Fatal("later planned target rejection changed durable state")
		}
	})
}

func TestSQLiteRelationCandidateClosedCreateShapeRejectsDecoysAndUsesPKOrdinalOne(t *testing.T) {
	t.Run("linear_mixed_inline_table_matcher_accepts_large_relation_set", func(t *testing.T) {
		const relationCount = 1024
		model := relationBackendModel{
			Table: "large_relation_model",
			Columns: []relationBackendColumn{{
				Name: "id", Type: "INTEGER", NotNull: true, PrimaryKey: true, AutoIncrement: true, Position: 1,
			}},
			Relations: make([]relationBackendRelation, relationCount),
		}
		for index := range model.Relations {
			model.Relations[index] = relationBackendRelation{
				Name: fmt.Sprintf("target_%04d", index), Column: fmt.Sprintf("target_%04d_id", index),
				TargetTable: "author", TargetColumn: "id", Nullable: index%3 == 0,
				OnDelete: relationBackendProtect, Position: index + 2,
			}
		}
		statement := sqliteRelationCompileClosedCreateTable(
			model,
			model.Table,
			func(relation relationBackendRelation) bool { return relation.Position%2 == 0 },
		)
		if !sqliteRelationCreateSQLIsExact(statement, model.Table, model) {
			t.Fatalf("linear matcher rejected %d-relation mixed inline/table shape", relationCount)
		}
		corrupted := strings.Replace(statement, ` ON DELETE NO ACTION`, ` ON DELETE CASCADE`, 1)
		if sqliteRelationCreateSQLIsExact(corrupted, model.Table, model) {
			t.Fatal("linear matcher accepted a large-shape delete-policy mutation")
		}
	})

	t.Run("primary_key_ordinal_is_one_even_when_second_column", func(t *testing.T) {
		database, _, _ := sqliteRelationOpenCandidate(t)
		defer database.Close()
		model := relationBackendModel{
			Table: "second_pk",
			Columns: []relationBackendColumn{
				{Name: "label", Type: "VARCHAR", MaxLength: 200, NotNull: true, Position: 1},
				{Name: "id", Type: "INTEGER", NotNull: true, PrimaryKey: true, AutoIncrement: true, Position: 2},
			},
		}
		statement, err := sqliteRelationCompileCreateTable(model, model.Table)
		if err != nil {
			t.Fatalf("compile second-position primary key: %v", err)
		}
		sqliteRelationExec(t, database, statement)
		connection, err := database.Conn(context.Background())
		if err != nil {
			t.Fatalf("pin exact-shape connection: %v", err)
		}
		defer connection.Close()
		if err := sqliteRelationAssertModelShape(context.Background(), connection, model); err != nil {
			t.Fatalf("second-position declared primary key shape: %v", err)
		}
	})

	targetCases := []struct {
		name      string
		targetSQL string
	}{
		{
			name:      "autoincrement_string_decoy",
			targetSQL: `CREATE TABLE "author" ("id" INTEGER NOT NULL PRIMARY KEY, "note" VARCHAR(200) DEFAULT 'AUTOINCREMENT')`,
		},
		{
			name:      "autoincrement_comment_is_not_closed_shape",
			targetSQL: `CREATE TABLE "author" ("id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT /* unmodeled */)`,
		},
	}
	for _, test := range targetCases {
		t.Run(test.name, func(t *testing.T) {
			database, backend, _ := sqliteRelationOpenCandidate(t)
			defer database.Close()
			sqliteRelationExec(t, database, test.targetSQL)
			intent := relationBackendStepIntent{App: "blog", Name: test.name, Changes: []relationBackendChange{{
				Kind: relationBackendCreateModel, After: relationBackendArticleModel(false),
			}}}
			_, err := relationBackendOpenStep(
				context.Background(), backend,
				relationBackendTransition{App: intent.App, Name: intent.Name, Direction: relationBackendApply, ToRevision: 1},
				intent,
			)
			if !errors.Is(err, sqliteRelationErrDrift) {
				t.Fatalf("target CREATE-shape error = %v, want drift", err)
			}
			if sqliteRelationRevision(t, database) != 0 || sqliteRelationTableExistsFromDatabase(t, database, "article") {
				t.Fatal("target CREATE-shape rejection changed durable state")
			}
		})
	}

	model := relationBackendArticleModel(false)
	shapeCases := []struct {
		name       string
		articleSQL string
	}{
		{
			name: "check", articleSQL: `CREATE TABLE "article" (` +
				`"id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, "title" VARCHAR(200) NOT NULL CHECK (length("title") > 0), ` +
				`"author_id" INTEGER NOT NULL, FOREIGN KEY ("author_id") REFERENCES "author" ("id") ON DELETE NO ACTION)`,
		},
		{
			name: "collate", articleSQL: `CREATE TABLE "article" (` +
				`"id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, "title" VARCHAR(200) NOT NULL COLLATE NOCASE, ` +
				`"author_id" INTEGER NOT NULL, FOREIGN KEY ("author_id") REFERENCES "author" ("id") ON DELETE NO ACTION)`,
		},
		{
			name: "deferrable", articleSQL: `CREATE TABLE "article" (` +
				`"id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, "title" VARCHAR(200) NOT NULL, "author_id" INTEGER NOT NULL, ` +
				`FOREIGN KEY ("author_id") REFERENCES "author" ("id") ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED)`,
		},
		{
			name: "unique", articleSQL: `CREATE TABLE "article" (` +
				`"id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, "title" VARCHAR(200) NOT NULL UNIQUE, "author_id" INTEGER NOT NULL, ` +
				`FOREIGN KEY ("author_id") REFERENCES "author" ("id") ON DELETE NO ACTION)`,
		},
	}
	for _, test := range shapeCases {
		t.Run(test.name, func(t *testing.T) {
			database, _, _ := sqliteRelationOpenCandidate(t)
			defer database.Close()
			sqliteRelationExec(t, database, `CREATE TABLE "author" ("id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, "name" VARCHAR(200) NOT NULL)`)
			sqliteRelationExec(t, database, test.articleSQL)
			connection, err := database.Conn(context.Background())
			if err != nil {
				t.Fatalf("pin malformed-shape connection: %v", err)
			}
			defer connection.Close()
			if err := sqliteRelationAssertModelShape(context.Background(), connection, model); !errors.Is(err, sqliteRelationErrDrift) {
				t.Fatalf("malformed CREATE shape error = %v, want drift", err)
			}
		})
	}
}

func sqliteRelationContainsForeignKey(
	foreignKeys []sqliteRelationPhysicalForeignKey,
	want sqliteRelationPhysicalForeignKey,
) bool {
	for _, foreignKey := range foreignKeys {
		if foreignKey == want {
			return true
		}
	}
	return false
}

func sqliteRelationContainsForeignKeyFolded(
	foreignKeys []sqliteRelationPhysicalForeignKey,
	want sqliteRelationPhysicalForeignKey,
) bool {
	for _, foreignKey := range foreignKeys {
		if sqliteRelationPhysicalForeignKeysEqual(foreignKey, want) {
			return true
		}
	}
	return false
}

func sqliteRelationTableExistsFromDatabase(t *testing.T, database *sql.DB, table string) bool {
	t.Helper()
	var count int
	if err := database.QueryRowContext(
		context.Background(),
		`SELECT COUNT(*) FROM "main"."sqlite_schema" WHERE "type" = 'table' AND "name" = ? COLLATE NOCASE`,
		table,
	).Scan(&count); err != nil {
		t.Fatalf("inspect SQLite table %q: %v", table, err)
	}
	return count == 1
}

func sqliteRelationOpenCandidate(t *testing.T) (*sql.DB, *sqliteRelationBackend, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "relation-candidate.sqlite3")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("sql.Open(): %v", err)
	}
	database.SetMaxOpenConns(1)
	if err := sqliteRelationInitialize(context.Background(), database); err != nil {
		_ = database.Close()
		t.Fatalf("sqliteRelationInitialize(): %v", err)
	}
	return database, &sqliteRelationBackend{database: database}, path
}

func sqliteRelationApplyInitialArticle(t *testing.T, backend *sqliteRelationBackend) {
	t.Helper()
	result, err := faultExecutePlan(context.Background(), backend, []faultExecutorStep{{
		Transition: relationBackendTransition{App: "blog", Name: "0001", Direction: relationBackendApply, FromRevision: 0, ToRevision: 1},
		Intent:     relationBackendArticleCreateIntent(),
	}})
	if err != nil {
		t.Fatalf("apply initial relation schema: %v", err)
	}
	if result.ConfirmedSteps != 1 {
		t.Fatalf("initial confirmed steps = %d, want 1", result.ConfirmedSteps)
	}
}

func sqliteRelationExec(t *testing.T, database *sql.DB, statement string) {
	t.Helper()
	if _, err := database.ExecContext(context.Background(), statement); err != nil {
		t.Fatalf("exec %q: %v", statement, err)
	}
}

func sqliteRelationRevision(t *testing.T, database *sql.DB) int64 {
	t.Helper()
	var revision int64
	if err := database.QueryRowContext(
		context.Background(),
		`SELECT "revision" FROM `+sqliteRelationQualifiedMain(sqliteRelationRevisionTable)+` WHERE "singleton" = 1`,
	).Scan(&revision); err != nil {
		t.Fatalf("read relation revision: %v", err)
	}
	return revision
}

func sqliteRelationRecorderCount(t *testing.T, database *sql.DB, app, name string) int {
	t.Helper()
	var count int
	if err := database.QueryRowContext(
		context.Background(),
		`SELECT COUNT(*) FROM `+sqliteRelationQualifiedMain(sqliteRelationRecorderTable)+` WHERE "app" = ? AND "name" = ?`,
		app,
		name,
	).Scan(&count); err != nil {
		t.Fatalf("read relation recorder count: %v", err)
	}
	return count
}

func sqliteRelationSequenceState(t *testing.T, database *sql.DB, table string) string {
	t.Helper()
	rows, err := database.QueryContext(
		context.Background(),
		`SELECT typeof("name"), hex(CAST("name" AS BLOB)), typeof("seq"), quote("seq") `+
			`FROM "main"."sqlite_sequence" WHERE "name" COLLATE NOCASE = ? ORDER BY "rowid"`,
		table,
	)
	if err != nil {
		t.Fatalf("read sqlite_sequence state for %q: %v", table, err)
	}
	defer rows.Close()
	var builder strings.Builder
	for rows.Next() {
		var nameType, nameHex, sequenceType, sequenceQuote string
		if err := rows.Scan(&nameType, &nameHex, &sequenceType, &sequenceQuote); err != nil {
			t.Fatalf("scan sqlite_sequence state for %q: %v", table, err)
		}
		fmt.Fprintf(&builder, "%s:%s:%s:%s\n", nameType, nameHex, sequenceType, sequenceQuote)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate sqlite_sequence state for %q: %v", table, err)
	}
	return builder.String()
}

func sqliteRelationColumnExists(t *testing.T, database *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := database.QueryContext(context.Background(), `PRAGMA main.table_xinfo(`+sqliteRelationQuoteIdentifier(table)+`)`)
	if err != nil {
		t.Fatalf("read table_xinfo(%q): %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey, hidden int
		var name, dataType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey, &hidden); err != nil {
			t.Fatalf("scan table_xinfo(%q): %v", table, err)
		}
		if name == column {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate table_xinfo(%q): %v", table, err)
	}
	return false
}

func sqliteRelationForeignKeysFromDatabase(t *testing.T, database *sql.DB, table string) []sqliteRelationPhysicalForeignKey {
	t.Helper()
	connection, err := database.Conn(context.Background())
	if err != nil {
		t.Fatalf("pin foreign-key inspection connection: %v", err)
	}
	defer connection.Close()
	foreignKeys, err := sqliteRelationReadForeignKeys(context.Background(), connection, table)
	if err != nil {
		t.Fatalf("sqliteRelationReadForeignKeys(%q): %v", table, err)
	}
	return foreignKeys
}

func sqliteRelationDumpState(t *testing.T, database *sql.DB) string {
	t.Helper()
	var revision int64
	if err := database.QueryRowContext(context.Background(),
		`SELECT "revision" FROM `+sqliteRelationQualifiedMain(sqliteRelationRevisionTable)+` WHERE "singleton" = 1`,
	).Scan(&revision); err != nil {
		t.Fatalf("dump revision: %v", err)
	}
	rows, err := database.QueryContext(context.Background(),
		`SELECT "type", "name", coalesce("sql", '') FROM "main"."sqlite_schema" ORDER BY "type", "name"`,
	)
	if err != nil {
		t.Fatalf("dump sqlite_schema: %v", err)
	}
	defer rows.Close()
	var builder strings.Builder
	fmt.Fprintf(&builder, "revision=%d\n", revision)
	for rows.Next() {
		var objectType, name, statement string
		if err := rows.Scan(&objectType, &name, &statement); err != nil {
			t.Fatalf("scan sqlite_schema dump: %v", err)
		}
		fmt.Fprintf(&builder, "%s:%s:%s\n", objectType, name, statement)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate sqlite_schema dump: %v", err)
	}
	return builder.String()
}
