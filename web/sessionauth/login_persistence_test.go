package sessionauth

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
)

type loginTestProvider struct {
	manager *sessions.Manager
	run     func(context.Context, auth.SessionLogin) (auth.SessionLoginResult, error)
}

func (p *loginTestProvider) Sessions() *sessions.Manager { return p.manager }
func (p *loginTestProvider) Login(ctx context.Context, login auth.SessionLogin) (auth.SessionLoginResult, error) {
	return p.run(ctx, login)
}

type loginTestAuthenticator struct {
	credential auth.Credential
	err        error
}

func (a loginTestAuthenticator) Authenticate(context.Context, string, string) (auth.Credential, error) {
	return a.credential, a.err
}
func (a loginTestAuthenticator) Resolve(context.Context, string) (auth.Credential, error) {
	return a.credential, a.err
}

func TestLoginPersistenceBindingRequiresExactManager(t *testing.T) {
	for _, mode := range []string{"nil_pointer", "other_manager", "valid"} {
		t.Run(mode, func(t *testing.T) {
			config := csrfKeyRuntimeConfig(t, CSRFKeyRing{}, nil)
			var provider *loginTestProvider
			if mode != "nil_pointer" {
				provider = &loginTestProvider{manager: config.Sessions}
				if mode == "other_manager" {
					provider.manager, _ = config.Sessions.WithStore(config.Sessions.Store())
				}
			}
			config.LoginPersistence = provider
			runtime, err := New(config)
			if mode == "valid" {
				if err != nil || runtime == nil {
					t.Fatal(err)
				}
			} else if err == nil || runtime != nil {
				t.Fatal("invalid persistence binding accepted")
			}
		})
	}
}

func TestLoginPersistenceHTTPRejectsUncommittedOrMismatchedResults(t *testing.T) {
	for _, mode := range []string{"zero", "wrong_identity", "wrong_stamp", "missing_binding", "inactive", "denied", "wrapped_denial", "unknown", "current_permissions", "auth_wrapped_denial"} {
		t.Run(mode, func(t *testing.T) {
			config := csrfKeyRuntimeConfig(t, CSRFKeyRing{}, nil)
			principal, _ := auth.NewPrincipal(auth.PrincipalConfig{ID: "member", Active: true, Permissions: []auth.Permission{"app.item.view"}})
			credential, _ := auth.NewCredential("member", "private-encoded-material", principal)
			config.Authenticator = loginTestAuthenticator{credential: credential}
			if mode == "auth_wrapped_denial" {
				config.Authenticator = loginTestAuthenticator{err: errors.Join(auth.ErrInvalidCredentials, &query.Error{Code: query.CodeTransactionOutcomeUnknown})}
			}
			calls := 0
			provider := &loginTestProvider{manager: config.Sessions}
			provider.run = func(ctx context.Context, login auth.SessionLogin) (auth.SessionLoginResult, error) {
				calls++
				if err := login.Validate(); err != nil {
					t.Fatal(err)
				}
				if mode == "zero" {
					return auth.SessionLoginResult{}, nil
				}
				if mode == "denied" {
					return auth.SessionLoginResult{}, auth.ErrInvalidCredentials
				}
				if mode == "wrapped_denial" || mode == "unknown" {
					return auth.SessionLoginResult{}, errors.Join(auth.ErrInvalidCredentials, &query.Error{Code: query.CodeCommitOutcomeUnknown})
				}
				current := credential
				switch mode {
				case "wrong_identity":
					p, _ := auth.NewPrincipal(auth.PrincipalConfig{ID: "another", Active: true})
					current, _ = auth.NewCredential("member", "private-encoded-material", p)
				case "wrong_stamp":
					current, _ = auth.NewCredential("member", "another-private-encoding", principal)
				case "inactive":
					p, _ := auth.NewPrincipal(auth.PrincipalConfig{ID: "member"})
					current, _ = auth.NewCredential("member", "private-encoded-material", p)
				case "current_permissions":
					p, _ := auth.NewPrincipal(auth.PrincipalConfig{ID: "member", Active: true})
					current, _ = auth.NewCredential("member", "private-encoded-material", p)
				}
				values := map[string]string{auth.SessionPrincipalIDKey: current.Principal().ID(), auth.SessionCredentialStampKey: current.SessionStamp()}
				if mode == "missing_binding" {
					delete(values, auth.SessionCredentialStampKey)
				}
				record, err := config.Sessions.Create(ctx, values)
				if err != nil {
					t.Fatal(err)
				}
				return auth.SessionLoginResult{Credential: current, Record: record}, nil
			}
			config.LoginPersistence = provider
			runtime, err := New(config)
			if err != nil {
				t.Fatal(err)
			}
			configured, err := settings.New(settings.Definition{ProjectName: "login_persistence", InstalledApps: []apps.Config{{Name: "github.com/progresshans/godj/web/sessionauth", Label: "loginprobe"}}})
			if err != nil {
				t.Fatal(err)
			}
			application, err := web.NewApplication(web.Config{Settings: configured, Routes: []web.Route{{Name: "loginprobe:login", Method: "POST", Path: "/login/", Handler: func(request *web.Request) (web.Response, error) {
				result, err := runtime.Login(request, "member", "not used")
				if err == auth.ErrInvalidCredentials {
					return web.NewResponse(401, nil, nil)
				}
				if err != nil {
					return web.Response{}, err
				}
				if result.Principal().Has("app.item.view") {
					t.Error("published stale principal instead of committed current admission")
				}
				response, err := web.NewResponse(200, nil, nil)
				if err != nil {
					return web.Response{}, err
				}
				return result.Apply(response)
			}}}})
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			application.ServeHTTP(response, httptest.NewRequest("POST", "/login/", nil))
			want := 500
			if mode == "denied" {
				want = 401
			}
			if mode == "current_permissions" {
				want = 200
			}
			if response.Code != want {
				t.Fatal("persistence result status", mode, response.Code, want)
			}
			if want != 200 && len(response.Result().Cookies()) != 0 {
				t.Fatal("failed login emitted cookies")
			}
			if want == 200 && len(response.Result().Cookies()) != 2 {
				t.Fatal("confirmed login omitted cookies")
			}
			wantCalls := 1
			if mode == "auth_wrapped_denial" {
				wantCalls = 0
			}
			if calls != wantCalls {
				t.Fatal("persistence retried or ran after failed verification", calls)
			}
		})
	}
}
