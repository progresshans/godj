package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/internal/siteapp"
	"github.com/progresshans/godj/examples/article/webapp"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/web"
)

const (
	defaultListenAddress     = "127.0.0.1:8000"
	articleSQLiteDatabaseEnv = "GODJ_ARTICLE_SQLITE_DATABASE"
	articlePostgresURLEnv    = "GODJ_ARTICLE_POSTGRES_URL"
	articlePostgresSchemaEnv = "GODJ_ARTICLE_POSTGRES_SCHEMA"
	articleAdminUsernameEnv  = "GODJ_ARTICLE_ADMIN_USERNAME"
	articleAdminPasswordEnv  = "GODJ_ARTICLE_ADMIN_PASSWORD"
)

type serveConfig struct {
	listenAddress     string
	database          string
	databaseSpecified bool
}

type databaseKind uint8

const (
	databaseKindSQLite databaseKind = iota + 1
	databaseKindPostgres
)

type databaseConfig struct {
	kind           databaseKind
	sqliteDatabase string
	postgresURL    string
	postgresSchema string
}

type publicationConfig struct {
	authenticated bool
	username      string
	password      string
}

func (publicationConfig) String() string   { return "publicationConfig{redacted}" }
func (publicationConfig) GoString() string { return "publicationConfig{redacted}" }

type articleBackend interface {
	systemstate.Backend
	Close() error
}

type lookupEnvFunc func(string) (string, bool)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "article site failed:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string, stdout, stderr io.Writer) error {
	return runWithListener(ctx, arguments, stdout, stderr, net.Listen, openArticleBackend)
}

type listenFunc func(network, address string) (net.Listener, error)
type openArticleBackendFunc func(context.Context, databaseConfig) (articleBackend, error)

func runWithListener(
	ctx context.Context,
	arguments []string,
	stdout, stderr io.Writer,
	listen listenFunc,
	openBackend openArticleBackendFunc,
) (resultErr error) {
	if ctx == nil {
		return errors.New("article site: nil context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if openBackend == nil {
		return errors.New("article site: nil backend opener")
	}
	config, err := parseServeConfig(arguments, stderr)
	if err != nil {
		return err
	}
	database, err := databaseConfigForServe(config, os.LookupEnv)
	if err != nil {
		return err
	}
	publication, err := publicationConfigForServe(config, os.LookupEnv)
	if err != nil {
		return err
	}
	backend, err := openBackend(ctx, database)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, backend.Close())
	}()
	application, err := applicationForServe(ctx, backend, publication)
	if err != nil {
		return err
	}
	listener, err := listen("tcp", config.listenAddress)
	if err != nil {
		return fmt.Errorf("article site: listen: %w", err)
	}
	ownedListener := &onceCloseListener{Listener: listener}
	defer func() {
		resultErr = errors.Join(resultErr, ownedListener.Close())
	}()
	server, err := web.NewServer(application, web.ServerOptions{})
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "article site listening on http://%s\n", ownedListener.Addr()); err != nil {
		return fmt.Errorf("article site: report listener: %w", err)
	}
	if err := server.Serve(ctx, ownedListener); err != nil {
		if contextErr := ctx.Err(); contextErr != nil && err == contextErr {
			return nil
		}
		return fmt.Errorf("article site: serve: %w", err)
	}
	return nil
}

func applicationForServe(
	ctx context.Context,
	backend articleBackend,
	publication publicationConfig,
) (*web.Application, error) {
	if !publication.authenticated {
		return webapp.NewApplication(backend)
	}
	return siteapp.New(ctx, siteapp.Config{
		Backend:  backend,
		Username: publication.username,
		Password: publication.password,
	})
}

type onceCloseListener struct {
	net.Listener
	once     sync.Once
	closeErr error
}

func (l *onceCloseListener) Close() error {
	l.once.Do(func() {
		l.closeErr = l.Listener.Close()
	})
	return l.closeErr
}

func parseServeConfig(arguments []string, stderr io.Writer) (serveConfig, error) {
	if len(arguments) == 0 || arguments[0] != "serve" {
		return serveConfig{}, errors.New("article site: expected serve subcommand")
	}
	flags := flag.NewFlagSet("article-site serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var config serveConfig
	flags.StringVar(&config.listenAddress, "listen", defaultListenAddress, "TCP listen address")
	flags.StringVar(&config.database, "database", "", "SQLite database path or DSN for direct site invocation")
	if err := flags.Parse(arguments[1:]); err != nil {
		return serveConfig{}, fmt.Errorf("article site: parse serve flags: %w", err)
	}
	if flags.NArg() != 0 {
		return serveConfig{}, errors.New("article site: serve accepts no positional arguments")
	}
	config.listenAddress = strings.TrimSpace(config.listenAddress)
	config.database = strings.TrimSpace(config.database)
	flags.Visit(func(flag *flag.Flag) {
		if flag.Name == "database" {
			config.databaseSpecified = true
		}
	})
	if config.listenAddress == "" {
		return serveConfig{}, errors.New("article site: listen address is empty")
	}
	if config.databaseSpecified && config.database == "" {
		return serveConfig{}, errors.New("article site: --database is empty")
	}
	return config, nil
}

func databaseConfigForServe(config serveConfig, lookup lookupEnvFunc) (databaseConfig, error) {
	if lookup == nil {
		return databaseConfig{}, errors.New("article site: environment lookup is nil")
	}
	sqliteDatabase, sqliteConfigured, err := requiredEnvironmentValue(lookup, articleSQLiteDatabaseEnv)
	if err != nil {
		return databaseConfig{}, err
	}
	postgresURL, postgresURLConfigured, err := requiredEnvironmentValue(lookup, articlePostgresURLEnv)
	if err != nil {
		return databaseConfig{}, err
	}
	postgresSchema, postgresSchemaConfigured, err := requiredEnvironmentValue(lookup, articlePostgresSchemaEnv)
	if err != nil {
		return databaseConfig{}, err
	}
	if sqliteConfigured && (postgresURLConfigured || postgresSchemaConfigured) {
		return databaseConfig{}, errors.New("article site: SQLite and PostgreSQL environment are mutually exclusive")
	}
	if config.databaseSpecified && (sqliteConfigured || postgresURLConfigured || postgresSchemaConfigured) {
		return databaseConfig{}, errors.New("article site: --database and database environment are mutually exclusive")
	}
	if config.databaseSpecified {
		return databaseConfig{kind: databaseKindSQLite, sqliteDatabase: config.database}, nil
	}
	if sqliteConfigured {
		return databaseConfig{kind: databaseKindSQLite, sqliteDatabase: sqliteDatabase}, nil
	}
	if postgresURLConfigured != postgresSchemaConfigured {
		if !postgresURLConfigured {
			return databaseConfig{}, fmt.Errorf("article site: %s is required with %s", articlePostgresURLEnv, articlePostgresSchemaEnv)
		}
		return databaseConfig{}, fmt.Errorf("article site: %s is required with %s", articlePostgresSchemaEnv, articlePostgresURLEnv)
	}
	if postgresURLConfigured {
		return databaseConfig{
			kind:           databaseKindPostgres,
			postgresURL:    postgresURL,
			postgresSchema: postgresSchema,
		}, nil
	}
	return databaseConfig{}, fmt.Errorf(
		"article site: configure %s or both %s and %s",
		articleSQLiteDatabaseEnv,
		articlePostgresURLEnv,
		articlePostgresSchemaEnv,
	)
}

func publicationConfigForServe(config serveConfig, lookup lookupEnvFunc) (publicationConfig, error) {
	if lookup == nil {
		return publicationConfig{}, errors.New("article site: environment lookup is nil")
	}
	username, usernameConfigured := lookup(articleAdminUsernameEnv)
	password, passwordConfigured := lookup(articleAdminPasswordEnv)
	if !usernameConfigured && !passwordConfigured {
		return publicationConfig{}, nil
	}
	if usernameConfigured != passwordConfigured {
		return publicationConfig{}, fmt.Errorf(
			"article site: %s and %s must be configured together",
			articleAdminUsernameEnv,
			articleAdminPasswordEnv,
		)
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return publicationConfig{}, fmt.Errorf("article site: %s is empty", articleAdminUsernameEnv)
	}
	if strings.TrimSpace(password) == "" {
		return publicationConfig{}, fmt.Errorf("article site: %s is empty", articleAdminPasswordEnv)
	}
	if !loopbackListenAddress(config.listenAddress) {
		return publicationConfig{}, errors.New("article site: authenticated Admin/API mode requires a loopback listen address")
	}
	return publicationConfig{
		authenticated: true,
		username:      username,
		password:      password,
	}, nil
}

func loopbackListenAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil || host == "" {
		return false
	}
	parsed := net.ParseIP(host)
	return parsed != nil && parsed.IsLoopback()
}

func requiredEnvironmentValue(lookup lookupEnvFunc, name string) (string, bool, error) {
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

func openArticleBackend(ctx context.Context, config databaseConfig) (articleBackend, error) {
	switch config.kind {
	case databaseKindSQLite:
		backend, err := sqlite.Open(ctx, config.sqliteDatabase)
		if err != nil {
			return nil, fmt.Errorf("article site: open SQLite database: %w", err)
		}
		return backend, nil
	case databaseKindPostgres:
		backend, err := postgres.Open(ctx, postgres.Config{URL: config.postgresURL, Schema: config.postgresSchema})
		if err != nil {
			return nil, fmt.Errorf("article site: open PostgreSQL database: %w", err)
		}
		return backend, nil
	default:
		return nil, errors.New("article site: database configuration is invalid")
	}
}
