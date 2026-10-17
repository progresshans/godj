package sqlite

import (
	"github.com/progresshans/godj/internal/identitytest"
	"testing"
)

func TestSQLiteIdentityManagementAdmin(t *testing.T) {
	identitytest.RunManagementAdmin(t, openSQLiteIdentityPair)
}
