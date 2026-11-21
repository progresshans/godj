package articleapp

import (
	"context"

	"github.com/progresshans/godj/db"
)

// preparedAtomic preserves Article's operation-specific error contract around
// the shared one-callback, confirmed-result transaction boundary.
func preparedAtomic[T any](ctx context.Context, repository Repository, operation MutationOperation, run func(context.Context, db.Session) (T, error)) (T, error) {
	result, err := db.AtomicResult(ctx, repository.backend, run)
	if err != nil {
		var zero T
		return zero, mutationError(ctx, string(operation), err)
	}
	return result, nil
}
