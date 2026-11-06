package postgres

import (
	"github.com/progresshans/godj/internal/identitytest"
	"testing"
)

func TestPostgresIdentityLoginLifecycle(t *testing.T) {
	identitytest.RunLoginLifecycle(t, openPostgresUnusablePair)
}
func TestPostgresIdentityLoginBoundaries(t *testing.T) {
	identitytest.RunLoginBoundaries(t, openPostgresUnusablePair)
}
