package sqlite

import (
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/internal/accountstoretest"
)

func TestSQLiteAccountCredentialStore(t *testing.T) {
	accountstoretest.Run(t, "sqlite", func(t *testing.T) (accountstoretest.Backend, func(*testing.T) accountstoretest.Backend) {
		path := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "account-store.sqlite3")) + "?mode=rwc&_pragma=busy_timeout(5000)"
		open := func(t *testing.T) accountstoretest.Backend {
			backend, err := Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := backend.Close(); err != nil {
					t.Error(err)
				}
			})
			backend.database.SetMaxOpenConns(1)
			return backend
		}
		first := open(t)
		if _, err := first.(*Backend).database.ExecContext(t.Context(), "PRAGMA journal_mode=WAL"); err != nil {
			t.Fatal(err)
		}
		return first, open
	}, func(t *testing.T, store accountstoretest.Backend, model string, from, to int64) {
		t.Helper()
		table, known := map[string]string{"user": "godj_identity_user", "group": "godj_identity_group", "permission": "godj_identity_permission"}[model]
		if !known {
			t.Fatal("unknown stored key fixture model")
		}
		result, err := store.(*Backend).database.ExecContext(t.Context(), "UPDATE "+table+" SET id=? WHERE id=?", to, from)
		if err != nil {
			t.Fatal("prepare stored account key fixture", err)
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			t.Fatal("stored account key fixture did not affect one row", count, err)
		}
	})
}
