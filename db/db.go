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
