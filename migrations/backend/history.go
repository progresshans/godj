package backend

import "context"

// AppliedMigration is the backend-neutral transport identity returned by a
// durable migration history store. Semantic validation belongs to the
// migrations package, not to database backends.
type AppliedMigration struct {
	App  string
	Name string
}

// AppliedMigrationReader reads a snapshot of durable migration identities.
// It is intentionally separate from Recorder, Transaction, and AtomicBackend
// so read-only history does not widen the migration write boundary.
type AppliedMigrationReader interface {
	ReadAppliedMigrations(context.Context) ([]AppliedMigration, error)
}
