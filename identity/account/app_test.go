package identityaccount

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web/sessionauth"
	"strings"
	"testing"
)

type constructionGuard struct {
	t       *testing.T
	manager *sessions.Manager
}

func (g constructionGuard) Authenticate(context.Context, string, string) (auth.Credential, error) {
	g.t.Fatal("startup authenticated")
	return auth.Credential{}, nil
}
func (g constructionGuard) Resolve(context.Context, string) (auth.Credential, error) {
	g.t.Fatal("startup resolved")
	return auth.Credential{}, nil
}
func (g constructionGuard) Allowed(context.Context, auth.Principal, auth.Permission) (bool, error) {
	g.t.Fatal("startup authorized")
	return false, nil
}
func (g constructionGuard) Sessions() *sessions.Manager { return g.manager }
func (g constructionGuard) CheckPasswordChange(context.Context, sessions.ID, *string, *string) error {
	g.t.Fatal("startup checked password")
	return nil
}
func (g constructionGuard) ChangePassword(context.Context, sessions.ID, string, string) (auth.PasswordChangeResult, error) {
	g.t.Fatal("startup changed password")
	return auth.PasswordChangeResult{}, nil
}
func TestAccountConstructionValidatesCompositionWithoutPersistence(t *testing.T) {
	for _, mode := range []string{"default", "custom", "namespace", "missing_password", "cookie_path", "allowed_next", "root", "parameter", "overlap", "trailing", "escape"} {
		t.Run(mode, func(t *testing.T) {
			store, err := sessions.NewMemoryStore(4)
			if err != nil {
				t.Fatal(err)
			}
			manager, err := sessions.NewManager(store, sessions.Config{})
			if err != nil {
				t.Fatal(err)
			}
			guard := constructionGuard{t, manager}
			base, apiBase := "", ""
			if mode == "custom" {
				base, apiBase = "/my-account", "/my-api"
			}
			paths, err := AllowedNextPaths(base)
			if err != nil {
				t.Fatal(err)
			}
			webConfig := sessionauth.Config{Sessions: manager, Authenticator: guard, Authorizer: guard, PasswordChangePersistence: guard, LoginPath: "/login/", FallbackPath: paths[0], AllowedNextPaths: paths}
			if mode == "missing_password" {
				webConfig.PasswordChangePersistence = nil
			}
			if mode == "cookie_path" {
				webConfig.SessionCookie.Path = "/admin"
			}
			if mode == "allowed_next" {
				webConfig.FallbackPath = "/"
				webConfig.AllowedNextPaths = []string{"/"}
			}
			runtime, err := sessionauth.New(webConfig)
			if err != nil {
				t.Fatal(err)
			}
			configured, err := settings.New(settings.Definition{ProjectName: "account", InstalledApps: []apps.Config{{Name: "example.test/account", Label: "account"}}})
			if err != nil {
				t.Fatal(err)
			}
			config := Config{Apps: configured.Apps(), Namespace: "account", BasePath: base, APIBasePath: apiBase, Auth: runtime}
			switch mode {
			case "namespace":
				config.Namespace = "missing"
			case "root":
				config.BasePath = "/"
			case "parameter":
				config.BasePath = "/<int64:id>"
			case "overlap":
				config.BasePath = "/api/account"
			case "trailing":
				config.BasePath = "/account/"
			case "escape":
				config.BasePath = "/a%2fb"
			}
			application, err := New(config)
			if mode == "default" || mode == "custom" {
				if err != nil || len(application.Routes()) != 9 {
					t.Fatal("valid composition", err)
				}
				routes := application.Routes()
				routes[0].Path = "/mutated"
				if application.Routes()[0].Path == "/mutated" {
					t.Fatal("route ownership escaped")
				}
				encoded, _ := json.Marshal(config)
				if strings.Contains(string(encoded), "Auth") || !strings.Contains(fmt.Sprintf("%#v", application), "redacted") {
					t.Fatal("private config diagnostic")
				}
			} else if err == nil || application != nil {
				t.Fatal("invalid composition published", mode)
			}
		})
	}
}
