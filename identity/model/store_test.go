package model_test

import (
	"context"
	"errors"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/modeldef"
	"github.com/progresshans/godj/identity/models"
)

type untouchedAccountBackend struct{ calls int }

func (b *untouchedAccountBackend) ReadSnapshot(context.Context, func(db.Queryer) error) error {
	b.calls++
	return errors.New("unexpected account I/O")
}
func TestCredentialStoreRejectsUnownedConfigurationBeforeIO(t *testing.T) {
	binding, err := accountmodel.Bind(modeldef.AccountRole(), models.UserDescriptor{}, models.GoDjRelationSchema())
	if err != nil {
		t.Fatal(err)
	}
	backend := &untouchedAccountBackend{}
	for _, candidate := range []db.SnapshotReader{nil, (*untouchedAccountBackend)(nil)} {
		if store, err := accountmodel.NewCredentialStore(candidate, binding, models.GroupDescriptor{}, models.PermissionDescriptor{}); !errors.Is(err, &auth.Error{Code: auth.CodeInvalidConfig}) || store != nil {
			t.Fatal("nil backend accepted", err)
		}
	}
	if store, err := accountmodel.NewCredentialStore(backend, accountmodel.Binding[models.User]{}, models.GroupDescriptor{}, models.PermissionDescriptor{}); err == nil || store != nil {
		t.Fatal("zero binding accepted")
	}
	if store, err := accountmodel.NewCredentialStore(backend, binding, models.PermissionDescriptor{}, models.GroupDescriptor{}); err == nil || store != nil {
		t.Fatal("foreign catalog descriptors accepted")
	}
	if store, err := accountmodel.NewCredentialStore(backend, binding, &models.GroupDescriptor{}, models.PermissionDescriptor{}); err == nil || store != nil {
		t.Fatal("mutable descriptor accepted")
	}
	store, err := accountmodel.NewCredentialStore(backend, binding, models.GroupDescriptor{}, models.PermissionDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	for _, login := range []string{"", " padded", "\xff", "x\x00y"} {
		if got, found, err := store.CredentialByUsername(t.Context(), login); err == nil || found || got.SessionStamp() != "" {
			t.Fatal("invalid lookup admitted")
		}
	}
	if got, found, err := store.CredentialByID(t.Context(), ""); err == nil || found || got.SessionStamp() != "" {
		t.Fatal("invalid principal lookup admitted")
	}
	for _, candidate := range []*accountmodel.CredentialStore[models.User]{nil, {}} {
		if got, found, err := candidate.CredentialByUsername(t.Context(), "alice"); err == nil || found || got.SessionStamp() != "" {
			t.Fatal("zero store admitted")
		}
		if _, err := accountmodel.NewAuthenticator(t.Context(), candidate, nil); err == nil {
			t.Fatal("zero store created authenticator")
		}
	}
	if got, found, err := store.CredentialByUsername(nil, "alice"); err == nil || found || got.SessionStamp() != "" {
		t.Fatal("nil context admitted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got, found, err := store.CredentialByUsername(ctx, "alice"); !errors.Is(err, context.Canceled) || found || got.SessionStamp() != "" {
		t.Fatal("pre-cancelled lookup admitted", err)
	}
	for _, candidate := range []*accountmodel.Authenticator[models.User]{nil, {}} {
		if got, err := candidate.Authenticate(t.Context(), "alice", "password"); err == nil || got.SessionStamp() != "" {
			t.Fatal("zero authenticator admitted")
		}
		if got, err := candidate.ConfirmPassword(t.Context(), "principal-1", "password"); err == nil || got.SessionStamp() != "" {
			t.Fatal("zero confirmer admitted")
		}
		if got, err := candidate.Resolve(t.Context(), "principal-1"); err == nil || got.SessionStamp() != "" {
			t.Fatal("zero resolver admitted")
		}
	}
	if backend.calls != 0 {
		t.Fatal("configuration or input validation performed I/O")
	}
}
