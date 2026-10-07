package endpoint_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/endpoint"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/api/parameters"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

func pathConfig[T any](t *testing.T, path string, input endpoint.Input[T]) endpoint.Config[T, string] {
	t.Helper()
	out := encoder(t, output.String())
	return endpoint.Config[T, string]{
		Route:     web.Route{Name: "test:path", Method: "POST", Path: path},
		Admission: endpoint.All(view), Input: input, Output: out,
		Success: []endpoint.Status{{Code: 200, Description: "Prepared value."}},
		Handle: func(request *web.Request, _ auth.Principal, value T) (output.Prepared[string], error) {
			return out.Prepare(request.Context(), 200, fmt.Sprint(value))
		},
	}
}

func eraseInput[T any](input endpoint.Input[T]) endpoint.Input[struct{}] {
	return endpoint.Resolve(input, func(*web.Request, auth.Principal, T) (struct{}, error) { return struct{}{}, nil })
}

func TestTypedPathSharesRouterConversionAndOpenAPI(t *testing.T) {
	input := endpoint.Sequence(endpoint.PathInt64("id"), endpoint.PathString("key"))
	config := pathConfig(t, "/api/items/<int64:id>/<str:key>/", input)
	var calls atomic.Int64
	config.Handle = func(request *web.Request, actor auth.Principal, value endpoint.Pair[int64, string]) (output.Prepared[string], error) {
		calls.Add(1)
		if actor.ID() != "actor" {
			t.Error("admitted principal changed")
		}
		return config.Output.Prepare(request.Context(), 200, fmt.Sprintf("%d:%s", value.First, value.Second))
	}
	authentication := bearer(t, view)
	prepared, err := endpoint.New(authentication, config)
	if err != nil {
		t.Fatal(err)
	}
	operations, schemas, err := endpoint.Collect([]endpoint.Endpoint{prepared})
	if err != nil {
		t.Fatal(err)
	}
	if len(operations[0].Parameters) != 0 || len(schemas) != 0 {
		t.Fatal("path declarations duplicated router metadata")
	}
	document, err := openapi.New(openapi.Config{Title: "Paths", Version: "1", Authentication: authentication, Operations: operations, Schemas: schemas})
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Paths map[string]map[string]struct {
			Parameters []struct {
				Name, In string
				Required bool
				Schema   map[string]any
			}
		}
	}
	if err := json.Unmarshal(document.Bytes(), &wire); err != nil {
		t.Fatal(err)
	}
	parameters := wire.Paths["/api/items/{id}/{key}/"]["post"].Parameters
	if len(parameters) != 2 || parameters[0].Name != "id" || parameters[0].In != "path" || !parameters[0].Required || parameters[0].Schema["type"] != "integer" || parameters[0].Schema["format"] != "int64" || parameters[1].Name != "key" || parameters[1].Schema["type"] != "string" {
		t.Fatalf("router parameter schema: %#v", parameters)
	}
	if calls.Load() != 0 {
		t.Fatal("startup executed a request")
	}
	app := application(t, prepared, nil)
	for _, test := range []struct {
		name, id, key string
		valid         bool
	}{
		{"zero", "0", "key", true}, {"maximum", "9223372036854775807", "key", true},
		{"unicode and escaped percent", "7", url.PathEscape("한글 %2F?#"), true},
		{"maximum string bytes", "1", strings.Repeat("a", 512), true},
		{"leading zero", "01", "key", false}, {"negative", "-1", "key", false},
		{"overflow", "9223372036854775808", "key", false}, {"encoded separator", "1", "%2F", false},
		{"invalid utf8", "1", "%ff", false}, {"control", "1", "%00", false},
		{"overlong segment", "1", strings.Repeat("a", 513), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := calls.Load()
			response := send(app, "POST", "/api/items/"+test.id+"/"+test.key+"/", "", true)
			if !test.valid {
				if response.Code == 200 || calls.Load() != before {
					t.Fatal("invalid router segment reached handler", response.Code)
				}
				return
			}
			key, err := url.PathUnescape(test.key)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := json.Marshal(test.id + ":" + key)
			if response.Code != 200 || response.Body.String() != string(want) || calls.Load() != before+1 {
				t.Fatal("typed conversion differs", response.Code, response.Body)
			}
		})
	}
}

func TestInputCompositionRejectsConflictingSourcesAndPathsBeforeAdmission(t *testing.T) {
	var sets atomic.Int64
	body := endpoint.JSONBody("Body", bodyDeclaration(t, &sets), serializers.ModeFull, "")
	query, err := parameters.New(128, parameters.Field("page", parameters.Required(parameters.CanonicalInt64(1, 10)), func(*struct{}, int64) {}, "Page."))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, path string
		input      endpoint.Input[struct{}]
	}{
		{"unknown", "/<int64:id>/", eraseInput(endpoint.PathInt64("other"))},
		{"empty name", "/<int64:id>/", eraseInput(endpoint.PathInt64(""))},
		{"wrong converter", "/<str:id>/", eraseInput(endpoint.PathInt64("id"))},
		{"string on integer", "/<int64:id>/", eraseInput(endpoint.PathString("id"))},
		{"missing binding", "/<int64:id>/<str:key>/", eraseInput(endpoint.PathInt64("id"))},
		{"duplicate binding", "/<int64:id>/<int64:other>/", eraseInput(endpoint.Sequence(endpoint.PathInt64("id"), endpoint.PathInt64("id")))},
		{"binding on static", "/static/", eraseInput(endpoint.PathInt64("id"))},
		{"repeated body", "/static/", eraseInput(endpoint.Sequence(body, body))},
		{"repeated query", "/static/", eraseInput(endpoint.Sequence(endpoint.Query(query), endpoint.Query(query)))},
		{"NoQuery first", "/static/", eraseInput(endpoint.Sequence(endpoint.NoQuery(endpoint.NoInput()), endpoint.Query(query)))},
		{"NoQuery second", "/static/", eraseInput(endpoint.Sequence(endpoint.Query(query), endpoint.NoQuery(body)))},
		{"zero stage", "/static/", eraseInput(endpoint.Sequence(endpoint.NoInput(), endpoint.Input[string]{}))},
		{"zero resolution", "/static/", endpoint.Resolve(endpoint.Input[string]{}, func(*web.Request, auth.Principal, string) (struct{}, error) { return struct{}{}, nil })},
		{"nil resolver", "/static/", endpoint.Resolve(endpoint.NoInput(), (func(*web.Request, auth.Principal, struct{}) (struct{}, error))(nil))},
	} {
		t.Run(test.name, func(t *testing.T) {
			adapter := &startupAdapter{}
			if _, err := endpoint.New(adapter, pathConfig(t, test.path, test.input)); err == nil || adapter.wraps != 0 || sets.Load() != 0 {
				t.Fatal("invalid source reached admission", err, adapter.wraps)
			}
		})
	}
	// Manual path handling remains available when no typed path is declared.
	if _, err := endpoint.New(&startupAdapter{}, pathConfig(t, "/<int64:id>/", endpoint.NoInput())); err != nil {
		t.Fatal(err)
	}
	input := endpoint.NoInput()
	for i := 1; i < 64; i++ {
		input = eraseInput(input)
	}
	if _, err := endpoint.New(&startupAdapter{}, pathConfig(t, "/depth/", input)); err != nil {
		t.Fatal("64 stages rejected", err)
	}
	for name, overlong := range map[string]endpoint.Input[struct{}]{"resolve": eraseInput(input), "no query": endpoint.NoQuery(input), "sequence": eraseInput(endpoint.Sequence(input, endpoint.NoInput()))} {
		t.Run(name+" depth", func(t *testing.T) {
			if _, err := endpoint.New(&startupAdapter{}, pathConfig(t, "/depth/", overlong)); err == nil {
				t.Fatal("overlong pipeline accepted")
			}
		})
	}
}

func TestOrderedInputStopsAfterAdmissionResolutionBodyOrCancellationFailure(t *testing.T) {
	for _, test := range []struct {
		name, body                     string
		status                         int
		resolved, read, bound, handled bool
	}{
		{"valid", `{"name":"value"}`, 200, true, true, true, true},
		{"unauthenticated", `{`, 401, false, false, false, false},
		{"forbidden", `{`, 403, false, false, false, false},
		{"cancelled before", `{`, 500, false, false, false, false},
		{"not found", `{`, 404, true, false, false, false},
		{"undeclared rejection", `{`, 500, true, false, false, false},
		{"wrapped rejection", `{`, 500, true, false, false, false},
		{"direct parser error", `{`, 500, true, false, false, false},
		{"private lookup error", `{`, 500, true, false, false, false},
		{"cancelled lookup", `{`, 500, true, false, false, false},
		{"malformed body", `{`, 400, true, true, false, false},
		{"invalid body", `{}`, 400, true, true, false, false},
		{"reader failure", `{}`, 500, true, true, false, false},
		{"cancelled body", `{"name":"value"}`, 500, true, true, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var sets, resolutions, handlers atomic.Int64
			private := errors.New("private resolution failure")
			path := endpoint.Resolve(endpoint.PathInt64("id"), func(request *web.Request, actor auth.Principal, id int64) (string, error) {
				resolutions.Add(1)
				if actor.ID() != "actor" || id != 7 || request.Context() != ctx {
					t.Error("resolution lost admitted inputs")
				}
				switch test.name {
				case "not found":
					return "partial must not escape", endpoint.Reject(404, api.CodeNotFound, validation.NewErrors())
				case "undeclared rejection":
					return "partial", endpoint.Reject(409, api.CodeNotFound, validation.NewErrors())
				case "wrapped rejection":
					return "partial", fmt.Errorf("private wrapper: %w", endpoint.Reject(404, api.CodeNotFound, validation.NewErrors()))
				case "direct parser error":
					return "partial", &api.Error{Code: api.FailureBodyTooLarge}
				case "private lookup error":
					return "partial", private
				case "cancelled lookup":
					cancel()
					return "partial", private
				}
				return "resolved 7", nil
			})
			input := endpoint.Sequence(path, endpoint.JSONBody("Body", bodyDeclaration(t, &sets), serializers.ModeFull, ""))
			config := pathConfig(t, "/api/items/<int64:id>/", input)
			config.Errors = []endpoint.Status{{Code: 404, Description: "Target missing."}}
			config.Handle = func(request *web.Request, actor auth.Principal, value endpoint.Pair[string, bodyDTO]) (output.Prepared[string], error) {
				handlers.Add(1)
				name, present := value.Second.Name.Get()
				if actor.ID() != "actor" || !present || name != "value" || value.First != "resolved 7" {
					t.Error("typed pair or actor changed")
				}
				return config.Output.Prepare(request.Context(), 200, value.First+":"+name)
			}
			authentication := bearer(t, view)
			if test.name == "forbidden" {
				authentication = bearer(t)
			}
			prepared, err := endpoint.New(authentication, config)
			if err != nil {
				t.Fatal(err)
			}
			if resolutions.Load()+sets.Load()+handlers.Load() != 0 {
				t.Fatal("startup ran a stage")
			}
			var failure error
			app := application(t, prepared, func(response web.Response, err error) {
				failure = err
				if err != nil && response.Status() != 0 {
					t.Error("error published partial response")
				}
			})
			body := &observedBody{Reader: strings.NewReader(test.body)}
			if test.name == "reader failure" {
				body.failure = private
			}
			if test.name == "cancelled body" {
				body.cancel = cancel
			}
			if test.name == "cancelled before" {
				cancel()
			}
			request := httptest.NewRequest("POST", "/api/items/7/", nil).WithContext(ctx)
			request.Body, request.ContentLength = body, -1
			request.Header.Set("Content-Type", "application/json")
			if test.name != "unauthenticated" {
				request.Header.Set("Authorization", "Bearer fixture")
			}
			response := httptest.NewRecorder()
			app.ServeHTTP(response, request)
			if response.Code != test.status || (resolutions.Load() != 0) != test.resolved || (body.reads != 0) != test.read || (sets.Load() != 0) != test.bound || (handlers.Load() != 0) != test.handled || body.closes != 0 {
				t.Fatal("stage order changed", response.Code, response.Body, resolutions.Load(), body.reads, sets.Load(), handlers.Load())
			}
			if test.name == "valid" && response.Body.String() != `"resolved 7:value"` {
				t.Fatal(response.Body)
			}
			if strings.HasPrefix(test.name, "cancelled") && !errors.Is(failure, context.Canceled) {
				t.Fatal("cancellation cause lost", failure)
			}
			if (test.name == "private lookup error" || test.name == "cancelled lookup" || test.name == "reader failure") && !errors.Is(failure, private) {
				t.Fatal("internal cause lost", failure)
			}
			if strings.Contains(response.Body.String(), "private") || strings.Contains(response.Body.String(), "partial") {
				t.Fatal("private or partial result escaped")
			}
		})
	}
}

func TestComposedInputPreservesSharedSchemasAndConcurrentValues(t *testing.T) {
	var sets atomic.Int64
	shared := endpoint.JSONBody("SharedBody", bodyDeclaration(t, &sets), serializers.ModeFull, "Body.")
	compose := func(source endpoint.Input[bodyDTO]) endpoint.Input[endpoint.Pair[int64, bodyDTO]] {
		body := endpoint.Resolve(endpoint.NoQuery(source), func(_ *web.Request, _ auth.Principal, value bodyDTO) (bodyDTO, error) { return value, nil })
		return endpoint.Sequence(endpoint.PathInt64("id"), body)
	}
	config := pathConfig(t, "/api/first/<int64:id>/", compose(shared))
	config.Handle = func(request *web.Request, _ auth.Principal, value endpoint.Pair[int64, bodyDTO]) (output.Prepared[string], error) {
		name, _ := value.Second.Name.Get()
		return config.Output.Prepare(request.Context(), 200, fmt.Sprintf("%d:%s", value.First, name))
	}
	authentication := bearer(t, view)
	first, err := endpoint.New(authentication, config)
	if err != nil {
		t.Fatal(err)
	}
	config.Route.Name, config.Route.Path, config.Input = "test:second", "/api/second/<int64:id>/", compose(shared)
	second, err := endpoint.New(authentication, config)
	if err != nil {
		t.Fatal(err)
	}
	operations, schemas, err := endpoint.Collect([]endpoint.Endpoint{first, second})
	if err != nil || len(schemas) != 1 || schemas[0].Name != "SharedBody" {
		t.Fatal("composed input lost source identity", schemas, err)
	}
	if _, err := openapi.New(openapi.Config{Title: "Shared inputs", Version: "1", Authentication: authentication, Operations: operations, Schemas: schemas}); err != nil {
		t.Fatal(err)
	}
	config.Input = compose(endpoint.JSONBody("SharedBody", bodyDeclaration(t, &sets), serializers.ModeFull, "Body."))
	independent, err := endpoint.New(authentication, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := endpoint.Collect([]endpoint.Endpoint{first, independent}); err == nil {
		t.Fatal("independent schemas silently shared a name")
	}
	app := application(t, first, nil)
	var group sync.WaitGroup
	for id := 1; id <= 20; id++ {
		group.Go(func() {
			name := fmt.Sprintf("name-%d", id)
			response := send(app, "POST", fmt.Sprintf("/api/first/%d/", id), fmt.Sprintf(`{"name":%q}`, name), true)
			want, _ := json.Marshal(fmt.Sprintf("%d:%s", id, name))
			if response.Code != 200 || response.Body.String() != string(want) {
				t.Error("concurrent input crossed request boundary", response.Code, response.Body)
			}
		})
	}
	group.Wait()
	if sets.Load() != 20 {
		t.Fatal("body setters not request-local", sets.Load())
	}
	response := send(app, "POST", "/api/first/1/?unexpected=1", `{"name":"skip"}`, true)
	if response.Code != 400 || sets.Load() != 20 {
		t.Fatal("wrapped query policy was lost", response.Code)
	}
}

func TestSequenceCombinesQueryAndBodyInDeclaredOrder(t *testing.T) {
	type queryDTO struct{ Page int64 }
	for _, order := range []string{"query first", "body first"} {
		for _, raw := range []string{"page=2", "page=invalid"} {
			t.Run(order+"/"+raw, func(t *testing.T) {
				var sets atomic.Int64
				query, err := parameters.New(128, parameters.Field("page", parameters.Required(parameters.CanonicalInt64(1, 9)), func(value *queryDTO, page int64) { value.Page = page }, "Page."))
				if err != nil {
					t.Fatal(err)
				}
				body := endpoint.JSONBody("Body", bodyDeclaration(t, &sets), serializers.ModeFull, "Body.")
				var input endpoint.Input[string]
				if order == "query first" {
					input = endpoint.Resolve(endpoint.Sequence(endpoint.Query(query), body), func(_ *web.Request, _ auth.Principal, value endpoint.Pair[queryDTO, bodyDTO]) (string, error) {
						name, _ := value.Second.Name.Get()
						return fmt.Sprintf("%d:%s", value.First.Page, name), nil
					})
				} else {
					input = endpoint.Resolve(endpoint.Sequence(body, endpoint.Query(query)), func(_ *web.Request, _ auth.Principal, value endpoint.Pair[bodyDTO, queryDTO]) (string, error) {
						name, _ := value.First.Name.Get()
						return fmt.Sprintf("%d:%s", value.Second.Page, name), nil
					})
				}
				prepared, err := endpoint.New(bearer(t, view), pathConfig(t, "/api/combined/", input))
				if err != nil {
					t.Fatal(err)
				}
				operation, err := prepared.Operation()
				if err != nil {
					t.Fatal(err)
				}
				if len(operation.Parameters) != 1 || operation.RequestBody == nil {
					t.Fatal("combined source declarations were lost")
				}
				response := send(application(t, prepared, nil), "POST", "/api/combined/?"+raw, `{"name":"value"}`, true)
				if raw == "page=2" {
					if response.Code != 200 || response.Body.String() != `"2:value"` || sets.Load() != 1 {
						t.Fatal("combined input failed", response.Code, response.Body)
					}
				} else {
					wantSets := int64(0)
					if order == "body first" {
						wantSets = 1
					}
					if response.Code != 400 || sets.Load() != wantSets {
						t.Fatal("declared input order changed", response.Code, sets.Load())
					}
				}
			})
		}
	}
}

func boundedInputTree(depth int) endpoint.Input[struct{}] {
	if depth == 0 {
		return endpoint.NoInput()
	}
	return eraseInput(endpoint.Sequence(boundedInputTree(depth-1), boundedInputTree(depth-1)))
}

func TestInputGraphBudgetCountsSharedSourceIdentity(t *testing.T) {
	// Two independent trees have 2047 nodes each. Two policy nodes reach the
	// aggregate 4096 identity limit without exceeding either request's budget.
	var endpoints []endpoint.Endpoint
	for i := 0; i < 2; i++ {
		tree := boundedInputTree(10)
		for _, input := range []endpoint.Input[struct{}]{tree, endpoint.NoQuery(tree)} {
			prepared, err := endpoint.New(&startupAdapter{}, pathConfig(t, "/tree/", input))
			if err != nil {
				t.Fatal(err)
			}
			endpoints = append(endpoints, prepared)
		}
	}
	if _, _, err := endpoint.Collect(endpoints); err != nil {
		t.Fatal("shared source was counted twice", err)
	}
	extra, err := endpoint.New(&startupAdapter{}, pathConfig(t, "/extra/", endpoint.NoInput()))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := endpoint.Collect(append(endpoints, extra)); err == nil {
		t.Fatal("aggregate 4097 identity graph accepted")
	}
}

func TestSharedStagesCannotAmplifyExecutionPastTheBudget(t *testing.T) {
	var calls atomic.Int64
	input := endpoint.Resolve(endpoint.NoInput(), func(*web.Request, auth.Principal, struct{}) (struct{}, error) { calls.Add(1); return struct{}{}, nil })
	for i := 0; i < 10; i++ {
		input = eraseInput(endpoint.Sequence(input, input))
	}
	// The shared graph is small and depth is only 22, but running this input
	// would visit 4094 stages. Two policy stages reach the exact execution cap.
	input = endpoint.NoQuery(endpoint.NoQuery(input))
	prepared, err := endpoint.New(bearer(t, view), pathConfig(t, "/budget/", input))
	if err != nil {
		t.Fatal("4096 execution stages rejected", err)
	}
	if calls.Load() != 0 {
		t.Fatal("construction evaluated shared stages")
	}
	response := send(application(t, prepared, nil), "POST", "/budget/", "", true)
	if response.Code != 200 || calls.Load() != 1024 {
		t.Fatal("allowed shared input did not execute exactly", response.Code, calls.Load())
	}
	for name, invalid := range map[string]endpoint.Input[struct{}]{"policy": endpoint.NoQuery(input), "resolve": eraseInput(input), "duplicate": eraseInput(endpoint.Sequence(input, input))} {
		t.Run(name, func(t *testing.T) {
			if _, err := endpoint.New(&startupAdapter{}, pathConfig(t, "/overflow/", invalid)); err == nil {
				t.Fatal("execution amplification accepted")
			}
		})
	}
	if calls.Load() != 1024 {
		t.Fatal("rejected declaration executed a stage")
	}
}
