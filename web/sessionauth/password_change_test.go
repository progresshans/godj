package sessionauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

type passwordChangeTestProvider struct {
	manager *sessions.Manager
	run     func(context.Context, sessions.ID, string, string) (auth.PasswordChangeResult, error)
}

func (p *passwordChangeTestProvider) Sessions() *sessions.Manager { return p.manager }
func (p *passwordChangeTestProvider) ChangePassword(ctx context.Context, id sessions.ID, old, next string) (auth.PasswordChangeResult, error) {
	return p.run(ctx, id, old, next)
}

func TestPasswordChangePersistenceRequiresExplicitExactManager(t *testing.T) {
	for _, mode := range []string{"omitted", "nil_pointer", "other_manager", "valid"} {
		t.Run(mode, func(t *testing.T) {
			config := csrfKeyRuntimeConfig(t, CSRFKeyRing{}, nil)
			var provider *passwordChangeTestProvider
			if mode == "valid" || mode == "other_manager" {
				provider = &passwordChangeTestProvider{manager: config.Sessions}
				if mode == "other_manager" {
					provider.manager, _ = config.Sessions.WithStore(config.Sessions.Store())
				}
			}
			if mode != "omitted" {
				config.PasswordChangePersistence = provider
			}
			runtime, err := New(config)
			if mode == "valid" || mode == "omitted" {
				if err != nil || runtime.CanChangePassword() != (mode == "valid") {
					t.Fatal("explicit password capability mismatch", err)
				}
			} else if err == nil || runtime != nil {
				t.Fatal("invalid password capability accepted")
			}
		})
	}
}

func TestPasswordChangePublishesOnlyCommittedBoundCookie(t *testing.T) {
	for _, mode := range []string{"success", "zero", "same_id", "wrong_identity", "wrong_stamp", "inactive", "unusable", "denied", "input", "wrapped_input", "caused_cancel", "caused_unknown", "unknown", "private_failure", "missing_cookie", "bad_cookie", "duplicate_cookie", "unconfigured"} {
		t.Run(mode, func(t *testing.T) {
			config := csrfKeyRuntimeConfig(t, CSRFKeyRing{}, nil)
			principal, _ := auth.NewPrincipal(auth.PrincipalConfig{ID: "self", Active: true})
			credential, _ := auth.NewCredential("self", "new-private-encoding", principal)
			previous, err := config.Sessions.Create(t.Context(), map[string]string{"payload": "retained"})
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			provider := &passwordChangeTestProvider{manager: config.Sessions}
			provider.run = func(ctx context.Context, id sessions.ID, old, next string) (auth.PasswordChangeResult, error) {
				calls++
				if id != previous.ID() || old != " raw old " || next != " raw new " {
					t.Fatal("runtime changed self password input")
				}
				switch mode {
				case "zero":
					return auth.PasswordChangeResult{}, nil
				case "denied":
					return auth.PasswordChangeResult{}, auth.ErrInvalidCredentials
				case "input":
					return auth.PasswordChangeResult{}, validation.Reject(validation.NewErrors(validation.New("old_password", "password_incorrect")), nil)
				case "caused_cancel":
					return auth.PasswordChangeResult{}, validation.Reject(validation.NewErrors(validation.New("password", "password_too_short")), errors.Join(context.Canceled, errors.New("private-password-marker")))
				case "caused_unknown":
					return auth.PasswordChangeResult{}, validation.Reject(validation.NewErrors(validation.New("password", "password_too_short")), &query.Error{Code: query.CodeCommitOutcomeUnknown})
				case "wrapped_input":
					return auth.PasswordChangeResult{}, errors.Join(validation.Reject(validation.NewErrors(validation.New("password", "password_too_short")), nil), context.Canceled)
				case "unknown":
					return auth.PasswordChangeResult{}, errors.Join(auth.ErrInvalidCredentials, &query.Error{Code: query.CodeCommitOutcomeUnknown})
				case "private_failure":
					return auth.PasswordChangeResult{}, errors.New("private-password-marker")
				}
				current := credential
				if mode == "inactive" {
					p, _ := auth.NewPrincipal(auth.PrincipalConfig{ID: "self"})
					current, _ = auth.NewCredential("self", "new-private-encoding", p)
				}
				if mode == "unusable" {
					current, _ = auth.NewCredential("self", "!disabled", principal)
				}
				values := map[string]string{auth.SessionPrincipalIDKey: "self", auth.SessionCredentialStampKey: current.SessionStamp()}
				if mode == "wrong_identity" {
					values[auth.SessionPrincipalIDKey] = "someone-else"
				}
				if mode == "wrong_stamp" {
					values[auth.SessionCredentialStampKey] = "another-stamp"
				}
				record, e := config.Sessions.Create(ctx, values)
				if e != nil {
					t.Fatal(e)
				}
				if mode == "same_id" {
					record = previous
				}
				return auth.PasswordChangeResult{Credential: current, Record: record}, nil
			}
			if mode != "unconfigured" {
				config.PasswordChangePersistence = provider
			}
			runtime, err := New(config)
			if err != nil {
				t.Fatal(err)
			}
			configured, err := settings.New(settings.Definition{ProjectName: "password_probe", InstalledApps: []apps.Config{{Name: "github.com/progresshans/godj/web/sessionauth", Label: "passwordprobe"}}})
			if err != nil {
				t.Fatal(err)
			}
			var resultErr error
			application, err := web.NewApplication(web.Config{Settings: configured, Routes: []web.Route{{Name: "passwordprobe:change", Method: "POST", Path: "/change/", Handler: func(request *web.Request) (web.Response, error) {
				result, err := runtime.ChangePassword(request, " raw old ", " raw new ")
				resultErr = err
				if err == auth.ErrInvalidCredentials {
					return web.NewResponse(401, nil, nil)
				}
				if _, rejected := validation.Rejected(err); rejected {
					return web.NewResponse(400, nil, nil)
				}
				if err != nil {
					return web.Response{}, err
				}
				if result.Principal().ID() != "self" {
					t.Error("published wrong principal")
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
			request := httptest.NewRequest("POST", "/change/", nil)
			if mode != "missing_cookie" {
				value := previous.ID().Encoded()
				if mode == "bad_cookie" {
					value = "invalid"
				}
				request.AddCookie(&http.Cookie{Name: config.SessionCookie.Name, Value: value})
				// The config fixture may use the default name through normalization.
				request.Header.Set("Cookie", runtime.sessionCookie.Name+"="+value)
				if mode == "duplicate_cookie" {
					request.AddCookie(&http.Cookie{Name: runtime.sessionCookie.Name, Value: value})
				}
			}
			response := httptest.NewRecorder()
			application.ServeHTTP(response, request)
			want := 500
			if mode == "success" {
				want = 200
			}
			if mode == "denied" || mode == "missing_cookie" || mode == "bad_cookie" || mode == "duplicate_cookie" {
				want = 401
			}
			if mode == "input" {
				want = 400
			}
			if response.Code != want {
				t.Fatal("password publication status", mode, response.Code, want)
			}
			cookies := response.Result().Cookies()
			if mode == "success" {
				if len(cookies) != 1 || cookies[0].Name != runtime.sessionCookie.Name {
					t.Fatal("password change must publish only its session cookie")
				}
			} else if len(cookies) != 0 {
				t.Fatal("failed change published cookie")
			}
			wantCalls := 1
			if mode == "missing_cookie" || mode == "bad_cookie" || mode == "duplicate_cookie" || mode == "unconfigured" {
				wantCalls = 0
			}
			if calls != wantCalls {
				t.Fatal("password persistence call count", calls, wantCalls)
			}
			if (mode == "unknown" || mode == "caused_unknown") && !errors.Is(resultErr, &query.Error{Code: query.CodeCommitOutcomeUnknown}) || (mode == "wrapped_input" || mode == "caused_cancel") && !errors.Is(resultErr, context.Canceled) {
				t.Fatal("password failure cause lost", resultErr)
			}
			if strings.Contains(fmt.Sprintf("%v %+v %#v", resultErr, resultErr, resultErr), "private-password-marker") {
				t.Fatal("password failure diagnostic exposed private material")
			}
		})
	}
}
