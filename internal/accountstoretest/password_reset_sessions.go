package accountstoretest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/validation"
)

func passwordResetSessionFence(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	for _, mode := range []string{"entry_new", "entry_existing", "finish_anonymous", "finish_self", "final_policy_expiry", "token_expiry", "current_payload_limit"} {
		t.Run(mode, func(t *testing.T) {
			now, tokenNow := joined, joined
			target := roleTarget(t, backend, f, "reset-fence-"+mode)
			base := passwordComposition(t, backend, &now, f.hasher, accountmodel.PasswordChangeConfig[models.User]{})
			config := selectedResetConfig(t, &tokenNow)
			owner := selectedResetOwner(t, base, config)
			token := selectedResetToken(t, owner, target.PrincipalID)
			var previous sessions.Record
			var err error
			if mode == "finish_self" {
				previous = passwordSession(t, base.manager, passwordCredential(t, f, target), "self-proof")
			} else if mode != "entry_new" {
				previous, err = base.manager.Create(t.Context(), map[string]string{"cart": "preserved"})
			}
			if err != nil {
				t.Fatal(err)
			}
			entry := mode == "entry_new" || mode == "entry_existing"
			if !entry {
				previous = selectedResetProof(t, owner, previous, token)
			}
			ready, start := make(chan struct{}, 1), make(chan struct{})
			tracked := &passwordScopeOwner{Backend: &passwordWriteGate{backend, ready, start}}
			hasher := &passwordChangeHasher{PasswordHasher: f.hasher}
			configured := passwordComposition(t, tracked, &now, hasher, accountmodel.PasswordChangeConfig[models.User]{})
			if mode == "current_payload_limit" {
				configured.manager, err = sessions.NewManager(configured.store, sessions.Config{Clock: func() time.Time { return now }, Limits: sessions.Limits{MaxValueBytes: 64}})
				if err != nil {
					t.Fatal(err)
				}
			}
			if mode == "final_policy_expiry" {
				config.ValidatePassword = func(context.Context, string, models.User) (validation.Errors, error) {
					if tracked.scopes.Load() != 0 {
						now = previous.IdleExpiresAt()
					}
					return validation.Errors{}, nil
				}
			}
			owner = selectedResetOwner(t, configured, config)
			hasher.hashes.Store(0)
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			type outcome struct {
				record sessions.Record
				result auth.PasswordResetResult
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				var result outcome
				if entry {
					result.record, result.err = owner.StartPasswordReset(ctx, previous.ID(), target.PrincipalID, token.Encoded())
				} else {
					result.result, result.err = owner.ResetPassword(ctx, previous.ID(), target.PrincipalID, password+"-new")
				}
				done <- result
			}()
			select {
			case <-ready:
			case <-ctx.Done():
				t.Fatal("reset did not wait at its native write gate")
			}
			switch mode {
			case "entry_new":
				now = joined.Add(30 * time.Minute)
			case "entry_existing", "finish_anonymous", "finish_self":
				now = previous.IdleExpiresAt()
			case "token_expiry":
				tokenNow = joined.Add(72*time.Hour + time.Second)
			case "current_payload_limit":
				changed, err := previous.WithValue("current-payload", string(bytes.Repeat([]byte{'x'}, 65)))
				if err != nil {
					t.Fatal(err)
				}
				if err := base.store.Delete(t.Context(), previous.ID()); err != nil {
					t.Fatal(err)
				}
				if created, err := base.store.Create(t.Context(), changed); err != nil || !created {
					t.Fatal("current larger payload fixture", err)
				}
			}
			before := passwordState(t, backend)
			close(start)
			result := <-done
			selectedResetNoResult(t, result.result, result.err)
			wantHashes := int64(1)
			if entry {
				wantHashes = 0
			}
			if !reflect.DeepEqual(result.record, sessions.Record{}) || hasher.hashes.Load() != wantHashes || tracked.writes.Load() != 1 {
				t.Fatal("refused reset repeated work or published a session")
			}
			if mode == "current_payload_limit" {
				requireProfileCode(t, result.err, accountmodel.CodePersistence)
			} else if result.err != auth.ErrInvalidResetProof {
				t.Fatal("expired proof/candidate was not refused after the gate", result.err)
			}
			passwordUnchanged(t, reopen(t), before)
		})
	}
}

func passwordResetEntropy(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	now := joined
	base := passwordComposition(t, backend, &now, f.hasher, accountmodel.PasswordChangeConfig[models.User]{})
	collision := passwordSession(t, base.manager, profileActor(t, f), "reset-collision-control")
	collisionBytes, err := base64.RawURLEncoding.DecodeString(collision.ID().Encoded())
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"create", "rotate", "finish"} {
		t.Run(stage, func(t *testing.T) {
			for _, mode := range []string{"confirmed_retry", "exhausted", "unknown_collision", "entropy_failure"} {
				t.Run(mode, func(t *testing.T) {
					target := roleTarget(t, backend, f, "reset-entropy-"+stage+"-"+mode)
					config := selectedResetConfig(t, &now)
					baseOwner := selectedResetOwner(t, base, config)
					token := selectedResetToken(t, baseOwner, target.PrincipalID)
					var previous sessions.Record
					if stage != "create" {
						previous, err = base.manager.Create(t.Context(), map[string]string{"cart": stage + mode})
						if err != nil {
							t.Fatal(err)
						}
					}
					if stage == "finish" {
						previous = selectedResetProof(t, baseOwner, previous, token)
					}
					unique := sha256.Sum256([]byte("selected-reset-entropy-" + stage + mode))
					data := append(append([]byte{}, collisionBytes...), unique[:]...)
					if mode == "exhausted" {
						data = bytes.Repeat(collisionBytes, 4)
					} else if mode == "entropy_failure" {
						data = nil
					}
					random := bytes.NewReader(data)
					faulty := &profileFaultBackend{Backend: backend}
					if mode == "unknown_collision" {
						faulty.mode, faulty.fault = "cleanup_failure", &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown}
					}
					hasher := &passwordChangeHasher{PasswordHasher: f.hasher}
					configured := passwordComposition(t, faulty, &now, hasher, accountmodel.PasswordChangeConfig[models.User]{})
					configured.manager, err = sessions.NewManager(configured.store, sessions.Config{Clock: func() time.Time { return now }, Random: random})
					if err != nil {
						t.Fatal(err)
					}
					owner := selectedResetOwner(t, configured, config)
					hasher.hashes.Store(0)
					before := passwordState(t, backend)
					var result auth.PasswordResetResult
					if stage == "finish" {
						result, err = owner.ResetPassword(t.Context(), previous.ID(), target.PrincipalID, password+"-new")
					} else {
						result.Record, err = owner.StartPasswordReset(t.Context(), previous.ID(), target.PrincipalID, token.Encoded())
					}
					wantHashes := int64(0)
					if stage == "finish" {
						wantHashes = 1
					}
					if hasher.hashes.Load() != wantHashes {
						t.Fatal("reset entropy retried password work")
					}
					if mode == "confirmed_retry" {
						if err != nil || !result.Record.ID().Valid() || result.ClearSession || faulty.calls != 2 || random.Len() != 0 {
							t.Fatal("confirmed reset collision did not retry only session allocation", err)
						}
						accountSessionRecord(t, base.store, result.Record, true)
						if stage == "finish" && (profileRow(t, backend, target.ID).Revision != 2 || len(profileHistory(t, backend, passwordCredential(t, f, target).Principal(), target.ID)) != 1) {
							t.Fatal("reset collision repeated or omitted account effects")
						}
					} else {
						selectedResetNoResult(t, result, err)
						if mode == "unknown_collision" {
							requireProfileCode(t, err, accountmodel.CodeOutcomeUnknown)
							if faulty.calls != 1 || random.Len() != 32 || !errors.Is(err, faulty.fault) {
								t.Fatal("uncertain reset collision retried or lost cause", err)
							}
						} else if mode == "exhausted" && (faulty.calls != 4 || random.Len() != 0) || mode == "entropy_failure" && faulty.calls != 0 {
							t.Fatal("reset entropy attempt bound changed", faulty.calls)
						}
						passwordUnchanged(t, reopen(t), before)
					}
					accountSessionRecord(t, base.store, collision, true)
				})
			}
		})
	}
	t.Run("self_clear_no_entropy", func(t *testing.T) {
		target := roleTarget(t, backend, f, "reset-self-no-entropy")
		owner := selectedResetOwner(t, base, selectedResetConfig(t, &now))
		previous := passwordSession(t, base.manager, passwordCredential(t, f, target), "self")
		proof := selectedResetProof(t, owner, previous, selectedResetToken(t, owner, target.PrincipalID))
		// No bytes are available. Successful self completion must only delete.
		base.manager, err = sessions.NewManager(base.store, sessions.Config{Clock: func() time.Time { return now }, Random: bytes.NewReader(nil)})
		if err != nil {
			t.Fatal(err)
		}
		owner = selectedResetOwner(t, base, selectedResetConfig(t, &now))
		result, err := owner.ResetPassword(t.Context(), proof.ID(), target.PrincipalID, password+"-self")
		if err != nil || !result.ClearSession || !reflect.DeepEqual(result.Record, sessions.Record{}) || profileRow(t, reopen(t), target.ID).Revision != 2 {
			t.Fatal("self reset attempted session allocation", err)
		}
		accountSessionRecord(t, base.store, proof, false)
	})
}

func passwordResetManySessions(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	now := joined
	tracked := &passwordScopeOwner{Backend: backend}
	hasher := &passwordChangeHasher{PasswordHasher: f.hasher, scopes: &tracked.scopes}
	configured := passwordComposition(t, tracked, &now, hasher, accountmodel.PasswordChangeConfig[models.User]{})
	owner := selectedResetOwner(t, configured, selectedResetConfig(t, &now))
	credential := profileActor(t, f)
	var prior []sessions.Record
	for range 257 {
		prior = append(prior, passwordSession(t, configured.manager, credential, "target-many"))
	}
	control, err := configured.manager.Create(t.Context(), map[string]string{"cart": "untouched"})
	if err != nil {
		t.Fatal(err)
	}
	proof := selectedResetProof(t, owner, prior[0], selectedResetToken(t, owner, f.user.PrincipalID))
	hasher.hashes.Store(0)
	tracked.writes.Store(0)
	result, err := owner.ResetPassword(t.Context(), proof.ID(), f.user.PrincipalID, password+"-batch")
	if err != nil || !result.ClearSession || hasher.hashes.Load() != 1 || tracked.writes.Load() != 1 || accountSessionCount(t, reopen(t)) != 1 || profileRow(t, backend, f.user.ID).Revision != 2 || len(profileHistory(t, backend, credential.Principal(), f.user.ID)) != 1 {
		t.Fatal("multi-batch reset revocation was not one complete transition", err)
	}
	for _, record := range append(prior, proof) {
		accountSessionRecord(t, configured.store, record, false)
	}
	accountSessionRecord(t, configured.store, control, true)
}
