package accountstoretest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/sessions"
)

func passwordResetConcurrent(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	for _, mode := range []string{"reset", "self_change", "administrator", "login", "deactivate"} {
		t.Run(mode, func(t *testing.T) {
			target := roleTarget(t, backend, f, "reset-race-"+mode)
			credential := passwordCredential(t, f, target)
			now := joined
			base := passwordComposition(t, backend, &now, f.hasher, accountmodel.PasswordChangeConfig[models.User]{})
			old := passwordSession(t, base.manager, credential, "old-browser")
			baseOwner := selectedResetOwner(t, base, selectedResetConfig(t, &now))
			token := selectedResetToken(t, baseOwner, target.PrincipalID)
			proof := selectedResetProof(t, baseOwner, sessions.Record{}, token)
			var peerProof sessions.Record
			var err error
			if mode == "reset" {
				peerProof = selectedResetProof(t, baseOwner, sessions.Record{}, token)
			} else if mode == "login" {
				peerProof, err = base.manager.Create(t.Context(), map[string]string{"anonymous": "pending-login"})
				if err != nil {
					t.Fatal(err)
				}
			}
			beforeCount := accountSessionCount(t, backend)
			ready, start := make(chan struct{}, 2), make(chan struct{})
			hashers := []*passwordChangeHasher{{PasswordHasher: f.hasher}, {PasswordHasher: f.hasher}}
			configured := passwordComposition(t, &passwordWriteGate{backend, ready, start}, &now, hashers[0], accountmodel.PasswordChangeConfig[models.User]{})
			peer := passwordComposition(t, &passwordWriteGate{reopen(t), ready, start}, &now, hashers[1], accountmodel.PasswordChangeConfig[models.User]{})
			owner := selectedResetOwner(t, configured, selectedResetConfig(t, &now))
			peerOwner := selectedResetOwner(t, peer, selectedResetConfig(t, &now))
			administrator := passwordEditOwner(t, peer, passwordEditConfig())
			roles := roleEditor(t, peer.store)
			inactive := roleInput(t, map[string]query.Value{"active": query.Boolean(false)}, nil)
			login, err := peer.store.LoginPersistence(peer.manager)
			if err != nil {
				t.Fatal(err)
			}
			for _, hasher := range hashers {
				hasher.hashes.Store(0)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			type outcome struct {
				err    error
				record sessions.Record
			}
			done, peerDone := make(chan outcome, 1), make(chan outcome, 1)
			go func() {
				result, err := owner.ResetPassword(ctx, proof.ID(), target.PrincipalID, password+"-reset")
				done <- outcome{err, result.Record}
			}()
			go func() {
				var result outcome
				switch mode {
				case "reset":
					value, err := peerOwner.ResetPassword(ctx, peerProof.ID(), target.PrincipalID, password+"-peer")
					result = outcome{err, value.Record}
				case "self_change":
					value, err := peer.owner.ChangePassword(ctx, old.ID(), password, password+"-peer")
					result = outcome{err, value.Record}
				case "administrator":
					_, result.err = administrator.SetPassword(ctx, actor, target.ID, 1, password+"-peer")
				case "deactivate":
					_, result.err = roles.Update(ctx, actor, target.ID, 1, inactive)
				case "login":
					value, err := login.Login(ctx, selectedSessionLogin(credential, peerProof, now))
					result = outcome{err, value.Record}
				}
				peerDone <- result
			}()
			for range 2 {
				select {
				case <-ready:
				case <-ctx.Done():
					t.Fatal("reset and peer did not reach independent native write fences")
				}
			}
			close(start)
			result, other := <-done, <-peerDone
			if (result.err == nil) == (other.err == nil) {
				t.Fatal("reset race did not have exactly one valid winner", result.err, other.err)
			}
			wantPeerHashes := int64(1)
			if mode == "login" || mode == "deactivate" {
				wantPeerHashes = 0
			}
			if hashers[0].hashes.Load() != 1 || hashers[1].hashes.Load() != wantPeerHashes {
				t.Fatal("reset race repeated password work")
			}
			stored := profileRow(t, reopen(t), target.ID)
			expectedRevision, expectedAudit, expectedCount := int64(2), 1, beforeCount-1
			wantPassword := password + "-reset"
			if result.err != nil {
				if result.err != auth.ErrInvalidResetProof || result.record.ID().Valid() {
					t.Fatal("stale reset published a result or lost refusal", result.err)
				}
				accountSessionRecord(t, base.store, proof, true)
				wantPassword = password + "-peer"
				if mode == "self_change" {
					expectedCount++
				} else if mode == "login" {
					expectedRevision, expectedAudit, expectedCount = 1, 0, beforeCount
					wantPassword = password
					if stored.LastLogin == nil || !stored.LastLogin.Equal(now) {
						t.Fatal("winning login did not bind full current LastLogin")
					}
				} else if mode == "deactivate" {
					wantPassword = password
					if stored.Active {
						t.Fatal("winning deactivation did not persist")
					}
				}
				if other.record.ID().Valid() {
					accountSessionRecord(t, base.store, other.record, true)
				}
			} else {
				if !result.record.ID().Valid() {
					t.Fatal("reset winner omitted rotated anonymous proof")
				}
				accountSessionRecord(t, base.store, proof, false)
				accountSessionRecord(t, base.store, result.record, true)
				switch mode {
				case "reset":
					if other.err != auth.ErrInvalidResetProof {
						t.Fatal("concurrent reset was not invalidated", other.err)
					}
				case "administrator", "deactivate":
					requireProfileCode(t, other.err, accountmodel.CodeConflict)
				case "self_change", "login":
					if !errors.Is(other.err, auth.ErrInvalidCredentials) {
						t.Fatal("old credential peer survived reset", other.err)
					}
				}
				if other.record.ID().Valid() {
					t.Fatal("losing peer published session")
				}
				if peerProof.ID().Valid() {
					accountSessionRecord(t, base.store, peerProof, true)
				}
			}
			if stored.Revision != expectedRevision || len(profileHistory(t, backend, actor, target.ID)) != expectedAudit || accountSessionCount(t, backend) != expectedCount {
				t.Fatal("reset race left partial or repeated effects", stored.Revision, expectedRevision, accountSessionCount(t, backend), expectedCount)
			}
			if ok, err := f.hasher.Verify(t.Context(), wantPassword, stored.EncodedPassword); err != nil || !ok {
				t.Fatal("reset race password differs from durable winner", err)
			}
			accountSessionRecord(t, base.store, old, mode == "login" && other.err == nil)
		})
	}
}

func passwordResetNonpositiveKeys(t *testing.T, backend Backend, storedKey StoredKeyFixture) {
	f := provision(t, backend, ir.AccountExact)
	now := joined
	configured := passwordComposition(t, backend, &now, f.hasher, accountmodel.PasswordChangeConfig[models.User]{})
	owner := selectedResetOwner(t, configured, selectedResetConfig(t, &now))
	for _, key := range []int64{0, -17} {
		t.Run(fmt.Sprintf("key_%d", key), func(t *testing.T) {
			row := roleTarget(t, backend, f, fmt.Sprintf("reset-key-%d", key))
			storedKey(t, backend, "user", row.ID, key)
			(models.UserDescriptor{}).SetPrimaryKey(&row, key)
			old := passwordSession(t, configured.manager, passwordCredential(t, f, row), "signed-key")
			proof := selectedResetProof(t, owner, sessions.Record{}, selectedResetToken(t, owner, row.PrincipalID))
			result, err := owner.ResetPassword(t.Context(), proof.ID(), row.PrincipalID, password+"-new")
			if err != nil || !result.Record.ID().Valid() || result.ClearSession {
				t.Fatal("present nonpositive reset key was lost", err)
			}
			stored := profileRow(t, backend, key)
			entries := profileHistory(t, backend, passwordCredential(t, f, stored).Principal(), key)
			if stored.Revision != 2 || stored.EncodedPassword == row.EncodedPassword || len(entries) != 1 || entries[0].ObjectID() != key {
				t.Fatal("nonpositive reset row/audit mismatch")
			}
			accountSessionRecord(t, configured.store, old, false)
			accountSessionRecord(t, configured.store, proof, false)
			accountSessionRecord(t, configured.store, result.Record, true)
		})
	}
}
