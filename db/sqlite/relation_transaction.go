package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

const relationCleanupTimeout = 5 * time.Second

var _ db.RelationAtomic = (*Backend)(nil)
var _ db.RelationSession = (*relationSession)(nil)

type relationPinnedConnection interface {
	writeExecutor
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	Raw(func(any) error) error
	Close() error
}

var _ relationPinnedConnection = (*sql.Conn)(nil)

// relationRetentionState serializes retention-producing raw transactions and
// owns the one connection whose transaction could not be proven terminated.
// closing rejects and wakes admissions without making Conn.Close safe; sealed
// means sql.DB.Close has completed and a late admitted owner may terminally
// close its own unconfirmed connection. The pointer keeps Backend comparable
// and avoids a process-global cleanup registry.
type relationRetentionState struct {
	mu          sync.Mutex
	closing     bool
	sealed      bool
	quarantined bool
	active      *relationTransactionAdmission
	changed     chan struct{}
	retained    relationPinnedConnection
}

func newRelationRetentionState() *relationRetentionState {
	return &relationRetentionState{changed: make(chan struct{})}
}

type relationTransactionAdmission struct {
	state    *relationRetentionState
	released atomic.Bool
}

func sqliteBackendRecoveryRequiredError() error {
	return &query.Error{
		Category: query.CategoryBackend,
		Code:     query.CodeBackendRecoveryRequired,
		Detail:   "SQLite backend retained an unconfirmed raw transaction; close, reconcile, and open a fresh backend",
	}
}

func isSQLiteBackendRecoveryRequired(err error) bool {
	return errors.Is(err, &query.Error{
		Category: query.CategoryBackend,
		Code:     query.CodeBackendRecoveryRequired,
	})
}

func (state *relationRetentionState) availabilityError() error {
	if state == nil {
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: "SQLite backend lifecycle state is nil"}
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.availabilityErrorLocked()
}

func (state *relationRetentionState) availabilityErrorLocked() error {
	if state.closing || state.sealed {
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: "SQLite backend lifecycle state is closed"}
	}
	if state.quarantined {
		return sqliteBackendRecoveryRequiredError()
	}
	return nil
}

func (state *relationRetentionState) acquire(ctx context.Context) (*relationTransactionAdmission, error) {
	if state == nil {
		return nil, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: "SQLite backend lifecycle state is nil"}
	}
	if ctx == nil {
		return nil, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: "context is nil"}
	}
	for {
		state.mu.Lock()
		if err := state.availabilityErrorLocked(); err != nil {
			state.mu.Unlock()
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			state.mu.Unlock()
			return nil, err
		}
		if state.active == nil {
			admission := &relationTransactionAdmission{state: state}
			state.active = admission
			state.mu.Unlock()
			return admission, nil
		}
		changed := state.changed
		if changed == nil {
			changed = make(chan struct{})
			state.changed = changed
		}
		state.mu.Unlock()

		select {
		case <-changed:
			continue
		case <-ctx.Done():
			state.mu.Lock()
			availabilityErr := state.availabilityErrorLocked()
			state.mu.Unlock()
			if availabilityErr != nil {
				return nil, availabilityErr
			}
			return nil, ctx.Err()
		}
	}
}

func (admission *relationTransactionAdmission) release() {
	if admission == nil || admission.state == nil || !admission.released.CompareAndSwap(false, true) {
		return
	}
	state := admission.state
	state.mu.Lock()
	if state.active == admission {
		state.active = nil
		state.signalLocked()
	}
	state.mu.Unlock()
}

func (admission *relationTransactionAdmission) retain(connection relationPinnedConnection) error {
	if admission == nil || admission.state == nil {
		return errors.New("retain poisoned SQLite relation connection: transaction admission is nil")
	}
	if connection == nil {
		return errors.New("retain poisoned SQLite relation connection: connection is nil")
	}
	state := admission.state
	state.mu.Lock()
	if admission.released.Load() || state.active != admission {
		state.mu.Unlock()
		return errors.New("retain poisoned SQLite relation connection: transaction admission is inactive")
	}
	if state.sealed {
		state.mu.Unlock()
		if err := connection.Close(); err != nil {
			return fmt.Errorf("terminally close poisoned SQLite relation connection after backend close: %w", err)
		}
		return nil
	}
	if state.quarantined || state.retained != nil {
		state.mu.Unlock()
		return errors.New("retain poisoned SQLite relation connection: quarantine already owns a connection")
	}
	state.quarantined = true
	state.retained = connection
	state.signalLocked()
	state.mu.Unlock()
	return sqliteBackendRecoveryRequiredError()
}

func (state *relationRetentionState) signalLocked() {
	if state.changed != nil {
		close(state.changed)
	}
	state.changed = make(chan struct{})
}

func (state *relationRetentionState) beginClose() {
	if state == nil {
		return
	}
	state.mu.Lock()
	if !state.closing {
		state.closing = true
		state.signalLocked()
	}
	state.mu.Unlock()
}

// sealAndDrain runs only after sql.DB.Close has sealed the pool. It drains the
// pre-seal retained handle; an already-admitted operation that discovers an
// unconfirmed handle later observes sealed in retain and closes it there.
func (state *relationRetentionState) sealAndDrain() error {
	if state == nil {
		return nil
	}
	state.mu.Lock()
	state.closing = true
	state.sealed = true
	retained := state.retained
	state.retained = nil
	state.signalLocked()
	state.mu.Unlock()

	if retained == nil {
		return nil
	}
	if err := retained.Close(); err != nil {
		return fmt.Errorf("terminally close poisoned SQLite relation connection: %w", err)
	}
	return nil
}

// AtomicRelation uses one pinned connection for FK verification, raw BEGIN
// IMMEDIATE, the callback, and terminal SQL. It never retries a failed BEGIN or
// invokes the callback after returning.
func (b *Backend) AtomicRelation(ctx context.Context, callback func(db.RelationSession) error) error {
	if err := b.validateWriteContext(ctx); err != nil {
		return err
	}
	if callback == nil {
		return &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan, Detail: "atomic relation callback is nil"}
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
		return fmt.Errorf("acquire pinned SQLite relation connection: %w", err)
	}
	if err := verifyRelationForeignKeys(ctx, connection); err != nil {
		return errors.Join(err, closeUnusedRelationConnection(connection))
	}
	return executeAdmittedAtomicRelation(ctx, callback, connection, admission, &b.queryCount)
}

func verifyRelationForeignKeys(ctx context.Context, connection *sql.Conn) error {
	if connection == nil {
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: "SQLite relation connection is nil"}
	}
	var enabled int64
	if err := connection.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&enabled); err != nil {
		return fmt.Errorf("verify SQLite relation foreign_keys pragma: %w", err)
	}
	if enabled != 1 {
		return &query.Error{
			Category: query.CategoryBackend,
			Code:     query.CodeInvalidPlan,
			Detail:   fmt.Sprintf("SQLite relation connection foreign_keys pragma is %d, want 1", enabled),
		}
	}
	return nil
}

func executeAtomicRelation(
	ctx context.Context,
	callback func(db.RelationSession) error,
	connection relationPinnedConnection,
	retention *relationRetentionState,
	queryCount *atomic.Uint64,
) (resultErr error) {
	admission, err := retention.acquire(ctx)
	if err != nil {
		return err
	}
	defer admission.release()
	return executeAdmittedAtomicRelation(ctx, callback, connection, admission, queryCount)
}

func executeAdmittedAtomicRelation(
	ctx context.Context,
	callback func(db.RelationSession) error,
	connection relationPinnedConnection,
	admission *relationTransactionAdmission,
	queryCount *atomic.Uint64,
) (resultErr error) {
	if connection == nil {
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: "SQLite relation connection is nil"}
	}
	if _, err := connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		primary := fmt.Errorf("begin immediate SQLite relation transaction: %w", err)
		confirmed, discardErr := forceDiscardRelationConnection(connection)
		if !confirmed {
			discardErr = errors.Join(discardErr, admission.retain(connection))
		}
		return errors.Join(primary, discardErr)
	}

	session := &relationSession{
		connection: connection,
		queryCount: queryCount,
		active:     true,
	}
	deferredCleanup := true
	defer func() {
		panicValue := recover()
		if panicValue == nil {
			return
		}
		if deferredCleanup {
			session.deactivate()
			_, _ = rollbackRelationConnection(ctx, connection, admission)
		}
		panic(panicValue)
	}()

	callbackErr := callback(session)
	mutationPossible := session.deactivate()
	if callbackErr != nil {
		deferredCleanup = false
		return finishRelationPreCommitFailure(ctx, connection, admission, callbackErr, mutationPossible)
	}
	if contextErr := ctx.Err(); contextErr != nil {
		deferredCleanup = false
		return finishRelationPreCommitFailure(ctx, connection, admission, contextErr, mutationPossible)
	}
	if _, err := connection.ExecContext(ctx, "COMMIT"); err != nil {
		deferredCleanup = false
		_, cleanupErr := rollbackRelationConnection(ctx, connection, admission)
		return &query.Error{
			Category: query.CategoryBackend,
			Code:     query.CodeCommitOutcomeUnknown,
			Detail:   "SQLite relation COMMIT returned an error; durable outcome requires reconciliation",
			Cause: errors.Join(
				fmt.Errorf("commit SQLite relation transaction: %w", err),
				cleanupErr,
			),
		}
	}

	deferredCleanup = false
	// A successful COMMIT is authoritative. Connection-return errors cannot
	// turn durable success into an error.
	_ = connection.Close()
	return nil
}

func finishRelationPreCommitFailure(
	ctx context.Context,
	connection relationPinnedConnection,
	admission *relationTransactionAdmission,
	primary error,
	mutationPossible bool,
) error {
	terminated, cleanupErr := rollbackRelationConnection(ctx, connection, admission)
	cause := errors.Join(primary, cleanupErr)
	if mutationPossible && !terminated {
		return &query.Error{
			Category: query.CategoryBackend,
			Code:     query.CodeTransactionOutcomeUnknown,
			Detail:   "SQLite relation transaction termination could not be confirmed after mutation became possible",
			Cause:    cause,
		}
	}
	return cause
}

func rollbackRelationConnection(
	ctx context.Context,
	connection relationPinnedConnection,
	admission *relationTransactionAdmission,
) (bool, error) {
	cleanupCtx, cancel := relationDetachedCleanupContext(ctx)
	defer cancel()
	_, rollbackErr := connection.ExecContext(cleanupCtx, "ROLLBACK")
	if rollbackErr == nil {
		if err := connection.Close(); err != nil {
			return true, fmt.Errorf("close rolled-back SQLite relation connection: %w", err)
		}
		return true, nil
	}
	if errors.Is(rollbackErr, sql.ErrConnDone) {
		return true, fmt.Errorf("rollback SQLite relation transaction: %w", rollbackErr)
	}
	confirmed, discardErr := forceDiscardRelationConnection(connection)
	if !confirmed {
		discardErr = errors.Join(discardErr, admission.retain(connection))
	}
	return confirmed, errors.Join(
		fmt.Errorf("rollback SQLite relation transaction: %w", rollbackErr),
		discardErr,
	)
}

func forceDiscardRelationConnection(connection relationPinnedConnection) (bool, error) {
	if connection == nil {
		return false, errors.New("discard SQLite relation connection: connection is nil")
	}
	err := connection.Raw(func(any) error { return driver.ErrBadConn })
	if errors.Is(err, driver.ErrBadConn) || errors.Is(err, sql.ErrConnDone) {
		return true, nil
	}
	if err == nil {
		return false, errors.New("discard SQLite relation connection: Raw returned nil without confirming discard")
	}
	return false, fmt.Errorf("discard SQLite relation connection: %w", err)
}

func relationDetachedCleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		return context.WithTimeout(context.Background(), relationCleanupTimeout)
	}
	return context.WithTimeout(context.WithoutCancel(ctx), relationCleanupTimeout)
}

func closeUnusedRelationConnection(connection *sql.Conn) error {
	if connection == nil {
		return nil
	}
	if err := connection.Close(); err != nil {
		return fmt.Errorf("close unused SQLite relation connection: %w", err)
	}
	return nil
}

type relationSession struct {
	mu               sync.Mutex
	connection       relationPinnedConnection
	queryCount       *atomic.Uint64
	active           bool
	mutationPossible bool
}

func (session *relationSession) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	if session == nil {
		return nil, inactiveRelationSessionError()
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if err := session.validateLocked(ctx); err != nil {
		return nil, err
	}
	statement, arguments, err := Compile(plan)
	if err != nil {
		return nil, err
	}
	if session.queryCount != nil {
		session.queryCount.Add(1)
	}
	rows, err := session.connection.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, fmt.Errorf("execute SQLite relation transaction query: %w", err)
	}
	if rows == nil {
		return nil, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: "SQLite relation query returned nil rows without an error"}
	}
	return rows, nil
}

func (session *relationSession) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	if session == nil {
		return 0, inactiveRelationSessionError()
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if err := session.validateLocked(ctx); err != nil {
		return 0, err
	}
	statement, arguments, err := CompileInsert(plan)
	if err != nil {
		return 0, err
	}
	session.mutationPossible = true
	result, err := session.connection.ExecContext(ctx, statement, arguments...)
	if err != nil {
		return 0, classifyInsertError(err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read SQLite relation insert rows affected: %w", err)
	}
	if rowsAffected != 1 {
		return 0, &query.Error{
			Category: query.CategoryBackend,
			Code:     query.CodeUnexpectedRows,
			Detail:   fmt.Sprintf("insert affected %d rows, want 1", rowsAffected),
		}
	}
	lastInsertID, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read SQLite relation insert last insert id: %w", err)
	}
	return lastInsertID, nil
}

func (session *relationSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	if session == nil {
		return 0, inactiveRelationSessionError()
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if err := session.validateLocked(ctx); err != nil {
		return 0, err
	}
	statement, arguments, err := CompileUpdate(plan)
	if err != nil {
		return 0, err
	}
	session.mutationPossible = true
	result, err := session.connection.ExecContext(ctx, statement, arguments...)
	if err != nil {
		return 0, fmt.Errorf("execute SQLite relation transaction update: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read SQLite relation update rows affected: %w", err)
	}
	return rowsAffected, nil
}

func (session *relationSession) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	if session == nil {
		return 0, inactiveRelationSessionError()
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if err := session.validateLocked(ctx); err != nil {
		return 0, err
	}
	statement, arguments, err := CompileDelete(plan)
	if err != nil {
		return 0, err
	}
	session.mutationPossible = true
	result, err := session.connection.ExecContext(ctx, statement, arguments...)
	if err != nil {
		return 0, fmt.Errorf("execute SQLite relation transaction delete: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read SQLite relation delete rows affected: %w", err)
	}
	return rowsAffected, nil
}

func (session *relationSession) RelationSetNull(ctx context.Context, plan query.RelationSetNullPlan) (int64, error) {
	if session == nil {
		return 0, inactiveRelationSessionError()
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if err := session.validateLocked(ctx); err != nil {
		return 0, err
	}
	statement, arguments, err := compileRelationSetNull(plan)
	if err != nil {
		return 0, err
	}
	session.mutationPossible = true
	return executeCompiledRelationSetNull(ctx, session.connection, statement, arguments)
}

func (session *relationSession) validateLocked(ctx context.Context) error {
	if session == nil || session.connection == nil || !session.active {
		return inactiveRelationSessionError()
	}
	if ctx == nil {
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: "context is nil"}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func inactiveRelationSessionError() error {
	return &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: "SQLite relation transaction session is nil or no longer active"}
}

func (session *relationSession) deactivate() bool {
	if session == nil {
		return false
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	session.active = false
	return session.mutationPossible
}
