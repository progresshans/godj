package db

import (
	"context"
	"errors"
	"sync"

	"github.com/progresshans/godj/query"
)

// AtomicResult publishes a prepared application result only after one joined
// callback and confirmed commit. Prepare output, audit and other transactional
// effects inside run. Do not retain a borrowed session or return a lazy value
// that needs it later. The callback's context is canceled when its owner ends.
//
// A skipped, repeated, unfinished or swallowed callback is an unknown outcome,
// never a successful partial result. Errors are not retried. Cancellation after
// confirmed commit does not convert success into a retryable write failure.
func AtomicResult[T any](ctx context.Context, backend Atomic, run func(context.Context, Session) (T, error)) (T, error) {
	if nilValue(backend) {
		var zero T
		return zero, atomicConfigError("transaction owner is nil")
	}
	return atomicResult(ctx, backend.Atomic, run)
}

// CoordinatedAtomicResult uses the backend's database coordination fence with
// AtomicResult's single-callback, confirmed-commit and no-retry publication
// contract. It never falls back to an unfenced Atomic implementation.
func CoordinatedAtomicResult[T any](ctx context.Context, backend CoordinatedAtomic, run func(context.Context, Session) (T, error)) (T, error) {
	if nilValue(backend) {
		var zero T
		return zero, atomicConfigError("coordinated transaction owner is nil")
	}
	return atomicResult(ctx, backend.CoordinatedAtomic, run)
}

// RelationAtomicResult preserves the relation-session capability while using
// the same prepared-result and callback-ownership contract as AtomicResult.
func RelationAtomicResult[T any](ctx context.Context, backend RelationAtomic, run func(context.Context, RelationSession) (T, error)) (T, error) {
	if nilValue(backend) {
		var zero T
		return zero, atomicConfigError("relation transaction owner is nil")
	}
	return atomicResult(ctx, backend.AtomicRelation, run)
}

func atomicResult[T any, S Session](ctx context.Context, invoke func(context.Context, func(S) error) error, run func(context.Context, S) (T, error)) (T, error) {
	var zero T
	if nilValue(ctx) || run == nil {
		return zero, atomicConfigError("transaction context and callback are required")
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	var result T
	var callbackError error
	entries, completed, sealed := 0, false, false
	err := invoke(ctx, func(session S) error {
		mu.Lock()
		if sealed {
			mu.Unlock()
			return atomicOwnershipError(nil, nil)
		}
		entries++
		if entries != 1 {
			mu.Unlock()
			return atomicOwnershipError(nil, nil)
		}
		mu.Unlock()
		value, failure := func() (T, error) {
			if nilValue(session) {
				return zero, atomicConfigError("transaction session is nil")
			}
			check := func() error {
				if err := work.Err(); err != nil {
					return err
				}
				if validator, borrowed := any(session).(SessionValidator); borrowed {
					return validator.ValidateSession(work)
				}
				return nil
			}
			if err := check(); err != nil {
				return zero, err
			}
			value, err := run(work, session)
			if err != nil {
				return zero, err
			}
			if err := check(); err != nil {
				return zero, err
			}
			return value, nil
		}()
		mu.Lock()
		result, callbackError, completed = value, failure, true
		mu.Unlock()
		return failure
	})
	mu.Lock()
	sealed = true
	valid := entries <= 1 && (entries == 0 && err != nil || completed)
	value, failure := result, callbackError
	mu.Unlock()
	if !valid || failure != nil && (err == nil || !errors.Is(err, failure)) {
		return zero, atomicOwnershipError(err, failure)
	}
	if err != nil {
		return zero, err
	}
	return value, nil
}

func atomicConfigError(detail string) error {
	return &query.Error{Category: query.CategoryArgument, Code: query.CodeInvalidPlan, Detail: detail}
}

func atomicOwnershipError(err, callback error) error {
	return &query.Error{Category: query.CategoryBackend, Code: query.CodeTransactionOutcomeUnknown,
		Detail: "transaction did not confirm one complete prepared callback", Cause: errors.Join(err, callback)}
}
