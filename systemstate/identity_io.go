package systemstate

import (
	"context"

	"github.com/progresshans/godj/db"
)

var _ db.SnapshotReader = (*Runtime)(nil)

// ReadSnapshot delegates to the native stable snapshot. Identity password work
// runs only after this scope has ended; it must not borrow Runtime's write gate.
func (runtime *Runtime) ReadSnapshot(ctx context.Context, callback func(db.Queryer) error) error {
	if err := runtime.validBackendCall(ctx); err != nil {
		return err
	}
	backend, ok := runtime.backend.(db.SnapshotReader)
	if !ok || isNilInterface(backend) {
		return &Error{Code: CodeInvalidConfig, Field: "snapshot_backend", Detail: "native read snapshots are unavailable"}
	}
	if callback == nil {
		return &Error{Code: CodeInvalidInput, Field: "callback", Detail: "snapshot callback is nil"}
	}
	return backend.ReadSnapshot(ctx, func(scope db.Queryer) error {
		if err := runtime.requireHistoricalOwnership(ctx, scope); err != nil {
			return err
		}
		return callback(scope)
	})
}

// CoordinatedAtomic exposes the same fence used by Runtime.Atomic, sessions and
// audit to identity maintenance. All effects use the one borrowed Session.
func (runtime *Runtime) CoordinatedAtomic(ctx context.Context, callback func(db.Session) error) error {
	return runtime.withAtomic(ctx, callback)
}

// CoordinatedAtomicRelation exposes that same domain with the borrowed
// capabilities needed by identity collections and host relation deletion.
func (runtime *Runtime) CoordinatedAtomicRelation(ctx context.Context, callback func(db.RelationSession) error) error {
	return runtime.AtomicRelation(ctx, callback)
}
