package databaseconfig

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

func TestFromEnvironmentSelectsOneRedactedBackend(t *testing.T) {
	const secret = "article-database-secret"
	tests := []struct {
		name        string
		environment map[string]string
		wantKind    Kind
		wantError   string
	}{
		{name: "sqlite", environment: map[string]string{SQLiteDatabaseEnv: " file:article.sqlite3 "}, wantKind: KindSQLite},
		{name: "postgres", environment: map[string]string{PostgresURLEnv: " postgres://article:" + secret + "@localhost/article ", PostgresSchemaEnv: " article_runtime "}, wantKind: KindPostgres},
		{name: "missing", wantError: "configure " + SQLiteDatabaseEnv},
		{name: "empty sqlite", environment: map[string]string{SQLiteDatabaseEnv: " "}, wantError: SQLiteDatabaseEnv + " is empty"},
		{name: "postgres URL only", environment: map[string]string{PostgresURLEnv: "postgres://article:" + secret + "@localhost/article"}, wantError: PostgresSchemaEnv + " is required"},
		{name: "postgres schema only", environment: map[string]string{PostgresSchemaEnv: "article_runtime"}, wantError: PostgresURLEnv + " is required"},
		{name: "mixed", environment: map[string]string{SQLiteDatabaseEnv: "article.sqlite3", PostgresURLEnv: "postgres://article:" + secret + "@localhost/article", PostgresSchemaEnv: "article_runtime"}, wantError: "mutually exclusive"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := FromEnvironment(func(name string) (string, bool) {
				value, ok := test.environment[name]
				return value, ok
			})
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("FromEnvironment() error = %v, want containing %q", err, test.wantError)
				}
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("FromEnvironment() exposed secret: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if config.Kind() != test.wantKind {
				t.Fatalf("Kind() = %d, want %d", config.Kind(), test.wantKind)
			}
			if strings.Contains(fmt.Sprintf("%v %#v", config, config), secret) {
				t.Fatalf("formatted config exposed secret")
			}
		})
	}
}

func TestForServeRejectsDirectAndEnvironmentConflict(t *testing.T) {
	_, err := ForServe("article.sqlite3", true, func(name string) (string, bool) {
		if name == PostgresSchemaEnv {
			return "article_runtime", true
		}
		return "", false
	})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("ForServe() error = %v", err)
	}
}

func TestOpenSQLiteAndRejectInvalidConfiguration(t *testing.T) {
	if _, err := Open(context.Background(), Config{}); err == nil {
		t.Fatal("Open(zero) error = nil")
	}
	config, err := SQLite(filepath.Join(t.TempDir(), "article.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	backend, err := Open(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationSQLRendererDerivesCredentialFreeFrozenProfile(t *testing.T) {
	t.Parallel()
	if renderer := (Config{}).MigrationSQLRenderer(); renderer != nil {
		t.Fatalf("zero config renderer = %T, want nil", renderer)
	}

	sqliteConfig, err := SQLite("file:frozen.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	empty, err := sqliteConfig.MigrationSQLRenderer().RenderForwardMigrationSQL(
		context.Background(),
		migrationbackend.ForwardMigrationSQLRequest{
			App: "blog", Name: "zero",
			Intent: migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{}},
		},
	)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("SQLite renderer empty result = %#v, %v", empty, err)
	}

	const urlSecret = "article-renderer-url-secret"
	postgresConfig, err := PostgreSQL(
		"postgres://article:"+urlSecret+"@localhost/article",
		"frozen_schema",
	)
	if err != nil {
		t.Fatal(err)
	}
	request := migrationbackend.ForwardMigrationSQLRequest{
		App: "blog", Name: "0001_article",
		Intent: migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
			OperationIndex: 0,
			Kind:           migrationbackend.MigrationCreateModel,
			After: ir.Model{
				Name: "article", GoName: "Article", DBTable: "blog_article",
				Fields: []ir.Field{{
					Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true,
				}},
			},
		}}},
	}
	statements, err := postgresConfig.MigrationSQLRenderer().RenderForwardMigrationSQL(context.Background(), request)
	if err != nil || len(statements) != 1 || !strings.Contains(statements[0], `"frozen_schema".`) ||
		strings.Contains(statements[0], urlSecret) {
		t.Fatalf("PostgreSQL renderer = %#v, %v", statements, err)
	}

	const invalidSchemaSecret = "invalid-schema-secret!"
	invalidConfig, err := PostgreSQL("postgres://article:"+urlSecret+"@localhost/article", invalidSchemaSecret)
	if err != nil {
		t.Fatal(err)
	}
	statements, err = invalidConfig.MigrationSQLRenderer().RenderForwardMigrationSQL(context.Background(), request)
	if err == nil || statements != nil || strings.Contains(err.Error(), invalidSchemaSecret) || strings.Contains(err.Error(), urlSecret) {
		t.Fatalf("invalid PostgreSQL renderer = %#v, %v", statements, err)
	}
}
