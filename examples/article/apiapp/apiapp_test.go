package apiapp_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/api"
	apisessionauth "github.com/progresshans/godj/api/sessionauth"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/apiapp"
	"github.com/progresshans/godj/examples/article/articleapp"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

func TestRoutesReverseNegotiationAndAllowAreClosedAndDeterministic(t *testing.T) {
	harness := newHarness(t)
	routes := harness.adapter.Routes()
	if len(routes) != 10 {
		t.Fatalf("route count = %d, want 10", len(routes))
	}
	names := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		if _, duplicate := names[route.Name]; duplicate {
			t.Fatalf("duplicate route name %q", route.Name)
		}
		names[route.Name] = struct{}{}
	}
	originalFirst := routes[0]
	routes[0] = web.Route{}
	if fresh := harness.adapter.Routes(); fresh[0].Name != originalFirst.Name || fresh[0].Handler == nil {
		t.Fatal("Routes returned mutable application-owned storage")
	}
	if _, err := apiapp.New(harness.backend, nil); err == nil {
		t.Fatal("New accepted a nil authentication runtime")
	}
	var typedNilAuthentication *recordingAuthentication
	if _, err := apiapp.New(harness.backend, typedNilAuthentication); err == nil {
		t.Fatal("New accepted a typed-nil authentication adapter")
	}
	var nilBackend *sqlite.Backend
	if _, err := apiapp.New(nilBackend, &recordingAuthentication{}); err == nil {
		t.Fatal("New accepted a typed-nil backend")
	}
	path, err := harness.application.ReverseWith(apiapp.DetailRouteName, web.Int64Argument("id", 42))
	if err != nil || path != "/api/articles/42/" {
		t.Fatalf("detail reverse = %q, %v", path, err)
	}

	method := harness.do(t, http.MethodPost, "/api/articles/1/", requestOptions{})
	assertResponse(t, method, http.StatusMethodNotAllowed, api.JSONContentType, `{"code":"method_not_allowed","errors":[]}`)
	if got := method.header.Get("Allow"); got != "DELETE, GET, HEAD, OPTIONS, PATCH, PUT" {
		t.Fatalf("Allow = %q", got)
	}

	notFound := harness.do(t, http.MethodGet, "/api/articles/01/", requestOptions{})
	assertResponse(t, notFound, http.StatusNotFound, api.JSONContentType, `{"code":"not_found","errors":[]}`)
	unacceptable := harness.do(t, http.MethodGet, "/api/articles/", requestOptions{accept: "text/html"})
	assertResponse(t, unacceptable, http.StatusNotAcceptable, api.JSONContentType, `{"code":"not_acceptable","errors":[]}`)
	nonAPI := harness.do(t, http.MethodGet, "/missing/", requestOptions{})
	assertResponse(t, nonAPI, http.StatusNotFound, "text/plain; charset=utf-8", "Not Found\n")
}

func TestNewBuildsAuthenticationRoutesAtomically(t *testing.T) {
	harness := newHarness(t)
	expected := []struct {
		name       string
		method     string
		path       string
		permission auth.Permission
	}{
		{name: apiapp.ListRouteName, method: http.MethodGet, path: apiapp.ListPath, permission: articleapp.ArticleViewPermission},
		{name: apiapp.Namespace + ":article-list-head", method: http.MethodHead, path: apiapp.ListPath, permission: articleapp.ArticleViewPermission},
		{name: apiapp.Namespace + ":article-list-options", method: http.MethodOptions, path: apiapp.ListPath, permission: articleapp.ArticleViewPermission},
		{name: apiapp.Namespace + ":article-create", method: http.MethodPost, path: apiapp.ListPath, permission: articleapp.ArticleAddPermission},
		{name: apiapp.DetailRouteName, method: http.MethodGet, path: apiapp.DetailPath, permission: articleapp.ArticleViewPermission},
		{name: apiapp.Namespace + ":article-detail-head", method: http.MethodHead, path: apiapp.DetailPath, permission: articleapp.ArticleViewPermission},
		{name: apiapp.Namespace + ":article-detail-options", method: http.MethodOptions, path: apiapp.DetailPath, permission: articleapp.ArticleViewPermission},
		{name: apiapp.Namespace + ":article-update", method: http.MethodPut, path: apiapp.DetailPath, permission: articleapp.ArticleChangePermission},
		{name: apiapp.Namespace + ":article-partial-update", method: http.MethodPatch, path: apiapp.DetailPath, permission: articleapp.ArticleChangePermission},
		{name: apiapp.Namespace + ":article-delete", method: http.MethodDelete, path: apiapp.DetailPath, permission: articleapp.ArticleDeletePermission},
	}

	authentication := &recordingAuthentication{}
	application, err := apiapp.New(harness.backend, authentication)
	if err != nil {
		t.Fatal(err)
	}
	routes := application.Routes()
	if len(routes) != len(expected) || len(authentication.calls) != len(expected) {
		t.Fatalf("routes/calls = %d/%d, want %d/%d", len(routes), len(authentication.calls), len(expected), len(expected))
	}
	for index, want := range expected {
		got := routes[index]
		if got.Name != want.name || got.Method != want.method || got.Path != want.path || got.Handler == nil {
			t.Fatalf("route %d = %#v, want name=%q method=%q path=%q with handler", index, got, want.name, want.method, want.path)
		}
		if authentication.calls[index].permission != want.permission || authentication.calls[index].handler == nil {
			t.Fatalf("authentication call %d = %#v, want permission %q with handler", index, authentication.calls[index], want.permission)
		}
	}

	for _, test := range []struct {
		name   string
		failAt int
		nilAt  int
	}{
		{name: "Require error", failAt: 4},
		{name: "nil protected handler", nilAt: 7},
	} {
		t.Run(test.name, func(t *testing.T) {
			authentication := &recordingAuthentication{failAt: test.failAt, nilAt: test.nilAt}
			application, err := apiapp.New(harness.backend, authentication)
			if application != nil || err == nil {
				t.Fatalf("New = %#v, %v", application, err)
			}
			wantCalls := test.failAt
			if wantCalls == 0 {
				wantCalls = test.nilAt
			}
			if len(authentication.calls) != wantCalls {
				t.Fatalf("Require calls = %d, want %d", len(authentication.calls), wantCalls)
			}
		})
	}
}

func TestArticleJSONCRUDPreservesFullPartialLocationAndEmptySemantics(t *testing.T) {
	harness := newHarness(t)
	created, err := harness.repository.Create(context.Background(), articleapp.Input{Title: "Existing", Published: true})
	if err != nil || created.ID != 1 {
		t.Fatalf("seed = %#v, %v", created, err)
	}
	token, csrf := harness.csrf(t, harness.allSession, "/api/articles/")

	list := harness.do(t, http.MethodGet, "/api/articles/", requestOptions{session: harness.allSession})
	assertResponse(t, list, http.StatusOK, api.JSONContentType, `{"count":1,"next":null,"previous":null,"results":[{"id":1,"title":"Existing","published":true,"summary":null}]}`)

	create := harness.do(t, http.MethodPost, "/api/articles/", requestOptions{
		body:        `{"title":"  Created  ","published":true,"summary":"before PUT"}`,
		contentType: api.JSONContentType,
		session:     harness.allSession,
		csrf:        csrf,
		token:       token,
	})
	assertResponse(t, create, http.StatusCreated, api.JSONContentType, `{"id":2,"title":"Created","published":true,"summary":"before PUT"}`)
	if got := create.header.Get("Location"); got != "/api/articles/2/" || strings.Contains(got, "example.test") {
		t.Fatalf("Location = %q", got)
	}

	detail := harness.do(t, http.MethodGet, "/api/articles/2/", requestOptions{session: harness.allSession})
	assertResponse(t, detail, http.StatusOK, api.JSONContentType, `{"id":2,"title":"Created","published":true,"summary":"before PUT"}`)
	head := harness.do(t, http.MethodHead, "/api/articles/2/", requestOptions{session: harness.allSession})
	assertResponse(t, head, http.StatusOK, api.JSONContentType, "")
	options := harness.do(t, http.MethodOptions, "/api/articles/2/", requestOptions{session: harness.allSession})
	assertResponse(t, options, http.StatusOK, api.JSONContentType, `{"methods":["DELETE","GET","HEAD","OPTIONS","PATCH","PUT"]}`)
	if got := options.header.Get("Allow"); got != "DELETE, GET, HEAD, OPTIONS, PATCH, PUT" {
		t.Fatalf("OPTIONS Allow = %q", got)
	}

	invalidPUT := harness.do(t, http.MethodPut, "/api/articles/2/", requestOptions{
		body:        `{"published":false,"summary":null}`,
		contentType: api.JSONContentType,
		session:     harness.allSession,
		csrf:        csrf,
		token:       token,
	})
	assertResponse(t, invalidPUT, http.StatusBadRequest, api.JSONContentType, `{"code":"validation_error","errors":[{"field":"title","code":"required","params":[]}]}`)
	assertArticle(t, harness.repository, 2, "Created", true, pointer("before PUT"))
	for _, method := range []string{http.MethodPut, http.MethodPatch} {
		missingWithInvalidBody := harness.do(t, method, "/api/articles/999/", requestOptions{
			body:        `{"title":`,
			contentType: api.JSONContentType,
			session:     harness.allSession,
			csrf:        csrf,
			token:       token,
		})
		assertResponse(t, missingWithInvalidBody, http.StatusNotFound, api.JSONContentType, `{"code":"not_found","errors":[]}`)
	}
	assertArticle(t, harness.repository, 2, "Created", true, pointer("before PUT"))

	put := harness.do(t, http.MethodPut, "/api/articles/2/", requestOptions{
		body:        `{"title":"Replaced"}`,
		contentType: api.JSONContentType,
		session:     harness.allSession,
		csrf:        csrf,
		token:       token,
	})
	assertResponse(t, put, http.StatusOK, api.JSONContentType, `{"id":2,"title":"Replaced","published":false,"summary":"before PUT"}`)

	patch := harness.do(t, http.MethodPatch, "/api/articles/2/", requestOptions{
		body:        `{"published":true,"summary":""}`,
		contentType: api.JSONContentType,
		session:     harness.allSession,
		csrf:        csrf,
		token:       token,
	})
	assertResponse(t, patch, http.StatusOK, api.JSONContentType, `{"id":2,"title":"Replaced","published":true,"summary":""}`)
	emptyPatch := harness.do(t, http.MethodPatch, "/api/articles/2/", requestOptions{
		body:        `{}`,
		contentType: api.JSONContentType,
		session:     harness.allSession,
		csrf:        csrf,
		token:       token,
	})
	assertResponse(t, emptyPatch, http.StatusOK, api.JSONContentType, patch.body)
	nullPatch := harness.do(t, http.MethodPatch, "/api/articles/2/", requestOptions{
		body:        `{"summary":null}`,
		contentType: api.JSONContentType,
		session:     harness.allSession,
		csrf:        csrf,
		token:       token,
	})
	assertResponse(t, nullPatch, http.StatusOK, api.JSONContentType, `{"id":2,"title":"Replaced","published":true,"summary":null}`)

	deleted := harness.do(t, http.MethodDelete, "/api/articles/2/", requestOptions{
		session: harness.allSession,
		csrf:    csrf,
		token:   token,
	})
	assertResponse(t, deleted, http.StatusNoContent, "", "")
	repeated := harness.do(t, http.MethodDelete, "/api/articles/2/", requestOptions{
		session: harness.allSession,
		csrf:    csrf,
		token:   token,
	})
	assertResponse(t, repeated, http.StatusNotFound, api.JSONContentType, `{"code":"not_found","errors":[]}`)
	zero := harness.do(t, http.MethodGet, "/api/articles/0/", requestOptions{session: harness.allSession})
	assertResponse(t, zero, http.StatusNotFound, api.JSONContentType, `{"code":"not_found","errors":[]}`)
}

func TestListFilterOrderingPaginationAndInvalidQueries(t *testing.T) {
	harness := newHarness(t)
	summaries := []*string{nil, pointer("needle only in summary"), pointer("misc"), pointer("API"), pointer("draft")}
	fixtures := []articleapp.Input{
		{Title: "Go Guide", Published: true, Summary: summaries[0]},
		{Title: "Django Notes", Published: false, Summary: summaries[1]},
		{Title: "Other", Published: true, Summary: summaries[2]},
		{Title: "Go Deep Dive", Published: true, Summary: summaries[3]},
		{Title: "Go Draft", Published: false, Summary: summaries[4]},
	}
	for _, input := range fixtures {
		if _, err := harness.repository.Create(context.Background(), input); err != nil {
			t.Fatal(err)
		}
	}

	combined := harness.do(t, http.MethodGet, "/api/articles/?search=go&published=true&ordering=-id", requestOptions{session: harness.allSession})
	assertResponse(t, combined, http.StatusOK, api.JSONContentType, `{"count":2,"next":null,"previous":null,"results":[{"id":4,"title":"Go Deep Dive","published":true,"summary":"API"},{"id":1,"title":"Go Guide","published":true,"summary":null}]}`)
	titleOnly := harness.do(t, http.MethodGet, "/api/articles/?search=needle", requestOptions{session: harness.allSession})
	assertResponse(t, titleOnly, http.StatusOK, api.JSONContentType, `{"count":0,"next":null,"previous":null,"results":[]}`)

	page1 := harness.do(t, http.MethodGet, "/api/articles/?page=1", requestOptions{session: harness.allSession})
	if page1.status != http.StatusOK || !strings.Contains(page1.body, `"next":"/api/articles/?page=2"`) || !strings.Contains(page1.body, `"previous":null`) {
		t.Fatalf("page 1 = %d %q", page1.status, page1.body)
	}
	page2 := harness.do(t, http.MethodGet, "/api/articles/?page=2", requestOptions{session: harness.allSession})
	if page2.status != http.StatusOK || !strings.Contains(page2.body, `"next":"/api/articles/?page=3"`) || !strings.Contains(page2.body, `"previous":"/api/articles/"`) {
		t.Fatalf("page 2 = %d %q", page2.status, page2.body)
	}
	page3 := harness.do(t, http.MethodGet, "/api/articles/?page=3", requestOptions{session: harness.allSession})
	if page3.status != http.StatusOK || !strings.Contains(page3.body, `"next":null`) || !strings.Contains(page3.body, `"previous":"/api/articles/?page=2"`) {
		t.Fatalf("page 3 = %d %q", page3.status, page3.body)
	}

	invalid := []struct {
		target string
		field  string
		code   string
	}{
		{target: "/api/articles/?published=yes", field: "published", code: "invalid"},
		{target: "/api/articles/?ordering=title", field: "ordering", code: "invalid_choice"},
		{target: "/api/articles/?extra=1", field: "extra", code: "unknown"},
		{target: "/api/articles/?=1", field: "", code: "unknown"},
		{target: "/api/articles/?search=go&search=django", field: "search", code: "duplicate"},
		{target: "/api/articles/?search=" + strings.Repeat("x", 65), field: "search", code: "max_length"},
		{target: "/api/articles/?page_size=100", field: "page_size", code: "unknown"},
	}
	for _, test := range invalid {
		response := harness.do(t, http.MethodGet, test.target, requestOptions{session: harness.allSession})
		if response.status != http.StatusBadRequest || !strings.Contains(response.body, `"field":"`+test.field+`"`) || !strings.Contains(response.body, `"code":"`+test.code+`"`) {
			t.Fatalf("GET %s = %d %q", test.target, response.status, response.body)
		}
	}
	for _, target := range []string{"/api/articles/?page=0", "/api/articles/?page=nope", "/api/articles/?page=99"} {
		response := harness.do(t, http.MethodGet, target, requestOptions{session: harness.allSession})
		assertResponse(t, response, http.StatusNotFound, api.JSONContentType, `{"code":"not_found","errors":[]}`)
	}
}

func TestAuthenticationCSRFTransportAndValidationDenialsMutateNoArticles(t *testing.T) {
	harness := newHarness(t)
	if _, err := harness.repository.Create(context.Background(), articleapp.Input{Title: "Existing"}); err != nil {
		t.Fatal(err)
	}

	anonymous := harness.do(t, http.MethodGet, "/api/articles/", requestOptions{})
	assertResponse(t, anonymous, http.StatusForbidden, api.JSONContentType, `{"code":"not_authenticated","errors":[]}`)
	if anonymous.header.Get("Location") != "" || anonymous.header.Get("WWW-Authenticate") != "" {
		t.Fatalf("anonymous headers = %#v", anonymous.header)
	}
	denied := harness.do(t, http.MethodGet, "/api/articles/", requestOptions{session: harness.deniedSession})
	assertResponse(t, denied, http.StatusForbidden, api.JSONContentType, `{"code":"permission_denied","errors":[]}`)

	allToken, allCSRF := harness.csrf(t, harness.allSession, "/api/articles/")
	viewToken, viewCSRF := harness.csrf(t, harness.viewSession, "/api/articles/")
	before := articleCount(t, harness.repository)
	missingCSRF := harness.do(t, http.MethodPost, "/api/articles/", requestOptions{
		body:        `{"title":"Blocked"}`,
		contentType: api.JSONContentType,
		session:     harness.allSession,
	})
	assertResponse(t, missingCSRF, http.StatusForbidden, api.JSONContentType, `{"code":"csrf_rejected","errors":[]}`)
	permission := harness.do(t, http.MethodPost, "/api/articles/", requestOptions{
		body:        `{"title":"Blocked"}`,
		contentType: api.JSONContentType,
		session:     harness.viewSession,
		csrf:        viewCSRF,
		token:       viewToken,
	})
	assertResponse(t, permission, http.StatusForbidden, api.JSONContentType, `{"code":"permission_denied","errors":[]}`)
	changeDenied := harness.do(t, http.MethodPut, "/api/articles/1/", requestOptions{
		body:        `{"title":"Blocked replacement"}`,
		contentType: api.JSONContentType,
		session:     harness.viewSession,
		csrf:        viewCSRF,
		token:       viewToken,
	})
	assertResponse(t, changeDenied, http.StatusForbidden, api.JSONContentType, `{"code":"permission_denied","errors":[]}`)
	deleteDenied := harness.do(t, http.MethodDelete, "/api/articles/1/", requestOptions{
		session: harness.viewSession,
		csrf:    viewCSRF,
		token:   viewToken,
	})
	assertResponse(t, deleteDenied, http.StatusForbidden, api.JSONContentType, `{"code":"permission_denied","errors":[]}`)

	invalidBodies := []struct {
		name        string
		body        string
		contentType string
		status      int
		code        api.ResponseCode
	}{
		{name: "empty", contentType: api.JSONContentType, status: 400, code: api.CodeParseError},
		{name: "duplicate", body: `{"title":"a","title":"b"}`, contentType: api.JSONContentType, status: 400, code: api.CodeParseError},
		{name: "list", body: `[]`, contentType: api.JSONContentType, status: 400, code: api.CodeParseError},
		{name: "trailing", body: `{} {}`, contentType: api.JSONContentType, status: 400, code: api.CodeParseError},
		{name: "unsupported", body: `{}`, contentType: "text/plain", status: 415, code: api.CodeUnsupportedMedia},
		{name: "oversize", body: `{"title":"` + strings.Repeat("x", 4090) + `"}`, contentType: api.JSONContentType, status: 413, code: api.CodeRequestTooLarge},
		{name: "summary max length", body: `{"title":"Valid","summary":"` + strings.Repeat("x", 201) + `"}`, contentType: api.JSONContentType, status: 400, code: api.CodeValidationError},
		{name: "control", body: `{"title":"bad\u0001"}`, contentType: api.JSONContentType, status: 400, code: api.CodeValidationError},
		{name: "unknown read-only", body: `{"id":9,"zeta":1}`, contentType: api.JSONContentType, status: 400, code: api.CodeValidationError},
	}
	for _, test := range invalidBodies {
		response := harness.do(t, http.MethodPost, "/api/articles/", requestOptions{
			body:        test.body,
			contentType: test.contentType,
			session:     harness.allSession,
			csrf:        allCSRF,
			token:       allToken,
		})
		if response.status != test.status || !strings.Contains(response.body, `"code":"`+string(test.code)+`"`) {
			t.Fatalf("%s response = %d %q", test.name, response.status, response.body)
		}
		if test.name == "unknown read-only" {
			want := `{"code":"validation_error","errors":[{"field":"id","code":"read_only","params":[]},{"field":"title","code":"required","params":[]},{"field":"zeta","code":"unknown","params":[]}]}`
			if response.body != want {
				t.Fatalf("ordered validation = %q, want %q", response.body, want)
			}
		}
	}
	for _, method := range []string{http.MethodPut, http.MethodPatch} {
		body := `{"summary":"` + strings.Repeat("x", 201) + `"}`
		if method == http.MethodPut {
			body = `{"title":"Valid","summary":"` + strings.Repeat("x", 201) + `"}`
		}
		response := harness.do(t, method, "/api/articles/1/", requestOptions{
			body:        body,
			contentType: api.JSONContentType,
			session:     harness.allSession,
			csrf:        allCSRF,
			token:       allToken,
		})
		want := `{"code":"validation_error","errors":[{"field":"summary","code":"max_length","params":[{"key":"max_length","value":"200"}]}]}`
		assertResponse(t, response, http.StatusBadRequest, api.JSONContentType, want)
	}
	if after := articleCount(t, harness.repository); after != before {
		t.Fatalf("denials changed Article count from %d to %d", before, after)
	}
}

type harness struct {
	backend       *sqlite.Backend
	repository    articleapp.Repository
	adapter       *apiapp.Application
	application   *web.Application
	allSession    *http.Cookie
	viewSession   *http.Cookie
	deniedSession *http.Cookie
}

type authenticationCall struct {
	permission auth.Permission
	handler    api.AuthenticatedHandler
}

type recordingAuthentication struct {
	calls  []authenticationCall
	failAt int
	nilAt  int
}

func (a *recordingAuthentication) Require(permission auth.Permission, handler api.AuthenticatedHandler) (web.Handler, error) {
	a.calls = append(a.calls, authenticationCall{permission: permission, handler: handler})
	call := len(a.calls)
	if call == a.failAt {
		return nil, errors.New("injected authentication construction failure")
	}
	if call == a.nilAt {
		return nil, nil
	}
	return func(request *web.Request) (web.Response, error) {
		return handler(request, auth.Anonymous())
	}, nil
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	backend, err := sqlite.OpenMemory(ctx, strings.ReplaceAll(t.Name(), "/", "_"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	if _, err := backend.ExecContext(ctx, `CREATE TABLE "godj_conformance_article" (
		"id" INTEGER PRIMARY KEY AUTOINCREMENT,
		"title" TEXT NOT NULL,
		"published" INTEGER NOT NULL,
		"summary" TEXT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	repository, err := articleapp.NewRepository(backend)
	if err != nil {
		t.Fatal(err)
	}

	all, err := auth.NewPrincipal(auth.PrincipalConfig{
		ID:     "all-user",
		Active: true,
		Permissions: []auth.Permission{
			articleapp.ArticleViewPermission,
			articleapp.ArticleAddPermission,
			articleapp.ArticleChangePermission,
			articleapp.ArticleDeletePermission,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := auth.NewPrincipal(auth.PrincipalConfig{
		ID:          "view-user",
		Active:      true,
		Permissions: []auth.Permission{articleapp.ArticleViewPermission},
	})
	if err != nil {
		t.Fatal(err)
	}
	denied, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "denied-user", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator{principals: []auth.Principal{all, viewer, denied}}
	store, err := sessions.NewMemoryStore(32)
	if err != nil {
		t.Fatal(err)
	}
	fixedTime := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	manager, err := sessions.NewManager(store, sessions.Config{
		Clock:  func() time.Time { return fixedTime },
		Random: bytes.NewReader(testEntropy(4096, 3)),
	})
	if err != nil {
		t.Fatal(err)
	}
	allSession := sessionCookie(t, manager, all)
	viewSession := sessionCookie(t, manager, viewer)
	deniedSession := sessionCookie(t, manager, denied)
	webRuntime, err := websessionauth.New(websessionauth.Config{
		Sessions:         manager,
		Authenticator:    authenticator,
		Authorizer:       auth.PrincipalAuthorizer{},
		SessionCookie:    websessionauth.CookieConfig{AllowInsecure: true},
		CSRFCookie:       websessionauth.CookieConfig{AllowInsecure: true},
		FallbackPath:     apiapp.ListPath,
		AllowedNextPaths: []string{apiapp.ListPath},
		Clock:            func() time.Time { return fixedTime },
		Random:           bytes.NewReader(testEntropy(1<<20, 7)),
	})
	if err != nil {
		t.Fatal(err)
	}
	apiRuntime, err := apisessionauth.New(webRuntime)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := apiapp.New(backend, apiRuntime)
	if err != nil {
		t.Fatal(err)
	}
	middleware, err := apiapp.Middleware()
	if err != nil {
		t.Fatal(err)
	}
	configured, err := settings.New(settings.Definition{
		ProjectName: "article_api_test",
		InstalledApps: []apps.Config{{
			Name:  "github.com/progresshans/godj/examples/article/articleapp",
			Label: apiapp.Namespace,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	application, err := web.NewApplication(web.Config{
		Settings:   configured,
		Routes:     adapter.Routes(),
		Middleware: middleware,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &harness{
		backend:       backend,
		repository:    repository,
		adapter:       adapter,
		application:   application,
		allSession:    allSession,
		viewSession:   viewSession,
		deniedSession: deniedSession,
	}
}

type testAuthenticator struct {
	principals []auth.Principal
}

func (a testAuthenticator) Authenticate(context.Context, string, string) (auth.Principal, error) {
	return auth.Principal{}, auth.ErrInvalidCredentials
}

func (a testAuthenticator) Resolve(_ context.Context, id string) (auth.Principal, error) {
	for _, principal := range a.principals {
		if principal.ID() == id {
			return principal, nil
		}
	}
	return auth.Principal{}, auth.ErrInvalidCredentials
}

func sessionCookie(t *testing.T, manager *sessions.Manager, principal auth.Principal) *http.Cookie {
	t.Helper()
	record, err := manager.Create(context.Background(), map[string]string{"_godj_principal_id": principal.ID()})
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: websessionauth.DefaultSessionCookieName, Value: record.ID().Encoded(), Path: "/"}
}

type requestOptions struct {
	body        string
	contentType string
	accept      string
	session     *http.Cookie
	csrf        *http.Cookie
	token       string
}

type responseResult struct {
	status  int
	header  http.Header
	body    string
	cookies []*http.Cookie
}

func (h *harness) do(t *testing.T, method, target string, options requestOptions) responseResult {
	t.Helper()
	request := httptest.NewRequest(method, "http://attacker.example"+target, strings.NewReader(options.body))
	accept := options.accept
	if accept == "" {
		accept = api.JSONContentType
	}
	request.Header.Set("Accept", accept)
	if options.contentType != "" {
		request.Header.Set("Content-Type", options.contentType)
	}
	if options.session != nil {
		request.AddCookie(options.session)
	}
	if options.csrf != nil {
		request.AddCookie(options.csrf)
	}
	if options.token != "" {
		request.Header.Set(websessionauth.DefaultCSRFHeader, options.token)
	}
	recorder := httptest.NewRecorder()
	h.application.ServeHTTP(recorder, request)
	response := recorder.Result()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return responseResult{
		status:  response.StatusCode,
		header:  response.Header.Clone(),
		body:    string(body),
		cookies: response.Cookies(),
	}
}

func (h *harness) csrf(t *testing.T, session *http.Cookie, target string) (string, *http.Cookie) {
	t.Helper()
	response := h.do(t, http.MethodGet, target, requestOptions{session: session})
	if response.status != http.StatusOK {
		t.Fatalf("CSRF seed = %d %q", response.status, response.body)
	}
	token := response.header.Get(websessionauth.DefaultCSRFHeader)
	if len(token) != 128 {
		t.Fatalf("CSRF token length = %d", len(token))
	}
	for _, cookie := range response.cookies {
		if cookie.Name == websessionauth.DefaultCSRFCookieName {
			clone := *cookie
			if !clone.HttpOnly || clone.Value == "" || clone.Value == token {
				t.Fatalf("CSRF cookie = %#v", clone)
			}
			return token, &clone
		}
	}
	t.Fatal("CSRF cookie missing")
	return "", nil
}

func assertResponse(t *testing.T, response responseResult, status int, contentType, body string) {
	t.Helper()
	if response.status != status || response.header.Get("Content-Type") != contentType || response.body != body {
		t.Fatalf("response = status %d content-type %q body %q, want %d %q %q", response.status, response.header.Get("Content-Type"), response.body, status, contentType, body)
	}
}

func assertArticle(t *testing.T, repository articleapp.Repository, id int64, title string, published bool, summary *string) {
	t.Helper()
	article, found, err := repository.Get(context.Background(), id)
	if err != nil || !found || article.Title != title || article.Published != published || !equalPointer(article.Summary, summary) {
		t.Fatalf("Article %d = %#v, found=%t, error=%v", id, article, found, err)
	}
}

func articleCount(t *testing.T, repository articleapp.Repository) int64 {
	t.Helper()
	page, err := repository.List(context.Background(), articleapp.ListOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	return page.Total
}

func equalPointer(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func pointer(value string) *string { return &value }

func testEntropy(size int, seed byte) []byte {
	result := make([]byte, size)
	for index := range result {
		result[index] = byte((index*17+int(seed))%251) + 1
	}
	return result
}
