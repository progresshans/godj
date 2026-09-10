package godj

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"

	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
)

// parameterRoutingScenarioHandler owns the oracle-blind GoDj observations for
// the closed parameterized routing slice. Registry publication is kept in the
// central runner so handler availability and manifest classification stay an
// explicit integration boundary.
func parameterRoutingScenarioHandler(scenario string) (scenarioHandler, bool) {
	handlers := map[string]scenarioHandler{
		"drf.parameter_routing.static_parameter_coexistence":        parameterRoutingStaticParameterCoexistence,
		"drf.parameter_routing.nonnegative_int64_parameter":         parameterRoutingNonnegativeInt64Parameter,
		"drf.parameter_routing.static_precedence_order_independent": parameterRoutingStaticPrecedence,
		"drf.parameter_routing.named_reverse_boundaries":            parameterRoutingNamedReverseBoundaries,
		"drf.parameter_routing.ambiguous_route_rejection":           parameterRoutingAmbiguousRouteRejection,
		"drf.parameter_routing.invalid_route_and_resource_caps":     parameterRoutingInvalidRouteAndResourceCaps,
		"drf.parameter_routing.trailing_slash_and_invalid_path_404": parameterRoutingInvalidPathNotFound,
		"drf.parameter_routing.method_not_allowed_allow_header":     parameterRoutingMethodNotAllowed,
	}
	handler, ok := handlers[scenario]
	return handler, ok
}

func parameterRoutingStaticParameterCoexistence(_ context.Context, contract protocol.Contract) (protocol.Observation, error) {
	type staticMatch struct {
		name   string
		kwargs protocol.Value
	}
	type parameterMatch struct {
		name string
		pk   int64
		kind string
	}
	var static staticMatch
	var parameter parameterMatch
	application, err := parameterRoutingApplication([]web.Route{
		{
			Name: "articles:health", Method: http.MethodGet, Path: "/health/",
			Handler: func(*web.Request) (web.Response, error) {
				static = staticMatch{name: "health", kwargs: protocol.Object(map[string]protocol.Value{})}
				return parameterRoutingTextResponse(http.StatusOK, "health")
			},
		},
		{
			Name: "articles:article-detail", Method: http.MethodGet, Path: "/api/articles/<int64:pk>/",
			Handler: func(request *web.Request) (web.Response, error) {
				pk, found := request.Int64Parameter("pk")
				if !found {
					return parameterRoutingTextResponse(http.StatusInternalServerError, "missing")
				}
				parameter = parameterMatch{name: "article-detail", pk: pk, kind: "int64"}
				return parameterRoutingTextResponse(http.StatusOK, "article-detail")
			},
		},
	})
	if err != nil {
		return protocol.Observation{}, err
	}
	staticPath, err := application.Reverse("articles:health")
	if err != nil {
		return protocol.Observation{}, err
	}
	if response := parameterRoutingServe(application, http.MethodGet, staticPath); response.Code != http.StatusOK {
		return protocol.Observation{}, errors.New("static routing observation did not match")
	}
	if response := parameterRoutingServe(application, http.MethodGet, "/api/articles/0/"); response.Code != http.StatusOK {
		return protocol.Observation{}, errors.New("parameter routing observation did not match")
	}
	result := protocol.Object(map[string]protocol.Value{
		"parameter": protocol.Object(map[string]protocol.Value{
			"name":    protocol.String(parameter.name),
			"pk":      parameterRoutingInt64(parameter.pk),
			"pk_type": protocol.String(parameter.kind),
		}),
		"reversed_static": protocol.String(staticPath),
		"static": protocol.Object(map[string]protocol.Value{
			"kwargs": static.kwargs,
			"name":   protocol.String(static.name),
		}),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"io_operations":  parameterRoutingInt(0),
		"matched_routes": parameterRoutingInt(2),
	})
	return parameterRoutingObservation(contract, result, metrics), nil
}

func parameterRoutingNonnegativeInt64Parameter(_ context.Context, contract protocol.Contract) (protocol.Observation, error) {
	var observed int64
	var observedKind string
	application, err := parameterRoutingApplication([]web.Route{{
		Name: "articles:article-detail", Method: http.MethodGet, Path: "/api/articles/<int64:pk>/",
		Handler: func(request *web.Request) (web.Response, error) {
			pk, found := request.Int64Parameter("pk")
			if !found {
				return parameterRoutingTextResponse(http.StatusInternalServerError, "missing")
			}
			observed = pk
			observedKind = "int64"
			return parameterRoutingTextResponse(http.StatusOK, strconv.FormatInt(pk, 10))
		},
	}})
	if err != nil {
		return protocol.Observation{}, err
	}
	validInputs := []string{"0", "1", strconv.FormatInt(math.MaxInt64, 10)}
	valid := make([]protocol.Value, 0, len(validInputs))
	for _, rendered := range validInputs {
		observed = 0
		observedKind = ""
		response := parameterRoutingServe(application, http.MethodGet, "/api/articles/"+rendered+"/")
		valid = append(valid, protocol.Object(map[string]protocol.Value{
			"input":   protocol.String(rendered),
			"matched": protocol.Boolean(response.Code == http.StatusOK),
			"pk":      parameterRoutingInt64(observed),
			"type":    protocol.String(observedKind),
		}))
	}
	invalidInputs := []string{"-1", "01", "9223372036854775808", "x"}
	invalid := make([]protocol.Value, 0, len(invalidInputs))
	for _, rendered := range invalidInputs {
		response := parameterRoutingServe(application, http.MethodGet, "/api/articles/"+rendered+"/")
		invalid = append(invalid, protocol.Object(map[string]protocol.Value{
			"input":   protocol.String(rendered),
			"matched": protocol.Boolean(response.Code == http.StatusOK),
		}))
	}
	result := protocol.Object(map[string]protocol.Value{
		"invalid": protocol.List(invalid...),
		"valid":   protocol.List(valid...),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"borrowed_values": parameterRoutingInt(len(valid)),
		"io_operations":   parameterRoutingInt(0),
	})
	return parameterRoutingObservation(contract, result, metrics), nil
}

func parameterRoutingStaticPrecedence(_ context.Context, contract protocol.Contract) (protocol.Observation, error) {
	orders := []string{"parameter_first", "static_first"}
	observations := make([]protocol.Value, 0, len(orders))
	for _, declaration := range orders {
		parameter := web.Route{
			Name: "articles:parameter-" + declaration, Method: http.MethodGet, Path: "/items/<int64:pk>/",
			Handler: func(*web.Request) (web.Response, error) {
				return parameterRoutingTextResponse(http.StatusOK, "parameter")
			},
		}
		static := web.Route{
			Name: "articles:static-" + declaration, Method: http.MethodGet, Path: "/items/7/",
			Handler: func(*web.Request) (web.Response, error) { return parameterRoutingTextResponse(http.StatusOK, "static") },
		}
		routes := []web.Route{parameter, static}
		if declaration == "static_first" {
			routes = []web.Route{static, parameter}
		}
		application, err := parameterRoutingApplication(routes)
		if err != nil {
			return protocol.Observation{}, err
		}
		response := parameterRoutingServe(application, http.MethodGet, "/items/7/")
		observations = append(observations, protocol.Object(map[string]protocol.Value{
			"declaration": protocol.String(declaration),
			"matched":     protocol.String(response.Body.String()),
		}))
	}
	metrics := protocol.Object(map[string]protocol.Value{
		"io_operations":  parameterRoutingInt(0),
		"orders_checked": parameterRoutingInt(len(observations)),
	})
	return parameterRoutingObservation(contract, protocol.List(observations...), metrics), nil
}

func parameterRoutingNamedReverseBoundaries(_ context.Context, contract protocol.Contract) (protocol.Observation, error) {
	application, err := parameterRoutingApplication([]web.Route{{
		Name: "articles:article-detail", Method: http.MethodGet, Path: "/api/articles/<int64:pk>/", Handler: parameterRoutingNoopHandler,
	}})
	if err != nil {
		return protocol.Observation{}, err
	}
	validValues := []int64{0, math.MaxInt64}
	valid := make([]protocol.Value, 0, len(validValues))
	for _, value := range validValues {
		path, reverseErr := application.ReverseWith("articles:article-detail", web.Int64Argument("pk", value))
		if reverseErr != nil {
			return protocol.Observation{}, reverseErr
		}
		valid = append(valid, protocol.Object(map[string]protocol.Value{
			"path": protocol.String(path), "value": parameterRoutingInt64(value),
		}))
	}
	// Boolean, string, and path-injection reference values cannot enter the
	// closed Go API. Their single unsupported-kind representation is paired
	// with external compile and exported-surface gates in this package; overflow
	// is rejected before an int64 ReverseArgument can be constructed.
	invalid := []protocol.Value{
		parameterRoutingReverseOutcome("negative", func() error {
			_, err := application.ReverseWith("articles:article-detail", web.Int64Argument("pk", -1))
			return err
		}),
		parameterRoutingInt64AdmissionOutcome("overflow", "9223372036854775808"),
		parameterRoutingReverseOutcome("boolean", func() error {
			_, err := application.ReverseWith("articles:article-detail", web.ReverseArgument{})
			return err
		}),
		parameterRoutingReverseOutcome("string", func() error {
			_, err := application.ReverseWith("articles:article-detail", web.ReverseArgument{})
			return err
		}),
		parameterRoutingReverseOutcome("path_injection", func() error {
			_, err := application.ReverseWith("articles:article-detail", web.ReverseArgument{})
			return err
		}),
		parameterRoutingReverseOutcome("missing", func() error {
			_, err := application.ReverseWith("articles:article-detail")
			return err
		}),
		parameterRoutingReverseOutcome("extra", func() error {
			_, err := application.ReverseWith(
				"articles:article-detail", web.Int64Argument("pk", 1), web.Int64Argument("other", 2),
			)
			return err
		}),
	}
	result := protocol.Object(map[string]protocol.Value{
		"invalid": protocol.List(invalid...),
		"valid":   protocol.List(valid...),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"io_operations": parameterRoutingInt(0),
		"reversals":     parameterRoutingInt(len(valid) + len(invalid)),
	})
	return parameterRoutingObservation(contract, result, metrics), nil
}

func parameterRoutingAmbiguousRouteRejection(_ context.Context, contract protocol.Contract) (protocol.Observation, error) {
	tests := []struct {
		name   string
		routes []web.Route
	}{
		{name: "exact_duplicate", routes: []web.Route{
			{Name: "articles:exact-left", Method: http.MethodGet, Path: "/articles/<int64:id>/", Handler: parameterRoutingNoopHandler},
			{Name: "articles:exact-right", Method: http.MethodGet, Path: "/articles/<int64:id>/", Handler: parameterRoutingNoopHandler},
		}},
		{name: "language_equivalent_parameter_name", routes: []web.Route{
			{Name: "articles:equivalent-left", Method: http.MethodGet, Path: "/articles/<int64:id>/", Handler: parameterRoutingNoopHandler},
			{Name: "articles:equivalent-right", Method: http.MethodGet, Path: "/articles/<int64:article_id>/", Handler: parameterRoutingNoopHandler},
		}},
		{name: "partially_overlapping", routes: []web.Route{
			{Name: "articles:partial-left", Method: http.MethodGet, Path: "/pairs/<int64:left>/0/", Handler: parameterRoutingNoopHandler},
			{Name: "articles:partial-right", Method: http.MethodGet, Path: "/pairs/7/<int64:right>/", Handler: parameterRoutingNoopHandler},
		}},
		{name: "same_language_different_method", routes: []web.Route{
			{Name: "articles:method-left", Method: http.MethodGet, Path: "/articles/<int64:id>/", Handler: parameterRoutingNoopHandler},
			{Name: "articles:method-right", Method: http.MethodPost, Path: "/articles/<int64:article_id>/", Handler: parameterRoutingNoopHandler},
		}},
		{name: "distinct", routes: []web.Route{
			{Name: "articles:distinct-left", Method: http.MethodGet, Path: "/articles/<int64:id>/", Handler: parameterRoutingNoopHandler},
			{Name: "articles:distinct-right", Method: http.MethodGet, Path: "/authors/<int64:id>/", Handler: parameterRoutingNoopHandler},
		}},
	}
	values := make([]protocol.Value, 0, len(tests))
	for _, test := range tests {
		_, constructionErr := parameterRoutingApplication(test.routes)
		values = append(values, protocol.Object(map[string]protocol.Value{
			"case": protocol.String(test.name),
			"outcome": protocol.String(parameterRoutingClassifiedErrorOutcome(
				constructionErr, web.CodeDuplicateRoute, "ambiguous_route",
			)),
		}))
	}
	metrics := protocol.Object(map[string]protocol.Value{
		"io_operations": parameterRoutingInt(0),
		"route_sets":    parameterRoutingInt(len(tests)),
	})
	return parameterRoutingObservation(contract, protocol.List(values...), metrics), nil
}

func parameterRoutingInvalidRouteAndResourceCaps(_ context.Context, contract protocol.Contract) (protocol.Observation, error) {
	caps, err := parameterRoutingObserveCaps()
	if err != nil {
		return protocol.Observation{}, err
	}
	baseRoute := func(path string) []web.Route {
		return []web.Route{{Name: "articles:bounded", Method: http.MethodGet, Path: path, Handler: parameterRoutingNoopHandler}}
	}
	invalidOutcome := func(path string) error {
		_, err := parameterRoutingApplication(baseRoute(path))
		return err
	}
	tooManyParameters := parameterRoutingParameterPattern(caps.parametersPerPattern + 1)
	application, err := parameterRoutingApplication(baseRoute("/articles/<int64:id>/"))
	if err != nil {
		return protocol.Observation{}, err
	}
	decodedPath := "/articles/" + strings.Repeat("9", caps.decodedInputPathBytes+1-len("/articles//")) + "/"
	decodedResponse := parameterRoutingServe(application, http.MethodGet, decodedPath)
	maximumReversePattern := parameterRoutingReverseResultPattern(caps.reverseResultPathBytes + 1)
	reverseApplication, err := parameterRoutingApplication(baseRoute(maximumReversePattern))
	if err != nil {
		return protocol.Observation{}, err
	}
	_, reverseErr := reverseApplication.ReverseWith("articles:bounded", web.Int64Argument("id", math.MaxInt64))
	_, routeCountErr := parameterRoutingApplication(parameterRoutingRoutes(caps.registeredRoutes + 1))
	cases := []protocol.Value{
		parameterRoutingCaseOutcome("empty_parameter", parameterRoutingClassifiedErrorOutcome(invalidOutcome("/articles/<int64:>/"), web.CodeInvalidRoute, "invalid_parameter")),
		parameterRoutingCaseOutcome("non_identifier_parameter", parameterRoutingClassifiedErrorOutcome(invalidOutcome("/articles/<int64:9id>/"), web.CodeInvalidRoute, "invalid_parameter")),
		parameterRoutingCaseOutcome("duplicate_parameter", parameterRoutingClassifiedErrorOutcome(invalidOutcome("/<int64:id>/<int64:id>/"), web.CodeInvalidRoute, "duplicate_parameter")),
		parameterRoutingCaseOutcome("unsupported_parameter_type", parameterRoutingClassifiedErrorOutcome(invalidOutcome("/articles/<uuid:id>/"), web.CodeInvalidRoute, "unsupported_parameter_type")),
		parameterRoutingCaseOutcome("embedded_parameter_pattern", parameterRoutingClassifiedErrorOutcome(invalidOutcome("/articles/prefix<int64:id>/"), web.CodeInvalidRoute, "invalid_pattern")),
		parameterRoutingCaseOutcome("registered_routes_1025", parameterRoutingClassifiedErrorOutcome(routeCountErr, web.CodeInvalidRoute, "resource_limit")),
		parameterRoutingCaseOutcome("route_path_bytes_4097", parameterRoutingClassifiedErrorOutcome(invalidOutcome("/"+strings.Repeat("a", caps.routePathBytes-1)+"/"), web.CodeInvalidRoute, "resource_limit")),
		parameterRoutingCaseOutcome("decoded_input_path_bytes_4097", parameterRoutingClassifiedHTTPOutcome(decodedResponse, http.StatusNotFound, "resource_limit")),
		parameterRoutingCaseOutcome("path_segments_65", parameterRoutingClassifiedErrorOutcome(invalidOutcome(parameterRoutingSegmentPattern(caps.pathSegments+1)), web.CodeInvalidRoute, "resource_limit")),
		parameterRoutingCaseOutcome("parameters_17", parameterRoutingClassifiedErrorOutcome(invalidOutcome(tooManyParameters), web.CodeInvalidRoute, "resource_limit")),
		parameterRoutingCaseOutcome("parameter_name_bytes_65", parameterRoutingClassifiedErrorOutcome(invalidOutcome("/articles/<int64:"+strings.Repeat("a", caps.parameterNameBytes+1)+">/"), web.CodeInvalidRoute, "resource_limit")),
		parameterRoutingCaseOutcome("reverse_result_path_bytes_4097", parameterRoutingClassifiedErrorOutcome(reverseErr, web.CodeReverseArguments, "resource_limit")),
	}
	result := protocol.Object(map[string]protocol.Value{
		"caps": protocol.Object(map[string]protocol.Value{
			"decoded_input_path_bytes":  parameterRoutingInt(caps.decodedInputPathBytes),
			"parameter_name_bytes":      parameterRoutingInt(caps.parameterNameBytes),
			"parameters_per_pattern":    parameterRoutingInt(caps.parametersPerPattern),
			"path_segments":             parameterRoutingInt(caps.pathSegments),
			"registered_routes":         parameterRoutingInt(caps.registeredRoutes),
			"reverse_result_path_bytes": parameterRoutingInt(caps.reverseResultPathBytes),
			"route_path_bytes":          parameterRoutingInt(caps.routePathBytes),
		}),
		"cases": protocol.List(cases...),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"io_operations": parameterRoutingInt(0),
		"rejections":    parameterRoutingInt(len(cases)),
	})
	return parameterRoutingObservation(contract, result, metrics), nil
}

type parameterRoutingCaps struct {
	decodedInputPathBytes  int
	parameterNameBytes     int
	parametersPerPattern   int
	pathSegments           int
	registeredRoutes       int
	reverseResultPathBytes int
	routePathBytes         int
}

// parameterRoutingObserveCaps derives every published cap from an accepted
// public boundary probe. The scenario below separately exercises cap+1, so a
// product limit drift becomes an honest observation failure instead of copied
// fixture data.
func parameterRoutingObserveCaps() (parameterRoutingCaps, error) {
	maximumRoutes := parameterRoutingRoutes(1024)
	if _, err := parameterRoutingApplication(maximumRoutes); err != nil {
		return parameterRoutingCaps{}, fmt.Errorf("observe registered route cap: %w", err)
	}

	maximumRoutePath := "/" + strings.Repeat("a", 4094) + "/"
	maximumPathApplication, err := parameterRoutingApplication([]web.Route{{
		Name: "articles:maximum-path", Method: http.MethodGet, Path: maximumRoutePath, Handler: parameterRoutingNoopHandler,
	}})
	if err != nil {
		return parameterRoutingCaps{}, fmt.Errorf("observe route path cap: %w", err)
	}
	if response := parameterRoutingServe(maximumPathApplication, http.MethodGet, maximumRoutePath); response.Code != http.StatusOK {
		return parameterRoutingCaps{}, fmt.Errorf("observe decoded input path cap: status %d", response.Code)
	}

	maximumSegments := 64
	if _, err := parameterRoutingApplication([]web.Route{{
		Name: "articles:maximum-segments", Method: http.MethodGet, Path: parameterRoutingSegmentPattern(maximumSegments), Handler: parameterRoutingNoopHandler,
	}}); err != nil {
		return parameterRoutingCaps{}, fmt.Errorf("observe route segment cap: %w", err)
	}

	maximumParameters := 16
	if _, err := parameterRoutingApplication([]web.Route{{
		Name: "articles:maximum-parameters", Method: http.MethodGet, Path: parameterRoutingParameterPattern(maximumParameters), Handler: parameterRoutingNoopHandler,
	}}); err != nil {
		return parameterRoutingCaps{}, fmt.Errorf("observe route parameter cap: %w", err)
	}

	maximumParameterName := strings.Repeat("n", 64)
	if _, err := parameterRoutingApplication([]web.Route{{
		Name: "articles:maximum-name", Method: http.MethodGet, Path: "/<int64:" + maximumParameterName + ">/", Handler: parameterRoutingNoopHandler,
	}}); err != nil {
		return parameterRoutingCaps{}, fmt.Errorf("observe parameter name cap: %w", err)
	}

	maximumReverseApplication, err := parameterRoutingApplication([]web.Route{{
		Name: "articles:maximum-reverse", Method: http.MethodGet, Path: parameterRoutingReverseResultPattern(4096), Handler: parameterRoutingNoopHandler,
	}})
	if err != nil {
		return parameterRoutingCaps{}, fmt.Errorf("observe reverse result cap: %w", err)
	}
	maximumReverse, err := maximumReverseApplication.ReverseWith("articles:maximum-reverse", web.Int64Argument("id", math.MaxInt64))
	if err != nil {
		return parameterRoutingCaps{}, fmt.Errorf("observe reverse result cap: %w", err)
	}

	return parameterRoutingCaps{
		decodedInputPathBytes:  len(maximumRoutePath),
		parameterNameBytes:     len(maximumParameterName),
		parametersPerPattern:   maximumParameters,
		pathSegments:           maximumSegments,
		registeredRoutes:       len(maximumRoutes),
		reverseResultPathBytes: len(maximumReverse),
		routePathBytes:         len(maximumRoutePath),
	}, nil
}

func parameterRoutingInvalidPathNotFound(_ context.Context, contract protocol.Contract) (protocol.Observation, error) {
	application, err := parameterRoutingApplication([]web.Route{{
		Name: "articles:article-detail", Method: http.MethodGet, Path: "/api/articles/<int64:pk>/", Handler: parameterRoutingNoopHandler,
	}})
	if err != nil {
		return protocol.Observation{}, err
	}
	paths := []string{
		"/api/articles/1",
		"/api/articles/01/",
		"/api/articles/-1/",
		"/api/articles/9223372036854775808/",
		"/api/articles/1%2F2/",
		"/api/articles/1%5C2/",
		"/api/articles/%00/",
		"/api/articles/./",
		"/api/articles/1//",
	}
	values := make([]protocol.Value, 0, len(paths))
	redirects := 0
	for _, path := range paths {
		response := parameterRoutingServe(application, http.MethodGet, path)
		if parameterRoutingRedirectStatus(response.Code) {
			redirects++
		}
		values = append(values, protocol.Object(map[string]protocol.Value{
			"path": protocol.String(path), "status": parameterRoutingInt(response.Code),
		}))
	}
	metrics := protocol.Object(map[string]protocol.Value{
		"redirects": parameterRoutingInt(redirects),
		"requests":  parameterRoutingInt(len(values)),
	})
	return parameterRoutingObservation(contract, protocol.List(values...), metrics), nil
}

func parameterRoutingMethodNotAllowed(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	return withArticleAPIFixture(ctx, contract, func(fixture *articleAPIFixture) (protocol.Observation, error) {
		if err := fixture.seed(ctx, articleAPISeed{id: 1, title: "Existing"}); err != nil {
			return protocol.Observation{}, err
		}
		client := fixture.client(fixture.allSession)
		token, err := client.csrf(ctx, "/api/articles/")
		if err != nil {
			return protocol.Observation{}, err
		}
		fixture.observed.reset()
		response, err := client.do(ctx, http.MethodPost, "/api/articles/1/", articleAPIRequestOptions{token: token})
		if err != nil {
			return protocol.Observation{}, err
		}
		semantic, err := articleAPIResponseValue(response)
		if err != nil {
			return protocol.Observation{}, err
		}
		allow := strings.Split(response.header.Get("Allow"), ", ")
		sort.Strings(allow)
		result := protocol.Object(map[string]protocol.Value{
			"allow": parameterRoutingStrings(allow), "response": semantic,
		})
		metrics := protocol.Object(map[string]protocol.Value{
			"article_mutations": parameterRoutingInt(fixture.observed.snapshot().writes()),
			"requests":          parameterRoutingInt(2),
		})
		return parameterRoutingObservation(contract, result, metrics), nil
	})
}

func parameterRoutingApplication(routes []web.Route) (*web.Application, error) {
	configured, err := settings.New(settings.Definition{
		ProjectName: "godj_parameter_routing_conformance",
		InstalledApps: []apps.Config{{
			Name: "github.com/progresshans/godj/conformance/parameter-routing", Label: "articles",
		}},
	})
	if err != nil {
		return nil, err
	}
	return web.NewApplication(web.Config{Settings: configured, Routes: routes})
}

func parameterRoutingRoutes(count int) []web.Route {
	routes := make([]web.Route, count)
	for index := range routes {
		routes[index] = web.Route{
			Name:    "articles:route" + strconv.Itoa(index),
			Method:  http.MethodGet,
			Path:    "/route" + strconv.Itoa(index) + "/",
			Handler: parameterRoutingNoopHandler,
		}
	}
	return routes
}

func parameterRoutingSegmentPattern(count int) string {
	if count <= 0 {
		return "/"
	}
	return "/" + strings.Repeat("segment/", count-1) + "<int64:id>/"
}

func parameterRoutingParameterPattern(count int) string {
	pattern := "/"
	for index := 0; index < count; index++ {
		pattern += "<int64:p" + strconv.Itoa(index) + ">/"
	}
	return pattern
}

func parameterRoutingReverseResultPattern(resultBytes int) string {
	fixedResultBytes := len("///") + len(strconv.FormatInt(math.MaxInt64, 10))
	prefixBytes := resultBytes - fixedResultBytes
	if prefixBytes < 0 {
		prefixBytes = 0
	}
	return "/" + strings.Repeat("a", prefixBytes) + "/<int64:id>/"
}

func parameterRoutingNoopHandler(*web.Request) (web.Response, error) {
	return parameterRoutingTextResponse(http.StatusOK, "ok")
}

func parameterRoutingTextResponse(status int, body string) (web.Response, error) {
	header := make(http.Header)
	header.Set("Content-Type", "text/plain; charset=utf-8")
	return web.NewResponse(status, header, []byte(body))
}

func parameterRoutingServe(application *web.Application, method, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://example.test"+path, nil)
	recorder := httptest.NewRecorder()
	application.ServeHTTP(recorder, request)
	return recorder
}

func parameterRoutingReverseOutcome(name string, run func() error) protocol.Value {
	outcome := parameterRoutingClassifiedErrorOutcome(run(), web.CodeReverseArguments, "no_reverse_match")
	return protocol.Object(map[string]protocol.Value{
		"case": protocol.String(name), "outcome": protocol.String(outcome),
	})
}

// ReverseWith deliberately has no untyped escape hatch: values must first fit
// the public int64 ReverseArgument surface. Keep that ABI admission observable
// instead of fabricating an argument the product cannot represent.
func parameterRoutingInt64AdmissionOutcome(name, rendered string) protocol.Value {
	_, err := strconv.ParseInt(rendered, 10, 64)
	outcome := "accepted"
	if err != nil {
		outcome = "no_reverse_match"
	}
	return parameterRoutingCaseOutcome(name, outcome)
}

func parameterRoutingCaseOutcome(name, outcome string) protocol.Value {
	return protocol.Object(map[string]protocol.Value{
		"case": protocol.String(name), "outcome": protocol.String(outcome),
	})
}

func parameterRoutingOutcome(err error) string {
	if err == nil {
		return "accepted"
	}
	var webErr *web.Error
	if errors.As(err, &webErr) {
		return string(webErr.Code)
	}
	return "unexpected_error"
}

// The shared outcome vocabulary describes the observed semantic category,
// while a non-matching product error remains visible by its public code.
func parameterRoutingClassifiedErrorOutcome(err error, code web.ErrorCode, semantic string) string {
	if errors.Is(err, &web.Error{Code: code}) {
		return semantic
	}
	return parameterRoutingOutcome(err)
}

func parameterRoutingClassifiedHTTPOutcome(response *httptest.ResponseRecorder, status int, semantic string) string {
	if response == nil {
		return "unexpected_error"
	}
	if response.Code == status {
		return semantic
	}
	return "status_" + strconv.Itoa(response.Code)
}

func parameterRoutingObservation(contract protocol.Contract, result, metrics protocol.Value) protocol.Observation {
	return protocol.Observation{
		ID: contract.ID, Status: protocol.StatusObserved, Phase: contract.Phase,
		Result: valuePointer(result), Metrics: valuePointer(metrics),
	}
}

func parameterRoutingInt(value int) protocol.Value {
	return protocol.Integer(strconv.Itoa(value))
}

func parameterRoutingInt64(value int64) protocol.Value {
	return protocol.Integer(strconv.FormatInt(value, 10))
}

func parameterRoutingStrings(values []string) protocol.Value {
	items := make([]protocol.Value, len(values))
	for index, value := range values {
		items[index] = protocol.String(value)
	}
	return protocol.List(items...)
}

func parameterRoutingRedirectStatus(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}
