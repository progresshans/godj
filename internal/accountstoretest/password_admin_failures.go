package accountstoretest

import (
	"context"
	"errors"
	"maps"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

func passwordAdminCommitFailures(t *testing.T, open Open) {
	for _, mode := range []string{"skipped", "nil_session", "root_session", "password_update", "revoke_failure", "late_revoke_failure", "audit_insert", "unusable_audit", "repeated", "validator_rejection", "validator_failure", "cleanup_failure", "swallowed_rejection", "invalid_clock", "unknown_rollback", "unknown_commit", "cancel_confirmed_rollback", "post_commit_cancel", "callback_cancel", "swallowed_failure", "opaque"} {
		t.Run(mode, func(t *testing.T) {
			backend, reopen := open(t)
			f := provisionSelfPassword(t, backend)
			if mode == "late_revoke_failure" {
				for len(f.targetSessions) < 258 {
					f.targetSessions = append(f.targetSessions, passwordSession(t, f.runtime.Sessions(), f.credential, "late administrative revoke"))
				}
			}
			count := len(f.targetSessions)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var fault error = errors.New("private administrative password storage failure")
			if mode == "opaque" {
				fault = passwordWriteOpaqueError{"private administrative password storage failure"}
			}
			config := f.config(t)
			config.Accounts.Now = f.clock
			if mode == "invalid_clock" {
				config.Accounts.Now = func() time.Time { return time.Time{} }
			}
			final := false
			config.Accounts.ValidatePassword = func(context.Context, string, orm.ModelView[models.User]) (validation.Errors, error) {
				if final && (mode == "validator_rejection" || mode == "validator_failure" || mode == "cleanup_failure" || mode == "swallowed_rejection") {
					diagnostics := validation.NewErrors(validation.New("password", "changed_policy"))
					if mode == "validator_failure" {
						return diagnostics, fault
					}
					return diagnostics, nil
				}
				return validation.Errors{}, nil
			}
			owner := newUserReferenceOwner(t, backend, f.userReferenceFixture, config)
			owner.boundary.mode, owner.boundary.fault, owner.boundary.cancel = mode, fault, cancel
			if mode == "swallowed_failure" {
				owner.boundary.mode = "swallowed_rejection"
			}
			effects := map[string]int{}
			owner.boundary.decorate = func(scope db.Session) db.Session {
				faultMode := mode
				if mode == "unusable_audit" || mode == "swallowed_failure" || mode == "opaque" {
					faultMode = "audit_insert"
				}
				return &passwordFaultScope{scope, faultMode, fault, effects}
			}
			owner.hasher.beforeHash = func(context.Context) error { final = true; return nil }
			before := userReferenceSnapshot(t, reopen(t))
			entropy := f.entropy.count()
			var result accountmodel.PasswordEditResult
			var err error
			if mode == "unusable_audit" {
				result, err = owner.runtime.Accounts().Passwords().SetUnusablePassword(ctx, f.actor.Principal(), f.target.ID, f.target.Revision)
			} else {
				result, err = owner.runtime.Accounts().Passwords().SetPassword(ctx, f.actor.Principal(), f.target.ID, f.target.Revision, passwordAdminRaw)
			}
			committed := mode == "unknown_commit" || mode == "post_commit_cancel"
			if mode == "post_commit_cancel" {
				if err != nil || result.ID != f.target.ID || result.Revision != f.target.Revision+1 || result.RevokedSessions != count || ctx.Err() == nil {
					t.Fatal("confirmed administrative commit lost to late cancellation", err)
				}
			} else {
				passwordEditNoResult(t, result, err)
				if mode == "validator_rejection" {
					if diagnostics, rejected := validation.Rejected(err); !rejected || diagnostics.ByField("password").Empty() || errors.Unwrap(err) != nil {
						t.Fatal("safe administrative final rejection lost", err)
					}
				} else {
					if _, rejected := validation.Rejected(err); rejected {
						t.Fatal("administrative storage failure became validation")
					}
					code := accountmodel.CodePersistence
					if mode == "skipped" || mode == "repeated" || mode == "swallowed_rejection" || mode == "swallowed_failure" || mode == "unknown_commit" || mode == "unknown_rollback" {
						code = accountmodel.CodeOutcomeUnknown
					}
					requireProfileCode(t, err, code)
				}
			}
			wantHashes := int64(1)
			if mode == "unusable_audit" {
				wantHashes = 0
			}
			if owner.boundary.calls != 1 || owner.hasher.hashes.Load() != wantHashes || owner.hasher.verifies.Load() != 0 || f.entropy.count() != entropy {
				t.Fatal("administrative password failure repeated work or touched bearer entropy")
			}
			if mode == "password_update" || mode == "revoke_failure" || mode == "late_revoke_failure" || mode == "audit_insert" || mode == "unusable_audit" || mode == "validator_failure" || mode == "cleanup_failure" || mode == "swallowed_failure" {
				if !errors.Is(err, fault) {
					t.Fatal("administrative fault not retained", err)
				}
			}
			if mode == "opaque" && !errors.As(err, new(passwordWriteOpaqueError)) {
				t.Fatal("opaque administrative write cause lost", err)
			}
			if mode == "unknown_commit" || mode == "unknown_rollback" {
				if !errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) {
					t.Fatal("administrative native uncertainty lost", err)
				}
			}
			if mode == "skipped" || mode == "repeated" || mode == "swallowed_rejection" || mode == "swallowed_failure" {
				if !errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) {
					t.Fatal("administrative callback ownership failure lost its uncertain result", err)
				}
			}
			if mode == "cancel_confirmed_rollback" || mode == "callback_cancel" {
				if !errors.Is(err, context.Canceled) || errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) || errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) {
					t.Fatal("confirmed administrative rollback classification lost", err)
				}
			}
			wantEffects := map[string]int{}
			switch mode {
			case "password_update":
				wantEffects["update:godj_identity_user"] = 1
			case "revoke_failure", "late_revoke_failure", "invalid_clock":
				wantEffects["update:godj_identity_user"] = 1
				wantEffects["session_delete"] = count
				if mode == "revoke_failure" {
					wantEffects["session_delete"] = 2
				}
			case "audit_insert", "unusable_audit", "repeated", "unknown_rollback", "unknown_commit", "cancel_confirmed_rollback", "post_commit_cancel", "callback_cancel", "swallowed_failure", "opaque":
				wantEffects["update:godj_identity_user"] = 1
				wantEffects["session_delete"] = count
				wantEffects["insert:godj_account_audit_event"] = 1
			}
			if !maps.Equal(effects, wantEffects) {
				t.Fatal("administrative failure missed its physical write prefix", mode, effects, wantEffects)
			}
			after := userReferenceSnapshot(t, reopen(t))
			if committed {
				if owner.boundary.nativeError != nil {
					t.Fatal("administrative committed result did not complete its native write", owner.boundary.nativeError)
				}
				expected, wanted := passwordAdminExpected(t, f, owner, before)
				if mode == "post_commit_cancel" && result != wanted {
					t.Fatal("administrative committed metadata differs")
				}
				assertUserReferenceStateAt(t, expected, after, f.actor.Principal(), f.clock(), catalogExpectedEvent{"user", f.target.ID, admin.ActionChange, []string{"encoded_password"}})
			} else {
				assertUserReferenceState(t, before, after, f.actor.Principal())
			}
			for _, value := range []any{result, err} {
				selfPasswordPrivate(t, value, passwordAdminRaw, f.target.EncodedPassword, "private administrative password storage failure", "private password opaque fault")
			}
		})
	}
}
