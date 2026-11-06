package endpoint_test

import (
	"bytes"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/endpoint"
	"github.com/progresshans/godj/api/input"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/web"
)

func TestObjectEndpointsShareExactSchemasAndAdmissionBeforeBinding(t *testing.T) {
	type requestDTO struct{ Fields, Credential bodyDTO }
	var setters, handlers atomic.Int64
	leaf := bodyDeclaration(t, &setters)
	body, err := input.Object(api.ParserConfig{MaxBodyBytes: 256},
		input.ObjectField("fields", leaf, func(out *requestDTO, value bodyDTO) { setters.Add(1); out.Fields = value }),
		input.ObjectField("credential", leaf, func(out *requestDTO, value bodyDTO) { setters.Add(1); out.Credential = value }),
	)
	if err != nil {
		t.Fatal(err)
	}
	item := endpoint.JSONBody("EnvelopeRequest", body, serializers.ModeFull, "Separate typed inputs.")
	out := encoder(t, output.Int64())
	adapter := bearer(t, view)
	single, err := endpoint.New(adapter, endpoint.Config[requestDTO, int64]{
		Route: web.Route{Name: "test:object", Method: "POST", Path: "/api/object/"}, Admission: endpoint.All(view), Input: item, Output: out,
		Success: []endpoint.Status{{Code: 200, Description: "Accepted object count."}},
		Handle: func(request *web.Request, _ auth.Principal, _ requestDTO) (output.Prepared[int64], error) {
			handlers.Add(1)
			return out.Prepare(request.Context(), 200, 1)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	list, err := endpoint.New(adapter, endpoint.Config[[]requestDTO, int64]{
		Route: web.Route{Name: "test:objects", Method: "POST", Path: "/api/objects/"}, Admission: endpoint.All(view), Input: endpoint.JSONListBody(item, input.ListConfig{MinItems: 1, MaxItems: 2}, "Object envelopes."), Output: out,
		Success: []endpoint.Status{{Code: 200, Description: "Accepted object count."}},
		Handle: func(request *web.Request, _ auth.Principal, values []requestDTO) (output.Prepared[int64], error) {
			handlers.Add(1)
			return out.Prepare(request.Context(), 200, int64(len(values)))
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	operations, schemas, err := endpoint.Collect([]endpoint.Endpoint{single, list})
	if err != nil || len(schemas) != 1 {
		t.Fatal("shared envelope schema", len(schemas), err)
	}
	document, err := openapi.New(openapi.Config{Title: "Objects", Version: "1", Authentication: adapter, Operations: operations, Schemas: schemas})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(document.Bytes(), []byte(`"required":["fields","credential"]`)) || !bytes.Contains(document.Bytes(), []byte(`"items":{"$ref":"#/components/schemas/EnvelopeRequest"}`)) {
		t.Fatal("nested or array schema lost its actual input")
	}
	valid := `{"fields":{"name":"model"},"credential":{"name":"command"}}`
	for _, test := range []struct {
		name, path, raw   string
		endpoint          endpoint.Endpoint
		authenticated     bool
		status            int
		setters, handlers int64
		body              string
	}{
		{name: "single", path: "/api/object/", raw: valid, endpoint: single, authenticated: true, status: 200, setters: 4, handlers: 1, body: "1"},
		{name: "list", path: "/api/objects/", raw: "[" + valid + "," + valid + "]", endpoint: list, authenticated: true, status: 200, setters: 8, handlers: 1, body: "2"},
		{name: "first_child", path: "/api/object/", raw: `{"fields":{},"credential":{"name":"command"}}`, endpoint: single, authenticated: true, status: 400},
		{name: "second_child", path: "/api/object/", raw: `{"fields":{"name":"model"},"credential":{}}`, endpoint: single, authenticated: true, status: 400},
		{name: "unknown", path: "/api/object/", raw: `{"fields":{"name":"model"},"credential":{"name":"command"},"hidden":1}`, endpoint: single, authenticated: true, status: 400},
		{name: "anonymous", path: "/api/object/", raw: "malformed", endpoint: single, status: 401},
	} {
		t.Run(test.name, func(t *testing.T) {
			setters.Store(0)
			handlers.Store(0)
			response := send(application(t, test.endpoint, nil), "POST", test.path, test.raw, test.authenticated)
			if response.Code != test.status || setters.Load() != test.setters || handlers.Load() != test.handlers || test.body != "" && response.Body.String() != test.body {
				t.Fatal("envelope endpoint admission/binding", response.Code, setters.Load(), handlers.Load(), response.Body)
			}
		})
	}
}
