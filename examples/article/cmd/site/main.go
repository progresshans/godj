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

	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/webapp"
	"github.com/progresshans/godj/web"
)

const defaultListenAddress = "127.0.0.1:8000"

type serveConfig struct {
	listenAddress string
	database      string
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
	return runWithListener(ctx, arguments, stdout, stderr, net.Listen)
}

type listenFunc func(network, address string) (net.Listener, error)

func runWithListener(
	ctx context.Context,
	arguments []string,
	stdout, stderr io.Writer,
	listen listenFunc,
) (resultErr error) {
	if ctx == nil {
		return errors.New("article site: nil context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	config, err := parseServeConfig(arguments, stderr)
	if err != nil {
		return err
	}
	backend, err := sqlite.Open(ctx, config.database)
	if err != nil {
		return fmt.Errorf("article site: open database: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, backend.Close())
	}()
	application, err := webapp.NewApplication(backend)
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
	flags.StringVar(&config.database, "database", "", "SQLite database path or DSN")
	if err := flags.Parse(arguments[1:]); err != nil {
		return serveConfig{}, fmt.Errorf("article site: parse serve flags: %w", err)
	}
	if flags.NArg() != 0 {
		return serveConfig{}, errors.New("article site: serve accepts no positional arguments")
	}
	config.listenAddress = strings.TrimSpace(config.listenAddress)
	config.database = strings.TrimSpace(config.database)
	if config.listenAddress == "" {
		return serveConfig{}, errors.New("article site: listen address is empty")
	}
	if config.database == "" {
		return serveConfig{}, errors.New("article site: --database is required")
	}
	return config, nil
}
