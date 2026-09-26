package postgres

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/internal/snapshotdriver"
	"github.com/progresshans/godj/internal/identitytest"
)

func TestPostgresReadSnapshotFailureAndLifetimeOwnership(t *testing.T) {
	snapshotdriver.Probe(t, func(state *snapshotdriver.State) snapshotdriver.Backend {
		return &Backend{database: sql.OpenDB(snapshotdriver.Connector{State: state}), schema: "public"}
	}, "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY NOT DEFERRABLE")
}

func TestPostgresReadSnapshotRejectsInvalidCallsBeforeConnection(t *testing.T) {
	state := &snapshotdriver.State{}
	backend := &Backend{database: sql.OpenDB(snapshotdriver.Connector{State: state}), schema: "public"}
	t.Cleanup(func() { _ = backend.Close() })
	callback := func(db.Queryer) error { t.Fatal("invalid call executed callback"); return nil }
	if err := backend.ReadSnapshot(nil, callback); err == nil {
		t.Fatal("nil context accepted")
	}
	if err := backend.ReadSnapshot(t.Context(), nil); err == nil {
		t.Fatal("nil callback accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := backend.ReadSnapshot(ctx, callback); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if state.Snapshot().Connections != 0 {
		t.Fatal("invalid call acquired connection")
	}
}

func TestPostgresIdentityDirectoryUsesOneCurrentReadSnapshot(t *testing.T) {
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
	reader := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
	writer := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
	identitytest.RunDirectory(t, reader, writer)
}

func TestPostgresIdentityDirectoryRejectsPartialOrInvalidState(t *testing.T) {
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
	backend := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
	identitytest.RunDirectoryValidation(t, backend)
}

func TestPostgresStoredIdentityAuthenticationAndHTTP(t *testing.T) {
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
	backend := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
	backend.database.SetMaxOpenConns(1)
	identitytest.RunAuthentication(t, backend)
}

func TestPostgresOperatorAdoptionPreservesSessionAuditAndOwnership(t *testing.T) {
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
	backend := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
	identitytest.RunOperatorTransition(t, backend)
}
func TestPostgresIdentityBootstrapReconcilesUnknownOutcome(t *testing.T) {
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
	backend := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
	identitytest.RunIdentityBootstrap(t, backend)
}

func TestPostgresIdentityTransitionFailureConcurrencyAndReadOwnership(t *testing.T) {
	url := postgresIntegrationURL(t)
	identitytest.RunTransitionBoundaries(t, func(t *testing.T) (identitytest.TransitionBackend, identitytest.TransitionBackend) {
		namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
		first := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
		second := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
		return first, second
	})
}

func TestPostgresIdentityPasswordManagement(t *testing.T) {
	url := postgresIntegrationURL(t)
	identitytest.RunPasswordManagement(t, func(t *testing.T) (identitytest.TransitionBackend, identitytest.TransitionBackend) {
		namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
		first := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
		second := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
		first.database.SetMaxOpenConns(1)
		second.database.SetMaxOpenConns(1)
		return first, second
	})
}
