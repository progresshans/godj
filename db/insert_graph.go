package db

import (
	"context"

	"github.com/progresshans/godj/query"
)

// InsertGraphLimits applies to the complete graph, not to a batch which the
// caller can split: all its rows must fit in one native statement.
type InsertGraphLimits struct {
	Rows       int
	Parameters int
}

// GraphInserter belongs only to a live borrowed write session. It inserts a
// complete graph using the declared tables' native integer AutoField keys and
// unchanged native constraint timing. The caller supplies all keys and values;
// it ordinarily obtains new keys from AutoKeyAllocator in the same scope.
//
// The result owns one actual INSERT key per input row, in input order. Every
// key must equal the requested key and still exist after the whole statement.
// The enclosing owner still validates complete typed rows, policy and topology
// before commit; this method is not an authorization or commit receipt.
//
// An internal savepoint contains the statement, result verification and any
// temporary resources. No partial result escapes an error. A confirmed rollback
// preserves earlier parent work and permits a new attempt chosen by the caller;
// unconfirmed savepoint cleanup poisons the parent and requires root rollback.
// Calls require parent cursors and operations to have finished, and retained
// handles expire with their original session. Root backends and read snapshots
// do not advertise this capability.
type GraphInserter interface {
	SessionValidator
	InsertGraphLimits(context.Context) (InsertGraphLimits, error)
	InsertGraph(context.Context, query.InsertGraphPlan) ([]int64, error)
}
