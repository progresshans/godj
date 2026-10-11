package db

import "context"

// SnapshotReader executes callback once against one stable database read
// snapshot. All reads after the first observation see that same committed
// state, including when another connection changes rows between queries.
// The borrowed Queryer exposes no mutation or transaction capability and
// expires when callback returns. Callers must finish using rows in callback.
// Callbacks use only that handle; opening another transaction on the same
// backend may wait for resources or admission owned by the current snapshot.
//
// A nil callback or canceled context is rejected before opening a transaction.
// Callback, cancellation and cleanup errors are returned without automatic
// retry. A failed read-snapshot cleanup is not a write commit-unknown receipt:
// no writes are admitted. Callers publish captured results only on nil error.
type SnapshotReader interface {
	ReadSnapshot(context.Context, func(Queryer) error) error
}
