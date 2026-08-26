// Package siteapp composes the Article development server's opt-in Admin and
// JSON API surface. Credentials, sessions, and Admin audit history live in the
// explicitly migrated system schema. CSRF signing remains process-local unless
// startup explicitly injects one deployment-shared key ring.
package siteapp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/progresshans/godj/admin"
	apisessionauth "github.com/progresshans/godj/api/sessionauth"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/examples/article/adminapp"
	"github.com/progresshans/godj/examples/article/apiapp"
	"github.com/progresshans/godj/examples/article/articleapp"
	"github.com/progresshans/godj/examples/article/webapp"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/web"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

const (
	adminBasePath       = "/admin"
	developmentAdminID  = "article-development-admin"
	maximumSessions     = 256
	maximumAuditEntries = 1024
)

const configDiagnostic = "siteapp.Config{redacted}"

// Config is an opaque immutable startup value. Secret-bearing backend,
// password, and CSRF key state remains behind an unexported pointer so generic
// formatting cannot recursively inspect it.
type Config struct {
	state *configState
}

type configState struct {
	backend     systemstate.Backend
	username    string
	password    string
	csrfKeyRing websessionauth.CSRFKeyRing
}

// NewConfig copies the required startup input into opaque immutable state.
// Without WithCSRFKeyRing, web/sessionauth retains its process-local key
// behavior.
func NewConfig(backend systemstate.Backend, username, password string) Config {
	return Config{state: &configState{
		backend:  backend,
		username: username,
		password: password,
	}}
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

// New builds one public Web + Admin + JSON API application. AllowInsecure is
// explicit because cmd/site permits this mode only on a loopback listener.
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
	if strings.TrimSpace(configured.username) == "" {
		return nil, errors.New("article site application: configured username is empty")
	}
	if strings.TrimSpace(configured.password) == "" {
		return nil, errors.New("article site application: configured password is empty")
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
	hasher, err := auth.NewDefaultPBKDF2()
	if err != nil {
		return nil, fmt.Errorf("article site application: password profile: %w", err)
	}
	runtime, err := systemstate.Open(ctx, configured.backend, systemstate.BootstrapConfig{
		Username:    configured.username,
		Password:    configured.password,
		PrincipalID: developmentAdminID,
		Active:      true,
		Permissions: []auth.Permission{
			admin.DefaultAccessPermission,
			articleapp.ArticleViewPermission,
			articleapp.ArticleAddPermission,
			articleapp.ArticleChangePermission,
			articleapp.ArticleDeletePermission,
		},
		PasswordHasher: hasher,
		MaxSessions:    maximumSessions,
		AuditCapacity:  maximumAuditEntries,
	})
	if err != nil {
		return nil, fmt.Errorf("article site application: system state: %w", err)
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
