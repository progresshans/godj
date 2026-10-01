package identityaccount

import (
	"net/http"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

const codeInvalidResetLink api.ResponseCode = "invalid_reset_link"

func (a *Application) resetAPI() ([]openapi.Operation, []openapi.NamedSchema, error) {
	if a.resetMailer == nil {
		return nil, nil, nil
	}
	email, err := serializers.StringField("email", serializers.WithTrimWhitespace(false), serializers.WithAllowEmpty(), serializers.WithMaxLength(MaximumInputBytes))
	if err != nil {
		return nil, nil, err
	}
	a.resetEmailJSON, err = serializers.NewSpec([]serializers.Field{email})
	if err != nil {
		return nil, nil, err
	}
	password, err := serializers.StringField("new_password", serializers.WithTrimWhitespace(false), serializers.WithMaxLength(MaximumInputBytes))
	if err != nil {
		return nil, nil, err
	}
	a.resetPasswordJSON, err = serializers.NewSpec([]serializers.Field{password})
	if err != nil {
		return nil, nil, err
	}
	var schemas []openapi.NamedSchema
	for _, entry := range []struct {
		name string
		spec serializers.Spec
	}{{"PasswordResetRequest", a.resetEmailJSON}, {"PasswordResetComplete", a.resetPasswordJSON}} {
		schema, err := openapi.RequestSchema(entry.spec, serializers.ModeFull)
		if err != nil {
			return nil, nil, err
		}
		schemas = append(schemas, openapi.NamedSchema{Name: entry.name, Schema: schema})
	}
	errorSchema, err := openapi.Ref(openapi.ErrorSchemaName)
	if err != nil {
		return nil, nil, err
	}
	errorResponse := func(status int, detail string) openapi.Response {
		return openapi.Response{Status: status, Description: detail, ContentType: api.JSONContentType, Schema: errorSchema}
	}
	requestSchema, err := openapi.Ref("PasswordResetRequest")
	if err != nil {
		return nil, nil, err
	}
	passwordSchema, err := openapi.Ref("PasswordResetComplete")
	if err != nil {
		return nil, nil, err
	}
	operations := []openapi.Operation{
		{Route: web.Route{Name: a.namespace + ":account-api-reset-csrf", Method: "GET", Path: a.apiBasePath + "/reset/", Handler: func(r *web.Request) (web.Response, error) {
			if !noQuery(r) {
				return invalidJSON()
			}
			return api.NoContent()
		}}, CSRFOnly: true, Summary: "Prepare a password reset request", Description: "No login session is required or read. Retain the CSRF cookie and masked response header for subsequent unsafe requests. Query parameters are not accepted.", Responses: []openapi.Response{{Status: 204, Description: "Read the configured masked CSRF response header and retain its paired cookie."}, errorResponse(400, "Query parameters are not accepted.")}},
		{Route: web.Route{Name: a.namespace + ":account-api-reset-request", Method: "POST", Path: a.apiBasePath + "/reset/", Handler: a.resetRequestJSON}, CSRFOnly: true, Summary: "Request a password reset email", Description: "No login is required. CSRF and origin checks precede parsing. The required email string uses Unicode trimming and shared email validation. Every valid email receives the same 204 acknowledgement, including unknown/inactive/unusable accounts and private delivery or infrastructure failures. This is not a delivery receipt or a constant-time guarantee. Failures are separately reported to the host; delivery is never retried automatically. Follow any received email link with the same cookie-retaining browser/client to establish the server-side proof. No query, null, unknown or duplicate members are accepted.", RequestBody: &openapi.RequestBody{Schema: requestSchema, Required: true}, Responses: []openapi.Response{{Status: 204, Description: "Request acknowledged without disclosing account or delivery state."}, errorResponse(400, "Invalid email, body or query."), errorResponse(413, "Body exceeds 65536 bytes."), errorResponse(415, "Only application/json with optional UTF-8 charset is accepted.")}},
		{Route: web.Route{Name: a.namespace + ":account-api-reset-proof", Method: "GET", Path: a.apiBasePath + "/reset/<str:uid>/", Handler: a.resetProofJSON}, CSRFOnly: true, SessionCookieRequired: true, Summary: "Check a session-bound reset link", Description: "The uid is the canonical unpadded base64url principal ID from the email link, bounded to 128 decoded bytes. First follow the email link, including its token-free redirect, retaining cookies. This endpoint checks the current server proof without session touch, cleanup or password hashing and returns a fresh masked CSRF header. It neither requires nor creates a login. No token is accepted here and query parameters are not accepted.", Responses: []openapi.Response{{Status: 204, Description: "Current proof is valid; this read grants no later write authority."}, errorResponse(400, "Query parameters are not accepted."), errorResponse(403, "Missing, malformed, expired, wrong-target or invalidated proof; code invalid_reset_link.")}},
		{Route: web.Route{Name: a.namespace + ":account-api-reset-complete", Method: "POST", Path: a.apiBasePath + "/reset/<str:uid>/", Handler: a.resetCompleteJSON}, CSRFOnly: true, SessionCookieRequired: true, Summary: "Complete a password reset", Description: "CSRF precedes proof admission and JSON parsing. The uid is the canonical base64url principal ID from the email link. Required new_password preserves whitespace. Confirmation belongs to the HTML form and is not accepted by this JSON command. Current proof/account/session/policy are rechecked under the final write fence. Password/revision, target-session revocation, current-proof cleanup and value-free audit commit together. No automatic login occurs; last_login is preserved. Success rotates an anonymous/other-account session or clears the current target login cookie. Keep the independent CSRF cookie. Invalid proof is 403 invalid_reset_link. A 503 outcome_unknown means reconcile durable state before another submission; never retry automatically. Null, duplicate/unknown members and query parameters are rejected.", RequestBody: &openapi.RequestBody{Schema: passwordSchema, Required: true}, Responses: []openapi.Response{{Status: 204, Description: "Reset committed. Accept the replacement or deletion session cookie.", Headers: []openapi.Header{{Name: "Set-Cookie", Schema: openapi.String(), Required: true, Description: "Rotated proof-free session, or deletion of the target's current login cookie."}}}, errorResponse(400, "Invalid body, query or confirmed password-policy error."), errorResponse(403, "CSRF rejected or reset proof invalid."), errorResponse(413, "Body exceeds 65536 bytes."), errorResponse(415, "Only application/json with optional UTF-8 charset is accepted."), errorResponse(503, "Commit outcome unknown; no cookie or Retry-After is published.")}},
	}
	for i := range operations {
		handler, err := a.apiAuth.RequireCSRF(operations[i].Route.Handler)
		if err != nil {
			return nil, nil, err
		}
		operations[i].Route.Handler = handler
	}
	return operations, schemas, nil
}

func (a *Application) resetRequestJSON(r *web.Request) (web.Response, error) {
	if !noQuery(r) {
		return invalidJSON()
	}
	object, err := a.parser.ParseObjectFor(r, a.resetEmailJSON)
	if err != nil {
		return jsonRequestFailure(err)
	}
	bound, err := a.resetEmailJSON.Bind(object, serializers.ModeFull)
	if err != nil {
		return web.Response{}, err
	}
	if !bound.Valid() {
		return api.ErrorResponse(400, api.CodeValidationError, bound.Errors())
	}
	value, _ := bound.Values().Get("email")
	raw, _ := value.AsString()
	email, failures := identity.CleanPasswordResetEmail(raw)
	if !failures.Empty() {
		return api.ErrorResponse(400, api.CodeValidationError, failures)
	}
	a.requestResetMail(r.Context(), email)
	return api.NoContent()
}
func invalidResetJSON() (web.Response, error) {
	return api.ErrorResponse(403, codeInvalidResetLink, validation.Errors{})
}
func (a *Application) resetProofJSON(r *web.Request) (web.Response, error) {
	if !noQuery(r) {
		return invalidJSON()
	}
	principal, ok := resetPrincipal(r)
	if !ok {
		return invalidResetJSON()
	}
	err := a.auth.CheckPasswordReset(r, principal, nil)
	if err == auth.ErrInvalidResetProof {
		return invalidResetJSON()
	}
	if err != nil {
		return web.Response{}, err
	}
	return api.NoContent()
}
func (a *Application) resetCompleteJSON(r *web.Request) (web.Response, error) {
	if !noQuery(r) {
		return invalidJSON()
	}
	principal, ok := resetPrincipal(r)
	if !ok {
		return invalidResetJSON()
	}
	if err := a.auth.CheckPasswordReset(r, principal, nil); err != nil {
		if err == auth.ErrInvalidResetProof {
			return invalidResetJSON()
		}
		return web.Response{}, err
	}
	object, err := a.parser.ParseObjectFor(r, a.resetPasswordJSON)
	if err != nil {
		return jsonRequestFailure(err)
	}
	bound, err := a.resetPasswordJSON.Bind(object, serializers.ModeFull)
	if err != nil {
		return web.Response{}, err
	}
	if !bound.Valid() {
		return api.ErrorResponse(400, api.CodeValidationError, bound.Errors())
	}
	value, _ := bound.Values().Get("new_password")
	password, _ := value.AsString()
	result, err := a.auth.ResetPassword(r, principal, password)
	if err == auth.ErrInvalidResetProof {
		return invalidResetJSON()
	}
	if failures, rejected := validation.Rejected(err); rejected {
		failures, err = presentPasswordErrors(failures, "new_password")
		if err != nil {
			return web.Response{}, err
		}
		return api.ErrorResponse(400, api.CodeValidationError, failures)
	}
	if err != nil {
		if unknownOutcome(err) {
			a.reportReset(r.Context(), err)
			body, e := serializers.NewObject(serializers.MemberOf("code", serializers.String("outcome_unknown")), serializers.MemberOf("errors", serializers.Integers()))
			if e != nil {
				return web.Response{}, e
			}
			return api.JSON(http.StatusServiceUnavailable, body.Value())
		}
		return web.Response{}, err
	}
	response, err := api.NoContent()
	if err != nil {
		return web.Response{}, err
	}
	return result.Apply(response)
}
func jsonRequestFailure(err error) (web.Response, error) {
	if response, handled, e := api.RequestErrorResponse(err); handled {
		return response, e
	}
	return web.Response{}, err
}
