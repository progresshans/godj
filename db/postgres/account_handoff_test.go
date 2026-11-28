package postgres

import (
	"testing"

	"github.com/progresshans/godj/internal/identitytest"
)

func accountHandoffPostgresPair(t *testing.T) (identitytest.TransitionBackend, identitytest.TransitionBackend) {
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
	return openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace), openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
}
func TestPostgresSelectedAccountHistoricalHandoff(t *testing.T) {
	identitytest.RunAccountHandoff(t, accountHandoffPostgresPair, func(t *testing.T, store identitytest.TransitionBackend, from, to int64) {
		t.Helper()
		backend := store.(*Backend)
		table, err := quoteTable(backend.schema, "godj_identity_user")
		if err != nil {
			t.Fatal(err)
		}
		result, err := backend.database.ExecContext(t.Context(), "UPDATE "+table+" SET id=$1 WHERE id=$2", to, from)
		if err != nil {
			t.Fatal("prepare historical account key fixture", err)
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			t.Fatal("historical account key fixture did not affect one row", count, err)
		}
	})
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
