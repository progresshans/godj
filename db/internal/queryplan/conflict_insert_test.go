package queryplan_test

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/query"
)

func TestConflictInsertResultRequiresExactRowsAndNeverReadsLastID(t *testing.T) {
	failure := errors.New("rows metadata failed")
	for _, test := range []struct {
		result   sql.Result
		inserted bool
		code     string
		cause    error
	}{
		{result: conflictResult{rows: 0}}, {result: conflictResult{rows: 1}, inserted: true},
		{code: query.CodeInvalidPlan},
		{result: conflictResult{rows: -1}, code: query.CodeUnexpectedRows},
		{result: conflictResult{rows: 2}, code: query.CodeUnexpectedRows},
		{result: conflictResult{rows: 1, err: failure}, cause: failure},
	} {
		t.Run(fmt.Sprint(test.result), func(t *testing.T) {
			inserted, err := queryplan.ConflictInsertResult(test.result)
			if inserted != test.inserted || (err != nil) != (test.code != "" || test.cause != nil) ||
				(test.code != "" && !errors.Is(err, &query.Error{Code: test.code})) ||
				(test.cause != nil && !errors.Is(err, test.cause)) {
				t.Fatalf("result = %v, %v", inserted, err)
			}
		})
	}
}

type conflictResult struct {
	rows int64
	err  error
}

func (conflictResult) LastInsertId() (int64, error) {
	panic("conflict result must not read a stale last insert ID")
}
func (r conflictResult) RowsAffected() (int64, error) { return r.rows, r.err }
