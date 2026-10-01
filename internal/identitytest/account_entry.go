package identitytest

import (
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/web/sessionauth"
)

func RunAccountEntryRefusals(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	backend, _ := open(t)
	f := newManagementFixture(t, backend, 0)
	ordinaryAccount(t, f)
	h := newAccountHTTP(t, f, backend)
	response, body := h.send(t, h.client, "GET", "/account/login/", "", "", nil)
	if response.StatusCode != 200 {
		t.Fatal("login form unavailable")
	}
	token := accountCSRF(t, body)
	cases := []struct {
		name        string
		status      int
		field, code string
	}{
		{"wrong_password", 200, "__all__", "invalid_login"}, {"unknown_username", 200, "__all__", "invalid_login"},
		{"inactive", 200, "__all__", "invalid_login"}, {"required", 200, "username", "required"},
		{"duplicate", 200, "username", "multiple"}, {"escaped_username", 200, "__all__", "invalid_login"},
		{"csrf", 403, "", ""}, {"origin", 403, "", ""}, {"query", 400, "", ""},
		{"duplicate_next", 400, "", ""}, {"unknown_field", 400, "", ""}, {"media", 415, "", ""},
	}
	for _, test := range cases {
		t.Run("login/"+test.name, func(t *testing.T) {
			if test.name == "inactive" {
				if _, err := models.UserObjects.Update(t.Context(), backend, f.user, models.UserPatch{}.WithActive(false)); err != nil {
					t.Fatal(err)
				}
				defer func() {
					if _, err := models.UserObjects.Update(t.Context(), backend, f.user, models.UserPatch{}.WithActive(true)); err != nil {
						t.Fatal(err)
					}
				}()
			}
			data := url.Values{"username": {"member"}, "password": {managementOldPassword}, "csrfmiddlewaretoken": {token}}
			path, media := "/account/login/", "application/x-www-form-urlencoded"
			headers := make(http.Header)
			switch test.name {
			case "wrong_password":
				data.Set("password", "wrong private password")
			case "unknown_username":
				data.Set("username", "unknown")
			case "required":
				data.Del("username")
			case "duplicate":
				data.Add("username", "duplicate")
			case "escaped_username":
				data.Set("username", `"><script>alert('login')</script>`)
			case "csrf":
				data.Set("csrfmiddlewaretoken", "invalid")
			case "origin":
				headers.Set("Origin", "https://attacker.example")
			case "query":
				path += "?next=/account/password/"
			case "duplicate_next":
				data["next"] = []string{"/account/password/", "https://attacker.example/"}
			case "unknown_field":
				data.Set("staff", "true")
			case "media":
				media = "application/json"
			}
			before := f.stored(t)
			rows, audit := snapshotIdentitySystemRows(t, backend)
			response, body := h.send(t, h.client, "POST", path, data.Encode(), media, headers)
			if response.StatusCode != test.status || len(response.Cookies()) != 0 || response.Header.Get("Cache-Control") != "no-store" {
				t.Fatal("login refusal publication", test.name, response.StatusCode)
			}
			if test.code != "" {
				if got := accountErrors(body)[test.field]; len(got) != 1 || got[0] != test.code {
					t.Fatal("login refusal diagnostics", test.name, accountErrors(body))
				}
			}
			if strings.Contains(body, managementOldPassword) || strings.Contains(body, "wrong private password") || strings.Contains(body, before.EncodedPassword) || strings.Contains(body, "<script>") {
				t.Fatal("login refusal exposed private or executable input")
			}
			if test.name == "escaped_username" && !strings.Contains(body, "&lt;script&gt;") {
				t.Fatal("username was dropped rather than escaped")
			}
			afterRows, afterAudit := snapshotIdentitySystemRows(t, backend)
			if f.hasher.calls.Load() != 0 || !reflect.DeepEqual(before, f.stored(t)) || !reflect.DeepEqual(rows, afterRows) || !reflect.DeepEqual(audit, afterAudit) {
				t.Fatal("refused login changed state")
			}
		})
	}
	h.login(t, h.client, managementOldPassword)
	response, _ = h.send(t, h.client, "GET", "/api/account/csrf/", "", "", nil)
	token = response.Header.Get(sessionauth.DefaultCSRFHeader)
	for _, mode := range []string{"get", "csrf", "origin", "duplicate_csrf", "unknown_field"} {
		t.Run("logout/"+mode, func(t *testing.T) {
			data := url.Values{"csrfmiddlewaretoken": {token}}
			headers := make(http.Header)
			method := "POST"
			want := 403
			switch mode {
			case "get":
				method = "GET"
				want = 405
			case "csrf":
				data.Set("csrfmiddlewaretoken", "invalid")
			case "origin":
				headers.Set("Origin", "https://attacker.example")
			case "duplicate_csrf":
				data.Add("csrfmiddlewaretoken", token)
			case "unknown_field":
				data.Set("username", "member")
				want = 400
			}
			before := f.stored(t)
			rows, audit := snapshotIdentitySystemRows(t, backend)
			response, _ := h.send(t, h.client, method, "/account/logout/", data.Encode(), "application/x-www-form-urlencoded", headers)
			if response.StatusCode != want || len(response.Cookies()) != 0 {
				t.Fatal("logout refusal publication", mode, response.StatusCode)
			}
			afterRows, afterAudit := snapshotIdentitySystemRows(t, backend)
			if !reflect.DeepEqual(before, f.stored(t)) || !reflect.DeepEqual(rows, afterRows) || !reflect.DeepEqual(audit, afterAudit) {
				t.Fatal("refused logout changed state")
			}
		})
	}
}
