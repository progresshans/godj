package db

import (
	"context"

	"github.com/progresshans/godj/query"
)

// BatchQueryer reads one source query in positive, explicitly sized batches.
// It is an optional execution capability, separate from ordinary Query.
//
// scan decodes one row into caller-owned batch storage. It must not execute
// other database work or retain the row or driver-owned values. Once a whole
// batch is decoded and its transport is ready for further queries, yield is
// called with the query executor to use for related reads. yield may also use
// any write capabilities that executor advertises. Returning false stops
// successfully; an error from either callback stops without retrying.
//
// No yield occurs for an empty result or a failed batch. Earlier yielded
// batches remain observable after a later failure. No full-result cache is
// consulted or populated. Source order, multiplicity and limits are preserved;
// implementations must not replace the source with repeated OFFSET queries.
//
// Interleaved database work uses the executor supplied to yield. Native root
// executors share the pinned source connection during the stream, then return
// to their original backend after it ends. They do not advertise a borrowed
// SessionValidator. Direct query rowsets belong to the callback and are closed
// before its executor advances the source. Consume and close them before
// starting another operation on that connection. Calls on the active executor
// are serialized by the caller; retained root executors can be shared once the
// stream has ended under the original backend's concurrency contract.
//
// Calls are synchronous. All owned rows/cursors close on return or panic, and
// the original panic propagates. Borrowed sessions keep their existing lifetime
// and transaction owner: this operation does not commit or roll them back.
type BatchQueryer interface {
	QueryBatches(ctx context.Context, plan query.Plan, size int, scan func(Row) error, yield func(Queryer) (bool, error)) error
}
