package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"example.com/godj-project-bundle/project"
	auditmodels "github.com/progresshans/godj/account/audit/models"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	identityadmin "github.com/progresshans/godj/identity/admin"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	sessionmodels "github.com/progresshans/godj/sessions/database/models"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/settings/database"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
	websession "github.com/progresshans/godj/web/sessionauth"
)

// Real generated projects register the selected account directly. No manual
// CRUD callbacks, default User DTO, SQL adapter or hidden account row appears.
func assertAccountAdminHTTP[M, G, P any](t *testing.T, backend database.Backend, binding accountmodel.Binding[M], selected *accountmodel.SessionStore[M], hasher auth.PasswordHasher, original M, profileField, groupExtra, permissionExtra string,
	factory func(*accountmodel.SessionStore[M], *accountmodel.Authenticator[M], auth.Authorizer, accountmodel.AccountManagementConfig[M]) (*accountmodel.AccountManagement[M], error),
	catalogFactory func(*accountmodel.SessionStore[M], auth.Authorizer, accountmodel.CatalogConfig[G, P]) (*accountmodel.Catalog[G, P], error)) {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	role, err := binding.Role()
	check(err)
	objects, err := binding.Objects()
	check(err)
	values, err := objects.ModelValues(original)
	check(err)
	loginName, _ := values[role.Login].String()
	keyField, err := objects.PrimaryKeyField()
	check(err)
	key := orm.NewAutoField[M](keyField)
	fields := []string{role.Login, profileField}
	for _, name := range []string{role.Email, role.Active.Field, role.Staff.Field, role.Superuser.Field, role.Groups, role.Permissions} {
		if name != "" && !slices.Contains(fields, name) {
			fields = append(fields, name)
		}
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	clock := func() time.Time { return now }
	verifier, err := selected.Authenticator(t.Context(), hasher)
	check(err)
	credential, err := verifier.Authenticate(t.Context(), loginName, accountPasswordText)
	check(err)
	actor := credential.Principal()
	overlay := &catalogAdminOverlay{}
	owner, err := factory(selected, verifier, overlay, accountmodel.AccountManagementConfig[M]{
		ReadFields: fields, CreateFields: fields, EditFields: fields,
		Permissions:           accountmodel.AccountPermissions{View: "account.view", Add: "account.add", Change: "account.change", Delete: "account.delete", Additional: []auth.Permission{"account.manage"}},
		GroupPermissions:      accountmodel.DeletionPermissions{Delete: "catalog.delete_group", Change: "catalog.change_group"},
		PermissionPermissions: accountmodel.DeletionPermissions{Delete: "catalog.delete_permission", Change: "catalog.change_permission"},
		Now:                   clock, MaxPasswordBytes: 64,
		ValidatePassword: func(_ context.Context, raw string, candidate M) (validation.Errors, error) {
			values, err := objects.ModelValues(candidate)
			if err != nil {
				return validation.Errors{}, err
			}
			if secret, _ := values[role.Password].String(); secret != "" {
				return validation.Errors{}, errors.New("policy received encoded secret")
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
			active, _ := values[role.Active.Field].Boolean()
			profile, _ := values[profileField].String()
			if !active && profile != "KR" {
				return validation.NewErrors(validation.New(validation.NonField, "profile_required")), nil
			}
			return validation.Errors{}, nil
		},
	})
	check(err)
	groupFields, permissionFields := []string{role.Catalog.GroupName, role.Catalog.GroupPermissions}, []string{role.Catalog.PermissionCode, role.Catalog.PermissionName}
	if groupExtra != "" {
		groupFields = append(groupFields, groupExtra)
	}
	if permissionExtra != "" {
		permissionFields = append(permissionFields, permissionExtra)
	}
	catalog, err := catalogFactory(selected, auth.PrincipalAuthorizer{}, accountmodel.CatalogConfig[G, P]{
		Group:              accountmodel.CatalogModelConfig[G]{ReadFields: groupFields, WriteFields: groupFields, Permissions: accountmodel.CatalogPermissions{View: "catalog.view_group", Add: "catalog.add_group", Change: "catalog.change_group", Delete: "catalog.delete_group"}},
		Permission:         accountmodel.CatalogModelConfig[P]{ReadFields: permissionFields, WriteFields: permissionFields, Permissions: accountmodel.CatalogPermissions{View: "catalog.view_permission", Add: "catalog.add_permission", Change: "catalog.change_permission", Delete: "catalog.delete_permission"}},
		AccountPermissions: accountmodel.DeletionPermissions{Delete: "account.delete", Change: "account.change"}, Now: clock,
	})
	check(err)
	var groupID, permissionID int64
	for index := 0; index < 21; index++ {
		permission := map[string]query.Value{role.Catalog.PermissionCode: query.String(fmt.Sprintf("assigned.capability%d", index)), role.Catalog.PermissionName: query.String(fmt.Sprintf("Assignment %d", index))}
		if permissionExtra != "" {
			permission[permissionExtra] = query.String("Selected permission profile")
		}
		input, err := catalog.Permissions().Manager().Input(permission)
		check(err)
		created, err := catalog.Permissions().Create(t.Context(), actor, input)
		check(err)
		permissionID = created.ID
		group := map[string]query.Value{role.Catalog.GroupName: query.String(fmt.Sprintf("Assignment team %d", index))}
		if groupExtra != "" {
			group[groupExtra] = query.Integer(7)
		}
		groupInput, err := catalog.Groups().Manager().Input(group)
		check(err)
		groupInput, err = groupInput.WithCollections(map[string][]int64{role.Catalog.GroupPermissions: {permissionID}})
		check(err)
		created, err = catalog.Groups().Create(t.Context(), actor, groupInput)
		check(err)
		groupID = created.ID
	}
	configured, err := settings.New(settings.Definition{ProjectName: "selected_account_management", InstalledApps: append(project.InstalledApps(), apps.Config{Name: "example.com/godj-project-bundle/consumer", Label: "accountmanagement"})})
	check(err)
	builder := admin.NewBuilder(configured.Apps())
	check(identityadmin.RegisterAccount(builder, owner, identityadmin.AccountRegistration{Slug: "accounts", ListFields: []string{keyField.Name, role.Login, profileField, role.Revision}}))
	registry, err := builder.Build()
	check(err)
	if len(registry.All()) != 1 {
		t.Fatal("account registration introduced a hidden model")
	}
	paths, err := admin.SiteAllowedNextPaths(registry, "/admin")
	check(err)
	manager, err := sessions.NewManager(selected, sessions.Config{Clock: clock})
	check(err)
	persistence, err := selected.LoginPersistence(manager)
	check(err)
	runtime, err := websession.New(websession.Config{Sessions: manager, Authenticator: verifier, Authorizer: auth.PrincipalAuthorizer{}, LoginPersistence: persistence, Clock: clock,
		SessionCookie: websession.CookieConfig{Path: "/", AllowInsecure: true}, CSRFCookie: websession.CookieConfig{Path: "/", AllowInsecure: true}, LoginPath: "/admin/login/", FallbackPath: "/admin/", AllowedNextPaths: paths})
	check(err)
	site, err := admin.NewSite(admin.SiteConfig{Apps: configured.Apps(), Namespace: "accountmanagement", Registry: registry, Auth: runtime})
	check(err)
	application, err := web.NewApplication(web.Config{Settings: configured, Routes: site.Routes()})
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
	send := func(client *http.Client, method, path string, data url.Values) (int, http.Header, string) {
		t.Helper()
		request, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, strings.NewReader(data.Encode()))
		check(err)
		if method == "POST" {
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		response, err := client.Do(request)
		check(err)
		body, err := io.ReadAll(response.Body)
		check(err)
		check(response.Body.Close())
		return response.StatusCode, response.Header, string(body)
	}
	csrf := func(page string) string {
		t.Helper()
		match := regexp.MustCompile(`name="csrfmiddlewaretoken" value="([^"]+)"`).FindStringSubmatch(page)
		if len(match) != 2 {
			t.Fatal("account form lacks CSRF")
		}
		return html.UnescapeString(match[1])
	}
	get := func(client *http.Client, path string) string {
		t.Helper()
		status, _, body := send(client, "GET", path, nil)
		if status != 200 {
			t.Fatal("account page", path, status)
		}
		return body
	}
	login := func(client *http.Client, username, password string) sessions.ID {
		t.Helper()
		page := get(client, "/admin/login/")
		status, _, _ := send(client, "POST", "/admin/login/", url.Values{"username": {username}, "password": {password}, "csrfmiddlewaretoken": {csrf(page)}, "next": {"/admin/accounts/"}})
		if status != 302 {
			t.Fatal("selected account login failed", status)
		}
		for _, cookie := range client.Jar.Cookies(baseURL) {
			if cookie.Name == websession.DefaultSessionCookieName {
				id, err := sessions.ParseID(cookie.Value)
				check(err)
				return id
			}
		}
		t.Fatal("selected account session absent")
		return sessions.ID{}
	}
	client := newClient()
	login(client, loginName, accountPasswordText)
	type durable struct {
		rows        []M
		memberships []map[string][]int64
		sessions    []sessionmodels.Session
		audits      []auditmodels.Event
	}
	snapshot := func() durable {
		t.Helper()
		rows, err := objects.Using(backend).OrderBy(key.Asc()).All(t.Context())
		check(err)
		memberships := make([]map[string][]int64, len(rows))
		for index, row := range rows {
			value, _, err := objects.PrimaryKey(row)
			check(err)
			id, _ := value.Integer()
			profile, err := owner.Reader().Get(t.Context(), actor, id)
			check(err)
			memberships[index] = profile.View().Collections()
		}
		s, err := sessionmodels.SessionObjects.Using(backend).OrderBy(sessionmodels.SessionFields.ID.Asc()).All(t.Context())
		check(err)
		e, err := auditmodels.EventObjects.Using(backend).OrderBy(auditmodels.EventFields.ID.Asc()).All(t.Context())
		check(err)
		return durable{rows, memberships, s, e}
	}
	dataFor := func(login, profile string) url.Values {
		data := url.Values{role.Login: {login}, profileField: {profile}, role.Groups: {strconv.FormatInt(groupID, 10)}, role.Permissions: {strconv.FormatInt(permissionID, 10)}}
		if role.Email != "" && role.Email != role.Login {
			data.Set(role.Email, "Managed@EXAMPLE.TEST")
		}
		for _, name := range []string{role.Active.Field, role.Staff.Field, role.Superuser.Field} {
			if name != "" {
				data.Set(name, "on")
			}
		}
		return data
	}
	t.Run("bounded_choices", func(t *testing.T) {
		before := snapshot()
		for _, field := range []string{role.Groups, role.Permissions} {
			path := "/admin/accounts/add/choices/" + field + "/"
			status, headers, body := send(client, "GET", path+"?limit=1", nil)
			var page struct {
				Items         []struct{ Value, Label string }
				Offset, Limit int
				More          bool
			}
			check(json.Unmarshal([]byte(body), &page))
			if status != 200 || len(page.Items) != 1 || !page.More || page.Limit != 1 || headers.Get("Cache-Control") != "no-store" {
				t.Fatal("account assignment is not bounded/current", status)
			}
			overlay.denied.Store(auth.Permission("catalog.view_permission"))
			status, _, _ = send(client, "GET", path+"?search=20&limit=1", nil)
			overlay.denied.Store(auth.Permission(""))
			if status != 200 {
				t.Fatal("assignment required unrelated catalog view")
			}
		}
		if !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("choice read changed durable state")
		}
	})
	for _, mode := range []string{"partial_unique", "invalid_choice", "password_required", "password_mismatch", "password_bytes", "password_policy", "csrf", "protected_input", "additional_permission", "add_requires_change"} {
		t.Run(mode, func(t *testing.T) {
			path := "/admin/accounts/add/"
			page := get(client, path)
			data := dataFor("managed", "NZ")
			data.Set("password1", accountPasswordText)
			data.Set("password2", accountPasswordText)
			data.Set("csrfmiddlewaretoken", csrf(page))
			want, code := 200, ""
			switch mode {
			case "partial_unique":
				data.Set(role.Login, loginName)
				data.Set(role.Email, "invalid")
				code = "unique"
			case "invalid_choice":
				data.Set(role.Login, loginName)
				data.Set(role.Groups, "9223372036854775807")
				code = "invalid_choice"
			case "password_required":
				data.Set("password1", "")
				data.Set("password2", "")
				code = "required"
			case "password_mismatch":
				data.Set("password2", "different")
				code = "password_mismatch"
			case "password_bytes":
				data.Set("password1", strings.Repeat("界", 22))
				data.Set("password2", strings.Repeat("界", 22))
				code = "max_length"
			case "password_policy":
				data.Set("password1", "weak")
				data.Set("password2", "weak")
				code = "weak"
			case "csrf":
				data.Del("csrfmiddlewaretoken")
				want = 403
			case "protected_input":
				data.Set(role.Password, "forged")
				want = 400
			case "additional_permission":
				want = 403
			case "add_requires_change":
				want = 403
			}
			before := snapshot()
			if mode == "additional_permission" {
				overlay.denied.Store(auth.Permission("account.manage"))
			}
			if mode == "add_requires_change" {
				overlay.denied.Store(auth.Permission("account.change"))
			}
			status, headers, body := send(client, "POST", path, data)
			if mode == "additional_permission" || mode == "add_requires_change" {
				denied, _, _ := send(client, "GET", "/admin/accounts/add/choices/"+role.Groups+"/?limit=1", nil)
				if denied != 403 {
					t.Fatal("choice source omitted current account authority", denied)
				}
			}
			overlay.denied.Store(auth.Permission(""))
			if status != want || headers.Get("Location") != "" || !reflect.DeepEqual(before, snapshot()) {
				t.Fatal("rejected creation changed durable state", mode, status, want)
			}
			if code != "" && !strings.Contains(body, `data-error-code="`+code+`"`) {
				t.Fatal("account form lost diagnostic", mode)
			}
			if strings.Contains(body, accountPasswordText) || strings.Contains(body, `value="weak"`) {
				t.Fatal("creation refusal echoed password")
			}
			if mode == "invalid_choice" && !strings.Contains(body, `data-error-code="unique"`) {
				t.Fatal("bad relation suppressed login uniqueness")
			}
		})
	}
	var targetID, disabledID int64
	t.Run("create_usable", func(t *testing.T) {
		page := get(client, "/admin/accounts/add/")
		for _, field := range fields {
			if !strings.Contains(page, `name="`+field+`"`) {
				t.Fatal("selected account field missing", field)
			}
		}
		for _, field := range []string{role.Password, role.PrincipalID, role.Revision} {
			if strings.Contains(page, `name="`+field+`"`) {
				t.Fatal("server field became writable", field)
			}
		}
		data := dataFor("managed", "NZ")
		data.Set("password1", accountPasswordText)
		data.Set("password2", accountPasswordText)
		data.Set("csrfmiddlewaretoken", csrf(page))
		status, headers, _ := send(client, "POST", "/admin/accounts/add/", data)
		if status != 302 || !strings.HasPrefix(headers.Get("Location"), "/admin/accounts/") {
			t.Fatal("account creation not acknowledged", status)
		}
		rows, err := owner.Reader().List(t.Context(), actor, 0, 100)
		check(err)
		for _, row := range rows.Items {
			if name, _ := row.View().Values()[role.Login].String(); name == "managed" {
				targetID = row.ID()
			}
		}
		if targetID <= 0 {
			t.Fatal("selected account row missing")
		}
		row, err := owner.Reader().Get(t.Context(), actor, targetID)
		check(err)
		groups, _ := row.View().Collection(role.Groups)
		permissions, _ := row.View().Collection(role.Permissions)
		if row.Revision() != 1 || !slices.Equal(groups, []int64{groupID}) || !slices.Equal(permissions, []int64{permissionID}) {
			t.Fatal("created account lost selected memberships")
		}
		if len(snapshot().rows) != 2 {
			t.Fatal("creation inserted a hidden account")
		}
		_, err = verifier.Authenticate(t.Context(), "managed", accountPasswordText)
		check(err)
	})
	if targetID <= 0 {
		t.Fatal("usable account prerequisite failed")
	}
	path := "/admin/accounts/change/?id=" + strconv.FormatInt(targetID, 10)
	t.Run("create_unusable", func(t *testing.T) {
		data := dataFor("disabled", "NZ")
		data.Set("unusable_password", "on")
		data.Set("csrfmiddlewaretoken", csrf(get(client, "/admin/accounts/add/")))
		status, _, _ := send(client, "POST", "/admin/accounts/add/", data)
		if status != 302 {
			t.Fatal("explicit unusable creation failed", status)
		}
		rows, err := owner.Reader().List(t.Context(), actor, 0, 100)
		check(err)
		for _, row := range rows.Items {
			if name, _ := row.View().Values()[role.Login].String(); name == "disabled" {
				disabledID = row.ID()
			}
		}
		if disabledID <= 0 {
			t.Fatal("unusable row absent")
		}
		if _, err := verifier.Authenticate(t.Context(), "disabled", accountPasswordText); err == nil {
			t.Fatal("unusable credential authenticated")
		}
	})
	for _, mode := range []string{"stale", "edit_policy", "edit_additional", "choice_operation"} {
		t.Run(mode, func(t *testing.T) {
			page := get(client, path)
			before := snapshot()
			data := dataFor("managed", "NZ")
			data.Set("expected_revision", "1")
			data.Set("csrfmiddlewaretoken", csrf(page))
			want := 200
			switch mode {
			case "stale":
				data.Set("expected_revision", "2")
				want = 409
			case "edit_policy":
				data.Del(role.Active.Field)
			case "edit_additional":
				overlay.denied.Store(auth.Permission("account.manage"))
				want = 403
			case "choice_operation":
				overlay.denied.Store(auth.Permission("account.add"))
				status, _, _ := send(client, "GET", "/admin/accounts/add/choices/"+role.Permissions+"/?limit=1", nil)
				if status != 403 {
					t.Fatal("add choice ignored denied add grant", status)
				}
				get(client, "/admin/accounts/change/choices/"+role.Permissions+"/?id="+strconv.FormatInt(targetID, 10)+"&limit=1")
				overlay.denied.Store(auth.Permission(""))
				if !reflect.DeepEqual(before, snapshot()) {
					t.Fatal("operation choice changed state")
				}
				return
			}
			status, headers, body := send(client, "POST", path, data)
			overlay.denied.Store(auth.Permission(""))
			if status != want || headers.Get("Location") != "" || !reflect.DeepEqual(before, snapshot()) {
				t.Fatal("rejected edit changed durable state", mode, status, want)
			}
			if mode == "edit_policy" && !strings.Contains(body, `data-error-code="profile_required"`) {
				t.Fatal("typed final policy missing")
			}
		})
	}
	t.Run("update_and_history", func(t *testing.T) {
		page := get(client, path)
		if !strings.Contains(page, "Assignment team 20") || !strings.Contains(page, "Assignment 20") {
			t.Fatal("selected choice outside first page lost its label")
		}
		data := dataFor("managed", "KR")
		data.Set("expected_revision", "1")
		data.Set("csrfmiddlewaretoken", csrf(page))
		status, _, _ := send(client, "POST", path, data)
		if status != 302 {
			t.Fatal("selected profile edit failed", status)
		}
		row, err := owner.Reader().Get(t.Context(), actor, targetID)
		check(err)
		profile, _ := row.View().Values()[profileField].String()
		if row.Revision() != 2 || profile != "KR" {
			t.Fatal("typed profile edit not stored")
		}
		history := get(client, "/admin/accounts/history/?id="+strconv.FormatInt(targetID, 10))
		if !strings.Contains(history, profileField) || strings.Contains(history, accountPasswordText) {
			t.Fatal("selected account history missing or leaked password")
		}
		before := snapshot()
		data.Set("expected_revision", "2")
		data.Set("csrfmiddlewaretoken", csrf(get(client, path)))
		status, _, _ = send(client, "POST", path, data)
		if status != 302 || !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("no-op changed revision, memberships or audit", status)
		}
	})
	t.Run("password_command", func(t *testing.T) {
		sibling := newClient()
		bearer := login(sibling, "managed", accountPasswordText)
		command := "/admin/accounts/command/password/?id=" + strconv.FormatInt(targetID, 10)
		data := url.Values{"password1": {"replacement-password"}, "password2": {"replacement-password"}, "expected_revision": {"2"}, "csrfmiddlewaretoken": {csrf(get(client, command))}}
		status, _, _ := send(client, "POST", command, data)
		if status != 302 {
			t.Fatal("registered password command failed", status)
		}
		if _, present, err := selected.Load(t.Context(), bearer); err != nil || present {
			t.Fatal("password replacement retained target session", err)
		}
		if _, err := verifier.Authenticate(t.Context(), "managed", accountPasswordText); err == nil {
			t.Fatal("old password remained usable")
		}
		_, err := verifier.Authenticate(t.Context(), "managed", "replacement-password")
		check(err)
		row, err := owner.Reader().Get(t.Context(), actor, targetID)
		check(err)
		if row.Revision() != 3 {
			t.Fatal("password command revision missing")
		}
	})
	t.Run("self_deactivation", func(t *testing.T) {
		self, sibling := newClient(), newClient()
		bearers := []sessions.ID{login(self, "managed", "replacement-password"), login(sibling, "managed", "replacement-password")}
		data := dataFor("managed", "KR")
		data.Del(role.Active.Field)
		data.Set("expected_revision", "3")
		data.Set("csrfmiddlewaretoken", csrf(get(self, path)))
		status, headers, body := send(self, "POST", path, data)
		if status != 302 || headers.Get("Location") == "" || strings.Contains(body, "replacement-password") {
			t.Fatal("committed self-deactivation was not acknowledged", status)
		}
		for _, bearer := range bearers {
			if _, present, err := selected.Load(t.Context(), bearer); err != nil || present {
				t.Fatal("self-deactivation retained session", err)
			}
		}
		status, _, _ = send(sibling, "GET", "/admin/accounts/", nil)
		if status != 302 {
			t.Fatal("revoked sibling retained Admin access", status)
		}
		row, err := owner.Reader().Get(t.Context(), actor, targetID)
		check(err)
		active, _ := row.View().Values()[role.Active.Field].Boolean()
		if active || row.Revision() != 4 {
			t.Fatal("self-deactivation not stored")
		}
	})
	t.Run("delete_and_audit", func(t *testing.T) {
		for _, target := range []struct{ id, revision int64 }{{targetID, 4}, {disabledID, 1}} {
			path := "/admin/accounts/delete/?id=" + strconv.FormatInt(target.id, 10)
			status, _, _ := send(client, "POST", path, url.Values{"confirm": {"yes"}, "expected_revision": {strconv.FormatInt(target.revision, 10)}, "csrfmiddlewaretoken": {csrf(get(client, path))}})
			if status != 302 {
				t.Fatal("account deletion failed", status)
			}
			if _, err := owner.Reader().Get(t.Context(), actor, target.id); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeNotFound}) {
				t.Fatal("deleted account still visible", err)
			}
		}
		stored := snapshot()
		if len(stored.rows) != 1 {
			t.Fatal("account deletion left hidden rows")
		}
		var events []auditmodels.Event
		for _, event := range stored.audits {
			if event.Model == role.Model.AppLabel+"."+role.Model.ModelName && event.ObjectID == targetID {
				events = append(events, event)
			}
		}
		if len(events) != 5 {
			t.Fatal("account lifecycle did not append exactly add/edit/password/deactivation/delete", len(events))
		}
	})
}
