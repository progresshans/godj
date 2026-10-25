package identityadmin

import (
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/schema/ir"
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
	var created, edited []string
	for _, field := range descriptor.CreateFormFields {
		created = append(created, field.Name())
		if field.Name() == "password1" || field.Name() == "password2" {
			if field.Widget() != forms.PasswordInput {
				t.Fatal("creation secret is public text")
			}
		}
	}
	for _, field := range descriptor.FormFields {
		edited = append(edited, field.Name())
	}
	if !slices.Equal(created, []string{"username", "groups", "password1", "password2", "unusable_password"}) || !slices.Equal(edited, config.EditFields) {
		t.Fatal("read/create/edit policies merged", created, edited)
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

func TestSelectedAccountCreationKeepsPasswordInputsSeparateFromModel(t *testing.T) {
	model := models.UserDescriptor{}.Metadata()
	for _, name := range []string{"password1", "password2", "unusable_password"} {
		field := model.Fields[1].Clone()
		field.Name, field.GoName, field.Column, field.Kind, field.Unique, field.MaxLength = name, "Profile"+name, name, ir.FieldChar, false, 150
		model.Fields = append(model.Fields, field)
	}
	inputs, err := accountCreationForm(model, formmodel.Definition{Fields: []string{"username", "password1", "password2", "unusable_password"}}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if inputs.first == "password1" || inputs.confirm == "password2" || inputs.unusable == "unusable_password" {
		t.Fatal("command shadows real selected profile")
	}
	spec, err := inputs.definition.Spec(model)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, first, second string
		disabled, valid     bool
	}{
		{"usable", "  λ private  ", "  λ private  ", false, true},
		{"required", "", "", false, false}, {"mismatch", "one", "two", false, false},
		{"byte_limit", strings.Repeat("界", 7), strings.Repeat("界", 7), false, false},
		{"unusable_empty", "", "", true, true}, {"unusable_ignores_password", "one", "two", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := map[string][]string{"username": {"candidate"}, "password1": {"public one"}, "password2": {"public two"}, "unusable_password": {"public profile"}, inputs.first: {test.first}, inputs.confirm: {test.second}}
			if test.disabled {
				data[inputs.unusable] = []string{"on"}
			}
			bound, err := spec.Bind(t.Context(), forms.NewData(data), nil)
			if err != nil || bound.Valid() != test.valid {
				t.Fatal("creation password policy differs", err)
			}
			if test.valid {
				if actual, _ := bound.Cleaned().String(inputs.first); actual != test.first {
					t.Fatal("private password bytes changed")
				}
				if actual, _ := bound.Cleaned().String("password1"); actual != "public one" {
					t.Fatal("real profile input overwritten by command")
				}
			}
		})
	}
}
