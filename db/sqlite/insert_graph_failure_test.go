package sqlite

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

func TestSQLiteInsertGraphFailuresStayInsideSavepoint(t *testing.T) {
	for _, point := range []string{"create_input", "create_receipts", "create_trigger", "insert_before", "insert_after", "read_receipts", "wrong_receipt", "readback", "drop_before", "drop_after", "late_cancel", "unconfirmed_rollback", "unconfirmed_release"} {
		t.Run(point, func(t *testing.T) {
			backend, observer := sqliteInsertGraphFixture(t)
			backend.database.SetMaxOpenConns(1)
			transaction, err := backend.database.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer transaction.Rollback()
			marker := insertgraphtest.Row{Key: 99, Peer: 99, Token: "parent"}
			if _, err := transaction.ExecContext(t.Context(), "INSERT INTO graph_self VALUES(99,99,'parent',NULL)"); err != nil {
				t.Fatal(err)
			}
			failure := errors.New("injected graph boundary failure")
			work, cancel := context.WithCancel(t.Context())
			defer cancel()
			fired, attempts := false, 0
			connection := &relationFaultConnection{
				exec: func(ctx context.Context, statement string, arguments []any) (sql.Result, error) {
					write := strings.HasPrefix(statement, `INSERT INTO temp."godj_graph_`)
					if write {
						attempts++
					}
					if point == "unconfirmed_rollback" && strings.HasPrefix(statement, "ROLLBACK TO SAVEPOINT") || point == "unconfirmed_release" && strings.HasPrefix(statement, "RELEASE SAVEPOINT") {
						return nil, failure
					}
					target := !fired && (point == "create_input" && strings.HasPrefix(statement, "CREATE TEMP TABLE") && strings.Contains(statement, "_input\"") ||
						point == "create_receipts" && strings.HasPrefix(statement, "CREATE TEMP TABLE") && strings.Contains(statement, "_receipts\"") ||
						point == "create_trigger" && strings.HasPrefix(statement, "CREATE TEMP TRIGGER") ||
						(point == "insert_before" || point == "insert_after" || point == "unconfirmed_rollback") && write ||
						(point == "drop_before" || point == "drop_after") && strings.HasPrefix(statement, "DROP TABLE temp."))
					if target {
						fired = true
						if point == "insert_after" || point == "drop_after" {
							if _, err := transaction.ExecContext(ctx, statement, arguments...); err != nil {
								return nil, err
							}
						}
						return nil, failure
					}
					return transaction.ExecContext(ctx, statement, arguments...)
				},
				query: func(ctx context.Context, statement string, arguments []any) (*sql.Rows, error) {
					receipts := strings.HasPrefix(statement, "SELECT graph_key FROM temp.")
					readback := strings.HasPrefix(statement, "SELECT graph_key FROM (")
					if !fired && ((point == "read_receipts" || point == "wrong_receipt") && receipts || (point == "readback" || point == "late_cancel") && readback) {
						fired = true
						if point == "wrong_receipt" {
							return transaction.QueryContext(ctx, "SELECT 777 UNION ALL SELECT 888")
						}
						if point == "late_cancel" {
							cancel()
							return nil, ctx.Err()
						}
						return nil, failure
					}
					return transaction.QueryContext(ctx, statement, arguments...)
				},
			}
			session := newRelationSession(t.Context(), connection, nil)
			defer session.scope.Finish()
			plan, left, right := insertgraphtest.Cycle(t, session, "fault")
			keys, err := session.InsertGraph(work, plan)
			if err == nil || keys != nil || attempts > 1 {
				t.Fatal("graph failure leaked keys or retried native insertion", keys, attempts, err)
			}
			switch point {
			case "wrong_receipt":
				if !errors.Is(err, &query.Error{Code: query.CodeUnexpectedRows}) {
					t.Fatal("wrong native keys were not diagnosed", err)
				}
			case "late_cancel":
				if !errors.Is(err, context.Canceled) {
					t.Fatal("cancellation cause lost", err)
				}
			default:
				if !errors.Is(err, failure) {
					t.Fatal("injected failure was not reached or lost", fired, err)
				}
			}
			if point == "unconfirmed_rollback" || point == "unconfirmed_release" {
				if !errors.Is(err, &query.Error{Code: query.CodeTransactionRollbackRequired}) || session.ValidateSession(t.Context()) == nil || session.scope.Finish() == nil {
					t.Fatal("unconfirmed private savepoint remained usable", err)
				}
				if err := transaction.Rollback(); err != nil {
					t.Fatal(err)
				}
				insertgraphtest.RequireRows(t, observer, "graph_left", nil)
				insertgraphtest.RequireRows(t, observer, "graph_right", nil)
				insertgraphtest.RequireRows(t, observer, "graph_self", nil)
				sqliteRequireNoGraphObjects(t, backend.database)
				return
			}
			insertgraphtest.RequireRows(t, session, "graph_left", nil)
			insertgraphtest.RequireRows(t, session, "graph_right", nil)
			insertgraphtest.RequireRows(t, session, "graph_self", []insertgraphtest.Row{marker})
			sqliteRequireNoGraphObjects(t, transaction)
			keys, err = session.InsertGraph(t.Context(), plan)
			if err != nil || len(keys) != 2 || keys[0] != left.Key || keys[1] != right.Key {
				t.Fatal("confirmed rollback prevented the caller's next graph", keys, err)
			}
			if err := session.scope.Finish(); err != nil {
				t.Fatal(err)
			}
			if err := transaction.Commit(); err != nil {
				t.Fatal(err)
			}
			insertgraphtest.RequireRows(t, observer, "graph_left", []insertgraphtest.Row{left})
			insertgraphtest.RequireRows(t, observer, "graph_right", []insertgraphtest.Row{right})
			insertgraphtest.RequireRows(t, observer, "graph_self", []insertgraphtest.Row{marker})
			sqliteRequireNoGraphObjects(t, backend.database)
		})
	}
}

func TestSQLiteInsertGraphReservesTheWholeNativeOperation(t *testing.T) {
	backend, _ := sqliteInsertGraphFixture(t)
	transaction, err := backend.database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	entered, release := make(chan struct{}), make(chan struct{})
	var hold sync.Once
	connection := &relationFaultConnection{
		exec: func(ctx context.Context, statement string, arguments []any) (sql.Result, error) {
			var interrupted error
			if strings.HasPrefix(statement, `INSERT INTO temp."godj_graph_`) {
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
			return transaction.ExecContext(ctx, statement, arguments...)
		},
		query: func(ctx context.Context, statement string, arguments []any) (*sql.Rows, error) {
			return transaction.QueryContext(ctx, statement, arguments...)
		},
	}
	session := newRelationSession(t.Context(), connection, nil)
	defer session.scope.Finish()
	insertgraphtest.CheckInFlight(t, session, entered, sync.OnceFunc(func() { close(release) }))
	sqliteRequireNoGraphObjects(t, transaction)
}

func TestSQLiteInsertGraphUnconfirmedOwnerKeepsConnection(t *testing.T) {
	for _, outcome := range []string{"commit", "rollback"} {
		t.Run(outcome, func(t *testing.T) {
			backend, _ := sqliteInsertGraphFixture(t)
			native, err := backend.database.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				_, _ = native.ExecContext(context.Background(), "ROLLBACK")
				_ = native.Close()
			}()
			failure, ownerFailure := errors.New("native graph terminal failure"), errors.New("owner rejected graph")
			attempts := 0
			connection := &relationFaultConnection{
				exec: func(ctx context.Context, statement string, arguments []any) (sql.Result, error) {
					if statement == "ROLLBACK" || outcome == "commit" && statement == "COMMIT" {
						return nil, failure
					}
					if strings.HasPrefix(statement, `INSERT INTO temp."godj_graph_`) {
						attempts++
					}
					return native.ExecContext(ctx, statement, arguments...)
				},
				query: func(ctx context.Context, statement string, arguments []any) (*sql.Rows, error) {
					return native.QueryContext(ctx, statement, arguments...)
				},
				raw: func(func(any) error) error { return nil }, // No confirmed discard.
			}
			retention := newRelationRetentionState()
			defer retention.sealAndDrain()
			calls := 0
			var retained db.GraphInserter
			err = executeAtomicRelation(t.Context(), func(session db.RelationSession) error {
				calls++
				retained = session.(db.GraphInserter)
				plan, left, right := insertgraphtest.Cycle(t, session, "unknown")
				keys, err := retained.InsertGraph(t.Context(), plan)
				if err != nil || len(keys) != 2 || keys[0] != left.Key || keys[1] != right.Key || !session.(*relationSession).state.mutationPossible.Load() {
					t.Fatal("graph did not own its actual mutations", keys, err)
				}
				if outcome == "rollback" {
					return ownerFailure
				}
				return nil
			}, connection, retention, nil)
			want := query.CodeTransactionOutcomeUnknown
			if outcome == "commit" {
				want = query.CodeCommitOutcomeUnknown
			}
			if calls != 1 || attempts != 1 || !errors.Is(err, failure) || !errors.Is(err, &query.Error{Code: want}) || !errors.Is(err, &query.Error{Code: query.CodeBackendRecoveryRequired}) || retainedConnectionCount(retention) != 1 || connection.closeCalls.Load() != 0 {
				t.Fatal("uncertain graph owner retried, pooled its connection, or reported success", calls, attempts, err)
			}
			if outcome == "rollback" && !errors.Is(err, ownerFailure) {
				t.Fatal("owner error lost", err)
			}
			if keys, err := retained.InsertGraph(context.Background(), query.InsertGraphPlan{}); keys != nil || err == nil {
				t.Fatal("uncertain graph owner leaked a live handle", keys, err)
			}
		})
	}
}
