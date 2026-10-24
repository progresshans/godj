package model_test

import (
	"context"
	"errors"
	"math"
	"slices"
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
	sessiondb "github.com/progresshans/godj/sessions/database"
	"github.com/progresshans/godj/validation"
)

func TestAccountRoleCompositionAndInputsRejectBeforeIO(t *testing.T) {
	backend := &untouchedCatalogBackend{}
	binding, err := project.BindAccount()
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := accountmodel.NewCredentialStore(backend, binding, models.GroupDescriptor{}, models.PermissionDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := accountmodel.NewSessionStore(credentials, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	collections, err := project.BindCollections()
	if err != nil {
		t.Fatal(err)
	}
	config := accountmodel.RoleConfig[models.User]{Fields: []string{"active", "staff", "superuser", "groups", "permissions"}, ChangePermission: "account.change"}
	build := func(c accountmodel.RoleConfig[models.User]) (*accountmodel.RoleEditor[models.User], error) {
		return accountmodel.NewRoleEditor(sessions, binding, collections.IdentityUserGroups, collections.IdentityUserPermissions, auth.PrincipalAuthorizer{}, c)
	}
	for _, fields := range [][]string{nil, {"staff", "staff"}, {"unknown"}, {"id"}, {"principal_id"}, {"encoded_password"}, {"revision"}, {"last_login"}, {"date_joined"}, {"first_name"}, {"username"}} {
		invalid := config
		invalid.Fields = fields
		if editor, err := build(invalid); editor != nil || !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeInvalidConfig}) {
			t.Fatal("invalid role exposure", fields, err)
		}
	}
	for _, policy := range []accountmodel.RoleConfig[models.User]{{Fields: config.Fields}, {Fields: config.Fields, ChangePermission: "invalid permission"}, {Fields: config.Fields, ChangePermission: config.ChangePermission, AdditionalPermissions: []auth.Permission{config.ChangePermission}}} {
		if editor, err := build(policy); editor != nil || err == nil {
			t.Fatal("invalid grant policy")
		}
	}
	host, err := hostproject.BindCollections()
	if err != nil {
		t.Fatal(err)
	}
	if editor, err := accountmodel.NewRoleEditor(sessions, binding, host.AccountsUserGroups, collections.IdentityUserPermissions, auth.PrincipalAuthorizer{}, config); editor != nil || err == nil {
		t.Fatal("foreign group source admitted")
	}
	if editor, err := accountmodel.NewRoleEditor(sessions, binding, collections.IdentityUserGroups, host.AccountsUserPermissions, auth.PrincipalAuthorizer{}, config); editor != nil || err == nil {
		t.Fatal("foreign permission source admitted")
	}
	role := modeldef.AccountRole()
	role.AuthenticateNormalization = ir.AccountUnicode16NFKC
	foreign, err := accountmodel.Bind(role, models.UserDescriptor{}, models.GoDjRelationSchema())
	if err != nil {
		t.Fatal(err)
	}
	if editor, err := accountmodel.NewRoleEditor(sessions, foreign, collections.IdentityUserGroups, collections.IdentityUserPermissions, auth.PrincipalAuthorizer{}, config); editor != nil || err == nil {
		t.Fatal("same model with another role policy admitted")
	}
	for _, overlay := range []auth.Authorizer{nil, (*auth.PrincipalAuthorizer)(nil)} {
		if editor, err := accountmodel.NewRoleEditor(sessions, binding, collections.IdentityUserGroups, collections.IdentityUserPermissions, overlay, config); editor != nil || err == nil {
			t.Fatal("nil overlay admitted")
		}
	}
	narrow, err := accountmodel.NewCredentialStore(&untouchedProfileBackend{}, binding, models.GroupDescriptor{}, models.PermissionDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	narrowSessions, err := accountmodel.NewSessionStore(narrow, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if editor, err := accountmodel.NewRoleEditor(narrowSessions, binding, collections.IdentityUserGroups, collections.IdentityUserPermissions, auth.PrincipalAuthorizer{}, config); editor != nil || err == nil {
		t.Fatal("ordinary coordinated writer admitted")
	}
	fixed := false
	role = modeldef.AccountRole()
	role.Staff = ir.AccountBoolean{Constant: &fixed}
	constant, err := accountmodel.Bind(role, models.UserDescriptor{}, models.GoDjRelationSchema())
	if err != nil {
		t.Fatal(err)
	}
	constantCredentials, err := accountmodel.NewCredentialStore(backend, constant, models.GroupDescriptor{}, models.PermissionDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	constantSessions, err := accountmodel.NewSessionStore(constantCredentials, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if editor, err := accountmodel.NewRoleEditor(constantSessions, constant, collections.IdentityUserGroups, collections.IdentityUserPermissions, auth.PrincipalAuthorizer{}, config); editor != nil || err == nil {
		t.Fatal("constant role became editable")
	}
	editor, err := build(config)
	if err != nil {
		t.Fatal(err)
	}
	config.Fields[0] = "encoded_password"
	editor.Fields()[0] = "encoded_password"
	if !slices.Contains(editor.Fields(), "active") || slices.Contains(editor.Fields(), "encoded_password") {
		t.Fatal("selection aliases caller")
	}
	actor, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "actor", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	input, err := models.UserObjects.Input(nil)
	if err != nil {
		t.Fatal(err)
	}
	invalid := []orm.ModelInput[models.User]{{}}
	for name, value := range map[string]query.Value{"first_name": query.String("private"), "principal_id": query.String("foreign"), "revision": query.Integer(7), "encoded_password": query.String("private-hash")} {
		value, err := models.UserObjects.Input(map[string]query.Value{name: value})
		if err != nil {
			t.Fatal(err)
		}
		invalid = append(invalid, value)
	}
	for _, key := range []query.Value{query.Null(), query.Integer(1)} {
		bound, err := input.WithTargetKey(key)
		if err != nil {
			t.Fatal(err)
		}
		invalid = append(invalid, bound)
	}
	for _, value := range invalid {
		if result, err := editor.Update(t.Context(), actor, 0, 1, value); result.Revision != 0 || !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeInvalidInput}) {
			t.Fatal("invalid role input reached I/O", err)
		}
	}
	for _, name := range []string{"groups", "permissions"} {
		overflow, err := input.WithCollections(map[string][]int64{name: make([]int64, 257)})
		if err != nil {
			t.Fatal(err)
		}
		if result, err := editor.Update(t.Context(), actor, 0, 1, overflow); result.Revision != 0 {
			t.Fatal("overflow published result")
		} else if _, rejected := validation.Rejected(err); !rejected {
			t.Fatal("raw duplicate count escaped bound", err)
		}
	}
	for _, revision := range []int64{0, -1, math.MaxInt64} {
		if _, err := editor.Update(t.Context(), actor, 0, revision, input); err == nil {
			t.Fatal("invalid revision admitted")
		}
	}
	if _, err := editor.Update(nil, actor, 0, 1, input); err == nil {
		t.Fatal("nil context admitted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := editor.Update(ctx, actor, 0, 1, input); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
	if _, err := editor.Update(t.Context(), auth.Anonymous(), 0, 1, input); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodePermission}) {
		t.Fatal("anonymous actor admitted", err)
	}
	if backend.calls != 0 || backend.writes != 0 {
		t.Fatal("invalid role composition or input performed I/O")
	}
}
