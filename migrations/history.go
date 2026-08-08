package migrations

import (
	"context"
	"errors"
	"fmt"

	"github.com/progresshans/godj/migrations/backend"
)

const CodeReadFailed ErrorCode = "read_failed"

// RecorderError reports a failure to obtain a durable applied-migration
// snapshot. Invalid identities returned by a successful read remain
// PlanningError values so recorder I/O and migration semantics stay distinct.
type RecorderError struct {
	Category ErrorCategory
	Code     ErrorCode
	Cause    error
}

func (e *RecorderError) Error() string {
	if e == nil {
		return "migration recorder error"
	}
	message := fmt.Sprintf("%s/%s", e.Category, e.Code)
	if e.Cause != nil {
		message += ": " + e.Cause.Error()
	}
	return message
}

// Unwrap preserves cancellation, driver, and reader-specific causes for
// errors.Is and errors.As without changing the recorder error classification.
func (e *RecorderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// LoadAppliedState reads, copies, and validates a durable migration history
// snapshot. Cancellation is checked before calling the reader. Once a reader
// has returned a successful snapshot, cancellation arriving afterward does not
// invalidate that snapshot.
func LoadAppliedState(ctx context.Context, reader backend.AppliedMigrationReader) (AppliedState, error) {
	if ctx == nil {
		return AppliedState{}, newRecorderReadError(errors.New("context is nil"))
	}
	if err := ctx.Err(); err != nil {
		return AppliedState{}, newRecorderReadError(err)
	}
	if isNilInterface(reader) {
		return AppliedState{}, newRecorderReadError(errors.New("applied migration reader is nil"))
	}

	records, err := reader.ReadAppliedMigrations(ctx)
	if err != nil {
		return AppliedState{}, newRecorderReadError(err)
	}
	keys := make([]MigrationKey, len(records))
	for index, record := range records {
		keys[index] = MigrationKey{App: record.App, Name: record.Name}
	}
	return NewAppliedState(keys...)
}

func newRecorderReadError(cause error) *RecorderError {
	return &RecorderError{
		Category: CategoryRecorder,
		Code:     CodeReadFailed,
		Cause:    cause,
	}
}
