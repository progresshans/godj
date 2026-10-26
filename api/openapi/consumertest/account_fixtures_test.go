package consumertest_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	accountaudit "github.com/progresshans/godj/account/audit"
	accountdomain "github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	fixtureaccounts "github.com/progresshans/godj/conformance/identityfixture/accounts"
	hostproject "github.com/progresshans/godj/conformance/identityfixture/project"
	"github.com/progresshans/godj/examples/article/apiapp"
	"github.com/progresshans/godj/identity"
	identityaccount "github.com/progresshans/godj/identity/account"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/mail"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

type accountServerInput struct {
	URL           string `json:"url"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	NewPassword   string `json:"new_password"`
	Email         string `json:"email"`
	ResetPassword string `json:"reset_password"`
	MailProof     string `json:"mail_proof"`
}

func newAccountConsumerFixture(t *testing.T) (accountServerInput, []byte, func(*testing.T)) {
	t.Helper()
	ctx := t.Context()
	input := accountServerInput{Email: "ordinary@example.test", ResetPassword: " reset raw " + consumerToken(t) + " ", MailProof: consumerToken(t), Username: "ordinary", Password: " old raw " + consumerToken(t) + " ", NewPassword: " new raw " + consumerToken(t) + " "}
	backend := newConsumerBackend(t, "account-client", identityConsumerSources(t)...)
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10000})
	if err != nil {
		t.Fatal(err)
	}
	firstConfig, err := fixtureaccounts.FirstAccountConfig()
	if err != nil {
		t.Fatal(err)
	}
	first, err := hostproject.NewFirstAccount(backend, hasher, firstConfig)
	if err != nil {
		t.Fatal(err)
	}
	seed, err := first.Manager().Input(map[string]query.Value{"username": query.String("reset-fixture-owner"), "email": query.String("")})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := first.Prepare(ctx, seed, "Quartz-Meadow-971!independent")
	if err != nil {
		t.Fatal(err)
	}
	initial, err := first.Commit(ctx, pending)
	if err != nil {
		t.Fatal(err)
	}
	loginAt := time.Date(2026, 9, 27, 1, 2, 3, 123456000, time.UTC)
	config, err := fixtureaccounts.Policy()
	if err != nil {
		t.Fatal(err)
	}
	config.Sessions.Clock = func() time.Time { return loginAt }
	runtime, err := hostproject.NewAccountRuntime(ctx, backend, hasher, auth.PrincipalAuthorizer{}, config)
	if err != nil {
		t.Fatal(err)
	}
	startup, err := runtime.InspectStartup(ctx)
	if err != nil || !startup.Ready() || startup.Ownership().OperationID() != initial.Ownership.OperationID() || startup.AuditEvents() != 1 || startup.SessionRecords() != 0 {
		t.Fatal("selected reset initial ownership", err)
	}
	bootstrap, err := runtime.Authenticator().Authenticate(ctx, "reset-fixture-owner", "Quartz-Meadow-971!independent")
	if err != nil {
		t.Fatal(err)
	}
	creator := runtime.Accounts().Creator()
	candidate, err := creator.Manager().Input(map[string]query.Value{"username": query.String(input.Username), "email": query.String(input.Email)})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := creator.Prepare(ctx, bootstrap.Principal(), candidate, input.Password)
	if err != nil {
		t.Fatal(err)
	}
	created, err := creator.Commit(ctx, bootstrap.Principal(), prepared)
	if err != nil {
		t.Fatal(err)
	}
	row, found, err := models.UserObjects.Using(backend).Filter(models.UserFields.ID.Exact(created.ID)).OrderBy(models.UserFields.ID.Asc()).First(ctx)
	if err != nil || !found || row.Username != input.Username || row.Email != input.Email {
		t.Fatal("selected reset fixture account absent", err)
	}
	ordinary, err := runtime.Authenticator().Authenticate(ctx, input.Username, input.Password)
	if err != nil {
		t.Fatal(err)
	}
	principal := ordinary.Principal()
	namespace, bound := principal.CredentialDomain()
	if !bound {
		t.Fatal("selected reset account lacks credential domain")
	}
	manager := runtime.Sessions()
	credential, err := runtime.Authenticator().Resolve(ctx, principal.ID())
	if err != nil {
		t.Fatal(err)
	}
	oldSession, err := manager.Create(ctx, map[string]string{auth.SessionPrincipalIDKey: principal.ID(), auth.SessionCredentialStampKey: credential.SessionStamp()})
	if err != nil {
		t.Fatal(err)
	}
	unrelated, err := manager.Create(ctx, map[string]string{"unrelated": "preserve"})
	if err != nil {
		t.Fatal(err)
	}
	keys, err := auth.NewPasswordResetKeyRing(bytes.Repeat([]byte{37}, 32))
	if err != nil {
		t.Fatal(err)
	}
	reset, err := runtime.PasswordResetter(accountmodel.RuntimeResetConfig{Keys: keys, Clock: func() time.Time { return loginAt }})
	if err != nil {
		t.Fatal(err)
	}
	memory, err := mail.NewMemory(mail.MemoryConfig{})
	if err != nil {
		t.Fatal(err)
	}
	from, err := mail.ParseAddress("sender@example.test")
	if err != nil {
		t.Fatal(err)
	}
	mailer, err := identity.NewPasswordResetMailer(reset, memory, identity.PasswordResetMailConfig{From: from, Origin: "https://reset.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	var reports atomic.Int64
	next, err := identityaccount.AllowedNextPaths("")
	if err != nil {
		t.Fatal(err)
	}
	webAuth, err := sessionauth.New(sessionauth.Config{Sessions: manager, Authenticator: runtime.Authenticator(), Authorizer: auth.PrincipalAuthorizer{}, LoginPersistence: runtime.LoginPersistence(), PasswordChangePersistence: runtime.PasswordChangePersistence(), PasswordResetPersistence: reset, Clock: func() time.Time { return loginAt }, SessionCookie: sessionauth.CookieConfig{AllowInsecure: true}, CSRFCookie: sessionauth.CookieConfig{AllowInsecure: true}, LoginPath: "/account/login/", FallbackPath: next[0], AllowedNextPaths: next})
	if err != nil {
		t.Fatal(err)
	}
	installed := []apps.Config{{Name: "github.com/progresshans/godj/examples/article/articleapp", Label: apiapp.Namespace}}
	configured, err := settings.New(settings.Definition{ProjectName: "account_client", InstalledApps: installed})
	if err != nil {
		t.Fatal(err)
	}
	account, err := identityaccount.New(identityaccount.Config{Apps: configured.Apps(), Namespace: apiapp.Namespace, Auth: webAuth, PasswordReset: &identityaccount.PasswordResetConfig{Mailer: mailer, ReportError: func(context.Context, error) { reports.Add(1) }}})
	if err != nil {
		t.Fatal(err)
	}
	document, err := account.OpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	// Fixture-only mailbox transport: the unrelated client reads the real
	// Memory sender output after invoking the product email request operation.
	// It is absent from the product's routes and OpenAPI and requires a private
	// capability passed to this one child over stdin.
	routes := append(account.Routes(), web.Route{Name: apiapp.Namespace + ":fixture-reset-mail", Method: "GET", Path: "/__test/reset-mail/", Handler: func(request *web.Request) (web.Response, error) {
		if request.HTTP().Header.Get("X-Fixture-Proof") != input.MailProof {
			return web.NewResponse(403, nil, nil)
		}
		messages, err := memory.Snapshot()
		if err != nil || len(messages) != 1 {
			return web.Response{}, errors.New("fixture mailbox not ready")
		}
		for _, line := range strings.Split(messages[0].Message().Text(), "\n") {
			if strings.HasPrefix(line, "https://reset.example.test/account/reset/") {
				return api.JSON(200, serializers.String(line))
			}
		}
		return web.Response{}, errors.New("fixture reset link missing")
	}})
	input.URL = serveConsumerAPI(t, "account_client", installed, routes, account.Middleware())
	verify := func(t *testing.T) {
		if reports.Load() != 0 {
			t.Fatal("reset reported unexpected failure")
		}
		messages, err := memory.Snapshot()
		if err != nil || len(messages) != 1 || messages[0].Message().Recipients()[0].Mailbox() != input.Email {
			t.Fatal("reset mailbox recipient/duplicate delivery")
		}
		reopened, err := hostproject.NewAccountRuntime(ctx, backend, hasher, auth.PrincipalAuthorizer{}, config)
		if err != nil {
			t.Fatal(err)
		}
		currentOwnership, err := accountdomain.Read(ctx, backend, initial.Ownership.RoleFingerprint())
		if err != nil || currentOwnership.Sequence() != initial.Ownership.Sequence() || currentOwnership.OperationID() != initial.Ownership.OperationID() || currentOwnership.Generation() != initial.Ownership.Generation() || currentOwnership.EvidenceSHA256() != initial.Ownership.EvidenceSHA256() || !currentOwnership.At().Equal(initial.Ownership.At()) {
			t.Fatal("selected reset ownership changed", err)
		}
		current, err := reopened.Authenticator().Authenticate(ctx, input.Username, input.ResetPassword)
		if err != nil || current.Principal().Staff() || current.Principal().Superuser() || len(current.Principal().Permissions()) != 0 {
			t.Fatal("generated account changed identity or failed to commit", err)
		}
		if _, err := reopened.Authenticator().Authenticate(ctx, input.Username, input.Password); err != auth.ErrInvalidCredentials {
			t.Fatal("generated account retained old password")
		}
		row, found, err := models.UserObjects.Using(backend).Filter(models.UserFields.PrincipalID.Exact(principal.ID())).OrderBy(models.UserFields.ID.Asc()).First(ctx)
		if err != nil || !found || row.Revision != 4 || row.LastLogin == nil || !row.LastLogin.Equal(loginAt) {
			t.Fatal("generated account revision/last_login drift", err)
		}
		if _, found, err := reopened.SessionStore().Load(ctx, oldSession.ID()); err != nil || found {
			t.Fatal("generated account did not revoke unused session", err)
		}
		retained, found, err := reopened.SessionStore().Load(ctx, unrelated.ID())
		if err != nil || !found || !reflect.DeepEqual(retained, unrelated) {
			t.Fatal("generated account changed unrelated session", err)
		}
		history, err := accountaudit.HistoryIn(ctx, backend, namespace, "godj_identity.user", row.ID, 10)
		if err != nil || len(history) != 4 {
			t.Fatal("generated account audit count", err)
		}
		if history[0].Action() != admin.ActionAdd || history[0].ActorID() != bootstrap.Principal().ID() {
			t.Fatal("selected reset fixture creation audit")
		}
		for _, event := range history[1:] {
			if event.ActorID() != principal.ID() || event.Action() != admin.ActionChange || !reflect.DeepEqual(event.ChangedFields(), []string{"password"}) {
				t.Fatal("generated account audit disclosure")
			}
		}
	}
	return input, document.Bytes(), verify
}
