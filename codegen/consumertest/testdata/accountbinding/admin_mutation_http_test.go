package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"slices"
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
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
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

// A host-selected account editor runs through the common Admin Site. Its
// confirmed write can remove the actor's own read authority and every session.
// This exercises the mutation contract, not automatic account registration.
func assertAccountAdminMutationHTTP[M any](t *testing.T, backend database.Backend, binding accountmodel.Binding[M], credentials *accountmodel.CredentialStore[M], selected *accountmodel.SessionStore[M], hasher auth.PasswordHasher, original M, profileField string,
	factory func(*accountmodel.SessionStore[M], auth.Authorizer, accountmodel.AccountEditConfig[M]) (*accountmodel.AccountEditor[M], error)) {
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
	if role.Active.Field == "" {
		t.Fatal("consumer requires a stored active flag")
	}
	keyValue, present, err := objects.PrimaryKey(original)
	check(err)
	id, integer := keyValue.Integer()
	if !present || !integer || id <= 0 {
		t.Fatal("Admin target key absent")
	}
	var keyField ir.Field
	for _, field := range metadata.Fields {
		if field.PrimaryKey {
			keyField = field
		}
	}
	key := orm.NewAutoField[M](keyField)
	originalValues, err := objects.ModelValues(original)
	check(err)
	loginName, _ := originalValues[role.Login].String()
	principalID, _ := originalValues[role.PrincipalID].String()
	actorCredential, present, err := credentials.CredentialByID(t.Context(), principalID)
	check(err)
	if !present {
		t.Fatal("first account credential absent")
	}
	actor := actorCredential.Principal()
	fields := []string{profileField, role.Active.Field}
	owner, err := factory(selected, auth.PrincipalAuthorizer{}, accountmodel.AccountEditConfig[M]{Fields: fields, ChangePermission: "account.change", Validate: func(_ context.Context, _ db.Queryer, _, candidate M, _ accountmodel.RoleMemberships) (validation.Errors, error) {
		values, err := objects.ModelValues(candidate)
		if err != nil {
			return validation.Errors{}, err
		}
		profile, _ := values[profileField].String()
		active, _ := values[role.Active.Field].Boolean()
		if !active && profile != "KR" {
			return validation.NewErrors(validation.New(validation.NonField, "profile_required")), nil
		}
		return validation.Errors{}, nil
	}})
	check(err)
	readFields := []string{keyField.Name, role.Login, profileField, role.Active.Field, role.Revision}
	reader, err := accountmodel.NewProfileReader(credentials, auth.PrincipalAuthorizer{}, accountmodel.ProfileReadConfig{Fields: readFields, ViewPermission: "account.view", ChangePermission: "account.change"})
	check(err)
	form, err := formmodel.NewSpecForFields(metadata, fields)
	check(err)
	projector, err := admin.NewModelProjector(metadata, func(row accountmodel.Profile[M], field ir.Field) (query.Value, bool) {
		value, present := row.View().Values()[field.Name]
		return value, present
	}, readFields...)
	check(err)
	configured, err := settings.New(settings.Definition{ProjectName: "selected_account_admin_mutation", InstalledApps: append(project.InstalledApps(), apps.Config{Name: "example.com/godj-project-bundle/consumer", Label: "accountadmin"})})
	check(err)
	var committed atomic.Bool
	var lateReads, attempts atomic.Int64
	var confirmed accountmodel.AccountChange
	builder := admin.NewBuilder(configured.Apps())
	check(admin.RegisterModel(builder, admin.ModelConfig[accountmodel.Profile[M]]{
		AppLabel: role.Model.AppLabel, Slug: "accounts", Model: metadata, FormFields: fields, ListFields: []string{keyField.Name, role.Login, profileField}, RevisionField: role.Revision,
		Permissions: admin.Permissions{View: "account.view", Add: "account.add", Change: "account.change", Delete: "account.delete"},
		List: func(ctx context.Context, actor auth.Principal, request admin.ListRequest) (admin.Page[accountmodel.Profile[M]], error) {
			page, err := reader.List(ctx, actor, request.Offset, request.Limit)
			return admin.Page[accountmodel.Profile[M]]{Items: page.Items, Total: page.Total, Offset: request.Offset, Limit: request.Limit}, err
		},
		Get: func(ctx context.Context, actor auth.Principal, id int64) (accountmodel.Profile[M], bool, error) {
			if committed.Load() {
				lateReads.Add(1)
			}
			row, err := reader.Get(ctx, actor, id)
			return row, err == nil, err
		},
		Snapshot: func(row accountmodel.Profile[M]) (admin.Object, error) {
			if committed.Load() {
				lateReads.Add(1)
				return admin.Object{}, errors.New("no display after commit")
			}
			label, _ := row.View().Values()[role.Login].String()
			return projector.Project(row, row.ID(), label)
		},
		Initial: func(row accountmodel.Profile[M]) (map[string]forms.Value, error) {
			if committed.Load() {
				lateReads.Add(1)
			}
			return formmodel.InitialView(objects, form, row.View())
		},
		Create: func(context.Context, auth.Principal, formmodel.BoundForm, admin.InlineSubmission) (admin.MutationResult, error) {
			return admin.MutationResult{}, admin.NewOperationError(admin.OperationDenied, nil)
		},
		Delete: func(context.Context, auth.Principal, admin.Mutation) (admin.MutationResult, error) {
			return admin.MutationResult{}, admin.NewOperationError(admin.OperationDenied, nil)
		},
		Update: func(ctx context.Context, actor auth.Principal, mutation admin.Mutation, bound formmodel.BoundForm, _ admin.InlineSubmission) (admin.MutationResult, []string, error) {
			attempts.Add(1)
			input, err := formmodel.PrepareInput(objects, bound)
			if err != nil {
				return admin.MutationResult{}, nil, err
			}
			result, err := owner.Update(ctx, actor, mutation.ID, mutation.Revision, input)
			if err != nil {
				return admin.MutationResult{}, nil, err
			}
			confirmed = result
			committed.Store(true)
			return admin.MutationResult{ID: result.ID, Revision: result.Revision}, result.ChangedFields, nil
		},
	}))
	registry, err := builder.Build()
	check(err)
	paths, err := admin.SiteAllowedNextPaths(registry, "/admin")
	check(err)
	manager, err := sessions.NewManager(selected, sessions.Config{})
	check(err)
	authenticator, err := selected.Authenticator(t.Context(), hasher)
	check(err)
	loginOwner, err := selected.LoginPersistence(manager)
	check(err)
	runtime, err := websession.New(websession.Config{Sessions: manager, Authenticator: authenticator, Authorizer: auth.PrincipalAuthorizer{}, LoginPersistence: loginOwner, SessionCookie: websession.CookieConfig{Path: "/", AllowInsecure: true}, CSRFCookie: websession.CookieConfig{Path: "/", AllowInsecure: true}, LoginPath: "/admin/login/", FallbackPath: "/admin/", AllowedNextPaths: paths})
	check(err)
	site, err := admin.NewSite(admin.SiteConfig{Apps: configured.Apps(), Namespace: "accountadmin", Registry: registry, Auth: runtime})
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
		if method == http.MethodPost {
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
			t.Fatal("Admin CSRF missing")
		}
		return html.UnescapeString(match[1])
	}
	login := func(client *http.Client) sessions.ID {
		t.Helper()
		status, _, page := send(client, http.MethodGet, "/admin/login/", nil)
		if status != http.StatusOK {
			t.Fatal("login form", status)
		}
		status, _, _ = send(client, http.MethodPost, "/admin/login/", url.Values{"username": {loginName}, "password": {accountPasswordText}, "csrfmiddlewaretoken": {csrf(page)}, "next": {"/admin/accounts/"}})
		if status != http.StatusFound {
			t.Fatal("selected login failed", status)
		}
		for _, cookie := range client.Jar.Cookies(baseURL) {
			if cookie.Name == websession.DefaultSessionCookieName {
				id, err := sessions.ParseID(cookie.Value)
				check(err)
				return id
			}
		}
		t.Fatal("selected login did not set its session")
		return sessions.ID{}
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
	client, sibling := newClient(), newClient()
	bearers := []sessions.ID{login(client), login(sibling)}
	before := snapshot()
	if len(before.rows) != 1 || len(before.sessions) < 2 {
		t.Fatal("self-deactivation fixture lacks row or live sessions")
	}
	beforeValues, err := objects.ModelValues(before.rows[0])
	check(err)
	revision, _ := beforeValues[role.Revision].Integer()
	path := "/admin/accounts/change/?id=" + strconv.FormatInt(id, 10)
	for _, mode := range []string{"csrf", "stale", "protected_input", "cross_field"} {
		t.Run(mode, func(t *testing.T) {
			status, _, page := send(client, http.MethodGet, path, nil)
			if status != http.StatusOK || strings.Contains(page, `name="`+role.Password+`"`) || strings.Contains(page, `name="`+role.PrincipalID+`"`) {
				t.Fatal("selected mutation form missing or exposes credentials", status)
			}
			data := url.Values{profileField: {"KR"}, "expected_revision": {strconv.FormatInt(revision, 10)}, "csrfmiddlewaretoken": {csrf(page)}}
			want := http.StatusOK
			previousAttempts := attempts.Load()
			switch mode {
			case "csrf":
				data.Del("csrfmiddlewaretoken")
				want = http.StatusForbidden
			case "stale":
				data.Set("expected_revision", strconv.FormatInt(revision+1, 10))
				want = http.StatusConflict
			case "protected_input":
				data.Set(role.Password, "forged")
				want = http.StatusBadRequest
			case "cross_field":
				data.Set(profileField, "NZ")
				previousAttempts++
			}
			status, headers, response := send(client, http.MethodPost, path, data)
			if status != want || headers.Get("Location") != "" || committed.Load() || attempts.Load() != previousAttempts || !reflect.DeepEqual(snapshot(), before) {
				t.Fatal("rejected mutation changed durable state", mode, status, want, attempts.Load(), previousAttempts)
			}
			if mode == "cross_field" && !strings.Contains(response, `data-error-code="profile_required"`) {
				t.Fatal("typed account policy did not reach the form")
			}
		})
	}
	t.Run("self_deactivation", func(t *testing.T) {
		status, _, page := send(client, http.MethodGet, path, nil)
		if status != http.StatusOK {
			t.Fatal("final change form", status)
		}
		previousAttempts := attempts.Load()
		status, headers, _ := send(client, http.MethodPost, path, url.Values{profileField: {"KR"}, "expected_revision": {strconv.FormatInt(revision, 10)}, "csrfmiddlewaretoken": {csrf(page)}})
		if status != http.StatusFound || !strings.HasPrefix(headers.Get("Location"), "/admin/accounts/?") || !committed.Load() || attempts.Load() != previousAttempts+1 || lateReads.Load() != 0 {
			t.Fatal("confirmed self-deactivation was reported as a failed read", status, attempts.Load(), lateReads.Load())
		}
		after := snapshot()
		if len(after.rows) != 1 || len(after.sessions) != 0 || len(after.audits) != len(before.audits)+1 || !reflect.DeepEqual(after.audits[:len(before.audits)], before.audits) {
			t.Fatal("deactivation did not atomically preserve rows/history and revoke sessions")
		}
		afterValues, err := objects.ModelValues(after.rows[0])
		check(err)
		actualRevision, _ := afterValues[role.Revision].Integer()
		active, _ := afterValues[role.Active.Field].Boolean()
		profile, _ := afterValues[profileField].String()
		if active || profile != "KR" || actualRevision != revision+1 || confirmed.ID != id || confirmed.Revision != actualRevision || confirmed.RevokedSessions != len(before.sessions) {
			t.Fatal("account receipt does not match the committed row and session revocation")
		}
		expectedFields := slices.Clone(fields)
		slices.Sort(expectedFields)
		actualFields := slices.Clone(confirmed.ChangedFields)
		slices.Sort(actualFields)
		if !slices.Equal(actualFields, expectedFields) {
			t.Fatal("combined profile/role change lost a field", actualFields)
		}
		event := after.audits[len(after.audits)-1]
		domain, bound := actor.CredentialDomain()
		encodedFields, err := json.Marshal(confirmed.ChangedFields)
		check(err)
		if !bound || event.Domain != domain.Identifier() || event.ActorID != principalID || event.Model != role.Model.AppLabel+"."+role.Model.ModelName || event.ObjectID != id || event.Action != string(admin.ActionChange) || event.ChangedFields != string(encodedFields) || event.RecordedAt.IsZero() {
			t.Fatal("committed audit differs from the account receipt or actor/domain")
		}
		preserved, err := objects.ApplyValues(after.rows[0], map[string]query.Value{profileField: beforeValues[profileField], role.Active.Field: beforeValues[role.Active.Field], role.Revision: beforeValues[role.Revision]})
		check(err)
		if !reflect.DeepEqual(preserved, before.rows[0]) {
			t.Fatal("self-deactivation overwrote credential, identity or server values")
		}
		if _, err := reader.Get(t.Context(), actor, id); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodePermission}) {
			t.Fatal("old actor unexpectedly retained read authority", err)
		}
		for index, bearer := range bearers {
			if _, present, err := selected.Load(t.Context(), bearer); present || err != nil {
				t.Fatal("browser session survived deactivation", err)
			}
			browser := client
			if index == 1 {
				browser = sibling
			}
			status, headers, _ := send(browser, http.MethodGet, path, nil)
			if status != http.StatusFound || !strings.HasPrefix(headers.Get("Location"), "/admin/login/") {
				t.Fatal("next browser request retained old authority", status)
			}
		}
		if lateReads.Load() != 0 || attempts.Load() != previousAttempts+1 || !reflect.DeepEqual(snapshot(), after) {
			t.Fatal("following a committed mutation performed a read or retried the write")
		}
	})
}
