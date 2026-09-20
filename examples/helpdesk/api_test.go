package helpdesk_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/web"
)

func TestHelpdeskAPICompositionAndNamedContractsWithoutIO(t *testing.T) {
	application := helpdeskConstructionApplication(t)
	recording := &helpdeskRecordingAuthentication{}
	adapter, err := application.API(helpdeskDescribedAuthentication{recording})
	if err != nil {
		t.Fatal(err)
	}
	document, err := adapter.OpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	decoded := decodeHelpdeskDocument(t, document.Bytes())
	assertHelpdeskOperationContracts(t, decoded, adapter.Routes())
	if !slices.Equal(recording.permissions, []auth.Permission{helpdesk.ViewTicket, helpdesk.ViewTicket, helpdesk.AddTicket, helpdesk.ChangeTicket, helpdesk.ChangeTicket}) {
		t.Fatalf("authentication composition = %v", recording.permissions)
	}
	if _, found := decoded.Components.Schemas[openapi.ErrorSchemaName]; !found {
		t.Fatal("the shared API error component is absent")
	}
	ticket := decoded.Components.Schemas["Ticket"]
	if !slices.Equal(ticket.Required, []string{"id", "subject", "details", "closed", "category", "priority", "resolution", "due_at", "reviewed", "service_on", "service_at", "elapsed", "effort", "expected_cost"}) || len(ticket.Properties) != 14 || ticket.AdditionalProperties {
		t.Fatalf("Ticket fields = %+v", ticket)
	}
	if ticket.Properties["subject"].MaxLength != 120 || !ticket.Properties["category"].ReadOnly || !ticket.Properties["details"].allowsType("null") {
		t.Fatal("Ticket response lost its encoder's field constraints")
	}
	input := decoded.Components.Schemas["TicketCreate"]
	update := decoded.Components.Schemas["TicketUpdate"]
	patch := decoded.Components.Schemas["TicketPatch"]
	for _, field := range []helpdeskDocumentSchema{ticket.Properties["expected_cost"], input.Properties["expected_cost"], update.Properties["expected_cost"], patch.Properties["expected_cost"]} {
		if len(field.AnyOf) != 2 || field.AnyOf[0].Type != "string" || field.AnyOf[0].MinLength != 4 || field.AnyOf[0].MaxLength != 14 || field.AnyOf[0].Pattern != `^-?(0|[1-9][0-9]{0,9})\.[0-9]{2}$` || !field.allowsType("null") {
			t.Fatal("Decimal OpenAPI precision/scale/nullable domain changed")
		}
	}
	for _, field := range []helpdeskDocumentSchema{ticket.Properties["effort"], input.Properties["effort"], update.Properties["effort"], patch.Properties["effort"]} {
		if len(field.AnyOf) != 2 || field.AnyOf[0].Type != "number" || field.AnyOf[0].Format != "double" || !field.allowsType("null") {
			t.Fatal("Float OpenAPI domain changed")
		}
		var min, max float64
		if json.Unmarshal(field.AnyOf[0].Minimum, &min) != nil || json.Unmarshal(field.AnyOf[0].Maximum, &max) != nil || min != -math.MaxFloat64 || max != math.MaxFloat64 {
			t.Fatal("Float finite bounds missing")
		}
	}
	for _, schema := range []helpdeskDocumentSchema{input, update, patch, ticket} {
		field, present := schema.Properties["reviewed"]
		if !present || !field.allowsType("boolean") || !field.allowsType("null") || len(field.Default) != 0 {
			t.Fatal("nullable Boolean schema invented false or lost null")
		}
	}
	if !slices.Equal(update.Required, []string{"subject"}) || string(update.Properties["closed"].Default) != "false" || len(patch.Required) != 0 || len(patch.Properties["closed"].Default) != 0 || len(patch.Properties) != 12 {
		t.Fatal("PUT/PATCH lost required/default/omission policy")
	}
	if len(input.Properties["priority"].AnyOf) != 2 || string(input.Properties["priority"].AnyOf[0].Enum) != "[1,0,-1]" || len(ticket.Properties["priority"].Enum) != 0 || len(ticket.Properties["priority"].AnyOf[0].Enum) != 0 {
		t.Fatal("choices input and existing-row output domains were conflated")
	}
	if !slices.Equal(input.Required, []string{"subject"}) || len(input.Properties) != 12 || input.AdditionalProperties || string(input.Properties["closed"].Default) != "false" || !input.Properties["details"].allowsType("null") || !input.Properties["priority"].allowsType("null") || !ticket.Properties["priority"].allowsType("null") {
		t.Fatalf("TicketCreate presence/defaults = %+v", input)
	}
	if !input.Properties["resolution"].allowsType("null") || !ticket.Properties["resolution"].allowsType("null") || ticket.Properties["resolution"].MaxLength != 0 || input.Properties["resolution"].MaxLength != 0 {
		t.Fatal("Text schema lost null or imposed a model length limit")
	}
	for _, field := range []helpdeskDocumentSchema{input.Properties["service_at"], ticket.Properties["service_at"], update.Properties["service_at"], patch.Properties["service_at"]} {
		if !field.allowsType("null") || len(field.AnyOf) != 2 || field.AnyOf[0].Type != "string" || field.AnyOf[0].Format != "" || field.AnyOf[0].Pattern != `^([01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9](\.[0-9]{6})?$` || field.AnyOf[0].MinLength != 8 || field.AnyOf[0].MaxLength != 15 {
			t.Fatal("clock schema conflates timezone format or loses its clock bounds")
		}
	}
	for _, field := range []helpdeskDocumentSchema{input.Properties["service_on"], ticket.Properties["service_on"], update.Properties["service_on"], patch.Properties["service_on"]} {
		if !field.allowsType("null") || len(field.AnyOf) != 2 || field.AnyOf[0].Type != "string" || field.AnyOf[0].Format != "date" {
			t.Fatal("calendar date wire format or presence missing")
		}
	}
	for _, field := range []helpdeskDocumentSchema{input.Properties["due_at"], ticket.Properties["due_at"]} {
		if !field.allowsType("null") || len(field.AnyOf) != 2 || field.AnyOf[0].Type != "string" || field.AnyOf[0].Format != "date-time" {
			t.Fatal("datetime null or format disappeared from OpenAPI")
		}
	}
	if _, found := input.Properties["id"]; found {
		t.Fatal("generated id became writable")
	}
	if _, found := input.Properties["category"]; found {
		t.Fatal("assigned category became writable")
	}
	if input.Properties["subject"].MaxLength != 0 || !bytes.Contains(input.Properties["subject"].Normalization, []byte(`"maxLengthAfterTrim":120`)) {
		t.Fatal("input normalization was replaced with a raw string bound")
	}
	category := decoded.Components.Schemas["CategorySummary"]
	if !slices.Equal(category.Required, []string{"id", "name"}) || len(category.Properties) != 2 || category.Properties["name"].Type != "string" || category.Properties["name"].MaxLength != 0 {
		t.Fatal("CategorySummary differs from the manually encoded category projection")
	}
	detail := decoded.Components.Schemas["TicketDetail"]
	if !slices.Equal(detail.Required, []string{"ticket", "category"}) || len(detail.Properties) != 2 || detail.Properties["ticket"].Ref != "#/components/schemas/Ticket" || detail.Properties["category"].Ref != "#/components/schemas/CategorySummary" {
		t.Fatal("TicketDetail does not reference both explicit projection identities")
	}
	routes := adapter.Routes()
	routes[0].Path, routes[0].Handler = "/changed/", nil
	detached := document.Routes()
	detached[0].Name = "changed"
	encoded := document.Bytes()
	encoded[0] = '!'
	again, err := adapter.OpenAPI()
	if err != nil || !bytes.Equal(again.Bytes(), document.Bytes()) || adapter.Routes()[0].Path != "/api/tickets/" || adapter.Routes()[0].Handler == nil || len(recording.permissions) != 5 {
		t.Fatalf("reading or mutating snapshots changed the API or repeated authentication: %v", err)
	}
}

func TestHelpdeskAPIConstructionRejectsIncompleteAuthentication(t *testing.T) {
	application := helpdeskConstructionApplication(t)
	var typedNil *helpdeskRecordingAuthentication
	for _, authentication := range []api.Authentication{nil, typedNil} {
		if result, err := application.API(authentication); result != nil || err == nil {
			t.Fatal("accepted nil authentication")
		}
	}
	if result, err := (*helpdesk.Application)(nil).API(&helpdeskRecordingAuthentication{}); result != nil || err == nil {
		t.Fatal("accepted nil application")
	}
	for index := 1; index <= 5; index++ {
		for _, recording := range []*helpdeskRecordingAuthentication{{failAt: index}, {nilAt: index}} {
			if result, err := application.API(recording); result != nil || err == nil || len(recording.permissions) != index {
				t.Fatalf("published a partial authentication composition at operation %d", index)
			}
		}
	}
	adapter, err := application.API(&helpdeskRecordingAuthentication{})
	if err != nil || len(adapter.Routes()) != 5 {
		t.Fatalf("custom authentication cannot serve routes: %v", err)
	}
	if _, err := adapter.OpenAPI(); err == nil {
		t.Fatal("undocumented custom authentication published a guessed profile")
	}
	if routes := (*helpdesk.API)(nil).Routes(); routes != nil {
		t.Fatal("nil API published routes")
	}
	if _, err := (*helpdesk.API)(nil).OpenAPI(); err == nil {
		t.Fatal("nil API published a document")
	}
}

func assertHelpdeskOperationContracts(t *testing.T, document helpdeskDocument, routes []web.Route) {
	t.Helper()
	if !strings.HasPrefix(document.OpenAPI, "3.1.") || len(document.Paths) != 2 || len(routes) != 5 || len(document.Paths["/api/tickets/"]) != 2 || len(document.Paths["/api/tickets/{id}/"]) != 3 {
		t.Fatal("Helpdesk document changed the five-operation surface")
	}
	for index, permission := range []auth.Permission{helpdesk.ViewTicket, helpdesk.AddTicket, helpdesk.ViewTicket, helpdesk.ChangeTicket, helpdesk.ChangeTicket} {
		route := routes[index]
		path := strings.ReplaceAll(route.Path, "<int64:id>", "{id}")
		operation := document.Paths[path][strings.ToLower(route.Method)]
		if operation.OperationID != route.Name || operation.Permission != string(permission) || route.Handler == nil || len(operation.Security) != 1 {
			t.Fatalf("route %s %s differs from its documented operation", route.Method, path)
		}
		if _, advertised := operation.Responses["406"]; advertised {
			t.Fatal("Helpdesk advertises Accept negotiation that it does not install")
		}
		if _, documented := operation.Responses["403"]; !documented {
			t.Fatal("authentication denial is undocumented")
		}
	}
	list := document.Paths["/api/tickets/"]["get"]
	create := document.Paths["/api/tickets/"]["post"]
	detail := document.Paths["/api/tickets/{id}/"]["get"]
	listSchema := list.Responses["200"].Content[api.JSONContentType].Schema
	if len(list.Parameters) != 0 || listSchema.Type != "array" || listSchema.Items == nil || listSchema.Items.Ref != "#/components/schemas/Ticket" {
		t.Fatal("list is not a bare array of Tickets without query parameters")
	}
	if create.RequestBody == nil || !create.RequestBody.Required || create.RequestBody.Content[api.JSONContentType].Schema.Ref != "#/components/schemas/TicketCreate" || create.Responses["201"].Content[api.JSONContentType].Schema.Ref != "#/components/schemas/Ticket" {
		t.Fatal("create input/output does not use the named Ticket contracts")
	}
	for name := range create.Responses["201"].Headers {
		if strings.EqualFold(name, "Location") {
			t.Fatal("create advertises a Location header it does not return")
		}
	}
	if detail.Responses["200"].Content[api.JSONContentType].Schema.Ref != "#/components/schemas/TicketDetail" || len(detail.Parameters) != 1 || detail.Parameters[0].Name != "id" || detail.Parameters[0].In != "path" {
		t.Fatal("detail does not document its relationship projection and path identifier")
	}
	for _, status := range []string{"400", "404", "413", "415"} {
		if _, documented := create.Responses[status]; !documented {
			t.Fatalf("create failure %s is undocumented", status)
		}
	}
	for method, schema := range map[string]string{"put": "TicketUpdate", "patch": "TicketPatch"} {
		operation := document.Paths["/api/tickets/{id}/"][method]
		if operation.RequestBody == nil || !operation.RequestBody.Required || operation.RequestBody.Content[api.JSONContentType].Schema.Ref != "#/components/schemas/"+schema || operation.Responses["200"].Content[api.JSONContentType].Schema.Ref != "#/components/schemas/Ticket" || len(operation.Security[0]) != 3 {
			t.Fatal("update request/response or CSRF contract differs")
		}
		for _, status := range []string{"400", "404", "413", "415"} {
			if _, exists := operation.Responses[status]; !exists {
				t.Fatal("update failure is undocumented")
			}
		}
	}
	var session, csrfCookie, csrfHeader string
	for name, scheme := range document.Components.SecuritySchemes {
		switch {
		case scheme.In == "header":
			csrfHeader = name
		case scheme.In == "cookie" && strings.Contains(strings.ToLower(scheme.Name), "csrf"):
			csrfCookie = name
		case scheme.In == "cookie":
			session = name
		}
	}
	if session == "" || csrfCookie == "" || csrfHeader == "" || len(list.Security[0]) != 1 || len(create.Security[0]) != 3 {
		t.Fatal("session read/write security does not preserve the CSRF transport boundary")
	}
	for _, scheme := range []string{session, csrfCookie, csrfHeader} {
		if _, required := create.Security[0][scheme]; !required {
			t.Fatalf("create does not require security scheme %q", scheme)
		}
	}
}

func assertHelpdeskResponseDocumented(t *testing.T, document helpdeskDocument, method, path string, response *httptest.ResponseRecorder) {
	t.Helper()
	status := http.StatusText(response.Code)
	declared, ok := document.Paths[path][strings.ToLower(method)].Responses[strconv.Itoa(response.Code)]
	if !ok {
		t.Fatalf("%s %s returned undocumented status %s", method, path, status)
	}
	if _, ok := declared.Content[response.Header().Get("Content-Type")]; !ok {
		t.Fatalf("%s %s returned undocumented content type %q", method, path, response.Header().Get("Content-Type"))
	}
}

type helpdeskDocument struct {
	OpenAPI    string
	Paths      map[string]map[string]helpdeskDocumentOperation
	Components struct {
		Schemas         map[string]helpdeskDocumentSchema
		SecuritySchemes map[string]struct{ Type, In, Name string }
	}
}

type helpdeskDocumentOperation struct {
	OperationID string
	Permission  string `json:"x-godj-permission"`
	Security    []map[string][]string
	Parameters  []struct{ Name, In string }
	Responses   map[string]struct {
		Content map[string]struct{ Schema helpdeskDocumentSchema }
		Headers map[string]json.RawMessage
	}
	RequestBody *struct {
		Required bool
		Content  map[string]struct{ Schema helpdeskDocumentSchema }
	}
}

type helpdeskDocumentSchema struct {
	Ref                  string `json:"$ref"`
	Type                 string
	Required             []string
	Format               string
	Pattern              string
	Minimum, Maximum     json.RawMessage
	MinLength            int
	Properties           map[string]helpdeskDocumentSchema
	Items                *helpdeskDocumentSchema
	AnyOf                []helpdeskDocumentSchema
	Default              json.RawMessage
	Enum                 json.RawMessage
	Normalization        json.RawMessage `json:"x-godj-normalization"`
	MaxLength            int
	ReadOnly             bool
	AdditionalProperties bool
}

func (schema helpdeskDocumentSchema) allowsType(kind string) bool {
	if schema.Type == kind {
		return true
	}
	for _, alternative := range schema.AnyOf {
		if alternative.allowsType(kind) {
			return true
		}
	}
	return false
}

func decodeHelpdeskDocument(t *testing.T, encoded []byte) helpdeskDocument {
	t.Helper()
	var document helpdeskDocument
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

type helpdeskRecordingAuthentication struct {
	permissions   []auth.Permission
	failAt, nilAt int
}

func (recording *helpdeskRecordingAuthentication) Require(permission auth.Permission, handler api.AuthenticatedHandler) (web.Handler, error) {
	recording.permissions = append(recording.permissions, permission)
	if len(recording.permissions) == recording.failAt {
		return nil, errors.New("injected authentication construction failure")
	}
	if len(recording.permissions) == recording.nilAt {
		return nil, nil
	}
	return func(request *web.Request) (web.Response, error) { return handler(request, auth.Anonymous()) }, nil
}

type helpdeskDescribedAuthentication struct {
	*helpdeskRecordingAuthentication
}

func (helpdeskDescribedAuthentication) DescribeAuthentication() (api.AuthenticationDescription, error) {
	return api.AuthenticationDescription{Kind: api.AuthenticationSession, SessionCookieName: "helpdesk_session", CSRFCookieName: "helpdesk_csrf", CSRFHeader: "X-Helpdesk-CSRF"}, nil
}

func helpdeskConstructionApplication(t *testing.T) *helpdesk.Application {
	t.Helper()
	application, err := helpdesk.New(helpdeskConstructionBackend{t: t}, 1)
	if err != nil {
		t.Fatal(err)
	}
	return application
}

type helpdeskConstructionBackend struct{ t *testing.T }

func (backend helpdeskConstructionBackend) Query(context.Context, query.Plan) (db.Rows, error) {
	backend.t.Fatal("API construction performed a query")
	return nil, nil
}
func (backend helpdeskConstructionBackend) Insert(context.Context, query.InsertPlan) (int64, error) {
	backend.t.Fatal("API construction inserted data")
	return 0, nil
}
func (backend helpdeskConstructionBackend) Update(context.Context, query.UpdatePlan) (int64, error) {
	backend.t.Fatal("API construction updated data")
	return 0, nil
}
func (backend helpdeskConstructionBackend) Delete(context.Context, query.DeletePlan) (int64, error) {
	backend.t.Fatal("API construction deleted data")
	return 0, nil
}
func (backend helpdeskConstructionBackend) Atomic(context.Context, func(db.Session) error) error {
	backend.t.Fatal("API construction opened a transaction")
	return nil
}
