package model_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/progresshans/godj/auth"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/sessions"
	sessiondb "github.com/progresshans/godj/sessions/database"
)

type passwordSetupHasher struct {
	auth.PasswordHasher
	calls int
}

func (hasher *passwordSetupHasher) Hash(ctx context.Context, raw string) (string, error) {
	hasher.calls++
	return hasher.PasswordHasher.Hash(ctx, raw)
}

func TestPasswordChangeRequiresExactSessionAndCredentialOwnersBeforeIO(t *testing.T) {
	backend := &untouchedProfileBackend{}
	store, err := project.NewAccountSessionStore(backend, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := sessions.NewManager(store, sessions.Config{})
	if err != nil {
		t.Fatal(err)
	}
	inner, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	hasher := &passwordSetupHasher{PasswordHasher: inner}
	verifier, err := store.Authenticator(t.Context(), hasher)
	if err != nil {
		t.Fatal(err)
	}
	other, err := project.NewAccountSessionStore(backend, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	otherManager, err := sessions.NewManager(other, sessions.Config{})
	if err != nil {
		t.Fatal(err)
	}
	otherVerifier, err := other.Authenticator(t.Context(), hasher)
	if err != nil {
		t.Fatal(err)
	}
	before := hasher.calls
	for _, test := range []struct {
		name     string
		manager  *sessions.Manager
		verifier *accountmodel.Authenticator[models.User]
		maximum  int
	}{
		{"nil_manager", nil, verifier, 0},
		{"nil_verifier", manager, nil, 0},
		{"foreign_manager", otherManager, verifier, 0},
		{"foreign_verifier", manager, otherVerifier, 0},
		{"negative_limit", manager, verifier, -1},
		{"oversized_limit", manager, verifier, 4097},
	} {
		t.Run(test.name, func(t *testing.T) {
			owner, err := store.PasswordChangePersistence(test.manager, test.verifier, accountmodel.PasswordChangeConfig[models.User]{MaxPasswordBytes: test.maximum})
			if owner != nil || !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeInvalidConfig}) {
				t.Fatal("foreign/invalid password composition accepted", err)
			}
		})
	}
	owner, err := store.PasswordChangePersistence(manager, verifier, accountmodel.PasswordChangeConfig[models.User]{})
	if err != nil || owner.Sessions() != manager {
		t.Fatal("exact password composition rejected", err)
	}
	if text := fmt.Sprintf("%#v", owner); text != "identity.model.PasswordChangePersistence{redacted}" {
		t.Fatal("password owner exposes implementation state")
	}
	if _, err := owner.ChangePassword(t.Context(), sessions.ID{}, "old", "new"); err != auth.ErrInvalidCredentials {
		t.Fatal("invalid bearer reached password work", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := owner.CheckPasswordChange(ctx, sessions.ID{}, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("pre-entry cancellation lost", err)
	}
	if _, err := owner.ChangePassword(nil, sessions.ID{}, "old", "new"); err == nil {
		t.Fatal("nil context admitted")
	}
	if hasher.calls != before || backend.calls != 0 || backend.writes != 0 {
		t.Fatal("composition or invalid invocation performed password/DB work")
	}
}
