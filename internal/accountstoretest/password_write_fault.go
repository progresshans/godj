package accountstoretest

import (
	"context"
	"errors"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type passwordWriteFaultScope struct {
	db.Session
	mode              string
	fault             error
	effects           *[]string
	faults            *int
	revokeAt, revokes int
}

type passwordWriteOpaqueError []string

func (passwordWriteOpaqueError) Error() string { return "private password opaque fault" }

func (scope *passwordWriteFaultScope) ValidateSession(ctx context.Context) error {
	return scope.Session.(db.SessionValidator).ValidateSession(ctx)
}
func (scope *passwordWriteFaultScope) fail() error { (*scope.faults)++; return scope.fault }
func (scope *passwordWriteFaultScope) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	if scope.mode == "query" {
		return nil, scope.fail()
	}
	return scope.Session.Query(ctx, plan)
}
func (scope *passwordWriteFaultScope) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	name := "insert:" + plan.Table()
	if plan.Table() == "godj_sessions_session" {
		name = "session"
	}
	if plan.Table() == "godj_account_audit_event" {
		name = "audit"
	}
	if scope.mode == "before_"+name {
		return 0, scope.fail()
	}
	key, err := scope.Session.Insert(ctx, plan)
	if err == nil {
		if key <= 0 {
			return 0, errors.New("password native insert did not allocate a key")
		}
		*scope.effects = append(*scope.effects, name)
		if scope.mode == "after_"+name {
			return 0, scope.fail()
		}
	}
	return key, err
}
func (scope *passwordWriteFaultScope) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	name := "update:" + plan.Table()
	if plan.Table() == "godj_identity_user" {
		name = "password"
	}
	if scope.mode == "before_"+name {
		return 0, scope.fail()
	}
	count, err := scope.Session.Update(ctx, plan)
	if err == nil {
		if count != 1 {
			return 0, errors.New("password native update did not affect one row")
		}
		*scope.effects = append(*scope.effects, name)
		if scope.mode == "after_"+name {
			return 0, scope.fail()
		}
	}
	return count, err
}
func (scope *passwordWriteFaultScope) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	name := "delete:" + plan.Table()
	if plan.Table() == "godj_sessions_session" {
		name = "revoke"
		if len(*scope.effects) == 0 {
			name = "rotation"
		}
	}
	if scope.mode == "before_"+name {
		return 0, scope.fail()
	}
	count, err := scope.Session.Delete(ctx, plan)
	if err == nil {
		if count != 1 {
			return 0, errors.New("password native delete did not affect one row")
		}
		*scope.effects = append(*scope.effects, name)
		if name == "revoke" {
			scope.revokes++
		}
		if scope.mode == "after_"+name && (name != "revoke" || scope.revokeAt == 0 || scope.revokes == scope.revokeAt) {
			return 0, scope.fail()
		}
	}
	return count, err
}
