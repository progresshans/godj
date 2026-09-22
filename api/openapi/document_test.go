package openapi_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/web"
)

// These fixtures describe handlers that have already been protected by the
// application. Document construction must not run Require a second time.
type describedAuthentication struct {
	description  api.AuthenticationDescription
	err          error
	requireCalls int
}

func (a *describedAuthentication) Require(auth.Permission, api.AuthenticatedHandler, ...auth.Permission) (web.Handler, error) {
	a.requireCalls++
	return nil, errors.New("documentation must not wrap an already protected handler")
}

func (a *describedAuthentication) DescribeAuthentication() (api.AuthenticationDescription, error) {
	if a == nil {
		return api.AuthenticationDescription{}, errors.New("nil authentication")
	}
	return a.description, a.err
}

type undescribedAuthentication struct{}

func (undescribedAuthentication) Require(auth.Permission, api.AuthenticatedHandler, ...auth.Permission) (web.Handler, error) {
	return nil, errors.New("unexpected Require call")
}

func sessionDescription() api.AuthenticationDescription {
	return api.AuthenticationDescription{
		Kind: api.AuthenticationSession, SessionCookieName: "app_session",
		CSRFCookieName: "app_csrf", CSRFHeader: "X-App-CSRF",
	}
}

func TestDocumentDescribesSelectedSessionProfileAndExistingHandlers(t *testing.T) {
	authentication := &describedAuthentication{description: sessionDescription()}
	config := documentConfig(t, authentication)
	handlerCalls := 0
	config.Operations[0].Route.Handler = func(*web.Request) (web.Response, error) {
		handlerCalls++
		return api.NoContent()
	}
	document := newDocument(t, config)
	if authentication.requireCalls != 0 || handlerCalls != 0 {
		t.Fatalf("construction called runtime handlers: Require=%d handler=%d", authentication.requireCalls, handlerCalls)
	}
	decoded := decodeDocument(t, document)
	if !strings.HasPrefix(decoded.OpenAPI, "3.1.") || decoded.Info["title"] != config.Title || decoded.Info["version"] != config.Version {
		t.Fatalf("document metadata = %q %#v", decoded.OpenAPI, decoded.Info)
	}
	session := findSecurityScheme(t, decoded, "apiKey", "cookie", "app_session")
	csrfCookie := findSecurityScheme(t, decoded, "apiKey", "cookie", "app_csrf")
	csrfHeader := findSecurityScheme(t, decoded, "apiKey", "header", "X-App-CSRF")
	if len(decoded.Components.SecuritySchemes) != 3 {
		t.Fatalf("session document described unrelated authentication schemes: %#v", decoded.Components.SecuritySchemes)
	}
	for _, method := range []string{"get", "head"} {
		requireSecurity(t, decoded.Paths["/articles/{id}/"][method], session)
	}
	for _, operation := range []operationJSON{
		decoded.Paths["/articles/"]["post"],
		decoded.Paths["/articles/{id}/"]["patch"],
		decoded.Paths["/articles/{id}/"]["delete"],
	} {
		requireSecurity(t, operation, session, csrfCookie, csrfHeader)
		for _, status := range []string{"403", "406", "500"} {
			if _, found := operation.Responses[status]; !found {
				t.Errorf("%s lacks known response %s", operation.OperationID, status)
			}
		}
		if _, found := operation.Responses["401"]; found {
			t.Errorf("session operation %s incorrectly describes a Bearer-style 401", operation.OperationID)
		}
	}
	for _, route := range document.Routes() {
		if route.Name == config.Operations[0].Route.Name {
			response, err := route.Handler(nil)
			if err != nil || response.Status() != http.StatusNoContent || handlerCalls != 1 {
				t.Fatalf("document route did not preserve application handler: status=%d calls=%d err=%v", response.Status(), handlerCalls, err)
			}
			return
		}
	}
	t.Fatal("document dropped an application route")
}

func TestDocumentBearerProfileMergesKnownErrorsWithoutCookieRequirements(t *testing.T) {
	authentication := &describedAuthentication{description: api.AuthenticationDescription{Kind: api.AuthenticationBearer}}
	config := documentConfig(t, authentication)
	errorSchema, err := openapi.JSONErrorSchema()
	if err != nil {
		t.Fatal(err)
	}
	config.Operations[1].Responses = append(config.Operations[1].Responses, openapi.Response{
		Status: http.StatusBadRequest, Description: "Invalid application input", ContentType: api.JSONContentType, Schema: errorSchema,
	})
	decoded := decodeDocument(t, newDocument(t, config))
	bearer := findSecurityScheme(t, decoded, "http", "", "")
	if len(decoded.Components.SecuritySchemes) != 1 || decoded.Components.SecuritySchemes[bearer].Scheme != "bearer" {
		t.Fatalf("Bearer scheme = %#v", decoded.Components.SecuritySchemes)
	}
	for _, path := range decoded.Paths {
		for _, operation := range path {
			requireSecurity(t, operation, bearer)
			for _, status := range []string{"400", "401", "403", "406", "500"} {
				if _, found := operation.Responses[status]; !found {
					t.Errorf("%s lacks known response %s", operation.OperationID, status)
				}
			}
		}
	}
	create := decoded.Paths["/articles/"]["post"]
	if create.Responses["400"].Content[api.JSONContentType].Schema == nil {
		t.Fatal("merged application/authentication 400 lost its JSON error schema")
	}
	for _, status := range []string{"400", "401", "403"} {
		if _, found := responseHeader(create.Responses[status], "WWW-Authenticate"); !found {
			t.Errorf("Bearer %s response lacks its authentication challenge header", status)
		}
	}
	if authentication.requireCalls != 0 {
		t.Fatalf("document construction called Require %d times", authentication.requireCalls)
	}
}

func TestDocumentOnlyAdvertisesConfiguredJSONNegotiation(t *testing.T) {
	for _, prefix := range []string{"", "/articles/", "/other/"} {
		config := documentConfig(t, &describedAuthentication{description: sessionDescription()})
		config.JSONPolicy = api.JSONPolicy{}
		if prefix != "" {
			var err error
			config.JSONPolicy, err = api.NewJSONPolicy(prefix)
			if err != nil {
				t.Fatal(err)
			}
		}
		decoded := decodeDocument(t, newDocument(t, config))
		for _, path := range decoded.Paths {
			for _, operation := range path {
				_, present := operation.Responses["406"]
				if present != (prefix == "/articles/") {
					t.Fatalf("policy %q, operation %s: 406=%v", prefix, operation.OperationID, present)
				}
			}
		}
	}
}

func TestDocumentRejectsPartialDynamicRouteNegotiation(t *testing.T) {
	config := documentConfig(t, &describedAuthentication{description: sessionDescription()})
	policy, err := api.NewJSONPolicy("/articles/1/")
	if err != nil {
		t.Fatal(err)
	}
	config.JSONPolicy = policy
	// The policy applies to id=1 but not to every value accepted by the same
	// /articles/{id}/ operation. One operation cannot advertise both policies.
	requireDocumentConfigError(t, config, "json_policy")
}

func TestDocumentMergedFailuresAllowOnlyOptionalApplicationHeaders(t *testing.T) {
	errorSchema, err := openapi.JSONErrorSchema()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name        string
		description api.AuthenticationDescription
		status      int
		merged      bool
	}{
		{"session forbidden", sessionDescription(), http.StatusForbidden, true},
		{"session negotiation", sessionDescription(), http.StatusNotAcceptable, true},
		{"bearer malformed", api.AuthenticationDescription{Kind: api.AuthenticationBearer}, http.StatusBadRequest, true},
		{"bearer unauthenticated", api.AuthenticationDescription{Kind: api.AuthenticationBearer}, http.StatusUnauthorized, true},
		{"bearer forbidden", api.AuthenticationDescription{Kind: api.AuthenticationBearer}, http.StatusForbidden, true},
		{"bearer negotiation", api.AuthenticationDescription{Kind: api.AuthenticationBearer}, http.StatusNotAcceptable, true},
		// A session application's own 400 is not an authentication failure.
		{"application only", sessionDescription(), http.StatusBadRequest, false},
	} {
		for _, required := range []bool{false, true} {
			t.Run(test.name+"/required="+strconv.FormatBool(required), func(t *testing.T) {
				config := documentConfig(t, &describedAuthentication{description: test.description})
				config.Operations[0].Responses = append(config.Operations[0].Responses, openapi.Response{
					Status: test.status, Description: "Application error", ContentType: api.JSONContentType, Schema: errorSchema,
					Headers: []openapi.Header{{Name: "X-Application-Error", Schema: openapi.String(), Required: required}},
				})
				if required && test.merged {
					requireDocumentConfigError(t, config, "operation.response.headers")
					return
				}
				decoded := decodeDocument(t, newDocument(t, config))
				response := decoded.Paths["/articles/"]["get"].Responses[strconv.Itoa(test.status)]
				header, found := responseHeader(response, "X-Application-Error")
				if !found || header.Required != required || header.Schema["type"] != "string" {
					t.Fatalf("application header presence changed while merging the response: %#v", header)
				}
			})
		}
	}
}

func TestDocumentMergesOnlyAliasesOfTheAPIErrorEnvelope(t *testing.T) {
	errorSchema, err := openapi.JSONErrorSchema()
	if err != nil {
		t.Fatal(err)
	}
	alias, err := openapi.Ref("ApplicationError")
	if err != nil {
		t.Fatal(err)
	}
	target, err := openapi.Ref("ApplicationErrorBody")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		schema openapi.Schema
		valid  bool
	}{
		{"API error envelope", errorSchema, true},
		{"different envelope", openapi.String(), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := documentConfig(t, &describedAuthentication{description: api.AuthenticationDescription{Kind: api.AuthenticationBearer}})
			config.Schemas = []openapi.NamedSchema{
				{Name: "ApplicationError", Schema: target},
				{Name: "ApplicationErrorBody", Schema: test.schema},
			}
			config.Operations[1].Responses = append(config.Operations[1].Responses, openapi.Response{
				Status: http.StatusBadRequest, Description: "Invalid application input", ContentType: api.JSONContentType, Schema: alias,
			})
			if !test.valid {
				requireDocumentConfigError(t, config, "operation.response")
				return
			}
			decoded := decodeDocument(t, newDocument(t, config))
			responseSchema := decoded.Paths["/articles/"]["post"].Responses["400"].Content[api.JSONContentType].Schema
			if responseSchema["$ref"] != "#/components/schemas/ApplicationError" || decoded.Components.Schemas["ApplicationError"]["$ref"] != "#/components/schemas/ApplicationErrorBody" {
				t.Fatal("merging the error response expanded or replaced the caller's schema identity")
			}
			if decoded.Components.Schemas["ApplicationErrorBody"]["type"] != "object" || decoded.Components.Schemas[openapi.ErrorSchemaName]["type"] != "object" {
				t.Fatal("merged aliases lost the caller or builtin error definition")
			}
		})
	}
}

func TestDocumentValidatesReferencesAtEveryOperationSchemaBoundary(t *testing.T) {
	reference, err := openapi.Ref("LateBound")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*openapi.Config)
		read   func(documentJSON) map[string]any
	}{
		{"request", func(config *openapi.Config) { config.Operations[1].RequestBody.Schema = reference },
			func(document documentJSON) map[string]any {
				return document.Paths["/articles/"]["post"].RequestBody.Content[api.JSONContentType].Schema
			}},
		{"response", func(config *openapi.Config) { config.Operations[0].Responses[0].Schema = reference },
			func(document documentJSON) map[string]any {
				return document.Paths["/articles/"]["get"].Responses["200"].Content[api.JSONContentType].Schema
			}},
		{"parameter", func(config *openapi.Config) { config.Operations[0].Parameters[0].Schema = reference },
			func(document documentJSON) map[string]any {
				return document.Paths["/articles/"]["get"].Parameters[0].Schema
			}},
		{"response header", func(config *openapi.Config) { config.Operations[1].Responses[0].Headers[0].Schema = reference },
			func(document documentJSON) map[string]any {
				return document.Paths["/articles/"]["post"].Responses["201"].Headers["Location"].Schema
			}},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := documentConfig(t, &describedAuthentication{description: sessionDescription()})
			test.change(&config)
			requireDocumentConfigError(t, config, "schema.reference")
			// Supplying the missing component must make the same boundary valid
			// while preserving its reference in the published document.
			config.Schemas = []openapi.NamedSchema{{Name: "LateBound", Schema: openapi.String()}}
			decoded := decodeDocument(t, newDocument(t, config))
			if test.read(decoded)["$ref"] != "#/components/schemas/LateBound" {
				t.Fatal("operation schema reference was dropped or expanded")
			}
		})
	}
}

func TestDocumentReservesTheBuiltinAPIErrorSchemaIdentity(t *testing.T) {
	errorSchema, err := openapi.JSONErrorSchema()
	if err != nil {
		t.Fatal(err)
	}
	config := documentConfig(t, &describedAuthentication{description: sessionDescription()})
	decoded := decodeDocument(t, newDocument(t, config))
	if decoded.Components.Schemas[openapi.ErrorSchemaName]["type"] != "object" || decoded.Paths["/articles/"]["get"].Responses["403"].Content[api.JSONContentType].Schema["$ref"] != "#/components/schemas/"+openapi.ErrorSchemaName {
		t.Fatal("the default authentication error does not use the builtin component identity")
	}
	for _, test := range []struct {
		name   string
		schema openapi.Schema
	}{
		{"same definition collision", errorSchema},
		{"different definition override", openapi.String()},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := documentConfig(t, &describedAuthentication{description: sessionDescription()})
			config.Schemas = []openapi.NamedSchema{{Name: openapi.ErrorSchemaName, Schema: test.schema}}
			requireDocumentConfigError(t, config, "schema.components")
		})
	}
}

func TestDocumentPreservesTransportPresenceAndBodylessResponses(t *testing.T) {
	config := documentConfig(t, &describedAuthentication{description: sessionDescription()})
	document := newDocument(t, config)
	decoded := decodeDocument(t, document)
	detail := decoded.Paths["/articles/{id}/"]["get"]
	if len(detail.Parameters) != 1 {
		t.Fatalf("detail parameters = %#v", detail.Parameters)
	}
	parameter := detail.Parameters[0]
	if parameter.Name != "id" || parameter.In != "path" || !parameter.Required ||
		parameter.Schema["type"] != "integer" || parameter.Schema["format"] != "int64" ||
		parameter.Schema["minimum"] != json.Number("0") || parameter.Schema["maximum"] != json.Number("9223372036854775807") {
		t.Fatalf("router parameter contract = %#v", parameter)
	}
	patch := decoded.Paths["/articles/{id}/"]["patch"]
	if patch.RequestBody == nil || !patch.RequestBody.Required || patch.RequestBody.Content[api.JSONContentType].Schema == nil {
		t.Fatalf("PATCH body presence was confused with optional properties: %#v", patch.RequestBody)
	}
	create := decoded.Paths["/articles/"]["post"]
	header, found := responseHeader(create.Responses["201"], "Location")
	if !found || !header.Required || header.Schema["type"] != "string" {
		t.Fatalf("created Location header = %#v, found=%t", header, found)
	}
	if len(decoded.Paths["/articles/{id}/"]["delete"].Responses["204"].Content) != 0 {
		t.Fatal("204 was documented as a JSON null response")
	}
	for status, response := range decoded.Paths["/articles/{id}/"]["head"].Responses {
		if len(response.Content) != 0 {
			t.Errorf("HEAD %s incorrectly describes a response body", status)
		}
	}
	internalError := detail.Responses["500"]
	if len(internalError.Content) != 1 || internalError.Content["text/plain"].Schema["type"] != "string" {
		t.Fatalf("500 did not preserve Web's plain-text error representation: %#v", internalError.Content)
	}
	response, err := document.Response()
	if err != nil || response.Status() != http.StatusOK || response.Header().Get("Content-Type") != api.JSONContentType || !bytes.Equal(response.Body(), document.Bytes()) {
		t.Fatalf("document response status=%d headers=%v err=%v", response.Status(), response.Header(), err)
	}
}

func TestDocumentSnapshotsDeclarationsAndReturnsDetachedValues(t *testing.T) {
	authentication := &describedAuthentication{description: sessionDescription()}
	config := documentConfig(t, authentication)
	document := newDocument(t, config)
	original := document.Bytes()
	repeated := newDocument(t, config)
	if !bytes.Equal(original, repeated.Bytes()) {
		t.Fatal("the same declarations produced different document bytes")
	}
	config.Operations[0].Route.Name = "changed:list"
	config.Operations[0].Route.Handler = nil
	config.Operations[0].Parameters[0].Name = "changed"
	config.Operations[0].Responses[0].Description = "changed"
	config.Operations[1].Summary = "changed"
	config.Operations[1].RequestBody.Schema = openapi.Schema{}
	config.Operations[1].Responses[0].Headers[0].Name = "Changed"
	authentication.description = api.AuthenticationDescription{Kind: api.AuthenticationBearer}
	returned := document.Bytes()
	returned[0] = '!'
	routes := document.Routes()
	if len(routes) != len(config.Operations) {
		t.Fatalf("Routes() length = %d, want %d", len(routes), len(config.Operations))
	}
	routes[0].Name = "changed:returned"
	routes[0].Handler = nil
	if !bytes.Equal(original, document.Bytes()) {
		t.Fatal("mutating caller configuration or returned bytes changed the document")
	}
	for _, route := range document.Routes() {
		if strings.HasPrefix(route.Name, "changed:") || route.Handler == nil {
			t.Fatalf("mutating caller configuration or returned routes changed the snapshot: %q", route.Name)
		}
	}
}

func TestDocumentRejectsUnpublishableConfiguration(t *testing.T) {
	var nilAuthentication *describedAuthentication
	tests := []struct {
		name   string
		change func(*openapi.Config)
	}{
		{"missing title", func(c *openapi.Config) { c.Title = "" }},
		{"missing version", func(c *openapi.Config) { c.Version = "" }},
		{"invalid metadata text", func(c *openapi.Config) { c.Title = string([]byte{0xff}) }},
		{"no operations", func(c *openapi.Config) { c.Operations = nil }},
		{"nil authentication", func(c *openapi.Config) { c.Authentication = nil }},
		{"typed nil authentication", func(c *openapi.Config) { c.Authentication = nilAuthentication }},
		{"unknown authentication", func(c *openapi.Config) { c.Authentication = undescribedAuthentication{} }},
		{"authentication description failure", func(c *openapi.Config) {
			c.Authentication = &describedAuthentication{err: errors.New("description failed")}
		}},
		{"unknown authentication kind", func(c *openapi.Config) {
			c.Authentication = &describedAuthentication{description: api.AuthenticationDescription{Kind: 99}}
		}},
		{"missing session name", func(c *openapi.Config) {
			d := sessionDescription()
			d.SessionCookieName = ""
			c.Authentication = &describedAuthentication{description: d}
		}},
		{"invalid CSRF header", func(c *openapi.Config) {
			d := sessionDescription()
			d.CSRFHeader = "X-CSRF\r\nOther"
			c.Authentication = &describedAuthentication{description: d}
		}},
		{"missing permission", func(c *openapi.Config) { c.Operations[0].Permission = "" }},
		{"invalid permission", func(c *openapi.Config) { c.Operations[0].Permission = "not:a.permission" }},
		{"missing operation ID", func(c *openapi.Config) { c.Operations[0].Route.Name = "" }},
		{"route name without namespace", func(c *openapi.Config) { c.Operations[0].Route.Name = "list" }},
		{"route name with invalid local part", func(c *openapi.Config) { c.Operations[0].Route.Name = "articles:bad name" }},
		{"missing route handler", func(c *openapi.Config) { c.Operations[0].Route.Handler = nil }},
		{"unsupported method", func(c *openapi.Config) { c.Operations[0].Route.Method = "CONNECT" }},
		{"lowercase method", func(c *openapi.Config) { c.Operations[0].Route.Method = "get" }},
		{"empty path", func(c *openapi.Config) { c.Operations[0].Route.Path = "" }},
		{"unsupported path converter", func(c *openapi.Config) { c.Operations[0].Route.Path = "/articles/<uuid:id>/" }},
		{"duplicate operation ID", func(c *openapi.Config) { c.Operations[1].Route.Name = c.Operations[0].Route.Name }},
		{"duplicate method and path", func(c *openapi.Config) { c.Operations[1].Route.Method = c.Operations[0].Route.Method }},
		{"different names for one template shape", func(c *openapi.Config) { c.Operations[3].Route.Path = "/articles/<int64:article>/" }},
		{"overlapping different parameter shapes", func(c *openapi.Config) {
			// Both GET routes match /articles/1/2/ even though replacing each
			// parameter with {} yields two different path template shapes.
			c.Operations[0].Route.Path = "/articles/<int64:left>/2/"
			c.Operations[2].Route.Path = "/articles/1/<int64:right>/"
		}},
		{"unknown parameter location", func(c *openapi.Config) { c.Operations[0].Parameters[0].In = "unknown" }},
		{"invalid parameter schema", func(c *openapi.Config) { c.Operations[0].Parameters[0].Schema = openapi.Schema{} }},
		{"empty value on header parameter", func(c *openapi.Config) {
			c.Operations[0].Parameters[0].In = "header"
			c.Operations[0].Parameters[0].AllowEmptyValue = true
		}},
		{"duplicate parameter", func(c *openapi.Config) {
			c.Operations[0].Parameters = append(c.Operations[0].Parameters, c.Operations[0].Parameters[0])
		}},
		{"explicit path parameter conflicts with router", func(c *openapi.Config) {
			c.Operations[2].Parameters = []openapi.Parameter{{Name: "id", In: "path", Required: true, Schema: openapi.String()}}
		}},
		{"invalid request schema", func(c *openapi.Config) { c.Operations[1].RequestBody.Schema = openapi.Schema{} }},
		{"no responses", func(c *openapi.Config) { c.Operations[0].Responses = nil }},
		{"missing response description", func(c *openapi.Config) { c.Operations[0].Responses[0].Description = "" }},
		{"informational response", func(c *openapi.Config) { c.Operations[0].Responses[0].Status = 199 }},
		{"invalid status", func(c *openapi.Config) { c.Operations[0].Responses[0].Status = 600 }},
		{"invalid response schema", func(c *openapi.Config) { c.Operations[0].Responses[0].Schema = openapi.Schema{} }},
		{"media type without subtype", func(c *openapi.Config) { c.Operations[0].Responses[0].ContentType = "json" }},
		{"duplicate response status", func(c *openapi.Config) {
			c.Operations[0].Responses = append(c.Operations[0].Responses, c.Operations[0].Responses[0])
		}},
		{"204 response with body", func(c *openapi.Config) { c.Operations[0].Responses[0].Status = http.StatusNoContent }},
		{"invalid header name", func(c *openapi.Config) { c.Operations[1].Responses[0].Headers[0].Name = "Location\nInjected" }},
		{"invalid header schema", func(c *openapi.Config) { c.Operations[1].Responses[0].Headers[0].Schema = openapi.Schema{} }},
		{"case insensitive duplicate header", func(c *openapi.Config) {
			c.Operations[1].Responses[0].Headers = append(c.Operations[1].Responses[0].Headers, openapi.Header{Name: "location", Schema: openapi.String()})
		}},
		{"reserved session CSRF header", func(c *openapi.Config) {
			c.Operations[0].Responses[0].Headers = []openapi.Header{{Name: "x-aPp-cSrF", Schema: openapi.String()}}
		}},
		{"reserved Bearer challenge header", func(c *openapi.Config) {
			c.Authentication = &describedAuthentication{description: api.AuthenticationDescription{Kind: api.AuthenticationBearer}}
			c.Operations[0].Responses[0].Headers = []openapi.Header{{Name: "wWw-AuThEnTiCaTe", Schema: openapi.String()}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := documentConfig(t, &describedAuthentication{description: sessionDescription()})
			test.change(&config)
			document, err := openapi.New(config)
			if err == nil {
				t.Fatal("New accepted invalid configuration")
			}
			if len(document.Bytes()) != 0 || len(document.Routes()) != 0 {
				t.Fatal("failed construction published a partial document or routes")
			}
		})
	}
}

func TestZeroDocumentCannotPublishAResponse(t *testing.T) {
	var document openapi.Document
	if len(document.Bytes()) != 0 || len(document.Routes()) != 0 {
		t.Fatal("zero document exposed data")
	}
	if _, err := document.Response(); err == nil {
		t.Fatal("zero document published a response")
	}
}

func documentConfig(t *testing.T, authentication api.Authentication) openapi.Config {
	t.Helper()
	policy, err := api.NewJSONPolicy("/articles/")
	if err != nil {
		t.Fatal(err)
	}
	input, err := openapi.Object(openapi.Property{Name: "title", Schema: openapi.String()})
	if err != nil {
		t.Fatal(err)
	}
	output, err := openapi.Object(
		openapi.Property{Name: "id", Schema: openapi.Integer(), Required: true},
		openapi.Property{Name: "title", Schema: openapi.String(), Required: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	route := func(name, method, path string) web.Route {
		return web.Route{Name: "articles:" + name, Method: method, Path: path, Handler: func(*web.Request) (web.Response, error) { return api.NoContent() }}
	}
	jsonResponse := func(status int) openapi.Response {
		return openapi.Response{Status: status, Description: "Article response", ContentType: api.JSONContentType, Schema: output}
	}
	created := jsonResponse(http.StatusCreated)
	created.Headers = []openapi.Header{{Name: "Location", Description: "Created article", Schema: openapi.String(), Required: true}}
	return openapi.Config{
		Title: "Article API", Version: "1", Authentication: authentication,
		JSONPolicy: policy,
		Operations: []openapi.Operation{
			{Route: route("list", http.MethodGet, "/articles/"), Permission: "articles.view_article", Parameters: []openapi.Parameter{{Name: "search", In: "query", Schema: openapi.String()}}, Responses: []openapi.Response{jsonResponse(http.StatusOK)}},
			{Route: route("create", http.MethodPost, "/articles/"), Summary: "Create article", Permission: "articles.add_article", RequestBody: &openapi.RequestBody{Schema: input, Required: true}, Responses: []openapi.Response{created}},
			{Route: route("detail", http.MethodGet, "/articles/<int64:id>/"), Permission: "articles.view_article", Responses: []openapi.Response{jsonResponse(http.StatusOK)}},
			{Route: route("patch", http.MethodPatch, "/articles/<int64:id>/"), Permission: "articles.change_article", RequestBody: &openapi.RequestBody{Schema: input, Required: true}, Responses: []openapi.Response{jsonResponse(http.StatusOK)}},
			{Route: route("delete", http.MethodDelete, "/articles/<int64:id>/"), Permission: "articles.delete_article", Responses: []openapi.Response{{Status: http.StatusNoContent, Description: "Deleted"}}},
			{Route: route("head", http.MethodHead, "/articles/<int64:id>/"), Permission: "articles.view_article", Responses: []openapi.Response{jsonResponse(http.StatusOK)}},
		},
	}
}

func newDocument(t *testing.T, config openapi.Config) openapi.Document {
	t.Helper()
	document, err := openapi.New(config)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func requireDocumentConfigError(t *testing.T, config openapi.Config, field string) {
	t.Helper()
	document, err := openapi.New(config)
	if !errors.Is(err, &api.Error{Code: api.FailureInvalidConfig, Field: field}) {
		t.Fatalf("New error = %v, want invalid_config at %s", err, field)
	}
	if len(document.Bytes()) != 0 || len(document.Routes()) != 0 {
		t.Fatal("failed document composition published partial bytes or routes")
	}
}

type documentJSON struct {
	OpenAPI    string                              `json:"openapi"`
	Info       map[string]string                   `json:"info"`
	Paths      map[string]map[string]operationJSON `json:"paths"`
	Components struct {
		SecuritySchemes map[string]securitySchemeJSON `json:"securitySchemes"`
		Schemas         map[string]map[string]any     `json:"schemas"`
	} `json:"components"`
}

type securitySchemeJSON struct {
	Type   string `json:"type"`
	In     string `json:"in"`
	Name   string `json:"name"`
	Scheme string `json:"scheme"`
}

type operationJSON struct {
	OperationID string                  `json:"operationId"`
	Parameters  []parameterJSON         `json:"parameters"`
	RequestBody *requestBodyJSON        `json:"requestBody"`
	Responses   map[string]responseJSON `json:"responses"`
	Security    []map[string][]string   `json:"security"`
}

type parameterJSON struct {
	Name     string         `json:"name"`
	In       string         `json:"in"`
	Required bool           `json:"required"`
	Schema   map[string]any `json:"schema"`
}

type requestBodyJSON struct {
	Required bool                 `json:"required"`
	Content  map[string]mediaJSON `json:"content"`
}

type responseJSON struct {
	Content map[string]mediaJSON  `json:"content"`
	Headers map[string]headerJSON `json:"headers"`
}

type headerJSON struct {
	Required bool           `json:"required"`
	Schema   map[string]any `json:"schema"`
}

type mediaJSON struct {
	Schema map[string]any `json:"schema"`
}

func decodeDocument(t *testing.T, document openapi.Document) documentJSON {
	t.Helper()
	var decoded documentJSON
	decoder := json.NewDecoder(bytes.NewReader(document.Bytes()))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func findSecurityScheme(t *testing.T, document documentJSON, kind, location, name string) string {
	t.Helper()
	for key, scheme := range document.Components.SecuritySchemes {
		if scheme.Type == kind && scheme.In == location && scheme.Name == name {
			return key
		}
	}
	t.Fatalf("no security scheme for %q %q %q: %#v", kind, location, name, document.Components.SecuritySchemes)
	return ""
}

func requireSecurity(t *testing.T, operation operationJSON, schemes ...string) {
	t.Helper()
	if operation.OperationID == "" {
		t.Fatal("operation missing from document")
	}
	want := make(map[string][]string, len(schemes))
	for _, scheme := range schemes {
		want[scheme] = []string{}
	}
	if len(operation.Security) != 1 || !reflect.DeepEqual(operation.Security[0], want) {
		t.Fatalf("%s security = %#v, want one AND requirement %#v", operation.OperationID, operation.Security, want)
	}
}

func responseHeader(response responseJSON, name string) (headerJSON, bool) {
	for candidate, header := range response.Headers {
		if strings.EqualFold(candidate, name) {
			return header, true
		}
	}
	return headerJSON{}, false
}
