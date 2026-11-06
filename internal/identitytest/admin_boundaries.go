package identitytest

import (
	"context"
	"errors"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/systemstate"
)

type adminMutationBoundary struct {
	*systemstate.Runtime
	mode          string
	calls, faults int
	before        func(context.Context) error
}

func (b *adminMutationBoundary) after(err error) error {
	if err == nil && b.mode == "unknown_commit" {
		return &query.Error{Code: query.CodeCommitOutcomeUnknown, Detail: "private-admin-fault"}
	}
	return err
}
func (b *adminMutationBoundary) CoordinatedAtomicRelation(ctx context.Context, callback func(db.RelationSession) error) error {
	b.calls++
	if b.before != nil {
		if err := b.before(ctx); err != nil {
			return err
		}
	}
	return b.after(b.Runtime.CoordinatedAtomicRelation(ctx, callback))
}
func (b *adminMutationBoundary) CoordinatedAtomic(ctx context.Context, callback func(db.Session) error) error {
	b.calls++
	if b.before != nil {
		if err := b.before(ctx); err != nil {
			return err
		}
	}
	return b.after(b.Runtime.CoordinatedAtomic(ctx, callback))
}
func (b *adminMutationBoundary) AppendAudit(ctx context.Context, session db.Session, event admin.PreparedEvent) error {
	if b.mode == "audit_failure" {
		b.faults++
		return errors.New("private-admin-fault")
	}
	return b.Runtime.AppendAudit(ctx, session, event)
}
