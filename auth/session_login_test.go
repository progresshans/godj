package auth

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/sessions"
)

func TestAuthenticatedSessionPreservesOnlyCompatibleOwnerData(t *testing.T) {
	for _, mode := range []string{"fresh", "anonymous", "same", "different", "changed_stamp", "partial_principal", "partial_stamp"} {
		t.Run(mode, func(t *testing.T) {
			store, _ := sessions.NewMemoryStore(8)
			now := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
			manager, err := sessions.NewManager(store, sessions.Config{Clock: func() time.Time { return now }, AbsoluteLifetime: time.Hour, IdleTimeout: time.Minute})
			if err != nil {
				t.Fatal(err)
			}
			principal, err := NewPrincipal(PrincipalConfig{ID: "current", Active: true})
			if err != nil {
				t.Fatal(err)
			}
			credential, err := NewCredential("member", "opaque-current-encoding", principal)
			if err != nil {
				t.Fatal(err)
			}
			values := map[string]string{"private_owner_data": "retained only for the same identity"}
			switch mode {
			case "same":
				values[SessionPrincipalIDKey] = principal.ID()
				values[SessionCredentialStampKey] = credential.SessionStamp()
			case "different":
				values[SessionPrincipalIDKey] = "other"
				values[SessionCredentialStampKey] = credential.SessionStamp()
			case "changed_stamp":
				values[SessionPrincipalIDKey] = principal.ID()
				values[SessionCredentialStampKey] = "stale"
			case "partial_principal":
				values[SessionPrincipalIDKey] = principal.ID()
			case "partial_stamp":
				values[SessionCredentialStampKey] = credential.SessionStamp()
			}
			var previous sessions.Record
			if mode != "fresh" {
				previous, err = manager.Create(t.Context(), values)
				if err != nil {
					t.Fatal(err)
				}
			}
			now = now.Add(time.Second)
			record, err := EstablishSession(t.Context(), manager, credential, previous)
			if err != nil {
				t.Fatal(err)
			}
			if record.ID() == previous.ID() {
				t.Fatal("login retained the old bearer identifier")
			}
			expected := mode == "anonymous" || mode == "same"
			_, present := record.Value("private_owner_data")
			if present != expected {
				t.Fatal("login crossed identity data ownership", mode)
			}
			if expected && !record.CreatedAt().Equal(previous.CreatedAt()) {
				t.Fatal("same-owner rotation lost the existing lifetime")
			}
			if !expected && !record.CreatedAt().Equal(now) {
				t.Fatal("fresh login inherited another lifetime")
			}
			id, _ := record.Value(SessionPrincipalIDKey)
			stamp, _ := record.Value(SessionCredentialStampKey)
			if id != principal.ID() || !credential.MatchesSessionStamp(stamp) {
				t.Fatal("login did not bind the verified credential")
			}
			if previous.ID().Valid() {
				if _, found, err := store.Load(t.Context(), previous.ID()); err != nil || found {
					t.Fatal("old session survived login", err)
				}
			}
			request := SessionLogin{Credential: credential, Previous: record, At: now, Admit: func(context.Context, Principal) error { return nil }}
			if err := request.Validate(); err != nil {
				t.Fatal(err)
			}
			result := SessionLoginResult{Credential: credential, Record: record}
			for _, data := range []any{request, result} {
				for _, format := range []string{"%v", "%+v", "%#v", "%q", "%#x", "%f"} {
					formatted := fmt.Sprintf(format, data)
					for _, secret := range []string{"opaque-current-encoding", credential.SessionStamp(), record.ID().Encoded(), "private_owner_data"} {
						if strings.Contains(formatted, secret) {
							t.Fatal("login diagnostics exposed credential or session data")
						}
					}
				}
			}
		})
	}
}

func TestSessionLoginRejectsIncompleteIdentityAndTime(t *testing.T) {
	principal, _ := NewPrincipal(PrincipalConfig{ID: "member", Active: true})
	credential, _ := NewCredential("member", "opaque", principal)
	valid := SessionLogin{Credential: credential, At: time.Now(), Admit: func(context.Context, Principal) error { return nil }}
	for _, mode := range []string{"zero", "no_admission", "invalid_time", "inactive"} {
		t.Run(mode, func(t *testing.T) {
			input := valid
			switch mode {
			case "zero":
				input.Credential = Credential{}
			case "no_admission":
				input.Admit = nil
			case "invalid_time":
				input.At = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
			case "inactive":
				p, _ := NewPrincipal(PrincipalConfig{ID: "member"})
				input.Credential, _ = NewCredential("member", "opaque", p)
			}
			if err := input.Validate(); err == nil {
				t.Fatal("invalid login context accepted")
			}
		})
	}
}
