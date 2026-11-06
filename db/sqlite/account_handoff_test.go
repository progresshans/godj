package sqlite

import (
	"testing"

	"github.com/progresshans/godj/internal/identitytest"
)

func TestSQLiteSelectedAccountHistoricalHandoff(t *testing.T) {
	identitytest.RunAccountHandoff(t, openSQLiteIdentityPair, func(t *testing.T, store identitytest.TransitionBackend, from, to int64) {
		t.Helper()
		result, err := store.(*Backend).database.ExecContext(t.Context(), "UPDATE godj_identity_user SET id=? WHERE id=?", to, from)
		if err != nil {
			t.Fatal("prepare historical account key fixture", err)
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			t.Fatal("historical account key fixture did not affect one row", count, err)
		}
	})
}
func TestSQLiteSelectedAccountHistoricalHandoffConcurrency(t *testing.T) {
	identitytest.RunAccountHandoffConcurrency(t, openSQLiteIdentityPair)
}

func TestSQLiteOperatorSelectedAccountHandoff(t *testing.T) {
	identitytest.RunOperatorAccountHandoff(t, openSQLiteIdentityPair)
}
func TestSQLiteSelectedAccountHistoricalArchive(t *testing.T) {
	identitytest.RunAccountHandoffArchive(t, openSQLiteIdentityPair)
}
