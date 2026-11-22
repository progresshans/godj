package sqlite

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/internal/migrationdefaulttest"
	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

func TestSQLiteMigrationFieldNullability(t *testing.T) {
	migrationdefaulttest.RunFieldNullability(t, func(t *testing.T) func() migrationdefaulttest.Fixture {
		path := filepath.Join(t.TempDir(), "tickets.sqlite")
		return func() migrationdefaulttest.Fixture {
			backend := openMigrationHistoryFileBackend(t, path)
			return migrationdefaulttest.Fixture{Backend: backend, SQL: backend.database, Namespace: "main", Renderer: NewMigrationSQLRenderer(), SQLite: true}
		}
	}, func(err error) string {
		var cause *modernsqlite.Error
		if errors.As(err, &cause) {
			switch cause.Code() {
			case sqlite3.SQLITE_CONSTRAINT_NOTNULL:
				return "not_null"
			case sqlite3.SQLITE_CONSTRAINT_UNIQUE:
				return "unique"
			case sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY:
				return "foreign_key"
			}
		}
		return ""
	})
}

func TestSQLiteMigrationDefaultBackfill(t *testing.T) {
	migrationdefaulttest.Run(t, func(t *testing.T) migrationdefaulttest.Fixture {
		backend := openMigrationTestBackend(t)
		return migrationdefaulttest.Fixture{Backend: backend, SQL: backend.database, Namespace: "main", Renderer: NewMigrationSQLRenderer(), SQLite: true}
	})
}
