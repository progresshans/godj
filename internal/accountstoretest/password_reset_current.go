package accountstoretest

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	hostmodels "github.com/progresshans/godj/conformance/identityfixture/models"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/validation"
)

func passwordResetCurrent(t *testing.T, open Open) {
	for _, mode := range []string{"profile", "policy", "password", "email", "last_login", "inactive", "unusable", "deleted", "expired", "future"} {
		t.Run(mode, func(t *testing.T) {
			backend, reopen := open(t)
			f := provisionSelfPassword(t, backend)
			if mode == "deleted" {
				for _, guard := range userReferenceSnapshot(t, reopen(t)).guards {
					if guard.OwnerID == f.target.ID {
						if count, err := hostmodels.GuardObjects.Delete(t.Context(), backend, &guard); err != nil || count != 1 {
							t.Fatal("reset delete fence fixture", err)
						}
					}
				}
			}
			config := f.config(t)
			config.Accounts.Now = f.clock
			config.Accounts.ValidatePassword = func(_ context.Context, _ string, row orm.ModelView[models.User]) (validation.Errors, error) {
				if passwordViewString(t, row, "first_name") == "refuse-current" {
					return validation.NewErrors(validation.New("password", "password_too_similar")), nil
				}
				return validation.Errors{}, nil
			}
			f.owner = newUserReferenceOwner(t, backend, f.userReferenceFixture, config)
			f.runtime = f.owner.runtime
			tokenAt, tokenNow := f.clock(), f.clock()
			resetConfig := resetNativeConfig(t, f)
			resetConfig.Timeout, resetConfig.Clock = time.Hour, func() time.Time { return tokenNow }
			owner, err := f.runtime.PasswordResetter(resetConfig)
			if err != nil {
				t.Fatal(err)
			}
			token := selectedResetToken(t, owner, f.target.PrincipalID)
			beforeSetup := userReferenceSnapshot(t, reopen(t))
			entropy := f.entropy.count()
			proof := selectedResetProof(t, owner, sessions.Record{}, token)
			afterSetup := userReferenceSnapshot(t, reopen(t))
			expected, wanted := resetEntryExpected(t, f, beforeSetup, afterSetup, sessions.Record{}, f.entropy.one(t, entropy), token.Encoded())
			if !reflect.DeepEqual(proof.Snapshot(), wanted.Snapshot()) {
				t.Fatal("reset current-state fixture lost its exact proof")
			}
			assertUserReferenceState(t, expected, afterSetup, f.actor.Principal())
			other := reopen(t)
			writer := newUserReferenceOwner(t, other, f.userReferenceFixture, config)
			f.instant.Add(int64(time.Minute))
			tokenNow = f.clock()
			f.owner.boundary.calls = 0
			f.owner.hasher.hashes.Store(0)
			f.owner.hasher.verifies.Store(0)
			f.owner.policies.Store(0)
			var checkpoint userReferenceState
			entropyAtCheckpoint := 0
			entered := false
			f.owner.hasher.beforeHash = func(ctx context.Context) error {
				entered = true
				before := userReferenceSnapshot(t, other)
				expected, row := before, f.target
				patch := models.UserPatch{}.WithRevision(row.Revision + 1)
				deleted := false
				switch mode {
				case "profile":
					patch = patch.WithUsername("renamed").WithFirstName("current reset profile").WithStaff(true)
					row.Username, row.FirstName, row.Staff = "renamed", "current reset profile", true
				case "policy":
					patch, row.FirstName = patch.WithFirstName("refuse-current"), "refuse-current"
				case "password":
					encoded, err := f.base.hasher.Hash(ctx, userReferencePassword)
					if err != nil {
						return err
					}
					patch, row.EncodedPassword = patch.WithEncodedPassword(encoded), encoded
				case "email":
					patch, row.Email = patch.WithEmail("concurrent@example.test"), "concurrent@example.test"
				case "last_login":
					at := f.clock()
					patch, row.LastLogin = patch.WithLastLogin(at), &at
				case "inactive":
					patch, row.Active = patch.WithActive(false), false
				case "unusable":
					encoded, err := auth.MakeUnusablePassword(ctx)
					if err != nil {
						return err
					}
					patch, row.EncodedPassword = patch.WithEncodedPassword(encoded), encoded
				case "deleted":
					result, err := writer.runtime.Accounts().Deleter().Delete(ctx, f.actor.Principal(), row.ID, row.Revision)
					if err != nil || result.ID != row.ID || result.Revision != row.Revision || result.DeletedRows != 3 || result.RevokedSessions != len(f.targetSessions) {
						t.Fatal("competing selected reset target deletion lost its complete graph", err)
					}
					expected.account.users = slices.DeleteFunc(slices.Clone(expected.account.users), func(value models.User) bool { return value.ID == row.ID })
					expected.account.direct = slices.DeleteFunc(slices.Clone(expected.account.direct), func(link models.UserPermissionsLink) bool { return link.SourceID == row.ID })
					expected.notes = slices.DeleteFunc(slices.Clone(expected.notes), func(note hostmodels.Note) bool { return note.OwnerID == row.ID })
					for _, record := range f.targetSessions {
						loginRemoveRecord(t, &expected.account, loginNamespace(t, f.credential), record.ID())
					}
					deleted = true
				case "expired":
					tokenNow = tokenAt.Add(time.Hour + time.Second)
				case "future":
					tokenNow = tokenAt.Add(-time.Second)
				}
				if mode != "deleted" && mode != "expired" && mode != "future" {
					row.Revision++
					if _, err := models.UserObjects.Patch(ctx, other, f.target, patch); err != nil {
						return err
					}
					userReferenceReplace(&expected.account, row)
				}
				checkpoint = userReferenceSnapshot(t, other)
				entropyAtCheckpoint = f.entropy.count()
				if deleted {
					assertUserReferenceStateAt(t, expected, checkpoint, f.actor.Principal(), f.clock(), catalogExpectedEvent{"user", row.ID, admin.ActionDelete, nil})
				} else {
					assertUserReferenceState(t, expected, checkpoint, f.actor.Principal())
				}
				f.target = row
				return nil
			}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			result, err := owner.ResetPassword(ctx, proof.ID(), f.target.PrincipalID, selfPassword)
			if !entered || f.owner.hasher.hashes.Load() != 1 || f.owner.hasher.verifies.Load() != 0 || f.owner.boundary.calls != 1 {
				t.Fatal("reset did not recheck after one outside-scope hash", err)
			}
			id := f.entropy.one(t, entropyAtCheckpoint)
			after := userReferenceSnapshot(t, reopen(t))
			if mode == "profile" {
				if err != nil || f.owner.policies.Load() != 2 {
					t.Fatal("current profile was not admitted by both reset policies", err)
				}
				expected, envelope := resetFinishExpected(t, f, f.owner, checkpoint, after, proof, id)
				if result.ClearSession != envelope.ClearSession || !reflect.DeepEqual(result.Record.Snapshot(), envelope.Record.Snapshot()) {
					t.Fatal("reset did not publish its current native result")
				}
				assertUserReferenceStateAt(t, expected, after, f.credential.Principal(), f.clock(), catalogExpectedEvent{"user", f.target.ID, admin.ActionChange, []string{"encoded_password"}})
				current, err := writer.runtime.Authenticator().Authenticate(t.Context(), f.target.Username, selfPassword)
				if err != nil || current.Principal().ID() != f.target.PrincipalID || !current.Principal().Staff() {
					t.Fatal("reset lost the competing current profile on reconnect", err)
				}
			} else {
				selectedResetNoResult(t, result, err)
				if mode == "policy" {
					if diagnostics, rejected := validation.Rejected(err); !rejected || diagnostics.ByField("password").Empty() || errors.Unwrap(err) != nil || f.owner.policies.Load() != 2 {
						t.Fatal("current reset policy was not a direct refusal", err)
					}
				} else if err != auth.ErrInvalidResetProof {
					t.Fatal("current account or token invalidation lost its direct refusal", mode, err)
				}
				if !reflect.DeepEqual(checkpoint, after) {
					t.Fatal("refused reset changed the competing writer's exact native/host state")
				}
			}
			for _, value := range []any{result, err, owner} {
				selfPasswordPrivate(t, value, selfPassword, token.Encoded(), proof.ID().Encoded(), id.Encoded(), f.owner.encoded.Load().(string))
			}
		})
	}
}
