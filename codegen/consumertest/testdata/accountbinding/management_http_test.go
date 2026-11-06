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
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"example.com/godj-project-bundle/project"
	accountaudit "github.com/progresshans/godj/account/audit"
	auditmodels "github.com/progresshans/godj/account/audit/models"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/api"
	apisession "github.com/progresshans/godj/api/sessionauth"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	identityadmin "github.com/progresshans/godj/identity/admin"
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
type managementHTTPBackend struct {
	database.Backend
	beforeWrite     atomic.Pointer[managementHTTPWriteHook]
	beforeHistory   atomic.Pointer[managementHTTPHistoryHook]
	writeFault      atomic.Value
	readFault       atomic.Bool
	writes          atomic.Int64
	auditFaults     atomic.Int64
	protectedFaults atomic.Int64
	readFailures    atomic.Int64
}

type managementHTTPWriteHook struct{ before func(context.Context) error }
type managementHTTPHistoryHook struct {
	before func(context.Context, db.Queryer) error
}

func (backend *managementHTTPBackend) ReadSnapshot(ctx context.Context, read func(db.Queryer) error) error {
	err := backend.Backend.ReadSnapshot(ctx, func(reader db.Queryer) error {
		if backend.beforeHistory.Load() == nil {
			return read(reader)
		}
		validator, ok := reader.(db.SessionValidator)
		if !ok {
			return errors.New("native history probe lost its borrowed scope")
		}
		return read(&managementHTTPHistoryReader{Queryer: reader, SessionValidator: validator, backend: backend})
	})
	if backend.readFault.Swap(false) {
		backend.readFailures.Add(1)
		return errors.Join(err, errors.New("private cleanup failure"))
	}
	return err
}

type managementHTTPHistoryReader struct {
	db.Queryer
	db.SessionValidator
	backend *managementHTTPBackend
}

func (reader *managementHTTPHistoryReader) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	if plan.Table() == "godj_account_audit_event" {
		if hook := reader.backend.beforeHistory.Swap(nil); hook != nil {
			if err := hook.before(ctx, reader.Queryer); err != nil {
				return nil, err
			}
		}
	}
	return reader.Queryer.Query(ctx, plan)
}

func (backend *managementHTTPBackend) CoordinatedAtomic(ctx context.Context, write func(db.Session) error) error {
	return backend.coordinated(ctx, write, backend.Backend.CoordinatedAtomic)
}

func (backend *managementHTTPBackend) CoordinatedAtomicRelation(ctx context.Context, write func(db.RelationSession) error) error {
	return backend.coordinated(ctx, func(scope db.Session) error {
		relation, ok := scope.(db.RelationSession)
		if !ok {
			return errors.New("native completion probe lost relation scope")
		}
		return write(relation)
	}, func(ctx context.Context, call func(db.Session) error) error {
		return backend.Backend.CoordinatedAtomicRelation(ctx, func(scope db.RelationSession) error { return call(scope) })
	})
}

func (backend *managementHTTPBackend) coordinated(ctx context.Context, write func(db.Session) error, invoke func(context.Context, func(db.Session) error) error) error {
	backend.writes.Add(1)
	if hook := backend.beforeWrite.Swap(nil); hook != nil {
		if err := hook.before(ctx); err != nil {
			return err
		}
	}
	fault, _ := backend.writeFault.Swap("").(string)
	if fault == "cancel" {
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		return invoke(canceled, write)
	}
	err := invoke(ctx, func(scope db.Session) error {
		if fault == "audit_failure" {
			validator, valid := scope.(db.SessionValidator)
			if !valid {
				return errors.New("native audit probe lacks its scope validator")
			}
			if relation, ok := scope.(db.RelationSession); ok {
				conflict, valid := scope.(db.ConflictInserter)
				if !valid {
					return errors.New("native relation audit probe lacks conflict insertion")
				}
				scope = &managementHTTPAuditScope{RelationSession: relation, ConflictInserter: conflict, SessionValidator: validator, failures: &backend.auditFaults}
			} else {
				scope = &managementHTTPAtomicAuditScope{Session: scope, SessionValidator: validator, failures: &backend.auditFaults}
			}
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
	if fault == "rejected_cleanup" {
		var protected *query.ProtectedForeignKeyError
		if errors.As(err, &protected) {
			backend.protectedFaults.Add(1)
		}
		return errors.Join(err, errors.New("private rollback cleanup failure"))
	}
	if err == nil && fault == "unknown" {
		return &query.Error{Code: query.CodeCommitOutcomeUnknown}
	}
	return err
}

type managementHTTPAtomicAuditScope struct {
	db.Session
	db.SessionValidator
	failures *atomic.Int64
}

func (scope *managementHTTPAtomicAuditScope) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	key, err := scope.Session.Insert(ctx, plan)
	if err == nil && plan.Table() == "godj_account_audit_event" {
		scope.failures.Add(1)
		return 0, errors.New("private failure after audit insert")
	}
	return key, err
}

type managementHTTPAuditScope struct {
	db.RelationSession
	db.ConflictInserter
	db.SessionValidator
	failures *atomic.Int64
}

func (scope *managementHTTPAuditScope) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	key, err := scope.RelationSession.Insert(ctx, plan)
	if err == nil && plan.Table() == "godj_account_audit_event" {
		scope.failures.Add(1)
		return 0, errors.New("private failure after audit insert")
	}
	return key, err
}

type managementHTTPHasher struct {
	auth.PasswordHasher
	calls atomic.Int64
	fault atomic.Value
}

func (hasher *managementHTTPHasher) Hash(ctx context.Context, raw string) (string, error) {
	hasher.calls.Add(1)
	mode, _ := hasher.fault.Load().(string)
	switch mode {
	case "invalid_input":
		return "", &auth.Error{Code: auth.CodeInvalidInput, Field: "password"}
	case "wrapped_input":
		return "", fmt.Errorf("private hasher failure: %w", &auth.Error{Code: auth.CodeInvalidInput, Field: "password"})
	case "failure":
		return "", errors.New("private hasher failure")
	}
	return hasher.PasswordHasher.Hash(ctx, raw)
}

// Arm a cleanup failure only after HTTP authentication and admission. This
// separates the management owner's snapshot from session resolution snapshots.
type managementHTTPOverlay struct {
	catalogAdminOverlay
	backend                 *managementHTTPBackend
	failReadAfterAdmission  atomic.Bool
	writeAfterAdmission     atomic.Value
	writeHookAfterAdmission atomic.Pointer[managementHTTPWriteHook]
	writeStart              atomic.Int64
	writeArmed              atomic.Int64
}

func (overlay *managementHTTPOverlay) Allowed(ctx context.Context, actor auth.Principal, permission auth.Permission) (bool, error) {
	allowed, err := overlay.catalogAdminOverlay.Allowed(ctx, actor, permission)
	if err == nil && allowed && overlay.failReadAfterAdmission.Swap(false) {
		overlay.backend.readFault.Store(true)
	}
	if err == nil && allowed {
		if hook := overlay.writeHookAfterAdmission.Swap(nil); hook != nil {
			overlay.writeStart.Store(overlay.backend.writes.Load())
			overlay.backend.beforeWrite.Store(hook)
			overlay.writeArmed.Add(1)
		}
		if fault, _ := overlay.writeAfterAdmission.Swap("").(string); fault != "" {
			// Admin's normal session access has its own transaction. Arm the
			// lifecycle failure after that access, before the final owner fence.
			overlay.writeStart.Store(overlay.backend.writes.Load())
			overlay.backend.writeFault.Store(fault)
			overlay.writeArmed.Add(1)
		}
	}
	return allowed, err
}

// Observe full native rows independently of HTTP output, then remove just the
// target so callers can compare every unrelated durable value and relation.
func managementHTTPWithout[M any](t *testing.T, manager orm.Manager[M], rows []M, target int64) ([]M, map[string]query.Value, int) {
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

// Seed a preexisting large row revision through the same typed Query AST used
// by application bulk updates. This fixture bypasses the management API, which
// must never expose revision as writable request data.
func managementHTTPSetRevision[M any](t *testing.T, backend database.Backend, manager orm.Manager[M], id int64, field string, revision int64) {
	t.Helper()
	metadata, err := manager.Metadata()
	if err != nil {
		t.Fatal(err)
	}
	var selected ir.Field
	for _, candidate := range metadata.Fields {
		if candidate.Name == field {
			selected = candidate
		}
	}
	key, err := manager.PrimaryKeyField()
	if err != nil {
		t.Fatal(err)
	}
	count, err := manager.Using(backend).Filter(orm.NewAutoField[M](key).Exact(id)).Update(t.Context(), orm.Assign(orm.NewIntegerField[M](selected), revision))
	if err != nil || count != 1 {
		t.Fatal("large native revision fixture failed", count, err)
	}
}

// The selected generated owners publish JSON and Admin on the same native
// state. Their failure checks share independent row/session/audit observations;
// each transport still uses its real authentication, parsing and response path.
func assertAccountManagementHTTP[M, G, P any](t *testing.T, backend database.Backend, backendConfig database.Config, binding accountmodel.Binding[M], hasher auth.PasswordHasher, original M, profileField, groupExtra, permissionExtra string,
	factory func(context.Context, sessiondb.Backend, auth.PasswordHasher, auth.Authorizer, accountmodel.RuntimeConfig[M, G, P]) (*accountmodel.Runtime[M, G, P], error), protect ...func(*testing.T, int64) func()) {
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
	faults, ok := backend.(*managementHTTPBackend)
	if !ok {
		t.Fatal("HTTP consumer lacks its native completion probe")
	}
	role, err := binding.Role()
	check(err)
	objects, err := binding.Objects()
	check(err)
	stored, err := objects.ModelValues(original)
	check(err)
	actorLogin, _ := stored[role.Login].String()
	statusName := "password_usable"
	if value, present := stored["password_usable"]; present {
		if actual, valid := value.String(); !valid || actual != "profile-status" {
			t.Fatal("missing real status-name profile fixture")
		}
		statusName = "password_usable_status1"
	}
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
	overlay := &managementHTTPOverlay{backend: faults}
	now := time.Now().UTC().Truncate(time.Microsecond)
	var elapsed atomic.Int64
	clock := func() time.Time { return now.Add(time.Duration(elapsed.Load())) }
	readFields := slices.Clone(fields)
	if role.LastLogin != "" {
		readFields = append(readFields, role.LastLogin)
	}
	var blockedPasswordPolicy atomic.Bool
	var passwordPolicies atomic.Int64
	accountPolicy := accountmodel.AccountManagementConfig[M]{
		ReadFields: readFields, CreateFields: fields, EditFields: fields,
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
			passwordPolicies.Add(1)
			values, err := objects.ModelValues(candidate)
			if err != nil {
				return validation.Errors{}, err
			}
			if encoded, _ := values[role.Password].String(); encoded != "" {
				return validation.Errors{}, errors.New("password policy received encoded material")
			}
			if blockedPasswordPolicy.Load() {
				return validation.NewErrors(validation.New("password", "blocked_by_host")), nil
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
	trackedHasher := &managementHTTPHasher{PasswordHasher: hasher}
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
	configured, err := settings.New(settings.Definition{ProjectName: "selected_account_http", InstalledApps: append(project.InstalledApps(), apps.Config{Name: "example.com/godj-project-bundle/consumer", Label: "selectedapi"})})
	check(err)
	adminBuilder := admin.NewBuilder(configured.Apps())
	check(identityadmin.RegisterAccount(adminBuilder, owner, identityadmin.AccountRegistration{Slug: "accounts"}))
	check(identityadmin.RegisterCatalog(adminBuilder, catalog, identityadmin.CatalogRegistration{
		Groups: identityadmin.CatalogPresentation{Slug: "groups"}, Permissions: identityadmin.CatalogPresentation{Slug: "permissions"},
	}))
	adminRegistry, err := adminBuilder.Build()
	check(err)
	adminPaths, err := admin.SiteAllowedNextPaths(adminRegistry, "/admin")
	check(err)
	adminRuntime, err := websession.New(websession.Config{Sessions: manager, Authenticator: verifier, Authorizer: overlay, LoginPersistence: persistence, Clock: clock,
		SessionCookie: websession.CookieConfig{Path: "/", AllowInsecure: true}, CSRFCookie: websession.CookieConfig{Path: "/", AllowInsecure: true},
		LoginPath: "/admin/login/", FallbackPath: "/admin/", AllowedNextPaths: adminPaths})
	check(err)
	adminSite, err := admin.NewSite(admin.SiteConfig{Apps: configured.Apps(), Namespace: "selectedapi", Registry: adminRegistry, Auth: adminRuntime})
	check(err)
	routes = append(routes, adminSite.Routes()...)
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
	adminSend := func(client *http.Client, method, endpoint string, data url.Values) (int, http.Header, []byte) {
		t.Helper()
		request, err := http.NewRequestWithContext(t.Context(), method, server.URL+endpoint, strings.NewReader(data.Encode()))
		check(err)
		if method == "POST" {
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.Header.Set("Origin", server.URL)
		}
		status, headers, body := readResponse(client.Do(request))
		if strings.Contains(string(body), "private") {
			t.Fatal("Admin published a private execution cause")
		}
		return status, headers, body
	}
	adminToken := regexp.MustCompile(`name="csrfmiddlewaretoken" value="([A-Za-z0-9_-]{128})"`)
	prepareAdmin := func(client *http.Client, endpoint string, fields map[string]any, revision string) func() (int, http.Header, []byte) {
		t.Helper()
		status, _, body := adminSend(client, "GET", endpoint, nil)
		token := adminToken.FindSubmatch(body)
		if status != 200 || len(token) != 2 {
			t.Fatal("Admin preparation did not render its real form", endpoint, status)
		}
		data := url.Values{"csrfmiddlewaretoken": {string(token[1])}}
		for field, value := range fields {
			switch value := value.(type) {
			case string:
				data.Set(field, value)
			case int:
				data.Set(field, strconv.Itoa(value))
			case bool:
				if value {
					data.Set(field, "on")
				}
			default:
				t.Fatal("unsupported Admin field observation", field)
			}
		}
		if revision != "" {
			data.Set("expected_revision", revision)
		}
		return func() (int, http.Header, []byte) { return adminSend(client, "POST", endpoint, data) }
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
		if status != 200 || detail.ID != created.ID || detail.Revision != created.Revision || !detail.PasswordUsable || len(detail.Fields) != len(readFields) || header.Get("Revision") != "1" {
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
	run("native_input_rejections", func(t *testing.T) {
		before := snapshot()
		hashes := trackedHasher.calls.Load()
		target := path("accounts", created.ID)
		revision := strconv.FormatInt(created.Revision, 10)
		for index, probe := range []struct {
			method, path, body, revision string
			csrf                         bool
			status                       int
		}{
			{"PATCH", target, fieldsBody(map[string]any{role.Active.Field: false}), revision, false, 403},
			{"PATCH", target, "invalid", `W/"1"`, true, 400},
			{"PATCH", target, "invalid", `*`, true, 400},
			{"PATCH", target, fieldsBody(map[string]any{role.Password: "forged"}), revision, true, 400},
			{"PATCH", target, fieldsBody(map[string]any{role.PrincipalID: "forged"}), revision, true, 400},
			{"PATCH", target, fieldsBody(map[string]any{role.Revision: 200}), revision, true, 400},
			{"PATCH", target, fieldsBody(map[string]any{"password_usable": false}), revision, true, 400},
			{"PUT", target, fieldsBody(map[string]any{role.Login: "forged@example.test", "password_usable": false}), revision, true, 400},
			{"PATCH", target, `{"password":"forged"}`, revision, true, 400},
			{"PATCH", target, fieldsBody(map[string]any{role.Groups: nil}), revision, true, 400},
			{"PATCH", target, fieldsBody(map[string]any{role.Groups: []json.Number{"1.0"}}), revision, true, 400},
			{"PATCH", target, fieldsBody(map[string]any{role.Groups: []int64{9223372036854775807}}), revision, true, 400},
			{"PATCH", target, `{"fields":{` + strconv.Quote(role.Active.Field) + `:false,` + strconv.Quote(role.Active.Field) + `:true}}`, revision, true, 400},
			{"POST", "/api/identity/accounts/", fieldsBody(accountFields("missing@example.test")), "", true, 400},
			{"POST", "/api/identity/accounts/", creationBody(func() map[string]any {
				values := accountFields("forged@example.test")
				values["password_usable"] = true
				return values
			}(), "native-fault-password"), "", true, 400},
			{"POST", target + "password/", encode(map[string]any{"credential": map[string]any{"password": nil}, "password_usable": true}), revision, true, 400},
			{"DELETE", target, `{}`, revision, true, 400},
			{"GET", "/api/identity/accounts/?limit=0", "", "", false, 400},
			{"GET", "/api/identity/accounts/?limit=1&limit=2", "", "", false, 400},
			{"GET", "/api/identity/accounts/?offset=2147483648", "", "", false, 400},
			{"GET", target + "?ignored=x", "", "", false, 400},
			{"POST", "/api/identity/accounts/", creationBody(accountFields("large@example.test"), strings.Repeat("x", 65536)), "", true, 413},
		} {
			status, header, body := send(client, probe.method, probe.path, probe.body, probe.revision, probe.csrf)
			if status != probe.status || header.Get("Revision") != "" || trackedHasher.calls.Load() != hashes || !reflect.DeepEqual(before, snapshot()) {
				t.Fatal("input rejection hashed, published or changed durable state", index, status)
			}
			if strings.Contains(string(body), `"password_usable":`) {
				t.Fatal("rejection published a successful account representation")
			}
		}
		for _, field := range []string{keyField.Name, role.CreatedAt, role.LastLogin} {
			if field == "" {
				continue
			}
			var value any = "2026-01-01T00:00:00Z"
			if field == keyField.Name {
				value = 42
			}
			status, header, _ := send(client, "PATCH", target, fieldsBody(map[string]any{field: value}), revision, true)
			if status != 400 || header.Get("Revision") != "" || trackedHasher.calls.Load() != hashes || !reflect.DeepEqual(before, snapshot()) {
				t.Fatal("server field became writable", field, status)
			}
		}
		status, _, _ := send(newClient(), "POST", "/api/identity/accounts/", `{broken`, "", true)
		if status != 403 || trackedHasher.calls.Load() != hashes || !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("anonymous input reached management", status)
		}
	})
	for _, resource := range []string{"accounts", "groups", "permissions"} {
		run("native_revision_"+resource, func(t *testing.T) {
			const revision = int64(9007199254740993)
			fields, changedField := accountFields("header@example.test"), profileField
			if resource == "groups" {
				fields = map[string]any{role.Catalog.GroupName: "Header group"}
				changedField = role.Catalog.GroupName
				if groupExtra != "" {
					fields[groupExtra] = 7
				}
			}
			if resource == "permissions" {
				fields = map[string]any{role.Catalog.PermissionCode: "jsonheader.target", role.Catalog.PermissionName: "Header permission"}
				changedField = role.Catalog.PermissionName
				if permissionExtra != "" {
					fields[permissionExtra] = "header fixture"
				}
			}
			body := fieldsBody(fields)
			if resource == "accounts" {
				body = creationBody(fields, nil)
			}
			status, header, value := send(client, "POST", "/api/identity/"+resource+"/", body, "", true)
			target := decodeReceipt(status, header, value, 201)
			switch resource {
			case "accounts":
				managementHTTPSetRevision(t, faults.Backend, objects, target.ID, role.Revision, revision)
			case "groups":
				managementHTTPSetRevision(t, faults.Backend, catalog.Groups().Manager(), target.ID, role.Catalog.GroupRevision, revision)
			case "permissions":
				managementHTTPSetRevision(t, faults.Backend, catalog.Permissions().Manager(), target.ID, role.Catalog.PermissionRevision, revision)
			}
			before := snapshot()
			writes := faults.writes.Load()
			faults.writeFault.Store("cancel")
			changedValue := "Header updated"
			if resource == "accounts" {
				changedValue = "NZ"
			}
			body = fieldsBody(map[string]any{changedField: changedValue})
			status, header, _ = send(client, "PATCH", path(resource, target.ID), body, strconv.FormatInt(revision, 10), true)
			if status != 500 || header.Get("Revision") != "" || faults.writes.Load()-writes != 1 || !reflect.DeepEqual(before, snapshot()) {
				t.Fatal("canceled accepted header mutated or retried", status)
			}
			status, header, value = send(client, "PATCH", path(resource, target.ID), body, strconv.FormatInt(revision, 10), true)
			updated := decodeReceipt(status, header, value, 200)
			if updated.ID != target.ID || updated.Revision != revision+1 {
				t.Fatal("native header lost exact int64 revision")
			}
			before = snapshot()
			status, header, value = send(client, "PATCH", path(resource, target.ID), body, strconv.FormatInt(revision, 10), true)
			if status != 412 || header.Get("Revision") != "" || !strings.Contains(string(value), `"code":"revision_conflict"`) || !reflect.DeepEqual(before, snapshot()) {
				t.Fatal("stale large revision changed durable state", status)
			}
			status, header, value = send(client, "GET", path(resource, target.ID), "", "", false)
			var detail struct{ ID, Revision int64 }
			check(json.Unmarshal(value, &detail))
			if status != 200 || detail.ID != target.ID || detail.Revision != revision+1 || header.Get("Revision") != strconv.FormatInt(revision+1, 10) || !reflect.DeepEqual(before, snapshot()) {
				t.Fatal("native detail lost committed revision", status)
			}
		})
	}
	grantKeys := map[auth.Permission]int64{}
	grantKey := func(code auth.Permission) int64 {
		if id, found := grantKeys[code]; found {
			return id
		}
		fields := map[string]any{role.Catalog.PermissionCode: string(code), role.Catalog.PermissionName: string(code)}
		if permissionExtra != "" {
			fields[permissionExtra] = "HTTP grant"
		}
		status, header, body := send(client, "POST", "/api/identity/permissions/", fieldsBody(fields), "", true)
		result := decodeReceipt(status, header, body, 201)
		grantKeys[code] = result.ID
		return result.ID
	}
	admissionIndex := 0
	for _, resource := range []struct {
		name                              string
		view, add, change, remove, common auth.Permission
	}{
		{"accounts", "account.view", "account.add", "account.change", "account.delete", "account.manage"},
		{"groups", "catalog.view_group", "catalog.add_group", "catalog.change_group", "catalog.delete_group", "catalog.manage"},
		{"permissions", "catalog.view_permission", "catalog.add_permission", "catalog.change_permission", "catalog.delete_permission", "catalog.manage"},
	} {
		fields := accountFields("grant-target@example.test")
		if resource.name == "groups" {
			fields = map[string]any{role.Catalog.GroupName: "HTTP grant target"}
			if groupExtra != "" {
				fields[groupExtra] = 7
			}
		}
		if resource.name == "permissions" {
			fields = map[string]any{role.Catalog.PermissionCode: "jsongrant.target", role.Catalog.PermissionName: "HTTP grant target"}
			if permissionExtra != "" {
				fields[permissionExtra] = "HTTP target"
			}
		}
		body := fieldsBody(fields)
		if resource.name == "accounts" {
			body = creationBody(fields, nil)
		}
		status, header, value := send(client, "POST", "/api/identity/"+resource.name+"/", body, "", true)
		target := decodeReceipt(status, header, value, 201)
		for _, code := range []auth.Permission{resource.view, resource.add, resource.change, resource.remove, resource.common} {
			grantKey(code)
		}
		for _, access := range []string{"none", "view", "change", "add", "add_change", "missing_common", "delete"} {
			run("admission_"+resource.name+"_"+access, func(t *testing.T) {
				admissionIndex++
				keys := []int64{grantKey(resource.common)}
				switch access {
				case "view":
					keys = append(keys, grantKey(resource.view))
				case "change":
					keys = append(keys, grantKey(resource.change))
				case "add":
					keys = append(keys, grantKey(resource.add))
				case "add_change":
					keys = append(keys, grantKey(resource.add), grantKey(resource.change))
				case "delete":
					keys = append(keys, grantKey(resource.remove))
				case "missing_common":
					keys = []int64{grantKey(resource.view), grantKey(resource.add), grantKey(resource.change), grantKey(resource.remove)}
				}
				name := fmt.Sprintf("permit%d@example.test", admissionIndex)
				actorFields := accountFields(name)
				actorFields[role.Permissions] = keys
				if role.Staff.Field != "" {
					actorFields[role.Staff.Field] = false
				}
				status, header, value := send(client, "POST", "/api/identity/accounts/", creationBody(actorFields, "HTTP permission password"), "", true)
				member := decodeReceipt(status, header, value, 201)
				browser := newClient()
				login(browser, name, "HTTP permission password")
				expect := func(method, endpoint, body, revision string, want int) {
					t.Helper()
					before := snapshot()
					hashes := trackedHasher.calls.Load()
					status, _, _ := send(browser, method, endpoint, body, revision, method != "GET")
					if status != want {
						t.Fatal("selected native HTTP permission matrix", resource.name, access, method, status, want)
					}
					if want == 403 && (!reflect.DeepEqual(before, snapshot()) || trackedHasher.calls.Load() != hashes) {
						t.Fatal("denied native API changed state or hashed")
					}
				}
				readStatus, changeStatus, createStatus, deleteStatus := 403, 403, 403, 403
				if access == "view" || access == "change" || access == "add_change" {
					readStatus = 200
				}
				if access == "change" || access == "add_change" {
					changeStatus = 200
				}
				if access == "add_change" || access == "add" && resource.name != "accounts" {
					createStatus = 201
				}
				if access == "delete" {
					deleteStatus = 200
				}
				expect("GET", "/api/identity/"+resource.name+"/", "", "", readStatus)
				expect("GET", path(resource.name, target.ID), "", "", readStatus)
				expect("GET", path(resource.name, target.ID)+"history/", "", "", readStatus)
				expect("PATCH", path(resource.name, target.ID), fieldsBody(map[string]any{}), "1", changeStatus)
				createFields := accountFields(fmt.Sprintf("allowed%d@example.test", admissionIndex))
				if resource.name == "groups" {
					createFields = map[string]any{role.Catalog.GroupName: fmt.Sprintf("HTTP allowed group %d", admissionIndex)}
					if groupExtra != "" {
						createFields[groupExtra] = 7
					}
				}
				if resource.name == "permissions" {
					createFields = map[string]any{role.Catalog.PermissionCode: fmt.Sprintf("jsongrant.allowed%d", admissionIndex), role.Catalog.PermissionName: fmt.Sprintf("HTTP allowed permission %d", admissionIndex)}
					if permissionExtra != "" {
						createFields[permissionExtra] = "HTTP created"
					}
				}
				body := fieldsBody(createFields)
				if resource.name == "accounts" {
					body = creationBody(createFields, nil)
				}
				expect("POST", "/api/identity/"+resource.name+"/", body, "", createStatus)
				expect("DELETE", path(resource.name, target.ID), "", "1", deleteStatus)
				if access == "change" {
					status, header, value := send(client, "PATCH", path("accounts", member.ID), fieldsBody(map[string]any{role.Permissions: []int64{}}), "1", true)
					decodeReceipt(status, header, value, 200)
					expect("GET", path(resource.name, target.ID), "", "", 403)
				}
			})
		}
	}
	// Preserve the old native HTTP failure matrix on the selected owners and
	// renamed generated models. Every mode reaches one real write transaction;
	// acknowledgement-loss cases never authorize a blind retry.
	failureIndex := 0
	for _, transport := range []string{"json", "admin"} {
		for _, resource := range []string{"accounts", "groups", "permissions"} {
			operations := []string{"create", "update", "delete"}
			if resource == "accounts" {
				operations = append(operations, "password", "create_unusable", "disable")
			}
			for _, operation := range operations {
				for _, mode := range []string{"audit_failure", "unknown_rollback", "unknown"} {
					run("failure_"+transport+"_"+resource+"_"+operation+"_"+mode, func(t *testing.T) {
						creation := operation == "create" || operation == "create_unusable"
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
						if operation == "create_unusable" {
							body = creationBody(fields, nil)
						}
						method, endpoint, revision := "POST", "/api/identity/"+resource+"/", ""
						var target receipt
						var targetSession sessions.ID
						if !creation {
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
							if operation == "password" || operation == "disable" {
								method, endpoint = "POST", endpoint+"password/"
								var password any = "native-replacement-password"
								if operation == "disable" {
									password = nil
								}
								body = encode(map[string]any{"credential": map[string]any{"password": password}})
							}
						}
						submit := func() (int, http.Header, []byte) { return send(client, method, endpoint, body, revision, true) }
						if transport == "admin" {
							values := maps.Clone(fields)
							action := "add"
							if resource == "accounts" {
								values[role.Active.Field] = true
							}
							if creation && resource == "accounts" {
								values["password1"], values["password2"] = "native-fault-password", "native-fault-password"
								if operation == "create_unusable" {
									values["unusable_password"] = true
								}
							}
							if operation == "update" {
								action, values[changedField] = "change", "NZ"
								if resource == "accounts" {
									values[role.Active.Field] = false
								}
							}
							if operation == "delete" {
								action, values = "delete", map[string]any{"confirm": "yes"}
							}
							if operation == "password" {
								action, values = "command/password", map[string]any{"password1": "native-replacement-password", "password2": "native-replacement-password"}
							}
							if operation == "disable" {
								action, values = "command/disable-password", map[string]any{"confirm": true}
							}
							endpoint = "/admin/" + resource + "/" + action + "/"
							if !creation {
								endpoint += "?id=" + strconv.FormatInt(target.ID, 10)
							}
							submit = prepareAdmin(client, endpoint, values, revision)
						}
						before := snapshot()
						writes, hashes, auditFailures, armed := faults.writes.Load(), trackedHasher.calls.Load(), faults.auditFaults.Load(), overlay.writeArmed.Load()
						policies := passwordPolicies.Load()
						if transport == "admin" {
							overlay.writeAfterAdmission.Store(mode)
						} else {
							faults.writeFault.Store(mode)
						}
						status, header, value := submit()
						if transport == "admin" {
							if overlay.writeArmed.Load()-armed != 1 {
								t.Fatal("Admin never reached the mutation admission boundary")
							}
							writes = overlay.writeStart.Load()
						}
						wantStatus, wantHashes := 503, int64(0)
						if mode == "audit_failure" {
							wantStatus = 500
						}
						if resource == "accounts" && (operation == "create" || operation == "password") {
							wantHashes = 1
						}
						if status != wantStatus || faults.writes.Load()-writes != 1 || trackedHasher.calls.Load()-hashes != wantHashes || header.Get("Revision") != "" || header.Get("Location") != "" || header.Get("Retry-After") != "" || header.Get("Cache-Control") != "no-store" {
							t.Fatal("failure retried, missed native work or published a receipt", status, faults.writes.Load()-writes, trackedHasher.calls.Load()-hashes)
						}
						if (operation == "create_unusable" || operation == "disable") && passwordPolicies.Load() != policies {
							t.Fatal("unusable command invoked password policy")
						}
						if (faults.auditFaults.Load()-auditFailures == 1) != (mode == "audit_failure") {
							t.Fatal("audit failure was not reached exactly once")
						}
						if mode == "audit_failure" {
							if string(value) != "Internal Server Error\n" {
								t.Fatal("execution failure exposed diagnostics or partial result")
							}
						} else if transport == "admin" {
							if string(value) != "The outcome is unknown. Check the current stored state before starting another operation. Do not retry automatically.\n" {
								t.Fatal("Admin lost the uncertain outcome or published target metadata")
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
						if creation {
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
							before.Accounts, oldValues, oldIndex = managementHTTPWithout(t, objects, before.Accounts, target.ID)
							after.Accounts, newValues, newIndex = managementHTTPWithout(t, objects, after.Accounts, target.ID)
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
							before.Groups, oldValues, oldIndex = managementHTTPWithout(t, catalog.Groups().Manager(), before.Groups, target.ID)
							after.Groups, newValues, newIndex = managementHTTPWithout(t, catalog.Groups().Manager(), after.Groups, target.ID)
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
							before.Permissions, oldValues, _ = managementHTTPWithout(t, catalog.Permissions().Manager(), before.Permissions, target.ID)
							after.Permissions, newValues, _ = managementHTTPWithout(t, catalog.Permissions().Manager(), after.Permissions, target.ID)
						}
						if (oldValues == nil) != creation || (newValues == nil) != (operation == "delete") {
							t.Fatal("unknown result has wrong durable existence")
						}
						if operation == "update" || operation == "password" || operation == "disable" {
							expected := maps.Clone(oldValues)
							expected[revisionField] = query.Integer(target.Revision + 1)
							if operation == "update" {
								expected[changedField] = query.String("NZ")
								if resource == "accounts" {
									expected[role.Active.Field] = query.Boolean(false)
								}
							} else {
								encoded, _ := newValues[role.Password].String()
								if operation == "disable" {
									if auth.IsPasswordUsable(encoded) {
										t.Fatal("unknown disable retained a usable credential")
									}
								} else {
									valid, err := hasher.Verify(t.Context(), "native-replacement-password", encoded)
									check(err)
									if !valid {
										t.Fatal("unknown password result lost replacement")
									}
								}
								if reflect.DeepEqual(newValues[role.Password], oldValues[role.Password]) {
									t.Fatal("unknown password result retained its old credential")
								}
								expected[role.Password] = newValues[role.Password]
							}
							if !reflect.DeepEqual(expected, newValues) {
								t.Fatal("unknown update lost requested values or changed unrelated fields")
							}
						}
						if creation {
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
								if operation == "create_unusable" {
									if auth.IsPasswordUsable(encoded) {
										t.Fatal("unknown unusable creation published a usable credential")
									}
								} else {
									valid, err := hasher.Verify(t.Context(), "native-fault-password", encoded)
									check(err)
									if !valid {
										t.Fatal("unknown create lost requested credential")
									}
								}
							}
						}
						if resource == "accounts" && !creation {
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
	}
	for _, operation := range []string{"create", "password"} {
		for _, mode := range []string{"invalid_input", "wrapped_input", "failure"} {
			run("hasher_"+operation+"_"+mode, func(t *testing.T) {
				before := snapshot()
				hashes, writes := trackedHasher.calls.Load(), faults.writes.Load()
				body := creationBody(accountFields("hash-failure@example.test"), "native hash candidate")
				endpoint, revision := "/api/identity/accounts/", ""
				if operation == "password" {
					endpoint, revision = path("accounts", created.ID)+"password/", strconv.FormatInt(created.Revision, 10)
					body = encode(map[string]any{"credential": map[string]any{"password": "native hash candidate"}})
				}
				trackedHasher.fault.Store(mode)
				status, header, value := send(client, "POST", endpoint, body, revision, true)
				trackedHasher.fault.Store("")
				want := 500
				if mode == "invalid_input" {
					want = 400
				}
				if status != want || trackedHasher.calls.Load()-hashes != 1 || faults.writes.Load() != writes || header.Get("Revision") != "" || !reflect.DeepEqual(before, snapshot()) {
					t.Fatal("hasher boundary retried, mutated or lost classification", operation, mode, status)
				}
				if mode == "invalid_input" && !strings.Contains(string(value), `"field":"/credential/password"`) {
					t.Fatal("hasher input refusal lost command origin")
				}
				if mode != "invalid_input" && string(value) != "Internal Server Error\n" {
					t.Fatal("hasher infrastructure failure exposed diagnostics")
				}
			})
		}
	}
	if len(protect) > 0 {
		run("protected_delete_cleanup", func(t *testing.T) {
			cleanup := protect[0](t, created.ID)
			defer cleanup()
			before := snapshot()
			for _, mode := range []string{"protected", "rejected_cleanup"} {
				writes, failures := faults.writes.Load(), faults.protectedFaults.Load()
				if mode == "rejected_cleanup" {
					faults.writeFault.Store(mode)
				}
				status, header, value := send(client, "DELETE", path("accounts", created.ID), "", strconv.FormatInt(created.Revision, 10), true)
				want := 400
				if mode == "rejected_cleanup" {
					want = 500
				}
				if status != want || faults.writes.Load()-writes != 1 || header.Get("Revision") != "" || !reflect.DeepEqual(before, snapshot()) {
					t.Fatal("protected native deletion mutated or misclassified cleanup", mode, status)
				}
				if mode == "protected" && (!strings.Contains(string(value), `"field":"/fields/__all__"`) || !strings.Contains(string(value), `"code":"protected"`)) {
					t.Fatal("native protection lost safe input diagnostic")
				}
				if mode == "rejected_cleanup" && (faults.protectedFaults.Load()-failures != 1 || string(value) != "Internal Server Error\n") {
					t.Fatal("cleanup fault missed actual PROTECT or leaked cause")
				}
			}
		})
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
		for _, target := range []struct {
			resource string
			id       int64
		}{{"accounts", created.ID}, {"groups", group.ID}, {"permissions", permission.ID}} {
			for _, endpoint := range []string{"/api/identity/" + target.resource + "/", path(target.resource, target.id), path(target.resource, target.id) + "history/"} {
				before := snapshot()
				failures := faults.readFailures.Load()
				overlay.failReadAfterAdmission.Store(true)
				status, header, value := send(client, "GET", endpoint, "", "", false)
				if status != 500 || faults.readFailures.Load()-failures != 1 || overlay.failReadAfterAdmission.Load() || header.Get("Revision") != "" || string(value) != "Internal Server Error\n" || !reflect.DeepEqual(before, snapshot()) {
					t.Fatal("management read cleanup published or changed durable state", endpoint, status)
				}
			}
		}
	})
	run("admin_read_cleanup_failure", func(t *testing.T) {
		for _, target := range []struct {
			resource string
			id       int64
		}{{"accounts", created.ID}, {"groups", group.ID}, {"permissions", permission.ID}} {
			for _, endpoint := range []string{"/admin/" + target.resource + "/", "/admin/" + target.resource + "/change/?id=" + strconv.FormatInt(target.id, 10), "/admin/" + target.resource + "/history/?id=" + strconv.FormatInt(target.id, 10)} {
				before := snapshot()
				failures := faults.readFailures.Load()
				overlay.failReadAfterAdmission.Store(true)
				status, header, body := adminSend(client, "GET", endpoint, nil)
				if status != 500 || faults.readFailures.Load()-failures != 1 || overlay.failReadAfterAdmission.Load() || header.Get("Location") != "" || header.Get("Retry-After") != "" || header.Get("Cache-Control") != "no-store" || string(body) != "Internal Server Error\n" || !reflect.DeepEqual(before, snapshot()) {
					t.Fatal("Admin read cleanup exposed a partial page or changed durable state", endpoint, status)
				}
			}
		}
	})

	// The submitting browser has its own authority so the independent root
	// observer can still compare all native state after that authority changes.
	boundaryActor := func(name string) (receipt, *http.Client, auth.Principal) {
		t.Helper()
		fields := accountFields(name)
		fields[role.Superuser.Field] = true
		if role.Staff.Field != "" {
			fields[role.Staff.Field] = true
		}
		status, header, body := send(client, "POST", "/api/identity/accounts/", creationBody(fields, "boundary actor password"), "", true)
		created := decodeReceipt(status, header, body, 201)
		browser := newClient()
		login(browser, name, "boundary actor password")
		credential, err := verifier.Authenticate(t.Context(), name, "boundary actor password")
		check(err)
		return created, browser, credential.Principal()
	}
	nativeChange := func(ctx context.Context, scope db.Session, id int64, values map[string]query.Value) error {
		row, err := objects.Using(scope).Filter(key.Exact(id)).Get(ctx)
		if err != nil {
			return err
		}
		updated, err := objects.ApplyValues(row, values)
		if err != nil {
			return err
		}
		names := make([]string, 0, len(values))
		for name := range values {
			names = append(names, name)
		}
		slices.Sort(names)
		return objects.Save(ctx, scope, &updated, orm.UpdateFieldNames[M](names...))
	}
	expectNativeChange := func(expected *durable, id int64, values map[string]query.Value) {
		t.Helper()
		_, _, index := managementHTTPWithout(t, objects, expected.Accounts, id)
		if index < 0 {
			t.Fatal("native boundary target missing")
		}
		expected.Accounts = slices.Clone(expected.Accounts)
		updated, err := objects.ApplyValues(expected.Accounts[index], values)
		check(err)
		expected.Accounts[index] = updated
	}
	for _, mode := range []string{"revision", "authority"} {
		run("admin_write_fence_"+mode, func(t *testing.T) {
			writer, err := database.Open(t.Context(), backendConfig)
			check(err)
			defer func() { check(writer.Close()) }()
			submitter, browser, _ := boundaryActor("fence-" + mode + "@example.test")
			fields := accountFields("target-" + mode + "@example.test")
			status, header, body := send(client, "POST", "/api/identity/accounts/", creationBody(fields, "boundary target password"), "", true)
			target := decodeReceipt(status, header, body, 201)
			fields[profileField] = "NZ"
			submit := prepareAdmin(browser, "/admin/accounts/change/?id="+strconv.FormatInt(target.ID, 10), fields, strconv.FormatInt(target.Revision, 10))
			before := snapshot()
			expected := before
			changedID, want := target.ID, 409
			changes := map[string]query.Value{"password": query.String("competing winner"), role.Revision: query.Integer(target.Revision + 1)}
			if mode == "authority" {
				changedID, want = submitter.ID, 403
				changes = map[string]query.Value{role.Superuser.Field: query.Boolean(false), role.Revision: query.Integer(submitter.Revision + 1)}
			}
			expectNativeChange(&expected, changedID, changes)
			var calls atomic.Int64
			hashes, armed := trackedHasher.calls.Load(), overlay.writeArmed.Load()
			overlay.writeHookAfterAdmission.Store(&managementHTTPWriteHook{before: func(ctx context.Context) error {
				calls.Add(1)
				return writer.CoordinatedAtomic(ctx, func(scope db.Session) error { return nativeChange(ctx, scope, changedID, changes) })
			}})
			defer func() { overlay.writeHookAfterAdmission.Store(nil); faults.beforeWrite.Store(nil) }()
			status, header, _ = submit()
			if status != want || header.Get("Location") != "" || calls.Load() != 1 ||
				overlay.writeArmed.Load() != armed+1 || faults.writes.Load()-overlay.writeStart.Load() != 1 || trackedHasher.calls.Load() != hashes {
				t.Fatal("Admin final fence did not reject exactly one late native write", mode, status)
			}
			if !reflect.DeepEqual(expected, snapshot()) {
				t.Fatal("rejected Admin write changed state beyond the independent competing commit", mode)
			}
		})
	}
	historySequences := func(body []byte) []int64 {
		t.Helper()
		matches := regexp.MustCompile(`data-sequence="([0-9]+)"`).FindAllSubmatch(body, -1)
		result := make([]int64, len(matches))
		for index, match := range matches {
			sequence, err := strconv.ParseInt(string(match[1]), 10, 64)
			check(err)
			result[index] = sequence
		}
		return result
	}
	run("admin_history_snapshot_and_scope", func(t *testing.T) {
		writer, err := database.Open(t.Context(), backendConfig)
		check(err)
		defer func() { check(writer.Close()) }()
		submitter, browser, retainedActor := boundaryActor("history-reader@example.test")
		status, header, body := send(client, "POST", "/api/identity/accounts/", creationBody(accountFields("history-target@example.test"), "history target password"), "", true)
		target := decodeReceipt(status, header, body, 201)
		endpoint := "/admin/accounts/history/?id=" + strconv.FormatInt(target.ID, 10)
		baseline, err := owner.Reader().History(t.Context(), retainedActor, target.ID, 100)
		check(err)
		if len(baseline) != 1 || baseline[0].Action() != admin.ActionAdd {
			t.Fatal("history baseline is not the real creation event")
		}
		// Establish ordinary session access before comparing read-only state.
		status, _, body = adminSend(browser, "GET", endpoint, nil)
		if status != 200 || !slices.Equal(historySequences(body), []int64{baseline[0].Sequence()}) {
			t.Fatal("initial Admin history differs", status)
		}
		before := snapshot()
		expected := before
		targetChanges := map[string]query.Value{"password": query.String("history winner"), role.Revision: query.Integer(target.Revision + 1)}
		actorChanges := map[string]query.Value{role.Superuser.Field: query.Boolean(false), role.Revision: query.Integer(submitter.Revision + 1)}
		expectNativeChange(&expected, target.ID, targetChanges)
		expectNativeChange(&expected, submitter.ID, actorChanges)
		domain, bound := actor.CredentialDomain()
		if !bound {
			t.Fatal("history actor lost selected domain")
		}
		model := role.Model.AppLabel + "." + role.Model.ModelName
		event, err := accountaudit.Prepare(actor, model, target.ID, admin.ActionChange, []string{"password"}, clock())
		check(err)
		type observation struct {
			reader db.Queryer
			entry  accountaudit.Entry
		}
		observed := make(chan observation, 1)
		var calls atomic.Int64
		faults.beforeHistory.Store(&managementHTTPHistoryHook{before: func(ctx context.Context, reader db.Queryer) error {
			calls.Add(1)
			var entry accountaudit.Entry
			err := writer.CoordinatedAtomic(ctx, func(scope db.Session) error {
				if err := nativeChange(ctx, scope, target.ID, targetChanges); err != nil {
					return err
				}
				if err := nativeChange(ctx, scope, submitter.ID, actorChanges); err != nil {
					return err
				}
				var err error
				entry, err = accountaudit.AppendIn(ctx, scope, event)
				return err
			})
			if err == nil {
				observed <- observation{reader: reader, entry: entry}
			}
			return err
		}})
		defer faults.beforeHistory.Store(nil)
		status, _, body = adminSend(browser, "GET", endpoint, nil)
		if status != 200 || calls.Load() != 1 || !slices.Equal(historySequences(body), []int64{baseline[0].Sequence()}) {
			t.Fatal("Admin history mixed old authority with a newly committed audit", status)
		}
		var seen observation
		select {
		case seen = <-observed:
		default:
			t.Fatal("history probe did not observe its separate commit")
		}
		after := snapshot()
		if len(after.Audits) != len(before.Audits)+1 {
			t.Fatal("competing history commit lost or duplicated audit")
		}
		added := after.Audits[len(before.Audits)]
		if added.ID != seen.entry.Sequence() || added.Domain != domain.Identifier() || added.ActorID != actor.ID() ||
			added.Model != model || added.ObjectID != target.ID || added.Action != string(admin.ActionChange) ||
			added.ChangedFields != `["password"]` || !added.RecordedAt.Equal(clock()) {
			t.Fatal("competing history audit lost its exact native semantics")
		}
		after.Audits = after.Audits[:len(before.Audits)]
		if !reflect.DeepEqual(expected, after) {
			t.Fatal("history read changed state beyond the separate authorized commit")
		}
		for _, reader := range []db.Queryer{seen.reader, faults.Backend} {
			if entries, err := accountaudit.HistoryIn(t.Context(), reader, domain, model, target.ID, 100); err == nil || entries != nil {
				t.Fatal("expired or root history reader impersonated a borrowed scope")
			}
		}
		entries, err := owner.Reader().History(t.Context(), retainedActor, target.ID, 100)
		failure, valid := err.(*accountmodel.Error)
		if !valid || failure.Code != accountmodel.CodePermission || entries != nil {
			t.Fatal("retained history actor reused revoked authority", err)
		}
		status, _, body = adminSend(browser, "GET", endpoint, nil)
		if status != 403 || len(historySequences(body)) != 0 {
			t.Fatal("next Admin history retained revoked authority", status)
		}
		status, _, body = adminSend(client, "GET", endpoint, nil)
		if status != 200 || !slices.Equal(historySequences(body), []int64{baseline[0].Sequence(), seen.entry.Sequence()}) {
			t.Fatal("fresh authorized Admin history omitted the concurrent commit", status)
		}
	})
	run("admin_history_storage_failure", func(t *testing.T) {
		before := snapshot()
		cause := errors.Join(errors.New("private history query failure"), &accountmodel.Error{Code: accountmodel.CodePermission})
		var calls atomic.Int64
		hook := &managementHTTPHistoryHook{before: func(context.Context, db.Queryer) error { calls.Add(1); return cause }}
		faults.beforeHistory.Store(hook)
		defer faults.beforeHistory.Store(nil)
		entries, err := owner.Reader().History(t.Context(), actor, created.ID, 100)
		failure, valid := err.(*accountmodel.Error)
		if !valid || failure.Code != accountmodel.CodePersistence || !errors.Is(err, cause) || entries != nil || calls.Load() != 1 {
			t.Fatal("history storage failure was downgraded or published provisional entries", err)
		}
		faults.beforeHistory.Store(hook)
		status, header, body := adminSend(client, "GET", "/admin/accounts/history/?id="+strconv.FormatInt(created.ID, 10), nil)
		if status != 500 || header.Get("Location") != "" || len(historySequences(body)) != 0 || calls.Load() != 2 || !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("Admin history storage failure became a permission refusal or changed native state", status)
		}
	})
	run("admin_status_display_boundary", func(t *testing.T) {
		before := snapshot()
		hashes := trackedHasher.calls.Load()
		status, _, page := adminSend(client, "GET", "/admin/accounts/add/", nil)
		if status != 200 || strings.Contains(string(page), `data-field-name="`+statusName+`"`) {
			t.Fatal("creation displayed an unstored password state")
		}
		endpoint := "/admin/accounts/change/?id=" + strconv.FormatInt(created.ID, 10)
		for _, field := range slices.Compact([]string{"password_usable", statusName}) {
			fields := accountFields("api@example.test")
			fields[field] = "false"
			submit := prepareAdmin(client, endpoint, fields, strconv.FormatInt(created.Revision, 10))
			status, headers, _ := submit()
			if status != 400 || headers.Get("Location") != "" || trackedHasher.calls.Load() != hashes || !reflect.DeepEqual(before, snapshot()) {
				t.Fatal("derived status or hidden profile became a mutable input", field, status)
			}
		}

		viewerFields := accountFields("status-viewer@example.test")
		viewerFields[role.Permissions] = []int64{grantKey("account.view"), grantKey("account.manage")}
		if role.Staff.Field != "" {
			viewerFields[role.Staff.Field] = true
		}
		status, header, body := send(client, "POST", "/api/identity/accounts/", creationBody(viewerFields, "status-view-password"), "", true)
		viewer := decodeReceipt(status, header, body, 201)
		observer := newClient()
		login(observer, "status-viewer@example.test", "status-view-password")
		before = snapshot()
		status, _, body = adminSend(observer, "GET", endpoint, nil)
		if status != 200 || !strings.Contains(string(body), `data-field-name="`+statusName+`">Enabled`) || strings.Contains(string(body), ` name="`+role.Login+`"`) || strings.Contains(string(body), "Change password") || strings.Contains(string(body), "profile-status") {
			t.Fatal("view-only selected account lost status or gained writable controls", status)
		}
		token := adminToken.FindSubmatch(body)
		if len(token) != 2 {
			t.Fatal("view-only status page lost CSRF")
		}
		status, _, _ = adminSend(observer, "POST", "/admin/accounts/command/disable-password/?id="+strconv.FormatInt(created.ID, 10), url.Values{
			"csrfmiddlewaretoken": {string(token[1])}, "expected_revision": {strconv.FormatInt(created.Revision, 10)}, "confirm": {"on"},
		})
		if status != 403 || !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("view-only status admitted a credential mutation", status)
		}
		status, header, body = send(client, "PATCH", path("accounts", viewer.ID), fieldsBody(map[string]any{role.Permissions: []int64{}}), strconv.FormatInt(viewer.Revision, 10), true)
		decodeReceipt(status, header, body, 200)
		before = snapshot()
		status, _, body = adminSend(observer, "GET", endpoint, nil)
		if status != 403 || strings.Contains(string(body), "Password login") || !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("status presentation retained revoked authority", status)
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
	run("nullable_password_and_status", func(t *testing.T) {
		command := path("accounts", created.ID) + "password/"
		revision := strconv.FormatInt(created.Revision, 10)
		before := snapshot()
		hashes, policies := trackedHasher.calls.Load(), passwordPolicies.Load()
		blockedPasswordPolicy.Store(true)
		defer blockedPasswordPolicy.Store(false)
		for _, body := range []string{`{}`, `{"credential":{}}`, `{"credential":{"password":""}}`, `{"credential":{"password":false}}`, `{"credential":{"password":null,"extra":true}}`} {
			status, header, _ := send(client, "POST", command, body, revision, true)
			if status != 400 || header.Get("Revision") != "" || !reflect.DeepEqual(before, snapshot()) {
				t.Fatal("invalid nullable password command changed state", status)
			}
		}
		for _, test := range []struct {
			revision string
			csrf     bool
			status   int
		}{{revision, false, 403}, {"", true, 428}, {strconv.FormatInt(created.Revision+1, 10), true, 412}} {
			status, _, _ := send(client, "POST", command, `{"credential":{"password":null}}`, test.revision, test.csrf)
			if status != test.status || !reflect.DeepEqual(before, snapshot()) {
				t.Fatal("nullable command bypassed transport or revision", status)
			}
		}
		assertStatus := func(usable, active bool) {
			t.Helper()
			before := snapshot()
			type observation struct {
				ID, Revision   int64
				Fields         map[string]json.RawMessage
				PasswordUsable *bool `json:"password_usable"`
			}
			checkStatus := func(row observation) {
				var enabled bool
				check(json.Unmarshal(row.Fields[role.Active.Field], &enabled))
				if row.ID != created.ID || row.Revision != created.Revision || row.PasswordUsable == nil || *row.PasswordUsable != usable || enabled != active {
					t.Fatal("derived password status is missing, mistyped or confused with activation")
				}
				if row.Fields[role.Password] != nil || row.Fields[role.PrincipalID] != nil {
					t.Fatal("password status exposed a stored credential")
				}
			}
			status, _, value := send(client, "GET", path("accounts", created.ID), "", "", false)
			var detail observation
			check(json.Unmarshal(value, &detail))
			if status != 200 {
				t.Fatal("password status detail", status)
			}
			checkStatus(detail)
			status, _, value = send(client, "GET", "/api/identity/accounts/?limit=100", "", "", false)
			var page struct{ Items []observation }
			check(json.Unmarshal(value, &page))
			seen := false
			for _, item := range page.Items {
				if item.ID == created.ID {
					checkStatus(item)
					seen = true
				}
			}
			if status != 200 || !seen || !reflect.DeepEqual(before, snapshot()) {
				t.Fatal("password status list mutated or omitted target", status)
			}
			adminPage := "/admin/accounts/change/?id=" + strconv.FormatInt(created.ID, 10)
			status, _, value = adminSend(client, "GET", adminPage, nil)
			label := "Disabled"
			if usable {
				label = "Enabled"
			}
			if status != 200 || !strings.Contains(string(value), `data-field-name="`+statusName+`">`+label) || strings.Contains(string(value), ` name="`+statusName+`"`) || strings.Contains(string(value), "profile-status") || !reflect.DeepEqual(before, snapshot()) {
				t.Fatal("selected Admin lost read-only credential state or confused it with activation", status)
			}
			invalid := accountFields("api@example.test")
			invalid[profileField], invalid[role.Active.Field] = "forbidden", active
			submit := prepareAdmin(client, adminPage, invalid, strconv.FormatInt(created.Revision, 10))
			status, _, value = submit()
			if status != 200 || !strings.Contains(string(value), `data-field-name="`+statusName+`">`+label) || !reflect.DeepEqual(before, snapshot()) {
				t.Fatal("rejected Admin edit lost the observed credential status", status)
			}
			if statusName != "password_usable" {
				row, err := objects.Using(backend).Filter(key.Exact(created.ID)).Get(t.Context())
				check(err)
				values, err := objects.ModelValues(row)
				check(err)
				profile, valid := values["password_usable"].String()
				if !valid || profile != "profile-status" {
					t.Fatal("derived state overwrote the unrelated real profile")
				}
			}
		}
		assertStatus(false, true)
		row, err := objects.Using(backend).Filter(key.Exact(created.ID)).Get(t.Context())
		check(err)
		previous, err := objects.ModelValues(row)
		check(err)
		status, header, value := send(client, "POST", command, `{"credential":{"password":null}}`, revision, true)
		created = decodeReceipt(status, header, value, 200)
		row, err = objects.Using(backend).Filter(key.Exact(created.ID)).Get(t.Context())
		check(err)
		after, err := objects.ModelValues(row)
		check(err)
		encoded, _ := after[role.Password].String()
		oldRevision, _ := previous[role.Revision].Integer()
		newRevision, _ := after[role.Revision].Integer()
		if created.Revision != oldRevision+1 || newRevision != created.Revision {
			t.Fatal("repeated null lost its revision")
		}
		if auth.IsPasswordUsable(encoded) || reflect.DeepEqual(previous[role.Password], after[role.Password]) || trackedHasher.calls.Load() != hashes || passwordPolicies.Load() != policies {
			t.Fatal("repeated null did not rotate independently of policy/hash")
		}
		assertStatus(false, true)
		status, header, value = send(client, "POST", "/api/identity/accounts/", creationBody(accountFields("without@example.test"), nil), "", true)
		without := decodeReceipt(status, header, value, 201)
		profile, err := owner.Reader().Get(t.Context(), actor, without.ID)
		check(err)
		if profile.PasswordUsable() || trackedHasher.calls.Load() != hashes || passwordPolicies.Load() != policies {
			t.Fatal("null creation ran policy/hash or stayed usable")
		}
		beforeBlocked := snapshot()
		status, _, _ = send(client, "POST", command, `{"credential":{"password":"valid but blocked"}}`, strconv.FormatInt(created.Revision, 10), true)
		if status != 400 || trackedHasher.calls.Load() != hashes || passwordPolicies.Load() != policies+1 || !reflect.DeepEqual(beforeBlocked, snapshot()) {
			t.Fatal("usable password bypassed configured host policy", status)
		}
		status, header, value = send(client, "PATCH", path("accounts", created.ID), fieldsBody(map[string]any{role.Active.Field: false}), strconv.FormatInt(created.Revision, 10), true)
		created = decodeReceipt(status, header, value, 200)
		assertStatus(false, false)
		blockedPasswordPolicy.Store(false)
		const restored = "  restored JSON password  "
		status, header, value = send(client, "POST", command, encode(map[string]any{"credential": map[string]any{"password": restored}}), strconv.FormatInt(created.Revision, 10), true)
		created = decodeReceipt(status, header, value, 200)
		assertStatus(true, false)
		status, header, value = send(client, "PATCH", path("accounts", created.ID), fieldsBody(map[string]any{role.Active.Field: true}), strconv.FormatInt(created.Revision, 10), true)
		created = decodeReceipt(status, header, value, 200)
		assertStatus(true, true)
		login(newClient(), "api@example.test", restored)
	})

	run("admin_last_login_display", func(t *testing.T) {
		const loginName, password = "login-observation@example.test", "selected login observation password"
		fields := accountFields(loginName)
		fields[role.Permissions] = []int64{grantKey("account.view"), grantKey("account.manage")}
		if role.Staff.Field != "" {
			fields[role.Staff.Field] = true
		}
		status, header, body := send(client, "POST", "/api/identity/accounts/", creationBody(fields, password), "", true)
		target := decodeReceipt(status, header, body, 201)
		endpoint := "/admin/accounts/change/?id=" + strconv.FormatInt(target.ID, 10)
		storedValues := func() map[string]query.Value {
			t.Helper()
			row, err := objects.Using(faults.Backend).Filter(key.Exact(target.ID)).Get(t.Context())
			check(err)
			values, err := objects.ModelValues(row)
			check(err)
			return values
		}
		expectTime := func(values map[string]query.Value, want *time.Time) {
			t.Helper()
			if role.LastLogin == "" {
				return
			}
			value, present := values[role.LastLogin]
			if !present {
				t.Fatal("selected stored LastLogin field is missing")
			}
			if want == nil {
				if !value.IsNull() {
					t.Fatal("unobserved account already has a login timestamp")
				}
				return
			}
			instant, valid := value.DateTime()
			if !valid || !instant.Equal(*want) {
				t.Fatal("native LastLogin differs from the successful HTTP login")
			}
		}
		expectProfile := func(profile accountmodel.Profile[M], want *time.Time) {
			t.Helper()
			value, present := profile.View().Get(role.LastLogin)
			if role.LastLogin == "" {
				if present {
					t.Fatal("absent LastLogin acquired an implicit field")
				}
				return
			}
			if !present {
				t.Fatal("explicit LastLogin exposure is missing")
			}
			expectTime(map[string]query.Value{role.LastLogin: value}, want)
		}
		assertDisplay := func(want *time.Time) {
			t.Helper()
			// Warm the ordinary Admin session access at this instant before
			// asserting that presentation itself preserves all durable state.
			status, _, _ := adminSend(client, "GET", "/admin/", nil)
			if status != 200 {
				t.Fatal("managing browser lost current admission", status)
			}
			before := snapshot()
			expectTime(storedValues(), want)
			profile, err := owner.Reader().Get(t.Context(), actor, target.ID)
			check(err)
			expectProfile(profile, want)
			status, _, body := send(client, "GET", path("accounts", target.ID), "", "", false)
			var detail struct{ Fields map[string]json.RawMessage }
			check(json.Unmarshal(body, &detail))
			if status != 200 {
				t.Fatal("selected LastLogin JSON read failed", status)
			}
			if role.LastLogin != "" {
				raw, present := detail.Fields[role.LastLogin]
				if !present {
					t.Fatal("selected LastLogin missing from JSON")
				}
				var actual *time.Time
				check(json.Unmarshal(raw, &actual))
				if (actual == nil) != (want == nil) || actual != nil && !actual.Equal(*want) {
					t.Fatal("JSON LastLogin differs from the native value")
				}
			} else if detail.Fields["last_login"] != nil || detail.Fields["seen"] != nil {
				t.Fatal("absent LastLogin acquired an implicit JSON field")
			}
			status, _, body = adminSend(client, "GET", endpoint, nil)
			if status != 200 {
				t.Fatal("LastLogin Admin page", status)
			}
			if role.LastLogin == "" {
				if strings.Contains(string(body), "Last login") {
					t.Fatal("absent LastLogin rendered Never instead of staying absent")
				}
			} else {
				label := "Never"
				if want != nil {
					label = want.UTC().Format(time.RFC3339Nano)
				}
				if !strings.Contains(string(body), `data-field-name="`+role.LastLogin+`">`+label) || strings.Contains(string(body), ` name="`+role.LastLogin+`"`) {
					t.Fatal("LastLogin lost its read-only selected-field display")
				}
				invalid := accountFields(loginName)
				invalid[profileField] = "forbidden"
				submit := prepareAdmin(client, endpoint, invalid, strconv.FormatInt(target.Revision, 10))
				status, _, body = submit()
				if status != 200 || !strings.Contains(string(body), `data-field-name="`+role.LastLogin+`">`+label) {
					t.Fatal("rejected edit lost the observed LastLogin", status)
				}
			}
			if !reflect.DeepEqual(before, snapshot()) {
				t.Fatal("LastLogin display changed account, membership, session or audit")
			}
		}
		original := storedValues()
		principal, _ := original[role.PrincipalID].String()
		before := snapshot()
		_, err := verifier.Authenticate(t.Context(), loginName, password)
		check(err)
		_, err = verifier.Resolve(t.Context(), principal)
		check(err)
		if !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("authentication or resolution wrote LastLogin")
		}
		assertDisplay(nil)
		visitor := newClient()
		loginToken := func(path string) string {
			t.Helper()
			status, _, page := adminSend(visitor, "GET", path, nil)
			match := adminToken.FindSubmatch(page)
			if status != 200 || len(match) != 2 {
				t.Fatal("native Admin login lost its real form", status)
			}
			return string(match[1])
		}
		token := loginToken("/admin/login/")
		before = snapshot()
		status, header, _ = adminSend(visitor, "POST", "/admin/login/", url.Values{"username": {loginName}, "password": {"wrong password"}, "csrfmiddlewaretoken": {token}, "next": {"/admin/"}})
		if status != 200 || header.Get("Location") != "" || !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("denied Admin login changed LastLogin or sessions", status)
		}
		var previous sessions.ID
		var previousDigest string
		var observed time.Time
		for attempt := 0; attempt < 2; attempt++ {
			elapsed.Add(int64(time.Second))
			source := "/admin/login/"
			if previous.Valid() {
				source = endpoint
			}
			token = loginToken(source)
			before = snapshot()
			at := clock()
			status, header, _ = adminSend(visitor, "POST", "/admin/login/", url.Values{"username": {loginName}, "password": {password}, "csrfmiddlewaretoken": {token}, "next": {"/admin/"}})
			if status != 302 || header.Get("Location") != "/admin/" {
				t.Fatal("native Admin login did not publish its session", status)
			}
			var current sessions.ID
			for _, cookie := range visitor.Jar.Cookies(baseURL) {
				if cookie.Name == websession.DefaultSessionCookieName {
					current, err = sessions.ParseID(cookie.Value)
					check(err)
				}
			}
			record, found, err := selected.Load(t.Context(), current)
			check(err)
			if !current.Valid() || current == previous || !found {
				t.Fatal("successful Admin login did not rotate its durable session")
			}
			if value, _ := record.Value(auth.SessionPrincipalIDKey); value != principal {
				t.Fatal("login stored another selected principal")
			}
			if previous.Valid() {
				_, found, err := selected.Load(t.Context(), previous)
				check(err)
				if found {
					t.Fatal("previous Admin login session survived rotation")
				}
			}
			after := snapshot()
			var oldValues, newValues map[string]query.Value
			before.Accounts, oldValues, _ = managementHTTPWithout(t, objects, before.Accounts, target.ID)
			after.Accounts, newValues, _ = managementHTTPWithout(t, objects, after.Accounts, target.ID)
			expected := maps.Clone(oldValues)
			if role.LastLogin != "" {
				expected[role.LastLogin] = query.DateTime(at)
			}
			if !reflect.DeepEqual(expected, newValues) {
				t.Fatal("login changed revision/profile/credential or missed LastLogin")
			}
			prior := make(map[string]sessionmodels.Session, len(before.Sessions))
			for _, row := range before.Sessions {
				prior[row.Digest] = row
			}
			var added string
			for _, row := range after.Sessions {
				if old, present := prior[row.Digest]; present {
					if !reflect.DeepEqual(old, row) {
						t.Fatal("login changed an unrelated session")
					}
					delete(prior, row.Digest)
				} else {
					if added != "" {
						t.Fatal("login inserted multiple sessions")
					}
					added = row.Digest
				}
			}
			if added == "" || previousDigest == "" && len(prior) != 0 || previousDigest != "" && (len(prior) != 1 || prior[previousDigest].Digest != previousDigest) {
				t.Fatal("login removed the wrong session set")
			}
			before.Sessions = after.Sessions
			if !reflect.DeepEqual(before, after) {
				t.Fatal("login changed unrelated rows, memberships or management audit")
			}
			previous, previousDigest, observed = current, added, at
			assertDisplay(&observed)
		}
		// A later ordinary access advances only session access metadata.
		elapsed.Add(int64(time.Second))
		beforeValues := storedValues()
		status, _, body = adminSend(visitor, "GET", endpoint, nil)
		if status != 200 || strings.Contains(string(body), ` name="`+role.Login+`"`) || !reflect.DeepEqual(beforeValues, storedValues()) {
			t.Fatal("view-only access changed LastLogin or gained editable fields", status)
		}
		assertDisplay(&observed)
		field := role.LastLogin
		if field == "" {
			field = "last_login"
		}
		forged := accountFields(loginName)
		forged[field] = "2000-01-01T00:00:00Z"
		submit := prepareAdmin(client, endpoint, forged, strconv.FormatInt(target.Revision, 10))
		before = snapshot()
		status, _, _ = submit()
		if status != 400 || !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("server-owned LastLogin became writable", status)
		}
		reopened, err := database.Open(t.Context(), backendConfig)
		check(err)
		defer func() {
			if err := reopened.Close(); err != nil {
				t.Error(err)
			}
		}()
		rebuilt, err := factory(t.Context(), reopened, hasher, overlay, accountmodel.RuntimeConfig[M, G, P]{Accounts: accountPolicy, Catalog: catalogPolicy, Sessions: sessions.Config{Clock: clock}})
		check(err)
		profile, err := rebuilt.Accounts().Reader().Get(t.Context(), actor, target.ID)
		check(err)
		expectProfile(profile, &observed)
		description, err := rebuilt.Accounts().Description()
		check(err)
		if description.LastLoginField != role.LastLogin {
			t.Fatal("reconstructed owner lost the selected observation role")
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
	run("self_password_keeps_confirmed_receipt", func(t *testing.T) {
		fields := accountFields("rescue@example.test")
		fields[role.Superuser.Field] = true
		status, header, value := send(client, "POST", "/api/identity/accounts/", creationBody(fields, "recovery administrator password"), "", true)
		decodeReceipt(status, header, value, 201)
		recovery := newClient()
		login(recovery, "rescue@example.test", "recovery administrator password")
		profile, err := owner.Reader().Get(t.Context(), actor, actorID)
		check(err)
		hashes := trackedHasher.calls.Load()
		status, header, value = send(client, "POST", path("accounts", actorID)+"password/", `{"credential":{"password":null}}`, strconv.FormatInt(profile.Revision(), 10), true)
		result := decodeReceipt(status, header, value, 200)
		_, found, err := selected.Load(t.Context(), actorSession)
		check(err)
		if result.ID != actorID || result.Revision != profile.Revision()+1 || found || trackedHasher.calls.Load() != hashes {
			t.Fatal("self-disable lost confirmed receipt or retained session")
		}
		status, _, _ = send(client, "GET", "/api/identity/accounts/", "", "", false)
		if status != 403 {
			t.Fatal("self-disabled browser retained management", status)
		}
		status, header, value = send(recovery, "POST", path("accounts", actorID)+"password/", encode(map[string]any{"credential": map[string]any{"password": accountPasswordText}}), strconv.FormatInt(result.Revision, 10), true)
		result = decodeReceipt(status, header, value, 200)
		if result.ID != actorID || result.Revision != profile.Revision()+2 || trackedHasher.calls.Load() != hashes+1 {
			t.Fatal("other administrator failed to restore selected account")
		}
		client = newClient()
		actorSession = login(client, actorLogin, accountPasswordText)
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
