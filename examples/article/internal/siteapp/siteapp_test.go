package siteapp

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/account/domain"
	domainmodels "github.com/progresshans/godj/account/domain/models"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/apiapp"
	"github.com/progresshans/godj/examples/article/internal/accounts"
	"github.com/progresshans/godj/examples/article/internal/accounttest"
	"github.com/progresshans/godj/examples/article/internal/operatorconfig"
	"github.com/progresshans/godj/examples/article/modeldef"
	"github.com/progresshans/godj/examples/article/webapp"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/migrations"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/project/firstaccount"
	"github.com/progresshans/godj/systemstate"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

func TestConfigDiagnosticsAndJSONNeverExposeSecrets(t *testing.T) {
	t.Parallel()

	const (
		backendMarker = "postgresql://backend-secret-marker"
		csrfKeyMarker = "siteapp-csrf-key-secret-marker!!"
	)
	keyRing, err := websessionauth.NewCSRFKeyRing([]byte(csrfKeyMarker))
	if err != nil {
		t.Fatalf("NewCSRFKeyRing(): %v", err)
	}
	config := NewConfig(configMarkerBackend{URL: backendMarker}).
		WithLoopbackAuthentication().
		WithCSRFKeyRing(keyRing)
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("json.Marshal(Config): %v", err)
	}
	if string(encoded) != `"siteapp.Config{redacted}"` {
		t.Fatalf("Config JSON diagnostic = %s, want explicit redaction", encoded)
	}
	rendered := []string{string(encoded)}
	for _, diagnostic := range []string{fmt.Sprint(config), fmt.Sprint(&config)} {
		if diagnostic != configDiagnostic {
			t.Fatalf("Config default diagnostic = %q, want %q", diagnostic, configDiagnostic)
		}
		rendered = append(rendered, diagnostic)
	}
	for _, format := range []string{
		"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%o", "%p", "%w",
		"%020d", "%#[1]v",
	} {
		for _, diagnostic := range []string{fmt.Sprintf(format, config), fmt.Sprintf(format, &config)} {
			// fmt reserves invalid/special %p and %w representations before
			// consulting Formatter. Opaque state makes those paths secret-free.
			if format != "%p" && format != "%w" && diagnostic != configDiagnostic {
				t.Fatalf("Config format %q diagnostic = %q, want %q", format, diagnostic, configDiagnostic)
			}
			rendered = append(rendered, diagnostic)
		}
	}
	wrapFormat := "wrapped config: %w"
	wrapped := fmt.Errorf(wrapFormat, config).Error()
	rendered = append(rendered, wrapped)
	for _, diagnostic := range rendered {
		for _, marker := range append(secretMarkerEncodings(backendMarker), secretMarkerEncodings(csrfKeyMarker)...) {
			if strings.Contains(diagnostic, marker) {
				t.Fatalf("Config diagnostic exposes encoded secret marker %q: %q", marker, diagnostic)
			}
		}
	}
	if strings.Contains(string(encoded), `"Username"`) || strings.Contains(string(encoded), `"Password"`) ||
		strings.Contains(string(encoded), `"Backend"`) ||
		strings.Contains(string(encoded), `"CSRFKeyRing"`) {
		t.Fatalf("Config JSON publishes a secret-bearing field: %s", encoded)
	}
	var decoded Config
	if err := json.Unmarshal([]byte(`{"Username":"admin","Password":"legacy-secret","Backend":{"URL":"`+backendMarker+`"}}`), &decoded); err != nil {
		t.Fatalf("json.Unmarshal(Config): %v", err)
	}
	if decoded.state != nil {
		t.Fatal("Config JSON populated a secret-bearing field")
	}
}

func TestNewConfigPreservesStartupValidationOrder(t *testing.T) {
	t.Parallel()

	if application, err := New(nil, NewConfig(nil)); application != nil ||
		err == nil || err.Error() != "article site application: context is nil" {
		t.Fatalf("New(nil context) = (%v, %v)", application, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if application, err := New(canceled, NewConfig(nil)); application != nil ||
		err == nil || err != context.Canceled {
		t.Fatalf("New(canceled context) = (%v, %v)", application, err)
	}
	for _, config := range []Config{{}, NewConfig(nil), NewConfig(nil).WithLoopbackAuthentication()} {
		application, err := New(context.Background(), config)
		if application != nil || !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeInvalidConfig}) {
			t.Fatalf("New(invalid backend) = (%v, %v)", application, err)
		}
	}
}

func TestNewPublishesOnlyInstalledFreshAccountDomain(t *testing.T) {
	ctx := t.Context()
	absent := openSiteAppStateBackend(t, "siteapp-public-only")
	migrateSiteAppState(t, ctx, absent)
	application, err := New(ctx, NewConfig(absent))
	if err != nil || application == nil {
		t.Fatalf("fresh startup: %v", err)
	}
	if path, err := application.Reverse(webapp.ArticleListRoute); err != nil || path != webapp.ArticleListPath {
		t.Fatal("public route", err)
	}
	if _, err := application.Reverse(apiapp.Namespace + ":admin-index"); err == nil {
		t.Fatal("fresh application published Admin")
	}
	if _, err := application.Reverse(apiapp.OpenAPIRouteName); err == nil {
		t.Fatal("fresh application published API")
	}
	response := httptest.NewRecorder()
	application.ServeHTTP(response, httptest.NewRequest(http.MethodGet, apiapp.OpenAPIPath, nil))
	if response.Code != http.StatusNotFound || response.Body.String() != "Not Found\n" {
		t.Fatal("fresh API document visible")
	}
	if _, err := accounttest.Provision(ctx, absent, "later-admin", "later-provision-secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := application.Reverse(apiapp.Namespace + ":admin-index"); err == nil {
		t.Fatal("running public process hot-upgraded Admin")
	}
	if _, err := application.Reverse(apiapp.OpenAPIRouteName); err == nil {
		t.Fatal("running public process hot-upgraded API")
	}
	restarted, err := New(ctx, NewConfig(absent).WithLoopbackAuthentication())
	if err != nil || restarted == nil {
		t.Fatal("explicit restart did not publish ready account", err)
	}
	if _, err := restarted.Reverse(apiapp.Namespace + ":admin-index"); err != nil {
		t.Fatal("restarted Admin missing", err)
	}

	unmigrated := openSiteAppStateBackend(t, "siteapp-unmigrated")
	if got, err := New(ctx, NewConfig(unmigrated)); got != nil || !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeSchemaUnavailable}) {
		t.Fatal("unmigrated startup", err)
	}
	wrong := openSiteAppStateBackend(t, "siteapp-wrong-history")
	migrateSiteAppState(t, ctx, wrong)
	if _, err := wrong.ExecContext(ctx, `INSERT INTO "godj_migrations" ("app", "name") VALUES ('godj_account_domain', '0002_unexpected')`); err != nil {
		t.Fatal(err)
	}
	if got, err := New(ctx, NewConfig(wrong)); got != nil || !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeSchemaUnavailable}) {
		t.Fatal("unexpected storage migration", err)
	}
}

func TestNewRejectsCorruptSelectedAccountStorage(t *testing.T) {
	for _, test := range []struct {
		name, statement string
		provision       bool
		want            error
	}{
		{"unowned_row", `INSERT INTO "godj_identity_user" ("principal_id","username","encoded_password","active","staff","superuser","revision","date_joined","first_name","last_name","email") VALUES ('unowned','unowned','!',1,0,0,1,'2026-10-27T00:00:00Z','','','')`, false, &accountmodel.Error{Code: accountmodel.CodeNotFresh}},
		{"history_gap", `UPDATE "godj_account_domain_transition" SET "sequence" = 2`, true, domain.ErrCorrupt},
		{"invalid_generation", `UPDATE "godj_account_domain_transition" SET "generation" = 'invalid'`, true, domain.ErrCorrupt},
		{"foreign_role", `UPDATE "godj_account_domain_transition" SET "role_fingerprint" = '` + strings.Repeat("a", 64) + `'`, true, domain.ErrMismatch},
		{"corrupt_session", `INSERT INTO "godj_sessions_session" ("namespace","digest","payload") VALUES ('foreign','` + strings.Repeat("b", 64) + `','malformed')`, false, nil},
		{"orphaned_audit", `INSERT INTO "godj_account_audit_event" ("domain","actor_id","model","object_id","action","changed_fields","recorded_at") VALUES ('` + strings.Repeat("c", 64) + `','operator','godj_conformance.article',1,'add','[]','2026-10-27T00:00:00Z')`, false, &accountmodel.Error{Code: accountmodel.CodeNotFresh}},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := openSiteAppStateBackend(t, "selected-"+test.name)
			migrateSiteAppState(t, t.Context(), backend)
			if test.provision {
				if _, err := accounttest.Provision(t.Context(), backend, "admin", "river compass evergreen 71!"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := backend.ExecContext(t.Context(), test.statement); err != nil {
				t.Fatal(err)
			}
			if got, err := New(t.Context(), NewConfig(backend).WithLoopbackAuthentication()); got != nil || err == nil || test.want != nil && !errors.Is(err, test.want) {
				t.Fatal("invalid selected storage published or wrong failure", err)
			}
		})
	}
}

func TestNewRejectsHistoricalOperatorStorageWithoutAdoption(t *testing.T) {
	for _, mode := range []string{"empty", "corrupt", "cardinality", "orphaned_audit", "provisioned"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			backend := openSiteAppStateBackend(t, "legacy-"+mode)
			sources := append(modeldef.MigrationSources(), systemstate.InitialDefinitionSource())
			loaded, _, err := migrationdefinition.Load(sources...)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
				t.Fatal(err)
			}
			if mode == "corrupt" || mode == "cardinality" {
				count := 1
				if mode == "cardinality" {
					count = 2
				}
				for range count {
					if _, err := backend.ExecContext(ctx, `INSERT INTO "godj_system_credential" ("principal_id","username","encoded_password","active","permissions","definition_digest") VALUES ('legacy','admin','malformed',1,'malformed','malformed')`); err != nil {
						t.Fatal(err)
					}
				}
			}
			if mode == "orphaned_audit" {
				if _, err := backend.ExecContext(ctx, `INSERT INTO "godj_system_audit" ("actor_id","model","object_id","action","changed_fields","display_label") VALUES ('legacy','godj_conformance.article','1','add','v1.AAA','Article')`); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "provisioned" {
				policy, err := operatorconfig.CredentialPolicy()
				if err != nil {
					t.Fatal(err)
				}
				if err := systemstate.ProvisionOperator(ctx, backend, systemstate.ProvisionOperatorConfig{Username: "admin", Password: "legacy-adoption-secret", CredentialPolicy: policy}); err != nil {
					t.Fatal(err)
				}
			}
			if got, err := New(ctx, NewConfig(backend).WithLoopbackAuthentication()); got != nil || !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeTransitionRequired}) {
				t.Fatal("historical storage was accepted", err)
			}
			command, err := accounts.FirstAccount(func(context.Context) (firstaccount.Backend, error) { return backend, nil })
			if err != nil {
				t.Fatal(err)
			}
			opened, err := command.Open(ctx)
			if opened != backend || !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeTransitionRequired}) {
				t.Fatal("first account ignored historical ownership or lost acquired backend", err)
			}
			count, err := domainmodels.TransitionObjects.Using(backend).Count(ctx)
			if err != nil || count != 0 {
				t.Fatal("rejection initialized selected ownership", err)
			}

		})
	}
}

func openSiteAppStateBackend(t *testing.T, name string) *sqlite.Backend {
	t.Helper()
	backend, err := sqlite.OpenMemory(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("close %s backend: %v", name, err)
		}
	})
	return backend
}

func migrateSiteAppState(t *testing.T, ctx context.Context, backend *sqlite.Backend) {
	t.Helper()
	loaded, _, err := migrationdefinition.Load(modeldef.MigrationSources()...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.ExecContext(ctx, `CREATE TABLE "godj_conformance_article" (
  "id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
  "title" VARCHAR(200) NOT NULL,
  "published" BOOLEAN NOT NULL,
  "summary" VARCHAR(200) NULL,
  "slug" VARCHAR(50) NULL UNIQUE
)`); err != nil {
		t.Fatal(err)
	}
}

func secretMarkerEncodings(value string) []string {
	material := []byte(value)
	decimal := make([]string, len(material))
	octal := make([]string, len(material))
	for index, character := range material {
		decimal[index] = strconv.Itoa(int(character))
		octal[index] = strconv.FormatUint(uint64(character), 8)
	}
	return []string{
		value,
		hex.EncodeToString(material),
		strings.ToUpper(hex.EncodeToString(material)),
		base64.StdEncoding.EncodeToString(material),
		base64.RawStdEncoding.EncodeToString(material),
		base64.URLEncoding.EncodeToString(material),
		base64.RawURLEncoding.EncodeToString(material),
		strings.Join(decimal, " "),
		strings.Join(octal, " "),
	}
}

type configMarkerBackend struct {
	Backend
	URL string
}
