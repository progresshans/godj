package accountstoretest

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/sessions"
	sessiondb "github.com/progresshans/godj/sessions/database"
	"github.com/progresshans/godj/validation"
)

func selectedResetConfig(t *testing.T, now *time.Time) accountmodel.PasswordResetConfig[models.User] {
	t.Helper()
	keys, err := auth.NewPasswordResetKeyRing(bytes.Repeat([]byte{66}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return accountmodel.PasswordResetConfig[models.User]{Keys: keys, Clock: func() time.Time { return *now }}
}
func selectedResetOwner(t *testing.T, configured passwordFixture, config accountmodel.PasswordResetConfig[models.User]) *accountmodel.PasswordResetter[models.User] {
	t.Helper()
	owner, err := project.NewAccountPasswordResetter(configured.store, configured.manager, configured.authenticator, config)
	if err != nil || owner.Sessions() != configured.manager {
		t.Fatal("selected reset composition", err)
	}
	return owner
}
func selectedResetToken(t *testing.T, owner *accountmodel.PasswordResetter[models.User], principalID string) auth.PasswordResetToken {
	t.Helper()
	token, err := owner.IssueToken(t.Context(), principalID)
	if err != nil || token.PrincipalID() != principalID || token.Encoded() == "" {
		t.Fatal("selected reset issuance", err)
	}
	return token
}
func selectedResetProof(t *testing.T, owner *accountmodel.PasswordResetter[models.User], previous sessions.Record, token auth.PasswordResetToken) sessions.Record {
	t.Helper()
	proof, err := owner.StartPasswordReset(t.Context(), previous.ID(), token.PrincipalID(), token.Encoded())
	if err != nil || !proof.ID().Valid() || proof.ID() == previous.ID() {
		t.Fatal("selected reset proof entry", err)
	}
	return proof
}
func selectedResetNoResult(t *testing.T, result auth.PasswordResetResult, err error) {
	t.Helper()
	if err == nil || !reflect.DeepEqual(result, auth.PasswordResetResult{}) {
		t.Fatal("failed reset published a cookie outcome", err)
	}
}

func passwordResetLifecycle(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	for _, mode := range []string{"missing", "anonymous", "self", "other", "expired", "same_password"} {
		t.Run(mode, func(t *testing.T) {
			now := joined
			target := roleTarget(t, backend, f, "reset-"+mode)
			lastLogin := joined.Add(-time.Hour).Add(123 * time.Microsecond)
			if _, err := models.UserObjects.Patch(t.Context(), backend, target, models.UserPatch{}.WithFirstName("preserved").WithEmail("member@example.test").WithLastLogin(lastLogin)); err != nil {
				t.Fatal(err)
			}
			beforeRow := profileRow(t, backend, target.ID)
			tracked := &passwordScopeOwner{Backend: backend}
			hasher := &passwordChangeHasher{PasswordHasher: f.hasher, scopes: &tracked.scopes}
			configured := passwordComposition(t, tracked, &now, hasher, accountmodel.PasswordChangeConfig[models.User]{})
			config := selectedResetConfig(t, &now)
			var policyScopes []int64
			config.ValidatePassword = func(_ context.Context, _ string, row models.User) (validation.Errors, error) {
				if row.ID != target.ID || row.EncodedPassword != "" || row.FirstName != "preserved" {
					t.Fatal("reset policy did not receive actual private typed profile")
				}
				policyScopes = append(policyScopes, tracked.scopes.Load())
				return validation.Errors{}, nil
			}
			owner := selectedResetOwner(t, configured, config)
			credential := passwordCredential(t, f, target)
			siblings := []sessions.Record{passwordSession(t, configured.manager, credential, "target-first"), passwordSession(t, configured.manager, credential, "target-second")}
			control := passwordSession(t, configured.manager, profileActor(t, f), "other-control")
			anonymous, err := configured.manager.Create(t.Context(), map[string]string{"anonymous": "control"})
			if err != nil {
				t.Fatal(err)
			}
			database, err := sessiondb.New(backend, sessiondb.Config{})
			if err != nil {
				t.Fatal(err)
			}
			history, err := database.Store(strings.Repeat("b", 64))
			if err != nil {
				t.Fatal(err)
			}
			if created, err := history.Create(t.Context(), siblings[0]); err != nil || !created {
				t.Fatal("historical reset fixture", err)
			}
			var previous sessions.Record
			switch mode {
			case "anonymous", "same_password":
				previous, err = configured.manager.Create(t.Context(), map[string]string{"cart": "preserved", "csrf": "independent"})
			case "self":
				previous = passwordSession(t, configured.manager, credential, "self-browser")
			case "other":
				previous = passwordSession(t, configured.manager, profileActor(t, f), "other-browser")
			case "expired":
				now = joined.Add(-2 * time.Hour)
				previous, err = configured.manager.Create(t.Context(), map[string]string{"expired": "must-not-inherit"})
				now = joined
			}
			if err != nil {
				t.Fatal(err)
			}
			hasher.hashes.Store(0)
			hasher.verifies.Store(0)
			tracked.writes.Store(0)
			unchanged := passwordState(t, backend)
			token := selectedResetToken(t, owner, target.PrincipalID)
			if token.Username() != target.Username || token.Email() != beforeRow.Email {
				t.Fatal("token delivery differs from actual selected row")
			}
			passwordUnchanged(t, reopen(t), unchanged)
			now = now.Add(time.Minute)
			proof := selectedResetProof(t, owner, previous, token)
			if tracked.writes.Load() != 1 || hasher.hashes.Load() != 0 || hasher.verifies.Load() != 0 || len(policyScopes) != 0 {
				t.Fatal("reset entry hashed, validated a password or repeated write")
			}
			if targetID, ok := proof.Value(auth.SessionResetPrincipalIDKey); !ok || targetID != target.PrincipalID {
				t.Fatal("proof lost its target")
			}
			if encoded, ok := proof.Value(auth.SessionResetTokenKey); !ok || encoded != token.Encoded() {
				t.Fatal("proof lost its recipient token")
			}
			if mode == "missing" || mode == "expired" {
				if len(proof.Values()) != 2 {
					t.Fatal("new proof inherited an authentication or expired payload")
				}
			} else {
				wanted := previous.Values()
				wanted[auth.SessionResetPrincipalIDKey] = target.PrincipalID
				wanted[auth.SessionResetTokenKey] = token.Encoded()
				if !reflect.DeepEqual(proof.Values(), wanted) || !proof.CreatedAt().Equal(previous.CreatedAt()) || !proof.AbsoluteExpiresAt().Equal(previous.AbsoluteExpiresAt()) {
					t.Fatal("reset entry lost current payload or lifetime")
				}
				accountSessionRecord(t, configured.store, previous, false)
			}
			beforeCheck := passwordState(t, backend)
			tracked.writes.Store(0)
			if err := owner.CheckPasswordReset(t.Context(), proof.ID(), target.PrincipalID, nil); err != nil {
				t.Fatal(err)
			}
			raw := "  selected reset λ password  "
			if mode == "same_password" {
				raw = password
			}
			if err := owner.CheckPasswordReset(t.Context(), proof.ID(), target.PrincipalID, &raw); err != nil {
				t.Fatal(err)
			}
			if tracked.writes.Load() != 0 || hasher.hashes.Load() != 0 || !reflect.DeepEqual(policyScopes, []int64{0}) {
				t.Fatal("reset check touched session, hashed or retained a DB scope", policyScopes)
			}
			passwordUnchanged(t, reopen(t), beforeCheck)
			policyScopes = nil
			tracked.writes.Store(0)
			now = now.Add(time.Minute)
			result, err := owner.ResetPassword(t.Context(), proof.ID(), target.PrincipalID, raw)
			if err != nil || tracked.writes.Load() != 1 || hasher.hashes.Load() != 1 || hasher.verifies.Load() != 0 || !reflect.DeepEqual(policyScopes, []int64{0, 1}) {
				t.Fatal("reset did not couple one hash/fence and current typed policy", err, policyScopes)
			}
			accountSessionRecord(t, configured.store, proof, false)
			if mode == "self" {
				if !result.ClearSession || !reflect.DeepEqual(result.Record, sessions.Record{}) {
					t.Fatal("self reset retained or reauthenticated its browser")
				}
			} else {
				if result.ClearSession || !result.Record.ID().Valid() || result.Record.ID() == proof.ID() {
					t.Fatal("anonymous/other reset did not rotate")
				}
				expected := proof.WithoutValue(auth.SessionResetPrincipalIDKey).WithoutValue(auth.SessionResetTokenKey)
				if !reflect.DeepEqual(result.Record.Values(), expected.Values()) || !result.Record.CreatedAt().Equal(proof.CreatedAt()) || !result.Record.AbsoluteExpiresAt().Equal(proof.AbsoluteExpiresAt()) {
					t.Fatal("reset lost unrelated browser state")
				}
				accountSessionRecord(t, configured.store, result.Record, true)
			}
			for _, record := range siblings {
				accountSessionRecord(t, configured.store, record, false)
			}
			accountSessionRecord(t, configured.store, control, true)
			accountSessionRecord(t, configured.store, anonymous, true)
			if got, present, err := history.Load(t.Context(), siblings[0].ID()); err != nil || !present || !reflect.DeepEqual(got.Snapshot(), siblings[0].Snapshot()) {
				t.Fatal("reset revoked another namespace", err)
			}
			after := profileRow(t, reopen(t), target.ID)
			preserved := after
			preserved.EncodedPassword = beforeRow.EncodedPassword
			preserved.Revision = beforeRow.Revision
			if after.Revision != beforeRow.Revision+1 || after.EncodedPassword == beforeRow.EncodedPassword || !reflect.DeepEqual(preserved, beforeRow) {
				t.Fatal("reset overwrote selected profile, LastLogin or roles")
			}
			if valid, err := f.hasher.Verify(t.Context(), raw, after.EncodedPassword); err != nil || !valid {
				t.Fatal("reset changed raw password bytes", err)
			}
			entries := profileHistory(t, reopen(t), credential.Principal(), target.ID)
			if len(entries) != 1 || entries[0].ActorID() != target.PrincipalID || !entries[0].At().Equal(now) || !reflect.DeepEqual(entries[0].ChangedFields(), []string{"encoded_password"}) {
				t.Fatal("reset self audit is missing or exposes nonselected fields")
			}
			afterState := passwordState(t, backend)
			if record, err := owner.StartPasswordReset(t.Context(), sessions.ID{}, target.PrincipalID, token.Encoded()); err != auth.ErrInvalidResetProof || record.ID().Valid() {
				t.Fatal("completed token replay accepted", err)
			}
			if err := owner.CheckPasswordReset(t.Context(), result.Record.ID(), target.PrincipalID, nil); err != auth.ErrInvalidResetProof {
				t.Fatal("completed session retained reset proof", err)
			}
			passwordUnchanged(t, reopen(t), afterState)
			if text := fmt.Sprintf("%#v %#v", owner, result); strings.Contains(text, raw) || strings.Contains(text, token.Encoded()) || strings.Contains(text, after.EncodedPassword) {
				t.Fatal("reset diagnostics exposed secrets")
			}
		})
	}
}
