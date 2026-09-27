package identityadmin

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/validation"
)

type configOnlyBackend struct {
	Backend
	marker string
}

func registeredIdentityForTest(t *testing.T) admin.Registry {
	t.Helper()
	configured, err := settings.New(settings.Definition{ProjectName: "identity", InstalledApps: []apps.Config{{Name: "github.com/progresshans/godj/identity/models", Label: "godj_identity"}}})
	if err != nil {
		t.Fatal(err)
	}
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10000})
	if err != nil {
		t.Fatal(err)
	}
	policies, err := project.BindRelationDeleters()
	if err != nil {
		t.Fatal(err)
	}
	builder := admin.NewBuilder(configured.Apps())
	if err := Register(builder, NewConfig(configOnlyBackend{}, hasher, auth.PrincipalAuthorizer{}, DeletionPolicies{Users: policies.IdentityUser, Groups: policies.IdentityGroup, Permissions: policies.IdentityPermission})); err != nil {
		t.Fatal(err)
	}
	registry, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func TestIdentityRegistrationIsDetachedSecretFreeAndDoesNotReadStorage(t *testing.T) {
	configured, err := settings.New(settings.Definition{ProjectName: "identity", InstalledApps: []apps.Config{{Name: "github.com/progresshans/godj/identity/models", Label: "godj_identity"}}})
	if err != nil {
		t.Fatal(err)
	}
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10000})
	if err != nil {
		t.Fatal(err)
	}
	deletions, err := project.BindRelationDeleters()
	if err != nil {
		t.Fatal(err)
	}
	config := NewConfig(configOnlyBackend{marker: "private-config-marker"}, hasher, auth.PrincipalAuthorizer{}, DeletionPolicies{Users: deletions.IdentityUser, Groups: deletions.IdentityGroup, Permissions: deletions.IdentityPermission})
	for _, invalid := range []Config{{}, config.WithPrincipalIDs(nil), config.WithPasswordValidators(nil), NewConfig(nil, hasher, auth.PrincipalAuthorizer{}, config.state.deletions), NewConfig(config.state.backend, hasher, auth.PrincipalAuthorizer{}, DeletionPolicies{})} {
		if err := Register(admin.NewBuilder(configured.Apps()), invalid); err == nil {
			t.Fatal("invalid config registered")
		}
	}
	policies := []identity.PasswordValidator{identity.PasswordValidatorFunc(func(string, identity.Profile) validation.Errors { return validation.Errors{} })}
	custom := config.WithPasswordValidators(policies...).WithPrincipalIDs(func(context.Context) (string, error) { return "private-principal-source", nil })
	policies[0] = nil
	if len(config.state.validators) != 0 || len(custom.state.validators) != 1 || custom.state.validators[0] == nil {
		t.Fatal("config options mutated or aliased")
	}
	builder := admin.NewBuilder(configured.Apps())
	if err := Register(builder, custom); err != nil {
		t.Fatal(err)
	}
	registry, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.All()) != 3 {
		t.Fatal("identity registration incomplete")
	}
	user, found := registry.Lookup("godj_identity", "user")
	if !found || user.RevisionField != "revision" || !reflect.DeepEqual(user.AddPermissions, []auth.Permission{identity.AddUser, identity.ChangeUser}) || len(user.Commands) != 1 || user.Commands[0].Name != "password" {
		t.Fatal("user command/authority/revision contract lost", found, user.AddPermissions)
	}
	names := func(fields []forms.Field) []string {
		var values []string
		for _, field := range fields {
			values = append(values, field.Name())
		}
		return values
	}
	if !reflect.DeepEqual(names(user.FormFields), userFields) || !reflect.DeepEqual(names(user.CreateFormFields), []string{"username", "password1", "password2"}) {
		t.Fatal("private fields exposed or create/edit policies conflated")
	}
	if user.FormFields[0].MaxLength() != 256 || user.CreateFormFields[0].MaxLength() != 150 {
		t.Fatal("creation input limit replaced storage-width editing")
	}
	for _, field := range user.CreateFormFields[1:] {
		if field.Widget() != forms.PasswordInput {
			t.Fatal("password became public text")
		}
	}
	user.CreateFormFields[0] = forms.Field{}
	again, _ := registry.Lookup("godj_identity", "user")
	if again.CreateFormFields[0].Name() != "username" {
		t.Fatal("descriptor aliases registered fields")
	}
	for _, value := range []any{config, &config, custom, &custom} {
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != `"identityadmin.Config{redacted}"` {
			t.Fatal("config JSON exposes internals", err)
		}
		for _, format := range []string{"%v", "%+v", "%#v", "%d", "%x", "%p", "%w"} {
			if strings.Contains(fmt.Sprintf(format, value), "private-") {
				t.Fatal("config diagnostic leaked", format)
			}
		}
	}
}
