package postgres

import (
	"testing"

	"github.com/progresshans/godj/internal/identitytest"
)

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
