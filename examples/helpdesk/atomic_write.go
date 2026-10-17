package helpdesk

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/progresshans/godj/db"
)

type applicationRelationOwner struct{ db.RelationAtomic }

func (owner applicationRelationOwner) Atomic(ctx context.Context, run func(db.Session) error) error {
	return owner.AtomicRelation(ctx, func(session db.RelationSession) error { return run(session) })
}

func runApplicationRelationAtomic[T any](ctx context.Context, backend db.RelationAtomic, operation string, run func(context.Context, db.RelationSession) (T, error)) (T, error) {
	return runApplicationAtomic(ctx, applicationRelationOwner{backend}, operation, func(ctx context.Context, session db.Session) (T, error) {
		relation, valid := session.(db.RelationSession)
		if !valid || nilFormReader(relation) {
			var zero T
			return zero, fmt.Errorf("helpdesk: %s transaction lost its relation session", operation)
		}
		return run(ctx, relation)
	})
}

// runApplicationAtomic publishes only one joined callback's confirmed result.
// A broken adapter cannot return success after skipping, repeating, swallowing
// or retaining application work. Cancellation after confirmed commit does not
// turn the result into a retryable write.
func runApplicationAtomic[T any](ctx context.Context, backend db.Atomic, operation string, run func(context.Context, db.Session) (T, error)) (T, error) {
	var zero T
	workContext, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	var result T
	var callbackErr error
	entries, completed, sealed := 0, false, false
	err := backend.Atomic(ctx, func(session db.Session) error {
		mu.Lock()
		if sealed {
			mu.Unlock()
			return fmt.Errorf("helpdesk: %s transaction callback outlived its owner", operation)
		}
		entries++
		if entries != 1 {
			mu.Unlock()
			return fmt.Errorf("helpdesk: repeated %s transaction callback", operation)
		}
		mu.Unlock()
		value, err := run(workContext, session)
		mu.Lock()
		result, callbackErr, completed = value, err, true
		mu.Unlock()
		return err
	})
	mu.Lock()
	sealed = true
	valid := entries <= 1 && (entries == 0 && err != nil || completed)
	value, failure := result, callbackErr
	mu.Unlock()
	if !valid || failure != nil && (err == nil || !errors.Is(err, failure)) {
		return zero, errors.Join(fmt.Errorf("helpdesk: invalid %s transaction ownership", operation), err, failure)
	}
	if err != nil {
		return zero, operationError(ctx, err)
	}
	return value, nil
}
