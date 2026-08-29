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
	"github.com/progresshans/godj/internal/projectcheck/linked"
	"github.com/progresshans/godj/internal/projectcheck/migrateprotocol"
	projectgeneratelinked "github.com/progresshans/godj/internal/projectgenerate/linked"
	projectgenerateprotocol "github.com/progresshans/godj/internal/projectgenerate/protocol"
	projectmigrationprotocol "github.com/progresshans/godj/internal/projectmigration/protocol"
	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
)

// MigrationBackend is the complete project-owned database resource required by
// one explicit migration invocation.
type MigrationBackend interface {
	backend.RevisionFencedBackend
	Close() error
}

// Config declares project-owned migration definition sources, a lazy backend
// opener, and the optional declaration-only whole-project generation input.
// LoadProjectSpec must not import generated app or project packages; it is
// called only for the private generation or makemigrations snapshot request.
// It must be a pure declaration snapshot and must not open a database.
// OpenMigrationBackend is called only for the private explicit-migrate request.
type Config struct {
	MigrationDefinitionRoots   []string
	MigrationDefinitionSources []definition.Source
	LoadProjectSpec            func(context.Context) (codegen.ProjectSpec, error)
	OpenMigrationBackend       func(context.Context) (MigrationBackend, error)
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
