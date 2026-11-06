package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/insertgraphtest"
	"github.com/progresshans/godj/query"
)

type insertGraphFaultTransaction struct {
	*sql.Tx
	exec  func(context.Context, string, []any) (sql.Result, error)
	query func(context.Context, string, []any) (*sql.Rows, error)
}

func (transaction *insertGraphFaultTransaction) ExecContext(ctx context.Context, statement string, args ...any) (sql.Result, error) {
	if transaction.exec != nil {
		return transaction.exec(ctx, statement, args)
	}
	return transaction.Tx.ExecContext(ctx, statement, args...)
}

func (transaction *insertGraphFaultTransaction) QueryContext(ctx context.Context, statement string, args ...any) (*sql.Rows, error) {
	if transaction.query != nil {
		return transaction.query(ctx, statement, args)
	}
	return transaction.Tx.QueryContext(ctx, statement, args...)
}

func drainGraphFaultRows(rows *sql.Rows, err error) error {
	if err != nil {
		if rows != nil {
			err = errors.Join(err, rows.Close())
		}
		return err
	}
	for rows.Next() {
		var key int64
		if err := rows.Scan(&key); err != nil {
			return errors.Join(err, rows.Close())
		}
	}
	return errors.Join(rows.Err(), rows.Close())
}

func TestPostgresInsertGraphFailuresStayInsideSavepoint(t *testing.T) {
	for _, point := range []string{"statement_before", "statement_after", "wrong_result", "readback", "late_cancel", "unconfirmed_rollback", "unconfirmed_release"} {
		t.Run(point, func(t *testing.T) {
			backend, observer := postgresInsertGraphFixture(t)
			failure := errors.New("injected graph boundary failure")
			work, cancel := context.WithCancel(t.Context())
			defer cancel()
			fired, attempts := false, 0
			var left, right insertgraphtest.Row
			marker := insertgraphtest.Row{Key: 99, Peer: 99, Token: "parent"}
			err := backend.atomic(t.Context(), func(ctx context.Context) (transactionHandle, error) {
				native, err := backend.database.BeginTx(ctx, nil)
				if err != nil {
					return nil, err
				}
				transaction := &insertGraphFaultTransaction{Tx: native}
				transaction.exec = func(ctx context.Context, statement string, args []any) (sql.Result, error) {
					if point == "unconfirmed_rollback" && strings.HasPrefix(statement, "ROLLBACK TO SAVEPOINT") || point == "unconfirmed_release" && strings.HasPrefix(statement, "RELEASE SAVEPOINT") {
						return nil, failure
					}
					return native.ExecContext(ctx, statement, args...)
				}
				transaction.query = func(ctx context.Context, statement string, args []any) (*sql.Rows, error) {
					write := strings.HasPrefix(statement, "WITH godj_graph_")
					if write {
						attempts++
					}
					readback := strings.HasPrefix(statement, "SELECT graph_key FROM (")
					if !fired && (write && point != "readback" && point != "unconfirmed_release" || point == "readback" && readback) {
						fired = true
						if point == "statement_after" || point == "wrong_result" || point == "late_cancel" {
							if err := drainGraphFaultRows(native.QueryContext(ctx, statement, args...)); err != nil {
								return nil, err
							}
						}
						if point == "wrong_result" {
							return native.QueryContext(ctx, "SELECT 777::bigint UNION ALL SELECT 888::bigint")
						}
						if point == "late_cancel" {
							cancel()
							return nil, ctx.Err()
						}
						return nil, failure
					}
					return native.QueryContext(ctx, statement, args...)
				}
				return transaction, nil
			}, func(session *transactionSession) error {
				table, _ := quoteTable(backend.schema, "graph_self")
				if _, err := session.transaction.ExecContext(t.Context(), "INSERT INTO "+table+" VALUES(99,99,'parent',NULL)"); err != nil {
					return err
				}
				plan, a, b := insertgraphtest.Cycle(t, session, "fault")
				left, right = a, b
				keys, err := session.InsertGraph(work, plan)
				if err == nil || keys != nil || attempts != 1 {
					t.Fatal("graph failure leaked keys or retried", keys, attempts, err)
				}
				switch point {
				case "wrong_result":
					if !errors.Is(err, &query.Error{Code: query.CodeUnexpectedRows}) {
						t.Fatal("wrong native keys were not diagnosed", err)
					}
				case "late_cancel":
					if !errors.Is(err, context.Canceled) {
						t.Fatal("graph cancellation lost its cause", err)
					}
				default:
					if !errors.Is(err, failure) {
						t.Fatal("injected failure not reached or lost", fired, err)
					}
				}
				if point == "unconfirmed_rollback" || point == "unconfirmed_release" {
					if !errors.Is(err, &query.Error{Code: query.CodeTransactionRollbackRequired}) || session.ValidateSession(t.Context()) == nil {
						t.Fatal("unconfirmed private savepoint left a usable parent", err)
					}
					return nil // The root must reject even a swallowed cleanup error.
				}
				insertgraphtest.RequireRows(t, session, "graph_left", nil)
				insertgraphtest.RequireRows(t, session, "graph_right", nil)
				insertgraphtest.RequireRows(t, session, "graph_self", []insertgraphtest.Row{marker})
				keys, err = session.InsertGraph(t.Context(), plan)
				if err != nil || len(keys) != 2 || keys[0] != a.Key || keys[1] != b.Key {
					t.Fatal("confirmed rollback prevented the next graph", keys, err)
				}
				return nil
			})
			if point == "unconfirmed_rollback" || point == "unconfirmed_release" {
				if !errors.Is(err, &query.Error{Code: query.CodeTransactionRollbackRequired}) {
					t.Fatal("root committed after unconfirmed savepoint cleanup", err)
				}
				insertgraphtest.RequireRows(t, observer, "graph_left", nil)
				insertgraphtest.RequireRows(t, observer, "graph_right", nil)
				insertgraphtest.RequireRows(t, observer, "graph_self", nil)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			insertgraphtest.RequireRows(t, observer, "graph_left", []insertgraphtest.Row{left})
			insertgraphtest.RequireRows(t, observer, "graph_right", []insertgraphtest.Row{right})
			insertgraphtest.RequireRows(t, observer, "graph_self", []insertgraphtest.Row{marker})
		})
	}
}

func TestPostgresInsertGraphReservesTheWholeNativeOperation(t *testing.T) {
	backend, _ := postgresInsertGraphFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var hold sync.Once
	err := backend.atomic(t.Context(), func(ctx context.Context) (transactionHandle, error) {
		native, err := backend.database.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		transaction := &insertGraphFaultTransaction{Tx: native}
		transaction.query = func(ctx context.Context, statement string, arguments []any) (*sql.Rows, error) {
			var interrupted error
			if strings.HasPrefix(statement, "WITH godj_graph_") {
				hold.Do(func() {
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
						interrupted = ctx.Err()
					}
				})
			}
			if interrupted != nil {
				return nil, interrupted
			}
			return native.QueryContext(ctx, statement, arguments...)
		}
		return transaction, nil
	}, func(session *transactionSession) error {
		insertgraphtest.CheckInFlight(t, session, entered, sync.OnceFunc(func() { close(release) }))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPostgresInsertGraphUncertainOwnerNeverReportsSuccess(t *testing.T) {
	for _, outcome := range []string{"commit", "rollback"} {
		t.Run(outcome, func(t *testing.T) {
			backend, observer := postgresInsertGraphFixture(t)
			failure, ownerFailure := errors.New("native graph acknowledgement failure"), errors.New("owner rejected graph")
			calls := 0
			var retained db.GraphInserter
			var left, right insertgraphtest.Row
			err := backend.atomic(t.Context(), func(ctx context.Context) (transactionHandle, error) {
				native, err := backend.database.BeginTx(ctx, nil)
				if err != nil {
					return nil, err
				}
				fault := &autoKeyFaultTransaction{Tx: native}
				if outcome == "commit" {
					fault.commitFailure = failure
				} else {
					fault.rollbackFailure = failure
				}
				return fault, nil
			}, func(session *transactionSession) error {
				calls++
				retained = session
				plan, a, b := insertgraphtest.Cycle(t, session, "unknown")
				left, right = a, b
				keys, err := session.InsertGraph(t.Context(), plan)
				if err != nil || len(keys) != 2 || keys[0] != a.Key || keys[1] != b.Key {
					t.Fatal("graph did not use actual native keys", keys, err)
				}
				if outcome == "rollback" {
					return ownerFailure
				}
				return nil
			})
			want := query.CodeTransactionOutcomeUnknown
			if outcome == "commit" {
				want = query.CodeCommitOutcomeUnknown
			}
			if calls != 1 || !errors.Is(err, failure) || !errors.Is(err, &query.Error{Code: want}) || outcome == "rollback" && !errors.Is(err, ownerFailure) {
				t.Fatal("uncertain graph owner retried or reported success", calls, err)
			}
			if keys, err := retained.InsertGraph(context.Background(), query.InsertGraphPlan{}); keys != nil || err == nil {
				t.Fatal("uncertain graph owner leaked a live handle", keys, err)
			}
			var expectedLeft, expectedRight []insertgraphtest.Row
			if outcome == "commit" {
				expectedLeft, expectedRight = []insertgraphtest.Row{left}, []insertgraphtest.Row{right}
			}
			insertgraphtest.RequireRows(t, observer, "graph_left", expectedLeft)
			insertgraphtest.RequireRows(t, observer, "graph_right", expectedRight)
		})
	}
}
