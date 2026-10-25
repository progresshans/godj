package accountstoretest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/validation"
)

type passwordFaultScope struct {
	db.Session
	mode    string
	fault   error
	effects map[string]int
}

func (scope *passwordFaultScope) ValidateSession(ctx context.Context) error {
	return scope.Session.(db.SessionValidator).ValidateSession(ctx)
}
func (scope *passwordFaultScope) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	key, err := scope.Session.Insert(ctx, plan)
	if err == nil {
		scope.effects["insert:"+plan.Table()]++
		if plan.Table() == "godj_sessions_session" && scope.mode == "session_insert" || plan.Table() == "godj_account_audit_event" && scope.mode == "audit_insert" {
			err = scope.fault
		}
	}
	return key, err
}
func (scope *passwordFaultScope) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	count, err := scope.Session.Update(ctx, plan)
	if err == nil {
		scope.effects["update:"+plan.Table()]++
		if plan.Table() == "godj_identity_user" && scope.mode == "password_update" {
			err = scope.fault
		}
	}
	return count, err
}
func (scope *passwordFaultScope) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	count, err := scope.Session.Delete(ctx, plan)
	if err == nil && plan.Table() == "godj_sessions_session" {
		scope.effects["session_delete"]++
		deletes := scope.effects["session_delete"]
		if scope.mode == "rotation_delete" && deletes == 1 || scope.mode == "revoke_failure" && deletes == 2 || scope.mode == "late_revoke_failure" && deletes == 258 {
			err = scope.fault
		}
	}
	return count, err
}

func passwordCommitFailures(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	now := joined
	base, manager, _ := accountSessions(t, backend, &now)
	control := passwordSession(t, manager, profileActor(t, f), "other-account")
	for _, mode := range []string{"skipped", "nil_session", "root_session", "rotation_delete", "session_insert", "password_update", "revoke_failure", "late_revoke_failure", "audit_insert", "repeated", "validator_rejection", "validator_failure", "cleanup_failure", "swallowed_rejection", "unknown_rollback", "unknown_commit", "cancel_before_commit", "cancel_confirmed_rollback", "post_commit_cancel"} {
		t.Run(mode, func(t *testing.T) {
			target := roleTarget(t, backend, f, "password-failure-"+mode)
			credential := passwordCredential(t, f, target)
			current := passwordSession(t, manager, credential, "current")
			siblingCount := 1
			if mode == "late_revoke_failure" {
				siblingCount = 257
			}
			siblings := make([]sessions.Record, siblingCount)
			for i := range siblings {
				siblings[i] = passwordSession(t, manager, credential, "sibling")
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fault := errors.New("private password mutation failure")
			owner := &profileFaultBackend{Backend: backend, mode: mode, fault: fault, cancel: cancel}
			effects := map[string]int{}
			owner.decorate = func(scope db.Session) db.Session { return &passwordFaultScope{scope, mode, fault, effects} }
			hasher := &passwordChangeHasher{PasswordHasher: f.hasher}
			final := false
			configured := passwordComposition(t, owner, &now, hasher, accountmodel.PasswordChangeConfig[models.User]{ValidatePassword: func(context.Context, string, models.User) (validation.Errors, error) {
				if final && (mode == "validator_rejection" || mode == "validator_failure" || mode == "cleanup_failure" || mode == "swallowed_rejection") {
					diagnostics := validation.NewErrors(validation.New("password", "changed_policy"))
					if mode == "validator_failure" {
						return diagnostics, fault
					}
					return diagnostics, nil
				}
				return validation.Errors{}, nil
			}})
			hasher.hashes.Store(0)
			hasher.beforeHash = func(context.Context) error { final = true; return nil }
			before := passwordState(t, backend)
			result, err := configured.owner.ChangePassword(ctx, current.ID(), password, password+"-new")
			committed := mode == "unknown_commit" || mode == "post_commit_cancel"
			if mode == "post_commit_cancel" {
				if err != nil || !result.Record.ID().Valid() || ctx.Err() == nil {
					t.Fatal("late cancellation erased confirmed password commit", err)
				}
			} else {
				passwordNoResult(t, result, err)
				if mode == "validator_rejection" {
					if diagnostics, rejected := validation.Rejected(err); !rejected || diagnostics.ByField("password").Empty() {
						t.Fatal("confirmed password rejection lost", err)
					}
				} else {
					if _, rejected := validation.Rejected(err); rejected || err == auth.ErrInvalidCredentials {
						t.Fatal("write/cleanup failure became ordinary rejection", err)
					}
					code := accountmodel.CodePersistence
					if mode == "skipped" || mode == "repeated" || mode == "swallowed_rejection" || mode == "unknown_commit" || mode == "unknown_rollback" || errors.Is(owner.nativeError, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) || errors.Is(owner.nativeError, &query.Error{Code: query.CodeCommitOutcomeUnknown}) {
						code = accountmodel.CodeOutcomeUnknown
					}
					requireProfileCode(t, err, code)
				}
				if (mode == "cancel_before_commit" || mode == "cancel_confirmed_rollback") && (!errors.Is(err, context.Canceled) || owner.nativeError == nil || !errors.Is(err, owner.nativeError)) {
					t.Fatal("canceled password operation lost native outcome", err)
				}
				if mode == "cancel_confirmed_rollback" && (errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) || errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown})) {
					t.Fatal("independent owner did not acknowledge password rollback", err)
				}
				if strings.Contains(fmt.Sprintf("%+v", err), fault.Error()) || strings.Contains(fmt.Sprintf("%#v", result), password) {
					t.Fatal("password mutation error leaked")
				}
			}
			if owner.calls != 1 || hasher.hashes.Load() != 1 {
				t.Fatal("password failure retried native write or hash", owner.calls, hasher.hashes.Load())
			}
			if mode == "rotation_delete" || mode == "session_insert" || mode == "password_update" || mode == "revoke_failure" || mode == "late_revoke_failure" || mode == "audit_insert" || mode == "validator_failure" || mode == "cleanup_failure" {
				if !errors.Is(err, fault) {
					t.Fatal("password failure did not follow intended effect", err)
				}
			}
			if mode == "late_revoke_failure" && effects["session_delete"] != 258 || mode == "revoke_failure" && effects["session_delete"] != 2 || mode == "audit_insert" && (effects["session_delete"] != 2 || effects["update:godj_identity_user"] != 1 || effects["insert:godj_account_audit_event"] != 1) {
				t.Fatal("password fault missed completed earlier writes", effects)
			}
			if committed {
				row := profileRow(t, reopen(t), target.ID)
				if row.Revision != target.Revision+1 || row.EncodedPassword == target.EncodedPassword || len(profileHistory(t, backend, credential.Principal(), target.ID)) != 1 || accountSessionCount(t, backend) != int64(len(before.sessions)-siblingCount) {
					t.Fatal("password commit left partial account/session/audit")
				}
				preserved := row
				preserved.Revision = target.Revision
				preserved.EncodedPassword = target.EncodedPassword
				if !reflect.DeepEqual(preserved, target) {
					t.Fatal("password commit changed unrelated account fields")
				}
				accountSessionRecord(t, base, current, false)
				for _, sibling := range siblings {
					accountSessionRecord(t, base, sibling, false)
				}
				if mode == "post_commit_cancel" {
					accountSessionRecord(t, base, result.Record, true)
				}
			} else {
				passwordUnchanged(t, reopen(t), before)
			}
			accountSessionRecord(t, base, control, true)
		})
	}
}
