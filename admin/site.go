package admin

import (
	"crypto/rand"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	pathpkg "path"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/templates"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

const (
	DefaultBasePath      = "/admin"
	MaximumFormBodyBytes = 64 * 1024
	MaximumQueryBytes    = 4 * 1024
	MaximumInputValues   = 128
	MaximumInputBytes    = 4 * 1024
	MaximumBasePathBytes = 128
)

const DefaultAccessPermission auth.Permission = "godj.admin.access"

//go:embed site_templates/*.html
var siteTemplateFiles embed.FS

// SiteConfig binds one immutable Registry to the static Web router and one
// explicit session-auth runtime. The runtime's login and fallback paths must
// be configured to BasePath/login/ and BasePath/ respectively.
type SiteConfig struct {
	Apps      apps.Registry
	Namespace string
	BasePath  string
	Registry  Registry
	Auth      *sessionauth.Runtime
	PageSize  int
	// AccessPermission distinguishes an authenticated non-admin principal
	// (redirect to login) from an admin principal lacking a model permission
	// (403). Zero selects DefaultAccessPermission.
	AccessPermission auth.Permission
	Random           io.Reader
}

// Site is an immutable concurrent-use Admin HTTP route collection.
type Site struct {
	namespace string
	basePath  string
	registry  Registry
	auth      *sessionauth.Runtime
	templates *templates.Engine
	routes    []web.Route
	pageSize  int
	access    auth.Permission
	noticeKey [32]byte
}

// NewSite validates and compiles the complete bounded Admin route and
// template surface before publishing any handler.
func NewSite(config SiteConfig) (*Site, error) {
	if _, ok := config.Apps.Lookup(config.Namespace); !ok {
		return nil, &ConfigError{Path: "site.namespace", Code: "not_installed"}
	}
	basePath, err := normalizeBasePath(config.BasePath)
	if err != nil {
		return nil, err
	}
	if config.Auth == nil {
		return nil, &ConfigError{Path: "site.auth", Code: "missing"}
	}
	pageSize := config.PageSize
	if pageSize == 0 {
		pageSize = DefaultListLimit
	}
	if pageSize < 1 || pageSize > MaximumListLimit {
		return nil, &ConfigError{Path: "site.page_size", Code: "invalid"}
	}
	access := config.AccessPermission
	if access == "" {
		access = DefaultAccessPermission
	}
	validatedAccess, err := auth.NewPermission(string(access))
	if err != nil || validatedAccess != access {
		return nil, &ConfigError{Path: "site.access_permission", Code: "invalid", Cause: err}
	}
	random := config.Random
	if random == nil {
		random = rand.Reader
	}
	var noticeKey [32]byte
	if _, err := io.ReadFull(random, noticeKey[:]); err != nil {
		return nil, &ConfigError{Path: "site.random", Code: "entropy_failure", Cause: err}
	}
	if len(config.Registry.models) == 0 || len(config.Registry.byIdentity) != len(config.Registry.models) ||
		len(config.Registry.bySlug) != len(config.Registry.models) {
		return nil, &ConfigError{Path: "site.registry", Code: "invalid"}
	}
	templateFS, err := fs.Sub(siteTemplateFiles, "site_templates")
	if err != nil {
		return nil, &ConfigError{Path: "site.templates", Code: "invalid", Cause: err}
	}
	engine, err := templates.New(templateFS, templates.Config{})
	if err != nil {
		return nil, &ConfigError{Path: "site.templates", Code: "invalid", Cause: err}
	}
	site := &Site{
		namespace: config.Namespace,
		basePath:  basePath,
		registry:  config.Registry,
		auth:      config.Auth,
		templates: engine,
		pageSize:  pageSize,
		access:    access,
		noticeKey: noticeKey,
	}
	if site.auth.LoginPath() != basePath+"/login/" || site.auth.FallbackPath() != basePath+"/" {
		return nil, &ConfigError{Path: "site.auth.redirect_paths", Code: "mismatch"}
	}
	if !site.auth.CookiesApplyTo(basePath + "/") {
		return nil, &ConfigError{Path: "site.auth.cookie_paths", Code: "mismatch"}
	}
	expectedNext, err := SiteAllowedNextPaths(site.registry, basePath)
	if err != nil {
		return nil, err
	}
	actualNext := site.auth.AllowedNextPaths()
	if len(actualNext) != len(expectedNext) {
		return nil, &ConfigError{Path: "site.auth.allowed_next_paths", Code: "mismatch"}
	}
	for _, expected := range expectedNext {
		if !site.auth.AllowsNext(expected) {
			return nil, &ConfigError{Path: "site.auth.allowed_next_paths", Code: "mismatch"}
		}
	}
	if err := site.buildRoutes(); err != nil {
		return nil, err
	}
	if err := site.validateRoutesAndNextPaths(); err != nil {
		return nil, err
	}
	return site, nil
}

// Routes returns a detached route declaration slice. Handler closures retain
// only the immutable Site snapshot and registered callbacks.
func (site *Site) Routes() []web.Route {
	if site == nil {
		return nil
	}
	return append([]web.Route(nil), site.routes...)
}

func (site *Site) buildRoutes() error {
	if site == nil || site.auth == nil || site.templates == nil {
		return &ConfigError{Path: "site", Code: "invalid"}
	}
	name := func(suffix string) string { return site.namespace + ":admin-" + suffix }
	loginPath := site.basePath + "/login/"
	logoutPath := site.basePath + "/logout/"
	indexPath := site.basePath + "/"
	routes := []web.Route{
		{Name: name("login-get"), Method: http.MethodGet, Path: loginPath, Handler: site.auth.Optional(site.loginGet)},
		{Name: name("login-post"), Method: http.MethodPost, Path: loginPath, Handler: site.loginPost},
		{Name: name("logout-post"), Method: http.MethodPost, Path: logoutPath, Handler: site.logoutPost},
		{Name: name("index"), Method: http.MethodGet, Path: indexPath, Handler: site.adminRequire("", site.indexGet)},
	}
	for index := range site.registry.models {
		model := site.registry.models[index]
		prefix := site.basePath + "/" + model.slug
		modelName := func(suffix string) string { return name(model.slug + "-" + suffix) }
		routes = append(routes,
			web.Route{Name: modelName("list"), Method: http.MethodGet, Path: prefix + "/", Handler: site.adminRequire(model.permissions.View, site.modelList(model))},
			web.Route{Name: modelName("add-get"), Method: http.MethodGet, Path: prefix + "/add/", Handler: site.adminRequire(model.permissions.Add, site.modelAddGet(model))},
			web.Route{Name: modelName("add-post"), Method: http.MethodPost, Path: prefix + "/add/", Handler: site.modelAddPost(model)},
			web.Route{Name: modelName("change-get"), Method: http.MethodGet, Path: prefix + "/change/", Handler: site.adminRequire(model.permissions.Change, site.modelChangeGet(model))},
			web.Route{Name: modelName("change-post"), Method: http.MethodPost, Path: prefix + "/change/", Handler: site.modelChangePost(model)},
			web.Route{Name: modelName("delete-get"), Method: http.MethodGet, Path: prefix + "/delete/", Handler: site.adminRequire(model.permissions.Delete, site.modelDeleteGet(model))},
			web.Route{Name: modelName("delete-post"), Method: http.MethodPost, Path: prefix + "/delete/", Handler: site.modelDeletePost(model)},
			web.Route{Name: modelName("history"), Method: http.MethodGet, Path: prefix + "/history/", Handler: site.adminRequire(model.permissions.View, site.modelHistory(model))},
		)
		for actionIndex := range model.actions {
			action := model.actions[actionIndex]
			routes = append(routes, web.Route{
				Name:    modelName("action-" + action.name),
				Method:  http.MethodPost,
				Path:    prefix + "/action/" + action.name + "/",
				Handler: site.modelAction(model, action),
			})
		}
	}
	site.routes = append([]web.Route(nil), routes...)
	return nil
}

func normalizeBasePath(value string) (string, error) {
	if value == "" {
		value = DefaultBasePath
	}
	if value == "/" || len(value) > MaximumBasePathBytes || !utf8.ValidString(value) || !strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") ||
		pathpkg.Clean(value) != value || strings.ContainsAny(value, "%?#\\{}:<>*") {
		return "", &ConfigError{Path: "site.base_path", Code: "invalid"}
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return "", &ConfigError{Path: "site.base_path", Code: "invalid"}
		}
	}
	return value, nil
}

// SiteAllowedNextPaths returns the exact static GET paths a session-auth
// Runtime must accept before it can be attached to NewSite.
func SiteAllowedNextPaths(registry Registry, basePath string) ([]string, error) {
	basePath, err := normalizeBasePath(basePath)
	if err != nil {
		return nil, err
	}
	if len(registry.models) == 0 {
		return nil, &ConfigError{Path: "site.registry", Code: "invalid"}
	}
	paths := []string{basePath + "/"}
	for _, model := range registry.models {
		prefix := basePath + "/" + model.slug
		paths = append(paths, prefix+"/", prefix+"/add/", prefix+"/change/", prefix+"/delete/", prefix+"/history/")
	}
	return paths, nil
}

func (site *Site) validateRoutesAndNextPaths() error {
	byName := make(map[string]struct{}, len(site.routes))
	byMethodPath := make(map[string]struct{}, len(site.routes))
	for _, route := range site.routes {
		if _, duplicate := byName[route.Name]; duplicate {
			return &ConfigError{Path: "site.routes.name", Code: "duplicate"}
		}
		byName[route.Name] = struct{}{}
		key := route.Method + " " + route.Path
		if _, duplicate := byMethodPath[key]; duplicate {
			return &ConfigError{Path: "site.routes.method_path", Code: "duplicate"}
		}
		byMethodPath[key] = struct{}{}
		if route.Method == http.MethodGet && route.Path != site.basePath+"/login/" && !site.auth.AllowsNext(route.Path) {
			return &ConfigError{Path: "site.auth.allowed_next_paths", Code: "missing", Cause: fmt.Errorf("path %q", route.Path)}
		}
	}
	return nil
}

func (site *Site) modelPath(model registeredModel) string {
	return site.basePath + "/" + model.slug + "/"
}

func (site *Site) String() string {
	if site == nil {
		return "admin.Site{nil}"
	}
	return fmt.Sprintf("admin.Site{models:%d}", len(site.registry.models))
}
