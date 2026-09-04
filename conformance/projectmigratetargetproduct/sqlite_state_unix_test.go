//go:build darwin || linux

package projectmigratetargetproduct_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

type targetSQLiteSchemaObject struct {
	typeName string
	name     string
	table    string
	sql      string
}

type targetSQLiteTableCount struct {
	table string
	count int64
}

type targetSQLiteHistoryRow struct {
	app  string
	name string
}

type targetSQLiteRevisionRow struct {
	present     bool
	singleton   int64
	format      int64
	epoch       string
	revision    int64
	fingerprint string
}

type targetSQLiteSnapshot struct {
	digest   [sha256.Size]byte
	schema   []targetSQLiteSchemaObject
	counts   []targetSQLiteTableCount
	history  []targetSQLiteHistoryRow
	revision targetSQLiteRevisionRow
}

func targetHistory(app, name string) targetSQLiteHistoryRow {
	return targetSQLiteHistoryRow{app: app, name: name}
}

func targetInitializeSQLite(t *testing.T, path string) {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := database.ExecContext(ctx, "VACUUM"); err != nil {
		_ = database.Close()
		t.Fatalf("initialize empty SQLite file: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close initialized SQLite file: %v", err)
	}
	targetAssertNoSQLiteSidecars(t, path)
}

func targetCaptureSQLite(t *testing.T, path string) targetSQLiteSnapshot {
	t.Helper()
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("SQLite snapshot source %q is not a regular file: %v", path, err)
	}
	database, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := database.PingContext(ctx); err != nil {
		_ = database.Close()
		t.Fatalf("ping SQLite snapshot: %v", err)
	}

	snapshot := targetSQLiteSnapshot{}
	rows, err := database.QueryContext(ctx, `SELECT "type", "name", "tbl_name", COALESCE("sql", '')
		FROM "sqlite_schema"
		WHERE "name" NOT LIKE 'sqlite_%'
		ORDER BY "type", "name", "tbl_name", "sql"`)
	if err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	for rows.Next() {
		var object targetSQLiteSchemaObject
		if err := rows.Scan(&object.typeName, &object.name, &object.table, &object.sql); err != nil {
			_ = rows.Close()
			_ = database.Close()
			t.Fatal(err)
		}
		snapshot.schema = append(snapshot.schema, object)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}

	for _, object := range snapshot.schema {
		if object.typeName != "table" {
			continue
		}
		var count int64
		if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+targetQuoteSQLiteIdentifier(object.name)).Scan(&count); err != nil {
			_ = database.Close()
			t.Fatalf("count SQLite table %q: %v", object.name, err)
		}
		snapshot.counts = append(snapshot.counts, targetSQLiteTableCount{table: object.name, count: count})
	}

	if targetSnapshotHasTable(snapshot, "godj_migrations") {
		historyRows, err := database.QueryContext(ctx, `SELECT "app", "name" FROM "godj_migrations" ORDER BY "app", "name"`)
		if err != nil {
			_ = database.Close()
			t.Fatal(err)
		}
		for historyRows.Next() {
			var row targetSQLiteHistoryRow
			if err := historyRows.Scan(&row.app, &row.name); err != nil {
				_ = historyRows.Close()
				_ = database.Close()
				t.Fatal(err)
			}
			snapshot.history = append(snapshot.history, row)
		}
		if err := errors.Join(historyRows.Err(), historyRows.Close()); err != nil {
			_ = database.Close()
			t.Fatal(err)
		}
	}
	if targetSnapshotHasTable(snapshot, "godj_migration_revision") {
		var epoch, fingerprint []byte
		row := targetSQLiteRevisionRow{present: true}
		if err := database.QueryRowContext(ctx, `SELECT "singleton", "format_version", "epoch", "revision", "history_fingerprint"
			FROM "godj_migration_revision"`).Scan(&row.singleton, &row.format, &epoch, &row.revision, &fingerprint); err != nil {
			_ = database.Close()
			t.Fatal(err)
		}
		row.epoch = hex.EncodeToString(epoch)
		row.fingerprint = hex.EncodeToString(fingerprint)
		snapshot.revision = row
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close SQLite snapshot: %v", err)
	}

	document, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.digest = sha256.Sum256(document)
	targetAssertNoSQLiteSidecars(t, path)
	return snapshot
}

func targetAssertSQLiteUnchanged(t *testing.T, path string, before targetSQLiteSnapshot) {
	t.Helper()
	after := targetCaptureSQLite(t, path)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("read-only migrate plan changed SQLite state\nbefore=%+v\nafter=%+v", before, after)
	}
}

func targetAssertInitializedEmpty(t *testing.T, snapshot targetSQLiteSnapshot) {
	t.Helper()
	if len(snapshot.schema) != 0 || len(snapshot.counts) != 0 || len(snapshot.history) != 0 || snapshot.revision.present {
		t.Fatalf("initialized empty SQLite state is not empty: %+v", snapshot)
	}
}

func targetAssertSQLiteHistory(t *testing.T, path string, want ...targetSQLiteHistoryRow) {
	t.Helper()
	snapshot := targetCaptureSQLite(t, path)
	canonical := append([]targetSQLiteHistoryRow(nil), want...)
	sort.Slice(canonical, func(left, right int) bool {
		if canonical[left].app != canonical[right].app {
			return canonical[left].app < canonical[right].app
		}
		return canonical[left].name < canonical[right].name
	})
	if !reflect.DeepEqual(snapshot.history, canonical) {
		t.Fatalf("SQLite migration history = %+v, want %+v", snapshot.history, canonical)
	}
	if !snapshot.revision.present || snapshot.revision.singleton != 1 || snapshot.revision.format != 1 ||
		len(snapshot.revision.epoch) != 32 || len(snapshot.revision.fingerprint) != sha256.Size*2 {
		t.Fatalf("SQLite revision row is not current and bounded: %+v", snapshot.revision)
	}
	if snapshot.revision.epoch == strings.Repeat("0", len(snapshot.revision.epoch)) {
		t.Fatal("SQLite revision epoch is all-zero")
	}
	wantFingerprint := targetFingerprintHistory(canonical)
	if snapshot.revision.fingerprint != hex.EncodeToString(wantFingerprint[:]) {
		t.Fatalf("SQLite revision fingerprint = %q, want %x", snapshot.revision.fingerprint, wantFingerprint)
	}
}

func targetSQLiteEpoch(t *testing.T, path string) string {
	t.Helper()
	snapshot := targetCaptureSQLite(t, path)
	if !snapshot.revision.present || len(snapshot.revision.epoch) != 32 || snapshot.revision.epoch == strings.Repeat("0", 32) {
		t.Fatalf("SQLite revision epoch is not current: %+v", snapshot.revision)
	}
	return snapshot.revision.epoch
}

func targetAssertSQLiteEpoch(t *testing.T, path, want string) {
	t.Helper()
	if got := targetSQLiteEpoch(t, path); got != want {
		t.Fatalf("SQLite revision epoch changed across one database lifecycle: got %q want %q", got, want)
	}
}

func targetAssertSQLiteRevision(t *testing.T, path string, want int64) {
	t.Helper()
	snapshot := targetCaptureSQLite(t, path)
	if snapshot.revision.revision != want {
		t.Fatalf("SQLite revision = %d, want %d", snapshot.revision.revision, want)
	}
}

func targetAssertSQLiteTables(t *testing.T, path string, present ...string) {
	t.Helper()
	snapshot := targetCaptureSQLite(t, path)
	got := make([]string, 0, len(snapshot.schema))
	for _, object := range snapshot.schema {
		if object.typeName != "table" || object.name == "godj_migrations" || object.name == "godj_migration_revision" {
			continue
		}
		got = append(got, object.name)
	}
	want := append([]string(nil), present...)
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SQLite application tables = %v, want exact %v", got, want)
	}
}

func targetAssertSQLiteTablesAbsent(t *testing.T, path string, absent ...string) {
	t.Helper()
	snapshot := targetCaptureSQLite(t, path)
	for _, name := range absent {
		if targetSnapshotHasTable(snapshot, name) {
			t.Errorf("SQLite table %q is present", name)
		}
	}
}

func targetInsertSQLiteValue(t *testing.T, path, table, value string) int64 {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := database.ExecContext(ctx, `INSERT INTO `+targetQuoteSQLiteIdentifier(table)+` ("value") VALUES (?)`, value)
	if err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	targetAssertNoSQLiteSidecars(t, path)
	return id
}

func targetSQLiteValues(t *testing.T, path, table string) []string {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rows, err := database.QueryContext(ctx, `SELECT "value" FROM `+targetQuoteSQLiteIdentifier(table)+` ORDER BY "id"`)
	if err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			_ = rows.Close()
			_ = database.Close()
			t.Fatal(err)
		}
		values = append(values, value)
	}
	if err := errors.Join(rows.Err(), rows.Close(), database.Close()); err != nil {
		t.Fatal(err)
	}
	targetAssertNoSQLiteSidecars(t, path)
	return values
}

func targetSnapshotHasTable(snapshot targetSQLiteSnapshot, name string) bool {
	for _, object := range snapshot.schema {
		if object.typeName == "table" && object.name == name {
			return true
		}
	}
	return false
}

func targetAssertNoSQLiteSidecars(t *testing.T, path string) {
	t.Helper()
	for _, suffix := range []string{"-journal", "-wal", "-shm"} {
		candidate := path + suffix
		if _, err := os.Lstat(candidate); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("SQLite sidecar %q remains: %v", candidate, err)
		}
	}
}

func targetQuoteSQLiteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func targetFingerprintHistory(records []targetSQLiteHistoryRow) [sha256.Size]byte {
	canonical := append([]targetSQLiteHistoryRow(nil), records...)
	sort.Slice(canonical, func(left, right int) bool {
		if canonical[left].app != canonical[right].app {
			return canonical[left].app < canonical[right].app
		}
		return canonical[left].name < canonical[right].name
	})
	hash := sha256.New()
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(canonical)))
	_, _ = hash.Write(length[:])
	for _, record := range canonical {
		for _, value := range []string{record.app, record.name} {
			binary.BigEndian.PutUint64(length[:], uint64(len(value)))
			_, _ = hash.Write(length[:])
			_, _ = hash.Write([]byte(value))
		}
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result
}
