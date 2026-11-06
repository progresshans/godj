package endpoint_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/endpoint"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/api/parameters"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/web"
)

func TestHeaderInputPreservesAdmissionPathQueryAndBodyOrder(t *testing.T) {
	header, err := parameters.NewHeader("If-Revision", parameters.Required(parameters.CanonicalInt64(1, math.MaxInt64-1)), 19, "Row condition.")
	if err != nil {
		t.Fatal(err)
	}
	var sets, calls atomic.Int64
	body := endpoint.JSONBody("Body", bodyDeclaration(t, &sets), serializers.ModeFull, "")
	input := endpoint.Sequence(endpoint.Sequence(endpoint.PathInt64("id"), endpoint.NoQuery(endpoint.Header(header))), body)
	config := pathConfig(t, "/headers/<int64:id>/", input)
	config.Handle = func(request *web.Request, actor auth.Principal, value endpoint.Pair[endpoint.Pair[int64, int64], bodyDTO]) (output.Prepared[string], error) {
		calls.Add(1)
		if actor.ID() != "actor" {
			t.Error("admitted identity changed")
		}
		name, _ := value.Second.Name.Get()
		return config.Output.Prepare(request.Context(), 200, fmt.Sprintf("%d:%d:%s", value.First.First, value.First.Second, name))
	}
	prepared, err := endpoint.New(bearer(t, view), config)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := prepared.Operation()
	if err != nil || len(operation.Parameters) != 1 || operation.Parameters[0].In != "header" || !operation.Parameters[0].Required || operation.Parameters[0].Name != "If-Revision" || calls.Load() != 0 || sets.Load() != 0 {
		t.Fatal("header runtime/declaration identity", err)
	}
	app := application(t, prepared, nil)
	for _, test := range []struct {
		name, target, body string
		header             http.Header
		authenticated      bool
		status             int
		read               bool
	}{
		{"accepted", "/headers/3/", `{"name":"  stored  "}`, http.Header{"iF-rEVISION": {"9007199254740993"}}, true, 200, true},
		{"missing", "/headers/3/", `not-json`, nil, true, 400, false},
		{"duplicate", "/headers/3/", `not-json`, http.Header{"If-Revision": {"1", "1"}}, true, 400, false},
		{"case alias", "/headers/3/", `not-json`, http.Header{"If-Revision": {"1"}, "if-revision": {"1"}}, true, 400, false},
		{"invalid header", "/headers/3/", `not-json`, http.Header{"If-Revision": {"01"}}, true, 400, false},
		{"query before header", "/headers/3/?bad=1", `not-json`, nil, true, 400, false},
		{"path before body", "/headers/03/", `not-json`, nil, true, 404, false},
		{"admission first", "/headers/3/", `not-json`, nil, false, 401, false},
		{"body after header", "/headers/3/", `not-json`, http.Header{"If-Revision": {"1"}}, true, 400, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := calls.Load()
			reader := &observedBody{Reader: strings.NewReader(test.body)}
			request := httptest.NewRequest("POST", test.target, nil)
			request.Body = reader
			request.ContentLength = -1
			request.Header = test.header.Clone()
			if request.Header == nil {
				request.Header = make(http.Header)
			}
			request.Header.Set("Content-Type", api.JSONContentType)
			if test.authenticated {
				request.Header.Set("Authorization", "Bearer fixture")
			}
			response := httptest.NewRecorder()
			app.ServeHTTP(response, request)
			if response.Code != test.status || (reader.reads > 0) != test.read || reader.closes != 0 {
				t.Fatal("header/body admission order", response.Code, response.Body, reader)
			}
			if test.status == 200 {
				if response.Body.String() != `"3:9007199254740993:stored"` || calls.Load() != before+1 {
					t.Fatal("typed header values changed", response.Body)
				}
			} else if calls.Load() != before {
				t.Fatal("invalid input reached handler")
			}
			if test.name == "missing" && !strings.Contains(response.Body.String(), `"field":"If-Revision","code":"required"`) {
				t.Fatal("missing field diagnostics changed", response.Body)
			}
			if test.name == "query before header" && !strings.Contains(response.Body.String(), `"field":"__all__"`) {
				t.Fatal("query failure was hidden by missing header", response.Body)
			}
		})
	}
}

type headerSessionDeclaration struct{ *startupAdapter }

func (headerSessionDeclaration) DescribeAuthentication() (api.AuthenticationDescription, error) {
	return api.AuthenticationDescription{Kind: api.AuthenticationSession, SessionCookieName: "session", CSRFCookieName: "csrf", CSRFHeader: "X-App-CSRF"}, nil
}

func TestHeaderCompositionKeepsSourceAndProfileOwnership(t *testing.T) {
	header, err := parameters.NewHeader("X-Value", parameters.Required(parameters.CanonicalInt64(0, 9)), 1, "")
	if err != nil {
		t.Fatal(err)
	}
	alias, err := parameters.NewHeader("x-value", parameters.Required(parameters.CanonicalInt64(0, 9)), 1, "")
	if err != nil {
		t.Fatal(err)
	}
	query, err := parameters.New(64, parameters.Field("X-Value", parameters.Required(parameters.CanonicalInt64(0, 9)), func(out *int64, value int64) { *out = value }, ""))
	if err != nil {
		t.Fatal(err)
	}
	for name, input := range map[string]endpoint.Input[struct{}]{
		"zero":              eraseInput(endpoint.Header(parameters.Header[int64]{})),
		"repeated":          eraseInput(endpoint.Sequence(endpoint.Header(header), endpoint.Header(header))),
		"case aliases":      eraseInput(endpoint.Sequence(endpoint.Header(header), endpoint.Header(alias))),
		"wrapped duplicate": eraseInput(endpoint.Sequence(endpoint.NoQuery(endpoint.Header(header)), endpoint.Header(alias))),
		"query conflict":    eraseInput(endpoint.NoQuery(endpoint.Sequence(endpoint.Header(header), endpoint.Query(query)))),
	} {
		t.Run(name, func(t *testing.T) {
			adapter := &startupAdapter{}
			if _, err := endpoint.New(adapter, pathConfig(t, "/headers/", input)); err == nil || adapter.wraps != 0 {
				t.Fatal("invalid header composition reached admission", err)
			}
		})
	}
	csrf, err := parameters.NewHeader("x-app-csrf", parameters.Required(parameters.String(128, false)), 128, "")
	if err != nil {
		t.Fatal(err)
	}
	adapter := headerSessionDeclaration{&startupAdapter{}}
	if _, err := endpoint.New(adapter, pathConfig(t, "/headers/", endpoint.Header(csrf))); err == nil || adapter.wraps != 0 {
		t.Fatal("profile header ownership lost", err)
	}
	config := pathConfig(t, "/headers/", endpoint.Sequence(endpoint.Query(query), endpoint.Header(header)))
	config.Handle = func(request *web.Request, _ auth.Principal, value endpoint.Pair[int64, int64]) (output.Prepared[string], error) {
		return config.Output.Prepare(request.Context(), 200, fmt.Sprintf("%d:%d", value.First, value.Second))
	}
	prepared, err := endpoint.New(bearer(t, view), config)
	if err != nil {
		t.Fatal("different sources collided", err)
	}
	request := httptest.NewRequest("POST", "/headers/?X-Value=5", nil)
	request.Header.Set("Authorization", "Bearer fixture")
	request.Header.Set("X-Value", "6")
	response := httptest.NewRecorder()
	application(t, prepared, nil).ServeHTTP(response, request)
	if response.Code != 200 || response.Body.String() != `"5:6"` {
		t.Fatal("query/header source mixed", response.Code, response.Body)
	}
	empty, err := parameters.New[struct{}](8)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := endpoint.New(&startupAdapter{}, pathConfig(t, "/headers/", endpoint.Sequence(endpoint.NoQuery(endpoint.Header(header)), endpoint.Query(empty)))); err != nil {
		t.Fatal("empty query policy regressed", err)
	}
}

func TestCancellationAfterHeaderSkipsBodyAndHandler(t *testing.T) {
	header, err := parameters.NewHeader("X-Value", parameters.Required(parameters.CanonicalInt64(0, 9)), 1, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	first := endpoint.Resolve(endpoint.Header(header), func(*web.Request, auth.Principal, int64) (int64, error) { cancel(); return 1, nil })
	var sets atomic.Int64
	config := pathConfig(t, "/headers/", endpoint.Sequence(first, endpoint.JSONBody("Body", bodyDeclaration(t, &sets), serializers.ModeFull, "")))
	config.Handle = func(*web.Request, auth.Principal, endpoint.Pair[int64, bodyDTO]) (output.Prepared[string], error) {
		t.Error("canceled header reached handler")
		return output.Prepared[string]{}, nil
	}
	prepared, err := endpoint.New(bearer(t, view), config)
	if err != nil {
		t.Fatal(err)
	}
	var observed error
	app := application(t, prepared, func(_ web.Response, err error) { observed = err })
	reader := &observedBody{Reader: strings.NewReader(`{"name":"unused"}`)}
	request := httptest.NewRequest("POST", "/headers/", nil).WithContext(ctx)
	request.Body = reader
	request.ContentLength = -1
	request.Header.Set("Content-Type", api.JSONContentType)
	request.Header.Set("Authorization", "Bearer fixture")
	request.Header.Set("X-Value", "1")
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != 500 || reader.reads != 0 || reader.closes != 0 || sets.Load() != 0 || !errors.Is(observed, context.Canceled) {
		t.Fatal("header cancellation boundary", response.Code, reader, observed)
	}
}
