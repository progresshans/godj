package accountstoretest

import (
	"context"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
)

func passwordResetConcurrent(t *testing.T, open Open) {
	for _, name := range []string{"reset", "shared_proof", "self_change", "administrator", "login", "deactivate", "login_before_reset", "reset_before_login"} {
		t.Run(name, func(t *testing.T) {
			mode, firstWriter := name, -1
			switch name {
			case "login_before_reset":
				mode, firstWriter = "login", 1
			case "reset_before_login":
				mode, firstWriter = "login", 0
			}
			backend, reopen := open(t)
			f := provisionSelfPassword(t, backend)
			base := resetNativeOwner(t, f)
			token := selectedResetToken(t, base, f.target.PrincipalID)
			proof := selectedResetProof(t, base, sessions.Record{}, token)
			var peerPrevious sessions.Record
			switch mode {
			case "reset":
				peerPrevious = selectedResetProof(t, base, sessions.Record{}, token)
			case "shared_proof":
				peerPrevious = proof
			case "self_change":
				peerPrevious = f.targetSessions[0]
			case "login":
				var err error
				peerPrevious, err = f.runtime.Sessions().Create(t.Context(), map[string]string{"anonymous": "pending peer login"})
				if err != nil {
					t.Fatal(err)
				}
			}
			f.instant.Add(int64(time.Minute))
			ready, start := make(chan struct{}, 2), make(chan struct{})
			afterFirst := make(chan struct{})
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			var once sync.Once
			release := func() { once.Do(func() { close(start) }) }
			var workers sync.WaitGroup
			defer func() { cancel(); release(); workers.Wait() }()
			entropy := make([]loginEntropy, 2)
			owners := make([]*userReferenceOwner, 2)
			resetters := make([]*accountmodel.PasswordResetter[models.User], 2)
			for index, connection := range []Backend{backend, reopen(t)} {
				config := f.config(t)
				config.Accounts.Now = f.clock
				config.Sessions.Random = &entropy[index]
				gate := start
				if firstWriter >= 0 && index != firstWriter {
					gate = afterFirst
				}
				owners[index] = newUserReferenceOwner(t, &passwordWriteGate{connection, ready, gate}, f.userReferenceFixture, config)
				var err error
				resetters[index], err = owners[index].runtime.PasswordResetter(resetNativeConfig(t, f))
				if err != nil {
					t.Fatal(err)
				}
			}
			inactive := roleInput(t, map[string]query.Value{"active": query.Boolean(false)}, nil)
			before := userReferenceSnapshot(t, reopen(t))
			type outcome struct {
				index    int
				err      error
				reset    auth.PasswordResetResult
				password auth.PasswordChangeResult
				login    auth.SessionLoginResult
				edit     accountmodel.PasswordEditResult
				change   accountmodel.AccountChange
			}
			peerPassword := userReferencePassword + "-race-peer"
			done := make(chan outcome, 2)
			for index := range 2 {
				workers.Add(1)
				go func(index int) {
					defer workers.Done()
					result := outcome{index: index}
					if index == 0 {
						result.reset, result.err = resetters[0].ResetPassword(ctx, proof.ID(), f.target.PrincipalID, selfPassword)
					} else {
						runtime := owners[1].runtime
						switch mode {
						case "reset", "shared_proof":
							result.reset, result.err = resetters[1].ResetPassword(ctx, peerPrevious.ID(), f.target.PrincipalID, peerPassword)
						case "self_change":
							result.password, result.err = runtime.PasswordChangePersistence().ChangePassword(ctx, peerPrevious.ID(), password, peerPassword)
						case "administrator":
							result.edit, result.err = runtime.Accounts().Passwords().SetPassword(ctx, f.actor.Principal(), f.target.ID, f.target.Revision, peerPassword)
						case "login":
							result.login, result.err = runtime.LoginPersistence().Login(ctx, selectedSessionLogin(f.credential, peerPrevious, f.clock()))
						case "deactivate":
							result.change, result.err = runtime.Accounts().Editor().Update(ctx, f.actor.Principal(), f.target.ID, f.target.Revision, inactive)
						}
					}
					done <- result
				}(index)
			}
			for range 2 {
				select {
				case <-ready:
				case result := <-done:
					t.Fatal("reset peer completed before both independent native fences", mode, result.err)
				case <-ctx.Done():
					t.Fatal("reset peers did not prepare before their independent write fences", ctx.Err())
				}
			}
			release()
			results := make([]outcome, 2)
			for received := range 2 {
				select {
				case result := <-done:
					results[result.index] = result
					// Both calls prepared independently. In the ordered cases the
					// second writer enters only after the first confirmed commit.
					if firstWriter >= 0 && received == 0 {
						if result.index != firstWriter || result.err != nil {
							t.Fatal("scheduled first writer did not commit", name, result.err)
						}
						close(afterFirst)
					}
				case <-ctx.Done():
					t.Fatal("reset peer writes did not finish", ctx.Err())
				}
			}
			workers.Wait()
			result, peer := results[0], results[1]
			if (result.err == nil) == (peer.err == nil) {
				t.Fatal("reset and competing operation did not have exactly one winner", result.err, peer.err)
			}
			resetPolicies, peerPolicies := int64(1), int64(1)
			if result.err == nil {
				resetPolicies = 2
			}
			if peer.err == nil {
				peerPolicies = 2
			}
			if mode == "login" || mode == "deactivate" {
				peerPolicies = 0
			}
			if owners[0].policies.Load() != resetPolicies || owners[1].policies.Load() != peerPolicies {
				t.Fatal("reset peer skipped or repeated its current typed password policy")
			}
			peerHashes, peerVerifies, peerEntropy := int64(1), int64(0), 0
			if mode == "login" || mode == "deactivate" {
				peerHashes = 0
			}
			if mode == "self_change" {
				peerVerifies = 1
			}
			if mode == "reset" || mode == "shared_proof" || mode == "self_change" || mode == "login" {
				peerEntropy = 1
			}
			if owners[0].hasher.hashes.Load() != 1 || owners[0].hasher.verifies.Load() != 0 || owners[1].hasher.hashes.Load() != peerHashes || owners[1].hasher.verifies.Load() != peerVerifies ||
				owners[0].boundary.calls != 1 || owners[1].boundary.calls != 1 || entropy[1].count() != peerEntropy {
				t.Fatal("reset peer repeated hash, verification, entropy or native writes")
			}
			resetID := entropy[0].one(t, 0)
			var peerID sessions.ID
			if peerEntropy == 1 {
				peerID = entropy[1].one(t, 0)
			}
			after := userReferenceSnapshot(t, reopen(t))
			winningPassword, active := selfPassword, true
			if result.err == nil {
				expected, envelope := resetFinishExpected(t, f, owners[0], before, after, proof, resetID)
				if result.reset.ClearSession != envelope.ClearSession || !reflect.DeepEqual(result.reset.Record.Snapshot(), envelope.Record.Snapshot()) {
					t.Fatal("winning reset published a different session")
				}
				assertUserReferenceStateAt(t, expected, after, f.credential.Principal(), f.clock(), catalogExpectedEvent{"user", f.target.ID, admin.ActionChange, []string{"encoded_password"}})
				switch mode {
				case "reset", "shared_proof":
					selectedResetNoResult(t, peer.reset, peer.err)
					if peer.err != auth.ErrInvalidResetProof {
						t.Fatal("losing reset was not a direct stale-proof refusal", peer.err)
					}
				case "self_change":
					passwordNoResult(t, peer.password, peer.err)
					if peer.err != auth.ErrInvalidCredentials {
						t.Fatal("losing self change did not reject its stale credential", peer.err)
					}
				case "login":
					loginNoResult(t, peer.login, peer.err)
					if peer.err != auth.ErrInvalidCredentials {
						t.Fatal("losing login did not reject its stale credential", peer.err)
					}
				case "administrator":
					passwordEditNoResult(t, peer.edit, peer.err)
					requireProfileCode(t, peer.err, accountmodel.CodeConflict)
				case "deactivate":
					if !reflect.DeepEqual(peer.change, accountmodel.AccountChange{}) {
						t.Fatal("losing deactivation published a partial change")
					}
					requireProfileCode(t, peer.err, accountmodel.CodeConflict)
				}
			} else {
				selectedResetNoResult(t, result.reset, result.err)
				if result.err != auth.ErrInvalidResetProof {
					t.Fatal("losing reset did not reject the current authority", result.err)
				}
				winningPassword = peerPassword
				switch mode {
				case "reset", "shared_proof":
					expected, envelope := resetFinishExpected(t, f, owners[1], before, after, peerPrevious, peerID)
					if peer.reset.ClearSession != envelope.ClearSession || !reflect.DeepEqual(peer.reset.Record.Snapshot(), envelope.Record.Snapshot()) {
						t.Fatal("winning peer reset lost its exact native result")
					}
					assertUserReferenceStateAt(t, expected, after, f.credential.Principal(), f.clock(), catalogExpectedEvent{"user", f.target.ID, admin.ActionChange, []string{"encoded_password"}})
				case "self_change":
					expected, envelope := selfPasswordExpected(t, f, owners[1], before, after, peerPrevious, peerID, f.clock())
					assertSelfPasswordResult(t, peer.password, envelope)
					assertUserReferenceStateAt(t, expected, after, f.credential.Principal(), f.clock(), catalogExpectedEvent{"user", f.target.ID, admin.ActionChange, []string{"encoded_password"}})
				case "login":
					winningPassword = password
					record := loginExpectedRecord(t, f.credential, peerPrevious, "rotate", peerID, f.clock())
					if !reflect.DeepEqual(peer.login.Record.Snapshot(), record.Snapshot()) {
						t.Fatal("winning login did not rotate the anonymous session with its existing data and absolute expiry")
					}
					if !peer.login.Credential.MatchesSessionStamp(f.credential.SessionStamp()) || !reflect.DeepEqual(peer.login.Credential.Principal(), f.credential.Principal()) {
						t.Fatal("winning login returned a different credential")
					}
					expected := loginExpectedState(t, before, after, f.credential, f.target, peerPrevious, record, f.clock())
					assertUserReferenceState(t, expected, after, f.actor.Principal())
				case "administrator", "deactivate":
					row, expected := f.target, before
					row.Revision++
					fields := []string{"encoded_password"}
					if mode == "administrator" {
						row.EncodedPassword = owners[1].encoded.Load().(string)
						if peer.edit != (accountmodel.PasswordEditResult{ID: row.ID, Revision: row.Revision, RevokedSessions: len(f.targetSessions)}) {
							t.Fatal("winning administrator password change lost its exact result")
						}
					} else {
						winningPassword, active, row.Active, fields = password, false, false, []string{"active"}
						if peer.change.ID != row.ID || peer.change.Revision != row.Revision || peer.change.RevokedSessions != len(f.targetSessions) || !slices.Equal(peer.change.ChangedFields, fields) {
							t.Fatal("winning deactivation lost its exact result")
						}
					}
					userReferenceReplace(&expected.account, row)
					for _, record := range f.targetSessions {
						loginRemoveRecord(t, &expected.account, loginNamespace(t, f.credential), record.ID())
					}
					assertUserReferenceStateAt(t, expected, after, f.actor.Principal(), f.clock(), catalogExpectedEvent{"user", f.target.ID, admin.ActionChange, fields})
				}
			}
			reopened := newUserReferenceOwner(t, reopen(t), f.userReferenceFixture, f.config(t))
			credential, err := reopened.runtime.Authenticator().Authenticate(t.Context(), f.target.Username, winningPassword)
			if active && (err != nil || credential.Principal().ID() != f.target.PrincipalID) || !active && err != auth.ErrInvalidCredentials {
				t.Fatal("reconnected authority differs from the winning operation", err)
			}
			assertUserReferenceState(t, after, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
		})
	}
}
