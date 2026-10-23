package postgres

import (
	"testing"

	"github.com/progresshans/godj/internal/accountstoretest"
)

func TestPostgresAccountCredentialStore(t *testing.T) {
	url := postgresIntegrationURL(t)
	accountstoretest.Run(t, func(t *testing.T) (accountstoretest.Backend, func(*testing.T) accountstoretest.Backend) {
		namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
		open := func(t *testing.T) accountstoretest.Backend {
			backend := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
			backend.database.SetMaxOpenConns(1)
			return backend
		}
		return open(t), open
	})
}
