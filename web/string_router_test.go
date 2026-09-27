package web_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/web"
)

func TestStringRoutesBorrowTypesAndEscapeExactlyOnce(t *testing.T) {
	var borrowed *web.Request
	app := newTestApplication(t, web.Config{Routes: []web.Route{{Name: "articles:string", Method: "GET", Path: "/reset/<str:uid>/<str:token>/<int64:attempt>/", Handler: func(r *web.Request) (web.Response, error) {
		borrowed = r
		uid, u := r.StringParameter("uid")
		token, v := r.StringParameter("token")
		attempt, w := r.Int64Parameter("attempt")
		if !u || !v || !w || uid != "한글 %2f?#" || token != "r1.a.A-_" || attempt != 7 {
			return web.Response{}, errors.New("wrong typed parameters")
		}
		if _, ok := r.Int64Parameter("uid"); ok {
			return web.Response{}, errors.New("coerced text")
		}
		if _, ok := r.StringParameter("attempt"); ok {
			return web.Response{}, errors.New("coerced integer")
		}
		if _, ok := r.StringParameter("missing"); ok {
			return web.Response{}, errors.New("missing value present")
		}
		path, err := r.ReverseWith("articles:string", web.StringArgument("token", token), web.Int64Argument("attempt", attempt), web.StringArgument("uid", uid))
		if err != nil {
			return web.Response{}, err
		}
		return testResponse(200, path)
	}}}})
	const want = "/reset/%ED%95%9C%EA%B8%80%20%252f%3F%23/r1.a.A-_/7/"
	path, err := app.ReverseWith("articles:string", web.Int64Argument("attempt", 7), web.StringArgument("uid", "한글 %2f?#"), web.StringArgument("token", "r1.a.A-_"))
	if err != nil || path != want {
		t.Fatalf("reverse = %q, %v", path, err)
	}
	response := serve(app, "GET", path)
	if response.Code != 200 || response.Body.String() != want {
		t.Fatalf("roundtrip = %d %q", response.Code, response.Body.String())
	}
	if value, ok := borrowed.StringParameter("token"); ok || value != "" {
		t.Fatal("released request retained parameter access")
	}
	var nilRequest *web.Request
	if _, ok := nilRequest.StringParameter("token"); ok {
		t.Fatal("nil request exposed parameter")
	}
	description, err := web.DescribeRoutePath("/reset/<str:uid>/<str:token>/<int64:attempt>/")
	wantParameters := []web.RouteParameterDescription{{Name: "uid", Kind: web.RouteParameterString, MaxBytes: 512}, {Name: "token", Kind: web.RouteParameterString, MaxBytes: 512}, {Name: "attempt", Kind: web.RouteParameterInt64, MaxBytes: 19}}
	if err != nil || description.Template != "/reset/{uid}/{token}/{attempt}/" || !reflect.DeepEqual(description.Parameters, wantParameters) {
		t.Fatalf("description = %#v %v", description, err)
	}
}

func TestStringRouteLimitsInjectionAndWrongArguments(t *testing.T) {
	app := newTestApplication(t, web.Config{Routes: []web.Route{{Name: "articles:string", Method: "GET", Path: "/v/<str:value>/", Handler: func(r *web.Request) (web.Response, error) {
		v, _ := r.StringParameter("value")
		return testResponse(200, v)
	}}, {Name: "articles:int", Method: "GET", Path: "/i/<int64:id>/", Handler: textHandler("integer")}}})
	for _, value := range []string{"a", "01", "-1", "r1.a.b", "...", ".a", "a.", "..a", strings.Repeat("x", 512), strings.Repeat("é", 256), "%2F", "?query#fragment", "a b", "a:b@c", "e\u0301"} {
		t.Run("valid_"+fmt.Sprint(len(value))+"_"+value[:min(8, len(value))], func(t *testing.T) {
			path, err := app.ReverseWith("articles:string", web.StringArgument("value", value))
			if err != nil {
				t.Fatal(err)
			}
			got := serve(app, "GET", path)
			if got.Code != 200 || got.Body.String() != value {
				t.Fatal("string did not round trip", got.Code)
			}
		})
	}
	for _, value := range []string{"", ".", "..", "/", "a/b", "a\\b", "a\x00b", "a\x1fb", "a\x7fb", "\xff", strings.Repeat("a", 513), strings.Repeat("é", 257)} {
		t.Run("reject_"+fmt.Sprint(len(value))+"_"+fmt.Sprintf("%x", []byte(value[:min(8, len(value))])), func(t *testing.T) {
			if _, err := app.ReverseWith("articles:string", web.StringArgument("value", value)); !errors.Is(err, &web.Error{Code: web.CodeReverseArguments}) {
				t.Fatal("invalid reverse accepted", err)
			}
			path := "/v/" + url.PathEscape(value) + "/"
			got := serve(app, "GET", path)
			if got.Code != 404 {
				t.Fatal("invalid segment routed", got.Code)
			}
		})
	}
	for _, path := range []string{"/v/a%2fb/", "/v/a%5Cb/", "/v/a%00b/", "/v/a%7Fb/", "/v/%2e/", "/v/%2e%2e/", "/v/a", "/v/a//"} {
		if got := serve(app, "GET", path); got.Code != 404 {
			t.Fatalf("unsafe path routed: %q %d", path, got.Code)
		}
	}
	for _, args := range [][]web.ReverseArgument{nil, {{}}, {web.Int64Argument("value", 1)}, {web.StringArgument("unknown", "x")}, {web.StringArgument("value", "x"), web.StringArgument("value", "y")}, {web.StringArgument("value", "x"), web.StringArgument("extra", "y")}} {
		if _, err := app.ReverseWith("articles:string", args...); !errors.Is(err, &web.Error{Code: web.CodeReverseArguments}) {
			t.Fatal("bad argument set accepted", err)
		}
	}
	if _, err := app.ReverseWith("articles:int", web.StringArgument("id", "1")); !errors.Is(err, &web.Error{Code: web.CodeReverseArguments}) {
		t.Fatal("text coerced to integer")
	}
	// Each segment fits; the overall decoded/escaped path budgets still apply.
	for _, suffix := range []string{strings.Repeat("a", 3600), strings.Repeat("%", 1200)} {
		long := newTestApplication(t, web.Config{Routes: []web.Route{{Name: "articles:long", Method: "GET", Path: "/<str:value>/" + suffix, Handler: textHandler("unexpected")}}})
		if _, err := long.ReverseWith("articles:long", web.StringArgument("value", strings.Repeat("é", 256))); !errors.Is(err, &web.Error{Code: web.CodeReverseArguments}) {
			t.Fatal("full path budget bypassed", err)
		}
	}
	for _, path := range []string{"/<str:>/", "/<str:9bad>/", "/<str:a-b>/", "/<str:id>/<int64:id>/", "/prefix<str:id>/", "/<str:" + strings.Repeat("a", 65) + ">/"} {
		if err := web.ValidateRouteDeclarations([]web.Route{{Name: "articles:bad", Method: "GET", Path: path, Handler: textHandler("bad")}}); !errors.Is(err, &web.Error{Code: web.CodeInvalidRoute}) {
			t.Fatal("invalid declaration accepted", path, err)
		}
	}
}

func TestStringRouteLanguagesOverlapAndStaticPrecedence(t *testing.T) {
	for _, pair := range [][2]string{{"/v/<str:a>/", "/v/<str:b>/"}, {"/v/<str:a>/", "/v/<int64:b>/"}, {"/<str:a>/0/", "/token/<int64:b>/"}, {"/<str:a>/01/", "/token/<str:b>/"}} {
		for _, reverse := range []bool{false, true} {
			left, right := pair[0], pair[1]
			if reverse {
				left, right = right, left
			}
			err := web.ValidateRouteDeclarations([]web.Route{{Name: "articles:a", Method: "GET", Path: left, Handler: textHandler("a")}, {Name: "articles:b", Method: "GET", Path: right, Handler: textHandler("b")}})
			if !errors.Is(err, &web.Error{Code: web.CodeDuplicateRoute}) {
				t.Fatal("overlap accepted", pair, err)
			}
		}
	}
	// Different methods retain distinct types. Invalid integer spellings match
	// only the string method, so Allow is input-sensitive and sorted.
	routes := []web.Route{{Name: "articles:get", Method: "GET", Path: "/v/<str:a>/", Handler: textHandler("string")}, {Name: "articles:post", Method: "POST", Path: "/v/<int64:a>/", Handler: textHandler("integer")}, {Name: "articles:static", Method: "PUT", Path: "/v/token/", Handler: textHandler("static")}}
	for _, order := range [][]web.Route{routes, {routes[2], routes[1], routes[0]}} {
		app := newTestApplication(t, web.Config{Routes: order})
		for _, c := range []struct {
			path, method string
			status       int
			body, allow  string
		}{{"/v/0/", "DELETE", 405, "", "GET, POST"}, {"/v/01/", "POST", 405, "", "GET"}, {"/v/token/", "GET", 405, "", "PUT"}, {"/v/token/", "PUT", 200, "static", ""}, {"/v/0/", "POST", 200, "integer", ""}, {"/v/0/", "GET", 200, "string", ""}} {
			got := serve(app, c.method, c.path)
			if got.Code != c.status || got.Header().Get("Allow") != c.allow || c.body != "" && got.Body.String() != c.body {
				t.Fatal("precedence/method mismatch", c, got.Code, got.Header())
			}
		}
	}
	for _, pair := range [][2]string{{"/<str:a>/x/", "/<int64:b>/y/"}, {"/token/<int64:id>/", "/<str:a>/01/"}} {
		if err := web.ValidateRouteDeclarations([]web.Route{{Name: "articles:a", Method: "GET", Path: pair[0], Handler: textHandler("a")}, {Name: "articles:b", Method: "GET", Path: pair[1], Handler: textHandler("b")}}); err != nil {
			t.Fatal("disjoint languages rejected", err)
		}
	}
}

func TestStringRoutePrefixUsesKindsAndByteBudgets(t *testing.T) {
	for _, c := range []struct {
		route, prefix string
		all, some     bool
	}{{"/reset/<str:uid>/<str:token>/", "/reset/", true, true}, {"/reset/<str:uid>/<str:token>/", "/reset/a/", false, true}, {"/reset/<str:uid>/<str:token>/", "/reset/a/r1.b.c/", false, true}, {"/reset/<str:uid>/<str:token>/", "/reset/01/", false, true}, {"/reset/<str:uid>/<int64:id>/", "/reset/a/01/", false, false}, {"/reset/<str:uid>/", "/reset/" + strings.Repeat("x", 513) + "/", false, false}, {"/reset/<str:uid>/", "/reset/" + strings.Repeat("é", 257) + "/", false, false}, {"/reset/<str:uid>", "/reset/a/", false, false}, {"/reset/<str:uid>/", "/other/", false, false}, {"/<str:uid>/" + strings.Repeat("x", 4000), "/" + strings.Repeat("a", 100) + "/", false, false}} {
		got, err := web.DescribeRoutePrefix(c.route, c.prefix)
		if err != nil || got.All != c.all || got.Some != c.some {
			t.Fatalf("prefix %q = %#v %v", c.prefix, got, err)
		}
	}
}

func TestStringRouteRealHTTPConcurrentRoundTrips(t *testing.T) {
	app := newTestApplication(t, web.Config{Routes: []web.Route{{Name: "articles:wire", Method: "GET", Path: "/v/<str:value>/", Handler: func(r *web.Request) (web.Response, error) {
		v, ok := r.StringParameter("value")
		if !ok {
			return web.Response{}, errors.New("parameter missing")
		}
		return testResponse(200, v)
	}}}})
	server := httptest.NewServer(app)
	defer server.Close()
	var workers sync.WaitGroup
	for _, value := range []string{"r1.abc.abcdef_-", "dXNlci0x", "set-password", "é한글", "%2f", "?#", "a b", strings.Repeat("x", 512)} {
		workers.Go(func() {
			path, err := app.ReverseWith("articles:wire", web.StringArgument("value", value))
			if err != nil {
				t.Error(err)
				return
			}
			response, err := server.Client().Get(server.URL + path)
			if err != nil {
				t.Error(err)
				return
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil || response.StatusCode != 200 || string(body) != value {
				t.Error("wire string mismatch", response.StatusCode, err)
			}
		})
	}
	workers.Wait()
}

func TestRouteDiagnosticsDoNotLogUntrustedPathValues(t *testing.T) {
	const secret = "private-reset-proof-r1.abc.secret"
	for _, mode := range []string{"handler", "panic", "middleware-before", "middleware-after", "response-budget", "unmatched"} {
		t.Run(mode, func(t *testing.T) {
			var log bytes.Buffer
			config := web.Config{Logger: slog.New(slog.NewJSONHandler(&log, nil)), Routes: []web.Route{{Name: "articles:reset", Method: "GET", Path: "/reset/<str:proof>/", Handler: func(*web.Request) (web.Response, error) {
				switch mode {
				case "panic":
					panic(secret)
				case "response-budget":
					return testResponse(200, strings.Repeat("x", 1025))
				case "handler":
					return web.Response{}, errors.New("safe failure")
				}
				return testResponse(200, "ok")
			}}}, MaxResponseBytes: 1024}
			if strings.HasPrefix(mode, "middleware") || mode == "unmatched" {
				config.Middleware = []web.Middleware{func(next web.Handler) web.Handler {
					return func(r *web.Request) (web.Response, error) {
						if mode == "middleware-after" {
							_, _ = next(r)
						}
						return web.Response{}, errors.New("safe middleware failure")
					}
				}}
			}
			path := "/reset/" + secret + "/"
			if mode == "unmatched" {
				path = "/unknown/" + secret + "/"
			}
			response := serve(newTestApplication(t, config), "GET", path)
			if response.Code != 500 || strings.Contains(log.String(), secret) || strings.Contains(log.String(), "/reset/") || strings.Contains(response.Body.String(), secret) {
				t.Fatal("raw path diagnostic leaked", response.Code, log.String())
			}
			var entry map[string]any
			if err := json.Unmarshal(log.Bytes(), &entry); err != nil {
				t.Fatal(err)
			}
			expected := "articles:reset"
			if mode == "middleware-before" || mode == "unmatched" {
				expected = ""
			}
			if entry["route"] != expected || entry["method"] != "GET" {
				t.Fatal("diagnostic lost route identity", entry)
			}
		})
	}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
		if strings.Contains(fmt.Sprintf(verb, web.StringArgument("proof", secret)), secret) {
			t.Fatal("argument formatting leaked")
		}
	}
}

func TestStringRoutePinnedDjangoReference(t *testing.T) {
	var reference struct {
		Django, Python string
		Source         map[string]string `json:"source_sha256"`
		Observations   []struct {
			Name, Value, Reverse string
			Matched              bool
		} `json:"observations"`
	}
	raw, err := os.ReadFile("testdata/string-routing-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || reference.Python != "3.14.3" || len(reference.Source) != 3 || len(reference.Observations) != 24 {
		t.Fatal("incomplete reference")
	}
	app := newTestApplication(t, web.Config{Routes: []web.Route{{Name: "articles:reference", Method: "GET", Path: "/v/<str:value>/", Handler: func(r *web.Request) (web.Response, error) {
		v, _ := r.StringParameter("value")
		return testResponse(200, v)
	}}}})
	deviations := map[string]bool{"dot": true, "dotdot": true, "backslash": true, "nul": true, "control": true, "delete": true, "ascii_over_limit": true, "unicode_over_limit": true}
	for _, observation := range reference.Observations {
		t.Run(observation.Name, func(t *testing.T) {
			want := observation.Matched && !deviations[observation.Name]
			path, err := app.ReverseWith("articles:reference", web.StringArgument("value", observation.Value))
			if !want {
				if !errors.Is(err, &web.Error{Code: web.CodeReverseArguments}) {
					t.Fatal("expected refusal", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			// Both frameworks may choose different legal percent spellings. Decode
			// once to compare values; request/response below verifies the actual URL.
			djangoPath, e := url.PathUnescape(observation.Reverse)
			goPath, f := url.PathUnescape(path)
			if e != nil || f != nil || djangoPath != goPath {
				t.Fatal("native reverse meaning differs")
			}
			response := serve(app, "GET", path)
			if response.Code != 200 || response.Body.String() != observation.Value {
				t.Fatal("native resolver value differs")
			}
		})
	}
}

func TestStringRouteOverlapRespectsWholePathLimit(t *testing.T) {
	left := strings.Repeat("a", 400)
	right := strings.Repeat("b", 400)
	for _, limit := range []int{4096, 4097} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			suffix := strings.Repeat("x", limit-len(left)-len(right)-3)
			routes := []web.Route{
				{Name: "articles:left", Method: "GET", Path: "/" + left + "/<str:b>/" + suffix, Handler: textHandler("left")},
				{Name: "articles:right", Method: "GET", Path: "/<str:a>/" + right + "/" + suffix, Handler: textHandler("right")},
			}
			err := web.ValidateRouteDeclarations(routes)
			if limit == 4096 {
				if !errors.Is(err, &web.Error{Code: web.CodeDuplicateRoute}) {
					t.Fatal("boundary overlap was accepted", err)
				}
				return
			}
			if err != nil {
				t.Fatal("impossible oversized intersection rejected disjoint routes", err)
			}
			app := newTestApplication(t, web.Config{Routes: routes})
			for _, c := range []struct{ path, body string }{{"/" + left + "/0/" + suffix, "left"}, {"/0/" + right + "/" + suffix, "right"}} {
				response := serve(app, "GET", c.path)
				if response.Code != 200 || response.Body.String() != c.body {
					t.Fatal("disjoint route was not independently reachable")
				}
			}
			if response := serve(app, "GET", "/"+left+"/"+right+"/"+suffix); response.Code != 404 {
				t.Fatal("oversized intersection routed")
			}
		})
	}
}
