package sqlite

import (
	"testing"

	"github.com/progresshans/godj/internal/migrationdefaulttest"
)

func TestSQLiteMigrationDefaultBackfill(t *testing.T) {
	migrationdefaulttest.Run(t, func(t *testing.T) migrationdefaulttest.Fixture {
		backend := openMigrationTestBackend(t)
		return migrationdefaulttest.Fixture{Backend: backend, SQL: backend.database, Namespace: "main", Renderer: NewMigrationSQLRenderer(), SQLite: true}
	})
}
