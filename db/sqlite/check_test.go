package sqlite

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/query"
)

func TestSQLiteCheckReadsExistingDatabaseWithoutChangingItsBytes(t *testing.T) {
	file := filepath.Join(t.TempDir(), "기존 data #.sqlite3")
	backend, err := Open(t.Context(), file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.ExecContext(t.Context(), "CREATE TABLE owned (id INTEGER PRIMARY KEY, value TEXT); INSERT INTO owned(value) VALUES ('preserved')"); err != nil {
		_ = backend.Close()
		t.Fatal(err)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(file)}).String()
	for _, dsn := range []string{file, uri, uri + "?mode=rwc", uri + "?mode=rw&cache=private&_busy_timeout=1000", uri + "?mode=ro&cache=shared"} {
		if err := Check(t.Context(), dsn); err != nil {
			t.Fatal("existing database check failed", err)
		}
		after, err := os.ReadFile(file)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("check changed stored database bytes", err)
		}
	}
	// URI escaping must still select a filename containing a literal question
	// mark; an ordinary driver DSN would instead interpret it as parameters.
	escaped := filepath.Join(filepath.Dir(file), "question?.sqlite3")
	if err := os.WriteFile(escaped, before, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Check(t.Context(), (&url.URL{Scheme: "file", Path: escaped}).String()); err != nil {
		t.Fatal("escaped URI selected another database", err)
	}
	entries, err := os.ReadDir(filepath.Dir(file))
	if err != nil || len(entries) != 2 {
		t.Fatal("read check left unexpected files", err)
	}
}

func TestSQLiteCheckNeverCreatesOrRepairsMissingOrInvalidFiles(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "missing.sqlite3")
	for _, dsn := range []string{missing, (&url.URL{Scheme: "file", Path: missing, RawQuery: "mode=rwc"}).String()} {
		if err := Check(t.Context(), dsn); err == nil {
			t.Fatal("missing database reported healthy")
		}
		if _, err := os.Lstat(missing); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("check created a missing database", err)
		}
	}
	invalid := filepath.Join(root, "invalid.sqlite3")
	original := bytes.Repeat([]byte("invalid-database-bytes"), 32)
	if err := os.WriteFile(invalid, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Check(t.Context(), invalid); err == nil {
		t.Fatal("database header was not read")
	}
	if after, err := os.ReadFile(invalid); err != nil || !bytes.Equal(after, original) {
		t.Fatal("invalid database was repaired", err)
	}
	if err := Check(t.Context(), root); err == nil {
		t.Fatal("directory reported as a database")
	}
	if err := Check(nil, missing); err == nil {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := Check(ctx, missing); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation did not precede I/O", err)
	}
}

func TestSQLiteCheckRejectsInitializationSQLAndAmbiguousTargetsBeforeIO(t *testing.T) {
	file := filepath.Join(t.TempDir(), "unopened.sqlite3")
	uri := (&url.URL{Scheme: "file", Path: file}).String()
	for _, dsn := range []string{
		"", ":memory:", "file::memory:", "file:?cache=shared", "file:temporary?mode=memory", "file://other-host/path", "file://user@localhost/path",
		uri + "?mode=ro&mode=rwc", uri + "?mode=memory", uri + "?cache=unknown", uri + "?cache=private&cache=shared",
		uri + "?_pragma=journal_mode(WAL)", uri + "?_pragma=" + url.QueryEscape("foreign_keys=ON; ATTACH DATABASE 'outside.sqlite3' AS outside"),
		uri + "?_journal=WAL", uri + "?vfs=private", uri + "?immutable=1", uri + "?_busy_timeout=-1", uri + "?_busy_timeout=01", uri + "?_busy_timeout=2147483648", uri + "?mode=ro;private=value", uri + "#ignored",
		"file:%00private", "file:%zz", "private\x00value",
	} {
		err := Check(t.Context(), dsn)
		if !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) || strings.Contains(err.Error(), "private=value") || strings.Contains(err.Error(), file) {
			t.Fatal("unsafe DSN reached I/O or exposed its value", err)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(file))
	if err != nil || len(entries) != 0 {
		t.Fatal("rejected check created files", err)
	}
}

func TestSQLiteCheckPreservesRelativeDriverTarget(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	backend, err := Open(t.Context(), "relative.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.ExecContext(t.Context(), "CREATE TABLE owned (id INTEGER)"); err != nil {
		_ = backend.Close()
		t.Fatal(err)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	for _, dsn := range []string{"relative.sqlite3?_busy_timeout=0", "file:relative.sqlite3?mode=rwc", "file:./relative.sqlite3?mode=rw"} {
		if err := Check(t.Context(), dsn); err != nil {
			t.Fatal("relative target changed", err)
		}
	}
	data, err := os.ReadFile("relative.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("?leading.sqlite3", data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Check(t.Context(), "?leading.sqlite3"); err != nil {
		t.Fatal("driver's literal leading question mark was treated as a query", err)
	}
}

func TestSQLiteCheckCancellationReleasesItsReadConnection(t *testing.T) {
	file := filepath.Join(t.TempDir(), "locked.sqlite3")
	writer, err := Open(t.Context(), file)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := writer.ExecContext(t.Context(), "CREATE TABLE owned (id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	connection, err := writer.database.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(t.Context(), "BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	defer connection.ExecContext(context.Background(), "ROLLBACK")
	ctx, cancel := context.WithCancel(t.Context())
	deadline := time.AfterFunc(25*time.Millisecond, cancel)
	defer deadline.Stop()
	defer cancel()
	uri := (&url.URL{Scheme: "file", Path: file, RawQuery: "_busy_timeout=2000"}).String()
	err = Check(ctx, uri)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("locked read did not stop on cancellation", err)
	}
	if _, err := connection.ExecContext(t.Context(), "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	if err := Check(t.Context(), uri); err != nil {
		t.Fatal("canceled check retained a connection or lock", err)
	}
}

func TestSQLiteCheckDoesNotLexicallyRedirectASymlinkParentTarget(t *testing.T) {
	root := t.TempDir()
	base, actual := filepath.Join(root, "base"), filepath.Join(root, "actual")
	for _, path := range []string{base, filepath.Join(actual, "child")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	good := filepath.Join(base, "selected.sqlite3")
	backend, err := Open(t.Context(), good)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.ExecContext(t.Context(), "CREATE TABLE owned (id INTEGER)"); err != nil {
		_ = backend.Close()
		t.Fatal(err)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(actual, "selected.sqlite3")
	original := bytes.Repeat([]byte("not-a-database"), 32)
	if err := os.WriteFile(bad, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(actual, "child"), filepath.Join(base, "link")); err != nil {
		t.Fatal(err)
	}
	target := base + string(filepath.Separator) + "link" + string(filepath.Separator) + ".." + string(filepath.Separator) + "selected.sqlite3"
	// Check's result must agree with the unmodified driver's target, rather
	// than accepting the different healthy file selected by filepath.Clean.
	backend, openErr := Open(t.Context(), target)
	if openErr == nil {
		var count int
		openErr = backend.database.QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_schema").Scan(&count)
		if err := backend.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if openErr == nil {
		t.Fatal("symlink parent negative control did not select the invalid database")
	}
	if err := Check(t.Context(), target); err == nil {
		t.Fatal("check silently selected a different database")
	}
	if after, err := os.ReadFile(bad); err != nil || !bytes.Equal(after, original) {
		t.Fatal("check changed the configured target", err)
	}
}
