package accountstoretest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
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

func passwordAdminRechecks(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	changedHash, err := f.hasher.Hash(t.Context(), "concurrent-administrative-password")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"current_profile", "policy_rejection", "target_revision", "target_hash", "target_principal", "target_deleted", "actor_inactive", "actor_revoked", "domain_changed"} {
		t.Run(mode, func(t *testing.T) {
			now := joined
			target := roleTarget(t, backend, f, "admin-recheck-"+mode)
			tracked := &passwordScopeOwner{Backend: backend}
			hasher := &passwordChangeHasher{PasswordHasher: f.hasher, scopes: &tracked.scopes}
			configured := passwordComposition(t, tracked, &now, hasher, accountmodel.PasswordChangeConfig[models.User]{})
			config := passwordEditConfig()
			policies := 0
			config.ValidatePassword = func(_ context.Context, _ string, row models.User) (validation.Errors, error) {
				policies++
				if row.EncodedPassword != "" {
					t.Fatal("typed policy exposed hash")
				}
				if row.FirstName == "after-hash" && mode == "policy_rejection" {
					return validation.NewErrors(validation.New("password", "similar_profile")), nil
				}
				return validation.Errors{}, nil
			}
			owner := passwordEditOwner(t, configured, config)
			previous := passwordSession(t, configured.manager, passwordCredential(t, f, target), "target")
			var checkpoint passwordDurableState
			hasher.hashes.Store(0)
			hasher.beforeHash = func(context.Context) error {
				patch := models.UserPatch{}
				switch mode {
				case "current_profile", "policy_rejection":
					patch = patch.WithFirstName("after-hash").WithLastLogin(joined.Add(-time.Hour))
				case "target_revision":
					patch = patch.WithRevision(2)
				case "target_hash":
					patch = patch.WithEncodedPassword(changedHash)
				case "target_principal":
					patch = patch.WithPrincipalID("replacement-principal")
				case "target_deleted":
					if _, err := models.UserObjects.Delete(t.Context(), backend, &target); err != nil {
						return err
					}
				case "actor_inactive":
					if _, err := models.UserObjects.Patch(t.Context(), backend, f.user, models.UserPatch{}.WithActive(false)); err != nil {
						return err
					}
				case "actor_revoked":
					if count, err := models.UserPermissionsLinkObjects.Delete(t.Context(), backend, &f.direct); err != nil || count != 1 {
						return errors.Join(err, errors.New("actor grant not removed"))
					}
				case "domain_changed":
					f.ownership = transitionSessionDomain(t, backend, f.binding, f.ownership)
				}
				if mode == "current_profile" || mode == "policy_rejection" || mode == "target_revision" || mode == "target_hash" || mode == "target_principal" {
					if _, err := models.UserObjects.Patch(t.Context(), backend, target, patch); err != nil {
						return err
					}
				}
				checkpoint = passwordState(t, backend)
				return nil
			}
			result, err := owner.SetPassword(t.Context(), actor, target.ID, target.Revision, password+"-new")
			if hasher.hashes.Load() != 1 || tracked.writes.Load() != 1 {
				t.Fatal("recheck did not follow one hash/fence")
			}
			if mode == "current_profile" {
				stored := profileRow(t, reopen(t), target.ID)
				if err != nil || policies != 2 || result.RevokedSessions != 1 || stored.FirstName != "after-hash" || stored.LastLogin == nil || !stored.LastLogin.Equal(joined.Add(-time.Hour)) || stored.Revision != 2 {
					t.Fatal("password edit lost concurrent profile", err)
				}
				accountSessionRecord(t, configured.store, previous, false)
			} else {
				passwordEditNoResult(t, result, err)
				if mode == "policy_rejection" {
					if diagnostics, rejected := validation.Rejected(err); !rejected || diagnostics.ByField("password").Empty() || policies != 2 {
						t.Fatal("current typed policy not applied", err)
					}
				} else {
					code := accountmodel.CodeConflict
					if mode == "target_deleted" {
						code = accountmodel.CodeNotFound
					}
					if mode == "actor_inactive" || mode == "actor_revoked" || mode == "domain_changed" {
						code = accountmodel.CodePermission
					}
					requireProfileCode(t, err, code)
				}
				passwordUnchanged(t, reopen(t), checkpoint)
			}
			// Independent subcases retain their original actor; domain change is last.
			if mode == "actor_inactive" {
				if _, err := models.UserObjects.Patch(t.Context(), backend, f.user, models.UserPatch{}.WithActive(true)); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "actor_revoked" {
				var err error
				f.direct, err = models.UserPermissionsLinkObjects.Create(t.Context(), backend, models.NewUserPermissionsLinkCreate(f.user.ID, f.grants[0].ID))
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func passwordAdminCommitFailures(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	now := joined
	base, manager, _ := accountSessions(t, backend, &now)
	control := passwordSession(t, manager, profileActor(t, f), "other-account")
	for _, mode := range []string{"skipped", "nil_session", "root_session", "password_update", "revoke_failure", "late_revoke_failure", "audit_insert", "unusable_audit", "repeated", "validator_rejection", "validator_failure", "cleanup_failure", "swallowed_rejection", "invalid_clock", "unknown_rollback", "unknown_commit", "cancel_confirmed_rollback", "post_commit_cancel"} {
		t.Run(mode, func(t *testing.T) {
			target := roleTarget(t, backend, f, "admin-failure-"+mode)
			credential := passwordCredential(t, f, target)
			count := 2
			if mode == "late_revoke_failure" {
				count = 258
			}
			previous := make([]sessions.Record, count)
			for i := range previous {
				previous[i] = passwordSession(t, manager, credential, "revoke")
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fault := errors.New("private admin password storage failure")
			backendOwner := &profileFaultBackend{Backend: backend, mode: mode, fault: fault, cancel: cancel}
			effects := map[string]int{}
			backendOwner.decorate = func(scope db.Session) db.Session {
				faultMode := mode
				if mode == "unusable_audit" {
					faultMode = "audit_insert"
				}
				return &passwordFaultScope{scope, faultMode, fault, effects}
			}
			hasher := &passwordChangeHasher{PasswordHasher: f.hasher}
			configured := passwordComposition(t, backendOwner, &now, hasher, accountmodel.PasswordChangeConfig[models.User]{})
			config := passwordEditConfig()
			if mode == "invalid_clock" {
				config.Now = func() time.Time { return time.Time{} }
			}
			final := false
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
			owner := passwordEditOwner(t, configured, config)
			hasher.hashes.Store(0)
			hasher.beforeHash = func(context.Context) error { final = true; return nil }
			before := passwordState(t, backend)
			var result accountmodel.PasswordEditResult
			var err error
			if mode == "unusable_audit" {
				result, err = owner.SetUnusablePassword(ctx, actor, target.ID, 1)
			} else {
				result, err = owner.SetPassword(ctx, actor, target.ID, 1, password+"-new")
			}
			committed := mode == "unknown_commit" || mode == "post_commit_cancel"
			if mode == "post_commit_cancel" {
				if err != nil || result.ID != target.ID || result.Revision != 2 || result.RevokedSessions != count || ctx.Err() == nil {
					t.Fatal("confirmed commit lost to late cancellation", err)
				}
			} else {
				passwordEditNoResult(t, result, err)
				if mode == "validator_rejection" {
					if diagnostics, rejected := validation.Rejected(err); !rejected || diagnostics.ByField("password").Empty() {
						t.Fatal("safe final rejection lost", err)
					}
				} else {
					if _, rejected := validation.Rejected(err); rejected {
						t.Fatal("storage failure became validation")
					}
					code := accountmodel.CodePersistence
					if mode == "skipped" || mode == "repeated" || mode == "swallowed_rejection" || mode == "unknown_commit" || mode == "unknown_rollback" {
						code = accountmodel.CodeOutcomeUnknown
					}
					requireProfileCode(t, err, code)
				}
			}
			wantHashes := int64(1)
			if mode == "unusable_audit" {
				wantHashes = 0
			}
			if backendOwner.calls != 1 || hasher.hashes.Load() != wantHashes {
				t.Fatal("administrative password failure retried")
			}
			if mode == "password_update" || mode == "revoke_failure" || mode == "late_revoke_failure" || mode == "audit_insert" || mode == "unusable_audit" || mode == "validator_failure" || mode == "cleanup_failure" {
				if !errors.Is(err, fault) {
					t.Fatal("fault not retained", err)
				}
			}
			if mode == "late_revoke_failure" && effects["session_delete"] != 258 || mode == "revoke_failure" && effects["session_delete"] != 2 || (mode == "audit_insert" || mode == "unusable_audit") && (effects["session_delete"] != 2 || effects["update:godj_identity_user"] != 1 || effects["insert:godj_account_audit_event"] != 1) {
				t.Fatal("fault missed completed writes", effects)
			}
			if mode == "cancel_confirmed_rollback" && (!errors.Is(err, context.Canceled) || errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) || errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown})) {
				t.Fatal("confirmed rollback classification lost", err)
			}
			if committed {
				row := profileRow(t, reopen(t), target.ID)
				if row.Revision != 2 || row.EncodedPassword == target.EncodedPassword || accountSessionCount(t, backend) != int64(len(before.sessions)-count) || len(profileHistory(t, backend, actor, target.ID)) != 1 {
					t.Fatal("partial administrative password commit")
				}
				preserved := row
				preserved.Revision, preserved.EncodedPassword = target.Revision, target.EncodedPassword
				if !reflect.DeepEqual(preserved, target) {
					t.Fatal("committed password overwrote profile")
				}
				for _, old := range previous {
					accountSessionRecord(t, base, old, false)
				}
			} else {
				passwordUnchanged(t, reopen(t), before)
			}
			accountSessionRecord(t, base, control, true)
		})
	}
}

func passwordAdminReadFailures(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	for _, mode := range []string{"skipped", "nil", "typed_nil", "root", "repeated", "query", "close", "swallowed", "cleanup", "cancel", "denial_cleanup", "missing_cleanup"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fault := errors.New("private administrative password read failure")
			readOwner := &creationSnapshotBackend{Backend: backend}
			readOwner.read = snapshotFunc(func(ctx context.Context, observe func(db.Queryer) error) error {
				switch mode {
				case "skipped":
					return nil
				case "nil":
					return observe(nil)
				case "typed_nil":
					return observe((*profileReadProbe)(nil))
				case "root":
					return observe(backend)
				}
				err := backend.ReadSnapshot(ctx, func(scope db.Queryer) error {
					probe := &profileReadProbe{Queryer: scope, validator: scope.(db.SessionValidator)}
					if mode == "query" || mode == "close" || mode == "swallowed" {
						probe.fault, probe.closeFail = fault, mode == "close"
					}
					err := observe(probe)
					if mode == "swallowed" {
						return nil
					}
					if mode == "repeated" {
						return errors.Join(err, observe(probe))
					}
					return err
				})
				if mode == "cleanup" || mode == "denial_cleanup" || mode == "missing_cleanup" {
					return errors.Join(err, fault)
				}
				if mode == "cancel" {
					cancel()
				}
				return err
			})
			now := joined
			tracked := &passwordScopeOwner{Backend: readOwner}
			hasher := &passwordChangeHasher{PasswordHasher: f.hasher, scopes: &tracked.scopes}
			configured := passwordComposition(t, tracked, &now, hasher, accountmodel.PasswordChangeConfig[models.User]{})
			config, id := passwordEditConfig(), f.user.ID
			if mode == "denial_cleanup" {
				config.AdditionalPermissions = []auth.Permission{"account.absent"}
			}
			if mode == "missing_cleanup" {
				id = 99999
			}
			owner := passwordEditOwner(t, configured, config)
			hasher.hashes.Store(0)
			before := passwordState(t, backend)
			result, err := owner.SetPassword(ctx, actor, id, 1, password+"-new")
			passwordEditNoResult(t, result, err)
			requireProfileCode(t, err, accountmodel.CodePersistence)
			if hasher.hashes.Load() != 0 || tracked.writes.Load() != 0 {
				t.Fatal("failed snapshot progressed to mutation")
			}
			if mode == "query" || mode == "close" || mode == "swallowed" || mode == "cleanup" || mode == "denial_cleanup" || mode == "missing_cleanup" {
				if !errors.Is(err, fault) {
					t.Fatal("read cleanup cause lost", fmt.Sprintf("%v", err))
				}
			}
			passwordUnchanged(t, reopen(t), before)
		})
	}
}
