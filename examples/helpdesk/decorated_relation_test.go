package helpdesk_test

import (
	"context"
	"errors"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

// Existing statement counters/faults must run inside the new relation
// transaction, preserving its lifetime and native conflict capability.
type decoratedHelpdeskRelation struct {
	db.Session
	relation db.RelationSession
}

func (s decoratedHelpdeskRelation) ValidateSession(ctx context.Context) error {
	validator, ok := s.relation.(db.SessionValidator)
	if !ok {
		return errors.New("fixture requires session lifetime validation")
	}
	return validator.ValidateSession(ctx)
}
func (s decoratedHelpdeskRelation) RelationSetNull(ctx context.Context, plan query.RelationSetNullPlan) (int64, error) {
	return s.relation.RelationSetNull(ctx, plan)
}
func (s decoratedHelpdeskRelation) InsertOnConflict(ctx context.Context, plan query.ConflictInsertPlan) (bool, error) {
	inserter, ok := s.relation.(db.ConflictInserter)
	if !ok {
		return false, errors.New("fixture requires native conflict insertion")
	}
	return inserter.InsertOnConflict(ctx, plan)
}
