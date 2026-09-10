//go:build darwin || linux

package projectcheck

import (
	"context"
	"io"

	"github.com/progresshans/godj/internal/projectcheck/showmigrationsprotocol"
)

// ShowMigrationsFailure and ShowMigrationsResult are the closed read-only
// status protocol values. The global owner adds no backend-specific data or
// raw causes.
type ShowMigrationsFailure = showmigrationsprotocol.Failure
type ShowMigrationsResult = showmigrationsprotocol.Result

// ShowMigrationsReport combines the status outcome with bounded process,
// selection, publication, and cleanup observations.
type ShowMigrationsReport struct {
	Report
	HasShowMigrationsResult  bool
	ShowMigrationsResult     ShowMigrationsResult
	HasShowMigrationsFailure bool
	ShowMigrationsFailure    ShowMigrationsFailure
}

// ShowMigrationsInvocation is snapshotted before project selection or child
// launch.
type ShowMigrationsInvocation struct {
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
