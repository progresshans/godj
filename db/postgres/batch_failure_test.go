package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/batchtest"
	"github.com/progresshans/godj/query"
)

type batchFaultState struct {
	transaction                                                 *transactionTestState
	mu                                                          sync.Mutex
	declares, fetches, rowCloses, cursorCloses                  int
	declareError, fetchError, rowError, closeError, cursorError error
}

type batchFaultConnector struct{ state *batchFaultState }

func (connector batchFaultConnector) Connect(context.Context) (driver.Conn, error) {
	return &batchFaultConnection{transactionTestConnection: transactionTestConnection{state: connector.state.transaction}, fault: connector.state}, nil
}
func (connector batchFaultConnector) Driver() driver.Driver {
	return transactionTestDriver{state: connector.state.transaction}
}

type batchFaultConnection struct {
	transactionTestConnection
	fault *batchFaultState
}

func (connection *batchFaultConnection) ExecContext(ctx context.Context, statement string, _ []driver.NamedValue) (driver.Result, error) {
	connection.fault.mu.Lock()
	defer connection.fault.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.HasPrefix(statement, "DECLARE ") {
		connection.fault.declares++
		return driver.RowsAffected(0), connection.fault.declareError
	}
	if strings.HasPrefix(statement, "CLOSE ") {
		connection.fault.cursorCloses++
		return driver.RowsAffected(0), connection.fault.cursorError
	}
	return nil, errors.New("unexpected batch SQL")
}
func (connection *batchFaultConnection) QueryContext(ctx context.Context, statement string, _ []driver.NamedValue) (driver.Rows, error) {
	connection.fault.mu.Lock()
	defer connection.fault.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(statement, "FETCH FORWARD ") {
		return nil, errors.New("source query was reissued")
	}
	connection.fault.fetches++
	if connection.fault.fetchError != nil {
		return nil, connection.fault.fetchError
	}
	return &batchFaultRows{fault: connection.fault, transactionTestRows: transactionTestRows{values: [][]driver.Value{{int64(1)}, {int64(2)}}}}, nil
}

type batchFaultRows struct {
	transactionTestRows
	fault *batchFaultState
}

func (rows *batchFaultRows) Next(values []driver.Value) error {
	if rows.index == 1 && rows.fault.rowError != nil {
		return rows.fault.rowError
	}
	return rows.transactionTestRows.Next(values)
}
func (rows *batchFaultRows) Close() error {
	rows.fault.mu.Lock()
	defer rows.fault.mu.Unlock()
	rows.fault.rowCloses++
	return rows.fault.closeError
}

func TestPostgresBatchFailureClosesBeforePublicationAndKeepsTransactionOwner(t *testing.T) {
	for _, failure := range []string{"declare", "fetch", "row", "row_close", "cursor_close", "panic", "callback_and_cleanup", "commit_unknown"} {
		t.Run(failure, func(t *testing.T) {
			signal, cleanup := errors.New("injected native failure"), errors.New("injected cleanup failure")
			state := &batchFaultState{transaction: &transactionTestState{}}
			switch failure {
			case "declare":
				state.declareError = signal
			case "fetch":
				state.fetchError = signal
			case "row":
				state.rowError = signal
			case "row_close":
				state.closeError = signal
			case "cursor_close":
				state.cursorError = signal
			case "callback_and_cleanup":
				state.cursorError = cleanup
			case "commit_unknown":
				state.transaction.setCommitError(signal)
			}
			backend := &Backend{database: sql.OpenDB(batchFaultConnector{state: state}), schema: "godj_test"}
			t.Cleanup(func() { _ = backend.Close() })
			yields := 0
			var err error
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				err = backend.Atomic(t.Context(), func(session db.Session) error {
					return session.(db.BatchQueryer).QueryBatches(t.Context(), batchtest.Plan(), 2, func(row db.Row) error { var value int64; return row.Scan(&value) }, func(db.Queryer) (bool, error) {
						yields++
						state.mu.Lock()
						closed := state.rowCloses
						state.mu.Unlock()
						if closed != 1 {
							t.Fatal("published while native rows open")
						}
						if failure == "panic" {
							panic(signal)
						}
						if failure == "callback_and_cleanup" {
							return false, signal
						}
						return false, nil
					})
				})
			}()
			if failure == "panic" {
				if recovered != signal {
					t.Fatal("panic lost", recovered)
				}
			} else if !errors.Is(err, signal) {
				t.Fatal("native failure lost", err)
			}
			if failure == "callback_and_cleanup" && !errors.Is(err, cleanup) {
				t.Fatal("cleanup failure lost", err)
			}
			if failure == "commit_unknown" && !errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) {
				t.Fatal("commit classified as definite failure", err)
			}
			wantFetch, wantClose, wantRows, wantYield := 1, 1, 1, 1
			switch failure {
			case "declare":
				wantFetch, wantClose, wantRows, wantYield = 0, 0, 0, 0
			case "fetch":
				wantRows, wantYield = 0, 0
			case "row", "row_close":
				wantYield = 0
			}
			state.mu.Lock()
			if state.declares != 1 || state.fetches != wantFetch || state.cursorCloses != wantClose || state.rowCloses != wantRows || yields != wantYield {
				t.Errorf("resource/publication counts: %d %d %d %d %d", state.declares, state.fetches, state.cursorCloses, state.rowCloses, yields)
			}
			state.mu.Unlock()
			transaction := state.transaction.snapshot()
			if transaction.begins != 1 || failure == "commit_unknown" && transaction.commits != 1 || failure != "commit_unknown" && (transaction.commits != 0 || transaction.rollbacks != 1) {
				t.Fatal("transaction owner changed", transaction)
			}
		})
	}
}
