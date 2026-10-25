package database

import (
	"context"
	"errors"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/sessions"
)

// Inspect validates every namespace's identity and payload under the configured
// global capacity/record limits. It never creates, expires or removes records.
// No arbitrary namespace is needed to inspect the database's own inventory.
func (database *Database) Inspect(ctx context.Context) (int, error) {
	if database == nil || database.state == nil || nilValue(ctx) {
		return 0, failure(sessions.CodeInvalidConfig, nil)
	}
	count, err := db.ReadSnapshotResult(ctx, database.state.backend, database.InspectIn)
	if err != nil {
		return 0, failure(sessions.CodeStoreFailure, err)
	}
	return count, nil
}

// InspectIn composes inspection in the caller's existing borrowed snapshot or
// transaction. It opens no nested scope and returns no records or bearer data.
// Its result is provisional until that owner's successful cleanup/commit.
func (database *Database) InspectIn(ctx context.Context, reader db.Queryer) (int, error) {
	if database == nil || database.state == nil || nilValue(ctx) || nilValue(reader) {
		return 0, failure(sessions.CodeInvalidConfig, nil)
	}
	validator, ok := reader.(db.SessionValidator)
	if !ok || nilValue(validator) {
		return 0, failure(sessions.CodeInvalidConfig, nil)
	}
	switch reader.(type) {
	case db.Atomic, db.CoordinatedAtomic, db.RelationAtomic, db.CoordinatedRelationAtomic, db.SnapshotReader:
		return 0, failure(sessions.CodeInvalidConfig, nil)
	}
	if err := errors.Join(ctx.Err(), validator.ValidateSession(ctx)); err != nil {
		return 0, failure(sessions.CodeStoreFailure, err)
	}
	count, err := database.state.inspect(ctx, reader)
	if err = errors.Join(err, ctx.Err(), validator.ValidateSession(ctx)); err != nil {
		return 0, failure(sessions.CodeStoreFailure, err)
	}
	return count, nil
}
