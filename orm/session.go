package orm

import (
	"context"
	"errors"

	"github.com/progresshans/godj/db"
)

// A session cache is provisional and cannot outlive its transaction callback.
// Root backends have no SessionValidator and retain ordinary cache semantics.
func validateQuerySession(ctx context.Context, backend db.Queryer) error {
	scope, scoped := backend.(db.SessionValidator)
	if !scoped {
		return nil
	}
	if interfaceIsNil(scope) {
		return relationBackendInvalidPlan("query session validator is nil")
	}
	return joinContextErr(scope.ValidateSession(ctx), ctx)
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
