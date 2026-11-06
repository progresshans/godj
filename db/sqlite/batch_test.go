package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"runtime"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/batchtest"
	"github.com/progresshans/godj/query"
)

func TestSQLiteSessionBatches(t *testing.T) {
	backend, err := OpenMemory(t.Context(), t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, statement := range []string{
		"CREATE TABLE batch_source(value INTEGER NOT NULL)",
		"INSERT INTO batch_source VALUES(3),(1),(1),(4),(2)",
		"CREATE TABLE batch_writes(id INTEGER PRIMARY KEY, value INTEGER NOT NULL)",
	} {
		if _, err := backend.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, variant := range []struct {
		name  string
		owner batchtest.Owner
	}{
		{"ordinary", backend.Atomic}, {"coordinated", backend.CoordinatedAtomic},
		{"relation", func(ctx context.Context, callback func(db.Session) error) error {
			return backend.AtomicRelation(ctx, func(session db.RelationSession) error { return callback(session) })
		}},
		{"coordinated_relation", func(ctx context.Context, callback func(db.Session) error) error {
			return backend.CoordinatedAtomicRelation(ctx, func(session db.RelationSession) error { return callback(session) })
		}},
	} {
		t.Run(variant.name, func(t *testing.T) { batchtest.Check(t, backend, variant.owner, nil) })
	}
}

func TestSQLiteGoexitRetainsUnconfirmedTransaction(t *testing.T) {
	for _, coordinated := range []bool{false, true} {
		t.Run(map[bool]string{false: "relation", true: "coordinated"}[coordinated], func(t *testing.T) {
			connection := &relationFaultConnection{
				exec: func(_ context.Context, statement string, _ []any) (sql.Result, error) {
					if statement == "ROLLBACK" {
						return nil, errors.New("unconfirmed rollback")
					}
					return relationFaultResult(1), nil
				},
				raw: func(func(any) error) error { return nil }, // Does not confirm discard.
			}
			retention := newRelationRetentionState()
			done := make(chan struct{})
			var returned bool
			var retained db.Session
			go func() {
				defer close(done)
				callback := func(session db.Session) error { retained = session; runtime.Goexit(); return nil }
				if coordinated {
					_ = executeCoordinatedAtomic(t.Context(), callback, connection, retention, nil)
				} else {
					_ = executeAtomicRelation(t.Context(), func(session db.RelationSession) error { return callback(session) }, connection, retention, nil)
				}
				returned = true
			}()
			<-done
			if returned || retained == nil {
				t.Fatal("Goexit did not propagate")
			}
			if err := retained.(db.SessionValidator).ValidateSession(t.Context()); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
				t.Fatal("session remained active", err)
			}
			if connection.closeCalls.Load() != 0 || retainedConnectionCount(retention) != 1 {
				t.Fatal("uncertain connection returned to pool")
			}
			if err := retention.availabilityError(); !errors.Is(err, &query.Error{Code: query.CodeBackendRecoveryRequired}) {
				t.Fatal("missing quarantine", err)
			}
			if err := retention.sealAndDrain(); err != nil {
				t.Fatal(err)
			}
			if connection.closeCalls.Load() != 1 {
				t.Fatal("retained connection not closed exactly once")
			}
		})
	}
}
