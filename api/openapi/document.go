// Package openapi describes GoDj's bounded JSON APIs from their actual route
// declarations and serializer projections. It does not parse requests, grant
// permissions, perform I/O, or replace application validation.
package openapi

import (
	"encoding/json"
	"math"
	"mime"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/web"
)

// Config describes one API protected by one actual authentication profile.
// Route handlers must already be wrapped by that Authentication. New never
// invokes them, performs authentication, or constructs a second wrapper.
type Config struct {
	Title          string
	Version        string
	Operations     []Operation
	Authentication api.Authentication `json:"-"`
}

// Operation associates documentation with the same route used for execution.
// Permission describes the permission used to wrap Route.Handler.
type Operation struct {
	Route       web.Route `json:"-"`
	Summary     string
	Description string
	Permission  auth.Permission
	Parameters  []Parameter
	RequestBody *RequestBody
	Responses   []Response
}

// Parameter describes a query or header parameter. Path parameters are derived
// from the Web route compiler and must not be redeclared here.
type Parameter struct {
	Name            string
	In              string
	Description     string
	Required        bool
	AllowEmptyValue bool
	Schema          Schema
}

type RequestBody struct {
	Schema      Schema
	Description string
	Required    bool
}

type Response struct {
	Status      int
	Description string
	ContentType string
	Schema      Schema
	Headers     []Header
}

type Header struct {
	Name        string
	Description string
	Schema      Schema
	Required    bool
}

// Document is an immutable startup snapshot. The zero value is invalid.
type Document struct {
	encoded []byte
	routes  []web.Route
}

// Bytes returns a detached UTF-8 OpenAPI 3.1.1 JSON document.
func (d Document) Bytes() []byte { return append([]byte(nil), d.encoded...) }

// Routes returns detached route declarations. It does not add a schema route;
// the application chooses where and with what permission to publish Response.
func (d Document) Routes() []web.Route { return append([]web.Route(nil), d.routes...) }

func (d Document) Response() (web.Response, error) {
	if len(d.encoded) == 0 {
		return web.Response{}, documentError("document", "document is zero or invalid")
	}
	return web.NewResponse(http.StatusOK, http.Header{"Content-Type": {api.JSONContentType}}, d.encoded)
}

// New validates the complete declaration before publishing routes or bytes.
// It deliberately refuses undocumented authentication rather than guessing
// cookie names, advertising extra profiles, or accidentally documenting public access.
func New(config Config) (Document, error) {
	if !validText(config.Title, 256, true) || !validText(config.Version, 128, true) {
		return Document{}, documentError("info", "title and version must be bounded non-empty text")
	}
	if len(config.Operations) == 0 || len(config.Operations) > 1024 {
		return Document{}, documentError("operations", "operation count is outside the supported range")
	}
	routes := make([]web.Route, len(config.Operations))
	for index, operation := range config.Operations {
		routes[index] = operation.Route
	}
	if err := web.ValidateRouteDeclarations(routes); err != nil {
		return Document{}, &api.Error{Code: api.FailureInvalidConfig, Field: "operations", Detail: "route declarations are invalid", Cause: err}
	}
	description, err := describeAuthentication(config.Authentication)
	if err != nil {
		return Document{}, err
	}
	errorSchema, err := JSONErrorSchema()
	if err != nil {
		return Document{}, err
	}
	paths := make(map[string]map[string]any)
	ids := make(map[string]bool)
	shapes := make(map[string]string)
	for _, operation := range config.Operations {
		route := operation.Route
		if route.Handler == nil || !validText(route.Name, 256, true) || !supportedMethod(route.Method) {
			return Document{}, documentError("operation.route", "route needs a handler, a name, and a supported uppercase HTTP method")
		}
		if ids[route.Name] {
			return Document{}, documentError("operation.id", "operation ID is duplicated")
		}
		ids[route.Name] = true
		path, err := web.DescribeRoutePath(route.Path)
		if err != nil {
			return Document{}, &api.Error{Code: api.FailureInvalidConfig, Field: "operation.path", Detail: "route path is invalid", Cause: err}
		}
		shape := path.Template
		for _, name := range path.Parameters {
			shape = strings.ReplaceAll(shape, "{"+name+"}", "{}")
		}
		if previous, found := shapes[shape]; found && previous != path.Template {
			return Document{}, documentError("operation.path", "equivalent path templates use different parameter names")
		}
		shapes[shape] = path.Template
		method := strings.ToLower(route.Method)
		if paths[path.Template] == nil {
			paths[path.Template] = make(map[string]any)
		}
		if _, duplicate := paths[path.Template][method]; duplicate {
			return Document{}, documentError("operation.path", "method and path are duplicated")
		}
		value, err := operationValue(operation, path, description, errorSchema)
		if err != nil {
			return Document{}, err
		}
		paths[path.Template][method] = value
	}
	value := map[string]any{
		"openapi":    "3.1.1",
		"info":       map[string]string{"title": config.Title, "version": config.Version},
		"paths":      paths,
		"components": map[string]any{"securitySchemes": securitySchemes(description)},
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return Document{}, &api.Error{Code: api.FailureInvalidConfig, Field: "document", Detail: "document encoding failed", Cause: err}
	}
	if len(encoded) > int(web.DefaultMaxResponseBytes) {
		return Document{}, documentError("document", "document exceeds the default Web response limit")
	}
	return Document{encoded: encoded, routes: routes}, nil
}

func operationValue(operation Operation, path web.RoutePathDescription, profile api.AuthenticationDescription, errorSchema Schema) (map[string]any, error) {
	if !validText(operation.Summary, 256, false) || !validText(operation.Description, 8192, false) {
		return nil, documentError("operation.description", "operation text is invalid or too long")
	}
	permission, err := auth.NewPermission(string(operation.Permission))
	if err != nil || permission != operation.Permission {
		return nil, documentError("operation.permission", "one explicit permission is required")
	}
	if len(operation.Parameters) > 128 || len(operation.Responses) == 0 || len(operation.Responses) > 32 {
		return nil, documentError("operation", "parameter or response count is outside the supported range")
	}
	parameters := make([]any, 0, len(path.Parameters)+len(operation.Parameters))
	for _, name := range path.Parameters {
		parameters = append(parameters, map[string]any{
			"name": name, "in": "path", "required": true,
			"schema":      map[string]any{"type": "integer", "format": "int64", "minimum": 0, "maximum": int64(math.MaxInt64)},
			"description": "A canonical non-negative int64 decimal path segment. Invalid spellings are not routed.",
		})
	}
	seen := make(map[string]bool)
	for _, parameter := range operation.Parameters {
		if parameter.In != "query" && parameter.In != "header" || !validToken(parameter.Name) || !validText(parameter.Description, 4096, false) {
			return nil, documentError("operation.parameter", "only named query/header parameters with bounded descriptions are supported")
		}
		if parameter.AllowEmptyValue && parameter.In != "query" {
			return nil, documentError("operation.parameter", "empty values may only be enabled for query parameters")
		}
		key := parameter.In + ":" + parameter.Name
		if parameter.In == "header" {
			key = strings.ToLower(key)
			if reservedParameterHeader(parameter.Name) || strings.EqualFold(parameter.Name, profile.CSRFHeader) {
				return nil, documentError("operation.parameter", "authentication and representation headers are owned by the profile")
			}
		}
		if seen[key] {
			return nil, documentError("operation.parameter", "parameter is duplicated")
		}
		seen[key] = true
		schema, err := rawSchema(parameter.Schema)
		if err != nil {
			return nil, err
		}
		value := map[string]any{
			"name": parameter.Name, "in": parameter.In, "description": parameter.Description,
			"required": parameter.Required, "schema": schema,
		}
		if parameter.AllowEmptyValue {
			value["allowEmptyValue"] = true
		}
		parameters = append(parameters, value)
	}
	responses := make(map[string]any)
	for _, response := range operation.Responses {
		for _, header := range response.Headers {
			if profile.Kind == api.AuthenticationSession && strings.EqualFold(header.Name, profile.CSRFHeader) ||
				profile.Kind == api.AuthenticationBearer && strings.EqualFold(header.Name, "WWW-Authenticate") {
				return nil, documentError("operation.response.headers", "authentication response headers are owned by the profile")
			}
		}
		key := strconv.Itoa(response.Status)
		if _, duplicate := responses[key]; duplicate {
			return nil, documentError("operation.response", "response status is duplicated")
		}
		value, err := responseValue(response)
		if err != nil {
			return nil, err
		}
		responses[key] = value
	}
	for _, status := range failureStatuses(profile) {
		key := strconv.Itoa(status)
		if existing, found := responses[key]; found {
			// A body validation 400 and a Bearer syntax 400 share the same wire
			// envelope. Refuse a declaration that would hide that difference.
			if !sameJSONErrorContent(existing.(map[string]any), errorSchema) {
				return nil, documentError("operation.response", "profile failure status must use the API JSON error envelope")
			}
		} else {
			value, err := responseValue(Response{Status: status, Description: http.StatusText(status), ContentType: api.JSONContentType, Schema: errorSchema})
			if err != nil {
				return nil, err
			}
			responses[key] = value
		}
		if profile.Kind == api.AuthenticationBearer && status != 406 {
			addResponseHeader(responses[key].(map[string]any), "WWW-Authenticate", "Bearer authentication challenge; never contains credential material.")
		}
	}
	if _, found := responses["500"]; found {
		return nil, documentError("operation.response", "the Web runtime owns the internal-error response")
	}
	internal, err := responseValue(Response{Status: 500, Description: "Internal server error. Internal causes are not exposed.", ContentType: "text/plain", Schema: String()})
	if err != nil {
		return nil, err
	}
	responses["500"] = internal
	security := map[string][]string{}
	if profile.Kind == api.AuthenticationBearer {
		security["bearerAuth"] = []string{}
	} else {
		security["sessionAuth"] = []string{}
		if !safeMethod(operation.Route.Method) {
			security["csrfCookie"] = []string{}
			security["csrfHeader"] = []string{}
		} else {
			for _, response := range operation.Responses {
				// Optional because routing/negotiation failures can share a status
				// while occurring before authentication or the application handler.
				addResponseHeader(responses[strconv.Itoa(response.Status)].(map[string]any), profile.CSRFHeader, "Fresh masked CSRF token on authenticated safe handler responses; reuse with the HttpOnly CSRF cookie for unsafe methods.")
			}
		}
	}
	if operation.Route.Method == http.MethodHead {
		for _, response := range responses {
			delete(response.(map[string]any), "content")
		}
	}
	value := map[string]any{
		"operationId": operation.Route.Name, "summary": operation.Summary, "description": operation.Description,
		"parameters": parameters, "responses": responses, "security": []any{security},
		"x-godj-permission": string(operation.Permission),
	}
	if body := operation.RequestBody; body != nil {
		if operation.Route.Method != http.MethodPost && operation.Route.Method != http.MethodPut && operation.Route.Method != http.MethodPatch {
			return nil, documentError("operation.request_body", "JSON bodies are supported on POST, PUT and PATCH only")
		}
		if !validText(body.Description, 4096, false) {
			return nil, documentError("operation.request_body", "body description is invalid or too long")
		}
		schema, err := rawSchema(body.Schema)
		if err != nil {
			return nil, err
		}
		value["requestBody"] = map[string]any{
			"required": body.Required, "description": body.Description,
			"content": map[string]any{api.JSONContentType: map[string]any{"schema": schema}},
		}
	}
	return value, nil
}

func responseValue(response Response) (map[string]any, error) {
	if response.Status < 200 || response.Status > 599 || !validText(response.Description, 4096, true) || len(response.Headers) > 32 {
		return nil, documentError("operation.response", "response status, description, or header count is invalid")
	}
	value := map[string]any{"description": response.Description}
	if response.ContentType == "" {
		if response.Schema.Value().Kind() != 0 {
			return nil, documentError("operation.response", "a response schema requires a media type")
		}
	} else {
		mediaType, _, err := mime.ParseMediaType(response.ContentType)
		if err != nil || mediaType != response.ContentType || !strings.Contains(mediaType, "/") || strings.Contains(mediaType, "*") || response.Status == 204 || response.Status == 304 {
			return nil, documentError("operation.response", "response media type is invalid or the status forbids a body")
		}
		schema, err := rawSchema(response.Schema)
		if err != nil {
			return nil, err
		}
		value["content"] = map[string]any{mediaType: map[string]any{"schema": schema}}
	}
	headers := make(map[string]any)
	seen := make(map[string]bool)
	for _, header := range response.Headers {
		key := strings.ToLower(header.Name)
		if !validToken(header.Name) || key == "content-type" || seen[key] || !validText(header.Description, 4096, false) {
			return nil, documentError("operation.response.headers", "response header is invalid, reserved, or duplicated")
		}
		seen[key] = true
		schema, err := rawSchema(header.Schema)
		if err != nil {
			return nil, err
		}
		headers[header.Name] = map[string]any{"schema": schema, "description": header.Description, "required": header.Required}
	}
	if len(headers) > 0 {
		value["headers"] = headers
	}
	return value, nil
}

func rawSchema(schema Schema) (json.RawMessage, error) {
	if _, valid := schema.Value().AsObject(); !valid {
		return nil, documentError("schema", "schema is zero or invalid")
	}
	encoded, err := serializers.Encode(schema.Value(), serializers.Limits{})
	if err != nil {
		return nil, &api.Error{Code: api.FailureInvalidConfig, Field: "schema", Detail: "schema cannot be encoded", Cause: err}
	}
	return encoded, nil
}

func sameJSONErrorContent(response map[string]any, schema Schema) bool {
	want, err := rawSchema(schema)
	if err != nil {
		return false
	}
	content, ok := response["content"].(map[string]any)
	if !ok || len(content) != 1 {
		return false
	}
	media, ok := content[api.JSONContentType].(map[string]any)
	if !ok {
		return false
	}
	actual, ok := media["schema"].(json.RawMessage)
	return ok && string(actual) == string(want)
}

func addResponseHeader(response map[string]any, name, description string) {
	headers, _ := response["headers"].(map[string]any)
	if headers == nil {
		headers = make(map[string]any)
		response["headers"] = headers
	}
	headers[name] = map[string]any{"description": description, "schema": map[string]string{"type": "string"}}
}

func describeAuthentication(authentication api.Authentication) (api.AuthenticationDescription, error) {
	if authentication == nil {
		return api.AuthenticationDescription{}, documentError("authentication", "authentication is required")
	}
	value := reflect.ValueOf(authentication)
	switch value.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Func, reflect.Slice, reflect.Chan, reflect.Interface:
		if value.IsNil() {
			return api.AuthenticationDescription{}, documentError("authentication", "authentication is nil")
		}
	}
	describer, ok := authentication.(api.AuthenticationDescriber)
	if !ok {
		return api.AuthenticationDescription{}, documentError("authentication", "authentication does not describe its accepted transport")
	}
	description, err := describer.DescribeAuthentication()
	if err != nil {
		return api.AuthenticationDescription{}, &api.Error{Code: api.FailureInvalidConfig, Field: "authentication", Detail: "authentication description is unavailable", Cause: err}
	}
	switch description.Kind {
	case api.AuthenticationSession:
		if !validToken(description.SessionCookieName) || !validToken(description.CSRFCookieName) || description.SessionCookieName == description.CSRFCookieName ||
			!validToken(description.CSRFHeader) || reservedParameterHeader(description.CSRFHeader) {
			return api.AuthenticationDescription{}, documentError("authentication", "session profile needs distinct cookie names and an explicit CSRF header")
		}
	case api.AuthenticationBearer:
		if description.SessionCookieName != "" || description.CSRFCookieName != "" || description.CSRFHeader != "" {
			return api.AuthenticationDescription{}, documentError("authentication", "Bearer profile cannot declare cookie or CSRF transport")
		}
	default:
		return api.AuthenticationDescription{}, documentError("authentication", "authentication profile is unsupported")
	}
	return description, nil
}

func securitySchemes(profile api.AuthenticationDescription) map[string]any {
	if profile.Kind == api.AuthenticationBearer {
		return map[string]any{"bearerAuth": map[string]string{"type": "http", "scheme": "bearer"}}
	}
	return map[string]any{
		"sessionAuth": map[string]string{"type": "apiKey", "in": "cookie", "name": profile.SessionCookieName},
		"csrfCookie":  map[string]string{"type": "apiKey", "in": "cookie", "name": profile.CSRFCookieName, "description": "HttpOnly CSRF cookie paired with the masked request token."},
		"csrfHeader":  map[string]string{"type": "apiKey", "in": "header", "name": profile.CSRFHeader, "description": "Masked token obtained from an authenticated safe response."},
	}
}

func failureStatuses(profile api.AuthenticationDescription) []int {
	if profile.Kind == api.AuthenticationBearer {
		return []int{400, 401, 403, 406}
	}
	return []int{403, 406}
}

func safeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions || method == http.MethodTrace
}

func supportedMethod(method string) bool {
	switch method {
	case "GET", "HEAD", "OPTIONS", "POST", "PUT", "PATCH", "DELETE", "TRACE":
		return true
	default:
		return false
	}
}

func validText(value string, limit int, required bool) bool {
	if required && strings.TrimSpace(value) == "" || len(value) > limit || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r < 0x20 && r != '\n' && r != '\t' || r == 0x7f {
			return false
		}
	}
	return true
}

func validToken(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if r <= 0x20 || r >= 0x7f || strings.ContainsRune("()<>@,;:\\\"/[]?={}", r) {
			return false
		}
	}
	return true
}

func reservedParameterHeader(name string) bool {
	switch strings.ToLower(name) {
	case "authorization", "cookie", "content-type", "accept":
		return true
	default:
		return false
	}
}

func documentError(field, detail string) error {
	return &api.Error{Code: api.FailureInvalidConfig, Field: field, Detail: detail}
}
