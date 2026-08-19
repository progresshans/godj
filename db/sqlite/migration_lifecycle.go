package sqlite

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
	"sort"
	"sync"
	"time"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
	sqlite3 "modernc.org/sqlite/lib"
)

const (
	migrationRevisionTable         = "godj_migration_revision"
	migrationRevisionFormatVersion = int64(1)
	migrationRevisionEpochSize     = 16
	migrationCleanupTimeout        = 5 * time.Second
)

const createMigrationRevisionTableSQL = `CREATE TABLE "godj_migration_revision" (` +
	`"singleton" INTEGER NOT NULL PRIMARY KEY CHECK ("singleton" = 1), ` +
	`"format_version" INTEGER NOT NULL CHECK ("format_version" > 0), ` +
	`"epoch" BLOB NOT NULL CHECK (typeof("epoch") = 'blob' AND length("epoch") = 16), ` +
	`"revision" INTEGER NOT NULL CHECK (typeof("revision") = 'integer' AND "revision" >= 0), ` +
	`"history_fingerprint" BLOB NOT NULL CHECK (typeof("history_fingerprint") = 'blob' AND length("history_fingerprint") = 32))`

const readMigrationRevisionSQL = `SELECT ` +
	`"singleton", typeof("singleton"), ` +
	`"format_version", typeof("format_version"), ` +
	`"epoch", typeof("epoch"), ` +
	`"revision", typeof("revision"), ` +
	`"history_fingerprint", typeof("history_fingerprint") ` +
	`FROM "godj_migration_revision"`

var _ migrationbackend.RevisionFencedBackend = (*Backend)(nil)
var _ migrationbackend.RevisionFencedSession = (*sqliteRevisionFencedSession)(nil)
var _ migrationbackend.RevisionFencedTransaction = (*sqliteRevisionFencedTransaction)(nil)

type migrationPinnedConnection interface {
	migrationSQLExecutor
	Raw(func(any) error) error
	Close() error
}

var _ migrationPinnedConnection = (*sql.Conn)(nil)

type migrationRevisionToken struct {
	initialized bool
	epoch       [migrationRevisionEpochSize]byte
	revision    int64
	fingerprint [sha256.Size]byte
}

type migrationRevisionSnapshot struct {
	metadataPresent bool
	recorderPresent bool
	records         []migrationbackend.AppliedMigration
	token           migrationRevisionToken
}

type revisionSessionState uint8

const (
	revisionSessionOpen revisionSessionState = iota + 1
	revisionSessionReady
	revisionSessionActive
	revisionSessionPoisoned
	revisionSessionClosing
	revisionSessionClosed
)

type sqliteRevisionFencedSession struct {
	mu                      sync.Mutex
	backend                 *Backend
	state                   revisionSessionState
	records                 []migrationbackend.AppliedMigration
	token                   migrationRevisionToken
	active                  *sqliteRevisionFencedTransaction
	relationBeginCheckpoint func(sqliteRelationBeginCheckpoint)
	relationConnectionHook  func(migrationPinnedConnection) migrationPinnedConnection
	closeDone               chan struct{}
	closeErr                error
}

func (b *Backend) OpenRevisionFencedSession(ctx context.Context) (migrationbackend.RevisionFencedSession, error) {
	if b == nil || b.database == nil || b.closed.Load() {
		return nil, errors.New("open SQLite revision-fenced migration session: backend is nil or closed")
	}
	if ctx == nil {
		return nil, errors.New("open SQLite revision-fenced migration session: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("open SQLite revision-fenced migration session: %w", err)
	}
	return &sqliteRevisionFencedSession{backend: b, state: revisionSessionOpen}, nil
}

func (session *sqliteRevisionFencedSession) ReadAppliedMigrations(ctx context.Context) ([]migrationbackend.AppliedMigration, error) {
	if session == nil {
		return nil, errors.New("read SQLite revision-fenced history: session is nil")
	}
	if ctx == nil {
		return nil, errors.New("read SQLite revision-fenced history: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("read SQLite revision-fenced history: %w", err)
	}

	session.mu.Lock()
	defer session.mu.Unlock()
	if session.state != revisionSessionOpen {
		return nil, fmt.Errorf("read SQLite revision-fenced history: session state %d does not permit a snapshot", session.state)
	}

	snapshot, err := readAtomicMigrationRevisionSnapshot(ctx, session.backend)
	if err != nil {
		session.state = revisionSessionPoisoned
		return nil, err
	}
	if !snapshot.metadataPresent && snapshot.recorderPresent {
		session.state = revisionSessionPoisoned
		return nil, newRevisionFenceError(
			migrationbackend.RevisionFenceFailureAdoptionRequired,
			errors.New("SQLite migration recorder exists without revision metadata; exclusive adoption is required"),
		)
	}
	session.records = cloneAppliedMigrations(snapshot.records)
	session.token = snapshot.token
	session.state = revisionSessionReady
	return cloneAppliedMigrations(snapshot.records), nil
}

func (session *sqliteRevisionFencedSession) BeginFencedMigration(
	ctx context.Context,
	transition migrationbackend.HistoryTransition,
) (migrationbackend.RevisionFencedTransaction, error) {
	if session == nil {
		return nil, errors.New("begin SQLite revision-fenced migration: session is nil")
	}
	if ctx == nil {
		return nil, errors.New("begin SQLite revision-fenced migration: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("begin SQLite revision-fenced migration: %w", err)
	}
	if transition.Migration.App == "" || transition.Migration.Name == "" {
		return nil, newRevisionFenceError(
			migrationbackend.RevisionFenceFailureIntegrity,
			errors.New("history transition requires a non-empty app and migration name"),
		)
	}
	if transition.Kind != migrationbackend.HistoryTransitionApply && transition.Kind != migrationbackend.HistoryTransitionUnapply {
		return nil, newRevisionFenceError(
			migrationbackend.RevisionFenceFailureIntegrity,
			fmt.Errorf("history transition kind %d is invalid", transition.Kind),
		)
	}

	session.mu.Lock()
	defer session.mu.Unlock()
	if session.state != revisionSessionReady {
		return nil, fmt.Errorf("begin SQLite revision-fenced migration: session state %d is not ready", session.state)
	}

	successorRecords, err := migrationHistorySuccessor(session.records, transition)
	if err != nil {
		session.state = revisionSessionPoisoned
		return nil, err
	}
	successorToken := session.token
	successorToken.initialized = true
	successorToken.fingerprint = fingerprintMigrationHistory(successorRecords)
	if session.token.initialized {
		if session.token.revision == math.MaxInt64 {
			session.state = revisionSessionPoisoned
			return nil, newRevisionFenceError(
				migrationbackend.RevisionFenceFailureIntegrity,
				errors.New("SQLite migration revision is exhausted"),
			)
		}
		successorToken.revision = session.token.revision + 1
	} else {
		if transition.Kind != migrationbackend.HistoryTransitionApply {
			session.state = revisionSessionPoisoned
			return nil, newRevisionFenceError(
				migrationbackend.RevisionFenceFailureIntegrity,
				errors.New("an uninitialized history cannot begin with an unapply transition"),
			)
		}
		if _, err := rand.Read(successorToken.epoch[:]); err != nil {
			session.state = revisionSessionPoisoned
			return nil, fmt.Errorf("generate SQLite migration revision epoch: %w", err)
		}
		successorToken.revision = 1
	}

	connection, err := session.backend.database.Conn(ctx)
	if err != nil {
		session.state = revisionSessionPoisoned
		return nil, classifyRevisionIO("acquire pinned migration connection", err)
	}
	if _, err := connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		discardErr := discardMigrationConnection(connection)
		session.state = revisionSessionPoisoned
		return nil, errors.Join(classifyRevisionIO("begin immediate migration transaction", err), discardErr)
	}

	transaction := &sqliteRevisionFencedTransaction{
		connection:       connection,
		session:          session,
		transition:       transition,
		expectedRecords:  cloneAppliedMigrations(session.records),
		successorRecords: cloneAppliedMigrations(successorRecords),
		expectedToken:    session.token,
		successorToken:   successorToken,
		bootstrap:        !session.token.initialized,
	}
	if err := transaction.claimRevision(ctx); err != nil {
		cleanupErr := transaction.rollbackWithoutSession(ctx)
		session.state = revisionSessionPoisoned
		return nil, errors.Join(err, cleanupErr)
	}
	session.active = transaction
	session.state = revisionSessionActive
	return transaction, nil
}

func (session *sqliteRevisionFencedSession) Close(ctx context.Context) error {
	if session == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("close SQLite revision-fenced migration session: context is nil")
	}

	session.mu.Lock()
	if session.state == revisionSessionClosed {
		err := session.closeErr
		session.mu.Unlock()
		return err
	}
	if session.state == revisionSessionClosing {
		done := session.closeDone
		session.mu.Unlock()
		select {
		case <-done:
		case <-ctx.Done():
			return fmt.Errorf("close SQLite revision-fenced migration session while another close is in progress: %w", ctx.Err())
		}
		session.mu.Lock()
		err := session.closeErr
		session.mu.Unlock()
		return err
	}
	active := session.active
	session.active = nil
	session.state = revisionSessionClosing
	session.closeDone = make(chan struct{})
	done := session.closeDone
	session.mu.Unlock()

	var cleanupErr error
	if active != nil {
		cleanupErr = active.rollbackWithoutSession(ctx)
	}
	if cleanupErr != nil {
		cleanupErr = fmt.Errorf("close SQLite revision-fenced migration session: %w", cleanupErr)
	}

	session.mu.Lock()
	session.closeErr = cleanupErr
	session.state = revisionSessionClosed
	close(done)
	session.mu.Unlock()
	return cleanupErr
}

func (session *sqliteRevisionFencedSession) finishTransaction(
	transaction *sqliteRevisionFencedTransaction,
	committed bool,
	successorToken migrationRevisionToken,
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
		session.records = cloneAppliedMigrations(successorRecords)
	}
	if poison {
		session.state = revisionSessionPoisoned
	} else {
		session.state = revisionSessionReady
	}
}

type sqliteRevisionFencedTransaction struct {
	mu               sync.Mutex
	connection       migrationPinnedConnection
	session          *sqliteRevisionFencedSession
	transition       migrationbackend.HistoryTransition
	expectedRecords  []migrationbackend.AppliedMigration
	successorRecords []migrationbackend.AppliedMigration
	expectedToken    migrationRevisionToken
	successorToken   migrationRevisionToken
	bootstrap        bool
	recorderCalled   bool
	relation         *sqliteRelationFencedState
	failure          error
	done             bool
}

func (transaction *sqliteRevisionFencedTransaction) claimRevision(ctx context.Context) error {
	current, err := inspectMigrationRevisionSnapshot(ctx, transaction.connection)
	if err != nil {
		return err
	}
	if !transaction.expectedToken.initialized {
		if current.metadataPresent || current.recorderPresent {
			return newRevisionFenceError(
				migrationbackend.RevisionFenceFailureStale,
				errors.New("fresh SQLite migration history changed before bootstrap"),
			)
		}
		if _, err := transaction.connection.ExecContext(ctx, createMigrationRevisionTableSQL); err != nil {
			return classifyRevisionIntegrityIO("create migration revision metadata", err)
		}
		if _, err := transaction.connection.ExecContext(
			ctx,
			`INSERT INTO "godj_migration_revision" (`+
				`"singleton", "format_version", "epoch", "revision", "history_fingerprint") `+
				`VALUES (1, ?, ?, ?, ?)`,
			migrationRevisionFormatVersion,
			transaction.successorToken.epoch[:],
			transaction.successorToken.revision,
			transaction.successorToken.fingerprint[:],
		); err != nil {
			return classifyRevisionIntegrityIO("initialize migration revision metadata", err)
		}
		return nil
	}

	if !current.metadataPresent {
		return newRevisionFenceError(
			migrationbackend.RevisionFenceFailureStale,
			errors.New("SQLite migration revision metadata disappeared after snapshot"),
		)
	}
	if !current.recorderPresent {
		return newRevisionFenceError(
			migrationbackend.RevisionFenceFailureIntegrity,
			errors.New("SQLite migration revision metadata exists without a recorder"),
		)
	}
	if !equalMigrationRevisionToken(current.token, transaction.expectedToken) ||
		!equalAppliedMigrations(current.records, transaction.expectedRecords) {
		return newRevisionFenceError(
			migrationbackend.RevisionFenceFailureStale,
			errors.New("SQLite migration history revision is stale"),
		)
	}

	result, err := transaction.connection.ExecContext(
		ctx,
		`UPDATE "godj_migration_revision" `+
			`SET "revision" = ?, "history_fingerprint" = ? `+
			`WHERE "singleton" = 1 AND "format_version" = ? AND "epoch" = ? `+
			`AND "revision" = ? AND "history_fingerprint" = ?`,
		transaction.successorToken.revision,
		transaction.successorToken.fingerprint[:],
		migrationRevisionFormatVersion,
		transaction.expectedToken.epoch[:],
		transaction.expectedToken.revision,
		transaction.expectedToken.fingerprint[:],
	)
	if err != nil {
		return classifyRevisionIO("claim migration history revision", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return classifyRevisionIntegrityIO("count migration history revision claim", err)
	}
	if rowsAffected != 1 {
		return newRevisionFenceError(
			migrationbackend.RevisionFenceFailureStale,
			fmt.Errorf("migration history revision claim changed %d rows, want 1", rowsAffected),
		)
	}
	return nil
}

func (transaction *sqliteRevisionFencedTransaction) CreateModel(ctx context.Context, model ir.Model) error {
	if transaction != nil && transaction.relation != nil {
		return transaction.executeRelationCreateModel(ctx, model)
	}
	statement, err := compileMigrationCreateModel(model)
	if err != nil {
		return err
	}
	return transaction.execute(ctx, "create model", func(executor migrationSQLExecutor) error {
		if _, err := executor.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("create SQLite model %q: %w", model.DBTable, err)
		}
		return nil
	})
}

func (transaction *sqliteRevisionFencedTransaction) DeleteModel(ctx context.Context, model ir.Model) error {
	if transaction != nil && transaction.relation != nil {
		return transaction.executeRelationDeleteModel(ctx, model)
	}
	statement, err := compileMigrationDeleteModel(model)
	if err != nil {
		return err
	}
	return transaction.execute(ctx, "delete model", func(executor migrationSQLExecutor) error {
		if _, err := executor.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("delete SQLite model %q: %w", model.DBTable, err)
		}
		return nil
	})
}

func (transaction *sqliteRevisionFencedTransaction) TableExists(ctx context.Context, table string) (bool, error) {
	if table == "" {
		return false, errors.New("inspect SQLite fenced migration table: name is empty")
	}
	var count int
	err := transaction.execute(ctx, "inspect table", func(executor migrationSQLExecutor) error {
		return executor.QueryRowContext(
			ctx,
			`SELECT COUNT(*) FROM "sqlite_schema" WHERE "type" = 'table' AND "name" = ?`,
			table,
		).Scan(&count)
	})
	return count == 1, err
}

func (transaction *sqliteRevisionFencedTransaction) AddField(ctx context.Context, model ir.Model, field ir.Field) error {
	if transaction != nil && transaction.relation != nil {
		return transaction.executeRelationAddField(ctx, model, field)
	}
	if field.PrimaryKey {
		return migrationbackend.NewCapabilityError(
			"sqlite_add_field",
			fmt.Sprintf("field %s.%s must be non-primary-key", model.DBTable, field.Column),
			nil,
		)
	}
	statement, err := compileMigrationAddField(model, field)
	if err != nil {
		return err
	}
	return transaction.execute(ctx, "add field", func(executor migrationSQLExecutor) error {
		if field.Default != nil || !field.Nullable {
			empty, err := sqliteTableEmpty(ctx, executor, model.DBTable)
			if err != nil {
				return err
			}
			if !empty && field.Default != nil {
				return migrationbackend.NewCapabilityError(
					"sqlite_add_field",
					fmt.Sprintf("table %s contains rows; adding field %s with a migration default requires one-time backfill or table rebuild", model.DBTable, field.Column),
					nil,
				)
			}
			if !empty {
				return migrationbackend.NewCapabilityError(
					"sqlite_add_field",
					fmt.Sprintf("table %s contains rows; adding non-null field %s requires table rebuild", model.DBTable, field.Column),
					nil,
				)
			}
		}
		if _, err := executor.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("add SQLite field %s.%s: %w", model.DBTable, field.Column, err)
		}
		return nil
	})
}

func (transaction *sqliteRevisionFencedTransaction) RemoveField(ctx context.Context, model ir.Model, field ir.Field) error {
	if transaction != nil && transaction.relation != nil {
		return transaction.executeRelationRemoveField(ctx, model, field)
	}
	if field.PrimaryKey {
		return migrationbackend.NewCapabilityError(
			"sqlite_drop_column",
			fmt.Sprintf("field %s.%s must be non-primary-key", model.DBTable, field.Column),
			nil,
		)
	}
	statement, err := compileMigrationRemoveField(model, field)
	if err != nil {
		return err
	}
	return transaction.execute(ctx, "remove field", func(executor migrationSQLExecutor) error {
		if err := preflightSQLiteDropColumn(ctx, executor, model, field); err != nil {
			return err
		}
		if _, err := executor.ExecContext(ctx, statement); err != nil {
			if sqliteDropColumnCapabilityFailure(err) {
				return migrationbackend.NewCapabilityError(
					"sqlite_drop_column",
					fmt.Sprintf("SQLite rejected native DROP COLUMN for %s.%s; table rebuild is disabled", model.DBTable, field.Column),
					err,
				)
			}
			return fmt.Errorf("remove SQLite field %s.%s: %w", model.DBTable, field.Column, err)
		}
		return nil
	})
}

func (transaction *sqliteRevisionFencedTransaction) RecordApplied(ctx context.Context, app, name string) error {
	return transaction.record(ctx, migrationbackend.HistoryTransitionApply, app, name)
}

func (transaction *sqliteRevisionFencedTransaction) RecordUnapplied(ctx context.Context, app, name string) error {
	return transaction.record(ctx, migrationbackend.HistoryTransitionUnapply, app, name)
}

func (transaction *sqliteRevisionFencedTransaction) record(
	ctx context.Context,
	kind migrationbackend.HistoryTransitionKind,
	app,
	name string,
) error {
	return transaction.execute(ctx, "record migration history", func(executor migrationSQLExecutor) error {
		if app == "" || name == "" {
			return newRevisionFenceError(
				migrationbackend.RevisionFenceFailureIntegrity,
				errors.New("recorded migration identity is empty"),
			)
		}
		if transaction.recorderCalled {
			return newRevisionFenceError(
				migrationbackend.RevisionFenceFailureIntegrity,
				errors.New("fenced migration recorder transition was called more than once"),
			)
		}
		if kind != transaction.transition.Kind || app != transaction.transition.Migration.App || name != transaction.transition.Migration.Name {
			return newRevisionFenceError(
				migrationbackend.RevisionFenceFailureIntegrity,
				fmt.Errorf("recorder transition %d %s.%s does not match declared transition", kind, app, name),
			)
		}
		if err := transaction.verifyRelationBeforeRecord(ctx, executor); err != nil {
			return err
		}
		if kind == migrationbackend.HistoryTransitionApply {
			if transaction.bootstrap {
				if err := ensureMigrationRecorder(ctx, executor); err != nil {
					return err
				}
			}
			if _, err := executor.ExecContext(
				ctx,
				`INSERT INTO "godj_migrations" ("app", "name") VALUES (?, ?)`,
				app,
				name,
			); err != nil {
				return fmt.Errorf("record applied SQLite migration %s.%s: %w", app, name, err)
			}
		} else {
			result, err := executor.ExecContext(
				ctx,
				`DELETE FROM "godj_migrations" WHERE "app" = ? AND "name" = ?`,
				app,
				name,
			)
			if err != nil {
				return fmt.Errorf("record unapplied SQLite migration %s.%s: %w", app, name, err)
			}
			rowsAffected, err := result.RowsAffected()
			if err != nil {
				return fmt.Errorf("count removed SQLite migration record %s.%s: %w", app, name, err)
			}
			if rowsAffected != 1 {
				return newRevisionFenceError(
					migrationbackend.RevisionFenceFailureIntegrity,
					fmt.Errorf("record unapplied SQLite migration %s.%s removed %d records, want 1", app, name, rowsAffected),
				)
			}
		}
		transaction.recorderCalled = true
		return nil
	})
}

func (transaction *sqliteRevisionFencedTransaction) CommitFenced(ctx context.Context) (migrationbackend.CommitOutcome, error) {
	if transaction == nil {
		return migrationbackend.CommitOutcome{Durability: migrationbackend.CommitUnknown}, errors.New("commit SQLite fenced migration: transaction is nil")
	}
	if ctx == nil {
		return migrationbackend.CommitOutcome{Durability: migrationbackend.CommitUnknown}, errors.New("commit SQLite fenced migration: context is nil")
	}

	transaction.mu.Lock()
	if transaction.done || transaction.connection == nil {
		transaction.mu.Unlock()
		return migrationbackend.CommitOutcome{Durability: migrationbackend.CommitUnknown}, errors.New("commit SQLite fenced migration: transaction is already complete")
	}
	if err := ctx.Err(); err != nil {
		primary := fmt.Errorf("commit SQLite fenced migration: %w", err)
		cleanupErr := transaction.rollbackLocked(ctx)
		transaction.mu.Unlock()
		transaction.session.finishTransaction(transaction, false, migrationRevisionToken{}, nil, true)
		return migrationbackend.CommitOutcome{Durability: migrationbackend.CommitRolledBack}, errors.Join(primary, cleanupErr)
	}
	if transaction.failure != nil {
		primary := transaction.failure
		cleanupErr := transaction.rollbackLocked(ctx)
		transaction.mu.Unlock()
		transaction.session.finishTransaction(transaction, false, migrationRevisionToken{}, nil, true)
		return migrationbackend.CommitOutcome{Durability: migrationbackend.CommitRolledBack}, errors.Join(primary, cleanupErr)
	}
	if !transaction.recorderCalled {
		primary := newRevisionFenceError(
			migrationbackend.RevisionFenceFailureIntegrity,
			errors.New("fenced migration cannot commit without its declared recorder transition"),
		)
		cleanupErr := transaction.rollbackLocked(ctx)
		transaction.mu.Unlock()
		transaction.session.finishTransaction(transaction, false, migrationRevisionToken{}, nil, true)
		return migrationbackend.CommitOutcome{Durability: migrationbackend.CommitRolledBack}, errors.Join(primary, cleanupErr)
	}
	if err := transaction.verifyRelationCommitReady(); err != nil {
		cleanupErr := transaction.rollbackLocked(ctx)
		transaction.mu.Unlock()
		transaction.session.finishTransaction(transaction, false, migrationRevisionToken{}, nil, true)
		return migrationbackend.CommitOutcome{Durability: migrationbackend.CommitRolledBack}, errors.Join(err, cleanupErr)
	}
	if err := transaction.verifySuccessor(ctx); err != nil {
		cleanupErr := transaction.rollbackLocked(ctx)
		transaction.mu.Unlock()
		transaction.session.finishTransaction(transaction, false, migrationRevisionToken{}, nil, true)
		return migrationbackend.CommitOutcome{Durability: migrationbackend.CommitRolledBack}, errors.Join(err, cleanupErr)
	}

	_, commitErr := transaction.connection.ExecContext(ctx, "COMMIT")
	if commitErr == nil {
		transaction.done = true
		closeErr := closeOrDiscardMigrationConnection(transaction.connection)
		transaction.connection = nil
		transaction.mu.Unlock()
		transaction.session.finishTransaction(
			transaction,
			true,
			transaction.successorToken,
			transaction.successorRecords,
			closeErr != nil,
		)
		return migrationbackend.CommitOutcome{Durability: migrationbackend.CommitCommitted}, closeErr
	}

	primary := classifyRevisionIO("commit fenced migration transaction", commitErr)
	rolledBack, cleanupErr := transaction.rollbackAfterCommitFailureLocked(ctx)
	transaction.mu.Unlock()
	transaction.session.finishTransaction(transaction, false, migrationRevisionToken{}, nil, true)
	if rolledBack {
		return migrationbackend.CommitOutcome{Durability: migrationbackend.CommitRolledBack}, errors.Join(primary, cleanupErr)
	}
	return migrationbackend.CommitOutcome{Durability: migrationbackend.CommitUnknown}, errors.Join(primary, cleanupErr)
}

func (transaction *sqliteRevisionFencedTransaction) Rollback(ctx context.Context) error {
	if transaction == nil {
		return errors.New("rollback SQLite fenced migration: transaction is nil")
	}
	if ctx == nil {
		return errors.New("rollback SQLite fenced migration: context is nil")
	}
	transaction.mu.Lock()
	if transaction.done || transaction.connection == nil {
		transaction.mu.Unlock()
		return nil
	}
	err := transaction.rollbackLocked(ctx)
	transaction.mu.Unlock()
	transaction.session.finishTransaction(transaction, false, migrationRevisionToken{}, nil, true)
	return err
}

func (transaction *sqliteRevisionFencedTransaction) rollbackWithoutSession(ctx context.Context) error {
	transaction.mu.Lock()
	defer transaction.mu.Unlock()
	if transaction.done || transaction.connection == nil {
		return nil
	}
	return transaction.rollbackLocked(ctx)
}

func (transaction *sqliteRevisionFencedTransaction) execute(
	ctx context.Context,
	stage string,
	operation func(migrationSQLExecutor) error,
) error {
	if transaction == nil {
		return errors.New("execute SQLite fenced migration: transaction is nil")
	}
	if ctx == nil {
		return errors.New("execute SQLite fenced migration: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("execute SQLite fenced migration: %w", err)
	}
	transaction.mu.Lock()
	defer transaction.mu.Unlock()
	if transaction.done || transaction.connection == nil {
		return errors.New("execute SQLite fenced migration: transaction is already complete")
	}
	if transaction.failure != nil {
		return transaction.failure
	}
	err := classifyRevisionIO(stage, operation(transaction.connection))
	if err != nil {
		transaction.failure = err
	}
	return err
}

func (transaction *sqliteRevisionFencedTransaction) verifySuccessor(ctx context.Context) error {
	current, err := inspectMigrationRevisionSnapshot(ctx, transaction.connection)
	if err != nil {
		return err
	}
	if !current.metadataPresent || !current.recorderPresent ||
		!equalMigrationRevisionToken(current.token, transaction.successorToken) ||
		!equalAppliedMigrations(current.records, transaction.successorRecords) {
		return newRevisionFenceError(
			migrationbackend.RevisionFenceFailureIntegrity,
			errors.New("fenced migration durable successor does not match its declared history transition"),
		)
	}
	return nil
}

func (transaction *sqliteRevisionFencedTransaction) rollbackLocked(ctx context.Context) error {
	cleanupCtx, cancel := migrationDetachedCleanupContext(ctx)
	defer cancel()
	_, rollbackErr := transaction.connection.ExecContext(cleanupCtx, "ROLLBACK")
	transaction.done = true
	if rollbackErr == nil {
		closeErr := closeOrDiscardMigrationConnection(transaction.connection)
		transaction.connection = nil
		return closeErr
	}
	discardErr := discardMigrationConnection(transaction.connection)
	transaction.connection = nil
	return errors.Join(
		classifyRevisionIO("rollback fenced migration transaction", rollbackErr),
		discardErr,
	)
}

func (transaction *sqliteRevisionFencedTransaction) rollbackAfterCommitFailureLocked(ctx context.Context) (bool, error) {
	cleanupCtx, cancel := migrationDetachedCleanupContext(ctx)
	defer cancel()
	_, rollbackErr := transaction.connection.ExecContext(cleanupCtx, "ROLLBACK")
	transaction.done = true
	if rollbackErr == nil {
		closeErr := closeOrDiscardMigrationConnection(transaction.connection)
		transaction.connection = nil
		return true, closeErr
	}
	discardErr := discardMigrationConnection(transaction.connection)
	transaction.connection = nil
	return false, errors.Join(
		classifyRevisionIO("rollback after fenced migration commit failure", rollbackErr),
		discardErr,
	)
}

func readAtomicMigrationRevisionSnapshot(ctx context.Context, backend *Backend) (migrationRevisionSnapshot, error) {
	transaction, err := backend.database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return migrationRevisionSnapshot{}, classifyRevisionIO("begin atomic migration history snapshot", err)
	}
	snapshot, snapshotErr := inspectMigrationRevisionSnapshot(ctx, transaction)
	if snapshotErr != nil {
		rollbackErr := transaction.Rollback()
		if errors.Is(rollbackErr, sql.ErrTxDone) {
			rollbackErr = nil
		}
		return migrationRevisionSnapshot{}, errors.Join(
			snapshotErr,
			classifyRevisionIO("rollback atomic migration history snapshot", wrapRollbackMigration(rollbackErr)),
		)
	}
	if err := transaction.Commit(); err != nil {
		return migrationRevisionSnapshot{}, classifyRevisionIO("commit atomic migration history snapshot", err)
	}
	return snapshot, nil
}

func inspectMigrationRevisionSnapshot(ctx context.Context, executor migrationSQLExecutor) (migrationRevisionSnapshot, error) {
	metadataType, metadataPresent, err := sqliteSchemaObjectType(ctx, executor, migrationRevisionTable)
	if err != nil {
		return migrationRevisionSnapshot{}, err
	}
	recorderType, recorderPresent, err := sqliteSchemaObjectType(ctx, executor, migrationRecorderTable)
	if err != nil {
		return migrationRevisionSnapshot{}, err
	}
	if metadataPresent && metadataType != "table" {
		return migrationRevisionSnapshot{}, newRevisionFenceError(
			migrationbackend.RevisionFenceFailureIntegrity,
			fmt.Errorf("SQLite migration revision object is %q, want table", metadataType),
		)
	}
	if metadataPresent {
		if err := validateMigrationRevisionTableShape(ctx, executor); err != nil {
			return migrationRevisionSnapshot{}, err
		}
	}
	if recorderPresent && recorderType != "table" {
		if !metadataPresent {
			return migrationRevisionSnapshot{recorderPresent: true}, nil
		}
		return migrationRevisionSnapshot{}, newRevisionFenceError(
			migrationbackend.RevisionFenceFailureIntegrity,
			fmt.Errorf("SQLite migration recorder object is %q, want table", recorderType),
		)
	}
	if !metadataPresent {
		return migrationRevisionSnapshot{
			metadataPresent: false,
			recorderPresent: recorderPresent,
			records:         []migrationbackend.AppliedMigration{},
			token: migrationRevisionToken{
				fingerprint: fingerprintMigrationHistory(nil),
			},
		}, nil
	}
	if !recorderPresent {
		return migrationRevisionSnapshot{}, newRevisionFenceError(
			migrationbackend.RevisionFenceFailureIntegrity,
			errors.New("SQLite migration revision metadata exists without a recorder table"),
		)
	}
	if err := validateMigrationRecorderTableShape(ctx, executor); err != nil {
		return migrationRevisionSnapshot{}, err
	}

	records, err := readRevisionRecorderHistory(ctx, executor)
	if err != nil {
		return migrationRevisionSnapshot{}, err
	}
	token, err := readMigrationRevisionToken(ctx, executor)
	if err != nil {
		return migrationRevisionSnapshot{}, err
	}
	fingerprint := fingerprintMigrationHistory(records)
	if fingerprint != token.fingerprint {
		return migrationRevisionSnapshot{}, newRevisionFenceError(
			migrationbackend.RevisionFenceFailureIntegrity,
			errors.New("stored SQLite migration history fingerprint does not match recorder identities"),
		)
	}
	return migrationRevisionSnapshot{
		metadataPresent: true,
		recorderPresent: true,
		records:         records,
		token:           token,
	}, nil
}

func validateMigrationRecorderTableShape(ctx context.Context, executor migrationSQLExecutor) (resultErr error) {
	type expectedColumn struct {
		name       string
		declared   string
		notNull    int
		primaryKey int
	}
	expected := []expectedColumn{
		{name: "app", declared: "VARCHAR(255)", notNull: 1, primaryKey: 1},
		{name: "name", declared: "VARCHAR(255)", notNull: 1, primaryKey: 2},
	}
	rows, err := executor.QueryContext(ctx, `PRAGMA table_info("godj_migrations")`)
	if err != nil {
		return classifyRevisionIntegrityIO("inspect migration recorder columns", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, classifyRevisionIntegrityIO("close migration recorder columns", rows.Close()))
	}()
	index := 0
	for rows.Next() {
		var (
			sequence     int
			name         string
			declaredType string
			notNull      int
			defaultValue sql.NullString
			primaryKey   int
		)
		if err := rows.Scan(&sequence, &name, &declaredType, &notNull, &defaultValue, &primaryKey); err != nil {
			return classifyRevisionIntegrityIO("scan migration recorder column", err)
		}
		if index >= len(expected) {
			return newRevisionFenceError(
				migrationbackend.RevisionFenceFailureIntegrity,
				fmt.Errorf("SQLite migration recorder has unexpected extra column %q", name),
			)
		}
		want := expected[index]
		if sequence != index || name != want.name || declaredType != want.declared ||
			notNull != want.notNull || defaultValue.Valid || primaryKey != want.primaryKey {
			return newRevisionFenceError(
				migrationbackend.RevisionFenceFailureIntegrity,
				fmt.Errorf(
					"SQLite migration recorder column[%d] shape=(%q,%q,notnull=%d,default=%v,pk=%d), want=(%q,%q,notnull=%d,default=NULL,pk=%d)",
					sequence,
					name,
					declaredType,
					notNull,
					defaultValue,
					primaryKey,
					want.name,
					want.declared,
					want.notNull,
					want.primaryKey,
				),
			)
		}
		index++
	}
	if err := rows.Err(); err != nil {
		return classifyRevisionIntegrityIO("iterate migration recorder columns", err)
	}
	if index != len(expected) {
		return newRevisionFenceError(
			migrationbackend.RevisionFenceFailureIntegrity,
			fmt.Errorf("SQLite migration recorder has %d columns, want %d", index, len(expected)),
		)
	}
	if err := rows.Close(); err != nil {
		return classifyRevisionIntegrityIO("close migration recorder columns", err)
	}
	var definition string
	if err := executor.QueryRowContext(
		ctx,
		`SELECT "sql" FROM "sqlite_schema" WHERE "type" = 'table' AND "name" = ?`,
		migrationRecorderTable,
	).Scan(&definition); err != nil {
		return classifyRevisionIntegrityIO("read migration recorder definition", err)
	}
	if definition != migrationRecorderTableDefinitionSQL {
		return newRevisionFenceError(
			migrationbackend.RevisionFenceFailureIntegrity,
			errors.New("SQLite migration recorder definition does not match the revision-fenced format"),
		)
	}
	return nil
}

func validateMigrationRevisionTableShape(ctx context.Context, executor migrationSQLExecutor) (resultErr error) {
	type expectedColumn struct {
		name       string
		declared   string
		notNull    int
		primaryKey int
	}
	expected := []expectedColumn{
		{name: "singleton", declared: "INTEGER", notNull: 1, primaryKey: 1},
		{name: "format_version", declared: "INTEGER", notNull: 1},
		{name: "epoch", declared: "BLOB", notNull: 1},
		{name: "revision", declared: "INTEGER", notNull: 1},
		{name: "history_fingerprint", declared: "BLOB", notNull: 1},
	}
	rows, err := executor.QueryContext(ctx, `PRAGMA table_info("godj_migration_revision")`)
	if err != nil {
		return classifyRevisionIntegrityIO("inspect migration revision metadata columns", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, classifyRevisionIntegrityIO("close migration revision metadata columns", rows.Close()))
	}()
	index := 0
	for rows.Next() {
		var (
			sequence     int
			name         string
			declaredType string
			notNull      int
			defaultValue sql.NullString
			primaryKey   int
		)
		if err := rows.Scan(&sequence, &name, &declaredType, &notNull, &defaultValue, &primaryKey); err != nil {
			return classifyRevisionIntegrityIO("scan migration revision metadata column", err)
		}
		if index >= len(expected) {
			return newRevisionFenceError(
				migrationbackend.RevisionFenceFailureIntegrity,
				fmt.Errorf("SQLite migration revision metadata has unexpected extra column %q", name),
			)
		}
		want := expected[index]
		if sequence != index || name != want.name || declaredType != want.declared ||
			notNull != want.notNull || defaultValue.Valid || primaryKey != want.primaryKey {
			return newRevisionFenceError(
				migrationbackend.RevisionFenceFailureIntegrity,
				fmt.Errorf(
					"SQLite migration revision column[%d] shape=(%q,%q,notnull=%d,default=%v,pk=%d), want=(%q,%q,notnull=%d,default=NULL,pk=%d)",
					sequence,
					name,
					declaredType,
					notNull,
					defaultValue,
					primaryKey,
					want.name,
					want.declared,
					want.notNull,
					want.primaryKey,
				),
			)
		}
		index++
	}
	if err := rows.Err(); err != nil {
		return classifyRevisionIntegrityIO("iterate migration revision metadata columns", err)
	}
	if index != len(expected) {
		return newRevisionFenceError(
			migrationbackend.RevisionFenceFailureIntegrity,
			fmt.Errorf("SQLite migration revision metadata has %d columns, want %d", index, len(expected)),
		)
	}
	if err := rows.Close(); err != nil {
		return classifyRevisionIntegrityIO("close migration revision metadata columns", err)
	}
	var definition string
	if err := executor.QueryRowContext(
		ctx,
		`SELECT "sql" FROM "sqlite_schema" WHERE "type" = 'table' AND "name" = ?`,
		migrationRevisionTable,
	).Scan(&definition); err != nil {
		return classifyRevisionIntegrityIO("read migration revision metadata definition", err)
	}
	if definition != createMigrationRevisionTableSQL {
		return newRevisionFenceError(
			migrationbackend.RevisionFenceFailureIntegrity,
			errors.New("SQLite migration revision metadata definition does not match format v1"),
		)
	}
	return nil
}

func sqliteSchemaObjectType(ctx context.Context, executor migrationSQLExecutor, name string) (string, bool, error) {
	var objectType string
	err := executor.QueryRowContext(
		ctx,
		`SELECT "type" FROM "sqlite_schema" WHERE "name" = ? ORDER BY "type" LIMIT 1`,
		name,
	).Scan(&objectType)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, classifyRevisionIO("inspect SQLite schema object "+name, err)
	}
	return objectType, true, nil
}

func readRevisionRecorderHistory(ctx context.Context, executor migrationSQLExecutor) (records []migrationbackend.AppliedMigration, resultErr error) {
	rows, err := executor.QueryContext(ctx, readAppliedMigrationsSQL)
	if err != nil {
		return nil, classifyRevisionIntegrityIO("read revision-fenced migration recorder", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			records = nil
			resultErr = errors.Join(resultErr, classifyRevisionIntegrityIO("close revision-fenced migration recorder rows", err))
		}
	}()
	for rows.Next() {
		var record migrationbackend.AppliedMigration
		if err := rows.Scan(&record.App, &record.Name); err != nil {
			return nil, classifyRevisionIntegrityIO("scan revision-fenced migration recorder", err)
		}
		if record.App == "" || record.Name == "" {
			return nil, newRevisionFenceError(
				migrationbackend.RevisionFenceFailureIntegrity,
				errors.New("SQLite migration recorder contains an empty identity"),
			)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyRevisionIntegrityIO("iterate revision-fenced migration recorder", err)
	}
	sortAppliedMigrations(records)
	for index := 1; index < len(records); index++ {
		if records[index] == records[index-1] {
			return nil, newRevisionFenceError(
				migrationbackend.RevisionFenceFailureIntegrity,
				fmt.Errorf("SQLite migration recorder duplicates %s.%s", records[index].App, records[index].Name),
			)
		}
	}
	return records, nil
}

func readMigrationRevisionToken(ctx context.Context, executor migrationSQLExecutor) (token migrationRevisionToken, resultErr error) {
	rows, err := executor.QueryContext(ctx, readMigrationRevisionSQL)
	if err != nil {
		return migrationRevisionToken{}, classifyRevisionIntegrityIO("read migration revision metadata", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			token = migrationRevisionToken{}
			resultErr = errors.Join(resultErr, classifyRevisionIntegrityIO("close migration revision metadata rows", err))
		}
	}()
	rowCount := 0
	for rows.Next() {
		rowCount++
		if rowCount > 1 {
			return migrationRevisionToken{}, newRevisionFenceError(
				migrationbackend.RevisionFenceFailureIntegrity,
				errors.New("SQLite migration revision metadata contains more than one row"),
			)
		}
		var (
			singleton, formatVersion, revision   int64
			singletonType, formatType, epochType string
			revisionType, fingerprintType        string
			epoch, fingerprint                   []byte
		)
		if err := rows.Scan(
			&singleton,
			&singletonType,
			&formatVersion,
			&formatType,
			&epoch,
			&epochType,
			&revision,
			&revisionType,
			&fingerprint,
			&fingerprintType,
		); err != nil {
			return migrationRevisionToken{}, classifyRevisionIntegrityIO("scan migration revision metadata", err)
		}
		if singletonType != "integer" || singleton != 1 {
			return migrationRevisionToken{}, newRevisionFenceError(
				migrationbackend.RevisionFenceFailureIntegrity,
				fmt.Errorf("SQLite migration revision singleton has type=%q value=%d", singletonType, singleton),
			)
		}
		if formatType != "integer" || formatVersion != migrationRevisionFormatVersion {
			return migrationRevisionToken{}, newRevisionFenceError(
				migrationbackend.RevisionFenceFailureIntegrity,
				fmt.Errorf("SQLite migration revision format has type=%q value=%d", formatType, formatVersion),
			)
		}
		if epochType != "blob" || len(epoch) != migrationRevisionEpochSize {
			return migrationRevisionToken{}, newRevisionFenceError(
				migrationbackend.RevisionFenceFailureIntegrity,
				fmt.Errorf("SQLite migration revision epoch has type=%q length=%d", epochType, len(epoch)),
			)
		}
		if revisionType != "integer" || revision < 0 {
			return migrationRevisionToken{}, newRevisionFenceError(
				migrationbackend.RevisionFenceFailureIntegrity,
				fmt.Errorf("SQLite migration revision has type=%q value=%d", revisionType, revision),
			)
		}
		if fingerprintType != "blob" || len(fingerprint) != sha256.Size {
			return migrationRevisionToken{}, newRevisionFenceError(
				migrationbackend.RevisionFenceFailureIntegrity,
				fmt.Errorf("SQLite migration history fingerprint has type=%q length=%d", fingerprintType, len(fingerprint)),
			)
		}
		token.initialized = true
		copy(token.epoch[:], epoch)
		token.revision = revision
		copy(token.fingerprint[:], fingerprint)
	}
	if err := rows.Err(); err != nil {
		return migrationRevisionToken{}, classifyRevisionIntegrityIO("iterate migration revision metadata", err)
	}
	if rowCount != 1 {
		return migrationRevisionToken{}, newRevisionFenceError(
			migrationbackend.RevisionFenceFailureIntegrity,
			fmt.Errorf("SQLite migration revision metadata contains %d rows, want 1", rowCount),
		)
	}
	return token, nil
}

func migrationHistorySuccessor(
	records []migrationbackend.AppliedMigration,
	transition migrationbackend.HistoryTransition,
) ([]migrationbackend.AppliedMigration, error) {
	successor := cloneAppliedMigrations(records)
	index := sort.Search(len(successor), func(index int) bool {
		return compareAppliedMigration(successor[index], transition.Migration) >= 0
	})
	if transition.Kind == migrationbackend.HistoryTransitionApply {
		if index < len(successor) && successor[index] == transition.Migration {
			return nil, newRevisionFenceError(
				migrationbackend.RevisionFenceFailureIntegrity,
				fmt.Errorf("migration %s.%s is already applied", transition.Migration.App, transition.Migration.Name),
			)
		}
		successor = append(successor, migrationbackend.AppliedMigration{})
		copy(successor[index+1:], successor[index:])
		successor[index] = transition.Migration
		return successor, nil
	}
	if index >= len(successor) || successor[index] != transition.Migration {
		return nil, newRevisionFenceError(
			migrationbackend.RevisionFenceFailureIntegrity,
			fmt.Errorf("migration %s.%s is not applied", transition.Migration.App, transition.Migration.Name),
		)
	}
	return append(successor[:index:index], successor[index+1:]...), nil
}

func fingerprintMigrationHistory(records []migrationbackend.AppliedMigration) [sha256.Size]byte {
	canonical := cloneAppliedMigrations(records)
	sortAppliedMigrations(canonical)
	hash := sha256.New()
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(canonical)))
	_, _ = hash.Write(length[:])
	for _, record := range canonical {
		for _, value := range []string{record.App, record.Name} {
			binary.BigEndian.PutUint64(length[:], uint64(len(value)))
			_, _ = hash.Write(length[:])
			_, _ = hash.Write([]byte(value))
		}
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result
}

func cloneAppliedMigrations(records []migrationbackend.AppliedMigration) []migrationbackend.AppliedMigration {
	if records == nil {
		return []migrationbackend.AppliedMigration{}
	}
	return append([]migrationbackend.AppliedMigration(nil), records...)
}

func sortAppliedMigrations(records []migrationbackend.AppliedMigration) {
	sort.Slice(records, func(left, right int) bool {
		return compareAppliedMigration(records[left], records[right]) < 0
	})
}

func compareAppliedMigration(left, right migrationbackend.AppliedMigration) int {
	if left.App < right.App {
		return -1
	}
	if left.App > right.App {
		return 1
	}
	if left.Name < right.Name {
		return -1
	}
	if left.Name > right.Name {
		return 1
	}
	return 0
}

func equalAppliedMigrations(left, right []migrationbackend.AppliedMigration) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalMigrationRevisionToken(left, right migrationRevisionToken) bool {
	return left.initialized == right.initialized &&
		left.epoch == right.epoch &&
		left.revision == right.revision &&
		left.fingerprint == right.fingerprint
}

func newRevisionFenceError(kind migrationbackend.RevisionFenceFailureKind, cause error) *migrationbackend.RevisionFenceError {
	return &migrationbackend.RevisionFenceError{Kind: kind, Cause: cause}
}

func classifyRevisionIO(stage string, err error) error {
	if err == nil {
		return nil
	}
	if migrationbackend.IsCapabilityError(err) || migrationbackend.IsRevisionFenceError(err) {
		return err
	}
	var sqliteError interface{ Code() int }
	if errors.As(err, &sqliteError) {
		switch sqliteError.Code() & 0xff {
		case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED:
			return newRevisionFenceError(
				migrationbackend.RevisionFenceFailureContended,
				fmt.Errorf("%s: %w", stage, err),
			)
		}
	}
	return err
}

func classifyRevisionIntegrityIO(stage string, err error) error {
	if err == nil {
		return nil
	}
	classified := classifyRevisionIO(stage, err)
	if migrationbackend.IsRevisionFenceError(classified) {
		return classified
	}
	return newRevisionFenceError(
		migrationbackend.RevisionFenceFailureIntegrity,
		fmt.Errorf("%s: %w", stage, classified),
	)
}

func migrationDetachedCleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		return context.WithTimeout(context.Background(), migrationCleanupTimeout)
	}
	return context.WithTimeout(context.WithoutCancel(ctx), migrationCleanupTimeout)
}

func discardMigrationConnection(connection migrationPinnedConnection) error {
	if connection == nil {
		return nil
	}
	err := connection.Raw(func(any) error { return driver.ErrBadConn })
	if errors.Is(err, driver.ErrBadConn) || errors.Is(err, sql.ErrConnDone) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("discard SQLite migration connection: %w", err)
	}
	return nil
}

func closeOrDiscardMigrationConnection(connection migrationPinnedConnection) error {
	if connection == nil {
		return nil
	}
	closeErr := connection.Close()
	if closeErr == nil {
		return nil
	}
	return errors.Join(
		wrapCloseMigrationConnection(closeErr),
		discardMigrationConnection(connection),
	)
}

func rollbackAndReleasePinnedMigration(ctx context.Context, connection migrationPinnedConnection) error {
	if connection == nil {
		return nil
	}
	cleanupCtx, cancel := migrationDetachedCleanupContext(ctx)
	defer cancel()
	_, rollbackErr := connection.ExecContext(cleanupCtx, "ROLLBACK")
	if rollbackErr == nil {
		return closeOrDiscardMigrationConnection(connection)
	}
	return errors.Join(
		classifyRevisionIO("rollback pinned migration transaction", wrapRollbackMigration(rollbackErr)),
		discardMigrationConnection(connection),
	)
}

func rejectLegacyMigrationWhenRevisionMetadataPresent(ctx context.Context, executor migrationSQLExecutor) error {
	objectType, present, err := sqliteSchemaObjectType(ctx, executor, migrationRevisionTable)
	if err != nil {
		return err
	}
	if !present {
		return nil
	}
	if objectType != "table" {
		return newRevisionFenceError(
			migrationbackend.RevisionFenceFailureIntegrity,
			fmt.Errorf("SQLite migration revision object is %q, want table", objectType),
		)
	}
	if _, err := inspectMigrationRevisionSnapshot(ctx, executor); err != nil {
		return err
	}
	return migrationbackend.NewCapabilityError(
		"revision_fenced_migration_lifecycle",
		"revision metadata exists; use the revision-fenced lifecycle instead of legacy BeginMigration",
		nil,
	)
}
