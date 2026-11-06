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

	"github.com/progresshans/godj/api/bearerauth"
	"github.com/progresshans/godj/auth"
)

type documentVerifier struct{ calls *int }

func (v documentVerifier) Verify(context.Context, bearerauth.Token) (auth.Principal, error) {
	*v.calls++
	return auth.Principal{}, errors.New("document construction must not authenticate")
}

func TestSelectedManagementRevisionAndCredentialSchema(t *testing.T) {
	owner, catalog, probe := selectedOwners(t)
	calls := 0
	authentication, err := bearerauth.New(bearerauth.Config{Verifier: documentVerifier{&calls}, Authorizer: auth.PrincipalAuthorizer{}})
	if err != nil {
		t.Fatal(err)
	}
	config := ManagementConfig{Authentication: authentication}
	application, err := NewManagement(owner, catalog, config)
	if err != nil {
		t.Fatal(err)
	}
	document, err := application.OpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	decoder := json.NewDecoder(bytes.NewReader(document.Bytes()))
	decoder.UseNumber()
	if err := decoder.Decode(&schema); err != nil {
		t.Fatal(err)
	}
	paths := schema["paths"].(map[string]any)
	for _, resource := range []string{"accounts", "groups", "permissions"} {
		patch := paths["/api/identity/"+resource+"/{id}/"].(map[string]any)["patch"].(map[string]any)
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
			t.Fatal("revision header policy is absent or describes query decoding")
		}
		for _, bound := range []struct {
			schema  map[string]any
			maximum string
		}{{condition, "9223372036854775806"}, {response["schema"].(map[string]any), "9223372036854775807"}} {
			if bound.schema["type"] != "integer" || bound.schema["format"] != "int64" || bound.schema["minimum"] != json.Number("1") || bound.schema["maximum"] != json.Number(bound.maximum) {
				t.Fatal("row revision schema lost exact int64 bounds")
			}
		}
		if response["required"] != true {
			t.Fatal("response revision became optional")
		}
	}
	definitions := schema["components"].(map[string]any)["schemas"].(map[string]any)
	for _, name := range []string{"Account", "AccountSummary"} {
		definition := definitions[name].(map[string]any)
		if !slices.Contains(definition["required"].([]any), any("password_usable")) {
			t.Fatal("derived password status became optional", name)
		}
	}
	for _, name := range []string{"AccountCreate", "AccountPassword"} {
		definition := definitions[name].(map[string]any)
		credential := definition["properties"].(map[string]any)["credential"].(map[string]any)
		password := credential["properties"].(map[string]any)["password"].(map[string]any)
		encoded, err := json.Marshal(password)
		if err != nil || !strings.Contains(string(encoded), `"type":"null"`) || !slices.Contains(definition["required"].([]any), any("credential")) || !slices.Contains(credential["required"].([]any), any("password")) {
			t.Fatal("credential must be present with an explicit nullable password", name, err)
		}
	}
	encoded, err := json.Marshal(config)
	if err != nil || strings.Contains(string(encoded), "Authentication") || strings.Contains(fmt.Sprintf("%#v", config), "documentVerifier") {
		t.Fatal("configuration exposed authentication collaborator", err)
	}
	if calls != 0 || probe.reads.Load() != 0 || probe.writes.Load() != 0 {
		t.Fatal("schema construction performed I/O")
	}
}
