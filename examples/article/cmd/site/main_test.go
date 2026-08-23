package main

import (
	"bytes"
	"context"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestParseServeConfig(t *testing.T) {
	config, err := parseServeConfig([]string{"serve"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if config.listenAddress != defaultListenAddress || config.database != "" || config.databaseSpecified {
		t.Fatalf("config = %#v", config)
	}
	config, err = parseServeConfig([]string{"serve", "--listen", "127.0.0.1:0"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if config.listenAddress != "127.0.0.1:0" || config.database != "" || config.databaseSpecified {
		t.Fatalf("runserver config = %#v", config)
	}
	config, err = parseServeConfig([]string{"serve", "--database", "file:article.sqlite3"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if config.listenAddress != defaultListenAddress || config.database != "file:article.sqlite3" || !config.databaseSpecified {
		t.Fatalf("explicit config = %#v", config)
	}
}

func TestParseServeConfigRejectsInvalidArguments(t *testing.T) {
	tests := [][]string{
		nil,
		{"run", "--database", "article.sqlite3"},
		{"serve", "--database", "article.sqlite3", "unexpected"},
		{"serve", "--listen", " ", "--database", "article.sqlite3"},
		{"serve", "--database", " "},
	}
	for _, arguments := range tests {
		if _, err := parseServeConfig(arguments, &bytes.Buffer{}); err == nil {
			t.Errorf("parseServeConfig(%q) error = nil", arguments)
		}
	}
}

func TestDatabaseConfigForServeSelectsStrictEnvironment(t *testing.T) {
	tests := []struct {
		name        string
		config      serveConfig
		environment map[string]string
		want        databaseConfig
		wantError   string
	}{
		{
			name:        "sqlite environment",
			environment: map[string]string{articleSQLiteDatabaseEnv: " file:article.sqlite3 "},
			want:        databaseConfig{kind: databaseKindSQLite, sqliteDatabase: "file:article.sqlite3"},
		},
		{
			name: "postgres environment",
			environment: map[string]string{
				articlePostgresURLEnv:    " postgres://article:secret@127.0.0.1/article ",
				articlePostgresSchemaEnv: " article_runtime ",
			},
			want: databaseConfig{
				kind:           databaseKindPostgres,
				postgresURL:    "postgres://article:secret@127.0.0.1/article",
				postgresSchema: "article_runtime",
			},
		},
		{
			name:   "direct sqlite compatibility",
			config: serveConfig{database: "article.sqlite3", databaseSpecified: true},
			want:   databaseConfig{kind: databaseKindSQLite, sqliteDatabase: "article.sqlite3"},
		},
		{
			name:      "missing configuration",
			wantError: "configure " + articleSQLiteDatabaseEnv,
		},
		{
			name:        "empty sqlite",
			environment: map[string]string{articleSQLiteDatabaseEnv: " "},
			wantError:   articleSQLiteDatabaseEnv + " is empty",
		},
		{
			name:        "postgres URL without schema",
			environment: map[string]string{articlePostgresURLEnv: "postgres://article:secret@127.0.0.1/article"},
			wantError:   articlePostgresSchemaEnv + " is required",
		},
		{
			name:        "postgres schema without URL",
			environment: map[string]string{articlePostgresSchemaEnv: "article_runtime"},
			wantError:   articlePostgresURLEnv + " is required",
		},
		{
			name: "backend conflict",
			environment: map[string]string{
				articleSQLiteDatabaseEnv: "article.sqlite3",
				articlePostgresURLEnv:    "postgres://article:secret@127.0.0.1/article",
				articlePostgresSchemaEnv: "article_runtime",
			},
			wantError: "SQLite and PostgreSQL environment are mutually exclusive",
		},
		{
			name:   "direct and environment conflict",
			config: serveConfig{database: "article.sqlite3", databaseSpecified: true},
			environment: map[string]string{
				articlePostgresURLEnv:    "postgres://article:secret@127.0.0.1/article",
				articlePostgresSchemaEnv: "article_runtime",
			},
			wantError: "--database and database environment are mutually exclusive",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lookup := func(name string) (string, bool) {
				value, exists := test.environment[name]
				return value, exists
			}
			got, err := databaseConfigForServe(test.config, lookup)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("databaseConfigForServe() error = %v, want containing %q", err, test.wantError)
				}
				if strings.Contains(err.Error(), "secret") {
					t.Fatalf("databaseConfigForServe() exposed PostgreSQL URL secret: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("databaseConfigForServe() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestOpenArticleBackendPostgresDiagnosticDoesNotExposeURLSecret(t *testing.T) {
	const secret = "runtime-password"
	_, err := openArticleBackend(context.Background(), databaseConfig{
		kind:           databaseKindPostgres,
		postgresURL:    "https://article:" + secret + "@example.invalid/article",
		postgresSchema: "article_runtime",
	})
	if err == nil {
		t.Fatal("openArticleBackend() error = nil")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("openArticleBackend() exposed PostgreSQL URL secret: %v", err)
	}
}

func TestRunRejectsNilContextBeforeSideEffects(t *testing.T) {
	if err := run(nil, []string{"serve"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("run(nil) error = nil")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := run(canceled, []string{"serve"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("run(canceled) error = nil")
	}
}

func TestRunClosesBackendAndListenerOnceWhenContextCancelsBeforeServeOwnership(t *testing.T) {
	clearArticleDatabaseEnvironment(t)
	t.Setenv(articleSQLiteDatabaseEnv, "file:site-cancel-window?mode=memory&cache=shared")
	ctx, cancel := context.WithCancel(context.Background())
	var backendCloses atomic.Int32
	openBackend := func(ctx context.Context, config databaseConfig) (articleBackend, error) {
		backend, err := openArticleBackend(ctx, config)
		if err != nil {
			return nil, err
		}
		return &countingArticleBackend{articleBackend: backend, closes: &backendCloses}, nil
	}
	var listener *countingListener
	listen := func(network, address string) (net.Listener, error) {
		opened, err := net.Listen(network, "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		listener = &countingListener{Listener: opened}
		return listener, nil
	}
	stdout := cancelWriter{cancel: cancel}
	err := runWithListener(
		ctx,
		[]string{"serve", "--listen", "127.0.0.1:0"},
		stdout,
		&bytes.Buffer{},
		listen,
		openBackend,
	)
	if err != nil {
		t.Fatalf("runWithListener() error = %v", err)
	}
	if listener == nil {
		t.Fatal("listener was not created")
	}
	if closes := listener.closes.Load(); closes != 1 {
		t.Fatalf("listener Close() calls = %d, want 1", closes)
	}
	if closes := backendCloses.Load(); closes != 1 {
		t.Fatalf("backend Close() calls = %d, want 1", closes)
	}
}

type countingArticleBackend struct {
	articleBackend
	closes *atomic.Int32
}

func (backend *countingArticleBackend) Close() error {
	backend.closes.Add(1)
	return backend.articleBackend.Close()
}

func clearArticleDatabaseEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		articleSQLiteDatabaseEnv,
		articlePostgresURLEnv,
		articlePostgresSchemaEnv,
	} {
		value, configured := os.LookupEnv(name)
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if configured {
				if err := os.Setenv(name, value); err != nil {
					t.Errorf("restore %s: %v", name, err)
				}
				return
			}
			if err := os.Unsetenv(name); err != nil {
				t.Errorf("unset %s: %v", name, err)
			}
		})
	}
}

type cancelWriter struct {
	cancel context.CancelFunc
}

func (w cancelWriter) Write(payload []byte) (int, error) {
	w.cancel()
	return len(payload), nil
}

type countingListener struct {
	net.Listener
	closes atomic.Int32
}

func (l *countingListener) Close() error {
	l.closes.Add(1)
	return l.Listener.Close()
}
