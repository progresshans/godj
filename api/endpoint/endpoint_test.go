package endpoint_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/bearerauth"
	"github.com/progresshans/godj/api/endpoint"
	bodyinput "github.com/progresshans/godj/api/input"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/api/parameters"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

const view auth.Permission = "tests.view"
const change auth.Permission = "tests.change"

type verifierFunc func(context.Context, bearerauth.Token) (auth.Principal, error)

func (f verifierFunc) Verify(ctx context.Context, token bearerauth.Token) (auth.Principal, error) {
	return f(ctx, token)
}

func bearer(t *testing.T, permissions ...auth.Permission) *bearerauth.Runtime {
	t.Helper()
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "actor", Active: true, Permissions: permissions})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := bearerauth.New(bearerauth.Config{
		Verifier:   verifierFunc(func(context.Context, bearerauth.Token) (auth.Principal, error) { return principal, nil }),
		Authorizer: auth.PrincipalAuthorizer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func encoder[T any](t *testing.T, shape output.Shape[T]) output.Output[T] {
	t.Helper()
	value, err := output.New(shape, serializers.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func application(t *testing.T, prepared endpoint.Endpoint, observe func(web.Response, error)) *web.Application {
	t.Helper()
	operation, err := prepared.Operation()
	if err != nil {
		t.Fatal(err)
	}
	if observe != nil {
		handler := operation.Route.Handler
		operation.Route.Handler = func(request *web.Request) (web.Response, error) {
			response, err := handler(request)
			observe(response, err)
			return response, err
		}
	}
	configured, err := settings.New(settings.Definition{ProjectName: "endpoint_test", InstalledApps: []apps.Config{{Name: "example.test/endpoint", Label: "test"}}})
	if err != nil {
		t.Fatal(err)
	}
	app, err := web.NewApplication(web.Config{Settings: configured, Routes: []web.Route{operation.Route}})
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func send(app *web.Application, method, target, body string, authenticated bool) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if authenticated {
		request.Header.Set("Authorization", "Bearer fixture")
	}
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	return response
}

func baseConfig(t *testing.T) endpoint.Config[struct{}, string] {
	t.Helper()
	out := encoder(t, output.String())
	return endpoint.Config[struct{}, string]{
		Route:     web.Route{Name: "test:typed", Method: http.MethodGet, Path: "/api/typed/"},
		Admission: endpoint.All(view), Input: endpoint.NoInput(), Output: out,
		Success: []endpoint.Status{{Code: 200, Description: "Value."}},
		Handle: func(request *web.Request, _ auth.Principal, _ struct{}) (output.Prepared[string], error) {
			return out.Prepare(request.Context(), 200, "value")
		},
	}
}

// This adapter tests startup capability and permission ownership only. Runtime
// admission is exercised below with the actual Bearer and Session adapters.
type startupAdapter struct {
	wraps       int
	permissions []auth.Permission
	mode        string
}

func (adapter *startupAdapter) DescribeAuthentication() (api.AuthenticationDescription, error) {
	return api.AuthenticationDescription{Kind: api.AuthenticationBearer}, nil
}
func (adapter *startupAdapter) Require(first auth.Permission, handler api.AuthenticatedHandler, rest ...auth.Permission) (web.Handler, error) {
	adapter.wraps++
	adapter.permissions = append([]auth.Permission{first}, rest...)
	if len(rest) > 0 {
		rest[0] = "mutated.permission"
	}
	if adapter.mode == "error" {
		return nil, errors.New("private adapter failure")
	}
	if adapter.mode == "nil" {
		return nil, nil
	}
	return func(request *web.Request) (web.Response, error) { return handler(request, auth.Principal{}) }, nil
}

func TestEndpointRejectsInvalidConstructionBeforeWrapping(t *testing.T) {
	for name, mutate := range map[string]func(*endpoint.Config[struct{}, string]){
		"nil handler": func(c *endpoint.Config[struct{}, string]) { c.Handle = nil },
		"bound route": func(c *endpoint.Config[struct{}, string]) {
			c.Route.Handler = func(*web.Request) (web.Response, error) { return web.Response{}, nil }
		},
		"bad route":                      func(c *endpoint.Config[struct{}, string]) { c.Route.Path = "/api/<uuid:id>/" },
		"zero input":                     func(c *endpoint.Config[struct{}, string]) { c.Input = endpoint.Input[struct{}]{} },
		"zero output":                    func(c *endpoint.Config[struct{}, string]) { c.Output = output.Output[string]{} },
		"zero admission":                 func(c *endpoint.Config[struct{}, string]) { c.Admission = endpoint.Admission{} },
		"empty permission":               func(c *endpoint.Config[struct{}, string]) { c.Admission = endpoint.All("") },
		"duplicate permission":           func(c *endpoint.Config[struct{}, string]) { c.Admission = endpoint.Any(view, view) },
		"missing alternative capability": func(c *endpoint.Config[struct{}, string]) { c.Admission = endpoint.Any(view, change) },
		"missing principal capability":   func(c *endpoint.Config[struct{}, string]) { c.Admission = endpoint.Authenticated() },
		"bearer cannot be csrf only":     func(c *endpoint.Config[struct{}, string]) { c.Admission = endpoint.CSRFOnly(false) },
		"no success":                     func(c *endpoint.Config[struct{}, string]) { c.Success = nil },
		"duplicate success":              func(c *endpoint.Config[struct{}, string]) { c.Success = append(c.Success, c.Success[0]) },
		"no content":                     func(c *endpoint.Config[struct{}, string]) { c.Success[0].Code = 204 },
		"reset content":                  func(c *endpoint.Config[struct{}, string]) { c.Success[0].Code = 205 },
		"redirect":                       func(c *endpoint.Config[struct{}, string]) { c.Success[0].Code = 302 },
		"empty description":              func(c *endpoint.Config[struct{}, string]) { c.Success[0].Description = "" },
		"server error": func(c *endpoint.Config[struct{}, string]) {
			c.Errors = []endpoint.Status{{Code: 500, Description: "Internal."}}
		},
		"duplicate error": func(c *endpoint.Config[struct{}, string]) {
			c.Errors = []endpoint.Status{{Code: 404, Description: "Missing."}, {Code: 404, Description: "Missing."}}
		},
		"too many responses": func(c *endpoint.Config[struct{}, string]) { c.Errors = make([]endpoint.Status, 33) },
		"zero query":         func(c *endpoint.Config[struct{}, string]) { c.Input = endpoint.Query(parameters.Query[struct{}]{}) },
		"zero body": func(c *endpoint.Config[struct{}, string]) {
			c.Input = endpoint.JSONBody("Body", bodyinput.Body[struct{}]{}, serializers.ModeFull, "")
		},
		"no query with declared query": func(c *endpoint.Config[struct{}, string]) {
			query, err := parameters.New(128, parameters.Field("q", parameters.Required(parameters.CanonicalInt64(-100, 100)), func(*struct{}, int64) {}, ""))
			if err != nil {
				t.Fatal(err)
			}
			c.Input = endpoint.NoQuery(endpoint.Query(query))
		},
	} {
		t.Run(name, func(t *testing.T) {
			config, adapter := baseConfig(t), &startupAdapter{}
			mutate(&config)
			value, err := endpoint.New(adapter, config)
			if err == nil || adapter.wraps != 0 {
				t.Fatal("invalid declaration reached wrapper", err, adapter.wraps)
			}
			if _, err := value.Operation(); err == nil {
				t.Fatal("published invalid endpoint")
			}
		})
	}
	var typedNil *startupAdapter
	for _, adapter := range []api.Authentication{nil, typedNil} {
		if _, err := endpoint.New(adapter, baseConfig(t)); err == nil {
			t.Fatal("accepted absent authentication")
		}
	}
	for _, mode := range []string{"error", "nil"} {
		adapter := &startupAdapter{mode: mode}
		if value, err := endpoint.New(adapter, baseConfig(t)); err == nil {
			t.Fatal("accepted failed wrapper", value)
		}
		if adapter.wraps != 1 {
			t.Fatal("wrapper was not attempted exactly once")
		}
	}
	if operations, schemas, err := endpoint.Collect([]endpoint.Endpoint{{}}); err == nil || operations != nil || schemas != nil {
		t.Fatal("collected zero endpoint")
	}
}

type bodyDTO struct{ Name bodyinput.Presence[string] }

func bodyDeclaration(t *testing.T, sets *atomic.Int64) bodyinput.Body[bodyDTO] {
	t.Helper()
	field, err := serializers.StringField("name", serializers.WithMaxLength(16))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := serializers.NewSpec([]serializers.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	body, err := bodyinput.New(spec, api.ParserConfig{MaxBodyBytes: 64}, bodyinput.Field("name", bodyinput.String(), func(value *bodyDTO, name bodyinput.Presence[string]) {
		sets.Add(1)
		value.Name = name
	}))
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestEndpointSnapshotsAndCollectsTheActualDeclarations(t *testing.T) {
	var reads, sets, calls atomic.Int64
	out := encoder(t, output.Named("Result", output.Object(output.Field("name", output.String(), func(value string) string { reads.Add(1); return value }))))
	body := bodyDeclaration(t, &sets)
	input := endpoint.NoQuery(endpoint.JSONBody("Request", body, serializers.ModeFull, "Typed body."))
	permissions := []auth.Permission{change}
	admission := endpoint.All(view, permissions...)
	permissions[0] = "changed.permission"
	config := endpoint.Config[bodyDTO, string]{
		Route: web.Route{Name: "test:create", Method: "POST", Path: "/api/create/"}, Admission: admission, Input: input, Output: out,
		Success: []endpoint.Status{{Code: 201, Description: "Created."}}, Errors: []endpoint.Status{{Code: 400, Description: "Bad input."}, {Code: 404, Description: "Missing."}},
		Handle: func(request *web.Request, _ auth.Principal, value bodyDTO) (output.Prepared[string], error) {
			calls.Add(1)
			name, _ := value.Name.Get()
			return out.Prepare(request.Context(), 201, name)
		},
	}
	adapter := &startupAdapter{}
	first, err := endpoint.New(adapter, config)
	if err != nil {
		t.Fatal(err)
	}
	config.Route.Name, config.Route.Path = "test:second", "/api/second/"
	second, err := endpoint.New(adapter, config)
	if err != nil {
		t.Fatal(err)
	}
	config.Success[0].Code, config.Errors[0].Description = 202, "changed"
	if adapter.wraps != 2 || !slices.Equal(adapter.permissions, []auth.Permission{view, change}) || reads.Load()+sets.Load()+calls.Load() != 0 {
		t.Fatal("startup retained permissions or evaluated a DTO", adapter)
	}
	operation, err := first.Operation()
	if err != nil || operation.Permission != view || !slices.Equal(operation.AdditionalPermissions, []auth.Permission{change}) || operation.RequestBody.Description != "Typed body." {
		t.Fatal("operation differs from runtime declaration", operation, err)
	}
	operation.AdditionalPermissions[0] = "returned.mutation"
	operation.RequestBody.Description = "returned mutation"
	operation.Responses[0].Status = 202
	operations, schemas, err := endpoint.Collect([]endpoint.Endpoint{first, second}, out.Declaration())
	if err != nil || len(operations) != 2 || len(schemas) != 2 || operations[0].Responses[0].Status != 201 || operations[0].RequestBody.Description != "Typed body." || operations[0].AdditionalPermissions[0] != change {
		t.Fatal("collect lost identity or ownership", err, operations, schemas)
	}
	document, err := openapi.New(openapi.Config{Title: "Endpoints", Version: "1", Authentication: adapter, Operations: operations, Schemas: schemas})
	if err != nil || len(document.Routes()) != 2 {
		t.Fatal("collected graph does not prepare", err)
	}
	response := send(application(t, first, nil), "POST", "/api/create/", `{"name":"ready"}`, false)
	if response.Code != 201 || response.Body.String() != `{"name":"ready"}` || reads.Load() != 1 || sets.Load() != 1 || calls.Load() != 1 {
		t.Fatal("document route differs from typed execution", response.Code, response.Body, reads.Load(), sets.Load(), calls.Load())
	}
	config.Success[0].Code = 201
	config.Input = endpoint.JSONBody("Request", body, serializers.ModeFull, "")
	independent, err := endpoint.New(adapter, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := endpoint.Collect([]endpoint.Endpoint{first, independent}); err == nil {
		t.Fatal("independent input names were silently merged")
	}
	for name, invalid := range map[string]endpoint.Input[bodyDTO]{
		"bad component":      endpoint.JSONBody("no/slash", body, serializers.ModeFull, ""),
		"reserved component": endpoint.JSONBody(openapi.ErrorSchemaName, body, serializers.ModeFull, ""),
		"bad mode":           endpoint.JSONBody("Body", body, serializers.Mode(99), ""),
	} {
		t.Run(name, func(t *testing.T) {
			config.Input = invalid
			if _, err := endpoint.New(adapter, config); err == nil {
				t.Fatal("invalid body accepted")
			}
		})
	}
}

func TestEndpointAdmissionPrecedesTypedQueryAndPreservesActor(t *testing.T) {
	type queryDTO struct{ Number int64 }
	for _, test := range []struct {
		name        string
		admission   endpoint.Admission
		permissions []auth.Permission
		accepted    bool
	}{
		{"all granted", endpoint.All(view, change), []auth.Permission{view, change}, true},
		{"all missing second", endpoint.All(view, change), []auth.Permission{view}, false},
		{"any second", endpoint.Any(view, change), []auth.Permission{change}, true},
		{"any none", endpoint.Any(view, change), nil, false},
		{"single any", endpoint.Any(view), []auth.Permission{view}, true},
		{"principal only", endpoint.Authenticated(), nil, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var sets, calls atomic.Int64
			query, err := parameters.New(64, parameters.Field("n", parameters.Required(parameters.CanonicalInt64(0, 20)), func(value *queryDTO, number int64) { sets.Add(1); value.Number = number }, "Number."))
			if err != nil {
				t.Fatal(err)
			}
			out := encoder(t, output.Int64())
			prepared, err := endpoint.New(bearer(t, test.permissions...), endpoint.Config[queryDTO, int64]{
				Route: web.Route{Name: "test:query", Method: "GET", Path: "/api/query/"}, Admission: test.admission, Input: endpoint.Query(query), Output: out,
				Success: []endpoint.Status{{Code: 200, Description: "Number."}},
				Handle: func(request *web.Request, actor auth.Principal, value queryDTO) (output.Prepared[int64], error) {
					calls.Add(1)
					if actor.ID() != "actor" {
						t.Error("admitted actor lost")
					}
					return out.Prepare(request.Context(), 200, value.Number)
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			operation, _ := prepared.Operation()
			if len(operation.Parameters) != 1 || operation.Parameters[0].Name != "n" || !operation.Parameters[0].Required {
				t.Fatal("query declaration missing")
			}
			operation.Parameters[0].Name = "changed"
			again, _ := prepared.Operation()
			if again.Parameters[0].Name != "n" {
				t.Fatal("operation shares parameter slice")
			}
			app := application(t, prepared, nil)
			anonymous := send(app, "GET", "/api/query/?n=bad", "", false)
			if anonymous.Code != 401 || sets.Load()+calls.Load() != 0 || anonymous.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Fatal("anonymous request reached parsing", anonymous.Code)
			}
			invalid := send(app, "GET", "/api/query/?n=bad", "", true)
			want := 403
			if test.accepted {
				want = 400
			}
			if invalid.Code != want || sets.Load()+calls.Load() != 0 {
				t.Fatal("permission/input precedence", invalid.Code, want, sets.Load(), calls.Load())
			}
			valid := send(app, "GET", "/api/query/?n=7", "", true)
			want = 403
			if test.accepted {
				want = 200
			}
			if valid.Code != want {
				t.Fatal("admission outcome", valid.Code, valid.Body)
			}
			if test.accepted && (valid.Body.String() != "7" || calls.Load() != 1 || sets.Load() != 1) {
				t.Fatal("typed query did not reach handler", valid.Body, calls.Load(), sets.Load())
			}
			if !test.accepted && calls.Load()+sets.Load() != 0 {
				t.Fatal("denied request invoked user code")
			}
		})
	}
}

type observedBody struct {
	io.Reader
	reads, closes int
	failure       error
	cancel        context.CancelFunc
}

func (body *observedBody) Read(data []byte) (int, error) {
	body.reads++
	if body.cancel != nil {
		body.cancel()
	}
	if body.failure != nil {
		return 0, body.failure
	}
	return body.Reader.Read(data)
}
func (body *observedBody) Close() error { body.closes++; return nil }

func TestEndpointBodyErrorsCancellationAndQueryPrecedence(t *testing.T) {
	for _, test := range []struct {
		name, query, body, media    string
		status                      int
		before, during, readFailure bool
		wantReads                   bool
	}{
		{name: "valid", body: `{"name":"value"}`, media: "application/json", status: 200, wantReads: true},
		{name: "required", body: `{}`, media: "application/json", status: 400, wantReads: true},
		{name: "malformed", body: `{`, media: "application/json", status: 400, wantReads: true},
		{name: "too large", body: `{"name":"` + strings.Repeat("x", 70) + `"}`, media: "application/json", status: 413, wantReads: true},
		{name: "media", body: `{}`, media: "text/plain", status: 415},
		{name: "query before media", query: "?q=1", body: `{}`, media: "text/plain", status: 400},
		{name: "raw empty segments", query: "?&&", body: `{}`, media: "application/json", status: 400},
		{name: "before", body: `{"name":"value"}`, media: "application/json", status: 500, before: true},
		{name: "during", body: `{"name":"value"}`, media: "application/json", status: 500, during: true, wantReads: true},
		{name: "reader failure", body: `{}`, media: "application/json", status: 500, readFailure: true, wantReads: true},
		{name: "reader and cancellation", body: `{}`, media: "application/json", status: 500, readFailure: true, during: true, wantReads: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var sets, calls atomic.Int64
			out := encoder(t, output.String())
			prepared, err := endpoint.New(bearer(t, view), endpoint.Config[bodyDTO, string]{
				Route: web.Route{Name: "test:body", Method: "POST", Path: "/api/body/"}, Admission: endpoint.All(view),
				Input: endpoint.NoQuery(endpoint.JSONBody("Body", bodyDeclaration(t, &sets), serializers.ModeFull, "")), Output: out,
				Success: []endpoint.Status{{Code: 200, Description: "Value."}},
				Handle: func(request *web.Request, _ auth.Principal, value bodyDTO) (output.Prepared[string], error) {
					calls.Add(1)
					name, _ := value.Name.Get()
					return out.Prepare(request.Context(), 200, name)
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			var failure error
			app := application(t, prepared, func(response web.Response, err error) {
				failure = err
				if err != nil && response.Status() != 0 {
					t.Error("internal error published a response")
				}
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if test.before {
				cancel()
			}
			private := errors.New("private body I/O failure")
			body := &observedBody{Reader: strings.NewReader(test.body)}
			if test.during {
				body.cancel = cancel
			}
			if test.readFailure {
				body.failure = private
			}
			request := httptest.NewRequest("POST", "/api/body/"+test.query, nil).WithContext(ctx)
			request.Body, request.ContentLength = body, -1
			request.Header.Set("Authorization", "Bearer fixture")
			request.Header.Set("Content-Type", test.media)
			response := httptest.NewRecorder()
			app.ServeHTTP(response, request)
			if response.Code != test.status || (body.reads > 0) != test.wantReads || body.closes != 0 {
				t.Fatal("body handling", response.Code, response.Body, body.reads, body.closes)
			}
			if test.status == 200 {
				if response.Body.String() != `"value"` || sets.Load() != 1 || calls.Load() != 1 {
					t.Fatal("valid DTO not consumed")
				}
			} else if sets.Load()+calls.Load() != 0 {
				t.Fatal("failed input invoked user code", sets.Load(), calls.Load())
			}
			if (test.before || test.during) && !errors.Is(failure, context.Canceled) {
				t.Fatal("cancellation cause lost", failure)
			}
			if test.readFailure && !errors.Is(failure, private) {
				t.Fatal("reader cause lost", failure)
			}
			if strings.Contains(response.Body.String(), "private") {
				t.Fatal("client saw private cause")
			}
		})
	}
}

func TestEndpointDiscardsUntrustedPreparedResponsesAndErrors(t *testing.T) {
	private := errors.New("private uncertain commit")
	for _, mode := range []string{"success", "declared rejection", "undeclared rejection", "invalid rejection", "wrapped rejection", "joined rejection", "internal error", "zero", "foreign same shape", "foreign wider budget", "undeclared success", "mime", "duplicate mime", "missing mime", "completed then cancellation", "cancelled outcome", "rejection and cancellation", "cancellation and failure"} {
		t.Run(mode, func(t *testing.T) {
			config := baseConfig(t)
			if mode == "foreign wider budget" {
				config.Output, _ = output.New(output.String(), serializers.Limits{MaxStringBytes: 1})
			}
			config.Errors = []endpoint.Status{{Code: 404, Description: "Missing."}}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			config.Handle = func(request *web.Request, _ auth.Principal, _ struct{}) (output.Prepared[string], error) {
				out, status := config.Output, 200
				if mode == "foreign same shape" {
					out = encoder(t, output.String())
				}
				if mode == "foreign wider budget" {
					out = encoder(t, output.String())
				}
				if mode == "undeclared success" {
					status = 201
				}
				prepared, err := out.Prepare(request.Context(), status, "prepared private value")
				if err != nil {
					return prepared, err
				}
				switch mode {
				case "declared rejection":
					return prepared, endpoint.Reject(404, api.CodeNotFound, validation.NewErrors())
				case "undeclared rejection":
					return prepared, endpoint.Reject(409, api.CodeValidationError, validation.NewErrors())
				case "invalid rejection":
					return prepared, endpoint.Reject(500, api.CodeValidationError, validation.NewErrors())
				case "wrapped rejection":
					return prepared, fmt.Errorf("private rollback: %w", endpoint.Reject(404, api.CodeNotFound, validation.NewErrors()))
				case "joined rejection":
					return prepared, errors.Join(endpoint.Reject(404, api.CodeNotFound, validation.NewErrors()), private)
				case "internal error":
					return prepared, private
				case "zero":
					return output.Prepared[string]{}, nil
				case "mime":
					return prepared.WithHeaders(http.Header{"Content-Type": {"text/html"}})
				case "duplicate mime":
					return prepared.WithHeaders(http.Header{"Content-Type": {api.JSONContentType}, "content-type": {api.JSONContentType}})
				case "missing mime":
					return prepared.WithHeaders(http.Header{})
				case "completed then cancellation":
					cancel()
				case "cancelled outcome":
					cancel()
					return prepared, context.Canceled
				case "rejection and cancellation":
					cancel()
					return prepared, endpoint.Reject(404, api.CodeNotFound, validation.NewErrors())
				case "cancellation and failure":
					cancel()
					return prepared, private
				}
				return prepared, nil
			}
			prepared, err := endpoint.New(bearer(t, view), config)
			if err != nil {
				t.Fatal(err)
			}
			var failure error
			app := application(t, prepared, func(response web.Response, err error) {
				failure = err
				if err != nil && response.Status() != 0 {
					t.Error("failure retained response")
				}
			})
			request := httptest.NewRequest("GET", "/api/typed/", nil).WithContext(ctx)
			request.Header.Set("Authorization", "Bearer fixture")
			response := httptest.NewRecorder()
			app.ServeHTTP(response, request)
			want := 500
			if mode == "success" || mode == "completed then cancellation" {
				want = 200
			}
			if mode == "declared rejection" {
				want = 404
			}
			if response.Code != want {
				t.Fatal("unexpected result", response.Code, response.Body, failure)
			}
			if want != 200 && strings.Contains(response.Body.String(), "private") {
				t.Fatal("failed response leaked bytes", response.Body)
			}
			if (mode == "cancelled outcome" || mode == "rejection and cancellation" || mode == "cancellation and failure") && !errors.Is(failure, context.Canceled) {
				t.Fatal("lost cancellation", failure)
			}
			if mode == "cancellation and failure" || mode == "internal error" || mode == "joined rejection" {
				if !errors.Is(failure, private) {
					t.Fatal("lost original failure", failure)
				}
			}
		})
	}
}

func TestEndpointReusesImmutableDeclarationsConcurrently(t *testing.T) {
	type queryDTO struct{ Number int64 }
	query, err := parameters.New(32, parameters.Field("n", parameters.Required(parameters.CanonicalInt64(-100, 100)), func(value *queryDTO, number int64) { value.Number = number }, ""))
	if err != nil {
		t.Fatal(err)
	}
	out := encoder(t, output.Int64())
	prepared, err := endpoint.New(bearer(t, view), endpoint.Config[queryDTO, int64]{
		Route: web.Route{Name: "test:query", Method: "GET", Path: "/api/query/"}, Admission: endpoint.All(view), Input: endpoint.Query(query), Output: out,
		Success: []endpoint.Status{{Code: 200, Description: "Number."}},
		Handle: func(request *web.Request, _ auth.Principal, value queryDTO) (output.Prepared[int64], error) {
			return out.Prepare(request.Context(), 200, value.Number)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	app := application(t, prepared, nil)
	before, _ := prepared.Operation()
	var workers sync.WaitGroup
	for index := range 16 {
		workers.Go(func() {
			for range 8 {
				response := send(app, "GET", fmt.Sprintf("/api/query/?n=%d", index), "", true)
				if response.Code != 200 || response.Body.String() != fmt.Sprint(index) {
					t.Error("request DTO leaked", response.Code, response.Body)
				}
				operation, err := prepared.Operation()
				if err != nil || !reflect.DeepEqual(operation.Parameters, before.Parameters) {
					t.Error("operation changed", err)
				}
				operation.Parameters[0].Name = "mutation"
			}
		})
	}
	workers.Wait()
}
