package accountstoretest

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/sessions"
)

func sessionRevokedPrevious(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	now := joined
	store, manager, _ := accountSessions(t, backend, &now)
	_, _, loginOwner := accountSessions(t, reopen(t), &now)
	editor := roleEditor(t, store)
	for _, deactivated := range []bool{true, false} {
		name := "current_credential"
		if deactivated {
			name = "deactivated"
		}
		t.Run(name, func(t *testing.T) {
			target := roleTarget(t, backend, f, "revoked-"+name)
			credential, present, err := f.store.CredentialByID(t.Context(), target.PrincipalID)
			if err != nil || !present {
				t.Fatal(err)
			}
			previous, err := manager.Create(t.Context(), map[string]string{auth.SessionPrincipalIDKey: target.PrincipalID})
			if err != nil {
				t.Fatal(err)
			}
			if deactivated {
				input := roleInput(t, map[string]query.Value{"active": query.Boolean(false)}, nil)
				change, err := editor.Update(t.Context(), actor, target.ID, 1, input)
				if err != nil || change.RevokedSessions != 1 {
					t.Fatal("deactivation did not revoke previous session", err)
				}
			} else if err := store.Delete(t.Context(), previous.ID()); err != nil {
				t.Fatal(err)
			}
			before := profileRow(t, reopen(t), target.ID)
			admitted := false
			login := selectedSessionLogin(credential, previous, now)
			login.Admit = func(context.Context, auth.Principal) error { admitted = true; return nil }
			result, err := loginOwner.Login(t.Context(), login)
			if deactivated {
				if err != auth.ErrInvalidCredentials || admitted {
					t.Fatal("revoked session hid current credential rejection", err)
				}
			} else if err == auth.ErrInvalidCredentials || !admitted || !errors.Is(err, &sessions.Error{Code: sessions.CodeNotFound}) {
				t.Fatal("missing session became credential rejection", err)
			}
			if result.Record.ID().Valid() || result.Credential.SessionStamp() != "" || accountSessionCount(t, reopen(t)) != 0 {
				t.Fatal("rejected login published a new session")
			}
			if after := profileRow(t, reopen(t), target.ID); !reflect.DeepEqual(before, after) {
				t.Fatal("rejected login changed last-login or credential")
			}
			accountSessionRecord(t, store, previous, false)
		})
	}
}
