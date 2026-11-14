package postgres

import (
	"testing"

	"github.com/progresshans/godj/internal/identitytest"
)

func openPostgresIdentityPair(t *testing.T) (identitytest.TransitionBackend, identitytest.TransitionBackend) {
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
	first := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
	second := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
	first.database.SetMaxOpenConns(1)
	second.database.SetMaxOpenConns(1)
	return first, second
}
