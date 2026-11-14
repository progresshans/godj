package identitytest

import (
	"context"
	"errors"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
)

// Opaque host errors need not have comparable dynamic values.
type resetOpaqueError []string

func (resetOpaqueError) Error() string { return "private-password-fault" }

type resetMutationBoundary struct {
	TransitionBackend
	mode                   string
	armed                  bool
	calls, faults, deletes int
	afterCommit            func()
}

func (b *resetMutationBoundary) CoordinatedAtomic(ctx context.Context, callback func(db.Session) error) error {
	if !b.armed {
		return b.TransitionBackend.CoordinatedAtomic(ctx, callback)
	}
	b.calls++
	if b.mode == "zero" {
		return nil
	}
	err := b.TransitionBackend.CoordinatedAtomic(ctx, func(session db.Session) error {
		if b.mode == "nil" {
			return callback(nil)
		}
		err := callback(&resetMutationSession{Session: session, owner: b})
		if b.mode == "swallow" {
			return nil
		}
		if err != nil {
			return err
		}
		if b.mode == "twice" {
			return callback(session)
		}
		if b.mode == "cancel" || b.mode == "unknown_rollback" {
			return context.Canceled
		}
		return nil
	})
	if b.mode == "unknown_rollback" {
		return errors.Join(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown, Detail: "private-password-fault"})
	}
	if b.mode == "collision_unknown" && errors.Is(err, &sessions.Error{Code: sessions.CodeEntropy}) {
		return errors.Join(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown})
	}
	if b.mode == "unknown_commit" && err == nil {
		return &query.Error{Code: query.CodeCommitOutcomeUnknown, Detail: "private-password-fault"}
	}
	if b.mode == "validation_cleanup" {
		return errors.Join(err, errors.New("private-password-fault"))
	}
	if b.mode == "late_cancel" && err == nil {
		b.afterCommit()
	}
	return err
}

type resetMutationSession struct {
	db.Session
	owner *resetMutationBoundary
}

func (s *resetMutationSession) fault() error {
	s.owner.faults++
	if s.owner.mode == "opaque" {
		return resetOpaqueError{"private-password-fault"}
	}
	return errors.New("private-password-fault")
}
func (s *resetMutationSession) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	if s.owner.mode == "swallow" || s.owner.mode == "opaque" {
		return nil, s.fault()
	}
	return s.Session.Query(ctx, plan)
}
func (s *resetMutationSession) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	if plan.Table() == "godj_system_session" && s.owner.mode == "session_insert" || plan.Table() == "godj_system_audit" && s.owner.mode == "audit_insert" {
		return 0, s.fault()
	}
	n, err := s.Session.Insert(ctx, plan)
	if err == nil && (plan.Table() == "godj_system_session" && s.owner.mode == "after_session" || plan.Table() == "godj_system_audit" && s.owner.mode == "after_audit") {
		return 0, s.fault()
	}
	return n, err
}
func (s *resetMutationSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	if plan.Table() == "godj_identity_user" && s.owner.mode == "password_write" {
		return 0, s.fault()
	}
	n, err := s.Session.Update(ctx, plan)
	if err == nil && plan.Table() == "godj_identity_user" && s.owner.mode == "after_password" {
		return 0, s.fault()
	}
	return n, err
}
func (s *resetMutationSession) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	n, err := s.Session.Delete(ctx, plan)
	if plan.Table() == "godj_system_session" {
		s.owner.deletes++
		if err == nil && (s.owner.mode == "after_rotation_delete" && s.owner.deletes == 1 || s.owner.mode == "revoke" && s.owner.deletes == 2) {
			return 0, s.fault()
		}
	}
	return n, err
}
