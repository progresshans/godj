package identityapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/bearerauth"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/web"
)

type documentBackend struct{ identity.ManagementBackend }
type documentVerifier struct{ calls *int }

func (v documentVerifier) Verify(context.Context, bearerauth.Token) (auth.Principal, error) {
	*v.calls++
	return auth.Principal{}, errors.New("document construction must not authenticate")
}

type nilAuthenticationMap map[string]string

func (nilAuthenticationMap) Require(auth.Permission, api.AuthenticatedHandler, ...auth.Permission) (web.Handler, error) {
	panic("typed nil authentication used")
}
func (nilAuthenticationMap) RequireAny(auth.Permission, api.AuthenticatedHandler, ...auth.Permission) (web.Handler, error) {
	panic("typed nil authentication used")
}

func TestManagementDocumentIsBuiltFromActualProtectedRoutesAndAllowlist(t *testing.T) {
	calls := 0
	authentication, err := bearerauth.New(bearerauth.Config{Verifier: documentVerifier{&calls}, Authorizer: auth.PrincipalAuthorizer{}})
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
	config := Config{Namespace: "identity", Backend: documentBackend{}, PasswordHasher: hasher, Authorizer: auth.PrincipalAuthorizer{}, Authentication: authentication, Users: policies.IdentityUser, Groups: policies.IdentityGroup, Permissions: policies.IdentityPermission}
	application, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	document, err := application.OpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 || len(document.Routes()) != 19 || len(application.Routes()) != 19 {
		t.Fatal("startup I/O or incomplete routes", calls, len(application.Routes()))
	}
	var schema map[string]any
	decoder := json.NewDecoder(bytes.NewReader(document.Bytes()))
	decoder.UseNumber()
	if err := decoder.Decode(&schema); err != nil {
		t.Fatal(err)
	}
	paths := schema["paths"].(map[string]any)
	patch := paths["/api/identity/users/{id}/"].(map[string]any)["patch"].(map[string]any)
	var condition map[string]any
	for _, raw := range patch["parameters"].([]any) {
		parameter := raw.(map[string]any)
		if parameter["name"] == "If-Revision" {
			condition = parameter["schema"].(map[string]any)
			if parameter["required"] != true {
				t.Fatal("revision condition became optional")
			}
		}
	}
	response := patch["responses"].(map[string]any)["200"].(map[string]any)["headers"].(map[string]any)["Revision"].(map[string]any)
	if condition["x-godj-header-integer"] != "canonical-decimal" || condition["x-godj-max-bytes"] != json.Number("19") || condition["x-godj-query-integer"] != nil {
		t.Fatal("revision header parser policy is absent or describes query decoding")
	}
	for _, bound := range []struct {
		schema  map[string]any
		maximum string
	}{
		{condition, "9223372036854775806"}, {response["schema"].(map[string]any), "9223372036854775807"},
	} {
		if bound.schema["type"] != "integer" || bound.schema["format"] != "int64" || bound.schema["minimum"] != json.Number("1") || bound.schema["maximum"] != json.Number(bound.maximum) {
			t.Fatal("row revision schema lost exact int64 bounds")
		}
	}
	if response["required"] != true {
		t.Fatal("response revision became optional")
	}
	definitions := schema["components"].(map[string]any)["schemas"].(map[string]any)
	fields := definitions["User"].(map[string]any)["properties"].(map[string]any)
	for _, name := range []string{"encoded_password", "principal_id", "password"} {
		if _, exists := fields[name]; exists {
			t.Fatal("private field in User response", name)
		}
	}
	if len(fields) != 14 {
		t.Fatal("unexpected user projection", len(fields))
	}
	for _, name := range []string{"User", "UserSummary"} {
		definition := definitions[name].(map[string]any)
		field := definition["properties"].(map[string]any)["password_usable"].(map[string]any)
		if field["type"] != "boolean" || field["readOnly"] != true || !slices.Contains(definition["required"].([]any), any("password_usable")) {
			t.Fatal("password status must be required and read-only in each response", name)
		}
	}
	for _, name := range []string{"UserCreate", "UserUpdate", "UserPatch", "PasswordReplacement"} {
		if definitions[name].(map[string]any)["properties"].(map[string]any)["password_usable"] != nil {
			t.Fatal("derived password status became a writable input", name)
		}
	}
	create := definitions["UserCreate"].(map[string]any)["properties"].(map[string]any)
	for _, name := range []string{"UserCreate", "PasswordReplacement"} {
		definition := definitions[name].(map[string]any)
		password := definition["properties"].(map[string]any)["password"].(map[string]any)
		encoded, _ := json.Marshal(password)
		if !strings.Contains(string(encoded), `"type":"null"`) {
			t.Fatal("password schema lost explicit null", name)
		}
		required, _ := json.Marshal(definition["required"])
		if !strings.Contains(string(required), `"password"`) {
			t.Fatal("password became optional", name)
		}
	}
	if create["password"] == nil || create["revision"] != nil || create["id"] != nil {
		t.Fatal("create command allows private identity/version assignment")
	}
	config.Authentication = nilAuthenticationMap(nil)
	if app, err := New(config); app != nil || err == nil {
		t.Fatal("typed nil adapter published")
	}
	config.Authentication = authentication
	config.Users = Config{}.Users
	if app, err := New(config); app != nil || err == nil {
		t.Fatal("missing host deletion policy published")
	}
	for _, app := range []*Application{nil, {}} {
		if _, err := app.OpenAPI(); err == nil {
			t.Fatal("uninitialized document published")
		}
	}
	encoded, err := json.Marshal(config)
	if err != nil || string(encoded) != `"identityapi.Config{redacted}"` {
		t.Fatal("config encoding", err)
	}
	if strings.Contains(fmt.Sprintf("%#v", config), "documentBackend") {
		t.Fatal("config diagnostic exposed collaborator")
	}
}
