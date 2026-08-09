//go:build darwin || linux

// Package project provides the minimal project-linked GoDj runtime entrypoint.
package project

import (
	"context"
	"io"

	"github.com/progresshans/godj/internal/projectcheck/linked"
)

// Config declares project-owned migration definition roots.
type Config struct {
	MigrationDefinitionRoots []string
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

	_, err := linked.Run(
		ctx,
		linked.Config{MigrationDefinitionRoots: roots},
		arguments,
		stdin,
		stdout,
	)
	return err
}
