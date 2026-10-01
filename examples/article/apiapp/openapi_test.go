package apiapp_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/examples/article/apiapp"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

func TestArticleOpenAPIDescribesPublishedRoutesAndModelContracts(t *testing.T) {
	harness := newHarness(t)
	document, err := harness.adapter.OpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	var decoded articleDocument
	if err := json.Unmarshal(document.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(decoded.OpenAPI, "3.1.") || len(decoded.Paths) != 2 {
		t.Fatalf("document version/paths = %q/%d", decoded.OpenAPI, len(decoded.Paths))
	}
	ids := make(map[string]bool)
	for _, route := range harness.adapter.Routes() {
		path := strings.ReplaceAll(route.Path, "<int64:id>", "{id}")
		operation, ok := decoded.Paths[path][strings.ToLower(route.Method)]
		if !ok || operation.OperationID == "" || ids[operation.OperationID] {
			t.Fatalf("published route %s %s has no unique documented operation", route.Method, path)
		}
		ids[operation.OperationID] = true
		if route.Method == http.MethodHead {
			for status, response := range operation.Responses {
				if len(response.Content) != 0 {
					t.Fatalf("HEAD response %s declares body content", status)
				}
			}
		}
	}
	if len(ids) != 10 || len(document.Routes()) != len(ids) {
		t.Fatalf("published/documented route counts differ: %d/%d", len(document.Routes()), len(ids))
	}
	list := decoded.Paths[apiapp.ListPath]
	detail := decoded.Paths["/api/articles/{id}/"]
	for _, operation := range []articleDocumentOperation{list["post"], detail["put"]} {
		if operation.RequestBody == nil || !operation.RequestBody.Required {
			t.Fatal("full write does not require a request body")
		}
		schema := decoded.resolve(t, operation.RequestBody.Content[api.JSONContentType].Schema)
		if !slices.Equal(schema.Required, []string{"title"}) || string(schema.Properties["published"].Default) != "false" {
			t.Fatalf("full input required/default = %v/%s", schema.Required, schema.Properties["published"].Default)
		}
		if _, present := schema.Properties["id"]; present {
			t.Fatal("generated identifier is documented as an input field")
		}
		if !schema.Properties["summary"].allowsType("null") {
			t.Fatal("summary null input is absent from the schema")
		}
	}
	patch := decoded.resolve(t, detail["patch"].RequestBody.Content[api.JSONContentType].Schema)
	if len(patch.Required) != 0 || len(patch.Properties["published"].Default) != 0 {
		t.Fatal("PATCH documentation requires fields or applies a missing published default")
	}
	if _, ok := detail["delete"].Responses["204"]; !ok || len(detail["delete"].Responses["204"].Content) != 0 {
		t.Fatal("DELETE does not document its empty 204 response")
	}
	if !documentHeader(list["post"].Responses["201"], "Location").Required || !documentHeader(detail["options"].Responses["200"], "Allow").Required {
		t.Fatal("Location or Allow response header is missing")
	}
	if _, ok := list["get"].Responses["500"].Content["text/plain"]; !ok {
		t.Fatal("internal failures must retain the existing plain-text representation")
	}
	for _, name := range []string{"search", "published", "ordering", "page"} {
		parameter := documentParameter(list["get"], name)
		if parameter.In != "query" {
			t.Fatalf("query parameter %q is missing", name)
		}
		if want := name == "search" || name == "page"; parameter.AllowEmptyValue != want {
			t.Fatalf("query parameter %q allowEmptyValue = %t, want %t", name, parameter.AllowEmptyValue, want)
		}
	}
	if !slices.Equal(documentParameter(list["get"], "published").Schema.Enum, []string{"true", "false"}) {
		t.Fatal("published query no longer describes its exact accepted strings")
	}
	if description := documentParameter(list["get"], "search").Description; !strings.Contains(description, "64 UTF-8 bytes") {
		t.Fatal("search byte limit must not be described as a character count")
	}
	var cookieScheme, csrfCookieScheme, csrfHeaderScheme string
	for name, scheme := range decoded.Components.SecuritySchemes {
		if scheme.Type == "apiKey" && scheme.In == "cookie" && scheme.Name == websessionauth.DefaultSessionCookieName {
			cookieScheme = name
		}
		if scheme.Type == "apiKey" && scheme.In == "cookie" && scheme.Name == websessionauth.DefaultCSRFCookieName {
			csrfCookieScheme = name
		}
		if scheme.Type == "apiKey" && scheme.In == "header" && strings.EqualFold(scheme.Name, websessionauth.DefaultCSRFHeader) {
			csrfHeaderScheme = name
		}
	}
	if cookieScheme == "" {
		t.Fatal("document does not describe the actual session cookie")
	}
	for _, path := range decoded.Paths {
		for _, operation := range path {
			if len(operation.Security) != 1 {
				t.Fatal("operation does not select exactly one authentication profile")
			}
			if _, ok := operation.Security[0][cookieScheme]; !ok {
				t.Fatal("operation security does not use the session profile")
			}
		}
	}
	for _, scheme := range []string{csrfCookieScheme, csrfHeaderScheme} {
		if _, required := list["post"].Security[0][scheme]; scheme == "" || !required {
			t.Fatal("session write does not require both the CSRF cookie and header")
		}
		if _, required := list["get"].Security[0][scheme]; required {
			t.Fatal("safe session request requires CSRF transport")
		}
	}
}

func TestArticleOpenAPIResponseContractMatchesHTTPCreatePatchAndHEAD(t *testing.T) {
	harness := newHarness(t)
	document, err := harness.adapter.OpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	var decoded articleDocument
	if err := json.Unmarshal(document.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(harness.application)
	t.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = 10 * time.Second
	exchange := func(t *testing.T, method, target string, options requestOptions) responseResult {
		t.Helper()
		request, err := http.NewRequestWithContext(t.Context(), method, server.URL+target, strings.NewReader(options.body))
		if err != nil {
			t.Fatal(err)
		}
		accept := options.accept
		if accept == "" {
			accept = api.JSONContentType
		}
		request.Header.Set("Accept", accept)
		if options.contentType != "" {
			request.Header.Set("Content-Type", options.contentType)
		}
		for _, cookie := range []*http.Cookie{options.session, options.csrf} {
			if cookie != nil {
				request.AddCookie(cookie)
			}
		}
		if options.token != "" {
			request.Header.Set(websessionauth.DefaultCSRFHeader, options.token)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return responseResult{status: response.StatusCode, header: response.Header.Clone(), body: string(body), cookies: response.Cookies()}
	}
	initial := exchange(t, http.MethodGet, apiapp.ListPath, requestOptions{session: harness.allSession})
	token := initial.header.Get(websessionauth.DefaultCSRFHeader)
	var csrf *http.Cookie
	for _, cookie := range initial.cookies {
		if cookie.Name == websessionauth.DefaultCSRFCookieName {
			csrf = cookie
		}
	}
	if initial.status != http.StatusOK || token == "" || csrf == nil {
		t.Fatal("HTTP client could not obtain the documented session CSRF transport")
	}
	for _, name := range []string{"search", "page"} {
		response := exchange(t, http.MethodGet, apiapp.ListPath+"?"+name+"=", requestOptions{session: harness.allSession})
		if response.status != http.StatusOK || response.header.Get("Content-Type") != api.JSONContentType ||
			!documentParameter(decoded.Paths[apiapp.ListPath]["get"], name).AllowEmptyValue {
			t.Fatalf("empty query parameter %q differs between the document and HTTP response", name)
		}
	}
	created := exchange(t, http.MethodPost, apiapp.ListPath, requestOptions{
		body: `{"title":"Document consumer"}`, contentType: api.JSONContentType,
		session: harness.allSession, token: token, csrf: csrf,
	})
	if created.status != http.StatusCreated {
		t.Fatalf("create status = %d", created.status)
	}
	createResponse := decoded.Paths[apiapp.ListPath]["post"].Responses["201"]
	assertDocumentedArticle(t, decoded.resolve(t, createResponse.Content[created.header.Get("Content-Type")].Schema), created.body)
	if !bytes.Contains([]byte(created.body), []byte(`"published":false`)) || !bytes.Contains([]byte(created.body), []byte(`"summary":null`)) {
		t.Fatal("actual omitted values differ from the documented full-input behavior")
	}
	location := created.header.Get("Location")
	if location == "" {
		t.Fatal("created Article did not supply its documented Location")
	}
	for _, test := range []struct {
		name    string
		path    string
		status  int
		options requestOptions
	}{
		{name: "existing", path: location, status: http.StatusOK, options: requestOptions{session: harness.allSession}},
		{name: "missing", path: "/api/articles/9223372036854775807/", status: http.StatusNotFound, options: requestOptions{session: harness.allSession}},
		{name: "anonymous", path: location, status: http.StatusForbidden},
		{name: "unacceptable", path: location, status: http.StatusNotAcceptable, options: requestOptions{session: harness.allSession, accept: "text/html"}},
	} {
		t.Run("HEAD "+test.name, func(t *testing.T) {
			response := exchange(t, http.MethodHead, test.path, test.options)
			if response.status != test.status || response.body != "" || response.header.Get("Content-Type") != api.JSONContentType {
				t.Fatalf("HEAD HTTP response = status %d, body bytes %d, content type %q", response.status, len(response.body), response.header.Get("Content-Type"))
			}
			declared, ok := decoded.Paths["/api/articles/{id}/"]["head"].Responses[strconv.Itoa(test.status)]
			if !ok || len(declared.Content) != 0 {
				t.Fatal("actual empty HEAD response is absent from the document")
			}
		})
	}
	patched := exchange(t, http.MethodPatch, location, requestOptions{
		body: `{"summary":""}`, contentType: api.JSONContentType,
		session: harness.allSession, token: token, csrf: csrf,
	})
	if patched.status != http.StatusOK {
		t.Fatalf("patch status = %d", patched.status)
	}
	patchResponse := decoded.Paths["/api/articles/{id}/"]["patch"].Responses["200"]
	assertDocumentedArticle(t, decoded.resolve(t, patchResponse.Content[patched.header.Get("Content-Type")].Schema), patched.body)
	if !bytes.Contains([]byte(patched.body), []byte(`"summary":""`)) {
		t.Fatal("an accepted empty summary was not preserved in the documented response")
	}
}

func TestArticleOpenAPIRequiresAnExplicitAuthenticationDescription(t *testing.T) {
	harness := newHarness(t)
	custom := &recordingAuthentication{}
	application, err := apiapp.New(harness.backend, custom)
	if err != nil || application == nil || len(application.Routes()) != 10 {
		t.Fatalf("custom authentication cannot serve existing routes: %v", err)
	}
	if _, err := application.OpenAPI(); err == nil {
		t.Fatal("OpenAPI guessed a profile for an undescribed authentication adapter")
	}
	if len(custom.calls) != 10 {
		t.Fatal("document construction rebuilt authenticated handlers")
	}
	var absent *apiapp.Application
	if _, err := absent.OpenAPI(); err == nil {
		t.Fatal("nil Article adapter produced a document")
	}
}

type articleDocument struct {
	OpenAPI    string
	Paths      map[string]map[string]articleDocumentOperation
	Components struct {
		SecuritySchemes map[string]struct{ Type, In, Name string }
		Schemas         map[string]articleDocumentSchema
	}
}

func (document articleDocument) resolve(t *testing.T, schema articleDocumentSchema) articleDocumentSchema {
	t.Helper()
	if schema.Ref == "" {
		return schema
	}
	name, ok := strings.CutPrefix(schema.Ref, "#/components/schemas/")
	if !ok {
		t.Fatalf("schema has unsupported reference %q", schema.Ref)
	}
	resolved, found := document.Components.Schemas[name]
	if !found || resolved.Ref != "" {
		t.Fatalf("model schema %q is not a concrete definition", name)
	}
	return resolved
}

type articleDocumentOperation struct {
	OperationID string
	Parameters  []articleDocumentParameter
	Security    []map[string][]string
	RequestBody *struct {
		Required bool
		Content  map[string]articleDocumentMedia
	}
	Responses map[string]articleDocumentResponse
}

type articleDocumentParameter struct {
	Name, In, Description string
	Required              bool
	AllowEmptyValue       bool
	Schema                articleDocumentSchema
}

type articleDocumentResponse struct {
	Content map[string]articleDocumentMedia
	Headers map[string]struct {
		Required bool
	}
}

type articleDocumentMedia struct{ Schema articleDocumentSchema }

type articleDocumentSchema struct {
	Ref        string `json:"$ref"`
	Type       json.RawMessage
	Properties map[string]articleDocumentSchema
	Required   []string
	Default    json.RawMessage
	Enum       []string
	AnyOf      []articleDocumentSchema
}

func (schema articleDocumentSchema) allowsType(want string) bool {
	var single string
	if json.Unmarshal(schema.Type, &single) == nil {
		return single == want
	}
	var multiple []string
	if json.Unmarshal(schema.Type, &multiple) == nil && slices.Contains(multiple, want) {
		return true
	}
	for _, choice := range schema.AnyOf {
		if choice.allowsType(want) {
			return true
		}
	}
	return false
}

func documentParameter(operation articleDocumentOperation, name string) articleDocumentParameter {
	for _, parameter := range operation.Parameters {
		if strings.EqualFold(parameter.Name, name) {
			return parameter
		}
	}
	return articleDocumentParameter{}
}

func documentHeader(response articleDocumentResponse, name string) struct{ Required bool } {
	for key, header := range response.Headers {
		if strings.EqualFold(key, name) {
			return header
		}
	}
	return struct{ Required bool }{}
}

func assertDocumentedArticle(t *testing.T, schema articleDocumentSchema, body string) {
	t.Helper()
	var actual map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &actual); err != nil {
		t.Fatal(err)
	}
	if !schema.allowsType("object") || len(schema.Properties) != len(actual) {
		t.Fatal("actual Article fields differ from the documented response")
	}
	for name, value := range actual {
		property, exists := schema.Properties[name]
		if !exists || !slices.Contains(schema.Required, name) {
			t.Fatalf("returned Article field %q is not a required response field", name)
		}
		kind := "integer"
		switch {
		case bytes.Equal(value, []byte("null")):
			kind = "null"
		case bytes.Equal(value, []byte("true")), bytes.Equal(value, []byte("false")):
			kind = "boolean"
		case len(value) > 0 && value[0] == '"':
			kind = "string"
		}
		if !property.allowsType(kind) {
			t.Fatalf("Article field %q returned undocumented type %q", name, kind)
		}
	}
}
