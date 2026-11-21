package postgres

import (
	"testing"

	"github.com/progresshans/godj/internal/accountaudittest"
)

func TestPostgresAccountAudit(t *testing.T) {
	url := postgresIntegrationURL(t)
	accountaudittest.Run(t, func(t *testing.T) (accountaudittest.Backend, func(*testing.T) accountaudittest.Backend) {
		namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
		open := func(t *testing.T) accountaudittest.Backend {
			backend := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
			backend.database.SetMaxOpenConns(1)
			return backend
		}
		return open(t), open
	})
}
