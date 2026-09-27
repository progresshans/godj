package siteapp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/examples/article/internal/operatorconfig"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/mail"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/web/sessionauth"
)

func TestArticleCompositionSharesResetPolicyAndNativePersistence(t *testing.T) {
	backend := openSiteAppStateBackend(t, "reset-composition")
	migrateSiteAppState(t, t.Context(), backend)
	runtimeConfig, err := operatorconfig.IdentityRuntimeConfig()
	if err != nil {
		t.Fatal(err)
	}
	principal, err := operatorconfig.InitialSuperuser()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = systemstate.ProvisionIdentity(t.Context(), backend, systemstate.ProvisionIdentityConfig{Username: "admin", Password: "initial admin secret password", Principal: principal, PasswordHasher: runtimeConfig.PasswordHasher}); err != nil {
		t.Fatal(err)
	}
	encoded, err := runtimeConfig.PasswordHasher.Hash(t.Context(), "old ordinary reset password")
	if err != nil {
		t.Fatal(err)
	}
	user, err := models.UserObjects.Create(t.Context(), backend, models.NewUserCreate("reset-user", "reset-user", encoded, time.Now().UTC()).WithEmail("reset@example.test"))
	if err != nil {
		t.Fatal(err)
	}
	minimum, err := identity.NewMinimumLengthValidator(20)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := identity.NewPasswordResetKeyRing(bytes.Repeat([]byte{39}, 32))
	if err != nil {
		t.Fatal(err)
	}
	outbox, err := mail.NewMemory(mail.MemoryConfig{})
	if err != nil {
		t.Fatal(err)
	}
	from, err := mail.ParseAddress("sender@example.test")
	if err != nil {
		t.Fatal(err)
	}
	var reports atomic.Int64
	cfg := NewConfig(backend).WithLoopbackAuthentication().WithPasswordValidators(minimum).WithPasswordReset(identity.PasswordResetConfig{Keys: keys}, outbox, identity.PasswordResetMailConfig{From: from, Origin: "https://reset.example.test"}, func(context.Context, error) { reports.Add(1) })
	application, err := New(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(application)
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	call := func(method, path, body, token string) (*http.Response, string) {
		t.Helper()
		request, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			request.Header.Set(sessionauth.DefaultCSRFHeader, token)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("Referrer-Policy") != "no-referrer" {
			t.Fatal("composition privacy headers")
		}
		return response, string(raw)
	}
	response, _ := call("GET", "/api/account/reset/", "", "")
	token := response.Header.Get(sessionauth.DefaultCSRFHeader)
	if response.StatusCode != 204 || token == "" {
		t.Fatal("anonymous bootstrap failed")
	}
	response, body := call("POST", "/api/account/reset/", `{"email":"reset@example.test"}`, token)
	if response.StatusCode != 204 {
		t.Fatal("mail request failed", body)
	}
	messages, err := outbox.Snapshot()
	if err != nil || len(messages) != 1 {
		t.Fatal("shared reset mail missing", err)
	}
	var link *url.URL
	for _, line := range strings.Split(messages[0].Message().Text(), "\n") {
		if strings.HasPrefix(line, "https://reset.example.test/") {
			link, err = url.Parse(line)
		}
	}
	if err != nil || link == nil {
		t.Fatal("mail link invalid")
	}
	response, body = call("GET", link.Path, "", "")
	if response.StatusCode != 302 || strings.Contains(body, strings.Split(link.Path, "/")[4]) {
		t.Fatal("entry failed or exposed token")
	}
	confirmation := response.Header.Get("Location")
	if !strings.HasSuffix(confirmation, "/set-password/") {
		t.Fatal("entry not hidden")
	}
	parts := strings.Split(link.Path, "/")
	apiPath := "/api/account/reset/" + parts[3] + "/"
	response, _ = call("GET", apiPath, "", "")
	if response.StatusCode != 204 {
		t.Fatal("shared proof not admitted")
	}
	token = response.Header.Get(sessionauth.DefaultCSRFHeader)
	response, body = call("POST", apiPath, `{"new_password":"longer-than-eight"}`, token)
	if response.StatusCode != 400 || !strings.Contains(body, "password_too_short") {
		t.Fatal("reset lost global Article password policy", body)
	}
	const password = "  replacement ordinary reset password  "
	payload, _ := json.Marshal(map[string]string{"new_password": password})
	response, body = call("POST", apiPath, string(payload), token)
	if response.StatusCode != 204 || len(response.Cookies()) != 1 || reports.Load() != 0 {
		t.Fatal("shared reset did not commit", response.StatusCode, body)
	}
	current, found, err := models.UserObjects.Using(backend).Filter(models.UserFields.ID.Exact(user.ID)).OrderBy(models.UserFields.ID.Asc()).First(t.Context())
	if err != nil || !found || current.Revision != user.Revision+1 || current.LastLogin != nil || current.Staff || current.Superuser {
		t.Fatal("reset changed profile or login state", err)
	}
	matched, err := runtimeConfig.PasswordHasher.Verify(t.Context(), password, current.EncodedPassword)
	if err != nil || !matched {
		t.Fatal("reset password not durable", err)
	}
	reopened, err := systemstate.OpenIdentity(t.Context(), backend, runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	history, err := reopened.AuditHistory(t.Context(), "godj_identity.user", user.ID, 10)
	if err != nil || len(history) != 1 || history[0].ActorID != user.PrincipalID {
		t.Fatal("reset audit not durable", err)
	}
	response, body = call("GET", "/api/account/openapi.json", "", "")
	if response.StatusCode != 200 || !strings.Contains(body, `"x-godj-csrf-only":true`) || !strings.Contains(body, `"x-godj-session-cookie-required":true`) {
		t.Fatal("anonymous reset document unavailable")
	}
}
