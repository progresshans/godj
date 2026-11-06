package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/savepointtest"
	"github.com/progresshans/godj/query"
)

func TestSQLiteNativeSavepoints(t *testing.T) {
	backend, err := OpenMemory(t.Context(), t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := backend.ExecContext(t.Context(), `CREATE TABLE savepoint_items(id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE, parent_id INTEGER REFERENCES savepoint_items(id))`); err != nil {
		t.Fatal(err)
	}
	for _, variant := range []struct {
		name  string
		owner savepointtest.Owner
	}{
		{"ordinary", backend.Atomic}, {"coordinated", backend.CoordinatedAtomic},
		{"relation", func(ctx context.Context, callback func(db.Session) error) error {
			return backend.AtomicRelation(ctx, func(session db.RelationSession) error { return callback(session) })
		}},
		{"coordinated_relation", func(ctx context.Context, callback func(db.Session) error) error {
			return backend.CoordinatedAtomicRelation(ctx, func(session db.RelationSession) error { return callback(session) })
		}},
	} {
		t.Run(variant.name, func(t *testing.T) { savepointtest.Check(t, backend, variant.owner) })
	}
	checkSQLiteRootStreamSavepoints(t, backend)
}

func checkSQLiteRootStreamSavepoints(t *testing.T, backend *Backend) {
	t.Helper()
	if _, err := backend.ExecContext(t.Context(), `CREATE TABLE savepoint_stream(value INTEGER); INSERT INTO savepoint_stream VALUES(1),(2)`); err != nil {
		t.Fatal(err)
	}
	value := query.NewFieldRef("value", "value", query.FieldInteger, false)
	plan := query.NewPlan("savepoint_stream", []query.FieldRef{value}).WithOrderings(query.NewOrdering(value, query.Ascending))
	scans, yields := 0, 0
	err := backend.QueryBatches(t.Context(), plan, 1, func(row db.Row) error { var value int64; scans++; return row.Scan(&value) }, func(affinity db.Queryer) (bool, error) {
		yields++
		if yields == 1 {
			t.Run("pinned_ordinary", func(t *testing.T) { savepointtest.CheckPinned(t, affinity, affinity.(db.Atomic).Atomic) })
			t.Run("pinned_coordinated_relation", func(t *testing.T) {
				savepointtest.CheckPinned(t, affinity, func(ctx context.Context, callback func(db.Session) error) error {
					return affinity.(db.CoordinatedRelationAtomic).CoordinatedAtomicRelation(ctx, func(session db.RelationSession) error { return callback(session) })
				})
			})
		}
		return true, nil
	})
	if err != nil || scans != 2 || yields != 2 {
		t.Fatalf("root stream after savepoints = %d/%d/%v", scans, yields, err)
	}
}

func TestSQLiteRawSavepointFailureRollsBackAndRetainsUnknownRoot(t *testing.T) {
	t.Parallel()
	for _, coordinated := range []bool{false, true} {
		for _, unknownRoot := range []bool{false, true} {
			for _, stage := range []string{"SAVEPOINT ", "ROLLBACK TO ", "RELEASE "} {
				t.Run(strings.TrimSpace(stage)+map[bool]string{false: "/relation", true: "/coordinated"}[coordinated]+map[bool]string{false: "/confirmed", true: "/unknown"}[unknownRoot], func(t *testing.T) {
					t.Parallel()
					controlFailure := errors.New("savepoint transport failure")
					rootFailure := errors.New("root rollback failure")
					connection := &relationFaultConnection{exec: func(_ context.Context, statement string, _ []any) (sql.Result, error) {
						if strings.HasPrefix(statement, stage) {
							return nil, controlFailure
						}
						if statement == "ROLLBACK" && unknownRoot {
							return nil, rootFailure
						}
						return relationFaultResult(1), nil
					}}
					if unknownRoot {
						connection.raw = func(func(any) error) error { return nil }
					}
					retention := newRelationRetentionState()
					calls := 0
					callback := func(outer db.Session) error {
						if _, err := outer.Delete(context.Background(), relationDeleteTestPlan(1)); err != nil {
							return err
						}
						err := db.WithSavepoint(context.Background(), outer, func(db.Session) error {
							calls++
							if stage == "ROLLBACK TO " {
								return errors.New("child failure")
							}
							return nil
						})
						if !errors.Is(err, controlFailure) || !errors.Is(err, &query.Error{Code: query.CodeTransactionRollbackRequired}) {
							t.Errorf("control failure = %v", err)
						}
						return nil // Ignoring the error must not allow COMMIT.
					}
					var err error
					if coordinated {
						err = executeCoordinatedAtomic(context.Background(), callback, connection, retention, nil)
					} else {
						err = executeAtomicRelation(context.Background(), func(session db.RelationSession) error { return callback(session) }, connection, retention, nil)
					}
					if !errors.Is(err, controlFailure) || !errors.Is(err, &query.Error{Code: query.CodeTransactionRollbackRequired}) {
						t.Fatalf("ignored savepoint failure = %v", err)
					}
					wantCalls := 1
					if stage == "SAVEPOINT " {
						wantCalls = 0
					}
					if calls != wantCalls {
						t.Fatalf("child callbacks = %d, want %d", calls, wantCalls)
					}
					controlCalls, rollbacks := 0, 0
					for _, statement := range connection.statementSnapshot() {
						if statement == "COMMIT" {
							t.Fatal("poisoned scope committed")
						}
						if statement == "ROLLBACK" {
							rollbacks++
						}
						if strings.HasPrefix(statement, stage) {
							controlCalls++
						}
					}
					if controlCalls != 1 || rollbacks != 1 {
						t.Fatalf("control/rollback attempts = %d/%d", controlCalls, rollbacks)
					}
					if unknownRoot {
						if !errors.Is(err, rootFailure) || !errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) || retainedConnectionCount(retention) != 1 || connection.closeCalls.Load() != 0 {
							t.Fatalf("unknown root cleanup = %v, retained %d, closes %d", err, retainedConnectionCount(retention), connection.closeCalls.Load())
						}
						if err := retention.sealAndDrain(); err != nil {
							t.Fatal(err)
						}
					} else if errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) || retainedConnectionCount(retention) != 0 || connection.closeCalls.Load() != 1 {
						t.Fatalf("confirmed root cleanup = %v", err)
					}
				})
			}
		}
	}
}

func TestSQLiteOrdinarySavepointPoisonAndRollbackFailureTaxonomy(t *testing.T) {
	t.Parallel()
	for _, failRollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "confirmed", true: "unknown"}[failRollback], func(t *testing.T) {
			t.Parallel()
			controlFailure := errors.New("release failed")
			rollbackFailure := errors.New("rollback failed")
			state := &atomicCommitFaultState{exec: func(_ context.Context, sql string) error {
				if strings.HasPrefix(sql, "RELEASE ") {
					return controlFailure
				}
				return nil
			}}
			if failRollback {
				state.rollbackErr = rollbackFailure
			}
			database := sql.OpenDB(atomicCommitFaultConnector{state: state})
			backend := &Backend{database: database, relationRetention: newRelationRetentionState()}
			t.Cleanup(func() { _ = backend.Close() })
			err := backend.Atomic(context.Background(), func(session db.Session) error {
				_ = db.WithSavepoint(context.Background(), session, func(db.Session) error { return nil })
				return nil
			})
			if !errors.Is(err, controlFailure) || !errors.Is(err, &query.Error{Code: query.CodeTransactionRollbackRequired}) || state.commitCalls.Load() != 0 || state.rollbackCalls.Load() != 1 {
				t.Fatalf("poisoned Atomic = %v, commit/rollback = %d/%d", err, state.commitCalls.Load(), state.rollbackCalls.Load())
			}
			if errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) != failRollback || failRollback && !errors.Is(err, rollbackFailure) {
				t.Fatalf("rollback classification = %v", err)
			}
		})
	}
}
