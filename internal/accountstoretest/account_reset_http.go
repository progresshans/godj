package accountstoretest

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth/passwordpolicy"
	hostproject "github.com/progresshans/godj/conformance/identityfixture/project"
	identityaccount "github.com/progresshans/godj/identity/account"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/mail"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

type resetAccountHTTP struct {
	*accountHTTP
	owner *accountmodel.PasswordResetter[models.User]
	// send joins each request before fixture observations or hook changes.
	reports    []error
	reportHook func(error)
}

func newResetAccountHTTP(t *testing.T, f *loginFixture, backend Backend, sender mail.Sender, policy accountmodel.PasswordValidator[models.User]) *resetAccountHTTP {
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
	h := &resetAccountHTTP{accountHTTP: &accountHTTP{f: f, runtime: f.runtime, manager: f.runtime.Sessions(), now: &f.instant}}
	h.owner = resetNativeOwner(t, f)
	next, err := identityaccount.AllowedNextPaths("")
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := sessionauth.New(sessionauth.Config{Sessions: h.manager, Authenticator: h.runtime.Authenticator(), Authorizer: denyAccountPermission{}, LoginPersistence: h.runtime.LoginPersistence(), PasswordChangePersistence: h.runtime.PasswordChangePersistence(), PasswordResetPersistence: h.owner, Clock: f.clock,
		LoginPath: "/account/login/", FallbackPath: "/account/password/", AllowedNextPaths: next, SessionCookie: sessionauth.CookieConfig{AllowInsecure: true}, CSRFCookie: sessionauth.CookieConfig{AllowInsecure: true}})
	if err != nil {
		t.Fatal(err)
	}
	configured, err := settings.New(settings.Definition{ProjectName: "selected_account_reset", InstalledApps: append(hostproject.InstalledApps(), apps.Config{Name: "github.com/progresshans/godj/internal/accountstoretest", Label: "accountprobe"})})
	if err != nil {
		t.Fatal(err)
	}
	surface, err := identityaccount.New(identityaccount.Config{Apps: configured.Apps(), Namespace: "accountprobe", Auth: runtime, PasswordReset: &identityaccount.PasswordResetConfig{Mailer: selectedResetMailer(t, h.owner, sender, nil), ReportError: func(_ context.Context, err error) {
		h.reports = append(h.reports, err)
		if h.reportHook != nil {
			h.reportHook(err)
		}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	application, err := web.NewApplication(web.Config{Settings: configured, Routes: surface.Routes(), Middleware: surface.Middleware()})
	if err != nil {
		t.Fatal(err)
	}
	h.serve(t, application)
	return h
}

func resetHTTPHeaders(t *testing.T, response *http.Response) {
	t.Helper()
	if response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("Referrer-Policy") != "no-referrer" || response.Header.Get("WWW-Authenticate") != "" {
		t.Fatal("reset privacy headers missing", response.StatusCode)
	}
}

func (h *resetAccountHTTP) bootstrap(t *testing.T, client *http.Client, surface string) (string, http.Header) {
	t.Helper()
	path, want := "/account/reset/", 200
	if surface == "json" {
		path, want = "/api/account/reset/", 204
	}
	response, body := h.send(t, client, "GET", path, "", "", nil)
	resetHTTPHeaders(t, response)
	if response.StatusCode != want {
		t.Fatal("reset request bootstrap", response.StatusCode)
	}
	token := response.Header.Get(sessionauth.DefaultCSRFHeader)
	if surface == "form" {
		if !strings.Contains(body, "type=\"email\" id=\"id_email\" name=\"email\"") {
			t.Fatal("reset form lost its declared email widget")
		}
		token = accountCSRF(t, body)
	}
	if token == "" {
		t.Fatal("reset bootstrap lacks a masked CSRF token")
	}
	headers := make(http.Header)
	headers.Set(sessionauth.DefaultCSRFHeader, token)
	return token, headers
}

func (h *resetAccountHTTP) requestMail(t *testing.T, client *http.Client, surface, email string) (*http.Response, string) {
	t.Helper()
	token, headers := h.bootstrap(t, client, surface)
	if surface == "json" {
		raw, err := json.Marshal(map[string]string{"email": email})
		if err != nil {
			t.Fatal(err)
		}
		return h.send(t, client, "POST", "/api/account/reset/", string(raw), "application/json", headers)
	}
	return h.send(t, client, "POST", "/account/reset/", url.Values{"email": {email}, "csrfmiddlewaretoken": {token}}.Encode(), "application/x-www-form-urlencoded", nil)
}

type resetHTTPLink struct{ principal, token, raw, form, json string }

func resetDeliveredLink(t *testing.T, memory *mail.Memory) resetHTTPLink {
	t.Helper()
	messages, err := memory.Snapshot()
	if err != nil || len(messages) != 1 {
		t.Fatal("expected one delivered reset link", err)
	}
	principal, token := selectedResetDelivery(t, messages[0].Message())
	uid := base64.RawURLEncoding.EncodeToString([]byte(principal))
	return resetHTTPLink{principal, token, "/account/reset/" + uid + "/" + token + "/", "/account/reset/" + uid + "/set-password/", "/api/account/reset/" + uid + "/"}
}

func resetAccountForm(raw, confirmation, csrf string) string {
	return url.Values{"new_password1": {raw}, "new_password2": {confirmation}, "csrfmiddlewaretoken": {csrf}}.Encode()
}

func resetCompleteRequest(t *testing.T, surface string, link resetHTTPLink, raw, csrf string) (string, string, string, http.Header) {
	t.Helper()
	headers := make(http.Header)
	headers.Set(sessionauth.DefaultCSRFHeader, csrf)
	if surface == "form" {
		return link.form, resetAccountForm(raw, raw, csrf), "application/x-www-form-urlencoded", headers
	}
	encoded, err := json.Marshal(map[string]string{"new_password": raw})
	if err != nil {
		t.Fatal(err)
	}
	return link.json, string(encoded), "application/json", headers
}

func resetClientSession(t *testing.T, h *accountHTTP, client *http.Client, record sessions.Record) {
	t.Helper()
	address, err := url.Parse(h.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if record.ID().Valid() {
		client.Jar.SetCookies(address, []*http.Cookie{{Name: sessionauth.DefaultSessionCookieName, Value: record.ID().Encoded(), Path: "/"}})
	}
}

func resetClientCookie(t *testing.T, h *accountHTTP, client *http.Client, name string) string {
	t.Helper()
	address, err := url.Parse(h.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, cookie := range client.Jar.Cookies(address) {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

func (h *resetAccountHTTP) enter(t *testing.T, reopen func(*testing.T) Backend, client *http.Client, previous sessions.Record, link resetHTTPLink) sessions.Record {
	t.Helper()
	f := h.f
	before := userReferenceSnapshot(t, reopen(t))
	entropy := f.entropy.count()
	writes := f.owner.boundary.calls
	response, body := h.send(t, client, "GET", link.raw, "", "", nil)
	resetHTTPHeaders(t, response)
	if response.StatusCode != 302 || response.Header.Get("Location") != link.form || strings.Contains(body+response.Header.Get("Location"), link.token) || len(response.Cookies()) != 1 {
		t.Fatal("reset token exchange lost its hidden redirect")
	}
	after := userReferenceSnapshot(t, reopen(t))
	expected, proof := resetEntryExpected(t, f, before, after, previous, f.entropy.one(t, entropy), link.token)
	if response.Cookies()[0].Name != sessionauth.DefaultSessionCookieName || response.Cookies()[0].Value != proof.ID().Encoded() || !response.Cookies()[0].HttpOnly || response.Cookies()[0].SameSite != http.SameSiteLaxMode || f.owner.boundary.calls != writes+1 {
		t.Fatal("reset entry published a different opaque browser")
	}
	if !reflect.DeepEqual(h.current(t, client).Snapshot(), proof.Snapshot()) {
		t.Fatal("reset cookie differs from the independently expected proof")
	}
	assertUserReferenceState(t, expected, after, f.actor.Principal())
	for i, record := range f.targetSessions {
		if record.ID() == previous.ID() {
			f.targetSessions[i] = proof
		}
	}
	return proof
}

func (h *resetAccountHTTP) confirmToken(t *testing.T, client *http.Client, surface string, link resetHTTPLink) string {
	t.Helper()
	path, want := link.form, 200
	if surface == "json" {
		path, want = link.json, 204
	}
	response, body := h.send(t, client, "GET", path, "", "", nil)
	resetHTTPHeaders(t, response)
	if response.StatusCode != want || strings.Contains(body, link.token) {
		t.Fatal("reset confirmation lost proof or disclosed token", response.StatusCode)
	}
	if surface == "json" {
		token := response.Header.Get(sessionauth.DefaultCSRFHeader)
		if token == "" {
			t.Fatal("JSON confirmation lost masked CSRF")
		}
		return token
	}
	if !strings.Contains(body, "data-account-view=\"reset-confirm\"") {
		t.Fatal("form confirmation is not current")
	}
	return accountCSRF(t, body)
}

func resetAccountSetup(t *testing.T, h *resetAccountHTTP, reopen func(*testing.T) Backend, memory *mail.Memory, surface string, previous sessions.Record) (resetHTTPLink, sessions.Record, string) {
	t.Helper()
	f := h.f
	resetClientSession(t, h.accountHTTP, h.client, previous)
	before := userReferenceSnapshot(t, reopen(t))
	entropy := f.entropy.count()
	writes := f.owner.boundary.calls
	response, body := h.requestMail(t, h.client, surface, " \u2002MEMBER@example.test\u2002 ")
	resetHTTPHeaders(t, response)
	want := 302
	if surface == "json" {
		want = 204
	}
	if response.StatusCode != want || len(h.reports) != 0 || len(response.Cookies()) != 0 || f.owner.hasher.hashes.Load() != 0 || f.owner.hasher.verifies.Load() != 0 || f.owner.boundary.calls != writes || f.entropy.count() != entropy {
		t.Fatal("reset mail acknowledgement changed state/work", response.StatusCode, body)
	}
	assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
	link := resetDeliveredLink(t, memory)
	if link.principal != f.target.PrincipalID {
		t.Fatal("reset mail selected another account")
	}
	f.instant.Add(int64(time.Minute))
	proof := h.enter(t, reopen, h.client, previous, link)
	csrf := h.confirmToken(t, h.client, surface, link)
	return link, proof, csrf
}

func runAccountResetHTTP(add registerCase, open Open) {
	add("account_reset_http", func(t *testing.T) { accountResetHTTP(t, open) })
	add("account_reset_acknowledgement", func(t *testing.T) { accountResetAcknowledgement(t, open) })
	add("account_reset_refusals", func(t *testing.T) { accountResetRefusals(t, open) })
	add("account_reset_request_refusals", func(t *testing.T) { accountResetRequestRefusals(t, open) })
	add("account_reset_failures", func(t *testing.T) { accountResetFailures(t, open) })
}
