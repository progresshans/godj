package model

import (
	"context"
	"errors"
	"sync"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

type managementReadResult[T any] struct {
	value            T
	allowed, present bool
}

// Denied/not-found observations remain data until snapshot cleanup confirms
// that no scope, query or completion failure was hidden by a normal result.
func managementSnapshot[T any](ctx context.Context, backend db.SnapshotReader, authorize func(context.Context, db.Queryer) (auth.Principal, bool, error), read func(context.Context, db.Queryer, auth.CredentialDomain) (T, bool, error)) (T, error) {
	var zero T
	result, err := db.ReadSnapshotResult(ctx, backend, func(work context.Context, scope db.Queryer) (managementReadResult[T], error) {
		current, allowed, err := authorize(work, scope)
		if err != nil || !allowed {
			return managementReadResult[T]{}, err
		}
		namespace, bound := current.CredentialDomain()
		if !bound {
			return managementReadResult[T]{}, managementError(CodePersistence, nil)
		}
		value, present, err := read(work, scope, namespace)
		return managementReadResult[T]{value: value, allowed: true, present: present}, err
	})
	if err != nil {
		return zero, managementError(CodePersistence, err)
	}
	if !result.allowed {
		return zero, managementError(CodePermission, nil)
	}
	if !result.present {
		return zero, managementError(CodeNotFound, nil)
	}
	return result.value, nil
}

// Only the exact callback rejection survives a confirmed rollback as an
// ordinary management/validation result. Cleanup failures and native unknown
// outcomes must never turn into renderable input errors or automatic retries.
func managementWrite[T any](ctx context.Context, backend db.CoordinatedAtomic, write func(context.Context, db.Session) (T, error)) (T, error) {
	var mu sync.Mutex
	var callbackFailure error
	result, err := db.CoordinatedAtomicResult(ctx, backend, func(work context.Context, scope db.Session) (value T, failure error) {
		defer func() { mu.Lock(); callbackFailure = failure; mu.Unlock() }()
		return write(work, scope)
	})
	mu.Lock()
	callback := callbackFailure
	mu.Unlock()
	return managementCompletion(result, err, callback)
}

func managementRelationWrite[T any](ctx context.Context, backend db.CoordinatedRelationAtomic, write func(context.Context, db.RelationSession) (T, error)) (T, error) {
	var mu sync.Mutex
	var callbackFailure error
	result, err := db.CoordinatedRelationAtomicResult(ctx, backend, func(work context.Context, scope db.RelationSession) (value T, failure error) {
		defer func() { mu.Lock(); callbackFailure = failure; mu.Unlock() }()
		return write(work, scope)
	})
	mu.Lock()
	callback := callbackFailure
	mu.Unlock()
	return managementCompletion(result, err, callback)
}

func managementCompletion[T any](result T, err, callback error) (T, error) {
	var zero T
	if err == nil {
		return result, nil
	}
	for _, code := range []string{query.CodeCommitOutcomeUnknown, query.CodeTransactionOutcomeUnknown} {
		if errors.Is(err, &query.Error{Code: code}) {
			return zero, managementError(CodeOutcomeUnknown, err)
		}
	}
	// Compare only recognized pointer errors. A host error may contain a slice
	// and is not necessarily comparable as an arbitrary error interface.
	if _, rejected := validation.Rejected(err); rejected && err == callback && errors.Unwrap(err) == nil {
		return zero, err
	}
	if classified, ok := err.(*Error); ok && err == callback {
		return zero, classified
	}
	// The collector's concrete refusal is input only after the native owner
	// confirms the same callback error. Wrapped/joined cleanup failures and
	// backend errors with a matching query code remain execution failures.
	if protected, ok := err.(*query.ProtectedForeignKeyError); ok && protected.ProtectedSourceRows() > 0 && err == callback {
		return zero, validation.Reject(validation.NewErrors(validation.New(validation.NonField, "protected")), protected)
	}
	return zero, managementError(CodePersistence, err)
}
