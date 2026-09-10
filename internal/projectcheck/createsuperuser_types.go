//go:build darwin || linux

package projectcheck

import (
	"context"
	"io"
	"os"

	"github.com/progresshans/godj/internal/projectcheck/createsuperuserprotocol"
)

// CreatesuperuserFailure is one closed, detail-free public outcome.
type CreatesuperuserFailure = createsuperuserprotocol.Failure

// CreatesuperuserReport records only bounded lifecycle observations. It never
// retains the username, password, confirmation, private request or raw child
// output.
type CreatesuperuserReport struct {
	Report
	HasCreatesuperuserResult      bool
	HasCreatesuperuserFailure     bool
	CreatesuperuserFailure        CreatesuperuserFailure
	TerminalChecks                int
	UsernamePromptWrites          int
	PasswordPromptWrites          int
	ConfirmationPromptWrites      int
	TerminalRestoreAttempts       int
	SensitiveRequestWriteAttempts int
	SensitiveRequestBytesWritten  int
	RunnerStdoutRetainedBytes     int
	RunnerStdoutTruncated         bool
	KnownCreated                  bool
}

// CreatesuperuserInvocation is snapshotted before project selection, build or
// terminal input. Stdin must be the actual terminal file; this packet has no
// pipe, environment, file or noninteractive secret fallback.
type CreatesuperuserInvocation struct {
	Context     context.Context
	CWD         string
	Args        []string
	Environment []string
	Stdin       *os.File
	Stdout      io.Writer
	Stderr      io.Writer
	Interrupt   <-chan struct{}
	Backend     Backend
	workspace   workspaceHooks
}
