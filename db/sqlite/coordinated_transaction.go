package sqlite

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

var _ db.CoordinatedAtomic = (*Backend)(nil)
var _ db.Session = (*coordinatedSession)(nil)

// coordinatedSession deliberately exposes only db.Session. The wrapped raw
// transaction session also supports relation mutations, but that wider API is
// not part of the coordinated-atomic callback contract.
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
	if err := b.validateWriteContext(ctx); err != nil {
		return err
	}
	if callback == nil {
		return &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan, Detail: "coordinated atomic callback is nil"}
	}
	if b.relationRetention == nil || !b.relationRetention.accepting() {
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: "SQLite coordinated transaction retention state is nil or closed"}
	}

	connection, err := b.database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire pinned SQLite coordinated connection: %w", err)
	}
	return executeCoordinatedAtomic(ctx, callback, connection, b.relationRetention, &b.queryCount)
}

func executeCoordinatedAtomic(
	ctx context.Context,
	callback func(db.Session) error,
	connection relationPinnedConnection,
	retention *relationRetentionState,
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
			discardErr = errors.Join(discardErr, retention.retain(connection))
		}
		return errors.Join(primary, discardErr)
	}

	inner := &relationSession{
		connection: connection,
		queryCount: queryCount,
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
			_, _ = rollbackRelationConnection(ctx, connection, retention)
		}
		panic(panicValue)
	}()

	callbackErr := callback(session)
	inner.deactivate()
	if callbackErr != nil {
		deferredCleanup = false
		return finishCoordinatedPreCommitFailure(ctx, connection, retention, callbackErr)
	}
	if contextErr := ctx.Err(); contextErr != nil {
		deferredCleanup = false
		return finishCoordinatedPreCommitFailure(ctx, connection, retention, contextErr)
	}
	if _, err := connection.ExecContext(ctx, "COMMIT"); err != nil {
		deferredCleanup = false
		_, cleanupErr := rollbackRelationConnection(ctx, connection, retention)
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
	retention *relationRetentionState,
	primary error,
) error {
	terminated, cleanupErr := rollbackRelationConnection(ctx, connection, retention)
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
