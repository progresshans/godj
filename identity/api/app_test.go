package identityapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	if err := json.Unmarshal(document.Bytes(), &schema); err != nil {
		t.Fatal(err)
	}
	definitions := schema["components"].(map[string]any)["schemas"].(map[string]any)
	fields := definitions["User"].(map[string]any)["properties"].(map[string]any)
	for _, name := range []string{"encoded_password", "principal_id", "password"} {
		if _, exists := fields[name]; exists {
			t.Fatal("private field in User response", name)
		}
	}
	if len(fields) != 13 {
		t.Fatal("unexpected user projection", len(fields))
	}
	create := definitions["UserCreate"].(map[string]any)["properties"].(map[string]any)
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
