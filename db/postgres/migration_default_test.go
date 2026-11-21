package postgres

import (
	"testing"

	"github.com/progresshans/godj/internal/migrationdefaulttest"
)

func TestPostgresMigrationDefaultBackfill(t *testing.T) {
	url := postgresIntegrationURL(t)
	migrationdefaulttest.Run(t, func(t *testing.T) migrationdefaulttest.Fixture {
		namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
		backend := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
		return migrationdefaulttest.Fixture{Backend: backend, SQL: backend.database, Namespace: namespace, Renderer: NewMigrationSQLRenderer(MigrationSQLConfig{Schema: namespace})}
	})
}
