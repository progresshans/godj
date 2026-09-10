// Package databaseconfig owns the Article example's database selection and
// backend opening boundary. Diagnostics identify configuration keys and
// backend kinds without formatting credential-bearing values.
package databaseconfig

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/systemstate"
)

const (
	SQLiteDatabaseEnv = "GODJ_ARTICLE_SQLITE_DATABASE"
	PostgresURLEnv    = "GODJ_ARTICLE_POSTGRES_URL"
	PostgresSchemaEnv = "GODJ_ARTICLE_POSTGRES_SCHEMA"
)

// Kind identifies the configured Article database without exposing its value.
type Kind uint8

const (
	KindSQLite Kind = iota + 1
	KindPostgres
)

// Config is an immutable-by-contract, redacted database selection. Construct
// one through FromEnvironment, ForServe, SQLite, or PostgreSQL.
type Config struct {
	kind           Kind
	sqliteDatabase string
	postgresURL    string
	postgresSchema string
}

func (Config) String() string   { return "databaseconfig.Config{redacted}" }
func (Config) GoString() string { return "databaseconfig.Config{redacted}" }

// Kind reports the selected backend kind.
func (config Config) Kind() Kind { return config.kind }

// SQLiteDatabase returns the configured SQLite data source for the local
// Article site adapter. Callers must not log or publish the returned value.
func (config Config) SQLiteDatabase() string { return config.sqliteDatabase }

// PostgresURL returns the configured PostgreSQL URL for the local Article site
// adapter. Callers must not log or publish the returned value.
func (config Config) PostgresURL() string { return config.postgresURL }

// PostgresSchema returns the configured PostgreSQL schema.
func (config Config) PostgresSchema() string { return config.postgresSchema }

// MigrationSQLRenderer derives the immutable, credential-free SQL projection
// profile from this exact frozen database selection. A zero/failed selection
// returns nil so complete migration loading and target materialization retain
// precedence over renderer availability.
func (config Config) MigrationSQLRenderer() migrationbackend.MigrationSQLRenderer {
	switch config.kind {
	case KindSQLite:
		return sqlite.NewMigrationSQLRenderer()
	case KindPostgres:
		return postgres.NewMigrationSQLRenderer(postgres.MigrationSQLConfig{
			Schema: config.postgresSchema,
		})
	default:
		return nil
	}
}

// LookupEnvFunc is the narrow environment lookup dependency used by parsing
// tests and by the project-owned runner.
type LookupEnvFunc func(string) (string, bool)

// Backend is the shared Article site/migration backend lifetime.
type Backend interface {
	systemstate.Backend
	migrationbackend.RevisionFencedBackend
	Close() error
}

// FromEnvironment selects exactly one Article database from the project-owned
// environment. Values never appear in returned errors.
func FromEnvironment(lookup LookupEnvFunc) (Config, error) {
	return ForServe("", false, lookup)
}

// ForServe applies the site's explicit SQLite override before falling back to
// the same strict environment contract used by the project runner.
func ForServe(directSQLite string, directSpecified bool, lookup LookupEnvFunc) (Config, error) {
	if lookup == nil {
		return Config{}, errors.New("article site: environment lookup is nil")
	}
	directSQLite = strings.TrimSpace(directSQLite)
	if directSpecified && directSQLite == "" {
		return Config{}, errors.New("article site: --database is empty")
	}
	sqliteDatabase, sqliteConfigured, err := requiredEnvironmentValue(lookup, SQLiteDatabaseEnv)
	if err != nil {
		return Config{}, err
	}
	postgresURL, postgresURLConfigured, err := requiredEnvironmentValue(lookup, PostgresURLEnv)
	if err != nil {
		return Config{}, err
	}
	postgresSchema, postgresSchemaConfigured, err := requiredEnvironmentValue(lookup, PostgresSchemaEnv)
	if err != nil {
		return Config{}, err
	}
	if sqliteConfigured && (postgresURLConfigured || postgresSchemaConfigured) {
		return Config{}, errors.New("article site: SQLite and PostgreSQL environment are mutually exclusive")
	}
	if directSpecified && (sqliteConfigured || postgresURLConfigured || postgresSchemaConfigured) {
		return Config{}, errors.New("article site: --database and database environment are mutually exclusive")
	}
	if directSpecified {
		return SQLite(directSQLite)
	}
	if sqliteConfigured {
		return SQLite(sqliteDatabase)
	}
	if postgresURLConfigured != postgresSchemaConfigured {
		if !postgresURLConfigured {
			return Config{}, fmt.Errorf("article site: %s is required with %s", PostgresURLEnv, PostgresSchemaEnv)
		}
		return Config{}, fmt.Errorf("article site: %s is required with %s", PostgresSchemaEnv, PostgresURLEnv)
	}
	if postgresURLConfigured {
		return PostgreSQL(postgresURL, postgresSchema)
	}
	return Config{}, fmt.Errorf(
		"article site: configure %s or both %s and %s",
		SQLiteDatabaseEnv,
		PostgresURLEnv,
		PostgresSchemaEnv,
	)
}

// SQLite constructs one validated SQLite configuration.
func SQLite(database string) (Config, error) {
	database = strings.TrimSpace(database)
	if database == "" {
		return Config{}, errors.New("article site: SQLite database is empty")
	}
	return Config{kind: KindSQLite, sqliteDatabase: database}, nil
}

// PostgreSQL constructs one validated PostgreSQL configuration.
func PostgreSQL(url, schema string) (Config, error) {
	url = strings.TrimSpace(url)
	schema = strings.TrimSpace(schema)
	if url == "" {
		return Config{}, fmt.Errorf("article site: %s is empty", PostgresURLEnv)
	}
	if schema == "" {
		return Config{}, fmt.Errorf("article site: %s is empty", PostgresSchemaEnv)
	}
	return Config{kind: KindPostgres, postgresURL: url, postgresSchema: schema}, nil
}

// Open opens the selected backend without including its credential-bearing
// value or raw driver cause in a diagnostic.
func Open(ctx context.Context, config Config) (Backend, error) {
	if ctx == nil {
		return nil, errors.New("article site: nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch config.kind {
	case KindSQLite:
		backend, err := sqlite.Open(ctx, config.sqliteDatabase)
		if err != nil {
			return nil, errors.New("article site: open SQLite database failed")
		}
		return backend, nil
	case KindPostgres:
		backend, err := postgres.Open(ctx, postgres.Config{URL: config.postgresURL, Schema: config.postgresSchema})
		if err != nil {
			return nil, errors.New("article site: open PostgreSQL database failed")
		}
		return backend, nil
	default:
		return nil, errors.New("article site: database configuration is invalid")
	}
}

func requiredEnvironmentValue(lookup LookupEnvFunc, name string) (string, bool, error) {
	value, configured := lookup(name)
	if !configured {
		return "", false, nil
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false, fmt.Errorf("article site: %s is empty", name)
	}
	return value, true, nil
}
