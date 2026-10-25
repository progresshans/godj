package consumer

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"example.com/godj-project-bundle/project"
	auditmodels "github.com/progresshans/godj/account/audit/models"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	identityadmin "github.com/progresshans/godj/identity/admin"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/sessions"
	sessionmodels "github.com/progresshans/godj/sessions/database/models"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/settings/database"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
	websession "github.com/progresshans/godj/web/sessionauth"
)

type adminPasswordHasher struct {
	auth.PasswordHasher
	hashes   atomic.Int64
	verifies atomic.Int64
}

func (hasher *adminPasswordHasher) Hash(ctx context.Context, raw string) (string, error) {
	hasher.hashes.Add(1)
	return hasher.PasswordHasher.Hash(ctx, raw)
}

func (hasher *adminPasswordHasher) Verify(ctx context.Context, raw, encoded string) (bool, error) {
	hasher.verifies.Add(1)
	return hasher.PasswordHasher.Verify(ctx, raw, encoded)
}

type adminPasswordOverlay struct {
	denied          atomic.Bool
	accessDenied    atomic.Bool
	denyFinalAccess atomic.Bool
	accessChecks    atomic.Int64
}

func (overlay *adminPasswordOverlay) Allowed(ctx context.Context, actor auth.Principal, permission auth.Permission) (bool, error) {
	if permission == "account.admin_access" {
		count := overlay.accessChecks.Add(1)
		if overlay.accessDenied.Load() || overlay.denyFinalAccess.Load() && count > 1 {
			return false, nil
		}
	}
	if permission == "account.add" || permission == "account.delete" || permission == "account.password_confirm" && overlay.denied.Load() {
		return false, nil
	}
	return (auth.PrincipalAuthorizer{}).Allowed(ctx, actor, permission)
}

// This host enables selected profile reads/edits and product password commands
// in the real common Admin Site. Add/delete are explicitly denied by host
// policy. This is not the unfinished automatic account/catalog registration.
func assertAccountAdminPasswordHTTP[M any](t *testing.T, backend database.Backend, binding accountmodel.Binding[M], credentials *accountmodel.CredentialStore[M], selected *accountmodel.SessionStore[M], hasher auth.PasswordHasher, original M, profileField string,
	factory func(*accountmodel.SessionStore[M], *accountmodel.Authenticator[M], auth.Authorizer, accountmodel.PasswordEditConfig[M]) (*accountmodel.PasswordEditor[M], error)) {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	objects, err := binding.Objects()
	check(err)
	metadata, err := objects.Metadata()
	check(err)
	role, err := binding.Role()
	check(err)
	keyValue, present, err := objects.PrimaryKey(original)
	check(err)
	id, integer := keyValue.Integer()
	if !present || !integer {
		t.Fatal("Admin selected row key absent")
	}
	var keyField ir.Field
	for _, field := range metadata.Fields {
		if field.PrimaryKey {
			keyField = field
		}
	}
	key := orm.NewAutoField[M](keyField)
	values, err := objects.ModelValues(original)
	check(err)
	loginName, _ := values[role.Login].String()
	profileValue, _ := values[profileField].String()
	readRow := func() M {
		t.Helper()
		value, present, err := objects.Using(backend).Filter(key.Exact(id)).OrderBy(key.Asc()).First(t.Context())
		check(err)
		if !present {
			t.Fatal("Admin target disappeared")
		}
		return value
	}
	type durable struct {
		rows     []M
		sessions []sessionmodels.Session
		audits   []auditmodels.Event
	}
	snapshot := func() durable {
		t.Helper()
		rows, err := objects.Using(backend).OrderBy(key.Asc()).All(t.Context())
		check(err)
		sessions, err := sessionmodels.SessionObjects.Using(backend).OrderBy(sessionmodels.SessionFields.ID.Asc()).All(t.Context())
		check(err)
		audits, err := auditmodels.EventObjects.Using(backend).OrderBy(auditmodels.EventFields.ID.Asc()).All(t.Context())
		check(err)
		return durable{rows, sessions, audits}
	}
	observed := &adminPasswordHasher{PasswordHasher: hasher}
	verifier, err := selected.Authenticator(t.Context(), observed)
	check(err)
	overlay := &adminPasswordOverlay{}
	now := time.Now().UTC().Truncate(time.Microsecond)
	policyCalls := atomic.Int64{}
	owner, err := factory(selected, verifier, overlay, accountmodel.PasswordEditConfig[M]{ChangePermission: "account.password", AdditionalPermissions: []auth.Permission{"account.password_confirm"}, MaxPasswordBytes: 64, Now: func() time.Time { return now }, ValidatePassword: func(_ context.Context, raw string, row M) (validation.Errors, error) {
		policyCalls.Add(1)
		values, err := objects.ModelValues(row)
		if err != nil {
			return validation.Errors{}, err
		}
		encoded, _ := values[role.Password].String()
		profile, _ := values[profileField].String()
		if encoded != "" || profile != profileValue {
			return validation.Errors{}, errors.New("selected typed policy lost profile or exposed credential")
		}
		if raw == "policy rejected" {
			return validation.NewErrors(validation.New("password", "custom_policy")), nil
		}
		return validation.Errors{}, nil
	}})
	check(err)
	commands, err := identityadmin.PasswordCommands(owner)
	check(err)
	reader, err := accountmodel.NewProfileReader(credentials, overlay, accountmodel.ProfileReadConfig{Fields: []string{keyField.Name, role.Login, profileField, role.Revision}, ViewPermission: "account.view", ChangePermission: "account.change"})
	check(err)
	editor, err := accountmodel.NewProfileEditor(credentials, overlay, accountmodel.ProfileConfig[M]{Fields: []string{profileField}, ChangePermission: "account.change"})
	check(err)
	form, err := formmodel.NewSpecForFields(metadata, []string{profileField})
	check(err)
	projector, err := admin.NewModelProjector(metadata, func(row accountmodel.Profile[M], field ir.Field) (query.Value, bool) {
		value, present := row.View().Values()[field.Name]
		return value, present
	}, keyField.Name, role.Login, profileField, role.Revision)
	check(err)
	configured, err := settings.New(settings.Definition{ProjectName: "selected_account_admin", InstalledApps: append(project.InstalledApps(), apps.Config{Name: "example.com/godj-project-bundle/consumer", Label: "accountadmin"})})
	check(err)
	builder := admin.NewBuilder(configured.Apps())
	check(admin.RegisterModel(builder, admin.ModelConfig[accountmodel.Profile[M]]{
		AppLabel: role.Model.AppLabel, Slug: "accounts", Model: metadata, FormFields: []string{profileField}, ListFields: []string{keyField.Name, role.Login, profileField}, RevisionField: role.Revision,
		Permissions: admin.Permissions{View: "account.view", Add: "account.add", Change: "account.change", Delete: "account.delete"}, Commands: commands,
		List: func(ctx context.Context, actor auth.Principal, request admin.ListRequest) (admin.Page[accountmodel.Profile[M]], error) {
			page, err := reader.List(ctx, actor, request.Offset, request.Limit)
			return admin.Page[accountmodel.Profile[M]]{Items: page.Items, Total: page.Total, Offset: request.Offset, Limit: request.Limit}, err
		},
		Get: func(ctx context.Context, actor auth.Principal, id int64) (accountmodel.Profile[M], bool, error) {
			row, err := reader.Get(ctx, actor, id)
			if errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeNotFound}) {
				return row, false, nil
			}
			return row, err == nil, err
		},
		Snapshot: func(row accountmodel.Profile[M]) (admin.Object, error) {
			label, _ := row.View().Values()[role.Login].String()
			return projector.Project(row, row.ID(), label)
		},
		Initial: func(row accountmodel.Profile[M]) (map[string]forms.Value, error) {
			return formmodel.InitialView(objects, form, row.View())
		},
		Create: func(context.Context, auth.Principal, formmodel.BoundForm, admin.InlineSubmission) (accountmodel.Profile[M], error) {
			return accountmodel.Profile[M]{}, admin.NewOperationError(admin.OperationDenied, nil)
		},
		Delete: func(context.Context, auth.Principal, admin.Mutation) (accountmodel.Profile[M], error) {
			return accountmodel.Profile[M]{}, admin.NewOperationError(admin.OperationDenied, nil)
		},
		Update: func(ctx context.Context, actor auth.Principal, mutation admin.Mutation, bound formmodel.BoundForm, _ admin.InlineSubmission) (accountmodel.Profile[M], []string, error) {
			input, err := formmodel.PrepareInput(objects, bound)
			if err != nil {
				return accountmodel.Profile[M]{}, nil, err
			}
			changed, err := editor.Update(ctx, actor, mutation.ID, mutation.Revision, input)
			if err != nil {
				return accountmodel.Profile[M]{}, nil, err
			}
			row, err := reader.Get(ctx, actor, changed.ID)
			return row, changed.ChangedFields, err
		},
		History: func(ctx context.Context, actor auth.Principal, id int64, request admin.HistoryRequest) ([]admin.AuditEntry, error) {
			entries, err := reader.History(ctx, actor, id, request.Limit)
			if err != nil {
				return nil, err
			}
			result := make([]admin.AuditEntry, len(entries))
			for i, entry := range entries {
				result[i] = admin.AuditEntry{Sequence: uint64(entry.Sequence()), ActorID: entry.ActorID(), Model: entry.Model(), ObjectID: entry.ObjectID(), Action: entry.Action(), ChangedFields: entry.ChangedFields()}
			}
			return result, nil
		},
	}))
	registry, err := builder.Build()
	check(err)
	paths, err := admin.SiteAllowedNextPaths(registry, "/admin")
	check(err)
	manager, err := sessions.NewManager(selected, sessions.Config{Clock: func() time.Time { return now }})
	check(err)
	loginOwner, err := selected.LoginPersistence(manager)
	check(err)
	runtime, err := websession.New(websession.Config{Sessions: manager, Authenticator: verifier, Authorizer: overlay, LoginPersistence: loginOwner, Clock: func() time.Time { return now }, SessionCookie: websession.CookieConfig{Path: "/", AllowInsecure: true}, CSRFCookie: websession.CookieConfig{Path: "/", AllowInsecure: true}, LoginPath: "/admin/login/", FallbackPath: "/admin/", AllowedNextPaths: paths})
	check(err)
	site, err := admin.NewSite(admin.SiteConfig{Apps: configured.Apps(), Namespace: "accountadmin", Registry: registry, Auth: runtime, AccessPermission: "account.admin_access"})
	check(err)
	application, err := web.NewApplication(web.Config{Settings: configured, Routes: site.Routes()})
	check(err)
	server := httptest.NewServer(application)
	t.Cleanup(server.Close)
	baseURL, err := url.Parse(server.URL)
	check(err)
	newClient := func() *http.Client {
		jar, err := cookiejar.New(nil)
		check(err)
		return &http.Client{Transport: server.Client().Transport, Timeout: 30 * time.Second, Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	send := func(client *http.Client, method, path string, data url.Values) (int, http.Header, string) {
		t.Helper()
		request, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, strings.NewReader(data.Encode()))
		check(err)
		if method == "POST" {
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		response, err := client.Do(request)
		check(err)
		body, err := io.ReadAll(response.Body)
		check(err)
		check(response.Body.Close())
		return response.StatusCode, response.Header, string(body)
	}
	csrf := func(body string) string {
		t.Helper()
		match := regexp.MustCompile(`name="csrfmiddlewaretoken" value="([^"]+)"`).FindStringSubmatch(body)
		if len(match) != 2 {
			t.Fatal("common Admin CSRF missing")
		}
		return html.UnescapeString(match[1])
	}
	login := func(client *http.Client, raw string, expected int) {
		t.Helper()
		status, _, body := send(client, "GET", "/admin/login/", nil)
		if status != 200 {
			t.Fatal("Admin login form", status)
		}
		status, _, body = send(client, "POST", "/admin/login/", url.Values{"username": {loginName}, "password": {raw}, "csrfmiddlewaretoken": {csrf(body)}, "next": {"/admin/accounts/"}})
		if status != expected || strings.Contains(body, raw) {
			t.Fatal("Admin selected account login", status, expected)
		}
	}
	cookieID := func(client *http.Client) sessions.ID {
		t.Helper()
		for _, cookie := range client.Jar.Cookies(baseURL) {
			if cookie.Name == websession.DefaultSessionCookieName {
				id, err := sessions.ParseID(cookie.Value)
				check(err)
				return id
			}
		}
		t.Fatal("Admin session missing")
		return sessions.ID{}
	}
	input, err := verifier.LoginInput()
	check(err)
	loginCases := []string{"login_presentation", "login_csrf", "login_input_limit", "login_byte_limit", "login_duplicate", "login_reserved", "login_inactive", "login_access", "login_final_access", "login_fullwidth_policy", "login_trimmed"}
	if role.Staff.Field != "" {
		loginCases = append(loginCases, "login_staff")
	}
	for _, name := range loginCases {
		t.Run(name, func(t *testing.T) {
			probe := newClient()
			status, _, markup := send(probe, "GET", "/admin/login/", nil)
			if status != 200 {
				t.Fatal("selected Admin login page", status)
			}
			// Direct fixture mutation models a persisted inactive/non-staff row.
			// It is not a product account-edit path or an audit receipt.
			setFlag := func(name string, value bool) {
				t.Helper()
				for _, field := range metadata.Fields {
					if field.Name == name {
						count, err := objects.Using(backend).Filter(key.Exact(id)).Update(t.Context(), orm.Assign(orm.NewBooleanField[M](field), value))
						check(err)
						if count != 1 {
							t.Fatal("login fixture lost selected account")
						}
						return
					}
				}
				t.Fatal("login fixture selected an absent Boolean")
			}
			if name == "login_inactive" {
				setFlag(role.Active.Field, false)
				defer setFlag(role.Active.Field, true)
			}
			if name == "login_staff" {
				setFlag(role.Staff.Field, false)
				defer setFlag(role.Staff.Field, true)
			}
			before := snapshot()
			observed.hashes.Store(0)
			observed.verifies.Store(0)
			overlay.accessChecks.Store(0)
			if name == "login_presentation" {
				limit, label := 0, ""
				for _, field := range metadata.Fields {
					if field.Name == role.Login {
						limit, label = field.MaxLength, field.GoName
					}
				}
				if limit == 0 || limit > auth.MaximumUsernameBytes {
					limit = auth.MaximumUsernameBytes
				}
				if input.Label() != label || input.MaxLength() != limit || string(input.Normalization()) != string(role.LoginFormNormalization) || !strings.Contains(markup, "<label>"+html.EscapeString(label)+" <input") || !strings.Contains(markup, `maxlength="`+strconv.Itoa(limit)+`"`) {
					t.Fatal("Admin substituted default account input metadata")
				}
				if !reflect.DeepEqual(snapshot(), before) || observed.hashes.Load() != 0 || observed.verifies.Load() != 0 {
					t.Fatal("login presentation changed durable/password state")
				}
				return
			}
			data := url.Values{"username": {loginName}, "password": {accountPasswordText}, "csrfmiddlewaretoken": {csrf(markup)}, "next": {"https://foreign.test/"}}
			want, verifies, admitted := 200, int64(0), false
			switch name {
			case "login_csrf":
				data.Del("csrfmiddlewaretoken")
				want = 403
			case "login_input_limit":
				data.Set("username", strings.Repeat("x", input.MaxLength()+1))
			case "login_byte_limit":
				data.Set("username", strings.Repeat("界", auth.MaximumUsernameBytes/3+1))
			case "login_duplicate":
				data.Add("username", loginName)
				want = 400
			case "login_reserved":
				data.Set(role.PrincipalID, "private forged identity")
				want = 400
			case "login_inactive", "login_staff":
				verifies = 1
			case "login_access":
				overlay.accessDenied.Store(true)
				defer overlay.accessDenied.Store(false)
				verifies = 1
			case "login_final_access":
				overlay.denyFinalAccess.Store(true)
				defer overlay.denyFinalAccess.Store(false)
				verifies = 1
			case "login_fullwidth_policy":
				data.Set("username", strings.Map(func(r rune) rune {
					if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
						return r + 0xfee0
					}
					return r
				}, loginName))
				verifies = 1
				admitted = role.LoginFormNormalization == ir.AccountUnicode16NFKC || role.AuthenticateNormalization == ir.AccountUnicode16NFKC
			case "login_trimmed":
				data.Set("username", "\u001c  "+loginName+"\u3000")
				verifies, admitted = 1, true
			}
			if admitted {
				want = 302
			}
			status, headers, body := send(probe, "POST", "/admin/login/", data)
			if status != want || observed.hashes.Load() != 0 || observed.verifies.Load() != verifies || strings.Contains(body, accountPasswordText) {
				t.Fatal("Admin login input/admission boundary", name, status, want, observed.verifies.Load(), verifies)
			}
			if !admitted {
				if headers.Get("Set-Cookie") != "" || !reflect.DeepEqual(snapshot(), before) {
					t.Fatal("denied Admin login changed row, session, audit or cookie")
				}
				if want == 200 && !strings.Contains(body, `data-login-error="invalid_credentials"`) {
					t.Fatal("login denial lost uniform error")
				}
				if name == "login_final_access" && overlay.accessChecks.Load() != 2 {
					t.Fatal("login persistence omitted final admission")
				}
				return
			}
			if headers.Get("Location") != "/admin/" || headers.Get("Set-Cookie") == "" {
				t.Fatal("Admin login lost safe next or session cookie")
			}
			record, found, err := selected.Load(t.Context(), cookieID(probe))
			check(err)
			principalID, _ := values[role.PrincipalID].String()
			actualID, _ := record.Value(auth.SessionPrincipalIDKey)
			if !found || actualID != principalID {
				t.Fatal("Admin session did not commit to the selected store/account")
			}
			after := snapshot()
			if len(after.sessions) != len(before.sessions)+1 || !reflect.DeepEqual(after.audits, before.audits) || len(after.rows) != 1 || len(before.rows) != 1 {
				t.Fatal("Admin login durable effect outside selected session")
			}
			afterRow := after.rows[0]
			if role.LastLogin != "" {
				previous, err := objects.ModelValues(before.rows[0])
				check(err)
				current, err := objects.ModelValues(afterRow)
				check(err)
				at, present := current[role.LastLogin].DateTime()
				if !present || !at.Equal(now) {
					t.Fatal("Admin login omitted selected LastLogin")
				}
				afterRow, err = objects.ApplyValues(afterRow, map[string]query.Value{role.LastLogin: previous[role.LastLogin]})
				check(err)
			}
			if !reflect.DeepEqual(afterRow, before.rows[0]) {
				t.Fatal("Admin login changed revision, profile or credential")
			}
		})
	}
	client, sibling := newClient(), newClient()
	login(client, accountPasswordText, 302)
	login(sibling, accountPasswordText, 302)
	old, oldSibling := cookieID(client), cookieID(sibling)
	path := "/admin/accounts/command/password/?id=" + strconv.FormatInt(id, 10)
	changePath := "/admin/accounts/change/?id=" + strconv.FormatInt(id, 10)
	status, _, body := send(client, "GET", changePath, nil)
	if status != 200 || !strings.Contains(body, path) {
		t.Fatal("selected password command undiscoverable", status)
	}
	status, _, body = send(client, "GET", path, nil)
	if status != 200 || !strings.Contains(body, `type="password"`) || strings.Contains(body, `name="`+role.Password+`"`) || strings.Contains(body, `name="`+role.PrincipalID+`"`) {
		t.Fatal("selected Admin command exposed stored input", status)
	}
	current, err := objects.ModelValues(readRow())
	check(err)
	revision, _ := current[role.Revision].Integer()
	for _, mode := range []string{"csrf", "mismatch", "byte_limit", "policy", "reserved", "stale", "additional_permission"} {
		t.Run(mode, func(t *testing.T) {
			status, _, page := send(client, "GET", path, nil)
			if status != 200 {
				t.Fatal("password command form", status)
			}
			data := url.Values{"password1": {"private-new-password"}, "password2": {"private-new-password"}, "expected_revision": {strconv.FormatInt(revision, 10)}, "csrfmiddlewaretoken": {csrf(page)}}
			want := 200
			switch mode {
			case "csrf":
				data.Del("csrfmiddlewaretoken")
				want = 403
			case "mismatch":
				data.Set("password2", "private-other-password")
			case "byte_limit":
				data.Set("password1", strings.Repeat("界", 22))
				data.Set("password2", data.Get("password1"))
			case "policy":
				data.Set("password1", "policy rejected")
				data.Set("password2", "policy rejected")
			case "reserved":
				data.Set(role.Password, "forged-hash")
				want = 400
			case "stale":
				data.Set("expected_revision", strconv.FormatInt(revision+1, 10))
				want = 409
			case "additional_permission":
				overlay.denied.Store(true)
				defer overlay.denied.Store(false)
				want = 403
			}
			before := snapshot()
			observed.hashes.Store(0)
			status, headers, response := send(client, "POST", path, data)
			if status != want || observed.hashes.Load() != 0 || headers.Get("Location") != "" || headers.Get("Retry-After") != "" || !reflect.DeepEqual(before, snapshot()) || strings.Contains(response, "private-new") || strings.Contains(response, "policy rejected") || strings.Contains(response, "forged-hash") {
				t.Fatal("Admin rejection leaked or changed durable state", mode, status, want)
			}
			if mode == "policy" && !strings.Contains(response, `data-error-code="custom_policy"`) {
				t.Fatal("typed policy did not reach confirmation field")
			}
			if mode == "additional_permission" {
				status, _, response = send(client, "GET", changePath, nil)
				if status != 200 || strings.Contains(response, path) {
					t.Fatal("additional grant not applied to discovery")
				}
				status, _, _ = send(client, "GET", path, nil)
				if status != 403 {
					t.Fatal("additional grant not applied to GET")
				}
			}
		})
	}
	const changedPassword = "  new selected Admin λ password  "
	t.Run("change", func(t *testing.T) {
		before := readRow()
		beforeValues, err := objects.ModelValues(before)
		check(err)
		status, _, page := send(client, "GET", path, nil)
		if status != 200 {
			t.Fatal(status)
		}
		observed.hashes.Store(0)
		policyCalls.Store(0)
		status, _, body := send(client, "POST", path, url.Values{"password1": {changedPassword}, "password2": {changedPassword}, "expected_revision": {strconv.FormatInt(revision, 10)}, "csrfmiddlewaretoken": {csrf(page)}})
		if status != 302 || observed.hashes.Load() != 1 || policyCalls.Load() != 2 || strings.Contains(body, changedPassword) {
			t.Fatal("Admin password commit", status)
		}
		after := readRow()
		afterValues, err := objects.ModelValues(after)
		check(err)
		encoded, _ := afterValues[role.Password].String()
		oldEncoded, _ := beforeValues[role.Password].String()
		actualRevision, _ := afterValues[role.Revision].Integer()
		if actualRevision != revision+1 || encoded == oldEncoded {
			t.Fatal("Admin password/revision did not commit")
		}
		preserved, err := objects.ApplyValues(after, map[string]query.Value{role.Password: beforeValues[role.Password], role.Revision: beforeValues[role.Revision]})
		check(err)
		if !reflect.DeepEqual(preserved, before) {
			t.Fatal("Admin command overwrote profile, roles or last login")
		}
		for _, bearer := range []sessions.ID{old, oldSibling} {
			if _, present, err := selected.Load(t.Context(), bearer); present || err != nil {
				t.Fatal("Admin password retained a target session", err)
			}
		}
		if ok, err := hasher.Verify(t.Context(), changedPassword, encoded); !ok || err != nil {
			t.Fatal("Admin password bytes changed", err)
		}
		status, _, _ = send(sibling, "GET", changePath, nil)
		if status != 302 {
			t.Fatal("revoked browser remained authenticated")
		}
		login(newClient(), accountPasswordText, 200)
	})
	client = newClient()
	login(client, changedPassword, 302)
	current, err = objects.ModelValues(readRow())
	check(err)
	revision, _ = current[role.Revision].Integer()
	disablePath := "/admin/accounts/command/disable-password/?id=" + strconv.FormatInt(id, 10)
	t.Run("disable_confirmation", func(t *testing.T) {
		status, _, page := send(client, "GET", disablePath, nil)
		if status != 200 {
			t.Fatal(status)
		}
		before := snapshot()
		observed.hashes.Store(0)
		status, _, _ = send(client, "POST", disablePath, url.Values{"expected_revision": {strconv.FormatInt(revision, 10)}, "csrfmiddlewaretoken": {csrf(page)}})
		if status != 200 || observed.hashes.Load() != 0 || !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("disable lacked explicit confirmation")
		}
	})
	t.Run("disable", func(t *testing.T) {
		bearer := cookieID(client)
		status, _, page := send(client, "GET", disablePath, nil)
		if status != 200 {
			t.Fatal(status)
		}
		observed.hashes.Store(0)
		policyCalls.Store(0)
		status, _, _ = send(client, "POST", disablePath, url.Values{"confirm": {"on"}, "expected_revision": {strconv.FormatInt(revision, 10)}, "csrfmiddlewaretoken": {csrf(page)}})
		if status != 302 || observed.hashes.Load() != 0 || policyCalls.Load() != 0 {
			t.Fatal("disable used password work or failed", status)
		}
		values, err := objects.ModelValues(readRow())
		check(err)
		actualRevision, _ := values[role.Revision].Integer()
		principal, _ := values[role.PrincipalID].String()
		credential, present, err := credentials.CredentialByID(t.Context(), principal)
		check(err)
		if !present || credential.HasUsablePassword() || actualRevision != revision+1 {
			t.Fatal("disabled credential did not persist")
		}
		if _, present, err := selected.Load(t.Context(), bearer); err != nil || present {
			t.Fatal("disable retained its own browser")
		}
		entries, err := reader.History(t.Context(), credential.Principal(), id, 100)
		check(err)
		if len(entries) != 3 {
			t.Fatal("creation and two password audit entries missing", len(entries))
		}
		for _, entry := range entries[1:] {
			if entry.Model() != role.Model.AppLabel+"."+role.Model.ModelName || entry.ObjectID() != id || !reflect.DeepEqual(entry.ChangedFields(), []string{role.Password}) || entry.ActorID() != principal {
				t.Fatal("selected Admin audit source/field differs")
			}
		}
		login(newClient(), changedPassword, 200)
	})
	if len(registry.All()) != 1 {
		t.Fatal(fmt.Sprintf("unexpected hidden model registration: %d", len(registry.All())))
	}
}
