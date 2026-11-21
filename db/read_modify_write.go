package db

import "context"

// ReadModifyWritePolicy describes a native transaction's protection against
// overwriting a value changed since it was read. Zero and unknown policies do
// not authorize read-modify-write operations.
type ReadModifyWritePolicy string

const (
	// ReadModifyWriteRowLock requires the operation to read its source row with
	// a write lock. After a savepoint rollback, a fresh statement can see a
	// concurrent creator's committed row and acquire that row's lock.
	ReadModifyWriteRowLock ReadModifyWritePolicy = "row_lock"
	// ReadModifyWriteConflict uses the native transaction's read/write conflict
	// detection. A conflicting write must fail, never silently overwrite the
	// newer row. Busy/serialization errors are not an instruction to retry.
	ReadModifyWriteConflict ReadModifyWritePolicy = "transaction_conflict"
)

// ReadModifyWriteSession is an optional capability of a live writable session,
// not of an autocommit backend. Adapters must preserve the underlying native
// guarantee and lifetime validation. A read-only snapshot cannot advertise it.
type ReadModifyWriteSession interface {
	Session
	SessionValidator
	ReadModifyWritePolicy(context.Context) (ReadModifyWritePolicy, error)
}
