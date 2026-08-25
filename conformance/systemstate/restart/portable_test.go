package restart_test

import (
	"sort"
	"strings"
	"testing"
)

const (
	sqliteDistinctProcessRestartSentinel   = "TestSystemStateSQLiteDistinctProcessRestartSentinel"
	postgresDistinctProcessRestartSentinel = "TestSystemStatePostgresDistinctProcessRestartSentinel"

	articleSQLiteDatabaseEnv = "GODJ_ARTICLE_SQLITE_DATABASE"
	articlePostgresURLEnv    = "GODJ_ARTICLE_POSTGRES_URL"
	articlePostgresSchemaEnv = "GODJ_ARTICLE_POSTGRES_SCHEMA"
	articleAdminUsernameEnv  = "GODJ_ARTICLE_ADMIN_USERNAME"
	articleAdminPasswordEnv  = "GODJ_ARTICLE_ADMIN_PASSWORD"
	postgresTestURLEnv       = "GODJ_TEST_POSTGRES_URL"
	postgresRequiredEnv      = "GODJ_REQUIRE_POSTGRES"
)

var articleSiteEnvironmentNames = []string{
	articleSQLiteDatabaseEnv,
	articlePostgresURLEnv,
	articlePostgresSchemaEnv,
	articleAdminUsernameEnv,
	articleAdminPasswordEnv,
	postgresTestURLEnv,
	postgresRequiredEnv,
}

// TestSystemStateRestartHarnessPortableContracts is deliberately platform
// neutral and skip-free. The product sentinels need Unix process-group
// signals, while these environment and inventory rules must compile and remain
// stable on every supported Go host.
func TestSystemStateRestartHarnessPortableContracts(t *testing.T) {
	if sqliteDistinctProcessRestartSentinel != "TestSystemStateSQLiteDistinctProcessRestartSentinel" ||
		postgresDistinctProcessRestartSentinel != "TestSystemStatePostgresDistinctProcessRestartSentinel" {
		t.Fatalf(
			"restart sentinel inventory changed: sqlite=%q postgres=%q",
			sqliteDistinctProcessRestartSentinel,
			postgresDistinctProcessRestartSentinel,
		)
	}

	environment := restartEnvironment([]string{
		"KEEP=value",
		articlePostgresURLEnv + "=postgresql://ambient.invalid/database",
		articlePostgresSchemaEnv + "=ambient_schema",
		articleAdminPasswordEnv + "=ambient-secret",
		postgresTestURLEnv + "=postgresql://test-only.invalid/database",
		postgresRequiredEnv + "=1",
	}, map[string]string{
		articleSQLiteDatabaseEnv: "file:portable.sqlite3?mode=rwc",
		articleAdminUsernameEnv:  "portable-admin",
		articleAdminPasswordEnv:  "portable-password",
	})
	values := restartEnvironmentMap(environment)
	if values["KEEP"] != "value" || values[articleSQLiteDatabaseEnv] != "file:portable.sqlite3?mode=rwc" ||
		values[articleAdminUsernameEnv] != "portable-admin" || values[articleAdminPasswordEnv] != "portable-password" {
		t.Fatalf("restart environment lost explicit values: %#v", values)
	}
	for _, name := range []string{
		articlePostgresURLEnv,
		articlePostgresSchemaEnv,
		postgresTestURLEnv,
		postgresRequiredEnv,
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
	for _, name := range []string{articleSQLiteDatabaseEnv, postgresTestURLEnv, postgresRequiredEnv} {
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
	for name, value := range explicit {
		values[name] = value
	}
	environment := make([]string, 0, len(values))
	for name, value := range values {
		environment = append(environment, name+"="+value)
	}
	sort.Strings(environment)
	return environment
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
