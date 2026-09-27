// Package identityaccount provides ordinary-user login/logout, password-change
// forms and a session-authenticated password API over one explicit Web runtime.
// It owns presentation and input mapping, not identity/session persistence.
package identityaccount

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	apisession "github.com/progresshans/godj/api/sessionauth"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/templates"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

const (
	DefaultBasePath    = "/account"
	DefaultAPIBasePath = "/api/account"
	MaximumBodyBytes   = 64 * 1024
	MaximumQueryBytes  = 4096
	MaximumInputBytes  = 4096
)

//go:embed templates/*.html
var templateFiles embed.FS

type Config struct {
	Apps        apps.Registry
	Namespace   string
	BasePath    string
	APIBasePath string
	Auth        *sessionauth.Runtime `json:"-"`
}

func (Config) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identityaccount.Config{redacted}"))
}
func (Config) MarshalJSON() ([]byte, error) { return []byte(`"identityaccount.Config{redacted}"`), nil }

type Application struct {
	basePath, apiBasePath, namespace string
	auth                             *sessionauth.Runtime
	apiAuth                          *apisession.Runtime
	login, password                  forms.Spec
	passwordJSON                     serializers.Spec
	parser                           api.Parser
	policy                           api.JSONPolicy
	document                         openapi.Document
	engine                           *templates.Engine
	routes                           []web.Route
}

func (*Application) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identityaccount.Application{redacted}"))
}

// AllowedNextPaths prepares the account destinations that must be included in
// the shared Web runtime's explicit local redirect allowlist. Login/logout are
// not destinations. Admin can keep its own login and fallback paths.
func AllowedNextPaths(basePath string) ([]string, error) {
	base, err := normalizeBase(basePath, DefaultBasePath)
	if err != nil {
		return nil, err
	}
	return []string{base + "/password/", base + "/password/done/"}, nil
}

// New prepares the complete surface without I/O. The host supplies one runtime
// with explicit password-change persistence and its chosen password validators.
// Every account route must be covered by both cookie paths.
func New(config Config) (*Application, error) {
	if _, ok := config.Apps.Lookup(config.Namespace); !ok {
		return nil, invalidConfig("namespace")
	}
	base, err := normalizeBase(config.BasePath, DefaultBasePath)
	if err != nil {
		return nil, err
	}
	apiBase, err := normalizeBase(config.APIBasePath, DefaultAPIBasePath)
	if err != nil {
		return nil, err
	}
	if config.Auth == nil || !config.Auth.CanChangePassword() {
		return nil, invalidConfig("password_change_persistence")
	}
	for _, suffix := range []string{"/login/", "/logout/", "/password/", "/password/done/"} {
		if !config.Auth.CookiesApplyTo(base+suffix) || strings.HasPrefix(base+suffix, apiBase+"/") {
			return nil, invalidConfig("paths")
		}
	}
	for _, suffix := range []string{"/password/", "/csrf/", "/openapi.json"} {
		if !config.Auth.CookiesApplyTo(apiBase + suffix) {
			return nil, invalidConfig("cookie_paths")
		}
	}
	allowed, _ := AllowedNextPaths(base)
	for _, p := range allowed {
		if !config.Auth.AllowsNext(p) {
			return nil, invalidConfig("allowed_next_paths")
		}
	}
	apiAuth, err := apisession.New(config.Auth, apisession.WithReadOnlyResolution())
	if err != nil {
		return nil, err
	}
	policy, err := api.NewJSONPolicy(apiBase + "/")
	if err != nil {
		return nil, err
	}
	parser, err := api.NewParser(api.ParserConfig{MaxBodyBytes: MaximumBodyBytes, JSONLimits: serializers.Limits{MaxStringBytes: MaximumInputBytes}})
	if err != nil {
		return nil, err
	}
	source, err := fs.Sub(templateFiles, "templates")
	if err != nil {
		return nil, err
	}
	engine, err := templates.New(source, templates.Config{})
	if err != nil {
		return nil, err
	}
	a := &Application{basePath: base, apiBasePath: apiBase, namespace: config.Namespace, auth: config.Auth, apiAuth: apiAuth, policy: policy, parser: parser, engine: engine}
	if err := a.prepareForms(); err != nil {
		return nil, err
	}
	if err := a.prepareAPI(); err != nil {
		return nil, err
	}
	a.routes = []web.Route{
		{Name: a.namespace + ":account-login", Method: http.MethodGet, Path: base + "/login/", Handler: a.loginGet},
		{Name: a.namespace + ":account-login-submit", Method: http.MethodPost, Path: base + "/login/", Handler: a.loginPost},
		{Name: a.namespace + ":account-logout", Method: http.MethodPost, Path: base + "/logout/", Handler: a.logoutPost},
		{Name: a.namespace + ":account-password", Method: http.MethodGet, Path: base + "/password/", Handler: a.passwordGet},
		{Name: a.namespace + ":account-password-submit", Method: http.MethodPost, Path: base + "/password/", Handler: a.passwordPost},
		{Name: a.namespace + ":account-password-done", Method: http.MethodGet, Path: base + "/password/done/", Handler: a.passwordDone},
	}
	a.routes = append(a.routes, a.document.Routes()...)
	schema, err := a.apiAuth.RequireAuthenticated(func(request *web.Request, _ auth.Principal) (web.Response, error) { return a.schemaResponse(request) })
	if err != nil {
		return nil, err
	}
	a.routes = append(a.routes, web.Route{Name: a.namespace + ":account-openapi", Method: http.MethodGet, Path: a.OpenAPIPath(), Handler: schema})
	if err := web.ValidateRouteDeclarations(a.routes); err != nil {
		return nil, err
	}
	for i := range a.routes {
		a.routes[i].Handler = noStore(a.routes[i].Handler)
	}
	return a, nil
}

func (a *Application) Routes() []web.Route {
	if a == nil {
		return nil
	}
	return append([]web.Route(nil), a.routes...)
}
func (a *Application) Middleware() []web.Middleware {
	if a == nil {
		return nil
	}
	return a.policy.Middleware()
}
func (a *Application) OpenAPI() (openapi.Document, error) {
	if a == nil {
		return openapi.Document{}, invalidConfig("application")
	}
	return a.document, nil
}
func (a *Application) OpenAPIPath() string {
	if a == nil {
		return ""
	}
	return a.apiBasePath + "/openapi.json"
}

func normalizeBase(raw, fallback string) (string, error) {
	if raw == "" {
		raw = fallback
	}
	if len(raw) > 128 || !utf8.ValidString(raw) || raw == "/" || !strings.HasPrefix(raw, "/") || path.Clean(raw) != raw || strings.ContainsAny(raw, "\\%?#<> \t\r\n\x00") {
		return "", invalidConfig("base_path")
	}
	description, err := web.DescribeRoutePath(raw + "/")
	if err != nil || len(description.Parameters) != 0 {
		return "", invalidConfig("base_path")
	}
	return raw, nil
}

func invalidConfig(field string) error {
	return &api.Error{Code: api.FailureInvalidConfig, Field: field, Detail: "account surface configuration is invalid"}
}
