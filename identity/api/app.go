// Package identityapi adapts the identity manager to authenticated JSON HTTP.
// Schema IR owns model fields; this package owns exposure, routes and HTTP
// preconditions. The manager rechecks current authority inside each DB scope.
package identityapi

import (
	"fmt"
	"reflect"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/web"
)

const BasePath = "/api/identity/"

// Config requires the complete HOST relation policy, including its external
// references to identity models. No identity-only deletion policy is inferred.
// The API uses model permissions; active staff admission belongs to Admin.
type Config struct {
	Namespace      string
	Backend        identity.ManagementBackend
	PasswordHasher auth.PasswordHasher
	Authorizer     auth.Authorizer
	Authentication api.AlternativeAuthentication
	Users          orm.RelationDeleter[models.User]
	Groups         orm.RelationDeleter[models.Group]
	Permissions    orm.RelationDeleter[models.Permission]
}

func (Config) MarshalJSON() ([]byte, error) { return []byte(`"identityapi.Config{redacted}"`), nil }

func (Config) Format(s fmt.State, _ rune) { _, _ = s.Write([]byte("identityapi.Config{redacted}")) }

type Application struct {
	namespace    string
	manager      *identity.Manager
	users        orm.RelationDeleter[models.User]
	groups       orm.RelationDeleter[models.Group]
	permissions  orm.RelationDeleter[models.Permission]
	parser       api.Parser
	policy       api.JSONPolicy
	document     openapi.Document
	resources    []resource
	password     serializers.Spec
	userSummary  serializers.ModelEncoder[identity.UserDetails]
	userDetail   serializers.ModelEncoder[identity.UserDetails]
	groupSummary serializers.ModelEncoder[identity.GroupDetails]
	groupDetail  serializers.ModelEncoder[identity.GroupDetails]
	permission   serializers.ModelEncoder[identity.PermissionProfile]
}

func (Application) Format(s fmt.State, _ rune) {
	_, _ = s.Write([]byte("identityapi.Application{redacted}"))
}

type resource struct {
	name                      string
	title                     string
	view, add, change, remove auth.Permission
	create, update            serializers.Spec
	scalar, detail            serializers.Spec
}

// New prepares serializers, routes and their OpenAPI document without I/O.
func New(config Config) (*Application, error) {
	if nilAuthentication(config.Authentication) {
		return nil, fmt.Errorf("identity API: authentication is nil")
	}
	manager, err := identity.NewManager(config.Backend, config.PasswordHasher, config.Authorizer)
	if err != nil {
		return nil, err
	}
	for _, check := range []func() error{config.Users.ValidateBinding, config.Groups.ValidateBinding, config.Permissions.ValidateBinding} {
		if err := check(); err != nil {
			return nil, fmt.Errorf("identity API: host deletion policy: %w", err)
		}
	}
	policy, err := api.NewJSONPolicy(BasePath)
	if err != nil {
		return nil, err
	}
	parser, err := api.NewParser(api.ParserConfig{MaxBodyBytes: 64 * 1024, JSONLimits: serializers.Limits{MaxStringBytes: 4096}})
	if err != nil {
		return nil, err
	}
	a := &Application{namespace: config.Namespace, manager: manager, users: config.Users, groups: config.Groups, permissions: config.Permissions, parser: parser, policy: policy}
	if err := a.prepareSpecs(); err != nil {
		return nil, err
	}
	if err := a.prepareEncoders(); err != nil {
		return nil, err
	}
	a.document, err = a.describe(config.Authentication)
	if err != nil {
		return nil, err
	}
	return a, nil
}

func (a *Application) Routes() []web.Route {
	if a == nil {
		return nil
	}
	return a.document.Routes()
}
func (a *Application) Middleware() []web.Middleware {
	if a == nil {
		return nil
	}
	return a.policy.Middleware()
}
func (a *Application) OpenAPI() (openapi.Document, error) {
	if a == nil || len(a.document.Bytes()) == 0 {
		return openapi.Document{}, fmt.Errorf("identity API: application is nil")
	}
	return a.document, nil
}

func nilAuthentication(value api.AlternativeAuthentication) bool {
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
