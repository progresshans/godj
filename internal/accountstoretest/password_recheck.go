package accountstoretest

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

func passwordRechecks(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	changedHash, err := f.hasher.Hash(t.Context(), "concurrent-password-change")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"current_profile", "policy_rejection", "inactive", "changed_hash", "missing_principal", "deleted", "session_deleted", "session_identity", "session_stamp", "idle_expired", "absolute_expired", "revision_overflow", "domain_changed"} {
		t.Run(mode, func(t *testing.T) {
			now := joined
			target := roleTarget(t, backend, f, "password-recheck-"+mode)
			credential := passwordCredential(t, f, target)
			tracked := &passwordScopeOwner{Backend: backend}
			hasher := &passwordChangeHasher{PasswordHasher: f.hasher, scopes: &tracked.scopes}
			policies := 0
			configured := passwordComposition(t, tracked, &now, hasher, accountmodel.PasswordChangeConfig[models.User]{ValidatePassword: func(_ context.Context, _ string, row models.User) (validation.Errors, error) {
				policies++
				if row.EncodedPassword != "" {
					t.Fatal("policy exposed encoded credential")
				}
				if row.FirstName == "after-hash" {
					if mode == "policy_rejection" {
						return validation.NewErrors(validation.New("password", "similar_profile")), nil
					}
					if mode != "current_profile" || row.Username != "updated-"+mode || row.Revision != 7 {
						t.Fatal("policy lost current typed profile")
					}
				}
				return validation.Errors{}, nil
			}})
			base, _, _ := accountSessions(t, backend, &now)
			current := passwordSession(t, configured.manager, credential, mode)
			var checkpoint passwordDurableState
			hasher.hashes.Store(0)
			hasher.beforeHash = func(context.Context) error {
				patch := models.UserPatch{}
				switch mode {
				case "current_profile", "policy_rejection":
					at := joined.Add(-time.Hour)
					patch = patch.WithFirstName("after-hash").WithUsername("updated-" + mode).WithRevision(7).WithLastLogin(at)
					if _, err := models.UserPermissionsLinkObjects.Create(t.Context(), backend, models.NewUserPermissionsLinkCreate(target.ID, f.grants[0].ID)); err != nil {
						return err
					}
				case "inactive":
					patch = patch.WithActive(false)
				case "changed_hash":
					patch = patch.WithEncodedPassword(changedHash)
				case "missing_principal":
					patch = patch.WithPrincipalID("replacement-principal")
				case "revision_overflow":
					patch = patch.WithRevision(math.MaxInt64)
				case "deleted":
					if _, err := models.UserObjects.Delete(t.Context(), backend, &target); err != nil {
						return err
					}
				case "session_deleted", "session_identity", "session_stamp":
					if err := base.Delete(t.Context(), current.ID()); err != nil {
						return err
					}
					if mode != "session_deleted" {
						key, value := auth.SessionPrincipalIDKey, "different-principal"
						if mode == "session_stamp" {
							key, value = auth.SessionCredentialStampKey, "different-stamp"
						}
						modified, err := current.WithValue(key, value)
						if err != nil {
							return err
						}
						created, err := base.Create(t.Context(), modified)
						if err != nil {
							return err
						}
						if !created {
							return errors.New("session recheck fixture not inserted")
						}
					}
				case "idle_expired":
					now = current.IdleExpiresAt()
				case "absolute_expired":
					now = current.AbsoluteExpiresAt()
				case "domain_changed":
					f.ownership = transitionSessionDomain(t, backend, f.binding, f.ownership)
				}
				if mode == "current_profile" || mode == "policy_rejection" || mode == "inactive" || mode == "changed_hash" || mode == "missing_principal" || mode == "revision_overflow" {
					if _, err := models.UserObjects.Patch(t.Context(), backend, target, patch); err != nil {
						return err
					}
				}
				checkpoint = passwordState(t, backend)
				return nil
			}
			result, err := configured.owner.ChangePassword(t.Context(), current.ID(), password, password+"-new")
			if hasher.hashes.Load() != 1 {
				t.Fatal("recheck did not follow exactly one hash")
			}
			if mode == "current_profile" {
				if err != nil || policies != 2 || !result.Credential.Principal().Has("account.direct") {
					t.Fatal("current profile/grants were not used", err)
				}
				stored := profileRow(t, reopen(t), target.ID)
				if stored.Revision != 8 || stored.FirstName != "after-hash" || stored.Username != "updated-"+mode || stored.LastLogin == nil || !stored.LastLogin.Equal(joined.Add(-time.Hour)) || stored.EncodedPassword == target.EncodedPassword {
					t.Fatal("password write overwrote concurrent profile state")
				}
				entries := profileHistory(t, backend, result.Credential.Principal(), target.ID)
				if len(entries) != 1 {
					t.Fatal("current profile password audit missing")
				}
			} else {
				passwordNoResult(t, result, err)
				if mode == "policy_rejection" {
					if diagnostics, rejected := validation.Rejected(err); !rejected || diagnostics.ByField("password").Empty() || policies != 2 {
						t.Fatal("current typed policy was not rechecked", err)
					}
				} else if mode != "revision_overflow" && err != auth.ErrInvalidCredentials {
					t.Fatal("stale session or account admitted", mode, err)
				}
				passwordUnchanged(t, reopen(t), checkpoint)
			}
		})
	}
}

func passwordVerifyRecheck(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	now := joined
	hasher := &passwordChangeHasher{PasswordHasher: f.hasher}
	configured := passwordComposition(t, backend, &now, hasher, accountmodel.PasswordChangeConfig[models.User]{})
	current := passwordSession(t, configured.manager, profileActor(t, f), "verify-recheck")
	var checkpoint passwordDurableState
	hasher.hashes.Store(0)
	hasher.afterVerify = func(context.Context) error {
		if _, err := models.UserObjects.Patch(t.Context(), backend, f.user, models.UserPatch{}.WithActive(false)); err != nil {
			return err
		}
		checkpoint = passwordState(t, backend)
		return nil
	}
	result, err := configured.owner.ChangePassword(t.Context(), current.ID(), password, password+"-new")
	passwordNoResult(t, result, err)
	if hasher.hashes.Load() != 0 || reflect.DeepEqual(checkpoint, passwordDurableState{}) {
		t.Fatal("verification recheck was bypassed")
	}
	passwordUnchanged(t, reopen(t), checkpoint)
}
