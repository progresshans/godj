package restart_test

import (
	"sort"
	"strings"
	"testing"
)

const (
	sqliteDistinctProcessRestartSentinel   = "TestSystemStateSQLiteDistinctProcessRestartSentinel"
	postgresDistinctProcessRestartSentinel = "TestSystemStatePostgresDistinctProcessRestartSentinel"
	postgresTwoProcessCoordinationSentinel = "TestSystemStatePostgresTwoProcessCoordinationRestartSentinel"

	articleSQLiteDatabaseEnv             = "GODJ_ARTICLE_SQLITE_DATABASE"
	articlePostgresURLEnv                = "GODJ_ARTICLE_POSTGRES_URL"
	articlePostgresSchemaEnv             = "GODJ_ARTICLE_POSTGRES_SCHEMA"
	articleAdminUsernameEnv              = "GODJ_ARTICLE_ADMIN_USERNAME"
	articleAdminPasswordEnv              = "GODJ_ARTICLE_ADMIN_PASSWORD"
	postgresTestURLEnv                   = "GODJ_TEST_POSTGRES_URL"
	postgresRequiredEnv                  = "GODJ_REQUIRE_POSTGRES"
	postgresAttestationCaptureEnv        = "GODJ_SYSTEM_STATE_POSTGRES_ATTESTATION_CAPTURE"
	projectOperatorAttestationCaptureEnv = "GODJ_PROJECT_OPERATOR_POSTGRES_ATTESTATION_CAPTURE"
)

var articleSiteEnvironmentNames = []string{
	articleSQLiteDatabaseEnv,
	articlePostgresURLEnv,
	articlePostgresSchemaEnv,
	postgresTestURLEnv,
	postgresRequiredEnv,
	postgresAttestationCaptureEnv,
	projectOperatorAttestationCaptureEnv,
}

var retiredArticleCredentialEnvironmentNames = []string{
	articleAdminUsernameEnv,
	articleAdminPasswordEnv,
}

// TestSystemStateRestartHarnessPortableContracts is deliberately platform
// neutral and skip-free. The product sentinels need Unix process-group
// signals, while these environment and inventory rules must compile and remain
// stable on every supported Go host.
func TestSystemStateRestartHarnessPortableContracts(t *testing.T) {
	if sqliteDistinctProcessRestartSentinel != "TestSystemStateSQLiteDistinctProcessRestartSentinel" ||
		postgresDistinctProcessRestartSentinel != "TestSystemStatePostgresDistinctProcessRestartSentinel" ||
		postgresTwoProcessCoordinationSentinel != "TestSystemStatePostgresTwoProcessCoordinationRestartSentinel" {
		t.Fatalf(
			"restart sentinel inventory changed: sqlite=%q postgres=%q coordination=%q",
			sqliteDistinctProcessRestartSentinel,
			postgresDistinctProcessRestartSentinel,
			postgresTwoProcessCoordinationSentinel,
		)
	}

	environment := restartEnvironment([]string{
		"KEEP=value",
		articlePostgresURLEnv + "=postgresql://ambient.invalid/database",
		articlePostgresSchemaEnv + "=ambient_schema",
		articleAdminUsernameEnv + "=ambient-admin",
		articleAdminPasswordEnv + "=ambient-secret",
		postgresTestURLEnv + "=postgresql://test-only.invalid/database",
		postgresRequiredEnv + "=1",
		postgresAttestationCaptureEnv + "=/ambient/capture.json",
		projectOperatorAttestationCaptureEnv + "=/ambient/operator-capture.json",
	}, map[string]string{
		articleSQLiteDatabaseEnv: "file:portable.sqlite3?mode=rwc",
		// Retired startup credentials are deliberately supplied here to prove
		// that even an explicit test-harness map cannot reintroduce them.
		articleAdminUsernameEnv: "portable-admin",
		articleAdminPasswordEnv: "portable-password",
	})
	values := restartEnvironmentMap(environment)
	if values["KEEP"] != "value" || values[articleSQLiteDatabaseEnv] != "file:portable.sqlite3?mode=rwc" {
		t.Fatalf("restart environment lost explicit values: %#v", values)
	}
	assertRestartEnvironmentOmitsRetiredCredentials(t, values)
	for _, name := range []string{
		articlePostgresURLEnv,
		articlePostgresSchemaEnv,
		postgresTestURLEnv,
		postgresRequiredEnv,
		postgresAttestationCaptureEnv,
		projectOperatorAttestationCaptureEnv,
	} {
		if _, exists := values[name]; exists {
			t.Fatalf("restart environment retained mutually exclusive %s", name)
		}
	}
	if !sort.StringsAreSorted(environment) {
		t.Fatalf("restart environment is not deterministic: %q", environment)
	}

	postgresEnvironment := restartEnvironment([]string{
		"KEEP=value",
		articleSQLiteDatabaseEnv + "=file:ambient.sqlite3?mode=rwc",
		postgresTestURLEnv + "=postgresql://test-only.invalid/database",
		postgresRequiredEnv + "=1",
		postgresAttestationCaptureEnv + "=/ambient/capture.json",
		projectOperatorAttestationCaptureEnv + "=/ambient/operator-capture.json",
	}, map[string]string{
		articlePostgresURLEnv:    "postgresql://article.invalid/database",
		articlePostgresSchemaEnv: "portable_schema",
		articleAdminUsernameEnv:  "portable-admin",
		articleAdminPasswordEnv:  "portable-password",
	})
	postgresValues := restartEnvironmentMap(postgresEnvironment)
	if postgresValues[articlePostgresURLEnv] != "postgresql://article.invalid/database" ||
		postgresValues[articlePostgresSchemaEnv] != "portable_schema" {
		t.Fatal("restart environment lost explicit PostgreSQL site configuration")
	}
	assertRestartEnvironmentOmitsRetiredCredentials(t, postgresValues)
	for _, name := range []string{articleSQLiteDatabaseEnv, postgresTestURLEnv, postgresRequiredEnv, postgresAttestationCaptureEnv, projectOperatorAttestationCaptureEnv} {
		if _, exists := postgresValues[name]; exists {
			t.Fatalf("PostgreSQL restart environment retained test-only or SQLite %s", name)
		}
	}
	if !sort.StringsAreSorted(postgresEnvironment) {
		t.Fatal("PostgreSQL restart environment is not deterministic")
	}
}

func restartEnvironment(base []string, explicit map[string]string) []string {
	values := restartEnvironmentMap(base)
	for _, name := range articleSiteEnvironmentNames {
		delete(values, name)
	}
	for _, name := range retiredArticleCredentialEnvironmentNames {
		delete(values, name)
	}
	for name, value := range explicit {
		values[name] = value
	}
	// Raw operator credentials were retired from Article startup. Scrub them
	// after explicit values as well as ambient values so no test helper can
	// accidentally reconstruct the removed bootstrap surface.
	for _, name := range retiredArticleCredentialEnvironmentNames {
		delete(values, name)
	}
	environment := make([]string, 0, len(values))
	for name, value := range values {
		environment = append(environment, name+"="+value)
	}
	sort.Strings(environment)
	return environment
}

func assertRestartEnvironmentOmitsRetiredCredentials(t *testing.T, values map[string]string) {
	t.Helper()
	for _, retired := range retiredArticleCredentialEnvironmentNames {
		if _, exists := values[retired]; exists {
			t.Fatalf("restart environment retained retired startup credential %s", retired)
		}
		for _, active := range articleSiteEnvironmentNames {
			if active == retired {
				t.Fatalf("active Article environment inventory retained retired startup credential %s", retired)
			}
		}
	}
}

func restartEnvironmentMap(environment []string) map[string]string {
	values := make(map[string]string, len(environment))
	for _, entry := range environment {
		name, value, found := strings.Cut(entry, "=")
		if !found || name == "" {
			continue
		}
		values[name] = value
	}
	return values
}
