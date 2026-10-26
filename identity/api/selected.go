package identityapi

import (
	"fmt"
	"math"
	"net/http"
	"reflect"
	"slices"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/api/parameters"
	"github.com/progresshans/godj/auth"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/internal/identifiers"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/web"
)

// ManagementConfig supplies one authentication profile and static API subtree.
// Empty Namespace uses the selected account's app label; empty BasePath uses
// /api/identity/.
// Model exposure, permissions and transactions come from the actual owners.
type ManagementConfig struct {
	Namespace      string
	BasePath       string
	Authentication api.PolicyAuthentication `json:"-"`
}

func (ManagementConfig) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identityapi.ManagementConfig{redacted}"))
}

// Management publishes selected account and catalog JSON operations. It holds
// immutable declarations, not request state, model copies or cached authority.
type Management struct {
	document openapi.Document
	policy   api.JSONPolicy
}

func (Management) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identityapi.Management{redacted}"))
}

// NewManagement composes the generated account/catalog lifecycle owners.
// Startup performs no model reads, password hashing or user callbacks. Each
// request rechecks the selected owner's current authority and durable domain.
func NewManagement[M, G, P any](account *accountmodel.AccountManagement[M], catalog *accountmodel.Catalog[G, P], config ManagementConfig) (*Management, error) {
	if config.Namespace == "" {
		description, err := account.Description()
		if err != nil {
			return nil, err
		}
		config.Namespace = description.Identity.AppLabel
	}
	if !identifiers.SQL(config.Namespace) {
		return nil, selectedConfigError("namespace", nil)
	}
	if config.BasePath == "" {
		config.BasePath = BasePath
	}
	if selectedNil(config.Authentication) {
		return nil, selectedConfigError("authentication", nil)
	}
	policy, err := api.NewJSONPolicy(config.BasePath)
	if err != nil {
		return nil, err
	}
	revision, err := parameters.NewHeader("If-Revision", parameters.Required(parameters.CanonicalInt64(1, math.MaxInt64-1)), 19,
		"One canonical positive decimal row revision, without leading zeroes. Missing is 428, invalid is 400, stale is 412. This is not an ETag.")
	if err != nil {
		return nil, err
	}
	builder := &selectedBuilder{config: config, revision: revision}
	if err := addSelectedAccount(builder, account); err != nil {
		return nil, err
	}
	if err := addSelectedCatalog(builder, catalog.Groups(), "groups", "Group"); err != nil {
		return nil, err
	}
	if err := addSelectedCatalog(builder, catalog.Permissions(), "permissions", "Permission"); err != nil {
		return nil, err
	}
	document, err := openapi.New(openapi.Config{Title: "Selected account management", Version: "1",
		Authentication: config.Authentication, JSONPolicy: policy, Operations: builder.operations, Schemas: builder.schemas})
	if err != nil {
		return nil, err
	}
	return &Management{document: document, policy: policy}, nil
}

func (management *Management) Routes() []web.Route {
	if management == nil {
		return nil
	}
	return management.document.Routes()
}

func (management *Management) Middleware() []web.Middleware {
	if management == nil {
		return nil
	}
	return management.policy.Middleware()
}

// OpenAPI returns the exact prepared route and input/output declarations.
// Hosts choose the schema URL and its access policy explicitly.
func (management *Management) OpenAPI() (openapi.Document, error) {
	if management == nil || len(management.document.Bytes()) == 0 {
		return openapi.Document{}, selectedConfigError("management", nil)
	}
	return management.document, nil
}

type selectedBuilder struct {
	config     ManagementConfig
	revision   parameters.Header[int64]
	operations []openapi.Operation
	schemas    []openapi.NamedSchema
}

func (builder *selectedBuilder) schema(name string, schema openapi.Schema) (openapi.Schema, error) {
	ref, err := openapi.Ref(name)
	if err != nil {
		return openapi.Schema{}, err
	}
	builder.schemas = append(builder.schemas, openapi.NamedSchema{Name: name, Schema: schema})
	return ref, nil
}

func (builder *selectedBuilder) add(operation openapi.Operation, policy api.PermissionPolicy, handler api.AuthenticatedHandler) error {
	required, alternatives := policy.Required(), policy.Alternatives()
	if !policy.Valid() || handler == nil {
		return selectedConfigError("operation", nil)
	}
	if len(alternatives) != 0 {
		operation.Permission, operation.AlternativePermissions = alternatives[0], slices.Clone(alternatives[1:])
		operation.AdditionalPermissions = required
	} else {
		operation.Permission, operation.AdditionalPermissions = required[0], slices.Clone(required[1:])
	}
	execute := func(request *web.Request, actor auth.Principal) (web.Response, error) {
		if request == nil || request.HTTP() == nil || request.HTTP().URL == nil {
			return web.Response{}, selectedConfigError("request", nil)
		}
		if err := request.Context().Err(); err != nil {
			return web.Response{}, err
		}
		return handler(request, actor)
	}
	protected, err := builder.config.Authentication.RequirePolicy(policy, execute)
	if err != nil {
		return err
	}
	if protected == nil {
		return selectedConfigError("authentication.handler", nil)
	}
	operation.Route.Handler = noStore(protected)
	operation.Description = selectedManagementPolicy + operation.Description
	builder.operations = append(builder.operations, operation)
	return nil
}

func (builder *selectedBuilder) operation(resource, action, method, suffix string, responses []openapi.Response) openapi.Operation {
	return openapi.Operation{Route: web.Route{Name: builder.config.Namespace + ":" + resource + "-" + action,
		Method: method, Path: builder.config.BasePath + resource + "/" + suffix}, Summary: action + " " + resource, Responses: responses}
}

const selectedManagementPolicy = "Authentication and transport CSRF precede path, query, revision and body validation. Owners recheck current authority, credential domain, target state, revision, uniqueness and related effects in their native snapshot or transaction. Object IDs are canonical signed int64 decimal strings, including zero and negative keys, without a plus sign or leading zeroes. Read fields are explicitly selected. Writes return only confirmed ID and revision metadata and never reread a target or actor after commit. Outcome-unknown is 503 without Retry-After; reconcile durable state before another operation. Responses use Cache-Control: no-store. "

func selectedParserConfig() api.ParserConfig {
	return api.ParserConfig{MaxBodyBytes: 64 * 1024, JSONLimits: serializers.Limits{MaxStringBytes: 4096}}
}

func selectedPolicies(view, add, change, remove auth.Permission, additional []auth.Permission, account bool) (map[string]api.PermissionPolicy, error) {
	result := make(map[string]api.PermissionPolicy, 4)
	var err error
	alternatives := []auth.Permission{view}
	if change != view {
		alternatives = append(alternatives, change)
	}
	result["read"], err = api.NewPermissionPolicy(additional, alternatives)
	if err != nil {
		return nil, err
	}
	for _, action := range []struct {
		name       string
		permission auth.Permission
	}{{"create", add}, {"change", change}, {"delete", remove}} {
		required := []auth.Permission{action.permission}
		if account && action.name == "create" {
			required = append(required, change)
		}
		result[action.name], err = api.NewPermissionPolicy(append(required, additional...), nil)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func selectedResponses(status int, schema openapi.Schema, revision bool, failures ...int) ([]openapi.Response, error) {
	response := openapi.Response{Status: status, Description: http.StatusText(status), ContentType: api.JSONContentType, Schema: schema}
	if revision {
		value, err := openapi.IntegerRange(1, math.MaxInt64)
		if err != nil {
			return nil, err
		}
		response.Headers = []openapi.Header{{Name: "Revision", Required: true, Schema: value, Description: "Confirmed positive decimal row revision."}}
	}
	ref, err := openapi.Ref(openapi.ErrorSchemaName)
	if err != nil {
		return nil, err
	}
	responses := []openapi.Response{response}
	for _, failure := range failures {
		responses = append(responses, jsonResponse(failure, http.StatusText(failure), ref))
	}
	return responses, nil
}

func selectedConfigError(field string, cause error) error {
	return &api.Error{Code: api.FailureInvalidConfig, Field: "management." + field, Detail: "selected management declaration is invalid", Cause: cause}
}

func selectedNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	}
	return false
}
