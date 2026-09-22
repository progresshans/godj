package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

var _ db.CoordinatedAtomic = (*Backend)(nil)
var _ db.CoordinatedRelationAtomic = (*Backend)(nil)
var _ db.Session = (*coordinatedSession)(nil)

// coordinatedSession exposes ordinary writes and the conflict-insert
// capability. The wrapped raw session's bulk relation mutations belong to the
// separate coordinated-relation callback contract.
type coordinatedSession struct {
	session *relationSession
}

func (session *coordinatedSession) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	if session == nil || session.session == nil {
		return nil, inactiveCoordinatedSessionError()
	}
	return session.session.Query(ctx, plan)
}

func (session *coordinatedSession) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	if session == nil || session.session == nil {
		return 0, inactiveCoordinatedSessionError()
	}
	return session.session.Insert(ctx, plan)
}

func (session *coordinatedSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	if session == nil || session.session == nil {
		return 0, inactiveCoordinatedSessionError()
	}
	return session.session.Update(ctx, plan)
}

func (session *coordinatedSession) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	if session == nil || session.session == nil {
		return 0, inactiveCoordinatedSessionError()
	}
	return session.session.Delete(ctx, plan)
}

func inactiveCoordinatedSessionError() error {
	return &query.Error{
		Category: query.CategoryBackend,
		Code:     query.CodeInvalidPlan,
		Detail:   "SQLite coordinated transaction session is nil or no longer active",
	}
}

// CoordinatedAtomic pins one physical connection and acquires SQLite's writer
// fence with one literal BEGIN IMMEDIATE. The backend does not retry BUSY or
// LOCKED results; any configured driver busy timeout is the only acquisition
// wait.
func (b *Backend) CoordinatedAtomic(ctx context.Context, callback func(db.Session) error) error {
	return b.coordinatedAtomic(ctx, callback, false)
}

// CoordinatedAtomicRelation keeps BEGIN IMMEDIATE, retention, cleanup and
// lifetime ownership in the shared coordinated transaction engine. It verifies
// the pinned connection's FK enforcement before exposing relation mutations.
func (b *Backend) CoordinatedAtomicRelation(ctx context.Context, callback func(db.RelationSession) error) error {
	if callback == nil {
		return b.coordinatedAtomic(ctx, nil, true)
	}
	return b.coordinatedAtomic(ctx, func(session db.Session) error {
		coordinated, ok := session.(*coordinatedSession)
		if !ok || coordinated == nil || coordinated.session == nil {
			return inactiveCoordinatedSessionError()
		}
		return callback(coordinated.session)
	}, true)
}

func (b *Backend) coordinatedAtomic(ctx context.Context, callback func(db.Session) error, requireRelations bool) error {
	if err := b.validateWriteContext(ctx); err != nil {
		return err
	}
	if callback == nil {
		return &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan, Detail: "coordinated atomic callback is nil"}
	}
	admission, err := b.relationRetention.acquire(ctx)
	if err != nil {
		return err
	}
	defer admission.release()
	if err := b.validateBackendContext(ctx); err != nil {
		return err
	}

	connection, err := b.database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire pinned SQLite coordinated connection: %w", err)
	}
	if requireRelations {
		if err := verifyRelationForeignKeys(ctx, connection); err != nil {
			return errors.Join(err, closeUnusedRelationConnection(connection))
		}
	}
	return executeAdmittedCoordinatedAtomic(ctx, callback, connection, admission, &b.queryCount)
}

func executeCoordinatedAtomic(
	ctx context.Context,
	callback func(db.Session) error,
	connection relationPinnedConnection,
	retention *relationRetentionState,
	queryCount *atomic.Uint64,
) error {
	admission, err := retention.acquire(ctx)
	if err != nil {
		return err
	}
	defer admission.release()
	return executeAdmittedCoordinatedAtomic(ctx, callback, connection, admission, queryCount)
}

func executeAdmittedCoordinatedAtomic(
	ctx context.Context,
	callback func(db.Session) error,
	connection relationPinnedConnection,
	admission *relationTransactionAdmission,
	queryCount *atomic.Uint64,
) error {
	if connection == nil {
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: "SQLite coordinated connection is nil"}
	}
	if callback == nil {
		return &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan, Detail: "coordinated atomic callback is nil"}
	}
	if _, err := connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		primary := fmt.Errorf("acquire SQLite coordinated transaction fence: %w", err)
		confirmed, discardErr := forceDiscardRelationConnection(connection)
		if !confirmed {
			discardErr = errors.Join(discardErr, admission.retain(connection))
		}
		return errors.Join(primary, discardErr)
	}

	lifetime, finishLifetime := context.WithCancelCause(ctx)
	defer finishLifetime(sql.ErrTxDone)
	inner := &relationSession{
		connection: connection,
		queryCount: queryCount,
		lifetime:   lifetime,
		active:     true,
	}
	session := &coordinatedSession{session: inner}
	deferredCleanup := true
	defer func() {
		panicValue := recover()
		if panicValue == nil {
			return
		}
		if deferredCleanup {
			inner.deactivate()
			_, _ = rollbackRelationConnection(ctx, connection, admission)
		}
		panic(panicValue)
	}()

	callbackErr := callback(session)
	inner.deactivate()
	if callbackErr != nil {
		deferredCleanup = false
		return finishCoordinatedPreCommitFailure(ctx, connection, admission, callbackErr)
	}
	if contextErr := ctx.Err(); contextErr != nil {
		deferredCleanup = false
		return finishCoordinatedPreCommitFailure(ctx, connection, admission, contextErr)
	}
	if _, err := connection.ExecContext(ctx, "COMMIT"); err != nil {
		deferredCleanup = false
		_, cleanupErr := rollbackRelationConnection(ctx, connection, admission)
		return &query.Error{
			Category: query.CategoryBackend,
			Code:     query.CodeCommitOutcomeUnknown,
			Detail:   "SQLite coordinated COMMIT returned an error; durable outcome requires reconciliation",
			Cause: errors.Join(
				fmt.Errorf("commit SQLite coordinated transaction: %w", err),
				cleanupErr,
			),
		}
	}

	deferredCleanup = false
	// The literal COMMIT success is authoritative. Returning the connection to
	// the pool cannot downgrade a durable success.
	_ = connection.Close()
	return nil
}

func finishCoordinatedPreCommitFailure(
	ctx context.Context,
	connection relationPinnedConnection,
	admission *relationTransactionAdmission,
	primary error,
) error {
	terminated, cleanupErr := rollbackRelationConnection(ctx, connection, admission)
	if terminated && cleanupErr == nil {
		// Confirmed clean rollback preserves the callback's error ownership.
		return primary
	}
	cause := errors.Join(primary, cleanupErr)
	if !terminated {
		return &query.Error{
			Category: query.CategoryBackend,
			Code:     query.CodeTransactionOutcomeUnknown,
			Detail:   "SQLite coordinated transaction termination could not be confirmed",
			Cause:    cause,
		}
	}
	return cause
}
