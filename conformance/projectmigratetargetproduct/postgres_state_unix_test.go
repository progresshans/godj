//go:build darwin || linux

package projectmigratetargetproduct_test

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
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
	"github.com/progresshans/godj/conformance/internal/dbstate"
	"github.com/progresshans/godj/conformance/internal/testfixture"
)

var targetPostgresSchemaSequence atomic.Uint64

type targetPostgresNamespace struct {
	oid   uint32
	owner string
	acl   string
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
	dbstate.PostgresCatalog
	namespace targetPostgresNamespace
	counts    map[string]int64
	history   []dbstate.HistoryRow
	revisions []targetPostgresRevision
	values    map[string][]targetPostgresValue
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
		t.Fatalf("connect PostgreSQL schema owner: %v", testfixture.PostgresSafeError(err))
	}
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := connection.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("create isolated targeted migrate PostgreSQL schema: %v", testfixture.PostgresSafeError(err))
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		cleanup, err := pgx.Connect(cleanupCtx, databaseURL)
		if err != nil {
			t.Errorf("connect targeted migrate PostgreSQL schema cleanup: %v", testfixture.PostgresSafeError(err))
			return
		}
		if _, err := cleanup.Exec(cleanupCtx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Errorf("drop targeted migrate PostgreSQL schema: %v", testfixture.PostgresSafeError(err))
		}
		if err := cleanup.Close(cleanupCtx); err != nil {
			t.Errorf("close targeted migrate PostgreSQL schema cleanup: %v", testfixture.PostgresSafeError(err))
		}
	})
	if err := connection.Close(ctx); err != nil {
		t.Fatalf("close PostgreSQL schema owner: %v", testfixture.PostgresSafeError(err))
	}
	return schema
}

func targetCapturePostgres(t *testing.T, databaseURL, schema string) targetPostgresSnapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect targeted migrate PostgreSQL inspector: %v", testfixture.PostgresSafeError(err))
	}
	defer func() {
		if err := connection.Close(ctx); err != nil {
			t.Errorf("close targeted migrate PostgreSQL inspector: %v", testfixture.PostgresSafeError(err))
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
		t.Fatalf("inspect targeted migrate PostgreSQL namespace: %v", testfixture.PostgresSafeError(err))
	}

	snapshot.PostgresCatalog, err = dbstate.CapturePostgresCatalog(ctx, connection, schema)
	if err != nil {
		t.Fatalf("inspect targeted migrate PostgreSQL catalog: %v", testfixture.PostgresSafeError(err))
	}
	var rows pgx.Rows

	for _, table := range snapshot.Tables {
		quoted := pgx.Identifier{schema, table}.Sanitize()
		var count int64
		if err := connection.QueryRow(ctx, "SELECT COUNT(*) FROM "+quoted).Scan(&count); err != nil {
			t.Fatalf("count targeted migrate PostgreSQL table: %v", testfixture.PostgresSafeError(err))
		}
		snapshot.counts[table] = count
		if strings.HasPrefix(table, "target_") {
			rows, err = connection.Query(ctx, `SELECT "id", "value" FROM `+quoted+` ORDER BY "id"`)
			if err != nil {
				t.Fatalf("inspect targeted migrate PostgreSQL sentinel rows: %v", testfixture.PostgresSafeError(err))
			}
			snapshot.values[table] = make([]targetPostgresValue, 0)
			for rows.Next() {
				var value targetPostgresValue
				if err := rows.Scan(&value.id, &value.value); err != nil {
					rows.Close()
					t.Fatalf("scan targeted migrate PostgreSQL sentinel: %v", testfixture.PostgresSafeError(err))
				}
				snapshot.values[table] = append(snapshot.values[table], value)
			}
			targetClosePostgresRows(t, rows, "sentinel rows")
		}
	}

	if targetPostgresHasTable(snapshot, "godj_migrations") {
		rows, err = connection.Query(ctx, `SELECT "app", "name" FROM `+pgx.Identifier{schema, "godj_migrations"}.Sanitize()+` ORDER BY "app", "name"`)
		if err != nil {
			t.Fatalf("inspect targeted migrate PostgreSQL history: %v", testfixture.PostgresSafeError(err))
		}
		for rows.Next() {
			var history dbstate.HistoryRow
			if err := rows.Scan(&history.App, &history.Name); err != nil {
				rows.Close()
				t.Fatalf("scan targeted migrate PostgreSQL history: %v", testfixture.PostgresSafeError(err))
			}
			snapshot.history = append(snapshot.history, history)
		}
		targetClosePostgresRows(t, rows, "history")
	}
	if targetPostgresHasTable(snapshot, "godj_migration_revision") {
		rows, err = connection.Query(ctx, `SELECT "singleton", "format_version", "epoch", "revision", "history_fingerprint"
			FROM `+pgx.Identifier{schema, "godj_migration_revision"}.Sanitize()+` ORDER BY "singleton"`)
		if err != nil {
			t.Fatalf("inspect targeted migrate PostgreSQL revision: %v", testfixture.PostgresSafeError(err))
		}
		for rows.Next() {
			var revision targetPostgresRevision
			var epoch, fingerprint []byte
			if err := rows.Scan(&revision.singleton, &revision.format, &epoch, &revision.revision, &fingerprint); err != nil {
				rows.Close()
				t.Fatalf("scan targeted migrate PostgreSQL revision: %v", testfixture.PostgresSafeError(err))
			}
			revision.epoch = hex.EncodeToString(epoch)
			revision.fingerprint = hex.EncodeToString(fingerprint)
			snapshot.revisions = append(snapshot.revisions, revision)
		}
		targetClosePostgresRows(t, rows, "revision")
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
		t.Fatalf("finish targeted migrate PostgreSQL %s query: %v", operation, testfixture.PostgresSafeError(err))
	}
}

func targetPostgresHasTable(snapshot targetPostgresSnapshot, table string) bool {
	for _, candidate := range snapshot.Tables {
		if candidate == table {
			return true
		}
	}
	return false
}

func targetAssertPostgresEmpty(t *testing.T, snapshot targetPostgresSnapshot) {
	t.Helper()
	if snapshot.namespace.oid == 0 || snapshot.namespace.owner == "" || len(snapshot.Tables) != 0 ||
		len(snapshot.OtherRelations) != 0 || len(snapshot.Columns) != 0 || len(snapshot.Constraints) != 0 ||
		len(snapshot.Indexes) != 0 || snapshot.Triggers != 0 || snapshot.Policies != 0 || snapshot.Rules != 0 ||
		len(snapshot.counts) != 0 || len(snapshot.history) != 0 || len(snapshot.revisions) != 0 ||
		len(snapshot.Sequences) != 0 || len(snapshot.values) != 0 {
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
	history []dbstate.HistoryRow,
	values map[string][]targetPostgresValue,
) targetPostgresSnapshot {
	t.Helper()
	snapshot := targetCapturePostgres(t, databaseURL, schema)
	wantTables := append([]string{"godj_migration_revision", "godj_migrations"}, tables...)
	sort.Strings(wantTables)
	if !reflect.DeepEqual(snapshot.Tables, wantTables) || len(snapshot.OtherRelations) != 0 {
		t.Fatalf("targeted migrate PostgreSQL tables/other relations = %v/%v, want exact %v/empty", snapshot.Tables, snapshot.OtherRelations, wantTables)
	}
	if !reflect.DeepEqual(snapshot.Columns, targetExpectedPostgresColumns(tables)) {
		t.Fatalf("targeted migrate PostgreSQL columns differ from exact current profile: %+v", snapshot.Columns)
	}
	if !reflect.DeepEqual(snapshot.Constraints, targetExpectedPostgresConstraints(tables)) {
		t.Fatalf("targeted migrate PostgreSQL constraints differ from exact current profile: %+v", snapshot.Constraints)
	}
	if !reflect.DeepEqual(snapshot.Indexes, targetExpectedPostgresIndexes(tables)) {
		t.Fatalf("targeted migrate PostgreSQL indexes differ from exact current profile: %+v", snapshot.Indexes)
	}
	if snapshot.Triggers != 0 || snapshot.Policies != 0 || snapshot.Rules != 0 {
		t.Fatalf("targeted migrate PostgreSQL trigger/policy/rule counts = %d/%d/%d, want zero", snapshot.Triggers, snapshot.Policies, snapshot.Rules)
	}
	canonicalHistory := append([]dbstate.HistoryRow(nil), history...)
	sort.Slice(canonicalHistory, func(left, right int) bool {
		if canonicalHistory[left].App != canonicalHistory[right].App {
			return canonicalHistory[left].App < canonicalHistory[right].App
		}
		return canonicalHistory[left].Name < canonicalHistory[right].Name
	})
	if !reflect.DeepEqual(snapshot.history, canonicalHistory) {
		t.Fatalf("targeted migrate PostgreSQL history = %+v, want %+v", snapshot.history, canonicalHistory)
	}
	wantFingerprint := dbstate.FingerprintHistory(canonicalHistory)
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
	if !reflect.DeepEqual(snapshot.Sequences, targetExpectedPostgresSequences(tables, wantValues)) {
		t.Fatalf("targeted migrate PostgreSQL sequences differ from exact AutoField profile: %+v", snapshot.Sequences)
	}
	return snapshot
}

func targetExpectedPostgresColumns(tables []string) []dbstate.PostgresColumn {
	column := func(table string, ordinal int, name, typeName string, notNull bool, identity string, primary bool) dbstate.PostgresColumn {
		return dbstate.PostgresColumn{
			Table: table, Ordinal: ordinal, Name: name, DataType: typeName, NotNull: notNull,
			Identity: identity, DefaultCollation: true, Primary: primary,
		}
	}
	result := []dbstate.PostgresColumn{
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
		if result[left].Table != result[right].Table {
			return result[left].Table < result[right].Table
		}
		return result[left].Ordinal < result[right].Ordinal
	})
	return result
}

func targetExpectedPostgresConstraints(tables []string) []dbstate.PostgresConstraint {
	primary := func(table, name, key string) dbstate.PostgresConstraint {
		return dbstate.PostgresConstraint{Table: table, Name: name, Kind: "p", Validated: true, Key: key, IndexName: name}
	}
	result := []dbstate.PostgresConstraint{
		primary("godj_migration_revision", "godj_migration_revision_pkey", "1"),
		primary("godj_migrations", "godj_migrations_pkey", "1,2"),
	}
	for _, table := range tables {
		result = append(result, primary(table, targetPostgresDerivedName("godj/postgres/primary-key/v1", "godj_pk_", table), "1"))
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Table != result[right].Table {
			return result[left].Table < result[right].Table
		}
		return result[left].Name < result[right].Name
	})
	return result
}

func targetExpectedPostgresIndexes(tables []string) []dbstate.PostgresIndex {
	primary := func(table, name, keys string, count int) dbstate.PostgresIndex {
		return dbstate.PostgresIndex{
			Table: table, Name: name, Primary: true, Unique: true, Valid: true, Ready: true, Live: true,
			KeyCount: count, AttributeCount: count, Keys: keys, Method: "btree",
		}
	}
	result := []dbstate.PostgresIndex{
		primary("godj_migration_revision", "godj_migration_revision_pkey", "singleton", 1),
		primary("godj_migrations", "godj_migrations_pkey", "app,name", 2),
	}
	for _, table := range tables {
		result = append(result, primary(table, targetPostgresDerivedName("godj/postgres/primary-key/v1", "godj_pk_", table), "id", 1))
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Table != result[right].Table {
			return result[left].Table < result[right].Table
		}
		return result[left].Name < result[right].Name
	})
	return result
}

func targetExpectedPostgresSequences(tables []string, values map[string][]targetPostgresValue) []dbstate.PostgresSequence {
	result := make([]dbstate.PostgresSequence, 0, len(tables))
	for _, table := range tables {
		last := int64(1)
		called := false
		if rows := values[table]; len(rows) > 0 {
			last = rows[len(rows)-1].id
			called = true
		}
		result = append(result, dbstate.PostgresSequence{
			Name: targetPostgresDerivedName("godj/postgres/identity-sequence/v1", "godj_seq_", table, "id"),
			Kind: "S", Persistence: "p", DataType: "bigint", Start: 1, Increment: 1, Minimum: 1,
			Maximum: math.MaxInt64, Cache: 1, OwnerTable: table, OwnerColumn: "id", Dependency: "i",
			Last: last, Called: called,
		})
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Name < result[right].Name })
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
		t.Fatalf("connect targeted migrate PostgreSQL sentinel writer: %v", testfixture.PostgresSafeError(err))
	}
	defer func() {
		if err := connection.Close(ctx); err != nil {
			t.Errorf("close targeted migrate PostgreSQL sentinel writer: %v", testfixture.PostgresSafeError(err))
		}
	}()
	var identifier int64
	if err := connection.QueryRow(ctx, `INSERT INTO `+pgx.Identifier{schema, table}.Sanitize()+` ("value") VALUES ($1) RETURNING "id"`, value).Scan(&identifier); err != nil {
		t.Fatalf("insert targeted migrate PostgreSQL sentinel: %v", testfixture.PostgresSafeError(err))
	}
	return identifier
}
