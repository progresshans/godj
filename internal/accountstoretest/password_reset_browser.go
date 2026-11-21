package accountstoretest

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	hostproject "github.com/progresshans/godj/conformance/identityfixture/project"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/sessions"
)

func passwordResetBrowserCurrent(t *testing.T, open Open) {
	for _, mode := range []string{"logout", "replacement", "proof_target", "proof_token", "authentication_id", "authentication_stamp", "authentication", "payload", "idle_renewed"} {
		t.Run(mode, func(t *testing.T) {
			backend, reopen := open(t)
			f := provisionSelfPassword(t, backend)
			owner := resetNativeOwner(t, f)
			token := selectedResetToken(t, owner, f.target.PrincipalID)
			proof := selectedResetProof(t, owner, sessions.Record{}, token)
			other := reopen(t)
			writer := newUserReferenceOwner(t, other, f.userReferenceFixture, f.config(t))
			peer, err := writer.runtime.PasswordResetter(resetNativeConfig(t, f))
			if err != nil {
				t.Fatal(err)
			}
			actorToken := selectedResetToken(t, peer, f.actor.Principal().ID())
			f.instant.Add(int64(time.Minute))
			f.owner.boundary.calls = 0
			var checkpoint userReferenceState
			var changed sessions.Record
			entropyAtCheckpoint := 0
			entered := false
			f.owner.hasher.beforeHash = func(ctx context.Context) error {
				entered = true
				before := userReferenceSnapshot(t, other)
				expected := before
				changed = proof
				var err error
				switch mode {
				case "logout":
					if err := writer.runtime.Sessions().Delete(ctx, proof.ID()); err != nil {
						return err
					}
					loginRemoveRecord(t, &expected.account, loginNamespace(t, f.credential), proof.ID())
				case "replacement":
					entropy := f.entropy.count()
					result, err := peer.StartPasswordReset(ctx, proof.ID(), f.actor.Principal().ID(), actorToken.Encoded())
					if err != nil {
						return err
					}
					values := proof.Values()
					values[auth.SessionResetPrincipalIDKey], values[auth.SessionResetTokenKey] = f.actor.Principal().ID(), actorToken.Encoded()
					changed = resetExpectedRecord(t, proof, f.entropy.one(t, entropy), values, f.clock())
					if !reflect.DeepEqual(changed.Snapshot(), result.Snapshot()) {
						t.Fatal("competing proof rotation lost its exact current target")
					}
					loginRemoveRecord(t, &expected.account, loginNamespace(t, f.credential), proof.ID())
				case "idle_renewed":
					at := proof.CreatedAt().Add(9 * time.Minute)
					f.instant.Store(at.UnixNano())
					snapshot := proof.Snapshot()
					snapshot.AccessedAt, snapshot.IdleExpiresAt = at, at.Add(loginIdle)
					changed, err = sessions.RestoreRecord(snapshot, sessions.Limits{})
					if err != nil {
						return err
					}
					loaded, found, err := writer.runtime.Sessions().Load(ctx, proof.ID())
					if err != nil || !found || !reflect.DeepEqual(loaded.Snapshot(), changed.Snapshot()) {
						t.Fatal("competing access did not renew the expected native proof", err)
					}
					f.instant.Store(proof.CreatedAt().Add(11 * time.Minute).UnixNano())
				default:
					key, value := "payload", "concurrent current browser payload"
					switch mode {
					case "proof_target":
						key, value = auth.SessionResetPrincipalIDKey, f.actor.Principal().ID()
					case "proof_token":
						key, value = auth.SessionResetTokenKey, actorToken.Encoded()
					case "authentication_id", "authentication":
						key, value = auth.SessionPrincipalIDKey, f.actor.Principal().ID()
					case "authentication_stamp":
						key, value = auth.SessionCredentialStampKey, f.actor.SessionStamp()
					}
					changed, err = proof.WithValue(key, value)
					if err == nil && mode == "authentication" {
						changed, err = changed.WithValue(auth.SessionCredentialStampKey, f.actor.SessionStamp())
					}
					if err != nil {
						return err
					}
					if err := writer.runtime.SessionStore().Delete(ctx, proof.ID()); err != nil {
						return err
					}
					inserted, err := writer.runtime.SessionStore().Create(ctx, changed)
					if err != nil || !inserted {
						t.Fatal("competing same-bearer record was not stored", err)
					}
					loginRemoveRecord(t, &expected.account, loginNamespace(t, f.credential), proof.ID())
				}
				checkpoint = userReferenceSnapshot(t, other)
				if mode != "logout" {
					loginPutRecord(t, &expected.account, checkpoint.account, loginNamespace(t, f.credential), changed, mode == "idle_renewed")
				}
				assertUserReferenceState(t, expected, checkpoint, f.actor.Principal())
				entropyAtCheckpoint = f.entropy.count()
				return nil
			}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			result, err := owner.ResetPassword(ctx, proof.ID(), f.target.PrincipalID, selfPassword)
			if !entered || f.owner.hasher.hashes.Load() != 1 || f.owner.hasher.verifies.Load() != 0 || f.owner.boundary.calls != 1 {
				t.Fatal("current browser recheck lost its outside-scope hash and single write", err)
			}
			id := f.entropy.one(t, entropyAtCheckpoint)
			after := userReferenceSnapshot(t, reopen(t))
			if mode == "payload" || mode == "idle_renewed" {
				if err != nil || f.owner.policies.Load() != 2 {
					t.Fatal("reset rejected current unrelated browser state", err)
				}
				expected, envelope := resetFinishExpected(t, f, f.owner, checkpoint, after, changed, id)
				if result.ClearSession != envelope.ClearSession || !reflect.DeepEqual(result.Record.Snapshot(), envelope.Record.Snapshot()) {
					t.Fatal("reset restored stale browser values or lifetime")
				}
				assertUserReferenceStateAt(t, expected, after, f.credential.Principal(), f.clock(), catalogExpectedEvent{"user", f.target.ID, admin.ActionChange, []string{"encoded_password"}})
			} else {
				selectedResetNoResult(t, result, err)
				if err != auth.ErrInvalidResetProof || f.owner.policies.Load() != 1 {
					t.Fatal("changed browser authority lost its direct proof refusal", err)
				}
				assertUserReferenceState(t, checkpoint, after, f.actor.Principal())
				if mode == "replacement" {
					if err := peer.CheckPasswordReset(t.Context(), changed.ID(), f.actor.Principal().ID(), nil); err != nil {
						t.Fatal("losing reset damaged the replacement proof", err)
					}
					if err := peer.CheckPasswordReset(t.Context(), changed.ID(), f.target.PrincipalID, nil); err != auth.ErrInvalidResetProof {
						t.Fatal("replacement proof admitted its old target", err)
					}
					// Compare at the issue second so only the current account/key
					// binding can explain a changed token.
					at := f.clock()
					f.instant.Store(proof.CreatedAt().UnixNano())
					fresh := selectedResetToken(t, owner, f.target.PrincipalID)
					f.instant.Store(at.UnixNano())
					if fresh.Encoded() != token.Encoded() {
						t.Fatal("proof replacement changed the account token")
					}
				}
			}
			assertUserReferenceState(t, after, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
			for _, value := range []any{owner, result, err} {
				selfPasswordPrivate(t, value, selfPassword, token.Encoded(), actorToken.Encoded(), proof.ID().Encoded(), id.Encoded(), f.owner.encoded.Load().(string))
			}
		})
	}
}

func passwordResetBrowserAdmission(t *testing.T, open Open) {
	for _, mode := range []string{"empty_target", "missing_target", "other_target", "malformed_start", "limits_new", "limits_existing"} {
		t.Run(mode, func(t *testing.T) {
			backend, reopen := open(t)
			f := provisionSelfPassword(t, backend)
			config := f.config(t)
			limited := mode == "limits_new" || mode == "limits_existing"
			owner := resetNativeOwner(t, f)
			manager := f.runtime.Sessions()
			if limited {
				config.Sessions.Limits.MaxValues = 1
				if mode == "limits_existing" {
					config.Sessions.Limits.MaxValues = 2
				}
				var err error
				manager, err = sessions.NewManager(f.runtime.SessionStore(), config.Sessions)
				if err != nil {
					t.Fatal(err)
				}
				owner, err = hostproject.NewAccountPasswordResetter(f.runtime.SessionStore(), manager, f.runtime.Authenticator(), accountmodel.PasswordResetConfig[models.User]{Keys: resetNativeConfig(t, f).Keys, Clock: f.clock})
				if err != nil {
					t.Fatal(err)
				}
			}
			token := selectedResetToken(t, owner, f.target.PrincipalID)
			var proof sessions.Record
			if limited {
				if mode == "limits_existing" {
					var err error
					proof, err = manager.Create(t.Context(), map[string]string{"cart": "one current value"})
					if err != nil {
						t.Fatal(err)
					}
				}
			} else {
				proof = selectedResetProof(t, owner, sessions.Record{}, token)
			}
			before := userReferenceSnapshot(t, reopen(t))
			entropy := f.entropy.count()
			f.owner.boundary.calls = 0
			if limited || mode == "malformed_start" {
				raw := token.Encoded()
				if mode == "malformed_start" {
					raw = "invalid"
				}
				result, err := owner.StartPasswordReset(t.Context(), proof.ID(), f.target.PrincipalID, raw)
				if err == nil || !reflect.DeepEqual(result, sessions.Record{}) {
					t.Fatal("invalid entry published a proof", err)
				}
				if mode == "malformed_start" && err != auth.ErrInvalidResetProof || limited && err == auth.ErrInvalidResetProof {
					t.Fatal("configured limits became ordinary proof refusal", err)
				}
			} else {
				id := map[string]string{"empty_target": "", "missing_target": "absent-reset-principal", "other_target": f.actor.Principal().ID()}[mode]
				if err := owner.CheckPasswordReset(t.Context(), proof.ID(), id, nil); err != auth.ErrInvalidResetProof {
					t.Fatal("wrong proof target admitted", err)
				}
				result, err := owner.ResetPassword(t.Context(), proof.ID(), id, selfPassword)
				selectedResetNoResult(t, result, err)
				if err != auth.ErrInvalidResetProof {
					t.Fatal("wrong proof target hashed or lost direct refusal", err)
				}
			}
			writes, entropyDelta := 0, 0
			if mode == "limits_existing" {
				writes, entropyDelta = 1, 1
			}
			if f.owner.hasher.hashes.Load() != 0 || f.owner.hasher.verifies.Load() != 0 || f.owner.policies.Load() != 0 || f.owner.boundary.calls != writes || f.entropy.count() != entropy+entropyDelta {
				t.Fatal("invalid proof/limit repeated work or changed admission order")
			}
			assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
		})
	}
}
