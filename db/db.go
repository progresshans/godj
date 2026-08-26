// Package db defines the narrow execution boundary between the generic ORM
// and database backends. Backend-specific SQL stays behind Queryer.
package db

import (
	"context"

	"github.com/progresshans/godj/query"
)

type Row interface {
	Scan(destinations ...any) error
}

type Rows interface {
	Row
	Next() bool
	Err() error
	Close() error
}

type Queryer interface {
	// Query returns non-nil Rows with a nil error on success. On failure it
	// returns a non-nil error; if it also returns Rows, the caller closes them.
	// Returning nil Rows and a nil error is a backend contract violation.
	Query(context.Context, query.Plan) (Rows, error)
}

// Mutator is the database-independent one-row write boundary. Implementations
// compile immutable query plans and return backend result metadata.
type Mutator interface {
	Insert(context.Context, query.InsertPlan) (lastInsertID int64, err error)
	// Update returns the exact affected-row count. Generic Save orchestration
	// distinguishes zero rows from one row and must not receive a synthesized
	// success count from a backend.
	Update(context.Context, query.UpdatePlan) (rowsAffected int64, err error)
	Delete(context.Context, query.DeletePlan) (rowsAffected int64, err error)
}

// Session is bound to one transaction for the duration of an Atomic callback.
// It is invalid once that callback returns.
type Session interface {
	Queryer
	Mutator
}

// Atomic executes callback in one transaction-bound Session. A nil callback,
// callback error, or context cancellation observed before commit does not
// commit writes when rollback is confirmed. An error returned by the literal
// COMMIT operation has an unknown outcome: implementations return
// query.CodeCommitOutcomeUnknown, callers must not retry automatically, and
// reconciliation is required before deciding whether to issue the work again.
type Atomic interface {
	Atomic(context.Context, func(Session) error) error
}

// CoordinatedAtomic executes callback in a transaction-bound Session after
// acquiring the backend's database/schema coordination fence. An acquisition
// error or cancellation before the fence is acquired invokes callback zero
// times. Once the fence is acquired, callback is invoked exactly once, even if
// the context becomes canceled at that boundary. Implementations execute
// synchronously and never retry acquisition, callback, or commit on the
// caller's behalf. Callers must not nest backend transactions or acquire a
// different backend coordination domain from callback.
//
// Callback errors and cancellation observed after callback return roll back
// when rollback can be confirmed. A literal COMMIT error has the same unknown
// outcome and no-retry contract as Atomic: implementations return
// query.CodeCommitOutcomeUnknown and callers reconcile before issuing the work
// again.
type CoordinatedAtomic interface {
	CoordinatedAtomic(context.Context, func(Session) error) error
}
