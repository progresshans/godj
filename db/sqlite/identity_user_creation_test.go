package sqlite

import (
	"github.com/progresshans/godj/internal/identitytest"
	"testing"
)

func TestSQLiteIdentityUserCreationValidation(t *testing.T) {
	identitytest.RunUserCreationValidation(t, openSQLiteIdentityPair)
}
