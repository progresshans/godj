package sqlite

import (
	"github.com/progresshans/godj/internal/identitytest"
	"testing"
)

func TestSQLiteIdentityUserCreationForm(t *testing.T) {
	identitytest.RunUserCreationForms(t, openSQLiteIdentityPair)
}
