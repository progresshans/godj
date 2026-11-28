package queryplan_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/query"
)

// The old RowsAffected/LastInsertId metadata path is replaced by exact native
// RETURNING cardinality and transport ownership, including zero and signed keys.
func TestConflictInsertReturningDiscardsPartialKeysAndClosesTransport(t *testing.T) {
	failure := errors.New("native returning failure")
	for _, mode := range []string{"success", "zero", "negative", "noop", "extra", "null", "scan", "next", "close", "cancel", "rows_and_error", "nil_rows", "query_error"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			key := int64(9007199254740993)
			state := &bulkRowsState{values: []driver.Value{key}}
			executor := bulkExecutor{}
			switch mode {
			case "zero":
				key = 0
				state.values[0] = key
			case "negative":
				key = -7
				state.values[0] = key
			case "noop":
				state.values = nil
			case "extra":
				state.values = append(state.values, int64(2))
			case "null":
				state.values[0] = nil
			case "scan":
				state.values[0] = "not an integer"
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
				executor.nilRows = true
				executor.queryErr = failure
			}
			executor.database = sql.OpenDB(bulkRowsConnector{state})
			defer executor.database.Close()
			result, err := queryplan.ExecuteConflictInsert(ctx, executor, "authored native insert", nil, func(err error) error { return err })
			switch mode {
			case "success", "zero", "negative":
				if err != nil || !result.Inserted || result.Key != key {
					t.Fatal("actual key lost", result, err)
				}
			case "noop":
				if err != nil || result != (db.ConflictInsertResult{}) {
					t.Fatal("no-op returned an unrelated key", result, err)
				}
			default:
				if err == nil || result != (db.ConflictInsertResult{}) {
					t.Fatal("partial native output escaped failure", result, err)
				}
			}
			if (mode == "next" || mode == "close" || mode == "rows_and_error" || mode == "query_error") && !errors.Is(err, failure) {
				t.Fatal("cause lost", err)
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost", err)
			}
			if mode == "extra" && !errors.Is(err, &query.Error{Code: query.CodeUnexpectedRows}) {
				t.Fatal("cardinality lost", err)
			}
			want := int64(1)
			if executor.nilRows {
				want = 0
			}
			if state.closed.Load() != want {
				t.Fatal("transport close count", state.closed.Load(), want)
			}
		})
	}
}
