// Package siteapp composes the Article development server's public surface and
// its explicitly provisioned Admin/JSON API surface. Credentials, sessions,
// and Admin audit history live in the explicitly migrated system schema. CSRF
// signing remains process-local unless startup injects a shared key ring.
package siteapp

import (
	"context"
	"errors"
	"fmt"

	"github.com/progresshans/godj/admin"
	apisessionauth "github.com/progresshans/godj/api/sessionauth"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/examples/article/adminapp"
	"github.com/progresshans/godj/examples/article/apiapp"
	"github.com/progresshans/godj/examples/article/internal/operatorconfig"
	"github.com/progresshans/godj/examples/article/webapp"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/systemstate"
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

type configState struct {
	backend                     systemstate.Backend
	csrfKeyRing                 websessionauth.CSRFKeyRing
	allowLoopbackAuthentication bool
}

// NewConfig copies the raw-password-free startup input into opaque immutable
// state. Without WithCSRFKeyRing, web/sessionauth retains its process-local key
// behavior. Authenticated publication remains disabled until the caller has
// proven that its listener is loopback-only.
func NewConfig(backend systemstate.Backend) Config {
	return Config{state: &configState{backend: backend}}
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

// New first verifies the exact migrated system state without a raw password.
// A clean credential-absent state produces the public Article application;
// an existing credential produces the composed public + Admin + JSON API
// application only when the caller asserted loopback publication.
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
	runtimeConfig, err := operatorconfig.RuntimeConfig()
	if err != nil {
		return nil, fmt.Errorf("article site application: operator policy: %w", err)
	}
	runtime, err := systemstate.OpenExisting(ctx, configured.backend, runtimeConfig)
	if err != nil {
		if exactCredentialAbsent(err) {
			application, publicErr := webapp.NewApplication(configured.backend)
			if publicErr != nil {
				return nil, fmt.Errorf("article site application: public-only composition: %w", publicErr)
			}
			return application, nil
		}
		return nil, fmt.Errorf("article site application: system state: %w", err)
	}
	if !configured.allowLoopbackAuthentication {
		return nil, errors.New("article site application: authenticated Admin/API mode requires a loopback listener")
	}
	projectSettings, err := settings.New(settings.Definition{
		ProjectName: "article_example",
		InstalledApps: []apps.Config{{
			Name:  "github.com/progresshans/godj/examples/article/models",
			Label: apiapp.Namespace,
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("article site application: settings: %w", err)
	}
	adminService, err := adminapp.NewDurableService(runtime, runtime)
	if err != nil {
		return nil, fmt.Errorf("article site application: Article Admin service: %w", err)
	}
	builder := admin.NewBuilder(projectSettings.Apps())
	if err := adminapp.RegisterArticle(builder, adminService); err != nil {
		return nil, fmt.Errorf("article site application: register Article Admin: %w", err)
	}
	registry, err := builder.Build()
	if err != nil {
		return nil, fmt.Errorf("article site application: build Admin registry: %w", err)
	}

	manager, err := sessions.NewManager(runtime.SessionStore(), sessions.Config{})
	if err != nil {
		return nil, fmt.Errorf("article site application: session manager: %w", err)
	}
	allowedNext, err := admin.SiteAllowedNextPaths(registry, adminBasePath)
	if err != nil {
		return nil, fmt.Errorf("article site application: Admin next paths: %w", err)
	}
	webRuntime, err := websessionauth.New(websessionauth.Config{
		Sessions:         manager,
		Authenticator:    runtime.Authenticator(),
		Authorizer:       auth.PrincipalAuthorizer{},
		SessionCookie:    websessionauth.CookieConfig{Path: "/", AllowInsecure: true},
		CSRFCookie:       websessionauth.CookieConfig{Path: "/", AllowInsecure: true},
		CSRFKeyRing:      configured.csrfKeyRing,
		LoginPath:        adminBasePath + "/login/",
		FallbackPath:     adminBasePath + "/",
		AllowedNextPaths: allowedNext,
	})
	if err != nil {
		return nil, fmt.Errorf("article site application: session authentication: %w", err)
	}
	adminSite, err := admin.NewSite(admin.SiteConfig{
		Apps:      projectSettings.Apps(),
		Namespace: apiapp.Namespace,
		BasePath:  adminBasePath,
		Registry:  registry,
		Auth:      webRuntime,
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
	articleAPI, err := apiapp.New(runtime, apiRuntime)
	if err != nil {
		return nil, fmt.Errorf("article site application: Article API: %w", err)
	}
	middleware, err := apiapp.Middleware()
	if err != nil {
		return nil, fmt.Errorf("article site application: API middleware: %w", err)
	}
	routes := append(adminSite.Routes(), articleAPI.Routes()...)
	application, err := webapp.NewComposedApplication(runtime, routes, middleware)
	if err != nil {
		return nil, fmt.Errorf("article site application: compose Web application: %w", err)
	}
	return application, nil
}

func exactCredentialAbsent(err error) bool {
	var stateError *systemstate.Error
	return errors.As(err, &stateError) && stateError != nil &&
		stateError.Code == systemstate.CodeCredentialAbsent && stateError.Field == "credential"
}
