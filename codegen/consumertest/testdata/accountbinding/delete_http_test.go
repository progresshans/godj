package consumer

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"example.com/godj-project-bundle/project"
	auditmodels "github.com/progresshans/godj/account/audit/models"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
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

// The host exposes the generated selected-account deletion owner over real
// TCP. Authentication uses independent browser cookies, and the target has
// two logged-in sessions before deletion. Automatic Admin/API registration
// remains a separate product assembly task.
func assertAccountDeletionHTTP[M any](t *testing.T, backend database.Backend, binding accountmodel.Binding[M], selected *accountmodel.SessionStore[M], hasher auth.PasswordHasher, actorLogin, actorPassword string, target M,
	factory func(*accountmodel.SessionStore[M], auth.Authorizer, accountmodel.AccountDeleteConfig) (*accountmodel.AccountDeleter[M], error), protect func() func()) {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	objects, err := binding.Objects()
	check(err)
	role, err := binding.Role()
	check(err)
	metadata, err := objects.Metadata()
	check(err)
	var key orm.AutoField[M]
	for _, field := range metadata.Fields {
		if field.PrimaryKey {
			key = orm.NewAutoField[M](field)
		}
	}
	value, present, err := objects.PrimaryKey(target)
	check(err)
	id, integer := value.Integer()
	if !present || !integer {
		t.Fatal("deletion consumer target key missing")
	}
	values, err := objects.ModelValues(target)
	check(err)
	targetLogin, _ := values[role.Login].String()
	secret, _ := values[role.Password].String()
	subject, _ := values[role.PrincipalID].String()
	now := time.Now().UTC().Truncate(time.Microsecond)
	clock := func() time.Time { return now }
	manager, err := sessions.NewManager(selected, sessions.Config{Clock: clock})
	check(err)
	verifier, err := selected.Authenticator(t.Context(), hasher)
	check(err)
	loginOwner, err := selected.LoginPersistence(manager)
	check(err)
	runtime, err := websession.New(websession.Config{Sessions: manager, Authenticator: verifier, Authorizer: auth.PrincipalAuthorizer{}, LoginPersistence: loginOwner, Clock: clock,
		SessionCookie: websession.CookieConfig{Path: "/", AllowInsecure: true}, CSRFCookie: websession.CookieConfig{Path: "/", AllowInsecure: true}, LoginPath: "/login/", FallbackPath: "/delete/", AllowedNextPaths: []string{"/delete/"}})
	check(err)
	policy := accountmodel.DeletionPermissions{Delete: "account.team", Change: "account.team"}
	owner, err := factory(selected, auth.PrincipalAuthorizer{}, accountmodel.AccountDeleteConfig{Account: policy, Group: policy, Permission: policy, Now: clock})
	check(err)
	deniedPolicy := policy
	deniedPolicy.Additional = []auth.Permission{"account.absent"}
	deniedOwner, err := factory(selected, auth.PrincipalAuthorizer{}, accountmodel.AccountDeleteConfig{Account: deniedPolicy, Group: policy, Permission: policy, Now: clock})
	check(err)
	var deny atomic.Bool
	var committed atomic.Pointer[accountmodel.AccountDeletion]
	routes := []web.Route{
		{Name: "account:delete_login_form", Method: "GET", Path: "/login/", Handler: func(request *web.Request) (web.Response, error) {
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
		{Name: "account:delete_login", Method: "POST", Path: "/login/", Handler: func(request *web.Request) (web.Response, error) {
			form, err := profileFormData(request)
			if err != nil {
				return web.Response{}, err
			}
			if err := runtime.VerifyCSRF(request, form["csrfmiddlewaretoken"]); err != nil {
				return web.HTML(403, []byte("CSRF rejected"))
			}
			result, err := runtime.Login(request, form.Get("username"), form.Get("password"))
			if err != nil {
				return web.Response{}, err
			}
			response, err := web.NewResponse(204, nil, nil)
			if err != nil {
				return web.Response{}, err
			}
			return result.Apply(response)
		}},
		{Name: "account:delete_probe", Method: "GET", Path: "/session/", Handler: runtime.Optional(func(_ *web.Request, principal auth.Principal) (web.Response, error) {
			status := 401
			if principal.Authenticated() {
				status = 204
			}
			return web.NewResponse(status, nil, nil)
		})},
		{Name: "account:delete", Method: "DELETE", Path: "/delete/", Handler: runtime.Require("account.team", func(request *web.Request, actor auth.Principal) (web.Response, error) {
			if err := runtime.VerifyCSRF(request, request.HTTP().Header.Values(runtime.CSRFHeader())); err != nil {
				return web.NewResponse(403, nil, nil)
			}
			versions := request.HTTP().Header.Values("If-Revision")
			if len(versions) != 1 {
				return web.NewResponse(400, nil, nil)
			}
			revision, err := strconv.ParseInt(versions[0], 10, 64)
			if err != nil || revision < 1 {
				return web.NewResponse(400, nil, nil)
			}
			selectedOwner := owner
			if deny.Load() {
				selectedOwner = deniedOwner
			}
			result, err := selectedOwner.Delete(request.Context(), actor, id, revision)
			if errors.Is(err, &accountmodel.Error{Code: accountmodel.CodePermission}) {
				return web.NewResponse(403, nil, nil)
			}
			if errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeConflict}) {
				return web.NewResponse(409, nil, nil)
			}
			var protected *query.ProtectedForeignKeyError
			if errors.As(err, &protected) {
				return web.NewResponse(409, nil, nil)
			}
			if err != nil {
				return web.Response{}, err
			}
			committed.Store(&result)
			return web.NewResponse(204, nil, nil)
		})},
	}
	configured, err := settings.New(settings.Definition{ProjectName: "account_delete_consumer", InstalledApps: append(project.InstalledApps(), apps.Config{Name: "example.com/godj-project-bundle/consumer", Label: "account"})})
	check(err)
	application, err := web.NewApplication(web.Config{Settings: configured, Routes: routes})
	check(err)
	server := httptest.NewServer(application)
	t.Cleanup(server.Close)
	newClient := func() *http.Client {
		jar, err := cookiejar.New(nil)
		check(err)
		return &http.Client{Jar: jar, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	send := func(client *http.Client, method, path, body, token, revision string) (int, string) {
		t.Helper()
		request, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, strings.NewReader(body))
		check(err)
		if body != "" {
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if token != "" {
			request.Header.Set(runtime.CSRFHeader(), token)
		}
		if revision != "" {
			request.Header.Set("If-Revision", revision)
		}
		response, err := client.Do(request)
		check(err)
		raw, err := io.ReadAll(response.Body)
		check(err)
		check(response.Body.Close())
		for _, private := range []string{secret, subject} {
			if private != "" && strings.Contains(string(raw), private) {
				t.Fatal("deletion HTTP exposed private account values")
			}
		}
		return response.StatusCode, string(raw)
	}
	login := func(client *http.Client, name, password string) (string, sessions.ID) {
		t.Helper()
		status, token := send(client, "GET", "/login/", "", "", "")
		if status != 200 || len(token) != 128 {
			t.Fatal("deletion login CSRF", status)
		}
		form := url.Values{"username": {name}, "password": {password}, "csrfmiddlewaretoken": {token}}
		status, _ = send(client, "POST", "/login/", form.Encode(), "", "")
		if status != 204 {
			t.Fatal("deletion browser login", status)
		}
		status, token = send(client, "GET", "/login/", "", "", "")
		if status != 200 {
			t.Fatal("deletion token refresh", status)
		}
		location, err := url.Parse(server.URL)
		check(err)
		for _, cookie := range client.Jar.Cookies(location) {
			if cookie.Name == websession.DefaultSessionCookieName {
				id, err := sessions.ParseID(cookie.Value)
				check(err)
				return token, id
			}
		}
		t.Fatal("deletion login session absent")
		return "", sessions.ID{}
	}
	actor, targetClient, otherTargetClient := newClient(), newClient(), newClient()
	token, actorSession := login(actor, actorLogin, actorPassword)
	_, targetSession := login(targetClient, targetLogin, accountPasswordText)
	_, otherTargetSession := login(otherTargetClient, targetLogin, accountPasswordText)
	snapshot := func() []byte {
		t.Helper()
		rows, err := objects.Using(backend).OrderBy(key.Asc()).All(t.Context())
		check(err)
		records, err := sessionmodels.SessionObjects.Using(backend).OrderBy(sessionmodels.SessionFields.ID.Asc()).All(t.Context())
		check(err)
		audits, err := auditmodels.EventObjects.Using(backend).OrderBy(auditmodels.EventFields.ID.Asc()).All(t.Context())
		check(err)
		wire, err := json.Marshal(struct {
			Rows     []M
			Sessions []sessionmodels.Session
			Audits   []auditmodels.Event
		}{rows, records, audits})
		check(err)
		return wire
	}
	for _, mode := range []string{"csrf", "stale", "additional_permission", "protected"} {
		if mode == "protected" && protect == nil {
			continue
		}
		t.Run(mode, func(t *testing.T) {
			var cleanup func()
			if mode == "protected" {
				cleanup = protect()
				defer cleanup()
			}
			csrf, revision, want := token, "1", 403
			if mode == "csrf" {
				csrf = ""
			}
			if mode == "stale" {
				revision, want = "2", 409
			}
			if mode == "protected" {
				want = 409
			}
			deny.Store(mode == "additional_permission")
			defer deny.Store(false)
			before := snapshot()
			status, _ := send(actor, "DELETE", "/delete/", "", csrf, revision)
			if status != want || !reflect.DeepEqual(before, snapshot()) {
				t.Fatal("rejected deletion changed durable state or response", mode, status)
			}
		})
	}
	t.Run("commit", func(t *testing.T) {
		status, _ := send(actor, "DELETE", "/delete/", "", token, "1")
		result := committed.Load()
		if status != 204 || result == nil || result.ID != id || result.Revision != 1 || result.RevokedSessions != 2 {
			t.Fatal("HTTP deletion result did not commit sessions and target", status)
		}
		if count, err := objects.Using(backend).Filter(key.Exact(id)).Count(t.Context()); err != nil || count != 0 {
			t.Fatal("HTTP deletion retained target", err)
		}
		for _, session := range []sessions.ID{targetSession, otherTargetSession} {
			if _, present, err := selected.Load(t.Context(), session); err != nil || present {
				t.Fatal("HTTP deletion retained target browser", err)
			}
		}
		if _, present, err := selected.Load(t.Context(), actorSession); err != nil || !present {
			t.Fatal("HTTP deletion revoked unrelated actor browser", err)
		}
		for _, client := range []*http.Client{targetClient, otherTargetClient} {
			if status, _ := send(client, "GET", "/session/", "", "", ""); status != 401 {
				t.Fatal("deleted browser remained authenticated", status)
			}
		}
		events, err := auditmodels.EventObjects.Using(backend).Filter(auditmodels.EventFields.Model.Exact(role.Model.AppLabel+"."+role.Model.ModelName), auditmodels.EventFields.ObjectID.Exact(id), auditmodels.EventFields.Action.Exact("delete")).All(t.Context())
		check(err)
		if len(events) != 1 {
			t.Fatal("HTTP deletion audit missing or duplicated")
		}
	})
}
