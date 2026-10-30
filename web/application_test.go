package web_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
)

func TestApplicationNamedStaticRoutingAndReverse(t *testing.T) {
	application := newTestApplication(t, web.Config{Routes: []web.Route{
		{Name: "articles:list", Method: http.MethodGet, Path: "/articles/", Handler: textHandler("list")},
		{Name: "articles:create", Method: http.MethodPost, Path: "/articles/", Handler: textHandler("create")},
	}})

	path, err := application.Reverse("articles:list")
	if err != nil || path != "/articles/" {
		t.Fatalf("Reverse() = %q, %v", path, err)
	}
	if _, err := application.Reverse("articles:missing"); !errors.Is(err, &web.Error{Code: web.CodeReverseNotFound}) {
		t.Fatalf("Reverse(missing) error = %v", err)
	}

	tests := []struct {
		method string
		path   string
		status int
		body   string
		allow  string
	}{
		{method: http.MethodGet, path: "/articles/", status: http.StatusOK, body: "list"},
		{method: http.MethodPost, path: "/articles/", status: http.StatusOK, body: "create"},
		{method: http.MethodPut, path: "/articles/", status: http.StatusMethodNotAllowed, body: "Method Not Allowed\n", allow: "GET, POST"},
		{method: http.MethodGet, path: "/articles", status: http.StatusNotFound, body: "Not Found\n"},
		{method: http.MethodGet, path: "/missing/", status: http.StatusNotFound, body: "Not Found\n"},
	}
	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			response := serve(application, test.method, test.path)
			if response.Code != test.status || response.Body.String() != test.body {
				t.Fatalf("response = %d %q", response.Code, response.Body.String())
			}
			if got := response.Header().Get("Allow"); got != test.allow {
				t.Fatalf("Allow = %q, want %q", got, test.allow)
			}
		})
	}
}

func TestApplicationRejectsInvalidAndDuplicateRoutes(t *testing.T) {
	tests := []struct {
		name   string
		routes []web.Route
		code   web.ErrorCode
	}{
		{name: "lowercase method", routes: []web.Route{{Name: "articles:list", Method: "get", Path: "/", Handler: textHandler("ok")}}, code: web.CodeInvalidRoute},
		{name: "non ASCII method", routes: []web.Route{{Name: "articles:list", Method: "ＧＥＴ", Path: "/", Handler: textHandler("ok")}}, code: web.CodeInvalidRoute},
		{name: "dynamic path", routes: []web.Route{{Name: "articles:list", Method: "GET", Path: "/articles/{id}", Handler: textHandler("ok")}}, code: web.CodeInvalidRoute},
		{name: "control path", routes: []web.Route{{Name: "articles:list", Method: "GET", Path: "/articles/\x01", Handler: textHandler("ok")}}, code: web.CodeInvalidRoute},
		{name: "invalid UTF-8 path", routes: []web.Route{{Name: "articles:list", Method: "GET", Path: string([]byte{'/', 0xff}), Handler: textHandler("ok")}}, code: web.CodeInvalidRoute},
		{name: "unknown namespace", routes: []web.Route{{Name: "accounts:list", Method: "GET", Path: "/", Handler: textHandler("ok")}}, code: web.CodeInvalidRoute},
		{name: "duplicate name", routes: []web.Route{{Name: "articles:list", Method: "GET", Path: "/", Handler: textHandler("ok")}, {Name: "articles:list", Method: "POST", Path: "/", Handler: textHandler("ok")}}, code: web.CodeDuplicateRoute},
		{name: "duplicate method and path", routes: []web.Route{{Name: "articles:list", Method: "GET", Path: "/", Handler: textHandler("ok")}, {Name: "articles:other", Method: "GET", Path: "/", Handler: textHandler("ok")}}, code: web.CodeDuplicateRoute},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := web.NewApplication(web.Config{Settings: testSettings(t), Routes: test.routes})
			if !errors.Is(err, &web.Error{Code: test.code}) {
				t.Fatalf("New() error = %v, want code %q", err, test.code)
			}
		})
	}
}

func TestApplicationStaticPathCanonicalForms(t *testing.T) {
	tests := []struct {
		path  string
		valid bool
	}{
		{path: "/", valid: true},
		{path: "//", valid: false},
		{path: "/articles/", valid: true},
		{path: "/articles//", valid: false},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			_, err := web.NewApplication(web.Config{
				Settings: testSettings(t),
				Routes: []web.Route{{
					Name: "articles:list", Method: http.MethodGet, Path: test.path, Handler: textHandler("ok"),
				}},
			})
			if test.valid && err != nil {
				t.Fatalf("NewApplication(path=%q) error = %v", test.path, err)
			}
			if !test.valid && !errors.Is(err, &web.Error{Code: web.CodeInvalidRoute}) {
				t.Fatalf("NewApplication(path=%q) error = %v, want invalid route", test.path, err)
			}
		})
	}
}

func TestMiddlewareOrderAndSingleDownstreamInvocation(t *testing.T) {
	var trace []string
	middleware := func(name string) web.Middleware {
		return func(next web.Handler) web.Handler {
			return func(request *web.Request) (web.Response, error) {
				trace = append(trace, name+" before")
				response, err := next(request)
				trace = append(trace, name+" after")
				return response, err
			}
		}
	}
	application := newTestApplication(t, web.Config{
		Middleware: []web.Middleware{middleware("first"), middleware("second")},
		Routes: []web.Route{{
			Name: "articles:list", Method: "GET", Path: "/", Handler: func(*web.Request) (web.Response, error) {
				trace = append(trace, "handler")
				return testResponse(http.StatusOK, "ok")
			},
		}},
	})
	response := serve(application, "GET", "/")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	want := []string{"first before", "second before", "handler", "second after", "first after"}
	if !reflect.DeepEqual(trace, want) {
		t.Fatalf("trace = %v, want %v", trace, want)
	}

	doubleNext := func(next web.Handler) web.Handler {
		return func(request *web.Request) (web.Response, error) {
			response, _ := next(request)
			_, _ = next(request)
			return response, nil
		}
	}
	application = newTestApplication(t, web.Config{
		Middleware: []web.Middleware{doubleNext},
		Routes:     []web.Route{{Name: "articles:list", Method: "GET", Path: "/", Handler: textHandler("must not escape")}},
	})
	response = serve(application, "GET", "/")
	if response.Code != http.StatusInternalServerError || response.Body.String() != "Internal Server Error\n" {
		t.Fatalf("double next response = %d %q", response.Code, response.Body.String())
	}
}

func TestHandlerFailureAndResponseLimitWriteNoPartialBody(t *testing.T) {
	var log bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&log, nil))
	application := newTestApplication(t, web.Config{
		Logger:           logger,
		MaxResponseBytes: 4,
		Routes: []web.Route{
			{Name: "articles:error", Method: "GET", Path: "/error/", Handler: func(*web.Request) (web.Response, error) {
				response, _ := testResponse(http.StatusOK, "partial private bytes")
				return response, errors.New("private handler detail")
			}},
			{Name: "articles:large", Method: "GET", Path: "/large/", Handler: textHandler("large")},
		},
	})
	for _, path := range []string{"/error/", "/large/"} {
		response := serve(application, "GET", path)
		if response.Code != http.StatusInternalServerError || response.Body.String() != "Internal Server Error\n" {
			t.Fatalf("%s response = %d %q", path, response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "private") || strings.Contains(response.Body.String(), "large") {
			t.Fatalf("%s leaked handler output: %q", path, response.Body.String())
		}
	}
	if !strings.Contains(log.String(), "private handler detail") || !strings.Contains(log.String(), "response_too_large") {
		t.Fatalf("internal log did not retain diagnostics: %q", log.String())
	}
}

func TestRequestContextAndBorrowedLifetime(t *testing.T) {
	var captured *web.Request
	application := newTestApplication(t, web.Config{Routes: []web.Route{{
		Name: "articles:list", Method: "GET", Path: "/", Handler: func(request *web.Request) (web.Response, error) {
			captured = request
			if !errors.Is(request.Context().Err(), context.Canceled) {
				return testResponse(http.StatusInternalServerError, "context was not propagated")
			}
			if request.Settings().ProjectName() != "test_project" {
				return testResponse(http.StatusInternalServerError, "settings were not propagated")
			}
			if _, ok := request.Apps().Lookup("articles"); !ok {
				return testResponse(http.StatusInternalServerError, "apps were not propagated")
			}
			if path, err := request.Reverse("articles:list"); err != nil || path != "/" {
				return testResponse(http.StatusInternalServerError, "reverse was not propagated")
			}
			return testResponse(http.StatusOK, "canceled")
		},
	}}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest("GET", "http://example.test/", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	application.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "canceled" {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	if captured == nil || captured.HTTP() != nil || captured.Method() != "" || captured.Path() != "" {
		t.Fatalf("borrowed request remained active: %#v", captured)
	}
	if captured.Settings().ProjectName() != "" {
		t.Fatal("borrowed settings remained active")
	}
	if _, ok := captured.Apps().Lookup("articles"); ok {
		t.Fatal("borrowed app registry remained active")
	}
	if _, err := captured.Reverse("articles:list"); !errors.Is(err, &web.Error{Code: web.CodeInvalidRequest}) {
		t.Fatalf("Reverse after request error = %v", err)
	}
	if !errors.Is(captured.Context().Err(), context.Canceled) {
		t.Fatalf("Context after request error = %v", captured.Context().Err())
	}
}

func TestApplicationRejectsMiddlewareStartupPanic(t *testing.T) {
	panicking := func(web.Handler) web.Handler {
		panic("private startup detail")
	}
	_, err := web.NewApplication(web.Config{
		Settings:   testSettings(t),
		Middleware: []web.Middleware{panicking},
	})
	if !errors.Is(err, &web.Error{Code: web.CodeInvalidConfig, Field: "middleware"}) {
		t.Fatalf("NewApplication() error = %v", err)
	}
}

func TestApplicationConcurrentRequestsHaveIndependentMiddlewareState(t *testing.T) {
	middleware := func(next web.Handler) web.Handler {
		return func(request *web.Request) (web.Response, error) {
			return next(request)
		}
	}
	application := newTestApplication(t, web.Config{
		Middleware: []web.Middleware{middleware, middleware},
		Routes:     []web.Route{{Name: "articles:list", Method: "GET", Path: "/", Handler: textHandler("ok")}},
	})
	const requests = 64
	var wait sync.WaitGroup
	errorsFound := make(chan error, requests)
	for index := 0; index < requests; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			response := serve(application, "GET", "/")
			if response.Code != http.StatusOK || response.Body.String() != "ok" {
				errorsFound <- fmt.Errorf("response = %d %q", response.Code, response.Body.String())
			}
		}()
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Error(err)
	}
}

func newTestApplication(t *testing.T, config web.Config) *web.Application {
	t.Helper()
	config.Settings = testSettings(t)
	application, err := web.NewApplication(config)
	if err != nil {
		t.Fatal(err)
	}
	return application
}

func testSettings(t testing.TB) settings.Settings {
	t.Helper()
	configured, err := settings.New(settings.Definition{
		ProjectName: "test_project",
		InstalledApps: []apps.Config{{
			Name:  "example.test/articles",
			Label: "articles",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return configured
}

func textHandler(body string) web.Handler {
	return func(*web.Request) (web.Response, error) {
		return testResponse(http.StatusOK, body)
	}
}

func testResponse(status int, body string) (web.Response, error) {
	header := make(http.Header)
	header.Set("Content-Type", "text/plain; charset=utf-8")
	return web.NewResponse(status, header, []byte(body))
}

func serve(application *web.Application, method, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://example.test"+path, nil)
	response := httptest.NewRecorder()
	application.ServeHTTP(response, request)
	return response
}
