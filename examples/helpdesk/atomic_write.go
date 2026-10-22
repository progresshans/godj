package helpdesk

import (
	"context"
	"fmt"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/validation"
)

func runApplicationRelationAtomic[T any](ctx context.Context, backend db.RelationAtomic, operation string, run func(context.Context, db.RelationSession) (T, error)) (T, error) {
	result, err := db.RelationAtomicResult(ctx, backend, run)
	if err != nil {
		var zero T
		return zero, applicationWriteError(ctx, operation, err)
	}
	return result, nil
}

func runApplicationAtomic[T any](ctx context.Context, backend db.Atomic, operation string, run func(context.Context, db.Session) (T, error)) (T, error) {
	result, err := db.AtomicResult(ctx, backend, run)
	if err != nil {
		var zero T
		return zero, applicationWriteError(ctx, operation, err)
	}
	return result, nil
}

func applicationWriteError(ctx context.Context, operation string, err error) error {
	// These exact errors identify a confirmed rollback's client outcome. Do not
	// wrap them or search through an uncertain transaction's error tree.
	if err == admin.ErrObjectNotFound {
		return operationError(ctx, err)
	}
	if _, rejected := validation.Rejected(err); rejected {
		return operationError(ctx, err)
	}
	return operationError(ctx, fmt.Errorf("helpdesk: %s: %w", operation, err))
}
