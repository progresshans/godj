//go:build darwin || linux

// Package project provides the minimal project-linked GoDj runtime entrypoint.
package project

import (
	"context"
	"io"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/projectcheck/linked"
	projectgeneratelinked "github.com/progresshans/godj/internal/projectgenerate/linked"
	projectgenerateprotocol "github.com/progresshans/godj/internal/projectgenerate/protocol"
)

// Config declares project-owned migration definition roots and the optional
// declaration-only whole-project generation input. LoadProjectSpec must not
// import generated app or project packages; it is called only for the private
// generation request.
type Config struct {
	MigrationDefinitionRoots []string
	LoadProjectSpec          func(context.Context) (codegen.ProjectSpec, error)
}

// Run serves the private project-runner protocol for one invocation.
func Run(
	ctx context.Context,
	config Config,
	argv []string,
	stdin io.Reader,
	stdout io.Writer,
) error {
	roots := append([]string(nil), config.MigrationDefinitionRoots...)
	arguments := append([]string(nil), argv...)
	if len(arguments) == 1 && arguments[0] == projectgenerateprotocol.PrivateArgument {
		_, err := projectgeneratelinked.Run(
			ctx,
			projectgeneratelinked.Loader(config.LoadProjectSpec),
			arguments,
			stdin,
			stdout,
		)
		return err
	}

	_, err := linked.Run(
		ctx,
		linked.Config{MigrationDefinitionRoots: roots},
		arguments,
		stdin,
		stdout,
	)
	return err
}
