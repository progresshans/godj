package postgres

import (
	"github.com/progresshans/godj/internal/identitytest"
	"testing"
)

func accountHandoffPostgresPair(t *testing.T) (identitytest.TransitionBackend, identitytest.TransitionBackend) {
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
	return openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace), openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
}
func TestPostgresSelectedAccountHistoricalHandoff(t *testing.T) {
	identitytest.RunAccountHandoff(t, accountHandoffPostgresPair)
}
func TestPostgresSelectedAccountHistoricalHandoffConcurrency(t *testing.T) {
	identitytest.RunAccountHandoffConcurrency(t, accountHandoffPostgresPair)
}

func TestPostgresOperatorSelectedAccountHandoff(t *testing.T) {
	identitytest.RunOperatorAccountHandoff(t, accountHandoffPostgresPair)
}
func TestPostgresSelectedAccountHistoricalArchive(t *testing.T) {
	identitytest.RunAccountHandoffArchive(t, accountHandoffPostgresPair)
}
