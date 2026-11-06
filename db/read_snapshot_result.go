package db

import (
	"context"
	"errors"
	"sync"

	"github.com/progresshans/godj/query"
)

// ReadSnapshotResult publishes detached read data only after exactly one joined
// callback and successful snapshot cleanup. The borrowed reader must expose a
// live SessionValidator and no mutation capability. It must not be retained.
// Skipped, repeated, unfinished or swallowed callbacks publish no result.
// Cancellation, including cancellation during cleanup, also discards results.
// Read failures are never reported as uncertain write commits or retried.
func ReadSnapshotResult[T any](ctx context.Context, backend SnapshotReader, read func(context.Context, Queryer) (T, error)) (T, error) {
	var zero T
	if nilValue(ctx) || nilValue(backend) || read == nil {
		return zero, snapshotResultConfig("snapshot context, owner and callback are required")
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
	err := backend.ReadSnapshot(ctx, func(reader Queryer) error {
		mu.Lock()
		if sealed {
			mu.Unlock()
			return snapshotResultOwnership(nil, nil)
		}
		entries++
		if entries != 1 {
			mu.Unlock()
			return snapshotResultOwnership(nil, nil)
		}
		mu.Unlock()
		value, failure := func() (T, error) {
			if nilValue(reader) {
				return zero, snapshotResultConfig("snapshot reader is nil")
			}
			validator, borrowed := reader.(SessionValidator)
			if !borrowed || nilValue(validator) {
				return zero, snapshotResultConfig("snapshot reader is not a borrowed scope")
			}
			switch reader.(type) {
			case Mutator, Atomic, CoordinatedAtomic, RelationAtomic, CoordinatedRelationAtomic, SnapshotReader:
				return zero, snapshotResultConfig("snapshot reader exposes mutation or transaction capability")
			}
			check := func() error { return errors.Join(work.Err(), validator.ValidateSession(work)) }
			if err := check(); err != nil {
				return zero, err
			}
			value, err := read(work, reader)
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
		return zero, snapshotResultOwnership(err, failure)
	}
	if err = errors.Join(err, ctx.Err()); err != nil {
		return zero, err
	}
	return value, nil
}

func snapshotResultConfig(detail string) error {
	return &query.Error{Category: query.CategoryArgument, Code: query.CodeInvalidPlan, Detail: detail}
}

func snapshotResultOwnership(owner, callback error) error {
	return &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan,
		Detail: "snapshot did not confirm one complete read callback", Cause: errors.Join(owner, callback)}
}
