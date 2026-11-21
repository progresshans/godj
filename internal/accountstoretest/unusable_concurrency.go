package accountstoretest

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
)

type unusableBeforeWrite struct {
	Backend
	before func(context.Context) error
	calls  int
}

func (owner *unusableBeforeWrite) CoordinatedAtomic(ctx context.Context, write func(db.Session) error) error {
	owner.calls++
	if err := owner.before(ctx); err != nil {
		return err
	}
	return owner.Backend.CoordinatedAtomic(ctx, write)
}

func runUnusableFences(t *testing.T, open Open) {
	t.Run("unusable_fences", func(t *testing.T) {
		for _, mode := range []string{"actor_deactivated", "group_grant_removed", "target_revision", "target_hash_without_revision"} {
			t.Run(mode, func(t *testing.T) {
				backend, reopen := open(t)
				f := provisionUnusable(t, backend, 3)
				config := unusableConfig(t)
				// Retain the earlier superuser principal while the database
				// gives it only a current group-owned password permission.
				if !f.actor.Principal().Superuser() {
					t.Fatal("unusable fence lacks stale superuser input")
				}
				currentActor, err := models.UserObjects.Patch(t.Context(), backend, f.base.user, models.UserPatch{}.WithSuperuser(false).WithRevision(3))
				if err != nil {
					t.Fatal(err)
				}
				permission, err := models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate(string(config.Accounts.Permissions.Change), "Change password"))
				if err != nil {
					t.Fatal(err)
				}
				group, err := models.GroupObjects.Create(t.Context(), backend, models.NewGroupCreate("Unusable password editors"))
				if err != nil {
					t.Fatal(err)
				}
				grant, err := models.GroupPermissionsLinkObjects.Create(t.Context(), backend, models.NewGroupPermissionsLinkCreate(group.ID, permission.ID))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := models.UserGroupsLinkObjects.Create(t.Context(), backend, models.NewUserGroupsLinkCreate(currentActor.ID, group.ID)); err != nil {
					t.Fatal(err)
				}
				current, err := f.owner.runtime.Authenticator().Resolve(t.Context(), currentActor.PrincipalID)
				if err != nil || current.Principal().Superuser() || !current.Principal().Has(config.Accounts.Permissions.Change) {
					t.Fatal("unusable fixture did not admit through its current group grant", err)
				}
				before := userReferenceSnapshot(t, reopen(t))
				expected := before
				switch mode {
				case "actor_deactivated":
					row := currentActor
					row.Active, row.Revision = false, row.Revision+1
					userReferenceReplace(&expected.account, row)
				case "group_grant_removed":
					expected.account.grants = slices.DeleteFunc(slices.Clone(expected.account.grants), func(row models.GroupPermissionsLink) bool { return row.ID == grant.ID })
				case "target_revision":
					row := f.target
					row.Revision++
					userReferenceReplace(&expected.account, row)
				case "target_hash_without_revision":
					row := f.target
					row.EncodedPassword = "!independent"
					userReferenceReplace(&expected.account, row)
				}
				writer := reopen(t)
				boundary := &unusableBeforeWrite{Backend: backend, before: func(ctx context.Context) error {
					ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
					defer cancel()
					return writer.CoordinatedAtomic(ctx, func(scope db.Session) error {
						switch mode {
						case "actor_deactivated":
							_, err := models.UserObjects.Patch(ctx, scope, currentActor, models.UserPatch{}.WithActive(false).WithRevision(currentActor.Revision+1))
							return err
						case "group_grant_removed":
							count, err := models.GroupPermissionsLinkObjects.Delete(ctx, scope, &grant)
							if err != nil || count != 1 {
								return errors.Join(err, errors.New("unusable fence did not remove its current group grant"))
							}
							return nil
						case "target_revision":
							_, err := models.UserObjects.Patch(ctx, scope, f.target, models.UserPatch{}.WithRevision(f.target.Revision+1))
							return err
						default:
							_, err := models.UserObjects.Patch(ctx, scope, f.target, models.UserPatch{}.WithEncodedPassword("!independent"))
							return err
						}
					})
				}}
				owner := newUserReferenceOwner(t, boundary, f.userReferenceFixture, config)
				result, err := owner.runtime.Accounts().Passwords().SetUnusablePassword(t.Context(), f.actor.Principal(), f.target.ID, 1)
				passwordEditNoResult(t, result, err)
				code := accountmodel.CodePermission
				if mode == "target_revision" || mode == "target_hash_without_revision" {
					code = accountmodel.CodeConflict
				}
				requireProfileCode(t, err, code)
				if owner.hasher.hashes.Load() != 0 || owner.policies.Load() != 0 || owner.boundary.calls != 1 || boundary.calls != 1 {
					t.Fatal("unusable final fence hashed, validated a raw secret or replayed")
				}
				assertUserReferenceState(t, expected, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
				for _, record := range f.records {
					accountSessionRecord(t, f.owner.runtime.SessionStore(), record, true)
				}
			})
		}
	})
}

func runUnusableConcurrentReplacement(t *testing.T, open Open) {
	t.Run("unusable_concurrent_replacement", func(t *testing.T) {
		backend, reopen := open(t)
		f := provisionUnusable(t, backend, 3)
		before := userReferenceSnapshot(t, reopen(t))
		credential, err := f.owner.runtime.Authenticator().Resolve(t.Context(), f.target.PrincipalID)
		if err != nil {
			t.Fatal(err)
		}
		ready, start := make(chan struct{}, 2), make(chan struct{})
		owners := []*userReferenceOwner{
			newUserReferenceOwner(t, &passwordWriteGate{backend, ready, start}, f.userReferenceFixture, unusableConfig(t)),
			newUserReferenceOwner(t, &passwordWriteGate{reopen(t), ready, start}, f.userReferenceFixture, unusableConfig(t)),
		}
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		var release sync.Once
		unblock := func() { release.Do(func() { close(start) }) }
		var workers sync.WaitGroup
		defer func() { cancel(); unblock(); workers.Wait() }()
		type outcome struct {
			index  int
			result accountmodel.PasswordEditResult
			err    error
		}
		done := make(chan outcome, 2)
		for index, owner := range owners {
			workers.Add(1)
			go func() {
				defer workers.Done()
				var result accountmodel.PasswordEditResult
				var err error
				if index == 0 {
					result, err = owner.runtime.Accounts().Passwords().SetUnusablePassword(ctx, f.actor.Principal(), f.target.ID, 1)
				} else {
					result, err = owner.runtime.Accounts().Passwords().SetPassword(ctx, f.actor.Principal(), f.target.ID, 1, userReferencePassword)
				}
				done <- outcome{index, result, err}
			}()
		}
		for range 2 {
			select {
			case <-ready:
			case early := <-done:
				t.Fatal("credential command failed before both native fences", early.err)
			case <-ctx.Done():
				t.Fatal("independent disable/replace commands did not reach both native fences", ctx.Err())
			}
		}
		unblock()
		winner := -1
		for range 2 {
			select {
			case result := <-done:
				if result.err == nil {
					if winner != -1 || result.result.ID != f.target.ID || result.result.Revision != 2 || result.result.RevokedSessions != 3 {
						t.Fatal("disable/replace race did not have one complete winner")
					}
					winner = result.index
				} else {
					passwordEditNoResult(t, result.result, result.err)
					requireProfileCode(t, result.err, accountmodel.CodeConflict)
				}
			case <-ctx.Done():
				t.Fatal("disable/replace race did not finish within its deadline", ctx.Err())
			}
		}
		workers.Wait()
		if winner < 0 || owners[0].hasher.hashes.Load() != 0 || owners[0].policies.Load() != 0 || owners[1].hasher.hashes.Load() != 1 || owners[0].boundary.calls != 1 || owners[1].boundary.calls != 1 {
			t.Fatal("disable/replace race repeated password work or a native attempt")
		}
		policies := int64(1)
		if winner == 1 {
			policies++
		}
		if owners[1].policies.Load() != policies {
			t.Fatal("replacement policy did not follow its actual winning or stale phases")
		}
		f.changed(t, before, owners[winner], winner == 0)
		after := userReferenceSnapshot(t, reopen(t))
		current, err := f.owner.runtime.Authenticator().Resolve(t.Context(), f.target.PrincipalID)
		if err != nil || current.HasUsablePassword() != (winner == 1) || current.MatchesSessionStamp(credential.SessionStamp()) {
			t.Fatal("race winner did not rotate the selected credential", err)
		}
		_, err = f.owner.runtime.Authenticator().Authenticate(t.Context(), "member", userReferencePassword)
		if winner == 1 && err != nil || winner == 0 && !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatal("race winner has a different password authentication result", err)
		}
		if _, err := f.owner.runtime.Authenticator().Authenticate(t.Context(), "member", password); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatal("race retained the old password", err)
		}
		assertUserReferenceState(t, after, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
	})
}
