package identityadmin

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/identity/internal/forminput"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	sessiondb "github.com/progresshans/godj/sessions/database"
	"github.com/progresshans/godj/validation"
)

//go:embed testdata/inputs.json
var referenceInputs []byte

//go:embed testdata/django61.json
var djangoReference []byte

type namedInput struct{ Name, Value string }
type formObservation struct {
	Name     string
	Valid    bool
	Username *string
	Codes    map[string][]string
}
type inputCorpus struct {
	Usernames, Emails []namedInput
	Passwords         []struct{ Name, Password1, Password2 string }
}
type formReference struct {
	Django, Python, Unicode string
	InputSHA256             string `json:"input_sha256"`
	Usernames, Passwords    []formObservation
	Emails                  []struct {
		Name  string
		Valid bool
	}
}

func selectedIdentityForReference(t *testing.T) admin.Registry {
	t.Helper()
	probe := &catalogRegistrationProbe{}
	store, err := project.NewAccountSessionStore(probe, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := store.Authenticator(t.Context(), hasher)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := project.NewAccountManagement(store, verifier, auth.PrincipalAuthorizer{}, accountmodel.AccountManagementConfig[models.User]{
		ReadFields: []string{"username", "email"}, CreateFields: []string{"username"}, EditFields: []string{"username", "email"},
		Permissions:           accountmodel.AccountPermissions{View: "account.view", Add: "account.add", Change: "account.change", Delete: "account.delete"},
		GroupPermissions:      accountmodel.DeletionPermissions{Change: "catalog.change_group", Delete: "catalog.delete_group"},
		PermissionPermissions: accountmodel.DeletionPermissions{Change: "catalog.change_permission", Delete: "catalog.delete_permission"},
	})
	if err != nil {
		t.Fatal(err)
	}
	installed, err := apps.New(project.InstalledApps())
	if err != nil {
		t.Fatal(err)
	}
	builder := admin.NewBuilder(installed)
	if err := RegisterAccount(builder, owner, AccountRegistration{Slug: "accounts", CreationLoginInput: accountmodel.UnicodeUsernameInput(150)}); err != nil {
		t.Fatal(err)
	}
	registry, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	if probe.calls != 0 {
		t.Fatal("selected reference registration performed storage I/O")
	}
	return registry
}

func TestIdentityFormDjangoReferenceSubset(t *testing.T) {
	var inputs inputCorpus
	var expected formReference
	if err := json.Unmarshal(referenceInputs, &inputs); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(djangoReference, &expected); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(referenceInputs)
	if expected.InputSHA256 != hex.EncodeToString(hash[:]) || expected.Django != "6.1" || expected.Python != "3.14.3" || expected.Unicode != "16.0.0" {
		t.Fatal("reference source/input binding mismatch")
	}
	if len(inputs.Usernames) != len(expected.Usernames) || len(inputs.Passwords) != len(expected.Passwords) || len(inputs.Emails) != len(expected.Emails) {
		t.Fatal("reference omitted inputs")
	}
	registry := selectedIdentityForReference(t)
	descriptor, _ := registry.Lookup("godj_identity", "user")
	spec, err := forms.NewSpec(descriptor.CreateFormFields, forminput.CreationPasswords())
	if err != nil {
		t.Fatal(err)
	}
	observe := func(data map[string][]string) formObservation {
		form, err := spec.Bind(t.Context(), forms.NewData(data), nil)
		if err != nil {
			t.Fatal(err)
		}
		// This corpus has no stored users or password policy. The complete
		// read-backed post-clean phase is exercised by the shared DB/HTTP suite.
		var grammar validation.Errors
		if value, present := form.Cleaned().Get("username"); present {
			grammar = forminput.UsernameValidator("username", 150).ValidateField(value)
		}
		form, err = form.WithErrors(grammar)
		if err != nil {
			t.Fatal(err)
		}
		result := formObservation{Valid: form.Valid(), Codes: map[string][]string{}}
		if username, present := form.Cleaned().String("username"); present {
			result.Username = &username
		}
		for _, failure := range form.Errors().All() {
			result.Codes[string(failure.Field())] = append(result.Codes[string(failure.Field())], string(failure.Code()))
		}
		return result
	}
	for i, input := range inputs.Usernames {
		t.Run("username/"+input.Name, func(t *testing.T) {
			want := expected.Usernames[i]
			got := observe(map[string][]string{"username": {input.Value}, "password1": {"reference-secret"}, "password2": {"reference-secret"}})
			got.Name = input.Name
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("username behavior differs: got %+v, reference %+v", got, want)
			}
		})
	}
	for i, input := range inputs.Passwords {
		t.Run("confirmation/"+input.Name, func(t *testing.T) {
			want := expected.Passwords[i]
			got := observe(map[string][]string{"username": {"Reference"}, "password1": {input.Password1}, "password2": {input.Password2}})
			got.Name = input.Name
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("confirmation behavior differs: got %+v, reference %+v", got, want)
			}
		})
	}
	for i, input := range inputs.Emails {
		t.Run("email/"+input.Name, func(t *testing.T) {
			if want := expected.Emails[i]; want.Name != input.Name || validation.ValidEmail(input.Value) != want.Valid {
				t.Fatal("EmailValidator behavior differs", input.Name, validation.ValidEmail(input.Value), want.Valid)
			}
		})
	}
}
