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
// Calls are synchronous. All owned rows/cursors close on return or panic, and
// the original panic propagates. Borrowed sessions keep their existing lifetime
// and transaction owner: this operation does not commit or roll them back.
type BatchQueryer interface {
	QueryBatches(ctx context.Context, plan query.Plan, size int, scan func(Row) error, yield func(Queryer) (bool, error)) error
}
