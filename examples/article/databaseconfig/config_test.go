package databaseconfig

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
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
