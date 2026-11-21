package consumertest_test

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"slices"
	"testing"
	"time"

	accountaudit "github.com/progresshans/godj/account/audit"
	apisessionauth "github.com/progresshans/godj/api/sessionauth"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/helpdesk"
	helpdeskaccounts "github.com/progresshans/godj/examples/helpdesk/accounts"
	helpdeskproject "github.com/progresshans/godj/examples/helpdesk/project"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/web"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

type helpdeskAccountFixture struct {
	runtime        *helpdeskaccounts.Runtime
	authentication *apisessionauth.Runtime
	actor, viewer  auth.Principal
	actorSession   sessions.Record
	viewerSession  sessions.Record
	verify         func(*testing.T)
}

type helpdeskVerificationBackend interface {
	db.Queryer
	db.SnapshotReader
}

func helpdeskDomainHistory(t *testing.T, backend db.SnapshotReader, actor auth.Principal, model string, id int64, limit int) ([]accountaudit.Entry, error) {
	t.Helper()
	namespace, bound := actor.CredentialDomain()
	if !bound {
		t.Fatal("Helpdesk audit verification requires the actual selected actor")
	}
	entries, err := selectedAuditHistory(t.Context(), backend, namespace, model, id, limit)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.Domain() != namespace || entry.ActorID() != actor.ID() || entry.Model() != model || entry.ObjectID() != id {
			t.Fatal("Helpdesk audit changed actor, domain or target")
		}
	}
	return entries, nil
}

func newHelpdeskAccountFixture(t *testing.T, backend *sqlite.Backend) helpdeskAccountFixture {
	t.Helper()
	ctx := t.Context()
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10000})
	if err != nil {
		t.Fatal(err)
	}
	firstConfig, err := helpdeskaccounts.FirstAccountConfig()
	if err != nil {
		t.Fatal(err)
	}
	first, err := helpdeskproject.NewFirstAccount(backend, hasher, firstConfig)
	if err != nil {
		t.Fatal(err)
	}
	input, err := first.Manager().Input(map[string]query.Value{"username": query.String("helpdesk-bootstrap"), "email": query.String("")})
	if err != nil {
		t.Fatal(err)
	}
	const password = "Quartz-river-kestrel-57!"
	preparedFirst, err := first.Prepare(ctx, input, password)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := first.Commit(ctx, preparedFirst)
	if err != nil {
		t.Fatal(err)
	}
	config, err := helpdeskaccounts.Policy()
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := helpdeskproject.NewAccountRuntime(ctx, backend, hasher, auth.PrincipalAuthorizer{}, config)
	if err != nil {
		t.Fatal(err)
	}
	startup, err := runtime.InspectStartup(ctx)
	if err != nil || !startup.Ready() || startup.Ownership().OperationID() != initial.Ownership.OperationID() || startup.SessionRecords() != 0 || startup.AuditEvents() != 1 {
		t.Fatal("Helpdesk selected-account startup", err)
	}
	bootstrap, err := runtime.Authenticator().Authenticate(ctx, "helpdesk-bootstrap", password)
	if err != nil {
		t.Fatal(err)
	}
	// Neither API account has ViewCategory or staff/superuser authority. Ticket
	// responses retain their independently specified category-summary policy.
	permissions := []auth.Permission{helpdesk.ViewTicket, helpdesk.AddTicket, helpdesk.ChangeTicket, helpdesk.DeleteTicket,
		helpdesk.ViewServiceReport, helpdesk.AddServiceReport, helpdesk.ChangeServiceReport, helpdesk.DeleteServiceReport,
		helpdesk.ViewLabel, helpdesk.AddLabel, helpdesk.ChangeLabel, helpdesk.DeleteLabel,
		helpdesk.ViewTicketLabel, helpdesk.AddTicketLabel, helpdesk.ChangeTicketLabel, helpdesk.DeleteTicketLabel}
	keys := make(map[auth.Permission]int64)
	catalog := runtime.Catalog().Permissions()
	for _, permission := range permissions {
		input, err := catalog.Manager().Input(map[string]query.Value{"code": query.String(string(permission)), "name": query.String(string(permission))})
		if err != nil {
			t.Fatal(err)
		}
		created, err := catalog.Create(ctx, bootstrap.Principal(), input)
		if err != nil {
			t.Fatal(err)
		}
		keys[permission] = created.ID
	}
	creator := runtime.Accounts().Creator()
	create := func(name string, grants []auth.Permission) (auth.Principal, int64) {
		t.Helper()
		input, err := creator.Manager().Input(map[string]query.Value{
			"username": query.String(name), "email": query.String(""),
			"active": query.Boolean(true), "staff": query.Boolean(false), "superuser": query.Boolean(false),
		})
		if err != nil {
			t.Fatal(err)
		}
		selected := make([]int64, len(grants))
		for index, permission := range grants {
			selected[index] = keys[permission]
		}
		input, err = input.WithCollections(map[string][]int64{"permissions": selected})
		if err != nil {
			t.Fatal(err)
		}
		prepared, err := creator.Prepare(ctx, bootstrap.Principal(), input, password)
		if err != nil {
			t.Fatal(err)
		}
		created, err := creator.Commit(ctx, bootstrap.Principal(), prepared)
		if err != nil {
			t.Fatal(err)
		}
		credential, err := runtime.Authenticator().Authenticate(ctx, name, password)
		if err != nil {
			t.Fatal(err)
		}
		actor := credential.Principal()
		actual, expected := actor.Permissions(), slices.Clone(grants)
		slices.Sort(actual)
		slices.Sort(expected)
		if _, bound := actor.CredentialDomain(); !bound || actor.Staff() || actor.Superuser() || actor.Has(helpdesk.ViewCategory) || !slices.Equal(actual, expected) {
			t.Fatal("Helpdesk API account gained implicit authority or lost its domain")
		}
		return actor, created.ID
	}
	actor, actorID := create("helpdesk-client", permissions)
	viewer, viewerID := create("helpdesk-view", []auth.Permission{helpdesk.ViewTicket, helpdesk.ViewServiceReport, helpdesk.ViewLabel, helpdesk.ViewTicketLabel})
	webAuth, err := websessionauth.New(websessionauth.Config{
		Sessions: runtime.Sessions(), Authenticator: runtime.Authenticator(), Authorizer: auth.PrincipalAuthorizer{},
		LoginPersistence: runtime.LoginPersistence(),
		SessionCookie:    websessionauth.CookieConfig{Path: "/", AllowInsecure: true}, CSRFCookie: websessionauth.CookieConfig{Path: "/", AllowInsecure: true},
		FallbackPath: "/api/tickets/", AllowedNextPaths: []string{"/api/tickets/"},
	})
	if err != nil {
		t.Fatal(err)
	}
	loginURL := serveConsumerAPI(t, "helpdesk_login", helpdesk.InstalledApps(), []web.Route{
		{Name: "helpdesk:consumer-csrf", Method: "GET", Path: "/login/", Handler: func(request *web.Request) (web.Response, error) {
			token, err := webAuth.CSRFToken(request)
			if err != nil {
				return web.Response{}, err
			}
			response, err := web.NewResponse(http.StatusOK, nil, []byte(token.Value()))
			if err != nil {
				return web.Response{}, err
			}
			return token.Apply(response)
		}},
		{Name: "helpdesk:consumer-login", Method: "POST", Path: "/login/", Handler: func(request *web.Request) (web.Response, error) {
			if err := webAuth.VerifyCSRF(request, nil); err != nil {
				return web.NewResponse(http.StatusForbidden, nil, nil)
			}
			result, err := webAuth.Login(request, request.HTTP().Header.Get("X-Test-Username"), password)
			if err != nil {
				return web.Response{}, err
			}
			response, err := web.NewResponse(http.StatusOK, nil, nil)
			if err != nil {
				return web.Response{}, err
			}
			return result.Apply(response)
		}},
	}, nil)
	login := func(username string) sessions.Record {
		t.Helper()
		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatal(err)
		}
		client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
		request, err := http.NewRequestWithContext(ctx, "GET", loginURL+"/login/", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		token, err := io.ReadAll(io.LimitReader(response.Body, 1024))
		closed := response.Body.Close()
		if err != nil || closed != nil || response.StatusCode != http.StatusOK {
			t.Fatal("Helpdesk native CSRF setup failed")
		}
		request, err = http.NewRequestWithContext(ctx, "POST", loginURL+"/login/", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("X-Test-Username", username)
		request.Header.Set(websessionauth.DefaultCSRFHeader, string(token))
		response, err = client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_, readErr := io.Copy(io.Discard, response.Body)
		closed = response.Body.Close()
		if readErr != nil || closed != nil || response.StatusCode != http.StatusOK {
			t.Fatal("Helpdesk native login failed")
		}
		endpoint, _ := url.Parse(loginURL)
		for _, cookie := range jar.Cookies(endpoint) {
			if cookie.Name != websessionauth.DefaultSessionCookieName {
				continue
			}
			id, err := sessions.ParseID(cookie.Value)
			if err != nil {
				t.Fatal(err)
			}
			record, found, err := runtime.SessionStore().Load(ctx, id)
			if err != nil || !found {
				t.Fatal("Helpdesk native login did not persist its session", err)
			}
			return record
		}
		t.Fatal("Helpdesk native login omitted its cookie")
		return sessions.Record{}
	}
	actorSession, viewerSession := login("helpdesk-client"), login("helpdesk-view")
	profile, err := apisessionauth.New(webAuth)
	if err != nil {
		t.Fatal(err)
	}
	return helpdeskAccountFixture{runtime: runtime, authentication: profile, actor: actor, viewer: viewer, actorSession: actorSession, viewerSession: viewerSession, verify: func(t *testing.T) {
		t.Helper()
		current, err := runtime.InspectStartup(t.Context())
		if err != nil || !current.Ready() || current.Ownership().OperationID() != initial.Ownership.OperationID() {
			t.Fatal("Helpdesk business calls changed selected ownership", err)
		}
		if total, err := models.UserObjects.Using(backend).Count(t.Context()); err != nil || total != 3 {
			t.Fatal("Helpdesk fixture account inventory differs", err)
		}
		for _, expected := range []struct {
			id      int64
			actor   auth.Principal
			session sessions.Record
		}{{actorID, actor, actorSession}, {viewerID, viewer, viewerSession}} {
			row, err := models.UserObjects.Using(backend).Filter(models.UserFields.ID.Exact(expected.id)).Get(t.Context())
			if err != nil || row.PrincipalID != expected.actor.ID() || !row.Active || row.Staff || row.Superuser || row.Revision != 1 || row.LastLogin == nil {
				t.Fatal("Helpdesk native login or account state differs", err)
			}
			resolved, err := runtime.Authenticator().Resolve(t.Context(), row.PrincipalID)
			if err != nil || !slices.Equal(resolved.Principal().Permissions(), expected.actor.Permissions()) {
				t.Fatal("Helpdesk business calls changed account grants", err)
			}
			if _, found, err := runtime.SessionStore().Load(t.Context(), expected.session.ID()); err != nil || !found {
				t.Fatal("Helpdesk business calls lost the native session", err)
			}
		}
	}}
}
