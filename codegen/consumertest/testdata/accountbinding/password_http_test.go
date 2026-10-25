package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"example.com/godj-project-bundle/project"
	auditmodels "github.com/progresshans/godj/account/audit/models"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	identityaccount "github.com/progresshans/godj/identity/account"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/sessions"
	sessiondb "github.com/progresshans/godj/sessions/database"
	sessionmodels "github.com/progresshans/godj/sessions/database/models"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/settings/database"
	"github.com/progresshans/godj/web"
	websession "github.com/progresshans/godj/web/sessionauth"
)

type passwordHTTPHasher struct {
	auth.PasswordHasher
	hashes atomic.Int64
}

func (hasher *passwordHTTPHasher) Hash(ctx context.Context, raw string) (string, error) {
	hasher.hashes.Add(1)
	return hasher.PasswordHasher.Hash(ctx, raw)
}

// The shared account application owns the password Form/JSON parsing, error
// mapping, CSRF and responses. Login uses the selected authenticator directly;
// the account application's default-User login form is a separate I1 migration.
func assertAccountPasswordHTTP[M any](t *testing.T, backend database.Backend, binding accountmodel.Binding[M], selected *accountmodel.SessionStore[M], hasher auth.PasswordHasher, login string, row M, policy accountmodel.PasswordValidator[M]) {
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
	storedKey, present, err := objects.PrimaryKey(row)
	check(err)
	id, integer := storedKey.Integer()
	if !present || !integer {
		t.Fatal("password consumer has no stored account key")
	}
	readRow := func() M {
		t.Helper()
		current, present, err := objects.Using(backend).Filter(key.Exact(id)).OrderBy(key.Asc()).First(t.Context())
		check(err)
		if !present {
			t.Fatal("selected password account disappeared")
		}
		return current
	}
	type durable struct {
		rows     []M
		sessions []sessionmodels.Session
		audits   []auditmodels.Event
	}
	snapshot := func() durable {
		t.Helper()
		rows, err := objects.Using(backend).OrderBy(key.Asc()).All(t.Context())
		check(err)
		records, err := sessionmodels.SessionObjects.Using(backend).OrderBy(sessionmodels.SessionFields.ID.Asc()).All(t.Context())
		check(err)
		audits, err := auditmodels.EventObjects.Using(backend).OrderBy(auditmodels.EventFields.ID.Asc()).All(t.Context())
		check(err)
		return durable{rows, records, audits}
	}
	var now atomic.Int64
	now.Store(time.Now().UTC().Truncate(time.Microsecond).UnixNano())
	clock := func() time.Time { return time.Unix(0, now.Load()).UTC() }
	manager, err := sessions.NewManager(selected, sessions.Config{Clock: clock})
	check(err)
	observedHasher := &passwordHTTPHasher{PasswordHasher: hasher}
	verifier, err := selected.Authenticator(t.Context(), observedHasher)
	check(err)
	passwordOwner, err := selected.PasswordChangePersistence(manager, verifier, accountmodel.PasswordChangeConfig[M]{ValidatePassword: policy})
	check(err)
	loginOwner, err := selected.LoginPersistence(manager)
	check(err)
	next, err := identityaccount.AllowedNextPaths("")
	check(err)
	runtime, err := websession.New(websession.Config{Sessions: manager, Authenticator: verifier, Authorizer: auth.PrincipalAuthorizer{}, LoginPersistence: loginOwner, PasswordChangePersistence: passwordOwner, Clock: clock,
		SessionCookie: websession.CookieConfig{Path: "/", AllowInsecure: true}, CSRFCookie: websession.CookieConfig{Path: "/", AllowInsecure: true},
		LoginPath: "/selected-login/", FallbackPath: "/account/password/", AllowedNextPaths: next})
	check(err)
	configured, err := settings.New(settings.Definition{ProjectName: "selected_account_password", InstalledApps: append(project.InstalledApps(), apps.Config{Name: "example.com/godj-project-bundle/consumer", Label: "account"})})
	check(err)
	surface, err := identityaccount.New(identityaccount.Config{Apps: configured.Apps(), Namespace: "account", Auth: runtime})
	check(err)
	routes := append(surface.Routes(),
		web.Route{Name: "account:selected-login-token", Method: "GET", Path: "/selected-login/", Handler: func(request *web.Request) (web.Response, error) {
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
		web.Route{Name: "account:selected-login", Method: "POST", Path: "/selected-login/", Handler: func(request *web.Request) (web.Response, error) {
			data, err := profileFormData(request)
			if err != nil {
				return web.Response{}, err
			}
			if err := runtime.VerifyCSRF(request, data["csrfmiddlewaretoken"]); err != nil {
				return web.HTML(403, nil)
			}
			result, err := runtime.Login(request, data.Get("login"), data.Get("password"))
			if err == auth.ErrInvalidCredentials {
				return web.HTML(401, nil)
			}
			if err != nil {
				return web.Response{}, err
			}
			response, err := web.NewResponse(204, nil, nil)
			if err != nil {
				return web.Response{}, err
			}
			return result.Apply(response)
		}})
	application, err := web.NewApplication(web.Config{Settings: configured, Routes: routes, Middleware: surface.Middleware()})
	check(err)
	server := httptest.NewServer(application)
	t.Cleanup(server.Close)
	baseURL, err := url.Parse(server.URL)
	check(err)
	newClient := func() *http.Client {
		jar, err := cookiejar.New(nil)
		check(err)
		return &http.Client{Transport: server.Client().Transport, Timeout: 30 * time.Second, Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	client, sibling := newClient(), newClient()
	send := func(client *http.Client, method, path, raw, media, csrf, origin string) (int, http.Header, string) {
		t.Helper()
		request, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, strings.NewReader(raw))
		check(err)
		if media != "" {
			request.Header.Set("Content-Type", media)
		}
		if csrf != "" {
			request.Header.Set(runtime.CSRFHeader(), csrf)
		}
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		response, err := client.Do(request)
		check(err)
		payload, err := io.ReadAll(response.Body)
		check(err)
		check(response.Body.Close())
		return response.StatusCode, response.Header, string(payload)
	}
	cookie := func(client *http.Client, name string) string {
		for _, value := range client.Jar.Cookies(baseURL) {
			if value.Name == name {
				return value.Value
			}
		}
		return ""
	}
	current := func(client *http.Client) sessions.Record {
		t.Helper()
		id, err := sessions.ParseID(cookie(client, websession.DefaultSessionCookieName))
		check(err)
		record, found, err := selected.Load(t.Context(), id)
		check(err)
		if !found {
			t.Fatal("HTTP cookie has no committed session")
		}
		return record
	}
	logIn := func(client *http.Client, raw string) sessions.Record {
		t.Helper()
		status, _, token := send(client, "GET", "/selected-login/", "", "", "", "")
		if status != 200 || len(token) != 128 {
			t.Fatal("login CSRF missing", status)
		}
		data := url.Values{"login": {login}, "password": {raw}, "csrfmiddlewaretoken": {token}}
		status, _, _ = send(client, "POST", "/selected-login/", data.Encode(), "application/x-www-form-urlencoded", "", "")
		if status != 204 {
			t.Fatal("selected HTTP login failed", status)
		}
		return current(client)
	}
	previous := logIn(client, accountPasswordText)
	other := logIn(sibling, accountPasswordText)
	csrfCookie := cookie(client, websession.DefaultCSRFCookieName)
	csrfPattern := regexp.MustCompile(`name="csrfmiddlewaretoken" value="([^"]+)"`)
	status, _, markup := send(client, "GET", "/account/password/", "", "", "", "")
	match := csrfPattern.FindStringSubmatch(markup)
	if status != 200 || len(match) != 2 {
		t.Fatal("real account password form missing", status)
	}
	token := match[1]
	form := func(old, next, confirmation, csrf string) string {
		return url.Values{"old_password": {old}, "new_password1": {next}, "new_password2": {confirmation}, "csrfmiddlewaretoken": {csrf}}.Encode()
	}
	for _, test := range []struct {
		name, path, raw, media, csrf, origin string
		status                               int
		fields                               []string
	}{
		{"form_csrf", "/account/password/", form(accountPasswordText, "strong-next-password", "strong-next-password", ""), "application/x-www-form-urlencoded", "", "", 403, nil},
		{"form_origin", "/account/password/", form(accountPasswordText, "strong-next-password", "strong-next-password", token), "application/x-www-form-urlencoded", "", "https://foreign.test", 403, nil},
		{"form_confirmation", "/account/password/", form("wrong-password", "strong-next-password", "different-confirmation", token), "application/x-www-form-urlencoded", "", "", 200, []string{"old_password", "new_password2"}},
		{"form_combined", "/account/password/", form("wrong-password", "short", "short", token), "application/x-www-form-urlencoded", "", "", 200, []string{"old_password", "new_password2"}},
		{"form_target", "/account/password/", form(accountPasswordText, "strong-next-password", "strong-next-password", token) + "&target_id=1", "application/x-www-form-urlencoded", "", "", 400, nil},
		{"json_csrf_before_parse", "/api/account/password/", `{"unparsed`, "application/json", "", "", 403, nil},
		{"json_target", "/api/account/password/", `{"old_password":"old","new_password":"next","target_id":1}`, "application/json", token, "", 400, nil},
		{"json_duplicate", "/api/account/password/", `{"old_password":"old","old_password":"other","new_password":"next"}`, "application/json", token, "", 400, nil},
		{"json_null", "/api/account/password/", `{"old_password":null,"new_password":"next"}`, "application/json", token, "", 400, nil},
		{"json_combined", "/api/account/password/", `{"old_password":"wrong-password","new_password":"short"}`, "application/json", token, "", 400, []string{"old_password", "new_password"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := snapshot()
			observedHasher.hashes.Store(0)
			status, headers, payload := send(client, "POST", test.path, test.raw, test.media, test.csrf, test.origin)
			if status != test.status || headers.Get("Set-Cookie") != "" || observedHasher.hashes.Load() != 0 || !reflect.DeepEqual(snapshot(), before) {
				t.Fatal("password HTTP refusal changed account/session/audit or hashed", status, test.status)
			}
			for _, field := range test.fields {
				if !strings.Contains(payload, field) {
					t.Fatal("HTTP password diagnostic field missing", field)
				}
			}
			for _, secret := range []string{accountPasswordText, "wrong-password", "strong-next-password", "different-confirmation"} {
				if strings.Contains(payload, `value="`+secret+`"`) || strings.Contains(payload, `"`+secret+`"`) {
					t.Fatal("HTTP password response echoed secret")
				}
			}
		})
	}
	oldPassword := accountPasswordText
	for _, path := range []string{"form_success", "json_success"} {
		t.Run(path, func(t *testing.T) {
			if path == "json_success" {
				other = logIn(sibling, oldPassword)
			}
			before := readRow()
			beforeValues, err := objects.ModelValues(before)
			check(err)
			beforeAudit := len(snapshot().audits)
			observedHasher.hashes.Store(0)
			now.Add(int64(time.Minute))
			nextPassword := "changed-password-" + path
			var status int
			var headers http.Header
			if path == "form_success" {
				status, headers, _ = send(client, "POST", "/account/password/", form(oldPassword, nextPassword, nextPassword, token), "application/x-www-form-urlencoded", "", "")
				if status != 302 || headers.Get("Location") != "/account/password/done/" {
					t.Fatal("password form did not publish confirmed redirect", status)
				}
			} else {
				wire, err := json.Marshal(map[string]string{"old_password": oldPassword, "new_password": nextPassword})
				check(err)
				status, headers, _ = send(client, "POST", "/api/account/password/", string(wire), "application/json", token, "")
				if status != 204 {
					t.Fatal("password JSON did not commit", status)
				}
			}
			if headers.Get("Set-Cookie") == "" || observedHasher.hashes.Load() != 1 || cookie(client, websession.DefaultCSRFCookieName) != csrfCookie {
				t.Fatal("password response omitted rotation, rehashed or changed independent CSRF")
			}
			next := current(client)
			if next.ID() == previous.ID() || !next.CreatedAt().Equal(previous.CreatedAt()) || !next.AbsoluteExpiresAt().Equal(previous.AbsoluteExpiresAt()) {
				t.Fatal("HTTP password rotation reset absolute session lifetime")
			}
			credential, err := verifier.Authenticate(t.Context(), login, nextPassword)
			check(err)
			expected := previous.Values()
			expected[auth.SessionCredentialStampKey] = credential.SessionStamp()
			if !reflect.DeepEqual(next.Values(), expected) {
				t.Fatal("HTTP rotation lost selected session payload")
			}
			for _, revoked := range []sessions.Record{previous, other} {
				if record, found, err := selected.Load(t.Context(), revoked.ID()); err != nil || found || record.ID().Valid() {
					t.Fatal("HTTP password change retained earlier session", err)
				}
			}
			currentValues, err := objects.ModelValues(readRow())
			check(err)
			oldRevision, _ := beforeValues[role.Revision].Integer()
			revision, _ := currentValues[role.Revision].Integer()
			if revision != oldRevision+1 || currentValues[role.Password].Equal(beforeValues[role.Password]) || len(snapshot().audits) != beforeAudit+1 {
				t.Fatal("HTTP password change omitted actual account revision/hash/audit")
			}
			for name, value := range beforeValues {
				if name != role.Password && name != role.Revision && !currentValues[name].Equal(value) {
					t.Fatal("HTTP password update overwrote profile or last login", name)
				}
			}
			status, _, _ = send(sibling, "GET", "/api/account/csrf/", "", "", "", "")
			if status != 403 {
				t.Fatal("revoked browser remained authenticated", status)
			}
			status, _, _ = send(client, "GET", "/account/password/done/", "", "", "", "")
			if status != 200 {
				t.Fatal("rotated current browser was not authenticated", status)
			}
			oldPassword, previous = nextPassword, next
		})
	}
	// Rebuild selected owners from generated factories and resolve the exact
	// committed cookie. Process restart and a new DB connection are separate gates.
	reopened, err := project.NewAccountSessionStore(backend, sessiondb.Config{})
	check(err)
	if record, found, err := reopened.Load(t.Context(), previous.ID()); err != nil || !found || !reflect.DeepEqual(record.Snapshot(), previous.Snapshot()) {
		t.Fatal("rebound selected store lost committed cookie", err)
	}
	if _, err := verifier.Authenticate(t.Context(), login, accountPasswordText); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatal("original credential survived HTTP password changes", err)
	}
	logIn(newClient(), oldPassword)
}
