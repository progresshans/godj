package endpoint_test

import (
	"slices"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/endpoint"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/web"
)

func TestCompoundEndpointAdmissionMatchesDocumentBeforeBody(t *testing.T) {
	policy, err := api.NewPermissionPolicy([]auth.Permission{"tests.scope"}, []auth.Permission{view, change})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"first", "alternative", "missing_common", "missing_alternatives", "bad_body"} {
		t.Run(mode, func(t *testing.T) {
			grants := []auth.Permission{"tests.scope", view}
			if mode == "alternative" {
				grants[1] = change
			}
			if mode == "missing_common" {
				grants = grants[1:]
			}
			if mode == "missing_alternatives" {
				grants = grants[:1]
			}
			var sets, calls atomic.Int64
			out := encoder(t, output.String())
			config := endpoint.Config[bodyDTO, string]{Route: web.Route{Name: "test:policy", Method: "POST", Path: "/api/policy/"}, Admission: endpoint.Permissions(policy), Input: endpoint.JSONBody("PolicyBody", bodyDeclaration(t, &sets), serializers.ModeFull, ""), Output: out, Success: []endpoint.Status{{Code: 200, Description: "Value."}}, Handle: func(request *web.Request, _ auth.Principal, _ bodyDTO) (output.Prepared[string], error) {
				calls.Add(1)
				return out.Prepare(request.Context(), 200, "accepted")
			}}
			adapter := bearer(t, grants...)
			prepared, err := endpoint.New(adapter, config)
			if err != nil {
				t.Fatal(err)
			}
			operation, err := prepared.Operation()
			if err != nil || operation.Permission != view || !slices.Equal(operation.AlternativePermissions, []auth.Permission{change}) || !slices.Equal(operation.AdditionalPermissions, []auth.Permission{"tests.scope"}) {
				t.Fatal("compound metadata", operation, err)
			}
			operations, schemas, err := endpoint.Collect([]endpoint.Endpoint{prepared})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := openapi.New(openapi.Config{Title: "Policy", Version: "1", Authentication: adapter, Operations: operations, Schemas: schemas}); err != nil {
				t.Fatal(err)
			}
			body := `{"name":"allowed"}`
			status, count := 200, int64(1)
			if mode == "missing_common" || mode == "missing_alternatives" {
				body, status, count = "invalid JSON", 403, 0
			}
			if mode == "bad_body" {
				body, status, count = "invalid JSON", 400, 0
			}
			response := send(application(t, prepared, nil), "POST", "/api/policy/", body, true)
			if response.Code != status || sets.Load() != count || calls.Load() != count {
				t.Fatal("admission/body order", response.Code, sets.Load(), calls.Load())
			}
		})
	}
	config := baseConfig(t)
	config.Admission = endpoint.Permissions(policy)
	adapter := &startupAdapter{}
	if _, err := endpoint.New(adapter, config); err == nil || adapter.wraps != 0 {
		t.Fatal("missing compound capability did not fail before wrapping")
	}
	config.Admission = endpoint.Permissions(api.PermissionPolicy{})
	if _, err := endpoint.New(adapter, config); err == nil || adapter.wraps != 0 {
		t.Fatal("zero policy published")
	}
	// A single alternative is a conjunction and needs no compound capability.
	single, err := api.NewPermissionPolicy([]auth.Permission{"tests.scope"}, []auth.Permission{view})
	if err != nil {
		t.Fatal(err)
	}
	config.Admission = endpoint.Permissions(single)
	prepared, err := endpoint.New(adapter, config)
	if err != nil || adapter.wraps != 1 || !slices.Equal(adapter.permissions, []auth.Permission{"tests.scope", view}) {
		t.Fatal("single alternative", err, adapter)
	}
	operation, err := prepared.Operation()
	if err != nil || operation.Permission != "tests.scope" || !slices.Equal(operation.AdditionalPermissions, []auth.Permission{view}) || len(operation.AlternativePermissions) != 0 {
		t.Fatal("single alternative metadata", operation, err)
	}
}
