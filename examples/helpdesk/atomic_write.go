package helpdesk

import (
	"context"

	"github.com/progresshans/godj/db"
)

// Preserve direct application errors after a confirmed rollback. HTTP/Admin
// adapters distinguish those exact values from wrapped/joined infrastructure
// failures (including the atomic owner's unknown-outcome errors).
func runApplicationRelationAtomic[T any](ctx context.Context, backend db.RelationAtomic, run func(context.Context, db.RelationSession) (T, error)) (T, error) {
	result, err := db.RelationAtomicResult(ctx, backend, run)
	if err != nil {
		var zero T
		return zero, operationError(ctx, err)
	}
	return result, nil
}

func runApplicationAtomic[T any](ctx context.Context, backend db.Atomic, run func(context.Context, db.Session) (T, error)) (T, error) {
	result, err := db.AtomicResult(ctx, backend, run)
	if err != nil {
		var zero T
		return zero, operationError(ctx, err)
	}
	return result, nil
}
