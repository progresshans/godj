package web_test

import (
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/web"
)

func TestParameterizedRoutePublicSurfaceAndBorrowedParameter(t *testing.T) {
	var captured *web.Request
	application := newTestApplication(t, web.Config{Routes: []web.Route{
		{Name: "articles:list", Method: http.MethodGet, Path: "/articles/", Handler: textHandler("list")},
		{Name: "articles:revision", Method: http.MethodGet, Path: "/articles/<int64:id>/revisions/<int64:revision>/", Handler: func(request *web.Request) (web.Response, error) {
			captured = request
			id, idFound := request.Int64Parameter("id")
			revision, revisionFound := request.Int64Parameter("revision")
			if !idFound || !revisionFound || id != 42 || revision != 7 {
				return testResponse(http.StatusInternalServerError, "parameters unavailable")
			}
			if _, found := request.Int64Parameter("missing"); found {
				return testResponse(http.StatusInternalServerError, "unexpected parameter")
			}
			path, err := request.ReverseWith(
				"articles:revision",
				web.Int64Argument("revision", revision),
				web.Int64Argument("id", id),
			)
			if err != nil || path != "/articles/42/revisions/7/" {
				return testResponse(http.StatusInternalServerError, "request reverse failed")
			}
			return testResponse(http.StatusOK, "matched")
		}},
	}})

	if path, err := application.Reverse("articles:list"); err != nil || path != "/articles/" {
		t.Fatalf("static Reverse() = %q, %v", path, err)
	}
	path, err := application.ReverseWith(
		"articles:revision",
		web.Int64Argument("id", 42),
		web.Int64Argument("revision", 7),
	)
	if err != nil || path != "/articles/42/revisions/7/" {
		t.Fatalf("ReverseWith() = %q, %v", path, err)
	}
	response := serve(application, http.MethodGet, path)
	if response.Code != http.StatusOK || response.Body.String() != "matched" {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	if captured == nil {
		t.Fatal("handler did not capture request")
	}
	if value, found := captured.Int64Parameter("id"); found || value != 0 {
		t.Fatalf("released Int64Parameter() = %d, %t", value, found)
	}
	if _, err := captured.ReverseWith("articles:revision", web.Int64Argument("id", 42), web.Int64Argument("revision", 7)); !errors.Is(err, &web.Error{Code: web.CodeInvalidRequest}) {
		t.Fatalf("released ReverseWith() error = %v", err)
	}
}

func TestParameterizedRouteCanonicalInt64Grammar(t *testing.T) {
	application := newTestApplication(t, web.Config{Routes: []web.Route{{
		Name: "articles:detail", Method: http.MethodGet, Path: "/articles/<int64:id>/", Handler: func(request *web.Request) (web.Response, error) {
			value, found := request.Int64Parameter("id")
			if !found {
				return testResponse(http.StatusInternalServerError, "missing")
			}
			return testResponse(http.StatusOK, strconv.FormatInt(value, 10))
		},
	}}})

	accepted := []string{"0", "1", "9", "10", "9223372036854775807"}
	for _, segment := range accepted {
		t.Run("accepted_"+segment, func(t *testing.T) {
			response := serve(application, http.MethodGet, "/articles/"+segment+"/")
			if response.Code != http.StatusOK || response.Body.String() != segment {
				t.Fatalf("response = %d %q", response.Code, response.Body.String())
			}
		})
	}

	rejected := []string{
		"", "00", "01", "+1", "-1", "1.0", "%201", "1%20", "１", "abc",
		"9223372036854775808", "18446744073709551615", "999999999999999999999999999999999",
		"%2f", "%2F", "%5c", "%5C", "%2e", "%2E", "%00", "%1f", "%7f",
	}
	for _, segment := range rejected {
		t.Run("rejected_"+strings.ReplaceAll(segment, "%", "percent"), func(t *testing.T) {
			response := serve(application, http.MethodGet, "/articles/"+segment+"/")
			if response.Code != http.StatusNotFound || response.Body.String() != "Not Found\n" {
				t.Fatalf("response = %d %q", response.Code, response.Body.String())
			}
		})
	}

	for _, path := range []string{
		"/articles/1", "/articles//1/", "/articles/./1/", "/articles/../1/",
		"/articles/%2e%2e/1/", "/articles/1%2f2/", "/articles/1%5c2/", "/articles/1%001/",
	} {
		t.Run("invalid_path_"+strings.ReplaceAll(path, "/", "_"), func(t *testing.T) {
			response := serve(application, http.MethodGet, path)
			if response.Code != http.StatusNotFound {
				t.Fatalf("response = %d %q", response.Code, response.Body.String())
			}
		})
	}
}

func TestStaticPathShadowsParametersForEveryMethodAndOrder(t *testing.T) {
	routes := []web.Route{
		{Name: "articles:dynamic-get", Method: http.MethodGet, Path: "/articles/<int64:id>/", Handler: textHandler("dynamic get")},
		{Name: "articles:dynamic-delete", Method: http.MethodDelete, Path: "/articles/<int64:id>/", Handler: textHandler("dynamic delete")},
		{Name: "articles:static", Method: http.MethodPost, Path: "/articles/7/", Handler: textHandler("static")},
	}
	orders := [][]web.Route{routes, []web.Route{routes[2], routes[1], routes[0]}}
	for index, order := range orders {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			application := newTestApplication(t, web.Config{Routes: order})
			response := serve(application, http.MethodPost, "/articles/7/")
			if response.Code != http.StatusOK || response.Body.String() != "static" {
				t.Fatalf("static response = %d %q", response.Code, response.Body.String())
			}
			for _, method := range []string{http.MethodGet, http.MethodDelete, http.MethodPut} {
				response = serve(application, method, "/articles/7/")
				if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != "POST" {
					t.Fatalf("%s shadow response = %d Allow=%q", method, response.Code, response.Header().Get("Allow"))
				}
			}
			response = serve(application, http.MethodGet, "/articles/8/")
			if response.Code != http.StatusOK || response.Body.String() != "dynamic get" {
				t.Fatalf("dynamic response = %d %q", response.Code, response.Body.String())
			}
			response = serve(application, http.MethodPatch, "/articles/8/")
			if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != "DELETE, GET" {
				t.Fatalf("dynamic 405 = %d Allow=%q", response.Code, response.Header().Get("Allow"))
			}
			response = serve(application, http.MethodPost, "/articles%2f7/")
			if response.Code != http.StatusNotFound {
				t.Fatalf("encoded static path response = %d %q", response.Code, response.Body.String())
			}
		})
	}
}

func TestParameterizedRouterPreservesEncodedDotInsideStaticSegment(t *testing.T) {
	application := newTestApplication(t, web.Config{Routes: []web.Route{{
		Name: "articles:versioned-static", Method: http.MethodGet, Path: "/articles/report.v1/", Handler: textHandler("static"),
	}}})

	response := serve(application, http.MethodGet, "/articles/report%2ev1/")
	if response.Code != http.StatusOK || response.Body.String() != "static" {
		t.Fatalf("encoded-dot static response = %d %q", response.Code, response.Body.String())
	}
	for _, unsafe := range []string{"/articles/%2e/", "/articles/%2e%2e/"} {
		response = serve(application, http.MethodGet, unsafe)
		if response.Code != http.StatusNotFound {
			t.Fatalf("dot-segment %q response = %d %q", unsafe, response.Code, response.Body.String())
		}
	}
}

func TestParameterizedRouteRejectsAmbiguousLanguages(t *testing.T) {
	tests := []struct {
		name   string
		left   string
		right  string
		method string
	}{
		{name: "same language different name", left: "/articles/<int64:id>/", right: "/articles/<int64:article>/", method: http.MethodGet},
		{name: "partially overlapping languages", left: "/pairs/<int64:left>/0/", right: "/pairs/7/<int64:right>/", method: http.MethodGet},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := web.NewApplication(web.Config{Settings: testSettings(t), Routes: []web.Route{
				{Name: "articles:left", Method: test.method, Path: test.left, Handler: textHandler("left")},
				{Name: "articles:right", Method: test.method, Path: test.right, Handler: textHandler("right")},
			}})
			if !errors.Is(err, &web.Error{Code: web.CodeDuplicateRoute, Field: "method_path"}) {
				t.Fatalf("NewApplication() error = %v", err)
			}
		})
	}

	application := newTestApplication(t, web.Config{Routes: []web.Route{
		{Name: "articles:get", Method: http.MethodGet, Path: "/articles/<int64:id>/", Handler: textHandler("get")},
		{Name: "articles:post", Method: http.MethodPost, Path: "/articles/<int64:article>/", Handler: textHandler("post")},
		{Name: "articles:non-overlap", Method: http.MethodGet, Path: "/articles/<int64:id>/literal/", Handler: textHandler("literal")},
	}})
	if response := serve(application, http.MethodPost, "/articles/1/"); response.Code != http.StatusOK || response.Body.String() != "post" {
		t.Fatalf("different-method response = %d %q", response.Code, response.Body.String())
	}
}

func TestParameterizedRouteRejectsInvalidPatternsAndResourceExhaustion(t *testing.T) {
	tooManySegments := "/" + strings.Repeat("segment/", 65) + "<int64:id>/"
	tooManyParameters := "/"
	for index := 0; index < 17; index++ {
		tooManyParameters += "<int64:p" + strconv.Itoa(index) + ">/"
	}
	tests := []struct {
		name string
		path string
	}{
		{name: "unknown converter", path: "/articles/<uuid:id>/"},
		{name: "empty name", path: "/articles/<int64:>/"},
		{name: "invalid initial", path: "/articles/<int64:9id>/"},
		{name: "invalid hyphen", path: "/articles/<int64:article-id>/"},
		{name: "non ASCII name", path: "/articles/<int64:아이디>/"},
		{name: "embedded parameter", path: "/articles/prefix<int64:id>/"},
		{name: "duplicate parameter", path: "/<int64:id>/<int64:id>/"},
		{name: "malformed open", path: "/articles/<int64:id/"},
		{name: "malformed close", path: "/articles/int64:id>/"},
		{name: "curly syntax remains invalid", path: "/articles/{id}/"},
		{name: "double slash", path: "/articles//<int64:id>/"},
		{name: "path byte cap", path: "/" + strings.Repeat("a", 4097) + "/<int64:id>/"},
		{name: "segment cap", path: tooManySegments},
		{name: "parameter cap", path: tooManyParameters},
		{name: "parameter name byte cap", path: "/articles/<int64:" + strings.Repeat("a", 65) + ">/"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := web.NewApplication(web.Config{Settings: testSettings(t), Routes: []web.Route{{
				Name: "articles:detail", Method: http.MethodGet, Path: test.path, Handler: textHandler("detail"),
			}}})
			if !errors.Is(err, &web.Error{Code: web.CodeInvalidRoute}) {
				t.Fatalf("NewApplication(path=%q) error = %v", test.path, err)
			}
		})
	}

	tooManyRoutes := make([]web.Route, 1025)
	for index := range tooManyRoutes {
		tooManyRoutes[index] = web.Route{
			Name:    "articles:route" + strconv.Itoa(index),
			Method:  http.MethodGet,
			Path:    "/route" + strconv.Itoa(index) + "/",
			Handler: textHandler("route"),
		}
	}
	if _, err := web.NewApplication(web.Config{Settings: testSettings(t), Routes: tooManyRoutes[:1024]}); err != nil {
		t.Fatalf("NewApplication(max routes) error = %v", err)
	}
	if _, err := web.NewApplication(web.Config{Settings: testSettings(t), Routes: tooManyRoutes}); !errors.Is(err, &web.Error{Code: web.CodeInvalidRoute, Field: "routes"}) {
		t.Fatalf("NewApplication(too many routes) error = %v", err)
	}

	application := newTestApplication(t, web.Config{Routes: []web.Route{{
		Name: "articles:detail", Method: http.MethodGet, Path: "/articles/<int64:id>/", Handler: textHandler("detail"),
	}}})
	request := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	request.URL.Path = "/articles/" + strings.Repeat("9", 4096) + "/"
	request.RequestURI = ""
	response := httptest.NewRecorder()
	application.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("oversized request response = %d %q", response.Code, response.Body.String())
	}
}

func TestParameterizedRouteAcceptsExactResourceBoundaries(t *testing.T) {
	parameterPattern := "/"
	arguments := make([]web.ReverseArgument, 0, 16)
	for index := 0; index < 16; index++ {
		name := "p" + strconv.Itoa(index)
		parameterPattern += "<int64:" + name + ">/"
		arguments = append(arguments, web.Int64Argument(name, int64(index)))
	}
	segmentPattern := "/" + strings.Repeat("segment/", 63) + "<int64:id>/"
	maximumName := strings.Repeat("n", 64)
	maximumBytesPattern := "/" + strings.Repeat("a", 4083) + "/<int64:id>/"
	application := newTestApplication(t, web.Config{Routes: []web.Route{
		{Name: "articles:parameters", Method: http.MethodGet, Path: parameterPattern, Handler: textHandler("parameters")},
		{Name: "articles:segments", Method: http.MethodGet, Path: segmentPattern, Handler: textHandler("segments")},
		{Name: "articles:name", Method: http.MethodGet, Path: "/<int64:" + maximumName + ">/", Handler: textHandler("name")},
		{Name: "articles:bytes", Method: http.MethodGet, Path: maximumBytesPattern, Handler: textHandler("bytes")},
	}})

	if path, err := application.ReverseWith("articles:parameters", arguments...); err != nil || path == "" {
		t.Fatalf("ReverseWith(max parameters) = %q, %v", path, err)
	}
	if path, err := application.ReverseWith("articles:segments", web.Int64Argument("id", 1)); err != nil || strings.Count(strings.Trim(path, "/"), "/")+1 != 64 {
		t.Fatalf("ReverseWith(max segments) = %q, %v", path, err)
	}
	if path, err := application.ReverseWith("articles:name", web.Int64Argument(maximumName, 1)); err != nil || path != "/1/" {
		t.Fatalf("ReverseWith(max name) = %q, %v", path, err)
	}
	if len(maximumBytesPattern) != 4096 {
		t.Fatalf("test pattern bytes = %d", len(maximumBytesPattern))
	}
	if path, err := application.ReverseWith("articles:bytes", web.Int64Argument("id", 0)); err != nil || len(path) >= 4096 {
		t.Fatalf("ReverseWith(max pattern bytes) length = %d, error = %v", len(path), err)
	}
	if _, err := application.ReverseWith("articles:bytes", web.Int64Argument("id", math.MaxInt64)); !errors.Is(err, &web.Error{Code: web.CodeReverseArguments}) {
		t.Fatalf("ReverseWith(oversized result) error = %v", err)
	}

	maximumStaticPath := "/" + strings.Repeat("s", 4094) + "/"
	staticApplication := newTestApplication(t, web.Config{Routes: []web.Route{{
		Name: "articles:maximum-static", Method: http.MethodGet, Path: maximumStaticPath, Handler: textHandler("static"),
	}}})
	if response := serve(staticApplication, http.MethodGet, maximumStaticPath); response.Code != http.StatusOK || response.Body.String() != "static" {
		t.Fatalf("maximum input path response = %d %q", response.Code, response.Body.String())
	}
}

func TestParameterizedReverseRejectsInvalidArgumentSets(t *testing.T) {
	application := newTestApplication(t, web.Config{Routes: []web.Route{
		{Name: "articles:list", Method: http.MethodGet, Path: "/articles/", Handler: textHandler("list")},
		{Name: "articles:detail", Method: http.MethodGet, Path: "/articles/<int64:id>/", Handler: textHandler("detail")},
	}})

	for _, value := range []int64{0, 1, math.MaxInt64} {
		path, err := application.ReverseWith("articles:detail", web.Int64Argument("id", value))
		want := "/articles/" + strconv.FormatInt(value, 10) + "/"
		if err != nil || path != want {
			t.Fatalf("ReverseWith(%d) = %q, %v, want %q", value, path, err, want)
		}
	}
	tests := []struct {
		name      string
		routeName string
		arguments []web.ReverseArgument
	}{
		{name: "missing", routeName: "articles:detail"},
		{name: "extra static", routeName: "articles:list", arguments: []web.ReverseArgument{web.Int64Argument("id", 1)}},
		{name: "extra dynamic", routeName: "articles:detail", arguments: []web.ReverseArgument{web.Int64Argument("id", 1), web.Int64Argument("extra", 2)}},
		{name: "wrong kind", routeName: "articles:detail", arguments: []web.ReverseArgument{{}}},
		{name: "negative", routeName: "articles:detail", arguments: []web.ReverseArgument{web.Int64Argument("id", -1)}},
		{name: "wrong name", routeName: "articles:detail", arguments: []web.ReverseArgument{web.Int64Argument("other", 1)}},
		{name: "duplicate", routeName: "articles:detail", arguments: []web.ReverseArgument{web.Int64Argument("id", 1), web.Int64Argument("id", 2)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := application.ReverseWith(test.routeName, test.arguments...)
			if !errors.Is(err, &web.Error{Code: web.CodeReverseArguments, Field: "arguments"}) {
				t.Fatalf("ReverseWith() error = %v", err)
			}
		})
	}
	if _, err := application.Reverse("articles:detail"); !errors.Is(err, &web.Error{Code: web.CodeReverseArguments}) {
		t.Fatalf("Reverse(parameterized) error = %v", err)
	}
	if _, err := application.ReverseWith("articles:missing", web.Int64Argument("id", 1)); !errors.Is(err, &web.Error{Code: web.CodeReverseNotFound}) {
		t.Fatalf("ReverseWith(missing) error = %v", err)
	}
}

func FuzzParameterizedRouteCanonicalInt64(f *testing.F) {
	configured := testSettings(f)
	application, err := web.NewApplication(web.Config{Settings: configured, Routes: []web.Route{{
		Name: "articles:detail", Method: http.MethodGet, Path: "/articles/<int64:id>/", Handler: textHandler("matched"),
	}}})
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range []string{"0", "1", "01", "-1", "9223372036854775807", "9223372036854775808", "abc", "\x00"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, segment string) {
		request := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
		request.URL.Path = "/articles/" + segment + "/"
		request.RequestURI = ""
		response := httptest.NewRecorder()
		application.ServeHTTP(response, request)
		_, parseError := strconv.ParseInt(segment, 10, 64)
		canonical := parseError == nil && (segment == "0" || segment != "" && segment[0] >= '1' && segment[0] <= '9')
		if canonical && response.Code != http.StatusOK {
			t.Fatalf("canonical %q response = %d %q", segment, response.Code, response.Body.String())
		}
		if !canonical && response.Code != http.StatusNotFound {
			t.Fatalf("non-canonical %q response = %d %q", segment, response.Code, response.Body.String())
		}
	})
}
