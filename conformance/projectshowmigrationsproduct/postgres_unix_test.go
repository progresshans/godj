//go:build darwin || linux

package projectshowmigrationsproduct_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	externalStatusPostgresTestURLEnvironment  = "GODJ_TEST_POSTGRES_URL"
	externalStatusPostgresRequiredEnvironment = "GODJ_REQUIRE_POSTGRES"
)

var externalStatusPostgresSchemaSequence atomic.Uint64

type externalPostgresNamespace struct {
	oid   uint32
	owner string
	acl   string
}

type externalPostgresRelation struct {
	name        string
	kind        string
	persistence string
	options     string
}

type externalPostgresColumn struct {
	table      string
	ordinal    int
	name       string
	typeName   string
	notNull    bool
	identity   string
	generated  string
	defaultSQL string
	collation  string
}

type externalPostgresConstraint struct {
	table      string
	name       string
	kind       string
	definition string
	validated  bool
	deferrable bool
	deferred   bool
}

type externalPostgresIndex struct {
	table      string
	name       string
	definition string
	primary    bool
	unique     bool
	valid      bool
	ready      bool
	live       bool
}

type externalPostgresSequence struct {
	name        string
	typeName    string
	start       int64
	increment   int64
	minimum     int64
	maximum     int64
	cache       int64
	cycle       bool
	ownerTable  string
	ownerColumn string
	dependency  string
	last        int64
	called      bool
}

type externalPostgresTableCount struct {
	table string
	count int64
}

type externalPostgresRevisionRow struct {
	singleton   int64
	format      int64
	epoch       string
	revision    int64
	fingerprint string
}

type externalPostgresAuthorRow struct {
	id   int64
	name string
}

type externalPostgresArticleRow struct {
	id           int64
	title        string
	hasPublished bool
	published    bool
}

type externalPostgresSnapshot struct {
	namespace   externalPostgresNamespace
	relations   []externalPostgresRelation
	columns     []externalPostgresColumn
	constraints []externalPostgresConstraint
	indexes     []externalPostgresIndex
	sequences   []externalPostgresSequence
	counts      []externalPostgresTableCount
	history     []externalSQLiteHistoryRow
	revisions   []externalPostgresRevisionRow
	authors     []externalPostgresAuthorRow
	articles    []externalPostgresArticleRow
}

func TestGlobalShowMigrationsPostgresReadOnlyFreshPrefixRestart(t *testing.T) {
	databaseURL := externalStatusPostgresTestURL(t)
	project := newExternalStatusProject(t)

	t.Run("MIG-111_empty_catalog", func(t *testing.T) {
		schema := externalStatusCreatePostgresSchema(t, databaseURL)
		marker := project.postgresMarker(t, "mig-111-empty")
		sensitive := externalStatusPostgresSensitive(t, project, databaseURL, schema)
		before := externalStatusCapturePostgres(t, databaseURL, schema)
		externalStatusAssertPostgresEmpty(t, before)

		result := project.runShow(t, project.postgresEnvironment(t, databaseURL, schema, marker, "empty"))
		externalStatusAssertSuccess(t, result, externalStatusEmptyOutput, sensitive...)
		externalStatusAssertReadLifecycle(t, marker)
		after := externalStatusCapturePostgres(t, databaseURL, schema)
		externalStatusAssertPostgresUnchanged(t, before, after)
	})

	t.Run("MIG-112_fresh_unapplied", func(t *testing.T) {
		schema := externalStatusCreatePostgresSchema(t, databaseURL)
		marker := project.postgresMarker(t, "mig-112-fresh")
		sensitive := externalStatusPostgresSensitive(t, project, databaseURL, schema)
		before := externalStatusCapturePostgres(t, databaseURL, schema)
		externalStatusAssertPostgresEmpty(t, before)

		result := project.runShow(t, project.postgresEnvironment(t, databaseURL, schema, marker, "full"))
		externalStatusAssertSuccess(t, result, externalStatusFreshOutput, sensitive...)
		externalStatusAssertReadLifecycle(t, marker)
		after := externalStatusCapturePostgres(t, databaseURL, schema)
		externalStatusAssertPostgresUnchanged(t, before, after)
	})

	t.Run("MIG-113_applied_prefix", func(t *testing.T) {
		schema := externalStatusCreatePostgresSchema(t, databaseURL)
		marker := project.postgresMarker(t, "mig-113-prefix")
		sensitive := externalStatusPostgresSensitive(t, project, databaseURL, schema)
		seedEnvironment := project.postgresEnvironment(t, databaseURL, schema, marker, "prefix")
		externalStatusPostgresMigrateSetup(t, project, seedEnvironment, marker, sensitive)
		externalStatusSeedPostgresApplicationRows(t, databaseURL, schema)
		before := externalStatusCapturePostgres(t, databaseURL, schema)
		externalStatusAssertPostgresHistory(t, before, 2,
			externalSQLiteHistoryRow{app: "authors", name: "0001_author"},
			externalSQLiteHistoryRow{app: "blog", name: "0001_article"},
		)

		result := project.runShow(t, project.postgresEnvironment(t, databaseURL, schema, marker, "full"))
		externalStatusAssertSuccess(t, result, externalStatusPrefixOutput, sensitive...)
		externalStatusAssertReadLifecycle(t, marker)
		after := externalStatusCapturePostgres(t, databaseURL, schema)
		externalStatusAssertPostgresUnchanged(t, before, after)
	})

	t.Run("MIG-114_fully_applied_distinct_process_restart", func(t *testing.T) {
		schema := externalStatusCreatePostgresSchema(t, databaseURL)
		marker := project.postgresMarker(t, "mig-114-full-restart")
		sensitive := externalStatusPostgresSensitive(t, project, databaseURL, schema)
		environment := project.postgresEnvironment(t, databaseURL, schema, marker, "full")
		externalStatusPostgresMigrateSetup(t, project, environment, marker, sensitive)
		externalStatusSeedPostgresApplicationRows(t, databaseURL, schema)
		before := externalStatusCapturePostgres(t, databaseURL, schema)
		externalStatusAssertPostgresHistory(t, before, 3,
			externalSQLiteHistoryRow{app: "authors", name: "0001_author"},
			externalSQLiteHistoryRow{app: "blog", name: "0001_article"},
			externalSQLiteHistoryRow{app: "blog", name: "0002_publish"},
		)

		first := project.runShow(t, environment)
		externalStatusAssertSuccess(t, first, externalStatusFullOutput, sensitive...)
		firstPID := externalStatusAssertReadLifecycle(t, marker)
		externalStatusAssertPostgresUnchanged(t, before, externalStatusCapturePostgres(t, databaseURL, schema))

		externalStatusResetMarker(t, marker)
		second := project.runShow(t, environment)
		externalStatusAssertSuccess(t, second, externalStatusFullOutput, sensitive...)
		secondPID := externalStatusAssertReadLifecycle(t, marker)
		if first.stdout != second.stdout || firstPID == secondPID || firstPID == os.Getpid() || secondPID == os.Getpid() {
			t.Fatalf("PostgreSQL restart proof was not byte-identical, process-distinct, and external: first_pid=%d second_pid=%d", firstPID, secondPID)
		}
		externalStatusAssertPostgresUnchanged(t, before, externalStatusCapturePostgres(t, databaseURL, schema))
	})

	t.Run("MIG-116_unknown_record_visible", func(t *testing.T) {
		schema := externalStatusCreatePostgresSchema(t, databaseURL)
		marker := project.postgresMarker(t, "mig-116-unknown")
		sensitive := externalStatusPostgresSensitive(t, project, databaseURL, schema)
		seedEnvironment := project.postgresEnvironment(t, databaseURL, schema, marker, "unknown_seed")
		externalStatusPostgresMigrateSetup(t, project, seedEnvironment, marker, sensitive)
		externalStatusSeedPostgresApplicationRows(t, databaseURL, schema)
		before := externalStatusCapturePostgres(t, databaseURL, schema)
		externalStatusAssertPostgresHistory(t, before, 5,
			externalSQLiteHistoryRow{app: "authors", name: "0001_author"},
			externalSQLiteHistoryRow{app: "blog", name: "0000_removed"},
			externalSQLiteHistoryRow{app: "blog", name: "0001_article"},
			externalSQLiteHistoryRow{app: "blog", name: "9999_removed"},
			externalSQLiteHistoryRow{app: "legacy", name: "0001_gone"},
		)

		result := project.runShow(t, project.postgresEnvironment(t, databaseURL, schema, marker, "full"))
		externalStatusAssertSuccess(t, result, externalStatusUnknownOutput, sensitive...)
		externalStatusAssertReadLifecycle(t, marker)
		externalStatusAssertPostgresUnchanged(t, before, externalStatusCapturePostgres(t, databaseURL, schema))
	})

	t.Run("MIG-117_inconsistent_known_history", func(t *testing.T) {
		schema := externalStatusCreatePostgresSchema(t, databaseURL)
		marker := project.postgresMarker(t, "mig-117-inconsistent")
		sensitive := externalStatusPostgresSensitive(t, project, databaseURL, schema)
		environment := project.postgresEnvironment(t, databaseURL, schema, marker, "full")
		externalStatusPostgresMigrateSetup(t, project, environment, marker, sensitive)
		externalStatusSeedPostgresApplicationRows(t, databaseURL, schema)
		externalStatusInstallInconsistentPostgresHistory(t, databaseURL, schema)
		before := externalStatusCapturePostgres(t, databaseURL, schema)
		externalStatusAssertPostgresHistory(t, before, 3,
			externalSQLiteHistoryRow{app: "blog", name: "0001_article"},
			externalSQLiteHistoryRow{app: "blog", name: "0002_publish"},
		)

		result := project.runShow(t, environment)
		externalStatusAssertFailure(t, result, 1, "migration_history_error/inconsistent_applied_history\n", sensitive...)
		externalStatusAssertReadLifecycle(t, marker)
		externalStatusAssertPostgresUnchanged(t, before, externalStatusCapturePostgres(t, databaseURL, schema))
	})

	externalStatusAuditApplicationSources(t, project.repository, project.root)
	externalStatusAssertArtifactsRedacted(t, project.root, externalStatusPostgresSensitive(t, project, databaseURL, "")...)
}

func externalStatusPostgresTestURL(t *testing.T) string {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv(externalStatusPostgresTestURLEnvironment))
	if databaseURL != "" {
		return databaseURL
	}
	if os.Getenv(externalStatusPostgresRequiredEnvironment) == "1" {
		t.Fatalf("%s=1 requires %s", externalStatusPostgresRequiredEnvironment, externalStatusPostgresTestURLEnvironment)
	}
	t.Skip("GODJ_TEST_POSTGRES_URL is not configured; showmigrations PostgreSQL product E2E was not run")
	return ""
}

func externalStatusPostgresSensitive(t *testing.T, project *externalStatusProject, databaseURL, schema string) []string {
	t.Helper()
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal("parse showmigrations PostgreSQL URL: database URL is invalid")
	}
	values := []string{databaseURL, schema, project.secret}
	if len(config.Password) >= 4 {
		values = append(values, config.Password)
	}
	return values
}

func (project *externalStatusProject) postgresMarker(t *testing.T, name string) string {
	t.Helper()
	directory := project.universe + string(os.PathSeparator) + "state" + string(os.PathSeparator) + name
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	return directory + string(os.PathSeparator) + "backend-events.log"
}

func externalStatusCreatePostgresSchema(t *testing.T, databaseURL string) string {
	t.Helper()
	sequence := externalStatusPostgresSchemaSequence.Add(1)
	schema := fmt.Sprintf("godj_sm_%d_%d_%d", os.Getpid(), time.Now().UnixNano(), sequence)
	if len(schema) > 63 {
		t.Fatal("generated showmigrations PostgreSQL schema exceeds identifier limit")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect PostgreSQL schema owner: %v", externalStatusPostgresSafeError(err))
	}
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := connection.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("create PostgreSQL product schema: %v", externalStatusPostgresSafeError(err))
	}
	if err := connection.Close(ctx); err != nil {
		t.Fatalf("close PostgreSQL schema owner: %v", externalStatusPostgresSafeError(err))
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		cleanup, err := pgx.Connect(cleanupCtx, databaseURL)
		if err != nil {
			t.Errorf("connect PostgreSQL schema cleanup: %v", externalStatusPostgresSafeError(err))
			return
		}
		if _, err := cleanup.Exec(cleanupCtx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Errorf("drop PostgreSQL product schema: %v", externalStatusPostgresSafeError(err))
		}
		if err := cleanup.Close(cleanupCtx); err != nil {
			t.Errorf("close PostgreSQL schema cleanup: %v", externalStatusPostgresSafeError(err))
		}
	})
	return schema
}

func externalStatusCapturePostgres(t *testing.T, databaseURL, schema string) externalPostgresSnapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect PostgreSQL state inspector: %v", externalStatusPostgresSafeError(err))
	}
	defer func() {
		if err := connection.Close(ctx); err != nil {
			t.Errorf("close PostgreSQL state inspector: %v", externalStatusPostgresSafeError(err))
		}
	}()

	snapshot := externalPostgresSnapshot{}
	if err := connection.QueryRow(ctx, `SELECT "n"."oid", "owner"."rolname", COALESCE("n"."nspacl"::text, '')
		FROM "pg_catalog"."pg_namespace" AS "n"
		JOIN "pg_catalog"."pg_roles" AS "owner" ON "owner"."oid" = "n"."nspowner"
		WHERE "n"."nspname" = $1`, schema).Scan(&snapshot.namespace.oid, &snapshot.namespace.owner, &snapshot.namespace.acl); err != nil {
		t.Fatalf("inspect PostgreSQL namespace: %v", externalStatusPostgresSafeError(err))
	}

	rows, err := connection.Query(ctx, `SELECT "c"."relname", "c"."relkind"::text, "c"."relpersistence"::text,
		COALESCE("pg_catalog"."array_to_string"("c"."reloptions", ','), '')
		FROM "pg_catalog"."pg_class" AS "c"
		JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "c"."relnamespace"
		WHERE "n"."nspname" = $1 ORDER BY "c"."relkind", "c"."relname"`, schema)
	if err != nil {
		t.Fatalf("inspect PostgreSQL relations: %v", externalStatusPostgresSafeError(err))
	}
	for rows.Next() {
		var value externalPostgresRelation
		if err := rows.Scan(&value.name, &value.kind, &value.persistence, &value.options); err != nil {
			rows.Close()
			t.Fatalf("scan PostgreSQL relation: %v", externalStatusPostgresSafeError(err))
		}
		snapshot.relations = append(snapshot.relations, value)
	}
	externalStatusClosePostgresRows(t, rows, "relations")

	rows, err = connection.Query(ctx, `SELECT "c"."relname", "a"."attnum", "a"."attname",
		"pg_catalog"."format_type"("a"."atttypid", "a"."atttypmod"), "a"."attnotnull",
		"a"."attidentity"::text, "a"."attgenerated"::text,
		COALESCE("pg_catalog"."pg_get_expr"("d"."adbin", "d"."adrelid"), ''),
		CASE WHEN "a"."attcollation" = 0 THEN '' ELSE "collation_ns"."nspname" || '.' || "collation"."collname" END
		FROM "pg_catalog"."pg_class" AS "c"
		JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "c"."relnamespace"
		JOIN "pg_catalog"."pg_attribute" AS "a" ON "a"."attrelid" = "c"."oid"
		LEFT JOIN "pg_catalog"."pg_attrdef" AS "d" ON "d"."adrelid" = "a"."attrelid" AND "d"."adnum" = "a"."attnum"
		LEFT JOIN "pg_catalog"."pg_collation" AS "collation" ON "collation"."oid" = "a"."attcollation"
		LEFT JOIN "pg_catalog"."pg_namespace" AS "collation_ns" ON "collation_ns"."oid" = "collation"."collnamespace"
		WHERE "n"."nspname" = $1 AND "c"."relkind" IN ('r', 'p') AND "a"."attnum" > 0 AND NOT "a"."attisdropped"
		ORDER BY "c"."relname", "a"."attnum"`, schema)
	if err != nil {
		t.Fatalf("inspect PostgreSQL columns: %v", externalStatusPostgresSafeError(err))
	}
	for rows.Next() {
		var value externalPostgresColumn
		if err := rows.Scan(&value.table, &value.ordinal, &value.name, &value.typeName, &value.notNull,
			&value.identity, &value.generated, &value.defaultSQL, &value.collation); err != nil {
			rows.Close()
			t.Fatalf("scan PostgreSQL column: %v", externalStatusPostgresSafeError(err))
		}
		snapshot.columns = append(snapshot.columns, value)
	}
	externalStatusClosePostgresRows(t, rows, "columns")

	rows, err = connection.Query(ctx, `SELECT "table"."relname", "constraint"."conname", "constraint"."contype"::text,
		"pg_catalog"."pg_get_constraintdef"("constraint"."oid", true), "constraint"."convalidated",
		"constraint"."condeferrable", "constraint"."condeferred"
		FROM "pg_catalog"."pg_constraint" AS "constraint"
		JOIN "pg_catalog"."pg_class" AS "table" ON "table"."oid" = "constraint"."conrelid"
		JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "table"."relnamespace"
		WHERE "n"."nspname" = $1 ORDER BY "table"."relname", "constraint"."conname"`, schema)
	if err != nil {
		t.Fatalf("inspect PostgreSQL constraints: %v", externalStatusPostgresSafeError(err))
	}
	for rows.Next() {
		var value externalPostgresConstraint
		if err := rows.Scan(&value.table, &value.name, &value.kind, &value.definition, &value.validated, &value.deferrable, &value.deferred); err != nil {
			rows.Close()
			t.Fatalf("scan PostgreSQL constraint: %v", externalStatusPostgresSafeError(err))
		}
		snapshot.constraints = append(snapshot.constraints, value)
	}
	externalStatusClosePostgresRows(t, rows, "constraints")

	rows, err = connection.Query(ctx, `SELECT "table"."relname", "index_class"."relname",
		"pg_catalog"."pg_get_indexdef"("index"."indexrelid"), "index"."indisprimary", "index"."indisunique",
		"index"."indisvalid", "index"."indisready", "index"."indislive"
		FROM "pg_catalog"."pg_index" AS "index"
		JOIN "pg_catalog"."pg_class" AS "table" ON "table"."oid" = "index"."indrelid"
		JOIN "pg_catalog"."pg_class" AS "index_class" ON "index_class"."oid" = "index"."indexrelid"
		JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "table"."relnamespace"
		WHERE "n"."nspname" = $1 ORDER BY "table"."relname", "index_class"."relname"`, schema)
	if err != nil {
		t.Fatalf("inspect PostgreSQL indexes: %v", externalStatusPostgresSafeError(err))
	}
	for rows.Next() {
		var value externalPostgresIndex
		if err := rows.Scan(&value.table, &value.name, &value.definition, &value.primary, &value.unique, &value.valid,
			&value.ready, &value.live); err != nil {
			rows.Close()
			t.Fatalf("scan PostgreSQL index: %v", externalStatusPostgresSafeError(err))
		}
		snapshot.indexes = append(snapshot.indexes, value)
	}
	externalStatusClosePostgresRows(t, rows, "indexes")

	rows, err = connection.Query(ctx, `SELECT "c"."relname", "pg_catalog"."format_type"("s"."seqtypid", NULL),
		"s"."seqstart", "s"."seqincrement", "s"."seqmin", "s"."seqmax", "s"."seqcache", "s"."seqcycle",
		COALESCE("owner_table"."relname", ''), COALESCE("owner_column"."attname", ''), COALESCE("dependency"."deptype"::text, '')
		FROM "pg_catalog"."pg_sequence" AS "s"
		JOIN "pg_catalog"."pg_class" AS "c" ON "c"."oid" = "s"."seqrelid"
		JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "c"."relnamespace"
		LEFT JOIN "pg_catalog"."pg_depend" AS "dependency" ON "dependency"."classid" = 'pg_catalog.pg_class'::regclass
		 AND "dependency"."objid" = "c"."oid" AND "dependency"."refclassid" = 'pg_catalog.pg_class'::regclass
		 AND "dependency"."deptype" IN ('a', 'i')
		LEFT JOIN "pg_catalog"."pg_class" AS "owner_table" ON "owner_table"."oid" = "dependency"."refobjid"
		LEFT JOIN "pg_catalog"."pg_attribute" AS "owner_column" ON "owner_column"."attrelid" = "dependency"."refobjid"
		 AND "owner_column"."attnum" = "dependency"."refobjsubid"
		WHERE "n"."nspname" = $1 ORDER BY "c"."relname"`, schema)
	if err != nil {
		t.Fatalf("inspect PostgreSQL sequences: %v", externalStatusPostgresSafeError(err))
	}
	for rows.Next() {
		var value externalPostgresSequence
		if err := rows.Scan(&value.name, &value.typeName, &value.start, &value.increment, &value.minimum, &value.maximum,
			&value.cache, &value.cycle, &value.ownerTable, &value.ownerColumn, &value.dependency); err != nil {
			rows.Close()
			t.Fatalf("scan PostgreSQL sequence: %v", externalStatusPostgresSafeError(err))
		}
		snapshot.sequences = append(snapshot.sequences, value)
	}
	externalStatusClosePostgresRows(t, rows, "sequences")
	for index := range snapshot.sequences {
		identifier := pgx.Identifier{schema, snapshot.sequences[index].name}.Sanitize()
		if err := connection.QueryRow(ctx, "SELECT last_value, is_called FROM "+identifier).Scan(
			&snapshot.sequences[index].last, &snapshot.sequences[index].called); err != nil {
			t.Fatalf("inspect PostgreSQL sequence state: %v", externalStatusPostgresSafeError(err))
		}
	}

	for _, relation := range snapshot.relations {
		if relation.kind != "r" && relation.kind != "p" {
			continue
		}
		var count int64
		if err := connection.QueryRow(ctx, "SELECT COUNT(*) FROM "+pgx.Identifier{schema, relation.name}.Sanitize()).Scan(&count); err != nil {
			t.Fatalf("count PostgreSQL table: %v", externalStatusPostgresSafeError(err))
		}
		snapshot.counts = append(snapshot.counts, externalPostgresTableCount{table: relation.name, count: count})
	}

	if externalStatusPostgresHasTable(snapshot, "godj_migrations") {
		rows, err = connection.Query(ctx, `SELECT "app", "name" FROM `+pgx.Identifier{schema, "godj_migrations"}.Sanitize()+` ORDER BY "app", "name"`)
		if err != nil {
			t.Fatalf("inspect PostgreSQL history: %v", externalStatusPostgresSafeError(err))
		}
		for rows.Next() {
			var value externalSQLiteHistoryRow
			if err := rows.Scan(&value.app, &value.name); err != nil {
				rows.Close()
				t.Fatalf("scan PostgreSQL history: %v", externalStatusPostgresSafeError(err))
			}
			snapshot.history = append(snapshot.history, value)
		}
		externalStatusClosePostgresRows(t, rows, "history")
	}
	if externalStatusPostgresHasTable(snapshot, "godj_migration_revision") {
		rows, err = connection.Query(ctx, `SELECT "singleton", "format_version", "epoch", "revision", "history_fingerprint" FROM `+
			pgx.Identifier{schema, "godj_migration_revision"}.Sanitize()+` ORDER BY "singleton"`)
		if err != nil {
			t.Fatalf("inspect PostgreSQL revision: %v", externalStatusPostgresSafeError(err))
		}
		for rows.Next() {
			var value externalPostgresRevisionRow
			var epoch, fingerprint []byte
			if err := rows.Scan(&value.singleton, &value.format, &epoch, &value.revision, &fingerprint); err != nil {
				rows.Close()
				t.Fatalf("scan PostgreSQL revision: %v", externalStatusPostgresSafeError(err))
			}
			value.epoch = hex.EncodeToString(epoch)
			value.fingerprint = hex.EncodeToString(fingerprint)
			snapshot.revisions = append(snapshot.revisions, value)
		}
		externalStatusClosePostgresRows(t, rows, "revision")
	}
	if externalStatusPostgresHasTable(snapshot, "authors_author") {
		rows, err = connection.Query(ctx, `SELECT "id", "name" FROM `+pgx.Identifier{schema, "authors_author"}.Sanitize()+` ORDER BY "id"`)
		if err != nil {
			t.Fatalf("inspect PostgreSQL authors: %v", externalStatusPostgresSafeError(err))
		}
		for rows.Next() {
			var value externalPostgresAuthorRow
			if err := rows.Scan(&value.id, &value.name); err != nil {
				rows.Close()
				t.Fatalf("scan PostgreSQL author: %v", externalStatusPostgresSafeError(err))
			}
			snapshot.authors = append(snapshot.authors, value)
		}
		externalStatusClosePostgresRows(t, rows, "authors")
	}
	if externalStatusPostgresHasTable(snapshot, "blog_article") {
		hasPublished := externalStatusPostgresHasColumn(snapshot, "blog_article", "published")
		statement := `SELECT "id", "title" FROM ` + pgx.Identifier{schema, "blog_article"}.Sanitize() + ` ORDER BY "id"`
		if hasPublished {
			statement = `SELECT "id", "title", "published" FROM ` + pgx.Identifier{schema, "blog_article"}.Sanitize() + ` ORDER BY "id"`
		}
		rows, err = connection.Query(ctx, statement)
		if err != nil {
			t.Fatalf("inspect PostgreSQL articles: %v", externalStatusPostgresSafeError(err))
		}
		for rows.Next() {
			value := externalPostgresArticleRow{hasPublished: hasPublished}
			if hasPublished {
				err = rows.Scan(&value.id, &value.title, &value.published)
			} else {
				err = rows.Scan(&value.id, &value.title)
			}
			if err != nil {
				rows.Close()
				t.Fatalf("scan PostgreSQL article: %v", externalStatusPostgresSafeError(err))
			}
			snapshot.articles = append(snapshot.articles, value)
		}
		externalStatusClosePostgresRows(t, rows, "articles")
	}
	return snapshot
}

func externalStatusClosePostgresRows(t *testing.T, rows pgx.Rows, operation string) {
	t.Helper()
	if rows == nil {
		t.Fatalf("PostgreSQL %s query returned nil rows", operation)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("finish PostgreSQL %s query: %v", operation, externalStatusPostgresSafeError(err))
	}
}

func externalStatusPostgresHasTable(snapshot externalPostgresSnapshot, table string) bool {
	for _, relation := range snapshot.relations {
		if relation.kind == "r" && relation.name == table {
			return true
		}
	}
	return false
}

func externalStatusPostgresHasColumn(snapshot externalPostgresSnapshot, table, column string) bool {
	for _, value := range snapshot.columns {
		if value.table == table && value.name == column {
			return true
		}
	}
	return false
}

func externalStatusAssertPostgresEmpty(t *testing.T, snapshot externalPostgresSnapshot) {
	t.Helper()
	if snapshot.namespace.oid == 0 || snapshot.namespace.owner == "" || len(snapshot.relations) != 0 ||
		len(snapshot.columns) != 0 || len(snapshot.constraints) != 0 || len(snapshot.indexes) != 0 ||
		len(snapshot.sequences) != 0 || len(snapshot.counts) != 0 || len(snapshot.history) != 0 ||
		len(snapshot.revisions) != 0 || len(snapshot.authors) != 0 || len(snapshot.articles) != 0 {
		t.Fatalf("fresh PostgreSQL schema is not empty and bounded: %+v", snapshot)
	}
}

func externalStatusAssertPostgresUnchanged(t *testing.T, before, after externalPostgresSnapshot) {
	t.Helper()
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("read-only showmigrations changed PostgreSQL schema/recorder/revision/application/sequence state\nbefore=%+v\nafter=%+v", before, after)
	}
}

func externalStatusAssertPostgresHistory(t *testing.T, snapshot externalPostgresSnapshot, revision int64, want ...externalSQLiteHistoryRow) {
	t.Helper()
	if !reflect.DeepEqual(snapshot.history, want) {
		t.Fatalf("PostgreSQL migration history = %+v, want %+v", snapshot.history, want)
	}
	if len(snapshot.revisions) != 1 || snapshot.revisions[0].singleton != 1 || snapshot.revisions[0].format != 1 ||
		len(snapshot.revisions[0].epoch) != 32 || snapshot.revisions[0].revision != revision ||
		len(snapshot.revisions[0].fingerprint) != sha256.Size*2 {
		t.Fatalf("PostgreSQL revision row is not current and bounded: %+v", snapshot.revisions)
	}
	wantFingerprint := externalStatusFingerprintHistory(want)
	if snapshot.revisions[0].fingerprint != hex.EncodeToString(wantFingerprint[:]) {
		t.Fatalf("PostgreSQL revision fingerprint = %q, want %x", snapshot.revisions[0].fingerprint, wantFingerprint)
	}
	if len(snapshot.authors) != 1 || snapshot.authors[0].name != "durable author sentinel" ||
		len(snapshot.articles) != 1 || snapshot.articles[0].title != "durable article sentinel" {
		t.Fatalf("PostgreSQL application sentinel rows are not exact: authors=%+v articles=%+v", snapshot.authors, snapshot.articles)
	}
}

func externalStatusPostgresMigrateSetup(t *testing.T, project *externalStatusProject, environment []string, marker string, sensitive []string) {
	t.Helper()
	result := project.runMigrate(t, environment)
	externalStatusAssertRedacted(t, result, sensitive...)
	if result.exitCode != 0 || result.stderr != "" || result.stdout == "" {
		t.Fatalf("external PostgreSQL migrate setup failed: exit=%d stdout=%q stderr=%q", result.exitCode, result.stdout, result.stderr)
	}
	externalStatusResetMarker(t, marker)
}

func externalStatusSeedPostgresApplicationRows(t *testing.T, databaseURL, schema string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect PostgreSQL application seed: %v", externalStatusPostgresSafeError(err))
	}
	defer func() {
		if err := connection.Close(ctx); err != nil {
			t.Errorf("close PostgreSQL application seed: %v", externalStatusPostgresSafeError(err))
		}
	}()
	if _, err := connection.Exec(ctx, `INSERT INTO `+pgx.Identifier{schema, "authors_author"}.Sanitize()+` ("name") VALUES ($1)`, "durable author sentinel"); err != nil {
		t.Fatalf("seed PostgreSQL author: %v", externalStatusPostgresSafeError(err))
	}
	var publishedColumns int
	if err := connection.QueryRow(ctx, `SELECT COUNT(*) FROM "pg_catalog"."pg_attribute" AS "a"
		JOIN "pg_catalog"."pg_class" AS "c" ON "c"."oid" = "a"."attrelid"
		JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "c"."relnamespace"
		WHERE "n"."nspname" = $1 AND "c"."relname" = 'blog_article' AND "a"."attname" = 'published'
		AND "a"."attnum" > 0 AND NOT "a"."attisdropped"`, schema).Scan(&publishedColumns); err != nil {
		t.Fatalf("inspect PostgreSQL published column: %v", externalStatusPostgresSafeError(err))
	}
	statement := `INSERT INTO ` + pgx.Identifier{schema, "blog_article"}.Sanitize() + ` ("title") VALUES ($1)`
	arguments := []any{"durable article sentinel"}
	if publishedColumns == 1 {
		statement = `INSERT INTO ` + pgx.Identifier{schema, "blog_article"}.Sanitize() + ` ("title", "published") VALUES ($1, $2)`
		arguments = append(arguments, false)
	} else if publishedColumns != 0 {
		t.Fatalf("PostgreSQL published column count = %d, want 0 or 1", publishedColumns)
	}
	if _, err := connection.Exec(ctx, statement, arguments...); err != nil {
		t.Fatalf("seed PostgreSQL article: %v", externalStatusPostgresSafeError(err))
	}
}

func externalStatusInstallInconsistentPostgresHistory(t *testing.T, databaseURL, schema string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect PostgreSQL inconsistent history fixture: %v", externalStatusPostgresSafeError(err))
	}
	defer func() {
		if err := connection.Close(ctx); err != nil {
			t.Errorf("close PostgreSQL inconsistent history fixture: %v", externalStatusPostgresSafeError(err))
		}
	}()
	transaction, err := connection.Begin(ctx)
	if err != nil {
		t.Fatalf("begin PostgreSQL inconsistent history fixture: %v", externalStatusPostgresSafeError(err))
	}
	committed := false
	defer func() {
		if !committed {
			_ = transaction.Rollback(ctx)
		}
	}()
	historyTable := pgx.Identifier{schema, "godj_migrations"}.Sanitize()
	result, err := transaction.Exec(ctx, `DELETE FROM `+historyTable+` WHERE "app" = $1 AND "name" = $2`, "authors", "0001_author")
	if err != nil || result.RowsAffected() != 1 {
		t.Fatalf("remove PostgreSQL applied parent: affected=%d error=%v", result.RowsAffected(), externalStatusPostgresSafeError(err))
	}
	rows, err := transaction.Query(ctx, `SELECT "app", "name" FROM `+historyTable+` ORDER BY "app", "name"`)
	if err != nil {
		t.Fatalf("read PostgreSQL inconsistent history: %v", externalStatusPostgresSafeError(err))
	}
	var history []externalSQLiteHistoryRow
	for rows.Next() {
		var value externalSQLiteHistoryRow
		if err := rows.Scan(&value.app, &value.name); err != nil {
			rows.Close()
			t.Fatalf("scan PostgreSQL inconsistent history: %v", externalStatusPostgresSafeError(err))
		}
		history = append(history, value)
	}
	externalStatusClosePostgresRows(t, rows, "inconsistent history")
	fingerprint := externalStatusFingerprintHistory(history)
	revisionTable := pgx.Identifier{schema, "godj_migration_revision"}.Sanitize()
	result, err = transaction.Exec(ctx, `UPDATE `+revisionTable+` SET "history_fingerprint" = $1 WHERE "singleton" = 1`, fingerprint[:])
	if err != nil || result.RowsAffected() != 1 {
		t.Fatalf("update PostgreSQL inconsistent-history fingerprint: affected=%d error=%v", result.RowsAffected(), externalStatusPostgresSafeError(err))
	}
	if err := transaction.Commit(ctx); err != nil {
		t.Fatalf("commit PostgreSQL inconsistent history fixture: %v", externalStatusPostgresSafeError(err))
	}
	committed = true
}

func externalStatusPostgresSafeError(err error) error {
	if err == nil {
		return nil
	}
	var structured interface{ SQLState() string }
	if errors.As(err, &structured) {
		return fmt.Errorf("PostgreSQL SQLSTATE %s", structured.SQLState())
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("PostgreSQL operation timed out")
	}
	if errors.Is(err, context.Canceled) {
		return errors.New("PostgreSQL operation was canceled")
	}
	return errors.New("PostgreSQL operation failed")
}
