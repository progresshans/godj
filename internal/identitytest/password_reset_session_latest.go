package identitytest

import (
	"context"
	"reflect"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/sessions"
)

// Replacing a record with the same ID models a host-side payload update between
// the read and the final fence. The stored payload, proof and authentication
// binding must remain authoritative even when the bearer ID itself is unchanged.
func RunPasswordResetSessionLatest(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	for _, mode := range []string{"payload", "proof", "authentication"} {
		t.Run(mode, func(t *testing.T) {
			backend, other := open(t)
			f := newManagementFixture(t, backend, 4)
			now := loginInstant
			_, p := resetSessionBinding(t, f, backend, &now, sessions.Config{})
			runtime, second := resetSessionBinding(t, f, other, &now, sessions.Config{})
			proof, _ := startResetProof(t, p, sessions.Record{}, f.user.PrincipalID)
			changed := proof
			var err error
			switch mode {
			case "payload":
				changed, err = proof.WithValue("payload", "new concurrent private payload")
			case "proof":
				token := resetToken(t, second.Resetter(), f.root.PrincipalID)
				changed, err = proof.WithValue(auth.SessionResetTokenKey, token.Encoded())
			case "authentication":
				credential, e := runtime.Authenticator().Resolve(t.Context(), f.root.PrincipalID)
				if e != nil {
					t.Fatal(e)
				}
				changed, err = proof.WithValue(auth.SessionPrincipalIDKey, f.root.PrincipalID)
				if err == nil {
					changed, err = changed.WithValue(auth.SessionCredentialStampKey, credential.SessionStamp())
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			f.hasher.hook = func(ctx context.Context) error {
				if err := second.Sessions().Store().Delete(ctx, proof.ID()); err != nil {
					return err
				}
				created, err := second.Sessions().Store().Create(ctx, changed)
				if err == nil && !created {
					t.Fatal("same-ID mutation did not replace fixture record")
				}
				return err
			}
			before := f.stored(t)
			result, err := p.ResetPassword(t.Context(), proof.ID(), f.user.PrincipalID, selfPassword)
			f.hasher.hook = nil
			if f.hasher.calls.Load() != 1 {
				t.Fatal("latest-state reset repeated password work")
			}
			if mode == "payload" {
				if err != nil {
					t.Fatal(err)
				}
				if value, _ := result.Record.Value("payload"); value != "new concurrent private payload" {
					t.Fatal("reset overwrote current session payload")
				}
				assertResetCommit(t, f, before, selfPassword, 2)
			} else {
				if err != auth.ErrInvalidResetProof || result.Record.ID().Valid() || result.ClearSession {
					t.Fatal("reset admitted a replaced proof or authentication binding", mode, err)
				}
				stored, found, e := second.Sessions().Store().Load(t.Context(), proof.ID())
				if e != nil || !found || !reflect.DeepEqual(stored, changed) || !reflect.DeepEqual(before, f.stored(t)) {
					t.Fatal("stale reset damaged latest session or account", e)
				}
				_, audit := snapshotIdentitySystemRows(t, backend)
				if len(audit) != 0 {
					t.Fatal("refused latest-state check wrote audit")
				}
			}
		})
	}
}
