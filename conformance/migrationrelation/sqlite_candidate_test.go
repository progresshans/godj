package migrationrelation

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
	_ "modernc.org/sqlite"
)

const (
	sqliteRelationRevisionTable  = "__godj_relation_revision"
	sqliteRelationRecorderTable  = "__godj_relation_migrations"
	sqliteRelationCleanupTimeout = 2 * time.Second
)

var (
	sqliteRelationErrForeignKeysOff  = errors.New("SQLite foreign key enforcement is not enabled on pinned connection")
	sqliteRelationErrRequiredRows    = errors.New("required relation AddField cannot alter a populated table")
	sqliteRelationErrDrift           = errors.New("declared relation model differs from physical SQLite table")
	sqliteRelationErrIndex           = errors.New("bounded relation remake rejects unmanaged index")
	sqliteRelationErrTrigger         = errors.New("bounded relation remake rejects unmanaged trigger")
	sqliteRelationErrView            = errors.New("bounded relation remake rejects unmanaged view")
	sqliteRelationErrGenerated       = errors.New("bounded relation remake rejects generated or hidden column")
	sqliteRelationErrInbound         = errors.New("bounded relation remake rejects inbound foreign key")
	sqliteRelationErrTempCollision   = errors.New("bounded relation remake temporary table already exists")
	sqliteRelationErrTempShadow      = errors.New("SQLite TEMP object shadows a relation migration identifier")
	sqliteRelationErrForeignKeyCheck = errors.New("SQLite foreign_key_check reported a violation")
	sqliteRelationErrRevision        = errors.New("relation migration revision fence mismatch")
	sqliteRelationErrRecorder        = errors.New("relation migration recorder mismatch")
	sqliteRelationErrControlCatalog  = errors.New("SQLite relation control catalog is outside the closed shape")
)

type sqliteRelationBackend struct {
	database *sql.DB
	faults   *faultPlan

	// closeConnection is a candidate-only seam for proving that a sql.Conn
	// close failure is followed by an explicit discard. Production-shaped
	// candidate use leaves it nil and calls (*sql.Conn).Close directly.
	closeConnection  func(*sql.Conn) error
	commitConnection func(context.Context, *sql.Conn) (sql.Result, error)
	physicalLimits   *sqliteRelationPhysicalLimits

	mu            sync.Mutex
	trace         []string
	openCalls     int
	beginCalls    int
	commitCalls   int
	rollbackCalls int
	discardCalls  int
}

type sqliteRelationPhysicalLimits struct {
	MaxObjects        int
	MaxStatementBytes int
	MaxBatchBytes     int
	MaxGraphWork      int
	MaxCatalogWork    int
	MaxTargetChecks   int
}

var sqliteRelationDefaultPhysicalLimits = sqliteRelationPhysicalLimits{
	MaxObjects:        migrationdefinition.MaxSources,
	MaxStatementBytes: migrationdefinition.MaxDocumentBytes,
	MaxBatchBytes:     migrationdefinition.MaxBatchBytes,
	MaxGraphWork:      migrationdefinition.MaxJSONValues,
	MaxCatalogWork:    migrationdefinition.MaxJSONValues,
	MaxTargetChecks:   migrationdefinition.MaxJSONValues,
}

type sqliteRelationSchemaObject struct {
	Schema string
	Type   string
	Name   string
	Owner  string
	SQL    string
}

type sqliteRelationPhysicalCatalog struct {
	Objects []sqliteRelationSchemaObject
}

type sqliteRelationSequenceSnapshot struct {
	Name    string
	Present bool
	Value   int64
}

var sqliteRelationInitializeAfterValidationHook func() error

var _ relationBackendOptionalBackend = (*sqliteRelationBackend)(nil)

func (backend *sqliteRelationBackend) RelationMigrationCapabilities() relationBackendCapabilities {
	return relationBackendCapabilities{
		Profile:               1,
		CreateModel:           true,
		NullableAddField:      true,
		EmptyRequiredAddField: true,
		BoundedRemake:         true,
	}
}

func (backend *sqliteRelationBackend) OpenRelationMigrationSession(context.Context) (relationBackendOptionalSession, error) {
	backend.mu.Lock()
	backend.openCalls++
	backend.mu.Unlock()
	return &sqliteRelationSession{backend: backend}, nil
}

func (backend *sqliteRelationBackend) sqliteRelationTrace(event string) {
	backend.mu.Lock()
	backend.trace = append(backend.trace, event)
	backend.mu.Unlock()
}

func (backend *sqliteRelationBackend) sqliteRelationTraceSnapshot() []string {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return append([]string(nil), backend.trace...)
}

type sqliteRelationSession struct {
	backend *sqliteRelationBackend
}

var _ relationBackendOptionalSession = (*sqliteRelationSession)(nil)

func (session *sqliteRelationSession) BeginRelationFencedMigration(
	ctx context.Context,
	transition relationBackendTransition,
	intent relationBackendStepIntent,
) (relationBackendTransaction, error) {
	if ctx == nil {
		return nil, errors.New("SQLite relation begin context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := relationBackendValidateResourceShape(intent); err != nil {
		return nil, err
	}
	pinned := intent.relationBackendClone()
	if err := relationBackendValidateIntent(pinned); err != nil {
		return nil, err
	}
	if err := relationBackendValidateTransition(transition, pinned); err != nil {
		return nil, fmt.Errorf("%w: %w", sqliteRelationErrRevision, err)
	}

	connection, err := session.backend.database.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("pin SQLite relation connection: %w", err)
	}
	closeUnused := func(primary error) error {
		return errors.Join(primary, sqliteRelationCloseConnection(session.backend, connection))
	}

	session.backend.sqliteRelationTrace("PRAGMA foreign_keys = ON")
	if err := session.backend.faults.faultHit(faultStagePragmaEnable); err != nil {
		return nil, closeUnused(fmt.Errorf("enable pinned SQLite foreign keys: %w", err))
	}
	if _, err := connection.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		return nil, closeUnused(fmt.Errorf("enable pinned SQLite foreign keys: %w", err))
	}
	session.backend.sqliteRelationTrace("PRAGMA foreign_keys")
	if err := session.backend.faults.faultHit(faultStagePragmaRead); err != nil {
		return nil, closeUnused(fmt.Errorf("read pinned SQLite foreign keys: %w", err))
	}
	var foreignKeys int
	if err := connection.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		return nil, closeUnused(fmt.Errorf("read pinned SQLite foreign keys: %w", err))
	}
	if foreignKeys != 1 {
		return nil, closeUnused(fmt.Errorf("%w: readback=%d", sqliteRelationErrForeignKeysOff, foreignKeys))
	}

	session.backend.sqliteRelationTrace("BEGIN IMMEDIATE")
	if err := session.backend.faults.faultHit(faultStageBegin); err != nil {
		return nil, closeUnused(fmt.Errorf("begin immediate relation migration: %w", err))
	}
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return nil, errors.Join(
			fmt.Errorf("begin immediate relation migration: %w", err),
			sqliteRelationDiscardConnection(session.backend, connection),
		)
	}
	session.backend.mu.Lock()
	session.backend.beginCalls++
	session.backend.mu.Unlock()

	transaction := &sqliteRelationTransaction{
		backend: session.backend, connection: connection,
		transition: transition, intent: pinned,
		remakeSequences: make(map[string]sqliteRelationSequenceSnapshot),
	}
	physicalLimits := sqliteRelationDefaultPhysicalLimits
	if session.backend.physicalLimits != nil {
		physicalLimits = *session.backend.physicalLimits
	}
	catalog, err := sqliteRelationLoadPhysicalCatalog(ctx, connection, physicalLimits)
	if err != nil {
		return nil, transaction.sqliteRelationRollbackAndClose(ctx, err)
	}
	if err := sqliteRelationValidateControlCatalog(ctx, connection, catalog); err != nil {
		return nil, transaction.sqliteRelationRollbackAndClose(ctx, err)
	}
	if err := sqliteRelationValidateDurableState(ctx, connection, physicalLimits); err != nil {
		return nil, transaction.sqliteRelationRollbackAndClose(ctx, err)
	}
	sequenceCatalog, err := sqliteRelationLoadSequenceCatalog(ctx, connection, physicalLimits)
	if err != nil {
		return nil, transaction.sqliteRelationRollbackAndClose(ctx, err)
	}
	if err := sqliteRelationPhysicalPreflight(
		ctx,
		connection,
		pinned,
		catalog,
		physicalLimits,
		transaction.remakeSequences,
		sequenceCatalog,
	); err != nil {
		return nil, transaction.sqliteRelationRollbackAndClose(ctx, err)
	}
	if err := transaction.sqliteRelationClaimRevision(ctx); err != nil {
		return nil, transaction.sqliteRelationRollbackAndClose(ctx, err)
	}
	return transaction, nil
}

func (*sqliteRelationSession) Close(context.Context) error { return nil }

type sqliteRelationTransaction struct {
	backend         *sqliteRelationBackend
	connection      *sql.Conn
	transition      relationBackendTransition
	intent          relationBackendStepIntent
	remakeSequences map[string]sqliteRelationSequenceSnapshot
	nextChange      int
	recorded        bool
	complete        bool
}

var _ relationBackendTransaction = (*sqliteRelationTransaction)(nil)

func (transaction *sqliteRelationTransaction) sqliteRelationClaimRevision(ctx context.Context) error {
	if err := transaction.backend.faults.faultHit(faultStageRevisionClaim); err != nil {
		return fmt.Errorf("claim relation revision: %w", err)
	}
	result, err := transaction.connection.ExecContext(
		ctx,
		`UPDATE `+sqliteRelationQualifiedMain(sqliteRelationRevisionTable)+` SET "revision" = ? WHERE "singleton" = 1 AND "revision" = ?`,
		transaction.transition.ToRevision,
		transaction.transition.FromRevision,
	)
	if err != nil {
		return fmt.Errorf("claim relation revision: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count relation revision claim: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("%w: affected %d rows", sqliteRelationErrRevision, rows)
	}
	return nil
}

func (transaction *sqliteRelationTransaction) ApplyRelationChange(ctx context.Context, change relationBackendChange) error {
	if ctx == nil {
		return errors.New("apply SQLite relation change context is nil")
	}
	if transaction.complete {
		return errors.New("SQLite relation transaction is complete")
	}
	if transaction.nextChange >= len(transaction.intent.Changes) ||
		!relationBackendChangesEqual(change, transaction.intent.Changes[transaction.nextChange]) {
		return relationBackendErrMismatch
	}

	var err error
	switch change.Kind {
	case relationBackendCreateModel:
		var statement string
		statement, err = sqliteRelationCompileCreateTable(change.After, change.After.Table)
		if err == nil {
			err = transaction.sqliteRelationExecStage(ctx, faultStageCreate, statement)
		}
	case relationBackendDeleteModel:
		err = transaction.sqliteRelationExecStage(
			ctx, faultStageDrop,
			`DROP TABLE `+sqliteRelationQualifiedMain(change.Before.Table),
		)
	case relationBackendAddField:
		var statement string
		statement, err = sqliteRelationCompileAddField(change.After.Table, change.Relation)
		if err == nil {
			err = transaction.sqliteRelationExecStage(ctx, faultStageCreate, statement)
		}
	case relationBackendRemoveField:
		err = transaction.sqliteRelationRemake(ctx, change)
	default:
		err = fmt.Errorf("unsupported relation change kind %d", change.Kind)
	}
	if err != nil {
		return err
	}
	transaction.nextChange++
	return nil
}

func (transaction *sqliteRelationTransaction) sqliteRelationExecStage(
	ctx context.Context,
	stage faultStage,
	statement string,
	arguments ...any,
) error {
	if err := transaction.backend.faults.faultHit(stage); err != nil {
		return fmt.Errorf("SQLite relation %s: %w", stage, err)
	}
	transaction.backend.sqliteRelationTrace(statement)
	if _, err := transaction.connection.ExecContext(ctx, statement, arguments...); err != nil {
		return fmt.Errorf("SQLite relation %s: %w", stage, err)
	}
	return nil
}

func (transaction *sqliteRelationTransaction) RecordRelationTransition(ctx context.Context) error {
	if ctx == nil {
		return errors.New("record SQLite relation transition context is nil")
	}
	if transaction.complete || transaction.recorded {
		return errors.New("SQLite relation transition recorder called in invalid state")
	}
	if transaction.nextChange != len(transaction.intent.Changes) {
		return fmt.Errorf("relation changes consumed %d of %d", transaction.nextChange, len(transaction.intent.Changes))
	}
	if err := transaction.sqliteRelationVerifyPhysicalAfterState(ctx); err != nil {
		return err
	}
	if err := transaction.backend.faults.faultHit(faultStageRecorder); err != nil {
		return fmt.Errorf("record SQLite relation transition: %w", err)
	}

	var err error
	switch transaction.transition.Direction {
	case relationBackendApply:
		_, err = transaction.connection.ExecContext(
			ctx,
			`INSERT INTO `+sqliteRelationQualifiedMain(sqliteRelationRecorderTable)+` ("app", "name") VALUES (?, ?)`,
			transaction.transition.App,
			transaction.transition.Name,
		)
	case relationBackendUnapply:
		var result sql.Result
		result, err = transaction.connection.ExecContext(
			ctx,
			`DELETE FROM `+sqliteRelationQualifiedMain(sqliteRelationRecorderTable)+` WHERE "app" = ? AND "name" = ?`,
			transaction.transition.App,
			transaction.transition.Name,
		)
		if err == nil {
			var rows int64
			rows, err = result.RowsAffected()
			if err == nil && rows != 1 {
				err = fmt.Errorf("%w: recorder delete affected %d rows", sqliteRelationErrRecorder, rows)
			}
		}
	default:
		err = errors.New("relation transition direction is invalid")
	}
	if err != nil {
		return fmt.Errorf("record SQLite relation transition: %w", err)
	}
	transaction.recorded = true
	return nil
}

func (transaction *sqliteRelationTransaction) sqliteRelationVerifyPhysicalAfterState(ctx context.Context) error {
	type finalState struct {
		model   relationBackendModel
		present bool
	}
	finalByTable := make(map[string]finalState)
	for _, change := range transaction.intent.Changes {
		if change.Kind == relationBackendDeleteModel {
			finalByTable[relationBackendIdentifierKey(change.Before.Table)] = finalState{model: change.Before.relationBackendClone(), present: false}
			continue
		}
		finalByTable[relationBackendIdentifierKey(change.After.Table)] = finalState{model: change.After.relationBackendClone(), present: true}
	}
	tables := make([]string, 0, len(finalByTable))
	for table := range finalByTable {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	for _, table := range tables {
		state := finalByTable[table]
		if state.present {
			if err := sqliteRelationAssertModelShape(ctx, transaction.connection, state.model); err != nil {
				return fmt.Errorf("verify relation after-state for %q: %w", state.model.Table, err)
			}
		} else {
			exists, err := sqliteRelationTableExists(ctx, transaction.connection, state.model.Table)
			if err != nil {
				return err
			}
			if exists {
				return fmt.Errorf("%w: deleted table %q still exists", sqliteRelationErrDrift, state.model.Table)
			}
		}
	}
	if err := transaction.backend.faults.faultHit(faultStageForeignKey); err != nil {
		return fmt.Errorf("verify SQLite relation foreign keys: %w", err)
	}
	rows, err := transaction.connection.QueryContext(ctx, `PRAGMA main.foreign_key_check`)
	if err != nil {
		return fmt.Errorf("run SQLite foreign_key_check: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		var table string
		var rowID sql.NullInt64
		var parent string
		var foreignKeyID int
		if err := rows.Scan(&table, &rowID, &parent, &foreignKeyID); err != nil {
			return fmt.Errorf("scan SQLite foreign_key_check: %w", err)
		}
		return fmt.Errorf("%w: table=%q parent=%q fk=%d", sqliteRelationErrForeignKeyCheck, table, parent, foreignKeyID)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate SQLite foreign_key_check: %w", err)
	}
	return nil
}

func (transaction *sqliteRelationTransaction) CommitRelationFenced(ctx context.Context) (migrationbackend.CommitOutcome, error) {
	unknown := migrationbackend.CommitOutcome{Durability: migrationbackend.CommitUnknown}
	if ctx == nil {
		return unknown, errors.New("commit SQLite relation context is nil")
	}
	if transaction.complete {
		return unknown, errors.New("SQLite relation transaction is complete")
	}
	if !transaction.recorded {
		return transaction.sqliteRelationCommitRollback(ctx, errors.New("relation transition was not recorded"))
	}
	if err := transaction.backend.faults.faultHit(faultStageRevisionVerify); err != nil {
		return transaction.sqliteRelationCommitRollback(ctx, fmt.Errorf("verify relation revision: %w", err))
	}
	if err := transaction.sqliteRelationVerifySuccessor(ctx); err != nil {
		return transaction.sqliteRelationCommitRollback(ctx, err)
	}

	mode, commitCause := transaction.backend.faults.faultCommit()
	transaction.backend.mu.Lock()
	transaction.backend.commitCalls++
	transaction.backend.mu.Unlock()
	switch mode {
	case faultCommitRolledBack:
		if commitCause == nil {
			commitCause = errors.New("injected commit failure")
		}
		return transaction.sqliteRelationCommitRollback(ctx, fmt.Errorf("commit SQLite relation: %w", commitCause))
	case faultCommitCommitted:
		if err := transaction.sqliteRelationRawCommit(ctx); err != nil {
			return transaction.sqliteRelationFinishUnknownCommitError(fmt.Errorf("commit SQLite relation: %w", err))
		}
		if commitCause == nil {
			commitCause = errors.New("injected post-commit cleanup failure")
		}
		return transaction.sqliteRelationFinish(
			migrationbackend.CommitOutcome{Durability: migrationbackend.CommitCommitted},
			fmt.Errorf("close committed SQLite relation transaction: %w", commitCause),
		)
	case faultCommitUnknown:
		// The candidate deliberately makes the external state durable and then
		// withholds proof. Core must retain its last confirmed state and must not
		// retry; a fresh process may discover the durable successor.
		if err := transaction.sqliteRelationRawCommit(ctx); err != nil {
			return transaction.sqliteRelationFinishUnknownCommitError(
				fmt.Errorf("commit SQLite relation: %w", errors.Join(commitCause, err)),
			)
		}
		if commitCause == nil {
			commitCause = errors.New("injected unknown commit durability")
		}
		return transaction.sqliteRelationFinish(unknown, fmt.Errorf("commit SQLite relation: %w", commitCause))
	case faultCommitNone:
		if err := transaction.sqliteRelationRawCommit(ctx); err != nil {
			return transaction.sqliteRelationFinishUnknownCommitError(fmt.Errorf("commit SQLite relation: %w", err))
		}
		return transaction.sqliteRelationFinish(
			migrationbackend.CommitOutcome{Durability: migrationbackend.CommitCommitted}, nil,
		)
	default:
		return transaction.sqliteRelationCommitRollback(ctx, errors.New("invalid injected commit mode"))
	}
}

func (transaction *sqliteRelationTransaction) sqliteRelationRawCommit(ctx context.Context) error {
	if transaction.backend != nil && transaction.backend.commitConnection != nil {
		_, err := transaction.backend.commitConnection(ctx, transaction.connection)
		return err
	}
	_, err := transaction.connection.ExecContext(ctx, `COMMIT`)
	return err
}

func (transaction *sqliteRelationTransaction) sqliteRelationFinishUnknownCommitError(
	primary error,
) (migrationbackend.CommitOutcome, error) {
	transaction.complete = true
	return migrationbackend.CommitOutcome{Durability: migrationbackend.CommitUnknown}, errors.Join(
		primary,
		sqliteRelationDiscardConnection(transaction.backend, transaction.connection),
	)
}

func (transaction *sqliteRelationTransaction) sqliteRelationVerifySuccessor(ctx context.Context) error {
	var revision int64
	if err := transaction.connection.QueryRowContext(
		ctx,
		`SELECT "revision" FROM `+sqliteRelationQualifiedMain(sqliteRelationRevisionTable)+` WHERE "singleton" = 1`,
	).Scan(&revision); err != nil {
		return fmt.Errorf("read relation successor revision: %w", err)
	}
	if revision != transaction.transition.ToRevision {
		return fmt.Errorf("%w: got %d want %d", sqliteRelationErrRevision, revision, transaction.transition.ToRevision)
	}
	var records int
	if err := transaction.connection.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM `+sqliteRelationQualifiedMain(sqliteRelationRecorderTable)+` WHERE "app" = ? AND "name" = ?`,
		transaction.transition.App,
		transaction.transition.Name,
	).Scan(&records); err != nil {
		return fmt.Errorf("read relation successor recorder: %w", err)
	}
	wantRecords := 1
	if transaction.transition.Direction == relationBackendUnapply {
		wantRecords = 0
	}
	if records != wantRecords {
		return fmt.Errorf("%w: got %d rows want %d", sqliteRelationErrRecorder, records, wantRecords)
	}
	return nil
}

func (transaction *sqliteRelationTransaction) sqliteRelationCommitRollback(
	ctx context.Context,
	primary error,
) (migrationbackend.CommitOutcome, error) {
	transaction.backend.mu.Lock()
	transaction.backend.rollbackCalls++
	transaction.backend.mu.Unlock()
	rollbackFault := transaction.backend.faults.faultHit(faultStageRollback)
	cleanupCtx, cancel := sqliteRelationDetachedCleanupContext(ctx)
	defer cancel()
	var rollbackErr error
	if rollbackFault != nil {
		rollbackErr = rollbackFault
	} else {
		_, rollbackErr = transaction.connection.ExecContext(cleanupCtx, `ROLLBACK`)
	}
	if rollbackErr == nil {
		return transaction.sqliteRelationFinish(
			migrationbackend.CommitOutcome{Durability: migrationbackend.CommitRolledBack}, primary,
		)
	}
	transaction.complete = true
	return migrationbackend.CommitOutcome{Durability: migrationbackend.CommitUnknown}, errors.Join(
		primary,
		fmt.Errorf("rollback SQLite relation: %w", rollbackErr),
		sqliteRelationDiscardConnection(transaction.backend, transaction.connection),
	)
}

func (transaction *sqliteRelationTransaction) RollbackRelation(ctx context.Context) error {
	if transaction.complete {
		return nil
	}
	transaction.backend.mu.Lock()
	transaction.backend.rollbackCalls++
	transaction.backend.mu.Unlock()
	rollbackFault := transaction.backend.faults.faultHit(faultStageRollback)
	cleanupCtx, cancel := sqliteRelationDetachedCleanupContext(ctx)
	defer cancel()
	var rollbackErr error
	if rollbackFault != nil {
		rollbackErr = rollbackFault
	} else {
		_, rollbackErr = transaction.connection.ExecContext(cleanupCtx, `ROLLBACK`)
	}
	if rollbackErr != nil {
		transaction.complete = true
		return errors.Join(
			fmt.Errorf("rollback SQLite relation: %w", rollbackErr),
			sqliteRelationDiscardConnection(transaction.backend, transaction.connection),
		)
	}
	_, finishErr := transaction.sqliteRelationFinish(
		migrationbackend.CommitOutcome{Durability: migrationbackend.CommitRolledBack}, nil,
	)
	return finishErr
}

func (transaction *sqliteRelationTransaction) sqliteRelationRollbackAndClose(ctx context.Context, primary error) error {
	transaction.backend.mu.Lock()
	transaction.backend.rollbackCalls++
	transaction.backend.mu.Unlock()
	cleanupCtx, cancel := sqliteRelationDetachedCleanupContext(ctx)
	defer cancel()
	_, rollbackErr := transaction.connection.ExecContext(cleanupCtx, `ROLLBACK`)
	if rollbackErr != nil {
		transaction.complete = true
		return errors.Join(
			primary,
			fmt.Errorf("rollback SQLite relation: %w", rollbackErr),
			sqliteRelationDiscardConnection(transaction.backend, transaction.connection),
		)
	}
	transaction.complete = true
	return errors.Join(primary, sqliteRelationCloseConnection(transaction.backend, transaction.connection))
}

func sqliteRelationDetachedCleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		return context.WithTimeout(context.Background(), sqliteRelationCleanupTimeout)
	}
	return context.WithTimeout(context.WithoutCancel(ctx), sqliteRelationCleanupTimeout)
}

func sqliteRelationDiscardConnection(backend *sqliteRelationBackend, connection *sql.Conn) error {
	if connection == nil {
		return nil
	}
	if backend != nil {
		backend.mu.Lock()
		backend.discardCalls++
		backend.mu.Unlock()
	}
	err := connection.Raw(func(any) error { return driver.ErrBadConn })
	if errors.Is(err, driver.ErrBadConn) || errors.Is(err, sql.ErrConnDone) {
		return nil
	}
	if err == nil {
		return errors.New("discard SQLite relation connection returned nil without confirmation")
	}
	return fmt.Errorf("discard SQLite relation connection: %w", err)
}

func sqliteRelationCloseConnection(backend *sqliteRelationBackend, connection *sql.Conn) error {
	if connection == nil {
		return nil
	}
	closeConnection := connection.Close
	if backend != nil && backend.closeConnection != nil {
		closeConnection = func() error { return backend.closeConnection(connection) }
	}
	if err := closeConnection(); err != nil {
		return errors.Join(
			fmt.Errorf("close SQLite relation connection: %w", err),
			sqliteRelationDiscardConnection(backend, connection),
		)
	}
	return nil
}

func (transaction *sqliteRelationTransaction) sqliteRelationFinish(
	outcome migrationbackend.CommitOutcome,
	primary error,
) (migrationbackend.CommitOutcome, error) {
	transaction.complete = true
	return outcome, errors.Join(primary, sqliteRelationCloseConnection(transaction.backend, transaction.connection))
}

func sqliteRelationInitialize(ctx context.Context, database *sql.DB) error {
	if ctx == nil {
		return errors.New("initialize SQLite relation candidate context is nil")
	}
	connection, err := database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("pin SQLite relation initialization connection: %w", err)
	}
	finish := func(primary error) error {
		return errors.Join(primary, sqliteRelationCloseConnection(nil, connection))
	}
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return errors.Join(
			fmt.Errorf("begin SQLite relation initialization: %w", err),
			sqliteRelationDiscardConnection(nil, connection),
		)
	}
	rollback := func(primary error) error {
		cleanupCtx, cancel := sqliteRelationDetachedCleanupContext(ctx)
		defer cancel()
		_, rollbackErr := connection.ExecContext(cleanupCtx, `ROLLBACK`)
		if rollbackErr != nil {
			return errors.Join(
				primary,
				fmt.Errorf("rollback SQLite relation initialization: %w", rollbackErr),
				sqliteRelationDiscardConnection(nil, connection),
			)
		}
		return finish(primary)
	}
	createStatements := []string{
		`CREATE TABLE IF NOT EXISTS ` + sqliteRelationQualifiedMain(sqliteRelationRevisionTable) + ` (` +
			`"singleton" INTEGER NOT NULL PRIMARY KEY CHECK ("singleton" = 1), ` +
			`"revision" INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS ` + sqliteRelationQualifiedMain(sqliteRelationRecorderTable) + ` (` +
			`"app" TEXT NOT NULL, "name" TEXT NOT NULL, PRIMARY KEY ("app", "name"))`,
	}
	for _, statement := range createStatements {
		if _, err := connection.ExecContext(ctx, statement); err != nil {
			return rollback(fmt.Errorf("initialize SQLite relation candidate: %w", err))
		}
	}
	catalog, err := sqliteRelationLoadPhysicalCatalog(ctx, connection, sqliteRelationDefaultPhysicalLimits)
	if err != nil {
		return rollback(fmt.Errorf("validate SQLite relation initialization catalog: %w", err))
	}
	if err := sqliteRelationValidateControlCatalog(ctx, connection, catalog); err != nil {
		return rollback(fmt.Errorf("validate SQLite relation initialization controls: %w", err))
	}
	if sqliteRelationInitializeAfterValidationHook != nil {
		if err := sqliteRelationInitializeAfterValidationHook(); err != nil {
			return rollback(fmt.Errorf("SQLite relation initialization validation hook: %w", err))
		}
	}
	if _, err := connection.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO `+sqliteRelationQualifiedMain(sqliteRelationRevisionTable)+` ("singleton", "revision") VALUES (1, 0)`,
	); err != nil {
		return rollback(fmt.Errorf("initialize SQLite relation revision: %w", err))
	}
	if err := sqliteRelationValidateDurableState(
		ctx,
		connection,
		sqliteRelationDefaultPhysicalLimits,
	); err != nil {
		return rollback(fmt.Errorf("validate SQLite relation durable state: %w", err))
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return errors.Join(
			fmt.Errorf("commit SQLite relation initialization: %w", err),
			sqliteRelationDiscardConnection(nil, connection),
		)
	}
	return finish(nil)
}

func sqliteRelationCompileCreateTable(model relationBackendModel, table string) (string, error) {
	if err := relationBackendValidateModel(model); err != nil {
		return "", err
	}
	definitions := make([]string, 0, len(model.Columns)+2*len(model.Relations))
	for _, field := range sqliteRelationOrderedFields(model) {
		if field.column != nil {
			definitions = append(definitions, sqliteRelationCompileScalarDefinition(*field.column))
			continue
		}
		definitions = append(definitions, sqliteRelationCompileRelationDefinition(*field.relation))
	}
	for _, relation := range sqliteRelationOrderedRelations(model) {
		definitions = append(definitions, sqliteRelationCompileForeignKeyDefinition(relation))
	}
	return "CREATE TABLE " + sqliteRelationQualifiedMain(table) + " (" + strings.Join(definitions, ", ") + ")", nil
}

type sqliteRelationOrderedField struct {
	position int
	column   *relationBackendColumn
	relation *relationBackendRelation
}

func sqliteRelationOrderedFields(model relationBackendModel) []sqliteRelationOrderedField {
	fields := make([]sqliteRelationOrderedField, len(model.Columns)+len(model.Relations))
	for index := range model.Columns {
		fields[model.Columns[index].Position-1] = sqliteRelationOrderedField{
			position: model.Columns[index].Position,
			column:   &model.Columns[index],
		}
	}
	for index := range model.Relations {
		fields[model.Relations[index].Position-1] = sqliteRelationOrderedField{
			position: model.Relations[index].Position,
			relation: &model.Relations[index],
		}
	}
	return fields
}

func sqliteRelationOrderedRelations(model relationBackendModel) []relationBackendRelation {
	relations := make([]relationBackendRelation, 0, len(model.Relations))
	for _, field := range sqliteRelationOrderedFields(model) {
		if field.relation != nil {
			relations = append(relations, *field.relation)
		}
	}
	return relations
}

func sqliteRelationCompileScalarDefinition(column relationBackendColumn) string {
	definition := sqliteRelationQuoteIdentifier(column.Name) + " "
	switch column.Type {
	case "INTEGER":
		definition += "INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT"
	case "VARCHAR":
		definition += fmt.Sprintf("VARCHAR(%d)", column.MaxLength)
		if column.Nullable {
			definition += " NULL"
		} else {
			definition += " NOT NULL"
		}
	case "BOOLEAN":
		definition += "BOOLEAN NOT NULL"
	}
	return definition
}

func sqliteRelationCompileRelationDefinition(relation relationBackendRelation) string {
	definition := sqliteRelationQuoteIdentifier(relation.Column) + " INTEGER"
	if relation.Nullable {
		definition += " NULL"
	} else {
		definition += " NOT NULL"
	}
	return definition
}

func sqliteRelationCompileForeignKeyDefinition(relation relationBackendRelation) string {
	return "FOREIGN KEY (" + sqliteRelationQuoteIdentifier(relation.Column) + ") REFERENCES " +
		sqliteRelationQuoteIdentifier(relation.TargetTable) + " (" +
		sqliteRelationQuoteIdentifier(relation.TargetColumn) + ") ON DELETE NO ACTION"
}

func sqliteRelationCompileAddField(table string, relation relationBackendRelation) (string, error) {
	if table == "" || relation.Column == "" || relation.TargetTable == "" || relation.TargetColumn == "" {
		return "", errors.New("SQLite relation AddField input is incomplete")
	}
	statement := "ALTER TABLE " + sqliteRelationQualifiedMain(table) + " ADD COLUMN " +
		sqliteRelationCompileRelationDefinition(relation)
	statement += " REFERENCES " + sqliteRelationQuoteIdentifier(relation.TargetTable) +
		" (" + sqliteRelationQuoteIdentifier(relation.TargetColumn) + ") ON DELETE NO ACTION"
	return statement, nil
}

func sqliteRelationQuoteIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func sqliteRelationQualifiedMain(identifier string) string {
	return `"main".` + sqliteRelationQuoteIdentifier(identifier)
}

func sqliteRelationTemporaryTable(change relationBackendChange) string {
	digest := sha256.Sum256([]byte(change.Before.Table + "\x00" + change.Relation.Column))
	return fmt.Sprintf("__godj_relation_%x", digest[:6])
}

func (transaction *sqliteRelationTransaction) sqliteRelationRemake(
	ctx context.Context,
	change relationBackendChange,
) error {
	temporary := sqliteRelationTemporaryTable(change)
	tableKey := relationBackendIdentifierKey(change.Before.Table)
	sequence, preflighted := transaction.remakeSequences[tableKey]
	if !preflighted {
		return fmt.Errorf(
			"%w: sqlite_sequence for remake table %q was not preflighted",
			sqliteRelationErrDrift,
			change.Before.Table,
		)
	}
	var beforeRows int64
	if err := transaction.connection.QueryRowContext(
		ctx, `SELECT COUNT(*) FROM `+sqliteRelationQualifiedMain(change.Before.Table),
	).Scan(&beforeRows); err != nil {
		return fmt.Errorf("count rows before relation remake: %w", err)
	}

	createSQL, err := sqliteRelationCompileCreateTable(change.After, temporary)
	if err != nil {
		return err
	}
	if err := transaction.sqliteRelationExecStage(ctx, faultStageCreate, createSQL); err != nil {
		return err
	}
	columns := sqliteRelationModelColumnNames(change.After)
	quoted := make([]string, len(columns))
	for index, column := range columns {
		quoted[index] = sqliteRelationQuoteIdentifier(column)
	}
	primaryKey := sqliteRelationPrimaryKey(change.After)
	copySQL := "INSERT INTO " + sqliteRelationQualifiedMain(temporary) + " (" + strings.Join(quoted, ", ") + ") " +
		"SELECT " + strings.Join(quoted, ", ") + " FROM " + sqliteRelationQualifiedMain(change.Before.Table) +
		" ORDER BY " + sqliteRelationQuoteIdentifier(primaryKey)
	if err := transaction.sqliteRelationExecStage(ctx, faultStageCopy, copySQL); err != nil {
		return err
	}
	var copiedRows int64
	if err := transaction.connection.QueryRowContext(
		ctx, `SELECT COUNT(*) FROM `+sqliteRelationQualifiedMain(temporary),
	).Scan(&copiedRows); err != nil {
		return fmt.Errorf("count copied rows during relation remake: %w", err)
	}
	if copiedRows != beforeRows {
		return fmt.Errorf("relation remake copied %d rows, want %d", copiedRows, beforeRows)
	}
	if err := transaction.sqliteRelationExecStage(
		ctx, faultStageDrop, `DROP TABLE `+sqliteRelationQualifiedMain(change.Before.Table),
	); err != nil {
		return err
	}
	if err := transaction.sqliteRelationExecStage(
		ctx, faultStageRename,
		`ALTER TABLE `+sqliteRelationQualifiedMain(temporary)+` RENAME TO `+sqliteRelationQuoteIdentifier(change.After.Table),
	); err != nil {
		return err
	}
	if _, err := transaction.connection.ExecContext(
		ctx,
		`DELETE FROM "main"."sqlite_sequence" WHERE "name" COLLATE NOCASE IN (?, ?)`,
		temporary,
		change.After.Table,
	); err != nil {
		return fmt.Errorf("clear SQLite sequence during relation remake: %w", err)
	}
	if sequence.Present {
		if _, err := transaction.connection.ExecContext(
			ctx,
			`INSERT INTO "main"."sqlite_sequence" ("name", "seq") VALUES (?, ?)`,
			change.After.Table,
			sequence.Value,
		); err != nil {
			return fmt.Errorf("restore SQLite sequence during relation remake: %w", err)
		}
	}
	if err := sqliteRelationVerifySequenceRestore(
		ctx,
		transaction.connection,
		change.After.Table,
		sequence,
	); err != nil {
		return err
	}
	return nil
}

func sqliteRelationVerifySequenceRestore(
	ctx context.Context,
	connection *sql.Conn,
	table string,
	want sqliteRelationSequenceSnapshot,
) error {
	rows, err := connection.QueryContext(
		ctx,
		`SELECT typeof("name"), "name", typeof("seq"), `+
			`CASE WHEN typeof("seq") = 'integer' THEN "seq" ELSE NULL END `+
			`FROM "main"."sqlite_sequence" WHERE "name" COLLATE NOCASE = ? LIMIT 2`,
		table,
	)
	if err != nil {
		return fmt.Errorf("verify SQLite sequence after relation remake: %w", err)
	}
	count := 0
	for rows.Next() {
		var nameType, name, sequenceType string
		var sequence sql.NullInt64
		if err := rows.Scan(&nameType, &name, &sequenceType, &sequence); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan SQLite sequence after relation remake: %w", err)
		}
		count++
		if count > 1 || !want.Present || nameType != "text" || name != table ||
			sequenceType != "integer" || !sequence.Valid || sequence.Int64 != want.Value {
			_ = rows.Close()
			return fmt.Errorf("%w: restored sqlite_sequence for %q is outside the closed shape", sqliteRelationErrDrift, table)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate SQLite sequence after relation remake: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close SQLite sequence after relation remake: %w", err)
	}
	wantCount := 0
	if want.Present {
		wantCount = 1
	}
	if count != wantCount {
		return fmt.Errorf(
			"%w: restored sqlite_sequence row count for %q=%d, want %d",
			sqliteRelationErrDrift,
			table,
			count,
			wantCount,
		)
	}
	return nil
}

func sqliteRelationLoadPhysicalCatalog(
	ctx context.Context,
	connection *sql.Conn,
	limits sqliteRelationPhysicalLimits,
) (sqliteRelationPhysicalCatalog, error) {
	if limits.MaxObjects <= 0 || limits.MaxStatementBytes <= 0 || limits.MaxBatchBytes <= 0 || limits.MaxGraphWork <= 0 ||
		limits.MaxCatalogWork <= 0 || limits.MaxTargetChecks <= 0 {
		return sqliteRelationPhysicalCatalog{}, fmt.Errorf("%w: physical catalog limits are invalid", sqliteRelationErrDrift)
	}
	catalog := sqliteRelationPhysicalCatalog{}
	statementBytes := 0
	for _, schema := range []string{"main", "temp"} {
		remainingObjects := limits.MaxObjects - len(catalog.Objects)
		if remainingObjects < 0 {
			return sqliteRelationPhysicalCatalog{}, fmt.Errorf(
				"%w: physical catalog object resource limit exceeded: more than %d",
				sqliteRelationErrDrift,
				limits.MaxObjects,
			)
		}
		rows, err := connection.QueryContext(
			ctx,
			`SELECT `+
				`COALESCE(length(CAST("type" AS BLOB)), -1), `+
				`COALESCE(length(CAST("name" AS BLOB)), -1), `+
				`COALESCE(length(CAST("tbl_name" AS BLOB)), -1), `+
				`COALESCE(length(CAST("sql" AS BLOB)), 0), `+
				`substr(CAST("type" AS BLOB), 1, ?), `+
				`substr(CAST("name" AS BLOB), 1, ?), `+
				`substr(CAST("tbl_name" AS BLOB), 1, ?), `+
				`COALESCE(substr(CAST("sql" AS BLOB), 1, ?), X'') FROM `+
				sqliteRelationQualifiedSchema(schema, "sqlite_schema")+` LIMIT ?`,
			migrationdefinition.MaxSourceIDBytes+1,
			migrationdefinition.MaxSourceIDBytes+1,
			migrationdefinition.MaxSourceIDBytes+1,
			limits.MaxStatementBytes+1,
			remainingObjects+1,
		)
		if err != nil {
			return sqliteRelationPhysicalCatalog{}, fmt.Errorf("list SQLite physical catalog in %s: %w", schema, err)
		}
		for rows.Next() {
			var object sqliteRelationSchemaObject
			var typeBytes, nameBytes, ownerBytes, sqlBytes int64
			var typePrefix, namePrefix, ownerPrefix, sqlPrefix []byte
			object.Schema = schema
			if err := rows.Scan(
				&typeBytes,
				&nameBytes,
				&ownerBytes,
				&sqlBytes,
				&typePrefix,
				&namePrefix,
				&ownerPrefix,
				&sqlPrefix,
			); err != nil {
				_ = rows.Close()
				return sqliteRelationPhysicalCatalog{}, fmt.Errorf("scan SQLite physical catalog in %s: %w", schema, err)
			}
			if typeBytes < 0 || nameBytes < 0 || ownerBytes < 0 || sqlBytes < 0 ||
				typeBytes > int64(migrationdefinition.MaxSourceIDBytes) ||
				nameBytes > int64(migrationdefinition.MaxSourceIDBytes) ||
				ownerBytes > int64(migrationdefinition.MaxSourceIDBytes) {
				_ = rows.Close()
				return sqliteRelationPhysicalCatalog{}, fmt.Errorf(
					"%w: physical catalog identifier resource limit exceeded",
					sqliteRelationErrDrift,
				)
			}
			if len(catalog.Objects) >= limits.MaxObjects {
				_ = rows.Close()
				return sqliteRelationPhysicalCatalog{}, fmt.Errorf(
					"%w: physical catalog object resource limit exceeded: more than %d",
					sqliteRelationErrDrift,
					limits.MaxObjects,
				)
			}
			if sqlBytes > int64(limits.MaxStatementBytes) {
				_ = rows.Close()
				return sqliteRelationPhysicalCatalog{}, fmt.Errorf(
					"%w: physical catalog SQL resource limit exceeded: %d > %d",
					sqliteRelationErrDrift,
					sqlBytes,
					limits.MaxStatementBytes,
				)
			}
			if sqlBytes > int64(limits.MaxBatchBytes-statementBytes) {
				_ = rows.Close()
				return sqliteRelationPhysicalCatalog{}, fmt.Errorf(
					"%w: aggregate physical catalog SQL resource limit exceeded: more than %d bytes",
					sqliteRelationErrDrift,
					limits.MaxBatchBytes,
				)
			}
			object.Type = string(typePrefix)
			object.Name = string(namePrefix)
			object.Owner = string(ownerPrefix)
			object.SQL = string(sqlPrefix)
			statementBytes += int(sqlBytes)
			catalog.Objects = append(catalog.Objects, object)
		}
		if err := rows.Close(); err != nil {
			return sqliteRelationPhysicalCatalog{}, fmt.Errorf("close SQLite physical catalog in %s: %w", schema, err)
		}
		if err := rows.Err(); err != nil {
			return sqliteRelationPhysicalCatalog{}, fmt.Errorf("iterate SQLite physical catalog in %s: %w", schema, err)
		}
	}
	return catalog, nil
}

func sqliteRelationValidateControlCatalog(
	ctx context.Context,
	connection *sql.Conn,
	catalog sqliteRelationPhysicalCatalog,
) error {
	expectedSQL := map[string]string{
		sqliteRelationRevisionTable: `CREATE TABLE "` + sqliteRelationRevisionTable + `" (` +
			`"singleton" INTEGER NOT NULL PRIMARY KEY CHECK ("singleton" = 1), "revision" INTEGER NOT NULL)`,
		sqliteRelationRecorderTable: `CREATE TABLE "` + sqliteRelationRecorderTable + `" (` +
			`"app" TEXT NOT NULL, "name" TEXT NOT NULL, PRIMARY KEY ("app", "name"))`,
	}
	found := make(map[string]int, len(expectedSQL))
	for _, object := range catalog.Objects {
		for control, statement := range expectedSQL {
			controlKey := relationBackendIdentifierKey(control)
			if object.Schema == "main" && relationBackendIdentifierKey(object.Name) == controlKey {
				if object.Type != "table" || object.Name != control || object.Owner != control || object.SQL != statement {
					return fmt.Errorf(
						"%w: main object %q has type=%q owner=%q SQL=%q",
						sqliteRelationErrControlCatalog,
						object.Name,
						object.Type,
						object.Owner,
						object.SQL,
					)
				}
				found[control]++
			}
			if (object.Type == "trigger" || object.Type == "view") &&
				(relationBackendIdentifierKey(object.Owner) == controlKey ||
					sqliteRelationSQLReferencesIdentifier(object.SQL, control)) {
				return fmt.Errorf(
					"%w: %s %s.%q references control table %q",
					sqliteRelationErrControlCatalog,
					object.Type,
					object.Schema,
					object.Name,
					control,
				)
			}
		}
	}
	for control := range expectedSQL {
		if found[control] != 1 {
			return fmt.Errorf(
				"%w: main control table %q count=%d, want 1",
				sqliteRelationErrControlCatalog,
				control,
				found[control],
			)
		}
	}
	if err := sqliteRelationValidateRevisionControlShape(ctx, connection); err != nil {
		return err
	}
	return sqliteRelationValidateRecorderControlShape(ctx, connection)
}

type sqliteRelationControlColumn struct {
	Name       string
	Type       string
	NotNull    int
	PrimaryKey int
}

func sqliteRelationReadControlColumns(
	ctx context.Context,
	connection *sql.Conn,
	table string,
) ([]sqliteRelationControlColumn, error) {
	rows, err := connection.QueryContext(ctx, `PRAGMA main.table_xinfo(`+sqliteRelationQuoteIdentifier(table)+`)`)
	if err != nil {
		return nil, fmt.Errorf("read SQLite control columns for %q: %w", table, err)
	}
	var columns []sqliteRelationControlColumn
	for rows.Next() {
		var cid, hidden int
		var column sqliteRelationControlColumn
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &column.Name, &column.Type, &column.NotNull, &defaultValue, &column.PrimaryKey, &hidden); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan SQLite control columns for %q: %w", table, err)
		}
		if cid != len(columns) || hidden != 0 || defaultValue.Valid {
			_ = rows.Close()
			return nil, fmt.Errorf("%w: control table %q has non-closed column metadata", sqliteRelationErrControlCatalog, table)
		}
		columns = append(columns, column)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close SQLite control columns for %q: %w", table, err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate SQLite control columns for %q: %w", table, err)
	}
	return columns, nil
}

func sqliteRelationValidateRevisionControlShape(ctx context.Context, connection *sql.Conn) error {
	columns, err := sqliteRelationReadControlColumns(ctx, connection, sqliteRelationRevisionTable)
	if err != nil {
		return err
	}
	want := []sqliteRelationControlColumn{
		{Name: "singleton", Type: "INTEGER", NotNull: 1, PrimaryKey: 1},
		{Name: "revision", Type: "INTEGER", NotNull: 1},
	}
	if len(columns) != len(want) || columns[0] != want[0] || columns[1] != want[1] {
		return fmt.Errorf("%w: revision control columns=%+v want=%+v", sqliteRelationErrControlCatalog, columns, want)
	}
	var indexes int
	if err := connection.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM "main"."sqlite_schema" WHERE "type" = 'index' AND "tbl_name" = ? COLLATE NOCASE`,
		sqliteRelationRevisionTable,
	).Scan(&indexes); err != nil {
		return fmt.Errorf("inspect revision control indexes: %w", err)
	}
	if indexes != 0 {
		return fmt.Errorf("%w: revision control index count=%d, want 0", sqliteRelationErrControlCatalog, indexes)
	}
	return nil
}

func sqliteRelationValidateRecorderControlShape(ctx context.Context, connection *sql.Conn) error {
	columns, err := sqliteRelationReadControlColumns(ctx, connection, sqliteRelationRecorderTable)
	if err != nil {
		return err
	}
	want := []sqliteRelationControlColumn{
		{Name: "app", Type: "TEXT", NotNull: 1, PrimaryKey: 1},
		{Name: "name", Type: "TEXT", NotNull: 1, PrimaryKey: 2},
	}
	if len(columns) != len(want) || columns[0] != want[0] || columns[1] != want[1] {
		return fmt.Errorf("%w: recorder control columns=%+v want=%+v", sqliteRelationErrControlCatalog, columns, want)
	}
	rows, err := connection.QueryContext(ctx, `PRAGMA main.index_list(`+sqliteRelationQuoteIdentifier(sqliteRelationRecorderTable)+`)`)
	if err != nil {
		return fmt.Errorf("read recorder control indexes: %w", err)
	}
	indexCount := 0
	for rows.Next() {
		var sequence, unique, partial int
		var name, origin string
		if err := rows.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan recorder control indexes: %w", err)
		}
		wantName := "sqlite_autoindex_" + sqliteRelationRecorderTable + "_1"
		if sequence != 0 || name != wantName || unique != 1 || origin != "pk" || partial != 0 {
			_ = rows.Close()
			return fmt.Errorf("%w: recorder control index %q is outside closed shape", sqliteRelationErrControlCatalog, name)
		}
		indexCount++
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close recorder control indexes: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate recorder control indexes: %w", err)
	}
	if indexCount != 1 {
		return fmt.Errorf("%w: recorder control index count=%d, want 1", sqliteRelationErrControlCatalog, indexCount)
	}
	return nil
}

func sqliteRelationValidateDurableState(
	ctx context.Context,
	connection *sql.Conn,
	limits sqliteRelationPhysicalLimits,
) error {
	revisionRows, err := connection.QueryContext(
		ctx,
		`SELECT typeof("singleton"), `+
			`CASE WHEN typeof("singleton") = 'integer' THEN "singleton" ELSE NULL END, `+
			`typeof("revision"), `+
			`CASE WHEN typeof("revision") = 'integer' THEN "revision" ELSE NULL END `+
			`FROM `+sqliteRelationQualifiedMain(sqliteRelationRevisionTable)+` LIMIT 2`,
	)
	if err != nil {
		return fmt.Errorf("%w: read durable revision: %v", sqliteRelationErrRevision, err)
	}
	var revision int64
	revisionCount := 0
	for revisionRows.Next() {
		var singletonType, revisionType string
		var singleton, revisionValue sql.NullInt64
		if err := revisionRows.Scan(&singletonType, &singleton, &revisionType, &revisionValue); err != nil {
			_ = revisionRows.Close()
			return fmt.Errorf("%w: scan durable revision: %v", sqliteRelationErrRevision, err)
		}
		revisionCount++
		if revisionCount > 1 || singletonType != "integer" || !singleton.Valid || singleton.Int64 != 1 ||
			revisionType != "integer" || !revisionValue.Valid || revisionValue.Int64 < 0 {
			_ = revisionRows.Close()
			return fmt.Errorf("%w: durable revision singleton is outside the closed shape", sqliteRelationErrRevision)
		}
		revision = revisionValue.Int64
	}
	if err := revisionRows.Err(); err != nil {
		_ = revisionRows.Close()
		return fmt.Errorf("%w: iterate durable revision: %v", sqliteRelationErrRevision, err)
	}
	if err := revisionRows.Close(); err != nil {
		return fmt.Errorf("%w: close durable revision: %v", sqliteRelationErrRevision, err)
	}
	if revisionCount != 1 {
		return fmt.Errorf("%w: durable revision row count=%d, want 1", sqliteRelationErrRevision, revisionCount)
	}

	rows, err := connection.QueryContext(
		ctx,
		`SELECT `+
			`typeof("app"), `+
			`typeof("name"), `+
			`COALESCE(length(CAST("app" AS BLOB)), -1), `+
			`COALESCE(length(CAST("name" AS BLOB)), -1), `+
			`substr(CAST("app" AS BLOB), 1, ?), `+
			`substr(CAST("name" AS BLOB), 1, ?) `+
			`FROM `+sqliteRelationQualifiedMain(sqliteRelationRecorderTable)+` LIMIT ?`,
		migrationdefinition.MaxSourceIDBytes+1,
		migrationdefinition.MaxSourceIDBytes+1,
		limits.MaxObjects+1,
	)
	if err != nil {
		return fmt.Errorf("%w: read durable recorder: %v", sqliteRelationErrRecorder, err)
	}
	records := 0
	aggregateBytes := 0
	for rows.Next() {
		var appType, nameType string
		var appBytes, nameBytes int64
		var appPrefix, namePrefix []byte
		if err := rows.Scan(&appType, &nameType, &appBytes, &nameBytes, &appPrefix, &namePrefix); err != nil {
			_ = rows.Close()
			return fmt.Errorf("%w: scan durable recorder: %v", sqliteRelationErrRecorder, err)
		}
		if records >= limits.MaxObjects {
			_ = rows.Close()
			return fmt.Errorf(
				"%w: durable recorder row resource limit exceeded: more than %d",
				sqliteRelationErrRecorder,
				limits.MaxObjects,
			)
		}
		if appType != "text" || nameType != "text" || appBytes <= 0 || nameBytes <= 0 ||
			appBytes > int64(migrationdefinition.MaxSourceIDBytes) ||
			nameBytes > int64(migrationdefinition.MaxSourceIDBytes) {
			_ = rows.Close()
			return fmt.Errorf(
				"%w: durable recorder identity resource limit exceeded: app_bytes=%d name_bytes=%d",
				sqliteRelationErrRecorder,
				appBytes,
				nameBytes,
			)
		}
		rowBytes := int(appBytes + nameBytes)
		if rowBytes > limits.MaxBatchBytes-aggregateBytes {
			_ = rows.Close()
			return fmt.Errorf(
				"%w: durable recorder aggregate identity bytes exceed %d",
				sqliteRelationErrRecorder,
				limits.MaxBatchBytes,
			)
		}
		if len(appPrefix) != int(appBytes) || len(namePrefix) != int(nameBytes) ||
			!utf8.Valid(appPrefix) || !utf8.Valid(namePrefix) {
			_ = rows.Close()
			return fmt.Errorf("%w: durable recorder identity is truncated or invalid UTF-8", sqliteRelationErrRecorder)
		}
		aggregateBytes += rowBytes
		records++
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("%w: iterate durable recorder: %v", sqliteRelationErrRecorder, err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("%w: close durable recorder: %v", sqliteRelationErrRecorder, err)
	}
	if revision < int64(records) {
		return fmt.Errorf(
			"%w: durable revision %d is behind %d recorder rows",
			sqliteRelationErrRecorder,
			revision,
			records,
		)
	}
	if (revision-int64(records))%2 != 0 {
		return fmt.Errorf(
			"%w: durable revision %d has impossible parity for %d recorder rows",
			sqliteRelationErrRecorder,
			revision,
			records,
		)
	}
	return nil
}

func sqliteRelationPhysicalPreflight(
	ctx context.Context,
	connection *sql.Conn,
	intent relationBackendStepIntent,
	catalog sqliteRelationPhysicalCatalog,
	limits sqliteRelationPhysicalLimits,
	remakeSequences map[string]sqliteRelationSequenceSnapshot,
	sequenceCatalog map[string]sqliteRelationSequenceSnapshot,
) error {
	catalogBytes := 0
	catalogObjectNames := make(map[string]struct{}, len(catalog.Objects))
	for _, object := range catalog.Objects {
		if len(object.SQL) > limits.MaxBatchBytes-catalogBytes {
			return fmt.Errorf("%w: aggregate physical catalog SQL resource limit exceeded", sqliteRelationErrDrift)
		}
		catalogBytes += len(object.SQL)
		catalogObjectNames[relationBackendIdentifierKey(object.Name)] = struct{}{}
	}
	catalogCheckTables := make(map[string]struct{})
	remakeTables := make(map[string]struct{})
	targetChecks := make(map[string]struct{})
	physicalShapeChecks := make(map[string]struct{})
	for _, change := range intent.Changes {
		switch change.Kind {
		case relationBackendRemoveField:
			tableKey := relationBackendIdentifierKey(change.Before.Table)
			catalogCheckTables[tableKey] = struct{}{}
			remakeTables[tableKey] = struct{}{}
		case relationBackendDeleteModel:
			catalogCheckTables[relationBackendIdentifierKey(change.Before.Table)] = struct{}{}
		}
		for _, relation := range append(
			append([]relationBackendRelation(nil), change.After.Relations...),
			change.Relation,
		) {
			if relation.TargetTable == "" || relation.TargetColumn == "" {
				continue
			}
			targetChecks[relationBackendIdentifierKey(relation.TargetTable)+"\x00"+
				relationBackendIdentifierKey(relation.TargetColumn)] = struct{}{}
		}
	}
	catalogWorkPerTable := catalogBytes + len(catalog.Objects)
	if len(catalogCheckTables) != 0 && catalogWorkPerTable != 0 &&
		len(catalogCheckTables) > limits.MaxCatalogWork/catalogWorkPerTable {
		return fmt.Errorf(
			"%w: aggregate catalog scan work exceeds %d",
			sqliteRelationErrDrift,
			limits.MaxCatalogWork,
		)
	}
	sequenceScanWork := 1
	for _, sequence := range sequenceCatalog {
		if len(sequence.Name) > limits.MaxCatalogWork-sequenceScanWork {
			sequenceScanWork = limits.MaxCatalogWork + 1
			break
		}
		sequenceScanWork += 1 + len(sequence.Name)
	}
	if len(remakeTables) != 0 &&
		(sequenceScanWork > limits.MaxCatalogWork || len(remakeTables) > limits.MaxCatalogWork/sequenceScanWork) {
		return fmt.Errorf(
			"%w: aggregate sqlite_sequence scan work exceeds %d",
			sqliteRelationErrDrift,
			limits.MaxCatalogWork,
		)
	}
	if len(targetChecks) > limits.MaxTargetChecks {
		return fmt.Errorf(
			"%w: aggregate physical target checks exceed %d",
			sqliteRelationErrDrift,
			limits.MaxTargetChecks,
		)
	}
	if err := sqliteRelationRejectTempShadows(ctx, connection, intent); err != nil {
		return err
	}
	effectiveSources, err := sqliteRelationLoadEffectiveSources(ctx, connection, catalog)
	if err != nil {
		return err
	}
	if err := sqliteRelationRejectControlInbound(effectiveSources); err != nil {
		return err
	}
	if err := sqliteRelationValidatePhysicalGraphWork(effectiveSources, intent, limits.MaxGraphWork); err != nil {
		return err
	}
	if err := sqliteRelationValidateEffectiveGraph(effectiveSources); err != nil {
		return err
	}
	deletedBefore := make(map[string]bool)
	virtualModels := make(map[string]relationBackendModel)
	createdInIntent := make(map[string]bool)
	physicalTargetChecks := make(map[string]struct{})
	catalogReferenceChecks := make(map[string]struct{})
	remakeStaticChecks := make(map[string]struct{})
	for _, change := range intent.Changes {
		switch change.Kind {
		case relationBackendCreateModel:
			tableKey := relationBackendIdentifierKey(change.After.Table)
			if sequence, exists := sequenceCatalog[tableKey]; exists {
				return fmt.Errorf(
					"%w: create table %q conflicts with orphan sqlite_sequence row %q",
					sqliteRelationErrDrift,
					change.After.Table,
					sequence.Name,
				)
			}
			if err := sqliteRelationRejectMainCreateNamespace(ctx, connection, change.After.Table); err != nil {
				return err
			}
			for _, relation := range change.After.Relations {
				if err := sqliteRelationAssertOrderedTarget(
					ctx,
					connection,
					virtualModels,
					deletedBefore,
					physicalTargetChecks,
					relation,
				); err != nil {
					return err
				}
			}
			virtualModels[tableKey] = change.After.relationBackendClone()
			createdInIntent[tableKey] = true
			effectiveSources[tableKey] = sqliteRelationEffectiveSource{
				Table:     change.After.Table,
				Columns:   append([]relationBackendColumn(nil), change.After.Columns...),
				Relations: append([]relationBackendRelation(nil), change.After.Relations...),
			}
			delete(deletedBefore, tableKey)
		case relationBackendDeleteModel:
			tableKey := relationBackendIdentifierKey(change.Before.Table)
			if _, virtual := virtualModels[tableKey]; !virtual {
				if _, checked := physicalShapeChecks[tableKey]; !checked {
					if err := sqliteRelationAssertModelShape(ctx, connection, change.Before); err != nil {
						return err
					}
					physicalShapeChecks[tableKey] = struct{}{}
				} else if err := sqliteRelationEffectiveSourceMatchesModel(effectiveSources[tableKey], change.Before); err != nil {
					return err
				}
			}
			if err := sqliteRelationRejectStaticCatalogReferences(
				catalog,
				change.Before.Table,
				catalogReferenceChecks,
			); err != nil {
				return err
			}
			if err := sqliteRelationRejectEffectiveInbound(effectiveSources, change.Before.Table); err != nil {
				return err
			}
			delete(virtualModels, tableKey)
			delete(effectiveSources, tableKey)
			deletedBefore[tableKey] = true
		case relationBackendAddField:
			tableKey := relationBackendIdentifierKey(change.Before.Table)
			if _, virtual := virtualModels[tableKey]; !virtual {
				if _, checked := physicalShapeChecks[tableKey]; !checked {
					if err := sqliteRelationAssertModelShape(ctx, connection, change.Before); err != nil {
						return err
					}
					physicalShapeChecks[tableKey] = struct{}{}
				} else if err := sqliteRelationEffectiveSourceMatchesModel(effectiveSources[tableKey], change.Before); err != nil {
					return err
				}
			}
			if err := sqliteRelationAssertOrderedTarget(
				ctx,
				connection,
				virtualModels,
				deletedBefore,
				physicalTargetChecks,
				change.Relation,
			); err != nil {
				return err
			}
			if !change.Relation.Nullable && !createdInIntent[tableKey] {
				var populated int
				if err := connection.QueryRowContext(
					ctx,
					`SELECT EXISTS (SELECT 1 FROM `+sqliteRelationQualifiedMain(change.Before.Table)+` LIMIT 1)`,
				).Scan(&populated); err != nil {
					return fmt.Errorf("inspect required relation AddField rows: %w", err)
				}
				if populated != 0 {
					return sqliteRelationErrRequiredRows
				}
			}
			virtualModels[tableKey] = change.After.relationBackendClone()
			effectiveSources[tableKey] = sqliteRelationEffectiveSource{
				Table:     change.After.Table,
				Columns:   append([]relationBackendColumn(nil), change.After.Columns...),
				Relations: append([]relationBackendRelation(nil), change.After.Relations...),
			}
		case relationBackendRemoveField:
			tableKey := relationBackendIdentifierKey(change.Before.Table)
			if _, virtual := virtualModels[tableKey]; !virtual {
				if _, checked := physicalShapeChecks[tableKey]; !checked {
					if err := sqliteRelationAssertModelShape(ctx, connection, change.Before); err != nil {
						return err
					}
					physicalShapeChecks[tableKey] = struct{}{}
				} else if err := sqliteRelationEffectiveSourceMatchesModel(effectiveSources[tableKey], change.Before); err != nil {
					return err
				}
			}
			if !createdInIntent[tableKey] {
				if err := sqliteRelationRejectRemakeHazards(
					ctx,
					connection,
					change,
					effectiveSources,
					catalog,
					catalogReferenceChecks,
					remakeStaticChecks,
					remakeSequences,
					sequenceCatalog,
					catalogObjectNames,
				); err != nil {
					return err
				}
			} else {
				temporary := sqliteRelationTemporaryTable(change)
				if _, collision := catalogObjectNames[relationBackendIdentifierKey(temporary)]; collision {
					return fmt.Errorf("%w: %q", sqliteRelationErrTempCollision, temporary)
				}
				remakeSequences[tableKey] = sqliteRelationSequenceSnapshot{}
			}
			virtualModels[tableKey] = change.After.relationBackendClone()
			effectiveSources[tableKey] = sqliteRelationEffectiveSource{
				Table:     change.After.Table,
				Columns:   append([]relationBackendColumn(nil), change.After.Columns...),
				Relations: append([]relationBackendRelation(nil), change.After.Relations...),
			}
		}
		if err := sqliteRelationValidateEffectiveGraph(effectiveSources); err != nil {
			return err
		}
	}
	return nil
}

func sqliteRelationRejectControlInbound(effectiveSources map[string]sqliteRelationEffectiveSource) error {
	controls := map[string]string{
		relationBackendIdentifierKey(sqliteRelationRevisionTable): sqliteRelationRevisionTable,
		relationBackendIdentifierKey(sqliteRelationRecorderTable): sqliteRelationRecorderTable,
	}
	for _, source := range effectiveSources {
		for _, relation := range source.Relations {
			if control, exists := controls[relationBackendIdentifierKey(relation.TargetTable)]; exists {
				return fmt.Errorf(
					"%w: user table %q has inbound foreign key to control table %q",
					sqliteRelationErrControlCatalog,
					source.Table,
					control,
				)
			}
		}
	}
	return nil
}

func sqliteRelationRejectMainCreateNamespace(
	ctx context.Context,
	connection *sql.Conn,
	table string,
) error {
	var objectType, objectName string
	err := connection.QueryRowContext(
		ctx,
		`SELECT "type", "name" FROM "main"."sqlite_schema" `+
			`WHERE "type" IN ('table', 'view', 'index') AND "name" = ? COLLATE NOCASE `+
			`ORDER BY "type", "name" LIMIT 1`,
		table,
	).Scan(&objectType, &objectName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect main SQLite create namespace for %q: %w", table, err)
	}
	return fmt.Errorf(
		"%w: create table %q conflicts with main %s %q",
		sqliteRelationErrDrift,
		table,
		objectType,
		objectName,
	)
}

type sqliteRelationEffectiveSource struct {
	Table     string
	Columns   []relationBackendColumn
	Relations []relationBackendRelation
}

func sqliteRelationValidateEffectiveGraph(effectiveSources map[string]sqliteRelationEffectiveSource) error {
	models := make(map[string]relationBackendModel, len(effectiveSources))
	for key, source := range effectiveSources {
		models[key] = relationBackendModel{
			Table:     source.Table,
			Columns:   append([]relationBackendColumn(nil), source.Columns...),
			Relations: append([]relationBackendRelation(nil), source.Relations...),
		}
	}
	if err := relationBackendValidateEffectiveGraph(models); err != nil {
		return fmt.Errorf("%w: physical relation graph: %w", sqliteRelationErrDrift, err)
	}
	return nil
}

func sqliteRelationEffectiveSourceMatchesModel(
	source sqliteRelationEffectiveSource,
	model relationBackendModel,
) error {
	actual := relationBackendModel{
		Table:     source.Table,
		Columns:   append([]relationBackendColumn(nil), source.Columns...),
		Relations: append([]relationBackendRelation(nil), source.Relations...),
	}
	if !relationBackendModelsEqual(actual, model) {
		return fmt.Errorf("%w: cached physical model %q does not match ordered before-state", sqliteRelationErrDrift, model.Table)
	}
	return nil
}

func sqliteRelationValidatePhysicalGraphWork(
	effectiveSources map[string]sqliteRelationEffectiveSource,
	intent relationBackendStepIntent,
	maximum int,
) error {
	members := make(map[string]int, len(effectiveSources))
	nodes := 0
	for key, source := range effectiveSources {
		count := len(source.Columns) + len(source.Relations)
		if count > maximum-nodes-1 {
			return fmt.Errorf("%w: physical graph node resource limit exceeded", sqliteRelationErrDrift)
		}
		members[key] = count
		nodes += count + 1
	}
	work := nodes
	if work > maximum {
		return fmt.Errorf("%w: physical graph validation work exceeds %d", sqliteRelationErrDrift, maximum)
	}
	for index, change := range intent.Changes {
		table := change.After.Table
		count := len(change.After.Columns) + len(change.After.Relations)
		if change.Kind == relationBackendDeleteModel {
			table = change.Before.Table
			count = 0
		}
		key := relationBackendIdentifierKey(table)
		if prior, exists := members[key]; exists {
			nodes -= prior + 1
		}
		if change.Kind == relationBackendDeleteModel {
			delete(members, key)
		} else {
			if count > maximum-nodes-1 {
				return fmt.Errorf("%w: physical graph node resource limit exceeded at change %d", sqliteRelationErrDrift, index)
			}
			members[key] = count
			nodes += count + 1
		}
		cost := nodes + 1
		if cost > maximum-work {
			return fmt.Errorf(
				"%w: aggregate physical graph validation work exceeds %d at change %d",
				sqliteRelationErrDrift,
				maximum,
				index,
			)
		}
		work += cost
	}
	return nil
}

func sqliteRelationLoadEffectiveSources(
	ctx context.Context,
	connection *sql.Conn,
	catalog sqliteRelationPhysicalCatalog,
) (map[string]sqliteRelationEffectiveSource, error) {
	var tables []sqliteRelationSchemaObject
	tableSQLBytes := 0
	for _, object := range catalog.Objects {
		if object.Schema != "main" || object.Type != "table" {
			continue
		}
		if len(tables) >= migrationdefinition.MaxSources {
			return nil, fmt.Errorf(
				"%w: physical table resource limit exceeded: more than %d",
				sqliteRelationErrDrift,
				migrationdefinition.MaxSources,
			)
		}
		if len(object.SQL) > migrationdefinition.MaxBatchBytes-tableSQLBytes {
			return nil, fmt.Errorf(
				"%w: aggregate physical table SQL resource limit exceeded: more than %d bytes",
				sqliteRelationErrDrift,
				migrationdefinition.MaxBatchBytes,
			)
		}
		tableSQLBytes += len(object.SQL)
		tables = append(tables, object)
	}

	result := make(map[string]sqliteRelationEffectiveSource, len(tables))
	aggregateNodes := len(tables)
	for _, tableObject := range tables {
		table := tableObject.Name
		if strings.HasPrefix(strings.ToLower(table), "sqlite_") {
			continue
		}
		key := relationBackendIdentifierKey(table)
		if key == relationBackendIdentifierKey(sqliteRelationRevisionTable) ||
			key == relationBackendIdentifierKey(sqliteRelationRecorderTable) {
			continue
		}
		physical, err := sqliteRelationReadForeignKeys(ctx, connection, table)
		if err != nil {
			return nil, err
		}
		columns, err := sqliteRelationReadEffectiveColumns(ctx, connection, table, tableObject.SQL)
		if err != nil {
			return nil, err
		}
		if len(physical) > migrationdefinition.MaxJSONValues-aggregateNodes ||
			len(columns) > migrationdefinition.MaxJSONValues-aggregateNodes-len(physical) {
			return nil, fmt.Errorf(
				"%w: aggregate physical schema node resource limit exceeded: more than %d",
				sqliteRelationErrDrift,
				migrationdefinition.MaxJSONValues,
			)
		}
		aggregateNodes += len(physical) + len(columns)
		relations := make([]relationBackendRelation, len(physical))
		for index, foreignKey := range physical {
			relations[index] = relationBackendRelation{
				Column: foreignKey.SourceColumn, TargetTable: foreignKey.TargetTable,
				TargetColumn: foreignKey.TargetColumn,
			}
		}
		result[key] = sqliteRelationEffectiveSource{Table: table, Columns: columns, Relations: relations}
	}
	return result, nil
}

func sqliteRelationReadEffectiveColumns(
	ctx context.Context,
	connection *sql.Conn,
	table,
	tableSQL string,
) ([]relationBackendColumn, error) {
	if len(tableSQL) > migrationdefinition.MaxDocumentBytes {
		return nil, fmt.Errorf(
			"%w: table %q SQL resource limit exceeded: %d > %d",
			sqliteRelationErrDrift,
			table,
			len(tableSQL),
			migrationdefinition.MaxDocumentBytes,
		)
	}
	rows, err := connection.QueryContext(ctx, `PRAGMA main.table_xinfo(`+sqliteRelationQuoteIdentifier(table)+`)`)
	if err != nil {
		return nil, fmt.Errorf("read SQLite relation graph columns for %q: %w", table, err)
	}
	var columns []relationBackendColumn
	for rows.Next() {
		var cid, notNull, primaryKey, hidden int
		var name, dataType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey, &hidden); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan SQLite relation graph columns for %q: %w", table, err)
		}
		if len(columns) >= migrationdefinition.MaxFieldsPerCreateModel {
			_ = rows.Close()
			return nil, fmt.Errorf(
				"%w: table %q column resource limit exceeded: more than %d",
				sqliteRelationErrDrift,
				table,
				migrationdefinition.MaxFieldsPerCreateModel,
			)
		}
		columns = append(columns, relationBackendColumn{
			Name:          name,
			Type:          strings.ToUpper(dataType),
			Nullable:      notNull == 0,
			NotNull:       notNull != 0,
			PrimaryKey:    primaryKey == 1,
			AutoIncrement: primaryKey == 1 && sqliteRelationTargetDefinitionIsExact(tableSQL, name),
			Position:      cid + 1,
		})
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close SQLite relation graph columns for %q: %w", table, err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate SQLite relation graph columns for %q: %w", table, err)
	}
	return columns, nil
}

func sqliteRelationRejectEffectiveInbound(
	effectiveSources map[string]sqliteRelationEffectiveSource,
	target string,
) error {
	targetKey := relationBackendIdentifierKey(target)
	sourceKeys := make([]string, 0, len(effectiveSources))
	for sourceKey := range effectiveSources {
		sourceKeys = append(sourceKeys, sourceKey)
	}
	sort.Strings(sourceKeys)
	for _, sourceKey := range sourceKeys {
		if sourceKey == targetKey {
			continue
		}
		source := effectiveSources[sourceKey]
		for _, relation := range source.Relations {
			if relationBackendIdentifierKey(relation.TargetTable) == targetKey {
				return fmt.Errorf("%w: %q -> %q", sqliteRelationErrInbound, source.Table, target)
			}
		}
	}
	return nil
}

func sqliteRelationRejectTempShadows(
	ctx context.Context,
	connection *sql.Conn,
	intent relationBackendStepIntent,
) error {
	names := map[string]string{
		relationBackendIdentifierKey(sqliteRelationRevisionTable): sqliteRelationRevisionTable,
		relationBackendIdentifierKey(sqliteRelationRecorderTable): sqliteRelationRecorderTable,
	}
	for _, change := range intent.Changes {
		for _, model := range []relationBackendModel{change.Before, change.After} {
			if model.Table != "" {
				names[relationBackendIdentifierKey(model.Table)] = model.Table
			}
			for _, relation := range model.Relations {
				names[relationBackendIdentifierKey(relation.TargetTable)] = relation.TargetTable
			}
		}
		if change.Relation.TargetTable != "" {
			names[relationBackendIdentifierKey(change.Relation.TargetTable)] = change.Relation.TargetTable
		}
	}
	keys := make([]string, 0, len(names))
	for key := range names {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		var objectType, objectName string
		err := connection.QueryRowContext(
			ctx,
			`SELECT "type", "name" FROM "temp"."sqlite_schema" `+
				`WHERE "name" = ? COLLATE NOCASE ORDER BY "type", "name" LIMIT 1`,
			names[key],
		).Scan(&objectType, &objectName)
		if err == nil {
			return fmt.Errorf("%w: temp %s %q collides with %q", sqliteRelationErrTempShadow, objectType, objectName, names[key])
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("inspect SQLite TEMP shadow for %q: %w", names[key], err)
		}
	}
	return nil
}

func sqliteRelationAssertOrderedTarget(
	ctx context.Context,
	connection *sql.Conn,
	virtualModels map[string]relationBackendModel,
	deletedBefore map[string]bool,
	physicalChecks map[string]struct{},
	relation relationBackendRelation,
) error {
	targetKey := relationBackendIdentifierKey(relation.TargetTable)
	if target, planned := virtualModels[targetKey]; planned {
		if sqliteRelationModelHasAutoPrimaryKey(target, relation.TargetColumn) {
			return nil
		}
		return fmt.Errorf(
			"%w: planned target %q.%q is not INTEGER AUTOINCREMENT primary key",
			sqliteRelationErrDrift,
			relation.TargetTable,
			relation.TargetColumn,
		)
	}
	if deletedBefore[targetKey] {
		return fmt.Errorf(
			"%w: target %q was deleted earlier in the ordered intent",
			sqliteRelationErrDrift,
			relation.TargetTable,
		)
	}
	physicalKey := targetKey + "\x00" + relationBackendIdentifierKey(relation.TargetColumn)
	if _, checked := physicalChecks[physicalKey]; checked {
		return nil
	}
	if err := sqliteRelationAssertTargetAutoPrimaryKey(ctx, connection, relation); err != nil {
		return err
	}
	physicalChecks[physicalKey] = struct{}{}
	return nil
}

func sqliteRelationModelHasAutoPrimaryKey(model relationBackendModel, columnName string) bool {
	for _, column := range model.Columns {
		if relationBackendIdentifierKey(column.Name) == relationBackendIdentifierKey(columnName) {
			return column.PrimaryKey && column.Type == "INTEGER" && column.AutoIncrement && column.NotNull
		}
	}
	return false
}

func sqliteRelationAssertTargetAutoPrimaryKey(
	ctx context.Context,
	connection *sql.Conn,
	relation relationBackendRelation,
) error {
	rows, err := connection.QueryContext(
		ctx,
		`PRAGMA main.table_xinfo(`+sqliteRelationQuoteIdentifier(relation.TargetTable)+`)`,
	)
	if err != nil {
		return fmt.Errorf("inspect relation target %q: %w", relation.TargetTable, err)
	}
	defer rows.Close()
	columns := 0
	found := false
	for rows.Next() {
		var cid, notNull, primaryKey, hidden int
		var name, dataType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey, &hidden); err != nil {
			return fmt.Errorf("scan relation target %q: %w", relation.TargetTable, err)
		}
		columns++
		if relationBackendIdentifierKey(name) == relationBackendIdentifierKey(relation.TargetColumn) && strings.EqualFold(dataType, "INTEGER") &&
			primaryKey == 1 && hidden == 0 && !defaultValue.Valid {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate relation target %q: %w", relation.TargetTable, err)
	}
	if columns == 0 || !found {
		return fmt.Errorf(
			"%w: target %q.%q is not a physical INTEGER primary key",
			sqliteRelationErrDrift,
			relation.TargetTable,
			relation.TargetColumn,
		)
	}
	var actualTable, tableSQL string
	if err := connection.QueryRowContext(
		ctx,
		`SELECT "name", "sql" FROM "main"."sqlite_schema" `+
			`WHERE "type" = 'table' AND "name" = ? COLLATE NOCASE`,
		relation.TargetTable,
	).Scan(&actualTable, &tableSQL); err != nil {
		return fmt.Errorf("%w: read target table %q: %v", sqliteRelationErrDrift, relation.TargetTable, err)
	}
	if relationBackendIdentifierKey(actualTable) != relationBackendIdentifierKey(relation.TargetTable) {
		return fmt.Errorf("%w: target table lookup returned %q", sqliteRelationErrDrift, actualTable)
	}
	if !sqliteRelationTargetDefinitionIsExact(tableSQL, relation.TargetColumn) {
		return fmt.Errorf(
			"%w: target %q.%q is not exact INTEGER PRIMARY KEY AUTOINCREMENT",
			sqliteRelationErrDrift,
			relation.TargetTable,
			relation.TargetColumn,
		)
	}
	return nil
}

func sqliteRelationRejectRemakeHazards(
	ctx context.Context,
	connection *sql.Conn,
	change relationBackendChange,
	effectiveSources map[string]sqliteRelationEffectiveSource,
	catalog sqliteRelationPhysicalCatalog,
	catalogReferenceChecks map[string]struct{},
	remakeStaticChecks map[string]struct{},
	remakeSequences map[string]sqliteRelationSequenceSnapshot,
	sequenceCatalog map[string]sqliteRelationSequenceSnapshot,
	catalogObjectNames map[string]struct{},
) error {
	table := change.Before.Table
	tableKey := relationBackendIdentifierKey(table)
	if err := sqliteRelationRejectStaticCatalogReferences(catalog, table, catalogReferenceChecks); err != nil {
		return err
	}
	if _, checked := remakeStaticChecks[tableKey]; !checked {
		rows, err := connection.QueryContext(ctx, `PRAGMA main.index_list(`+sqliteRelationQuoteIdentifier(table)+`)`)
		if err != nil {
			return fmt.Errorf("list SQLite indexes for %q: %w", table, err)
		}
		for rows.Next() {
			var sequence int
			var name string
			var unique int
			var origin string
			var partial int
			if err := rows.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
				_ = rows.Close()
				return fmt.Errorf("scan SQLite index for %q: %w", table, err)
			}
			if origin != "pk" {
				_ = rows.Close()
				return fmt.Errorf("%w: %q", sqliteRelationErrIndex, name)
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return fmt.Errorf("iterate SQLite index list for %q: %w", table, err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close SQLite index list for %q: %w", table, err)
		}
		sequence := sequenceCatalog[tableKey]
		remakeSequences[tableKey] = sequence
		remakeStaticChecks[tableKey] = struct{}{}
	}
	if err := sqliteRelationRejectEffectiveInbound(effectiveSources, table); err != nil {
		return err
	}
	temporary := sqliteRelationTemporaryTable(change)
	if _, collision := catalogObjectNames[relationBackendIdentifierKey(temporary)]; collision {
		return fmt.Errorf("%w: %q", sqliteRelationErrTempCollision, temporary)
	}
	return nil
}

func sqliteRelationRejectStaticCatalogReferences(
	catalog sqliteRelationPhysicalCatalog,
	table string,
	checked map[string]struct{},
) error {
	tableKey := relationBackendIdentifierKey(table)
	if _, exists := checked[tableKey]; exists {
		return nil
	}
	for _, object := range catalog.Objects {
		switch object.Type {
		case "trigger":
			if relationBackendIdentifierKey(object.Owner) == tableKey ||
				sqliteRelationSQLReferencesIdentifier(object.SQL, table) {
				return fmt.Errorf("%w: %q", sqliteRelationErrTrigger, object.Name)
			}
		case "view":
			if sqliteRelationSQLReferencesIdentifier(object.SQL, table) {
				return fmt.Errorf("%w: %q", sqliteRelationErrView, object.Name)
			}
		case "table":
			// A virtual table (and similar SQL-bearing extension object) may
			// depend on another table without exposing a SQLite foreign key.
			// Its own exact source table is safe; any other table definition
			// that token-references the touched table stays outside this bounded
			// feasibility packet and must fail closed before DROP/remake.
			if strings.Contains(strings.ToUpper(object.SQL), "CREATE VIRTUAL TABLE") &&
				relationBackendIdentifierKey(object.Name) != tableKey &&
				sqliteRelationSQLReferencesIdentifier(object.SQL, table) {
				return fmt.Errorf("%w: external table %q references %q", sqliteRelationErrDrift, object.Name, table)
			}
		}
	}
	checked[tableKey] = struct{}{}
	return nil
}

func sqliteRelationLoadSequenceCatalog(
	ctx context.Context,
	connection *sql.Conn,
	limits sqliteRelationPhysicalLimits,
) (map[string]sqliteRelationSequenceSnapshot, error) {
	var sequenceTable int
	if err := connection.QueryRowContext(
		ctx,
		`SELECT EXISTS (`+
			`SELECT 1 FROM "main"."sqlite_schema" `+
			`WHERE "type" = 'table' AND "name" = 'sqlite_sequence' LIMIT 1)`,
	).Scan(&sequenceTable); err != nil {
		return nil, fmt.Errorf("inspect SQLite sequence catalog: %w", err)
	}
	if sequenceTable == 0 {
		return map[string]sqliteRelationSequenceSnapshot{}, nil
	}
	rows, err := connection.QueryContext(
		ctx,
		`SELECT `+
			`typeof("name"), `+
			`COALESCE(length(CAST("name" AS BLOB)), -1), `+
			`substr(CAST("name" AS BLOB), 1, ?), `+
			`typeof("seq"), `+
			`CASE WHEN typeof("seq") = 'integer' THEN "seq" ELSE NULL END `+
			`FROM "main"."sqlite_sequence" LIMIT ?`,
		migrationdefinition.MaxSourceIDBytes+1,
		limits.MaxObjects+1,
	)
	if err != nil {
		return nil, fmt.Errorf("read SQLite sequence catalog: %w", err)
	}
	result := make(map[string]sqliteRelationSequenceSnapshot)
	aggregateNameBytes := 0
	for rows.Next() {
		var nameType string
		var nameBytes int64
		var namePrefix []byte
		var sequenceType string
		var sequence sql.NullInt64
		if err := rows.Scan(&nameType, &nameBytes, &namePrefix, &sequenceType, &sequence); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan SQLite sequence catalog: %w", err)
		}
		if len(result) >= limits.MaxObjects {
			_ = rows.Close()
			return nil, fmt.Errorf(
				"%w: sqlite_sequence row resource limit exceeded: more than %d",
				sqliteRelationErrDrift,
				limits.MaxObjects,
			)
		}
		if nameType != "text" || nameBytes <= 0 || nameBytes > int64(migrationdefinition.MaxSourceIDBytes) ||
			len(namePrefix) != int(nameBytes) || !utf8.Valid(namePrefix) {
			_ = rows.Close()
			return nil, fmt.Errorf(
				"%w: sqlite_sequence identity is outside the closed shape",
				sqliteRelationErrDrift,
			)
		}
		if int(nameBytes) > limits.MaxBatchBytes-aggregateNameBytes {
			_ = rows.Close()
			return nil, fmt.Errorf("%w: sqlite_sequence aggregate identity bytes exceed %d", sqliteRelationErrDrift, limits.MaxBatchBytes)
		}
		if sequenceType != "integer" || !sequence.Valid || sequence.Int64 < 0 {
			_ = rows.Close()
			return nil, fmt.Errorf(
				"%w: sqlite_sequence value for %q is not an integral nonnegative value",
				sqliteRelationErrDrift,
				string(namePrefix),
			)
		}
		name := string(namePrefix)
		key := relationBackendIdentifierKey(name)
		if prior, duplicate := result[key]; duplicate {
			_ = rows.Close()
			return nil, fmt.Errorf(
				"%w: sqlite_sequence has duplicate case-folded rows %q and %q",
				sqliteRelationErrDrift,
				prior.Name,
				name,
			)
		}
		aggregateNameBytes += int(nameBytes)
		result[key] = sqliteRelationSequenceSnapshot{Name: name, Present: true, Value: sequence.Int64}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("iterate SQLite sequence catalog: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close SQLite sequence catalog: %w", err)
	}
	return result, nil
}

func sqliteRelationSQLReferencesIdentifier(statement, identifier string) bool {
	statement = strings.ToLower(statement)
	identifier = strings.ToLower(identifier)
	if statement == "" || identifier == "" {
		return false
	}
	for searchFrom := 0; searchFrom <= len(statement)-len(identifier); {
		relative := strings.Index(statement[searchFrom:], identifier)
		if relative < 0 {
			return false
		}
		start := searchFrom + relative
		end := start + len(identifier)
		beforeBoundary := start == 0 || !sqliteRelationIdentifierByte(statement[start-1])
		afterBoundary := end == len(statement) || !sqliteRelationIdentifierByte(statement[end])
		if beforeBoundary && afterBoundary {
			return true
		}
		searchFrom = start + 1
	}
	return false
}

func sqliteRelationConsumeSchemaObjectBudget(
	name,
	statement string,
	objectCount,
	statementBytes *int,
) error {
	if len(name) > migrationdefinition.MaxSourceIDBytes {
		return fmt.Errorf(
			"%w: schema object name resource limit exceeded: %d > %d",
			sqliteRelationErrDrift,
			len(name),
			migrationdefinition.MaxSourceIDBytes,
		)
	}
	if len(statement) > migrationdefinition.MaxDocumentBytes {
		return fmt.Errorf(
			"%w: schema object SQL resource limit exceeded: %d > %d",
			sqliteRelationErrDrift,
			len(statement),
			migrationdefinition.MaxDocumentBytes,
		)
	}
	if *objectCount >= migrationdefinition.MaxSources {
		return fmt.Errorf(
			"%w: schema object count resource limit exceeded: more than %d",
			sqliteRelationErrDrift,
			migrationdefinition.MaxSources,
		)
	}
	if len(statement) > migrationdefinition.MaxBatchBytes-*statementBytes {
		return fmt.Errorf(
			"%w: aggregate schema object SQL resource limit exceeded: more than %d bytes",
			sqliteRelationErrDrift,
			migrationdefinition.MaxBatchBytes,
		)
	}
	*objectCount++
	*statementBytes += len(statement)
	return nil
}

func sqliteRelationIdentifierByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9' || value == '_'
}

func sqliteRelationQualifiedSchema(schema, object string) string {
	return sqliteRelationQuoteIdentifier(schema) + "." + sqliteRelationQuoteIdentifier(object)
}

type sqliteRelationPhysicalColumn struct {
	Name       string
	Type       string
	NotNull    bool
	PrimaryKey int
	Hidden     int
	Default    sql.NullString
}

type sqliteRelationPhysicalForeignKey struct {
	SourceColumn string
	TargetTable  string
	TargetColumn string
	OnUpdate     string
	OnDelete     string
}

func sqliteRelationAssertModelShape(ctx context.Context, connection *sql.Conn, model relationBackendModel) error {
	var actualTable, tableSQL string
	if err := connection.QueryRowContext(
		ctx,
		`SELECT "name", "sql" FROM "main"."sqlite_schema" `+
			`WHERE "type" = 'table' AND "name" = ? COLLATE NOCASE`,
		model.Table,
	).Scan(&actualTable, &tableSQL); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: table %q is absent", sqliteRelationErrDrift, model.Table)
		}
		return fmt.Errorf("read SQLite table SQL for %q: %w", model.Table, err)
	}
	upperSQL := strings.ToUpper(tableSQL)
	if strings.Contains(upperSQL, "WITHOUT ROWID") || strings.Contains(upperSQL, " STRICT") || strings.Contains(upperSQL, " VIRTUAL TABLE") {
		return fmt.Errorf("%w: unsupported table options for %q", sqliteRelationErrDrift, model.Table)
	}

	rows, err := connection.QueryContext(ctx, `PRAGMA main.table_xinfo(`+sqliteRelationQuoteIdentifier(actualTable)+`)`)
	if err != nil {
		return fmt.Errorf("read SQLite table_xinfo for %q: %w", model.Table, err)
	}
	var physical []sqliteRelationPhysicalColumn
	for rows.Next() {
		var cid int
		var column sqliteRelationPhysicalColumn
		var notNull int
		if err := rows.Scan(&cid, &column.Name, &column.Type, &notNull, &column.Default, &column.PrimaryKey, &column.Hidden); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan SQLite table_xinfo for %q: %w", model.Table, err)
		}
		column.NotNull = notNull != 0
		if column.Hidden != 0 {
			_ = rows.Close()
			return fmt.Errorf("%w: %q.%q hidden=%d", sqliteRelationErrGenerated, model.Table, column.Name, column.Hidden)
		}
		physical = append(physical, column)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close SQLite table_xinfo for %q: %w", model.Table, err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate SQLite table_xinfo for %q: %w", model.Table, err)
	}

	expectedColumns := make([]sqliteRelationPhysicalColumn, 0, len(model.Columns)+len(model.Relations))
	for _, field := range sqliteRelationOrderedFields(model) {
		if field.column != nil {
			column := *field.column
			primaryKey := 0
			if column.PrimaryKey {
				primaryKey = 1
			}
			declaredType := column.Type
			if column.Type == "VARCHAR" {
				declaredType = fmt.Sprintf("VARCHAR(%d)", column.MaxLength)
			}
			expectedColumns = append(expectedColumns, sqliteRelationPhysicalColumn{
				Name: column.Name, Type: declaredType,
				NotNull: column.NotNull, PrimaryKey: primaryKey,
			})
			continue
		}
		relation := *field.relation
		expectedColumns = append(expectedColumns, sqliteRelationPhysicalColumn{
			Name: relation.Column, Type: "INTEGER", NotNull: !relation.Nullable,
		})
	}
	if len(physical) != len(expectedColumns) {
		return fmt.Errorf("%w: table %q has %d columns, want %d", sqliteRelationErrDrift, model.Table, len(physical), len(expectedColumns))
	}
	for index := range physical {
		got, want := physical[index], expectedColumns[index]
		if relationBackendIdentifierKey(got.Name) != relationBackendIdentifierKey(want.Name) || !strings.EqualFold(got.Type, want.Type) ||
			got.NotNull != want.NotNull || got.PrimaryKey != want.PrimaryKey || got.Default.Valid {
			return fmt.Errorf("%w: table %q column %d got=%+v want=%+v", sqliteRelationErrDrift, model.Table, index, got, want)
		}
	}
	if !sqliteRelationCreateSQLIsExact(tableSQL, actualTable, model) {
		return fmt.Errorf("%w: table %q CREATE SQL is outside the closed candidate shapes", sqliteRelationErrDrift, model.Table)
	}

	physicalForeignKeys, err := sqliteRelationReadForeignKeys(ctx, connection, actualTable)
	if err != nil {
		return err
	}
	expectedForeignKeys := make([]sqliteRelationPhysicalForeignKey, len(model.Relations))
	for index, relation := range model.Relations {
		expectedForeignKeys[index] = sqliteRelationPhysicalForeignKey{
			SourceColumn: relation.Column, TargetTable: relation.TargetTable,
			TargetColumn: relation.TargetColumn, OnUpdate: "NO ACTION", OnDelete: "NO ACTION",
		}
	}
	sort.Slice(expectedForeignKeys, func(left, right int) bool {
		return relationBackendIdentifierKey(expectedForeignKeys[left].SourceColumn) <
			relationBackendIdentifierKey(expectedForeignKeys[right].SourceColumn)
	})
	if len(physicalForeignKeys) != len(expectedForeignKeys) {
		return fmt.Errorf("%w: table %q has %d foreign keys, want %d", sqliteRelationErrDrift, model.Table, len(physicalForeignKeys), len(expectedForeignKeys))
	}
	for index := range physicalForeignKeys {
		if !sqliteRelationPhysicalForeignKeysEqual(physicalForeignKeys[index], expectedForeignKeys[index]) {
			return fmt.Errorf("%w: table %q foreign key got=%+v want=%+v", sqliteRelationErrDrift, model.Table, physicalForeignKeys[index], expectedForeignKeys[index])
		}
	}
	return nil
}

func sqliteRelationPhysicalForeignKeysEqual(left, right sqliteRelationPhysicalForeignKey) bool {
	return relationBackendIdentifierKey(left.SourceColumn) == relationBackendIdentifierKey(right.SourceColumn) &&
		relationBackendIdentifierKey(left.TargetTable) == relationBackendIdentifierKey(right.TargetTable) &&
		relationBackendIdentifierKey(left.TargetColumn) == relationBackendIdentifierKey(right.TargetColumn) &&
		left.OnUpdate == right.OnUpdate && left.OnDelete == right.OnDelete
}

// sqliteRelationCreateSQLIsExact recognizes the deliberately closed candidate
// grammar in one pass. Each relation column may carry its FK inline or defer it
// to the ordered table-constraint tail; no relation subset enumeration occurs.
func sqliteRelationCreateSQLIsExact(tableSQL, actualTable string, model relationBackendModel) bool {
	prefixes := []string{
		"CREATE TABLE " + sqliteRelationQuoteIdentifier(actualTable) + " (",
		"CREATE TABLE " + sqliteRelationQualifiedMain(actualTable) + " (",
	}
	body := ""
	for _, prefix := range prefixes {
		if strings.HasPrefix(tableSQL, prefix) && strings.HasSuffix(tableSQL, ")") {
			body = tableSQL[len(prefix) : len(tableSQL)-1]
			break
		}
	}
	if body == "" {
		return false
	}
	definitions := sqliteRelationSplitDefinitions(body)
	fields := sqliteRelationOrderedFields(model)
	if len(definitions) < len(fields) || len(definitions) > len(fields)+len(model.Relations) {
		return false
	}
	pending := make([]relationBackendRelation, 0, len(model.Relations))
	for index, field := range fields {
		got := strings.TrimSpace(definitions[index])
		if field.column != nil {
			if got != sqliteRelationCompileScalarDefinition(*field.column) {
				return false
			}
			continue
		}
		relation := *field.relation
		base := sqliteRelationCompileRelationDefinition(relation)
		inline := base + " REFERENCES " + sqliteRelationQuoteIdentifier(relation.TargetTable) +
			" (" + sqliteRelationQuoteIdentifier(relation.TargetColumn) + ") ON DELETE NO ACTION"
		switch got {
		case inline:
		case base:
			pending = append(pending, relation)
		default:
			return false
		}
	}
	if len(definitions) != len(fields)+len(pending) {
		return false
	}
	for index, relation := range pending {
		if strings.TrimSpace(definitions[len(fields)+index]) != sqliteRelationCompileForeignKeyDefinition(relation) {
			return false
		}
	}
	return true
}

func sqliteRelationCompileClosedCreateTable(
	model relationBackendModel,
	table string,
	inline func(relationBackendRelation) bool,
) string {
	definitions := make([]string, 0, len(model.Columns)+2*len(model.Relations))
	var pending []relationBackendRelation
	for _, field := range sqliteRelationOrderedFields(model) {
		if field.column != nil {
			definitions = append(definitions, sqliteRelationCompileScalarDefinition(*field.column))
			continue
		}
		relation := *field.relation
		definition := sqliteRelationCompileRelationDefinition(relation)
		if inline(relation) {
			definition += " REFERENCES " + sqliteRelationQuoteIdentifier(relation.TargetTable) +
				" (" + sqliteRelationQuoteIdentifier(relation.TargetColumn) + ") ON DELETE NO ACTION"
		} else {
			pending = append(pending, relation)
		}
		definitions = append(definitions, definition)
	}
	for _, relation := range pending {
		definitions = append(definitions, sqliteRelationCompileForeignKeyDefinition(relation))
	}
	return "CREATE TABLE " + sqliteRelationQuoteIdentifier(table) + " (" + strings.Join(definitions, ", ") + ")"
}

func sqliteRelationTargetDefinitionIsExact(tableSQL, targetColumn string) bool {
	upperSQL := strings.ToUpper(tableSQL)
	for _, forbidden := range []string{"--", "/*", "*/", " CHECK", " COLLATE", " DEFERRABLE"} {
		if strings.Contains(upperSQL, forbidden) {
			return false
		}
	}
	open := strings.Index(tableSQL, "(")
	close := strings.LastIndex(tableSQL, ")")
	if open < 0 || close <= open {
		return false
	}
	const suffix = " INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT"
	for _, definition := range sqliteRelationSplitDefinitions(tableSQL[open+1 : close]) {
		definition = strings.TrimSpace(definition)
		if !strings.HasSuffix(definition, suffix) {
			continue
		}
		identifier, ok := sqliteRelationUnquoteIdentifier(strings.TrimSuffix(definition, suffix))
		if ok && relationBackendIdentifierKey(identifier) == relationBackendIdentifierKey(targetColumn) {
			return true
		}
	}
	return false
}

func sqliteRelationSplitDefinitions(body string) []string {
	var definitions []string
	start := 0
	depth := 0
	inQuote := false
	for index := 0; index < len(body); index++ {
		switch body[index] {
		case '"':
			if inQuote && index+1 < len(body) && body[index+1] == '"' {
				index++
				continue
			}
			inQuote = !inQuote
		case '(':
			if !inQuote {
				depth++
			}
		case ')':
			if !inQuote {
				depth--
				if depth < 0 {
					return nil
				}
			}
		case ',':
			if !inQuote && depth == 0 {
				definitions = append(definitions, body[start:index])
				start = index + 1
			}
		}
	}
	if inQuote || depth != 0 {
		return nil
	}
	definitions = append(definitions, body[start:])
	return definitions
}

func sqliteRelationUnquoteIdentifier(value string) (string, bool) {
	if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
		return "", false
	}
	body := value[1 : len(value)-1]
	for index := 0; index < len(body); index++ {
		if body[index] != '"' {
			continue
		}
		if index+1 >= len(body) || body[index+1] != '"' {
			return "", false
		}
		index++
	}
	return strings.ReplaceAll(body, `""`, `"`), true
}

func sqliteRelationReadForeignKeys(
	ctx context.Context,
	connection *sql.Conn,
	table string,
) ([]sqliteRelationPhysicalForeignKey, error) {
	rows, err := connection.QueryContext(ctx, `PRAGMA main.foreign_key_list(`+sqliteRelationQuoteIdentifier(table)+`)`)
	if err != nil {
		return nil, fmt.Errorf("read SQLite foreign_key_list for %q: %w", table, err)
	}
	defer rows.Close()
	var result []sqliteRelationPhysicalForeignKey
	for rows.Next() {
		var id, sequence int
		var foreignKey sqliteRelationPhysicalForeignKey
		var match string
		if err := rows.Scan(
			&id, &sequence, &foreignKey.TargetTable, &foreignKey.SourceColumn,
			&foreignKey.TargetColumn, &foreignKey.OnUpdate, &foreignKey.OnDelete, &match,
		); err != nil {
			return nil, fmt.Errorf("scan SQLite foreign_key_list for %q: %w", table, err)
		}
		if len(result) >= migrationdefinition.MaxFieldsPerCreateModel {
			return nil, fmt.Errorf(
				"%w: table %q foreign-key resource limit exceeded: more than %d",
				sqliteRelationErrDrift,
				table,
				migrationdefinition.MaxFieldsPerCreateModel,
			)
		}
		result = append(result, foreignKey)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate SQLite foreign_key_list for %q: %w", table, err)
	}
	sort.Slice(result, func(left, right int) bool {
		return relationBackendIdentifierKey(result[left].SourceColumn) <
			relationBackendIdentifierKey(result[right].SourceColumn)
	})
	return result, nil
}

func sqliteRelationTableExists(ctx context.Context, connection *sql.Conn, table string) (bool, error) {
	var count int
	if err := connection.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM "main"."sqlite_schema" `+
			`WHERE "type" = 'table' AND "name" = ? COLLATE NOCASE`,
		table,
	).Scan(&count); err != nil {
		return false, fmt.Errorf("inspect SQLite table %q: %w", table, err)
	}
	return count == 1, nil
}

func sqliteRelationModelColumnNames(model relationBackendModel) []string {
	columns := make([]string, 0, len(model.Columns)+len(model.Relations))
	for _, field := range sqliteRelationOrderedFields(model) {
		if field.column != nil {
			columns = append(columns, field.column.Name)
		} else {
			columns = append(columns, field.relation.Column)
		}
	}
	return columns
}

func sqliteRelationPrimaryKey(model relationBackendModel) string {
	for _, column := range model.Columns {
		if column.PrimaryKey {
			return column.Name
		}
	}
	return ""
}
