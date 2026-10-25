package consumer

import (
	"bytes"
	"context"
	"encoding/base64"
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

	"example.com/godj-project-bundle/project"
	auditmodels "github.com/progresshans/godj/account/audit/models"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/identity"
	identityaccount "github.com/progresshans/godj/identity/account"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/mail"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	sessiondb "github.com/progresshans/godj/sessions/database"
	sessionmodels "github.com/progresshans/godj/sessions/database/models"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/settings/database"
	"github.com/progresshans/godj/web"
	websession "github.com/progresshans/godj/web/sessionauth"
)

type resetHTTPHasher struct {
	auth.PasswordHasher
	hashes atomic.Int64
}

func (hasher *resetHTTPHasher) Hash(ctx context.Context, raw string) (string, error) {
	hasher.hashes.Add(1)
	return hasher.PasswordHasher.Hash(ctx, raw)
}

type resetHTTPHost[M any] struct {
	server   *httptest.Server
	store    *accountmodel.SessionStore[M]
	manager  *sessions.Manager
	verifier *accountmodel.Authenticator[M]
	owner    *accountmodel.PasswordResetter[M]
	runtime  *websession.Runtime
	config   settings.Settings
}

// Factories are the actual generated functions supplied by each independently
// generated project. The shared consumer never casts M to the built-in User.
func runSelectedResetHTTP[M any](t *testing.T, backend database.Backend, reopen func() database.Backend, binding accountmodel.Binding[M], storeFactory func(sessiondb.Backend, sessiondb.Config) (*accountmodel.SessionStore[M], error), resetFactory func(*accountmodel.SessionStore[M], *sessions.Manager, *accountmodel.Authenticator[M], accountmodel.PasswordResetConfig[M]) (*accountmodel.PasswordResetter[M], error), hasher auth.PasswordHasher, row, other M, policy accountmodel.PasswordValidator[M]) {
	t.Helper()
	check := func(err error) { t.Helper(); resetCheck(t, err) }
	role, err := binding.Role()
	check(err)
	objects, err := binding.Objects()
	check(err)
	metadata, err := objects.Metadata()
	check(err)
	var key orm.AutoField[M]
	for _, field := range metadata.Fields {
		if field.PrimaryKey {
			key = orm.NewAutoField[M](field)
		}
	}
	rowKey, present, err := objects.PrimaryKey(row)
	check(err)
	id, integer := rowKey.Integer()
	if !present || !integer {
		t.Fatal("selected reset has no actual stored key")
	}
	readRow := func() M {
		t.Helper()
		value, present, err := objects.Using(backend).Filter(key.Exact(id)).OrderBy(key.Asc()).First(t.Context())
		check(err)
		if !present {
			t.Fatal("selected reset account disappeared")
		}
		return value
	}
	values := func(value M) map[string]query.Value {
		t.Helper()
		result, err := objects.ModelValues(value)
		check(err)
		return result
	}
	if role.Email != "" && role.Email != role.Login {
		row, err = objects.ApplyValues(row, map[string]query.Value{role.Email: query.String("target@example.test")})
		check(err)
		check(objects.Save(t.Context(), backend, &row, orm.UpdateFieldNames[M](role.Email)))
	}
	principal, _ := values(row)[role.PrincipalID].String()
	loginName, _ := values(row)[role.Login].String()
	otherName, _ := values(other)[role.Login].String()
	otherID, _ := values(other)[role.PrincipalID].String()
	email, _ := values(row)[role.Email].String()
	type durable struct {
		rows     []M
		sessions []sessionmodels.Session
		audits   []auditmodels.Event
	}
	snapshot := func() durable {
		t.Helper()
		rows, err := objects.Using(backend).OrderBy(key.Asc()).All(t.Context())
		check(err)
		records, err := sessionmodels.SessionObjects.Using(backend).OrderBy(sessionmodels.SessionFields.ID.Asc()).All(t.Context())
		check(err)
		audits, err := auditmodels.EventObjects.Using(backend).OrderBy(auditmodels.EventFields.ID.Asc()).All(t.Context())
		check(err)
		return durable{rows, records, audits}
	}
	var now atomic.Int64
	now.Store(time.Now().UTC().Truncate(time.Microsecond).UnixNano())
	clock := func() time.Time { return time.Unix(0, now.Load()).UTC() }
	observed := &resetHTTPHasher{PasswordHasher: hasher}
	keys, err := auth.NewPasswordResetKeyRing(bytes.Repeat([]byte{66}, 32))
	check(err)
	rotatedKeys, err := auth.NewPasswordResetKeyRing(bytes.Repeat([]byte{77}, 32), bytes.Repeat([]byte{66}, 32))
	check(err)
	from, err := mail.ParseAddress("sender@example.test")
	check(err)
	memory, err := mail.NewMemory(mail.MemoryConfig{})
	check(err)
	mailConfig := identity.PasswordResetMailConfig{From: from, Origin: "https://trusted.example.test", ConfirmPath: "/account/reset", SiteName: "Selected model"}
	makeOwners := func(connection database.Backend, keys auth.PasswordResetKeyRing) resetHTTPHost[M] {
		t.Helper()
		store, err := storeFactory(connection, sessiondb.Config{})
		check(err)
		manager, err := sessions.NewManager(store, sessions.Config{Clock: clock})
		check(err)
		verifier, err := store.Authenticator(t.Context(), observed)
		check(err)
		owner, err := resetFactory(store, manager, verifier, accountmodel.PasswordResetConfig[M]{Keys: keys, Clock: clock, MaxPasswordBytes: 64, ValidatePassword: policy})
		check(err)
		return resetHTTPHost[M]{store: store, manager: manager, verifier: verifier, owner: owner}
	}
	first := makeOwners(backend, keys)
	if role.Email == "" {
		t.Run("no_email_role", func(t *testing.T) {
			before := snapshot()
			if first.owner.PasswordResetEmailAvailable() {
				t.Fatal("reset inferred an undeclared email role")
			}
			if _, err := identity.NewPasswordResetMailer(first.owner, memory, mailConfig); err == nil {
				t.Fatal("mail reset constructed without an explicit email role")
			}
			if candidates, err := first.owner.PasswordResetCandidates(t.Context(), "target@example.test"); err == nil || len(candidates) != 0 || !reflect.DeepEqual(snapshot(), before) {
				t.Fatal("unsupported mail lookup changed selected account state", err)
			}
			token, err := first.owner.IssueToken(t.Context(), principal)
			check(err)
			proof, err := first.owner.StartPasswordReset(t.Context(), sessions.ID{}, principal, token.Encoded())
			check(err)
			result, err := first.owner.ResetPassword(t.Context(), proof.ID(), principal, "private-reset-without-email")
			check(err)
			if !result.Record.ID().Valid() || values(readRow())[role.Password].Equal(values(row)[role.Password]) {
				t.Fatal("privileged reset incorrectly required an email field")
			}
		})
		return
	}
	if role.Active.Constant != nil && !*role.Active.Constant {
		t.Run("inactive_constant", func(t *testing.T) {
			before := snapshot()
			candidates, err := first.owner.PasswordResetCandidates(t.Context(), email)
			if err != nil || len(candidates) != 0 {
				t.Fatal("constant inactive role selected recipients", err)
			}
			if token, err := first.owner.IssueToken(t.Context(), principal); err != auth.ErrInvalidResetToken || token.Encoded() != "" || !reflect.DeepEqual(snapshot(), before) {
				t.Fatal("constant inactive role issued a token", err)
			}
		})
		return
	}
	var reports atomic.Int64
	build := func(connection database.Backend, keys auth.PasswordResetKeyRing) resetHTTPHost[M] {
		t.Helper()
		host := makeOwners(connection, keys)
		login, err := host.store.LoginPersistence(host.manager)
		check(err)
		change, err := host.store.PasswordChangePersistence(host.manager, host.verifier, accountmodel.PasswordChangeConfig[M]{ValidatePassword: policy})
		check(err)
		next, err := identityaccount.AllowedNextPaths("")
		check(err)
		host.runtime, err = websession.New(websession.Config{Sessions: host.manager, Authenticator: host.verifier, Authorizer: auth.PrincipalAuthorizer{}, LoginPersistence: login, PasswordChangePersistence: change, PasswordResetPersistence: host.owner, Clock: clock,
			SessionCookie: websession.CookieConfig{Path: "/", AllowInsecure: true}, CSRFCookie: websession.CookieConfig{Path: "/", AllowInsecure: true}, LoginPath: "/account/login/", FallbackPath: "/account/password/", AllowedNextPaths: next})
		check(err)
		host.config, err = settings.New(settings.Definition{ProjectName: "selected_reset", InstalledApps: append(project.InstalledApps(), apps.Config{Name: "example.com/godj-project-bundle/consumer", Label: "account"})})
		check(err)
		mailer, err := identity.NewPasswordResetMailer(host.owner, memory, mailConfig)
		check(err)
		surface, err := identityaccount.New(identityaccount.Config{Apps: host.config.Apps(), Namespace: "account", Auth: host.runtime, PasswordReset: &identityaccount.PasswordResetConfig{Mailer: mailer, ReportError: func(context.Context, error) { reports.Add(1) }}})
		check(err)
		app, err := web.NewApplication(web.Config{Settings: host.config, Routes: surface.Routes(), Middleware: surface.Middleware()})
		check(err)
		host.server = httptest.NewServer(app)
		t.Cleanup(host.server.Close)
		return host
	}
	host := build(backend, keys)
	t.Run("owner_mix_rejected", func(t *testing.T) {
		before := snapshot()
		mailer, err := identity.NewPasswordResetMailer(first.owner, memory, mailConfig)
		check(err)
		if _, err := identityaccount.New(identityaccount.Config{Apps: host.config.Apps(), Namespace: "account", Auth: host.runtime, PasswordReset: &identityaccount.PasswordResetConfig{Mailer: mailer, ReportError: func(context.Context, error) {}}}); err == nil || !reflect.DeepEqual(snapshot(), before) {
			t.Fatal("separately constructed mail owner mixed with completion authority", err)
		}
	})
	newClient := func() *http.Client {
		jar, err := cookiejar.New(nil)
		check(err)
		return &http.Client{Timeout: 30 * time.Second, Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	send := func(client *http.Client, method, path, raw, media, csrf, origin string) (int, http.Header, string) {
		t.Helper()
		request, err := http.NewRequestWithContext(t.Context(), method, host.server.URL+path, strings.NewReader(raw))
		check(err)
		if media != "" {
			request.Header.Set("Content-Type", media)
		}
		if csrf != "" {
			request.Header.Set(host.runtime.CSRFHeader(), csrf)
		}
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		response, err := client.Do(request)
		check(err)
		body, err := io.ReadAll(response.Body)
		check(err)
		check(response.Body.Close())
		if strings.Contains(path, "/reset/") && (response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("Referrer-Policy") != "no-referrer") {
			t.Fatal("selected reset omitted response privacy headers")
		}
		return response.StatusCode, response.Header, string(body)
	}
	csrfPattern := regexp.MustCompile(`name="csrfmiddlewaretoken" value="([^"]+)"`)
	csrfFrom := func(markup string) string {
		t.Helper()
		match := csrfPattern.FindStringSubmatch(markup)
		if len(match) != 2 {
			t.Fatal("selected account Form omitted CSRF")
		}
		return match[1]
	}
	cookie := func(client *http.Client, name string) string {
		base, err := url.Parse(host.server.URL)
		check(err)
		for _, cookie := range client.Jar.Cookies(base) {
			if cookie.Name == name {
				return cookie.Value
			}
		}
		return ""
	}
	current := func(client *http.Client) sessions.Record {
		t.Helper()
		id, err := sessions.ParseID(cookie(client, websession.DefaultSessionCookieName))
		check(err)
		record, found, err := host.store.Load(t.Context(), id)
		check(err)
		if !found {
			t.Fatal("confirmed HTTP cookie missing from selected DB session store")
		}
		return record
	}
	logIn := func(client *http.Client, login, raw string) sessions.Record {
		t.Helper()
		status, _, markup := send(client, "GET", "/account/login/", "", "", "", "")
		if status != 200 {
			t.Fatal("selected login form missing", status)
		}
		data := url.Values{"username": {login}, "password": {raw}, "csrfmiddlewaretoken": {csrfFrom(markup)}}
		status, _, _ = send(client, "POST", "/account/login/", data.Encode(), "application/x-www-form-urlencoded", "", "")
		if status != 302 {
			t.Fatal("selected login failed", status)
		}
		return current(client)
	}
	password := accountPasswordText
	for _, surface := range []string{"form", "json"} {
		t.Run(surface, func(t *testing.T) {
			for _, mode := range []string{"missing", "anonymous", "self", "other"} {
				t.Run(mode, func(t *testing.T) {
					client, sibling := newClient(), newClient()
					oldSibling := logIn(sibling, loginName, password)
					var previous sessions.Record
					switch mode {
					case "self":
						previous = logIn(client, loginName, password)
					case "other":
						previous = logIn(client, otherName, accountPasswordText)
					case "anonymous":
						previous, err = host.manager.Create(t.Context(), map[string]string{"cart": "preserved"})
						check(err)
						base, err := url.Parse(host.server.URL)
						check(err)
						client.Jar.SetCookies(base, []*http.Cookie{{Name: websession.DefaultSessionCookieName, Value: previous.ID().Encoded(), Path: "/"}})
					}
					path := "/account/reset/"
					if surface == "json" {
						path = "/api/account/reset/"
					}
					status, headers, markup := send(client, "GET", path, "", "", "", "")
					csrf := headers.Get(host.runtime.CSRFHeader())
					if surface == "form" {
						csrf = csrfFrom(markup)
						if status != 200 {
							t.Fatal(status)
						}
					} else if status != 204 || csrf == "" {
						t.Fatal("JSON reset bootstrap failed", status)
					}
					requestMail := func(email string) (int, http.Header, string) {
						if surface == "form" {
							return send(client, "POST", path, url.Values{"email": {email}, "csrfmiddlewaretoken": {csrf}}.Encode(), "application/x-www-form-urlencoded", "", "")
						}
						wire, err := json.Marshal(map[string]string{"email": email})
						check(err)
						return send(client, "POST", path, string(wire), "application/json", csrf, "")
					}
					before := snapshot()
					observed.hashes.Store(0)
					unknownStatus, unknownHeaders, unknownBody := requestMail("missing@example.test")
					status, headers, markup = requestMail(" \u2002" + strings.ToUpper(email) + "\u2002 ")
					if status != unknownStatus || headers.Get("Location") != unknownHeaders.Get("Location") || markup != unknownBody || observed.hashes.Load() != 0 || !reflect.DeepEqual(snapshot(), before) {
						t.Fatal("selected mail request leaked account presence or changed DB")
					}
					want := 302
					if surface == "json" {
						want = 204
					}
					if status != want {
						t.Fatal("selected mail request not acknowledged", status)
					}
					deliveries, err := memory.Drain()
					check(err)
					if len(deliveries) != 1 {
						t.Fatal("selected email role did not deliver once", len(deliveries))
					}
					message := deliveries[0].Message()
					address, err := mail.ParseAddress(email)
					check(err)
					if len(message.Recipients()) != 1 || message.Recipients()[0].Mailbox() != address.Mailbox() {
						t.Fatal("selected mail used a default or login column as recipient")
					}
					link := ""
					for _, line := range strings.Split(message.Text(), "\n") {
						if strings.HasPrefix(line, "https://trusted.example.test/account/reset/") {
							link = line
						}
					}
					parsed, err := url.Parse(link)
					check(err)
					parts := strings.Split(parsed.Path, "/")
					if len(parts) != 6 || parsed.Host != "trusted.example.test" || parts[3] != base64.RawURLEncoding.EncodeToString([]byte(principal)) {
						t.Fatal("selected mail escaped configured target/origin")
					}
					rawToken := parts[4]
					confirm := "/account/reset/" + parts[3] + "/set-password/"
					apiPath := "/api/account/reset/" + parts[3] + "/"
					status, headers, markup = send(client, "GET", parsed.Path, "", "", "", "")
					if status != 302 || headers.Get("Location") != confirm || strings.Contains(markup, rawToken) || strings.Contains(headers.Get("Location"), rawToken) {
						t.Fatal("entry did not hide raw recipient token", status)
					}
					proof := current(client)
					if proof.ID() == previous.ID() {
						t.Fatal("entry did not rotate selected proof")
					}
					oldServer := host.server
					host = build(reopen(), rotatedKeys)
					oldServer.Close()
					observed.hashes.Store(0)
					status, headers, markup = send(client, "GET", confirm, "", "", "", "")
					if status != 200 || !strings.Contains(markup, `data-account-view="reset-confirm"`) || strings.Contains(markup, rawToken) {
						t.Fatal("proof did not survive selected runtime/key rotation", status)
					}
					csrf = csrfFrom(markup)
					finish, media := confirm, "application/x-www-form-urlencoded"
					if surface == "json" {
						finish, media = apiPath, "application/json"
						status, headers, _ = send(client, "GET", apiPath, "", "", "", "")
						csrf = headers.Get(host.runtime.CSRFHeader())
						if status != 204 || csrf == "" {
							t.Fatal("restarted JSON proof refused", status)
						}
					}
					input := func(raw, confirmation string) string {
						if surface == "form" {
							return url.Values{"new_password1": {raw}, "new_password2": {confirmation}, "csrfmiddlewaretoken": {csrf}}.Encode()
						}
						wire, err := json.Marshal(map[string]string{"new_password": raw})
						check(err)
						return string(wire)
					}
					for _, refusal := range []string{"csrf", "origin", "weak", "byte_limit", "extra_target", "wrong_target", "fresh_browser"} {
						t.Run(refusal, func(t *testing.T) {
							before := snapshot()
							data, requestCSRF, origin, targetPath, browser := input("next-private-password", "next-private-password"), csrf, "", finish, client
							want := 400
							if surface == "form" {
								want = 200
							}
							switch refusal {
							case "csrf":
								requestCSRF = ""
								data = strings.ReplaceAll(data, csrf, "invalid")
								want = 403
							case "origin":
								origin = "https://foreign.example.test"
								want = 403
							case "weak":
								data = input("short", "short")
							case "byte_limit":
								data = input(strings.Repeat("界", 22), strings.Repeat("界", 22))
							case "extra_target":
								want = 400
								if surface == "form" {
									data += "&target=other"
								} else {
									data = strings.TrimSuffix(data, "}") + `,"target":"other"}`
								}
							case "wrong_target":
								targetPath = strings.Replace(finish, parts[3], base64.RawURLEncoding.EncodeToString([]byte(otherID)), 1)
								if surface == "json" {
									want = 403
								}
							case "fresh_browser":
								browser = newClient()
								base, err := url.Parse(host.server.URL)
								check(err)
								browser.Jar.SetCookies(base, []*http.Cookie{{Name: websession.DefaultCSRFCookieName, Value: cookie(client, websession.DefaultCSRFCookieName), Path: "/"}})
								if surface == "json" {
									want = 403
								}
							}
							status, headers, body := send(browser, "POST", targetPath, data, media, requestCSRF, origin)
							if status != want || headers.Get("Set-Cookie") != "" || observed.hashes.Load() != 0 || !reflect.DeepEqual(snapshot(), before) || strings.Contains(body, rawToken) || strings.Contains(body, "next-private-password") {
								t.Fatal("selected reset refusal mutated, hashed or exposed a secret", status, want)
							}
						})
					}
					beforeValues := values(readRow())
					beforeAudit := len(snapshot().audits)
					csrfCookie := cookie(client, websession.DefaultCSRFCookieName)
					nextPassword := "  selected-reset-" + surface + "-" + mode + "  "
					now.Add(int64(time.Minute))
					status, headers, body := send(client, "POST", finish, input(nextPassword, nextPassword), media, csrf, "")
					want = 302
					if surface == "json" {
						want = 204
					}
					if status != want || headers.Get("Set-Cookie") == "" || observed.hashes.Load() != 1 || reports.Load() != 0 || strings.Contains(body, nextPassword) || strings.Contains(body, rawToken) || cookie(client, websession.DefaultCSRFCookieName) != csrfCookie {
						t.Fatal("selected reset completion did not publish one confirmed cookie", status, want, observed.hashes.Load(), reports.Load())
					}
					if surface == "form" && headers.Get("Location") != "/account/reset/complete/" {
						t.Fatal("selected reset completion redirect")
					}
					afterValues := values(readRow())
					beforeRevision, _ := beforeValues[role.Revision].Integer()
					afterRevision, _ := afterValues[role.Revision].Integer()
					encoded, _ := afterValues[role.Password].String()
					valid, err := hasher.Verify(t.Context(), nextPassword, encoded)
					check(err)
					if !valid || afterRevision != beforeRevision+1 || afterValues[role.Password].Equal(beforeValues[role.Password]) || len(snapshot().audits) != beforeAudit+1 {
						t.Fatal("HTTP reset omitted selected password/revision/audit")
					}
					for name, value := range beforeValues {
						if name != role.Password && name != role.Revision && !value.Equal(afterValues[name]) {
							t.Fatal("selected reset overwrote profile/LastLogin", name)
						}
					}
					for _, revoked := range []sessions.Record{proof, oldSibling} {
						if result, found, err := host.store.Load(t.Context(), revoked.ID()); err != nil || found || result.ID().Valid() {
							t.Fatal("HTTP reset retained old target/proof session", err)
						}
					}
					if mode == "self" {
						if cookie(client, websession.DefaultSessionCookieName) != "" {
							t.Fatal("self reset retained login cookie")
						}
					} else {
						current := current(client)
						expected := proof.WithoutValue(auth.SessionResetPrincipalIDKey).WithoutValue(auth.SessionResetTokenKey)
						if !reflect.DeepEqual(current.Values(), expected.Values()) || !current.CreatedAt().Equal(proof.CreatedAt()) || !current.AbsoluteExpiresAt().Equal(proof.AbsoluteExpiresAt()) {
							t.Fatal("reset changed unrelated current payload or absolute lifetime")
						}
					}
					audit := snapshot().audits[beforeAudit]
					if audit.ActorID != principal || audit.ObjectID != id || audit.Model != role.Model.AppLabel+"."+role.Model.ModelName {
						t.Fatal("reset audit did not identify actual selected model/key")
					}
					// Replay is read-only even when an unrelated session is presented.
					after := snapshot()
					status, headers, body = send(client, "GET", parsed.Path, "", "", "", "")
					if status != 200 || headers.Get("Set-Cookie") != "" || !strings.Contains(body, `data-account-view="reset-invalid"`) || !reflect.DeepEqual(snapshot(), after) {
						t.Fatal("completed reset link replay was admitted", status)
					}
					password = nextPassword
				})
			}
		})
	}
	for _, field := range metadata.Fields {
		if field.Name != role.Email || field.MaxLength != 0 {
			continue
		}
		t.Run("stored_email_bound", func(t *testing.T) {
			current := readRow()
			changed, err := objects.ApplyValues(current, map[string]query.Value{role.Email: query.String(strings.Repeat("x", auth.MaximumPasswordResetEmailBytes+1))})
			check(err)
			check(objects.Save(t.Context(), backend, &changed, orm.UpdateFieldNames[M](role.Email)))
			before := snapshot()
			if token, err := host.owner.IssueToken(t.Context(), principal); err == nil || token.Encoded() != "" || !reflect.DeepEqual(snapshot(), before) {
				t.Fatal("unbounded selected text email reached token issuance", err)
			}
		})
	}
}
