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
	}, func(t *testing.T, store accountstoretest.Backend, from, to int64) {
		t.Helper()
		backend := store.(*Backend)
		table, err := quoteTable(backend.schema, "godj_identity_user")
		if err != nil {
			t.Fatal(err)
		}
		result, err := backend.database.ExecContext(t.Context(), "UPDATE "+table+" SET id=$1 WHERE id=$2", to, from)
		if err != nil {
			t.Fatal("prepare stored account key fixture", err)
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			t.Fatal("stored account key fixture did not affect one row", count, err)
		}
	})
}
