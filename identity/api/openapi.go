package identityapi

import (
	"math"
	"net/http"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/web"
)

func jsonResponse(status int, description string, schema openapi.Schema) openapi.Response {
	return openapi.Response{Status: status, Description: description, ContentType: api.JSONContentType, Schema: schema}
}

const managementPolicy = "Authentication and transport CSRF precede parsing. Each read owns a current database snapshot. Every mutation rechecks current model permissions, the supplied revision, complete candidate relations and uniqueness under one write fence. Confirmed writes include value-free audit; deactivation/password replacement/deletion revoke the affected user's sessions. A mutation is never automatically retried. Responses exclude principal IDs, encoded passwords and session material and use Cache-Control: no-store."
const revisionPolicy = " If-Revision must contain exactly one canonical positive decimal row revision, for example 1. No wildcard, list, weak validator or leading zero is accepted. Missing header returns 428; invalid header 400; stale revision 412. Fetch current state and resolve the conflict explicitly. Outcome-unknown returns 503 with code outcome_unknown: reconcile durable state before submitting any new operation. No-op updates retain the revision."

func (a *Application) describe(authentication api.AlternativeAuthentication) (openapi.Document, error) {
	errorSchema, err := openapi.Ref(openapi.ErrorSchemaName)
	if err != nil {
		return openapi.Document{}, err
	}
	var schemas []openapi.NamedSchema
	var operations []openapi.Operation
	count, _ := openapi.IntegerRange(0, math.MaxInt64)
	limit, _ := openapi.IntegerRange(1, 100)
	offset, _ := openapi.IntegerRange(0, math.MaxInt32)
	revision, _ := openapi.IntegerRange(1, math.MaxInt64)
	writableRevision, _ := openapi.IntegerRange(1, math.MaxInt64-1)
	versionHeader := openapi.Header{Name: "Revision", Schema: revision, Required: true, Description: "Current row revision as a canonical positive decimal, for example 1."}
	preconditionParameter := openapi.Parameter{Name: "If-Revision", In: "header", Required: true, Schema: writableRevision, Description: "One canonical positive decimal row revision (1..9223372036854775806). For password replacement, use the User revision. This is an application condition, not an ETag."}
	addSchema := func(name string, schema openapi.Schema) (openapi.Schema, error) {
		schemas = append(schemas, openapi.NamedSchema{Name: name, Schema: schema})
		return openapi.Ref(name)
	}
	protect := func(operation openapi.Operation, handler api.AuthenticatedHandler) error {
		var wrapped web.Handler
		var err error
		if len(operation.AlternativePermissions) != 0 {
			wrapped, err = authentication.RequireAny(operation.Permission, handler, operation.AlternativePermissions...)
		} else {
			wrapped, err = authentication.Require(operation.Permission, handler, operation.AdditionalPermissions...)
		}
		if err != nil {
			return err
		}
		if wrapped == nil {
			return &api.Error{Code: api.FailureInvalidConfig, Field: "authentication", Detail: "authentication returned a nil handler"}
		}
		operation.Route.Handler = noStore(wrapped)
		operations = append(operations, operation)
		return nil
	}
	for _, r := range a.resources {
		detail, err := openapi.ModelResponseSchema(r.detail)
		if err != nil {
			return openapi.Document{}, err
		}
		detailRef, err := addSchema(r.title, detail)
		if err != nil {
			return openapi.Document{}, err
		}
		scalar, err := openapi.ModelResponseSchema(r.scalar)
		if err != nil {
			return openapi.Document{}, err
		}
		scalarRef, err := addSchema(r.title+"Summary", scalar)
		if err != nil {
			return openapi.Document{}, err
		}
		items, err := openapi.Array(scalarRef)
		if err != nil {
			return openapi.Document{}, err
		}
		page, err := openapi.Object(openapi.Property{Name: "items", Schema: items, Required: true}, openapi.Property{Name: "count", Schema: count, Required: true}, openapi.Property{Name: "limit", Schema: limit, Required: true}, openapi.Property{Name: "offset", Schema: offset, Required: true})
		if err != nil {
			return openapi.Document{}, err
		}
		pageRef, err := addSchema(r.title+"Page", page)
		if err != nil {
			return openapi.Document{}, err
		}
		bad := jsonResponse(http.StatusBadRequest, "Malformed input or confirmed field rejection. Unknown/duplicate members, null nonnullable values, trailing JSON and forbidden fields are rejected.", errorSchema)
		missing := jsonResponse(http.StatusNotFound, "The target does not exist.", errorSchema)
		bodyFailures := []openapi.Response{jsonResponse(http.StatusRequestEntityTooLarge, "Request body exceeds 65536 bytes.", errorSchema), jsonResponse(http.StatusUnsupportedMediaType, "Only application/json with optional UTF-8 charset is accepted.", errorSchema)}
		preconditions := []openapi.Response{jsonResponse(http.StatusPreconditionRequired, "If-Revision is missing.", errorSchema), jsonResponse(http.StatusPreconditionFailed, "The current revision differs; no mutation committed.", errorSchema), jsonResponse(http.StatusServiceUnavailable, "Outcome unknown. Reconcile durable state before issuing new work; do not automatically retry.", errorSchema)}
		path := BasePath + r.name + "/"
		name := a.namespace + ":identity-" + r.name + "-"
		reads := []struct {
			suffix, path string
			handler      api.AuthenticatedHandler
			response     openapi.Response
			parameters   []openapi.Parameter
		}{
			{"list", path, a.list(r), jsonResponse(http.StatusOK, "Scalar page in ascending ID order; count and items share one snapshot.", pageRef), []openapi.Parameter{{Name: "limit", In: "query", Schema: limit, Description: "Default 20, maximum 100."}, {Name: "offset", In: "query", Schema: offset, Description: "Default zero. Canonical nonnegative decimal."}}},
			{"detail", path + "<int64:id>/", a.detail(r), jsonResponse(http.StatusOK, "Current detail and direct relation IDs.", detailRef), nil},
		}
		for _, read := range reads {
			if read.suffix == "detail" {
				read.response.Headers = []openapi.Header{versionHeader}
			}
			responses := []openapi.Response{read.response, bad}
			if read.suffix == "detail" {
				responses = append(responses, missing)
			}
			op := openapi.Operation{Route: web.Route{Name: name + read.suffix, Method: http.MethodGet, Path: read.path}, Summary: "Read " + r.name, Description: managementPolicy + " View OR change permission admits the request; an authorization failure never falls back. Lists accept only limit/offset, at most 1024 query bytes; details accept no query. Lists omit collections; details include direct groups/permissions, not inherited grants.", Permission: r.view, AlternativePermissions: []auth.Permission{r.change}, Parameters: read.parameters, Responses: responses}
			if err := protect(op, read.handler); err != nil {
				return openapi.Document{}, err
			}
		}
		for _, write := range []struct {
			suffix, method string
			spec           serializers.Spec
			mode           serializers.Mode
			handler        api.AuthenticatedHandler
		}{
			{"create", http.MethodPost, r.create, serializers.ModeFull, a.create(r)},
			{"update", http.MethodPut, r.update, serializers.ModeFull, a.update(r, serializers.ModeFull)},
			{"patch", http.MethodPatch, r.update, serializers.ModePartial, a.update(r, serializers.ModePartial)},
		} {
			input, err := openapi.RequestSchema(write.spec, write.mode)
			if err != nil {
				return openapi.Document{}, err
			}
			inputRef, err := addSchema(r.title+map[string]string{"create": "Create", "update": "Update", "patch": "Patch"}[write.suffix], input)
			if err != nil {
				return openapi.Document{}, err
			}
			op := openapi.Operation{Route: web.Route{Name: name + write.suffix, Method: write.method, Path: path + "<int64:id>/"}, Summary: write.suffix + " " + r.title, Permission: r.change, Description: managementPolicy + revisionPolicy + " PATCH preserves omitted fields. PUT applies declared scalar defaults; omitted optional collections remain unchanged. An explicit empty collection clears it. Relation IDs and effective grant limits are checked under the fence. Server identity, encoded password, timestamps and revision cannot be set through input.", Parameters: []openapi.Parameter{preconditionParameter}, RequestBody: &openapi.RequestBody{Schema: inputRef, Required: true}}
			status := http.StatusOK
			if write.suffix == "create" {
				status = http.StatusCreated
				op.Route.Path = path
				op.Permission = r.add
				op.Parameters = nil
				op.Description = managementPolicy + " Creates one record and requested direct relations. User creation requires BOTH add_user and change_user. The server selects a fresh opaque principal ID. The password member is required for User creation: a nonempty string sets a usable password with whitespace preserved; explicit null creates an account without password login. Omission and an empty string are rejected. Hosts may configure password validators for usable passwords; null does not invoke them. No password-confirmation policy is implied by this API."
				if r.name == "users" {
					op.AdditionalPermissions = []auth.Permission{identity.ChangeUser}
				}
			}
			success := jsonResponse(status, "Committed current detail.", detailRef)
			success.Headers = []openapi.Header{versionHeader}
			op.Responses = append([]openapi.Response{success, bad, missing}, bodyFailures...)
			if write.suffix != "create" {
				op.Responses = append(op.Responses, preconditions...)
			} else {
				op.Responses = append(op.Responses, preconditions[2])
			}
			if err := protect(op, write.handler); err != nil {
				return openapi.Document{}, err
			}
		}
		remove := openapi.Operation{Route: web.Route{Name: name + "delete", Method: http.MethodDelete, Path: path + "<int64:id>/"}, Summary: "Delete " + r.title, Permission: r.remove, Description: managementPolicy + revisionPolicy + " No request body is accepted. Host CASCADE/PROTECT/SET_NULL policies and direct relation-owner revisions are atomic. Delete permission does not expose target profile data.", Parameters: []openapi.Parameter{preconditionParameter}, Responses: append([]openapi.Response{{Status: http.StatusNoContent, Description: "Deletion committed."}, bad, missing}, preconditions...)}
		if err := protect(remove, a.remove(r)); err != nil {
			return openapi.Document{}, err
		}
		if r.name == "users" {
			body, err := openapi.RequestSchema(a.password, serializers.ModeFull)
			if err != nil {
				return openapi.Document{}, err
			}
			bodyRef, err := addSchema("PasswordReplacement", body)
			if err != nil {
				return openapi.Document{}, err
			}
			success := jsonResponse(http.StatusOK, "Committed user profile; all target sessions revoked, including the caller's if changing their own password.", scalarRef)
			success.Headers = []openapi.Header{versionHeader}
			op := openapi.Operation{Route: web.Route{Name: name + "password", Method: http.MethodPost, Path: path + "<int64:id>/password/"}, Summary: "Replace a user's password", Permission: identity.ChangeUser, Description: managementPolicy + revisionPolicy + " Administrative replacement, not self-service change/reset. A nonempty password string sets a usable password and preserves whitespace. Explicit null disables password login while retaining the account, roles and grants. The member is required; omission and empty strings are rejected. Every successful replacement, including repeated disablement, rotates the credential and revokes target sessions. Hashing or generation of an unusable marker runs once outside DB scopes after preflight; neither is returned. Password validators apply only to usable passwords. A successful response does not establish a login session.", Parameters: []openapi.Parameter{preconditionParameter}, RequestBody: &openapi.RequestBody{Schema: bodyRef, Required: true}, Responses: append(append([]openapi.Response{success, bad, missing}, bodyFailures...), preconditions...)}
			if err := protect(op, a.setPassword); err != nil {
				return openapi.Document{}, err
			}
		}
	}
	return openapi.New(openapi.Config{Title: "GoDj identity management", Version: "0.1", Authentication: authentication, JSONPolicy: a.policy, Operations: operations, Schemas: schemas})
}
