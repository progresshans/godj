package db

import (
	"context"
	"fmt"

	"github.com/progresshans/godj/query"
)

// ConflictInserter is a separate one-row write capability: a native conflict
// against the explicit unique tuple is a no-op; other errors are preserved.
// Inserted is true only for one returned row; Key is its actual generated key.
// Every error discards the result. A false/nil result reports zero affected rows,
// not independent proof of an existing row (a database trigger can skip an
// insertion too). Callers requiring final membership must verify it.
//
// A transaction session's result is provisional until its owner confirms
// commit. Implementations never retry or convert an uncertain transaction
// outcome into success. A backend/session without this capability must be
// rejected explicitly rather than replaced with a racy query-then-insert.
type ConflictInserter interface {
	InsertOnConflict(context.Context, query.ConflictInsertPlan) (ConflictInsertResult, error)
}

// ConflictInsertResult distinguishes an actual signed key (including zero)
// from no insertion. A session result is provisional until its owner commits.
type ConflictInsertResult struct {
	Inserted bool
	Key      int64
}

func (ConflictInsertResult) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("db.ConflictInsertResult{redacted}"))
}
