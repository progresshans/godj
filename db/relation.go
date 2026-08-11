package db

import (
	"context"

	"github.com/progresshans/godj/query"
)

// RelationMutator is the database-independent bulk SET_NULL boundary used by
// a relation-aware delete transaction.
type RelationMutator interface {
	RelationSetNull(context.Context, query.RelationSetNullPlan) (rowsAffected int64, err error)
}

// RelationSession is valid only during one AtomicRelation callback and binds
// reads, ordinary writes, and relation mutations to the same transaction.
type RelationSession interface {
	Session
	RelationMutator
}

// RelationAtomic executes one relation-delete callback on a transaction-bound
// RelationSession. A precondition or begin failure invokes callback zero times;
// otherwise implementations invoke it exactly once synchronously and preserve
// callback errors without committing.
type RelationAtomic interface {
	AtomicRelation(context.Context, func(RelationSession) error) error
}
