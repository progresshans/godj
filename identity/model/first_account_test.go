package model_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/modeldef"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestFirstAccountRejectsAmbiguousPrivilegeAndInputBeforeIO(t *testing.T) {
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
	config := accountmodel.FirstAccountConfig[models.User]{CreationPolicy: accountmodel.CreationPolicy[models.User]{Fields: []string{"username", "email"}}, EvidenceSHA256: strings.Repeat("a", 64)}
	build := func(config accountmodel.FirstAccountConfig[models.User]) (*accountmodel.FirstAccount[models.User], error) {
		return accountmodel.NewFirstAccount(store, binding, collections.IdentityUserGroups, collections.IdentityUserPermissions, hasher, config)
	}
	for _, field := range []string{"active", "staff", "superuser", "principal_id", "encoded_password", "revision", "date_joined", "last_login"} {
		invalid := config
		invalid.Fields = []string{"username", field}
		if owner, err := build(invalid); owner != nil || !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeInvalidConfig}) {
			t.Fatal("caller can select privileged/server field", field, err)
		}
	}
	for _, evidence := range []string{"", strings.Repeat("0", 64), strings.Repeat("A", 64), strings.Repeat("a", 63)} {
		invalid := config
		invalid.EvidenceSHA256 = evidence
		if owner, err := build(invalid); owner != nil || err == nil {
			t.Fatal("invalid declaration evidence accepted")
		}
	}
	for _, name := range []string{"active", "staff", "superuser"} {
		role := modeldef.AccountRole()
		flag := ir.AccountBoolean{Constant: new(false)}
		switch name {
		case "active":
			role.Active = flag
		case "staff":
			role.Staff = flag
		case "superuser":
			role.Superuser = flag
		}
		selected, err := accountmodel.Bind(role, models.UserDescriptor{}, models.GoDjRelationSchema())
		if err != nil {
			t.Fatal(err)
		}
		selectedStore, err := accountmodel.NewCredentialStore(backend, selected, models.GroupDescriptor{}, models.PermissionDescriptor{})
		if err != nil {
			t.Fatal(err)
		}
		if owner, err := accountmodel.NewFirstAccount(selectedStore, selected, collections.IdentityUserGroups, collections.IdentityUserPermissions, hasher, config); owner != nil || err == nil {
			t.Fatal("constant false role became an initial superuser", name)
		}
	}
	owner, err := build(config)
	if err != nil {
		t.Fatal(err)
	}
	input, err := models.UserObjects.Input(map[string]query.Value{"username": query.String("first"), "staff": query.Boolean(false)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Prepare(t.Context(), input, "password"); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeInvalidInput}) {
		t.Fatal("input overrode fixed role", err)
	}
	input, err = models.UserObjects.Input(map[string]query.Value{"username": query.String("first")})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"", strings.Repeat("p", 1025)} {
		if _, err := owner.Prepare(t.Context(), input, raw); err == nil {
			t.Fatal("invalid first password accepted")
		}
	}
	if _, err := owner.PrepareOperation(t.Context(), "invalid", input, "password"); err == nil {
		t.Fatal("invalid operation accepted")
	}
	if _, err := owner.Prepare(nil, input, "password"); err == nil {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := owner.Prepare(ctx, input, "password"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
	if _, err := owner.Commit(t.Context(), accountmodel.PreparedFirstAccount[models.User]{}); err == nil {
		t.Fatal("zero preparation committed")
	}
	if backend.calls != 0 || backend.writes != 0 || hasher.calls != 0 {
		t.Fatal("invalid first-account composition reached work")
	}
}
