package articleapp

import (
	"context"
	"errors"
	"sync"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

// preparedAtomic owns exactly one joined mutation/preparation callback and
// discards its result on rollback, cancellation or an uncertain completion.
func preparedAtomic[T any](ctx context.Context, repository Repository, operation MutationOperation, run func(context.Context, db.Session) (T, error)) (T, error) {
	var zero T
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	var prepared T
	var callbackError error
	entries, completed, sealed := 0, false, false
	err := repository.backend.Atomic(ctx, func(session db.Session) error {
		mu.Lock()
		if sealed {
			mu.Unlock()
			return errors.New("article: prepared mutation callback outlived its owner")
		}
		entries++
		if entries != 1 {
			mu.Unlock()
			return errors.New("article: repeated prepared mutation callback")
		}
		mu.Unlock()
		value, failure := func() (T, error) {
			if interfaceNil(session) {
				return zero, errors.New("article: prepared mutation session is absent")
			}
			if err := work.Err(); err != nil {
				return zero, err
			}
			result, err := run(work, session)
			if err != nil {
				return zero, err
			}
			if err := work.Err(); err != nil {
				return zero, err
			}
			return result, nil
		}()
		mu.Lock()
		prepared, callbackError, completed = value, failure, true
		mu.Unlock()
		return failure
	})
	mu.Lock()
	sealed = true
	valid := entries <= 1 && (entries == 0 && err != nil || completed)
	result, callbackErr := prepared, callbackError
	mu.Unlock()
	if !valid || callbackErr != nil && (err == nil || !errors.Is(err, callbackErr)) {
		failure := &query.Error{Category: query.CategoryBackend, Code: query.CodeTransactionOutcomeUnknown,
			Detail: "Article transaction did not confirm one complete prepared callback", Cause: errors.Join(err, callbackErr)}
		return zero, mutationError(ctx, string(operation), failure)
	}
	if err != nil {
		return zero, mutationError(ctx, string(operation), err)
	}
	return result, nil
}
