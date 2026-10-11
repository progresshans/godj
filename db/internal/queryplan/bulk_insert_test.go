package queryplan_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/query"
)

type bulkRowsState struct {
	values            []driver.Value
	nextErr, closeErr error
	closed            atomic.Int64
	cancel            context.CancelFunc
}

type bulkRowsConnector struct{ state *bulkRowsState }

func (c bulkRowsConnector) Connect(context.Context) (driver.Conn, error) {
	return bulkRowsConnection{state: c.state}, nil
}
func (c bulkRowsConnector) Driver() driver.Driver { return bulkRowsFactory{c} }

type bulkRowsFactory struct{ bulkRowsConnector }

func (f bulkRowsFactory) Open(string) (driver.Conn, error) {
	return f.Connect(context.Background())
}

type bulkRowsConnection struct{ state *bulkRowsState }

func (bulkRowsConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (bulkRowsConnection) Begin() (driver.Tx, error) { return nil, errors.New("unexpected begin") }
func (bulkRowsConnection) Close() error              { return nil }
func (c bulkRowsConnection) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &bulkRows{state: c.state}, nil
}

type bulkRows struct {
	state *bulkRowsState
	next  int
}

func (*bulkRows) Columns() []string { return []string{"id"} }
func (r *bulkRows) Close() error {
	r.state.closed.Add(1)
	return r.state.closeErr
}
func (r *bulkRows) Next(values []driver.Value) error {
	if r.next == len(r.state.values) {
		if r.state.nextErr != nil {
			return r.state.nextErr
		}
		return io.EOF
	}
	values[0] = r.state.values[r.next]
	r.next++
	if r.state.cancel != nil {
		r.state.cancel()
	}
	return nil
}

type bulkExecutor struct {
	database *sql.DB
	queryErr error
	nilRows  bool
	result   sql.Result
	execErr  error
}

func (e bulkExecutor) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return e.result, e.execErr
}
func (e bulkExecutor) QueryContext(ctx context.Context, statement string, args ...any) (*sql.Rows, error) {
	if e.nilRows {
		return nil, e.queryErr
	}
	rows, err := e.database.QueryContext(ctx, statement, args...)
	return rows, errors.Join(err, e.queryErr)
}

func bulkResultPlan(t *testing.T, ignore bool) query.BulkInsertPlan {
	t.Helper()
	policy := query.BulkConflict{}
	if ignore {
		var err error
		policy, err = query.NewBulkConflict(query.BulkConflictIgnore, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	plan, err := query.NewBulkInsertPlan("only_key", nil, [][]query.Value{nil, nil}, id, policy)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestBulkInsertReturningDiscardsPartialKeysAndClosesTransport(t *testing.T) {
	failure := errors.New("native bulk row failure")
	for _, mode := range []string{"success", "short", "extra", "scan", "next", "close", "cancel", "rows_and_error", "nil_rows", "query_error"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			state := &bulkRowsState{values: []driver.Value{int64(1), int64(9007199254740993)}}
			executor := bulkExecutor{}
			switch mode {
			case "short":
				state.values = state.values[:1]
			case "extra":
				state.values = append(state.values, int64(3))
			case "scan":
				state.values[1] = "not an integer key"
			case "next":
				state.nextErr = failure
			case "close":
				state.closeErr = failure
			case "cancel":
				state.cancel = cancel
			case "rows_and_error":
				executor.queryErr = failure
			case "nil_rows":
				executor.nilRows = true
			case "query_error":
				executor.nilRows, executor.queryErr = true, failure
			}
			executor.database = sql.OpenDB(bulkRowsConnector{state})
			defer executor.database.Close()
			result, err := queryplan.ExecuteBulkInsert(ctx, executor, bulkResultPlan(t, false), "authored returning statement", nil, func(err error) error { return err })
			if mode == "success" {
				if err != nil || result.RowsAffected != 2 || len(result.Keys) != 2 || result.Keys[0] != 1 || result.Keys[1] != 9007199254740993 {
					t.Fatal(result, err)
				}
			} else if err == nil || result.RowsAffected != 0 || len(result.Keys) != 0 {
				t.Fatal("partial native output escaped failure", result, err)
			}
			if (mode == "next" || mode == "close" || mode == "rows_and_error" || mode == "query_error") && !errors.Is(err, failure) {
				t.Fatal("native cause lost", err)
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost", err)
			}
			if want := int64(1); executor.nilRows {
				if state.closed.Load() != 0 {
					t.Fatal("closed nonexistent rows")
				}
			} else if state.closed.Load() != want {
				t.Fatal("native row transport not closed exactly once", state.closed.Load())
			}
		})
	}
}

type bulkResultFailure struct{ err error }

func (bulkResultFailure) LastInsertId() (int64, error)   { return 0, errors.New("unused") }
func (r bulkResultFailure) RowsAffected() (int64, error) { return 0, r.err }

func TestBulkInsertIgnoredRowsRequireBoundedActualResultMetadata(t *testing.T) {
	failure := errors.New("native result failure")
	for _, test := range []struct {
		name    string
		result  sql.Result
		err     error
		allowed bool
	}{
		{"none", driver.RowsAffected(0), nil, true}, {"some", driver.RowsAffected(1), nil, true}, {"all", driver.RowsAffected(2), nil, true},
		{"negative", driver.RowsAffected(-1), nil, false}, {"too_many", driver.RowsAffected(3), nil, false},
		{"nil", nil, nil, false}, {"driver", nil, failure, false}, {"metadata", bulkResultFailure{failure}, nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := queryplan.ExecuteBulkInsert(t.Context(), bulkExecutor{result: test.result, execErr: test.err}, bulkResultPlan(t, true), "authored ignored statement", nil, func(err error) error { return err })
			if test.allowed {
				want, _ := test.result.RowsAffected()
				if err != nil || result.RowsAffected != want || len(result.Keys) != 0 {
					t.Fatal(result, err)
				}
			} else if err == nil || result.RowsAffected != 0 || len(result.Keys) != 0 {
				t.Fatal("invented native success", result, err)
			}
			if (test.name == "driver" || test.name == "metadata") && !errors.Is(err, failure) {
				t.Fatal("native cause lost", err)
			}
		})
	}
}
