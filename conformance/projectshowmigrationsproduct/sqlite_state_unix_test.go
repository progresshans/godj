//go:build darwin || linux

package projectshowmigrationsproduct_test

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

type externalSQLiteSchemaObject struct {
	typeName string
	name     string
	table    string
	sql      string
}

type externalSQLiteTableCount struct {
	table string
	count int64
}

type externalSQLiteHistoryRow struct {
	app  string
	name string
}

type externalSQLiteRevisionRow struct {
	present     bool
	singleton   int64
	format      int64
	epoch       string
	revision    int64
	fingerprint string
}

type externalSQLiteSnapshot struct {
	digest   [sha256.Size]byte
	schema   []externalSQLiteSchemaObject
	counts   []externalSQLiteTableCount
	history  []externalSQLiteHistoryRow
	revision externalSQLiteRevisionRow
}

func externalStatusInitializeSQLite(t *testing.T, path string) {
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
	externalStatusAssertNoSQLiteSidecars(t, path)
}

func externalStatusCaptureSQLite(t *testing.T, path string) externalSQLiteSnapshot {
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

	snapshot := externalSQLiteSnapshot{}
	rows, err := database.QueryContext(ctx, `SELECT "type", "name", "tbl_name", COALESCE("sql", '')
		FROM "sqlite_schema"
		WHERE "name" NOT LIKE 'sqlite_%'
		ORDER BY "type", "name", "tbl_name", "sql"`)
	if err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	for rows.Next() {
		var object externalSQLiteSchemaObject
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
		statement := `SELECT COUNT(*) FROM ` + externalStatusQuoteSQLiteIdentifier(object.name)
		if err := database.QueryRowContext(ctx, statement).Scan(&count); err != nil {
			_ = database.Close()
			t.Fatalf("count SQLite table %q: %v", object.name, err)
		}
		snapshot.counts = append(snapshot.counts, externalSQLiteTableCount{table: object.name, count: count})
	}

	if externalStatusSnapshotHasTable(snapshot, "godj_migrations") {
		historyRows, err := database.QueryContext(ctx, `SELECT "app", "name" FROM "godj_migrations" ORDER BY "app", "name"`)
		if err != nil {
			_ = database.Close()
			t.Fatal(err)
		}
		for historyRows.Next() {
			var row externalSQLiteHistoryRow
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
	if externalStatusSnapshotHasTable(snapshot, "godj_migration_revision") {
		var epoch, fingerprint []byte
		row := externalSQLiteRevisionRow{present: true}
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
	externalStatusAssertNoSQLiteSidecars(t, path)
	return snapshot
}

func externalStatusSnapshotHasTable(snapshot externalSQLiteSnapshot, name string) bool {
	for _, object := range snapshot.schema {
		if object.typeName == "table" && object.name == name {
			return true
		}
	}
	return false
}

func externalStatusAssertSQLiteUnchanged(t *testing.T, before, after externalSQLiteSnapshot) {
	t.Helper()
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("read-only showmigrations changed SQLite state\nbefore=%+v\nafter=%+v", before, after)
	}
}

func externalStatusAssertNoSQLiteSidecars(t *testing.T, path string) {
	t.Helper()
	for _, suffix := range []string{"-journal", "-wal", "-shm"} {
		candidate := path + suffix
		if _, err := os.Lstat(candidate); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("SQLite sidecar %q remains: %v", candidate, err)
		}
	}
}

func externalStatusQuoteSQLiteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func externalStatusSeedApplicationRows(t *testing.T, path string) {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := database.ExecContext(ctx, `INSERT INTO "authors_author" ("name") VALUES (?)`, "durable author sentinel"); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	var publishedColumns int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('blog_article') WHERE "name" = 'published'`).Scan(&publishedColumns); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	statement := `INSERT INTO "blog_article" ("title") VALUES (?)`
	arguments := []any{"durable article sentinel"}
	if publishedColumns == 1 {
		statement = `INSERT INTO "blog_article" ("title", "published") VALUES (?, ?)`
		arguments = append(arguments, false)
	} else if publishedColumns != 0 {
		_ = database.Close()
		t.Fatalf("blog_article published column count = %d, want 0 or 1", publishedColumns)
	}
	if _, err := database.ExecContext(ctx, statement, arguments...); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	externalStatusAssertNoSQLiteSidecars(t, path)
}

func externalStatusInstallInconsistentHistory(t *testing.T, path string) {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = transaction.Rollback()
		}
		if database != nil {
			_ = database.Close()
		}
	}()
	result, err := transaction.ExecContext(ctx, `DELETE FROM "godj_migrations" WHERE "app" = ? AND "name" = ?`, "authors", "0001_author")
	if err != nil {
		t.Fatal(err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		t.Fatalf("remove applied known parent: affected=%d error=%v", affected, err)
	}
	rows, err := transaction.QueryContext(ctx, `SELECT "app", "name" FROM "godj_migrations" ORDER BY "app", "name"`)
	if err != nil {
		t.Fatal(err)
	}
	var history []externalSQLiteHistoryRow
	for rows.Next() {
		var row externalSQLiteHistoryRow
		if err := rows.Scan(&row.app, &row.name); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		history = append(history, row)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	fingerprint := externalStatusFingerprintHistory(history)
	update, err := transaction.ExecContext(ctx, `UPDATE "godj_migration_revision" SET "history_fingerprint" = ? WHERE "singleton" = 1`, fingerprint[:])
	if err != nil {
		t.Fatal(err)
	}
	updated, err := update.RowsAffected()
	if err != nil || updated != 1 {
		t.Fatalf("update inconsistent-history fingerprint: affected=%d error=%v", updated, err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	committed = true
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database = nil
	externalStatusAssertNoSQLiteSidecars(t, path)
}

func externalStatusFingerprintHistory(records []externalSQLiteHistoryRow) [sha256.Size]byte {
	canonical := append([]externalSQLiteHistoryRow(nil), records...)
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

func externalStatusAssertExpectedHistory(t *testing.T, snapshot externalSQLiteSnapshot, want ...externalSQLiteHistoryRow) {
	t.Helper()
	if !reflect.DeepEqual(snapshot.history, want) {
		t.Fatalf("SQLite migration history = %+v, want %+v", snapshot.history, want)
	}
	if !snapshot.revision.present || snapshot.revision.format != 1 || snapshot.revision.singleton != 1 ||
		len(snapshot.revision.epoch) != 32 || len(snapshot.revision.fingerprint) != sha256.Size*2 {
		t.Fatalf("SQLite revision row is not current and bounded: %+v", snapshot.revision)
	}
	wantFingerprint := externalStatusFingerprintHistory(want)
	if snapshot.revision.fingerprint != hex.EncodeToString(wantFingerprint[:]) {
		t.Fatalf("SQLite revision fingerprint = %q, want %x", snapshot.revision.fingerprint, wantFingerprint)
	}
}

func externalStatusAssertInitializedEmpty(t *testing.T, snapshot externalSQLiteSnapshot) {
	t.Helper()
	if len(snapshot.schema) != 0 || len(snapshot.counts) != 0 || len(snapshot.history) != 0 || snapshot.revision.present {
		t.Fatalf("initialized empty SQLite state is not empty: %+v", snapshot)
	}
}

func externalStatusAssertRevisionCount(t *testing.T, snapshot externalSQLiteSnapshot, want int64) {
	t.Helper()
	if snapshot.revision.revision != want {
		t.Fatalf("SQLite revision = %d, want %d", snapshot.revision.revision, want)
	}
}

func externalStatusMigrateSetup(t *testing.T, project *externalStatusProject, environment []string, database, marker string) {
	t.Helper()
	result := project.runMigrate(t, environment)
	externalStatusAssertRedacted(t, result, project.sensitive(database)...)
	if result.exitCode != 0 || result.stderr != "" || result.stdout == "" {
		t.Fatalf("external migrate setup failed: exit=%d stdout=%q stderr=%q", result.exitCode, result.stdout, result.stderr)
	}
	externalStatusResetMarker(t, marker)
}
