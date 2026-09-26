package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/internal/snapshotdriver"
	"github.com/progresshans/godj/internal/identitytest"
)

func TestSQLiteReadSnapshotFailureAndLifetimeOwnership(t *testing.T) {
	snapshotdriver.Probe(t, func(state *snapshotdriver.State) snapshotdriver.Backend {
		return &Backend{database: sql.OpenDB(snapshotdriver.Connector{State: state}), relationRetention: newRelationRetentionState()}
	}, "BEGIN")
}

func TestSQLiteReadSnapshotRejectsInvalidCallsBeforeConnection(t *testing.T) {
	state := &snapshotdriver.State{}
	backend := &Backend{database: sql.OpenDB(snapshotdriver.Connector{State: state}), relationRetention: newRelationRetentionState()}
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

func TestSQLiteIdentityDirectoryUsesOneCurrentReadSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.sqlite3")
	reader, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reader.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := reader.database.ExecContext(t.Context(), "PRAGMA journal_mode=WAL"); err != nil {
		t.Fatal(err)
	}
	writer, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := writer.Close(); err != nil {
			t.Error(err)
		}
	})
	identitytest.RunDirectory(t, reader, writer)
}

func TestSQLiteIdentityDirectoryRejectsPartialOrInvalidState(t *testing.T) {
	backend, err := OpenMemory(t.Context(), t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	identitytest.RunDirectoryValidation(t, backend)
}

func TestSQLiteStoredIdentityAuthenticationAndHTTP(t *testing.T) {
	backend, err := OpenMemory(t.Context(), t.Name())
	if err != nil {
		t.Fatal(err)
	}
	backend.database.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	identitytest.RunAuthentication(t, backend)
}

func TestSQLiteOperatorAdoptionPreservesSessionAuditAndOwnership(t *testing.T) {
	backend, err := OpenMemory(t.Context(), t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	identitytest.RunOperatorTransition(t, backend)
}
func TestSQLiteIdentityBootstrapReconcilesUnknownOutcome(t *testing.T) {
	backend, err := OpenMemory(t.Context(), t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	identitytest.RunIdentityBootstrap(t, backend)
}

func TestSQLiteIdentityTransitionFailureConcurrencyAndReadOwnership(t *testing.T) {
	identitytest.RunTransitionBoundaries(t, openSQLiteIdentityPair)
}

func TestSQLiteIdentityPasswordManagement(t *testing.T) {
	identitytest.RunPasswordManagement(t, openSQLiteIdentityPair)
}

func openSQLiteIdentityPair(t *testing.T) (identitytest.TransitionBackend, identitytest.TransitionBackend) {
	t.Helper()
	path := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "transition.sqlite3")) + "?mode=rwc&_pragma=busy_timeout(5000)"
	first, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := first.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := first.database.ExecContext(t.Context(), "PRAGMA journal_mode=WAL"); err != nil {
		t.Fatal(err)
	}
	second, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := second.Close(); err != nil {
			t.Error(err)
		}
	})
	first.database.SetMaxOpenConns(1)
	second.database.SetMaxOpenConns(1)
	return first, second
}
