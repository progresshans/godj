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

func TestPostgresIdentityPasswordResetMail(t *testing.T) {
	identitytest.RunPasswordResetMail(t, "postgres", openPostgresUnusablePair)
}
func TestPostgresIdentityPasswordResetMailBoundaries(t *testing.T) {
	identitytest.RunPasswordResetMailBoundaries(t, openPostgresUnusablePair)
}
func TestPostgresIdentityPasswordResetMailSnapshotBinding(t *testing.T) {
	identitytest.RunPasswordResetMailSnapshotBinding(t, openPostgresUnusablePair)
}
