package identityadmin

import (
	"slices"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	sessiondb "github.com/progresshans/godj/sessions/database"
)

func TestSelectedAccountRegistrationPreservesExposureAndPolicyBeforeIO(t *testing.T) {
	probe := &catalogRegistrationProbe{}
	sessions, err := project.NewAccountSessionStore(probe, sessiondb.Config{})
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
	installed, err := apps.New(project.InstalledApps())
	if err != nil {
		t.Fatal(err)
	}
	config := accountmodel.AccountManagementConfig[models.User]{
		ReadFields:   []string{"username", "email", "first_name", "active", "permissions"},
		CreateFields: []string{"username", "groups"}, EditFields: []string{"email", "first_name", "active", "permissions"},
		Permissions:           accountmodel.AccountPermissions{View: "account.view", Add: "account.add", Change: "account.change", Delete: "account.delete", Additional: []auth.Permission{"account.manage"}},
		GroupPermissions:      accountmodel.DeletionPermissions{Delete: "catalog.delete_group", Change: "catalog.change_group"},
		PermissionPermissions: accountmodel.DeletionPermissions{Delete: "catalog.delete_permission", Change: "catalog.change_permission"},
	}
	owner, err := project.NewAccountManagement(sessions, verifier, auth.PrincipalAuthorizer{}, config)
	if err != nil {
		t.Fatal(err)
	}
	presentation := AccountRegistration{Slug: "accounts", ListFields: []string{"id", "username", "revision"}}
	builder := admin.NewBuilder(installed)
	if err := RegisterAccount(builder, owner, presentation); err != nil {
		t.Fatal(err)
	}
	registry, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	presentation.ListFields[0] = "forged"
	descriptor, present := registry.Lookup("godj_identity", "user")
	if !present || len(registry.All()) != 1 || descriptor.Slug != "accounts" || !slices.Equal(descriptor.ListFields, []string{"id", "username", "revision"}) || !slices.Equal(descriptor.AdditionalPermissions, []auth.Permission{"account.manage"}) {
		t.Fatal("actual account registration widened or lost owner policy")
	}
	if descriptor.RevisionField != "revision" || !slices.Equal(descriptor.AddPermissions, []auth.Permission{"account.add", "account.manage", "account.change"}) {
		t.Fatal("selected registration lost revision or combined creation authority")
	}
	if len(descriptor.Commands) != 2 || descriptor.Commands[0].Name != "password" || descriptor.Commands[1].Name != "disable-password" {
		t.Fatal("selected registration lost password commands")
	}
	for _, command := range descriptor.Commands {
		if !slices.Equal(command.Permissions, []auth.Permission{"account.change", "account.manage"}) {
			t.Fatal("selected password command lost the owner's additional permission")
		}
	}
	if !slices.Equal(descriptor.ReadOnlyFields, []admin.ReadOnlyFieldDescriptor{{Name: "password_usable", Label: "Password login"}}) {
		t.Fatal("selected password state lost its read-only presentation")
	}
	withLogin := config
	withLogin.ReadFields = append(slices.Clone(config.ReadFields), "last_login")
	loginOwner, err := project.NewAccountManagement(sessions, verifier, auth.PrincipalAuthorizer{}, withLogin)
	if err != nil {
		t.Fatal(err)
	}
	loginBuilder := admin.NewBuilder(installed)
	if err := RegisterAccount(loginBuilder, loginOwner, AccountRegistration{Slug: "accounts"}); err != nil {
		t.Fatal(err)
	}
	loginRegistry, err := loginBuilder.Build()
	if err != nil {
		t.Fatal(err)
	}
	loginDescriptor, present := loginRegistry.Lookup("godj_identity", "user")
	if !present || !slices.Equal(loginDescriptor.ReadOnlyFields, []admin.ReadOnlyFieldDescriptor{{Name: "password_usable", Label: "Password login"}, {Name: "last_login", Label: "Last login"}}) {
		t.Fatal("explicit login-time exposure was lost")
	}
	for _, field := range append(loginDescriptor.FormFields, loginDescriptor.CreateFormFields...) {
		if field.Name() == "last_login" {
			t.Fatal("server login time became writable")
		}
	}
	var created, edited []string
	for _, field := range descriptor.CreateFormFields {
		created = append(created, field.Name())
		if field.Name() == "password1" || field.Name() == "password2" {
			if field.Widget() != forms.PasswordInput {
				t.Fatal("creation secret is public text")
			}
		}
		if field.Name() == "unusable_password" && field.Widget() != forms.Checkbox {
			t.Fatal("selected unusable-password choice lost its explicit control")
		}
	}
	for _, field := range descriptor.FormFields {
		edited = append(edited, field.Name())
	}
	// The allowlist selects fields; ModelForm preserves their Schema IR order.
	if !slices.Equal(created, []string{"username", "groups", "password1", "password2", "unusable_password"}) || !slices.Equal(edited, []string{"first_name", "email", "active", "permissions"}) {
		t.Fatal("read/create/edit policies merged", created, edited)
	}
	descriptor.CreateFormFields[0] = forms.Field{}
	again, present := registry.Lookup("godj_identity", "user")
	if !present || again.CreateFormFields[0].Name() != "username" {
		t.Fatal("returned selected descriptor aliases registered form fields")
	}
	for _, mode := range []string{"nil_owner", "nil_builder", "unreadable_edit", "missing_label", "collection_column", "secret_column", "missing_slug"} {
		t.Run(mode, func(t *testing.T) {
			selected, target, presentation := owner, admin.NewBuilder(installed), AccountRegistration{Slug: "accounts"}
			switch mode {
			case "nil_owner":
				selected = nil
			case "nil_builder":
				target = nil
			case "unreadable_edit", "missing_label":
				copy := config
				copy.ReadFields = []string{"username"}
				if mode == "missing_label" {
					copy.ReadFields = config.EditFields
				}
				var err error
				selected, err = project.NewAccountManagement(sessions, verifier, auth.PrincipalAuthorizer{}, copy)
				if err != nil {
					t.Fatal(err)
				}
			case "collection_column":
				presentation.ListFields = []string{"permissions"}
			case "secret_column":
				presentation.ListFields = []string{"encoded_password"}
			case "missing_slug":
				presentation.Slug = ""
			}
			if err := RegisterAccount(target, selected, presentation); err == nil {
				t.Fatal("invalid account presentation registered")
			}
		})
	}
	if probe.calls != 0 {
		t.Fatal("registration performed I/O")
	}
}
