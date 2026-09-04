// Package apiapp publishes the bounded authentication-profile-protected Article JSON API.
// It owns explicit Article conversion and route composition while persistence,
// generic JSON primitives, and authentication policy remain in lower packages.
package apiapp

import (
	"fmt"
	"net/http"
	"reflect"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/examples/article/articleapp"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/web"
)

const (
	Namespace = "godj_conformance"

	ListPath   = "/api/articles/"
	DetailPath = "/api/articles/<int64:id>/"

	ListRouteName   = Namespace + ":article-list"
	DetailRouteName = Namespace + ":article-detail"

	pageSize              = 2
	maximumSearchBytes    = 64
	maximumQueryBytes     = 4096
	maximumJSONBodyBytes  = 4096
	maximumJSONDepth      = 16
	maximumJSONStringByte = 1024
)

// Application is an immutable Article API adapter. Routes returns detached
// route declarations whose handlers share this read-only configuration.
type Application struct {
	repository articleapp.Repository
	parser     api.Parser
	spec       serializers.Spec
	routes     []web.Route
}

// New validates every construction dependency before publishing any route.
func New(backend articleapp.Backend, authentication api.Authentication) (*Application, error) {
	repository, err := articleapp.NewRepository(backend)
	if err != nil {
		return nil, fmt.Errorf("article api repository: %w", err)
	}
	if nilAuthentication(authentication) {
		return nil, fmt.Errorf("article api authentication: adapter is nil")
	}
	parser, err := api.NewParser(api.ParserConfig{
		MaxBodyBytes: maximumJSONBodyBytes,
		JSONLimits: serializers.Limits{
			MaxDocumentBytes: maximumJSONBodyBytes,
			MaxDepth:         maximumJSONDepth,
			MaxStringBytes:   maximumJSONStringByte,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("article api parser: %w", err)
	}
	spec, err := articleSpec()
	if err != nil {
		return nil, fmt.Errorf("article api serializer: %w", err)
	}
	application := &Application{
		repository: repository,
		parser:     parser,
		spec:       spec,
	}
	routes, err := application.buildRoutes(authentication)
	if err != nil {
		return nil, err
	}
	application.routes = routes
	return application, nil
}

// Routes returns a detached declaration set. Every method has a unique name
// because the lower router treats names as reverse identities, not resources.
func (a *Application) Routes() []web.Route {
	if a == nil {
		return nil
	}
	return append([]web.Route(nil), a.routes...)
}

func (a *Application) buildRoutes(authentication api.Authentication) ([]web.Route, error) {
	declarations := []struct {
		name       string
		method     string
		path       string
		permission auth.Permission
		handler    api.AuthenticatedHandler
	}{
		{name: ListRouteName, method: http.MethodGet, path: ListPath, permission: articleapp.ArticleViewPermission, handler: a.list},
		{name: Namespace + ":article-list-head", method: http.MethodHead, path: ListPath, permission: articleapp.ArticleViewPermission, handler: a.listHead},
		{name: Namespace + ":article-list-options", method: http.MethodOptions, path: ListPath, permission: articleapp.ArticleViewPermission, handler: a.listOptions},
		{name: Namespace + ":article-create", method: http.MethodPost, path: ListPath, permission: articleapp.ArticleAddPermission, handler: a.create},
		{name: DetailRouteName, method: http.MethodGet, path: DetailPath, permission: articleapp.ArticleViewPermission, handler: a.retrieve},
		{name: Namespace + ":article-detail-head", method: http.MethodHead, path: DetailPath, permission: articleapp.ArticleViewPermission, handler: a.retrieveHead},
		{name: Namespace + ":article-detail-options", method: http.MethodOptions, path: DetailPath, permission: articleapp.ArticleViewPermission, handler: a.detailOptions},
		{name: Namespace + ":article-update", method: http.MethodPut, path: DetailPath, permission: articleapp.ArticleChangePermission, handler: a.update},
		{name: Namespace + ":article-partial-update", method: http.MethodPatch, path: DetailPath, permission: articleapp.ArticleChangePermission, handler: a.patch},
		{name: Namespace + ":article-delete", method: http.MethodDelete, path: DetailPath, permission: articleapp.ArticleDeletePermission, handler: a.delete},
	}

	routes := make([]web.Route, 0, len(declarations))
	for _, declaration := range declarations {
		handler, err := authentication.Require(declaration.permission, declaration.handler)
		if err != nil {
			return nil, fmt.Errorf("article api authentication route %q: %w", declaration.name, err)
		}
		if handler == nil {
			return nil, fmt.Errorf("article api authentication route %q: handler is nil", declaration.name)
		}
		routes = append(routes, web.Route{
			Name:    declaration.name,
			Method:  declaration.method,
			Path:    declaration.path,
			Handler: handler,
		})
	}
	return routes, nil
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

// Middleware constructs the API subtree representation and JSON negotiation
// chain in outermost-first Web order.
func Middleware() ([]web.Middleware, error) {
	negotiation, err := api.JSONNegotiation("/api/")
	if err != nil {
		return nil, err
	}
	representation, err := api.Representation("/api/")
	if err != nil {
		return nil, err
	}
	return []web.Middleware{negotiation, representation}, nil
}
