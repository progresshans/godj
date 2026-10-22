package database

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

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

func TestDatabaseOpenRejectsInvalidContextBeforeCreatingAFile(t *testing.T) {
	t.Parallel()
	filename := filepath.Join(t.TempDir(), "unopened.sqlite3")
	config, err := SQLite(filename)
	if err != nil {
		t.Fatal(err)
	}
	if backend, err := Open(nil, config); backend != nil || !errors.Is(err, &Error{Field: "context"}) {
		t.Fatalf("nil context published a backend: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if backend, err := Open(ctx, config); backend != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation published a backend: %v", err)
	}
	if _, err := os.Stat(filename); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid invocation created a database file: %v", err)
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
	if err != nil || len(statements) != 1 || len(statements[0]) != 1 || !strings.Contains(statements[0][0], `"frozen_schema".`) ||
		strings.Contains(statements[0][0], urlSecret) {
		t.Fatalf("PostgreSQL renderer = %#v, %v", statements, err)
	}

	const invalidSchemaSecret = "invalid-schema-secret!"
	invalidConfig, err := PostgreSQL("postgres://article:"+urlSecret+"@localhost/article", invalidSchemaSecret)
	if err == nil || invalidConfig.Kind() != 0 || !errors.Is(err, &Error{Field: "postgres_schema"}) || strings.Contains(err.Error(), invalidSchemaSecret) || strings.Contains(err.Error(), urlSecret) {
		t.Fatalf("invalid PostgreSQL configuration reached renderer construction: %v", err)
	}
}
