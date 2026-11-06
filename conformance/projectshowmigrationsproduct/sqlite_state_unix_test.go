//go:build darwin || linux

package projectshowmigrationsproduct_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"github.com/progresshans/godj/conformance/internal/dbstate"
	"reflect"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func externalStatusAssertSQLiteUnchanged(t *testing.T, before, after dbstate.Snapshot) {
	t.Helper()
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("read-only showmigrations changed SQLite state\nbefore=%+v\nafter=%+v", before, after)
	}
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
	dbstate.AssertNoSQLiteSidecars(t, path)
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
	var history []dbstate.HistoryRow
	for rows.Next() {
		var row dbstate.HistoryRow
		if err := rows.Scan(&row.App, &row.Name); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		history = append(history, row)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	fingerprint := dbstate.FingerprintHistory(history)
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
	dbstate.AssertNoSQLiteSidecars(t, path)
}

func externalStatusAssertExpectedHistory(t *testing.T, snapshot dbstate.Snapshot, want ...dbstate.HistoryRow) {
	t.Helper()
	if !reflect.DeepEqual(snapshot.History, want) {
		t.Fatalf("SQLite migration history = %+v, want %+v", snapshot.History, want)
	}
	if !snapshot.Revision.Present || snapshot.Revision.Format != 1 || snapshot.Revision.Singleton != 1 ||
		len(snapshot.Revision.Epoch) != 32 || len(snapshot.Revision.Fingerprint) != sha256.Size*2 {
		t.Fatalf("SQLite revision row is not current and bounded: %+v", snapshot.Revision)
	}
	wantFingerprint := dbstate.FingerprintHistory(want)
	if snapshot.Revision.Fingerprint != hex.EncodeToString(wantFingerprint[:]) {
		t.Fatalf("SQLite revision fingerprint = %q, want %x", snapshot.Revision.Fingerprint, wantFingerprint)
	}
}

func externalStatusAssertInitializedEmpty(t *testing.T, snapshot dbstate.Snapshot) {
	t.Helper()
	if len(snapshot.Schema) != 0 || len(snapshot.Counts) != 0 || len(snapshot.History) != 0 || snapshot.Revision.Present {
		t.Fatalf("initialized empty SQLite state is not empty: %+v", snapshot)
	}
}

func externalStatusAssertRevisionCount(t *testing.T, snapshot dbstate.Snapshot, want int64) {
	t.Helper()
	if snapshot.Revision.Revision != want {
		t.Fatalf("SQLite revision = %d, want %d", snapshot.Revision.Revision, want)
	}
}

func externalStatusMigrateSetup(t *testing.T, project *externalStatusProject, environment []string, database, marker string) {
	t.Helper()
	result := project.runMigrate(t, environment)
	externalStatusAssertRedacted(t, result, project.sensitive(database)...)
	if result.ExitCode != 0 || result.Stderr != "" || result.Stdout == "" {
		t.Fatalf("external migrate setup failed: exit=%d stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	externalStatusResetMarker(t, marker)
}
