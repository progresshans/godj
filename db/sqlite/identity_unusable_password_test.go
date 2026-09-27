package sqlite

import (
	"github.com/progresshans/godj/internal/identitytest"
	"testing"
)

func TestSQLiteIdentityUnusablePasswords(t *testing.T) {
	identitytest.RunUnusablePasswords(t, openSQLiteIdentityPair)
}
func TestSQLiteIdentityUnusablePasswordHTTP(t *testing.T) {
	identitytest.RunUnusablePasswordHTTP(t, openSQLiteIdentityPair)
}
