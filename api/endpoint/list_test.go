package endpoint_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/endpoint"
	bodyinput "github.com/progresshans/godj/api/input"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

func TestJSONListSharesItemIdentityAndPreservesAdmission(t *testing.T) {
	var sets, calls atomic.Int64
	item := endpoint.JSONBody("Item", bodyDeclaration(t, &sets), serializers.ModeFull, "One item.")
	list := endpoint.NoQuery(endpoint.JSONListBody(item, bodyinput.ListConfig{MinItems: 1, MaxItems: 2, Parser: api.ParserConfig{MaxBodyBytes: 256}, ItemLimits: serializers.Limits{MaxDocumentBytes: 64}}, "At most two items."))
	authentication := bearer(t, view)
	singleConfig := pathConfig(t, "/one/", item)
	singleConfig.Route.Name = "test:one"
	single, err := endpoint.New(authentication, singleConfig)
	if err != nil {
		t.Fatal(err)
	}
	config := pathConfig(t, "/many/", list)
	config.Route.Name = "test:many"
	config.Handle = func(request *web.Request, _ auth.Principal, values []bodyDTO) (output.Prepared[string], error) {
		calls.Add(1)
		a, _ := values[0].Name.Get()
		return config.Output.Prepare(request.Context(), 200, fmt.Sprintf("%s:%d", a, len(values)))
	}
	prepared, err := endpoint.New(authentication, config)
	if err != nil {
		t.Fatal(err)
	}
	operations, schemas, err := endpoint.Collect([]endpoint.Endpoint{single, prepared})
	if err != nil {
		t.Fatal(err)
	}
	if len(schemas) != 1 || schemas[0].Name != "Item" || calls.Load() != 0 || sets.Load() != 0 {
		t.Fatal("shared body identity or startup purity lost", schemas)
	}
	document, err := openapi.New(openapi.Config{Title: "Lists", Version: "1", Authentication: authentication, Operations: operations, Schemas: schemas})
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Paths map[string]map[string]struct {
			RequestBody struct {
				Content map[string]struct {
					Schema struct {
						Type               string
						MinItems, MaxItems int
						Items              struct {
							Ref string `json:"$ref"`
						}
					}
				}
			}
		}
	}
	if err = json.Unmarshal(document.Bytes(), &wire); err != nil {
		t.Fatal(err)
	}
	schema := wire.Paths["/many/"]["post"].RequestBody.Content[api.JSONContentType].Schema
	if schema.Type != "array" || schema.MinItems != 1 || schema.MaxItems != 2 || schema.Items.Ref != "#/components/schemas/Item" {
		t.Fatalf("list item schema is not shared: %+v", schema)
	}
	app := application(t, prepared, nil)
	for _, test := range []struct {
		name, raw, target string
		auth              bool
		status            int
		read              bool
	}{
		{"accepted", `[ {"name":"  first  "}, {"name":"second"} ]` + strings.Repeat(" ", 80), "/many/", true, 200, true},
		{"invalid second", `[{"name":"valid"},{}]`, "/many/", true, 400, true},
		{"count", `[]`, "/many/", true, 400, true},
		{"query before read", `not json`, "/many/?bad=1", true, 400, false},
		{"admission before read", `not json`, "/many/", false, 401, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := calls.Load()
			reader := &observedBody{Reader: strings.NewReader(test.raw)}
			request := httptest.NewRequest("POST", test.target, nil)
			request.Body = reader
			request.ContentLength = -1
			request.Header.Set("Content-Type", api.JSONContentType)
			if test.auth {
				request.Header.Set("Authorization", "Bearer fixture")
			}
			response := httptest.NewRecorder()
			app.ServeHTTP(response, request)
			if response.Code != test.status || reader.closes != 0 || (reader.reads != 0) != test.read {
				t.Fatal("admission, query or body lifetime changed", response.Code, response.Body, reader)
			}
			if test.status == 200 {
				if calls.Load() != before+1 || response.Body.String() != `"first:2"` {
					t.Fatal("typed rows differ", response.Body)
				}
			} else if calls.Load() != before {
				t.Fatal("failed list reached handler")
			}
		})
	}
	independent := endpoint.JSONBody("Item", bodyDeclaration(t, &sets), serializers.ModeFull, "")
	other, err := endpoint.New(authentication, pathConfig(t, "/other/", endpoint.JSONListBody(independent, bodyinput.ListConfig{MaxItems: 2}, "")))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = endpoint.Collect([]endpoint.Endpoint{single, other}); err == nil {
		t.Fatal("independent item declarations shared a component name")
	}
}

func TestJSONListRejectsWrappedOrConflictingSources(t *testing.T) {
	var sets atomic.Int64
	item := endpoint.JSONBody("Item", bodyDeclaration(t, &sets), serializers.ModeFull, "")
	for name, source := range map[string]endpoint.Input[bodyDTO]{
		"zero":         {},
		"query policy": endpoint.NoQuery(item),
		"preparation": endpoint.Resolve(item, func(*web.Request, auth.Principal, bodyDTO) (bodyDTO, error) {
			t.Error("startup resolved input")
			return bodyDTO{}, nil
		}),
	} {
		t.Run(name, func(t *testing.T) {
			adapter := &startupAdapter{}
			_, err := endpoint.New(adapter, pathConfig(t, "/many/", endpoint.JSONListBody(source, bodyinput.ListConfig{MaxItems: 2}, "")))
			if err == nil || adapter.wraps != 0 || sets.Load() != 0 {
				t.Fatal("wrapped policy was silently discarded", err)
			}
		})
	}
	list := endpoint.JSONListBody(item, bodyinput.ListConfig{MaxItems: 2}, "")
	if _, err := endpoint.New(&startupAdapter{}, pathConfig(t, "/many/", endpoint.Sequence(item, list))); err == nil {
		t.Fatal("same request body read twice")
	}
	if _, err := endpoint.New(&startupAdapter{}, pathConfig(t, "/many/", endpoint.JSONListBody(list, bodyinput.ListConfig{MaxItems: 2}, ""))); err == nil {
		t.Fatal("nested arbitrary array accepted")
	}
	for _, limits := range []serializers.Limits{{MaxDepth: -1}, {MaxValues: 1}, {MaxDocumentBytes: 2}} {
		config := pathConfig(t, "/many/", list)
		config.ErrorLimits = limits
		config.SummarizeValidationErrors = true
		adapter := &startupAdapter{}
		if _, err := endpoint.New(adapter, config); err == nil || adapter.wraps != 0 {
			t.Fatal("invalid error budget accepted", limits, err)
		}
	}
}

func TestEndpointBoundsBothInputAndDirectRejectionDiagnostics(t *testing.T) {
	violations := make([]validation.Violation, 1500)
	for i := range violations {
		violations[i] = validation.New("name", "invalid")
	}
	diagnostics := validation.NewErrors(violations...)
	for _, origin := range []string{"input", "resolver", "handler"} {
		for _, policy := range []string{"default", "large", "summary"} {
			t.Run(origin+"/"+policy, func(t *testing.T) {
				var sets atomic.Int64
				item := endpoint.JSONBody("Item", bodyDeclaration(t, &sets), serializers.ModeFull, "")
				validator := func(context.Context, bodyDTO) (validation.Errors, error) {
					if origin == "input" {
						return diagnostics, nil
					}
					return validation.Errors{}, nil
				}
				list := endpoint.JSONListBody(item, bodyinput.ListConfig{MaxItems: 1}, "", validator)
				if origin == "resolver" {
					list = endpoint.Resolve(list, func(*web.Request, auth.Principal, []bodyDTO) ([]bodyDTO, error) {
						return nil, endpoint.Reject(400, api.CodeValidationError, diagnostics)
					})
				}
				config := pathConfig(t, "/many/", list)
				if policy == "large" {
					config.ErrorLimits = serializers.Limits{MaxValues: 65536, MaxArrayItems: 2048}
				}
				config.SummarizeValidationErrors = policy == "summary"
				config.Handle = func(*web.Request, auth.Principal, []bodyDTO) (output.Prepared[string], error) {
					if origin != "handler" {
						t.Error("failed input reached handler")
					}
					return output.Prepared[string]{}, endpoint.Reject(400, api.CodeValidationError, diagnostics)
				}
				prepared, err := endpoint.New(bearer(t, view), config)
				if err != nil {
					t.Fatal(err)
				}
				response := send(application(t, prepared, nil), "POST", "/many/", `[{"name":"ok"}]`, true)
				if policy == "default" {
					if response.Code != 500 {
						t.Fatal("oversized diagnostics escaped default budget", response.Code)
					}
					return
				}
				var envelope struct {
					Code   string
					Errors []struct {
						Field, Code string
						Params      []struct{ Key, Value string }
					}
				}
				if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || response.Code != 400 || envelope.Code != "validation_error" {
					t.Fatal("error envelope", response.Code, response.Body, err)
				}
				if policy == "large" {
					if len(envelope.Errors) != 1500 {
						t.Fatal("wide budget truncated diagnostics", len(envelope.Errors))
					}
					return
				}
				if len(envelope.Errors) != 1 || envelope.Errors[0].Field != "__all__" || envelope.Errors[0].Code != "too_many_errors" || len(envelope.Errors[0].Params) != 1 || envelope.Errors[0].Params[0].Key != "count" || envelope.Errors[0].Params[0].Value != "1500" {
					t.Fatal("summary concealed the complete count", response.Body)
				}
			})
		}
	}
}

func TestErrorSummaryDoesNotReclassifyInternalFailures(t *testing.T) {
	private := errors.New("private outcome unknown")
	for _, mode := range []string{"wrapped rejection", "joined rejection", "validator parser error", "invalid diagnostic", "nonvalidation overflow"} {
		t.Run(mode, func(t *testing.T) {
			var sets atomic.Int64
			item := endpoint.JSONBody("Item", bodyDeclaration(t, &sets), serializers.ModeFull, "")
			validator := func(context.Context, bodyDTO) (validation.Errors, error) {
				if mode == "validator parser error" {
					return validation.Errors{}, &api.Error{Code: api.FailureInvalidRequest, Cause: private}
				}
				return validation.Errors{}, nil
			}
			config := pathConfig(t, "/many/", endpoint.JSONListBody(item, bodyinput.ListConfig{MaxItems: 1}, "", validator))
			config.SummarizeValidationErrors = true
			config.Errors = []endpoint.Status{{Code: 404, Description: "Missing."}}
			config.Handle = func(*web.Request, auth.Principal, []bodyDTO) (output.Prepared[string], error) {
				failure := endpoint.Reject(400, api.CodeValidationError, validation.NewErrors(validation.New("name", "invalid")))
				switch mode {
				case "wrapped rejection":
					return output.Prepared[string]{}, fmt.Errorf("private wrapper: %w", failure)
				case "joined rejection":
					return output.Prepared[string]{}, errors.Join(private, failure)
				case "invalid diagnostic":
					return output.Prepared[string]{}, endpoint.Reject(400, api.CodeValidationError, validation.NewErrors(validation.New("name\x00private", "invalid")))
				case "nonvalidation overflow":
					return output.Prepared[string]{}, endpoint.Reject(404, api.CodeNotFound, validation.NewErrors(validation.New("name", "invalid", validation.NewParam("data", strings.Repeat("x", 1<<20)))))
				default:
					t.Error("validator failure reached handler")
					return output.Prepared[string]{}, nil
				}
			}
			prepared, err := endpoint.New(bearer(t, view), config)
			if err != nil {
				t.Fatal(err)
			}
			var observed error
			response := send(application(t, prepared, func(_ web.Response, err error) { observed = err }), "POST", "/many/", `[{"name":"ok"}]`, true)
			if response.Code != 500 || observed == nil || strings.Contains(response.Body.String(), "private") {
				t.Fatal("internal failure became a public validation summary", response.Code, response.Body, observed)
			}
			if (mode == "joined rejection" || mode == "validator parser error") && !errors.Is(observed, private) {
				t.Fatal("internal cause lost", observed)
			}
		})
	}
}
