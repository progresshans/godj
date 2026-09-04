package api_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

func TestParserAcceptsExactlyOneBoundedJSONObject(t *testing.T) {
	parser, err := api.NewParser(api.ParserConfig{MaxBodyBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name        string
		contentType []string
		body        string
		wantTitle   string
		wantCode    api.FailureCode
	}{
		{name: "json", contentType: []string{"application/json"}, body: `{"title":"Go"}`, wantTitle: "Go"},
		{name: "utf8", contentType: []string{"application/json; charset=UTF-8"}, body: `{"title":"Go"}`, wantTitle: "Go"},
		{name: "missing media", body: `{}`, wantCode: api.FailureUnsupportedMedia},
		{name: "wrong media", contentType: []string{"text/json"}, body: `{}`, wantCode: api.FailureUnsupportedMedia},
		{name: "duplicate media", contentType: []string{"application/json", "application/json"}, body: `{}`, wantCode: api.FailureUnsupportedMedia},
		{name: "unsupported parameter", contentType: []string{"application/json; profile=x"}, body: `{}`, wantCode: api.FailureUnsupportedMedia},
		{name: "duplicate field", contentType: []string{"application/json"}, body: `{"x":1,"x":2}`, wantCode: api.FailureInvalidRequest},
		{name: "top level list", contentType: []string{"application/json"}, body: `[]`, wantCode: api.FailureInvalidRequest},
		{name: "too large", contentType: []string{"application/json"}, body: `{"title":"` + strings.Repeat("x", 80) + `"}`, wantCode: api.FailureBodyTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var parsed serializers.Object
			var parseErr error
			application := testApplication(t, nil, []web.Route{{
				Name:   "test:parse",
				Method: http.MethodPost,
				Path:   "/parse/",
				Handler: func(request *web.Request) (web.Response, error) {
					parsed, parseErr = parser.ParseObject(request)
					return emptyOK(t), nil
				},
			}})
			request := httptest.NewRequest(http.MethodPost, "http://example.test/parse/", strings.NewReader(test.body))
			request.Header.Del("Content-Type")
			for _, value := range test.contentType {
				request.Header.Add("Content-Type", value)
			}
			recorder := httptest.NewRecorder()
			application.ServeHTTP(recorder, request)
			if test.wantCode != "" {
				if !errors.Is(parseErr, &api.Error{Code: test.wantCode}) {
					t.Fatalf("parse error = %v", parseErr)
				}
				return
			}
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			value, ok := parsed.Get("title")
			if title, stringOK := value.AsString(); !ok || !stringOK || title != test.wantTitle {
				t.Fatalf("title = %q, object=%v string=%v", title, ok, stringOK)
			}
		})
	}
}

func TestParserPreservesReadFailureAndBorrowedLifetime(t *testing.T) {
	parser, err := api.NewParser(api.ParserConfig{})
	if err != nil {
		t.Fatal(err)
	}
	var borrowed *web.Request
	var parseErr error
	application := testApplication(t, nil, []web.Route{{
		Name:   "test:parse",
		Method: http.MethodPost,
		Path:   "/parse/",
		Handler: func(request *web.Request) (web.Response, error) {
			borrowed = request
			_, parseErr = parser.ParseObject(request)
			return emptyOK(t), nil
		},
	}})
	request := httptest.NewRequest(http.MethodPost, "http://example.test/parse/", nil)
	request.Header.Set("Content-Type", "application/json")
	request.Body = failingBody{}
	application.ServeHTTP(httptest.NewRecorder(), request)
	if !errors.Is(parseErr, &api.Error{Code: api.FailureBodyRead}) || !errors.Is(parseErr, errForcedRead) {
		t.Fatalf("read error = %v", parseErr)
	}
	if _, err := parser.ParseObject(borrowed); !errors.Is(err, &api.Error{Code: api.FailureInvalidRequest}) {
		t.Fatalf("late parse error = %v", err)
	}
}

func TestErrorResponseIsOrderedSecretFreeAndMachineReadable(t *testing.T) {
	diagnostics := validation.NewErrors(validation.New(
		"title",
		serializers.CodeMaxLength,
		validation.NewParam("max_length", "200"),
	))
	response, err := api.ErrorResponse(http.StatusBadRequest, api.CodeValidationError, diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	if response.Status() != http.StatusBadRequest || response.Header().Get("Content-Type") != api.JSONContentType {
		t.Fatalf("response = %d %#v", response.Status(), response.Header())
	}
	want := `{"code":"validation_error","errors":[{"field":"title","code":"max_length","params":[{"key":"max_length","value":"200"}]}]}`
	if got := string(response.Body()); got != want {
		t.Fatalf("body = %s, want %s", got, want)
	}
	if _, err := api.ErrorResponse(http.StatusOK, api.CodeValidationError, diagnostics); !errors.Is(err, &api.Error{Code: api.FailureInvalidResponse}) {
		t.Fatalf("invalid status error = %v", err)
	}
}

func TestPageUsesRelativeLinksStableFieldOrderAndObjectResults(t *testing.T) {
	next, err := api.RelativeLink("/api/articles/?page=2&ordering=-id")
	if err != nil {
		t.Fatal(err)
	}
	article, err := serializers.NewObject(
		serializers.MemberOf("id", serializers.Integer(1)),
		serializers.MemberOf("title", serializers.String("Go")),
	)
	if err != nil {
		t.Fatal(err)
	}
	page, err := api.NewPage(3, next, api.Link{}, []serializers.Value{article.Value()})
	if err != nil {
		t.Fatal(err)
	}
	response, err := page.Response()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"count":3,"next":"/api/articles/?page=2&ordering=-id","previous":null,"results":[{"id":1,"title":"Go"}]}`
	if got := string(response.Body()); got != want {
		t.Fatalf("body = %s, want %s", got, want)
	}
	for _, invalid := range []string{
		"", "//evil.test/a", "https://evil.test/a", "/a\\b", "/a#fragment", "/a\n",
		"/api/../secret/?page=2", "/api//articles/?page=2", "/api/%61rticles/?page=2",
		"/api/%5Cevil/?page=2", "/api/%00/?page=2", "/api/%1f/?page=2",
	} {
		if _, err := api.RelativeLink(invalid); !errors.Is(err, &api.Error{Code: api.FailureInvalidConfig}) {
			t.Fatalf("RelativeLink(%q) error = %v", invalid, err)
		}
	}
	if _, err := api.NewPage(1, api.Link{}, api.Link{}, []serializers.Value{serializers.String("not-object")}); !errors.Is(err, &api.Error{Code: api.FailureInvalidConfig}) {
		t.Fatalf("non-object result error = %v", err)
	}
}

func TestRepresentationConvertsOnlyAPINotFoundAndMethodNotAllowed(t *testing.T) {
	middleware, err := api.Representation("/api/")
	if err != nil {
		t.Fatal(err)
	}
	application := testApplication(t, []web.Middleware{middleware}, []web.Route{{
		Name:    "test:existing",
		Method:  http.MethodGet,
		Path:    "/api/existing/",
		Handler: func(*web.Request) (web.Response, error) { return emptyOK(t), nil },
	}})
	tests := []struct {
		method      string
		path        string
		status      int
		contentType string
		allow       string
		body        string
	}{
		{method: http.MethodGet, path: "/api/missing/", status: 404, contentType: api.JSONContentType, body: `{"code":"not_found","errors":[]}`},
		{method: http.MethodPost, path: "/api/existing/", status: 405, contentType: api.JSONContentType, allow: "GET", body: `{"code":"method_not_allowed","errors":[]}`},
		{method: http.MethodGet, path: "/outside/", status: 404, contentType: "text/plain; charset=utf-8", body: "Not Found\n"},
	}
	for _, test := range tests {
		request := httptest.NewRequest(test.method, "http://example.test"+test.path, nil)
		recorder := httptest.NewRecorder()
		application.ServeHTTP(recorder, request)
		if recorder.Code != test.status || recorder.Header().Get("Content-Type") != test.contentType ||
			recorder.Header().Get("Allow") != test.allow || recorder.Body.String() != test.body {
			t.Fatalf("%s %s = status %d headers %#v body %q", test.method, test.path, recorder.Code, recorder.Header(), recorder.Body.String())
		}
	}
}

func TestRepresentationPreservesApplicationOwnedJSON404(t *testing.T) {
	middleware, err := api.Representation("/api/")
	if err != nil {
		t.Fatal(err)
	}
	diagnostics := validation.NewErrors(validation.New("id", "missing"))
	application := testApplication(t, []web.Middleware{middleware}, []web.Route{{
		Name: "test:detail", Method: http.MethodGet, Path: "/api/articles/1/", Handler: func(*web.Request) (web.Response, error) {
			return api.ErrorResponse(http.StatusNotFound, api.CodeNotFound, diagnostics)
		},
	}})

	request := httptest.NewRequest(http.MethodGet, "http://example.test/api/articles/1/", nil)
	recorder := httptest.NewRecorder()
	application.ServeHTTP(recorder, request)
	want := `{"code":"not_found","errors":[{"field":"id","code":"missing","params":[]}]}`
	if recorder.Code != http.StatusNotFound || recorder.Header().Get("Content-Type") != api.JSONContentType || recorder.Body.String() != want {
		t.Fatalf("response = status %d headers %#v body %q", recorder.Code, recorder.Header(), recorder.Body.String())
	}
}

func TestRepresentationCannotBeForgedByHandlerResponseFingerprint(t *testing.T) {
	middleware, err := api.Representation("/api/")
	if err != nil {
		t.Fatal(err)
	}
	application := testApplication(t, []web.Middleware{middleware}, []web.Route{{
		Name: "test:plain-domain-missing", Method: http.MethodGet, Path: "/api/domain-missing/", Handler: func(*web.Request) (web.Response, error) {
			header := make(http.Header)
			header.Set("Content-Type", "text/plain; charset=utf-8")
			header.Set("X-Domain-Outcome", "preserved")
			return web.NewResponse(http.StatusNotFound, header, []byte("Not Found\n"))
		},
	}})

	request := httptest.NewRequest(http.MethodGet, "http://example.test/api/domain-missing/", nil)
	recorder := httptest.NewRecorder()
	application.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound || recorder.Header().Get("X-Domain-Outcome") != "preserved" || recorder.Body.String() != "Not Found\n" {
		t.Fatalf("response = status %d headers %#v body %q", recorder.Code, recorder.Header(), recorder.Body.String())
	}
}

func TestJSONNegotiationRejectsSilentRendererFallbackOnlyInsideAPI(t *testing.T) {
	negotiation, err := api.JSONNegotiation("/api/")
	if err != nil {
		t.Fatal(err)
	}
	called := 0
	application := testApplication(t, []web.Middleware{negotiation}, []web.Route{
		{Name: "test:api", Method: http.MethodGet, Path: "/api/value/", Handler: func(*web.Request) (web.Response, error) {
			called++
			object, objectErr := serializers.NewObject(serializers.MemberOf("ok", serializers.Boolean(true)))
			if objectErr != nil {
				return web.Response{}, objectErr
			}
			return api.JSON(http.StatusOK, object.Value())
		}},
		{Name: "test:outside", Method: http.MethodGet, Path: "/outside/", Handler: func(*web.Request) (web.Response, error) {
			called++
			return emptyOK(t), nil
		}},
	})

	for _, test := range []struct {
		name       string
		path       string
		accept     string
		wantStatus int
		wantCalls  int
	}{
		{name: "missing accepted", path: "/api/value/", wantStatus: 200, wantCalls: 1},
		{name: "json accepted", path: "/api/value/", accept: "application/json", wantStatus: 200, wantCalls: 2},
		{name: "wildcard accepted", path: "/api/value/", accept: "text/html;q=0.9, */*;q=0.1", wantStatus: 200, wantCalls: 3},
		{name: "html rejected", path: "/api/value/", accept: "text/html", wantStatus: 406, wantCalls: 3},
		{name: "json quality zero", path: "/api/value/", accept: "application/json;q=0", wantStatus: 406, wantCalls: 3},
		{name: "exact exclusion beats wildcard", path: "/api/value/", accept: "application/json;q=0, */*;q=1", wantStatus: 406, wantCalls: 3},
		{name: "type exclusion beats wildcard", path: "/api/value/", accept: "application/*;q=0, */*;q=1", wantStatus: 406, wantCalls: 3},
		{name: "outside unaffected", path: "/outside/", accept: "text/html", wantStatus: 200, wantCalls: 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "http://example.test"+test.path, nil)
			if test.accept != "" {
				request.Header.Set("Accept", test.accept)
			}
			recorder := httptest.NewRecorder()
			application.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus || called != test.wantCalls {
				t.Fatalf("response = %d calls=%d body=%q", recorder.Code, called, recorder.Body.String())
			}
			if test.wantStatus == http.StatusNotAcceptable && recorder.Body.String() != `{"code":"not_acceptable","errors":[]}` {
				t.Fatalf("406 body = %q", recorder.Body.String())
			}
		})
	}
}

func TestNoContentHasEmptyBodyAndNoRepresentationHeader(t *testing.T) {
	response, err := api.NoContent()
	if err != nil {
		t.Fatal(err)
	}
	if response.Status() != http.StatusNoContent || len(response.Body()) != 0 || response.Header().Get("Content-Type") != "" {
		t.Fatalf("response = %d %#v %q", response.Status(), response.Header(), response.Body())
	}
}

func TestRequestErrorResponseMapsOnlyExpectedClientFailures(t *testing.T) {
	for _, test := range []struct {
		failure api.FailureCode
		status  int
		code    api.ResponseCode
		mapped  bool
	}{
		{failure: api.FailureUnsupportedMedia, status: 415, code: api.CodeUnsupportedMedia, mapped: true},
		{failure: api.FailureNotAcceptable, status: 406, code: api.CodeNotAcceptable, mapped: true},
		{failure: api.FailureBodyTooLarge, status: 413, code: api.CodeRequestTooLarge, mapped: true},
		{failure: api.FailureInvalidRequest, status: 400, code: api.CodeParseError, mapped: true},
		{failure: api.FailureBodyRead, mapped: false},
	} {
		response, mapped, err := api.RequestErrorResponse(&api.Error{Code: test.failure})
		if err != nil || mapped != test.mapped {
			t.Fatalf("map %s = %v, %v", test.failure, mapped, err)
		}
		if !mapped {
			continue
		}
		if response.Status() != test.status || !strings.Contains(string(response.Body()), `"code":"`+string(test.code)+`"`) {
			t.Fatalf("response = %d %s", response.Status(), response.Body())
		}
	}
}

func testApplication(t *testing.T, middleware []web.Middleware, routes []web.Route) *web.Application {
	t.Helper()
	configured, err := settings.New(settings.Definition{
		ProjectName: "api_test",
		InstalledApps: []apps.Config{{
			Name:  "example.test/api",
			Label: "test",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	application, err := web.NewApplication(web.Config{Settings: configured, Routes: routes, Middleware: middleware})
	if err != nil {
		t.Fatal(err)
	}
	return application
}

func emptyOK(t *testing.T) web.Response {
	t.Helper()
	response, err := web.NewResponse(http.StatusOK, make(http.Header), nil)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

var errForcedRead = errors.New("forced read failure")

type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, errForcedRead }
func (failingBody) Close() error             { return nil }

var _ io.ReadCloser = failingBody{}
