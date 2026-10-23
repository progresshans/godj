package identity

import (
	"context"
	"errors"
	"math"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

func (manager *Manager) validCall(ctx context.Context, actor auth.Principal) error {
	if ctx == nil {
		return managementError(CodeInvalidInput, "context", nil)
	}
	if err := ctx.Err(); err != nil {
		return managementError(CodeInvalidInput, "context", err)
	}
	if manager == nil || manager.state == nil {
		return managementError(CodeInvalidConfig, "manager", nil)
	}
	if !actor.Authenticated() {
		return managementError(CodePermission, "actor", nil)
	}
	return nil
}

func validUserRevision(id, revision int64) error {
	if id <= 0 || revision <= 0 || revision == math.MaxInt64 {
		return managementError(CodeInvalidInput, "user", nil)
	}
	return nil
}

func managementSnapshot[T any](ctx context.Context, manager *Manager, callback func(db.Queryer) (T, error)) (T, error) {
	var result, zero T
	var callbackErr error
	var rejectedInput validation.Errors
	calls := 0
	err := manager.state.backend.ReadSnapshot(ctx, func(reader db.Queryer) error {
		calls++
		if calls != 1 || nilIdentityValue(reader) {
			callbackErr = managementError(CodePersistence, "snapshot_contract", nil)
			return callbackErr
		}
		result, callbackErr = callback(reader)
		// An expected preflight rejection is read data. End the native read
		// scope successfully before publishing it; cleanup/cancellation errors
		// must remain execution failures, even when the input was also invalid.
		if diagnostics, rejected := validation.Rejected(callbackErr); rejected && errors.Unwrap(callbackErr) == nil {
			rejectedInput = diagnostics
			callbackErr = nil
		}
		return callbackErr
	})
	if err = errors.Join(err, callbackErr, ctx.Err()); err != nil {
		return zero, managementWriteFailure(err)
	}
	if calls != 1 {
		return zero, managementError(CodePersistence, "snapshot_contract", nil)
	}
	if !rejectedInput.Empty() {
		return zero, validation.Reject(rejectedInput, nil)
	}
	return result, nil
}

func managementRelationWrite[T any](ctx context.Context, manager *Manager, callback func(db.RelationSession) (T, error)) (T, error) {
	var result, zero T
	backend, ok := manager.state.backend.(db.CoordinatedRelationAtomic)
	if !ok || nilIdentityValue(backend) {
		return zero, managementError(CodeInvalidConfig, "manager", nil)
	}
	var callbackErr error
	calls := 0
	err := backend.CoordinatedAtomicRelation(ctx, func(session db.RelationSession) error {
		calls++
		if calls != 1 || nilIdentityValue(session) {
			callbackErr = managementError(CodePersistence, "transaction_contract", nil)
			return callbackErr
		}
		result, callbackErr = callback(session)
		if callbackErr == nil {
			callbackErr = ctx.Err()
		}
		return callbackErr
	})
	// Only an unchanged rejection after confirmed rollback is renderable input.
	// A joined cleanup error or a backend that swallows it is an execution error.
	if diagnostics, rejected := validation.Rejected(callbackErr); rejected && err == callbackErr {
		return zero, validation.Reject(diagnostics, nil)
	}
	// A concrete relation-policy refusal is presentable only after the backend
	// returns that exact callback error, confirming rollback without cleanup loss.
	if protected, ok := callbackErr.(*query.ProtectedForeignKeyError); ok && protected != nil && err == callbackErr {
		return zero, validation.Reject(validation.NewErrors(validation.New(validation.NonField, "protected")), protected)
	}
	if err = errors.Join(err, callbackErr); err != nil {
		return zero, managementWriteFailure(err)
	}
	if calls != 1 {
		return zero, managementError(CodePersistence, "transaction_contract", nil)
	}
	return result, nil
}

// Only the hasher's direct, cause-free password input rejection is user input.
// Cancellation, wrapped classifications and entropy/implementation failures
// remain execution errors; no hash error can publish a mutation or be retried.
func managementPassword(ctx context.Context, hasher auth.PasswordHasher, password string) (string, error) {
	encoded, err := hasher.Hash(ctx, password)
	if canceled := ctx.Err(); canceled != nil {
		return "", managementError(CodeInvalidInput, "password", errors.Join(err, canceled))
	}
	if err != nil {
		if invalid, ok := err.(*auth.Error); ok && invalid != nil && invalid.Code == auth.CodeInvalidInput && invalid.Field == "password" && invalid.Unwrap() == nil {
			return "", managementInputError("password", "invalid")
		}
		return "", managementError(CodeInvalidInput, "password", err)
	}
	return encoded, nil
}
