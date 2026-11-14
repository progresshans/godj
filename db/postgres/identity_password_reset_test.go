package postgres

import (
	"testing"

	"github.com/progresshans/godj/internal/identitytest"
)

func TestPostgresIdentityPasswordReset(t *testing.T) {
	identitytest.RunPasswordReset(t, openPostgresIdentityPair)
}
func TestPostgresIdentityPasswordResetBoundaries(t *testing.T) {
	identitytest.RunPasswordResetBoundaries(t, openPostgresIdentityPair)
}

func TestPostgresIdentityPasswordResetConcurrency(t *testing.T) {
	identitytest.RunPasswordResetConcurrency(t, openPostgresIdentityPair)
}

func TestPostgresIdentityPasswordResetMail(t *testing.T) {
	identitytest.RunPasswordResetMail(t, "postgres", openPostgresIdentityPair)
}
func TestPostgresIdentityPasswordResetMailBoundaries(t *testing.T) {
	identitytest.RunPasswordResetMailBoundaries(t, openPostgresIdentityPair)
}
func TestPostgresIdentityPasswordResetMailSnapshotBinding(t *testing.T) {
	identitytest.RunPasswordResetMailSnapshotBinding(t, openPostgresIdentityPair)
}

func TestPostgresIdentityPasswordResetComposition(t *testing.T) {
	identitytest.RunPasswordResetComposition(t, openPostgresIdentityPair)
}

func TestPostgresIdentityPasswordResetSessions(t *testing.T) {
	identitytest.RunPasswordResetSessions(t, openPostgresIdentityPair)
}

func TestPostgresIdentityPasswordResetSessionBoundaries(t *testing.T) {
	identitytest.RunPasswordResetSessionBoundaries(t, openPostgresIdentityPair)
}

func TestPostgresIdentityPasswordResetSessionRaces(t *testing.T) {
	identitytest.RunPasswordResetSessionRaces(t, openPostgresIdentityPair)
}

func TestPostgresIdentityPasswordResetSessionEntry(t *testing.T) {
	identitytest.RunPasswordResetSessionEntry(t, openPostgresIdentityPair)
}

func TestPostgresIdentityPasswordResetSessionHTTP(t *testing.T) {
	identitytest.RunPasswordResetSessionHTTP(t, openPostgresIdentityPair)
}

func TestPostgresIdentityPasswordResetSessionLatest(t *testing.T) {
	identitytest.RunPasswordResetSessionLatest(t, openPostgresIdentityPair)
}

func TestPostgresIdentityPasswordResetConsumer(t *testing.T) {
	identitytest.RunPasswordResetConsumer(t, openPostgresIdentityPair)
}

func TestPostgresIdentityPasswordResetConsumerAcknowledgement(t *testing.T) {
	identitytest.RunPasswordResetConsumerAcknowledgement(t, openPostgresIdentityPair)
}

func TestPostgresIdentityPasswordResetConsumerRefusals(t *testing.T) {
	identitytest.RunPasswordResetConsumerRefusals(t, openPostgresIdentityPair)
}

func TestPostgresIdentityPasswordResetConsumerBoundaries(t *testing.T) {
	identitytest.RunPasswordResetConsumerBoundaries(t, openPostgresIdentityPair)
}

func TestPostgresIdentityPasswordResetRequestRefusals(t *testing.T) {
	identitytest.RunPasswordResetRequestRefusals(t, openPostgresIdentityPair)
}
