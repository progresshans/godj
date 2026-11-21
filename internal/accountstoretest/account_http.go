package accountstoretest

import (
	"context"
	"encoding/hex"
	"encoding/json"
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

	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/auth/passwordpolicy"
	hostproject "github.com/progresshans/godj/conformance/identityfixture/project"
	identityaccount "github.com/progresshans/godj/identity/account"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	identityreference "github.com/progresshans/godj/internal/identitytest/testdata"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

const selfPassword = "  replacement private password  "

type accountHTTP struct {
	f           *loginFixture
	runtime     *accountmodel.Runtime[models.User, models.Group, models.Permission]
	manager     *sessions.Manager
	now         *atomic.Int64
	server      *httptest.Server
	client      *http.Client
	begin, done chan struct{}
}

type denyAccountPermission struct{}

func (denyAccountPermission) Allowed(context.Context, auth.Principal, auth.Permission) (bool, error) {
	return false, nil
}

func newAccountHTTP(t *testing.T, f *loginFixture, backend Backend, policy accountmodel.PasswordValidator[models.User], extra ...func(*sessionauth.Runtime) []web.Route) *accountHTTP {
	t.Helper()
	minimum, err := passwordpolicy.NewMinimumLengthValidator(8)
	if err != nil {
		t.Fatal(err)
	}
	config := f.config(t)
	config.Accounts.ValidatePassword = func(ctx context.Context, raw string, row orm.ModelView[models.User]) (validation.Errors, error) {
		diagnostics := minimum.ValidatePassword(raw, nil)
		if policy == nil {
			return diagnostics, nil
		}
		additional, err := policy(ctx, raw, row)
		return validation.Join(diagnostics, additional), err
	}
	f.owner = newUserReferenceOwner(t, backend, f.userReferenceFixture, config)
	f.runtime = f.owner.runtime
	h := &accountHTTP{f: f, runtime: f.runtime, manager: f.runtime.Sessions(), now: &f.instant}
	next, err := identityaccount.AllowedNextPaths("")
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := sessionauth.New(sessionauth.Config{Sessions: h.manager, Authenticator: h.runtime.Authenticator(), Authorizer: denyAccountPermission{}, LoginPersistence: h.runtime.LoginPersistence(), PasswordChangePersistence: h.runtime.PasswordChangePersistence(), Clock: f.clock,
		LoginPath: "/account/login/", FallbackPath: "/account/password/", AllowedNextPaths: next,
		SessionCookie: sessionauth.CookieConfig{AllowInsecure: true}, CSRFCookie: sessionauth.CookieConfig{AllowInsecure: true}})
	if err != nil {
		t.Fatal(err)
	}
	configured, err := settings.New(settings.Definition{ProjectName: "selected_account_http", InstalledApps: append(hostproject.InstalledApps(), apps.Config{Name: "github.com/progresshans/godj/internal/accountstoretest", Label: "accountprobe"})})
	if err != nil {
		t.Fatal(err)
	}
	account, err := identityaccount.New(identityaccount.Config{Apps: configured.Apps(), Namespace: "accountprobe", Auth: runtime})
	if err != nil {
		t.Fatal(err)
	}
	routes := account.Routes()
	for _, additional := range extra {
		routes = append(routes, additional(runtime)...)
	}
	application, err := web.NewApplication(web.Config{Settings: configured, Routes: routes, Middleware: account.Middleware()})
	if err != nil {
		t.Fatal(err)
	}
	h.serve(t, application)
	return h
}

func (h *accountHTTP) serve(t *testing.T, application *web.Application) {
	t.Helper()
	// Join each actual TCP request with its fixture setup and native observation.
	// Network completion alone does not synchronize injected test callbacks.
	h.begin, h.done = make(chan struct{}, 1), make(chan struct{}, 1)
	h.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-h.begin
		defer func() { h.done <- struct{}{} }()
		application.ServeHTTP(w, r)
	}))
	t.Cleanup(h.server.Close)
	h.client = h.newClient(t)
}

func (h *accountHTTP) newClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := *h.server.Client()
	client.Jar, client.Timeout = jar, 30*time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client
}

func (h *accountHTTP) send(t *testing.T, client *http.Client, method, path, body, media string, headers http.Header) (*http.Response, string) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, h.server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header = headers.Clone()
	if request.Header == nil {
		request.Header = make(http.Header)
	}
	if media != "" {
		request.Header.Set("Content-Type", media)
	}
	select {
	case h.begin <- struct{}{}:
	case <-time.After(30 * time.Second):
		t.Fatal("previous account HTTP request did not consume its fixture setup")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (256<<10)+1))
	closeErr := response.Body.Close()
	select {
	case <-h.done:
	case <-time.After(30 * time.Second):
		t.Fatal("selected account HTTP handler did not acknowledge completion")
	case <-t.Context().Done():
		t.Fatal("selected account HTTP handler did not finish", t.Context().Err())
	}
	if err != nil || closeErr != nil || len(data) > 256<<10 {
		t.Fatal("incomplete account response", err, closeErr)
	}
	return response, string(data)
}

var accountCSRFPattern = regexp.MustCompile(`name="csrfmiddlewaretoken" value="([^"]+)"`)
var accountErrorPattern = regexp.MustCompile(`data-error-field="([^"]*)" data-error-code="([^"]*)"`)

func accountCSRF(t *testing.T, body string) string {
	t.Helper()
	values := accountCSRFPattern.FindStringSubmatch(body)
	if len(values) != 2 {
		t.Fatal("account form lacks CSRF input")
	}
	return values[1]
}
func accountErrors(body string) map[string][]string {
	result := map[string][]string{}
	for _, match := range accountErrorPattern.FindAllStringSubmatch(body, -1) {
		result[match[1]] = append(result[match[1]], match[2])
	}
	return result
}
func accountForm(old, next, confirm, token string) url.Values {
	return url.Values{"old_password": {old}, "new_password1": {next}, "new_password2": {confirm}, "csrfmiddlewaretoken": {token}}
}

func (h *accountHTTP) login(t *testing.T, client *http.Client, raw string) sessions.Record {
	t.Helper()
	response, body := h.send(t, client, "GET", "/account/login/", "", "", nil)
	if response.StatusCode != 200 {
		t.Fatal("login GET", response.StatusCode)
	}
	before := userReferenceSnapshot(t, h.f.backend)
	entropy := h.f.entropy.count()
	data := url.Values{"username": {"  ｍｅｍｂｅｒ  "}, "password": {raw}, "next": {"https://attacker.example/"}, "csrfmiddlewaretoken": {accountCSRF(t, body)}}
	response, body = h.send(t, client, "POST", "/account/login/", data.Encode(), "application/x-www-form-urlencoded", nil)
	if response.StatusCode != 302 || response.Header.Get("Location") != "/account/password/" {
		t.Fatal("ordinary selected login", response.StatusCode, body)
	}
	record := loginExpectedRecord(t, h.f.credential, sessions.Record{}, "create", h.f.entropy.one(t, entropy), h.f.clock())
	after := userReferenceSnapshot(t, h.f.backend)
	expected := loginExpectedState(t, before, after, h.f.credential, h.f.target, sessions.Record{}, record, h.f.clock())
	assertUserReferenceState(t, expected, after, h.f.actor.Principal())
	if !reflect.DeepEqual(h.current(t, client).Snapshot(), record.Snapshot()) {
		t.Fatal("account login cookie lost its exact native record")
	}
	at := h.f.clock().UTC().Truncate(time.Microsecond)
	h.f.target.LastLogin = &at
	h.f.targetSessions = append(h.f.targetSessions, record)
	return record
}

func (h *accountHTTP) current(t *testing.T, client *http.Client) sessions.Record {
	t.Helper()
	base, err := url.Parse(h.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, cookie := range client.Jar.Cookies(base) {
		if cookie.Name != sessionauth.DefaultSessionCookieName {
			continue
		}
		id, err := sessions.ParseID(cookie.Value)
		if err != nil {
			t.Fatal(err)
		}
		record, found, err := h.runtime.SessionStore().Load(t.Context(), id)
		if err != nil || !found {
			t.Fatal("cookie has no committed selected session", err)
		}
		return record
	}
	t.Fatal("no current account cookie")
	return sessions.Record{}
}

type passwordHTTPReference struct {
	Django, Python, Backend string
	Sources                 map[string]string `json:"source_sha256"`
	Observations            struct {
		Refusals map[string]struct {
			Errors            map[string][]string
			Status            int
			NewCookie         bool   `json:"new_cookie"`
			PasswordUnchanged bool   `json:"password_unchanged"`
			SessionUnchanged  bool   `json:"session_unchanged"`
			LastLogin         string `json:"last_login"`
		}
		Success      map[string]any
		SamePassword map[string]any `json:"same_password"`
		Failures     map[string]map[string]any
		Changes      map[string]map[string]any `json:"post_validation_change"`
	}
}

func readPasswordHTTPReference(t *testing.T, vendor string) passwordHTTPReference {
	t.Helper()
	raw, err := identityreference.PasswordChange(vendor)
	if err != nil {
		t.Fatal(err)
	}
	var reference passwordHTTPReference
	if err := json.Unmarshal(raw, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || reference.Python != "3.14.3" || reference.Backend != map[string]string{"sqlite": "sqlite", "postgres": "postgresql"}[vendor] || len(reference.Sources) != 9 || len(reference.Observations.Refusals) != 10 || len(reference.Observations.Failures) != 3 || len(reference.Observations.Changes) != 2 {
		t.Fatal("incomplete independent password HTTP reference")
	}
	for _, name := range []string{"auth", "backends", "base_user", "forms", "hashers", "models", "session_backend", "session_middleware", "views"} {
		value := reference.Sources[name]
		digest, err := hex.DecodeString(value)
		if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != value {
			t.Fatal("password HTTP reference lost a source digest", name)
		}
	}
	return reference
}

func assertPasswordReferenceDeviations(t *testing.T) {
	t.Helper()
	for _, vendor := range []string{"sqlite", "postgres"} {
		reference := readPasswordHTTPReference(t, vendor).Observations
		// Native selected failures keep the whole transaction unchanged and
		// reject a current password/inactive change. The pinned Django view
		// saves its cached user before cycling/saving the session instead.
		if reference.Failures["password_write"]["password_changed"] != false || reference.Failures["session_cycle"]["password_changed"] != true || reference.Failures["session_final_save"]["password_changed"] != true || reference.Changes["inactive"]["active"] != true || reference.Changes["password_changed"]["concurrent_password_valid"] != false || reference.Changes["password_changed"]["new_password_valid"] != true {
			t.Fatal("independent password transaction/current-fence differences changed", vendor)
		}
	}
}
