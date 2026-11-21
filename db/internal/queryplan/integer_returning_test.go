package queryplan_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"slices"
	"testing"

	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/query"
)

func TestIntegerReturningRequiresTheCompleteReservation(t *testing.T) {
	for _, mode := range []string{"complete", "short", "extra", "late_scan", "late_transport", "close", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failure := errors.New("native reservation stream failed")
			state := &bulkRowsState{values: []driver.Value{int64(-7), int64(0), int64(9007199254740993)}}
			switch mode {
			case "short":
				state.values = state.values[:2]
			case "extra":
				state.values = append(state.values, int64(4))
			case "late_scan":
				state.values[2] = "corrupt"
			case "late_transport":
				state.nextErr = failure
			case "close":
				state.closeErr = failure
			case "canceled":
				state.cancel = cancel
			}
			database := sql.OpenDB(bulkRowsConnector{state})
			defer database.Close()
			keys, err := queryplan.ExecuteIntegerReturning(ctx, database, "native reservation", nil, 3, 3, func(err error) error { return err })
			if mode == "complete" {
				if err != nil || !slices.Equal(keys, []int64{-7, 0, 9007199254740993}) {
					t.Fatal("native integer result changed", keys, err)
				}
			} else if err == nil || keys != nil {
				t.Fatal("partial reserved keys escaped", keys, err)
			}
			if (mode == "short" || mode == "extra") && !errors.Is(err, &query.Error{Code: query.CodeUnexpectedRows}) {
				t.Fatal("exact allocation cardinality lost", err)
			}
			if (mode == "late_transport" || mode == "close") && !errors.Is(err, failure) {
				t.Fatal("transport cause lost", err)
			}
			if mode == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost", err)
			}
			if state.closed.Load() != 1 {
				t.Fatal("native reservation cursor was not closed exactly once", state.closed.Load())
			}
		})
	}
}
