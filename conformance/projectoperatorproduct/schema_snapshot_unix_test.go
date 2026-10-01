//go:build darwin || linux

package projectoperatorproduct_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	operatorMaximumSchemaRows  = 4096
	operatorMaximumSchemaBytes = 8 << 20
)

func operatorSQLiteSchemaSnapshot(t *testing.T, path string) []byte {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal("open external operator SQLite schema snapshot")
	}
	database.SetMaxOpenConns(1)
	defer database.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	snapshot, err := operatorReadSQLiteSchemaSnapshot(ctx, database)
	if err != nil {
		t.Fatal("read external operator SQLite schema snapshot")
	}
	return snapshot
}

func operatorReadSQLiteSchemaSnapshot(ctx context.Context, database *sql.DB) ([]byte, error) {
	if ctx == nil || database == nil {
		return nil, errors.New("SQLite schema snapshot input is nil")
	}
	rows, err := database.QueryContext(ctx, `
		SELECT "type", "name", "tbl_name", COALESCE("sql", '')
		FROM "sqlite_schema"
		WHERE "type" IN ('index', 'table', 'trigger', 'view')
		ORDER BY "type", "name", "tbl_name", COALESCE("sql", '')
	`)
	if err != nil {
		return nil, err
	}
	entries := make([][]string, 0, 32)
	for rows.Next() {
		var kind, name, table, definition string
		if err := rows.Scan(&kind, &name, &table, &definition); err != nil {
			_ = rows.Close()
			return nil, err
		}
		entries = append(entries, []string{"catalog", kind, name, table, definition})
		if len(entries) > operatorMaximumSchemaRows {
			_ = rows.Close()
			return nil, errors.New("SQLite schema snapshot exceeds its row limit")
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	for _, pragma := range []string{
		"application_id",
		"auto_vacuum",
		"encoding",
		"foreign_keys",
		"page_size",
		"schema_version",
		"user_version",
	} {
		var value string
		if err := database.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&value); err != nil {
			return nil, err
		}
		entries = append(entries, []string{"pragma", pragma, value})
	}
	return operatorCanonicalSchemaRows(entries)
}

func operatorPostgresSchemaSnapshot(t *testing.T, databaseURL, schema string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal("connect external operator PostgreSQL schema snapshot")
	}
	defer connection.Close(ctx)
	rows, err := connection.Query(ctx, `
		SELECT "kind", "relation", "position", "name", "definition", "flag", "extra"
		FROM (
			SELECT
				'relation'::text AS "kind",
				"c"."relname"::text AS "relation",
				''::text AS "position",
				''::text AS "name",
				"c"."relkind"::text AS "definition",
				"c"."relpersistence"::text AS "flag",
				("c"."relrowsecurity"::text || ':' || "c"."relforcerowsecurity"::text)::text AS "extra"
			FROM "pg_catalog"."pg_class" AS "c"
			JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "c"."relnamespace"
			WHERE "n"."nspname" = $1 AND "c"."relkind" IN ('r', 'p', 'S', 'v', 'm')

			UNION ALL

			SELECT
				'column'::text,
				"c"."relname"::text,
				"a"."attnum"::text,
				"a"."attname"::text,
				"pg_catalog"."format_type"("a"."atttypid", "a"."atttypmod")::text,
				"a"."attnotnull"::text,
				COALESCE("pg_catalog"."pg_get_expr"("ad"."adbin", "ad"."adrelid"), '')::text
			FROM "pg_catalog"."pg_attribute" AS "a"
			JOIN "pg_catalog"."pg_class" AS "c" ON "c"."oid" = "a"."attrelid"
			JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "c"."relnamespace"
			LEFT JOIN "pg_catalog"."pg_attrdef" AS "ad"
				ON "ad"."adrelid" = "a"."attrelid" AND "ad"."adnum" = "a"."attnum"
			WHERE "n"."nspname" = $1
				AND "c"."relkind" IN ('r', 'p', 'S', 'v', 'm')
				AND "a"."attnum" > 0
				AND NOT "a"."attisdropped"

			UNION ALL

			SELECT
				'constraint'::text,
				"c"."relname"::text,
				''::text,
				"constraint"."conname"::text,
				"constraint"."contype"::text,
				"constraint"."condeferrable"::text,
				"pg_catalog"."pg_get_constraintdef"("constraint"."oid", true)::text
			FROM "pg_catalog"."pg_constraint" AS "constraint"
			JOIN "pg_catalog"."pg_class" AS "c" ON "c"."oid" = "constraint"."conrelid"
			JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "c"."relnamespace"
			WHERE "n"."nspname" = $1

			UNION ALL

			SELECT
				'index'::text,
				"table"."relname"::text,
				''::text,
				"index"."relname"::text,
				''::text,
				''::text,
				"pg_catalog"."pg_get_indexdef"("mapping"."indexrelid")::text
			FROM "pg_catalog"."pg_index" AS "mapping"
			JOIN "pg_catalog"."pg_class" AS "table" ON "table"."oid" = "mapping"."indrelid"
			JOIN "pg_catalog"."pg_class" AS "index" ON "index"."oid" = "mapping"."indexrelid"
			JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "table"."relnamespace"
			WHERE "n"."nspname" = $1

			UNION ALL

			SELECT
				'trigger'::text,
				"c"."relname"::text,
				''::text,
				"trigger"."tgname"::text,
				''::text,
				"trigger"."tgenabled"::text,
				"pg_catalog"."pg_get_triggerdef"("trigger"."oid", true)::text
			FROM "pg_catalog"."pg_trigger" AS "trigger"
			JOIN "pg_catalog"."pg_class" AS "c" ON "c"."oid" = "trigger"."tgrelid"
			JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "c"."relnamespace"
			WHERE "n"."nspname" = $1 AND NOT "trigger"."tgisinternal"

			UNION ALL

			SELECT
				'policy'::text,
				"c"."relname"::text,
				''::text,
				"policy"."polname"::text,
				"policy"."polcmd"::text,
				"policy"."polpermissive"::text,
				(
					"policy"."polroles"::text || '|' ||
					COALESCE("pg_catalog"."pg_get_expr"("policy"."polqual", "policy"."polrelid"), '') || '|' ||
					COALESCE("pg_catalog"."pg_get_expr"("policy"."polwithcheck", "policy"."polrelid"), '')
				)::text
			FROM "pg_catalog"."pg_policy" AS "policy"
			JOIN "pg_catalog"."pg_class" AS "c" ON "c"."oid" = "policy"."polrelid"
			JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "c"."relnamespace"
			WHERE "n"."nspname" = $1

			UNION ALL

			SELECT
				'rule'::text,
				"c"."relname"::text,
				''::text,
				"rule"."rulename"::text,
				''::text,
				"rule"."ev_enabled"::text,
				"pg_catalog"."pg_get_ruledef"("rule"."oid", true)::text
			FROM "pg_catalog"."pg_rewrite" AS "rule"
			JOIN "pg_catalog"."pg_class" AS "c" ON "c"."oid" = "rule"."ev_class"
			JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "c"."relnamespace"
			WHERE "n"."nspname" = $1

			UNION ALL

			SELECT
				'sequence'::text,
				"c"."relname"::text,
				''::text,
				''::text,
				"pg_catalog"."format_type"("sequence"."seqtypid", NULL)::text,
				"sequence"."seqcycle"::text,
				(
					"sequence"."seqstart"::text || '|' ||
					"sequence"."seqincrement"::text || '|' ||
					"sequence"."seqmax"::text || '|' ||
					"sequence"."seqmin"::text || '|' ||
					"sequence"."seqcache"::text
				)::text
			FROM "pg_catalog"."pg_sequence" AS "sequence"
			JOIN "pg_catalog"."pg_class" AS "c" ON "c"."oid" = "sequence"."seqrelid"
			JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "c"."relnamespace"
			WHERE "n"."nspname" = $1

			UNION ALL

			SELECT
				'function'::text,
				''::text,
				''::text,
				"procedure"."proname"::text,
				"pg_catalog"."pg_get_function_identity_arguments"("procedure"."oid")::text,
				("procedure"."prokind"::text || ':' || "procedure"."provolatile"::text || ':' || "procedure"."prosecdef"::text)::text,
				"pg_catalog"."pg_get_functiondef"("procedure"."oid")::text
			FROM "pg_catalog"."pg_proc" AS "procedure"
			JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "procedure"."pronamespace"
			WHERE "n"."nspname" = $1 AND "procedure"."prokind" IN ('f', 'p', 'w')
		) AS "snapshot"
		ORDER BY "kind", "relation", "position", "name", "definition", "flag", "extra"
	`, schema)
	if err != nil {
		t.Fatal("query external operator PostgreSQL schema snapshot")
	}
	entries := make([][]string, 0, 64)
	for rows.Next() {
		entry := make([]string, 7)
		if err := rows.Scan(&entry[0], &entry[1], &entry[2], &entry[3], &entry[4], &entry[5], &entry[6]); err != nil {
			rows.Close()
			t.Fatal("scan external operator PostgreSQL schema snapshot")
		}
		entries = append(entries, entry)
		if len(entries) > operatorMaximumSchemaRows {
			rows.Close()
			t.Fatal("external operator PostgreSQL schema snapshot exceeds its row limit")
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal("finish external operator PostgreSQL schema snapshot")
	}
	rows.Close()
	snapshot, err := operatorCanonicalSchemaRows(entries)
	if err != nil {
		t.Fatal("canonicalize external operator PostgreSQL schema snapshot")
	}
	return snapshot
}

func operatorAssertPostgresSchemaSnapshotDetectsTriggerMutation(t *testing.T, databaseURL, schema string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal("connect PostgreSQL schema snapshot trigger regression")
	}
	defer connection.Close(ctx)
	prefix := pgx.Identifier{schema}.Sanitize() + "."
	if _, err := connection.Exec(ctx, `CREATE TABLE `+prefix+`"godj_schema_snapshot_probe" ("id" bigint PRIMARY KEY)`); err != nil {
		t.Fatal("create PostgreSQL schema snapshot probe table")
	}
	if _, err := connection.Exec(ctx, `
		CREATE FUNCTION `+prefix+`"godj_schema_snapshot_probe_function"()
		RETURNS trigger
		LANGUAGE plpgsql
		AS 'BEGIN RETURN NEW; END'
	`); err != nil {
		t.Fatal("create PostgreSQL schema snapshot probe function")
	}
	before := operatorPostgresSchemaSnapshot(t, databaseURL, schema)
	if _, err := connection.Exec(ctx, `
		CREATE TRIGGER "godj_schema_snapshot_probe_trigger"
		BEFORE INSERT ON `+prefix+`"godj_schema_snapshot_probe"
		FOR EACH ROW EXECUTE FUNCTION `+prefix+`"godj_schema_snapshot_probe_function"()
	`); err != nil {
		t.Fatal("create PostgreSQL schema snapshot probe trigger")
	}
	after := operatorPostgresSchemaSnapshot(t, databaseURL, schema)
	if bytes.Equal(before, after) {
		t.Fatal("PostgreSQL trigger mutation did not change canonical schema snapshot")
	}
	if _, err := connection.Exec(ctx, `DROP TABLE `+prefix+`"godj_schema_snapshot_probe" CASCADE`); err != nil {
		t.Fatal("drop PostgreSQL schema snapshot probe table")
	}
	if _, err := connection.Exec(ctx, `DROP FUNCTION `+prefix+`"godj_schema_snapshot_probe_function"()`); err != nil {
		t.Fatal("drop PostgreSQL schema snapshot probe function")
	}
}

func operatorCanonicalSchemaRows(rows [][]string) ([]byte, error) {
	framed := make([]string, len(rows))
	for index, row := range rows {
		var frame bytes.Buffer
		for _, field := range row {
			frame.WriteString(strconv.Itoa(len(field)))
			frame.WriteByte(':')
			frame.WriteString(field)
			frame.WriteByte(0)
			if frame.Len() > operatorMaximumSchemaBytes {
				return nil, errors.New("schema snapshot row exceeds its byte limit")
			}
		}
		framed[index] = frame.String()
	}
	sort.Strings(framed)
	var result bytes.Buffer
	for _, frame := range framed {
		result.WriteString(frame)
		result.WriteByte('\n')
		if result.Len() > operatorMaximumSchemaBytes {
			return nil, errors.New("schema snapshot exceeds its byte limit")
		}
	}
	return result.Bytes(), nil
}

func TestOperatorCanonicalSchemaRowsSortsAndFramesWithoutAmbiguity(t *testing.T) {
	first, err := operatorCanonicalSchemaRows([][]string{{"b", "c"}, {"a", "bc"}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := operatorCanonicalSchemaRows([][]string{{"a", "bc"}, {"b", "c"}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("canonical schema snapshot depends on input row order")
	}
	ambiguous, err := operatorCanonicalSchemaRows([][]string{{"ab", "c"}, {"a", "b", "c"}})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, ambiguous) {
		t.Fatal("canonical schema snapshot framing is ambiguous")
	}
}

func TestOperatorSQLiteSchemaSnapshotDetectsCatalogMutation(t *testing.T) {
	database, err := sql.Open("sqlite", filepathForSchemaTest(t))
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	defer database.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := database.ExecContext(ctx, `CREATE TABLE "article" ("id" INTEGER PRIMARY KEY, "title" TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	before, err := operatorReadSQLiteSchemaSnapshot(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `ALTER TABLE "article" ADD COLUMN "published" INTEGER NOT NULL DEFAULT 0`); err != nil {
		t.Fatal(err)
	}
	after, err := operatorReadSQLiteSchemaSnapshot(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(before, after) {
		t.Fatal("SQLite catalog mutation did not change canonical schema snapshot")
	}
}

func TestOperatorPostgresSchemaSnapshotDetectsTriggerMutation(t *testing.T) {
	databaseURL := operatorPostgresTestURL(t)
	schema, _ := operatorCreatePostgresSchema(t, databaseURL)
	operatorAssertPostgresSchemaSnapshotDetectsTriggerMutation(t, databaseURL, schema)
}

func filepathForSchemaTest(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "schema.sqlite3")
}
