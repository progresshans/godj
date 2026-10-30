package accountstoretest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/schema/ir"
)

func passwordAdminConcurrent(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	for _, mode := range []string{"administrator", "self_change", "login"} {
		t.Run(mode, func(t *testing.T) {
			target := roleTarget(t, backend, f, "admin-race-"+mode)
			credential := passwordCredential(t, f, target)
			now := joined
			base, manager, _ := accountSessions(t, backend, &now)
			previous := passwordSession(t, manager, credential, "old-browser")
			anonymous, err := manager.Create(t.Context(), map[string]string{"anonymous": "pending-login"})
			if err != nil {
				t.Fatal(err)
			}
			beforeCount := accountSessionCount(t, backend)
			ready, start := make(chan struct{}, 2), make(chan struct{})
			hashers := []*passwordChangeHasher{{PasswordHasher: f.hasher}, {PasswordHasher: f.hasher}}
			configured := passwordComposition(t, &passwordWriteGate{backend, ready, start}, &now, hashers[0], accountmodel.PasswordChangeConfig[models.User]{})
			peer := passwordComposition(t, &passwordWriteGate{reopen(t), ready, start}, &now, hashers[1], accountmodel.PasswordChangeConfig[models.User]{})
			owner := passwordEditOwner(t, configured, passwordEditConfig())
			other := passwordEditOwner(t, peer, passwordEditConfig())
			login, err := peer.store.LoginPersistence(peer.manager)
			if err != nil {
				t.Fatal(err)
			}
			for _, hasher := range hashers {
				hasher.hashes.Store(0)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			var wait sync.WaitGroup
			var result, otherResult accountmodel.PasswordEditResult
			var selfResult auth.PasswordChangeResult
			var loginResult auth.SessionLoginResult
			var failure, otherFailure error
			wait.Add(2)
			go func() {
				defer wait.Done()
				result, failure = owner.SetPassword(ctx, actor, target.ID, 1, password+"-admin")
			}()
			go func() {
				defer wait.Done()
				switch mode {
				case "administrator":
					otherResult, otherFailure = other.SetPassword(ctx, actor, target.ID, 1, password+"-peer")
				case "self_change":
					selfResult, otherFailure = peer.owner.ChangePassword(ctx, previous.ID(), password, password+"-self")
				case "login":
					loginResult, otherFailure = login.Login(ctx, selectedSessionLogin(credential, anonymous, now))
				}
			}()
			for range 2 {
				select {
				case <-ready:
				case <-ctx.Done():
					t.Fatal("independent admin and peer writes did not reach native fences")
				}
			}
			close(start)
			wait.Wait()
			if hashers[0].hashes.Load() != 1 || mode != "login" && hashers[1].hashes.Load() != 1 {
				t.Fatal("concurrent password owner retried hash")
			}
			expectedCount := beforeCount - 1
			passwordWant := password + "-admin"
			if mode == "login" {
				if failure != nil || result.Revision != 2 {
					t.Fatal("concurrent login blocked administrative password change", failure)
				}
				if otherFailure != nil && otherFailure != auth.ErrInvalidCredentials {
					t.Fatal("stale login became storage failure", otherFailure)
				}
				if otherFailure == nil {
					expectedCount--
					accountSessionRecord(t, base, loginResult.Record, false)
					accountSessionRecord(t, base, anonymous, false)
				} else {
					if loginResult.Record.ID().Valid() {
						t.Fatal("denied login published session")
					}
					accountSessionRecord(t, base, anonymous, true)
				}
			} else {
				if (failure == nil) == (otherFailure == nil) {
					t.Fatal("administrative password race did not have exactly one winner", failure, otherFailure)
				}
				if failure != nil {
					passwordEditNoResult(t, result, failure)
					requireProfileCode(t, failure, accountmodel.CodeConflict)
					passwordWant = password + "-peer"
					if mode == "self_change" {
						passwordWant = password + "-self"
						expectedCount++
						accountSessionRecord(t, base, selfResult.Record, true)
					}
				} else if mode == "administrator" {
					passwordEditNoResult(t, otherResult, otherFailure)
					requireProfileCode(t, otherFailure, accountmodel.CodeConflict)
				} else {
					passwordNoResult(t, selfResult, otherFailure)
					if !errors.Is(otherFailure, auth.ErrInvalidCredentials) {
						t.Fatal("self change retained invalid credential", otherFailure)
					}
				}
				accountSessionRecord(t, base, anonymous, true)
			}
			stored := profileRow(t, reopen(t), target.ID)
			if ok, err := f.hasher.Verify(t.Context(), passwordWant, stored.EncodedPassword); err != nil || !ok {
				t.Fatal("race winner password not durable", err)
			}
			if stored.Revision != 2 || len(profileHistory(t, backend, actor, target.ID)) != 1 || accountSessionCount(t, backend) != expectedCount {
				t.Fatal("password race left partial or repeated effects")
			}
			accountSessionRecord(t, base, previous, false)
		})
	}
}

func passwordAdminNonpositiveKeys(t *testing.T, backend Backend, storedKey StoredKeyFixture) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	now := joined
	configured := passwordComposition(t, backend, &now, f.hasher, accountmodel.PasswordChangeConfig[models.User]{})
	owner := passwordEditOwner(t, configured, passwordEditConfig())
	for _, key := range []int64{0, -17} {
		t.Run(fmt.Sprintf("key_%d", key), func(t *testing.T) {
			row := roleTarget(t, backend, f, fmt.Sprintf("admin-password-key-%d", key))
			storedKey(t, backend, "user", row.ID, key)
			(models.UserDescriptor{}).SetPrimaryKey(&row, key)
			old := passwordSession(t, configured.manager, passwordCredential(t, f, row), "signed-key")
			result, err := owner.SetPassword(t.Context(), actor, key, 1, password+"-new")
			if err != nil || result.ID != key || result.Revision != 2 || result.RevokedSessions != 1 {
				t.Fatal("nonpositive administrative password key lost", err)
			}
			result, err = owner.SetUnusablePassword(t.Context(), actor, key, 2)
			if err != nil || result.ID != key || result.Revision != 3 {
				t.Fatal("nonpositive unusable password key lost", err)
			}
			stored := profileRow(t, backend, key)
			entries := profileHistory(t, backend, actor, key)
			if stored.Revision != 3 || stored.EncodedPassword == row.EncodedPassword || len(entries) != 2 || entries[1].ObjectID() != key {
				t.Fatal("nonpositive password row/audit mismatch")
			}
			accountSessionRecord(t, configured.store, old, false)
		})
	}
}
