package postgres

import (
	"testing"

	"github.com/progresshans/godj/internal/identitytest"
)

func TestPostgresIdentityPasswordChange(t *testing.T) {
	identitytest.RunPasswordChange(t, openPostgresUnusablePair)
}
func TestPostgresIdentityPasswordChangeBoundaries(t *testing.T) {
	identitytest.RunPasswordChangeBoundaries(t, openPostgresUnusablePair)
}
func TestPostgresIdentityPasswordChangeConcurrency(t *testing.T) {
	identitytest.RunPasswordChangeConcurrency(t, openPostgresUnusablePair)
}
func TestPostgresIdentityPasswordChangeHTTP(t *testing.T) {
	identitytest.RunPasswordChangeHTTP(t, openPostgresUnusablePair)
}
