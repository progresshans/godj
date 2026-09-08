//go:build darwin || linux

package projectmigratetargetproduct_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"github.com/progresshans/godj/conformance/internal/dbstate"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func targetHistory(app, name string) dbstate.HistoryRow {
	return dbstate.HistoryRow{App: app, Name: name}
}

func targetAssertSQLiteUnchanged(t *testing.T, path string, before dbstate.Snapshot) {
	t.Helper()
	after := dbstate.CaptureSQLite(t, path)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("read-only migrate plan changed SQLite state\nbefore=%+v\nafter=%+v", before, after)
	}
}

func targetAssertSQLiteHistory(t *testing.T, path string, want ...dbstate.HistoryRow) {
	t.Helper()
	snapshot := dbstate.CaptureSQLite(t, path)
	canonical := append([]dbstate.HistoryRow(nil), want...)
	sort.Slice(canonical, func(left, right int) bool {
		if canonical[left].App != canonical[right].App {
			return canonical[left].App < canonical[right].App
		}
		return canonical[left].Name < canonical[right].Name
	})
	if !reflect.DeepEqual(snapshot.History, canonical) {
		t.Fatalf("SQLite migration history = %+v, want %+v", snapshot.History, canonical)
	}
	if !snapshot.Revision.Present || snapshot.Revision.Singleton != 1 || snapshot.Revision.Format != 1 ||
		len(snapshot.Revision.Epoch) != 32 || len(snapshot.Revision.Fingerprint) != sha256.Size*2 {
		t.Fatalf("SQLite revision row is not current and bounded: %+v", snapshot.Revision)
	}
	if snapshot.Revision.Epoch == strings.Repeat("0", len(snapshot.Revision.Epoch)) {
		t.Fatal("SQLite revision epoch is all-zero")
	}
	wantFingerprint := dbstate.FingerprintHistory(canonical)
	if snapshot.Revision.Fingerprint != hex.EncodeToString(wantFingerprint[:]) {
		t.Fatalf("SQLite revision fingerprint = %q, want %x", snapshot.Revision.Fingerprint, wantFingerprint)
	}
}

func targetSQLiteEpoch(t *testing.T, path string) string {
	t.Helper()
	snapshot := dbstate.CaptureSQLite(t, path)
	if !snapshot.Revision.Present || len(snapshot.Revision.Epoch) != 32 || snapshot.Revision.Epoch == strings.Repeat("0", 32) {
		t.Fatalf("SQLite revision epoch is not current: %+v", snapshot.Revision)
	}
	return snapshot.Revision.Epoch
}

func targetAssertSQLiteEpoch(t *testing.T, path, want string) {
	t.Helper()
	if got := targetSQLiteEpoch(t, path); got != want {
		t.Fatalf("SQLite revision epoch changed across one database lifecycle: got %q want %q", got, want)
	}
}

func targetAssertSQLiteRevision(t *testing.T, path string, want int64) {
	t.Helper()
	snapshot := dbstate.CaptureSQLite(t, path)
	if snapshot.Revision.Revision != want {
		t.Fatalf("SQLite revision = %d, want %d", snapshot.Revision.Revision, want)
	}
}

func targetAssertSQLiteTables(t *testing.T, path string, present ...string) {
	t.Helper()
	snapshot := dbstate.CaptureSQLite(t, path)
	got := make([]string, 0, len(snapshot.Schema))
	for _, object := range snapshot.Schema {
		if object.Type != "table" || object.Name == "godj_migrations" || object.Name == "godj_migration_revision" {
			continue
		}
		got = append(got, object.Name)
	}
	want := append([]string(nil), present...)
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SQLite application tables = %v, want exact %v", got, want)
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
	result, err := database.ExecContext(ctx, `INSERT INTO `+dbstate.QuoteSQLiteIdentifier(table)+` ("value") VALUES (?)`, value)
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
	dbstate.AssertNoSQLiteSidecars(t, path)
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
	rows, err := database.QueryContext(ctx, `SELECT "value" FROM `+dbstate.QuoteSQLiteIdentifier(table)+` ORDER BY "id"`)
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
	dbstate.AssertNoSQLiteSidecars(t, path)
	return values
}
