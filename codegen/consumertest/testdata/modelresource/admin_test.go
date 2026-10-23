package consumer

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"example.com/godj-project-bundle/inventory"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/crud"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

func registerAsset(t *testing.T, builder *admin.Builder, a assembly) {
	t.Helper()
	descriptor := inventory.AssetDescriptor{}
	model := descriptor.Metadata()
	projector, err := admin.NewModelProjector(model, descriptor.WriteFieldValue, append([]string{"id"}, a.resource.Fields()...)...)
	check(t, err)
	check(t, admin.RegisterModel(builder, admin.ModelConfig[inventory.Asset]{
		AppLabel: "inventory", Slug: "assets", Model: model,
		FormFields: a.resource.Fields(), ListFields: responseFields(), SearchFields: []string{"code"},
		Permissions: admin.Permissions{View: "inventory.view_asset", Add: editPermission(), Change: editPermission(), Delete: editPermission()},
		List: func(ctx context.Context, _ auth.Principal, request admin.ListRequest) (admin.Page[inventory.Asset], error) {
			options := crud.ListOptions[inventory.Asset]{Offset: request.Offset, Limit: request.Limit}
			if request.Search != "" {
				options.Filter = []orm.Predicate[inventory.Asset]{inventory.AssetFields.Code.IContains(request.Search)}
			}
			page, err := a.resource.List(ctx, a.backend, options)
			return admin.Page[inventory.Asset]{Items: page.Items, Total: page.Total, Offset: page.Offset, Limit: page.Limit}, err
		},
		Get: func(ctx context.Context, _ auth.Principal, id int64) (inventory.Asset, bool, error) {
			return a.resource.Get(ctx, a.backend, id)
		},
		Snapshot: func(value inventory.Asset) (admin.Object, error) {
			return projector.Project(value, value.ID, value.Code)
		},
		Initial: func(value inventory.Asset) (map[string]forms.Value, error) {
			return formmodel.InitialValues(model, a.form, value, descriptor.WriteFieldValue)
		},
		Create: func(ctx context.Context, _ auth.Principal, bound formmodel.BoundForm, _ admin.InlineSubmission) (inventory.Asset, error) {
			input, err := formmodel.PrepareInput(inventory.AssetObjects, bound)
			if err != nil {
				return inventory.Asset{}, err
			}
			return a.create(ctx, input)
		},
		Update: func(ctx context.Context, _ auth.Principal, mutation admin.Mutation, bound formmodel.BoundForm, _ admin.InlineSubmission) (inventory.Asset, []string, error) {
			input, err := formmodel.PrepareInput(inventory.AssetObjects, bound)
			if err != nil {
				return inventory.Asset{}, nil, err
			}
			result, err := a.update(ctx, mutation.ID, input)
			if err == crud.ErrNotFound {
				err = admin.ErrObjectNotFound
			}
			return result.Value, result.ChangedFields, err
		},
		Delete: func(ctx context.Context, _ auth.Principal, mutation admin.Mutation) (inventory.Asset, error) {
			return db.AtomicResult(ctx, a.backend, func(ctx context.Context, session db.Session) (inventory.Asset, error) {
				return a.resource.DeleteInSession(ctx, session, mutation.ID, crud.DeleteOptions[inventory.Asset]{Delete: func(ctx context.Context, session db.Session, value inventory.Asset) (int64, error) {
					return inventory.AssetObjects.Delete(ctx, session, &value)
				}})
			})
		},
	}))
}

var csrfPattern = regexp.MustCompile(`name="csrfmiddlewaretoken" value="([A-Za-z0-9_-]{128})"`)

func csrf(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	match := csrfPattern.FindStringSubmatch(response.Body.String())
	if len(match) != 2 {
		t.Fatal("rendered CSRF token absent", response.Code, response.Body.String())
	}
	return match[1]
}

func verifyAdmin(t *testing.T, backend resourceBackend) {
	probe := &observedBackend{resourceBackend: backend}
	a := newAssembly(t, probe, "admin")
	configured, err := settings.New(settings.Definition{ProjectName: "resource_admin", InstalledApps: []apps.Config{{Name: "example.com/godj-project-bundle/inventory", Label: "inventory"}}})
	check(t, err)
	builder := admin.NewBuilder(configured.Apps())
	registerAsset(t, builder, a)
	registry, err := builder.Build()
	check(t, err)
	model, found := registry.Lookup("inventory", "asset")
	if !found || model.Permissions.Change != editPermission() || !slices.Equal(model.ListFields, responseFields()) || len(model.FormFields) != len(a.resource.Fields()) {
		t.Fatal("Admin did not use shared explicit policy", model)
	}
	store, err := sessions.NewMemoryStore(16)
	check(t, err)
	manager, err := sessions.NewManager(store, sessions.Config{})
	check(t, err)
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10_000})
	check(t, err)
	encoded, err := hasher.Hash(t.Context(), "resource-test-password")
	check(t, err)
	var credentials []auth.Credential
	for _, actor := range []auth.Principal{principal(t, "writer", editPermission()), principal(t, "old", oldPermission)} {
		credential, err := auth.NewCredential(actor.ID(), encoded, actor)
		check(t, err)
		credentials = append(credentials, credential)
	}
	authenticator, err := auth.NewMemoryAuthenticator(credentials, hasher)
	check(t, err)
	allowed, err := admin.SiteAllowedNextPaths(registry, "/admin")
	check(t, err)
	runtime, err := sessionauth.New(sessionauth.Config{Sessions: manager, Authenticator: authenticator, Authorizer: auth.PrincipalAuthorizer{},
		SessionCookie: sessionauth.CookieConfig{Path: "/", AllowInsecure: true}, CSRFCookie: sessionauth.CookieConfig{Path: "/", AllowInsecure: true},
		LoginPath: "/admin/login/", FallbackPath: "/admin/", AllowedNextPaths: allowed})
	check(t, err)
	site, err := admin.NewSite(admin.SiteConfig{Apps: configured.Apps(), Namespace: "inventory", Registry: registry, Auth: runtime})
	check(t, err)
	application, err := web.NewApplication(web.Config{Settings: configured, Routes: site.Routes()})
	check(t, err)
	jar, err := cookiejar.New(nil)
	check(t, err)
	send := func(method, path string, values url.Values) (*httptest.ResponseRecorder, int64) {
		location, err := url.Parse("http://inventory.test" + path)
		check(t, err)
		body := &bodyProbe{Reader: strings.NewReader(values.Encode())}
		request := httptest.NewRequest(method, location.String(), body)
		if method == http.MethodPost {
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		for _, cookie := range jar.Cookies(location) {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		application.ServeHTTP(response, request)
		jar.SetCookies(location, response.Result().Cookies())
		return response, body.reads.Load()
	}
	login := func(actor string) {
		var err error
		jar, err = cookiejar.New(nil)
		check(t, err)
		page, _ := send(http.MethodGet, "/admin/login/", nil)
		if page.Code != http.StatusOK {
			t.Fatal("login page failed", page.Code)
		}
		response, _ := send(http.MethodPost, "/admin/login/", url.Values{"username": {actor}, "password": {"resource-test-password"}, "csrfmiddlewaretoken": {csrf(t, page)}})
		if response.Code != http.StatusFound {
			t.Fatal("login failed", response.Code, response.Body.String())
		}
	}
	login("old")
	beforeReads, beforeWrites := probe.reads.Load(), probe.writes.Load()
	oldPage, _ := send(http.MethodGet, "/admin/assets/add/", nil)
	if evolved {
		if oldPage.Code != http.StatusForbidden || probe.reads.Load() != beforeReads || probe.writes.Load() != beforeWrites {
			t.Fatal("changed Admin permission performed object I/O", oldPage.Code)
		}
		// Upload admission precedes allocating/reading multipart resources.
		// Ordinary HTML forms retain the existing body-token CSRF ordering.
		body := &bodyProbe{Reader: strings.NewReader("must not be parsed")}
		request := httptest.NewRequest(http.MethodPost, "http://inventory.test/admin/assets/add/", body)
		request.Header.Set("Content-Type", "multipart/form-data; boundary=not-read")
		for _, cookie := range jar.Cookies(request.URL) {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		application.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden || body.reads.Load() != 0 || probe.reads.Load() != beforeReads || probe.writes.Load() != beforeWrites {
			t.Fatal("denied Admin upload read its body or storage", response.Code, body.reads.Load())
		}
		index, _ := send(http.MethodGet, "/admin/", nil)
		response, reads := send(http.MethodPost, "/admin/assets/add/", url.Values{"code": {"old-admin"}, "csrfmiddlewaretoken": {csrf(t, index)}})
		if response.Code != http.StatusForbidden || reads == 0 || probe.reads.Load() != beforeReads || probe.writes.Load() != beforeWrites {
			t.Fatal("denied HTML form changed its CSRF/body or object boundary", response.Code, reads)
		}
	} else if oldPage.Code != http.StatusOK {
		t.Fatal("baseline Admin permission did not admit old actor", oldPage.Code)
	}
	login("writer")
	page, _ := send(http.MethodGet, "/admin/assets/add/", nil)
	if page.Code != http.StatusOK || strings.Contains(page.Body.String(), `data-field-name="priority"`) != evolved || strings.Contains(page.Body.String(), `data-field-name="stamp"`) {
		t.Fatal("rendered Form differs from explicit model fields", page.Code, page.Body.String())
	}
	beforeReads, beforeWrites = probe.reads.Load(), probe.writes.Load()
	// The HTML transport parses its body token before VerifyCSRF. A valid token
	// still cannot authorize a foreign Origin, and no object I/O is permitted.
	requestBody := &bodyProbe{Reader: strings.NewReader(url.Values{"code": {"csrf-denied"}, "csrfmiddlewaretoken": {csrf(t, page)}}.Encode())}
	request := httptest.NewRequest(http.MethodPost, "http://inventory.test/admin/assets/add/", requestBody)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "https://foreign.test")
	location, err := url.Parse("http://inventory.test/admin/assets/add/")
	check(t, err)
	for _, cookie := range jar.Cookies(location) {
		request.AddCookie(cookie)
	}
	rejected := httptest.NewRecorder()
	application.ServeHTTP(rejected, request)
	if rejected.Code != http.StatusForbidden || requestBody.reads.Load() == 0 || probe.reads.Load() != beforeReads || probe.writes.Load() != beforeWrites {
		t.Fatal("HTML CSRF denial changed its body-token or object boundary", rejected.Code, requestBody.reads.Load())
	}
	values := url.Values{"code": {"admin-asset"}, "enabled": {"on"}, "note": {"from HTML"}, "csrfmiddlewaretoken": {csrf(t, page)}}
	if evolved {
		values.Set("priority", "2")
		invalid, _ := send(http.MethodPost, "/admin/assets/add/", values)
		if invalid.Code != http.StatusOK || !strings.Contains(invalid.Body.String(), `data-error-field="priority"`) || probe.writes.Load() != beforeWrites {
			t.Fatal("added field validation did not reach Admin", invalid.Code)
		}
		values.Set("priority", "9")
		values.Set("csrfmiddlewaretoken", csrf(t, invalid))
	}
	created, _ := send(http.MethodPost, "/admin/assets/add/", values)
	if created.Code != http.StatusFound {
		t.Fatal("Admin create failed", created.Code, created.Body.String())
	}
	asset, found, err := a.resource.Query(backend).Filter(inventory.AssetFields.Code.Exact("admin-asset")).OrderBy(inventory.AssetFields.ID.Asc()).First(t.Context())
	check(t, err)
	if !found || asset.Stamp != "server" || asset.Note == nil || *asset.Note != "from HTML" {
		t.Fatal("Admin did not use common model preparation", asset)
	}
	if evolved {
		modelValues, err := inventory.AssetObjects.ModelValues(asset)
		check(t, err)
		if !modelValues["priority"].Equal(query.Integer(9)) {
			t.Fatal("Admin added field needed a handwritten converter")
		}
	}
	list, _ := send(http.MethodGet, "/admin/assets/?q=admin-asset", nil)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "admin-asset") || strings.Contains(list.Body.String(), "from HTML") == evolved {
		t.Fatal("Admin list projection differs from response policy", list.Code, list.Body.String())
	}
}
