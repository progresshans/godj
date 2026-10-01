package consumertest_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/bearerauth"
	apisession "github.com/progresshans/godj/api/sessionauth"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	hostdef "github.com/progresshans/godj/conformance/identityfixture/modeldef"
	hostmodels "github.com/progresshans/godj/conformance/identityfixture/models"
	hostproject "github.com/progresshans/godj/conformance/identityfixture/project"
	"github.com/progresshans/godj/identity"
	identityapi "github.com/progresshans/godj/identity/api"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/web"
	websession "github.com/progresshans/godj/web/sessionauth"
)

type identityServerInput struct {
	serverInput
	ActorID               int64 `json:"actor_id"`
	TargetID              int64 `json:"target_id"`
	ProtectedUserID       int64 `json:"protected_user_id"`
	ProtectedGroupID      int64 `json:"protected_group_id"`
	ProtectedPermissionID int64 `json:"protected_permission_id"`
	CascadeUserID         int64 `json:"cascade_user_id"`
	CascadeGroupID        int64 `json:"cascade_group_id"`
	CascadePermissionID   int64 `json:"cascade_permission_id"`
}

func identityConsumerSources(t *testing.T) []definition.Source {
	t.Helper()
	schema, err := hostdef.Schema()
	if err != nil {
		t.Fatal(err)
	}
	operations := make([]migrations.Operation, len(schema.Models))
	for index, model := range schema.Models {
		operations[index] = migrations.CreateModel{AppLabel: schema.AppLabel, Model: model}
	}
	document, err := definition.Encode(definition.Producer{Name: "independent-identity-consumer", Version: "1"}, migrations.Migration{
		App: schema.AppLabel, Name: "0001_initial", Dependencies: []migrations.MigrationKey{identity.InitialMigrationKey()}, Operations: operations,
	})
	if err != nil {
		t.Fatal(err)
	}
	return append(systemstate.IdentityMigrationSources(), definition.Source{SourceID: "identity-consumer/0001_initial", Document: document})
}

// Both profiles operate on one durable store. The Session profile resolves live
// credentials; the Bearer verifier deliberately returns its original snapshot.
// The final Bearer request must therefore still be denied after Session disables
// the actor. The session clients start from native HTTP login; token issuance
// remains the explicit static test verifier above.
func newIdentityConsumerFixtures(t *testing.T) (identityServerInput, identityServerInput, map[string][]byte, func(*testing.T)) {
	t.Helper()
	ctx := t.Context()
	backend := newConsumerBackend(t, "identity-generated-client", identityConsumerSources(t)...)
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10000})
	if err != nil {
		t.Fatal(err)
	}
	actor, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "identity-consumer-manager", Active: true, Staff: true, Superuser: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := systemstate.ProvisionIdentity(ctx, backend, systemstate.ProvisionIdentityConfig{Principal: actor, Username: "sdk-manager", Password: "original SDK password", PasswordHasher: hasher}); err != nil {
		t.Fatal(err)
	}
	runtime, err := systemstate.OpenIdentity(ctx, backend, systemstate.IdentityRuntimeConfig{PasswordHasher: hasher, MaxSessions: 32})
	if err != nil {
		t.Fatal(err)
	}
	root, found, err := models.UserObjects.Using(backend).Filter(models.UserFields.PrincipalID.Exact(actor.ID())).OrderBy(models.UserFields.ID.Asc()).First(ctx)
	if err != nil || !found {
		t.Fatal("identity consumer actor missing")
	}
	encoded, err := hasher.Hash(ctx, "original SDK password")
	if err != nil {
		t.Fatal(err)
	}
	seedUser := func(name string) models.User {
		input := models.NewUserCreate("sdk-"+name, name, encoded, time.Now().UTC()).WithFirstName("Original")
		if name == "managed-target" {
			input = input.WithEmail("  legacy-address  ")
		}
		value, err := models.UserObjects.Create(ctx, backend, input)
		if err != nil {
			t.Fatal("seed identity consumer user:", err)
		}
		return value
	}
	target, protectedUser, cascadeUser := seedUser("managed-target"), seedUser("protected-user"), seedUser("cascade-user")
	var viewPermissions []models.Permission
	var viewLinks []models.UserPermissionsLink
	for _, code := range []auth.Permission{identity.ViewUser, identity.ViewGroup, identity.ViewPermission} {
		permission, err := models.PermissionObjects.Create(ctx, backend, models.NewPermissionCreate(string(code), "Read identity"))
		if err != nil {
			t.Fatal(err)
		}
		link, err := models.UserPermissionsLinkObjects.Create(ctx, backend, models.NewUserPermissionsLinkCreate(target.ID, permission.ID))
		if err != nil {
			t.Fatal(err)
		}
		viewLinks = append(viewLinks, link)
		viewPermissions = append(viewPermissions, permission)
	}
	seedPermission := func(code string) models.Permission {
		value, err := models.PermissionObjects.Create(ctx, backend, models.NewPermissionCreate(code, "Host permission"))
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	seedGroup := func(name string) models.Group {
		value, err := models.GroupObjects.Create(ctx, backend, models.NewGroupCreate(name))
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	protectedPermission, cascadePermission := seedPermission("consumer.host.protected"), seedPermission("consumer.host.cascade")
	protectedGroup, cascadeGroup := seedGroup("Host protected"), seedGroup("Host set null")
	guard, err := hostmodels.GuardObjects.Create(ctx, backend, hostmodels.NewGuardCreate(protectedUser.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hostmodels.NoteObjects.Create(ctx, backend, hostmodels.NewNoteCreate("Cascade with user", cascadeUser.ID)); err != nil {
		t.Fatal(err)
	}
	accessGuard, err := hostmodels.AccessGuardObjects.Create(ctx, backend, hostmodels.NewAccessGuardCreate().WithGroupID(protectedGroup.ID).WithPermissionID(protectedPermission.ID))
	if err != nil {
		t.Fatal(err)
	}
	retainedNote, err := hostmodels.AccessNoteObjects.Create(ctx, backend, hostmodels.NewAccessNoteCreate("Retained after group deletion", protectedPermission.ID).WithGroupID(cascadeGroup.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hostmodels.AccessNoteObjects.Create(ctx, backend, hostmodels.NewAccessNoteCreate("Cascade with permission", cascadePermission.ID)); err != nil {
		t.Fatal(err)
	}
	loginAt := time.Date(2026, 9, 27, 1, 2, 3, 123456000, time.UTC)
	manager, err := sessions.NewManager(runtime.SessionStore(), sessions.Config{Clock: func() time.Time { return loginAt }})
	if err != nil {
		t.Fatal(err)
	}
	seedSession := func(principalID string) sessions.Record {
		credential, err := runtime.Authenticator().Resolve(ctx, principalID)
		if err != nil {
			t.Fatal(err)
		}
		record, err := manager.Create(ctx, map[string]string{auth.SessionPrincipalIDKey: principalID, auth.SessionCredentialStampKey: credential.SessionStamp(), "unrelated": "preserve"})
		if err != nil {
			t.Fatal(err)
		}
		return record
	}
	// These sessions never travel through HTTP: lazy invalidation by a later
	// request must not substitute for transactional revocation of all sessions.
	actorUnusedSession, targetUnusedSession := seedSession(root.PrincipalID), seedSession(target.PrincipalID)
	unrelatedSession, err := manager.Create(ctx, map[string]string{"unrelated": "preserve"})
	if err != nil {
		t.Fatal(err)
	}
	loginPersistence, err := runtime.LoginPersistence(manager)
	if err != nil {
		t.Fatal(err)
	}
	webAuth, err := websession.New(websession.Config{
		LoginPersistence: loginPersistence, Clock: func() time.Time { return loginAt },
		Sessions: manager, Authenticator: runtime.Authenticator(), Authorizer: auth.PrincipalAuthorizer{},
		SessionCookie: websession.CookieConfig{Path: "/", AllowInsecure: true}, CSRFCookie: websession.CookieConfig{Path: "/", AllowInsecure: true},
		FallbackPath: identityapi.BasePath + "users/", AllowedNextPaths: []string{identityapi.BasePath + "users/"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Establish sessions through real HTTP so last_login is an observed login,
	// not fixture data written into the model or a manually minted bearer.
	loginURL := serveConsumerAPI(t, "identity_login", []apps.Config{{Name: "github.com/progresshans/godj/identity", Label: "godj_identity"}}, []web.Route{
		{Name: "godj_identity:csrf", Method: "GET", Path: "/login/", Handler: func(request *web.Request) (web.Response, error) {
			token, err := webAuth.CSRFToken(request)
			if err != nil {
				return web.Response{}, err
			}
			response, err := web.NewResponse(200, nil, []byte(token.Value()))
			if err != nil {
				return web.Response{}, err
			}
			return token.Apply(response)
		}},
		{Name: "godj_identity:login", Method: "POST", Path: "/login/", Handler: func(request *web.Request) (web.Response, error) {
			if err := webAuth.VerifyCSRF(request, nil); err != nil {
				return web.NewResponse(403, nil, nil)
			}
			result, err := webAuth.Login(request, request.HTTP().Header.Get("X-Test-Username"), "original SDK password")
			if err != nil {
				return web.Response{}, err
			}
			response, err := web.NewResponse(200, nil, nil)
			if err != nil {
				return web.Response{}, err
			}
			return result.Apply(response)
		}},
	}, nil)
	httpLogin := func(username string) sessions.Record {
		t.Helper()
		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatal(err)
		}
		client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
		response, err := client.Get(loginURL + "/login/")
		if err != nil {
			t.Fatal(err)
		}
		token, err := io.ReadAll(response.Body)
		closed := response.Body.Close()
		if err != nil || closed != nil || response.StatusCode != 200 {
			t.Fatal("consumer CSRF setup failed")
		}
		request, err := http.NewRequestWithContext(ctx, "POST", loginURL+"/login/", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("X-Test-Username", username)
		request.Header.Set(websession.DefaultCSRFHeader, string(token))
		response, err = client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_, readErr := io.Copy(io.Discard, response.Body)
		closed = response.Body.Close()
		if readErr != nil || closed != nil || response.StatusCode != 200 {
			t.Fatal("consumer native login failed")
		}
		endpoint, _ := url.Parse(loginURL)
		for _, cookie := range jar.Cookies(endpoint) {
			if cookie.Name == websession.DefaultSessionCookieName {
				id, err := sessions.ParseID(cookie.Value)
				if err != nil {
					t.Fatal(err)
				}
				record, found, err := runtime.SessionStore().Load(ctx, id)
				if err != nil || !found {
					t.Fatal("consumer native login missing session", err)
				}
				return record
			}
		}
		t.Fatal("consumer native login missing cookie")
		return sessions.Record{}
	}
	actorSession, targetSession := httpLogin(root.Username), httpLogin(target.Username)
	root.LastLogin, target.LastLogin = &loginAt, &loginAt
	sessionAuth, err := apisession.New(webAuth)
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := runtime.Authenticator().Resolve(ctx, target.PrincipalID)
	if err != nil {
		t.Fatal(err)
	}
	fullToken, viewToken := consumerToken(t), consumerToken(t)
	bearerAuth, err := bearerauth.New(bearerauth.Config{Verifier: consumerTokenVerifier{fullToken: actor, viewToken: viewer.Principal()}, Authorizer: auth.PrincipalAuthorizer{}})
	if err != nil {
		t.Fatal(err)
	}
	policies, err := hostproject.BindRelationDeleters()
	if err != nil {
		t.Fatal(err)
	}
	validators, err := identity.DefaultPasswordValidators()
	if err != nil {
		t.Fatal(err)
	}
	documents := make(map[string][]byte)
	serve := func(name string, authentication api.AlternativeAuthentication) string {
		application, err := identityapi.New(identityapi.Config{Namespace: "godj_identity", Backend: runtime, PasswordHasher: hasher, PasswordValidators: validators, Authorizer: auth.PrincipalAuthorizer{}, Authentication: authentication, Users: policies.AccountsUser, Groups: policies.AccountsGroup, Permissions: policies.AccountsPermission})
		if err != nil {
			t.Fatal(err)
		}
		document, err := application.OpenAPI()
		if err != nil {
			t.Fatal(err)
		}
		documents[name] = document.Bytes()
		return serveConsumerAPI(t, name, []apps.Config{{Name: "github.com/progresshans/godj/identity", Label: "godj_identity"}}, application.Routes(), application.Middleware())
	}
	common := identityServerInput{ActorID: root.ID, TargetID: target.ID, ProtectedUserID: protectedUser.ID, ProtectedGroupID: protectedGroup.ID, ProtectedPermissionID: protectedPermission.ID, CascadeUserID: cascadeUser.ID, CascadeGroupID: cascadeGroup.ID, CascadePermissionID: cascadePermission.ID}
	sessionInput, bearerInput := common, common
	sessionInput.serverInput = serverInput{URL: serve("identitysession", sessionAuth), Session: actorSession.ID().Encoded(), ReadOnlySession: targetSession.ID().Encoded()}
	bearerInput.serverInput = serverInput{URL: serve("identitybearer", bearerAuth), Token: fullToken, ReadOnlyToken: viewToken}
	verify := func(t *testing.T) {
		t.Helper()
		ctx := t.Context()
		// Construct a fresh runtime instead of trusting the client receipt or any
		// of the response DTOs. The parent never reads generated client types.
		reopened, err := systemstate.OpenIdentity(ctx, backend, systemstate.IdentityRuntimeConfig{PasswordHasher: hasher, MaxSessions: 32})
		if err != nil {
			t.Fatal(err)
		}
		users, err := models.UserObjects.Using(backend).OrderBy(models.UserFields.ID.Asc()).All(ctx)
		if err != nil || len(users) != 4 {
			t.Fatal("generated identity client user inventory differs")
		}
		createdUsername := "Fred" + strings.Repeat("\U000105c0", 146)
		var created models.User
		for _, user := range users {
			switch user.ID {
			case root.ID:
				want := root
				want.Active, want.Revision = false, 2
				if !reflect.DeepEqual(user, want) {
					t.Fatal("identity actor deactivation changed unrelated profile")
				}
			case target.ID:
				want := target
				want.EncodedPassword, want.Revision = user.EncodedPassword, 7
				if user.EncodedPassword == target.EncodedPassword || !reflect.DeepEqual(user, want) {
					t.Fatal("identity password lifecycle changed unrelated profile")
				}
			case protectedUser.ID:
				if !reflect.DeepEqual(user, protectedUser) {
					t.Fatal("protected identity user changed")
				}
			default:
				created = user
				if user.Username != createdUsername || user.FirstName != "" || user.LastName != "" || user.Email != "" || user.Staff || user.Superuser || !user.Active || user.Revision != 7 || user.LastLogin != nil || user.DateJoined.IsZero() {
					t.Fatal("created identity profile/default/relation revision differs")
				}
			}
		}
		if created.ID == 0 || created.PrincipalID == "" || created.PrincipalID == root.PrincipalID || created.PrincipalID == target.PrincipalID {
			t.Fatal("server identity was not independently assigned")
		}
		for _, probe := range []struct{ username, password string }{{"managed-target", "  SDK replacement password  "}} {
			if credential, err := reopened.Authenticator().Authenticate(ctx, probe.username, probe.password); err != nil || !credential.Principal().Authenticated() {
				t.Fatal("stored SDK password did not authenticate")
			}
			if _, err := reopened.Authenticator().Authenticate(ctx, probe.username, strings.TrimSpace(probe.password)); err == nil {
				t.Fatal("SDK password whitespace was lost")
			}
		}
		if current, err := reopened.Authenticator().Resolve(ctx, created.PrincipalID); err != nil || current.HasUsablePassword() || !current.Principal().Active() || !strings.HasPrefix(created.EncodedPassword, "!") {
			t.Fatal("SDK null password did not persist as an active unusable credential", err)
		}
		for _, password := range []string{"", "  SDK created password  ", created.EncodedPassword, "godj-unmatchable-dummy-password"} {
			if _, err := reopened.Authenticator().Authenticate(ctx, created.Username, password); !errors.Is(err, auth.ErrInvalidCredentials) {
				t.Fatal("SDK unusable credential authenticated or became a store failure", err)
			}
		}
		if _, err := reopened.Authenticator().Authenticate(ctx, target.Username, "original SDK password"); err == nil {
			t.Fatal("SDK password replacement revived old credential")
		}
		groups, err := models.GroupObjects.Using(backend).OrderBy(models.GroupFields.ID.Asc()).All(ctx)
		if err != nil || !reflect.DeepEqual(groups, []models.Group{protectedGroup}) {
			t.Fatal("identity generated client group inventory differs")
		}
		permissions, err := models.PermissionObjects.Using(backend).OrderBy(models.PermissionFields.ID.Asc()).All(ctx)
		if err != nil || !reflect.DeepEqual(permissions, append(slices.Clone(viewPermissions), protectedPermission)) {
			t.Fatal("identity generated client permission inventory differs")
		}
		if count, err := models.GroupPermissionsLinkObjects.Using(backend).Count(ctx); err != nil || count != 0 {
			t.Fatal("deleted group permissions survived")
		}
		if count, err := models.UserGroupsLinkObjects.Using(backend).Count(ctx); err != nil || count != 0 {
			t.Fatal("deleted user groups survived")
		}
		links, err := models.UserPermissionsLinkObjects.Using(backend).OrderBy(models.UserPermissionsLinkFields.ID.Asc()).All(ctx)
		if err != nil || !reflect.DeepEqual(links, viewLinks) {
			t.Fatal("identity direct permissions changed")
		}
		if guards, err := hostmodels.GuardObjects.Using(backend).OrderBy(hostmodels.GuardFields.ID.Asc()).All(ctx); err != nil || !reflect.DeepEqual(guards, []hostmodels.Guard{guard}) {
			t.Fatal("host protected user guard changed")
		}
		if guards, err := hostmodels.AccessGuardObjects.Using(backend).OrderBy(hostmodels.AccessGuardFields.ID.Asc()).All(ctx); err != nil || !reflect.DeepEqual(guards, []hostmodels.AccessGuard{accessGuard}) {
			t.Fatal("host protected group/permission guard changed")
		}
		if count, err := hostmodels.NoteObjects.Using(backend).Count(ctx); err != nil || count != 0 {
			t.Fatal("host user cascade was not applied")
		}
		retainedNote.GroupID = nil
		if notes, err := hostmodels.AccessNoteObjects.Using(backend).OrderBy(hostmodels.AccessNoteFields.ID.Asc()).All(ctx); err != nil || !reflect.DeepEqual(notes, []hostmodels.AccessNote{retainedNote}) {
			t.Fatal("host group SET_NULL/permission CASCADE differs")
		}
		for _, record := range []sessions.Record{actorSession, targetSession, actorUnusedSession, targetUnusedSession} {
			if _, found, err := reopened.SessionStore().Load(ctx, record.ID()); err != nil || found {
				t.Fatal("revoked durable identity session survived")
			}
		}
		if current, found, err := reopened.SessionStore().Load(ctx, unrelatedSession.ID()); err != nil || !found || !reflect.DeepEqual(current.Snapshot(), unrelatedSession.Snapshot()) {
			t.Fatal("unrelated durable session changed")
		}
		for _, expected := range []struct {
			model  string
			id     int64
			fields [][]string
		}{
			{"user", root.ID, [][]string{{"active"}}},
			{"user", target.ID, [][]string{{"password"}, {"password"}, {"password"}, {"password"}, {"active"}, {"active"}}},
			{"user", protectedUser.ID, nil}, {"group", protectedGroup.ID, nil}, {"permission", protectedPermission.ID, nil},
		} {
			history, err := reopened.AuditHistory(ctx, "godj_identity."+expected.model, expected.id, 100)
			if err != nil || len(history) != len(expected.fields) {
				t.Fatal("identity client audit count differs")
			}
			for index, event := range history {
				if event.ActorID != actor.ID() || event.Action != admin.ActionChange || event.DisplayLabel != "" || !slices.Equal(event.ChangedFields, expected.fields[index]) {
					t.Fatal("identity client audit changed or exposed values")
				}
			}
		}
		history, err := reopened.AuditHistory(ctx, "godj_identity.user", created.ID, 100)
		// The two catalog deletions advance the User revision, but their audit
		// events belong to the deleted catalog resources, not each affected user.
		if err != nil || len(history) != 5 {
			t.Fatalf("created identity audit/no-op inventory differs: got %d events, want 5", len(history))
		}
		for index, event := range history {
			action := admin.ActionChange
			if index == 0 {
				action = admin.ActionAdd
			}
			if event.ActorID != actor.ID() || event.DisplayLabel != "" || event.Action != action {
				t.Fatal("created identity audit exposed values")
			}
		}
		for _, deleted := range []struct {
			model string
			id    int64
		}{{"user", cascadeUser.ID}, {"group", cascadeGroup.ID}, {"permission", cascadePermission.ID}} {
			history, err := reopened.AuditHistory(ctx, "godj_identity."+deleted.model, deleted.id, 100)
			if err != nil || len(history) != 1 || history[0].Action != admin.ActionDelete || history[0].ActorID != actor.ID() || history[0].DisplayLabel != "" || len(history[0].ChangedFields) != 0 {
				t.Fatal("identity host deletion audit differs")
			}
		}
	}
	return sessionInput, bearerInput, documents, verify
}
