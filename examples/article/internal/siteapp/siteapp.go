// Package siteapp composes the Article development server's public surface and
// its explicitly provisioned Admin/JSON API surface. Credentials, sessions,
// and Admin audit history live in explicitly migrated account storage. CSRF
// signing remains process-local unless startup injects a shared key ring.
package siteapp

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/progresshans/godj/admin"
	apisessionauth "github.com/progresshans/godj/api/sessionauth"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/article/adminapp"
	"github.com/progresshans/godj/examples/article/apiapp"
	"github.com/progresshans/godj/examples/article/articleapp"
	"github.com/progresshans/godj/examples/article/internal/accounts"
	"github.com/progresshans/godj/examples/article/project"
	"github.com/progresshans/godj/examples/article/webapp"
	"github.com/progresshans/godj/identity"
	identityaccount "github.com/progresshans/godj/identity/account"
	identityadmin "github.com/progresshans/godj/identity/admin"
	identityapi "github.com/progresshans/godj/identity/api"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/mail"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

const (
	adminBasePath = "/admin"
)

const configDiagnostic = "siteapp.Config{redacted}"

// Config is an opaque immutable startup value. The backend and CSRF key state
// remain behind an unexported pointer so generic formatting cannot recursively
// inspect them. Config never contains an operator username or raw password.
type Config struct {
	state *configState
}

// Backend supplies the exact model, native-scope and installation capabilities
// consumed by this host. Its lifetime remains with the server or caller.
type Backend interface {
	articleapp.Backend
	db.SnapshotReader
	db.CoordinatedAtomic
	migrationbackend.AppliedMigrationReader
}

type configState struct {
	backend                     Backend
	csrfKeyRing                 websessionauth.CSRFKeyRing
	allowLoopbackAuthentication bool
	passwordValidators          []accountmodel.PasswordValidator[models.User]
	passwordReset               *passwordResetConfiguration
}

type passwordResetConfiguration struct {
	reset  accountmodel.RuntimeResetConfig
	sender mail.Sender
	mail   identity.PasswordResetMailConfig
	report identityaccount.PasswordResetErrorReporter
}

// WithPasswordReset enables the ordinary-account reset routes with explicit
// signing keys, recipient-link origin, sender and private failure reporting.
// The same typed password policy applies to creation, change and reset.
// No sender, signing key or delivery URL is inferred from requests.
func (config Config) WithPasswordReset(reset accountmodel.RuntimeResetConfig, sender mail.Sender, mailConfig identity.PasswordResetMailConfig, report identityaccount.PasswordResetErrorReporter) Config {
	var configured configState
	if config.state != nil {
		configured = *config.state
	}
	configured.passwordReset = &passwordResetConfiguration{reset, sender, mailConfig, report}
	return Config{state: &configured}
}

// NewConfig copies the raw-password-free startup input into opaque immutable
// state. Without WithCSRFKeyRing, web/sessionauth retains its process-local key
// behavior. Authenticated publication remains disabled until the caller has
// proven that its listener is loopback-only.
func NewConfig(backend Backend) Config {
	return Config{state: &configState{backend: backend}}
}

// WithPasswordValidators applies one explicit identity policy to both Admin
// and JSON API. Caller-owned slices are copied before this config is published.
func (config Config) WithPasswordValidators(validators ...accountmodel.PasswordValidator[models.User]) Config {
	var configured configState
	if config.state != nil {
		configured = *config.state
	}
	configured.passwordValidators = append([]accountmodel.PasswordValidator[models.User](nil), validators...)
	return Config{state: &configured}
}

// WithCSRFKeyRing returns an immutable copy configured with an already-loaded
// deployment key ring. The ring remains opaque and has no material accessor.
func (config Config) WithCSRFKeyRing(csrfKeyRing websessionauth.CSRFKeyRing) Config {
	var configured configState
	if config.state != nil {
		configured = *config.state
	}
	configured.csrfKeyRing = csrfKeyRing
	return Config{state: &configured}
}

// WithLoopbackAuthentication returns an immutable copy whose caller has
// established that authenticated Admin/API routes will be published only on a
// loopback listener. A migrated clean credential-absent state remains
// public-only regardless of this flag.
func (config Config) WithLoopbackAuthentication() Config {
	var configured configState
	if config.state != nil {
		configured = *config.state
	}
	configured.allowLoopbackAuthentication = true
	return Config{state: &configured}
}

func (Config) String() string   { return configDiagnostic }
func (Config) GoString() string { return configDiagnostic }

// Format makes ordinary fmt verbs, flags, widths, and precisions
// secret-independent. Go reserves invalid/special %p and %w paths before this
// hook; Config's pointer-backed opaque state keeps those fallbacks secret-free.
func (Config) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte(configDiagnostic))
}

// MarshalJSON never publishes even the shape of secret-bearing startup state.
func (Config) MarshalJSON() ([]byte, error) {
	return []byte(`"siteapp.Config{redacted}"`), nil
}

// New inspects explicitly installed selected-account storage without a raw
// password. Only an exact fresh observation permits public-only startup. A
// ready domain publishes Admin/API only on an explicitly loopback listener.
// A missing schema or mismatched historical domain is an error, never adoption.
func New(ctx context.Context, config Config) (*web.Application, error) {
	if ctx == nil {
		return nil, errors.New("article site application: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var configured configState
	if config.state != nil {
		configured = *config.state
	}
	runtimeConfig, err := accounts.Policy()
	if err != nil {
		return nil, fmt.Errorf("article site application: account policy: %w", err)
	}
	validators := append([]accountmodel.PasswordValidator[models.User]{runtimeConfig.Accounts.ValidatePassword}, configured.passwordValidators...)
	for _, validator := range validators {
		if validator == nil {
			return nil, errors.New("article site application: nil password validator")
		}
	}
	runtimeConfig.Accounts.ValidatePassword = func(ctx context.Context, raw string, value models.User) (validation.Errors, error) {
		var failures validation.Errors
		for _, validator := range validators {
			if err := ctx.Err(); err != nil {
				return validation.Errors{}, err
			}
			result, err := validator(ctx, raw, value)
			if err != nil {
				return validation.Errors{}, err
			}
			failures = failures.Append(result)
		}
		return failures, ctx.Err()
	}
	hasher, err := auth.NewDefaultPBKDF2()
	if err != nil {
		return nil, err
	}
	runtime, err := project.NewAccountRuntime(ctx, configured.backend, hasher, auth.PrincipalAuthorizer{}, runtimeConfig)
	if err != nil {
		return nil, fmt.Errorf("article site application: account runtime: %w", err)
	}
	inspection, err := runtime.InspectStartup(ctx)
	if err != nil {
		return nil, fmt.Errorf("article site application: account storage: %w", err)
	}
	if inspection.Fresh() {
		application, err := webapp.NewApplication(configured.backend)
		if err != nil {
			return nil, fmt.Errorf("article site application: public-only composition: %w", err)
		}
		return application, nil
	}
	if !inspection.Ready() {
		return nil, errors.New("article site application: account storage is unavailable")
	}
	if !configured.allowLoopbackAuthentication {
		return nil, errors.New("article site application: authenticated Admin/API mode requires a loopback listener")
	}
	projectSettings, err := settings.New(settings.Definition{
		ProjectName:   "article_example",
		InstalledApps: project.InstalledApps(),
	})
	if err != nil {
		return nil, fmt.Errorf("article site application: settings: %w", err)
	}
	adminService, err := adminapp.NewSelectedService(configured.backend, runtime.Credentials(), auth.PrincipalAuthorizer{})
	if err != nil {
		return nil, fmt.Errorf("article site application: Article Admin service: %w", err)
	}
	builder := admin.NewBuilder(projectSettings.Apps())
	if err := adminapp.RegisterArticle(builder, adminService); err != nil {
		return nil, fmt.Errorf("article site application: register Article Admin: %w", err)
	}
	if err := identityadmin.RegisterAccount(builder, runtime.Accounts(), identityadmin.AccountRegistration{
		Slug: "users", ListFields: []string{"id", "username", "email", "active", "staff", "superuser"},
	}); err != nil {
		return nil, fmt.Errorf("article site application: account Admin: %w", err)
	}
	if err := identityadmin.RegisterCatalog(builder, runtime.Catalog(), identityadmin.CatalogRegistration{
		Groups:      identityadmin.CatalogPresentation{Slug: "groups", ListFields: []string{"id", "name"}},
		Permissions: identityadmin.CatalogPresentation{Slug: "permissions", ListFields: []string{"id", "code", "name"}},
	}); err != nil {
		return nil, fmt.Errorf("article site application: catalog Admin: %w", err)
	}
	registry, err := builder.Build()
	if err != nil {
		return nil, fmt.Errorf("article site application: build Admin registry: %w", err)
	}

	manager := runtime.Sessions()
	allowedNext, err := admin.SiteAllowedNextPaths(registry, adminBasePath)
	if err != nil {
		return nil, fmt.Errorf("article site application: Admin next paths: %w", err)
	}
	accountNext, err := identityaccount.AllowedNextPaths(identityaccount.DefaultBasePath)
	if err != nil {
		return nil, err
	}
	allowedNext = append(allowedNext, accountNext...)
	loginPersistence := runtime.LoginPersistence()
	passwordChangePersistence := runtime.PasswordChangePersistence()
	var resetPersistence auth.PasswordResetPersistence
	var resetSurface *identityaccount.PasswordResetConfig
	if reset := configured.passwordReset; reset != nil {
		persistence, err := runtime.PasswordResetter(reset.reset)
		if err != nil {
			return nil, fmt.Errorf("article site application: password reset persistence: %w", err)
		}
		mailer, err := identity.NewPasswordResetMailer(persistence, reset.sender, reset.mail)
		if err != nil {
			return nil, fmt.Errorf("article site application: password reset mail: %w", err)
		}
		resetPersistence = persistence
		resetSurface = &identityaccount.PasswordResetConfig{Mailer: mailer, ReportError: reset.report}
	}
	webRuntime, err := websessionauth.New(websessionauth.Config{
		Sessions:                  manager,
		Authenticator:             runtime.Authenticator(),
		LoginPersistence:          loginPersistence,
		PasswordChangePersistence: passwordChangePersistence,
		PasswordResetPersistence:  resetPersistence,
		Authorizer:                auth.PrincipalAuthorizer{},
		SessionCookie:             websessionauth.CookieConfig{Path: "/", AllowInsecure: true},
		CSRFCookie:                websessionauth.CookieConfig{Path: "/", AllowInsecure: true},
		CSRFKeyRing:               configured.csrfKeyRing,
		LoginPath:                 adminBasePath + "/login/",
		FallbackPath:              adminBasePath + "/",
		AllowedNextPaths:          allowedNext,
	})
	if err != nil {
		return nil, fmt.Errorf("article site application: session authentication: %w", err)
	}
	adminSite, err := admin.NewSite(admin.SiteConfig{
		Apps:                projectSettings.Apps(),
		Namespace:           apiapp.Namespace,
		BasePath:            adminBasePath,
		Registry:            registry,
		Auth:                webRuntime,
		AdditionalNextPaths: accountNext,
	})
	if err != nil {
		return nil, fmt.Errorf("article site application: Admin site: %w", err)
	}
	apiRuntime, err := apisessionauth.New(webRuntime)
	if err != nil {
		return nil, fmt.Errorf("article site application: API authentication: %w", err)
	}
	// API writes deliberately use the durable runtime backend without the Admin
	// mutation hook, so they persist Articles but do not synthesize Admin audit.
	articleAPI, err := apiapp.NewSelected(configured.backend, runtime.Credentials(), auth.PrincipalAuthorizer{}, apiRuntime)
	if err != nil {
		return nil, fmt.Errorf("article site application: Article API: %w", err)
	}
	document, err := articleAPI.OpenAPI()
	if err != nil {
		return nil, fmt.Errorf("article site application: Article OpenAPI: %w", err)
	}
	schemaHandler, err := apiRuntime.Require(articleapp.ArticleViewPermission, func(*web.Request, auth.Principal) (web.Response, error) {
		return document.Response()
	})
	if err != nil {
		return nil, fmt.Errorf("article site application: Article OpenAPI authentication: %w", err)
	}
	identityAPI, err := identityapi.NewManagement(runtime.Accounts(), runtime.Catalog(), identityapi.ManagementConfig{Namespace: apiapp.Namespace, Authentication: apiRuntime})
	if err != nil {
		return nil, fmt.Errorf("article site application: identity API: %w", err)
	}
	identityDocument, err := identityAPI.OpenAPI()
	if err != nil {
		return nil, err
	}
	identitySchema, err := apiRuntime.RequireAny(identity.ViewUser, func(*web.Request, auth.Principal) (web.Response, error) { return identityDocument.Response() }, identity.ChangeUser, identity.ViewGroup, identity.ChangeGroup, identity.ViewPermission, identity.ChangePermission)
	if err != nil {
		return nil, err
	}
	account, err := identityaccount.New(identityaccount.Config{Apps: projectSettings.Apps(), Namespace: apiapp.Namespace, Auth: webRuntime, PasswordReset: resetSurface})
	if err != nil {
		return nil, fmt.Errorf("article site application: account surface: %w", err)
	}
	routes := append(adminSite.Routes(), articleAPI.Routes()...)
	routes = append(routes, identityAPI.Routes()...)
	routes = append(routes, account.Routes()...)
	routes = append(routes, web.Route{Name: apiapp.Namespace + ":identity-openapi", Method: http.MethodGet, Path: identityapi.BasePath + "openapi.json", Handler: identitySchema})
	routes = append(routes, web.Route{
		Name:    apiapp.OpenAPIRouteName,
		Method:  http.MethodGet,
		Path:    apiapp.OpenAPIPath,
		Handler: schemaHandler,
	})
	middleware := append(articleAPI.Middleware(), identityAPI.Middleware()...)
	middleware = append(middleware, account.Middleware()...)
	application, err := webapp.NewComposedApplication(configured.backend, routes, middleware)
	if err != nil {
		return nil, fmt.Errorf("article site application: compose Web application: %w", err)
	}
	return application, nil
}
