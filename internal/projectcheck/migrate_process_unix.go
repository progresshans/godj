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

	"golang.org/x/sys/unix"
)

const migrateOwnedProcessGrace = 15 * time.Second

// executeOwnedMigrateProcess owns a project child whose protocol requires
// bounded post-exit process-group cleanup. It was introduced for write-capable
// migrate; read-only migration status reuses it with the shorter general grace.
// Unlike the short check/generate owner, it observes a cooperative exit
// throughout the grace window and rechecks a queued Wait result before
// force-killing.
func executeOwnedMigrateProcess(ctx context.Context, interrupt <-chan struct{}, command Command, stdoutMaximum, stderrMaximum int, grace time.Duration) ProcessResult {
	return executeOwnedMigrateProcessWithHooks(ctx, interrupt, command, stdoutMaximum, stderrMaximum, grace, migrateOwnedProcessHooks{})
}

type migrateOwnedProcessHooks struct {
	beforeTerminalReturn func()
}

type migrateTerminalArbiter struct {
	beforeReturn func()
}

func (arbiter *migrateTerminalArbiter) complete(waitComplete, drainComplete bool, processGroup int) bool {
	if !waitComplete || !drainComplete || runserverProcessGroupExists(processGroup) {
		return false
	}
	if arbiter.beforeReturn != nil {
		beforeReturn := arbiter.beforeReturn
		arbiter.beforeReturn = nil
		beforeReturn()
	}
	return true
}

func executeOwnedMigrateProcessWithHooks(ctx context.Context, interrupt <-chan struct{}, command Command, stdoutMaximum, stderrMaximum int, grace time.Duration, hooks migrateOwnedProcessHooks) ProcessResult {
	result := ProcessResult{ExitCode: -1}
	if ctx == nil || len(command.Argv) == 0 || command.Argv[0] == "" || stdoutMaximum < 0 || stderrMaximum < 0 || grace <= 0 {
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
	terminal := migrateTerminalArbiter{beforeReturn: hooks.beforeTerminalReturn}
	for primary == nil && !waitComplete {
		waitErr, waitComplete, drainComplete = reconcileMigrateCompletion(
			child.Process,
			waited,
			drained,
			waitErr,
			waitComplete,
			drainComplete,
		)
		if terminal.complete(waitComplete, drainComplete, child.Process.Pid) || waitComplete {
			break
		}
		if primary = pendingProcessCancellation(ctx, interrupt); primary != nil {
			break
		}
		select {
		case waitErr = <-waited:
			waitComplete = true
		case <-drained:
			drainComplete = true
		case <-interrupt:
			candidate := failure("migration_project_process_error", "project_interrupted")
			primary = &candidate
		case <-ctx.Done():
			primary = pendingProcessCancellation(ctx, interrupt)
		}
		if primary != nil {
			waitErr, waitComplete, drainComplete = reconcileMigrateCompletion(
				child.Process,
				waited,
				drained,
				waitErr,
				waitComplete,
				drainComplete,
			)
			if terminal.complete(waitComplete, drainComplete, child.Process.Pid) {
				primary = nil
			}
		}
	}
	if primary != nil {
		waitErr, waitComplete, drainComplete = reconcileMigrateCompletion(
			child.Process,
			waited,
			drained,
			waitErr,
			waitComplete,
			drainComplete,
		)
		if terminal.complete(waitComplete, drainComplete, child.Process.Pid) {
			primary = nil
		}
	}

	forcedClose := false
	postExitTimedOut := false
	if primary == nil && waitComplete {
		postExitTimedOut, primary = awaitMigratePostExit(
			ctx,
			interrupt,
			drained,
			child.Process.Pid,
			&drainComplete,
			grace,
			&terminal,
		)
	}
	if primary != nil {
		if !waitComplete {
			waitErr, waitComplete = reconcileOwnedProcessWait(child.Process, waited, waitErr)
		}
		if !drainComplete {
			drainComplete = reconcileMigrateDrain(drained)
		}
		if !waitComplete || !drainComplete || runserverProcessGroupExists(child.Process.Pid) {
			result.SIGINTAttempts++
			if signalErr := signalRunserverProcessGroup(child.Process.Pid, unix.SIGINT); signalErr != nil {
				result.CleanupFailed = true
			}
			waitErr, waitComplete, drainComplete = awaitMigrateCancellationGrace(
				waited,
				drained,
				child.Process.Pid,
				waitErr,
				waitComplete,
				drainComplete,
				grace,
			)
		}
		if !waitComplete {
			// The timer and Wait publication may become ready together. Observe the
			// queued/direct-exit state once more before issuing SIGKILL.
			waitErr, waitComplete = reconcileOwnedProcessWait(child.Process, waited, waitErr)
		}
		if !drainComplete {
			drainComplete = reconcileMigrateDrain(drained)
		}
		if !waitComplete || !drainComplete || runserverProcessGroupExists(child.Process.Pid) {
			result.SIGKILLAttempts++
			if signalErr := signalRunserverProcessGroup(child.Process.Pid, unix.SIGKILL); signalErr != nil {
				result.CleanupFailed = true
			}
		}
		if !drainComplete {
			drainComplete = awaitMigrateDrain(drained, min(grace, time.Second))
		}
		if !drainComplete {
			forcedClose = true
			if closeErr := stdoutReader.Close(); closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
				result.CleanupFailed = true
			}
			if closeErr := stderrReader.Close(); closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
				result.CleanupFailed = true
			}
		}
		if !waitComplete {
			waitErr = <-waited
			waitComplete = true
		}
		if !drainComplete {
			<-drained
			drainComplete = true
		}
	} else if postExitTimedOut {
		groupExisted := runserverProcessGroupExists(child.Process.Pid)
		if groupExisted {
			result.SIGKILLAttempts++
			if signalErr := signalRunserverProcessGroup(child.Process.Pid, unix.SIGKILL); signalErr != nil {
				result.CleanupFailed = true
			}
		}
		if !drainComplete {
			drainComplete = awaitMigrateDrain(drained, min(grace, time.Second))
		}
		if !drainComplete {
			forcedClose = true
			if !groupExisted {
				result.CleanupFailed = true
			}
			if closeErr := stdoutReader.Close(); closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
				result.CleanupFailed = true
			}
			if closeErr := stderrReader.Close(); closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
				result.CleanupFailed = true
			}
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
		if drainErr != nil && !(forcedClose && errors.Is(drainErr, os.ErrClosed)) {
			result.CleanupFailed = true
		}
	}
	if result.SIGKILLAttempts != 0 && waitRunserverProcessGroupGrace(child.Process.Pid, min(grace, time.Second)) {
		result.CleanupFailed = true
	}

	result.DirectReaps = 1
	result.StdoutScalar = stdoutCapture.scalar()
	result.StderrScalar = stderrCapture.scalar()
	if primary == nil && !result.CleanupFailed {
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

func awaitMigratePostExit(
	ctx context.Context,
	interrupt <-chan struct{},
	drained <-chan struct{},
	processGroup int,
	drainComplete *bool,
	grace time.Duration,
	terminal *migrateTerminalArbiter,
) (bool, *Failure) {
	if migratePostExitComplete(drained, processGroup, drainComplete, terminal) {
		return false, nil
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	poll := time.NewTicker(min(grace, 10*time.Millisecond))
	defer poll.Stop()
	for {
		if migratePostExitComplete(drained, processGroup, drainComplete, terminal) {
			return false, nil
		}
		var drainChannel <-chan struct{}
		if !*drainComplete {
			drainChannel = drained
		}
		select {
		case <-drainChannel:
			*drainComplete = true
		case <-poll.C:
		case <-interrupt:
			if migratePostExitComplete(drained, processGroup, drainComplete, terminal) {
				return false, nil
			}
			failure := failure("migration_project_process_error", "project_interrupted")
			return false, &failure
		case <-ctx.Done():
			if migratePostExitComplete(drained, processGroup, drainComplete, terminal) {
				return false, nil
			}
			return false, pendingProcessCancellation(ctx, interrupt)
		case <-timer.C:
			if migratePostExitComplete(drained, processGroup, drainComplete, terminal) {
				return false, nil
			}
			return true, nil
		}
	}
}

func reconcileMigrateCompletion(
	process *os.Process,
	waited <-chan error,
	drained <-chan struct{},
	waitErr error,
	waitComplete bool,
	drainComplete bool,
) (error, bool, bool) {
	if !waitComplete {
		waitErr, waitComplete = reconcileOwnedProcessWait(process, waited, waitErr)
	}
	if !drainComplete {
		drainComplete = reconcileMigrateDrain(drained)
	}
	return waitErr, waitComplete, drainComplete
}

func migratePostExitComplete(drained <-chan struct{}, processGroup int, drainComplete *bool, terminal *migrateTerminalArbiter) bool {
	if !*drainComplete {
		*drainComplete = reconcileMigrateDrain(drained)
	}
	return terminal.complete(true, *drainComplete, processGroup)
}

func awaitMigrateCancellationGrace(
	waited <-chan error,
	drained <-chan struct{},
	processGroup int,
	waitErr error,
	waitComplete bool,
	drainComplete bool,
	grace time.Duration,
) (error, bool, bool) {
	timer := time.NewTimer(grace)
	defer timer.Stop()
	poll := time.NewTicker(min(grace, 10*time.Millisecond))
	defer poll.Stop()
	for {
		if waitComplete && drainComplete && !runserverProcessGroupExists(processGroup) {
			return waitErr, true, true
		}
		var waitChannel <-chan error
		if !waitComplete {
			waitChannel = waited
		}
		var drainChannel <-chan struct{}
		if !drainComplete {
			drainChannel = drained
		}
		select {
		case waitErr = <-waitChannel:
			waitComplete = true
		case <-drainChannel:
			drainComplete = true
		case <-poll.C:
		case <-timer.C:
			return waitErr, waitComplete, drainComplete
		}
	}
}

func reconcileMigrateDrain(drained <-chan struct{}) bool {
	select {
	case <-drained:
		return true
	default:
		return false
	}
}

func awaitMigrateDrain(drained <-chan struct{}, grace time.Duration) bool {
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-drained:
		return true
	case <-timer.C:
		return false
	}
}
