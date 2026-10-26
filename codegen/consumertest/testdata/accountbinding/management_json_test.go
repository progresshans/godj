package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"example.com/godj-project-bundle/project"
	auditmodels "github.com/progresshans/godj/account/audit/models"
	"github.com/progresshans/godj/api"
	apisession "github.com/progresshans/godj/api/sessionauth"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	identityapi "github.com/progresshans/godj/identity/api"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/sessions"
	sessionmodels "github.com/progresshans/godj/sessions/database/models"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/settings/database"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
	websession "github.com/progresshans/godj/web/sessionauth"
)

// These failures surround the real native transaction. The unknown case
// commits before losing its acknowledgement; it is not a network experiment.
type managementJSONBackend struct {
	database.Backend
	writeFault atomic.Value
	readFault  atomic.Bool
}

func (backend *managementJSONBackend) ReadSnapshot(ctx context.Context, read func(db.Queryer) error) error {
	err := backend.Backend.ReadSnapshot(ctx, read)
	if backend.readFault.Swap(false) {
		return errors.Join(err, errors.New("private cleanup failure"))
	}
	return err
}

func (backend *managementJSONBackend) CoordinatedAtomicRelation(ctx context.Context, write func(db.RelationSession) error) error {
	fault, _ := backend.writeFault.Swap("").(string)
	native, supported := backend.Backend.(db.CoordinatedRelationAtomic)
	if !supported {
		return errors.New("native relation transactions are required")
	}
	err := native.CoordinatedAtomicRelation(ctx, func(scope db.RelationSession) error {
		if err := write(scope); err != nil {
			return err
		}
		if fault == "rollback" {
			return errors.New("private failure after native writes")
		}
		return nil
	})
	if err == nil && fault == "unknown" {
		return &query.Error{Code: query.CodeCommitOutcomeUnknown}
	}
	return err
}

// The selected generated owners publish their shared JSON application. The
// consumer supplies no handwritten CRUD callbacks or default account DTO.
func assertAccountManagementJSON[M, G, P any](t *testing.T, backend database.Backend, binding accountmodel.Binding[M], selected *accountmodel.SessionStore[M], hasher auth.PasswordHasher, original M, profileField, groupExtra, permissionExtra string,
	factory func(*accountmodel.SessionStore[M], *accountmodel.Authenticator[M], auth.Authorizer, accountmodel.AccountManagementConfig[M]) (*accountmodel.AccountManagement[M], error),
	catalogFactory func(*accountmodel.SessionStore[M], auth.Authorizer, accountmodel.CatalogConfig[G, P]) (*accountmodel.Catalog[G, P], error)) {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	run := func(name string, test func(*testing.T)) {
		t.Helper()
		if !t.Run(name, test) {
			t.FailNow()
		}
	}
	faults, ok := backend.(*managementJSONBackend)
	if !ok {
		t.Fatal("JSON consumer lacks its native completion probe")
	}
	role, err := binding.Role()
	check(err)
	objects, err := binding.Objects()
	check(err)
	stored, err := objects.ModelValues(original)
	check(err)
	actorLogin, _ := stored[role.Login].String()
	keyField, err := objects.PrimaryKeyField()
	check(err)
	key := orm.NewAutoField[M](keyField)
	actorKey, present, err := objects.PrimaryKey(original)
	check(err)
	actorID, integer := actorKey.Integer()
	if !present || !integer {
		t.Fatal("first account key absent")
	}
	fields := []string{role.Login, profileField, "password"}
	for _, field := range []string{role.Email, role.Active.Field, role.Staff.Field, role.Superuser.Field, role.Groups, role.Permissions} {
		if field != "" && !slices.Contains(fields, field) {
			fields = append(fields, field)
		}
	}
	verifier, err := selected.Authenticator(t.Context(), hasher)
	check(err)
	credential, err := verifier.Authenticate(t.Context(), actorLogin, accountPasswordText)
	check(err)
	actor := credential.Principal()
	overlay := &catalogAdminOverlay{}
	now := time.Now().UTC().Truncate(time.Microsecond)
	clock := func() time.Time { return now }
	owner, err := factory(selected, verifier, overlay, accountmodel.AccountManagementConfig[M]{
		ReadFields: fields, CreateFields: fields, EditFields: fields,
		Permissions:           accountmodel.AccountPermissions{View: "account.view", Add: "account.add", Change: "account.change", Delete: "account.delete", Additional: []auth.Permission{"account.manage"}},
		GroupPermissions:      accountmodel.DeletionPermissions{Delete: "catalog.delete_group", Change: "catalog.change_group"},
		PermissionPermissions: accountmodel.DeletionPermissions{Delete: "catalog.delete_permission", Change: "catalog.change_permission"},
		MaxPasswordBytes:      64, Now: clock,
		ValidateCreate: func(_ context.Context, _ db.Queryer, candidate M, _ accountmodel.RoleMemberships) (validation.Errors, error) {
			values, err := objects.ModelValues(candidate)
			if err != nil {
				return validation.Errors{}, err
			}
			if profile, _ := values["password"].String(); profile == "model-rejected" {
				return validation.NewErrors(validation.New("password", "model_policy")), nil
			}
			return validation.Errors{}, nil
		},
		ValidatePassword: func(_ context.Context, raw string, candidate M) (validation.Errors, error) {
			values, err := objects.ModelValues(candidate)
			if err != nil {
				return validation.Errors{}, err
			}
			if encoded, _ := values[role.Password].String(); encoded != "" {
				return validation.Errors{}, errors.New("password policy received encoded material")
			}
			if raw == "weak" {
				return validation.NewErrors(validation.New("password", "weak")), nil
			}
			return validation.Errors{}, nil
		},
		ValidateEdit: func(_ context.Context, _ db.Queryer, _, candidate M, _ accountmodel.RoleMemberships) (validation.Errors, error) {
			values, err := objects.ModelValues(candidate)
			if err != nil {
				return validation.Errors{}, err
			}
			profile, _ := values[profileField].String()
			if profile == "forbidden" {
				return validation.NewErrors(validation.New(validation.Field(profileField), "policy")), nil
			}
			return validation.Errors{}, nil
		},
	})
	check(err)
	groupFields := []string{role.Catalog.GroupName, role.Catalog.GroupPermissions}
	permissionFields := []string{role.Catalog.PermissionCode, role.Catalog.PermissionName}
	if groupExtra != "" {
		groupFields = append(groupFields, groupExtra)
	}
	if permissionExtra != "" {
		permissionFields = append(permissionFields, permissionExtra)
	}
	catalog, err := catalogFactory(selected, overlay, accountmodel.CatalogConfig[G, P]{
		Group:              accountmodel.CatalogModelConfig[G]{ReadFields: groupFields, WriteFields: groupFields, Permissions: accountmodel.CatalogPermissions{View: "catalog.view_group", Add: "catalog.add_group", Change: "catalog.change_group", Delete: "catalog.delete_group", Additional: []auth.Permission{"catalog.manage"}}},
		Permission:         accountmodel.CatalogModelConfig[P]{ReadFields: permissionFields, WriteFields: permissionFields, Permissions: accountmodel.CatalogPermissions{View: "catalog.view_permission", Add: "catalog.add_permission", Change: "catalog.change_permission", Delete: "catalog.delete_permission", Additional: []auth.Permission{"catalog.manage"}}},
		AccountPermissions: accountmodel.DeletionPermissions{Delete: "account.delete", Change: "account.change"}, Now: clock,
	})
	check(err)
	manager, err := sessions.NewManager(selected, sessions.Config{Clock: clock})
	check(err)
	persistence, err := selected.LoginPersistence(manager)
	check(err)
	runtime, err := websession.New(websession.Config{Sessions: manager, Authenticator: verifier, Authorizer: overlay, LoginPersistence: persistence, Clock: clock,
		SessionCookie: websession.CookieConfig{Path: "/", AllowInsecure: true}, CSRFCookie: websession.CookieConfig{Path: "/", AllowInsecure: true},
		LoginPath: "/login/", FallbackPath: "/api/identity/accounts/", AllowedNextPaths: []string{"/api/identity/accounts/"}})
	check(err)
	authentication, err := apisession.New(runtime, apisession.WithReadOnlyResolution())
	check(err)
	management, err := identityapi.NewManagement(owner, catalog, identityapi.ManagementConfig{Namespace: "selectedapi", Authentication: authentication})
	check(err)
	routes := management.Routes()
	routes = append(routes,
		web.Route{Name: "selectedapi:csrf", Method: "GET", Path: "/login/", Handler: func(request *web.Request) (web.Response, error) {
			token, err := runtime.CSRFToken(request)
			if err != nil {
				return web.Response{}, err
			}
			response, err := web.HTML(200, []byte(token.Value()))
			if err != nil {
				return web.Response{}, err
			}
			return token.Apply(response)
		}},
		web.Route{Name: "selectedapi:login", Method: "POST", Path: "/login/", Handler: func(request *web.Request) (web.Response, error) {
			if err := request.HTTP().ParseForm(); err != nil {
				return web.Response{}, err
			}
			if err := runtime.VerifyCSRF(request, request.HTTP().PostForm["csrfmiddlewaretoken"]); err != nil {
				return web.HTML(403, []byte("CSRF rejected"))
			}
			result, err := runtime.Login(request, request.HTTP().PostForm.Get("username"), request.HTTP().PostForm.Get("password"))
			if err != nil {
				return web.Response{}, err
			}
			response, err := web.NewResponse(204, nil, nil)
			if err != nil {
				return web.Response{}, err
			}
			return result.Apply(response)
		}},
	)
	configured, err := settings.New(settings.Definition{ProjectName: "selected_account_json", InstalledApps: append(project.InstalledApps(), apps.Config{Name: "example.com/godj-project-bundle/consumer", Label: "selectedapi"})})
	check(err)
	application, err := web.NewApplication(web.Config{Settings: configured, Routes: routes, Middleware: management.Middleware()})
	check(err)
	server := httptest.NewServer(application)
	t.Cleanup(server.Close)
	baseURL, err := url.Parse(server.URL)
	check(err)
	newClient := func() *http.Client {
		jar, err := cookiejar.New(nil)
		check(err)
		return &http.Client{Transport: server.Client().Transport, Jar: jar, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	readResponse := func(response *http.Response, err error) (int, http.Header, []byte) {
		t.Helper()
		check(err)
		body, err := io.ReadAll(response.Body)
		check(err)
		check(response.Body.Close())
		return response.StatusCode, response.Header, body
	}
	csrf := func(client *http.Client) string {
		t.Helper()
		request, err := http.NewRequestWithContext(t.Context(), "GET", server.URL+"/login/", nil)
		check(err)
		status, _, body := readResponse(client.Do(request))
		if status != 200 {
			t.Fatal("CSRF token request", status)
		}
		return string(body)
	}
	login := func(client *http.Client, name, password string) sessions.ID {
		t.Helper()
		form := url.Values{"username": {name}, "password": {password}, "csrfmiddlewaretoken": {csrf(client)}}
		request, err := http.NewRequestWithContext(t.Context(), "POST", server.URL+"/login/", strings.NewReader(form.Encode()))
		check(err)
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		status, _, _ := readResponse(client.Do(request))
		if status != 204 {
			t.Fatal("selected account login", status)
		}
		for _, cookie := range client.Jar.Cookies(baseURL) {
			if cookie.Name == websession.DefaultSessionCookieName {
				id, err := sessions.ParseID(cookie.Value)
				check(err)
				return id
			}
		}
		t.Fatal("login did not publish a session")
		return sessions.ID{}
	}
	send := func(client *http.Client, method, path, body, revision string, withCSRF bool) (int, http.Header, []byte) {
		t.Helper()
		token := ""
		if method != "GET" && withCSRF {
			token = csrf(client)
		}
		request, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, strings.NewReader(body))
		check(err)
		request.Header.Set("Content-Type", api.JSONContentType)
		request.Header.Set("Accept", api.JSONContentType)
		if revision != "" {
			request.Header.Set("If-Revision", revision)
		}
		if token != "" {
			request.Header.Set(websession.DefaultCSRFHeader, token)
			request.Header.Set("Origin", server.URL)
		}
		status, header, value := readResponse(client.Do(request))
		if strings.Contains(string(value), "private") {
			t.Fatal("private execution cause reached HTTP")
		}
		return status, header, value
	}
	encode := func(value any) string { raw, err := json.Marshal(value); check(err); return string(raw) }
	fieldsBody := func(fields map[string]any) string { return encode(map[string]any{"fields": fields}) }
	accountFields := func(name string) map[string]any {
		fields := map[string]any{role.Login: name, profileField: "KR", "password": "profile-value"}
		if role.Email != "" && role.Email != role.Login {
			fields[role.Email] = "Contact@EXAMPLE.TEST"
		}
		return fields
	}
	creationBody := func(fields map[string]any, password any) string {
		return encode(map[string]any{"fields": fields, "credential": map[string]any{"password": password}})
	}
	type receipt struct{ ID, Revision int64 }
	decodeReceipt := func(status int, header http.Header, body []byte, want int) receipt {
		t.Helper()
		var result receipt
		check(json.Unmarshal(body, &result))
		if status != want || result.Revision <= 0 || header.Get("Revision") != strconv.FormatInt(result.Revision, 10) || header.Get("Cache-Control") != "no-store" {
			t.Fatal("unconfirmed mutation receipt", status)
		}
		var fields map[string]json.RawMessage
		check(json.Unmarshal(body, &fields))
		if len(fields) != 2 {
			t.Fatal("mutation implicitly exposed a row")
		}
		return result
	}
	path := func(resource string, id int64) string {
		return "/api/identity/" + resource + "/" + strconv.FormatInt(id, 10) + "/"
	}
	client := newClient()
	actorSession := login(client, actorLogin, accountPasswordText)
	type durable struct {
		Accounts         []M
		Groups           []G
		Permissions      []P
		Memberships      []map[string][]int64
		GroupPermissions [][]int64
		Sessions         []sessionmodels.Session
		Audits           []auditmodels.Event
	}
	snapshot := func() durable {
		t.Helper()
		rows, err := objects.Using(backend).OrderBy(key.Asc()).All(t.Context())
		check(err)
		groupKey, err := catalog.Groups().Manager().PrimaryKeyField()
		check(err)
		groups, err := catalog.Groups().Manager().Using(backend).OrderBy(orm.NewAutoField[G](groupKey).Asc()).All(t.Context())
		check(err)
		groupPermissions := make([][]int64, len(groups))
		for index, row := range groups {
			pk, _, err := catalog.Groups().Manager().PrimaryKey(row)
			check(err)
			id, _ := pk.Integer()
			current, err := catalog.Groups().Get(t.Context(), actor, id)
			check(err)
			groupPermissions[index], _ = current.View().Collection(role.Catalog.GroupPermissions)
		}
		permissionKey, err := catalog.Permissions().Manager().PrimaryKeyField()
		check(err)
		permissions, err := catalog.Permissions().Manager().Using(backend).OrderBy(orm.NewAutoField[P](permissionKey).Asc()).All(t.Context())
		check(err)
		members := make([]map[string][]int64, len(rows))
		for index, row := range rows {
			pk, _, err := objects.PrimaryKey(row)
			check(err)
			id, _ := pk.Integer()
			profile, err := owner.Reader().Get(t.Context(), actor, id)
			check(err)
			members[index] = profile.View().Collections()
		}
		s, err := sessionmodels.SessionObjects.Using(backend).OrderBy(sessionmodels.SessionFields.ID.Asc()).All(t.Context())
		check(err)
		a, err := auditmodels.EventObjects.Using(backend).OrderBy(auditmodels.EventFields.ID.Asc()).All(t.Context())
		check(err)
		return durable{rows, groups, permissions, members, groupPermissions, s, a}
	}
	var permission, group, created receipt
	run("catalog_create", func(t *testing.T) {
		fields := map[string]any{role.Catalog.PermissionCode: "api.assigned", role.Catalog.PermissionName: "Assigned"}
		if permissionExtra != "" {
			fields[permissionExtra] = "API description"
		}
		status, header, body := send(client, "POST", "/api/identity/permissions/", fieldsBody(fields), "", true)
		permission = decodeReceipt(status, header, body, 201)
		fields = map[string]any{role.Catalog.GroupName: "API team", role.Catalog.GroupPermissions: []int64{permission.ID}}
		if groupExtra != "" {
			fields[groupExtra] = 7
		}
		status, header, body = send(client, "POST", "/api/identity/groups/", fieldsBody(fields), "", true)
		group = decodeReceipt(status, header, body, 201)
		actual, err := catalog.Groups().Get(t.Context(), actor, group.ID)
		check(err)
		keys, loaded := actual.View().Collection(role.Catalog.GroupPermissions)
		if !loaded || !slices.Equal(keys, []int64{permission.ID}) {
			t.Fatal("catalog JSON lost actual collection")
		}
	})
	run("create_and_login", func(t *testing.T) {
		name := "api@example.test"
		if role.WriteLoginNormalization == ir.AccountUnicode16NFKC {
			name = "ａｐｉ＠ｅｘａｍｐｌｅ．ｔｅｓｔ"
		}
		fields := accountFields(name)
		fields[role.Groups] = []int64{group.ID}
		fields[role.Permissions] = []int64{permission.ID}
		status, header, body := send(client, "POST", "/api/identity/accounts/", creationBody(fields, "created-password"), "", true)
		created = decodeReceipt(status, header, body, 201)
		row, err := objects.Using(backend).Filter(key.Exact(created.ID)).Get(t.Context())
		check(err)
		values, err := objects.ModelValues(row)
		check(err)
		if login, _ := values[role.Login].String(); login != "api@example.test" {
			t.Fatal("API stored a raw normalization alias")
		}
		if profile, _ := values[profileField].String(); profile != "KR" {
			t.Fatal("typed custom profile lost")
		}
		if profile, _ := values["password"].String(); profile != "profile-value" {
			t.Fatal("credential command overwrote the actual password profile field")
		}
		login(newClient(), "api@example.test", "created-password")
	})
	run("selected_read_and_list", func(t *testing.T) {
		status, header, body := send(client, "GET", path("accounts", created.ID), "", "", false)
		var detail struct {
			ID, Revision   int64
			Fields         map[string]json.RawMessage
			PasswordUsable bool `json:"password_usable"`
		}
		check(json.Unmarshal(body, &detail))
		if status != 200 || detail.ID != created.ID || detail.Revision != created.Revision || !detail.PasswordUsable || len(detail.Fields) != len(fields) || header.Get("Revision") != "1" {
			t.Fatal("selected account response differs", status)
		}
		for _, name := range []string{role.Password, role.PrincipalID} {
			if detail.Fields[name] != nil {
				t.Fatal("private selected field exposed")
			}
		}
		status, _, body = send(client, "GET", "/api/identity/accounts/?limit=100", "", "", false)
		var page struct {
			Count int64
			Items []struct{ Fields map[string]json.RawMessage }
		}
		check(json.Unmarshal(body, &page))
		if status != 200 || page.Count != 2 || len(page.Items) != 2 {
			t.Fatal("current page count differs")
		}
		for _, item := range page.Items {
			if item.Fields[role.Groups] != nil || item.Fields[role.Permissions] != nil {
				t.Fatal("list loaded per-row memberships")
			}
		}
	})
	for _, test := range []struct {
		name, body, revision string
		status               int
		csrf                 bool
		deny                 auth.Permission
	}{
		{"csrf", "invalid", "", 403, false, ""},
		{"additional_permission", "invalid", "", 403, true, "account.manage"},
		{"add_requires_change", "invalid", "", 403, true, "account.change"},
		{"password_policy", creationBody(accountFields("weak@example.test"), "weak"), "", 400, true, ""},
		{"model_password_policy", creationBody(func() map[string]any {
			fields := accountFields("model@example.test")
			fields["password"] = "model-rejected"
			return fields
		}(), "created-password"), "", 400, true, ""},
		{"protected_input", creationBody(map[string]any{role.Login: "bad@example.test", profileField: "KR", role.PrincipalID: "forged"}, "created-password"), "", 400, true, ""},
		{"credential_namespace", encode(map[string]any{"fields": accountFields("bad@example.test"), "credential": map[string]any{"password": "secret", role.Revision: 8}}), "", 400, true, ""},
	} {
		run(test.name, func(t *testing.T) {
			before := snapshot()
			overlay.denied.Store(test.deny)
			status, _, body := send(client, "POST", "/api/identity/accounts/", test.body, test.revision, test.csrf)
			overlay.denied.Store(auth.Permission(""))
			if status != test.status || !reflect.DeepEqual(before, snapshot()) {
				t.Fatal("refused creation changed durable state", status)
			}
			if test.name == "password_policy" && !strings.Contains(string(body), `"field":"/credential/password"`) {
				t.Fatal("password policy lost its command namespace")
			}
			if test.name == "model_password_policy" && !strings.Contains(string(body), `"field":"/fields/password"`) {
				t.Fatal("actual model field became a credential error")
			}
		})
	}
	run("revision_before_body", func(t *testing.T) {
		before := snapshot()
		status, _, _ := send(client, "PATCH", path("accounts", created.ID), "invalid", "", true)
		if status != 428 {
			t.Fatal("missing revision parsed a body", status)
		}
		status, _, _ = send(client, "PATCH", path("accounts", created.ID), "invalid", "01", true)
		if status != 400 || !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("invalid precondition changed storage", status)
		}
	})
	run("put_defaults_and_omitted_collections", func(t *testing.T) {
		fields := accountFields("api@example.test")
		delete(fields, "password")
		before := created.Revision
		status, header, body := send(client, "PUT", path("accounts", created.ID), fieldsBody(fields), strconv.FormatInt(before, 10), true)
		created = decodeReceipt(status, header, body, 200)
		if created.Revision != before+1 {
			t.Fatal("PUT omission default did not change the row once")
		}
		row, err := owner.Reader().Get(t.Context(), actor, created.ID)
		check(err)
		value, _ := row.View().Get("password")
		profile, _ := value.String()
		groups, loaded := row.View().Collection(role.Groups)
		permissions, selected := row.View().Collection(role.Permissions)
		if profile != "profile-default" || !loaded || !selected || !slices.Equal(groups, []int64{group.ID}) || !slices.Equal(permissions, []int64{permission.ID}) {
			t.Fatal("PUT lost IR defaults or destroyed omitted collections")
		}
	})
	run("update_noop_and_history", func(t *testing.T) {
		previous := created.Revision
		status, header, body := send(client, "PATCH", path("accounts", created.ID), fieldsBody(map[string]any{profileField: "NZ", role.Groups: []int64{}}), strconv.FormatInt(previous, 10), true)
		created = decodeReceipt(status, header, body, 200)
		if created.Revision != previous+1 {
			t.Fatal("account update did not advance revision once")
		}
		before := snapshot()
		status, header, body = send(client, "PATCH", path("accounts", created.ID), fieldsBody(map[string]any{}), strconv.FormatInt(created.Revision, 10), true)
		if result := decodeReceipt(status, header, body, 200); result.Revision != created.Revision || !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("no-op changed durable state")
		}
		status, _, _ = send(client, "PATCH", path("accounts", created.ID), fieldsBody(map[string]any{profileField: "KR"}), strconv.FormatInt(previous, 10), true)
		if status != 412 || !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("stale API update changed storage", status)
		}
		status, _, body = send(client, "GET", path("accounts", created.ID)+"history/?limit=100", "", "", false)
		var history []map[string]json.RawMessage
		check(json.Unmarshal(body, &history))
		if status != 200 || len(history) != 3 {
			t.Fatal("current-domain history missing", status, len(history))
		}
		for _, entry := range history {
			if len(entry) != 4 || entry["actor_id"] != nil {
				t.Fatal("history widened credential exposure")
			}
		}
	})
	run("catalog_put_and_permissions", func(t *testing.T) {
		fields := map[string]any{role.Catalog.GroupName: "API renamed", role.Catalog.GroupPermissions: []int64{permission.ID}}
		if groupExtra != "" {
			fields[groupExtra] = 0
		}
		status, header, body := send(client, "PUT", path("groups", group.ID), fieldsBody(fields), "1", true)
		group = decodeReceipt(status, header, body, 200)
		if group.Revision != 2 {
			t.Fatal("catalog PUT did not advance")
		}
		before := snapshot()
		overlay.denied.Store(auth.Permission("catalog.manage"))
		status, _, _ = send(client, "PATCH", path("permissions", permission.ID), "invalid", "", true)
		overlay.denied.Store(auth.Permission(""))
		if status != 403 || !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("catalog common permission lost", status)
		}
	})
	run("native_rollback", func(t *testing.T) {
		before := snapshot()
		faults.writeFault.Store("rollback")
		status, _, _ := send(client, "PATCH", path("accounts", created.ID), fieldsBody(map[string]any{profileField: "KR"}), strconv.FormatInt(created.Revision, 10), true)
		if status != 500 || !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("HTTP write failure retained native changes", status)
		}
	})
	run("unknown_after_native_commit", func(t *testing.T) {
		before := snapshot()
		faults.writeFault.Store("unknown")
		status, header, body := send(client, "POST", "/api/identity/accounts/", creationBody(accountFields("unknown@example.test"), nil), "", true)
		after := snapshot()
		if status != 503 || header.Get("Retry-After") != "" || !strings.Contains(string(body), `"code":"outcome_unknown"`) || len(after.Accounts) != len(before.Accounts)+1 || len(after.Audits) != len(before.Audits)+1 {
			t.Fatal("unknown completion was treated as rejection or retried", status)
		}
	})
	run("read_cleanup_failure", func(t *testing.T) {
		before := snapshot()
		faults.readFault.Store(true)
		status, _, _ := send(client, "GET", path("accounts", created.ID), "", "", false)
		if status != 500 || !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("read cleanup published successful data", status)
		}
	})
	run("password_revokes_sessions", func(t *testing.T) {
		a, b := newClient(), newClient()
		first := login(a, "api@example.test", "created-password")
		second := login(b, "api@example.test", "created-password")
		previous := created.Revision
		status, header, body := send(client, "POST", path("accounts", created.ID)+"password/", encode(map[string]any{"credential": map[string]any{"password": nil}}), strconv.FormatInt(previous, 10), true)
		created = decodeReceipt(status, header, body, 200)
		if created.Revision != previous+1 {
			t.Fatal("password receipt revision")
		}
		for _, id := range []sessions.ID{first, second} {
			_, found, err := selected.Load(t.Context(), id)
			check(err)
			if found {
				t.Fatal("password change retained target session")
			}
		}
		status, _, body = send(client, "GET", path("accounts", created.ID), "", "", false)
		var detail map[string]any
		check(json.Unmarshal(body, &detail))
		if status != 200 || detail["password_usable"] != false {
			t.Fatal("password metadata differs from committed credential")
		}
	})
	run("delete_and_audit", func(t *testing.T) {
		status, header, body := send(client, "DELETE", path("accounts", created.ID), "", strconv.FormatInt(created.Revision, 10), true)
		deleted := decodeReceipt(status, header, body, 200)
		if deleted.ID != created.ID {
			t.Fatal("deletion returned another target")
		}
		status, _, _ = send(client, "GET", path("accounts", created.ID), "", "", false)
		if status != 404 {
			t.Fatal("deleted account still visible", status)
		}
		status, header, body = send(client, "DELETE", path("groups", group.ID), "", strconv.FormatInt(group.Revision, 10), true)
		decodeReceipt(status, header, body, 200)
		status, header, body = send(client, "DELETE", path("permissions", permission.ID), "", strconv.FormatInt(permission.Revision, 10), true)
		decodeReceipt(status, header, body, 200)
	})
	run("self_deactivation_keeps_confirmed_receipt", func(t *testing.T) {
		profile, err := owner.Reader().Get(t.Context(), actor, actorID)
		check(err)
		fields := maps.Clone(accountFields(actorLogin))
		fields[role.Active.Field] = false
		status, header, body := send(client, "PATCH", path("accounts", actorID), fieldsBody(fields), strconv.FormatInt(profile.Revision(), 10), true)
		result := decodeReceipt(status, header, body, 200)
		if result.ID != actorID {
			t.Fatal("self-deactivation lost confirmed identity")
		}
		_, found, err := selected.Load(t.Context(), actorSession)
		check(err)
		if found {
			t.Fatal("self-deactivation retained managing session")
		}
		row, err := objects.Using(backend).Filter(key.Exact(actorID)).Get(t.Context())
		check(err)
		values, err := objects.ModelValues(row)
		check(err)
		if active, _ := values[role.Active.Field].Boolean(); active {
			t.Fatal("confirmed self-deactivation did not persist")
		}
	})
}
