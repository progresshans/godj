package consumer

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"example.com/godj-project-bundle/inventory"
	"github.com/progresshans/godj/api/bearerauth"
	"github.com/progresshans/godj/api/endpoint"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
)

type observedBackend struct {
	resourceBackend
	reads, writes atomic.Int64
}

func (backend *observedBackend) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	backend.reads.Add(1)
	return backend.resourceBackend.Query(ctx, plan)
}
func (backend *observedBackend) Atomic(ctx context.Context, run func(db.Session) error) error {
	backend.writes.Add(1)
	return backend.resourceBackend.Atomic(ctx, run)
}
func (backend *observedBackend) AtomicRelation(ctx context.Context, run func(db.RelationSession) error) error {
	backend.writes.Add(1)
	return backend.resourceBackend.AtomicRelation(ctx, run)
}

type bodyProbe struct {
	io.Reader
	reads atomic.Int64
}

func (body *bodyProbe) Read(value []byte) (int, error) {
	body.reads.Add(1)
	return body.Reader.Read(value)
}

type verifierFunc func(context.Context, bearerauth.Token) (auth.Principal, error)

func (f verifierFunc) Verify(ctx context.Context, token bearerauth.Token) (auth.Principal, error) {
	return f(ctx, token)
}

func principal(t *testing.T, id string, permission auth.Permission) auth.Principal {
	t.Helper()
	value, err := auth.NewPrincipal(auth.PrincipalConfig{ID: id, Active: true, Staff: true, Permissions: []auth.Permission{"inventory.view_asset", permission}})
	check(t, err)
	return value
}

func verifyHTTP(t *testing.T, backend resourceBackend) {
	probe := &observedBackend{resourceBackend: backend}
	a := newAssembly(t, probe, "http")
	current, old := principal(t, "writer", editPermission()), principal(t, "old", oldPermission)
	authentication, err := bearerauth.New(bearerauth.Config{Authorizer: auth.PrincipalAuthorizer{}, Verifier: verifierFunc(func(_ context.Context, token bearerauth.Token) (auth.Principal, error) {
		if token.Encoded() == "old" {
			return old, nil
		}
		return current, nil
	})})
	check(t, err)
	create, err := endpoint.New(authentication, endpoint.Config[orm.ModelInput[inventory.Asset], inventory.Asset]{
		Route:     web.Route{Name: "inventory:create", Method: http.MethodPost, Path: "/api/assets/"},
		Admission: endpoint.All(editPermission()), Input: endpoint.JSONBody("AssetInput", a.body, serializers.ModeFull, "Asset input."), Output: a.out,
		Success: []endpoint.Status{{Code: http.StatusCreated, Description: "Created asset."}},
		Handle: func(request *web.Request, _ auth.Principal, input orm.ModelInput[inventory.Asset]) (output.Prepared[inventory.Asset], error) {
			return db.AtomicResult(request.Context(), probe, func(ctx context.Context, session db.Session) (output.Prepared[inventory.Asset], error) {
				mutation, err := a.resource.CreateInSession(ctx, session, input, a.saveOptions(true))
				if err != nil {
					return output.Prepared[inventory.Asset]{}, err
				}
				return a.out.Prepare(ctx, http.StatusCreated, mutation.Value)
			})
		},
	})
	check(t, err)
	operations, schemas, err := endpoint.Collect([]endpoint.Endpoint{create})
	check(t, err)
	document, err := openapi.New(openapi.Config{Title: "Inventory", Version: "1", Authentication: authentication, Operations: operations, Schemas: schemas})
	check(t, err)
	configured, err := settings.New(settings.Definition{ProjectName: "resource_consumer", InstalledApps: []apps.Config{{Name: "example.com/godj-project-bundle/inventory", Label: "inventory"}}})
	check(t, err)
	application, err := web.NewApplication(web.Config{Settings: configured, Routes: document.Routes()})
	check(t, err)
	send := func(token, raw string) (*httptest.ResponseRecorder, int64) {
		body := &bodyProbe{Reader: strings.NewReader(raw)}
		request := httptest.NewRequest(http.MethodPost, "/api/assets/", body)
		request.Header.Set("Content-Type", "application/json")
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response := httptest.NewRecorder()
		application.ServeHTTP(response, request)
		return response, body.reads.Load()
	}
	beforeReads, beforeWrites := probe.reads.Load(), probe.writes.Load()
	unauthorized, read := send("", `{"code":"anonymous"}`)
	if unauthorized.Code != http.StatusUnauthorized || read != 0 || probe.reads.Load() != beforeReads || probe.writes.Load() != beforeWrites {
		t.Fatal("anonymous admission performed body/object I/O", unauthorized.Code, read)
	}
	oldResponse, read := send("old", `{"code":"old-actor"}`)
	if evolved {
		if oldResponse.Code != http.StatusForbidden || read != 0 || probe.reads.Load() != beforeReads || probe.writes.Load() != beforeWrites {
			t.Fatal("changed permission did not reject before I/O", oldResponse.Code, read)
		}
	} else if oldResponse.Code != http.StatusCreated || read == 0 {
		t.Fatal("baseline permission did not admit old actor", oldResponse.Code)
	}
	raw := `{"code":"http-asset","note":"private after change"}`
	if evolved {
		raw = `{"code":"http-asset","note":"private after change","priority":9}`
	}
	response, _ := send("writer", raw)
	if response.Code != http.StatusCreated {
		t.Fatal("typed HTTP create failed", response.Code, response.Body.String())
	}
	var decoded map[string]json.RawMessage
	check(t, json.Unmarshal(response.Body.Bytes(), &decoded))
	var keys []string
	for name := range decoded {
		keys = append(keys, name)
	}
	want := responseFields()
	slices.Sort(keys)
	slices.Sort(want)
	if !slices.Equal(keys, want) || evolved && string(decoded["priority"]) != "9" {
		t.Fatal("response projection differs from policy", keys, want)
	}
	// The model limit applies after trimming. A raw JSON Schema maxLength
	// would incorrectly reject this input even though its stored code fits.
	trimmed, _ := send("writer", `{"code":"    trimmed    "}`)
	if trimmed.Code != http.StatusCreated {
		t.Fatal("normalized model constraint rejected valid raw input", trimmed.Code)
	}
	var normalized struct {
		Code string `json:"code"`
	}
	check(t, json.Unmarshal(trimmed.Body.Bytes(), &normalized))
	if normalized.Code != "trimmed" {
		t.Fatal("HTTP input did not apply model string normalization", normalized.Code)
	}
	beforeWrites = probe.writes.Load()
	invalid, _ := send("writer", `{"code":"too-long-asset-code"}`)
	if invalid.Code != http.StatusBadRequest || probe.writes.Load() != beforeWrites {
		t.Fatal("model-derived length validation reached a write", invalid.Code)
	}
	if evolved {
		invalid, _ := send("writer", `{"code":"bad-choice","priority":2}`)
		if invalid.Code != http.StatusBadRequest || probe.writes.Load() != beforeWrites {
			t.Fatal("added field choices were not applied to HTTP", invalid.Code)
		}
	}
	var wire struct {
		Paths map[string]map[string]struct {
			Permission string `json:"x-godj-permission"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					Type          string            `json:"type"`
					MaxLength     *int              `json:"maxLength"`
					Default       json.RawMessage   `json:"default"`
					Enum          []json.RawMessage `json:"enum"`
					Normalization struct {
						TrimWhitespace     bool `json:"trimWhitespace"`
						AllowEmpty         bool `json:"allowEmptyAfterTrim"`
						MaxLengthAfterTrim int  `json:"maxLengthAfterTrim"`
					} `json:"x-godj-normalization"`
				} `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	check(t, json.Unmarshal(document.Bytes(), &wire))
	if wire.Paths["/api/assets/"]["post"].Permission != string(editPermission()) {
		t.Fatal("OpenAPI permission did not change with actual admission")
	}
	requestProperties := wire.Components.Schemas["AssetInput"].Properties
	code := requestProperties["code"]
	if code.Type != "string" || code.MaxLength != nil || !code.Normalization.TrimWhitespace || code.Normalization.AllowEmpty || code.Normalization.MaxLengthAfterTrim != 12 {
		t.Fatal("request schema does not describe post-normalization model constraints", code)
	}
	_, hasPriority := requestProperties["priority"]
	if hasPriority != evolved || evolved && (requestProperties["priority"].Type != "integer" || string(requestProperties["priority"].Default) != "5" || len(requestProperties["priority"].Enum) != 3) {
		t.Fatal("added field schema/default/choices differ from actual input")
	}
	keys = nil
	for name := range wire.Components.Schemas["Asset"].Properties {
		keys = append(keys, name)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, want) {
		t.Fatal("response schema differs from actual projection", keys, want)
	}
}
