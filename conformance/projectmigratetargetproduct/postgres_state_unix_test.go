//go:build darwin || linux

package projectmigratetargetproduct_test

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

var targetPostgresSchemaSequence atomic.Uint64

type targetPostgresNamespace struct {
	oid   uint32
	owner string
	acl   string
}

type targetPostgresRelation struct {
	name string
	kind string
}

type targetPostgresColumn struct {
	table            string
	ordinal          int
	name             string
	typeName         string
	notNull          bool
	identity         string
	generated        string
	hasDefault       bool
	defaultCollation bool
	primary          bool
}

type targetPostgresConstraint struct {
	table      string
	name       string
	kind       string
	deferrable bool
	deferred   bool
	validated  bool
	key        string
	indexName  string
}

type targetPostgresIndex struct {
	table          string
	name           string
	primary        bool
	unique         bool
	valid          bool
	ready          bool
	live           bool
	keyCount       int
	attributeCount int
	keys           string
	method         string
	hasPredicate   bool
	hasExpressions bool
	exclusion      bool
}

type targetPostgresSequence struct {
	name        string
	kind        string
	persistence string
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

type targetPostgresRevision struct {
	singleton   int64
	format      int64
	epoch       string
	revision    int64
	fingerprint string
}

type targetPostgresValue struct {
	id    int64
	value string
}

type targetPostgresSnapshot struct {
	namespace      targetPostgresNamespace
	tables         []string
	otherRelations []targetPostgresRelation
	columns        []targetPostgresColumn
	constraints    []targetPostgresConstraint
	indexes        []targetPostgresIndex
	triggers       int
	policies       int
	rules          int
	counts         map[string]int64
	history        []targetSQLiteHistoryRow
	revisions      []targetPostgresRevision
	sequences      []targetPostgresSequence
	values         map[string][]targetPostgresValue
}

func targetPostgresTestURL(t *testing.T) string {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv(targetPostgresTestURLEnvironment))
	if databaseURL != "" {
		return databaseURL
	}
	if os.Getenv(targetPostgresRequiredEnvironment) == "1" {
		t.Fatalf("%s=1 requires %s", targetPostgresRequiredEnvironment, targetPostgresTestURLEnvironment)
	}
	t.Skip("GODJ_TEST_POSTGRES_URL is not configured; targeted migrate PostgreSQL product E2E was not run")
	return ""
}

func targetPostgresSensitive(t *testing.T, project *targetExternalProject, databaseURL, schema string) []string {
	t.Helper()
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal("parse target migrate PostgreSQL URL: database URL is invalid")
	}
	values := []string{databaseURL, schema, project.secret}
	if len(config.Password) >= 4 {
		values = append(values, config.Password)
	}
	return values
}

func (project *targetExternalProject) postgresMarker(t *testing.T, name string) string {
	t.Helper()
	directory := project.universe + string(os.PathSeparator) + "state" + string(os.PathSeparator) + name
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	return directory + string(os.PathSeparator) + "runner-events.log"
}

func targetCreatePostgresSchema(t *testing.T, databaseURL string) string {
	t.Helper()
	sequence := targetPostgresSchemaSequence.Add(1)
	schema := fmt.Sprintf("godj_mt_%d_%d_%d", os.Getpid(), time.Now().UnixNano(), sequence)
	if len(schema) > 63 {
		t.Fatal("generated targeted migrate PostgreSQL schema exceeds identifier limit")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect PostgreSQL schema owner: %v", targetPostgresSafeError(err))
	}
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := connection.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("create isolated targeted migrate PostgreSQL schema: %v", targetPostgresSafeError(err))
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		cleanup, err := pgx.Connect(cleanupCtx, databaseURL)
		if err != nil {
			t.Errorf("connect targeted migrate PostgreSQL schema cleanup: %v", targetPostgresSafeError(err))
			return
		}
		if _, err := cleanup.Exec(cleanupCtx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Errorf("drop targeted migrate PostgreSQL schema: %v", targetPostgresSafeError(err))
		}
		if err := cleanup.Close(cleanupCtx); err != nil {
			t.Errorf("close targeted migrate PostgreSQL schema cleanup: %v", targetPostgresSafeError(err))
		}
	})
	if err := connection.Close(ctx); err != nil {
		t.Fatalf("close PostgreSQL schema owner: %v", targetPostgresSafeError(err))
	}
	return schema
}

func targetCapturePostgres(t *testing.T, databaseURL, schema string) targetPostgresSnapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect targeted migrate PostgreSQL inspector: %v", targetPostgresSafeError(err))
	}
	defer func() {
		if err := connection.Close(ctx); err != nil {
			t.Errorf("close targeted migrate PostgreSQL inspector: %v", targetPostgresSafeError(err))
		}
	}()

	snapshot := targetPostgresSnapshot{
		counts: make(map[string]int64),
		values: make(map[string][]targetPostgresValue),
	}
	if err := connection.QueryRow(ctx, `SELECT "n"."oid", "owner"."rolname", COALESCE("n"."nspacl"::text, '')
		FROM "pg_catalog"."pg_namespace" AS "n"
		JOIN "pg_catalog"."pg_roles" AS "owner" ON "owner"."oid" = "n"."nspowner"
		WHERE "n"."nspname" = $1`, schema).Scan(
		&snapshot.namespace.oid,
		&snapshot.namespace.owner,
		&snapshot.namespace.acl,
	); err != nil {
		t.Fatalf("inspect targeted migrate PostgreSQL namespace: %v", targetPostgresSafeError(err))
	}

	rows, err := connection.Query(ctx, `SELECT "c"."relname", "c"."relkind"::text
		FROM "pg_catalog"."pg_class" AS "c"
		JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "c"."relnamespace"
		WHERE "n"."nspname" = $1 ORDER BY "c"."relkind", "c"."relname"`, schema)
	if err != nil {
		t.Fatalf("inspect targeted migrate PostgreSQL relations: %v", targetPostgresSafeError(err))
	}
	for rows.Next() {
		var relation targetPostgresRelation
		if err := rows.Scan(&relation.name, &relation.kind); err != nil {
			rows.Close()
			t.Fatalf("scan targeted migrate PostgreSQL relation: %v", targetPostgresSafeError(err))
		}
		switch relation.kind {
		case "r":
			snapshot.tables = append(snapshot.tables, relation.name)
		case "i", "S":
		default:
			snapshot.otherRelations = append(snapshot.otherRelations, relation)
		}
	}
	targetClosePostgresRows(t, rows, "relations")

	rows, err = connection.Query(ctx, `SELECT "c"."relname", "a"."attnum", "a"."attname",
		"pg_catalog"."format_type"("a"."atttypid", "a"."atttypmod"),
		"a"."attnotnull", "a"."attidentity"::text, "a"."attgenerated"::text,
		("d"."oid" IS NOT NULL),
		COALESCE("a"."attcollation" = "type"."typcollation", "a"."attcollation" = 0),
		EXISTS (SELECT 1 FROM "pg_catalog"."pg_index" AS "i"
			WHERE "i"."indrelid" = "c"."oid" AND "i"."indisprimary" AND "a"."attnum" = ANY("i"."indkey"))
		FROM "pg_catalog"."pg_class" AS "c"
		JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "c"."relnamespace"
		JOIN "pg_catalog"."pg_attribute" AS "a" ON "a"."attrelid" = "c"."oid"
		JOIN "pg_catalog"."pg_type" AS "type" ON "type"."oid" = "a"."atttypid"
		LEFT JOIN "pg_catalog"."pg_attrdef" AS "d" ON "d"."adrelid" = "a"."attrelid" AND "d"."adnum" = "a"."attnum"
		WHERE "n"."nspname" = $1 AND "c"."relkind" = 'r' AND "a"."attnum" > 0 AND NOT "a"."attisdropped"
		ORDER BY "c"."relname", "a"."attnum"`, schema)
	if err != nil {
		t.Fatalf("inspect targeted migrate PostgreSQL columns: %v", targetPostgresSafeError(err))
	}
	for rows.Next() {
		var column targetPostgresColumn
		if err := rows.Scan(&column.table, &column.ordinal, &column.name, &column.typeName, &column.notNull,
			&column.identity, &column.generated, &column.hasDefault, &column.defaultCollation, &column.primary); err != nil {
			rows.Close()
			t.Fatalf("scan targeted migrate PostgreSQL column: %v", targetPostgresSafeError(err))
		}
		snapshot.columns = append(snapshot.columns, column)
	}
	targetClosePostgresRows(t, rows, "columns")

	rows, err = connection.Query(ctx, `SELECT "table"."relname", "constraint"."conname", "constraint"."contype"::text,
		"constraint"."condeferrable", "constraint"."condeferred", "constraint"."convalidated",
		COALESCE("pg_catalog"."array_to_string"("constraint"."conkey", ','), ''),
		COALESCE("constraint_index"."relname", '')
		FROM "pg_catalog"."pg_constraint" AS "constraint"
		JOIN "pg_catalog"."pg_class" AS "table" ON "table"."oid" = "constraint"."conrelid"
		JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "table"."relnamespace"
		LEFT JOIN "pg_catalog"."pg_class" AS "constraint_index" ON "constraint_index"."oid" = "constraint"."conindid"
		WHERE "n"."nspname" = $1 ORDER BY "table"."relname", "constraint"."conname"`, schema)
	if err != nil {
		t.Fatalf("inspect targeted migrate PostgreSQL constraints: %v", targetPostgresSafeError(err))
	}
	for rows.Next() {
		var constraint targetPostgresConstraint
		if err := rows.Scan(&constraint.table, &constraint.name, &constraint.kind, &constraint.deferrable,
			&constraint.deferred, &constraint.validated, &constraint.key, &constraint.indexName); err != nil {
			rows.Close()
			t.Fatalf("scan targeted migrate PostgreSQL constraint: %v", targetPostgresSafeError(err))
		}
		snapshot.constraints = append(snapshot.constraints, constraint)
	}
	targetClosePostgresRows(t, rows, "constraints")

	rows, err = connection.Query(ctx, `SELECT "table"."relname", "index_class"."relname",
		"index"."indisprimary", "index"."indisunique", "index"."indisvalid", "index"."indisready", "index"."indislive",
		"index"."indnkeyatts"::integer, "index"."indnatts"::integer,
		COALESCE((SELECT "pg_catalog"."string_agg"("attribute"."attname", ',' ORDER BY "key"."ordinality")
			FROM "pg_catalog"."unnest"("index"."indkey"::smallint[]) WITH ORDINALITY AS "key"("attribute_number", "ordinality")
			JOIN "pg_catalog"."pg_attribute" AS "attribute" ON "attribute"."attrelid" = "index"."indrelid"
				AND "attribute"."attnum" = "key"."attribute_number"
			WHERE "key"."ordinality" <= "index"."indnkeyatts"), ''),
		"access_method"."amname", ("index"."indpred" IS NOT NULL), ("index"."indexprs" IS NOT NULL), "index"."indisexclusion"
		FROM "pg_catalog"."pg_index" AS "index"
		JOIN "pg_catalog"."pg_class" AS "table" ON "table"."oid" = "index"."indrelid"
		JOIN "pg_catalog"."pg_class" AS "index_class" ON "index_class"."oid" = "index"."indexrelid"
		JOIN "pg_catalog"."pg_am" AS "access_method" ON "access_method"."oid" = "index_class"."relam"
		JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "table"."relnamespace"
		WHERE "n"."nspname" = $1 ORDER BY "table"."relname", "index_class"."relname"`, schema)
	if err != nil {
		t.Fatalf("inspect targeted migrate PostgreSQL indexes: %v", targetPostgresSafeError(err))
	}
	for rows.Next() {
		var index targetPostgresIndex
		if err := rows.Scan(&index.table, &index.name, &index.primary, &index.unique, &index.valid, &index.ready, &index.live,
			&index.keyCount, &index.attributeCount, &index.keys, &index.method, &index.hasPredicate,
			&index.hasExpressions, &index.exclusion); err != nil {
			rows.Close()
			t.Fatalf("scan targeted migrate PostgreSQL index: %v", targetPostgresSafeError(err))
		}
		snapshot.indexes = append(snapshot.indexes, index)
	}
	targetClosePostgresRows(t, rows, "indexes")

	if err := connection.QueryRow(ctx, `SELECT
		(SELECT COUNT(*) FROM "pg_catalog"."pg_trigger" AS "trigger"
			JOIN "pg_catalog"."pg_class" AS "table" ON "table"."oid" = "trigger"."tgrelid"
			JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "table"."relnamespace" WHERE "n"."nspname" = $1),
		(SELECT COUNT(*) FROM "pg_catalog"."pg_policy" AS "policy"
			JOIN "pg_catalog"."pg_class" AS "table" ON "table"."oid" = "policy"."polrelid"
			JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "table"."relnamespace" WHERE "n"."nspname" = $1),
		(SELECT COUNT(*) FROM "pg_catalog"."pg_rewrite" AS "rule"
			JOIN "pg_catalog"."pg_class" AS "table" ON "table"."oid" = "rule"."ev_class"
			JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "table"."relnamespace" WHERE "n"."nspname" = $1)`, schema).Scan(
		&snapshot.triggers,
		&snapshot.policies,
		&snapshot.rules,
	); err != nil {
		t.Fatalf("inspect targeted migrate PostgreSQL trigger/policy/rule counts: %v", targetPostgresSafeError(err))
	}

	for _, table := range snapshot.tables {
		quoted := pgx.Identifier{schema, table}.Sanitize()
		var count int64
		if err := connection.QueryRow(ctx, "SELECT COUNT(*) FROM "+quoted).Scan(&count); err != nil {
			t.Fatalf("count targeted migrate PostgreSQL table: %v", targetPostgresSafeError(err))
		}
		snapshot.counts[table] = count
		if strings.HasPrefix(table, "target_") {
			rows, err = connection.Query(ctx, `SELECT "id", "value" FROM `+quoted+` ORDER BY "id"`)
			if err != nil {
				t.Fatalf("inspect targeted migrate PostgreSQL sentinel rows: %v", targetPostgresSafeError(err))
			}
			snapshot.values[table] = make([]targetPostgresValue, 0)
			for rows.Next() {
				var value targetPostgresValue
				if err := rows.Scan(&value.id, &value.value); err != nil {
					rows.Close()
					t.Fatalf("scan targeted migrate PostgreSQL sentinel: %v", targetPostgresSafeError(err))
				}
				snapshot.values[table] = append(snapshot.values[table], value)
			}
			targetClosePostgresRows(t, rows, "sentinel rows")
		}
	}

	if targetPostgresHasTable(snapshot, "godj_migrations") {
		rows, err = connection.Query(ctx, `SELECT "app", "name" FROM `+pgx.Identifier{schema, "godj_migrations"}.Sanitize()+` ORDER BY "app", "name"`)
		if err != nil {
			t.Fatalf("inspect targeted migrate PostgreSQL history: %v", targetPostgresSafeError(err))
		}
		for rows.Next() {
			var history targetSQLiteHistoryRow
			if err := rows.Scan(&history.app, &history.name); err != nil {
				rows.Close()
				t.Fatalf("scan targeted migrate PostgreSQL history: %v", targetPostgresSafeError(err))
			}
			snapshot.history = append(snapshot.history, history)
		}
		targetClosePostgresRows(t, rows, "history")
	}
	if targetPostgresHasTable(snapshot, "godj_migration_revision") {
		rows, err = connection.Query(ctx, `SELECT "singleton", "format_version", "epoch", "revision", "history_fingerprint"
			FROM `+pgx.Identifier{schema, "godj_migration_revision"}.Sanitize()+` ORDER BY "singleton"`)
		if err != nil {
			t.Fatalf("inspect targeted migrate PostgreSQL revision: %v", targetPostgresSafeError(err))
		}
		for rows.Next() {
			var revision targetPostgresRevision
			var epoch, fingerprint []byte
			if err := rows.Scan(&revision.singleton, &revision.format, &epoch, &revision.revision, &fingerprint); err != nil {
				rows.Close()
				t.Fatalf("scan targeted migrate PostgreSQL revision: %v", targetPostgresSafeError(err))
			}
			revision.epoch = hex.EncodeToString(epoch)
			revision.fingerprint = hex.EncodeToString(fingerprint)
			snapshot.revisions = append(snapshot.revisions, revision)
		}
		targetClosePostgresRows(t, rows, "revision")
	}

	rows, err = connection.Query(ctx, `SELECT "c"."relname", "c"."relkind"::text, "c"."relpersistence"::text,
		"pg_catalog"."format_type"("s"."seqtypid", NULL), "s"."seqstart", "s"."seqincrement", "s"."seqmin",
		"s"."seqmax", "s"."seqcache", "s"."seqcycle", COALESCE("owner_table"."relname", ''),
		COALESCE("owner_column"."attname", ''), COALESCE("dependency"."deptype"::text, '')
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
		t.Fatalf("inspect targeted migrate PostgreSQL sequences: %v", targetPostgresSafeError(err))
	}
	for rows.Next() {
		var sequence targetPostgresSequence
		if err := rows.Scan(&sequence.name, &sequence.kind, &sequence.persistence, &sequence.typeName, &sequence.start,
			&sequence.increment, &sequence.minimum, &sequence.maximum, &sequence.cache, &sequence.cycle,
			&sequence.ownerTable, &sequence.ownerColumn, &sequence.dependency); err != nil {
			rows.Close()
			t.Fatalf("scan targeted migrate PostgreSQL sequence: %v", targetPostgresSafeError(err))
		}
		snapshot.sequences = append(snapshot.sequences, sequence)
	}
	targetClosePostgresRows(t, rows, "sequences")
	for index := range snapshot.sequences {
		quoted := pgx.Identifier{schema, snapshot.sequences[index].name}.Sanitize()
		if err := connection.QueryRow(ctx, "SELECT last_value, is_called FROM "+quoted).Scan(
			&snapshot.sequences[index].last,
			&snapshot.sequences[index].called,
		); err != nil {
			t.Fatalf("inspect targeted migrate PostgreSQL sequence state: %v", targetPostgresSafeError(err))
		}
	}
	return snapshot
}

func targetClosePostgresRows(t *testing.T, rows pgx.Rows, operation string) {
	t.Helper()
	if rows == nil {
		t.Fatalf("targeted migrate PostgreSQL %s query returned nil rows", operation)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("finish targeted migrate PostgreSQL %s query: %v", operation, targetPostgresSafeError(err))
	}
}

func targetPostgresHasTable(snapshot targetPostgresSnapshot, table string) bool {
	for _, candidate := range snapshot.tables {
		if candidate == table {
			return true
		}
	}
	return false
}

func targetAssertPostgresEmpty(t *testing.T, snapshot targetPostgresSnapshot) {
	t.Helper()
	if snapshot.namespace.oid == 0 || snapshot.namespace.owner == "" || len(snapshot.tables) != 0 ||
		len(snapshot.otherRelations) != 0 || len(snapshot.columns) != 0 || len(snapshot.constraints) != 0 ||
		len(snapshot.indexes) != 0 || snapshot.triggers != 0 || snapshot.policies != 0 || snapshot.rules != 0 ||
		len(snapshot.counts) != 0 || len(snapshot.history) != 0 || len(snapshot.revisions) != 0 ||
		len(snapshot.sequences) != 0 || len(snapshot.values) != 0 {
		t.Fatalf("fresh targeted migrate PostgreSQL schema is not exact and empty: %+v", snapshot)
	}
}

func targetAssertPostgresUnchanged(t *testing.T, databaseURL, schema string, before targetPostgresSnapshot) {
	t.Helper()
	after := targetCapturePostgres(t, databaseURL, schema)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("read-only target migrate changed PostgreSQL state\nbefore=%+v\nafter=%+v", before, after)
	}
}

func targetAssertPostgresState(
	t *testing.T,
	databaseURL,
	schema string,
	revision int64,
	tables []string,
	history []targetSQLiteHistoryRow,
	values map[string][]targetPostgresValue,
) targetPostgresSnapshot {
	t.Helper()
	snapshot := targetCapturePostgres(t, databaseURL, schema)
	wantTables := append([]string{"godj_migration_revision", "godj_migrations"}, tables...)
	sort.Strings(wantTables)
	if !reflect.DeepEqual(snapshot.tables, wantTables) || len(snapshot.otherRelations) != 0 {
		t.Fatalf("targeted migrate PostgreSQL tables/other relations = %v/%v, want exact %v/empty", snapshot.tables, snapshot.otherRelations, wantTables)
	}
	if !reflect.DeepEqual(snapshot.columns, targetExpectedPostgresColumns(tables)) {
		t.Fatalf("targeted migrate PostgreSQL columns differ from exact current profile: %+v", snapshot.columns)
	}
	if !reflect.DeepEqual(snapshot.constraints, targetExpectedPostgresConstraints(tables)) {
		t.Fatalf("targeted migrate PostgreSQL constraints differ from exact current profile: %+v", snapshot.constraints)
	}
	if !reflect.DeepEqual(snapshot.indexes, targetExpectedPostgresIndexes(tables)) {
		t.Fatalf("targeted migrate PostgreSQL indexes differ from exact current profile: %+v", snapshot.indexes)
	}
	if snapshot.triggers != 0 || snapshot.policies != 0 || snapshot.rules != 0 {
		t.Fatalf("targeted migrate PostgreSQL trigger/policy/rule counts = %d/%d/%d, want zero", snapshot.triggers, snapshot.policies, snapshot.rules)
	}
	canonicalHistory := append([]targetSQLiteHistoryRow(nil), history...)
	sort.Slice(canonicalHistory, func(left, right int) bool {
		if canonicalHistory[left].app != canonicalHistory[right].app {
			return canonicalHistory[left].app < canonicalHistory[right].app
		}
		return canonicalHistory[left].name < canonicalHistory[right].name
	})
	if !reflect.DeepEqual(snapshot.history, canonicalHistory) {
		t.Fatalf("targeted migrate PostgreSQL history = %+v, want %+v", snapshot.history, canonicalHistory)
	}
	wantFingerprint := targetFingerprintHistory(canonicalHistory)
	if len(snapshot.revisions) != 1 || snapshot.revisions[0].singleton != 1 || snapshot.revisions[0].format != 1 ||
		len(snapshot.revisions[0].epoch) != 32 || snapshot.revisions[0].epoch == strings.Repeat("0", 32) ||
		snapshot.revisions[0].revision != revision || snapshot.revisions[0].fingerprint != hex.EncodeToString(wantFingerprint[:]) {
		t.Fatalf("targeted migrate PostgreSQL revision is not exact/current: %+v", snapshot.revisions)
	}
	wantCounts := map[string]int64{
		"godj_migration_revision": 1,
		"godj_migrations":         int64(len(canonicalHistory)),
	}
	wantValues := make(map[string][]targetPostgresValue, len(tables))
	for _, table := range tables {
		rows := append([]targetPostgresValue(nil), values[table]...)
		if len(rows) == 0 {
			rows = make([]targetPostgresValue, 0)
		}
		wantValues[table] = rows
		wantCounts[table] = int64(len(rows))
	}
	if !reflect.DeepEqual(snapshot.counts, wantCounts) || !reflect.DeepEqual(snapshot.values, wantValues) {
		t.Fatalf("targeted migrate PostgreSQL counts/values = %+v/%+v, want %+v/%+v", snapshot.counts, snapshot.values, wantCounts, wantValues)
	}
	if !reflect.DeepEqual(snapshot.sequences, targetExpectedPostgresSequences(tables, wantValues)) {
		t.Fatalf("targeted migrate PostgreSQL sequences differ from exact AutoField profile: %+v", snapshot.sequences)
	}
	return snapshot
}

func targetExpectedPostgresColumns(tables []string) []targetPostgresColumn {
	column := func(table string, ordinal int, name, typeName string, notNull bool, identity string, primary bool) targetPostgresColumn {
		return targetPostgresColumn{
			table: table, ordinal: ordinal, name: name, typeName: typeName, notNull: notNull,
			identity: identity, defaultCollation: true, primary: primary,
		}
	}
	result := []targetPostgresColumn{
		column("godj_migration_revision", 1, "singleton", "smallint", true, "", true),
		column("godj_migration_revision", 2, "format_version", "integer", true, "", false),
		column("godj_migration_revision", 3, "epoch", "bytea", true, "", false),
		column("godj_migration_revision", 4, "revision", "bigint", true, "", false),
		column("godj_migration_revision", 5, "history_fingerprint", "bytea", true, "", false),
		column("godj_migrations", 1, "app", "character varying(255)", true, "", true),
		column("godj_migrations", 2, "name", "character varying(255)", true, "", true),
	}
	for _, table := range tables {
		result = append(result,
			column(table, 1, "id", "bigint", true, "d", true),
			column(table, 2, "value", "character varying(128)", true, "", false),
		)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].table != result[right].table {
			return result[left].table < result[right].table
		}
		return result[left].ordinal < result[right].ordinal
	})
	return result
}

func targetExpectedPostgresConstraints(tables []string) []targetPostgresConstraint {
	primary := func(table, name, key string) targetPostgresConstraint {
		return targetPostgresConstraint{table: table, name: name, kind: "p", validated: true, key: key, indexName: name}
	}
	result := []targetPostgresConstraint{
		primary("godj_migration_revision", "godj_migration_revision_pkey", "1"),
		primary("godj_migrations", "godj_migrations_pkey", "1,2"),
	}
	for _, table := range tables {
		result = append(result, primary(table, targetPostgresDerivedName("godj/postgres/primary-key/v1", "godj_pk_", table), "1"))
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].table != result[right].table {
			return result[left].table < result[right].table
		}
		return result[left].name < result[right].name
	})
	return result
}

func targetExpectedPostgresIndexes(tables []string) []targetPostgresIndex {
	primary := func(table, name, keys string, count int) targetPostgresIndex {
		return targetPostgresIndex{
			table: table, name: name, primary: true, unique: true, valid: true, ready: true, live: true,
			keyCount: count, attributeCount: count, keys: keys, method: "btree",
		}
	}
	result := []targetPostgresIndex{
		primary("godj_migration_revision", "godj_migration_revision_pkey", "singleton", 1),
		primary("godj_migrations", "godj_migrations_pkey", "app,name", 2),
	}
	for _, table := range tables {
		result = append(result, primary(table, targetPostgresDerivedName("godj/postgres/primary-key/v1", "godj_pk_", table), "id", 1))
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].table != result[right].table {
			return result[left].table < result[right].table
		}
		return result[left].name < result[right].name
	})
	return result
}

func targetExpectedPostgresSequences(tables []string, values map[string][]targetPostgresValue) []targetPostgresSequence {
	result := make([]targetPostgresSequence, 0, len(tables))
	for _, table := range tables {
		last := int64(1)
		called := false
		if rows := values[table]; len(rows) > 0 {
			last = rows[len(rows)-1].id
			called = true
		}
		result = append(result, targetPostgresSequence{
			name: targetPostgresDerivedName("godj/postgres/identity-sequence/v1", "godj_seq_", table, "id"),
			kind: "S", persistence: "p", typeName: "bigint", start: 1, increment: 1, minimum: 1,
			maximum: math.MaxInt64, cache: 1, ownerTable: table, ownerColumn: "id", dependency: "i",
			last: last, called: called,
		})
	}
	sort.Slice(result, func(left, right int) bool { return result[left].name < result[right].name })
	return result
}

func targetPostgresDerivedName(domain, prefix string, values ...string) string {
	hash := sha256.New()
	var length [8]byte
	for _, value := range append([]string{domain}, values...) {
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	return prefix + hex.EncodeToString(hash.Sum(nil)[:24])
}

func targetPostgresEpoch(t *testing.T, snapshot targetPostgresSnapshot) string {
	t.Helper()
	if len(snapshot.revisions) != 1 || len(snapshot.revisions[0].epoch) != 32 ||
		snapshot.revisions[0].epoch == strings.Repeat("0", 32) {
		t.Fatalf("targeted migrate PostgreSQL epoch is not current: %+v", snapshot.revisions)
	}
	return snapshot.revisions[0].epoch
}

func targetInsertPostgresValue(t *testing.T, databaseURL, schema, table, value string) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect targeted migrate PostgreSQL sentinel writer: %v", targetPostgresSafeError(err))
	}
	defer func() {
		if err := connection.Close(ctx); err != nil {
			t.Errorf("close targeted migrate PostgreSQL sentinel writer: %v", targetPostgresSafeError(err))
		}
	}()
	var identifier int64
	if err := connection.QueryRow(ctx, `INSERT INTO `+pgx.Identifier{schema, table}.Sanitize()+` ("value") VALUES ($1) RETURNING "id"`, value).Scan(&identifier); err != nil {
		t.Fatalf("insert targeted migrate PostgreSQL sentinel: %v", targetPostgresSafeError(err))
	}
	return identifier
}

func targetPostgresSafeError(err error) error {
	if err == nil {
		return nil
	}
	var sqlState interface{ SQLState() string }
	if errors.As(err, &sqlState) {
		return fmt.Errorf("PostgreSQL SQLSTATE %s", sqlState.SQLState())
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("PostgreSQL operation timed out")
	}
	if errors.Is(err, context.Canceled) {
		return errors.New("PostgreSQL operation was canceled")
	}
	return errors.New("PostgreSQL operation failed")
}
