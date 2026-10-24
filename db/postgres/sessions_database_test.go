package postgres

import (
	"testing"

	"github.com/progresshans/godj/internal/sessiondbtest"
)

func TestPostgresSessionDatabase(t *testing.T) {
	url := postgresIntegrationURL(t)
	sessiondbtest.Run(t, func(t *testing.T) (sessiondbtest.Backend, func(*testing.T) sessiondbtest.Backend) {
		namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
		open := func(t *testing.T) sessiondbtest.Backend {
			backend := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
			backend.database.SetMaxOpenConns(1)
			return backend
		}
		return open(t), open
	})
}
