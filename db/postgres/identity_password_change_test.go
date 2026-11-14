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
