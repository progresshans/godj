// Package backend defines the database boundary used by the migration core.
// Implementations must execute schema edits and recorder writes through the
// same transaction object.
package backend

import (
	"context"
	"errors"

	"github.com/progresshans/godj/schema/ir"
)

// SchemaEditor applies a normalized Schema IR change to a database schema.
type SchemaEditor interface {
	CreateModel(context.Context, ir.Model) error
	DeleteModel(context.Context, ir.Model) error
	AddField(context.Context, ir.Model, ir.Field) error
	RemoveField(context.Context, ir.Model, ir.Field) error
}

// Recorder maintains the durable app/name key for applied migrations.
type Recorder interface {
	RecordApplied(context.Context, string, string) error
	RecordUnapplied(context.Context, string, string) error
}

// Transaction binds schema editing and recorder updates to one atomic
// database transaction.
type Transaction interface {
	SchemaEditor
	Recorder
	Commit(context.Context) error
	Rollback(context.Context) error
}

// AtomicBackend starts a migration transaction. The caller commits successful
// work and makes a best-effort rollback after any operation, recorder, or
// commit failure.
type AtomicBackend interface {
	BeginMigration(context.Context) (Transaction, error)
}

// CapabilityError reports a backend-specific operation that cannot be
// represented safely. Backends must return this instead of silently skipping
// a schema change or applying an unsafe fallback.
type CapabilityError struct {
	Feature string
	Detail  string
	Cause   error
}

func (e *CapabilityError) Error() string {
	if e == nil {
		return "migration backend capability error"
	}
	message := "migration backend does not support " + e.Feature
	if e.Detail != "" {
		message += ": " + e.Detail
	}
	if e.Cause != nil {
		message += ": " + e.Cause.Error()
	}
	return message
}

func (e *CapabilityError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func NewCapabilityError(feature, detail string, cause error) *CapabilityError {
	if feature == "" {
		feature = "unknown_operation"
	}
	return &CapabilityError{Feature: feature, Detail: detail, Cause: cause}
}

func IsCapabilityError(err error) bool {
	var capabilityError *CapabilityError
	return errors.As(err, &capabilityError)
}
