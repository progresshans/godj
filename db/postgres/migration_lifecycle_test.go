package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
)

func TestPostgresMigrationCapabilities(t *testing.T) {
	t.Parallel()
	capabilities := (*Backend)(nil).MigrationCapabilities()
	if !capabilities.CreateModelForeignKeys ||
		!capabilities.AddNullableForeignKey ||
		!capabilities.AddRequiredForeignKeyToEmptyTable ||
		!capabilities.RemoveForeignKey {
		t.Fatalf("capabilities = %+v", capabilities)
	}
}

func TestPostgresMigrationAdvisoryLockKeyIsStableAndSchemaScoped(t *testing.T) {
	t.Parallel()
	const want int64 = 8317792078680468416
	if got := postgresMigrationAdvisoryLockKey("godj_phase_d"); got != want {
		t.Fatalf("lock key = %d, want %d", got, want)
	}
	if got := postgresMigrationAdvisoryLockKey("godj_phase_d_other"); got == want {
		t.Fatalf("different namespace reused lock key %d", got)
	}
}

func TestPostgresRevisionExpectedSnapshot(t *testing.T) {
	t.Parallel()
	record := migrationbackend.AppliedMigration{App: "app", Name: "0001"}
	token := postgresMigrationRevisionToken{initialized: true, revision: 7}
	copy(token.epoch[:], []byte("0123456789abcdef"))
	token.fingerprint = fingerprintPostgresMigrationHistory([]migrationbackend.AppliedMigration{record})

	tests := []struct {
		name        string
		transaction *postgresRevisionFencedTransaction
		current     postgresMigrationRevisionSnapshot
		wantKind    migrationbackend.RevisionFenceFailureKind
	}{
		{
			name: "fresh",
			transaction: &postgresRevisionFencedTransaction{
				expectedRecords: []migrationbackend.AppliedMigration{},
			},
			current: postgresMigrationRevisionSnapshot{records: []migrationbackend.AppliedMigration{}},
		},
		{
			name: "fresh_changed",
			transaction: &postgresRevisionFencedTransaction{
				expectedRecords: []migrationbackend.AppliedMigration{},
			},
			current:  postgresMigrationRevisionSnapshot{recorderPresent: true},
			wantKind: migrationbackend.RevisionFenceFailureStale,
		},
		{
			name: "exact",
			transaction: &postgresRevisionFencedTransaction{
				expectedRecords: []migrationbackend.AppliedMigration{record},
				expectedToken:   token,
			},
			current: postgresMigrationRevisionSnapshot{
				revisionPresent: true,
				recorderPresent: true,
				records:         []migrationbackend.AppliedMigration{record},
				token:           token,
			},
		},
		{
			name: "aba_revision_changed",
			transaction: &postgresRevisionFencedTransaction{
				expectedRecords: []migrationbackend.AppliedMigration{record},
				expectedToken:   token,
			},
			current: postgresMigrationRevisionSnapshot{
				revisionPresent: true,
				recorderPresent: true,
				records:         []migrationbackend.AppliedMigration{record},
				token: func() postgresMigrationRevisionToken {
					changed := token
					changed.revision++
					return changed
				}(),
			},
			wantKind: migrationbackend.RevisionFenceFailureStale,
		},
		{
			name: "revision_disappeared",
			transaction: &postgresRevisionFencedTransaction{
				expectedRecords: []migrationbackend.AppliedMigration{record},
				expectedToken:   token,
			},
			current:  postgresMigrationRevisionSnapshot{},
			wantKind: migrationbackend.RevisionFenceFailureStale,
		},
		{
			name: "recorder_disappeared",
			transaction: &postgresRevisionFencedTransaction{
				expectedRecords: []migrationbackend.AppliedMigration{record},
				expectedToken:   token,
			},
			current: postgresMigrationRevisionSnapshot{
				revisionPresent: true,
				token:           token,
			},
			wantKind: migrationbackend.RevisionFenceFailureIntegrity,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.transaction.verifyExpectedSnapshot(test.current)
			if test.wantKind == 0 {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var fenceError *migrationbackend.RevisionFenceError
			if !errors.As(err, &fenceError) || fenceError == nil || fenceError.Kind != test.wantKind {
				t.Fatalf("error = %v, want revision kind %d", err, test.wantKind)
			}
		})
	}
}

func TestPostgresFencedCommitRollbackDurability(t *testing.T) {
	t.Parallel()
	primary := errors.New("schema operation failed")
	rollbackFailure := errors.New("rollback failed")
	tests := []struct {
		name           string
		rollbackErr    error
		wantDurability migrationbackend.CommitDurability
		wantDiscard    bool
	}{
		{name: "proven", wantDurability: migrationbackend.CommitRolledBack},
		{name: "unknown", rollbackErr: rollbackFailure, wantDurability: migrationbackend.CommitUnknown, wantDiscard: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fakeConnection := &fakePostgresMigrationConnection{rollbackErr: test.rollbackErr}
			session := &postgresRevisionFencedSession{state: postgresRevisionSessionActive}
			transaction := &postgresRevisionFencedTransaction{
				connection: fakeConnection,
				session:    session,
				failure:    primary,
			}
			session.active = transaction
			outcome, err := transaction.CommitFenced(context.Background())
			if outcome.Durability != test.wantDurability || !errors.Is(err, primary) {
				t.Fatalf("CommitFenced() = (%+v, %v), want durability %d and primary", outcome, err, test.wantDurability)
			}
			if test.rollbackErr != nil && !errors.Is(err, test.rollbackErr) {
				t.Fatalf("CommitFenced() lost rollback error: %v", err)
			}
			if fakeConnection.rollbackCalls != 1 || fakeConnection.commitCalls != 0 {
				t.Fatalf("transaction calls commit=%d rollback=%d", fakeConnection.commitCalls, fakeConnection.rollbackCalls)
			}
			if fakeConnection.rawCalls != boolInt(test.wantDiscard) {
				t.Fatalf("discard calls = %d, want %t", fakeConnection.rawCalls, test.wantDiscard)
			}
			if session.state != postgresRevisionSessionPoisoned || session.active != nil {
				t.Fatalf("session state=%d active=%v", session.state, session.active)
			}
		})
	}
}

func TestPostgresFencedLiteralCommitErrorIsAlwaysUnknown(t *testing.T) {
	t.Parallel()
	commitFailure := errors.New("connection lost after COMMIT")
	fakeConnection := &fakePostgresMigrationConnection{commitErr: commitFailure}
	transaction := &postgresRevisionFencedTransaction{
		connection: fakeConnection,
	}
	outcome, err := transaction.commitVerifiedLocked(context.Background())
	if outcome.Durability != migrationbackend.CommitUnknown || !errors.Is(err, commitFailure) {
		t.Fatalf("commitVerifiedLocked() = (%+v, %v)", outcome, err)
	}
	if fakeConnection.commitCalls != 1 || fakeConnection.rollbackCalls != 0 {
		t.Fatalf("transaction calls commit=%d rollback=%d", fakeConnection.commitCalls, fakeConnection.rollbackCalls)
	}
	if fakeConnection.rawCalls != 1 || fakeConnection.closeCalls != 0 {
		t.Fatalf("connection calls raw=%d close=%d", fakeConnection.rawCalls, fakeConnection.closeCalls)
	}
}

func TestPostgresFencedCommittedCleanupErrorPreservesDurability(t *testing.T) {
	t.Parallel()
	closeFailure := errors.New("release failed")
	fakeConnection := &fakePostgresMigrationConnection{closeErr: closeFailure}
	transaction := &postgresRevisionFencedTransaction{
		connection: fakeConnection,
	}
	outcome, err := transaction.commitVerifiedLocked(context.Background())
	if outcome.Durability != migrationbackend.CommitCommitted || !errors.Is(err, closeFailure) {
		t.Fatalf("commitVerifiedLocked() = (%+v, %v)", outcome, err)
	}
	if fakeConnection.commitCalls != 1 || fakeConnection.rollbackCalls != 0 {
		t.Fatalf("transaction calls commit=%d rollback=%d", fakeConnection.commitCalls, fakeConnection.rollbackCalls)
	}
	if fakeConnection.closeCalls != 1 || fakeConnection.rawCalls != 1 {
		t.Fatalf("connection calls close=%d raw=%d", fakeConnection.closeCalls, fakeConnection.rawCalls)
	}
}

func TestPostgresFencedRollbackUsesDetachedBoundedContext(t *testing.T) {
	t.Parallel()
	fakeConnection := &fakePostgresMigrationConnection{}
	session := &postgresRevisionFencedSession{state: postgresRevisionSessionActive}
	transaction := &postgresRevisionFencedTransaction{connection: fakeConnection, session: session}
	session.active = transaction
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := transaction.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if fakeConnection.rollbackCalls != 1 || fakeConnection.rollbackContextErr != nil {
		t.Fatalf("rollback calls=%d context error=%v", fakeConnection.rollbackCalls, fakeConnection.rollbackContextErr)
	}
	if !fakeConnection.rollbackHasDeadline ||
		fakeConnection.rollbackDeadline.Sub(time.Now()) <= 0 ||
		fakeConnection.rollbackDeadline.Sub(time.Now()) > postgresMigrationCleanupTimeout {
		t.Fatalf("rollback deadline=%v present=%t", fakeConnection.rollbackDeadline, fakeConnection.rollbackHasDeadline)
	}
}

func TestPostgresMigrationControlBootstrapAndCASAreQualified(t *testing.T) {
	t.Parallel()
	fakeConnection := &fakePostgresMigrationConnection{rowsAffected: 1}
	session := &postgresRevisionFencedSession{backend: &Backend{schema: "phase_d"}}
	transaction := &postgresRevisionFencedTransaction{
		connection: fakeConnection,
		session:    session,
		bootstrap:  true,
		successorToken: postgresMigrationRevisionToken{
			initialized: true,
			revision:    1,
		},
	}
	if err := transaction.claimRevision(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fakeConnection.statements) != 3 {
		t.Fatalf("bootstrap statements = %v", fakeConnection.statements)
	}
	for _, check := range []struct {
		index int
		want  string
	}{
		{index: 0, want: `CREATE TABLE "phase_d"."godj_migrations"`},
		{index: 1, want: `CREATE TABLE "phase_d"."godj_migration_revision"`},
		{index: 2, want: `INSERT INTO "phase_d"."godj_migration_revision"`},
	} {
		if !strings.HasPrefix(fakeConnection.statements[check.index], check.want) {
			t.Fatalf("statement[%d] = %q, want prefix %q", check.index, fakeConnection.statements[check.index], check.want)
		}
	}

	fakeConnection.statements = nil
	transaction.bootstrap = false
	transaction.expectedToken = transaction.successorToken
	transaction.successorToken.revision = 2
	if err := transaction.claimRevision(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fakeConnection.statements) != 1 ||
		!strings.HasPrefix(fakeConnection.statements[0], `UPDATE "phase_d"."godj_migration_revision"`) {
		t.Fatalf("CAS statements = %v", fakeConnection.statements)
	}
}

func TestPostgresRecorderFailuresForceRollbackAndPoison(t *testing.T) {
	t.Parallel()

	execFailure := errors.New("recorder execution failed")
	rowsFailure := errors.New("recorder row count failed")
	for _, test := range []struct {
		name            string
		kind            migrationbackend.HistoryTransitionKind
		execErr         error
		rowsAffectedErr error
		rowsAffected    int64
		wantCause       error
	}{
		{name: "apply_execution", kind: migrationbackend.HistoryTransitionApply, execErr: execFailure, wantCause: execFailure},
		{name: "apply_row_count", kind: migrationbackend.HistoryTransitionApply, rowsAffectedErr: rowsFailure, wantCause: rowsFailure},
		{name: "apply_zero", kind: migrationbackend.HistoryTransitionApply, rowsAffected: 0},
		{name: "apply_two", kind: migrationbackend.HistoryTransitionApply, rowsAffected: 2},
		{name: "unapply_execution", kind: migrationbackend.HistoryTransitionUnapply, execErr: execFailure, wantCause: execFailure},
		{name: "unapply_row_count", kind: migrationbackend.HistoryTransitionUnapply, rowsAffectedErr: rowsFailure, wantCause: rowsFailure},
		{name: "unapply_zero", kind: migrationbackend.HistoryTransitionUnapply, rowsAffected: 0},
		{name: "unapply_two", kind: migrationbackend.HistoryTransitionUnapply, rowsAffected: 2},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			transition := migrationbackend.HistoryTransition{
				Migration: migrationbackend.AppliedMigration{App: "app", Name: "0001"},
				Kind:      test.kind,
			}
			schema, err := newPostgresMigrationSchema(
				transition,
				migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{}},
			)
			if err != nil {
				t.Fatal(err)
			}
			schema.namespace = "phase_d"
			schema.preflight = true

			connection := &fakePostgresMigrationConnection{
				execErr:         test.execErr,
				rowsAffectedErr: test.rowsAffectedErr,
				rowsAffected:    test.rowsAffected,
			}
			session := &postgresRevisionFencedSession{
				backend: &Backend{schema: "phase_d"},
				state:   postgresRevisionSessionActive,
			}
			transaction := &postgresRevisionFencedTransaction{
				connection: connection,
				session:    session,
				schema:     schema,
				transition: transition,
			}
			session.active = transaction

			if test.kind == migrationbackend.HistoryTransitionApply {
				err = transaction.RecordApplied(context.Background(), "app", "0001")
			} else {
				err = transaction.RecordUnapplied(context.Background(), "app", "0001")
			}
			if err == nil {
				t.Fatal("recorder failure = nil")
			}
			if test.wantCause != nil && !errors.Is(err, test.wantCause) {
				t.Fatalf("recorder error = %v, want cause %v", err, test.wantCause)
			}
			if test.wantCause == nil {
				var fenceError *migrationbackend.RevisionFenceError
				if !errors.As(err, &fenceError) || fenceError.Kind != migrationbackend.RevisionFenceFailureIntegrity {
					t.Fatalf("row-count error = %v, want integrity", err)
				}
			}

			outcome, commitErr := transaction.CommitFenced(context.Background())
			if outcome.Durability != migrationbackend.CommitRolledBack || !errors.Is(commitErr, err) {
				t.Fatalf("CommitFenced() = (%+v, %v), want rolled back with recorder error %v", outcome, commitErr, err)
			}
			if connection.commitCalls != 0 || connection.rollbackCalls != 1 {
				t.Fatalf("transaction calls commit=%d rollback=%d", connection.commitCalls, connection.rollbackCalls)
			}
			if connection.closeCalls != 1 || connection.rawCalls != 0 {
				t.Fatalf("connection release close=%d discard=%d", connection.closeCalls, connection.rawCalls)
			}
			if session.state != postgresRevisionSessionPoisoned || session.active != nil {
				t.Fatalf("session state=%d active=%v", session.state, session.active)
			}
		})
	}
}

func TestPostgresRevisionSessionCloseIsIdempotent(t *testing.T) {
	t.Parallel()
	session := &postgresRevisionFencedSession{state: postgresRevisionSessionOpen}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if session.state != postgresRevisionSessionClosed {
		t.Fatalf("state = %d", session.state)
	}
}

type fakePostgresMigrationConnection struct {
	commitErr           error
	rollbackErr         error
	closeErr            error
	rawErr              error
	execErr             error
	rowsAffectedErr     error
	rowsAffected        int64
	commitCalls         int
	rollbackCalls       int
	closeCalls          int
	rawCalls            int
	statements          []string
	rollbackContextErr  error
	rollbackDeadline    time.Time
	rollbackHasDeadline bool
}

func (connection *fakePostgresMigrationConnection) ExecContext(
	ctx context.Context,
	statement string,
	_ ...any,
) (sql.Result, error) {
	connection.statements = append(connection.statements, statement)
	switch statement {
	case "COMMIT":
		connection.commitCalls++
		return driver.RowsAffected(0), connection.commitErr
	case "ROLLBACK":
		connection.rollbackCalls++
		connection.rollbackContextErr = ctx.Err()
		connection.rollbackDeadline, connection.rollbackHasDeadline = ctx.Deadline()
		return driver.RowsAffected(0), connection.rollbackErr
	default:
		if connection.execErr != nil {
			return nil, connection.execErr
		}
		return fakePostgresMigrationResult{
			rowsAffected: connection.rowsAffected,
			err:          connection.rowsAffectedErr,
		}, nil
	}
}

type fakePostgresMigrationResult struct {
	rowsAffected int64
	err          error
}

func (result fakePostgresMigrationResult) LastInsertId() (int64, error) {
	return 0, errors.New("LastInsertId is unsupported")
}

func (result fakePostgresMigrationResult) RowsAffected() (int64, error) {
	return result.rowsAffected, result.err
}

func (*fakePostgresMigrationConnection) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errors.New("unexpected QueryContext")
}

func (*fakePostgresMigrationConnection) QueryRowContext(context.Context, string, ...any) *sql.Row {
	return &sql.Row{}
}

func (connection *fakePostgresMigrationConnection) Raw(callback func(any) error) error {
	connection.rawCalls++
	if connection.rawErr != nil {
		return connection.rawErr
	}
	return callback(nil)
}

func (connection *fakePostgresMigrationConnection) Close() error {
	connection.closeCalls++
	return connection.closeErr
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
