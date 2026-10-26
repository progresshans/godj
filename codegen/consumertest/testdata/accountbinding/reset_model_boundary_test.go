package consumer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"testing"
	"time"

	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings/database"
)

// This is a negative ownership test over explicitly seeded tables. It does not
// implement or assert completion of populated-model mapping/retirement.
func assertResetModelBoundary[M, N any](t *testing.T, backend database.Backend, before accountmodel.Binding[M], after accountmodel.Binding[N], original *accountmodel.SessionStore[M], replacement *accountmodel.SessionStore[N], originalRow M, replacementRow N, hasher auth.PasswordHasher) {
	t.Helper()
	check := func(err error) { t.Helper(); resetCheck(t, err) }
	beforeObjects, err := before.Objects()
	check(err)
	afterObjects, err := after.Objects()
	check(err)
	beforeRole, err := before.Role()
	check(err)
	afterRole, err := after.Role()
	check(err)
	beforeKey, a, err := beforeObjects.PrimaryKey(originalRow)
	check(err)
	afterKey, b, err := afterObjects.PrimaryKey(replacementRow)
	check(err)
	beforeID, _ := beforeKey.Integer()
	afterID, _ := afterKey.Integer()
	beforeValues, err := beforeObjects.ModelValues(originalRow)
	check(err)
	afterValues, err := afterObjects.ModelValues(replacementRow)
	check(err)
	if !a || !b || beforeID != afterID || !beforeValues[beforeRole.PrincipalID].Equal(afterValues[afterRole.PrincipalID]) || !beforeValues[beforeRole.Password].Equal(afterValues[afterRole.Password]) {
		t.Fatal("cross-model fixture does not reuse the exact key, principal and hash")
	}
	principal, _ := beforeValues[beforeRole.PrincipalID].String()
	clock := func() time.Time { return time.Date(2026, 10, 25, 12, 0, 0, 123000, time.UTC) }
	keys, err := auth.NewPasswordResetKeyRing(bytes.Repeat([]byte{66}, 32))
	check(err)
	manager, err := sessions.NewManager(original, sessions.Config{Clock: clock})
	check(err)
	verifier, err := original.Authenticator(t.Context(), hasher)
	check(err)
	owner, err := accountmodel.NewPasswordResetter(original, before, manager, verifier, accountmodel.PasswordResetConfig[M]{Keys: keys, Clock: clock})
	check(err)
	token, err := owner.IssueToken(t.Context(), principal)
	check(err)
	proof, err := owner.StartPasswordReset(t.Context(), sessions.ID{}, principal, token.Encoded())
	check(err)
	beforeFingerprint, err := before.RoleFingerprint()
	check(err)
	current, err := domain.Read(t.Context(), backend, beforeFingerprint)
	check(err)
	transition := func(fingerprint string) {
		t.Helper()
		evidence := sha256.Sum256([]byte("selected reset negative model boundary"))
		change, err := domain.Prepare(t.Context(), fingerprint, hex.EncodeToString(evidence[:]))
		check(err)
		check(backend.CoordinatedAtomic(t.Context(), func(scope db.Session) error {
			var err error
			current, err = domain.TransitionIn(t.Context(), scope, current, change, clock())
			return err
		}))
	}
	afterFingerprint, err := after.RoleFingerprint()
	check(err)
	transition(afterFingerprint)
	nextManager, err := sessions.NewManager(replacement, sessions.Config{Clock: clock})
	check(err)
	nextVerifier, err := replacement.Authenticator(t.Context(), hasher)
	check(err)
	next, err := accountmodel.NewPasswordResetter(replacement, after, nextManager, nextVerifier, accountmodel.PasswordResetConfig[N]{Keys: keys, Clock: clock})
	check(err)
	if record, err := next.StartPasswordReset(t.Context(), sessions.ID{}, principal, token.Encoded()); err != auth.ErrInvalidResetProof || record.ID().Valid() {
		t.Fatal("different account model inherited an old recipient token", err)
	}
	if err := next.CheckPasswordReset(t.Context(), proof.ID(), principal, nil); err != auth.ErrInvalidResetProof {
		t.Fatal("different account model inherited an old proof namespace", err)
	}
	// Even explicitly copying the raw proof record into the new namespace must
	// not bypass credential-domain token binding.
	if created, err := replacement.Create(t.Context(), proof); err != nil || !created {
		t.Fatal("copied negative proof fixture", err)
	}
	if result, err := next.ResetPassword(t.Context(), proof.ID(), principal, "must-never-become-password"); err != auth.ErrInvalidResetProof || !reflect.DeepEqual(result, auth.PasswordResetResult{}) {
		t.Fatal("copied proof crossed an actual model boundary", err)
	}
	fresh, err := next.IssueToken(t.Context(), principal)
	check(err)
	if fresh.Encoded() == token.Encoded() {
		t.Fatal("different model produced identical reset signature")
	}
	currentProof, err := next.StartPasswordReset(t.Context(), proof.ID(), principal, fresh.Encoded())
	check(err)
	result, err := next.ResetPassword(t.Context(), currentProof.ID(), principal, "replacement-only-password")
	check(err)
	if !result.Record.ID().Valid() {
		t.Fatal("replacement model could not reset independently")
	}
	transition(beforeFingerprint)
	if _, err := owner.IssueToken(t.Context(), principal); err != nil {
		t.Fatal("current original model cannot issue a fresh token", err)
	}
	if created, err := original.Create(t.Context(), proof); err != nil || !created {
		t.Fatal("new generation negative proof fixture", err)
	}
	if result, err := owner.ResetPassword(t.Context(), proof.ID(), principal, "must-not-cross-generation"); err != auth.ErrInvalidResetProof || !reflect.DeepEqual(result, auth.PasswordResetResult{}) {
		t.Fatal("original model inherited its earlier generation token", err)
	}
	currentCredential, err := verifier.Resolve(t.Context(), principal)
	check(err)
	login, _ := beforeValues[beforeRole.Login].String()
	verified, err := verifier.Authenticate(t.Context(), login, accountPasswordText)
	check(err)
	if !verified.MatchesSessionStamp(currentCredential.SessionStamp()) {
		t.Fatal("resetting the replacement changed original account credentials")
	}
}
