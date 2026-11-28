package articleapp

import (
	"context"
	"errors"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

// InScope borrows a caller-owned native snapshot or write transaction. It
// neither opens a nested transaction nor confirms a commit. Callers authorize
// and prepare all output in that scope, propagate every error, and publish only
// after the outer owner confirms cleanup or commit. Do not retain this value.
func InScope(ctx context.Context, scope db.Queryer) (Repository, error) {
	if interfaceNil(ctx) || interfaceNil(scope) {
		return Repository{}, invalid("scope", "a live borrowed database scope is required")
	}
	validator, ok := scope.(db.SessionValidator)
	if !ok || interfaceNil(validator) {
		return Repository{}, invalid("scope", "a live borrowed database scope is required")
	}
	if err := errors.Join(ctx.Err(), validator.ValidateSession(ctx)); err != nil {
		return Repository{}, err
	}
	return NewRepository(borrowedBackend{scope})
}

type borrowedBackend struct{ db.Queryer }

func (b borrowedBackend) session(ctx context.Context) (db.Session, error) {
	session, ok := b.Queryer.(db.Session)
	validator, validated := b.Queryer.(db.SessionValidator)
	if !ok || !validated || interfaceNil(ctx) {
		return nil, invalid("scope", "a live write scope is required")
	}
	if err := errors.Join(ctx.Err(), validator.ValidateSession(ctx)); err != nil {
		return nil, err
	}
	return session, nil
}

func (b borrowedBackend) Atomic(ctx context.Context, run func(db.Session) error) error {
	if run == nil {
		return invalid("scope", "a write callback is required")
	}
	session, err := b.session(ctx)
	if err != nil {
		return err
	}
	err = run(session)
	_, ended := b.session(ctx)
	if ended == nil {
		return err
	}
	return errors.Join(err, ended)
}
func (b borrowedBackend) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	session, err := b.session(ctx)
	if err != nil {
		return 0, err
	}
	return session.Insert(ctx, plan)
}
func (b borrowedBackend) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	session, err := b.session(ctx)
	if err != nil {
		return 0, err
	}
	return session.Update(ctx, plan)
}
func (b borrowedBackend) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	session, err := b.session(ctx)
	if err != nil {
		return 0, err
	}
	return session.Delete(ctx, plan)
}
