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

	"github.com/progresshans/godj/examples/article/databaseconfig"
	"github.com/progresshans/godj/examples/article/internal/siteapp"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/settings/database"
	"github.com/progresshans/godj/web"
)

const (
	defaultListenAddress     = "127.0.0.1:8000"
	articleSQLiteDatabaseEnv = databaseconfig.SQLiteDatabaseEnv
	articlePostgresURLEnv    = databaseconfig.PostgresURLEnv
	articlePostgresSchemaEnv = databaseconfig.PostgresSchemaEnv
)

type serveConfig struct {
	listenAddress     string
	database          string
	databaseSpecified bool
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "article site failed:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string, stdout, stderr io.Writer) error {
	return runWithListener(ctx, arguments, stdout, stderr, net.Listen, database.Open)
}

type listenFunc func(network, address string) (net.Listener, error)
type backendOpener func(context.Context, database.Config) (database.Backend, error)

func runWithListener(
	ctx context.Context,
	arguments []string,
	stdout, stderr io.Writer,
	listen listenFunc,
	openBackend backendOpener,
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
	environment, err := settings.NewEnvironment(os.Environ())
	if err != nil {
		return err
	}
	selected, err := databaseconfig.ForServe(config.database, config.databaseSpecified, environment.Lookup)
	if err != nil {
		return err
	}
	backend, err := openBackend(ctx, selected)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, backend.Close())
	}()
	application, err := applicationForServe(ctx, backend, loopbackListenAddress(config.listenAddress))
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
	backend database.Backend,
	allowLoopbackAuthentication bool,
) (*web.Application, error) {
	config := siteapp.NewConfig(backend)
	if allowLoopbackAuthentication {
		config = config.WithLoopbackAuthentication()
	}
	return siteapp.New(ctx, config)
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

func loopbackListenAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil || host == "" {
		return false
	}
	parsed := net.ParseIP(host)
	return parsed != nil && parsed.IsLoopback()
}
