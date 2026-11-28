package endpoint_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/endpoint"
	"github.com/progresshans/godj/api/output"
	apisession "github.com/progresshans/godj/api/sessionauth"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/web"
	websession "github.com/progresshans/godj/web/sessionauth"
)

type unusedAuthenticator struct{ calls atomic.Int64 }

func (a *unusedAuthenticator) Authenticate(context.Context, string, string) (auth.Credential, error) {
	a.calls.Add(1)
	return auth.Credential{}, auth.ErrInvalidCredentials
}
func (a *unusedAuthenticator) Resolve(context.Context, string) (auth.Credential, error) {
	a.calls.Add(1)
	return auth.Credential{}, auth.ErrInvalidCredentials
}

func TestEndpointAnonymousCSRFUsesSessionProfileBeforeBody(t *testing.T) {
	store, err := sessions.NewMemoryStore(8)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := sessions.NewManager(store, sessions.Config{})
	if err != nil {
		t.Fatal(err)
	}
	authenticator := &unusedAuthenticator{}
	runtime, err := websession.New(websession.Config{
		Sessions: manager, Authenticator: authenticator, Authorizer: auth.PrincipalAuthorizer{},
		SessionCookie: websession.CookieConfig{AllowInsecure: true}, CSRFCookie: websession.CookieConfig{AllowInsecure: true},
		FallbackPath: "/api/typed/", AllowedNextPaths: []string{"/api/typed/"},
	})
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := apisession.New(runtime)
	if err != nil {
		t.Fatal(err)
	}
	for _, proof := range []bool{false, true} {
		config := baseConfig(t)
		config.Admission = endpoint.CSRFOnly(proof)
		prepared, err := endpoint.New(adapter, config)
		if err != nil {
			t.Fatal(err)
		}
		operation, err := prepared.Operation()
		if err != nil || !operation.CSRFOnly || operation.SessionCookieRequired != proof || operation.Permission != "" {
			t.Fatal("CSRF proof declaration changed", err, operation)
		}
	}
	config := baseConfig(t)
	config.Admission = endpoint.CSRFOnly(false)
	config.Handle = func(request *web.Request, actor auth.Principal, _ struct{}) (output.Prepared[string], error) {
		if actor.Authenticated() {
			t.Error("CSRF-only handler received a principal")
		}
		return config.Output.Prepare(request.Context(), 200, "bootstrap")
	}
	bootstrap, err := endpoint.New(adapter, config)
	if err != nil {
		t.Fatal(err)
	}
	safe := send(application(t, bootstrap, nil), "GET", "http://example.test/api/typed/", "", false)
	if safe.Code != 200 {
		t.Fatal("anonymous bootstrap failed", safe.Code, safe.Body)
	}
	token := safe.Header().Get(websession.DefaultCSRFHeader)
	var cookie *http.Cookie
	for _, candidate := range safe.Result().Cookies() {
		if candidate.Name == websession.DefaultCSRFCookieName {
			cookie = candidate
		}
	}
	if token == "" || cookie == nil {
		t.Fatal("Session adapter did not add its CSRF pair")
	}
	var sets, calls atomic.Int64
	out := config.Output
	unsafe, err := endpoint.New(adapter, endpoint.Config[bodyDTO, string]{
		Route: web.Route{Name: "test:submit", Method: "POST", Path: "/api/submit/"}, Admission: endpoint.CSRFOnly(false),
		Input: endpoint.JSONBody("Body", bodyDeclaration(t, &sets), serializers.ModeFull, ""), Output: out,
		Success: []endpoint.Status{{Code: 200, Description: "Value."}},
		Handle: func(request *web.Request, actor auth.Principal, value bodyDTO) (output.Prepared[string], error) {
			calls.Add(1)
			if actor.Authenticated() {
				t.Error("CSRF-only handler resolved a principal")
			}
			name, _ := value.Name.Get()
			return out.Prepare(request.Context(), 200, name)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	app := application(t, unsafe, nil)
	for _, valid := range []bool{false, true} {
		body := &observedBody{Reader: strings.NewReader(`{"name":"accepted"}`)}
		request := httptest.NewRequest("POST", "http://example.test/api/submit/", nil)
		request.Body, request.ContentLength = body, -1
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(&http.Cookie{Name: websession.DefaultSessionCookieName, Value: "deliberately-invalid-session"})
		request.AddCookie(cookie)
		if valid {
			request.Header.Set(websession.DefaultCSRFHeader, token)
		}
		response := httptest.NewRecorder()
		app.ServeHTTP(response, request)
		if valid {
			if response.Code != 200 || response.Body.String() != `"accepted"` || calls.Load() != 1 || sets.Load() != 1 || body.reads == 0 {
				t.Fatal("paired CSRF failed", response.Code, response.Body)
			}
		} else if response.Code != 403 || body.reads != 0 || calls.Load()+sets.Load() != 0 || !strings.Contains(response.Body.String(), string(api.CodeCSRFRejected)) {
			t.Fatal("CSRF denial reached input", response.Code, response.Body, body.reads)
		}
		if body.closes != 0 {
			t.Fatal("endpoint closed borrowed request body")
		}
	}
	if authenticator.calls.Load() != 0 {
		t.Fatal("CSRF-only admission resolved session credentials")
	}
}

func TestEndpointRejectsRequestsOutsideBorrowedLifetime(t *testing.T) {
	config := baseConfig(t)
	var borrowed *web.Request
	config.Handle = func(request *web.Request, _ auth.Principal, _ struct{}) (output.Prepared[string], error) {
		borrowed = request
		return config.Output.Prepare(request.Context(), 200, "value")
	}
	prepared, err := endpoint.New(&startupAdapter{}, config)
	if err != nil {
		t.Fatal(err)
	}
	if response := send(application(t, prepared, nil), "GET", "/api/typed/", "", false); response.Code != 200 {
		t.Fatal(response.Code)
	}
	operation, _ := prepared.Operation()
	for _, request := range []*web.Request{nil, {}, borrowed} {
		response, err := operation.Route.Handler(request)
		if err == nil || response.Status() != 0 {
			t.Fatal("invalid request lifetime admitted", err, response.Status())
		}
	}
}
