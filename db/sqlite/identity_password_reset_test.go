package sqlite

import (
	"testing"

	"github.com/progresshans/godj/internal/identitytest"
)

func TestSQLiteIdentityPasswordResetSessions(t *testing.T) {
	identitytest.RunPasswordResetSessions(t, openSQLiteIdentityPair)
}

func TestSQLiteIdentityPasswordResetSessionBoundaries(t *testing.T) {
	identitytest.RunPasswordResetSessionBoundaries(t, openSQLiteIdentityPair)
}

func TestSQLiteIdentityPasswordResetSessionRaces(t *testing.T) {
	identitytest.RunPasswordResetSessionRaces(t, openSQLiteIdentityPair)
}

func TestSQLiteIdentityPasswordResetSessionEntry(t *testing.T) {
	identitytest.RunPasswordResetSessionEntry(t, openSQLiteIdentityPair)
}

func TestSQLiteIdentityPasswordResetSessionHTTP(t *testing.T) {
	identitytest.RunPasswordResetSessionHTTP(t, openSQLiteIdentityPair)
}

func TestSQLiteIdentityPasswordResetSessionLatest(t *testing.T) {
	identitytest.RunPasswordResetSessionLatest(t, openSQLiteIdentityPair)
}

func TestSQLiteIdentityPasswordResetConsumer(t *testing.T) {
	identitytest.RunPasswordResetConsumer(t, openSQLiteIdentityPair)
}

func TestSQLiteIdentityPasswordResetConsumerAcknowledgement(t *testing.T) {
	identitytest.RunPasswordResetConsumerAcknowledgement(t, openSQLiteIdentityPair)
}

func TestSQLiteIdentityPasswordResetConsumerRefusals(t *testing.T) {
	identitytest.RunPasswordResetConsumerRefusals(t, openSQLiteIdentityPair)
}

func TestSQLiteIdentityPasswordResetConsumerBoundaries(t *testing.T) {
	identitytest.RunPasswordResetConsumerBoundaries(t, openSQLiteIdentityPair)
}

func TestSQLiteIdentityPasswordResetRequestRefusals(t *testing.T) {
	identitytest.RunPasswordResetRequestRefusals(t, openSQLiteIdentityPair)
}
