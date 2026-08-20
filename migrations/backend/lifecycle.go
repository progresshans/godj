package backend

import (
	"context"
	"errors"
	"fmt"
)

// HistoryTransitionKind declares the recorder transition that a fenced
// migration transaction must commit together with its schema changes.
type HistoryTransitionKind uint8

const (
	HistoryTransitionApply HistoryTransitionKind = iota + 1
	HistoryTransitionUnapply
)

// HistoryTransition binds a migration identity to the recorder mutation that
// a fenced transaction must perform exactly once before it can commit.
type HistoryTransition struct {
	Migration AppliedMigration
	Kind      HistoryTransitionKind
}

// RevisionFencedBackend is the backend capability required by the high-level
// migration lifecycle. Every implementation reports its current migration
// feature set through the same port used to open a fenced session.
type RevisionFencedBackend interface {
	MigrationCapabilities() MigrationCapabilities
	OpenRevisionFencedSession(context.Context) (RevisionFencedSession, error)
}

// RevisionFencedSession owns one atomic applied-history snapshot and its
// private freshness token. Implementations must reject more than one snapshot
// read and must advance their token only after a committed fenced transaction.
type RevisionFencedSession interface {
	AppliedMigrationReader
	BeginMigration(context.Context, HistoryTransition, MigrationIntent) (RevisionFencedTransaction, error)
	Close(context.Context) error
}

// CommitDurability reports the durable state established by CommitFenced.
// Its zero value is invalid and must be treated as an unknown outcome.
type CommitDurability uint8

const (
	CommitRolledBack CommitDurability = iota + 1
	CommitCommitted
	CommitUnknown
)

// CommitOutcome is independent from the error returned by CommitFenced: a
// backend may prove that the migration committed even when terminal connection
// cleanup subsequently fails.
type CommitOutcome struct {
	Durability CommitDurability
}

// RevisionFencedTransaction binds schema edits, exactly one declared recorder
// transition, and the successor revision in one backend transaction.
type RevisionFencedTransaction interface {
	SchemaEditor
	Recorder
	CommitFenced(context.Context) (CommitOutcome, error)
	Rollback(context.Context) error
}

// RevisionFenceFailureKind is the backend-neutral raw failure class translated
// by the migrations package into its public error taxonomy.
type RevisionFenceFailureKind uint8

const (
	RevisionFenceFailureAdoptionRequired RevisionFenceFailureKind = iota + 1
	RevisionFenceFailureStale
	RevisionFenceFailureContended
	RevisionFenceFailureIntegrity
)

// RevisionFenceError carries a raw revision-fence failure across the backend
// boundary without importing the top-level migrations package.
type RevisionFenceError struct {
	Kind  RevisionFenceFailureKind
	Cause error
}

func (e *RevisionFenceError) Error() string {
	if e == nil {
		return "migration revision fence error"
	}
	message := fmt.Sprintf("migration revision fence failure kind %d", e.Kind)
	if e.Cause != nil {
		message += ": " + e.Cause.Error()
	}
	return message
}

func (e *RevisionFenceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// IsRevisionFenceError reports whether err contains a raw revision-fence
// carrier, including a malformed typed-nil carrier that core must fail closed
// as an integrity error.
func IsRevisionFenceError(err error) bool {
	var fenceError *RevisionFenceError
	return errors.As(err, &fenceError)
}
