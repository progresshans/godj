package identitytest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
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
	"github.com/progresshans/godj/identity"
	identityaccount "github.com/progresshans/godj/identity/account"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

type accountHTTP struct {
	*identityHTTP
	runtime *systemstate.Runtime
	manager *sessions.Manager
	now     atomic.Int64
}

func newAccountHTTP(t *testing.T, f *managementFixture, backend TransitionBackend, validators ...identity.PasswordValidator) *accountHTTP {
	t.Helper()
	h := &accountHTTP{}
	h.now.Store(loginInstant.UnixNano())
	clock := func() time.Time { return time.Unix(0, h.now.Load()).UTC() }
	var err error
	h.runtime, err = systemstate.OpenIdentity(t.Context(), backend, systemstate.IdentityRuntimeConfig{PasswordHasher: f.hasher, MaxSessions: 512})
	if err != nil {
		t.Fatal(err)
	}
	h.manager, err = sessions.NewManager(h.runtime.SessionStore(), sessions.Config{Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	login, err := h.runtime.LoginPersistence(h.manager)
	if err != nil {
		t.Fatal(err)
	}
	minimum, err := identity.NewMinimumLengthValidator(8)
	if err != nil {
		t.Fatal(err)
	}
	password, err := h.runtime.PasswordChangePersistence(h.manager, append([]identity.PasswordValidator{minimum}, validators...)...)
	if err != nil {
		t.Fatal(err)
	}
	next, err := identityaccount.AllowedNextPaths("")
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := sessionauth.New(sessionauth.Config{Sessions: h.manager, Authenticator: h.runtime.Authenticator(), Authorizer: denyIdentityAuthorization{}, LoginPersistence: login, PasswordChangePersistence: password, Clock: clock,
		LoginPath: "/account/login/", FallbackPath: "/account/password/", AllowedNextPaths: next,
		SessionCookie: sessionauth.CookieConfig{AllowInsecure: true}, CSRFCookie: sessionauth.CookieConfig{AllowInsecure: true}})
	if err != nil {
		t.Fatal(err)
	}
	configured, err := settings.New(settings.Definition{ProjectName: "account_http", InstalledApps: []apps.Config{{Name: "github.com/progresshans/godj/internal/identitytest", Label: "accountprobe"}}})
	if err != nil {
		t.Fatal(err)
	}
	account, err := identityaccount.New(identityaccount.Config{Apps: configured.Apps(), Namespace: "accountprobe", Auth: runtime})
	if err != nil {
		t.Fatal(err)
	}
	application, err := web.NewApplication(web.Config{Settings: configured, Routes: account.Routes(), Middleware: account.Middleware()})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(application)
	t.Cleanup(server.Close)
	h.identityHTTP = &identityHTTP{server: server}
	h.client = h.newClient(t)
	f.hasher.calls.Store(0)
	return h
}

func (h *accountHTTP) send(t *testing.T, client *http.Client, method, path, body, media string, headers http.Header) (*http.Response, string) {
	t.Helper()
	request, err := http.NewRequest(method, h.server.URL+path, strings.NewReader(body))
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
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
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
func (h *accountHTTP) login(t *testing.T, client *http.Client, password string) sessions.Record {
	t.Helper()
	response, body := h.send(t, client, "GET", "/account/login/", "", "", nil)
	if response.StatusCode != 200 {
		t.Fatal("login GET", response.StatusCode, body)
	}
	data := url.Values{"username": {"  ｍｅｍｂｅｒ  "}, "password": {password}, "next": {"https://attacker.example/"}, "csrfmiddlewaretoken": {accountCSRF(t, body)}}
	response, body = h.send(t, client, "POST", "/account/login/", data.Encode(), "application/x-www-form-urlencoded", nil)
	if response.StatusCode != 302 || response.Header.Get("Location") != "/account/password/" {
		t.Fatal("ordinary login", response.StatusCode, body)
	}
	record := h.current(t, client)
	if f := record.ID().Valid(); !f {
		t.Fatal("login has no stored session")
	}
	return record
}
func (h *accountHTTP) current(t *testing.T, client *http.Client) sessions.Record {
	t.Helper()
	base, _ := url.Parse(h.server.URL)
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
			t.Fatal("cookie has no committed session", err)
		}
		return record
	}
	t.Fatal("no current cookie")
	return sessions.Record{}
}
func accountForm(old, next, confirm, token string) url.Values {
	return url.Values{"old_password": {old}, "new_password1": {next}, "new_password2": {confirm}, "csrfmiddlewaretoken": {token}}
}
func ordinaryAccount(t *testing.T, f *managementFixture) {
	t.Helper()
	if _, err := models.UserObjects.Update(t.Context(), f.backend, f.user, models.UserPatch{}.WithStaff(false)); err != nil {
		t.Fatal(err)
	}
}

func RunAccountHTTP(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	t.Run("native_form_errors_are_read_only_and_passwords_never_render", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 4)
		ordinaryAccount(t, f)
		h := newAccountHTTP(t, f, backend)
		h.login(t, h.client, managementOldPassword)
		h.now.Add(int64(time.Minute))
		rows, audit := snapshotIdentitySystemRows(t, backend)
		before := f.stored(t)
		response, body := h.send(t, h.client, "GET", "/account/password/", "", "", nil)
		if response.StatusCode != 200 {
			t.Fatal("password GET", response.StatusCode, body)
		}
		token := accountCSRF(t, body)
		cases := []struct{ name, old, next, confirm string }{
			{"wrong_old", "wrong", selfPassword, selfPassword},
			{"stripped_old", " " + managementOldPassword, selfPassword, selfPassword},
			{"mismatch", managementOldPassword, selfPassword, strings.TrimSpace(selfPassword)},
			{"weak", managementOldPassword, "short", "short"},
			{"wrong_old_weak", "wrong", "short", "short"},
			{"wrong_old_mismatch", "wrong", selfPassword, "different"},
			{"missing_old_weak", "", "short", "short"},
			{"missing_first_weak", managementOldPassword, "", "short"},
			{"missing_confirmation", managementOldPassword, selfPassword, ""},
			{"all_missing", "", "", ""},
		}
		for _, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				response, body := h.send(t, h.client, "POST", "/account/password/", accountForm(test.old, test.next, test.confirm, token).Encode(), "application/x-www-form-urlencoded", nil)
				if response.StatusCode != 200 || len(response.Cookies()) != 0 || response.Header.Get("Cache-Control") != "no-store" {
					t.Fatal("invalid form response", response.StatusCode)
				}
				for _, database := range []string{"sqlite", "postgres"} {
					payload, err := passwordChangeReferences.ReadFile("testdata/password-change-django61-" + database + ".json")
					if err != nil {
						t.Fatal(err)
					}
					var reference struct {
						Observations struct {
							Refusals map[string]struct{ Errors map[string][]string }
						}
					}
					if err := json.Unmarshal(payload, &reference); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(accountErrors(body), reference.Observations.Refusals[test.name].Errors) {
						t.Fatal("native errors diverged", test.name, accountErrors(body), reference.Observations.Refusals[test.name].Errors)
					}
				}
				for _, private := range []string{managementOldPassword, selfPassword, "value=\"short\"", "value=\"wrong\"", before.EncodedPassword} {
					if strings.Contains(body, private) {
						t.Fatal("password rendered")
					}
				}
				if f.hasher.calls.Load() != 0 {
					t.Fatal("invalid form performed new hash work")
				}
				afterRows, afterAudit := snapshotIdentitySystemRows(t, backend)
				if !reflect.DeepEqual(before, f.stored(t)) || !reflect.DeepEqual(rows, afterRows) || !reflect.DeepEqual(audit, afterAudit) {
					t.Fatal("invalid form touched durable state")
				}
			})
		}
	})
	for _, surface := range []string{"form", "json"} {
		t.Run("committed_lifecycle/"+surface, func(t *testing.T) {
			backend, other := open(t)
			f := newManagementFixture(t, backend, 4)
			ordinaryAccount(t, f)
			h := newAccountHTTP(t, f, backend)
			otherClient := h.newClient(t)
			otherRecord := h.login(t, otherClient, managementOldPassword)
			previous := h.login(t, h.client, managementOldPassword)
			withPayload, err := previous.WithValue("payload", "preserved private data")
			if err != nil {
				t.Fatal(err)
			}
			previous, err = h.manager.Rotate(t.Context(), withPayload)
			if err != nil {
				t.Fatal(err)
			}
			base, _ := url.Parse(h.server.URL)
			h.client.Jar.SetCookies(base, []*http.Cookie{{Name: sessionauth.DefaultSessionCookieName, Value: previous.ID().Encoded(), Path: "/"}})
			before := f.stored(t)
			response, _ := h.send(t, h.client, "GET", "/api/account/csrf/", "", "", nil)
			if response.StatusCode != 204 {
				t.Fatal("account csrf", response.StatusCode)
			}
			token := response.Header.Get(sessionauth.DefaultCSRFHeader)
			if token == "" {
				t.Fatal("missing API CSRF header")
			}
			h.now.Add(int64(time.Minute))
			headers := make(http.Header)
			headers.Set(sessionauth.DefaultCSRFHeader, token)
			for i := 0; i < 2; i++ {
				old := managementOldPassword
				if i == 1 {
					old = selfPassword
				}
				if surface == "form" {
					response, _ = h.send(t, h.client, "POST", "/account/password/", accountForm(old, selfPassword, selfPassword, token).Encode(), "application/x-www-form-urlencoded", nil)
				} else {
					payload, _ := json.Marshal(map[string]string{"old_password": old, "new_password": selfPassword})
					response, _ = h.send(t, h.client, "POST", "/api/account/password/", string(payload), "application/json", headers)
				}
				want := 204
				if surface == "form" {
					want = 302
				}
				if response.StatusCode != want || len(response.Cookies()) != 1 || response.Cookies()[0].Name != sessionauth.DefaultSessionCookieName {
					t.Fatal("committed response", surface, response.StatusCode)
				}
				if surface == "form" && response.Header.Get("Location") != "/account/password/done/" {
					t.Fatal("success redirect")
				}
				current := h.current(t, h.client)
				credential, err := h.runtime.Authenticator().Resolve(t.Context(), f.user.PrincipalID)
				if err != nil {
					t.Fatal(err)
				}
				assertSelfPasswordCommit(t, f, h.runtime, before, previous, auth.PasswordChangeResult{Record: current, Credential: credential}, selfPassword, int64(i+2))
				previous = current
				before = f.stored(t)
			}
			if f.hasher.calls.Load() != 2 {
				t.Fatal("hash repeated", f.hasher.calls.Load())
			}
			if before.LastLogin == nil || !before.LastLogin.Equal(loginInstant) {
				t.Fatal("self change altered last_login")
			}
			if _, found, err := h.runtime.SessionStore().Load(t.Context(), otherRecord.ID()); err != nil || found {
				t.Fatal("other login session survived", err)
			}
			response, _ = h.send(t, otherClient, "GET", "/api/account/csrf/", "", "", nil)
			if response.StatusCode != 403 {
				t.Fatal("revoked session admitted")
			}
			reopened, err := systemstate.OpenIdentity(t.Context(), other, systemstate.IdentityRuntimeConfig{PasswordHasher: f.hasher.PasswordHasher, MaxSessions: 512})
			if err != nil {
				t.Fatal(err)
			}
			credential, err := reopened.Authenticator().Authenticate(t.Context(), "member", selfPassword)
			if err != nil || credential.Principal().Staff() || credential.Principal().Has(identity.ChangeUser) {
				t.Fatal("reopen changed account", err)
			}
			if _, err := reopened.Authenticator().Authenticate(t.Context(), "member", managementOldPassword); err != auth.ErrInvalidCredentials {
				t.Fatal("old password still valid")
			}
			response, body := h.send(t, h.client, "GET", "/account/password/done/", "", "", nil)
			if response.StatusCode != 200 || !strings.Contains(body, "Password changed") {
				t.Fatal("done page")
			}
			response, _ = h.send(t, h.client, "POST", "/account/logout/", url.Values{"csrfmiddlewaretoken": {accountCSRF(t, body)}}.Encode(), "application/x-www-form-urlencoded", nil)
			if response.StatusCode != 302 || len(response.Cookies()) != 2 {
				t.Fatal("logout failed", response.StatusCode)
			}
			if _, found, err := reopened.SessionStore().Load(t.Context(), previous.ID()); err != nil || found {
				t.Fatal("logout not durable", err)
			}
		})
	}
}

func RunAccountHTTPRefusals(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	for _, surface := range []string{"form", "json"} {
		t.Run(surface, func(t *testing.T) {
			backend, _ := open(t)
			f := newManagementFixture(t, backend, 0)
			ordinaryAccount(t, f)
			h := newAccountHTTP(t, f, backend)
			original := h.login(t, h.client, managementOldPassword)
			response, _ := h.send(t, h.client, "GET", "/api/account/csrf/", "", "", nil)
			token := response.Header.Get(sessionauth.DefaultCSRFHeader)
			h.now.Add(int64(time.Minute))
			rows, audit := snapshotIdentitySystemRows(t, backend)
			before := f.stored(t)
			cases := []string{"csrf", "origin", "query", "empty_query", "unknown", "duplicate", "null", "wrong_type", "oversize_string", "oversize_body", "media", "wrong_old_weak", "duplicate_cookie", "stale", "expired", "anonymous"}
			for _, mode := range cases {
				t.Run(mode, func(t *testing.T) {
					headers := make(http.Header)
					headers.Set(sessionauth.DefaultCSRFHeader, token)
					path, body, media := "/api/account/password/", `{"old_password":"`+managementOldPassword+`","new_password":"`+selfPassword+`"}`, "application/json"
					if surface == "form" {
						path = "/account/password/"
						media = "application/x-www-form-urlencoded"
						body = accountForm(managementOldPassword, selfPassword, selfPassword, token).Encode()
					}
					want := 400
					client := h.client
					switch mode {
					case "csrf":
						headers.Del(sessionauth.DefaultCSRFHeader)
						body = strings.ReplaceAll(body, token, "bad")
						want = 403
					case "origin":
						headers.Set("Origin", "https://attacker.example")
						want = 403
					case "query":
						path += "?unexpected=1"
					case "empty_query":
						path += "?"
					case "unknown":
						if surface == "form" {
							body += "&target=manager"
						} else {
							body = strings.TrimSuffix(body, "}") + `,"target":"manager"}`
						}
					case "duplicate":
						if surface == "form" {
							body += "&old_password=wrong"
							want = 200
						} else {
							body = strings.TrimSuffix(body, "}") + `,"old_password":"wrong"}`
						}
					case "null":
						if surface == "form" {
							body += "&old_password=%00"
							want = 200
						} else {
							body = `{"old_password":null,"new_password":"next"}`
						}
					case "wrong_type":
						if surface == "form" {
							body += "&new_password1=duplicate"
							want = 200
						} else {
							body = `{"old_password":17,"new_password":"next"}`
						}
					case "oversize_string":
						if surface == "form" {
							body = accountForm(managementOldPassword, strings.Repeat("x", 4097), selfPassword, token).Encode()
							want = 413
						} else {
							body = `{"old_password":"` + strings.Repeat("x", 4097) + `","new_password":"next"}`
						}
					case "oversize_body":
						body = strings.Repeat("x", 65537)
						want = 413
					case "media":
						media = "text/plain"
						want = 415
					case "wrong_old_weak":
						if surface == "form" {
							body = accountForm("wrong", "short", "short", token).Encode()
							want = 200
						} else {
							body = `{"old_password":"wrong","new_password":"short"}`
						}
					case "duplicate_cookie":
						headers.Add("Cookie", sessionauth.DefaultSessionCookieName+"="+original.ID().Encoded())
						want = 403
						if surface == "form" {
							want = 302
						}
					case "stale", "expired", "anonymous":
						client = h.newClient(t)
						base, _ := url.Parse(h.server.URL)
						if mode != "anonymous" {
							id := original.ID()
							if mode == "stale" {
								record, err := h.manager.Create(t.Context(), map[string]string{auth.SessionPrincipalIDKey: f.user.PrincipalID, auth.SessionCredentialStampKey: "stale"})
								if err != nil {
									t.Fatal(err)
								}
								id = record.ID()
								defer h.manager.Delete(t.Context(), id)
							}
							client.Jar.SetCookies(base, []*http.Cookie{{Name: sessionauth.DefaultSessionCookieName, Value: id.Encoded(), Path: "/"}})
						}
						if mode == "expired" {
							h.now.Add(int64(400 * 24 * time.Hour))
							defer h.now.Add(-int64(400 * 24 * time.Hour))
						}
						want = 403
						if surface == "form" {
							want = 302
						}
					}
					// The stale-session fixture itself is an explicit setup write.
					expectedRows, expectedAudit := snapshotIdentitySystemRows(t, backend)
					response, body = h.send(t, client, "POST", path, body, media, headers)
					if response.StatusCode != want || len(response.Cookies()) != 0 {
						t.Fatal("refusal response", surface, mode, response.StatusCode, want, body)
					}
					if f.hasher.calls.Load() != 0 {
						t.Fatal("preflight hashed", mode)
					}
					afterRows, afterAudit := snapshotIdentitySystemRows(t, backend)
					if !reflect.DeepEqual(before, f.stored(t)) || !reflect.DeepEqual(expectedRows, afterRows) || !reflect.DeepEqual(expectedAudit, afterAudit) {
						t.Fatal("refusal mutated durable state", mode)
					}
				})
			}
			afterRows, afterAudit := snapshotIdentitySystemRows(t, backend)
			if !reflect.DeepEqual(rows, afterRows) || !reflect.DeepEqual(audit, afterAudit) {
				t.Fatal("fixture cleanup changed baseline")
			}
		})
	}
}

func RunAccountHTTPBoundaries(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	for _, surface := range []string{"form", "json"} {
		for _, mode := range []string{"password_write", "audit_insert", "revoke", "unknown_rollback", "unknown_commit", "cancel", "validation_cleanup"} {
			t.Run(surface+"/"+mode, func(t *testing.T) {
				backend, _ := open(t)
				f := newManagementFixture(t, backend, 4)
				ordinaryAccount(t, f)
				boundary := &passwordChangeBoundary{TransitionBackend: backend, mode: mode}
				var validationCalls atomic.Int64
				validator := identity.PasswordValidatorFunc(func(string, identity.Profile) validation.Errors {
					count := validationCalls.Add(1)
					if mode == "validation_cleanup" && count == 2 {
						return validation.NewErrors(validation.New("password", "password_too_similar"))
					}
					return validation.Errors{}
				})
				h := newAccountHTTP(t, f, boundary, validator)
				previous := h.login(t, h.client, managementOldPassword)
				response, _ := h.send(t, h.client, "GET", "/api/account/csrf/", "", "", nil)
				token := response.Header.Get(sessionauth.DefaultCSRFHeader)
				rows, audit := snapshotIdentitySystemRows(t, backend)
				before := f.stored(t)
				f.hasher.hook = func(context.Context) error { boundary.armed = true; return nil }
				headers := make(http.Header)
				headers.Set(sessionauth.DefaultCSRFHeader, token)
				var body string
				if surface == "form" {
					response, body = h.send(t, h.client, "POST", "/account/password/", accountForm(managementOldPassword, selfPassword, selfPassword, token).Encode(), "application/x-www-form-urlencoded", nil)
				} else {
					payload, _ := json.Marshal(map[string]string{"old_password": managementOldPassword, "new_password": selfPassword})
					response, body = h.send(t, h.client, "POST", "/api/account/password/", string(payload), "application/json", headers)
				}
				if validationCalls.Load() != 2 {
					t.Fatal("failure did not reach both password-policy fences", validationCalls.Load())
				}
				want := 500
				if strings.HasPrefix(mode, "unknown_") {
					want = 503
				}
				if response.StatusCode != want || len(response.Cookies()) != 0 || response.Header.Get("Retry-After") != "" || boundary.calls != 1 || f.hasher.calls.Load() != 1 {
					t.Fatal("failure was retried, published, or misclassified", surface, mode, response.StatusCode, boundary.calls)
				}
				for _, secret := range []string{managementOldPassword, selfPassword, before.EncodedPassword, previous.ID().Encoded(), "private-password-fault"} {
					if strings.Contains(body, secret) {
						t.Fatal("failure disclosed private material")
					}
				}
				if surface == "json" && want == 503 && !strings.Contains(body, `"code":"outcome_unknown"`) {
					t.Fatal("unknown result classification lost", body)
				}
				afterRows, afterAudit := snapshotIdentitySystemRows(t, backend)
				if mode == "unknown_commit" {
					if f.stored(t).Revision != 2 || reflect.DeepEqual(rows, afterRows) || reflect.DeepEqual(audit, afterAudit) {
						t.Fatal("unknown commit fixture did not commit")
					}
				} else if !reflect.DeepEqual(before, f.stored(t)) || !reflect.DeepEqual(rows, afterRows) || !reflect.DeepEqual(audit, afterAudit) {
					t.Fatal("failed HTTP transaction did not roll back")
				}
			})
		}
	}
}
