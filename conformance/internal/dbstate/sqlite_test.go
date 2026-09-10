package dbstate

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSQLiteSnapshotIncludesPhysicalRowsSchemaHistoryAndRevisionWithoutMutation(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.sqlite3")
	InitializeSQLite(t, path)
	empty := CaptureSQLite(t, path)
	if len(empty.Schema) != 0 || len(empty.History) != 0 || empty.Revision.Present {
		t.Fatal("initial snapshot is not empty")
	}
	write := func(statements ...string) {
		t.Helper()
		database, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		defer database.Close()
		for _, statement := range statements {
			if _, err := database.ExecContext(t.Context(), statement); err != nil {
				t.Fatal(err)
			}
		}
	}
	fingerprint := FingerprintHistory([]HistoryRow{{App: "app", Name: "0001"}})
	write(`CREATE TABLE "odd""table" ("value" TEXT)`, `INSERT INTO "odd""table" VALUES ('before')`,
		`CREATE TABLE "godj_migrations" ("app" TEXT, "name" TEXT)`, `INSERT INTO "godj_migrations" VALUES ('app', '0001')`,
		`CREATE TABLE "godj_migration_revision" ("singleton" INTEGER, "format_version" INTEGER, "epoch" BLOB, "revision" INTEGER, "history_fingerprint" BLOB)`,
		`INSERT INTO "godj_migration_revision" VALUES (1, 1, X'01', 7, X'`+hex.EncodeToString(fingerprint[:])+`')`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := CaptureSQLite(t, path)
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || snapshot.Digest != sha256.Sum256(before) {
		t.Fatal("snapshot changed or omitted physical file bytes")
	}
	if !HasTable(snapshot, `odd"table`) || !reflect.DeepEqual(snapshot.History, []HistoryRow{{App: "app", Name: "0001"}}) || !snapshot.Revision.Present || snapshot.Revision.Revision != 7 || snapshot.Revision.Fingerprint != hex.EncodeToString(fingerprint[:]) {
		t.Fatalf("snapshot lost schema/history/revision: %+v", snapshot)
	}
	if len(snapshot.Counts) != 3 {
		t.Fatalf("table counts = %+v", snapshot.Counts)
	}
	for _, count := range snapshot.Counts {
		if count.Count != 1 {
			t.Fatalf("table count = %+v", count)
		}
	}
	write(`UPDATE "odd""table" SET "value" = 'after'`)
	changed := CaptureSQLite(t, path)
	if changed.Digest == snapshot.Digest || !reflect.DeepEqual(changed.Counts, snapshot.Counts) {
		t.Fatal("same-count row mutation was not observed through physical bytes")
	}
	write(`ALTER TABLE "odd""table" ADD COLUMN "added" INTEGER`)
	if reflect.DeepEqual(CaptureSQLite(t, path).Schema, snapshot.Schema) {
		t.Fatal("schema mutation was not observed")
	}
}

func TestHistoryFingerprintIsOrderIndependentAndFramesFieldBoundaries(t *testing.T) {
	t.Parallel()
	rows := []HistoryRow{{App: "b", Name: "1"}, {App: "a", Name: "2"}}
	before := append([]HistoryRow(nil), rows...)
	if FingerprintHistory(rows) != FingerprintHistory([]HistoryRow{rows[1], rows[0]}) || !reflect.DeepEqual(rows, before) {
		t.Fatal("fingerprint depends on caller order or mutates input")
	}
	if FingerprintHistory([]HistoryRow{{App: "ab", Name: "c"}}) == FingerprintHistory([]HistoryRow{{App: "a", Name: "bc"}}) {
		t.Fatal("fingerprint did not frame fields independently")
	}
}
