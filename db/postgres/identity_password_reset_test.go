package postgres

import (
	"testing"

	"github.com/progresshans/godj/internal/identitytest"
)

func TestPostgresIdentityPasswordReset(t *testing.T) {
	identitytest.RunPasswordReset(t, openPostgresUnusablePair)
}
func TestPostgresIdentityPasswordResetBoundaries(t *testing.T) {
	identitytest.RunPasswordResetBoundaries(t, openPostgresUnusablePair)
}

func TestPostgresIdentityPasswordResetConcurrency(t *testing.T) {
	identitytest.RunPasswordResetConcurrency(t, openPostgresUnusablePair)
}
