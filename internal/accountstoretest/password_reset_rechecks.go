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
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/validation"
)

func passwordResetRechecks(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	changedHash, err := f.hasher.Hash(t.Context(), "competing-reset-password")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"current_profile", "policy_rejection", "email", "last_login_precision", "inactive", "unusable", "changed_hash", "changed_principal", "deleted", "zero_revision", "revision_overflow", "session_deleted", "session_proof_target", "session_proof_token", "session_auth_id", "session_auth_stamp", "current_payload", "idle_renewed", "idle_expired", "absolute_expired", "domain_changed"} {
		t.Run(mode, func(t *testing.T) {
			now := joined
			target := roleTarget(t, backend, f, "reset-recheck-"+mode)
			lastLogin := joined.Add(-time.Hour).Add(123456 * time.Microsecond)
			if _, err := models.UserObjects.Patch(t.Context(), backend, target, models.UserPatch{}.WithEmail("member@example.test").WithLastLogin(lastLogin)); err != nil {
				t.Fatal(err)
			}
			tracked := &passwordScopeOwner{Backend: backend}
			hasher := &passwordChangeHasher{PasswordHasher: f.hasher, scopes: &tracked.scopes}
			configured := passwordComposition(t, tracked, &now, hasher, accountmodel.PasswordChangeConfig[models.User]{})
			config := selectedResetConfig(t, &now)
			policies := 0
			config.ValidatePassword = func(_ context.Context, _ string, row models.User) (validation.Errors, error) {
				policies++
				if row.EncodedPassword != "" {
					t.Fatal("reset policy exposed encoded password")
				}
				if row.FirstName == "after-hash" {
					if mode == "policy_rejection" {
						return validation.NewErrors(validation.New("password", "similar_profile")), nil
					}
					if mode != "current_profile" || row.Username != "renamed-"+mode || row.Revision != 7 {
						t.Fatal("reset policy missed current typed state")
					}
				}
				return validation.Errors{}, nil
			}
			owner := selectedResetOwner(t, configured, config)
			token := selectedResetToken(t, owner, target.PrincipalID)
			proof := selectedResetProof(t, owner, sessions.Record{}, token)
			base, manager, _ := accountSessions(t, backend, &now)
			var checkpoint passwordDurableState
			hasher.hashes.Store(0)
			hasher.verifies.Store(0)
			tracked.writes.Store(0)
			hasher.beforeHash = func(context.Context) error {
				patch := models.UserPatch{}
				patchAccount := true
				switch mode {
				case "current_profile", "policy_rejection":
					patch = patch.WithFirstName("after-hash").WithUsername("renamed-" + mode).WithRevision(7)
					if _, err := models.UserPermissionsLinkObjects.Create(t.Context(), backend, models.NewUserPermissionsLinkCreate(target.ID, f.grants[0].ID)); err != nil {
						return err
					}
				case "email":
					patch = patch.WithEmail("changed@example.test")
				case "last_login_precision":
					patch = patch.WithLastLogin(lastLogin.Add(time.Microsecond))
				case "inactive":
					patch = patch.WithActive(false)
				case "unusable":
					patch = patch.WithEncodedPassword("!explicit-unusable-marker")
				case "changed_hash":
					patch = patch.WithEncodedPassword(changedHash)
				case "changed_principal":
					patch = patch.WithPrincipalID("replacement-principal")
				case "zero_revision":
					patch = patch.WithRevision(0)
				case "revision_overflow":
					patch = patch.WithRevision(math.MaxInt64)
				case "deleted":
					patchAccount = false
					if _, err := models.UserObjects.Delete(t.Context(), backend, &target); err != nil {
						return err
					}
				case "session_deleted", "session_proof_target", "session_proof_token", "session_auth_id", "session_auth_stamp", "current_payload":
					patchAccount = false
					if err := base.Delete(t.Context(), proof.ID()); err != nil {
						return err
					}
					if mode != "session_deleted" {
						key, value := "latest-payload", "must-survive"
						switch mode {
						case "session_proof_target":
							key, value = auth.SessionResetPrincipalIDKey, f.user.PrincipalID
						case "session_proof_token":
							key, value = auth.SessionResetTokenKey, selectedResetToken(t, owner, f.user.PrincipalID).Encoded()
						case "session_auth_id":
							key, value = auth.SessionPrincipalIDKey, f.user.PrincipalID
						case "session_auth_stamp":
							key, value = auth.SessionCredentialStampKey, "new-authentication-stamp"
						}
						changed, err := proof.WithValue(key, value)
						if err != nil {
							return err
						}
						if inserted, err := base.Create(t.Context(), changed); err != nil || !inserted {
							return errors.Join(err, errors.New("reset proof fixture was not replaced"))
						}
					}
				case "idle_renewed":
					patchAccount = false
					now = joined.Add(20 * time.Minute)
					if _, found, err := manager.Load(t.Context(), proof.ID()); err != nil || !found {
						return errors.Join(err, errors.New("reset proof was not renewed"))
					}
					now = joined.Add(35 * time.Minute)
				case "idle_expired":
					patchAccount = false
					now = proof.IdleExpiresAt()
				case "absolute_expired":
					patchAccount = false
					now = proof.AbsoluteExpiresAt()
				case "domain_changed":
					patchAccount = false
					f.ownership = transitionSessionDomain(t, backend, f.binding, f.ownership)
				}
				if patchAccount {
					if _, err := models.UserObjects.Patch(t.Context(), backend, target, patch); err != nil {
						return err
					}
				}
				checkpoint = passwordState(t, backend)
				return nil
			}
			result, err := owner.ResetPassword(t.Context(), proof.ID(), target.PrincipalID, password+"-reset")
			if hasher.hashes.Load() != 1 || hasher.verifies.Load() != 0 {
				t.Fatal("reset repeated hash or verified old password")
			}
			wantWrites := int64(1)
			if mode == "absolute_expired" {
				wantWrites = 0
			}
			if tracked.writes.Load() != wantWrites {
				t.Fatal("reset repeated final fence", tracked.writes.Load(), wantWrites)
			}
			if mode == "current_profile" || mode == "current_payload" || mode == "idle_renewed" {
				if err != nil || policies != 2 || result.ClearSession || !result.Record.ID().Valid() {
					t.Fatal("current reset state was not preserved", err)
				}
				stored := profileRow(t, reopen(t), target.ID)
				wantRevision := int64(2)
				if mode == "current_profile" {
					wantRevision = 8
				}
				if stored.Revision != wantRevision || stored.LastLogin == nil || !stored.LastLogin.Equal(lastLogin) || stored.Email != "member@example.test" || stored.EncodedPassword == target.EncodedPassword {
					t.Fatal("reset restored stale profile or lost current revision")
				}
				if mode == "current_profile" && (stored.FirstName != "after-hash" || stored.Username != "renamed-"+mode) {
					t.Fatal("reset overwrote concurrent profile")
				}
				if mode == "current_payload" {
					if value, _ := result.Record.Value("latest-payload"); value != "must-survive" {
						t.Fatal("reset overwrote current browser payload")
					}
				}
				if mode == "idle_renewed" && !result.Record.IdleExpiresAt().After(now) {
					t.Fatal("reset used stale idle expiry")
				}
				if _, stored := result.Record.Value(auth.SessionResetTokenKey); stored {
					t.Fatal("reset proof remained")
				}
				accountSessionRecord(t, configured.store, proof, false)
				accountSessionRecord(t, configured.store, result.Record, true)
			} else {
				selectedResetNoResult(t, result, err)
				switch mode {
				case "policy_rejection":
					if diagnostics, rejected := validation.Rejected(err); !rejected || diagnostics.ByField("password").Empty() || policies != 2 {
						t.Fatal("reset missed current policy", err)
					}
				case "zero_revision", "revision_overflow":
					if err == auth.ErrInvalidResetProof {
						t.Fatal("damaged account became ordinary invalid proof")
					}
				default:
					if err != auth.ErrInvalidResetProof {
						t.Fatal("changed reset authority was not a direct refusal", mode, err)
					}
				}
				passwordUnchanged(t, reopen(t), checkpoint)
			}
			if mode == "current_profile" {
				current := passwordCredential(t, f, profileRow(t, backend, target.ID))
				if !reflect.DeepEqual(current.Principal().Permissions(), []auth.Permission{"account.direct"}) {
					t.Fatal("reset reverted current grants")
				}
			}
		})
	}
}
