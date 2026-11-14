package accountstoretest

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	hostproject "github.com/progresshans/godj/conformance/identityfixture/project"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/validation"
)

func passwordResetSessionFence(t *testing.T, open Open) {
	for _, mode := range []string{"entry_new", "entry_existing", "finish_anonymous", "finish_self", "final_policy_expiry", "token_expiry", "current_payload_limit", "entry_new_email", "entry_existing_email"} {
		t.Run(mode, func(t *testing.T) {
			backend, reopen := open(t)
			f := provisionSelfPassword(t, backend)
			base := f.runtime
			tokenNow := f.clock()
			baseReset := resetNativeOwner(t, f)
			token := selectedResetToken(t, baseReset, f.target.PrincipalID)
			var previous sessions.Record
			entry := strings.HasPrefix(mode, "entry_")
			if mode == "finish_self" {
				previous = f.targetSessions[0]
			} else if mode != "entry_new" && mode != "entry_new_email" {
				previous = resetBrowserPrevious(t, f, "anonymous")
			}
			if !entry {
				before := userReferenceSnapshot(t, reopen(t))
				entropy := f.entropy.count()
				proof := selectedResetProof(t, baseReset, previous, token)
				after := userReferenceSnapshot(t, reopen(t))
				expected, wanted := resetEntryExpected(t, f, before, after, previous, f.entropy.one(t, entropy), token.Encoded())
				if !reflect.DeepEqual(proof.Snapshot(), wanted.Snapshot()) {
					t.Fatal("reset gate fixture lost exact proof")
				}
				assertUserReferenceState(t, expected, after, f.actor.Principal())
				previous = wanted
				if mode == "finish_self" {
					f.targetSessions[0] = wanted
				}
			}
			ready, start := make(chan struct{}, 1), make(chan struct{})
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			var once sync.Once
			release := func() { once.Do(func() { close(start) }) }
			var worker sync.WaitGroup
			defer func() { cancel(); release(); worker.Wait() }()
			config := f.config(t)
			if mode == "final_policy_expiry" {
				config.Accounts.ValidatePassword = func(context.Context, string, orm.ModelView[models.User]) (validation.Errors, error) {
					if f.owner.hasher.scopes.Load() != 0 {
						f.instant.Store(previous.IdleExpiresAt().UnixNano())
					}
					return validation.Errors{}, nil
				}
			}
			f.owner = newUserReferenceOwner(t, &passwordWriteGate{backend, ready, start}, f.userReferenceFixture, config)
			f.runtime = f.owner.runtime
			resetConfig := resetNativeConfig(t, f)
			resetConfig.Clock = func() time.Time { return tokenNow }
			owner, err := f.runtime.PasswordResetter(resetConfig)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "current_payload_limit" {
				managerConfig := config.Sessions
				managerConfig.Limits.MaxValueBytes = 64
				manager, err := sessions.NewManager(f.runtime.SessionStore(), managerConfig)
				if err != nil {
					t.Fatal(err)
				}
				owner, err = hostproject.NewAccountPasswordResetter(f.runtime.SessionStore(), manager, f.runtime.Authenticator(), accountmodel.PasswordResetConfig[models.User]{Keys: resetConfig.Keys, Clock: resetConfig.Clock, ValidatePassword: config.Accounts.ValidatePassword})
				if err != nil {
					t.Fatal(err)
				}
			}
			f.instant.Add(int64(time.Minute))
			entropy := f.entropy.count()
			type outcome struct {
				result auth.PasswordResetResult
				err    error
			}
			done := make(chan outcome, 1)
			worker.Add(1)
			go func() {
				defer worker.Done()
				var value outcome
				if entry {
					value.result.Record, value.err = owner.StartPasswordReset(ctx, previous.ID(), f.target.PrincipalID, token.Encoded())
				} else {
					value.result, value.err = owner.ResetPassword(ctx, previous.ID(), f.target.PrincipalID, selfPassword)
				}
				done <- value
			}()
			select {
			case <-ready:
			case value := <-done:
				t.Fatal("reset completed before its native gate", value.err)
			case <-ctx.Done():
				t.Fatal("reset did not reach its native gate", ctx.Err())
			}
			before := userReferenceSnapshot(t, reopen(t))
			expected := before
			var changed sessions.Record
			switch mode {
			case "entry_new":
				f.instant.Add(int64(loginIdle))
			case "entry_existing", "finish_anonymous", "finish_self":
				f.instant.Store(previous.IdleExpiresAt().UnixNano())
			case "token_expiry":
				tokenNow = tokenNow.Add(73 * time.Hour)
			case "entry_new_email", "entry_existing_email":
				row := f.target
				row.Email = "changed-at-entry@example.test"
				if _, err := models.UserObjects.Patch(t.Context(), reopen(t), f.target, models.UserPatch{}.WithEmail(row.Email)); err != nil {
					t.Fatal(err)
				}
				userReferenceReplace(&expected.account, row)
			case "current_payload_limit":
				changed, err = previous.WithValue("current-payload", strings.Repeat("x", 65))
				if err != nil {
					t.Fatal(err)
				}
				if err := base.SessionStore().Delete(t.Context(), previous.ID()); err != nil {
					t.Fatal(err)
				}
				if inserted, err := base.SessionStore().Create(t.Context(), changed); err != nil || !inserted {
					t.Fatal("current oversized payload fixture", err)
				}
				loginRemoveRecord(t, &expected.account, loginNamespace(t, f.credential), previous.ID())
			}
			checkpoint := userReferenceSnapshot(t, reopen(t))
			if mode == "current_payload_limit" {
				loginPutRecord(t, &expected.account, checkpoint.account, loginNamespace(t, f.credential), changed, false)
			}
			assertUserReferenceState(t, expected, checkpoint, f.actor.Principal())
			release()
			var value outcome
			select {
			case value = <-done:
			case <-ctx.Done():
				t.Fatal("reset did not finish after its native gate", ctx.Err())
			}
			worker.Wait()
			selectedResetNoResult(t, value.result, value.err)
			if mode == "current_payload_limit" {
				requireProfileCode(t, value.err, accountmodel.CodePersistence)
			} else if value.err != auth.ErrInvalidResetProof {
				t.Fatal("reset gate lost its direct current refusal", value.err)
			}
			hashes := int64(1)
			if entry {
				hashes = 0
			}
			entropyDelta := 1
			if mode == "finish_self" {
				entropyDelta = 0
			}
			if f.owner.hasher.hashes.Load() != hashes || f.owner.hasher.verifies.Load() != 0 || f.owner.boundary.calls != 1 || f.entropy.count() != entropy+entropyDelta {
				t.Fatal("reset gate repeated preparation or persistence")
			}
			if mode == "final_policy_expiry" && f.owner.policies.Load() != 2 {
				t.Fatal("proof expiry was not checked after current policy")
			}
			assertUserReferenceState(t, checkpoint, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
		})
	}
}
