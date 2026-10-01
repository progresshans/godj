package sqlite

import (
	"testing"

	"github.com/progresshans/godj/internal/identitytest"
)

func TestSQLiteIdentityPasswordChange(t *testing.T) {
	identitytest.RunPasswordChange(t, openSQLiteIdentityPair)
}
func TestSQLiteIdentityPasswordChangeBoundaries(t *testing.T) {
	identitytest.RunPasswordChangeBoundaries(t, openSQLiteIdentityPair)
}
func TestSQLiteIdentityPasswordChangeConcurrency(t *testing.T) {
	identitytest.RunPasswordChangeConcurrency(t, openSQLiteIdentityPair)
}
func TestSQLiteIdentityPasswordChangeHTTP(t *testing.T) {
	identitytest.RunPasswordChangeHTTP(t, openSQLiteIdentityPair)
}

func TestSQLiteIdentityAccountHTTP(t *testing.T) {
	identitytest.RunAccountHTTP(t, openSQLiteIdentityPair)
}

func TestSQLiteIdentityAccountHTTPRefusals(t *testing.T) {
	identitytest.RunAccountHTTPRefusals(t, openSQLiteIdentityPair)
}

func TestSQLiteIdentityAccountHTTPBoundaries(t *testing.T) {
	identitytest.RunAccountHTTPBoundaries(t, openSQLiteIdentityPair)
}

func TestSQLiteIdentityAccountEntryRefusals(t *testing.T) {
	identitytest.RunAccountEntryRefusals(t, openSQLiteIdentityPair)
}
