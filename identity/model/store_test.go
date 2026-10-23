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
	"github.com/progresshans/godj/query"
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

type authorityInputScope struct{ reads int }

func (scope *authorityInputScope) Query(context.Context, query.Plan) (db.Rows, error) {
	scope.reads++
	return nil, errors.New("unexpected authorization read")
}
func (scope *authorityInputScope) ValidateSession(ctx context.Context) error { return ctx.Err() }

func TestAccountAuthorityRejectsInvalidScopeAndRequirementsBeforeRead(t *testing.T) {
	binding, err := accountmodel.Bind(modeldef.AccountRole(), models.UserDescriptor{}, models.GoDjRelationSchema())
	if err != nil {
		t.Fatal(err)
	}
	backend := &untouchedAccountBackend{}
	store, err := accountmodel.NewCredentialStore(backend, binding, models.GroupDescriptor{}, models.PermissionDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	scope := &authorityInputScope{}
	for _, required := range [][]auth.Permission{{""}, {"BAD permission"}, {"account.read", "account.read"}, make([]auth.Permission, auth.MaximumPermissions+1)} {
		current, allowed, err := store.AuthorizeIn(t.Context(), scope, auth.Anonymous(), auth.PrincipalAuthorizer{}, required[0], required[1:]...)
		if err == nil || allowed || current.ID() != "" {
			t.Fatal("invalid required permissions admitted")
		}
	}
	for _, reader := range []db.Queryer{nil, (*authorityInputScope)(nil), failedAuthorityReader{}} {
		current, allowed, err := store.AuthorizeIn(t.Context(), reader, auth.Anonymous(), auth.PrincipalAuthorizer{}, "account.read")
		if err == nil || allowed || current.ID() != "" {
			t.Fatal("unowned reader admitted")
		}
	}
	for _, overlay := range []auth.Authorizer{nil, (*auth.PrincipalAuthorizer)(nil)} {
		current, allowed, err := store.AuthorizeIn(t.Context(), scope, auth.Anonymous(), overlay, "account.read")
		if err == nil || allowed || current.ID() != "" {
			t.Fatal("nil overlay admitted")
		}
	}
	if current, allowed, err := store.AuthorizeIn(nil, scope, auth.Anonymous(), auth.PrincipalAuthorizer{}, "account.read"); err == nil || allowed || current.ID() != "" {
		t.Fatal("nil context admitted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if current, allowed, err := store.AuthorizeIn(ctx, scope, auth.Anonymous(), auth.PrincipalAuthorizer{}, "account.read"); !errors.Is(err, context.Canceled) || allowed || current.ID() != "" {
		t.Fatal("canceled context admitted", err)
	}
	if current, allowed, err := store.AuthorizeIn(t.Context(), scope, auth.Anonymous(), auth.PrincipalAuthorizer{}, "account.read"); err != nil || allowed || current.ID() != "" {
		t.Fatal("anonymous actor was not definitely denied", err)
	}
	if scope.reads != 0 || backend.calls != 0 {
		t.Fatal("invalid or anonymous authority made database reads")
	}
}

type failedAuthorityReader struct{}

func (failedAuthorityReader) Query(context.Context, query.Plan) (db.Rows, error) {
	return nil, errors.New("unowned reader")
}
