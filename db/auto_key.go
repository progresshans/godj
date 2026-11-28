package db

import (
	"context"

	"github.com/progresshans/godj/query"
)

// AutoKeyAllocator is an optional capability of a live borrowed write session,
// never a root backend or a read snapshot. It advances the declared table's
// actual native allocator and returns exactly Count distinct keys in allocation
// order. Zero and negative native keys remain valid. It does not insert a row,
// confer authorization or promise a gapless sequence.
// Callers serialize native operations, including cursor Next/Scan, against the
// entire reservation. An idle rowset is not itself an in-flight scope operation;
// its native connection and savepoint lifetime rules continue to apply.
//
// A reservation is provisional until the transaction owner confirms commit.
// No partial key list is returned on I/O, shape, scope or cancellation failure.
// PostgreSQL sequence consumption survives rollback, just as with a rolled-back
// INSERT; SQLite's allocator changes roll back with the transaction/savepoint.
// Callers must propagate errors to their owner and still require an INSERT of
// each intended row with native constraints. Explicit external writes or a
// separately reset sequence can conflict after allocation; reservation never
// replaces that final primary-key check or authorizes an overwrite.
type AutoKeyAllocator interface {
	SessionValidator
	ReserveAutoKeys(context.Context, query.AutoKeyPlan) ([]int64, error)
}
