// Package dbstate observes database state directly for product tests. It does
// not use GoDj migration readers or expected reference artifacts.
package dbstate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

type SchemaObject struct {
	Type  string
	Name  string
	Table string
	SQL   string
}

type TableCount struct {
	Table string
	Count int64
}

type HistoryRow struct {
	App  string
	Name string
}

type RevisionRow struct {
	Present     bool
	Singleton   int64
	Format      int64
	Epoch       string
	Revision    int64
	Fingerprint string
}

type Snapshot struct {
	Digest   [sha256.Size]byte
	Schema   []SchemaObject
	Counts   []TableCount
	History  []HistoryRow
	Revision RevisionRow
}

func InitializeSQLite(t *testing.T, path string) {
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
	AssertNoSQLiteSidecars(t, path)
}

func CaptureSQLite(t *testing.T, path string) Snapshot {
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

	snapshot := Snapshot{}
	rows, err := database.QueryContext(ctx, `SELECT "type", "name", "tbl_name", COALESCE("sql", '')
		FROM "sqlite_schema"
		WHERE "name" NOT LIKE 'sqlite_%'
		ORDER BY "type", "name", "tbl_name", "sql"`)
	if err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	for rows.Next() {
		var object SchemaObject
		if err := rows.Scan(&object.Type, &object.Name, &object.Table, &object.SQL); err != nil {
			_ = rows.Close()
			_ = database.Close()
			t.Fatal(err)
		}
		snapshot.Schema = append(snapshot.Schema, object)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}

	for _, object := range snapshot.Schema {
		if object.Type != "table" {
			continue
		}
		var count int64
		if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+QuoteSQLiteIdentifier(object.Name)).Scan(&count); err != nil {
			_ = database.Close()
			t.Fatalf("count SQLite table %q: %v", object.Name, err)
		}
		snapshot.Counts = append(snapshot.Counts, TableCount{Table: object.Name, Count: count})
	}

	if HasTable(snapshot, "godj_migrations") {
		historyRows, err := database.QueryContext(ctx, `SELECT "app", "name" FROM "godj_migrations" ORDER BY "app", "name"`)
		if err != nil {
			_ = database.Close()
			t.Fatal(err)
		}
		for historyRows.Next() {
			var row HistoryRow
			if err := historyRows.Scan(&row.App, &row.Name); err != nil {
				_ = historyRows.Close()
				_ = database.Close()
				t.Fatal(err)
			}
			snapshot.History = append(snapshot.History, row)
		}
		if err := errors.Join(historyRows.Err(), historyRows.Close()); err != nil {
			_ = database.Close()
			t.Fatal(err)
		}
	}
	if HasTable(snapshot, "godj_migration_revision") {
		var epoch, fingerprint []byte
		row := RevisionRow{Present: true}
		if err := database.QueryRowContext(ctx, `SELECT "singleton", "format_version", "epoch", "revision", "history_fingerprint"
			FROM "godj_migration_revision"`).Scan(&row.Singleton, &row.Format, &epoch, &row.Revision, &fingerprint); err != nil {
			_ = database.Close()
			t.Fatal(err)
		}
		row.Epoch = hex.EncodeToString(epoch)
		row.Fingerprint = hex.EncodeToString(fingerprint)
		snapshot.Revision = row
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close SQLite snapshot: %v", err)
	}

	document, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Digest = sha256.Sum256(document)
	AssertNoSQLiteSidecars(t, path)
	return snapshot
}

func HasTable(snapshot Snapshot, name string) bool {
	for _, object := range snapshot.Schema {
		if object.Type == "table" && object.Name == name {
			return true
		}
	}
	return false
}

func AssertNoSQLiteSidecars(t *testing.T, path string) {
	t.Helper()
	for _, suffix := range []string{"-journal", "-wal", "-shm"} {
		candidate := path + suffix
		if _, err := os.Lstat(candidate); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("SQLite sidecar %q remains: %v", candidate, err)
		}
	}
}

func QuoteSQLiteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func FingerprintHistory(records []HistoryRow) [sha256.Size]byte {
	canonical := append([]HistoryRow(nil), records...)
	sort.Slice(canonical, func(left, right int) bool {
		if canonical[left].App != canonical[right].App {
			return canonical[left].App < canonical[right].App
		}
		return canonical[left].Name < canonical[right].Name
	})
	hash := sha256.New()
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(canonical)))
	_, _ = hash.Write(length[:])
	for _, record := range canonical {
		for _, value := range []string{record.App, record.Name} {
			binary.BigEndian.PutUint64(length[:], uint64(len(value)))
			_, _ = hash.Write(length[:])
			_, _ = hash.Write([]byte(value))
		}
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result
}
