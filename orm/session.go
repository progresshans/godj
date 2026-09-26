package orm

import (
	"context"
	"errors"

	"github.com/progresshans/godj/db"
)

// A session cache is provisional and cannot outlive its transaction callback.
// Root backends have no SessionValidator and retain ordinary cache semantics.
func validateQuerySession(ctx context.Context, backend db.Queryer) error {
	_, err := executionBackend(ctx, backend)
	return err
}

func sessionReadResult[T any](ctx context.Context, backend db.Queryer, value T, err error) (T, error) {
	if scopeErr := validateQuerySession(ctx, backend); scopeErr != nil {
		var zero T
		if err == nil {
			return zero, scopeErr
		}
		return zero, errors.Join(err, scopeErr)
	}
	return value, err
}
