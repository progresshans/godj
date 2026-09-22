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

const (
	maximumJSONBodyBytes  = 4096
	maximumJSONListValues = 1 << 16
)

// API owns one authenticated set of Helpdesk operations. It performs no I/O
// during construction and borrows the Application's backend through handlers.
type API struct {
	authentication api.Authentication
	operations     []openapi.Operation
	schemas        []openapi.NamedSchema
}

// API binds authentication once for each collection and detail operation.
// Category policy and typed mutation paths remain shared with Admin. Callers
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
	partial, err := openapi.RequestSchema(a.input, serializers.ModePartial)
	if err != nil {
		return nil, fmt.Errorf("helpdesk API TicketPatch schema: %w", err)
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
	updateRef, err := openapi.Ref("TicketUpdate")
	if err != nil {
		return nil, err
	}
	patchRef, err := openapi.Ref("TicketPatch")
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
		Description: "Returns at most 20 tickets in the application's selected category, ordered by ascending identifier. The response is a bare array with a shared 65536-value budget; the 1 MiB byte, depth 16 and per-container limits still apply. Search matches the subject or stored external JSON text; source matches the value at external_payload.source. Nonempty filters combine with AND. Empty values omit the filter. Case and non-string JSON text conversion follow the selected database. Unknown or duplicate parameters, malformed encoding, invalid UTF-8, NUL, values over 64 UTF-8 bytes, and query strings over 2048 bytes return 400. Authentication and view permission checks run before parsing.",
		Permission:  ViewTicket,
		Parameters: []openapi.Parameter{
			{Name: "search", In: "query", Schema: openapi.String(), AllowEmptyValue: true, Description: "Literal case-insensitive subject or whole external JSON substring, at most 64 UTF-8 bytes. Empty means no filter. JSON containment is a separate operation."},
			{Name: "source", In: "query", Schema: openapi.String(), AllowEmptyValue: true, Description: "Literal case-insensitive text substring at external_payload.source, at most 64 UTF-8 bytes. Empty means no filter. A missing source does not match a nonempty filter."},
		},
		Responses: []openapi.Response{helpdeskJSONResponse(http.StatusOK, "The selected category's tickets.", items), helpdeskJSONResponse(http.StatusBadRequest, "The query parameters are invalid.", errorSchema)},
	}, a.apiList)
	if err != nil {
		return nil, err
	}
	create, err := protect(openapi.Operation{
		Route:       web.Route{Name: "helpdesk:ticket-create", Method: http.MethodPost, Path: "/api/tickets/"},
		Summary:     "Create a ticket",
		Description: "The application assigns the selected category and checks its existence within the create transaction. Authentication, CSRF when required, and permission checks precede body parsing. Structural input validation precedes the category lookup; uniqueness checks follow it in the same transaction.",
		Permission:  AddTicket,
		RequestBody: &openapi.RequestBody{Schema: inputRef, Required: true, Description: fmt.Sprintf("The body must be one JSON object, limited to %d bytes and depth %d. The JSON string limit is %d bytes, subject to the smaller whole-body limit. Duplicate members and trailing data are rejected. Subject is required. Omitted closed defaults to false; omitted details, priority, resolution, due_at, reviewed, service_on, service_at, elapsed, effort, expected_cost, external_reference and external_payload become null. External_payload accepts any JSON value with exact number tokens and canonical object order. JSON null clears the SQL value; PUT/PATCH omission preserves it. Empty objects, arrays and strings are present values. Empty object keys are accepted inside external_payload. Duplicate keys, NUL, malformed Unicode and request or model budget overflows are rejected. JSON strings remain strings rather than being parsed a second time. The payload itself is limited to depth 14 so it also fits the detail/list response envelope. Native storage normalization is checked inside the transaction before a write is committed. External_reference uses a canonical lowercase hyphenated UUID string for clients and responses. Runtime input also accepts UUID aliases, exact unsigned 128-bit JSON integer tokens, and booleans as zero or one; floating tokens such as 1.0 are invalid. The all-zero UUID is a present value and is unique across all tickets, as is every non-null external reference. Multiple SQL null references are allowed. Duplicate input returns 400 validation_error with the external_reference field and unique code. A concurrent storage conflict returns the same code on __all__ without disclosing a stored value. The transaction rolls back on either rejection. Null clears the reference, while PUT/PATCH omission preserves it. Expected_cost is an exact decimal with up to 14 total digits and 2 decimal places, returned as a fixed-scale JSON string. Decimal strings and exact JSON number tokens are accepted; excess precision/scale and non-finite values are rejected before persistence. Null clears the cost, while PUT/PATCH omission preserves an existing value. Boolean, array and object values are invalid. Effort is a finite binary64 work quantity; JSON numbers, booleans and decimal strings follow field conversion, rounding to binary64. Non-finite inputs and overflowing JSON integers are rejected before persistence. Elapsed is a signed duration with microsecond precision, rendered as normalized [days ]HH:MM:SS[.ffffff]. Duration strings accept Django day/time and ISO week/day/time inputs; numbers follow the pinned DRF JSON-number profile. Service_at is a clock without a date or timezone, with microsecond precision; accepted ISO offsets are discarded without converting the clock. Service_on is a calendar date without a clock or timezone; ISO calendar, compact and week inputs normalize to YYYY-MM-DD in years 0001 through 9999. Reviewed accepts only JSON true, false, or null; false is distinct from null. Due_at requires an RFC 3339 timestamp with an explicit offset; instants are normalized to UTC microseconds, truncating finer precision. Empty details and resolution are distinct from null. Resolution is multiline text without a model length limit; the request body budget still applies. Priority accepts 1 (Urgent), 0 (Normal), -1 (Low), or null; zero is distinct from null. Existing stored priorities outside these input choices remain visible in responses. Subject, details and resolution text is trimmed. The generated id and assigned category cannot be supplied.", maximumJSONBodyBytes, serializers.DefaultMaxDepth, serializers.DefaultMaxStringBytes)},
		Responses: []openapi.Response{
			helpdeskJSONResponse(http.StatusCreated, "The created ticket.", ticketRef),
			helpdeskJSONResponse(http.StatusBadRequest, "The body cannot be parsed, a supplied field is invalid, or a non-null external reference is already in use. A concurrent uniqueness conflict uses the __all__ diagnostic field.", errorSchema),
			helpdeskJSONResponse(http.StatusNotFound, "The application's selected category no longer exists.", errorSchema),
			helpdeskJSONResponse(http.StatusRequestEntityTooLarge, fmt.Sprintf("The request body exceeds %d bytes.", maximumJSONBodyBytes), errorSchema),
			helpdeskJSONResponse(http.StatusUnsupportedMediaType, "The request Content-Type is not supported application/json.", errorSchema),
		},
	}, a.apiCreate)
	if err != nil {
		return nil, err
	}
	writeResponses := []openapi.Response{
		helpdeskJSONResponse(http.StatusOK, "The updated ticket.", ticketRef),
		helpdeskJSONResponse(http.StatusBadRequest, "The body cannot be parsed, a supplied field is invalid, or a non-null external reference is already in use. A concurrent uniqueness conflict uses the __all__ diagnostic field.", errorSchema),
		notFound,
		helpdeskJSONResponse(http.StatusRequestEntityTooLarge, fmt.Sprintf("The request body exceeds %d bytes.", maximumJSONBodyBytes), errorSchema),
		helpdeskJSONResponse(http.StatusUnsupportedMediaType, "The request Content-Type is not supported application/json.", errorSchema),
	}
	const updateDescription = "Authentication, CSRF when required, and change permission checks precede body parsing. A nonpositive identifier returns 404. Structural input validation precedes the target lookup. Uniqueness checks follow the authorized target lookup and exclude the current ticket. Non-null external references are unique across all tickets; SQL null references are distinct. Duplicate input returns 400 validation_error with external_reference/unique; a concurrent storage conflict uses __all__/unique and rolls back the update. The target must belong to the application's selected category. The current row lookup and update share one transaction. Generated id and assigned category cannot be supplied. Omitted nullable fields preserve their stored values; explicit null clears them. Reviewed accepts only JSON true, false, or null. Service_on accepts ISO calendar, compact and week dates and returns YYYY-MM-DD without a clock or timezone. Service_at returns HH:MM:SS with six fractional digits when nonzero; omitted values are preserved and null clears the clock."
	update, err := protect(openapi.Operation{
		Route:   web.Route{Name: "helpdesk:ticket-update", Method: http.MethodPut, Path: "/api/tickets/<int64:id>/"},
		Summary: "Update a ticket", Description: updateDescription,
		Permission:  ChangeTicket,
		RequestBody: &openapi.RequestBody{Schema: updateRef, Required: true, Description: "Uses the create input's parsing and normalization limits. Subject is required; omitted closed defaults to false. Omitted nullable fields are preserved."},
		Responses:   writeResponses,
	}, a.apiUpdate)
	if err != nil {
		return nil, err
	}
	patch, err := protect(openapi.Operation{
		Route:   web.Route{Name: "helpdesk:ticket-patch", Method: http.MethodPatch, Path: "/api/tickets/<int64:id>/"},
		Summary: "Partially update a ticket", Description: updateDescription,
		Permission:  ChangeTicket,
		RequestBody: &openapi.RequestBody{Schema: patchRef, Required: true, Description: "Uses the create input's parsing and normalization limits. Every field is optional and defaults are not applied. Omitted fields, including closed, are preserved. An empty object returns the existing ticket without an UPDATE."},
		Responses:   writeResponses,
	}, a.apiPatch)
	if err != nil {
		return nil, err
	}
	reportOperations, reportSchemas, err := a.reportOperations(protect)
	if err != nil {
		return nil, err
	}
	labelOperations, labelSchemas, err := a.labelOperations(protect)
	if err != nil {
		return nil, err
	}
	operations := append([]openapi.Operation{list, create, detail, update, patch}, reportOperations...)
	operations = append(operations, labelOperations...)
	schemas := append(reportSchemas, labelSchemas...)
	return &API{
		authentication: authentication,
		operations:     operations,
		schemas: append([]openapi.NamedSchema{
			{Name: "Ticket", Schema: ticket},
			{Name: "TicketCreate", Schema: input},
			{Name: "TicketUpdate", Schema: input},
			{Name: "TicketPatch", Schema: partial},
			{Name: "TicketDetail", Schema: detailSchema},
			{Name: "CategorySummary", Schema: category},
		}, schemas...),
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
