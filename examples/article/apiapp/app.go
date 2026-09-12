// Package apiapp publishes the bounded authentication-profile-protected Article JSON API.
// It owns explicit Article conversion and route composition while persistence,
// generic JSON primitives, and authentication policy remain in lower packages.
package apiapp

import (
	"fmt"
	"reflect"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/examples/article/articleapp"
	articlemodels "github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/web"
)

const (
	Namespace = "godj_conformance"

	ListPath   = "/api/articles/"
	DetailPath = "/api/articles/<int64:id>/"

	ListRouteName    = Namespace + ":article-list"
	DetailRouteName  = Namespace + ":article-detail"
	OpenAPIPath      = "/api/openapi.json"
	OpenAPIRouteName = Namespace + ":article-openapi"

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
	repository     articleapp.Repository
	parser         api.Parser
	spec           serializers.Spec
	encoder        serializers.ModelEncoder[articlemodels.Article]
	authentication api.Authentication
	operations     []openapi.Operation
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
	descriptor := articlemodels.ArticleDescriptor{}
	encoder, err := serializers.NewModelEncoder(spec, descriptor.Metadata(), descriptor.WriteFieldValue)
	if err != nil {
		return nil, fmt.Errorf("article api encoder: %w", err)
	}
	application := &Application{
		repository:     repository,
		parser:         parser,
		spec:           spec,
		encoder:        encoder,
		authentication: authentication,
	}
	operations, err := application.buildOperations(authentication)
	if err != nil {
		return nil, err
	}
	application.operations = operations
	return application, nil
}

// Routes returns a detached declaration set. Every method has a unique name
// because the lower router treats names as reverse identities, not resources.
func (a *Application) Routes() []web.Route {
	if a == nil {
		return nil
	}
	routes := make([]web.Route, len(a.operations))
	for index, operation := range a.operations {
		routes[index] = operation.Route
	}
	return routes
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
