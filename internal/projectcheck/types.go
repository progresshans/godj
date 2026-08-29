//go:build darwin || linux

package projectcheck

import (
	"context"
	"io"

	"github.com/progresshans/godj/internal/projectcheck/protocol"
)

const (
	maxDescriptorBytes = 64 << 10
	maxAncestors       = 128
	maxResponseBytes   = 64 << 10
	maxDiagnosticBytes = 1 << 20
)

// Failure and Result reuse the protocol leaf's closed vocabulary and payload.
// Public exit remains an observation on Report and is always derived through
// protocol.ExitCode.
type Failure = protocol.Failure
type Result = protocol.Result

// Report contains observations made by the global kernel at actual callsites.
// Linked loader and discovery observations live in linked.Report and are
// combined only by the conformance adapter.
type Report struct {
	BuildCalls                   int
	RunnerCalls                  int
	AncestorDirectoriesInspected int
	DescriptorReads              int
	UserStdoutWrites             int
	UserStderrWrites             int
	PartialStdoutWrites          int
	RunnerResponseWrites         int
	ExitCode                     int
	HasResult                    bool
	Result                       Result
	HasFailure                   bool
	Failure                      Failure
	TempCreated                  int
	TempCleanupAttempts          int
	CleanupFailed                int
	ResidualTemp                 int
	DirectChildReaps             int
	GroupSIGINTAttempts          int
	GroupSIGKILLAttempts         int
	BuildStdoutRetainedBytes     int
	BuildStdoutTruncated         bool
	BuildStderrRetainedBytes     int
	BuildStderrTruncated         bool
	RunnerStderrRetainedBytes    int
	RunnerStderrTruncated        bool
	RawDiagnosticsDiscarded      bool
}

// Invocation is the invocation-local input to the global product kernel.
// Args and Environment are snapshotted by Run before any I/O.
type Invocation struct {
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

// ProcessStage identifies the child programs the global kernel owns. Runner
// stages intentionally retain their own private response bounds and lifetime.
type ProcessStage uint8

const (
	BuildStage ProcessStage = iota + 1
	RunnerStage
	GenerationRunnerStage
	MigrateRunnerStage
	MakemigrationsInventoryStage
	MakemigrationsRunnerStage
)

// Command is a fully separated argv invocation. It is never interpreted by a
// shell. Slices are owned by the caller and cloned before reaching a backend.
type Command struct {
	Dir   string
	Argv  []string
	Env   []string
	Stdin []byte
}

// StreamScalar records only the bounded diagnostic shape; raw diagnostics are
// discarded before public output.
type StreamScalar struct {
	RetainedBytes int
	Truncated     bool
}

// ProcessResult is returned by a process backend after it has synchronously
// reaped its direct child and joined its output drainers.
type ProcessResult struct {
	Stdout          []byte
	ExitCode        int
	Started         bool
	DirectReaps     int
	StdoutScalar    StreamScalar
	StderrScalar    StreamScalar
	Failure         *Failure
	CleanupFailed   bool
	SIGINTAttempts  int
	SIGKILLAttempts int
}

// Backend is the sole seam between global orchestration and either owned OS
// children or an in-process conformance adapter. Global and linked packages do
// not import one another.
type Backend interface {
	Execute(context.Context, <-chan struct{}, ProcessStage, Command) ProcessResult
}

func cloneCommand(command Command) Command {
	return Command{
		Dir:   command.Dir,
		Argv:  append([]string(nil), command.Argv...),
		Env:   append([]string(nil), command.Env...),
		Stdin: append([]byte(nil), command.Stdin...),
	}
}

func failure(category, code string) Failure {
	candidate := protocol.Failure{Category: category, Code: code}
	if _, ok := protocol.ExitCode(candidate); !ok {
		return protocol.Failure{Category: protocol.CategoryInternal, Code: protocol.CodeProjectInternalError}
	}
	return candidate
}
