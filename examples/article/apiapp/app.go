// Package apiapp publishes the bounded session-authenticated Article JSON API.
// It owns explicit Article conversion and route composition while persistence,
// generic JSON primitives, and session policy remain in their lower packages.
package apiapp

import (
	"fmt"
	"net/http"

	"github.com/progresshans/godj/api"
	apisessionauth "github.com/progresshans/godj/api/sessionauth"
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
	auth       *apisessionauth.Runtime
	parser     api.Parser
	spec       serializers.Spec
	routes     []web.Route
}

// New validates every construction dependency before publishing any route.
func New(backend articleapp.Backend, authentication *apisessionauth.Runtime) (*Application, error) {
	repository, err := articleapp.NewRepository(backend)
	if err != nil {
		return nil, fmt.Errorf("article api repository: %w", err)
	}
	if authentication == nil {
		return nil, fmt.Errorf("article api authentication: runtime is nil")
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
		auth:       authentication,
		parser:     parser,
		spec:       spec,
	}
	application.routes = application.buildRoutes()
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

func (a *Application) buildRoutes() []web.Route {
	return []web.Route{
		{Name: ListRouteName, Method: http.MethodGet, Path: ListPath, Handler: a.auth.Require(articleapp.ArticleViewPermission, a.list)},
		{Name: Namespace + ":article-list-head", Method: http.MethodHead, Path: ListPath, Handler: a.auth.Require(articleapp.ArticleViewPermission, a.listHead)},
		{Name: Namespace + ":article-list-options", Method: http.MethodOptions, Path: ListPath, Handler: a.auth.Require(articleapp.ArticleViewPermission, a.listOptions)},
		{Name: Namespace + ":article-create", Method: http.MethodPost, Path: ListPath, Handler: a.auth.Require(articleapp.ArticleAddPermission, a.create)},
		{Name: DetailRouteName, Method: http.MethodGet, Path: DetailPath, Handler: a.auth.Require(articleapp.ArticleViewPermission, a.retrieve)},
		{Name: Namespace + ":article-detail-head", Method: http.MethodHead, Path: DetailPath, Handler: a.auth.Require(articleapp.ArticleViewPermission, a.retrieveHead)},
		{Name: Namespace + ":article-detail-options", Method: http.MethodOptions, Path: DetailPath, Handler: a.auth.Require(articleapp.ArticleViewPermission, a.detailOptions)},
		{Name: Namespace + ":article-update", Method: http.MethodPut, Path: DetailPath, Handler: a.auth.Require(articleapp.ArticleChangePermission, a.update)},
		{Name: Namespace + ":article-partial-update", Method: http.MethodPatch, Path: DetailPath, Handler: a.auth.Require(articleapp.ArticleChangePermission, a.patch)},
		{Name: Namespace + ":article-delete", Method: http.MethodDelete, Path: DetailPath, Handler: a.auth.Require(articleapp.ArticleDeletePermission, a.delete)},
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
