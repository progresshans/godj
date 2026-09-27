package openapi_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/auth"
)

func TestDocumentDescribesAndOwnsAllRequiredPermissions(t *testing.T) {
	authentication := &describedAuthentication{description: sessionDescription()}
	config := documentConfig(t, authentication)
	config.Operations[0].AdditionalPermissions = []auth.Permission{"links.ticket", "links.label"}
	document, err := openapi.New(config)
	if err != nil {
		t.Fatal(err)
	}
	encoded := document.Bytes()
	if !bytes.Contains(encoded, []byte(`"x-godj-additional-permissions":["links.ticket","links.label"]`)) {
		t.Fatal("conjunction missing from document")
	}
	config.Operations[0].AdditionalPermissions[0] = "links.changed"
	if !bytes.Equal(encoded, document.Bytes()) || authentication.requireCalls != 0 {
		t.Fatal("document retained mutable configuration or executed authorization")
	}
	var value map[string]any
	if err := json.Unmarshal(encoded, &value); err != nil {
		t.Fatal(err)
	}
	for _, permissions := range [][]auth.Permission{{""}, {"Links.View"}, {config.Operations[0].Permission}, {"links.ticket", "links.ticket"}} {
		config.Operations[0].AdditionalPermissions = permissions
		if _, err := openapi.New(config); err == nil {
			t.Fatal("invalid conjunction documented")
		}
	}
}

func TestDocumentAlternativePermissionContract(t *testing.T) {
	authentication := &describedAuthentication{description: sessionDescription()}
	config := documentConfig(t, authentication)
	original := config.Operations[0].Permission
	config.Operations[0].AlternativePermissions = []auth.Permission{"links.change"}
	document, err := openapi.New(config)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(document.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, methods := range value["paths"].(map[string]any) {
		for _, raw := range methods.(map[string]any) {
			operation := raw.(map[string]any)
			if operation["operationId"] != config.Operations[0].Route.Name {
				continue
			}
			found = true
			permissions, ok := operation["x-godj-any-permissions"].([]any)
			if !ok || len(permissions) != 2 || permissions[0] != string(original) || permissions[1] != "links.change" || operation["x-godj-permission"] != nil || operation["x-godj-additional-permissions"] != nil || len(operation["security"].([]any)) == 0 {
				t.Fatal("alternative auth metadata")
			}
		}
	}
	if !found || authentication.requireCalls != 0 {
		t.Fatal("no operation or construction executed auth")
	}
	before := document.Bytes()
	config.Operations[0].AlternativePermissions[0] = "links.changed"
	if !bytes.Equal(before, document.Bytes()) {
		t.Fatal("document aliases alternatives")
	}
	for _, bad := range [][]auth.Permission{{""}, {original}, {"Links.View"}, {"links.change", "links.change"}} {
		config.Operations[0].AlternativePermissions = bad
		if _, err := openapi.New(config); err == nil {
			t.Fatal("invalid alternatives accepted")
		}
	}
	config.Operations[0].AlternativePermissions = []auth.Permission{"links.change"}
	config.Operations[0].AdditionalPermissions = []auth.Permission{"links.other"}
	if _, err := openapi.New(config); err == nil {
		t.Fatal("ambiguous mixed conjunction/disjunction accepted")
	}
}
