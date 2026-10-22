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
	"syscall"

	"github.com/progresshans/godj/examples/article/databaseconfig"
	"github.com/progresshans/godj/examples/article/internal/siteapp"
	projectserver "github.com/progresshans/godj/project/server"
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
	var listener net.ListenConfig
	return runWithListener(ctx, arguments, stdout, stderr, listener.Listen, database.Open)
}

type listenFunc func(context.Context, string, string) (net.Listener, error)
type backendOpener func(context.Context, database.Config) (database.Backend, error)

func runWithListener(
	ctx context.Context,
	arguments []string,
	stdout, stderr io.Writer,
	listen listenFunc,
	openBackend backendOpener,
) error {
	if ctx == nil {
		return errors.New("article site: nil context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if openBackend == nil || listen == nil {
		return errors.New("article site: nil resource opener")
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
	return projectserver.Run(ctx, projectserver.Config{
		Start: func(startContext context.Context) (projectserver.Runtime, error) {
			backend, err := openBackend(startContext, selected)
			var runtime projectserver.Runtime
			if backend != nil {
				runtime.Stop = func(context.Context) error { return backend.Close() }
			}
			if err != nil {
				return runtime, err
			}
			// Until this callback returns, it owns the opened backend, including
			// cleanup if application construction panics or calls Goexit.
			returned := false
			defer func() {
				if !returned && runtime.Stop != nil {
					_ = runtime.Stop(context.WithoutCancel(startContext))
				}
			}()
			runtime.Application, err = applicationForServe(startContext, backend, loopbackListenAddress(config.listenAddress))
			returned = true
			return runtime, err
		},
		Listen: func(listenContext context.Context) (net.Listener, error) {
			return listen(listenContext, "tcp", config.listenAddress)
		},
		Ready: func(_ context.Context, info projectserver.ListenerInfo) error {
			document := "article site listening on http://" + info.Address + "\n"
			count, err := io.WriteString(stdout, document)
			if err == nil && count != len(document) {
				return io.ErrShortWrite
			}
			return err
		},
	})
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
