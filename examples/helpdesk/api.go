package helpdesk

import (
	"fmt"
	"net/http"
	"reflect"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/web"
)

const maximumJSONBodyBytes = 4096

// API owns one authenticated set of Helpdesk operations. It performs no I/O
// during construction and borrows the Application's backend through handlers.
type API struct {
	authentication api.Authentication
	operations     []openapi.Operation
	schemas        []openapi.NamedSchema
}

// API binds authentication once for the collection, create, and detail routes.
// Category policy and the typed create path remain shared with Admin. Callers
// choose the listener, middleware, and whether to publish the OpenAPI document.
func (a *Application) API(authentication api.Authentication) (*API, error) {
	if a == nil || nilAuthentication(authentication) {
		return nil, fmt.Errorf("helpdesk API: application or authentication is nil")
	}
	ticket, err := openapi.ModelResponseSchema(a.output)
	if err != nil {
		return nil, fmt.Errorf("helpdesk API Ticket schema: %w", err)
	}
	input, err := openapi.RequestSchema(a.input, serializers.ModeFull)
	if err != nil {
		return nil, fmt.Errorf("helpdesk API TicketCreate schema: %w", err)
	}
	category, err := openapi.Object(
		openapi.Property{Name: "id", Schema: openapi.Integer(), Required: true},
		openapi.Property{Name: "name", Schema: openapi.String(), Required: true},
	)
	if err != nil {
		return nil, err
	}
	ticketRef, err := openapi.Ref("Ticket")
	if err != nil {
		return nil, err
	}
	inputRef, err := openapi.Ref("TicketCreate")
	if err != nil {
		return nil, err
	}
	categoryRef, err := openapi.Ref("CategorySummary")
	if err != nil {
		return nil, err
	}
	detailRef, err := openapi.Ref("TicketDetail")
	if err != nil {
		return nil, err
	}
	detailSchema, err := openapi.Object(
		openapi.Property{Name: "ticket", Schema: ticketRef, Required: true},
		openapi.Property{Name: "category", Schema: categoryRef, Required: true},
	)
	if err != nil {
		return nil, err
	}
	items, err := openapi.Array(ticketRef)
	if err != nil {
		return nil, err
	}
	errorSchema, err := openapi.Ref(openapi.ErrorSchemaName)
	if err != nil {
		return nil, err
	}
	notFound := helpdeskJSONResponse(http.StatusNotFound, "The ticket does not exist in the selected category or its identifier is not positive.", errorSchema)
	protect := func(operation openapi.Operation, handler api.AuthenticatedHandler) (openapi.Operation, error) {
		protected, err := authentication.Require(operation.Permission, handler)
		if err != nil {
			return openapi.Operation{}, fmt.Errorf("helpdesk API authentication route %q: %w", operation.Route.Name, err)
		}
		if protected == nil {
			return openapi.Operation{}, fmt.Errorf("helpdesk API authentication route %q: handler is nil", operation.Route.Name)
		}
		operation.Route.Handler = protected
		return operation, nil
	}
	detail, err := protect(openapi.Operation{
		Route:       web.Route{Name: "helpdesk:ticket-detail", Method: http.MethodGet, Path: "/api/tickets/<int64:id>/"},
		Summary:     "Retrieve a ticket and its category",
		Description: "The identifier must be positive. The selected category is fixed by the application. Viewing a ticket includes its category identity and name; a separate category-view permission is not required. Authentication and permission checks precede the joined lookup.",
		Permission:  ViewTicket,
		Responses:   []openapi.Response{helpdeskJSONResponse(http.StatusOK, "The ticket and its category summary.", detailRef), notFound},
	}, a.detail)
	if err != nil {
		return nil, err
	}
	list, err := protect(openapi.Operation{
		Route:       web.Route{Name: "helpdesk:ticket-list", Method: http.MethodGet, Path: "/api/tickets/"},
		Summary:     "List tickets",
		Description: "Returns at most 20 tickets in the application's selected category, ordered by ascending identifier. The response is a bare array. Query parameters are ignored.",
		Permission:  ViewTicket,
		Responses:   []openapi.Response{helpdeskJSONResponse(http.StatusOK, "The selected category's tickets.", items)},
	}, a.apiList)
	if err != nil {
		return nil, err
	}
	create, err := protect(openapi.Operation{
		Route:       web.Route{Name: "helpdesk:ticket-create", Method: http.MethodPost, Path: "/api/tickets/"},
		Summary:     "Create a ticket",
		Description: "The application assigns the selected category and checks its existence within the create transaction. Authentication, CSRF when required, and permission checks precede body parsing; input validation precedes the category lookup.",
		Permission:  AddTicket,
		RequestBody: &openapi.RequestBody{Schema: inputRef, Required: true, Description: fmt.Sprintf("The body must be one JSON object, limited to %d bytes and depth %d. The JSON string limit is %d bytes, subject to the smaller whole-body limit. Duplicate members and trailing data are rejected. Subject is required. Omitted closed defaults to false; omitted details and priority become null. Empty details is distinct from null. Priority is a signed int64, and zero is distinct from null. Text is trimmed. The generated id and assigned category cannot be supplied.", maximumJSONBodyBytes, serializers.DefaultMaxDepth, serializers.DefaultMaxStringBytes)},
		Responses: []openapi.Response{
			helpdeskJSONResponse(http.StatusCreated, "The created ticket.", ticketRef),
			helpdeskJSONResponse(http.StatusBadRequest, "The body cannot be parsed or a supplied field is invalid.", errorSchema),
			helpdeskJSONResponse(http.StatusNotFound, "The application's selected category no longer exists.", errorSchema),
			helpdeskJSONResponse(http.StatusRequestEntityTooLarge, fmt.Sprintf("The request body exceeds %d bytes.", maximumJSONBodyBytes), errorSchema),
			helpdeskJSONResponse(http.StatusUnsupportedMediaType, "The request Content-Type is not supported application/json.", errorSchema),
		},
	}, a.apiCreate)
	if err != nil {
		return nil, err
	}
	return &API{
		authentication: authentication,
		operations:     []openapi.Operation{list, create, detail},
		schemas: []openapi.NamedSchema{
			{Name: "Ticket", Schema: ticket},
			{Name: "TicketCreate", Schema: input},
			{Name: "TicketDetail", Schema: detailSchema},
			{Name: "CategorySummary", Schema: category},
		},
	}, nil
}

// Routes returns detached declarations whose handlers use this API's single
// authentication composition. It does not add a document endpoint or middleware.
func (a *API) Routes() []web.Route {
	if a == nil {
		return nil
	}
	routes := make([]web.Route, len(a.operations))
	for index, operation := range a.operations {
		routes[index] = operation.Route
	}
	return routes
}

// OpenAPI describes the same operations used by Routes. An authentication
// adapter without a documentation profile can serve routes but cannot publish
// this document. Helpdesk applies no Accept negotiation policy by default.
func (a *API) OpenAPI() (openapi.Document, error) {
	if a == nil {
		return openapi.Document{}, fmt.Errorf("helpdesk OpenAPI: API is nil")
	}
	return openapi.New(openapi.Config{
		Title:          "Helpdesk API",
		Version:        "development",
		Operations:     a.operations,
		Authentication: a.authentication,
		Schemas:        a.schemas,
	})
}

func helpdeskJSONResponse(status int, description string, schema openapi.Schema) openapi.Response {
	return openapi.Response{Status: status, Description: description, ContentType: api.JSONContentType, Schema: schema}
}

func nilAuthentication(authentication api.Authentication) bool {
	if authentication == nil {
		return true
	}
	value := reflect.ValueOf(authentication)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
