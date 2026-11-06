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

func TestPostgresIdentityPasswordResetComposition(t *testing.T) {
	identitytest.RunPasswordResetComposition(t, openPostgresUnusablePair)
}

func TestPostgresIdentityPasswordResetSessions(t *testing.T) {
	identitytest.RunPasswordResetSessions(t, openPostgresUnusablePair)
}

func TestPostgresIdentityPasswordResetSessionBoundaries(t *testing.T) {
	identitytest.RunPasswordResetSessionBoundaries(t, openPostgresUnusablePair)
}

func TestPostgresIdentityPasswordResetSessionRaces(t *testing.T) {
	identitytest.RunPasswordResetSessionRaces(t, openPostgresUnusablePair)
}

func TestPostgresIdentityPasswordResetSessionEntry(t *testing.T) {
	identitytest.RunPasswordResetSessionEntry(t, openPostgresUnusablePair)
}

func TestPostgresIdentityPasswordResetSessionHTTP(t *testing.T) {
	identitytest.RunPasswordResetSessionHTTP(t, openPostgresUnusablePair)
}

func TestPostgresIdentityPasswordResetSessionLatest(t *testing.T) {
	identitytest.RunPasswordResetSessionLatest(t, openPostgresUnusablePair)
}

func TestPostgresIdentityPasswordResetConsumer(t *testing.T) {
	identitytest.RunPasswordResetConsumer(t, openPostgresUnusablePair)
}

func TestPostgresIdentityPasswordResetConsumerAcknowledgement(t *testing.T) {
	identitytest.RunPasswordResetConsumerAcknowledgement(t, openPostgresUnusablePair)
}

func TestPostgresIdentityPasswordResetConsumerRefusals(t *testing.T) {
	identitytest.RunPasswordResetConsumerRefusals(t, openPostgresUnusablePair)
}

func TestPostgresIdentityPasswordResetConsumerBoundaries(t *testing.T) {
	identitytest.RunPasswordResetConsumerBoundaries(t, openPostgresUnusablePair)
}

func TestPostgresIdentityPasswordResetRequestRefusals(t *testing.T) {
	identitytest.RunPasswordResetRequestRefusals(t, openPostgresUnusablePair)
}
