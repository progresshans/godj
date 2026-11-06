package postgres

import (
	"testing"

	"github.com/progresshans/godj/internal/accountdomaintest"
)

func TestPostgresAccountDomainOwnership(t *testing.T) {
	url := postgresIntegrationURL(t)
	accountdomaintest.Run(t, func(t *testing.T) (accountdomaintest.Backend, func(*testing.T) accountdomaintest.Backend) {
		namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
		open := func(t *testing.T) accountdomaintest.Backend {
			backend := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
			backend.database.SetMaxOpenConns(1)
			return backend
		}
		return open(t), open
	})
}
