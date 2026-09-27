package identitytest

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	identityadmin "github.com/progresshans/godj/identity/admin"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

type identityAdminHTTP struct{ *identityHTTP }
type adminHTTPResult struct {
	status int
	header http.Header
	body   string
}

var adminCSRF = regexp.MustCompile(`name="csrfmiddlewaretoken" value="([A-Za-z0-9_-]{128})"`)
var adminRevision = regexp.MustCompile(`name="expected_revision" value="([0-9]+)"`)

func newIdentityAdminHTTP(t *testing.T, f *managementFixture, backend identityadmin.Backend, configure ...func(identityadmin.Config) identityadmin.Config) *identityAdminHTTP {
	t.Helper()
	policies := managementHost(t, f.backend)
	configured, err := settings.New(settings.Definition{ProjectName: "identity_admin", InstalledApps: []apps.Config{{Name: "github.com/progresshans/godj/identity/models", Label: "godj_identity"}}})
	if err != nil {
		t.Fatal(err)
	}
	config := identityadmin.NewConfig(backend, f.hasher, auth.PrincipalAuthorizer{}, identityadmin.DeletionPolicies{Users: policies.AccountsUser, Groups: policies.AccountsGroup, Permissions: policies.AccountsPermission})
	for _, option := range configure {
		config = option(config)
	}
	builder := admin.NewBuilder(configured.Apps())
	if err := identityadmin.Register(builder, config); err != nil {
		t.Fatal(err)
	}
	registry, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	paths, err := admin.SiteAllowedNextPaths(registry, "/admin")
	if err != nil {
		t.Fatal(err)
	}
	// Hold request access time fixed so byte-for-byte rollback assertions do
	// not conflate ordinary sliding session expiry with management effects.
	now := time.Now().UTC()
	manager, err := sessions.NewManager(f.runtime.SessionStore(), sessions.Config{Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	authRuntime, err := sessionauth.New(sessionauth.Config{Sessions: manager, Authenticator: f.runtime.Authenticator(), Authorizer: auth.PrincipalAuthorizer{}, SessionCookie: sessionauth.CookieConfig{Path: "/", AllowInsecure: true}, CSRFCookie: sessionauth.CookieConfig{Path: "/", AllowInsecure: true}, LoginPath: "/admin/login/", FallbackPath: "/admin/", AllowedNextPaths: paths})
	if err != nil {
		t.Fatal(err)
	}
	site, err := admin.NewSite(admin.SiteConfig{Apps: configured.Apps(), Namespace: "godj_identity", Registry: registry, Auth: authRuntime, PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	application, err := web.NewApplication(web.Config{Settings: configured, Routes: site.Routes()})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(application)
	t.Cleanup(server.Close)
	base := &identityHTTP{server: server}
	base.client = base.newClient(t)
	return &identityAdminHTTP{base}
}

func (h *identityAdminHTTP) call(t *testing.T, client *http.Client, method, path string, values url.Values, want int) adminHTTPResult {
	t.Helper()
	var body io.Reader
	if values != nil {
		body = strings.NewReader(values.Encode())
	}
	request, err := http.NewRequestWithContext(t.Context(), method, h.server.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	if values != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	closed := response.Body.Close()
	if err != nil || closed != nil {
		t.Fatal(err, closed)
	}
	if response.StatusCode != want {
		t.Fatalf("%s %s = %d, want %d: %s", method, path, response.StatusCode, want, data)
	}
	for _, marker := range []string{managementOldPassword, managementNewPassword, "private-admin-fault", "encoded_password", "principal_id"} {
		if strings.Contains(string(data), marker) {
			t.Fatal("Admin exposed private fields or diagnostics", marker)
		}
	}
	if want < 500 && response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("Admin response lost no-store")
	}
	return adminHTTPResult{response.StatusCode, response.Header.Clone(), string(data)}
}

func (h *identityAdminHTTP) loginAdmin(t *testing.T, client *http.Client, username, password string) {
	t.Helper()
	login := h.call(t, client, "GET", "/admin/login/", nil, 200)
	h.call(t, client, "POST", "/admin/login/", url.Values{"csrfmiddlewaretoken": {login.token(t)}, "username": {username}, "password": {password}, "next": {"/admin/"}}, 302)
}

func (r adminHTTPResult) token(t *testing.T) string {
	t.Helper()
	match := adminCSRF.FindStringSubmatch(r.body)
	if len(match) != 2 {
		t.Fatal("missing CSRF token")
	}
	return match[1]
}
func (r adminHTTPResult) revision(t *testing.T) int64 {
	t.Helper()
	match := adminRevision.FindStringSubmatch(r.body)
	if len(match) != 2 {
		t.Fatal("missing revision")
	}
	value, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func (r adminHTTPResult) contains(t *testing.T, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if !strings.Contains(r.body, marker) {
			t.Fatal("missing Admin content", marker, r.body)
		}
	}
}
func (r adminHTTPResult) excludes(t *testing.T, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if strings.Contains(r.body, marker) {
			t.Fatal("unexpected Admin content", marker)
		}
	}
}

func adminObjectPath(kind, action string, id int64) string {
	return "/admin/" + kind + "/" + action + "/?id=" + strconv.FormatInt(id, 10)
}

func (h *identityAdminHTTP) postForm(t *testing.T, path string, values url.Values, want int) adminHTTPResult {
	t.Helper()
	form := h.call(t, h.client, "GET", path, nil, 200)
	cloned := url.Values{}
	for name, items := range values {
		cloned[name] = append([]string(nil), items...)
	}
	values = cloned
	values.Set("csrfmiddlewaretoken", form.token(t))
	if strings.Contains(form.body, `name="confirm" value="yes"`) {
		values.Set("confirm", "yes")
	}
	if match := adminRevision.FindStringSubmatch(form.body); len(match) == 2 {
		if !values.Has("expected_revision") {
			values.Set("expected_revision", match[1])
		}
	}
	return h.call(t, h.client, "POST", path, values, want)
}
