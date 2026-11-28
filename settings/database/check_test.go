package database

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDatabaseCheckDoesNotCreateAnUnopenedDatabase(t *testing.T) {
	file := filepath.Join(t.TempDir(), "private-path.sqlite3")
	config, err := SQLite(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := Check(t.Context(), config); !errors.Is(err, &Error{Field: "database"}) || strings.Contains(fmt.Sprintf("%+v %#v", err, err), "private-path") {
		t.Fatal("missing database check lost its closed diagnostic", err)
	}
	if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("check created a database", err)
	}
	if err := Check(nil, config); !errors.Is(err, &Error{Field: "context"}) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := Check(ctx, config); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := Check(t.Context(), Config{}); !errors.Is(err, &Error{Field: "database"}) {
		t.Fatal(err)
	}
	unsupported, err := SQLite("file:private-path.sqlite3?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	if err := Check(t.Context(), unsupported); !errors.Is(err, &Error{Field: "sqlite_database"}) || strings.Contains(err.Error(), "private-path") {
		t.Fatal("unsafe check options were not distinguished", err)
	}
}

func TestDatabaseCheckSharesClosedPostgresConfigurationDiagnostics(t *testing.T) {
	config, err := PostgreSQL("postgres://user:private-password@localhost/database?sslmode=disable", "application")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PGSERVICE", "private-service")
	if err := Check(t.Context(), config); !errors.Is(err, &Error{Field: "environment"}) || strings.Contains(fmt.Sprintf("%+v %#v", err, err), "private-") {
		t.Fatal("check lost environment ownership or exposed credentials", err)
	}
}
