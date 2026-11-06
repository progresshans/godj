package openapi_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/web"
)

type csrfDescription struct{ *describedAuthentication }

func (a csrfDescription) RequireCSRF(web.Handler) (web.Handler, error) {
	a.requireCalls++
	return nil, errors.New("documentation must not wrap")
}

func TestCSRFOnlyDescribesAnonymousSafeAndPairedUnsafeAdmission(t *testing.T) {
	base := &describedAuthentication{description: sessionDescription()}
	config := documentConfig(t, csrfDescription{base})
	for i := range config.Operations {
		config.Operations[i].Permission = ""
		config.Operations[i].CSRFOnly = true
	}
	document := newDocument(t, config)
	decoded := decodeDocument(t, document)
	if base.requireCalls != 0 {
		t.Fatal("documentation executed CSRF wrapper")
	}
	csrfCookie := findSecurityScheme(t, decoded, "apiKey", "cookie", "app_csrf")
	csrfHeader := findSecurityScheme(t, decoded, "apiKey", "header", "X-App-CSRF")
	for _, methods := range decoded.Paths {
		for method, operation := range methods {
			safe := method == "get" || method == "head"
			if safe {
				if len(operation.Security) != 0 {
					t.Fatal("safe CSRF-only requires a session")
				}
				if _, found := operation.Responses["403"]; found {
					t.Fatal("safe CSRF-only invented authentication denial")
				}
			} else {
				requireSecurity(t, operation, csrfCookie, csrfHeader)
				if _, found := operation.Responses["403"]; !found {
					t.Fatal("missing CSRF rejection")
				}
			}
			if _, found := operation.Responses["406"]; !found {
				t.Fatal("negotiation disappeared")
			}
		}
	}
	if strings.Contains(string(document.Bytes()), `"x-godj-permission"`) || !strings.Contains(string(document.Bytes()), `"x-godj-csrf-only":true`) {
		t.Fatal("ambiguous admission metadata")
	}
	config.Authentication = base
	if _, err := openapi.New(config); err == nil {
		t.Fatal("undeclared CSRF capability accepted")
	}
	base.description = api.AuthenticationDescription{Kind: api.AuthenticationBearer}
	config.Authentication = csrfDescription{base}
	if _, err := openapi.New(config); err == nil {
		t.Fatal("Bearer advertised cookie CSRF admission")
	}
	base.description = sessionDescription()
	for _, mixed := range []string{"authenticated", "permission", "additional", "alternative"} {
		bad := config
		bad.Operations = append([]openapi.Operation(nil), config.Operations...)
		switch mixed {
		case "authenticated":
			bad.Operations[0].AuthenticatedOnly = true
		case "permission":
			bad.Operations[0].Permission = "articles.view"
		case "additional":
			bad.Operations[0].AdditionalPermissions = []auth.Permission{"articles.view"}
		case "alternative":
			bad.Operations[0].AlternativePermissions = []auth.Permission{"articles.view"}
		}
		if _, err := openapi.New(bad); err == nil {
			t.Fatal("mixed CSRF-only admission accepted", mixed)
		}
	}
}

func TestAnonymousApplicationProofRequiresExplicitSessionCookie(t *testing.T) {
	base := &describedAuthentication{description: sessionDescription()}
	config := documentConfig(t, csrfDescription{base})
	config.Operations = config.Operations[:1]
	operation := &config.Operations[0]
	operation.Permission = ""
	operation.CSRFOnly = true
	operation.SessionCookieRequired = true
	document := newDocument(t, config)
	decoded := decodeDocument(t, document)
	session := findSecurityScheme(t, decoded, "apiKey", "cookie", "app_session")
	requireSecurity(t, decoded.Paths["/articles/"]["get"], session)
	if _, found := decoded.Paths["/articles/"]["get"].Responses["403"]; !found {
		t.Fatal("application proof refusal absent")
	}
	if !strings.Contains(string(document.Bytes()), `"x-godj-session-cookie-required":true`) {
		t.Fatal("proof cookie requirement absent")
	}
	operation.CSRFOnly = false
	operation.AuthenticatedOnly = true
	if _, err := openapi.New(config); err == nil {
		t.Fatal("proof transport silently mixed with principal admission")
	}
}
