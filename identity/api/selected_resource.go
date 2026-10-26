package identityapi

import (
	"context"
	"math"
	"net/http"

	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/api"
	apiinput "github.com/progresshans/godj/api/input"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/api/parameters"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/web"
)

// This private adapter is constructed only from real lifecycle owners. Hosts
// supply no duplicate CRUD callbacks, model copies or replacement SQL paths.
type selectedResource[M any] struct {
	name, title            string
	manager                orm.Manager[M]
	readFields, listFields []string
	account                bool
	policies               map[string]api.PermissionPolicy
	createSchema           openapi.Schema
	create                 api.AuthenticatedHandler
	edit                   apiinput.Body[selectedModelInput[M]]
	get                    func(context.Context, auth.Principal, int64) (selectedRow[M], error)
	list                   func(context.Context, auth.Principal, int, int) (selectedPage[M], error)
	update                 func(context.Context, auth.Principal, int64, int64, orm.ModelInput[M]) (int64, int64, error)
	remove                 func(context.Context, auth.Principal, int64, int64) (int64, int64, error)
	history                func(context.Context, auth.Principal, int64, int) ([]accountaudit.Entry, error)
}

type selectedPageRequest struct{ Offset, Limit int64 }

func addSelectedResource[M any](builder *selectedBuilder, resource selectedResource[M]) error {
	metadata, err := resource.manager.Metadata()
	if err != nil {
		return err
	}
	// A collection-only read policy has an empty scalar list projection. Keep
	// its source/key check even when the fields object has no scalar getter.
	validateRow := func(row selectedRow[M]) error {
		source, valid := row.Fields.Model()
		key, present := row.Fields.PrimaryKey()
		id, integer := key.Integer()
		if !valid || !source.Equal(metadata) || !present || !integer || id != row.ID || row.Revision <= 0 {
			return &api.Error{Code: api.FailureInvalidResponse, Field: "management.fields", Detail: "selected read source or identity is invalid"}
		}
		return nil
	}
	detail, page, err := selectedOutputs(builder, resource.title, resource.manager, resource.readFields, resource.listFields, resource.account)
	if err != nil {
		return err
	}
	pageQuery, err := parameters.New(1024,
		parameters.Field("offset", parameters.Default(parameters.CanonicalInt64(0, math.MaxInt32), int64(0)), func(value *selectedPageRequest, offset int64) { value.Offset = offset }, "Page offset."),
		parameters.Field("limit", parameters.Default(parameters.CanonicalInt64(1, 100), int64(20)), func(value *selectedPageRequest, limit int64) { value.Limit = limit }, "Maximum rows, excluding collections."))
	if err != nil {
		return err
	}
	responses, err := selectedResponses(200, page.Schema(), false, 400, 403)
	if err != nil {
		return err
	}
	operation := builder.operation(resource.name, "list", "GET", "", responses)
	operation.Parameters = pageQuery.Parameters()
	if err := builder.add(operation, resource.policies["read"], func(request *web.Request, actor auth.Principal) (web.Response, error) {
		query, response, handled, err := parseSelectedQuery(request, pageQuery)
		if handled || err != nil {
			return response, err
		}
		result, err := resource.list(request.Context(), actor, int(query.Offset), int(query.Limit))
		if err != nil {
			return selectedFailure(err)
		}
		result.Offset, result.Limit = query.Offset, query.Limit
		for _, row := range result.Items {
			if err := validateRow(row); err != nil {
				return web.Response{}, err
			}
		}
		return page.JSON(request.Context(), http.StatusOK, result)
	}); err != nil {
		return err
	}
	responses, err = selectedResponses(200, detail.Schema(), true, 400, 403, 404)
	if err != nil {
		return err
	}
	operation = builder.operation(resource.name, "detail", "GET", "<str:id>/", responses)
	if err := builder.add(operation, resource.policies["read"], func(request *web.Request, actor auth.Principal) (web.Response, error) {
		id, valid := selectedID(request, false)
		if !valid {
			return invalidRequest()
		}
		row, err := resource.get(request.Context(), actor, id)
		if err != nil {
			return selectedFailure(err)
		}
		if err := validateRow(row); err != nil {
			return web.Response{}, err
		}
		response, err := detail.JSON(request.Context(), http.StatusOK, row)
		return selectedRevisionResponse(response, row.Revision, err)
	}); err != nil {
		return err
	}
	mutation, err := selectedMutationSchema()
	if err != nil {
		return err
	}
	responses, err = selectedResponses(201, mutation, true, 400, 403, 412, 413, 415, 503)
	if err != nil {
		return err
	}
	createSchema, err := builder.schema(resource.title+"Create", resource.createSchema)
	if err != nil {
		return err
	}
	operation = builder.operation(resource.name, "create", "POST", "", responses)
	operation.RequestBody = &openapi.RequestBody{Schema: createSchema, Required: true, Description: "Explicit selected fields; omission defaults follow the model source."}
	if err := builder.add(operation, resource.policies["create"], func(request *web.Request, actor auth.Principal) (web.Response, error) {
		if !selectedNoQuery(request) {
			return invalidRequest()
		}
		return resource.create(request, actor)
	}); err != nil {
		return err
	}
	for _, method := range []string{"PUT", "PATCH"} {
		mode, suffix := serializers.ModeFull, "Put"
		if method == "PATCH" {
			mode, suffix = serializers.ModePartial, "Patch"
		}
		schema, err := resource.edit.Schema(mode)
		if err != nil {
			return err
		}
		ref, err := builder.schema(resource.title+suffix, schema)
		if err != nil {
			return err
		}
		responses, err := selectedResponses(200, mutation, true, 400, 403, 404, 412, 413, 415, 428, 503)
		if err != nil {
			return err
		}
		operation := builder.operation(resource.name, suffix, method, "<str:id>/", responses)
		operation.Description = revisionPolicy
		operation.Parameters = []openapi.Parameter{builder.revision.Parameter()}
		operation.RequestBody = &openapi.RequestBody{Schema: ref, Required: true, Description: "Selected fields. PATCH preserves omission; PUT applies declared scalar defaults. Omitted collections are preserved."}
		if err := builder.add(operation, resource.policies["change"], func(request *web.Request, actor auth.Principal) (web.Response, error) {
			id, valid := selectedID(request, false)
			if !valid {
				return invalidRequest()
			}
			revision, response, handled, err := builder.condition(request)
			if handled || err != nil {
				return response, err
			}
			input, response, handled, err := parseSelectedBody(request, resource.edit, mode)
			if handled || err != nil {
				return response, err
			}
			key, version, err := resource.update(request.Context(), actor, id, revision, input.Fields)
			if err != nil {
				return selectedFailure(err)
			}
			return selectedMutationResponse(http.StatusOK, key, version)
		}); err != nil {
			return err
		}
	}
	responses, err = selectedResponses(200, mutation, true, 400, 403, 404, 412, 428, 503)
	if err != nil {
		return err
	}
	operation = builder.operation(resource.name, "delete", "DELETE", "<str:id>/", responses)
	operation.Description = revisionPolicy
	operation.Parameters = []openapi.Parameter{builder.revision.Parameter()}
	if err := builder.add(operation, resource.policies["delete"], func(request *web.Request, actor auth.Principal) (web.Response, error) {
		id, valid := selectedID(request, false)
		if !valid {
			return invalidRequest()
		}
		revision, response, handled, err := builder.condition(request)
		if handled || err != nil {
			return response, err
		}
		key, version, err := resource.remove(request.Context(), actor, id, revision)
		if err != nil {
			return selectedFailure(err)
		}
		return selectedMutationResponse(http.StatusOK, key, version)
	}); err != nil {
		return err
	}
	history, err := selectedHistoryOutput()
	if err != nil {
		return err
	}
	historyQuery, err := parameters.New(1024, parameters.Field("limit", parameters.Default(parameters.CanonicalInt64(1, admin.MaximumHistoryEntries), int64(20)),
		func(value *selectedPageRequest, limit int64) { value.Limit = limit }, "Maximum current-domain history entries."))
	if err != nil {
		return err
	}
	responses, err = selectedResponses(200, history.Schema(), false, 400, 403, 404)
	if err != nil {
		return err
	}
	operation = builder.operation(resource.name, "history", "GET", "<str:id>/history/", responses)
	operation.Description = "History contains sequence, action, changed field names and time; it excludes actor principal IDs and values. "
	operation.Parameters = historyQuery.Parameters()
	return builder.add(operation, resource.policies["read"], func(request *web.Request, actor auth.Principal) (web.Response, error) {
		id, valid := selectedID(request, true)
		if !valid {
			return invalidRequest()
		}
		query, response, handled, err := parseSelectedQuery(request, historyQuery)
		if handled || err != nil {
			return response, err
		}
		entries, err := resource.history(request.Context(), actor, id, int(query.Limit))
		if err != nil {
			return selectedFailure(err)
		}
		return history.JSON(request.Context(), http.StatusOK, entries)
	})
}

func parseSelectedQuery[T any](request *web.Request, query parameters.Query[T]) (T, web.Response, bool, error) {
	var zero T
	if request.HTTP().URL.ForceQuery {
		response, err := invalidRequest()
		return zero, response, true, err
	}
	value, diagnostics, err := query.Parse(request.HTTP().URL.RawQuery)
	if err != nil {
		return zero, web.Response{}, false, err
	}
	if !diagnostics.Empty() {
		response, err := api.ErrorResponse(http.StatusBadRequest, api.CodeValidationError, diagnostics)
		return zero, response, true, err
	}
	return value, web.Response{}, false, nil
}
