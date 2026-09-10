package bearerauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
)

func TestParseAuthorizationEnforcesExactBoundedB64TokenGrammar(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		header  http.Header
		outcome bearerOutcome
		encoded string
	}{
		{name: "missing", outcome: bearerMissing},
		{name: "unsupported scheme", header: http.Header{"Authorization": {"Basic abc"}}, outcome: bearerUnsupported},
		{name: "unsupported scheme with commas", header: http.Header{"Authorization": {`Digest username="a",realm="b"`}}, outcome: bearerUnsupported},
		{name: "one space", header: http.Header{"Authorization": {"Bearer abc"}}, outcome: bearerAccepted, encoded: "abc"},
		{name: "case insensitive multiple spaces", header: http.Header{"Authorization": {"bEaReR   abc"}}, outcome: bearerAccepted, encoded: "abc"},
		{name: "RFC alphabet and padding", header: http.Header{"Authorization": {"Bearer a-Z_~+/9=="}}, outcome: bearerAccepted, encoded: "a-Z_~+/9=="},
		{name: "unlimited suffix padding", header: http.Header{"Authorization": {"Bearer a================"}}, outcome: bearerAccepted, encoded: "a================"},
		{name: "no base64 re-encoding", header: http.Header{"Authorization": {"Bearer a"}}, outcome: bearerAccepted, encoded: "a"},
		{name: "duplicate values", header: http.Header{"Authorization": {"Bearer abc", "Bearer def"}}, outcome: bearerMalformed},
		{name: "duplicate differently cased keys", header: http.Header{"Authorization": {"Bearer abc"}, "authorization": {"Bearer def"}}, outcome: bearerMalformed},
		{name: "zero field values", header: http.Header{"Authorization": nil}, outcome: bearerMalformed},
		{name: "empty field value", header: http.Header{"Authorization": {""}}, outcome: bearerMalformed},
		{name: "joined fields", header: http.Header{"Authorization": {"Bearer abc, Bearer def"}}, outcome: bearerMalformed},
		{name: "tab separator", header: http.Header{"Authorization": {"Bearer\tabc"}}, outcome: bearerMalformed},
		{name: "empty token", header: http.Header{"Authorization": {"Bearer "}}, outcome: bearerMalformed},
		{name: "bare scheme", header: http.Header{"Authorization": {"Bearer"}}, outcome: bearerMalformed},
		{name: "interior padding", header: http.Header{"Authorization": {"Bearer ab=c"}}, outcome: bearerMalformed},
		{name: "character after padding", header: http.Header{"Authorization": {"Bearer abc=d"}}, outcome: bearerMalformed},
		{name: "padding only", header: http.Header{"Authorization": {"Bearer ==="}}, outcome: bearerMalformed},
		{name: "non ASCII", header: http.Header{"Authorization": {"Bearer café"}}, outcome: bearerMalformed},
		{name: "control", header: http.Header{"Authorization": {"Bearer abc\x7f"}}, outcome: bearerMalformed},
		{name: "trailing space", header: http.Header{"Authorization": {"Bearer abc "}}, outcome: bearerMalformed},
		{name: "leading space", header: http.Header{"Authorization": {" Bearer abc"}}, outcome: bearerMalformed},
		{name: "token 4096 bytes", header: http.Header{"Authorization": {"Bearer " + strings.Repeat("a", MaxTokenBytes)}}, outcome: bearerAccepted, encoded: strings.Repeat("a", MaxTokenBytes)},
		{name: "token 4097 bytes", header: http.Header{"Authorization": {"Bearer " + strings.Repeat("a", MaxTokenBytes+1)}}, outcome: bearerMalformed},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			token, outcome := parseAuthorization(test.header)
			if outcome != test.outcome {
				t.Fatalf("outcome = %d, want %d", outcome, test.outcome)
			}
			if outcome == bearerAccepted && token.Encoded() != test.encoded {
				t.Fatal("accepted token bytes changed")
			}
			if outcome != bearerAccepted && token.Encoded() != "" {
				t.Fatal("rejected header retained token material")
			}
		})
	}
}

func TestNewAndRequireRejectNilConfigurationBeforePublication(t *testing.T) {
	t.Parallel()

	validVerifier := verifierFunc(func(context.Context, Token) (auth.Principal, error) {
		return auth.Anonymous(), auth.ErrInvalidCredentials
	})
	validAuthorizer := authorizerFunc(func(context.Context, auth.Principal, auth.Permission) (bool, error) {
		return false, nil
	})
	var typedNilVerifier *recordingVerifier
	var typedNilVerifierFunc verifierFunc
	var typedNilAuthorizer *recordingAuthorizer
	var typedNilAuthorizerFunc authorizerFunc

	for _, test := range []struct {
		name   string
		config Config
		field  string
	}{
		{name: "nil verifier", config: Config{Authorizer: validAuthorizer}, field: "verifier"},
		{name: "typed nil verifier pointer", config: Config{Verifier: typedNilVerifier, Authorizer: validAuthorizer}, field: "verifier"},
		{name: "typed nil verifier function", config: Config{Verifier: typedNilVerifierFunc, Authorizer: validAuthorizer}, field: "verifier"},
		{name: "nil authorizer", config: Config{Verifier: validVerifier}, field: "authorizer"},
		{name: "typed nil authorizer pointer", config: Config{Verifier: validVerifier, Authorizer: typedNilAuthorizer}, field: "authorizer"},
		{name: "typed nil authorizer function", config: Config{Verifier: validVerifier, Authorizer: typedNilAuthorizerFunc}, field: "authorizer"},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := New(test.config); !errors.Is(err, &Error{Code: CodeInvalidConfig, Field: test.field}) {
				t.Fatalf("New error = %v", err)
			}
		})
	}

	runtime, err := New(Config{Verifier: validVerifier, Authorizer: validAuthorizer})
	if err != nil {
		t.Fatal(err)
	}
	view := mustPermission(t, "articles.view")
	if handler, err := runtime.Require(view, nil); handler != nil || !errors.Is(err, &Error{Code: CodeInvalidConfig, Field: "handler"}) {
		t.Fatalf("nil handler = %#v, %v", handler, err)
	}
	if handler, err := runtime.Require(auth.Permission("Articles.View"), func(*web.Request, auth.Principal) (web.Response, error) {
		return web.Response{}, nil
	}); handler != nil || !errors.Is(err, &Error{Code: CodeInvalidConfig, Field: "permission"}) {
		t.Fatalf("invalid permission = %#v, %v", handler, err)
	}
	var nilRuntime *Runtime
	if handler, err := nilRuntime.Require(view, func(*web.Request, auth.Principal) (web.Response, error) {
		return web.Response{}, nil
	}); handler != nil || !errors.Is(err, &Error{Code: CodeInvalidConfig, Field: "runtime"}) {
		t.Fatalf("nil runtime = %#v, %v", handler, err)
	}
	if handler, err := (&Runtime{}).Require(view, func(*web.Request, auth.Principal) (web.Response, error) {
		return web.Response{}, nil
	}); handler != nil || !errors.Is(err, &Error{Code: CodeInvalidConfig, Field: "runtime"}) {
		t.Fatalf("zero runtime = %#v, %v", handler, err)
	}
}

func TestRequireUsesFixedChallengesDenyOverlayAndNoAlternateTransportOrCSRF(t *testing.T) {
	t.Parallel()

	view := mustPermission(t, "articles.view")
	deletePermission := mustPermission(t, "articles.delete")
	viewPrincipal := mustPrincipal(t, view)

	tests := []struct {
		name            string
		header          http.Header
		principal       auth.Principal
		verifyErr       error
		permission      auth.Permission
		allow           bool
		status          int
		challenge       string
		code            api.ResponseCode
		verifierCalls   int64
		authorizerCalls int64
		handlerCalls    int64
	}{
		{name: "missing despite cookie query and body", permission: view, principal: viewPrincipal, allow: true, status: http.StatusUnauthorized, challenge: challengeBearer, code: api.CodeNotAuthenticated},
		{name: "unsupported", header: http.Header{"Authorization": {"Basic abc"}}, permission: view, principal: viewPrincipal, allow: true, status: http.StatusUnauthorized, challenge: challengeBearer, code: api.CodeNotAuthenticated},
		{name: "unsupported with commas", header: http.Header{"Authorization": {`Digest username="a",realm="b"`}}, permission: view, principal: viewPrincipal, allow: true, status: http.StatusUnauthorized, challenge: challengeBearer, code: api.CodeNotAuthenticated},
		{name: "malformed", header: http.Header{"Authorization": {"Bearer ab=c"}}, permission: view, principal: viewPrincipal, allow: true, status: http.StatusBadRequest, challenge: challengeInvalidRequest, code: api.CodeNotAuthenticated},
		{name: "empty field is malformed", header: http.Header{"Authorization": {""}}, permission: view, principal: viewPrincipal, allow: true, status: http.StatusBadRequest, challenge: challengeInvalidRequest, code: api.CodeNotAuthenticated},
		{name: "leading space is malformed", header: http.Header{"Authorization": {" Bearer abc"}}, permission: view, principal: viewPrincipal, allow: true, status: http.StatusBadRequest, challenge: challengeInvalidRequest, code: api.CodeNotAuthenticated},
		{name: "invalid credentials", header: http.Header{"Authorization": {"Bearer secret-token"}}, permission: view, verifyErr: auth.ErrInvalidCredentials, allow: true, status: http.StatusUnauthorized, challenge: challengeInvalidToken, code: api.CodeNotAuthenticated, verifierCalls: 1},
		{name: "inactive principal", header: http.Header{"Authorization": {"Bearer secret-token"}}, permission: view, principal: auth.Anonymous(), allow: true, status: http.StatusUnauthorized, challenge: challengeInvalidToken, code: api.CodeNotAuthenticated, verifierCalls: 1},
		{name: "permission absent from snapshot", header: http.Header{"Authorization": {"Bearer secret-token"}}, permission: deletePermission, principal: viewPrincipal, allow: true, status: http.StatusForbidden, challenge: challengeInsufficientScope, code: api.CodePermissionDenied, verifierCalls: 1},
		{name: "authorizer deny", header: http.Header{"Authorization": {"Bearer secret-token"}}, permission: view, principal: viewPrincipal, status: http.StatusForbidden, challenge: challengeInsufficientScope, code: api.CodePermissionDenied, verifierCalls: 1, authorizerCalls: 1},
		{name: "valid unsafe request", header: http.Header{"Authorization": {"Bearer secret-token"}}, permission: view, principal: viewPrincipal, allow: true, status: http.StatusOK, verifierCalls: 1, authorizerCalls: 1, handlerCalls: 1},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			verifier := &recordingVerifier{principal: test.principal, err: test.verifyErr}
			authorizer := &recordingAuthorizer{allowed: test.allow}
			runtime, err := New(Config{Verifier: verifier, Authorizer: authorizer})
			if err != nil {
				t.Fatal(err)
			}
			var handlerCalls atomic.Int64
			application, logs := protectedApplication(t, runtime, test.permission, func(_ *web.Request, resolved auth.Principal) (web.Response, error) {
				handlerCalls.Add(1)
				if resolved.ID() != test.principal.ID() {
					return web.Response{}, errors.New("resolved principal changed")
				}
				object, objectErr := serializers.NewObject(serializers.MemberOf("ok", serializers.Boolean(true)))
				if objectErr != nil {
					return web.Response{}, objectErr
				}
				return api.JSON(http.StatusOK, object.Value())
			})

			request := httptest.NewRequest(http.MethodPost, "http://example.test/api/test/?access_token=query-secret", strings.NewReader("access_token=body-secret"))
			if test.header == nil {
				request.Header = make(http.Header)
			} else {
				request.Header = test.header.Clone()
			}
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.AddCookie(&http.Cookie{Name: "sessionid", Value: "cookie-secret"})
			response := serve(t, application, request)
			body := readResponse(t, response)
			headerDiagnostic := fmt.Sprint(response.Header)
			for _, secret := range []string{"secret-token", "query-secret", "body-secret", "cookie-secret"} {
				if strings.Contains(body, secret) || strings.Contains(headerDiagnostic, secret) || strings.Contains(logs.String(), secret) {
					t.Fatal("Bearer credential escaped into an HTTP or log diagnostic")
				}
			}

			if response.StatusCode != test.status || response.Header.Get("WWW-Authenticate") != test.challenge {
				t.Fatalf("response = status %d challenge %q", response.StatusCode, response.Header.Get("WWW-Authenticate"))
			}
			if test.status == http.StatusOK {
				if body != `{"ok":true}` {
					t.Fatalf("success body = %q", body)
				}
			} else if body != `{"code":"`+string(test.code)+`","errors":[]}` {
				t.Fatalf("denial body = %q", body)
			}
			if response.Header.Get("X-GoDj-CSRFToken") != "" || len(response.Cookies()) != 0 {
				t.Fatalf("Bearer response published CSRF state: headers=%#v cookies=%d", response.Header, len(response.Cookies()))
			}
			if verifier.calls.Load() != test.verifierCalls || authorizer.calls.Load() != test.authorizerCalls || handlerCalls.Load() != test.handlerCalls {
				t.Fatalf("calls verifier/authorizer/handler = %d/%d/%d", verifier.calls.Load(), authorizer.calls.Load(), handlerCalls.Load())
			}
			if test.verifierCalls == 1 && verifier.encoded != "secret-token" {
				t.Fatal("verifier did not receive the accepted opaque token")
			}
			if test.verifierCalls == 1 && verifier.retained.Encoded() != "" {
				t.Fatal("Verifier-retained Token remained readable after Verify returned")
			}
		})
	}
}

func TestInfrastructureErrorsAreWrappedAndContextErrorsArePreservedWithoutRetryOrChallenge(t *testing.T) {
	t.Parallel()

	view := mustPermission(t, "articles.view")
	principal := mustPrincipal(t, view)
	header := http.Header{"Authorization": {"Bearer infrastructure-secret"}}

	t.Run("verifier", func(t *testing.T) {
		cause := errors.New("verifier-secret-cause")
		verifier := &recordingVerifier{err: cause}
		runtime, err := New(Config{Verifier: verifier, Authorizer: auth.PrincipalAuthorizer{}})
		if err != nil {
			t.Fatal(err)
		}
		_, outcome, resolveErr := runtime.resolve(context.Background(), header)
		if resolveErr != nil && strings.Contains(resolveErr.Error(), cause.Error()) {
			t.Fatal("verifier cause leaked through the returned error")
		}
		if outcome != bearerInvalid || !errors.Is(resolveErr, cause) || !errors.Is(resolveErr, &Error{Code: CodeVerification}) {
			t.Fatalf("resolve = unexpected outcome %d or error identity", outcome)
		}
		if verifier.calls.Load() != 1 {
			t.Fatal("verifier retried")
		}

		application, logs := protectedApplication(t, runtime, view, func(*web.Request, auth.Principal) (web.Response, error) {
			t.Fatal("handler ran after verifier failure")
			return web.Response{}, nil
		})
		request := httptest.NewRequest(http.MethodGet, "http://example.test/api/test/", nil)
		request.Header = header.Clone()
		response := serve(t, application, request)
		body := readResponse(t, response)
		if strings.Contains(body+logs.String()+fmt.Sprint(response.Header), cause.Error()) {
			t.Fatal("verifier infrastructure cause escaped into HTTP diagnostics")
		}
		if response.StatusCode != http.StatusInternalServerError || response.Header.Get("WWW-Authenticate") != "" {
			t.Fatalf("infrastructure response = status %d challenge %q", response.StatusCode, response.Header.Get("WWW-Authenticate"))
		}
		if verifier.calls.Load() != 2 {
			t.Fatalf("one HTTP request retried verifier: total calls %d", verifier.calls.Load())
		}
	})

	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "verifier cancellation", err: context.Canceled},
		{name: "verifier deadline", err: context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int64
			verifier := verifierFunc(func(context.Context, Token) (auth.Principal, error) {
				calls.Add(1)
				return auth.Principal{}, fmt.Errorf("private context wrapper: %w", test.err)
			})
			runtime, err := New(Config{Verifier: verifier, Authorizer: auth.PrincipalAuthorizer{}})
			if err != nil {
				t.Fatal(err)
			}
			_, outcome, resolveErr := runtime.resolve(context.Background(), header)
			if outcome != bearerInvalid || resolveErr != test.err || calls.Load() != 1 {
				t.Fatalf("context resolve = outcome %d calls %d", outcome, calls.Load())
			}
		})
	}

	t.Run("already canceled request stops before parsing and verification", func(t *testing.T) {
		verifier := &recordingVerifier{principal: principal}
		runtime, err := New(Config{Verifier: verifier, Authorizer: auth.PrincipalAuthorizer{}})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, outcome, resolveErr := runtime.resolve(ctx, nil)
		if outcome != bearerInvalid || resolveErr != context.Canceled || verifier.calls.Load() != 0 {
			t.Fatalf("canceled resolve = outcome %d calls %d", outcome, verifier.calls.Load())
		}
	})

	t.Run("already canceled authorization stops before snapshot denial", func(t *testing.T) {
		authorizer := &recordingAuthorizer{allowed: true}
		runtime, err := New(Config{Verifier: &recordingVerifier{principal: principal}, Authorizer: authorizer})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		allowed, authorizationErr := runtime.allowed(ctx, principal, mustPermission(t, "articles.delete"))
		if allowed || authorizationErr != context.Canceled || authorizer.calls.Load() != 0 {
			t.Fatalf("canceled snapshot authorization = allowed %t calls %d", allowed, authorizer.calls.Load())
		}
	})

	t.Run("authorizer", func(t *testing.T) {
		cause := errors.New("authorizer-secret-cause")
		authorizer := &recordingAuthorizer{err: cause}
		runtime, err := New(Config{Verifier: &recordingVerifier{principal: principal}, Authorizer: authorizer})
		if err != nil {
			t.Fatal(err)
		}
		allowed, authorizationErr := runtime.allowed(context.Background(), principal, view)
		if authorizationErr != nil && strings.Contains(authorizationErr.Error(), cause.Error()) {
			t.Fatal("authorizer cause leaked through the returned error")
		}
		if allowed || !errors.Is(authorizationErr, cause) || !errors.Is(authorizationErr, &Error{Code: CodeAuthorization}) {
			t.Fatalf("authorization = allowed %t with unexpected error identity", allowed)
		}
		if authorizer.calls.Load() != 1 {
			t.Fatal("authorizer retried")
		}
	})

	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "authorizer cancellation is preserved", err: context.Canceled},
		{name: "authorizer deadline is preserved", err: context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			authorizer := &recordingAuthorizer{err: fmt.Errorf("private context wrapper: %w", test.err)}
			runtime, err := New(Config{Verifier: &recordingVerifier{principal: principal}, Authorizer: authorizer})
			if err != nil {
				t.Fatal(err)
			}
			allowed, authorizationErr := runtime.allowed(context.Background(), principal, view)
			if allowed || authorizationErr != test.err || authorizer.calls.Load() != 1 {
				t.Fatalf("context authorization = allowed %t calls %d", allowed, authorizer.calls.Load())
			}
		})
	}
}

func TestConfigAndRuntimeFormattingDoNotExposeCollaborators(t *testing.T) {
	t.Parallel()

	const marker = "bearer-config-secret-canary"
	verifier := &recordingVerifier{encoded: marker}
	config := Config{Verifier: verifier, Authorizer: auth.PrincipalAuthorizer{}}
	runtime, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, rendered := range []string{
		fmt.Sprint(config),
		fmt.Sprintf("%+v", config),
		fmt.Sprintf("%#v", config),
		fmt.Sprintf("%d", config),
		string(encoded),
		fmt.Sprint(runtime),
		fmt.Sprintf("%+v", runtime),
		fmt.Sprintf("%#v", runtime),
		fmt.Sprintf("%d", runtime),
		fmt.Sprint(*runtime),
		fmt.Sprintf("%+v", *runtime),
		fmt.Sprintf("%#v", *runtime),
		fmt.Sprintf("%d", *runtime),
	} {
		if strings.Contains(rendered, marker) {
			t.Fatal("configuration diagnostic exposes collaborator state")
		}
	}
}

type verifierFunc func(context.Context, Token) (auth.Principal, error)

func (function verifierFunc) Verify(ctx context.Context, token Token) (auth.Principal, error) {
	return function(ctx, token)
}

type authorizerFunc func(context.Context, auth.Principal, auth.Permission) (bool, error)

func (function authorizerFunc) Allowed(ctx context.Context, principal auth.Principal, permission auth.Permission) (bool, error) {
	return function(ctx, principal, permission)
}

type recordingVerifier struct {
	principal auth.Principal
	err       error
	calls     atomic.Int64
	encoded   string
	retained  Token
}

func (verifier *recordingVerifier) Verify(_ context.Context, token Token) (auth.Principal, error) {
	verifier.calls.Add(1)
	verifier.encoded = token.Encoded()
	verifier.retained = token
	return verifier.principal, verifier.err
}

type recordingAuthorizer struct {
	allowed bool
	err     error
	calls   atomic.Int64
}

func (authorizer *recordingAuthorizer) Allowed(context.Context, auth.Principal, auth.Permission) (bool, error) {
	authorizer.calls.Add(1)
	return authorizer.allowed, authorizer.err
}

func protectedApplication(
	t *testing.T,
	runtime *Runtime,
	permission auth.Permission,
	handler api.AuthenticatedHandler,
) (*web.Application, *bytes.Buffer) {
	t.Helper()
	protected, err := runtime.Require(permission, handler)
	if err != nil {
		t.Fatal(err)
	}
	configured, err := settings.New(settings.Definition{
		ProjectName: "bearer_auth_test",
		InstalledApps: []apps.Config{{
			Name:  "example.test/bearerapi",
			Label: "bearerapi",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	logs := &bytes.Buffer{}
	application, err := web.NewApplication(web.Config{
		Settings: configured,
		Routes: []web.Route{{
			Name:    "bearerapi:test",
			Method:  http.MethodPost,
			Path:    "/api/test/",
			Handler: protected,
		}, {
			Name:    "bearerapi:get",
			Method:  http.MethodGet,
			Path:    "/api/test/",
			Handler: protected,
		}},
		Logger: slog.New(slog.NewTextHandler(logs, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return application, logs
}

func serve(t *testing.T, application *web.Application, request *http.Request) *http.Response {
	t.Helper()
	recorder := httptest.NewRecorder()
	application.ServeHTTP(recorder, request)
	return recorder.Result()
}

func readResponse(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func mustPermission(t *testing.T, value string) auth.Permission {
	t.Helper()
	permission, err := auth.NewPermission(value)
	if err != nil {
		t.Fatal(err)
	}
	return permission
}

func mustPrincipal(t *testing.T, permissions ...auth.Permission) auth.Principal {
	t.Helper()
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{
		ID:          "bearer-operator",
		Active:      true,
		Permissions: permissions,
	})
	if err != nil {
		t.Fatal(err)
	}
	return principal
}
