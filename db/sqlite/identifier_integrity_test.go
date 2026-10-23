package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/query"
)

// A missing model field must never be returned as a string bearing its name.
// Cover actual pool growth/replacement and reopening, including an attempted
// DSN override. Reload's generated Note consumer separately exercises this
// boundary before and after its explicit nullable-column migration.
func TestSQLitePhysicalConnectionsRejectMissingQuotedColumns(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "identifiers.sqlite3")
	backend, err := Open(ctx, "file:"+filepath.ToSlash(path)+"?_dqs=1&_dqs=true&_pragma=foreign_keys(0)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	for _, statement := range []string{
		`CREATE TABLE "note" ("id" INTEGER PRIMARY KEY, "title" TEXT NOT NULL)`,
		`INSERT INTO "note" ("title") VALUES ('before reload')`,
	} {
		if _, err := backend.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	backend.database.SetMaxOpenConns(3)
	var held []*sql.Conn
	for index := 0; index < 3; index++ {
		connection, err := backend.database.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, connection)
		t.Cleanup(func() { _ = connection.Close() })
		assertSQLiteIdentifierIntegrity(t, ctx, connection)
	}
	for _, connection := range held {
		if err := connection.Close(); err != nil {
			t.Fatal(err)
		}
	}
	backend.database.SetMaxIdleConns(0)
	replacement, err := backend.database.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertSQLiteIdentifierIntegrity(t, ctx, replacement)
	if err := replacement.Close(); err != nil {
		t.Fatal(err)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, "file:"+filepath.ToSlash(path)+"?mode=ro&_dqs=1")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	fields := []query.FieldRef{
		query.NewFieldRef("title", "title", query.FieldString, false),
		query.NewFieldRef("summary", "summary", query.FieldString, true),
	}
	rows, err := reopened.Query(ctx, query.NewPlan("note", fields))
	if rows != nil {
		_ = rows.Close()
		t.Fatal("unmigrated model returned rows")
	}
	if err == nil {
		t.Fatal("unmigrated model returned no error")
	}
	if _, err := reopened.ExecContext(ctx, `INSERT INTO "note" ("title") VALUES ('must stay read only')`); err == nil {
		t.Fatal("connection options lost read-only mode")
	}
}

func assertSQLiteIdentifierIntegrity(t *testing.T, ctx context.Context, connection *sql.Conn) {
	t.Helper()
	var title string
	if err := connection.QueryRowContext(ctx, `SELECT "title" FROM "note"`).Scan(&title); err != nil || title != "before reload" {
		t.Fatal("existing quoted field changed", title, err)
	}
	var summary *string
	if err := connection.QueryRowContext(ctx, `SELECT "summary" FROM "note"`).Scan(&summary); err == nil {
		t.Fatal("missing field was interpreted as a string literal")
	}
	if _, err := connection.ExecContext(ctx, `CREATE TABLE "invalid_check" ("id" INTEGER CHECK("missing_column" > 0))`); err == nil {
		t.Fatal("DDL accepted a missing field as a string literal")
	}
	var foreignKeys int
	if err := connection.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatal("identifier policy weakened foreign keys", foreignKeys, err)
	}
}

func TestSQLiteTemporaryAndMemoryConnectionsKeepTheirLifetime(t *testing.T) {
	for _, dsn := range []string{"", ":memory:", "file::memory:?cache=shared&_dqs=true"} {
		t.Run(dsn, func(t *testing.T) {
			backend, err := Open(t.Context(), dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer backend.Close()
			backend.database.SetMaxOpenConns(1)
			var name, file string
			var sequence int
			if err := backend.database.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&sequence, &name, &file); err != nil || name != "main" || file != "" {
				t.Fatal("private temporary database became a persistent file", name, file, err)
			}
			var value string
			if err := backend.database.QueryRowContext(t.Context(), `SELECT "missing_identifier"`).Scan(&value); err == nil {
				t.Fatal("temporary connection admitted a quoted string")
			}
		})
	}
}
