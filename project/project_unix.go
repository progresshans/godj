//go:build darwin || linux

// Package project provides the minimal project-linked GoDj runtime entrypoint.
package project

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/projectcheck/createsuperuserprotocol"
	"github.com/progresshans/godj/internal/projectcheck/linked"
	"github.com/progresshans/godj/internal/projectcheck/migrateprotocol"
	"github.com/progresshans/godj/internal/projectcheck/showmigrationsprotocol"
	"github.com/progresshans/godj/internal/projectcheck/sqlmigrateprotocol"
	projectgeneratelinked "github.com/progresshans/godj/internal/projectgenerate/linked"
	projectgenerateprotocol "github.com/progresshans/godj/internal/projectgenerate/protocol"
	projectmigrationprotocol "github.com/progresshans/godj/internal/projectmigration/protocol"
	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/systemstate"
)

// MigrationBackend is the complete project-owned database resource required by
// one explicit migration invocation.
type MigrationBackend interface {
	backend.RevisionFencedBackend
	Close() error
}

// SystemStateBackend is the project-owned durable resource used by one
// explicit operator-provisioning invocation. It is deliberately separate from
// MigrationBackend: the global command must not infer that both lifecycles use
// the same database handle or opener.
type SystemStateBackend interface {
	systemstate.Backend
	Close() error
}

// Config declares project-owned migration definition sources, a lazy backend
// opener, an immutable database-free migration SQL renderer, an independent
// system-state opener and immutable operator policy, and the optional
// declaration-only whole-project generation input.
// LoadProjectSpec must not import generated app or project packages; it is
// called only for the private generation or makemigrations snapshot request.
// It must be a pure declaration snapshot and must not open a database.
// OpenMigrationBackend is called only for private database-backed migration
// commands. Read-only status uses the same revision-fenced session boundary as
// migration execution without beginning a migration transaction.
// MigrationSQLRenderer is called only by the private database-free SQL
// projection command after complete definition loading and exact materialization;
// it must be immutable and must not retain credentials or a database handle.
// OpenSystemStateBackend and SystemOperatorPolicy are consumed only by the
// private createsuperuser command. The opener owns one closeable invocation
// resource; the policy contains no raw username or password.
type Config struct {
	MigrationDefinitionRoots   []string
	MigrationDefinitionSources []definition.Source
	LoadProjectSpec            func(context.Context) (codegen.ProjectSpec, error)
	OpenMigrationBackend       func(context.Context) (MigrationBackend, error)
	MigrationSQLRenderer       backend.MigrationSQLRenderer
	OpenSystemStateBackend     func(context.Context) (SystemStateBackend, error)
	SystemOperatorPolicy       systemstate.CredentialPolicy
}

// projectRunnerError is a closed, cause-free process outcome used only by the
// canonical project-runner main. Keeping the type private prevents callers from
// fabricating a known-created child exit through the public API.
type projectRunnerError struct {
	code int
}

func (failure projectRunnerError) Error() string {
	return "project: private runner failed"
}

// RunnerExitCode returns the process exit that a canonical project-runner main
// must use for Run's result. Most failures use exit 1. A createsuperuser insert
// whose private response could not be published uses one of two reserved
// child-only exits so the owning global command can preserve known-created,
// backend-cleanup precedence, and no-retry semantics. Call this on Run's direct
// result before wrapping it.
func RunnerExitCode(err error) int {
	if err == nil {
		return 0
	}
	if failure, ok := err.(projectRunnerError); ok {
		switch failure.code {
		case createsuperuserprotocol.KnownCreatedResponseFailureExitCode,
			createsuperuserprotocol.KnownCreatedBackendCleanupResponseFailureExitCode:
			return failure.code
		}
	}
	return 1
}

// Run serves the private project-runner protocol for one invocation.
func Run(
	ctx context.Context,
	config Config,
	argv []string,
	stdin io.Reader,
	stdout io.Writer,
) error {
	return run(ctx, config, argv, stdin, stdout, migrateSignalContext)
}

type signalContextOwner func(context.Context) (context.Context, context.CancelFunc)

func run(
	ctx context.Context,
	config Config,
	argv []string,
	stdin io.Reader,
	stdout io.Writer,
	ownSignalContext signalContextOwner,
) error {
	arguments := append([]string(nil), argv...)
	loadProjectSpec := config.LoadProjectSpec
	openMigrationBackend := config.OpenMigrationBackend
	migrationSQLRenderer := config.MigrationSQLRenderer
	openSystemStateBackend := config.OpenSystemStateBackend
	operatorPolicy := config.SystemOperatorPolicy
	if len(arguments) == 1 && arguments[0] == projectmigrationprotocol.PrivateArgument {
		makemigrationsConfig := linked.SnapshotMakemigrationsConfig(linked.MakemigrationsConfig{
			MigrationDefinitionRoots:   config.MigrationDefinitionRoots,
			MigrationDefinitionSources: config.MigrationDefinitionSources,
			LoadProjectSpec:            linked.ProjectSpecLoader(loadProjectSpec),
		})
		_, err := linked.RunSnapshottedMakemigrations(
			ctx,
			makemigrationsConfig,
			arguments,
			stdin,
			stdout,
		)
		return err
	}
	roots := append([]string(nil), config.MigrationDefinitionRoots...)
	sources := cloneDefinitionSources(config.MigrationDefinitionSources)
	if len(arguments) == 1 && arguments[0] == projectgenerateprotocol.PrivateArgument {
		_, err := projectgeneratelinked.Run(
			ctx,
			projectgeneratelinked.Loader(loadProjectSpec),
			arguments,
			stdin,
			stdout,
		)
		return err
	}
	if len(arguments) == 1 && arguments[0] == createsuperuserprotocol.PrivateArgument {
		if ctx == nil {
			return errors.New("project: nil context")
		}
		if ownSignalContext == nil {
			return errors.New("project: nil createsuperuser signal owner")
		}
		operatorContext, stop := ownSignalContext(ctx)
		if operatorContext == nil || stop == nil {
			return errors.New("project: invalid createsuperuser signal owner")
		}
		defer stop()
		// A private response may target fd 1. Without an explicit SIGPIPE
		// receiver, Unix terminates a Go command writing to a broken stdout
		// before Write can return EPIPE and before the known-created report can
		// cross the project-main exit boundary. Scope the override to this one
		// private invocation and never turn it into context cancellation.
		brokenPipe := make(chan os.Signal, 1)
		signal.Notify(brokenPipe, syscall.SIGPIPE)
		defer signal.Stop(brokenPipe)

		var opener func(context.Context) (linked.SystemStateBackend, error)
		if openSystemStateBackend != nil {
			opener = func(openContext context.Context) (linked.SystemStateBackend, error) {
				return openSystemStateBackend(openContext)
			}
		}
		report, err := linked.RunCreatesuperuser(
			operatorContext,
			linked.CreatesuperuserConfig{
				OpenSystemStateBackend: opener,
				CredentialPolicy:       operatorPolicy,
			},
			arguments,
			stdin,
			stdout,
		)
		return createsuperuserRunError(report, err)
	}
	if len(arguments) == 1 && arguments[0] == migrateprotocol.PrivateArgument {
		if ctx == nil {
			return errors.New("project: nil context")
		}
		if ownSignalContext == nil {
			return errors.New("project: nil migrate signal owner")
		}
		migrationContext, stop := ownSignalContext(ctx)
		if migrationContext == nil || stop == nil {
			return errors.New("project: invalid migrate signal owner")
		}
		defer stop()

		var opener func(context.Context) (linked.MigrationBackend, error)
		if openMigrationBackend != nil {
			opener = func(openContext context.Context) (linked.MigrationBackend, error) {
				return openMigrationBackend(openContext)
			}
		}
		_, err := linked.RunMigrate(
			migrationContext,
			linked.MigrateConfig{
				MigrationDefinitionRoots:   roots,
				MigrationDefinitionSources: sources,
				OpenMigrationBackend:       opener,
			},
			arguments,
			stdin,
			stdout,
		)
		return err
	}
	if len(arguments) == 1 && arguments[0] == showmigrationsprotocol.PrivateArgument {
		if ctx == nil {
			return errors.New("project: nil context")
		}
		if ownSignalContext == nil {
			return errors.New("project: nil migration status signal owner")
		}
		statusContext, stop := ownSignalContext(ctx)
		if statusContext == nil || stop == nil {
			return errors.New("project: invalid migration status signal owner")
		}
		defer stop()

		var opener func(context.Context) (linked.MigrationBackend, error)
		if openMigrationBackend != nil {
			opener = func(openContext context.Context) (linked.MigrationBackend, error) {
				return openMigrationBackend(openContext)
			}
		}
		_, err := linked.RunShowMigrations(
			statusContext,
			linked.ShowMigrationsConfig{
				MigrationDefinitionRoots:   roots,
				MigrationDefinitionSources: sources,
				OpenMigrationBackend:       opener,
			},
			arguments,
			stdin,
			stdout,
		)
		return err
	}
	if len(arguments) == 1 && arguments[0] == sqlmigrateprotocol.PrivateArgument {
		if ctx == nil {
			return errors.New("project: nil context")
		}
		if ownSignalContext == nil {
			return errors.New("project: nil sqlmigrate signal owner")
		}
		sqlContext, stop := ownSignalContext(ctx)
		if sqlContext == nil || stop == nil {
			return errors.New("project: invalid sqlmigrate signal owner")
		}
		defer stop()

		_, err := linked.RunSQLMigrate(
			sqlContext,
			linked.SQLMigrateConfig{
				MigrationDefinitionRoots:   roots,
				MigrationDefinitionSources: sources,
				MigrationSQLRenderer:       migrationSQLRenderer,
			},
			arguments,
			stdin,
			stdout,
		)
		return err
	}

	_, err := linked.Run(
		ctx,
		linked.Config{
			MigrationDefinitionRoots:   roots,
			MigrationDefinitionSources: sources,
		},
		arguments,
		stdin,
		stdout,
	)
	return err
}

func createsuperuserRunError(report linked.CreatesuperuserReport, err error) error {
	if err == nil || !report.KnownCreated {
		return err
	}
	// Do not retain the output error or its writer cause. The only facts
	// crossing the project-main boundary are the known durable insert and
	// whether backend cleanup had already failed before response publication.
	code := createsuperuserprotocol.KnownCreatedResponseFailureExitCode
	if report.BackendCleanupFailures != 0 {
		code = createsuperuserprotocol.KnownCreatedBackendCleanupResponseFailureExitCode
	}
	return projectRunnerError{code: code}
}

func migrateSignalContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}

func cloneDefinitionSources(sources []definition.Source) []definition.Source {
	if len(sources) == 0 {
		return nil
	}
	cloned := make([]definition.Source, len(sources))
	for index := range sources {
		cloned[index] = definition.Source{
			SourceID: strings.Clone(sources[index].SourceID),
			Document: append([]byte(nil), sources[index].Document...),
		}
	}
	return cloned
}
