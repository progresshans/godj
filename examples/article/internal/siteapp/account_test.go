package siteapp

import (
	"github.com/progresshans/godj/examples/article/internal/operatorconfig"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/systemstate"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestArticleOrdinaryAccountPasswordSurfaceAndStaffSeparation(t *testing.T) {
	backend := openSiteAppStateBackend(t, "ordinary-account-composition")
	migrateSiteAppState(t, t.Context(), backend)
	config, err := operatorconfig.IdentityRuntimeConfig()
	if err != nil {
		t.Fatal(err)
	}
	principal, err := operatorconfig.InitialSuperuser()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := systemstate.ProvisionIdentity(t.Context(), backend, systemstate.ProvisionIdentityConfig{Username: "admin", Password: "admin-secret-password", Principal: principal, PasswordHasher: config.PasswordHasher}); err != nil {
		t.Fatal(err)
	}
	encoded, err := config.PasswordHasher.Hash(t.Context(), "ordinary secret password")
	if err != nil {
		t.Fatal(err)
	}
	user, err := models.UserObjects.Create(t.Context(), backend, models.NewUserCreate("ordinary", "ordinary", encoded, time.Now().UTC()))
	if err != nil {
		t.Fatal(err)
	}
	minimum, err := identity.NewMinimumLengthValidator(20)
	if err != nil {
		t.Fatal(err)
	}
	application, err := New(t.Context(), NewConfig(backend).WithLoopbackAuthentication().WithPasswordValidators(minimum))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(application)
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	call := func(method, path string, data url.Values, want int) string {
		t.Helper()
		var reader io.Reader
		if data != nil {
			reader = strings.NewReader(data.Encode())
		}
		request, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, reader)
		if err != nil {
			t.Fatal(err)
		}
		if data != nil {
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != want {
			t.Fatal("account composition response", path, response.StatusCode, string(body), err)
		}
		if path == "/admin/" && !strings.HasPrefix(response.Header.Get("Location"), "/admin/login/?next=") {
			t.Fatal("ordinary account bypassed staff login")
		}
		return string(body)
	}
	token := func(body string) string {
		match := regexp.MustCompile(`name="csrfmiddlewaretoken" value="([A-Za-z0-9_-]{128})"`).FindStringSubmatch(body)
		if len(match) != 2 {
			t.Fatal("CSRF missing")
		}
		return match[1]
	}
	body := call("GET", "/account/login/", nil, 200)
	call("POST", "/account/login/", url.Values{"username": {"ordinary"}, "password": {"ordinary secret password"}, "csrfmiddlewaretoken": {token(body)}}, 302)
	before, found, err := models.UserObjects.Using(backend).Filter(models.UserFields.ID.Exact(user.ID)).OrderBy(models.UserFields.ID.Asc()).First(t.Context())
	if err != nil || !found || before.LastLogin == nil {
		t.Fatal("ordinary login not stored", err)
	}
	call("GET", "/admin/", nil, 302)
	body = call("GET", "/account/password/", nil, 200)
	data := url.Values{"old_password": {"ordinary secret password"}, "new_password1": {"short"}, "new_password2": {"short"}, "csrfmiddlewaretoken": {token(body)}}
	invalid := call("POST", "/account/password/", data, 200)
	if !strings.Contains(invalid, `data-error-code="password_too_short"`) {
		t.Fatal("shared configured policy missing")
	}
	data.Set("new_password1", " newly changed ordinary password ")
	data.Set("new_password2", " newly changed ordinary password ")
	call("POST", "/account/password/", data, 302)
	call("GET", "/account/password/done/", nil, 200)
	call("GET", "/admin/", nil, 302)
	schema := call("GET", "/api/account/openapi.json", nil, 200)
	if !strings.Contains(schema, `"x-godj-authenticated-only":true`) {
		t.Fatal("account OpenAPI missing")
	}
	after, found, err := models.UserObjects.Using(backend).Filter(models.UserFields.ID.Exact(user.ID)).OrderBy(models.UserFields.ID.Asc()).First(t.Context())
	if err != nil || !found || after.Revision != 2 || !after.LastLogin.Equal(*before.LastLogin) || after.Staff || after.Superuser {
		t.Fatal("account persistence changed profile", err)
	}
}
