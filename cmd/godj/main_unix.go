//go:build darwin || linux

package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/progresshans/godj/internal/projectcheck"
)

func main() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	interrupt := make(chan struct{})
	done := make(chan struct{})
	go func() {
		select {
		case <-signals:
			close(interrupt)
		case <-done:
		}
	}()
	exit := execute(context.Background(), "", os.Args[1:], os.Environ(), os.Stdin, os.Stdout, os.Stderr, interrupt)
	close(done)
	signal.Stop(signals)
	os.Exit(exit)
}

func execute(
	ctx context.Context,
	cwd string,
	args, environment []string,
	stdin *os.File,
	stdout, stderr io.Writer,
	interrupt <-chan struct{},
) int {
	if len(args) != 0 && args[0] == "createsuperuser" {
		// The public result is written to process fd 1 or fd 2. Keep Unix's
		// default SIGPIPE action from terminating the global owner before a
		// failed write can become the stable no-retry exit. This receiver is
		// scoped to createsuperuser and never participates in cancellation.
		brokenPipe := make(chan os.Signal, 1)
		signal.Notify(brokenPipe, syscall.SIGPIPE)
		defer signal.Stop(brokenPipe)
		report := projectcheck.RunCreatesuperuser(projectcheck.CreatesuperuserInvocation{
			Context:     ctx,
			CWD:         cwd,
			Args:        args,
			Environment: environment,
			Stdin:       stdin,
			Stdout:      stdout,
			Stderr:      stderr,
			Interrupt:   interrupt,
		})
		return report.ExitCode
	}
	if len(args) != 0 && args[0] == "runserver" {
		report := projectcheck.RunServer(projectcheck.RunServerInvocation{
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
	if len(args) != 0 && args[0] == "generate" {
		report := projectcheck.RunGenerate(projectcheck.GenerationInvocation{
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
	if len(args) != 0 && args[0] == "migrate" {
		report := projectcheck.RunMigrate(projectcheck.MigrateInvocation{
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
	if len(args) != 0 && args[0] == "makemigrations" {
		report := projectcheck.RunMakemigrations(projectcheck.MakemigrationsInvocation{
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
	if len(args) != 0 && args[0] == "showmigrations" {
		report := projectcheck.RunShowMigrations(projectcheck.ShowMigrationsInvocation{
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
	if len(args) != 0 && args[0] == "sqlmigrate" {
		report := projectcheck.RunSQLMigrate(projectcheck.SQLMigrateInvocation{
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
