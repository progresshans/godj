package db

import (
	"context"
	"github.com/progresshans/godj/query"
)

// QueryUpdater compiles and executes one native uniform UPDATE, without
// reading rows into application memory. CheckQueryUpdate validates context,
// receiver lifetime, metadata and backend capabilities without database I/O.
// QueryUpdate repeats those checks and returns matched rows, including rows
// whose value did not change. It never returns a partial count on error.
// The enclosing transaction/savepoint owner resolves rollback and uncertainty.
type QueryUpdater interface {
	CheckQueryUpdate(context.Context, query.QueryUpdatePlan) error
	QueryUpdate(context.Context, query.QueryUpdatePlan) (int64, error)
}
