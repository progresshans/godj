package accountstoretest

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	hostproject "github.com/progresshans/godj/conformance/identityfixture/project"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
	websession "github.com/progresshans/godj/web/sessionauth"
)

type userReferenceHTTP struct {
	server      *httptest.Server
	client      *http.Client
	loginStatus int
}

func newUserReferenceHTTP(t *testing.T, selected *accountmodel.Runtime[models.User, models.Group, models.Permission]) *userReferenceHTTP {
	t.Helper()
	return newUserReferenceHTTPConfigured(t, selected, func() time.Time { return joined }, 204)
}

func newUserReferenceHTTPConfigured(t *testing.T, selected *accountmodel.Runtime[models.User, models.Group, models.Permission], clock func() time.Time, loginStatus int, extra ...func(*websession.Runtime) []web.Route) *userReferenceHTTP {
	t.Helper()
	runtime, err := websession.New(websession.Config{
		Sessions: selected.Sessions(), Authenticator: selected.Authenticator(), LoginPersistence: selected.LoginPersistence(), Authorizer: auth.PrincipalAuthorizer{},
		Clock: clock, SessionCookie: websession.CookieConfig{Path: "/", AllowInsecure: true}, CSRFCookie: websession.CookieConfig{Path: "/", AllowInsecure: true},
		LoginPath: "/login/", FallbackPath: "/change/", AllowedNextPaths: []string{"/view/", "/change/"},
	})
	if err != nil {
		t.Fatal(err)
	}
	routes := []web.Route{
		{Name: "userreference:login_page", Method: http.MethodGet, Path: "/login/", Handler: func(request *web.Request) (web.Response, error) {
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
		{Name: "userreference:login", Method: http.MethodPost, Path: "/login/", Handler: func(request *web.Request) (web.Response, error) {
			httpRequest := request.HTTP()
			httpRequest.Body = http.MaxBytesReader(nil, httpRequest.Body, 64<<10)
			if err := httpRequest.ParseForm(); err != nil {
				return web.Response{}, err
			}
			if err := runtime.VerifyCSRF(request, httpRequest.PostForm["csrfmiddlewaretoken"]); err != nil {
				return web.HTML(403, []byte("CSRF rejected"))
			}
			var result websession.LoginResult
			var err error
			if httpRequest.PostForm.Get("staff") == "required" {
				result, err = runtime.LoginStaff(request, httpRequest.PostForm.Get("username"), httpRequest.PostForm.Get("password"), "")
			} else {
				result, err = runtime.Login(request, httpRequest.PostForm.Get("username"), httpRequest.PostForm.Get("password"))
			}
			if err == auth.ErrInvalidCredentials {
				return web.HTML(401, []byte("invalid credentials"))
			}
			if err != nil {
				return web.Response{}, err
			}
			response, err := web.NewResponse(loginStatus, nil, nil)
			if err != nil {
				return web.Response{}, err
			}
			return result.Apply(response)
		}},
		{Name: "userreference:logout", Method: http.MethodPost, Path: "/logout/", Handler: func(request *web.Request) (web.Response, error) {
			httpRequest := request.HTTP()
			httpRequest.Body = http.MaxBytesReader(nil, httpRequest.Body, 64<<10)
			if err := httpRequest.ParseForm(); err != nil {
				return web.Response{}, err
			}
			if err := runtime.VerifyCSRF(request, httpRequest.PostForm["csrfmiddlewaretoken"]); err != nil {
				return web.HTML(403, []byte("CSRF rejected"))
			}
			change, err := runtime.Logout(request)
			if err != nil {
				return web.Response{}, err
			}
			response, err := web.NewResponse(200, nil, nil)
			if err != nil {
				return web.Response{}, err
			}
			return change.Apply(response)
		}},
	}
	for _, action := range []string{"view", "change"} {
		routes = append(routes, web.Route{Name: "userreference:" + action, Method: http.MethodGet, Path: "/" + action + "/", Handler: func(request *web.Request) (web.Response, error) {
			principal, err := runtime.Principal(request)
			if err != nil {
				return web.Response{}, err
			}
			allowed, err := runtime.Authorized(request.Context(), principal, auth.Permission("helpdesk.ticket."+action))
			if err != nil {
				return web.Response{}, err
			}
			if !allowed {
				return web.HTML(403, []byte("permission denied"))
			}
			return web.HTML(200, []byte("allowed"))
		}})
	}
	for _, additional := range extra {
		routes = append(routes, additional(runtime)...)
	}
	configured, err := settings.New(settings.Definition{ProjectName: "selected_user_reference", InstalledApps: append(hostproject.InstalledApps(), apps.Config{Name: "github.com/progresshans/godj/internal/accountstoretest", Label: "userreference"})})
	if err != nil {
		t.Fatal(err)
	}
	application, err := web.NewApplication(web.Config{Settings: configured, Routes: routes})
	if err != nil {
		t.Fatal(err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(application)
	client := server.Client()
	client.Jar, client.Timeout = jar, 30*time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &userReferenceHTTP{server: server, client: client, loginStatus: loginStatus}
}

func (browser *userReferenceHTTP) close() { browser.server.Close() }

func (browser *userReferenceHTTP) request(t *testing.T, method, path string, data url.Values, want int) string {
	t.Helper()
	status, body := browser.exchange(t, method, path, data)
	if status != want {
		t.Fatal("selected user HTTP response differs", status, want)
	}
	return body
}

func (browser *userReferenceHTTP) exchange(t *testing.T, method, path string, data url.Values) (int, string) {
	t.Helper()
	response, body := browser.send(t, method, path, data)
	return response.StatusCode, body
}

func (browser *userReferenceHTTP) send(t *testing.T, method, path string, data url.Values) (*http.Response, string) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, browser.server.URL+path, strings.NewReader(data.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	if data != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	response, err := browser.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 {
		t.Fatal("selected user HTTP response is incomplete", response.StatusCode, err)
	}
	for _, secret := range []string{password, userReferencePassword, "reference-created", "pbkdf2_sha256$"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("selected user HTTP disclosed credential data")
		}
	}
	return response, string(raw)
}

func (browser *userReferenceHTTP) login(t *testing.T, login, password string) sessions.ID {
	t.Helper()
	_, id := browser.loginAttempt(t, login, password, false, browser.loginStatus)
	return id
}

func (browser *userReferenceHTTP) loginAttempt(t *testing.T, login, password string, staff bool, want int) (*http.Response, sessions.ID) {
	t.Helper()
	token := browser.request(t, http.MethodGet, "/login/", nil, 200)
	if len(token) != 128 {
		t.Fatal("selected HTTP login lost CSRF token")
	}
	data := url.Values{"username": {login}, "password": {password}, "csrfmiddlewaretoken": {token}}
	if staff {
		data.Set("staff", "required")
	}
	response, _ := browser.send(t, http.MethodPost, "/login/", data)
	if response.StatusCode != want {
		t.Fatal("selected login response differs", response.StatusCode, want)
	}
	if want != 200 && want != 204 {
		return response, sessions.ID{}
	}
	address, err := url.Parse(browser.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, cookie := range browser.client.Jar.Cookies(address) {
		if cookie.Name == websession.DefaultSessionCookieName {
			id, err := sessions.ParseID(cookie.Value)
			if err != nil {
				t.Fatal(err)
			}
			return response, id
		}
	}
	t.Fatal("selected HTTP login did not issue a session cookie")
	return response, sessions.ID{}
}

func (browser *userReferenceHTTP) expect(t *testing.T, path string, status int) {
	t.Helper()
	browser.request(t, http.MethodGet, path, nil, status)
}
