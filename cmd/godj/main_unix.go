//go:build darwin || linux

package main

import (
	"context"
	"io"
	"os"
	"os/signal"

	"github.com/progresshans/godj/internal/projectcheck"
)

func main() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	interrupt := make(chan struct{})
	done := make(chan struct{})
	go func() {
		select {
		case <-signals:
			close(interrupt)
		case <-done:
		}
	}()
	exit := execute(context.Background(), "", os.Args[1:], os.Environ(), os.Stdout, os.Stderr, interrupt)
	close(done)
	signal.Stop(signals)
	os.Exit(exit)
}

func execute(ctx context.Context, cwd string, args, environment []string, stdout, stderr io.Writer, interrupt <-chan struct{}) int {
	report := projectcheck.Run(projectcheck.Invocation{
		Context:     ctx,
		CWD:         cwd,
		Args:        args,
		Environment: environment,
		Stdout:      stdout,
		Stderr:      stderr,
		Interrupt:   interrupt,
	})
	return report.ExitCode
}
