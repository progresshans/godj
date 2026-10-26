package accountstoretest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/validation"
)

func passwordResetCommitFailures(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	now := joined
	base := passwordComposition(t, backend, &now, f.hasher, accountmodel.PasswordChangeConfig[models.User]{})
	baseOwner := selectedResetOwner(t, base, selectedResetConfig(t, &now))
	control := passwordSession(t, base.manager, profileActor(t, f), "reset-control")
	for _, mode := range []string{"skipped", "nil_session", "root_session", "rotation_delete", "session_insert", "password_update", "revoke_failure", "late_revoke_failure", "audit_insert", "self_clear_audit", "repeated", "validator_rejection", "validator_failure", "cleanup_failure", "swallowed_rejection", "invalid_clock", "unknown_rollback", "unknown_commit", "self_unknown_commit", "cancel_before_commit", "cancel_confirmed_rollback", "post_commit_cancel", "self_post_commit_cancel"} {
		t.Run(mode, func(t *testing.T) {
			target := roleTarget(t, backend, f, "reset-failure-"+mode)
			credential := passwordCredential(t, f, target)
			count := 2
			if mode == "late_revoke_failure" {
				count = 257
			}
			siblings := make([]sessions.Record, count)
			for i := range siblings {
				siblings[i] = passwordSession(t, base.manager, credential, "target-sibling")
			}
			self := strings.HasPrefix(mode, "self_")
			var previous sessions.Record
			if self {
				previous = passwordSession(t, base.manager, credential, "self-reset")
			}
			token := selectedResetToken(t, baseOwner, target.PrincipalID)
			proof := selectedResetProof(t, baseOwner, previous, token)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fault := errors.New("private reset persistence failure")
			ownerMode := mode
			if self {
				ownerMode = strings.TrimPrefix(mode, "self_")
			}
			faultMode := ownerMode
			if mode == "self_clear_audit" {
				faultMode = "audit_insert"
			}
			backendOwner := &profileFaultBackend{Backend: backend, mode: ownerMode, fault: fault, cancel: cancel}
			effects := map[string]int{}
			backendOwner.decorate = func(scope db.Session) db.Session { return &passwordFaultScope{scope, faultMode, fault, effects} }
			hasher := &passwordChangeHasher{PasswordHasher: f.hasher}
			configured := passwordComposition(t, backendOwner, &now, hasher, accountmodel.PasswordChangeConfig[models.User]{})
			config := selectedResetConfig(t, &now)
			final := false
			config.Clock = func() time.Time {
				if final && mode == "invalid_clock" {
					return time.Time{}
				}
				return now
			}
			config.ValidatePassword = func(context.Context, string, models.User) (validation.Errors, error) {
				if final && (mode == "validator_rejection" || mode == "validator_failure" || mode == "cleanup_failure" || mode == "swallowed_rejection") {
					diagnostics := validation.NewErrors(validation.New("password", "changed_policy"))
					if mode == "validator_failure" {
						return diagnostics, fault
					}
					return diagnostics, nil
				}
				return validation.Errors{}, nil
			}
			owner := selectedResetOwner(t, configured, config)
			hasher.hashes.Store(0)
			hasher.beforeHash = func(context.Context) error { final = true; return nil }
			before := passwordState(t, backend)
			result, err := owner.ResetPassword(ctx, proof.ID(), target.PrincipalID, password+"-reset")
			committed := ownerMode == "unknown_commit" || ownerMode == "post_commit_cancel"
			if ownerMode == "post_commit_cancel" {
				if err != nil || ctx.Err() == nil || result.ClearSession != self || result.Record.ID().Valid() == self {
					t.Fatal("confirmed reset commit lost to late cancellation", err)
				}
			} else {
				selectedResetNoResult(t, result, err)
				if mode == "validator_rejection" {
					if diagnostics, rejected := validation.Rejected(err); !rejected || diagnostics.ByField("password").Empty() {
						t.Fatal("confirmed reset policy refusal lost", err)
					}
				} else {
					if _, rejected := validation.Rejected(err); rejected || err == auth.ErrInvalidResetProof {
						t.Fatal("reset persistence failure became ordinary input refusal", err)
					}
					code := accountmodel.CodePersistence
					if mode == "skipped" || mode == "repeated" || mode == "swallowed_rejection" || ownerMode == "unknown_commit" || ownerMode == "unknown_rollback" || errors.Is(backendOwner.nativeError, &query.Error{Code: query.CodeCommitOutcomeUnknown}) || errors.Is(backendOwner.nativeError, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) {
						code = accountmodel.CodeOutcomeUnknown
					}
					requireProfileCode(t, err, code)
				}
				if strings.Contains(fmt.Sprintf("%#v", err), fault.Error()) || strings.Contains(fmt.Sprintf("%#v", result), token.Encoded()) {
					t.Fatal("reset failure diagnostics leaked")
				}
			}
			if backendOwner.calls != 1 || hasher.hashes.Load() != 1 {
				t.Fatal("reset failure retried native write or hash")
			}
			if mode == "rotation_delete" || mode == "session_insert" || mode == "password_update" || mode == "revoke_failure" || mode == "late_revoke_failure" || mode == "audit_insert" || mode == "self_clear_audit" || mode == "validator_failure" || mode == "cleanup_failure" {
				if !errors.Is(err, fault) {
					t.Fatal("reset fault did not reach its intended effect", err)
				}
			}
			if mode == "late_revoke_failure" && effects["session_delete"] != 258 || mode == "revoke_failure" && effects["session_delete"] != 2 || (mode == "audit_insert" || mode == "self_clear_audit") && (effects["session_delete"] != 3 || effects["update:godj_identity_user"] != 1 || effects["insert:godj_account_audit_event"] != 1) {
				t.Fatal("reset rollback omitted completed writes", effects)
			}
			if mode == "self_clear_audit" && effects["insert:godj_sessions_session"] != 0 {
				t.Fatal("self reset created a replacement session")
			}
			if mode == "cancel_before_commit" || mode == "cancel_confirmed_rollback" {
				if !errors.Is(err, context.Canceled) || backendOwner.nativeError == nil || !errors.Is(err, backendOwner.nativeError) {
					t.Fatal("reset cancellation lost native outcome", err)
				}
			}
			if mode == "cancel_confirmed_rollback" && (errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) || errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown})) {
				t.Fatal("confirmed rollback was classified as uncertain")
			}
			if committed {
				row := profileRow(t, reopen(t), target.ID)
				preserved := row
				preserved.EncodedPassword = target.EncodedPassword
				preserved.Revision = target.Revision
				removed := count
				if self {
					removed++
				}
				if row.Revision != target.Revision+1 || row.EncodedPassword == target.EncodedPassword || !reflect.DeepEqual(preserved, target) || accountSessionCount(t, backend) != int64(len(before.sessions)-removed) || len(profileHistory(t, backend, credential.Principal(), target.ID)) != 1 {
					t.Fatal("confirmed/unknown reset commit was partial")
				}
				accountSessionRecord(t, base.store, proof, false)
				for _, record := range siblings {
					accountSessionRecord(t, base.store, record, false)
				}
				if ownerMode == "post_commit_cancel" && !self {
					accountSessionRecord(t, base.store, result.Record, true)
				}
				for _, record := range passwordState(t, reopen(t)).sessions {
					if strings.Contains(record.Payload, token.Encoded()) {
						t.Fatal("committed reset retained its secret proof")
					}
				}
			} else {
				passwordUnchanged(t, reopen(t), before)
			}
			accountSessionRecord(t, base.store, control, true)
		})
	}
}

func passwordResetEntryFailures(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	now := joined
	base := passwordComposition(t, backend, &now, f.hasher, accountmodel.PasswordChangeConfig[models.User]{})
	for _, kind := range []string{"create", "rotate"} {
		t.Run(kind, func(t *testing.T) {
			for _, mode := range []string{"skipped", "nil_session", "root_session", "rotation_delete", "session_insert", "repeated", "cleanup_failure", "unknown_rollback", "unknown_commit", "cancel_confirmed_rollback", "post_commit_cancel"} {
				if kind == "create" && mode == "rotation_delete" {
					continue
				}
				t.Run(mode, func(t *testing.T) {
					var previous sessions.Record
					if kind == "rotate" {
						previous = passwordSession(t, base.manager, profileActor(t, f), "entry-control")
					}
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					fault := errors.New("private reset entry failure")
					ownerBackend := &profileFaultBackend{Backend: backend, mode: mode, fault: fault, cancel: cancel}
					effects := map[string]int{}
					ownerBackend.decorate = func(scope db.Session) db.Session { return &passwordFaultScope{scope, mode, fault, effects} }
					hasher := &passwordChangeHasher{PasswordHasher: f.hasher}
					configured := passwordComposition(t, ownerBackend, &now, hasher, accountmodel.PasswordChangeConfig[models.User]{})
					config := selectedResetConfig(t, &now)
					config.Clock = func() time.Time {
						if mode == "cleanup_failure" && ownerBackend.calls > 0 {
							return now.Add(73 * time.Hour)
						}
						return now
					}
					owner := selectedResetOwner(t, configured, config)
					token := selectedResetToken(t, owner, f.user.PrincipalID)
					hasher.hashes.Store(0)
					before := passwordState(t, backend)
					record, err := owner.StartPasswordReset(ctx, previous.ID(), f.user.PrincipalID, token.Encoded())
					committed := mode == "unknown_commit" || mode == "post_commit_cancel"
					if mode == "post_commit_cancel" {
						if err != nil || !record.ID().Valid() || ctx.Err() == nil {
							t.Fatal("confirmed entry lost to late cancellation", err)
						}
						accountSessionRecord(t, base.store, record, true)
					} else {
						if err == nil || !reflect.DeepEqual(record, sessions.Record{}) || err == auth.ErrInvalidResetProof {
							t.Fatal("entry failure published proof or became token refusal", err)
						}
						code := accountmodel.CodePersistence
						if mode == "skipped" || mode == "repeated" || mode == "unknown_rollback" || mode == "unknown_commit" {
							code = accountmodel.CodeOutcomeUnknown
						}
						requireProfileCode(t, err, code)
						if (mode == "rotation_delete" || mode == "session_insert" || mode == "cleanup_failure") && !errors.Is(err, fault) {
							t.Fatal("entry lost its persistence failure cause", err)
						}
					}
					if ownerBackend.calls != 1 || hasher.hashes.Load() != 0 {
						t.Fatal("failed entry retried or hashed")
					}
					if mode == "session_insert" && effects["insert:godj_sessions_session"] != 1 || mode == "rotation_delete" && effects["session_delete"] != 1 {
						t.Fatal("entry fault did not follow real DML", effects)
					}
					if !committed {
						passwordUnchanged(t, reopen(t), before)
					} else {
						after := passwordState(t, reopen(t))
						want := len(before.sessions)
						if kind == "create" {
							want++
						}
						if len(after.sessions) != want || !reflect.DeepEqual(after.users, before.users) || !reflect.DeepEqual(after.audits, before.audits) {
							t.Fatal("entry committed account changes or lost session")
						}
						if kind == "rotate" {
							accountSessionRecord(t, base.store, previous, false)
						}
					}
				})
			}
		})
	}
}
