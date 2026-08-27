//go:build darwin || linux

package projectcheck

import (
	"context"
	"io"

	"github.com/progresshans/godj/internal/projectcheck/migrateprotocol"
)

// MigrateFailure and MigrateResult are the closed migrate-protocol values. The
// global owner adds no database-specific fields or raw causes.
type MigrateFailure = migrateprotocol.Failure
type MigrateResult = migrateprotocol.Result

// MigrateReport combines the migrate outcome with existing bounded process,
// selection, and cleanup observations.
type MigrateReport struct {
	Report
	HasMigrateResult  bool
	MigrateResult     MigrateResult
	HasMigrateFailure bool
	MigrateFailure    MigrateFailure
}

// MigrateInvocation is snapshotted before project selection or child launch.
type MigrateInvocation struct {
	Context     context.Context
	CWD         string
	Args        []string
	Environment []string
	Stdout      io.Writer
	Stderr      io.Writer
	Interrupt   <-chan struct{}
	Backend     Backend
	workspace   workspaceHooks
}
