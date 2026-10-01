package identitytest

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	identityaccount "github.com/progresshans/godj/identity/account"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/mail"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

const resetConsumerPassword = "  replacement reset consumer password  "

type resetAccountHTTP struct {
	*accountHTTP
	persistence *systemstate.PasswordResetPersistence
	reports     atomic.Int64
	reportHook  func(error)
}

func newResetAccountHTTP(t *testing.T, f *managementFixture, backend TransitionBackend, sender mail.Sender) *resetAccountHTTP {
	t.Helper()
	h := &resetAccountHTTP{accountHTTP: &accountHTTP{}}
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
	change, err := h.runtime.PasswordChangePersistence(h.manager, minimum)
	if err != nil {
		t.Fatal(err)
	}
	now := loginInstant
	reset := resetConfig(t, &now, minimum)
	reset.Clock = clock
	h.persistence, err = h.runtime.PasswordResetPersistence(h.manager, reset)
	if err != nil {
		t.Fatal(err)
	}
	mailer := resetMailer(t, h.persistence.Resetter(), sender, resetMailConfig(t))
	next, err := identityaccount.AllowedNextPaths("")
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := sessionauth.New(sessionauth.Config{Sessions: h.manager, Authenticator: h.runtime.Authenticator(), Authorizer: denyIdentityAuthorization{}, LoginPersistence: login, PasswordChangePersistence: change, PasswordResetPersistence: h.persistence, Clock: clock,
		LoginPath: "/account/login/", FallbackPath: "/account/password/", AllowedNextPaths: next, SessionCookie: sessionauth.CookieConfig{AllowInsecure: true}, CSRFCookie: sessionauth.CookieConfig{AllowInsecure: true}})
	if err != nil {
		t.Fatal(err)
	}
	configured, err := settings.New(settings.Definition{ProjectName: "reset_consumer", InstalledApps: []apps.Config{{Name: "github.com/progresshans/godj/internal/identitytest", Label: "resetprobe"}}})
	if err != nil {
		t.Fatal(err)
	}
	surface, err := identityaccount.New(identityaccount.Config{Apps: configured.Apps(), Namespace: "resetprobe", Auth: runtime, PasswordReset: &identityaccount.PasswordResetConfig{Mailer: mailer, ReportError: func(_ context.Context, err error) {
		h.reports.Add(1)
		if h.reportHook != nil {
			h.reportHook(err)
		}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	app, err := web.NewApplication(web.Config{Settings: configured, Routes: surface.Routes(), Middleware: surface.Middleware()})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
	h.identityHTTP = &identityHTTP{server: server}
	h.client = h.newClient(t)
	f.hasher.calls.Store(0)
	return h
}
func resetConsumerHeaders(t *testing.T, response *http.Response) {
	t.Helper()
	if response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("Referrer-Policy") != "no-referrer" || response.Header.Get("WWW-Authenticate") != "" {
		t.Fatal("reset privacy headers missing", response.StatusCode, response.Header)
	}
}
func (h *resetAccountHTTP) bootstrap(t *testing.T, surface string) (string, http.Header) {
	t.Helper()
	path := "/account/reset/"
	if surface == "json" {
		path = "/api/account/reset/"
	}
	response, body := h.send(t, h.client, "GET", path, "", "", nil)
	resetConsumerHeaders(t, response)
	want := 200
	if surface == "json" {
		want = 204
	}
	if response.StatusCode != want {
		t.Fatal("reset request bootstrap", response.StatusCode)
	}
	token := response.Header.Get(sessionauth.DefaultCSRFHeader)
	if surface == "form" {
		if !strings.Contains(body, `type="email" id="id_email" name="email"`) {
			t.Fatal("reset form lost its email input widget")
		}
		token = accountCSRF(t, body)
	}
	headers := make(http.Header)
	headers.Set(sessionauth.DefaultCSRFHeader, token)
	return token, headers
}
func (h *resetAccountHTTP) requestMail(t *testing.T, surface, email string) (*http.Response, string) {
	t.Helper()
	token, headers := h.bootstrap(t, surface)
	if surface == "json" {
		body, _ := json.Marshal(map[string]string{"email": email})
		return h.send(t, h.client, "POST", "/api/account/reset/", string(body), "application/json", headers)
	}
	return h.send(t, h.client, "POST", "/account/reset/", url.Values{"email": {email}, "csrfmiddlewaretoken": {token}}.Encode(), "application/x-www-form-urlencoded", nil)
}
func resetMailLink(t *testing.T, outbox *mail.Memory) (string, string, string) {
	t.Helper()
	messages, err := outbox.Snapshot()
	if err != nil || len(messages) != 1 {
		t.Fatal("reset did not deliver exactly one message", err, len(messages))
	}
	principal, token := deliveredResetToken(t, messages[0].Message())
	uid := base64.RawURLEncoding.EncodeToString([]byte(principal))
	return "/account/reset/" + uid + "/" + token + "/", "/account/reset/" + uid + "/set-password/", "/api/account/reset/" + uid + "/"
}
func resetForm(password, confirmation, csrf string) string {
	return url.Values{"new_password1": {password}, "new_password2": {confirmation}, "csrfmiddlewaretoken": {csrf}}.Encode()
}

func RunPasswordResetConsumer(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	for _, surface := range []string{"form", "json"} {
		for _, mode := range []string{"missing", "anonymous", "self", "other"} {
			t.Run(surface+"/"+mode, func(t *testing.T) {
				backend, other := open(t)
				f := newManagementFixture(t, backend, 7)
				ordinaryAccount(t, f)
				memory := resetMemory(t)
				h := newResetAccountHTTP(t, f, backend, memory)
				previous := resetPrevious(t, h.runtime, h.manager, mode, f.user.PrincipalID)
				if previous.ID().Valid() {
					base, _ := url.Parse(h.server.URL)
					h.client.Jar.SetCookies(base, []*http.Cookie{{Name: sessionauth.DefaultSessionCookieName, Value: previous.ID().Encoded(), Path: "/"}})
				}
				before := f.stored(t)
				rows, audit := snapshotIdentitySystemRows(t, backend)
				response, body := h.requestMail(t, surface, " \u2002MEMBER@example.test\u2002 ")
				resetConsumerHeaders(t, response)
				want := 302
				if surface == "json" {
					want = 204
				}
				if response.StatusCode != want || h.reports.Load() != 0 {
					t.Fatal("request acknowledgement", response.StatusCode, body)
				}
				nowRows, nowAudit := snapshotIdentitySystemRows(t, backend)
				if !reflect.DeepEqual(rows, nowRows) || !reflect.DeepEqual(audit, nowAudit) || f.hasher.calls.Load() != 0 {
					t.Fatal("mail request changed server state")
				}
				link, confirmation, apiPath := resetMailLink(t, memory)
				fresh := h.newClient(t)
				response, body = h.send(t, fresh, "GET", confirmation, "", "", nil)
				if response.StatusCode != 200 || !strings.Contains(body, `data-account-view="reset-invalid"`) {
					t.Fatal("fresh browser admitted stored proof")
				}
				response, body = h.send(t, h.client, "GET", link, "", "", nil)
				resetConsumerHeaders(t, response)
				rawToken := strings.Split(link, "/")[4]
				if response.StatusCode != 302 || response.Header.Get("Location") != confirmation || strings.Contains(body, rawToken) || strings.Contains(response.Header.Get("Location"), rawToken) {
					t.Fatal("entry did not hide raw token")
				}
				proof := h.current(t, h.client)
				if proof.ID() == previous.ID() {
					t.Fatal("entry did not rotate")
				}
				if target, _ := proof.Value(auth.SessionResetPrincipalIDKey); target != f.user.PrincipalID {
					t.Fatal("proof wrong target")
				}
				// Reopen the real native database on another connection/runtime. Browser
				// session cookies survive; the new runtime issues its own masked CSRF token.
				old := h
				h = newResetAccountHTTP(t, f, other, memory)
				h.client = old.client
				old.server.Close()
				response, body = h.send(t, h.client, "GET", confirmation, "", "", nil)
				resetConsumerHeaders(t, response)
				if response.StatusCode != 200 || !strings.Contains(body, `data-account-view="reset-confirm"`) || strings.Contains(body, rawToken) {
					t.Fatal("proof did not survive runtime restart")
				}
				token := accountCSRF(t, body)
				headers := make(http.Header)
				headers.Set(sessionauth.DefaultCSRFHeader, token)
				if surface == "json" {
					response, _ = h.send(t, h.client, "GET", apiPath, "", "", nil)
					if response.StatusCode != 204 {
						t.Fatal("JSON proof refused")
					}
					headers.Set(sessionauth.DefaultCSRFHeader, response.Header.Get(sessionauth.DefaultCSRFHeader))
				}
				rows, audit = snapshotIdentitySystemRows(t, backend)
				path, media, input := confirmation, "application/x-www-form-urlencoded", resetForm("short", "short", token)
				if surface == "json" {
					path, media, input = apiPath, "application/json", `{"new_password":"short"}`
				}
				response, body = h.send(t, h.client, "POST", path, input, media, headers)
				resetConsumerHeaders(t, response)
				want = 200
				if surface == "json" {
					want = 400
				}
				if response.StatusCode != want || !strings.Contains(body, "password_too_short") {
					t.Fatal("password policy not surfaced", response.StatusCode, body)
				}
				nowRows, nowAudit = snapshotIdentitySystemRows(t, backend)
				if !reflect.DeepEqual(rows, nowRows) || !reflect.DeepEqual(audit, nowAudit) || f.hasher.calls.Load() != 0 {
					t.Fatal("weak reset changed state")
				}
				input = resetForm(resetConsumerPassword, resetConsumerPassword, token)
				if surface == "json" {
					encoded, _ := json.Marshal(map[string]string{"new_password": resetConsumerPassword})
					input = string(encoded)
				}
				response, body = h.send(t, h.client, "POST", path, input, media, headers)
				resetConsumerHeaders(t, response)
				want = 302
				if surface == "json" {
					want = 204
				}
				if response.StatusCode != want || len(response.Cookies()) != 1 || strings.Contains(body, resetConsumerPassword) {
					t.Fatal("completion response", response.StatusCode, body)
				}
				if surface == "form" && response.Header.Get("Location") != "/account/reset/complete/" {
					t.Fatal("incorrect completion destination")
				}
				if f.hasher.calls.Load() != 1 {
					t.Fatal("reset did not hash once")
				}
				assertResetCommit(t, f, before, resetConsumerPassword, before.Revision+1)
				if mode == "self" {
					if response.Cookies()[0].MaxAge >= 0 {
						t.Fatal("self reset did not clear login cookie")
					}
				} else {
					current := h.current(t, h.client)
					for _, key := range []string{auth.SessionResetPrincipalIDKey, auth.SessionResetTokenKey} {
						if _, exists := current.Value(key); exists {
							t.Fatal("reset retained proof")
						}
					}
					principal, _ := current.Value(auth.SessionPrincipalIDKey)
					wantPrincipal := ""
					if mode == "other" {
						wantPrincipal = "manager"
					}
					if principal != wantPrincipal {
						t.Fatal("reset changed authentication")
					}
					if previous.ID().Valid() {
						if !current.AbsoluteExpiresAt().Equal(previous.AbsoluteExpiresAt()) || !current.CreatedAt().Equal(previous.CreatedAt()) {
							t.Fatal("reset changed absolute lifetime")
						}
						if value, _ := current.Value("payload"); value != "preserved private reset payload" {
							t.Fatal("reset lost payload")
						}
					}
				}
				rows, audit = snapshotIdentitySystemRows(t, backend)
				response, body = h.send(t, h.client, "POST", path, input, media, headers)
				want = 200
				if surface == "json" {
					want = 403
				}
				if response.StatusCode != want || len(response.Cookies()) != 0 || f.hasher.calls.Load() != 1 {
					t.Fatal("consumed proof replayed", response.StatusCode, body)
				}
				nowRows, nowAudit = snapshotIdentitySystemRows(t, backend)
				if !reflect.DeepEqual(rows, nowRows) || !reflect.DeepEqual(audit, nowAudit) {
					t.Fatal("replay changed state")
				}
			})
		}
	}
}

func RunPasswordResetConsumerAcknowledgement(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	for _, surface := range []string{"form", "json"} {
		for _, mode := range []string{"known", "unknown", "inactive", "unusable", "delivery_error", "delivery_unknown", "delivery_panic", "delivery_cancel", "snapshot_error", "reporter_panic"} {
			t.Run(surface+"/"+mode, func(t *testing.T) {
				backend, _ := open(t)
				f := newManagementFixture(t, backend, 3)
				switch mode {
				case "inactive":
					if _, err := models.UserObjects.Update(t.Context(), backend, f.user, models.UserPatch{}.WithActive(false)); err != nil {
						t.Fatal(err)
					}
				case "unusable":
					unusable, err := auth.MakeUnusablePassword(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					if _, err = models.UserObjects.Update(t.Context(), backend, f.user, models.UserPatch{}.WithEncodedPassword(unusable)); err != nil {
						t.Fatal(err)
					}
				}
				sends := atomic.Int64{}
				private := errors.New("private recipient token diagnostic")
				sender := resetSenderFunc(func(context.Context, mail.Message) error {
					sends.Add(1)
					switch mode {
					case "delivery_error", "reporter_panic":
						return private
					case "delivery_unknown":
						return errors.Join(&mail.Error{Code: mail.CodeOutcomeUnknown}, private)
					case "delivery_panic":
						panic(private)
					case "delivery_cancel":
						return context.Canceled
					}
					return nil
				})
				fault := &resetConsumerReadFault{TransitionBackend: backend}
				h := newResetAccountHTTP(t, f, fault, sender)
				fault.active.Store(mode == "snapshot_error")
				if mode == "reporter_panic" {
					h.reportHook = func(error) { panic("private reporter panic") }
				}
				before := f.stored(t)
				rows, audit := snapshotIdentitySystemRows(t, backend)
				email := "member@example.test"
				if mode == "unknown" {
					email = "unknown@example.test"
				}
				response, body := h.requestMail(t, surface, email)
				resetConsumerHeaders(t, response)
				want := 302
				expected := "Found\n"
				location := "/account/reset/sent/"
				if surface == "json" {
					want, expected, location = 204, "", ""
				}
				if response.StatusCode != want || body != expected || response.Header.Get("Location") != location || len(response.Cookies()) != 0 {
					t.Fatal("public acknowledgement disclosed state", response.StatusCode, body)
				}
				expectedSends := int64(1)
				if mode == "unknown" || mode == "inactive" || mode == "unusable" || mode == "snapshot_error" {
					expectedSends = 0
				}
				if sends.Load() != expectedSends {
					t.Fatal("mail retry or recipient eligibility incorrect")
				}
				expectedReports := int64(0)
				if strings.HasPrefix(mode, "delivery_") || mode == "reporter_panic" || mode == "snapshot_error" {
					expectedReports = 1
				}
				if h.reports.Load() != expectedReports {
					t.Fatal("private failure not separately reported")
				}
				currentRows, currentAudit := snapshotIdentitySystemRows(t, backend)
				if !reflect.DeepEqual(before, f.stored(t)) || !reflect.DeepEqual(rows, currentRows) || !reflect.DeepEqual(audit, currentAudit) || f.hasher.calls.Load() != 0 {
					t.Fatal("request mutated identity/session/audit")
				}
			})
		}
	}
}

func RunPasswordResetConsumerRefusals(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	for _, surface := range []string{"form", "json"} {
		t.Run(surface, func(t *testing.T) {
			backend, _ := open(t)
			f := newManagementFixture(t, backend, 3)
			memory := resetMemory(t)
			h := newResetAccountHTTP(t, f, backend, memory)
			response, _ := h.requestMail(t, surface, f.user.Email)
			if response.StatusCode != 302 && response.StatusCode != 204 {
				t.Fatal("setup request")
			}
			link, confirmation, apiPath := resetMailLink(t, memory)
			response, _ = h.send(t, h.client, "GET", link, "", "", nil)
			if response.StatusCode != 302 {
				t.Fatal("setup entry")
			}
			response, html := h.send(t, h.client, "GET", confirmation, "", "", nil)
			token := accountCSRF(t, html)
			rows, audit := snapshotIdentitySystemRows(t, backend)
			before := f.stored(t)
			modes := []string{"csrf", "origin", "query", "empty_query", "unknown_member", "duplicate", "null", "oversize_string", "oversize_body", "media", "wrong_target", "missing_proof", "malformed_uid", "padded_uid", "missing_password", "weak", "method", "missing_route"}
			if surface == "json" {
				modes = append(modes, "accept")
			}
			for _, mode := range modes {
				t.Run(mode, func(t *testing.T) {
					path, body, media := confirmation, resetForm(resetConsumerPassword, resetConsumerPassword, token), "application/x-www-form-urlencoded"
					if surface == "json" {
						encoded, _ := json.Marshal(map[string]string{"new_password": resetConsumerPassword})
						path, body, media = apiPath, string(encoded), "application/json"
					}
					method := "POST"
					headers := make(http.Header)
					headers.Set(sessionauth.DefaultCSRFHeader, token)
					client := h.client
					want := 400
					switch mode {
					case "method":
						method = "PUT"
						want = 405
					case "missing_route":
						path += "extra/other/"
						want = 404
					case "accept":
						headers.Set("Accept", "application/xml")
						want = 406
					case "csrf":
						headers.Del(sessionauth.DefaultCSRFHeader)
						body = strings.ReplaceAll(body, token, "bad")
						want = 403
					case "origin":
						headers.Set("Origin", "https://attacker.test")
						want = 403
					case "query":
						path += "?unexpected=1"
					case "empty_query":
						path += "?"
					case "unknown_member":
						if surface == "form" {
							body += "&target=manager"
						} else {
							body = strings.TrimSuffix(body, "}") + `,"target":"manager"}`
						}
					case "duplicate":
						if surface == "form" {
							body += "&new_password1=wrong"
							want = 200
						} else {
							body = strings.TrimSuffix(body, "}") + `,"new_password":"wrong"}`
						}
					case "null":
						if surface == "form" {
							body = resetForm("\x00", "\x00", token)
							want = 200
						} else {
							body = `{"new_password":null}`
						}
					case "oversize_string":
						if surface == "form" {
							body = resetForm(strings.Repeat("x", 4097), resetConsumerPassword, token)
							want = 413
						} else {
							body = `{"new_password":"` + strings.Repeat("x", 4097) + `"}`
						}
					case "oversize_body":
						body = strings.Repeat("x", 65537)
						want = 413
					case "media":
						media = "text/plain"
						want = 415
					case "wrong_target", "malformed_uid", "padded_uid":
						parts := strings.Split(path, "/")
						index := 3
						if surface == "json" {
							index = 4
						}
						switch mode {
						case "wrong_target":
							parts[index] = base64.RawURLEncoding.EncodeToString([]byte("manager"))
						case "malformed_uid":
							parts[index] = "not.base64"
						case "padded_uid":
							parts[index] += "="
						}
						path = strings.Join(parts, "/")
						want = 200
						if surface == "json" {
							want = 403
						}
					case "missing_proof":
						client = h.newClient(t)
						base, _ := url.Parse(h.server.URL)
						for _, cookie := range h.client.Jar.Cookies(base) {
							if cookie.Name == sessionauth.DefaultCSRFCookieName {
								client.Jar.SetCookies(base, []*http.Cookie{cookie})
							}
						}
						want = 200
						if surface == "json" {
							want = 403
						}
					case "missing_password":
						if surface == "form" {
							body = url.Values{"csrfmiddlewaretoken": {token}}.Encode()
							want = 200
						} else {
							body = `{}`
						}
					case "weak":
						if surface == "form" {
							body = resetForm("short", "short", token)
							want = 200
						} else {
							body = `{"new_password":"short"}`
						}
					}
					response, body = h.send(t, client, method, path, body, media, headers)
					resetConsumerHeaders(t, response)
					if response.StatusCode != want {
						t.Fatal("wrong reset refusal", mode, response.StatusCode, body)
					}
					if strings.Contains(body, resetConsumerPassword) || f.hasher.calls.Load() != 0 || h.reports.Load() != 0 {
						t.Fatal("refusal leaked password, hashed or reported input as execution failure")
					}
					currentRows, currentAudit := snapshotIdentitySystemRows(t, backend)
					if !reflect.DeepEqual(before, f.stored(t)) || !reflect.DeepEqual(rows, currentRows) || !reflect.DeepEqual(audit, currentAudit) {
						t.Fatal("reset refusal changed persistent state")
					}
				})
			}
			// Actual native SetPasswordForm errors, including confirmation suppressing
			// password policy, are compared against the previously captured view.
			if surface == "form" {
				raw, err := resetHTTPReferences.ReadFile("testdata/password-reset-http-django61-sqlite.json")
				if err != nil {
					t.Fatal(err)
				}
				var reference struct {
					Observations struct {
						PasswordErrors map[string]struct {
							Errors map[string][]string
							Status int
						} `json:"password_errors"`
					}
				}
				if err = json.Unmarshal(raw, &reference); err != nil {
					t.Fatal(err)
				}
				for _, c := range []struct{ name, first, second string }{{"mismatch", resetConsumerPassword, "different"}, {"required", "", ""}, {"weak", "short", "short"}, {"weak_mismatch", "short", "other"}} {
					response, body := h.send(t, h.client, "POST", confirmation, resetForm(c.first, c.second, token), "application/x-www-form-urlencoded", nil)
					expected, ok := reference.Observations.PasswordErrors[c.name]
					if !ok || response.StatusCode != expected.Status || !reflect.DeepEqual(accountErrors(body), expected.Errors) {
						t.Fatal("native confirmation diagnostics differ", c.name, accountErrors(body))
					}
				}
				response, _ = h.send(t, h.client, "POST", link, resetForm(resetConsumerPassword, resetConsumerPassword, token), "application/x-www-form-urlencoded", nil)
				if response.StatusCode != 302 || response.Header.Get("Location") != confirmation || f.hasher.calls.Load() != 0 || !reflect.DeepEqual(before, f.stored(t)) {
					t.Fatal("raw-token POST changed password or failed hidden redirect")
				}
			}
		})
	}
}

func RunPasswordResetConsumerBoundaries(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	for _, surface := range []string{"form", "json"} {
		for _, mode := range []string{"password_write", "after_session", "after_rotation_delete", "audit_insert", "revoke", "unknown_rollback", "unknown_commit", "cancel"} {
			t.Run(surface+"/"+mode, func(t *testing.T) {
				backend, _ := open(t)
				f := newManagementFixture(t, backend, 4)
				boundary := &passwordChangeBoundary{TransitionBackend: backend, mode: mode}
				memory := resetMemory(t)
				h := newResetAccountHTTP(t, f, boundary, memory)
				h.requestMail(t, surface, f.user.Email)
				link, confirmation, apiPath := resetMailLink(t, memory)
				h.send(t, h.client, "GET", link, "", "", nil)
				_, html := h.send(t, h.client, "GET", confirmation, "", "", nil)
				token := accountCSRF(t, html)
				proof := h.current(t, h.client)
				rows, audit := snapshotIdentitySystemRows(t, backend)
				before := f.stored(t)
				reported := make(chan struct{})
				h.reportHook = func(error) { close(reported) }
				f.hasher.hook = func(context.Context) error { boundary.armed = true; return nil }
				path, body, media := confirmation, resetForm(resetConsumerPassword, resetConsumerPassword, token), "application/x-www-form-urlencoded"
				if surface == "json" {
					encoded, _ := json.Marshal(map[string]string{"new_password": resetConsumerPassword})
					path, body, media = apiPath, string(encoded), "application/json"
				}
				headers := make(http.Header)
				headers.Set(sessionauth.DefaultCSRFHeader, token)
				response, body := h.send(t, h.client, "POST", path, body, media, headers)
				select {
				case <-reported:
				case <-time.After(5 * time.Second):
					t.Fatal("execution failure was not reported")
				}
				boundary.armed = false
				f.hasher.hook = nil
				resetConsumerHeaders(t, response)
				want := 500
				if strings.HasPrefix(mode, "unknown_") {
					want = 503
				}
				if response.StatusCode != want || len(response.Cookies()) != 0 || response.Header.Get("Retry-After") != "" || boundary.calls != 1 || f.hasher.calls.Load() != 1 || h.reports.Load() != 1 {
					t.Fatal("failed reset was published, retried or misclassified", response.StatusCode, boundary.calls)
				}
				for _, secret := range []string{resetConsumerPassword, before.EncodedPassword, proof.ID().Encoded(), strings.Split(link, "/")[4], "private-password-fault"} {
					if strings.Contains(body, secret) {
						t.Fatal("failed reset disclosed private material")
					}
				}
				if surface == "json" && want == 503 && !strings.Contains(body, `"code":"outcome_unknown"`) {
					t.Fatal("unknown JSON outcome lost")
				}
				currentRows, currentAudit := snapshotIdentitySystemRows(t, backend)
				if mode == "unknown_commit" {
					assertResetCommit(t, f, before, resetConsumerPassword, before.Revision+1)
					if reflect.DeepEqual(rows, currentRows) || reflect.DeepEqual(audit, currentAudit) {
						t.Fatal("unknown commit did not persist")
					}
				} else if !reflect.DeepEqual(before, f.stored(t)) || !reflect.DeepEqual(rows, currentRows) || !reflect.DeepEqual(audit, currentAudit) {
					t.Fatal("failed reset lost atomic rollback")
				}
			})
		}
	}
}

type resetConsumerReadFault struct {
	TransitionBackend
	active atomic.Bool
}

func (b *resetConsumerReadFault) ReadSnapshot(ctx context.Context, fn func(db.Queryer) error) error {
	if b.active.Load() {
		return errors.New("private reset snapshot failure")
	}
	return b.TransitionBackend.ReadSnapshot(ctx, fn)
}

func RunPasswordResetRequestRefusals(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	for _, surface := range []string{"form", "json"} {
		t.Run(surface, func(t *testing.T) {
			backend, _ := open(t)
			f := newManagementFixture(t, backend, 2)
			memory := resetMemory(t)
			h := newResetAccountHTTP(t, f, backend, memory)
			token, _ := h.bootstrap(t, surface)
			before := f.stored(t)
			rows, audit := snapshotIdentitySystemRows(t, backend)
			for _, mode := range []string{"missing_csrf", "duplicate_csrf", "foreign_origin", "invalid", "empty", "missing", "nul", "email_length", "duplicate_email", "unknown", "wrong_media", "body_limit", "input_limit", "query", "empty_query"} {
				t.Run(mode, func(t *testing.T) {
					email := f.user.Email
					headers := make(http.Header)
					headers.Set(sessionauth.DefaultCSRFHeader, token)
					path, media := "/account/reset/", "application/x-www-form-urlencoded"
					want := 200
					if surface == "json" {
						path, media, want = "/api/account/reset/", "application/json", 400
					}
					switch mode {
					case "invalid":
						email = `"><script>alert(1)</script>`
					case "empty":
						email = "\u2002 "
					case "missing":
						email = ""
					case "nul":
						email = "bad\x00@example.test"
					case "email_length":
						email = strings.Repeat("x", 255) + "@example.test"
					case "input_limit":
						email = strings.Repeat("x", 4097)
						if surface == "form" {
							want = 413
						}
					}
					body := url.Values{"email": {email}, "csrfmiddlewaretoken": {token}}.Encode()
					if surface == "json" {
						encoded, _ := json.Marshal(map[string]string{"email": email})
						body = string(encoded)
					}
					switch mode {
					case "missing_csrf":
						headers.Del(sessionauth.DefaultCSRFHeader)
						body = strings.ReplaceAll(body, token, "invalid")
						want = 403
					case "duplicate_csrf":
						headers.Add(sessionauth.DefaultCSRFHeader, token)
						want = 403
					case "foreign_origin":
						headers.Set("Origin", "https://attacker.test")
						want = 403
					case "missing":
						if surface == "form" {
							body = url.Values{"csrfmiddlewaretoken": {token}}.Encode()
						} else {
							body = `{}`
						}
					case "duplicate_email":
						if surface == "form" {
							body += "&email=other%40example.test"
						} else {
							body = strings.TrimSuffix(body, "}") + `,"email":"other@example.test"}`
						}
					case "unknown":
						if surface == "form" {
							body += "&extra=1"
						} else {
							body = strings.TrimSuffix(body, "}") + `,"extra":1}`
						}
						want = 400
					case "wrong_media":
						media = "text/plain"
						want = 415
					case "body_limit":
						body = strings.Repeat("x", 65537)
						want = 413
					case "query":
						path += "?email=other"
						want = 400
					case "empty_query":
						path += "?"
						want = 400
					}
					response, body := h.send(t, h.client, "POST", path, body, media, headers)
					resetConsumerHeaders(t, response)
					if response.StatusCode != want || strings.Contains(body, "<script>") || h.reports.Load() != 0 || f.hasher.calls.Load() != 0 {
						t.Fatal("invalid email request admitted, unsafe or misclassified", mode, response.StatusCode, body)
					}
					messages, err := memory.Snapshot()
					if err != nil || len(messages) != 0 {
						t.Fatal("invalid email request sent mail", err)
					}
					currentRows, currentAudit := snapshotIdentitySystemRows(t, backend)
					if !reflect.DeepEqual(before, f.stored(t)) || !reflect.DeepEqual(rows, currentRows) || !reflect.DeepEqual(audit, currentAudit) {
						t.Fatal("invalid request mutated state")
					}
				})
			}
		})
	}
}
