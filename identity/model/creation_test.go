package model_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	hostproject "github.com/progresshans/godj/conformance/identityfixture/project"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/modeldef"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type creationNoHash struct{ calls int }

func (h *creationNoHash) Hash(context.Context, string) (string, error) {
	h.calls++
	return "", errors.New("unexpected hash")
}
func (*creationNoHash) Verify(context.Context, string, string) (bool, error) {
	return false, errors.New("unexpected verify")
}
func (*creationNoHash) ValidateEncoded(string) error { return errors.New("unexpected encoded check") }

func TestAccountCreationCompositionAndInputRejectBeforeIO(t *testing.T) {
	backend := &untouchedCatalogBackend{}
	hasher := &creationNoHash{}
	binding, err := project.BindAccount()
	if err != nil {
		t.Fatal(err)
	}
	store, err := accountmodel.NewCredentialStore(backend, binding, models.GroupDescriptor{}, models.PermissionDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	collections, err := project.BindCollections()
	if err != nil {
		t.Fatal(err)
	}
	config := accountmodel.CreationConfig[models.User]{Fields: []string{"username", "first_name", "email", "active", "groups", "permissions"}, AddPermission: "account.add", ChangePermission: "account.change"}
	build := func(c accountmodel.CreationConfig[models.User]) (*accountmodel.Creator[models.User], error) {
		return accountmodel.NewCreator(store, binding, collections.IdentityUserGroups, collections.IdentityUserPermissions, auth.PrincipalAuthorizer{}, hasher, c)
	}
	for _, fields := range [][]string{nil, {"first_name"}, {"username", "username"}, {"username", "unknown"}, {"username", "principal_id"}, {"username", "encoded_password"}, {"username", "revision"}, {"username", "last_login"}, {"username", "date_joined"}, {"username", "id"}} {
		invalid := config
		invalid.Fields = fields
		if result, err := build(invalid); result != nil || !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeInvalidConfig}) {
			t.Fatal("invalid creation exposure accepted", fields, err)
		}
	}
	for _, maximum := range []int{-1, 4097} {
		invalid := config
		invalid.MaxPasswordBytes = maximum
		if result, err := build(invalid); result != nil || err == nil {
			t.Fatal("invalid hash bound accepted")
		}
	}
	for _, permission := range []auth.Permission{"", "invalid permission", config.AddPermission} {
		invalid := config
		invalid.ChangePermission = permission
		if result, err := build(invalid); result != nil || err == nil {
			t.Fatal("missing or invalid second required grant accepted")
		}
	}
	host, err := hostproject.BindCollections()
	if err != nil {
		t.Fatal(err)
	}
	if result, err := accountmodel.NewCreator(store, binding, host.AccountsUserGroups, collections.IdentityUserPermissions, auth.PrincipalAuthorizer{}, hasher, config); result != nil || err == nil {
		t.Fatal("foreign host collection accepted")
	}
	role := modeldef.AccountRole()
	role.AuthenticateNormalization = ir.AccountUnicode16NFKC
	other, err := accountmodel.Bind(role, models.UserDescriptor{}, models.GoDjRelationSchema())
	if err != nil {
		t.Fatal(err)
	}
	if result, err := accountmodel.NewCreator(store, other, collections.IdentityUserGroups, collections.IdentityUserPermissions, auth.PrincipalAuthorizer{}, hasher, config); result != nil || err == nil {
		t.Fatal("foreign selected policy accepted")
	}
	creator, err := build(config)
	if err != nil {
		t.Fatal(err)
	}
	config.Fields[0] = "encoded_password"
	creator.Fields()[0] = "encoded_password"
	if !slices.Contains(creator.Fields(), "username") || slices.Contains(creator.Fields(), "encoded_password") {
		t.Fatal("field selection aliases caller")
	}
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "creator", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	namespace, err := binding.CredentialDomain([32]byte{1})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := auth.NewCredentialForDomain(namespace, "creator", "encoded", principal)
	if err != nil {
		t.Fatal(err)
	}
	actor := credential.Principal()
	input, err := models.UserObjects.Input(map[string]query.Value{"username": query.String("candidate")})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"", strings.Repeat("p", 1025)} {
		if _, err := creator.Prepare(t.Context(), actor, input, raw); err == nil {
			t.Fatal("unbounded password accepted")
		}
	}
	invalid := []orm.ModelInput[models.User]{{}}
	for name, value := range map[string]query.Value{"principal_id": query.String("forged"), "encoded_password": query.String("secret"), "revision": query.Integer(8)} {
		prepared, err := models.UserObjects.Input(map[string]query.Value{"username": query.String("candidate"), name: value})
		if err != nil {
			t.Fatal(err)
		}
		invalid = append(invalid, prepared)
	}
	bound, err := input.WithTargetKey(query.Integer(0))
	if err != nil {
		t.Fatal(err)
	}
	invalid = append(invalid, bound)
	for _, name := range []string{"groups", "permissions"} {
		overflow, err := input.WithCollections(map[string][]int64{name: make([]int64, 257)})
		if err != nil {
			t.Fatal(err)
		}
		invalid = append(invalid, overflow)
	}
	for _, input := range invalid {
		if _, err := creator.Prepare(t.Context(), actor, input, "password"); err == nil {
			t.Fatal("invalid creation input accepted")
		}
	}
	for _, actor := range []auth.Principal{principal, auth.Anonymous()} {
		if _, err := creator.Prepare(t.Context(), actor, input, "password"); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodePermission}) {
			t.Fatal("unbound actor accepted", err)
		}
	}
	if _, err := creator.Prepare(nil, actor, input, "password"); err == nil {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := creator.Prepare(ctx, actor, input, "password"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation hidden", err)
	}
	if backend.calls != 0 || backend.writes != 0 || hasher.calls != 0 {
		t.Fatal("invalid composition or input performed work")
	}
}
