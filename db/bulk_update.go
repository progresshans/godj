package db

import (
	"context"

	"github.com/progresshans/godj/query"
)

// BulkUpdater is an optional native multi-row update capability. BatchSize
// validates the source/field shape and reserves the actual parameters used by
// its predicate before returning a positive row limit. Both methods validate
// context and receiver lifetime, and use that receiver's actual connection.
// BulkUpdate reports matched rows, including unchanged values; duplicate and
// missing keys may reduce the count. It never returns a partial count on error.
// The enclosing owner controls all batches, rollback and uncertain outcomes.
type BulkUpdater interface {
	BulkUpdateBatchSize(context.Context, query.BulkUpdateSpec) (int, error)
	BulkUpdate(context.Context, query.BulkUpdatePlan) (int64, error)
}
