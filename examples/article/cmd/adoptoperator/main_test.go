package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/databaseconfig"
	"github.com/progresshans/godj/examples/article/internal/operatorconfig"
	"github.com/progresshans/godj/examples/article/internal/siteapp"
	"github.com/progresshans/godj/examples/article/project"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/sessions"
	sessiondb "github.com/progresshans/godj/sessions/database"
	"github.com/progresshans/godj/systemstate"
)

func TestAdoptOperatorRequiresExplicitRolesBeforeOpeningDatabase(t *testing.T) {
	for _, args := range [][]string{nil, {"--staff"}, {"--superuser"}, {"--inspect", "--staff=false"}, {"--staff=true", "--superuser=false", "extra"}} {
		var output bytes.Buffer
		code := run(t.Context(), args, func(string) (string, bool) { t.Fatal("invalid arguments read database selection"); return "", false }, &output)
		if code != 2 || output.String() != "{\"status\":\"failed\",\"code\":\"invalid_arguments\"}\n" {
			t.Fatal("invalid role selection accepted", args, code)
		}
	}
}

func TestAdoptOperatorPreservesLegacyIdentityUntilExplicitAccountHandoff(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "article.sqlite3")
	backend, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	document, err := os.ReadFile(filepath.Join("..", "..", "migrations", "0001_initial.godj.json"))
	if err != nil {
		t.Fatal(err)
	}
	article := definition.Source{SourceID: "article/0001", Document: document}
	migrate := func(sources []definition.Source) {
		t.Helper()
		loaded, _, err := definition.Load(sources...)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
			t.Fatal(err)
		}
	}
	migrate([]definition.Source{systemstate.InitialDefinitionSource(), article})
	expected, err := operatorconfig.RuntimeConfig()
	if err != nil {
		t.Fatal(err)
	}
	const password = "existing article private password"
	if err := systemstate.ProvisionOperator(ctx, backend, systemstate.ProvisionOperatorConfig{Username: "existing", Password: password, CredentialPolicy: expected.CredentialPolicy}); err != nil {
		t.Fatal(err)
	}
	old, err := systemstate.OpenExisting(ctx, backend, expected)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := old.Authenticator().Authenticate(ctx, "existing", password)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := sessions.NewManager(old.SessionStore(), sessions.Config{})
	if err != nil {
		t.Fatal(err)
	}
	session, err := manager.Create(ctx, map[string]string{"_godj_principal_id": credential.Principal().ID(), "_godj_credential_stamp": credential.SessionStamp()})
	if err != nil {
		t.Fatal(err)
	}
	migrate(append(systemstate.IdentityMigrationSources(), article))
	lookup := func(key string) (string, bool) { return path, key == databaseconfig.SQLiteDatabaseEnv }
	invoke := func(args ...string) (outcome, int) {
		t.Helper()
		var output bytes.Buffer
		code := run(context.Background(), args, lookup, &output)
		if bytes.Contains(output.Bytes(), []byte(password)) {
			t.Fatal("command emitted raw password")
		}
		var result outcome
		if err := json.Unmarshal(output.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result, code
	}
	adopted, code := invoke("--staff=true", "--superuser=false")
	if code != 0 || adopted.Status != "adopted" || adopted.Kind != "operator" || adopted.UserID <= 0 {
		t.Fatal("explicit Article adoption failed", adopted, code)
	}
	inspected, code := invoke("--inspect")
	if code != 0 || inspected.Status != "recorded" || inspected.UserID != adopted.UserID {
		t.Fatal("receipt cannot reconcile adoption", inspected, code)
	}
	if replay, code := invoke("--staff=true", "--superuser=true"); code != 1 || replay.Code != string(systemstate.CodeIdentityAlreadyInitialized) {
		t.Fatal("adoption replay changed stored role", replay, code)
	}
	config, err := operatorconfig.IdentityRuntimeConfig()
	if err != nil {
		t.Fatal(err)
	}
	current, err := systemstate.OpenIdentity(ctx, backend, config)
	if err != nil {
		t.Fatal(err)
	}
	authenticated, err := current.Authenticator().Authenticate(ctx, "existing", password)
	if err != nil || authenticated.SessionStamp() != credential.SessionStamp() || !authenticated.Principal().Staff() || authenticated.Principal().Superuser() {
		t.Fatal("adoption changed password/binding/explicit roles", err)
	}
	app, err := siteapp.New(ctx, siteapp.NewConfig(backend).WithLoopbackAuthentication())
	if app != nil || !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeSchemaUnavailable}) {
		t.Fatal("missing selected storage was accepted or misclassified", err)
	}
	// Installing the independent storage is necessary, but must not itself
	// adopt identity rows or revive their historical session namespace.
	sources := append(systemstate.IdentityMigrationSources(), article)
	sources = append(sources, domain.MigrationSources()...)
	sources = append(sources, accountaudit.MigrationSources()...)
	sources = append(sources, sessiondb.MigrationSources()...)
	migrate(sources)
	app, err = siteapp.New(ctx, siteapp.NewConfig(backend).WithLoopbackAuthentication())
	if app != nil || !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeTransitionRequired}) {
		t.Fatal("legacy adoption or schema installation implicitly selected the new account domain", err)
	}
	currentManager, err := sessions.NewManager(current.SessionStore(), sessions.Config{})
	if err != nil {
		t.Fatal(err)
	}
	preserved, found, err := currentManager.Peek(ctx, session.ID())
	if err != nil || !found || preserved.ID() != session.ID() ||
		!reflect.DeepEqual(preserved.Values(), session.Values()) ||
		!preserved.CreatedAt().Equal(session.CreatedAt()) || !preserved.AccessedAt().Equal(session.AccessedAt()) ||
		!preserved.AbsoluteExpiresAt().Equal(session.AbsoluteExpiresAt()) || !preserved.IdleExpiresAt().Equal(session.IdleExpiresAt()) {
		t.Fatal("adoption or refused startup changed the legacy session", err)
	}
	binding, err := project.BindAccount()
	if err != nil {
		t.Fatal(err)
	}
	handoffConfig := systemstate.IdentityHandoffConfig{Permission: "godj.admin.access", Authorizer: auth.PrincipalAuthorizer{}}
	owner, err := systemstate.NewIdentityHandoff(backend, binding, handoffConfig)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := owner.Prepare(ctx, authenticated.Principal(), strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	manifest := prepared.Manifest()
	receipt, err := owner.Commit(ctx, authenticated.Principal(), prepared)
	if err != nil || receipt.Sequence() != 1 {
		t.Fatal("explicit Article handoff failed", err)
	}
	if _, _, err := old.SessionStore().Load(ctx, session.ID()); err == nil {
		t.Fatal("old Article operator survived selected-domain handoff")
	}

	// Reopen a real independent connection and the host's generated binding.
	// The source includes Article's graph, not only the built-in identity app.
	reopened, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	})
	observer, err := systemstate.NewIdentityHandoff(reopened, binding, handoffConfig)
	if err != nil {
		t.Fatal(err)
	}
	actual, found, err := observer.Reconcile(ctx, manifest)
	if err != nil || !found || actual.OperationID() != receipt.OperationID() {
		t.Fatal("Article handoff did not reconcile after reconnect", err)
	}
	app, err = siteapp.New(ctx, siteapp.NewConfig(reopened).WithLoopbackAuthentication())
	if err != nil || app == nil {
		t.Fatal("explicitly handed-off Article could not start", err)
	}
	selected, err := project.NewAccountAuthenticator(ctx, reopened, config.PasswordHasher)
	if err != nil {
		t.Fatal(err)
	}
	selectedCredential, err := selected.Authenticate(ctx, "existing", password)
	if err != nil || selectedCredential.MatchesSessionStamp(credential.SessionStamp()) || !reflect.DeepEqual(selectedCredential.Principal().Permissions(), authenticated.Principal().Permissions()) {
		t.Fatal("Article handoff lost grants or inherited the old stamp", err)
	}
	store, err := project.NewAccountSessionStore(reopened, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Load(ctx, session.ID()); err != nil || found {
		t.Fatal("old Article session crossed the selected domain", err)
	}
}
