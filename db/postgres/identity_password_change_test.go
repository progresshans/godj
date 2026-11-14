package postgres

import (
	"testing"

	"github.com/progresshans/godj/internal/identitytest"
)

func TestPostgresIdentityPasswordChange(t *testing.T) {
	identitytest.RunPasswordChange(t, openPostgresIdentityPair)
}
func TestPostgresIdentityPasswordChangeBoundaries(t *testing.T) {
	identitytest.RunPasswordChangeBoundaries(t, openPostgresIdentityPair)
}
func TestPostgresIdentityPasswordChangeConcurrency(t *testing.T) {
	identitytest.RunPasswordChangeConcurrency(t, openPostgresIdentityPair)
}
func TestPostgresIdentityPasswordChangeHTTP(t *testing.T) {
	identitytest.RunPasswordChangeHTTP(t, openPostgresIdentityPair)
}

func TestPostgresIdentityAccountHTTP(t *testing.T) {
	identitytest.RunAccountHTTP(t, openPostgresIdentityPair)
}

func TestPostgresIdentityAccountHTTPRefusals(t *testing.T) {
	identitytest.RunAccountHTTPRefusals(t, openPostgresIdentityPair)
}

func TestPostgresIdentityAccountHTTPBoundaries(t *testing.T) {
	identitytest.RunAccountHTTPBoundaries(t, openPostgresIdentityPair)
}

func TestPostgresIdentityAccountEntryRefusals(t *testing.T) {
	identitytest.RunAccountEntryRefusals(t, openPostgresIdentityPair)
}
