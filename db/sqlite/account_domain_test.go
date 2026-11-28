package sqlite

import (
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/internal/accountdomaintest"
)

func TestSQLiteAccountDomainOwnership(t *testing.T) {
	accountdomaintest.Run(t, func(t *testing.T) (accountdomaintest.Backend, func(*testing.T) accountdomaintest.Backend) {
		path := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "account-domain.sqlite3")) + "?mode=rwc&_pragma=busy_timeout(5000)"
		open := func(t *testing.T) accountdomaintest.Backend {
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
	})
}
