package siteapp

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/auth/passwordpolicy"
	"github.com/progresshans/godj/examples/article/internal/accounttest"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web/sessionauth"
)

func TestArticleCompositionPublishesIdentityAdminWithDurableUserCreation(t *testing.T) {
	backend := openSiteAppStateBackend(t, "identity-admin-composition")
	migrateSiteAppState(t, t.Context(), backend)
	bootstrap, err := accounttest.Provision(t.Context(), backend, "admin", "admin-secret-password")
	if err != nil {
		t.Fatal(err)
	}
	minimum, err := passwordpolicy.NewMinimumLengthValidator(8)
	if err != nil {
		t.Fatal(err)
	}
	validators := []accountmodel.PasswordValidator[models.User]{func(_ context.Context, raw string, _ models.User) (validation.Errors, error) {
		return minimum.ValidatePassword(raw, nil), nil
	}}
	config := NewConfig(backend).WithLoopbackAuthentication().WithPasswordValidators(validators...)
	validators[0] = nil // Configuration must own its policy slice.
	application, err := New(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(application)
	defer server.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	call := func(method, path string, data url.Values, status int) string {
		t.Helper()
		var body io.Reader
		if data != nil {
			body = strings.NewReader(data.Encode())
		}
		request, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, body)
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
		content, err := io.ReadAll(response.Body)
		closed := response.Body.Close()
		if err != nil || closed != nil {
			t.Fatal(err, closed)
		}
		if response.StatusCode != status {
			t.Fatalf("%s %s = %d: %s", method, path, response.StatusCode, content)
		}
		for _, secret := range []string{"encoded_password", "admin-secret-password", "new-member-password"} {
			if strings.Contains(string(content), secret) {
				t.Fatal("composition exposed private input")
			}
		}
		return string(content)
	}
	token := func(body string) string {
		match := regexp.MustCompile(`name="csrfmiddlewaretoken" value="([A-Za-z0-9_-]{128})"`).FindStringSubmatch(body)
		if len(match) != 2 {
			t.Fatal("composition lost CSRF")
		}
		return match[1]
	}
	login := call("GET", "/admin/login/", nil, 200)
	call("POST", "/admin/login/", url.Values{"csrfmiddlewaretoken": {token(login)}, "username": {"admin"}, "password": {"admin-secret-password"}, "next": {"/admin/"}}, 302)
	index := call("GET", "/admin/", nil, 200)
	for _, path := range []string{"/admin/users/", "/admin/groups/", "/admin/permissions/", "/admin/articles/"} {
		if !strings.Contains(index, path) {
			t.Fatal("composition did not register model", path)
		}
		call("GET", path, nil, 200)
	}
	add := call("GET", "/admin/users/add/", nil, 200)
	rejected := call("POST", "/admin/users/add/", url.Values{"csrfmiddlewaretoken": {token(add)}, "username": {"Rejected"}, "password1": {"¹²³⁴⁵⁶⁷⁸"}, "password2": {"¹²³⁴⁵⁶⁷⁸"}}, 200)
	if !strings.Contains(rejected, `data-error-code="password_entirely_numeric"`) || strings.Contains(rejected, "¹²³⁴⁵⁶⁷⁸") {
		t.Fatal("Article Admin lost built-in policy or exposed password")
	}
	request, err := http.NewRequestWithContext(t.Context(), "POST", server.URL+"/api/identity/accounts/", strings.NewReader(`{"fields":{"username":"Rejected"},"credential":{"password":"¹²³⁴⁵⁶⁷⁸"}}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(sessionauth.DefaultCSRFHeader, token(rejected))
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(response.Body)
	closed := response.Body.Close()
	if err != nil || closed != nil || response.StatusCode != 400 || !strings.Contains(string(content), `"code":"password_entirely_numeric"`) || strings.Contains(string(content), "¹²³⁴⁵⁶⁷⁸") {
		t.Fatal("Article JSON API differs from Admin password policy", response.StatusCode, string(content), err, closed)
	}
	if count, err := models.UserObjects.Using(backend).Count(t.Context()); err != nil || count != 1 {
		t.Fatal("rejected Article password wrote user", err)
	}
	add = call("GET", "/admin/users/add/", nil, 200)
	call("POST", "/admin/users/add/", url.Values{"csrfmiddlewaretoken": {token(add)}, "username": {" Ｆｒｅｄ "}, "active": {"true"}, "password1": {"new-member-password"}, "password2": {"new-member-password"}}, 302)
	row, found, err := models.UserObjects.Using(backend).Filter(models.UserFields.Username.Exact("Fred")).OrderBy(models.UserFields.ID.Asc()).First(t.Context())
	if err != nil || !found || row.ID == bootstrap.Account.ID || row.PrincipalID == "" || !row.Active || row.Staff || row.Superuser {
		t.Fatal("composition did not persist safe new user", err)
	}
	hasher, err := auth.NewDefaultPBKDF2()
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := hasher.Verify(t.Context(), "new-member-password", row.EncodedPassword); err != nil || !ok {
		t.Fatal("composition did not persist valid password", err)
	}
}
