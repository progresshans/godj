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
