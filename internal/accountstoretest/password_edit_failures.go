package accountstoretest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
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
			var encoded string
			hasher := &passwordChangeHasher{PasswordHasher: f.hasher, scopes: &tracked.scopes, encode: func(ctx context.Context, raw string) (string, error) {
				value, err := f.hasher.Hash(ctx, raw)
				if err == nil {
					encoded = value
				}
				return value, err
			}}
			configured := passwordComposition(t, tracked, &now, hasher, accountmodel.PasswordChangeConfig[models.User]{})
			config := passwordEditConfig()
			policies := 0
			config.ValidatePassword = func(_ context.Context, _ string, row orm.ModelView[models.User]) (validation.Errors, error) {
				policies++
				if passwordViewString(t, row, "encoded_password") != "" {
					t.Fatal("typed policy exposed hash")
				}
				if passwordViewString(t, row, "first_name") == "after-hash" && mode == "policy_rejection" {
					return validation.NewErrors(validation.New("password", "similar_profile")), nil
				}
				return validation.Errors{}, nil
			}
			owner := passwordEditOwner(t, configured, config)
			previous := passwordSession(t, configured.manager, passwordCredential(t, f, target), "target")
			checkpoint := accountNativeSnapshot(t, reopen(t))
			expected := checkpoint
			writer := reopen(t)
			hasher.hashes.Store(0)
			tracked.writes.Store(0)
			hasher.beforeHash = func(context.Context) error {
				patch := models.UserPatch{}
				row := target
				switch mode {
				case "current_profile", "policy_rejection":
					patch = patch.WithFirstName("after-hash").WithLastLogin(joined.Add(-time.Hour))
					at := joined.Add(-time.Hour)
					row.FirstName, row.LastLogin = "after-hash", &at
				case "target_revision":
					patch = patch.WithRevision(2)
					row.Revision = 2
				case "target_hash":
					patch = patch.WithEncodedPassword(changedHash)
					row.EncodedPassword = changedHash
				case "target_principal":
					patch = patch.WithPrincipalID("replacement-principal")
					row.PrincipalID = "replacement-principal"
				case "target_deleted":
					expected.users = slices.DeleteFunc(slices.Clone(expected.users), func(row models.User) bool { return row.ID == target.ID })
					if _, err := models.UserObjects.Delete(t.Context(), writer, &target); err != nil {
						return err
					}
				case "actor_inactive":
					current := f.user
					current.Active = false
					userReferenceReplace(&expected, current)
					if _, err := models.UserObjects.Patch(t.Context(), writer, f.user, models.UserPatch{}.WithActive(false)); err != nil {
						return err
					}
				case "actor_revoked":
					expected.direct = slices.DeleteFunc(slices.Clone(expected.direct), func(row models.UserPermissionsLink) bool { return row.ID == f.direct.ID })
					if count, err := models.UserPermissionsLinkObjects.Delete(t.Context(), writer, &f.direct); err != nil || count != 1 {
						return errors.Join(err, errors.New("actor grant not removed"))
					}
				case "domain_changed":
					f.ownership = transitionSessionDomain(t, writer, f.binding, f.ownership)
				}
				if mode == "current_profile" || mode == "policy_rejection" || mode == "target_revision" || mode == "target_hash" || mode == "target_principal" {
					if _, err := models.UserObjects.Patch(t.Context(), writer, target, patch); err != nil {
						return err
					}
					userReferenceReplace(&expected, row)
				}
				checkpoint = accountNativeSnapshot(t, reopen(t))
				if mode != "domain_changed" {
					assertCatalogRows(t, expected, checkpoint, actor)
				}
				return nil
			}
			result, err := owner.SetPassword(t.Context(), actor, target.ID, target.Revision, password+"-new")
			if hasher.hashes.Load() != 1 || tracked.writes.Load() != 1 {
				t.Fatal("recheck did not follow one hash/fence", hasher.hashes.Load(), tracked.writes.Load())
			}
			if mode == "current_profile" {
				stored := profileRow(t, reopen(t), target.ID)
				if err != nil || policies != 2 || result.RevokedSessions != 1 || stored.FirstName != "after-hash" || stored.LastLogin == nil || !stored.LastLogin.Equal(joined.Add(-time.Hour)) || stored.Revision != 2 {
					t.Fatal("password edit lost concurrent profile", err)
				}
				accountSessionRecord(t, configured.store, previous, false)
				current := target
				at := joined.Add(-time.Hour)
				current.FirstName, current.LastLogin = "after-hash", &at
				current.EncodedPassword, current.Revision = encoded, 2
				userReferenceReplace(&checkpoint, current)
				loginRemoveRecord(t, &checkpoint, loginNamespace(t, passwordCredential(t, f, target)), previous.ID())
				assertCatalogRowsAt(t, checkpoint, accountNativeSnapshot(t, reopen(t)), actor, now, catalogExpectedEvent{"user", target.ID, admin.ActionChange, []string{"encoded_password"}})
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
				assertCatalogRows(t, checkpoint, accountNativeSnapshot(t, reopen(t)), actor)
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

func passwordAdminReadFailures(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	now := joined
	_, sessions, _ := accountSessions(t, backend, &now)
	passwordSession(t, sessions, profileActor(t, f), "read-failure-preserved")
	if _, err := sessions.Create(t.Context(), map[string]string{"anonymous": "read-failure-preserved"}); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"skipped", "nil", "typed_nil", "root", "repeated", "query", "close", "swallowed", "cleanup", "cancel", "denial_cleanup", "missing_cleanup"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fault := errors.New("private administrative password read failure")
			readOwner := &creationSnapshotBackend{Backend: backend}
			reads := 0
			readOwner.read = snapshotFunc(func(ctx context.Context, observe func(db.Queryer) error) error {
				reads++
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
			tracked := &passwordScopeOwner{Backend: readOwner}
			hasher := &passwordChangeHasher{PasswordHasher: f.hasher, scopes: &tracked.scopes}
			configured := passwordComposition(t, tracked, &now, hasher, accountmodel.PasswordChangeConfig[models.User]{})
			config, id := passwordEditConfig(), f.user.ID
			policies := 0
			config.ValidatePassword = func(context.Context, string, orm.ModelView[models.User]) (validation.Errors, error) {
				policies++
				return validation.Errors{}, nil
			}
			if mode == "denial_cleanup" {
				config.AdditionalPermissions = []auth.Permission{"account.absent"}
			}
			if mode == "missing_cleanup" {
				id = 99999
			}
			owner := passwordEditOwner(t, configured, config)
			hasher.hashes.Store(0)
			before := accountNativeSnapshot(t, backend)
			for _, unusable := range []bool{false, true} {
				beforeReads := reads
				work := ctx
				if mode == "cancel" && unusable {
					// The second command must enter a fresh read snapshot before
					// its own cleanup cancellation, rather than inherit the first.
					work, cancel = context.WithCancel(t.Context())
					defer cancel()
				}
				var result accountmodel.PasswordEditResult
				var err error
				if unusable {
					result, err = owner.SetUnusablePassword(work, actor, id, 1)
				} else {
					result, err = owner.SetPassword(work, actor, id, 1, password+"-new")
				}
				passwordEditNoResult(t, result, err)
				requireProfileCode(t, err, accountmodel.CodePersistence)
				if hasher.hashes.Load() != 0 || policies != 0 || tracked.writes.Load() != 0 || reads != beforeReads+1 {
					t.Fatal("failed snapshot progressed to password work or mutation")
				}
				if mode == "query" || mode == "close" || mode == "swallowed" || mode == "cleanup" || mode == "denial_cleanup" || mode == "missing_cleanup" {
					if !errors.Is(err, fault) {
						t.Fatal("read cleanup cause lost", fmt.Sprintf("%v", err))
					}
				}
				if mode == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatal("password read cleanup lost cancellation", err)
				}
				assertCatalogRows(t, before, accountNativeSnapshot(t, reopen(t)), actor)
			}
		})
	}
}
