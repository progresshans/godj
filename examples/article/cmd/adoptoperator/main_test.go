package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/databaseconfig"
	"github.com/progresshans/godj/examples/article/internal/operatorconfig"
	"github.com/progresshans/godj/examples/article/internal/siteapp"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/systemstate"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
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

func TestAdoptOperatorPreservesExistingArticleLoginAcrossReopen(t *testing.T) {
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
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	request.AddCookie(&http.Cookie{Name: websessionauth.DefaultSessionCookieName, Value: session.ID().Encoded()})
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatal("existing Article session failed after command adoption", response.Code)
	}
}
