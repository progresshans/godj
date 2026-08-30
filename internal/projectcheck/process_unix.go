//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/progresshans/godj/internal/projectcheck/migrateprotocol"
	"github.com/progresshans/godj/internal/projectcheck/protocol"
	"github.com/progresshans/godj/internal/projectcheck/showmigrationsprotocol"
	"github.com/progresshans/godj/internal/projectcheck/sqlmigrateprotocol"
	projectgenerateprotocol "github.com/progresshans/godj/internal/projectgenerate/protocol"
	projectmigrationprotocol "github.com/progresshans/godj/internal/projectmigration/protocol"
	"golang.org/x/sys/unix"
)

const ownedProcessGrace = 2 * time.Second

type processBackend struct{}

type ownedResponseProcessPolicy struct {
	stdoutMaximum int
	stderrMaximum int
	grace         time.Duration
}

func ownedResponseProcessPolicyForStage(stage ProcessStage) (ownedResponseProcessPolicy, bool) {
	switch stage {
	case MigrateRunnerStage:
		return ownedResponseProcessPolicy{
			stdoutMaximum: migrateprotocol.MaxResponseBytes,
			stderrMaximum: maxDiagnosticBytes,
			grace:         migrateOwnedProcessGrace,
		}, true
	case ShowMigrationsRunnerStage:
		return ownedResponseProcessPolicy{
			stdoutMaximum: showmigrationsprotocol.MaxResponseBytes,
			stderrMaximum: maxDiagnosticBytes,
			grace:         ownedProcessGrace,
		}, true
	case SQLMigrateRunnerStage:
		return ownedResponseProcessPolicy{
			stdoutMaximum: sqlmigrateprotocol.MaxResponseBytes,
			stderrMaximum: maxDiagnosticBytes,
			grace:         ownedProcessGrace,
		}, true
	default:
		return ownedResponseProcessPolicy{}, false
	}
}

func (processBackend) Execute(ctx context.Context, interrupt <-chan struct{}, stage ProcessStage, command Command) ProcessResult {
	if policy, ok := ownedResponseProcessPolicyForStage(stage); ok {
		// Migrate and the read-only status child each own a strict response pipe.
		// Reuse the bounded post-exit process-group owner so a descendant cannot
		// retain that pipe indefinitely after the direct child has exited.
		return executeOwnedMigrateProcess(
			ctx,
			interrupt,
			cloneCommand(command),
			policy.stdoutMaximum,
			policy.stderrMaximum,
			policy.grace,
		)
	}
	stdoutMaximum := maxDiagnosticBytes
	retainStdout := false
	if stage == RunnerStage {
		stdoutMaximum = protocol.MaxResponseBytes
		retainStdout = true
	} else if stage == GenerationRunnerStage {
		stdoutMaximum = projectgenerateprotocol.MaxResponseBytes
		retainStdout = true
	} else if stage == MakemigrationsInventoryStage {
		stdoutMaximum = int(defaultMakemigrationsBuildInputLimits().goListBytes)
		retainStdout = true
	} else if stage == MakemigrationsRunnerStage {
		stdoutMaximum = projectmigrationprotocol.MaxResponseBytes
		retainStdout = true
	}
	return executeOwnedProcess(ctx, interrupt, cloneCommand(command), stdoutMaximum, maxDiagnosticBytes, retainStdout)
}

type cappedDrain struct {
	maximum   int
	prefix    []byte
	truncated bool
}

func (capture *cappedDrain) Write(payload []byte) (int, error) {
	written := len(payload)
	if written == 0 {
		return 0, nil
	}
	remaining := capture.maximum - len(capture.prefix)
	retained := 0
	if remaining > 0 {
		if remaining > written {
			remaining = written
		}
		capture.prefix = append(capture.prefix, payload[:remaining]...)
		retained = remaining
	}
	if retained < written {
		capture.truncated = true
	}
	return written, nil
}

func (capture *cappedDrain) scalar() StreamScalar {
	return StreamScalar{RetainedBytes: len(capture.prefix), Truncated: capture.truncated}
}

func (capture *cappedDrain) take() []byte {
	result := append([]byte(nil), capture.prefix...)
	clear(capture.prefix)
	capture.prefix = nil
	return result
}

func (capture *cappedDrain) discard() {
	clear(capture.prefix)
	capture.prefix = nil
}

func executeOwnedProcess(ctx context.Context, interrupt <-chan struct{}, command Command, stdoutMaximum, stderrMaximum int, retainStdout bool) ProcessResult {
	result := ProcessResult{ExitCode: -1}
	if ctx == nil || len(command.Argv) == 0 || command.Argv[0] == "" || stdoutMaximum < 0 || stderrMaximum < 0 {
		return result
	}
	if primary := pendingProcessCancellation(ctx, interrupt); primary != nil {
		result.Failure = primary
		return result
	}
	child := exec.Command(command.Argv[0], command.Argv[1:]...)
	child.Dir = command.Dir
	child.Env = append([]string(nil), command.Env...)
	child.Stdin = bytes.NewReader(command.Stdin)
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return result
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
		return result
	}
	child.Stdout = stdoutWriter
	child.Stderr = stderrWriter
	if primary := pendingProcessCancellation(ctx, interrupt); primary != nil {
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
		_ = stderrReader.Close()
		_ = stderrWriter.Close()
		result.Failure = primary
		return result
	}
	if err := child.Start(); err != nil {
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
		_ = stderrReader.Close()
		_ = stderrWriter.Close()
		return result
	}
	result.Started = true
	_ = stdoutWriter.Close()
	_ = stderrWriter.Close()

	stdoutCapture := &cappedDrain{maximum: stdoutMaximum, prefix: make([]byte, 0, min(stdoutMaximum, 32<<10))}
	stderrCapture := &cappedDrain{maximum: stderrMaximum, prefix: make([]byte, 0, min(stderrMaximum, 32<<10))}
	drainErrors := make(chan error, 2)
	var drainers sync.WaitGroup
	drainers.Add(2)
	go func() {
		defer drainers.Done()
		_, copyErr := io.Copy(stdoutCapture, stdoutReader)
		drainErrors <- copyErr
	}()
	go func() {
		defer drainers.Done()
		_, copyErr := io.Copy(stderrCapture, stderrReader)
		drainErrors <- copyErr
	}()
	drained := make(chan struct{})
	go func() {
		drainers.Wait()
		close(drained)
	}()
	waited := make(chan error, 1)
	go func() { waited <- child.Wait() }()

	var waitErr error
	waitComplete := false
	drainComplete := false
	var primary *Failure
	for primary == nil && !(waitComplete && drainComplete) {
		if primary = pendingProcessCancellation(ctx, interrupt); primary != nil {
			break
		}
		select {
		case waitErr = <-waited:
			waitComplete = true
			primary = pendingProcessCancellation(ctx, interrupt)
		case <-drained:
			drainComplete = true
			primary = pendingProcessCancellation(ctx, interrupt)
		case <-interrupt:
			candidate := failure(protocol.CategoryProcess, protocol.CodeProjectInterrupted)
			primary = &candidate
		case <-ctx.Done():
			primary = pendingProcessCancellation(ctx, interrupt)
		}
	}

	forcedClose := false
	if primary != nil {
		if !waitComplete {
			waitErr, waitComplete = reconcileOwnedProcessWait(child.Process, waited, waitErr)
		}
		if !waitComplete {
			result.SIGINTAttempts++
			if signalErr := unix.Kill(-child.Process.Pid, unix.SIGINT); signalErr != nil && !errors.Is(signalErr, syscall.ESRCH) {
				result.CleanupFailed = true
			}
			timer := time.NewTimer(ownedProcessGrace)
			<-timer.C
			result.SIGKILLAttempts++
			if signalErr := unix.Kill(-child.Process.Pid, unix.SIGKILL); signalErr != nil && !errors.Is(signalErr, syscall.ESRCH) {
				result.CleanupFailed = true
			}
		}
		if closeErr := stdoutReader.Close(); closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
			result.CleanupFailed = true
		}
		if closeErr := stderrReader.Close(); closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
			result.CleanupFailed = true
		}
		forcedClose = true
		if !waitComplete {
			waitErr = <-waited
			waitComplete = true
		}
		if !drainComplete {
			<-drained
			drainComplete = true
		}
	}
	if !drainComplete {
		<-drained
		drainComplete = true
	}
	if !waitComplete {
		waitErr = <-waited
		waitComplete = true
	}
	if !forcedClose {
		if closeErr := stdoutReader.Close(); closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
			result.CleanupFailed = true
		}
		if closeErr := stderrReader.Close(); closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
			result.CleanupFailed = true
		}
	}
	for count := 0; count < 2; count++ {
		drainErr := <-drainErrors
		if drainErr != nil && !(primary != nil && errors.Is(drainErr, os.ErrClosed)) {
			result.CleanupFailed = true
		}
	}

	result.DirectReaps = 1
	result.StdoutScalar = stdoutCapture.scalar()
	result.StderrScalar = stderrCapture.scalar()
	if retainStdout && primary == nil && !result.CleanupFailed {
		result.Stdout = stdoutCapture.take()
	} else {
		stdoutCapture.discard()
	}
	stderrCapture.discard()
	if primary != nil {
		result.Failure = primary
	}
	if waitErr == nil {
		result.ExitCode = 0
	} else {
		var exitError *exec.ExitError
		if errors.As(waitErr, &exitError) {
			result.ExitCode = exitError.ExitCode()
		} else {
			result.ExitCode = 1
		}
	}
	return result
}

func reconcileQueuedWait(waited <-chan error, current error) (error, bool) {
	select {
	case waitErr := <-waited:
		return waitErr, true
	default:
		return current, false
	}
}

func reconcileOwnedProcessWait(process *os.Process, waited <-chan error, current error) (error, bool) {
	if waitErr, complete := reconcileQueuedWait(waited, current); complete {
		return waitErr, true
	}
	if process != nil && errors.Is(process.Signal(syscall.Signal(0)), os.ErrProcessDone) {
		return <-waited, true
	}
	return current, false
}

func pendingProcessCancellation(ctx context.Context, interrupt <-chan struct{}) *Failure {
	if interrupt != nil {
		select {
		case <-interrupt:
			primary := failure(protocol.CategoryProcess, protocol.CodeProjectInterrupted)
			return &primary
		default:
		}
	}
	if ctx != nil && ctx.Err() != nil {
		primary := failure(protocol.CategoryProcess, protocol.CodeProjectCanceled)
		return &primary
	}
	return nil
}
