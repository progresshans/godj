package sqlite

import (
	"testing"

	"github.com/progresshans/godj/internal/identitytest"
)

func TestSQLiteIdentityPasswordReset(t *testing.T) {
	identitytest.RunPasswordReset(t, openSQLiteIdentityPair)
}
func TestSQLiteIdentityPasswordResetBoundaries(t *testing.T) {
	identitytest.RunPasswordResetBoundaries(t, openSQLiteIdentityPair)
}

func TestSQLiteIdentityPasswordResetConcurrency(t *testing.T) {
	identitytest.RunPasswordResetConcurrency(t, openSQLiteIdentityPair)
}
