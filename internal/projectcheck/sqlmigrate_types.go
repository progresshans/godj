//go:build darwin || linux

package projectcheck

import (
	"context"
	"io"

	"github.com/progresshans/godj/internal/projectcheck/sqlmigrateprotocol"
)

type SQLMigrateFailure = sqlmigrateprotocol.Failure
type SQLMigrateResult = sqlmigrateprotocol.Result

// SQLMigrateReport combines the closed SQL outcome with bounded selection,
// process, cleanup and final-publication observations.
type SQLMigrateReport struct {
	Report
	HasSQLMigrateResult       bool
	SQLMigrateResult          SQLMigrateResult
	HasSQLMigrateFailure      bool
	SQLMigrateFailure         SQLMigrateFailure
	RunnerStdoutRetainedBytes int
	RunnerStdoutTruncated     bool
}

// SQLMigrateInvocation is snapshotted before any project or process I/O.
type SQLMigrateInvocation struct {
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
