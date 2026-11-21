package sqlite

import (
	"github.com/progresshans/godj/internal/identitytest"
	"testing"
)

func TestSQLiteIdentityUnicodeProfile(t *testing.T) {
	identitytest.RunIdentityUnicodeProfile(t, openSQLiteIdentityPair)
}
