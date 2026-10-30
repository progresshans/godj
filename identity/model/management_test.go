package model_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	hostmodels "github.com/progresshans/godj/conformance/identityfixture/models"
	hostproject "github.com/progresshans/godj/conformance/identityfixture/project"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/modeldef"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	sessiondb "github.com/progresshans/godj/sessions/database"
)

func unitManagementConfig() accountmodel.AccountManagementConfig[models.User] {
	return accountmodel.AccountManagementConfig[models.User]{
		ReadFields:   []string{"username", "email", "first_name", "active", "groups", "permissions"},
		CreateFields: []string{"username", "email", "groups"}, EditFields: []string{"email", "first_name", "active", "permissions"},
		Permissions:           accountmodel.AccountPermissions{View: "account.view", Add: "account.add", Change: "account.change", Delete: "account.delete", Additional: []auth.Permission{"account.manage"}},
		GroupPermissions:      accountmodel.DeletionPermissions{Delete: "catalog.delete_group", Change: "catalog.change_group"},
		PermissionPermissions: accountmodel.DeletionPermissions{Delete: "catalog.delete_permission", Change: "catalog.change_permission"},
	}
}

func TestAccountManagementRequiresSharedSourceAndPolicyBeforeIO(t *testing.T) {
	backend := &untouchedCatalogBackend{}
	sessions, err := project.NewAccountSessionStore(backend, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	inner, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	hasher := &passwordSetupHasher{PasswordHasher: inner}
	verifier, err := sessions.Authenticator(t.Context(), hasher)
	if err != nil {
		t.Fatal(err)
	}
	other, err := project.NewAccountSessionStore(backend, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	foreignVerifier, err := other.Authenticator(t.Context(), hasher)
	if err != nil {
		t.Fatal(err)
	}
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
	hostCollections, err := hostproject.BindCollections()
	if err != nil {
		t.Fatal(err)
	}
	hostDeletions, err := hostproject.BindRelationDeleters()
	if err != nil {
		t.Fatal(err)
	}
	hostBinding, err := accountmodel.Bind(modeldef.AccountRole(), models.UserDescriptor{}, models.GoDjRelationSchema(), hostmodels.GoDjRelationSchema())
	if err != nil {
		t.Fatal(err)
	}
	before := hasher.calls
	for _, mode := range []string{"nil_sessions", "foreign_verifier", "foreign_source", "foreign_collection", "foreign_deletion", "read_secret", "create_server", "edit_server", "missing_view", "reused_operation", "additional_operation", "missing_related_policy"} {
		t.Run(mode, func(t *testing.T) {
			config, store, expected, selected, groups, deletion := unitManagementConfig(), sessions, binding, verifier, collections.IdentityUserGroups, deletions.IdentityUser
			switch mode {
			case "nil_sessions":
				store = nil
			case "foreign_verifier":
				selected = foreignVerifier
			case "foreign_source":
				expected = hostBinding
			case "foreign_collection":
				groups = hostCollections.AccountsUserGroups
			case "foreign_deletion":
				deletion = hostDeletions.AccountsUser
			case "read_secret":
				config.ReadFields = []string{"username", "encoded_password"}
			case "create_server":
				config.CreateFields = []string{"username", "principal_id"}
			case "edit_server":
				config.EditFields = []string{"revision"}
			case "missing_view":
				config.Permissions.View = ""
			case "reused_operation":
				config.Permissions.Delete = config.Permissions.Change
			case "additional_operation":
				config.Permissions.Additional = []auth.Permission{config.Permissions.Add}
			case "missing_related_policy":
				config.GroupPermissions.Delete = ""
			}
			owner, err := accountmodel.NewAccountManagement(store, expected, selected, models.GroupDescriptor{}, models.PermissionDescriptor{}, groups, collections.IdentityUserPermissions, deletion, auth.PrincipalAuthorizer{}, config)
			if owner != nil || !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeInvalidConfig}) {
				t.Fatal("foreign or incoherent owner accepted", err)
			}
		})
	}
	calls := 0
	config := unitManagementConfig()
	config.PrincipalID = func(context.Context) (string, error) { calls++; return "generated", nil }
	config.Now = func() time.Time { calls++; return time.Now() }
	owner, err := project.NewAccountManagement(sessions, verifier, auth.PrincipalAuthorizer{}, config)
	if err != nil {
		t.Fatal(err)
	}
	config.Permissions.Additional[0], config.EditFields[0] = "forged", "encoded_password"
	description, err := owner.Description()
	if err != nil || description.Identity.ModelName != "user" || description.LoginField != "username" || description.PasswordField != "encoded_password" || !slices.Equal(description.Permissions.Additional, []auth.Permission{"account.manage"}) {
		t.Fatal("description lost role or owned policy", err)
	}
	description.Permissions.Additional[0] = "forged"
	again, err := owner.Description()
	if err != nil || again.Permissions.Additional[0] != "account.manage" || !slices.Equal(owner.Passwords().Permissions(), []auth.Permission{"account.change", "account.manage"}) || !slices.Equal(owner.Deleter().Permissions(), []auth.Permission{"account.delete", "account.manage"}) {
		t.Fatal("component policy aliases caller or differs", err)
	}
	for _, action := range []admin.Action{admin.ActionAdd, admin.ActionChange} {
		definition, err := owner.FormDefinition(action)
		if err != nil {
			t.Fatal(err)
		}
		want := owner.Editor().Fields()
		if action == admin.ActionAdd {
			want = owner.Creator().Fields()
		}
		if !slices.Equal(definition.Fields, want) {
			t.Fatal("operation exposure merged")
		}
		choices, err := owner.ChoiceSources(action)
		if err != nil || len(choices) != 1 || choices[0] == nil {
			t.Fatal("operation assignment missing", err)
		}
		choices[0] = nil
		fresh, err := owner.ChoiceSources(action)
		if err != nil || fresh[0] == nil {
			t.Fatal("choice slice aliases owner")
		}
	}
	if _, err := owner.FormDefinition(admin.ActionDelete); err == nil {
		t.Fatal("delete supplied writable form")
	}
	if _, err := owner.ChoiceSources(admin.ActionDelete); err == nil {
		t.Fatal("delete supplied assignments")
	}
	if backend.calls != 0 || backend.writes != 0 || hasher.calls != before || calls != 0 {
		t.Fatal("pure composition performed work")
	}
}

func TestAccountManagementFormAdmissionRejectsForeignAndProtectedInputBeforeIO(t *testing.T) {
	backend := &untouchedCatalogBackend{}
	sessions, err := project.NewAccountSessionStore(backend, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := sessions.Authenticator(t.Context(), hasher)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := project.NewAccountManagement(sessions, verifier, auth.PrincipalAuthorizer{}, unitManagementConfig())
	if err != nil {
		t.Fatal(err)
	}
	actor, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "actor", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"zero", "foreign_model", "wrong_key", "wrong_revision", "create_existing", "protected_invalid", "nil_context", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			fields := []string{"email", "first_name"}
			if mode == "protected_invalid" {
				fields = append(fields, "encoded_password")
			}
			spec, err := formmodel.NewSpecForFields(models.UserDescriptor{}.Metadata(), fields)
			if err != nil {
				t.Fatal(err)
			}
			row := models.User{ID: 0, Revision: 2}
			(models.UserDescriptor{}).SetPrimaryKey(&row, 0)
			form, err := formmodel.BindInstance(t.Context(), models.UserObjects, spec, forms.NewData(map[string][]string{"email": {"person@example.test"}, "first_name": {"Name"}}), &row, formmodel.PostClean{})
			if err != nil {
				t.Fatal(err)
			}
			bound, ctx, id, revision := form.BoundForm(), t.Context(), int64(0), int64(2)
			switch mode {
			case "zero":
				bound = formmodel.BoundForm{}
			case "foreign_model":
				spec, err := formmodel.NewSpecForFields(models.GroupDescriptor{}.Metadata(), []string{"name"})
				if err != nil {
					t.Fatal(err)
				}
				foreign, err := formmodel.BindInstance(ctx, models.GroupObjects, spec, forms.NewData(map[string][]string{"name": {"Group"}}), nil, formmodel.PostClean{})
				if err != nil {
					t.Fatal(err)
				}
				bound = foreign.BoundForm()
			case "wrong_key":
				id++
			case "wrong_revision":
				revision++
			case "nil_context":
				ctx = nil
			case "canceled":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			if mode == "create_existing" {
				err = owner.CheckCreateForm(ctx, actor, bound)
			} else {
				err = owner.CheckChangeForm(ctx, actor, id, revision, bound)
			}
			if !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeInvalidInput}) {
				t.Fatal("invalid form admission reached storage", err)
			}
		})
	}
	if backend.calls != 0 || backend.writes != 0 {
		t.Fatal("invalid form touched storage")
	}
}
