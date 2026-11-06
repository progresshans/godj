package postgres

import (
	"github.com/progresshans/godj/internal/identitytest"
	"testing"
)

func TestPostgresIdentityPasswordValidators(t *testing.T) {
	url := postgresIntegrationURL(t)
	identitytest.RunPasswordValidators(t, func(t *testing.T) (identitytest.TransitionBackend, identitytest.TransitionBackend) {
		namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
		first := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
		second := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
		first.database.SetMaxOpenConns(1)
		second.database.SetMaxOpenConns(1)
		return first, second
	})
}
