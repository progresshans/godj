package apiapp

import (
	"fmt"
	"net/http"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/examples/article/articleapp"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/web"
)

// OpenAPI describes the same operations used by Routes. A custom authentication
// adapter can serve the API without exposing a documentation profile; document
// construction explicitly rejects an adapter that does not describe its policy.
func (a *Application) OpenAPI() (openapi.Document, error) {
	if a == nil {
		return openapi.Document{}, fmt.Errorf("article api OpenAPI: application is nil")
	}
	return openapi.New(openapi.Config{
		Title:          "Article API",
		Version:        "development",
		Operations:     a.operations,
		Authentication: a.authentication,
		JSONPolicy:     a.jsonPolicy,
		Schemas:        a.schemas,
	})
}

func (a *Application) buildOperations(authentication api.Authentication) ([]openapi.Operation, error) {
	named := func(name string, schema openapi.Schema) (openapi.Schema, error) {
		reference, err := openapi.Ref(name)
		if err != nil {
			return openapi.Schema{}, err
		}
		a.schemas = append(a.schemas, openapi.NamedSchema{Name: name, Schema: schema})
		return reference, nil
	}
	article, err := openapi.ModelResponseSchema(a.spec)
	if err != nil {
		return nil, fmt.Errorf("article api response schema: %w", err)
	}
	article, err = named("Article", article)
	if err != nil {
		return nil, err
	}
	full, err := openapi.RequestSchema(a.spec, serializers.ModeFull)
	if err != nil {
		return nil, fmt.Errorf("article api full input schema: %w", err)
	}
	create, err := named("ArticleCreate", full)
	if err != nil {
		return nil, err
	}
	replace, err := named("ArticleReplace", full)
	if err != nil {
		return nil, err
	}
	partial, err := openapi.RequestSchema(a.spec, serializers.ModePartial)
	if err != nil {
		return nil, fmt.Errorf("article api partial input schema: %w", err)
	}
	partial, err = named("ArticlePatch", partial)
	if err != nil {
		return nil, err
	}
	items, err := openapi.Array(article)
	if err != nil {
		return nil, err
	}
	link, err := openapi.Nullable(openapi.String())
	if err != nil {
		return nil, err
	}
	page, err := openapi.Object(
		openapi.Property{Name: "count", Schema: openapi.Integer(), Required: true},
		openapi.Property{Name: "next", Schema: link, Required: true},
		openapi.Property{Name: "previous", Schema: link, Required: true},
		openapi.Property{Name: "results", Schema: items, Required: true},
	)
	if err != nil {
		return nil, err
	}
	page, err = named("ArticlePage", page)
	if err != nil {
		return nil, err
	}
	listOptions, err := articleOptionsSchema(http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPost)
	if err != nil {
		return nil, err
	}
	detailOptions, err := articleOptionsSchema(http.MethodDelete, http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPatch, http.MethodPut)
	if err != nil {
		return nil, err
	}
	query, err := articleQueryParameters()
	if err != nil {
		return nil, err
	}
	errorSchema, err := openapi.Ref(openapi.ErrorSchemaName)
	if err != nil {
		return nil, err
	}
	invalid := articleJSONResponse(http.StatusBadRequest, "The body cannot be parsed or a field or query parameter is invalid.", errorSchema)
	notFound := articleJSONResponse(http.StatusNotFound, "The Article or requested page does not exist, or its identifier is invalid.", errorSchema)
	tooLarge := articleJSONResponse(http.StatusRequestEntityTooLarge, "The request body exceeds 4096 bytes.", errorSchema)
	unsupported := articleJSONResponse(http.StatusUnsupportedMediaType, "The request Content-Type is not supported application/json.", errorSchema)
	listResponses := []openapi.Response{
		articleJSONResponse(http.StatusOK, "A page of Articles, with relative next and previous links or null.", page),
		invalid, notFound,
	}
	readResponses := []openapi.Response{articleJSONResponse(http.StatusOK, "The Article.", article), notFound}
	writeResponses := []openapi.Response{articleJSONResponse(http.StatusOK, "The updated Article.", article), invalid, notFound, tooLarge, unsupported}
	created := articleJSONResponse(http.StatusCreated, "The created Article.", article)
	created.Headers = []openapi.Header{{Name: "Location", Description: "Relative URL of the created Article.", Schema: openapi.String(), Required: true}}
	listOptionsResponse := articleJSONResponse(http.StatusOK, "The methods supported by the collection.", listOptions)
	listOptionsResponse.Headers = []openapi.Header{{Name: "Allow", Description: "GET, HEAD, OPTIONS, POST", Schema: openapi.String(), Required: true}}
	detailOptionsResponse := articleJSONResponse(http.StatusOK, "The methods supported by the detail route; no Article lookup is performed.", detailOptions)
	detailOptionsResponse.Headers = []openapi.Header{{Name: "Allow", Description: "DELETE, GET, HEAD, OPTIONS, PATCH, PUT", Schema: openapi.String(), Required: true}}
	const bodyLimits = "The body is a JSON object, limited to 4096 bytes and depth 16. Individual JSON strings are limited to 1024 bytes. Duplicate JSON members and trailing data are rejected. Text is trimmed and non-text control characters are rejected."
	const updateOrder = "Authentication, CSRF when required, and permission checks precede the Article lookup. A missing Article returns 404 before the update body is parsed."
	declarations := []struct {
		operation openapi.Operation
		handler   api.AuthenticatedHandler
	}{
		{operation: openapi.Operation{
			Route:   web.Route{Name: ListRouteName, Method: http.MethodGet, Path: ListPath},
			Summary: "List Articles", Description: "Returns two Articles per page. Search matches titles only. Unknown or duplicate query parameters are rejected. The raw query is limited to 4096 bytes.",
			Permission: articleapp.ArticleViewPermission, Parameters: query, Responses: listResponses,
		}, handler: a.list},
		{operation: openapi.Operation{
			Route:   web.Route{Name: Namespace + ":article-list-head", Method: http.MethodHead, Path: ListPath},
			Summary: "Read Article list headers", Description: "Runs the same query and validation as the list operation, without a response body.",
			Permission: articleapp.ArticleViewPermission, Parameters: query, Responses: listResponses,
		}, handler: a.listHead},
		{operation: openapi.Operation{
			Route:      web.Route{Name: Namespace + ":article-list-options", Method: http.MethodOptions, Path: ListPath},
			Summary:    "Describe Article collection methods",
			Permission: articleapp.ArticleViewPermission, Responses: []openapi.Response{listOptionsResponse},
		}, handler: a.listOptions},
		{operation: openapi.Operation{
			Route:       web.Route{Name: Namespace + ":article-create", Method: http.MethodPost, Path: ListPath},
			Summary:     "Create an Article",
			Permission:  articleapp.ArticleAddPermission,
			RequestBody: &openapi.RequestBody{Schema: create, Required: true, Description: bodyLimits + " Title is required; omitted published defaults to false and omitted summary becomes null. The generated id cannot be supplied."},
			Responses:   []openapi.Response{created, invalid, tooLarge, unsupported},
		}, handler: a.create},
		{operation: openapi.Operation{
			Route:   web.Route{Name: DetailRouteName, Method: http.MethodGet, Path: DetailPath},
			Summary: "Retrieve an Article", Description: "The identifier must be positive; a non-positive or missing Article returns 404.",
			Permission: articleapp.ArticleViewPermission, Responses: readResponses,
		}, handler: a.retrieve},
		{operation: openapi.Operation{
			Route:   web.Route{Name: Namespace + ":article-detail-head", Method: http.MethodHead, Path: DetailPath},
			Summary: "Read Article headers", Description: "Performs the same lookup as retrieval, without a response body.",
			Permission: articleapp.ArticleViewPermission, Responses: readResponses,
		}, handler: a.retrieveHead},
		{operation: openapi.Operation{
			Route:      web.Route{Name: Namespace + ":article-detail-options", Method: http.MethodOptions, Path: DetailPath},
			Summary:    "Describe Article detail methods",
			Permission: articleapp.ArticleViewPermission, Responses: []openapi.Response{detailOptionsResponse},
		}, handler: a.detailOptions},
		{operation: openapi.Operation{
			Route:   web.Route{Name: Namespace + ":article-update", Method: http.MethodPut, Path: DetailPath},
			Summary: "Update an Article", Description: updateOrder,
			Permission:  articleapp.ArticleChangePermission,
			RequestBody: &openapi.RequestBody{Schema: replace, Required: true, Description: bodyLimits + " Title is required. Omitted published defaults to false. Omitted summary preserves its current value; explicit null clears it."},
			Responses:   writeResponses,
		}, handler: a.update},
		{operation: openapi.Operation{
			Route:   web.Route{Name: Namespace + ":article-partial-update", Method: http.MethodPatch, Path: DetailPath},
			Summary: "Partially update an Article", Description: updateOrder,
			Permission:  articleapp.ArticleChangePermission,
			RequestBody: &openapi.RequestBody{Schema: partial, Required: true, Description: bodyLimits + " Only supplied fields change. An empty object is accepted. Explicit null clears summary; an empty string remains distinct from null."},
			Responses:   writeResponses,
		}, handler: a.patch},
		{operation: openapi.Operation{
			Route:      web.Route{Name: Namespace + ":article-delete", Method: http.MethodDelete, Path: DetailPath},
			Summary:    "Delete an Article",
			Permission: articleapp.ArticleDeletePermission,
			Responses:  []openapi.Response{{Status: http.StatusNoContent, Description: "The Article was deleted. The response has no body or Content-Type."}, notFound},
		}, handler: a.delete},
	}
	operations := make([]openapi.Operation, 0, len(declarations))
	for _, declaration := range declarations {
		operation := declaration.operation
		handler, err := authentication.Require(operation.Permission, declaration.handler)
		if err != nil {
			return nil, fmt.Errorf("article api authentication route %q: %w", operation.Route.Name, err)
		}
		if handler == nil {
			return nil, fmt.Errorf("article api authentication route %q: handler is nil", operation.Route.Name)
		}
		operation.Route.Handler = handler
		operations = append(operations, operation)
	}
	return operations, nil
}

func articleJSONResponse(status int, description string, schema openapi.Schema) openapi.Response {
	return openapi.Response{Status: status, Description: description, ContentType: api.JSONContentType, Schema: schema}
}

func articleOptionsSchema(methods ...string) (openapi.Schema, error) {
	method, err := openapi.EnumStrings(methods...)
	if err != nil {
		return openapi.Schema{}, err
	}
	list, err := openapi.Array(method)
	if err != nil {
		return openapi.Schema{}, err
	}
	return openapi.Object(openapi.Property{Name: "methods", Schema: list, Required: true})
}

func articleQueryParameters() ([]openapi.Parameter, error) {
	published, err := openapi.EnumStrings("true", "false")
	if err != nil {
		return nil, err
	}
	ordering, err := openapi.EnumStrings("id", "-id")
	if err != nil {
		return nil, err
	}
	return []openapi.Parameter{
		{Name: "search", In: "query", Schema: openapi.String(), AllowEmptyValue: true, Description: "Title substring, at most 64 UTF-8 bytes. An empty value is accepted. Duplicate values are rejected."},
		{Name: "published", In: "query", Schema: published, Description: "Exact lowercase true or false. Omission includes both values; empty or duplicate values are rejected."},
		{Name: "ordering", In: "query", Schema: ordering, Description: "Order by id ascending or descending. Omission orders by id ascending; duplicate values are rejected."},
		{Name: "page", In: "query", Schema: openapi.String(), AllowEmptyValue: true, Description: "Positive decimal page number. Omission or an empty value selects page 1. Two results per page. Invalid, overflowing, or out-of-range pages return 404; duplicate values return 400."},
	}, nil
}
