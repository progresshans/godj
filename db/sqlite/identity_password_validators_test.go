package sqlite

import (
	"github.com/progresshans/godj/internal/identitytest"
	"testing"
)

func TestSQLiteIdentityPasswordValidators(t *testing.T) {
	identitytest.RunPasswordValidators(t, openSQLiteIdentityPair)
}
