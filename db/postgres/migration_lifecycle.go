package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

const postgresMigrationCleanupTimeout = 5 * time.Second

var _ migrationbackend.RevisionFencedBackend = (*Backend)(nil)
var _ migrationbackend.RevisionFencedSession = (*postgresRevisionFencedSession)(nil)
var _ migrationbackend.RevisionFencedTransaction = (*postgresRevisionFencedTransaction)(nil)

type postgresMigrationPinnedConnection interface {
	migrationSQLExecutor
	Raw(func(any) error) error
	Close() error
}

var _ postgresMigrationPinnedConnection = (*sql.Conn)(nil)

type postgresRevisionSessionState uint8

const (
	postgresRevisionSessionOpen postgresRevisionSessionState = iota + 1
	postgresRevisionSessionReady
	postgresRevisionSessionActive
	postgresRevisionSessionPoisoned
	postgresRevisionSessionClosing
	postgresRevisionSessionClosed
)

type postgresRevisionFencedSession struct {
	mu        sync.Mutex
	backend   *Backend
	state     postgresRevisionSessionState
	records   []migrationbackend.AppliedMigration
	token     postgresMigrationRevisionToken
	active    *postgresRevisionFencedTransaction
	closeDone chan struct{}
	closeErr  error
}

type postgresRevisionFencedTransaction struct {
	mu               sync.Mutex
	connection       postgresMigrationPinnedConnection
	session          *postgresRevisionFencedSession
	schema           *postgresMigrationSchema
	transition       migrationbackend.HistoryTransition
	expectedRecords  []migrationbackend.AppliedMigration
	successorRecords []migrationbackend.AppliedMigration
	expectedToken    postgresMigrationRevisionToken
	successorToken   postgresMigrationRevisionToken
	bootstrap        bool
	recorderCalled   bool
	failure          error
	done             bool
}

// MigrationCapabilities advertises the bounded PostgreSQL 17 schema editor
// implemented by postgresMigrationSchema. Every operation remains subject to
// its sealed intent, catalog preflight, and exact post-operation verification.
func (*Backend) MigrationCapabilities() migrationbackend.MigrationCapabilities {
	return migrationbackend.MigrationCapabilities{
		CreateModelForeignKeys:            true,
		AddNullableForeignKey:             true,
		AddRequiredForeignKeyToEmptyTable: true,
		RemoveForeignKey:                  true,
	}
}

func (b *Backend) OpenRevisionFencedSession(ctx context.Context) (migrationbackend.RevisionFencedSession, error) {
	if err := b.validateContext(ctx); err != nil {
		return nil, err
	}
	return &postgresRevisionFencedSession{
		backend: b,
		state:   postgresRevisionSessionOpen,
	}, nil
}

func (session *postgresRevisionFencedSession) ReadAppliedMigrations(
	ctx context.Context,
) ([]migrationbackend.AppliedMigration, error) {
	if session == nil {
		return nil, errors.New("read PostgreSQL revision-fenced history: session is nil")
	}
	if ctx == nil {
		return nil, errors.New("read PostgreSQL revision-fenced history: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("read PostgreSQL revision-fenced history: %w", err)
	}

	session.mu.Lock()
	defer session.mu.Unlock()
	if session.state != postgresRevisionSessionOpen {
		return nil, fmt.Errorf(
			"read PostgreSQL revision-fenced history: session state %d does not permit a snapshot",
			session.state,
		)
	}
	snapshot, err := readAtomicPostgresMigrationSnapshot(ctx, session.backend)
	if err != nil {
		session.state = postgresRevisionSessionPoisoned
		return nil, err
	}
	if !snapshot.revisionPresent && snapshot.recorderPresent {
		session.state = postgresRevisionSessionPoisoned
		return nil, newPostgresRevisionFenceError(
			migrationbackend.RevisionFenceFailureAdoptionRequired,
			errors.New("PostgreSQL migration recorder exists without revision metadata; exclusive adoption is required"),
		)
	}
	session.records = clonePostgresAppliedMigrations(snapshot.records)
	session.token = snapshot.token
	session.state = postgresRevisionSessionReady
	return clonePostgresAppliedMigrations(snapshot.records), nil
}

func (session *postgresRevisionFencedSession) BeginMigration(
	ctx context.Context,
	transition migrationbackend.HistoryTransition,
	intent migrationbackend.MigrationIntent,
) (migrationbackend.RevisionFencedTransaction, error) {
	if session == nil {
		return nil, errors.New("begin PostgreSQL revision-fenced migration: session is nil")
	}
	if ctx == nil {
		return nil, errors.New("begin PostgreSQL revision-fenced migration: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("begin PostgreSQL revision-fenced migration: %w", err)
	}

	// The schema boundary clones and seals the complete intent before any
	// session state or database I/O is touched.
	schema, err := newPostgresMigrationSchema(transition, intent)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("begin PostgreSQL revision-fenced migration: %w", err)
	}

	session.mu.Lock()
	defer session.mu.Unlock()
	if session.state != postgresRevisionSessionReady {
		return nil, fmt.Errorf(
			"begin PostgreSQL revision-fenced migration: session state %d is not ready",
			session.state,
		)
	}

	successorRecords, err := postgresMigrationHistorySuccessor(session.records, transition)
	if err != nil {
		session.state = postgresRevisionSessionPoisoned
		return nil, err
	}
	successorToken := session.token
	successorToken.initialized = true
	successorToken.fingerprint = fingerprintPostgresMigrationHistory(successorRecords)
	if session.token.initialized {
		if session.token.revision == math.MaxInt64 {
			session.state = postgresRevisionSessionPoisoned
			return nil, postgresRevisionIntegrity("PostgreSQL migration revision is exhausted", nil)
		}
		successorToken.revision = session.token.revision + 1
	} else {
		if transition.Kind != migrationbackend.HistoryTransitionApply {
			session.state = postgresRevisionSessionPoisoned
			return nil, postgresRevisionIntegrity("an uninitialized PostgreSQL history cannot begin with unapply", nil)
		}
		if _, err := rand.Read(successorToken.epoch[:]); err != nil {
			session.state = postgresRevisionSessionPoisoned
			return nil, fmt.Errorf("generate PostgreSQL migration revision epoch: %w", err)
		}
		successorToken.revision = 1
	}

	connection, err := session.backend.database.Conn(ctx)
	if err != nil {
		session.state = postgresRevisionSessionPoisoned
		return nil, classifyPostgresMigrationIO(ctx, "acquire pinned PostgreSQL migration connection", err)
	}
	if _, err := connection.ExecContext(
		ctx,
		"BEGIN ISOLATION LEVEL READ COMMITTED READ WRITE NOT DEFERRABLE",
	); err != nil {
		session.state = postgresRevisionSessionPoisoned
		return nil, errors.Join(
			classifyPostgresMigrationIO(ctx, "begin PostgreSQL revision-fenced transaction", err),
			discardPostgresMigrationConnection(connection),
		)
	}
	transaction := &postgresRevisionFencedTransaction{
		connection:       connection,
		session:          session,
		schema:           schema,
		transition:       transition,
		expectedRecords:  clonePostgresAppliedMigrations(session.records),
		successorRecords: clonePostgresAppliedMigrations(successorRecords),
		expectedToken:    session.token,
		successorToken:   successorToken,
		bootstrap:        !session.token.initialized,
	}

	failBegin := func(primary error) (migrationbackend.RevisionFencedTransaction, error) {
		cleanupErr := transaction.rollbackWithoutSession(ctx)
		session.state = postgresRevisionSessionPoisoned
		return nil, errors.Join(primary, cleanupErr)
	}
	var locked bool
	if err := connection.QueryRowContext(
		ctx,
		`SELECT "pg_catalog"."pg_try_advisory_xact_lock"($1)`,
		postgresMigrationAdvisoryLockKey(session.backend.schema),
	).Scan(&locked); err != nil {
		return failBegin(classifyPostgresRevisionContention(ctx, "acquire PostgreSQL migration revision fence", err))
	}
	if !locked {
		return failBegin(newPostgresRevisionFenceError(
			migrationbackend.RevisionFenceFailureContended,
			errors.New("PostgreSQL migration revision fence is held by another transaction"),
		))
	}

	current, err := inspectPostgresMigrationSnapshot(ctx, connection, session.backend.schema)
	if err != nil {
		return failBegin(classifyPostgresRevisionContention(ctx, "read PostgreSQL migration revision fence", err))
	}
	if err := transaction.verifyExpectedSnapshot(current); err != nil {
		return failBegin(err)
	}
	if err := schema.Preflight(ctx, connection, session.backend.schema); err != nil {
		return failBegin(classifyPostgresRevisionContention(ctx, "preflight PostgreSQL migration schema", err))
	}
	if err := transaction.claimRevision(ctx); err != nil {
		return failBegin(classifyPostgresRevisionContention(ctx, "claim PostgreSQL migration revision fence", err))
	}
	if err := ctx.Err(); err != nil {
		return failBegin(fmt.Errorf("begin PostgreSQL revision-fenced migration: %w", err))
	}

	session.active = transaction
	session.state = postgresRevisionSessionActive
	return transaction, nil
}

func (session *postgresRevisionFencedSession) Close(ctx context.Context) error {
	if session == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("close PostgreSQL revision-fenced migration session: context is nil")
	}

	session.mu.Lock()
	if session.state == postgresRevisionSessionClosed {
		err := session.closeErr
		session.mu.Unlock()
		return err
	}
	if session.state == postgresRevisionSessionClosing {
		done := session.closeDone
		session.mu.Unlock()
		select {
		case <-done:
		case <-ctx.Done():
			return fmt.Errorf(
				"close PostgreSQL revision-fenced migration session while another close is in progress: %w",
				ctx.Err(),
			)
		}
		session.mu.Lock()
		err := session.closeErr
		session.mu.Unlock()
		return err
	}
	active := session.active
	session.active = nil
	session.state = postgresRevisionSessionClosing
	session.closeDone = make(chan struct{})
	done := session.closeDone
	session.mu.Unlock()

	var cleanupErr error
	if active != nil {
		cleanupErr = active.rollbackWithoutSession(ctx)
	}
	if cleanupErr != nil {
		cleanupErr = fmt.Errorf("close PostgreSQL revision-fenced migration session: %w", cleanupErr)
	}

	session.mu.Lock()
	session.closeErr = cleanupErr
	session.state = postgresRevisionSessionClosed
	close(done)
	session.mu.Unlock()
	return cleanupErr
}

func (session *postgresRevisionFencedSession) finishTransaction(
	transaction *postgresRevisionFencedTransaction,
	committed bool,
	successorToken postgresMigrationRevisionToken,
	successorRecords []migrationbackend.AppliedMigration,
	poison bool,
) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.active != transaction {
		return
	}
	session.active = nil
	if committed {
		session.token = successorToken
		session.records = clonePostgresAppliedMigrations(successorRecords)
	}
	if poison {
		session.state = postgresRevisionSessionPoisoned
	} else {
		session.state = postgresRevisionSessionReady
	}
}

func (transaction *postgresRevisionFencedTransaction) CreateModel(ctx context.Context, model ir.Model) error {
	return transaction.executeSchema(ctx, "create PostgreSQL migration model", func(executor migrationSQLExecutor) error {
		return transaction.schema.CreateModel(ctx, executor, model)
	})
}

func (transaction *postgresRevisionFencedTransaction) DeleteModel(ctx context.Context, model ir.Model) error {
	return transaction.executeSchema(ctx, "delete PostgreSQL migration model", func(executor migrationSQLExecutor) error {
		return transaction.schema.DeleteModel(ctx, executor, model)
	})
}

func (transaction *postgresRevisionFencedTransaction) AddField(
	ctx context.Context,
	model ir.Model,
	field ir.Field,
) error {
	return transaction.executeSchema(ctx, "add PostgreSQL migration field", func(executor migrationSQLExecutor) error {
		return transaction.schema.AddField(ctx, executor, model, field)
	})
}

func (transaction *postgresRevisionFencedTransaction) RemoveField(
	ctx context.Context,
	model ir.Model,
	field ir.Field,
) error {
	return transaction.executeSchema(ctx, "remove PostgreSQL migration field", func(executor migrationSQLExecutor) error {
		return transaction.schema.RemoveField(ctx, executor, model, field)
	})
}

func (transaction *postgresRevisionFencedTransaction) RecordApplied(ctx context.Context, app, name string) error {
	return transaction.record(ctx, migrationbackend.HistoryTransitionApply, app, name)
}

func (transaction *postgresRevisionFencedTransaction) RecordUnapplied(ctx context.Context, app, name string) error {
	return transaction.record(ctx, migrationbackend.HistoryTransitionUnapply, app, name)
}

func (transaction *postgresRevisionFencedTransaction) record(
	ctx context.Context,
	kind migrationbackend.HistoryTransitionKind,
	app,
	name string,
) error {
	return transaction.execute(ctx, "record PostgreSQL migration history", func(executor migrationSQLExecutor) error {
		if err := validatePostgresMigrationRecorderIdentity(migrationbackend.AppliedMigration{App: app, Name: name}); err != nil {
			return err
		}
		if transaction.recorderCalled {
			return postgresRevisionIntegrity("PostgreSQL fenced recorder transition was called more than once", nil)
		}
		if kind != transaction.transition.Kind ||
			app != transaction.transition.Migration.App ||
			name != transaction.transition.Migration.Name {
			return postgresRevisionIntegrity(
				fmt.Sprintf("PostgreSQL recorder transition %d %s.%s does not match declared transition", kind, app, name),
				nil,
			)
		}
		if err := transaction.schema.VerifyComplete(ctx, executor); err != nil {
			return err
		}
		table, err := quoteTable(transaction.session.backend.schema, postgresMigrationRecorderTable)
		if err != nil {
			return err
		}
		if kind == migrationbackend.HistoryTransitionApply {
			result, err := executor.ExecContext(
				ctx,
				`INSERT INTO `+table+` ("app", "name") VALUES ($1, $2)`,
				app,
				name,
			)
			if err != nil {
				return classifyPostgresMigrationIO(ctx, "record applied PostgreSQL migration", err)
			}
			rowsAffected, err := result.RowsAffected()
			if err != nil {
				return classifyPostgresMigrationIO(ctx, "count applied PostgreSQL migration record", err)
			}
			if rowsAffected != 1 {
				return postgresRevisionIntegrity(
					fmt.Sprintf("record applied PostgreSQL migration changed %d rows, want 1", rowsAffected),
					nil,
				)
			}
		} else {
			result, err := executor.ExecContext(
				ctx,
				`DELETE FROM `+table+` WHERE "app" = $1 AND "name" = $2`,
				app,
				name,
			)
			if err != nil {
				return classifyPostgresMigrationIO(ctx, "record unapplied PostgreSQL migration", err)
			}
			rowsAffected, err := result.RowsAffected()
			if err != nil {
				return classifyPostgresMigrationIO(ctx, "count unapplied PostgreSQL migration record", err)
			}
			if rowsAffected != 1 {
				return postgresRevisionIntegrity(
					fmt.Sprintf("record unapplied PostgreSQL migration changed %d rows, want 1", rowsAffected),
					nil,
				)
			}
		}
		transaction.recorderCalled = true
		return nil
	})
}

func (transaction *postgresRevisionFencedTransaction) CommitFenced(
	ctx context.Context,
) (migrationbackend.CommitOutcome, error) {
	unknown := migrationbackend.CommitOutcome{Durability: migrationbackend.CommitUnknown}
	if transaction == nil {
		return unknown, errors.New("commit PostgreSQL fenced migration: transaction is nil")
	}
	if ctx == nil {
		return unknown, errors.New("commit PostgreSQL fenced migration: context is nil")
	}

	transaction.mu.Lock()
	if transaction.done || transaction.connection == nil {
		transaction.mu.Unlock()
		return unknown, errors.New("commit PostgreSQL fenced migration: transaction is already complete")
	}
	if err := ctx.Err(); err != nil {
		primary := fmt.Errorf("commit PostgreSQL fenced migration: %w", err)
		rolledBack, cleanupErr := transaction.rollbackLocked(ctx)
		transaction.mu.Unlock()
		transaction.session.finishTransaction(transaction, false, postgresMigrationRevisionToken{}, nil, true)
		if rolledBack {
			return migrationbackend.CommitOutcome{Durability: migrationbackend.CommitRolledBack}, errors.Join(primary, cleanupErr)
		}
		return unknown, errors.Join(primary, cleanupErr)
	}
	if transaction.failure != nil {
		primary := transaction.failure
		rolledBack, cleanupErr := transaction.rollbackLocked(ctx)
		transaction.mu.Unlock()
		transaction.session.finishTransaction(transaction, false, postgresMigrationRevisionToken{}, nil, true)
		if rolledBack {
			return migrationbackend.CommitOutcome{Durability: migrationbackend.CommitRolledBack}, errors.Join(primary, cleanupErr)
		}
		return unknown, errors.Join(primary, cleanupErr)
	}
	if !transaction.recorderCalled {
		primary := postgresRevisionIntegrity(
			"PostgreSQL fenced migration cannot commit without its declared recorder transition",
			nil,
		)
		rolledBack, cleanupErr := transaction.rollbackLocked(ctx)
		transaction.mu.Unlock()
		transaction.session.finishTransaction(transaction, false, postgresMigrationRevisionToken{}, nil, true)
		if rolledBack {
			return migrationbackend.CommitOutcome{Durability: migrationbackend.CommitRolledBack}, errors.Join(primary, cleanupErr)
		}
		return unknown, errors.Join(primary, cleanupErr)
	}
	if err := transaction.verifySuccessor(ctx); err != nil {
		err = classifyPostgresRevisionContention(ctx, "verify PostgreSQL migration successor", err)
		rolledBack, cleanupErr := transaction.rollbackLocked(ctx)
		transaction.mu.Unlock()
		transaction.session.finishTransaction(transaction, false, postgresMigrationRevisionToken{}, nil, true)
		if rolledBack {
			return migrationbackend.CommitOutcome{Durability: migrationbackend.CommitRolledBack}, errors.Join(err, cleanupErr)
		}
		return unknown, errors.Join(err, cleanupErr)
	}

	outcome, commitErr := transaction.commitVerifiedLocked(ctx)
	transaction.mu.Unlock()
	if outcome.Durability == migrationbackend.CommitCommitted {
		transaction.session.finishTransaction(
			transaction,
			true,
			transaction.successorToken,
			transaction.successorRecords,
			commitErr != nil,
		)
		return outcome, commitErr
	}
	transaction.session.finishTransaction(transaction, false, postgresMigrationRevisionToken{}, nil, true)
	return outcome, commitErr
}

// commitVerifiedLocked performs only the terminal COMMIT attempt and pinned
// connection release. The caller must have already verified the schema,
// recorder, and successor revision and must hold transaction.mu.
func (transaction *postgresRevisionFencedTransaction) commitVerifiedLocked(
	ctx context.Context,
) (migrationbackend.CommitOutcome, error) {
	_, commitErr := transaction.connection.ExecContext(ctx, "COMMIT")
	transaction.done = true
	if commitErr != nil {
		// PostgreSQL can lose the connection after the server has accepted the
		// commit. Never issue ROLLBACK or retry after any literal COMMIT error.
		discardErr := discardPostgresMigrationConnection(transaction.connection)
		transaction.connection = nil
		return migrationbackend.CommitOutcome{Durability: migrationbackend.CommitUnknown}, errors.Join(
			fmt.Errorf("commit PostgreSQL fenced migration transaction: %w", commitErr),
			discardErr,
		)
	}
	closeErr := closeOrDiscardPostgresMigrationConnection(transaction.connection)
	transaction.connection = nil
	return migrationbackend.CommitOutcome{Durability: migrationbackend.CommitCommitted}, closeErr
}

func (transaction *postgresRevisionFencedTransaction) Rollback(ctx context.Context) error {
	if transaction == nil {
		return errors.New("rollback PostgreSQL fenced migration: transaction is nil")
	}
	if ctx == nil {
		return errors.New("rollback PostgreSQL fenced migration: context is nil")
	}
	transaction.mu.Lock()
	if transaction.done || transaction.connection == nil {
		transaction.mu.Unlock()
		return nil
	}
	_, err := transaction.rollbackLocked(ctx)
	transaction.mu.Unlock()
	transaction.session.finishTransaction(transaction, false, postgresMigrationRevisionToken{}, nil, true)
	return err
}

func (transaction *postgresRevisionFencedTransaction) executeSchema(
	ctx context.Context,
	stage string,
	operation func(migrationSQLExecutor) error,
) error {
	return transaction.execute(ctx, stage, func(executor migrationSQLExecutor) error {
		if transaction.recorderCalled {
			return postgresRevisionIntegrity("PostgreSQL schema operation was called after the recorder transition", nil)
		}
		return operation(executor)
	})
}

func (transaction *postgresRevisionFencedTransaction) execute(
	ctx context.Context,
	stage string,
	operation func(migrationSQLExecutor) error,
) error {
	if transaction == nil {
		return errors.New("execute PostgreSQL fenced migration: transaction is nil")
	}
	if ctx == nil {
		return errors.New("execute PostgreSQL fenced migration: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("execute PostgreSQL fenced migration: %w", err)
	}
	transaction.mu.Lock()
	defer transaction.mu.Unlock()
	if transaction.done || transaction.connection == nil {
		return errors.New("execute PostgreSQL fenced migration: transaction is already complete")
	}
	if transaction.failure != nil {
		return transaction.failure
	}
	err := classifyPostgresRevisionContention(ctx, stage, operation(transaction.connection))
	if err != nil {
		transaction.failure = err
	}
	return err
}

func (transaction *postgresRevisionFencedTransaction) verifyExpectedSnapshot(
	current postgresMigrationRevisionSnapshot,
) error {
	if !transaction.expectedToken.initialized {
		if current.revisionPresent || current.recorderPresent {
			return newPostgresRevisionFenceError(
				migrationbackend.RevisionFenceFailureStale,
				errors.New("fresh PostgreSQL migration history changed before bootstrap"),
			)
		}
		return nil
	}
	if !current.revisionPresent {
		return newPostgresRevisionFenceError(
			migrationbackend.RevisionFenceFailureStale,
			errors.New("PostgreSQL migration revision metadata disappeared after snapshot"),
		)
	}
	if !current.recorderPresent {
		return postgresRevisionIntegrity("PostgreSQL migration revision metadata exists without a recorder", nil)
	}
	if !equalPostgresMigrationRevisionToken(current.token, transaction.expectedToken) ||
		!equalPostgresAppliedMigrations(current.records, transaction.expectedRecords) {
		return newPostgresRevisionFenceError(
			migrationbackend.RevisionFenceFailureStale,
			errors.New("PostgreSQL migration history revision is stale"),
		)
	}
	return nil
}

func (transaction *postgresRevisionFencedTransaction) claimRevision(ctx context.Context) error {
	if transaction.bootstrap {
		return transaction.bootstrapControlTables(ctx)
	}
	table, err := quoteTable(transaction.session.backend.schema, postgresMigrationRevisionTable)
	if err != nil {
		return err
	}
	result, err := transaction.connection.ExecContext(
		ctx,
		`UPDATE `+table+` SET "revision" = $1, "history_fingerprint" = $2 `+
			`WHERE "singleton" = $3 AND "format_version" = $4 AND "epoch" = $5 `+
			`AND "revision" = $6 AND "history_fingerprint" = $7`,
		transaction.successorToken.revision,
		transaction.successorToken.fingerprint[:],
		int16(1),
		postgresMigrationRevisionFormat,
		transaction.expectedToken.epoch[:],
		transaction.expectedToken.revision,
		transaction.expectedToken.fingerprint[:],
	)
	if err != nil {
		return classifyPostgresRevisionContention(ctx, "claim PostgreSQL migration history revision", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return classifyPostgresMigrationIO(ctx, "count PostgreSQL migration history revision claim", err)
	}
	if rowsAffected != 1 {
		return newPostgresRevisionFenceError(
			migrationbackend.RevisionFenceFailureStale,
			fmt.Errorf("PostgreSQL migration history revision claim changed %d rows, want 1", rowsAffected),
		)
	}
	return nil
}

func (transaction *postgresRevisionFencedTransaction) bootstrapControlTables(ctx context.Context) error {
	namespace := transaction.session.backend.schema
	recorder, err := quoteTable(namespace, postgresMigrationRecorderTable)
	if err != nil {
		return err
	}
	revision, err := quoteTable(namespace, postgresMigrationRevisionTable)
	if err != nil {
		return err
	}
	recorderPrimaryKey, err := quoteIdentifier(postgresMigrationRecorderPrimaryKey)
	if err != nil {
		return err
	}
	revisionPrimaryKey, err := quoteIdentifier(postgresMigrationRevisionPrimaryKey)
	if err != nil {
		return err
	}
	if _, err := transaction.connection.ExecContext(
		ctx,
		`CREATE TABLE `+recorder+` (`+
			`"app" VARCHAR(255) NOT NULL, `+
			`"name" VARCHAR(255) NOT NULL, `+
			`CONSTRAINT `+recorderPrimaryKey+` PRIMARY KEY ("app", "name"))`,
	); err != nil {
		return classifyPostgresRevisionContention(ctx, "create PostgreSQL migration recorder", err)
	}
	if _, err := transaction.connection.ExecContext(
		ctx,
		`CREATE TABLE `+revision+` (`+
			`"singleton" SMALLINT NOT NULL, `+
			`"format_version" INTEGER NOT NULL, `+
			`"epoch" BYTEA NOT NULL, `+
			`"revision" BIGINT NOT NULL, `+
			`"history_fingerprint" BYTEA NOT NULL, `+
			`CONSTRAINT `+revisionPrimaryKey+` PRIMARY KEY ("singleton"))`,
	); err != nil {
		return classifyPostgresRevisionContention(ctx, "create PostgreSQL migration revision metadata", err)
	}
	if _, err := transaction.connection.ExecContext(
		ctx,
		`INSERT INTO `+revision+` (`+
			`"singleton", "format_version", "epoch", "revision", "history_fingerprint") `+
			`VALUES ($1, $2, $3, $4, $5)`,
		int16(1),
		postgresMigrationRevisionFormat,
		transaction.successorToken.epoch[:],
		transaction.successorToken.revision,
		transaction.successorToken.fingerprint[:],
	); err != nil {
		return classifyPostgresMigrationIO(ctx, "initialize PostgreSQL migration revision metadata", err)
	}
	return nil
}

func (transaction *postgresRevisionFencedTransaction) verifySuccessor(ctx context.Context) error {
	current, err := inspectPostgresMigrationSnapshot(
		ctx,
		transaction.connection,
		transaction.session.backend.schema,
	)
	if err != nil {
		return err
	}
	if !current.revisionPresent || !current.recorderPresent ||
		!equalPostgresMigrationRevisionToken(current.token, transaction.successorToken) ||
		!equalPostgresAppliedMigrations(current.records, transaction.successorRecords) {
		return postgresRevisionIntegrity(
			"PostgreSQL fenced migration durable successor does not match its declared history transition",
			nil,
		)
	}
	return nil
}

// rollbackLocked returns whether rollback was proven. It must be called with
// transaction.mu held. The detached timeout is awaited synchronously before
// the same pinned connection is released or discarded, so cleanup never races
// a goroutine against Conn.Raw/Close. A failed rollback leaves durability
// unknown to CommitFenced.
func (transaction *postgresRevisionFencedTransaction) rollbackLocked(ctx context.Context) (bool, error) {
	if transaction.done || transaction.connection == nil {
		return true, nil
	}
	connection := transaction.connection
	transaction.connection = nil
	transaction.done = true
	return rollbackAndReleasePostgresMigrationConnection(ctx, connection)
}

func rollbackAndReleasePostgresMigrationConnection(
	ctx context.Context,
	connection postgresMigrationPinnedConnection,
) (bool, error) {
	if connection == nil {
		return true, nil
	}
	cleanupCtx, cancel := postgresMigrationDetachedCleanupContext(ctx)
	_, rollbackErr := connection.ExecContext(cleanupCtx, "ROLLBACK")
	if rollbackErr == nil {
		cancel()
		closeErr := closeOrDiscardPostgresMigrationConnection(connection)
		return true, closeErr
	}
	classifiedRollbackErr := classifyPostgresMigrationIO(
		cleanupCtx,
		"rollback PostgreSQL fenced migration transaction",
		rollbackErr,
	)
	cancel()
	discardErr := discardPostgresMigrationConnection(connection)
	return false, errors.Join(
		classifiedRollbackErr,
		discardErr,
	)
}

func (transaction *postgresRevisionFencedTransaction) rollbackWithoutSession(ctx context.Context) error {
	transaction.mu.Lock()
	defer transaction.mu.Unlock()
	if transaction.done || transaction.connection == nil {
		return nil
	}
	_, err := transaction.rollbackLocked(ctx)
	return err
}

func postgresMigrationDetachedCleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		return context.WithTimeout(context.Background(), postgresMigrationCleanupTimeout)
	}
	return context.WithTimeout(context.WithoutCancel(ctx), postgresMigrationCleanupTimeout)
}

func postgresMigrationAdvisoryLockKey(namespace string) int64 {
	hash := sha256.New()
	for _, value := range []string{"godj/postgres/migration-revision-lock/v1", namespace} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	return int64(binary.BigEndian.Uint64(hash.Sum(nil)[:8]))
}

func discardPostgresMigrationConnection(connection postgresMigrationPinnedConnection) error {
	if connection == nil {
		return nil
	}
	err := connection.Raw(func(any) error { return driver.ErrBadConn })
	if errors.Is(err, driver.ErrBadConn) || errors.Is(err, sql.ErrConnDone) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("discard PostgreSQL migration connection: %w", err)
	}
	return nil
}

func closeOrDiscardPostgresMigrationConnection(connection postgresMigrationPinnedConnection) error {
	if connection == nil {
		return nil
	}
	if err := connection.Close(); err != nil {
		return errors.Join(
			fmt.Errorf("close PostgreSQL migration connection: %w", err),
			discardPostgresMigrationConnection(connection),
		)
	}
	return nil
}
