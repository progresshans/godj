// Package postgres implements GoDj's current PostgreSQL database backend.
//
// It compiles the database-independent query AST to schema-qualified
// PostgreSQL SQL. The current profile deliberately supports PostgreSQL 17
// only, fixes every connection to UTC with search_path=pg_catalog, and never
// relies on search_path to resolve application tables.
package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

const (
	// DriverModule identifies the driver implementation owned by this backend.
	DriverModule = "github.com/jackc/pgx/v5"
	// DriverVersion is the exact pgx version in the current backend profile.
	DriverVersion = "v5.10.0"
	// CurrentServerMajor is the only PostgreSQL major in the current profile.
	CurrentServerMajor                  = 17
	postgresPhysicalSessionCloseTimeout = 5 * time.Second
)

// Config identifies one PostgreSQL database and one application schema.
// Schema is always rendered explicitly in generated SQL.
type Config struct {
	URL    string
	Schema string
}

type serverProfile struct {
	versionNumber                int
	timezone                     string
	searchPath                   string
	clientEncoding               string
	serverEncoding               string
	standardConformingStrings    string
	synchronousCommit            string
	defaultTransactionLevel      string
	defaultTransactionReadOnly   string
	defaultTransactionDeferrable string
	fsync                        string
	fullPageWrites               string
	sessionReplicationRole       string
	databaseEncoding             string
	databaseLocaleProvider       string
	databaseCollation            string
	databaseCType                string
	databaseLocale               sql.NullString
}

type Backend struct {
	database *sql.DB
	schema   string
	profile  serverProfile
	closed   atomic.Bool
}

var _ db.Queryer = (*Backend)(nil)

// Open validates the current PostgreSQL profile before publishing a backend.
// The configured schema must already exist; schema creation belongs to the
// migration/provisioning boundary rather than ordinary query startup.
func Open(ctx context.Context, config Config) (*Backend, error) {
	if ctx == nil {
		return nil, backendInvalid("context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateConfig(config); err != nil {
		return nil, err
	}

	connectionConfig, err := currentConnectionConfig(config.URL)
	if err != nil {
		// Do not retain the parse error: pgx errors may echo a URL containing
		// credentials. The stable diagnostic is intentionally secret-free.
		return nil, backendInvalid("PostgreSQL URL is invalid")
	}
	database := stdlib.OpenDB(
		*connectionConfig,
		stdlib.OptionAfterConnect(validateAndCloseInvalidPostgresPhysicalSession),
		stdlib.OptionResetSession(func(ctx context.Context, connection *pgx.Conn) error {
			if err := validateCurrentPostgresPhysicalSession(ctx, connection); err != nil {
				return errors.Join(driver.ErrBadConn, err)
			}
			return nil
		}),
	)
	if err := database.PingContext(ctx); err != nil {
		_ = database.Close()
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
		return nil, fmt.Errorf("ping PostgreSQL database: %w", redactConnectionError(err))
	}

	profile, schemaExists, err := readServerProfile(ctx, database, config.Schema)
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	if err := validateServerProfile(profile, schemaExists, config.Schema); err != nil {
		_ = database.Close()
		return nil, err
	}

	return &Backend{
		database: database,
		schema:   config.Schema,
		profile:  profile,
	}, nil
}

func validateAndCloseInvalidPostgresPhysicalSession(ctx context.Context, connection *pgx.Conn) error {
	err := validateCurrentPostgresPhysicalSession(ctx, connection)
	if err == nil || connection == nil {
		return err
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), postgresPhysicalSessionCloseTimeout)
	defer cancel()
	if closeErr := connection.Close(closeCtx); closeErr != nil {
		// The validation failure owns the public diagnostic. Do not retain an
		// arbitrary close error from a connection the pool never published.
		return errors.Join(err, errors.New("close invalid PostgreSQL physical session failed"))
	}
	return err
}

func currentConnectionConfig(rawURL string) (*pgx.ConnConfig, error) {
	connectionConfig, err := pgx.ParseConfig(rawURL)
	if err != nil {
		return nil, err
	}
	if connectionConfig.RuntimeParams == nil {
		connectionConfig.RuntimeParams = make(map[string]string)
	}
	// The current profile owns every behavioral startup parameter. In
	// particular, session_replication_role can disable ForeignKey triggers and
	// options can smuggle arbitrary `-c` assignments. Keep application_name as
	// the sole caller-controlled observability label and reject every other
	// non-profile runtime parameter instead of growing an implicit URL API.
	allowedRuntimeParameters := map[string]struct{}{
		"application_name":               {},
		"client_encoding":                {},
		"default_transaction_deferrable": {},
		"default_transaction_isolation":  {},
		"default_transaction_read_only":  {},
		"search_path":                    {},
		"standard_conforming_strings":    {},
		"synchronous_commit":             {},
		"timezone":                       {},
	}
	for parameter, value := range connectionConfig.RuntimeParams {
		if _, allowed := allowedRuntimeParameters[parameter]; allowed {
			continue
		}
		if strings.TrimSpace(value) != "" {
			return nil, fmt.Errorf("unsupported PostgreSQL startup parameter %s", parameter)
		}
		delete(connectionConfig.RuntimeParams, parameter)
	}
	// These assignments deliberately override URL-provided startup values so
	// every physical connection created by database/sql uses the exact current
	// profile before it can execute application SQL.
	connectionConfig.RuntimeParams["client_encoding"] = "UTF8"
	connectionConfig.RuntimeParams["default_transaction_deferrable"] = "off"
	connectionConfig.RuntimeParams["default_transaction_isolation"] = "read committed"
	connectionConfig.RuntimeParams["default_transaction_read_only"] = "off"
	connectionConfig.RuntimeParams["search_path"] = "pg_catalog"
	connectionConfig.RuntimeParams["standard_conforming_strings"] = "on"
	connectionConfig.RuntimeParams["synchronous_commit"] = "on"
	connectionConfig.RuntimeParams["timezone"] = "UTC"
	return connectionConfig, nil
}

func validateCurrentPostgresPhysicalSession(ctx context.Context, connection *pgx.Conn) error {
	if ctx == nil {
		return backendInvalid("PostgreSQL physical-session context is nil")
	}
	if connection == nil {
		return backendInvalid("PostgreSQL physical session is nil")
	}
	var timezone, searchPath, clientEncoding, standardConformingStrings, synchronousCommit string
	var defaultTransactionLevel, defaultTransactionReadOnly, defaultTransactionDeferrable string
	var fsync, fullPageWrites, replicationRole string
	if err := connection.QueryRow(
		ctx,
		`SELECT current_setting('TimeZone'), current_setting('search_path'), `+
			`current_setting('client_encoding'), current_setting('standard_conforming_strings'), `+
			`current_setting('synchronous_commit'), current_setting('default_transaction_isolation'), `+
			`current_setting('default_transaction_read_only'), current_setting('default_transaction_deferrable'), `+
			`current_setting('fsync'), current_setting('full_page_writes'), `+
			`current_setting('session_replication_role')`,
	).Scan(
		&timezone,
		&searchPath,
		&clientEncoding,
		&standardConformingStrings,
		&synchronousCommit,
		&defaultTransactionLevel,
		&defaultTransactionReadOnly,
		&defaultTransactionDeferrable,
		&fsync,
		&fullPageWrites,
		&replicationRole,
	); err != nil {
		return fmt.Errorf("validate PostgreSQL physical session: %w", err)
	}
	if timezone != "UTC" || searchPath != "pg_catalog" || clientEncoding != "UTF8" ||
		standardConformingStrings != "on" || synchronousCommit != "on" ||
		defaultTransactionLevel != "read committed" || defaultTransactionReadOnly != "off" ||
		defaultTransactionDeferrable != "off" || fsync != "on" || fullPageWrites != "on" ||
		replicationRole != "origin" {
		return backendInvalid("PostgreSQL physical session is outside the current runtime profile")
	}
	return nil
}

func validateConfig(config Config) error {
	if strings.TrimSpace(config.URL) == "" {
		return backendInvalid("PostgreSQL URL is empty")
	}
	parsed, err := url.Parse(config.URL)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") ||
		parsed.Host == "" || parsed.Path == "" || parsed.Path == "/" || parsed.Fragment != "" {
		return backendInvalid("PostgreSQL URL must identify a database with a postgres or postgresql URL")
	}
	if err := validateSchemaIdentifier(config.Schema); err != nil {
		return backendInvalid(err.Error())
	}
	return nil
}

func readServerProfile(ctx context.Context, database *sql.DB, schema string) (serverProfile, bool, error) {
	const statement = `SELECT
		current_setting('server_version_num')::integer,
		current_setting('TimeZone'),
		current_setting('search_path'),
		current_setting('client_encoding'),
		current_setting('server_encoding'),
		current_setting('standard_conforming_strings'),
		current_setting('synchronous_commit'),
		current_setting('default_transaction_isolation'),
		current_setting('default_transaction_read_only'),
		current_setting('default_transaction_deferrable'),
		current_setting('fsync'),
		current_setting('full_page_writes'),
		current_setting('session_replication_role'),
		pg_catalog.pg_encoding_to_char(current_database_row.encoding),
		current_database_row.datlocprovider,
		current_database_row.datcollate,
		current_database_row.datctype,
		current_database_row.datlocale,
		EXISTS (SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname = $1)
	FROM pg_catalog.pg_database AS current_database_row
	WHERE current_database_row.datname = pg_catalog.current_database()`
	var profile serverProfile
	var schemaExists bool
	if err := database.QueryRowContext(ctx, statement, schema).Scan(
		&profile.versionNumber,
		&profile.timezone,
		&profile.searchPath,
		&profile.clientEncoding,
		&profile.serverEncoding,
		&profile.standardConformingStrings,
		&profile.synchronousCommit,
		&profile.defaultTransactionLevel,
		&profile.defaultTransactionReadOnly,
		&profile.defaultTransactionDeferrable,
		&profile.fsync,
		&profile.fullPageWrites,
		&profile.sessionReplicationRole,
		&profile.databaseEncoding,
		&profile.databaseLocaleProvider,
		&profile.databaseCollation,
		&profile.databaseCType,
		&profile.databaseLocale,
		&schemaExists,
	); err != nil {
		return serverProfile{}, false, classifyDatabaseError(ctx, "read server profile", schema, "", err)
	}
	return profile, schemaExists, nil
}

func validateServerProfile(profile serverProfile, schemaExists bool, schema string) error {
	if profile.versionNumber/10000 != CurrentServerMajor {
		return &query.Error{
			Category: query.CategoryBackend,
			Code:     query.CodeUnsupported,
			Detail: fmt.Sprintf(
				"PostgreSQL server major is %d, current profile requires %d",
				profile.versionNumber/10000,
				CurrentServerMajor,
			),
		}
	}
	if profile.timezone != "UTC" || profile.searchPath != "pg_catalog" {
		return backendInvalid("PostgreSQL connection profile is not timezone=UTC and search_path=pg_catalog")
	}
	if profile.clientEncoding != "UTF8" || profile.serverEncoding != "UTF8" || profile.databaseEncoding != "UTF8" {
		return backendInvalid("PostgreSQL current profile requires UTF8 client, server, and database encoding")
	}
	if profile.standardConformingStrings != "on" {
		return backendInvalid("PostgreSQL current profile requires standard_conforming_strings=on")
	}
	if profile.synchronousCommit != "on" {
		return backendInvalid("PostgreSQL current profile requires synchronous_commit=on")
	}
	if profile.defaultTransactionLevel != "read committed" ||
		profile.defaultTransactionReadOnly != "off" || profile.defaultTransactionDeferrable != "off" {
		return backendInvalid(
			"PostgreSQL current profile requires READ COMMITTED, READ WRITE, NOT DEFERRABLE transaction defaults",
		)
	}
	if profile.fsync != "on" || profile.fullPageWrites != "on" {
		return backendInvalid("PostgreSQL current profile requires fsync=on and full_page_writes=on")
	}
	if profile.sessionReplicationRole != "origin" {
		return backendInvalid("PostgreSQL current profile requires session_replication_role=origin")
	}
	if profile.databaseLocaleProvider != "c" {
		return backendInvalid("PostgreSQL current profile requires the libc database locale provider")
	}
	if profile.databaseCollation != "C" || profile.databaseCType != "C" {
		return backendInvalid("PostgreSQL current profile requires C database collation and character classification")
	}
	if profile.databaseLocale.Valid {
		return backendInvalid("PostgreSQL current libc profile requires database locale to be NULL")
	}
	if !schemaExists {
		return backendInvalid(fmt.Sprintf("configured PostgreSQL schema %q does not exist", schema))
	}
	return nil
}

func (b *Backend) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	if err := b.validateContext(ctx); err != nil {
		return nil, err
	}
	statement, arguments, err := compilePlan(b.schema, plan)
	if err != nil {
		return nil, err
	}
	rows, err := b.database.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, classifyDatabaseError(ctx, "query", b.schema, plan.Table(), err)
	}
	return rows, nil
}

func (b *Backend) validateContext(ctx context.Context) error {
	if b == nil || b.database == nil || b.closed.Load() {
		return backendInvalid("PostgreSQL backend is nil or closed")
	}
	if ctx == nil {
		return backendInvalid("context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

// Close is idempotent and prevents new operations from entering the pool.
func (b *Backend) Close() error {
	if b == nil || !b.closed.CompareAndSwap(false, true) {
		return nil
	}
	if b.database == nil {
		return nil
	}
	if err := b.database.Close(); err != nil {
		return fmt.Errorf("close PostgreSQL database: %w", err)
	}
	return nil
}

func backendInvalid(detail string) *query.Error {
	return &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: detail}
}

// redactConnectionError preserves structured PostgreSQL errors while avoiding
// accidental publication of arbitrary URL-bearing driver diagnostics.
func redactConnectionError(err error) error {
	var structured interface{ SQLState() string }
	if errors.As(err, &structured) {
		return fmt.Errorf("PostgreSQL SQLSTATE %s", structured.SQLState())
	}
	return errors.New("PostgreSQL connection failed")
}
