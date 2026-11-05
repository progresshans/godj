package consumer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/progresshans/godj/admin"
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
	sessiondb "github.com/progresshans/godj/sessions/database"
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
	writeFault  atomic.Value
	readFault   atomic.Bool
	writes      atomic.Int64
	auditFaults atomic.Int64
}

func (backend *managementJSONBackend) ReadSnapshot(ctx context.Context, read func(db.Queryer) error) error {
	err := backend.Backend.ReadSnapshot(ctx, read)
	if backend.readFault.Swap(false) {
		return errors.Join(err, errors.New("private cleanup failure"))
	}
	return err
}

func (backend *managementJSONBackend) CoordinatedAtomicRelation(ctx context.Context, write func(db.RelationSession) error) error {
	backend.writes.Add(1)
	fault, _ := backend.writeFault.Swap("").(string)
	native, supported := backend.Backend.(db.CoordinatedRelationAtomic)
	if !supported {
		return errors.New("native relation transactions are required")
	}
	err := native.CoordinatedAtomicRelation(ctx, func(scope db.RelationSession) error {
		if fault == "audit_failure" {
			conflict, ok := scope.(db.ConflictInserter)
			validator, valid := scope.(db.SessionValidator)
			if !ok || !valid {
				return errors.New("native audit probe lacks required capabilities")
			}
			scope = &managementJSONAuditScope{RelationSession: scope, ConflictInserter: conflict, SessionValidator: validator, failures: &backend.auditFaults}
		}
		if err := write(scope); err != nil {
			return err
		}
		if fault == "unknown_rollback" {
			return &query.Error{Code: query.CodeCommitOutcomeUnknown}
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

type managementJSONAuditScope struct {
	db.RelationSession
	db.ConflictInserter
	db.SessionValidator
	failures *atomic.Int64
}

func (scope *managementJSONAuditScope) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	key, err := scope.RelationSession.Insert(ctx, plan)
	if err == nil && plan.Table() == "godj_account_audit_event" {
		scope.failures.Add(1)
		return 0, errors.New("private failure after audit insert")
	}
	return key, err
}

type managementJSONHasher struct {
	auth.PasswordHasher
	calls atomic.Int64
}

func (hasher *managementJSONHasher) Hash(ctx context.Context, raw string) (string, error) {
	hasher.calls.Add(1)
	return hasher.PasswordHasher.Hash(ctx, raw)
}

// Observe full native rows independently of HTTP output, then remove just the
// target so callers can compare every unrelated durable value and relation.
func managementJSONWithout[M any](t *testing.T, manager orm.Manager[M], rows []M, target int64) ([]M, map[string]query.Value, int) {
	t.Helper()
	for index, row := range rows {
		pk, present, err := manager.PrimaryKey(row)
		if err != nil || !present {
			t.Fatal("native target key missing", err)
		}
		key, integer := pk.Integer()
		if !integer {
			t.Fatal("native target key is not an integer")
		}
		if key == target {
			values, err := manager.ModelValues(row)
			if err != nil {
				t.Fatal(err)
			}
			return slices.Delete(slices.Clone(rows), index, index+1), values, index
		}
	}
	return rows, nil, -1
}

// The selected generated owners publish their shared JSON application. The
// consumer supplies no handwritten CRUD callbacks or default account DTO.
func assertAccountManagementJSON[M, G, P any](t *testing.T, backend database.Backend, binding accountmodel.Binding[M], hasher auth.PasswordHasher, original M, profileField, groupExtra, permissionExtra string,
	factory func(context.Context, sessiondb.Backend, auth.PasswordHasher, auth.Authorizer, accountmodel.RuntimeConfig[M, G, P]) (*accountmodel.Runtime[M, G, P], error)) {
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
	overlay := &catalogAdminOverlay{}
	now := time.Now().UTC().Truncate(time.Microsecond)
	clock := func() time.Time { return now }
	accountPolicy := accountmodel.AccountManagementConfig[M]{
		ReadFields: fields, CreateFields: fields, EditFields: fields,
		Permissions:           accountmodel.AccountPermissions{View: "account.view", Add: "account.add", Change: "account.change", Delete: "account.delete", Additional: []auth.Permission{"account.manage"}},
		GroupPermissions:      accountmodel.DeletionPermissions{Delete: "catalog.delete_group", Change: "catalog.change_group", Additional: []auth.Permission{"catalog.manage"}},
		PermissionPermissions: accountmodel.DeletionPermissions{Delete: "catalog.delete_permission", Change: "catalog.change_permission", Additional: []auth.Permission{"catalog.manage"}},
		MaxPasswordBytes:      64, Now: clock,
		ValidateCreate: func(_ context.Context, _ db.Queryer, candidate M, _ accountmodel.AccountMemberships) (validation.Errors, error) {
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
		ValidateEdit: func(_ context.Context, _ db.Queryer, _, candidate M, _ accountmodel.AccountMemberships) (validation.Errors, error) {
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
	}
	groupFields := []string{role.Catalog.GroupName, role.Catalog.GroupPermissions}
	permissionFields := []string{role.Catalog.PermissionCode, role.Catalog.PermissionName}
	if groupExtra != "" {
		groupFields = append(groupFields, groupExtra)
	}
	if permissionExtra != "" {
		permissionFields = append(permissionFields, permissionExtra)
	}
	catalogPolicy := accountmodel.CatalogConfig[G, P]{
		Group:              accountmodel.CatalogModelConfig[G]{ReadFields: groupFields, WriteFields: groupFields, Permissions: accountmodel.CatalogPermissions{View: "catalog.view_group", Add: "catalog.add_group", Change: "catalog.change_group", Delete: "catalog.delete_group", Additional: []auth.Permission{"catalog.manage"}}},
		Permission:         accountmodel.CatalogModelConfig[P]{ReadFields: permissionFields, WriteFields: permissionFields, Permissions: accountmodel.CatalogPermissions{View: "catalog.view_permission", Add: "catalog.add_permission", Change: "catalog.change_permission", Delete: "catalog.delete_permission", Additional: []auth.Permission{"catalog.manage"}}},
		AccountPermissions: accountmodel.DeletionPermissions{Delete: "account.delete", Change: "account.change", Additional: []auth.Permission{"account.manage"}}, Now: clock,
	}
	trackedHasher := &managementJSONHasher{PasswordHasher: hasher}
	composed, err := factory(t.Context(), backend, trackedHasher, overlay, accountmodel.RuntimeConfig[M, G, P]{
		Accounts: accountPolicy, Catalog: catalogPolicy, Sessions: sessions.Config{Clock: clock},
	})
	check(err)
	owner, catalog := composed.Accounts(), composed.Catalog()
	selected, manager, verifier := composed.SessionStore(), composed.Sessions(), composed.Authenticator()
	persistence := composed.LoginPersistence()
	credential, err := verifier.Authenticate(t.Context(), actorLogin, accountPasswordText)
	check(err)
	actor := credential.Principal()
	run("composed_runtime", func(t *testing.T) {
		inspection, err := composed.InspectStartup(t.Context())
		check(err)
		fingerprint, err := binding.RoleFingerprint()
		check(err)
		if !inspection.Ready() || inspection.Fresh() || inspection.Ownership().RoleFingerprint() != fingerprint ||
			manager.Store() != selected || persistence.Sessions() != manager || composed.PasswordChangePersistence().Sessions() != manager {
			t.Fatal("generated runtime mixed source, sessions or startup state")
		}
	})
	run("typed_default_password_policy", func(t *testing.T) {
		policy, err := binding.DefaultPasswordValidator(accountmodel.PasswordAttribute{Field: role.Login, Label: "login"},
			accountmodel.PasswordAttribute{Field: "password", Label: "profile label"})
		check(err)
		for _, raw := range []string{actorLogin, "password", "12345678"} {
			diagnostics, err := policy(t.Context(), raw, original)
			check(err)
			if diagnostics.Empty() {
				t.Fatal("selected typed policy omitted a strength check")
			}
		}
		if invalid, err := binding.DefaultPasswordValidator(accountmodel.PasswordAttribute{Field: role.Password, Label: "secret"}); err == nil || invalid != nil {
			t.Fatal("encoded credential was admitted as a public attribute")
		}
		current, err := objects.ModelValues(original)
		check(err)
		if !reflect.DeepEqual(current, stored) {
			t.Fatal("typed policy changed the actual model")
		}
	})
	runtime, err := websession.New(websession.Config{Sessions: manager, Authenticator: verifier, Authorizer: overlay, LoginPersistence: persistence, PasswordChangePersistence: composed.PasswordChangePersistence(), Clock: clock,
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
	run("shared_reset_policy", func(t *testing.T) {
		keys, err := auth.NewPasswordResetKeyRing(bytes.Repeat([]byte{37}, 32))
		check(err)
		reset, err := composed.PasswordResetter(accountmodel.RuntimeResetConfig{Keys: keys, Clock: clock})
		check(err)
		if reset.Sessions() != manager || reset.MaxPasswordBytes() != owner.Passwords().MaxPasswordBytes() {
			t.Fatal("reset selected another session or password envelope")
		}
		row, err := objects.Using(backend).Filter(key.Exact(created.ID)).Get(t.Context())
		check(err)
		values, err := objects.ModelValues(row)
		check(err)
		principal, _ := values[role.PrincipalID].String()
		token, err := reset.IssueToken(t.Context(), principal)
		check(err)
		proof, err := reset.StartPasswordReset(t.Context(), sessions.ID{}, principal, token.Encoded())
		check(err)
		defer func() {
			if err := selected.Delete(t.Context(), proof.ID()); err != nil {
				t.Error(err)
			}
		}()
		before := snapshot()
		weak := "weak"
		diagnostics, rejected := validation.Rejected(reset.CheckPasswordReset(t.Context(), proof.ID(), principal, &weak))
		if !rejected || diagnostics.ByField("password").Empty() || diagnostics.ByField("password").All()[0].Code() != "weak" {
			t.Fatal("reset did not use the same typed account policy")
		}
		strong := "strong-reset-password"
		check(reset.CheckPasswordReset(t.Context(), proof.ID(), principal, &strong))
		if !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("reset admission changed account, sessions or audit")
		}
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
	// Preserve the old native HTTP failure matrix on the selected owners and
	// renamed generated models. Every mode reaches one real write transaction;
	// acknowledgement-loss cases never authorize a blind retry.
	failureIndex := 0
	for _, resource := range []string{"accounts", "groups", "permissions"} {
		operations := []string{"create", "update", "delete"}
		if resource == "accounts" {
			operations = append(operations, "password")
		}
		for _, operation := range operations {
			for _, mode := range []string{"audit_failure", "unknown_rollback", "unknown"} {
				run("failure_"+resource+"_"+operation+"_"+mode, func(t *testing.T) {
					failureIndex++
					name := fmt.Sprintf("fault%d@example.test", failureIndex)
					fields, changedField, revisionField, model := accountFields(name), profileField, role.Revision, role.Model
					if resource == "groups" {
						fields = map[string]any{role.Catalog.GroupName: name}
						if groupExtra != "" {
							fields[groupExtra] = 7
						}
						changedField, revisionField, model = role.Catalog.GroupName, role.Catalog.GroupRevision, role.Catalog.Group
					} else if resource == "permissions" {
						fields = map[string]any{role.Catalog.PermissionCode: fmt.Sprintf("jsonfault.p%d", failureIndex), role.Catalog.PermissionName: name}
						if permissionExtra != "" {
							fields[permissionExtra] = "failure fixture"
						}
						changedField, revisionField, model = role.Catalog.PermissionName, role.Catalog.PermissionRevision, role.Catalog.Permission
					}
					body := fieldsBody(fields)
					if resource == "accounts" {
						body = creationBody(fields, "native-fault-password")
					}
					method, endpoint, revision := "POST", "/api/identity/"+resource+"/", ""
					var target receipt
					var targetSession sessions.ID
					if operation != "create" {
						status, header, value := send(client, method, endpoint, body, "", true)
						target = decodeReceipt(status, header, value, 201)
						if resource == "accounts" {
							targetSession = login(newClient(), name, "native-fault-password")
						}
						method, endpoint, revision = "PATCH", path(resource, target.ID), strconv.FormatInt(target.Revision, 10)
						changed := map[string]any{changedField: "NZ"}
						if resource == "accounts" {
							changed[role.Active.Field] = false
						}
						body = fieldsBody(changed)
						if operation == "delete" {
							method, body = "DELETE", ""
						}
						if operation == "password" {
							method, endpoint = "POST", endpoint+"password/"
							body = encode(map[string]any{"credential": map[string]any{"password": "native-replacement-password"}})
						}
					}
					before := snapshot()
					writes, hashes, auditFailures := faults.writes.Load(), trackedHasher.calls.Load(), faults.auditFaults.Load()
					faults.writeFault.Store(mode)
					status, header, value := send(client, method, endpoint, body, revision, true)
					wantStatus, wantHashes := 503, int64(0)
					if mode == "audit_failure" {
						wantStatus = 500
					}
					if resource == "accounts" && (operation == "create" || operation == "password") {
						wantHashes = 1
					}
					if status != wantStatus || faults.writes.Load()-writes != 1 || trackedHasher.calls.Load()-hashes != wantHashes || header.Get("Revision") != "" || header.Get("Retry-After") != "" || header.Get("Cache-Control") != "no-store" {
						t.Fatal("failure retried, missed native work or published a receipt", status, faults.writes.Load()-writes, trackedHasher.calls.Load()-hashes)
					}
					if (faults.auditFaults.Load()-auditFailures == 1) != (mode == "audit_failure") {
						t.Fatal("audit failure was not reached exactly once")
					}
					if mode == "audit_failure" {
						if string(value) != "Internal Server Error\n" {
							t.Fatal("execution failure exposed diagnostics or partial result")
						}
					} else {
						var failure map[string]json.RawMessage
						check(json.Unmarshal(value, &failure))
						if failure["id"] != nil || failure["revision"] != nil || string(failure["code"]) != `"outcome_unknown"` {
							t.Fatal("failure published target metadata or lost uncertainty")
						}
					}
					after := snapshot()
					if mode != "unknown" {
						if !reflect.DeepEqual(before, after) {
							t.Fatal("confirmed native rollback retained rows, memberships, sessions or audit")
						}
						return
					}
					if len(after.Audits) != len(before.Audits)+1 {
						t.Fatal("committed unknown result lost or duplicated its audit")
					}
					event := after.Audits[len(before.Audits)]
					action := admin.ActionChange
					if operation == "create" {
						action, target.ID = admin.ActionAdd, event.ObjectID
					}
					if operation == "delete" {
						action = admin.ActionDelete
					}
					if event.ObjectID != target.ID || event.Model != model.AppLabel+"."+model.ModelName || event.ActorID != actor.ID() || event.Action != string(action) {
						t.Fatal("unknown result audit has another target, actor or action")
					}
					after.Audits = after.Audits[:len(before.Audits)]
					var oldValues, newValues map[string]query.Value
					oldIndex, newIndex := -1, -1
					switch resource {
					case "accounts":
						before.Accounts, oldValues, oldIndex = managementJSONWithout(t, objects, before.Accounts, target.ID)
						after.Accounts, newValues, newIndex = managementJSONWithout(t, objects, after.Accounts, target.ID)
						for _, item := range []struct {
							state *durable
							index int
						}{{&before, oldIndex}, {&after, newIndex}} {
							if item.index >= 0 {
								for _, keys := range item.state.Memberships[item.index] {
									if len(keys) != 0 {
										t.Fatal("unexpected target membership")
									}
								}
								item.state.Memberships = slices.Delete(slices.Clone(item.state.Memberships), item.index, item.index+1)
							}
						}
					case "groups":
						before.Groups, oldValues, oldIndex = managementJSONWithout(t, catalog.Groups().Manager(), before.Groups, target.ID)
						after.Groups, newValues, newIndex = managementJSONWithout(t, catalog.Groups().Manager(), after.Groups, target.ID)
						for _, item := range []struct {
							state *durable
							index int
						}{{&before, oldIndex}, {&after, newIndex}} {
							if item.index >= 0 {
								if len(item.state.GroupPermissions[item.index]) != 0 {
									t.Fatal("unexpected target group permission")
								}
								item.state.GroupPermissions = slices.Delete(slices.Clone(item.state.GroupPermissions), item.index, item.index+1)
							}
						}
					case "permissions":
						before.Permissions, oldValues, _ = managementJSONWithout(t, catalog.Permissions().Manager(), before.Permissions, target.ID)
						after.Permissions, newValues, _ = managementJSONWithout(t, catalog.Permissions().Manager(), after.Permissions, target.ID)
					}
					if (oldValues == nil) != (operation == "create") || (newValues == nil) != (operation == "delete") {
						t.Fatal("unknown result has wrong durable existence")
					}
					if operation == "update" || operation == "password" {
						expected := maps.Clone(oldValues)
						expected[revisionField] = query.Integer(target.Revision + 1)
						if operation == "update" {
							expected[changedField] = query.String("NZ")
							if resource == "accounts" {
								expected[role.Active.Field] = query.Boolean(false)
							}
						} else {
							encoded, _ := newValues[role.Password].String()
							valid, err := hasher.Verify(t.Context(), "native-replacement-password", encoded)
							check(err)
							if !valid || reflect.DeepEqual(newValues[role.Password], oldValues[role.Password]) {
								t.Fatal("unknown password result lost replacement")
							}
							expected[role.Password] = newValues[role.Password]
						}
						if !reflect.DeepEqual(expected, newValues) {
							t.Fatal("unknown update lost requested values or changed unrelated fields")
						}
					}
					if operation == "create" {
						actualRevision, valid := newValues[revisionField].Integer()
						if !valid || actualRevision != 1 {
							t.Fatal("unknown create lost initial revision")
						}
						for field, expected := range fields {
							switch expected := expected.(type) {
							case string:
								if resource == "accounts" && field == role.Email && role.Email != role.Login {
									expected = "Contact@example.test"
								}
								actual, valid := newValues[field].String()
								if !valid || actual != expected {
									t.Fatal("unknown create lost submitted field", field)
								}
							case int:
								actual, valid := newValues[field].Integer()
								if !valid || actual != int64(expected) {
									t.Fatal("unknown create lost submitted integer", field)
								}
							default:
								t.Fatal("missing independent field observation", field)
							}
						}
						if resource == "accounts" {
							encoded, _ := newValues[role.Password].String()
							valid, err := hasher.Verify(t.Context(), "native-fault-password", encoded)
							check(err)
							if !valid {
								t.Fatal("unknown create lost requested credential")
							}
						}
					}
					if resource == "accounts" && operation != "create" {
						_, found, err := selected.Load(t.Context(), targetSession)
						check(err)
						if found || len(after.Sessions) != len(before.Sessions)-1 {
							t.Fatal("unknown account mutation did not revoke only the target session")
						}
						prior := make(map[int64]sessionmodels.Session, len(before.Sessions))
						for _, row := range before.Sessions {
							prior[row.ID] = row
						}
						for _, row := range after.Sessions {
							if !reflect.DeepEqual(prior[row.ID], row) {
								t.Fatal("unknown mutation changed an unrelated session")
							}
						}
						before.Sessions = after.Sessions
					}
					if !reflect.DeepEqual(before, after) {
						t.Fatal("unknown mutation changed unrelated rows, memberships, sessions or audit")
					}
				})
			}
		}
	}
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
