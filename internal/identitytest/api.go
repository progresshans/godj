package identitytest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	apisession "github.com/progresshans/godj/api/sessionauth"
	"github.com/progresshans/godj/auth"
	hostmodels "github.com/progresshans/godj/conformance/identityfixture/models"
	"github.com/progresshans/godj/identity"
	identityapi "github.com/progresshans/godj/identity/api"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

func newManagementHTTP(t *testing.T, f *managementFixture, backend identity.ManagementBackend, validators ...identity.PasswordValidator) (*identityHTTP, *identityapi.Application) {
	t.Helper()
	policies := managementHost(t, f.backend)
	var application *identityapi.Application
	h := newIdentityHTTPWithRuntime(t, f.runtime, func(runtime *sessionauth.Runtime) ([]web.Route, []web.Middleware, error) {
		authentication, err := apisession.New(runtime)
		if err != nil {
			return nil, nil, err
		}
		application, err = identityapi.New(identityapi.Config{Namespace: "identityprobe", Backend: backend, PasswordHasher: f.hasher, PasswordValidators: validators, Authorizer: auth.PrincipalAuthorizer{}, Authentication: authentication, Users: policies.AccountsUser, Groups: policies.AccountsGroup, Permissions: policies.AccountsPermission})
		if err != nil {
			return nil, nil, err
		}
		return application.Routes(), application.Middleware(), nil
	})
	h.login(t, h.client, "manager", managementOldPassword, 200)
	return h, application
}

func managementCall(t *testing.T, h *identityHTTP, client *http.Client, method, path, body, revision string, csrf bool, want int) (*http.Response, map[string]json.RawMessage) {
	t.Helper()
	var token string
	if csrf && method != "GET" {
		_, token = h.request(t, client, "GET", "/login/", nil)
	}
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request, err := http.NewRequestWithContext(t.Context(), method, h.server.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if revision != "" {
		request.Header.Set("If-Revision", revision)
	}
	if token != "" {
		request.Header.Set(sessionauth.DefaultCSRFHeader, token)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	if response.StatusCode != want {
		t.Fatalf("%s %s: %d, want %d; response %s", method, path, response.StatusCode, want, data)
	}
	for _, secret := range []string{managementOldPassword, managementNewPassword, "private-user-management", "private-maintenance"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatal("HTTP diagnostics exposed private material")
		}
	}
	if response.StatusCode < 500 && response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("identity response lacks no-store")
	}
	if bytes.Contains(data, []byte(`"encoded_password":`)) || bytes.Contains(data, []byte(`"principal_id":`)) {
		t.Fatal("response exposes a forbidden stored field")
	}
	var decoded map[string]json.RawMessage
	if len(data) > 0 && response.Header.Get("Content-Type") == "application/json" {
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatal(err)
		}
	}
	return response, decoded
}
func apiInteger(t *testing.T, value map[string]json.RawMessage, key string) int64 {
	t.Helper()
	var number int64
	if err := json.Unmarshal(value[key], &number); err != nil {
		t.Fatal(key, err)
	}
	return number
}
func apiIDs(t *testing.T, value map[string]json.RawMessage, key string) []int64 {
	t.Helper()
	var keys []int64
	if err := json.Unmarshal(value[key], &keys); err != nil || keys == nil {
		t.Fatal("missing/nonarray collection", key, err)
	}
	return keys
}
func apiPath(kind string, id int64) string {
	return identityapi.BasePath + kind + "/" + strconv.FormatInt(id, 10) + "/"
}

func RunManagementAPI(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	t.Run("session_lifecycle_and_current_revision", func(t *testing.T) {
		backend, other := open(t)
		f := newManagementFixture(t, backend, 3)
		h, application := newManagementHTTP(t, f, f.runtime)
		_, permission := managementCall(t, h, h.client, "POST", identityapi.BasePath+"permissions/", `{"code":"helpdesk.ticket.change","name":"Change ticket"}`, "", true, 201)
		permissionID := apiInteger(t, permission, "id")
		_, group := managementCall(t, h, h.client, "POST", identityapi.BasePath+"groups/", fmt.Sprintf(`{"name":"  Editors  ","permissions":[%d,%d]}`, permissionID, permissionID), "", true, 201)
		groupID := apiInteger(t, group, "id")
		if string(group["name"]) != `"Editors"` || len(apiIDs(t, group, "permissions")) != 1 {
			t.Fatal("API cleaning/collection normalization")
		}
		response, user := managementCall(t, h, h.client, "POST", identityapi.BasePath+"users/", fmt.Sprintf(`{"username":"Ｆｒｅｄ","password":"  retained password  ","groups":[%d]}`, groupID), "", true, 201)
		userID := apiInteger(t, user, "id")
		if response.Header.Get("Revision") != "1" || string(user["username"]) != `"Fred"` {
			t.Fatal("create identity/version")
		}
		member := h.newClient(t)
		h.login(t, member, "Fred", "  retained password  ", 200)
		h.expect(t, member, "/change/", 200, f.user.EncodedPassword)
		h.login(t, h.newClient(t), "Fred", "retained password", 401)
		for _, kind := range []string{"users", "groups", "permissions"} {
			_, page := managementCall(t, h, h.client, "GET", identityapi.BasePath+kind+"/?limit=1&offset=0", "", "", false, 200)
			var items []map[string]json.RawMessage
			if err := json.Unmarshal(page["items"], &items); err != nil || len(items) != 1 || apiInteger(t, page, "count") < 1 {
				t.Fatal("bounded list", err)
			}
			if _, present := items[0]["groups"]; present {
				t.Fatal("list queried/exposed collections")
			}
		}
		managementCall(t, h, h.client, "PATCH", apiPath("users", userID), `{"first_name":"Edited"}`, "1", true, 200)
		managementCall(t, h, h.client, "PATCH", apiPath("users", userID), `{"first_name":"Lost"}`, "1", true, 412)
		response, user = managementCall(t, h, h.client, "PATCH", apiPath("users", userID), `{}`, "2", true, 200)
		if response.Header.Get("Revision") != "2" || string(user["first_name"]) != `"Edited"` || len(apiIDs(t, user, "groups")) != 1 {
			t.Fatal("no-op/omitted collection")
		}
		managementCall(t, h, h.client, "POST", apiPath("users", userID)+"password/", `{"password":"replacement managed-user password"}`, "2", true, 200)
		h.expect(t, member, "/change/", 403, f.user.EncodedPassword)
		h.login(t, h.newClient(t), "Fred", "  retained password  ", 401)
		h.login(t, member, "Fred", managementNewPassword, 200)
		managementCall(t, h, h.client, "PATCH", apiPath("users", userID), `{"active":false}`, "3", true, 200)
		h.expect(t, member, "/change/", 403, f.user.EncodedPassword)
		managementCall(t, h, h.client, "PATCH", apiPath("users", userID), `{"active":true}`, "4", true, 200)
		h.expect(t, member, "/change/", 403, f.user.EncodedPassword)
		h.login(t, member, "Fred", managementNewPassword, 200)
		// Rename preserves the relation IDs while changing effective authorization.
		managementCall(t, h, h.client, "PATCH", apiPath("permissions", permissionID), `{"code":"helpdesk.ticket.other"}`, "1", true, 200)
		h.expect(t, member, "/change/", 403, f.user.EncodedPassword)
		managementCall(t, h, h.client, "DELETE", apiPath("permissions", permissionID), "", "2", true, 204)
		managementCall(t, h, h.client, "PATCH", apiPath("groups", groupID), `{"name":"Stale"}`, "1", true, 412)
		_, group = managementCall(t, h, h.client, "GET", apiPath("groups", groupID), "", "", false, 200)
		if apiInteger(t, group, "revision") != 2 || len(apiIDs(t, group, "permissions")) != 0 {
			t.Fatal("permission deletion did not advance owner")
		}
		managementCall(t, h, h.client, "DELETE", apiPath("groups", groupID), "", "2", true, 204)
		managementCall(t, h, h.client, "PATCH", apiPath("users", userID), `{"email":"lost@example.test"}`, "5", true, 412)
		_, user = managementCall(t, h, h.client, "GET", apiPath("users", userID), "", "", false, 200)
		if apiInteger(t, user, "revision") != 6 || len(apiIDs(t, user, "groups")) != 0 {
			t.Fatal("group deletion did not advance user")
		}
		reopened, err := systemstate.OpenIdentity(t.Context(), other, systemstate.IdentityRuntimeConfig{PasswordHasher: f.hasher, MaxSessions: 512})
		if err != nil {
			t.Fatal(err)
		}
		credential, err := reopened.Authenticator().Authenticate(t.Context(), "Fred", managementNewPassword)
		if err != nil || !credential.Principal().Authenticated() {
			t.Fatal("reopened API-created identity", err)
		}
		if _, err := reopened.Authenticator().Authenticate(t.Context(), "Fred", "  retained password  "); err == nil {
			t.Fatal("old credential revived")
		}
		document, err := application.OpenAPI()
		if err != nil {
			t.Fatal(err)
		}
		var schema map[string]any
		if err := json.Unmarshal(document.Bytes(), &schema); err != nil {
			t.Fatal(err)
		}
		if len(schema["paths"].(map[string]any)) != 7 || !bytes.Contains(document.Bytes(), []byte(`"x-godj-any-permissions"`)) {
			t.Fatal("management surface/permission description incomplete")
		}
		managementCall(t, h, h.client, "DELETE", apiPath("users", userID), "", "6", true, 204)
		h.login(t, h.newClient(t), "Fred", managementNewPassword, 401)
		audit, err := f.runtime.AuditHistory(t.Context(), "godj_identity.user", userID, 100)
		if err != nil || len(audit) != 6 {
			t.Fatal("API effects/audit mismatch", len(audit), err)
		}
	})
	t.Run("transport_and_input_rejections_preserve_state", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		h, _ := newManagementHTTP(t, f, f.runtime)
		for _, test := range []struct {
			method, path, body, revision string
			csrf                         bool
			status                       int
		}{
			{"PATCH", apiPath("users", f.user.ID), `{"active":false}`, "1", false, 403},
			{"PATCH", apiPath("users", f.user.ID), `{"active":false}`, "", true, 428},
			{"PATCH", apiPath("users", f.user.ID), `{"active":false}`, `W/"1"`, true, 400},
			{"PATCH", apiPath("users", f.user.ID), `{"active":false}`, `"01"`, true, 400},
			{"PATCH", apiPath("users", f.user.ID), `{"active":false}`, `*`, true, 400},
			{"PATCH", apiPath("users", f.user.ID), `{"active":false}`, `"1", "2"`, true, 400},
			{"PATCH", apiPath("users", f.user.ID), `{"encoded_password":"injected"}`, "1", true, 400},
			{"PATCH", apiPath("users", f.user.ID), `{"principal_id":"injected"}`, "1", true, 400},
			{"PATCH", apiPath("users", f.user.ID), `{"revision":200}`, "1", true, 400},
			{"PATCH", apiPath("users", f.user.ID), `{"password":"injected"}`, "1", true, 400},
			{"PATCH", apiPath("users", f.user.ID), `{"date_joined":"2026-01-01T00:00:00Z"}`, "1", true, 400},
			{"PATCH", apiPath("users", f.user.ID), `{"groups":null}`, "1", true, 400},
			{"PATCH", apiPath("users", f.user.ID), `{"groups":[1.0]}`, "1", true, 400},
			{"PATCH", apiPath("users", f.user.ID), `{"groups":[999999]}`, "1", true, 400},
			{"PATCH", apiPath("users", f.user.ID), `{"active":false,"active":true}`, "1", true, 400},
			{"POST", identityapi.BasePath + "users/", `{"username":"copy","password":"x","principal_id":"chosen"}`, "", true, 400},
			{"POST", identityapi.BasePath + "users/", `{"username":"copy"}`, "", true, 400},
			{"DELETE", apiPath("users", f.user.ID), `{}`, "1", true, 400},
			{"GET", identityapi.BasePath + "users/?limit=0", "", "", false, 400},
			{"GET", identityapi.BasePath + "users/?limit=1&limit=2", "", "", false, 400},
			{"GET", identityapi.BasePath + "users/?offset=2147483648", "", "", false, 400},
			{"GET", apiPath("users", f.user.ID) + "?ignored=x", "", "", false, 400},
		} {
			before := catalogSnapshot(t, f)
			managementCall(t, h, h.client, test.method, test.path, test.body, test.revision, test.csrf, test.status)
			if !reflect.DeepEqual(before, catalogSnapshot(t, f)) {
				t.Fatal("rejected HTTP request changed persistent state", test.method, test.path)
			}
		}
		managementCall(t, h, h.newClient(t), "POST", identityapi.BasePath+"users/", `{broken`, "", true, 403)
		managementCall(t, h, h.client, "POST", identityapi.BasePath+"users/", `{"username":"large","password":"`+strings.Repeat("x", 65536)+`"}`, "", true, 413)
		if f.hasher.calls.Load() != 0 {
			t.Fatal("invalid input performed password hash")
		}
	})
	t.Run("host_delete_protection_and_cascade", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		h, _ := newManagementHTTP(t, f, f.runtime)
		note, err := hostmodels.NoteObjects.Create(t.Context(), backend, hostmodels.NewNoteCreate("keep until user deletion", f.user.ID))
		if err != nil {
			t.Fatal(err)
		}
		guard, err := hostmodels.GuardObjects.Create(t.Context(), backend, hostmodels.NewGuardCreate(f.user.ID))
		if err != nil {
			t.Fatal(err)
		}
		before := catalogSnapshot(t, f)
		managementCall(t, h, h.client, "DELETE", apiPath("users", f.user.ID), "", "1", true, 400)
		if !reflect.DeepEqual(before, catalogSnapshot(t, f)) {
			t.Fatal("protected delete mutated host")
		}
		if _, err := hostmodels.GuardObjects.Delete(t.Context(), backend, &guard); err != nil {
			t.Fatal(err)
		}
		managementCall(t, h, h.client, "DELETE", apiPath("users", f.user.ID), "", "1", true, 204)
		exists, err := hostmodels.NoteObjects.Using(backend).Filter(hostmodels.NoteFields.ID.Exact(note.ID)).Exists(t.Context())
		if err != nil || exists {
			t.Fatal("host cascade was bypassed", err)
		}
	})
	runManagementAPIPermissions(t, open)
	runManagementAPIHeaders(t, open)
	runManagementAPIFailures(t, open)
}
