package backend

import (
	"context"
	"errors"

	"github.com/progresshans/godj/migrations/internal/datascope"
)

// DataSession is borrowed from the enclosing migration transaction. A native
// backend invalidates it and closes outstanding results before returning.
type DataSession = datascope.Session

// DataScope is the opaque native authority for the checked model inventory.
type DataScope = datascope.Scope
type DataCallback func(context.Context, DataScope) error

// HistoricalDataTransaction consumes a RunData operation at its exact
// logical position in the sealed migration intent.
type HistoricalDataTransaction interface {
	RunData(context.Context, int, DataCallback) error
}

// ValidateRequiredHistory requires a persisted definition identity before any
// data callback, field rename or authored model deletion can execute.
func ValidateRequiredHistory(intent MigrationIntent, transition HistoryTransition) error {
	for _, op := range intent.Operations {
		if op.Kind == MigrationRunData || op.Kind == MigrationRenameField ||
			op.Kind == MigrationDeleteModel && transition.Kind == HistoryTransitionApply ||
			op.Kind == MigrationCreateModel && transition.Kind == HistoryTransitionUnapply {
			_, err := ParseDefinitionHistoryDigest(transition.Migration.HistoryDigest)
			return err
		}
	}
	return nil
}

// InvokeDataCallback constructs the public model scope only after the backend
// has verified the catalog and operation seal. Its owner controls the session
// lifetime and the single commit for schema, data and recorder changes.
func InvokeDataCallback(ctx context.Context, session DataSession, scope []MigrationModel, callback DataCallback) error {
	if ctx == nil || session == nil || callback == nil {
		return errors.New("invalid historical data callback or session")
	}
	if err := session.ValidateSession(ctx); err != nil {
		return err
	}
	if err := callback(ctx, datascope.New(session, scope)); err != nil {
		return err
	}
	return session.ValidateSession(ctx)
}
