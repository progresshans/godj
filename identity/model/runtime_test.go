package model_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	hostproject "github.com/progresshans/godj/conformance/identityfixture/project"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/validation"
)

func unitRuntimeConfig() accountmodel.RuntimeConfig[models.User, models.Group, models.Permission] {
	accounts, catalog := unitManagementConfig(), unitCatalogConfig()
	catalog.AccountPermissions.Additional = slices.Clone(accounts.Permissions.Additional)
	return accountmodel.RuntimeConfig[models.User, models.Group, models.Permission]{Accounts: accounts, Catalog: catalog}
}

func TestSelectedRuntimeSharesSessionPasswordAndManagementOwners(t *testing.T) {
	backend := &untouchedCatalogBackend{}
	inner, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	hasher := &passwordSetupHasher{PasswordHasher: inner}
	config := unitRuntimeConfig()
	callbacks := 0
	config.Sessions.Clock = func() time.Time { callbacks++; return time.Now() }
	config.Accounts.PrincipalID = func(context.Context) (string, error) { callbacks++; return "private-principal", nil }
	config.Accounts.Now = func() time.Time { callbacks++; return time.Now() }
	config.Accounts.MaxPasswordBytes = 80
	config.Accounts.ValidatePassword = func(context.Context, string, models.User) (validation.Errors, error) {
		callbacks++
		return validation.Errors{}, nil
	}
	runtime, err := project.NewAccountRuntime(t.Context(), backend, hasher, auth.PrincipalAuthorizer{}, config)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.SessionStore() == nil || runtime.Sessions() == nil || runtime.Sessions().Store() != runtime.SessionStore() ||
		runtime.Authenticator() == nil || runtime.LoginPersistence().Sessions() != runtime.Sessions() ||
		runtime.PasswordChangePersistence().Sessions() != runtime.Sessions() || runtime.Accounts() == nil || runtime.Catalog() == nil {
		t.Fatal("runtime mixed or omitted an owner")
	}
	keys, err := auth.NewPasswordResetKeyRing(bytes.Repeat([]byte{41}, 32))
	if err != nil {
		t.Fatal(err)
	}
	reset, err := runtime.PasswordResetter(accountmodel.RuntimeResetConfig{Keys: keys, Clock: func() time.Time { callbacks++; return time.Now() }})
	if err != nil || reset.Sessions() != runtime.Sessions() || reset.MaxPasswordBytes() != 80 {
		t.Fatal("reset lost the composed password/session policy", err)
	}
	config.Accounts.MaxPasswordBytes = 4
	config.Accounts.EditFields[0] = "encoded_password"
	config.Accounts.Permissions.Additional[0] = "private-forged"
	if runtime.Accounts().Passwords().MaxPasswordBytes() != 80 ||
		!slices.Equal(runtime.Accounts().Passwords().Permissions(), []auth.Permission{"account.change", "account.manage"}) {
		t.Fatal("runtime retained mutable declaration input")
	}
	if callbacks != 0 || backend.calls != 0 || backend.writes != 0 || hasher.calls != 1 {
		t.Fatal("runtime performed work beyond dummy-hash setup", callbacks, backend.calls, backend.writes, hasher.calls)
	}
	diagnosticConfig := config
	diagnosticConfig.Sessions.Random = bytes.NewBufferString("private-entropy")
	for _, value := range []any{runtime, reset, diagnosticConfig, accountmodel.RuntimeResetConfig{Keys: keys}} {
		if strings.Contains(fmt.Sprintf("%#v", value), "private") {
			t.Fatal("runtime diagnostics exposed configuration")
		}
	}
	if document, err := json.Marshal(diagnosticConfig); err != nil || bytes.Contains(document, []byte("private")) {
		t.Fatal("runtime configuration leaked into JSON")
	}
}

func TestSelectedRuntimeRejectsForeignSourceAndConflictingPoliciesBeforeWork(t *testing.T) {
	backend := &untouchedCatalogBackend{}
	inner, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	hasher := &passwordSetupHasher{PasswordHasher: inner}
	binding, err := project.BindAccount()
	if err != nil {
		t.Fatal(err)
	}
	collections, err := project.BindCollections()
	if err != nil {
		t.Fatal(err)
	}
	deletions, err := project.BindRelationDeleters()
	if err != nil {
		t.Fatal(err)
	}
	foreignCollections, err := hostproject.BindCollections()
	if err != nil {
		t.Fatal(err)
	}
	foreignDeletions, err := hostproject.BindRelationDeleters()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"nil_context", "canceled", "missing_account", "foreign_membership", "foreign_deletion", "account_grants", "group_grants", "permission_grants", "session_limits", "catalog_fields"} {
		t.Run(mode, func(t *testing.T) {
			config, selected, group, deletion := unitRuntimeConfig(), binding, collections.IdentityUserGroups, deletions.IdentityUser
			ctx := t.Context()
			switch mode {
			case "nil_context":
				ctx = nil
			case "canceled":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			case "missing_account":
				selected = accountmodel.Binding[models.User]{}
			case "foreign_membership":
				group = foreignCollections.AccountsUserGroups
			case "foreign_deletion":
				deletion = foreignDeletions.AccountsUser
			case "account_grants":
				config.Catalog.AccountPermissions.Additional = nil
			case "group_grants":
				config.Accounts.GroupPermissions.Change = "weaker.change"
			case "permission_grants":
				config.Accounts.PermissionPermissions.Delete = "weaker.delete"
			case "session_limits":
				config.Sessions.Limits.MaxValues = 1
			case "catalog_fields":
				config.Catalog.Group.ReadFields = []string{"missing"}
			}
			runtime, err := accountmodel.NewRuntime(ctx, backend, selected, models.GroupDescriptor{}, models.PermissionDescriptor{}, group,
				collections.IdentityUserPermissions, collections.IdentityGroupPermissions, deletion, deletions.IdentityGroup, deletions.IdentityPermission,
				hasher, auth.PrincipalAuthorizer{}, config)
			if err == nil || runtime != nil {
				t.Fatal("foreign or inconsistent runtime was published")
			}
			if mode == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost", err)
			}
			if backend.calls != 0 || backend.writes != 0 || hasher.calls != 0 {
				t.Fatal("invalid source or shared policy reached work")
			}
		})
	}
}
