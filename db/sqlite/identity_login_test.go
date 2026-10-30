package sqlite

import (
	"github.com/progresshans/godj/internal/identitytest"
	"testing"
)

func TestSQLiteIdentityLoginLifecycle(t *testing.T) {
	identitytest.RunLoginLifecycle(t, openSQLiteIdentityPair)
}
func TestSQLiteIdentityLoginBoundaries(t *testing.T) {
	identitytest.RunLoginBoundaries(t, openSQLiteIdentityPair)
}
