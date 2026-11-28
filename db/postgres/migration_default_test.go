package postgres

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/progresshans/godj/internal/migrationdefaulttest"
)

func postgresFieldTestDatabase(t *testing.T) func() migrationdefaulttest.Fixture {
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
	return func() migrationdefaulttest.Fixture {
		backend := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
		return migrationdefaulttest.Fixture{Backend: backend, SQL: backend.database, Namespace: namespace, Renderer: NewMigrationSQLRenderer(MigrationSQLConfig{Schema: namespace})}
	}
}

func postgresFieldConstraint(err error) string {
	var cause *pgconn.PgError
	if errors.As(err, &cause) {
		switch cause.Code {
		case "23502":
			return "not_null"
		case "23505":
			return "unique"
		case "23503":
			return "foreign_key"
		}
	}
	return ""
}

func TestPostgresMigrationFieldNullability(t *testing.T) {
	migrationdefaulttest.RunFieldNullability(t, postgresFieldTestDatabase, postgresFieldConstraint)
}

func TestPostgresHistoricalDataMigrations(t *testing.T) {
	migrationdefaulttest.RunHistoricalData(t, postgresFieldTestDatabase, postgresFieldConstraint)
}

func TestPostgresMigrationDefaultBackfill(t *testing.T) {
	url := postgresIntegrationURL(t)
	migrationdefaulttest.Run(t, func(t *testing.T) migrationdefaulttest.Fixture {
		namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
		backend := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
		return migrationdefaulttest.Fixture{Backend: backend, SQL: backend.database, Namespace: namespace, Renderer: NewMigrationSQLRenderer(MigrationSQLConfig{Schema: namespace})}
	})
}
