package siteapp

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/internal/operatorconfig"
	"github.com/progresshans/godj/examples/article/webapp"
	"github.com/progresshans/godj/migrations"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
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
		if application != nil || !errors.Is(err, &systemstate.Error{
			Code: systemstate.CodeInvalidConfig, Field: "backend",
		}) {
			t.Fatalf("New(invalid backend) = (%v, %v)", application, err)
		}
	}
}

func TestNewSelectsPublicOnlyOnlyForExactMigratedCredentialAbsent(t *testing.T) {
	ctx := context.Background()

	absent := openSiteAppStateBackend(t, "siteapp-public-only")
	migrateSiteAppState(t, ctx, absent)
	application, err := New(ctx, NewConfig(absent))
	if err != nil || application == nil {
		t.Fatalf("New(clean credential-absent) = (%v, %v)", application, err)
	}
	if path, err := application.Reverse(webapp.ArticleListRoute); err != nil || path != webapp.ArticleListPath {
		t.Fatalf("public-only Article route = %q, %v", path, err)
	}
	if _, err := application.Reverse("admin:index"); err == nil {
		t.Fatal("credential-absent application published Admin routes")
	}
	publicPolicy, err := operatorconfig.CredentialPolicy()
	if err != nil {
		t.Fatal(err)
	}
	if err := systemstate.ProvisionOperator(ctx, absent, systemstate.ProvisionOperatorConfig{
		Username:         "later-admin",
		Password:         "later-provision-secret",
		CredentialPolicy: publicPolicy,
	}); err != nil {
		t.Fatalf("ProvisionOperator(after public-only startup): %v", err)
	}
	if _, err := application.Reverse("admin:index"); err == nil {
		t.Fatal("already-running public-only application hot-upgraded Admin routes")
	}

	unmigrated := openSiteAppStateBackend(t, "siteapp-unmigrated")
	if got, err := New(ctx, NewConfig(unmigrated)); got != nil || !errors.Is(err, &systemstate.Error{
		Code: systemstate.CodeSchemaUnavailable, Field: "migration_history",
	}) {
		t.Fatalf("New(unmigrated) = (%v, %v)", got, err)
	}
	wrongHistory := openSiteAppStateBackend(t, "siteapp-wrong-history")
	migrateSiteAppState(t, ctx, wrongHistory)
	if _, err := wrongHistory.ExecContext(ctx, `INSERT INTO "godj_migrations" ("app", "name") VALUES ('godj_system', '0002_unexpected')`); err != nil {
		t.Fatalf("insert wrong system history: %v", err)
	}
	if got, err := New(ctx, NewConfig(wrongHistory)); got != nil || !errors.Is(err, &systemstate.Error{
		Code: systemstate.CodeSchemaUnavailable, Field: "migration_history",
	}) {
		t.Fatalf("New(wrong system history) = (%v, %v)", got, err)
	}

	corrupt := openSiteAppStateBackend(t, "siteapp-corrupt")
	migrateSiteAppState(t, ctx, corrupt)
	if _, err := corrupt.ExecContext(ctx, `INSERT INTO "godj_system_credential"
  ("principal_id", "username", "encoded_password", "active", "permissions", "definition_digest")
VALUES ('article-development-admin', 'admin', 'malformed', 1, 'malformed', 'malformed')`); err != nil {
		t.Fatalf("insert corrupt credential: %v", err)
	}
	if got, err := New(ctx, NewConfig(corrupt).WithLoopbackAuthentication()); got != nil ||
		!errors.Is(err, &systemstate.Error{Code: systemstate.CodeCorruptState}) {
		t.Fatalf("New(corrupt credential) = (%v, %v)", got, err)
	}

	cardinality := openSiteAppStateBackend(t, "siteapp-cardinality")
	migrateSiteAppState(t, ctx, cardinality)
	for index := 0; index < 2; index++ {
		if _, err := cardinality.ExecContext(ctx, `INSERT INTO "godj_system_credential"
  ("principal_id", "username", "encoded_password", "active", "permissions", "definition_digest")
VALUES ('article-development-admin', 'admin', 'malformed', 1, 'malformed', 'malformed')`); err != nil {
			t.Fatalf("insert credential cardinality row %d: %v", index, err)
		}
	}
	if got, err := New(ctx, NewConfig(cardinality)); got != nil ||
		!errors.Is(err, &systemstate.Error{Code: systemstate.CodeCardinality, Field: "credential"}) {
		t.Fatalf("New(credential cardinality) = (%v, %v)", got, err)
	}

	dependent := openSiteAppStateBackend(t, "siteapp-dependent-row")
	migrateSiteAppState(t, ctx, dependent)
	if _, err := dependent.ExecContext(ctx, `INSERT INTO "godj_system_audit"
  ("actor_id", "model", "object_id", "action", "changed_fields", "display_label")
VALUES ('operator', 'godj_conformance.article', '1', 'add', 'v1.AAA', 'Article')`); err != nil {
		t.Fatalf("insert dependent audit row: %v", err)
	}
	if got, err := New(ctx, NewConfig(dependent)); got != nil ||
		!errors.Is(err, &systemstate.Error{Code: systemstate.CodeCorruptState, Field: "credential"}) {
		t.Fatalf("New(dependent row without credential) = (%v, %v)", got, err)
	}

	mismatch := openSiteAppStateBackend(t, "siteapp-policy-mismatch")
	migrateSiteAppState(t, ctx, mismatch)
	canonical, err := operatorconfig.CredentialPolicy()
	if err != nil {
		t.Fatal(err)
	}
	otherPrincipal, err := auth.NewPrincipal(auth.PrincipalConfig{
		ID:          "article-other-operator",
		Active:      true,
		Permissions: canonical.Principal.Permissions(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := systemstate.ProvisionOperator(ctx, mismatch, systemstate.ProvisionOperatorConfig{
		Username: "admin",
		Password: "policy-mismatch-secret",
		CredentialPolicy: systemstate.CredentialPolicy{
			Principal:      otherPrincipal,
			PasswordHasher: canonical.PasswordHasher,
		},
	}); err != nil {
		t.Fatal(err)
	}
	if got, err := New(ctx, NewConfig(mismatch).WithLoopbackAuthentication()); got != nil ||
		!errors.Is(err, &systemstate.Error{Code: systemstate.CodeCredentialPolicyMismatch}) {
		t.Fatalf("New(policy mismatch) = (%v, %v)", got, err)
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
	loaded, _, err := migrationdefinition.Load(systemstate.InitialDefinitionSource())
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
  "summary" VARCHAR(200) NULL
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
	systemstate.Backend
	URL string
}
