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
	"sync/atomic"
	"testing"
	"time"

	"example.com/godj-project-bundle/project"
	accountaudit "github.com/progresshans/godj/account/audit"
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
	"github.com/progresshans/godj/web"
	websession "github.com/progresshans/godj/web/sessionauth"
)

type catalogAdminOverlay struct{ denied atomic.Value }

func (overlay *catalogAdminOverlay) Allowed(ctx context.Context, actor auth.Principal, permission auth.Permission) (bool, error) {
	if denied, _ := overlay.denied.Load().(auth.Permission); denied == permission {
		return false, nil
	}
	return (auth.PrincipalAuthorizer{}).Allowed(ctx, actor, permission)
}

// The same public registration runs with unrelated generated account, group,
// permission and through types. Real TCP requests own login/session/CSRF and
// render the selected model fields; no handwritten ModelConfig supplies CRUD.
func assertCatalogAdminHTTP[M, G, P any](t *testing.T, backend database.Backend, binding accountmodel.Binding[M], selected *accountmodel.SessionStore[M], hasher auth.PasswordHasher, row M, groupExtra, permissionExtra string,
	factory func(*accountmodel.SessionStore[M], auth.Authorizer, accountmodel.CatalogConfig[G, P]) (*accountmodel.Catalog[G, P], error)) {
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
	values, err := objects.ModelValues(row)
	check(err)
	login, _ := values[role.Login].String()
	groupFields := []string{role.Catalog.GroupName, role.Catalog.GroupPermissions}
	permissionFields := []string{role.Catalog.PermissionCode, role.Catalog.PermissionName}
	if groupExtra != "" {
		groupFields = append(groupFields, groupExtra)
	}
	if permissionExtra != "" {
		permissionFields = append(permissionFields, permissionExtra)
	}
	overlay := &catalogAdminOverlay{}
	now := time.Now().UTC().Truncate(time.Microsecond)
	clock := func() time.Time { return now }
	catalog, err := factory(selected, overlay, accountmodel.CatalogConfig[G, P]{
		Group:              accountmodel.CatalogModelConfig[G]{ReadFields: groupFields, WriteFields: groupFields, Permissions: accountmodel.CatalogPermissions{View: "catalog.view_group", Add: "catalog.add_group", Change: "catalog.change_group", Delete: "catalog.delete_group", Additional: []auth.Permission{"catalog.manage"}}},
		Permission:         accountmodel.CatalogModelConfig[P]{ReadFields: permissionFields, WriteFields: permissionFields, Permissions: accountmodel.CatalogPermissions{View: "catalog.view_permission", Add: "catalog.add_permission", Change: "catalog.change_permission", Delete: "catalog.delete_permission", Additional: []auth.Permission{"catalog.manage"}}},
		AccountPermissions: accountmodel.DeletionPermissions{Delete: "account.delete", Change: "account.change"}, Now: clock,
	})
	check(err)
	groups, permissions := catalog.Groups(), catalog.Permissions()
	configured, err := settings.New(settings.Definition{ProjectName: "selected_catalog_admin", InstalledApps: append(project.InstalledApps(), apps.Config{Name: "example.com/godj-project-bundle/consumer", Label: "catalogadmin"})})
	check(err)
	builder := admin.NewBuilder(configured.Apps())
	check(identityadmin.RegisterCatalog(builder, catalog, identityadmin.CatalogRegistration{Groups: identityadmin.CatalogPresentation{Slug: "teams"}, Permissions: identityadmin.CatalogPresentation{Slug: "grants"}}))
	registry, err := builder.Build()
	check(err)
	if len(registry.All()) != 2 {
		t.Fatal("catalog registration introduced hidden models")
	}
	for _, descriptor := range registry.All() {
		if !slices.Equal(descriptor.AdditionalPermissions, []auth.Permission{"catalog.manage"}) {
			t.Fatal("catalog additional policy lost in registration")
		}
	}
	paths, err := admin.SiteAllowedNextPaths(registry, "/admin")
	check(err)
	manager, err := sessions.NewManager(selected, sessions.Config{Clock: clock})
	check(err)
	verifier, err := selected.Authenticator(t.Context(), hasher)
	check(err)
	credential, err := verifier.Authenticate(t.Context(), login, accountPasswordText)
	check(err)
	actor := credential.Principal()
	loginOwner, err := selected.LoginPersistence(manager)
	check(err)
	runtime, err := websession.New(websession.Config{Sessions: manager, Authenticator: verifier, Authorizer: auth.PrincipalAuthorizer{}, LoginPersistence: loginOwner, Clock: clock,
		SessionCookie: websession.CookieConfig{Path: "/", AllowInsecure: true}, CSRFCookie: websession.CookieConfig{Path: "/", AllowInsecure: true}, LoginPath: "/admin/login/", FallbackPath: "/admin/", AllowedNextPaths: paths})
	check(err)
	site, err := admin.NewSite(admin.SiteConfig{Apps: configured.Apps(), Namespace: "catalogadmin", Registry: registry, Auth: runtime})
	check(err)
	application, err := web.NewApplication(web.Config{Settings: configured, Routes: site.Routes()})
	check(err)
	server := httptest.NewServer(application)
	t.Cleanup(server.Close)
	jar, err := cookiejar.New(nil)
	check(err)
	client := &http.Client{Transport: server.Client().Transport, Jar: jar, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	send := func(method, path string, data url.Values) (int, http.Header, string) {
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
			t.Fatal("catalog page lacks CSRF")
		}
		return html.UnescapeString(match[1])
	}
	get := func(path string) string {
		t.Helper()
		status, _, page := send("GET", path, nil)
		if status != 200 {
			t.Fatal("catalog page", path, status)
		}
		return page
	}
	page := get("/admin/login/")
	status, _, _ := send("POST", "/admin/login/", url.Values{"username": {login}, "password": {accountPasswordText}, "csrfmiddlewaretoken": {csrf(page)}, "next": {"/admin/teams/"}})
	if status != 302 {
		t.Fatal("catalog selected account login", status)
	}
	groupKey, err := groups.Manager().PrimaryKeyField()
	check(err)
	permissionKey, err := permissions.Manager().PrimaryKeyField()
	check(err)
	accountKey, err := objects.PrimaryKeyField()
	check(err)
	type durable struct {
		groups      []G
		permissions []P
		accounts    []M
		sessions    []sessionmodels.Session
		audits      []auditmodels.Event
	}
	snapshot := func() durable {
		t.Helper()
		g, err := groups.Manager().Using(backend).OrderBy(orm.NewAutoField[G](groupKey).Asc()).All(t.Context())
		check(err)
		p, err := permissions.Manager().Using(backend).OrderBy(orm.NewAutoField[P](permissionKey).Asc()).All(t.Context())
		check(err)
		a, err := objects.Using(backend).OrderBy(orm.NewAutoField[M](accountKey).Asc()).All(t.Context())
		check(err)
		s, err := sessionmodels.SessionObjects.Using(backend).OrderBy(sessionmodels.SessionFields.ID.Asc()).All(t.Context())
		check(err)
		e, err := auditmodels.EventObjects.Using(backend).OrderBy(auditmodels.EventFields.ID.Asc()).All(t.Context())
		check(err)
		return durable{g, p, a, s, e}
	}
	permissionData := func(code, name string) url.Values {
		data := url.Values{role.Catalog.PermissionCode: {code}, role.Catalog.PermissionName: {name}}
		if permissionExtra != "" {
			data.Set(permissionExtra, "Selected description")
		}
		return data
	}
	groupData := func(name string, keys ...int64) url.Values {
		data := url.Values{role.Catalog.GroupName: {name}}
		for _, key := range keys {
			data.Add(role.Catalog.GroupPermissions, strconv.FormatInt(key, 10))
		}
		if groupExtra != "" {
			data.Set(groupExtra, "7")
		}
		return data
	}
	var permissionID, groupID, lastID int64
	t.Run("create_permission", func(t *testing.T) {
		page := get("/admin/grants/add/")
		for _, name := range permissionFields {
			if !strings.Contains(page, `name="`+name+`"`) {
				t.Fatal("selected permission form field missing", name)
			}
		}
		data := permissionData("catalog.http", "HTTP permission")
		data.Set("csrfmiddlewaretoken", csrf(page))
		status, headers, _ := send("POST", "/admin/grants/add/", data)
		if status != 302 || !strings.HasPrefix(headers.Get("Location"), "/admin/grants/") {
			t.Fatal("permission create did not acknowledge commit", status)
		}
		pageResult, err := permissions.List(t.Context(), actor, 0, 100)
		check(err)
		if len(pageResult.Items) != 1 || pageResult.Items[0].Revision() != 1 {
			t.Fatal("selected permission not stored")
		}
		permissionID = pageResult.Items[0].ID()
	})
	t.Run("bounded_choices", func(t *testing.T) {
		for index := 0; index < 20; index++ {
			values := map[string]query.Value{role.Catalog.PermissionCode: query.String(fmt.Sprintf("catalog.page%d", index)), role.Catalog.PermissionName: query.String(fmt.Sprintf("Page permission %d", index))}
			if permissionExtra != "" {
				values[permissionExtra] = query.String("Selected description")
			}
			input, err := permissions.Manager().Input(values)
			check(err)
			created, err := permissions.Create(t.Context(), actor, input)
			check(err)
			lastID = created.ID
		}
		path := "/admin/teams/add/choices/" + role.Catalog.GroupPermissions + "/"
		status, headers, body := send("GET", path+"?limit=1", nil)
		var page struct {
			Items         []struct{ Value, Label string }
			Offset, Limit int
			More          bool
		}
		check(json.Unmarshal([]byte(body), &page))
		if status != 200 || len(page.Items) != 1 || page.Limit != 1 || !page.More || headers.Get("Cache-Control") != "no-store" || page.Items[0].Value != strconv.FormatInt(permissionID, 10) {
			t.Fatal("choice paging or deterministic selected key lost", status)
		}
		body = get(path + "?q=catalog.page19&limit=1")
		check(json.Unmarshal([]byte(body), &page))
		if len(page.Items) != 1 || page.More || page.Items[0].Value != strconv.FormatInt(lastID, 10) {
			t.Fatal("selected canonical code search failed")
		}
	})
	t.Run("create_group", func(t *testing.T) {
		page := get("/admin/teams/add/")
		if strings.Contains(page, "Page permission 19") {
			t.Fatal("unbounded initial choice enumeration")
		}
		data := groupData("HTTP group", lastID)
		data.Set("csrfmiddlewaretoken", csrf(page))
		status, _, _ := send("POST", "/admin/teams/add/", data)
		if status != 302 {
			t.Fatal("choice beyond first page not resolved for creation", status)
		}
		pageResult, err := groups.List(t.Context(), actor, 0, 100)
		check(err)
		if len(pageResult.Items) != 1 || pageResult.Items[0].Revision() != 1 {
			t.Fatal("selected group not stored")
		}
		groupID = pageResult.Items[0].ID()
		stored, err := groups.Get(t.Context(), actor, groupID)
		check(err)
		keys, present := stored.View().Collection(role.Catalog.GroupPermissions)
		if !present || !slices.Equal(keys, []int64{lastID}) {
			t.Fatal("form relation selection lost")
		}
	})
	if permissionExtra != "" {
		t.Run("partial_constraint", func(t *testing.T) {
			path := "/admin/grants/add/"
			data := permissionData("", "HTTP permission")
			data.Set("csrfmiddlewaretoken", csrf(get(path)))
			before := snapshot()
			status, headers, body := send("POST", path, data)
			if status != 200 || headers.Get("Location") != "" || !strings.Contains(body, `data-error-code="required"`) || !strings.Contains(body, `data-error-code="unique_together"`) || !reflect.DeepEqual(before, snapshot()) {
				t.Fatal("unrelated field rejection suppressed selected compound uniqueness", status)
			}
		})
	}
	groupPath := "/admin/teams/change/?id=" + strconv.FormatInt(groupID, 10)
	for _, mode := range []string{"partial_unique", "invalid_choice", "csrf", "stale", "protected_input", "additional_permission", "choice_operation"} {
		t.Run(mode, func(t *testing.T) {
			path := "/admin/grants/add/"
			data := permissionData("catalog.http", "Unique check")
			if mode == "partial_unique" {
				data.Set(role.Catalog.PermissionName, "")
			}
			if mode == "invalid_choice" {
				path, data = "/admin/teams/add/", groupData("HTTP group", 9_999_999)
			}
			if mode == "stale" {
				path, data = groupPath, groupData("HTTP group", lastID)
				data.Set("expected_revision", "2")
			}
			page := get(path)
			data.Set("csrfmiddlewaretoken", csrf(page))
			before := snapshot()
			want := 200
			switch mode {
			case "csrf":
				data.Del("csrfmiddlewaretoken")
				want = 403
			case "stale":
				want = 409
			case "protected_input":
				data.Set(permissionKey.Name, "123")
				want = 400
			case "additional_permission":
				overlay.denied.Store(auth.Permission("catalog.manage"))
				want = 403
			case "choice_operation":
				overlay.denied.Store(auth.Permission("catalog.add_group"))
				status, _, _ := send("GET", "/admin/teams/add/choices/"+role.Catalog.GroupPermissions+"/?limit=1", nil)
				if status != 403 {
					t.Fatal("choice source trusted stale admission", status)
				}
				get("/admin/teams/change/choices/" + role.Catalog.GroupPermissions + "/?id=" + strconv.FormatInt(groupID, 10) + "&limit=1")
				overlay.denied.Store(auth.Permission(""))
				if !reflect.DeepEqual(before, snapshot()) {
					t.Fatal("choice reads changed durable state")
				}
				return
			}
			status, headers, response := send("POST", path, data)
			if mode == "additional_permission" {
				denied, _, _ := send("GET", "/admin/teams/add/choices/"+role.Catalog.GroupPermissions+"/?limit=1", nil)
				if denied != 403 {
					t.Fatal("choice source omitted catalog extra grant", denied)
				}
				overlay.denied.Store(auth.Permission(""))
			}
			if status != want || headers.Get("Location") != "" || !reflect.DeepEqual(before, snapshot()) {
				t.Fatal("rejected catalog request changed durable state", mode, status, want)
			}
			if mode == "partial_unique" && (!strings.Contains(response, `data-error-code="unique"`) || !strings.Contains(response, `data-error-code="required"`)) {
				t.Fatal("invalid form suppressed valid code uniqueness")
			}
			if mode == "invalid_choice" && (!strings.Contains(response, `data-error-code="unique"`) || !strings.Contains(response, `data-error-code="invalid_choice"`)) {
				t.Fatal("relation rejection suppressed independent name uniqueness")
			}
		})
	}
	t.Run("update_and_history", func(t *testing.T) {
		page := get(groupPath)
		if !strings.Contains(page, "Page permission 19") {
			t.Fatal("current selection outside first page lost label")
		}
		data := groupData("Updated HTTP group", permissionID)
		data.Set("expected_revision", "1")
		data.Set("csrfmiddlewaretoken", csrf(page))
		status, _, _ := send("POST", groupPath, data)
		if status != 302 {
			t.Fatal("selected group update", status)
		}
		stored, err := groups.Get(t.Context(), actor, groupID)
		check(err)
		keys, _ := stored.View().Collection(role.Catalog.GroupPermissions)
		if stored.Revision() != 2 || !slices.Equal(keys, []int64{permissionID}) {
			t.Fatal("selected group conditional update lost")
		}
		page = get("/admin/teams/history/?id=" + strconv.FormatInt(groupID, 10))
		if !strings.Contains(page, role.Catalog.GroupName) || strings.Contains(page, accountPasswordText) {
			t.Fatal("durable selected-model history missing or leaks credentials")
		}
		path := "/admin/grants/change/?id=" + strconv.FormatInt(permissionID, 10)
		data = permissionData("catalog.http_updated", "Updated HTTP permission")
		data.Set("expected_revision", "1")
		data.Set("csrfmiddlewaretoken", csrf(get(path)))
		status, _, _ = send("POST", path, data)
		if status != 302 {
			t.Fatal("selected permission update", status)
		}
	})
	t.Run("delete_collected_membership", func(t *testing.T) {
		path := "/admin/grants/delete/?id=" + strconv.FormatInt(permissionID, 10)
		status, _, _ := send("POST", path, url.Values{"confirm": {"yes"}, "expected_revision": {"2"}, "csrfmiddlewaretoken": {csrf(get(path))}})
		if status != 302 {
			t.Fatal("permission deletion", status)
		}
		if _, err := permissions.Get(t.Context(), actor, permissionID); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeNotFound}) {
			t.Fatal("deleted permission remained visible", err)
		}
		stored, err := groups.Get(t.Context(), actor, groupID)
		check(err)
		keys, present := stored.View().Collection(role.Catalog.GroupPermissions)
		if !present || len(keys) != 0 || stored.Revision() != 3 {
			t.Fatal("permission deletion omitted group relation/revision lifecycle", stored.Revision())
		}
		path = "/admin/teams/delete/?id=" + strconv.FormatInt(groupID, 10)
		status, _, _ = send("POST", path, url.Values{"confirm": {"yes"}, "expected_revision": {"3"}, "csrfmiddlewaretoken": {csrf(get(path))}})
		if status != 302 {
			t.Fatal("group deletion", status)
		}
		if _, err := groups.Get(t.Context(), actor, groupID); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeNotFound}) {
			t.Fatal("deleted group remained visible", err)
		}
		namespace, _ := actor.CredentialDomain()
		for _, target := range []struct {
			model string
			id    int64
		}{{role.Catalog.Group.AppLabel + "." + role.Catalog.Group.ModelName, groupID}, {role.Catalog.Permission.AppLabel + "." + role.Catalog.Permission.ModelName, permissionID}} {
			var entries []accountaudit.Entry
			check(backend.ReadSnapshot(t.Context(), func(scope db.Queryer) error {
				var err error
				entries, err = accountaudit.HistoryIn(t.Context(), scope, namespace, target.model, target.id, 100)
				return err
			}))
			if len(entries) != 3 || entries[0].Action() != admin.ActionAdd || entries[1].Action() != admin.ActionChange || entries[2].Action() != admin.ActionDelete {
				t.Fatal("catalog HTTP lifecycle lost same-owner durable audit")
			}
		}
	})
}
