package identitytest

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/validation"
)

type policyRollbackBoundary struct {
	*systemstate.Runtime
	mode  string
	calls int
}

func (b *policyRollbackBoundary) after(err error) error {
	b.calls++
	if _, rejected := validation.Rejected(err); !rejected {
		return err
	}
	switch b.mode {
	case "cleanup":
		return errors.Join(err, errors.New("private-admin-fault"))
	case "swallowed":
		return nil
	case "unknown":
		return errors.Join(err, &query.Error{Code: query.CodeCommitOutcomeUnknown, Detail: "private-admin-fault"})
	default:
		return err
	}
}
func (b *policyRollbackBoundary) CoordinatedAtomic(ctx context.Context, callback func(db.Session) error) error {
	return b.after(b.Runtime.CoordinatedAtomic(ctx, callback))
}
func (b *policyRollbackBoundary) CoordinatedAtomicRelation(ctx context.Context, callback func(db.RelationSession) error) error {
	return b.after(b.Runtime.CoordinatedAtomicRelation(ctx, callback))
}

func runPasswordPolicyBoundaries(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	for _, operation := range []string{"create", "password"} {
		for _, mode := range []string{"preflight", "final", "cleanup", "swallowed", "unknown", "cancel_preflight", "cancel_final"} {
			t.Run(operation+"_policy_"+mode, func(t *testing.T) {
				backend, _ := open(t)
				f := newManagementFixture(t, backend, 3)
				boundary := &policyRollbackBoundary{Runtime: f.runtime, mode: mode}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				calls := 0
				validator := identity.PasswordValidatorFunc(func(_ string, profile identity.Profile) validation.Errors {
					calls++
					if profile.Username != "member" && profile.Username != "Candidate" {
						t.Fatal("policy got wrong user profile", profile.Username)
					}
					if calls == 1 && mode != "preflight" && mode != "cancel_preflight" {
						return validation.Errors{}
					}
					if mode == "cancel_preflight" || mode == "cancel_final" {
						cancel()
					}
					return validation.NewErrors(validation.New("password", "policy_rejected"))
				})
				manager, err := identity.NewManager(boundary, f.hasher, auth.PrincipalAuthorizer{}, identity.WithPasswordValidators(validator))
				if err != nil {
					t.Fatal(err)
				}
				beforeSessions, beforeAudit := snapshotIdentitySystemRows(t, backend)
				hashes := f.hasher.calls.Load()
				var id int64
				if operation == "create" {
					value, failure := manager.CreateUser(ctx, f.actor, identity.NewUserCreate("candidate", "Candidate"), managementNewPassword)
					id, err = value.ID, failure
				} else {
					value, failure := manager.SetPassword(ctx, f.actor, f.user.ID, 1, managementNewPassword)
					id, err = value.ID, failure
				}
				failures, rejected := validation.Rejected(err)
				wantCalls, wantWrites, wantHashes := 2, 1, int64(1)
				if mode == "preflight" || mode == "cancel_preflight" {
					wantCalls, wantWrites, wantHashes = 1, 0, 0
				}
				if id != 0 || err == nil || calls != wantCalls || boundary.calls != wantWrites || f.hasher.calls.Load()-hashes != wantHashes {
					t.Fatal("policy result, hash or fence count invalid", id, err, calls, boundary.calls, f.hasher.calls.Load()-hashes)
				}
				if mode == "preflight" || mode == "final" {
					if !rejected || failures.ByField("password").Len() != 1 {
						t.Fatal("confirmed policy rejection not renderable", err)
					}
				} else {
					if rejected {
						t.Fatal("uncertain/failed/canceled rollback became renderable input", err)
					}
					switch mode {
					case "cancel_preflight", "cancel_final":
						if !errors.Is(err, context.Canceled) {
							t.Fatal("cancellation lost", err)
						}
					case "unknown":
						if !errors.Is(err, &identity.Error{Code: identity.CodeOutcomeUnknown}) {
							t.Fatal("unknown classification lost", err)
						}
					default:
						if !errors.Is(err, &identity.Error{Code: identity.CodePersistence}) {
							t.Fatal("execution failure misclassified", err)
						}
					}
				}
				// Use a live parent context: the operation's cancellation must not
				// hide a partial user/session/audit effect from this observation.
				afterSessions, afterAudit := snapshotIdentitySystemRows(t, backend)
				if !reflect.DeepEqual(beforeSessions, afterSessions) || !reflect.DeepEqual(beforeAudit, afterAudit) {
					t.Fatal("policy rejection left session/audit effects")
				}
				if count, err := models.UserObjects.Using(backend).Count(t.Context()); err != nil || count != 2 {
					t.Fatal("rejected create persisted", err, count)
				}
				f.assertOutcome(t, false)
			})
		}
	}
}
