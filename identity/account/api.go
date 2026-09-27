package identityaccount

import (
	"net/http"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

func (a *Application) prepareAPI() error {
	var fields []serializers.Field
	for _, name := range []string{"old_password", "new_password"} {
		field, err := serializers.StringField(name, serializers.WithTrimWhitespace(false), serializers.WithMaxLength(MaximumInputBytes))
		if err != nil {
			return err
		}
		fields = append(fields, field)
	}
	var err error
	a.passwordJSON, err = serializers.NewSpec(fields)
	if err != nil {
		return err
	}
	input, err := openapi.RequestSchema(a.passwordJSON, serializers.ModeFull)
	if err != nil {
		return err
	}
	errorSchema, err := openapi.Ref(openapi.ErrorSchemaName)
	if err != nil {
		return err
	}
	password, err := a.apiAuth.RequireAuthenticated(a.changeJSON)
	if err != nil {
		return err
	}
	csrf, err := a.apiAuth.RequireAuthenticated(func(request *web.Request, _ auth.Principal) (web.Response, error) {
		if !noQuery(request) {
			return invalidJSON()
		}
		return web.NewResponse(http.StatusNoContent, nil, nil)
	})
	if err != nil {
		return err
	}
	inputRef, err := openapi.Ref("PasswordChange")
	if err != nil {
		return err
	}
	errorResponse := func(status int, detail string) openapi.Response {
		return openapi.Response{Status: status, Description: detail, ContentType: api.JSONContentType, Schema: errorSchema}
	}
	operations := []openapi.Operation{
		{Route: web.Route{Name: a.namespace + ":account-api-csrf", Method: http.MethodGet, Path: a.apiBasePath + "/csrf/", Handler: csrf}, AuthenticatedOnly: true, Summary: "Read a masked CSRF token", Description: "Requires an active session, with no model permission or staff requirement. The response header carries a fresh masked CSRF token. Reads do not refresh or clean up server sessions. Query parameters are not accepted.", Responses: []openapi.Response{{Status: 204, Description: "Read the configured CSRF response header and retain its paired cookie."}, errorResponse(400, "Query parameters are not accepted.")}},
		{Route: web.Route{Name: a.namespace + ":account-api-password", Method: http.MethodPost, Path: a.apiBasePath + "/password/", Handler: password}, AuthenticatedOnly: true, Summary: "Change your password", Description: "The current session identifies the account. Authentication and CSRF precede parsing and perform no session touch or cleanup. Required nonempty old_password and new_password strings preserve whitespace; null, unknown or duplicate members are rejected. No target ID, management permission, revision input or confirmation field is accepted. Current credential, session and password policy are rechecked under one write fence. Success changes password and current revision, rotates the current session cookie while retaining payload and absolute lifetime, revokes other sessions and records value-free audit. last_login is unchanged. Failure publishes no cookie. Outcome unknown is 503 with code outcome_unknown: reconcile durable state before new work; never automatically retry.", RequestBody: &openapi.RequestBody{Schema: inputRef, Required: true}, Responses: []openapi.Response{{Status: 204, Description: "Password, current-session rotation, other-session revocation and audit committed. Accept the replacement session cookie.", Headers: []openapi.Header{{Name: "Set-Cookie", Schema: openapi.String(), Required: true, Description: "Replacement session cookie; the independent CSRF cookie is retained."}}}, errorResponse(400, "Invalid body, query or confirmed password-policy/old-password error."), errorResponse(413, "Body exceeds 65536 bytes. Oversized JSON strings are invalid input (400)."), errorResponse(415, "Only application/json with optional UTF-8 charset is accepted."), errorResponse(503, "Commit outcome unknown. Reconcile before submitting another operation; no Retry-After is sent.")}},
	}
	resetOperations, resetSchemas, err := a.resetAPI()
	if err != nil {
		return err
	}
	a.document, err = openapi.New(openapi.Config{Title: "GoDj account", Version: "0.1", Authentication: a.apiAuth, JSONPolicy: a.policy,
		Schemas: append([]openapi.NamedSchema{{Name: "PasswordChange", Schema: input}}, resetSchemas...), Operations: append(operations, resetOperations...)})
	return err
}

func (a *Application) changeJSON(request *web.Request, _ auth.Principal) (web.Response, error) {
	if !noQuery(request) {
		return invalidJSON()
	}
	object, err := a.parser.ParseObjectFor(request, a.passwordJSON)
	if err != nil {
		if response, handled, e := api.RequestErrorResponse(err); handled {
			return response, e
		}
		return web.Response{}, err
	}
	bound, err := a.passwordJSON.Bind(object, serializers.ModeFull)
	if err != nil {
		return web.Response{}, err
	}
	if !bound.Valid() {
		return api.ErrorResponse(http.StatusBadRequest, api.CodeValidationError, bound.Errors())
	}
	old, _ := bound.Values().Get("old_password")
	next, _ := bound.Values().Get("new_password")
	oldText, _ := old.AsString()
	nextText, _ := next.AsString()
	result, err := a.auth.ChangePassword(request, oldText, nextText)
	if err == auth.ErrInvalidCredentials {
		return api.ErrorResponse(http.StatusForbidden, api.CodeNotAuthenticated, validation.Errors{})
	}
	if failures, rejected := validation.Rejected(err); rejected {
		failures, err = presentPasswordErrors(failures, "new_password")
		if err != nil {
			return web.Response{}, err
		}
		return api.ErrorResponse(http.StatusBadRequest, api.CodeValidationError, failures)
	}
	if err != nil {
		if unknownOutcome(err) {
			body, e := serializers.NewObject(serializers.MemberOf("code", serializers.String("outcome_unknown")), serializers.MemberOf("errors", serializers.Integers()))
			if e != nil {
				return web.Response{}, e
			}
			return api.JSON(http.StatusServiceUnavailable, body.Value())
		}
		return web.Response{}, err
	}
	response, err := web.NewResponse(http.StatusNoContent, nil, nil)
	if err != nil {
		return web.Response{}, err
	}
	return result.Apply(response)
}

func invalidJSON() (web.Response, error) {
	return api.ErrorResponse(http.StatusBadRequest, api.CodeParseError, validation.Errors{})
}
func (a *Application) schemaResponse(request *web.Request) (web.Response, error) {
	if !noQuery(request) {
		return invalidJSON()
	}
	return a.document.Response()
}
