//go:build darwin || linux

package projectcheck

import (
	"context"
	"io"

	"github.com/progresshans/godj/internal/projectcheck/migrateprotocol"
)

// MigrateFailure and MigrateResult are the closed public execute values. The
// global owner adds no database-specific fields or raw causes. A read-only
// plan is represented separately so existing execute reports keep their
// source/definition/digest shape.
type MigrateFailure = migrateprotocol.Failure
type MigrateResult = migrateprotocol.ExecuteResult
type MigratePlanRow = migrateprotocol.PlanRow

// MigrateReport combines the migrate outcome with existing bounded process,
// selection, and cleanup observations.
type MigrateReport struct {
	Report
	HasMigrateResult  bool
	MigrateResult     MigrateResult
	HasMigratePlan    bool
	MigratePlan       []MigratePlanRow
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
