package postgres

import (
	"github.com/progresshans/godj/internal/identitytest"
	"testing"
)

func openPostgresUnusablePair(t *testing.T) (identitytest.TransitionBackend, identitytest.TransitionBackend) {
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
	first := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
	second := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
	first.database.SetMaxOpenConns(1)
	second.database.SetMaxOpenConns(1)
	return first, second
}
func TestPostgresIdentityUnusablePasswords(t *testing.T) {
	identitytest.RunUnusablePasswords(t, openPostgresUnusablePair)
}
func TestPostgresIdentityUnusablePasswordHTTP(t *testing.T) {
	identitytest.RunUnusablePasswordHTTP(t, openPostgresUnusablePair)
}

func TestPostgresIdentityPasswordStatus(t *testing.T) {
	identitytest.RunPasswordStatus(t, openPostgresUnusablePair)
}
