package db

import (
	"context"

	"github.com/progresshans/godj/query"
)

// BulkInsertLimits describes one statement, not the enclosing operation.
// Positive row and parameter budgets let the ORM split an input while keeping
// every batch inside the same write scope. Zero or unknown limits are invalid.
type BulkInsertLimits struct {
	Rows       int
	Parameters int
}

// BulkInsertResult owns its ordered keys. Normal and conflict-update inserts
// return exactly one key per input row, including repeated keys when supported
// by the native statement. Conflict-ignore returns no keys because omitted
// rows cannot be positionally associated with the input. RowsAffected is the
// actual statement count and does not distinguish inserts from updates.
type BulkInsertResult struct {
	Keys         []int64
	RowsAffected int64
}

// BulkInserter is an optional native multi-row write capability. Methods use
// the receiver's actual connection/transaction and validate context and scope
// even when returning limits. A returned error never implies safe retry; the
// enclosing owner controls rollback, commit and uncertain outcomes.
type BulkInserter interface {
	BulkInsertLimits(context.Context) (BulkInsertLimits, error)
	BulkInsert(context.Context, query.BulkInsertPlan) (BulkInsertResult, error)
}
