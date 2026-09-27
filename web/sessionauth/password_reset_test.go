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

type resetTestProvider struct {
	manager *sessions.Manager
	start   func(context.Context, sessions.ID, string, string) (sessions.Record, error)
	check   func(context.Context, sessions.ID, string, *string) error
	finish  func(context.Context, sessions.ID, string, string) (auth.PasswordResetResult, error)
}

func (p *resetTestProvider) Sessions() *sessions.Manager { return p.manager }
func (p *resetTestProvider) StartPasswordReset(ctx context.Context, id sessions.ID, target, token string) (sessions.Record, error) {
	return p.start(ctx, id, target, token)
}
func (p *resetTestProvider) CheckPasswordReset(ctx context.Context, id sessions.ID, target string, password *string) error {
	return p.check(ctx, id, target, password)
}
func (p *resetTestProvider) ResetPassword(ctx context.Context, id sessions.ID, target, password string) (auth.PasswordResetResult, error) {
	return p.finish(ctx, id, target, password)
}

func TestPasswordResetPersistenceRequiresExactManager(t *testing.T) {
	for _, mode := range []string{"omitted", "nil_pointer", "other_manager", "valid"} {
		t.Run(mode, func(t *testing.T) {
			config := csrfKeyRuntimeConfig(t, CSRFKeyRing{}, nil)
			var provider *resetTestProvider
			if mode == "valid" || mode == "other_manager" {
				provider = &resetTestProvider{manager: config.Sessions}
			}
			if mode == "other_manager" {
				provider.manager, _ = config.Sessions.WithStore(config.Sessions.Store())
			}
			if mode != "omitted" {
				config.PasswordResetPersistence = provider
			}
			runtime, err := New(config)
			if mode == "valid" || mode == "omitted" {
				if err != nil || runtime.CanResetPassword() != (mode == "valid") {
					t.Fatal("reset capability mismatch", err)
				}
			} else if err == nil || runtime != nil {
				t.Fatal("invalid reset capability accepted")
			}
		})
	}
}

func TestPasswordResetRuntimePublishesOnlyConfirmedCookies(t *testing.T) {
	for _, operation := range []string{"start", "check", "finish"} {
		for _, mode := range []string{"success", "clear", "zero", "same_id", "wrong_target", "wrong_token", "retained_proof", "target_authenticated", "conflicting_clear", "nonzero_clear", "denied", "input", "wrapped_input", "unknown", "private_failure", "missing_cookie", "bad_cookie", "duplicate_cookie", "unconfigured"} {
			if operation != "finish" && (mode == "clear" || mode == "retained_proof" || mode == "target_authenticated" || mode == "conflicting_clear" || mode == "nonzero_clear") {
				continue
			}
			if operation == "check" && (mode == "zero" || mode == "same_id" || mode == "wrong_target" || mode == "wrong_token") {
				continue
			}
			if operation == "finish" && (mode == "wrong_target" || mode == "wrong_token") {
				continue
			}
			t.Run(operation+"/"+mode, func(t *testing.T) {
				config := csrfKeyRuntimeConfig(t, CSRFKeyRing{}, nil)
				previous, err := config.Sessions.Create(t.Context(), map[string]string{"payload": "private-payload"})
				if err != nil {
					t.Fatal(err)
				}
				calls := 0
				fail := func(id sessions.ID, target, value string) error {
					calls++
					if target != "target" || (mode == "missing_cookie" && operation == "start" && id.Valid()) || (mode != "missing_cookie" && id != previous.ID()) {
						t.Fatal("reset input binding changed")
					}
					if operation == "start" && value != "private-reset-token" || operation != "start" && value != " raw password " {
						t.Fatal("reset altered private input")
					}
					switch mode {
					case "denied":
						return auth.ErrInvalidResetProof
					case "input":
						return validation.Reject(validation.NewErrors(validation.New("password", "weak")), nil)
					case "wrapped_input":
						return errors.Join(validation.Reject(validation.NewErrors(validation.New("password", "weak")), nil), context.Canceled)
					case "unknown":
						return errors.Join(auth.ErrInvalidResetProof, &query.Error{Code: query.CodeCommitOutcomeUnknown})
					case "private_failure":
						return errors.New("private-failure-marker")
					}
					return nil
				}
				makeRecord := func(ctx context.Context, proof bool) sessions.Record {
					if mode == "zero" {
						return sessions.Record{}
					}
					if mode == "same_id" {
						return previous
					}
					values := map[string]string{"payload": "private-payload"}
					if proof || mode == "retained_proof" {
						values[auth.SessionResetPrincipalIDKey] = "target"
						values[auth.SessionResetTokenKey] = "private-reset-token"
					}
					if mode == "wrong_target" {
						values[auth.SessionResetPrincipalIDKey] = "wrong"
					}
					if mode == "wrong_token" {
						values[auth.SessionResetTokenKey] = "wrong-token"
					}
					if mode == "target_authenticated" {
						values[auth.SessionPrincipalIDKey] = "target"
						values[auth.SessionCredentialStampKey] = "private-stamp"
					}
					record, e := config.Sessions.Create(ctx, values)
					if e != nil {
						t.Fatal(e)
					}
					return record
				}
				provider := &resetTestProvider{manager: config.Sessions}
				provider.start = func(ctx context.Context, id sessions.ID, target, token string) (sessions.Record, error) {
					if err := fail(id, target, token); err != nil {
						return sessions.Record{}, err
					}
					return makeRecord(ctx, true), nil
				}
				provider.check = func(_ context.Context, id sessions.ID, target string, password *string) error {
					if password == nil {
						t.Fatal("check lost selected field")
					}
					return fail(id, target, *password)
				}
				provider.finish = func(ctx context.Context, id sessions.ID, target, password string) (auth.PasswordResetResult, error) {
					if err := fail(id, target, password); err != nil {
						return auth.PasswordResetResult{}, err
					}
					if mode == "clear" {
						return auth.PasswordResetResult{ClearSession: true}, nil
					}
					if mode == "nonzero_clear" {
						record, _ := (sessions.Record{}).WithValue("private", "private-payload")
						return auth.PasswordResetResult{ClearSession: true, Record: record}, nil
					}
					return auth.PasswordResetResult{ClearSession: mode == "conflicting_clear", Record: makeRecord(ctx, false)}, nil
				}
				if mode != "unconfigured" {
					config.PasswordResetPersistence = provider
				}
				runtime, err := New(config)
				if err != nil {
					t.Fatal(err)
				}
				configured, err := settings.New(settings.Definition{ProjectName: "reset_probe", InstalledApps: []apps.Config{{Name: "github.com/progresshans/godj/web/sessionauth", Label: "resetprobe"}}})
				if err != nil {
					t.Fatal(err)
				}
				var resultErr error
				application, err := web.NewApplication(web.Config{Settings: configured, Routes: []web.Route{{Name: "resetprobe:reset", Method: "POST", Path: "/reset/", Handler: func(request *web.Request) (web.Response, error) {
					var result PasswordResetResult
					switch operation {
					case "start":
						result, resultErr = runtime.StartPasswordReset(request, "target", "private-reset-token")
					case "check":
						password := " raw password "
						resultErr = runtime.CheckPasswordReset(request, "target", &password)
					case "finish":
						result, resultErr = runtime.ResetPassword(request, "target", " raw password ")
					}
					if strings.Contains(fmt.Sprintf("%v %#v %d %x", result, result, result, result), "private-") {
						t.Fatal("reset result leaked private data")
					}
					status := 200
					if resultErr == auth.ErrInvalidResetProof {
						status = 403
					} else if _, rejected := validation.Rejected(resultErr); rejected {
						status = 400
					} else if resultErr != nil {
						status = 500
					}
					response, e := web.NewResponse(status, nil, nil)
					if e != nil {
						return web.Response{}, e
					}
					return result.Apply(response) // Even an error's zero result must publish nothing.
				}}}})
				if err != nil {
					t.Fatal(err)
				}
				request := httptest.NewRequest("POST", "/reset/", nil)
				if mode != "missing_cookie" {
					value := previous.ID().Encoded()
					if mode == "bad_cookie" {
						value = "bad"
					}
					request.Header.Set("Cookie", runtime.sessionCookie.Name+"="+value)
					if mode == "duplicate_cookie" {
						request.AddCookie(&http.Cookie{Name: runtime.sessionCookie.Name, Value: value})
					}
				}
				response := httptest.NewRecorder()
				application.ServeHTTP(response, request)
				want := 500
				if mode == "success" || mode == "clear" || (mode == "missing_cookie" && operation == "start") {
					want = 200
				}
				if mode == "denied" || mode == "bad_cookie" || mode == "duplicate_cookie" || (mode == "missing_cookie" && operation != "start") {
					want = 403
				}
				if mode == "input" {
					want = 400
				}
				if response.Code != want {
					t.Fatal("reset status differs", operation, mode, response.Code, want, resultErr)
				}
				cookies := response.Result().Cookies()
				if want == 200 && operation != "check" {
					if len(cookies) != 1 || cookies[0].Name != runtime.sessionCookie.Name || strings.Contains(cookies[0].String(), "private-") {
						t.Fatal("wrong reset cookie publication")
					}
					if mode == "clear" && cookies[0].MaxAge != -1 {
						t.Fatal("target cookie was not cleared")
					}
				} else if len(cookies) != 0 {
					t.Fatal("failed/read-only reset published cookies")
				}
				wantCalls := 1
				if mode == "bad_cookie" || mode == "duplicate_cookie" || mode == "unconfigured" || (mode == "missing_cookie" && operation != "start") {
					wantCalls = 0
				}
				if calls != wantCalls {
					t.Fatal("reset persistence call count", calls, wantCalls)
				}
				if mode == "unknown" && !errors.Is(resultErr, &query.Error{Code: query.CodeCommitOutcomeUnknown}) || mode == "wrapped_input" && !errors.Is(resultErr, context.Canceled) {
					t.Fatal("reset cause lost", resultErr)
				}
				if strings.Contains(fmt.Sprintf("%v %#v %d", resultErr, resultErr, resultErr), "private-failure-marker") {
					t.Fatal("reset diagnostics leaked private cause")
				}
			})
		}
	}
}
