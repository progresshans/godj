package sqlite

import (
	"github.com/progresshans/godj/internal/identitytest"
	"testing"
)

func TestSQLiteSelectedAccountHistoricalHandoff(t *testing.T) {
	identitytest.RunAccountHandoff(t, openSQLiteIdentityPair)
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
