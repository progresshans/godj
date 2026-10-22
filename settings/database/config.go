// Package database snapshots a project's database selection and opens its
// explicitly owned backend. Parsing never opens resources or reads os.Environ.
package database

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/systemstate"
)

type Kind uint8

const (
	KindSQLite Kind = iota + 1
	KindPostgres
)

// Config owns an immutable, validated selection. Its zero value is absent;
// Open rejects it. Explicit value accessors must not be written to logs.
type Config struct {
	kind           Kind
	sqliteDatabase string
	postgresURL    string
	postgresSchema string
}

func (Config) Format(state fmt.State, _ rune) { fmt.Fprint(state, "database.Config{redacted}") }
func (c Config) Kind() Kind                   { return c.kind }
func (c Config) SQLiteDatabase() string       { return c.sqliteDatabase }
func (c Config) PostgresURL() string          { return c.postgresURL }
func (c Config) PostgresSchema() string       { return c.postgresSchema }

// Backend is one caller-owned database lifetime shared by model operations,
// migration commands and the identity runtime. The caller closes it exactly
// when that invocation or runtime ends; Config never retains a live backend.
type Backend interface {
	systemstate.IdentityBackend
	migrationbackend.RevisionFencedBackend
	Close() error
}

// Error contains a field and a closed diagnostic, never a database value or
// a raw driver error. Field identifies a constructor field or environment key.
type Error struct {
	Field  string
	Detail string
}

func (e *Error) Error() string {
	if e == nil {
		return "database configuration error"
	}
	return "database configuration: " + e.Field + ": " + e.Detail
}

func (e *Error) Is(target error) bool {
	want, ok := target.(*Error)
	return ok && e != nil && want != nil && (want.Field == "" || e.Field == want.Field)
}

type LookupEnvFunc func(string) (string, bool)

// EnvironmentNames selects the three project-owned keys. The zero value uses
// GODJ_SQLITE_DATABASE, GODJ_POSTGRES_URL and GODJ_POSTGRES_SCHEMA. A custom
// set must provide three distinct, bounded shell identifiers.
type EnvironmentNames struct {
	SQLiteDatabase string
	PostgresURL    string
	PostgresSchema string
}

// Options defines explicit selection policy. Override conflicts with every
// present database environment key. Default is used only when no key and no
// override was supplied; malformed or incomplete input never falls back.
type Options struct {
	Names    EnvironmentNames
	Default  Config
	Override Config
}

// FromEnvironment reads each selected key once and retains only the validated
// values. Pass settings.Environment.Lookup for a process-wide snapshot. Outer
// whitespace is ignored by this database configuration syntax.
func FromEnvironment(lookup LookupEnvFunc, options Options) (Config, error) {
	if lookup == nil {
		return Config{}, &Error{Field: "environment", Detail: "lookup is nil"}
	}
	names, err := normalizeEnvironmentNames(options.Names)
	if err != nil {
		return Config{}, err
	}
	values := make([]string, 3)
	present := make([]bool, 3)
	keys := []string{names.SQLiteDatabase, names.PostgresURL, names.PostgresSchema}
	for index, key := range keys {
		values[index], present[index] = lookup(key)
		values[index] = strings.TrimSpace(values[index])
		if present[index] && values[index] == "" {
			return Config{}, &Error{Field: key, Detail: "is empty"}
		}
	}
	anyPresent := present[0] || present[1] || present[2]
	if options.Override.kind != 0 && anyPresent {
		return Config{}, &Error{Field: "database", Detail: "explicit database and environment are mutually exclusive"}
	}
	if present[0] && (present[1] || present[2]) {
		return Config{}, &Error{Field: "database", Detail: "SQLite and PostgreSQL environment are mutually exclusive"}
	}
	if options.Override.kind != 0 {
		return options.Override, nil
	}
	if present[0] {
		selected, err := SQLite(values[0])
		return selected, remapField(err, "sqlite_database", names.SQLiteDatabase)
	}
	if present[1] != present[2] {
		missing, companion := names.PostgresURL, names.PostgresSchema
		if present[1] {
			missing, companion = companion, missing
		}
		return Config{}, &Error{Field: missing, Detail: "is required with " + companion}
	}
	if present[1] {
		selected, err := PostgreSQL(values[1], values[2])
		err = remapField(err, "postgres_url", names.PostgresURL)
		return selected, remapField(err, "postgres_schema", names.PostgresSchema)
	}
	if options.Default.kind != 0 {
		return options.Default, nil
	}
	return Config{}, &Error{Field: "database", Detail: "configure " + names.SQLiteDatabase + " or both " + names.PostgresURL + " and " + names.PostgresSchema}
}

func remapField(err error, from, to string) error {
	if failure, ok := err.(*Error); ok && failure.Field == from {
		return &Error{Field: to, Detail: failure.Detail}
	}
	return err
}

func normalizeEnvironmentNames(names EnvironmentNames) (EnvironmentNames, error) {
	if names == (EnvironmentNames{}) {
		return EnvironmentNames{"GODJ_SQLITE_DATABASE", "GODJ_POSTGRES_URL", "GODJ_POSTGRES_SCHEMA"}, nil
	}
	seen := make(map[string]bool, 3)
	for _, name := range []string{names.SQLiteDatabase, names.PostgresURL, names.PostgresSchema} {
		if !validEnvironmentName(name) || seen[name] {
			return EnvironmentNames{}, &Error{Field: "environment_names", Detail: "keys must be distinct shell identifiers of at most 128 bytes"}
		}
		seen[name] = true
	}
	return names, nil
}

func validEnvironmentName(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for index, character := range []byte(value) {
		letter := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character == '_'
		if !letter && !(index > 0 && character >= '0' && character <= '9') {
			return false
		}
	}
	return true
}

// SQLite validates a nonempty SQLite file or URI without opening it.
func SQLite(value string) (Config, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsRune(value, 0) {
		return Config{}, &Error{Field: "sqlite_database", Detail: "must be nonempty and contain no NUL"}
	}
	return Config{kind: KindSQLite, sqliteDatabase: value}, nil
}

// PostgreSQL validates the backend-owned URL shape and schema namespace.
// Credentials, TLS options and server availability are checked when opened.
func PostgreSQL(rawURL, schema string) (Config, error) {
	rawURL, schema = strings.TrimSpace(rawURL), strings.TrimSpace(schema)
	if err := postgres.ValidateConfig(postgres.Config{URL: rawURL, Schema: schema}); err != nil {
		var failure *query.Error
		if errors.As(err, &failure) && failure.Field == "schema" {
			return Config{}, &Error{Field: "postgres_schema", Detail: "is not a supported application schema"}
		}
		return Config{}, &Error{Field: "postgres_url", Detail: "must identify a database with a postgres or postgresql URL"}
	}
	return Config{kind: KindPostgres, postgresURL: rawURL, postgresSchema: schema}, nil
}

// MigrationSQLRenderer returns the credential-free profile for the selection.
// A zero configuration yields nil so complete definition loading can retain
// precedence over renderer availability in a project command.
func (c Config) MigrationSQLRenderer() migrationbackend.MigrationSQLRenderer {
	switch c.kind {
	case KindSQLite:
		return sqlite.NewMigrationSQLRenderer()
	case KindPostgres:
		return postgres.NewMigrationSQLRenderer(postgres.MigrationSQLConfig{Schema: c.postgresSchema})
	default:
		return nil
	}
}

// Open returns one backend whose lifetime belongs to the caller. Failed
// opening returns no backend and never retains a credential-bearing cause.
func Open(ctx context.Context, config Config) (Backend, error) {
	if ctx == nil {
		return nil, &Error{Field: "context", Detail: "is nil"}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var opened Backend
	var err error
	switch config.kind {
	case KindSQLite:
		opened, err = sqlite.Open(ctx, config.sqliteDatabase)
	case KindPostgres:
		opened, err = postgres.Open(ctx, postgres.Config{URL: config.postgresURL, Schema: config.postgresSchema})
	default:
		return nil, &Error{Field: "database", Detail: "no database was selected"}
	}
	if err != nil {
		if canceled := ctx.Err(); canceled != nil {
			return nil, canceled
		}
		return nil, &Error{Field: "database", Detail: "opening the selected backend failed"}
	}
	return opened, nil
}
