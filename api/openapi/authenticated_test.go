package openapi_test

import (
	"errors"
	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/web"
	"strings"
	"testing"
)

type principalDescription struct{ *describedAuthentication }

func (a principalDescription) RequireAuthenticated(api.AuthenticatedHandler) (web.Handler, error) {
	a.requireCalls++
	return nil, errors.New("documentation must not wrap handler")
}
func TestAuthenticatedOnlyRequiresExplicitCapabilityAndNoModelPermissions(t *testing.T) {
	for _, kind := range []api.AuthenticationKind{api.AuthenticationSession, api.AuthenticationBearer} {
		base := &describedAuthentication{description: sessionDescription()}
		if kind == api.AuthenticationBearer {
			base.description = api.AuthenticationDescription{Kind: kind}
		}
		config := documentConfig(t, base)
		for i := range config.Operations {
			config.Operations[i].Permission = ""
			config.Operations[i].AuthenticatedOnly = true
		}
		if _, err := openapi.New(config); err == nil {
			t.Fatal("undeclared capability accepted")
		}
		config.Authentication = principalDescription{base}
		document := newDocument(t, config)
		decoded := decodeDocument(t, document)
		if base.requireCalls != 0 {
			t.Fatal("document performed authentication")
		}
		for _, path := range decoded.Paths {
			for _, operation := range path {
				_, forbidden := operation.Responses["403"]
				if forbidden != (kind == api.AuthenticationSession) {
					t.Fatal("incorrect forbidden response")
				}
			}
		}
		text := string(document.Bytes())
		if !strings.Contains(text, `"x-godj-authenticated-only":true`) || strings.Contains(text, `"x-godj-permission"`) {
			t.Fatal("incorrect requirement extension")
		}
		for _, mode := range []string{"permission", "additional", "alternatives"} {
			bad := config
			bad.Operations = append([]openapi.Operation(nil), config.Operations...)
			switch mode {
			case "permission":
				bad.Operations[0].Permission = "articles.view"
			case "additional":
				bad.Operations[0].AdditionalPermissions = []auth.Permission{"articles.view"}
			case "alternatives":
				bad.Operations[0].AlternativePermissions = []auth.Permission{"articles.view"}
			}
			if _, err := openapi.New(bad); err == nil {
				t.Fatal("mixed auth-only requirements accepted", mode)
			}
		}
	}
}
