package model_test

import (
	"context"
	"errors"
	"testing"

	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/sessions"
	sessiondb "github.com/progresshans/godj/sessions/database"
)

func TestSelectedAccountSessionCompositionRejectsInvalidOwnershipBeforeIO(t *testing.T) {
	binding, err := project.BindAccount()
	if err != nil {
		t.Fatal(err)
	}
	backend := &untouchedProfileBackend{}
	credentials, err := accountmodel.NewCredentialStore(backend, binding, models.GroupDescriptor{}, models.PermissionDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	if store, err := accountmodel.NewSessionStore[models.User](nil, sessiondb.Config{}); store != nil || err == nil {
		t.Fatal("missing account reader accepted")
	}
	readOnly, err := accountmodel.NewCredentialStore(&untouchedAccountBackend{}, binding, models.GroupDescriptor{}, models.PermissionDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	if store, err := accountmodel.NewSessionStore(readOnly, sessiondb.Config{}); store != nil || err == nil {
		t.Fatal("unfenced session owner accepted")
	}
	store, err := accountmodel.NewSessionStore(credentials, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := sessions.NewManager(store, sessions.Config{})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := accountmodel.NewSessionStore(credentials, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	other, err := sessions.NewManager(foreign, sessions.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if owner, err := store.LoginPersistence(other); owner != nil || err == nil {
		t.Fatal("different store instance admitted as exact manager")
	}
	if owner, err := store.LoginPersistence(manager); err != nil || owner.Sessions() != manager {
		t.Fatal("exact manager binding rejected", err)
	}
	if _, _, err := store.Load(nil, sessions.ID{}); err == nil {
		t.Fatal("nil context accepted")
	}
	if _, _, err := store.Load(t.Context(), sessions.ID{}); err == nil {
		t.Fatal("zero bearer accepted")
	}
	if _, err := store.Create(t.Context(), sessions.Record{}); err == nil {
		t.Fatal("zero record accepted")
	}
	if _, err := store.RevokeIn(t.Context(), nil, "invalid\x00principal", sessions.ID{}); err == nil {
		t.Fatal("invalid principal accepted")
	}
	if _, err := store.RevokeIn(t.Context(), (*uninitializedSession)(nil), "principal", sessions.ID{}); err == nil {
		t.Fatal("typed nil scope accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := store.Delete(ctx, sessions.ID{}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
	if backend.calls != 0 || backend.writes != 0 {
		t.Fatal("invalid session composition performed I/O")
	}
}

type uninitializedSession struct{ db.Session }
