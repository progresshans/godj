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
// callback error, context cancellation, or commit error cannot commit writes.
type Atomic interface {
	Atomic(context.Context, func(Session) error) error
}
