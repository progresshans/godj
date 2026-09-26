package db

import (
	"context"

	"github.com/progresshans/godj/query"
)

// ConflictInserter is a separate one-row write capability: a native conflict
// against the explicit unique tuple is a no-op; other errors are preserved.
// inserted is true only for one affected row and is always false on error.
// No generated key is returned. A false/nil result reports zero affected rows,
// not independent proof of an existing row (a database trigger can skip an
// insertion too). Callers requiring final membership must verify it.
//
// A transaction session's result is provisional until its owner confirms
// commit. Implementations never retry or convert an uncertain transaction
// outcome into success. A backend/session without this capability must be
// rejected explicitly rather than replaced with a racy query-then-insert.
type ConflictInserter interface {
	InsertOnConflict(context.Context, query.ConflictInsertPlan) (inserted bool, err error)
}
