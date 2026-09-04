//go:build darwin || linux

package projectcheck

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// runserverProcessResult separates process observations from the public
// runserver failure vocabulary. The orchestration layer owns that mapping.
// Raw errors are internal diagnostics and must not be published directly.
type runserverProcessResult struct {
	Started         bool
	ExitCode        int
	DirectReaps     int
	SIGINTAttempts  int
	SIGKILLAttempts int
	Interrupted     bool
	Canceled        bool
	Forced          bool
	CleanupFailed   bool
	CleanupError    error
	StartError      error
	WaitError       error
	StdoutError     error
	StderrError     error
}

type runserverProcessWait struct {
	state *os.ProcessState
	err   error
}

type runserverProcessStream uint8

const (
	runserverProcessStdout runserverProcessStream = iota + 1
	runserverProcessStderr
)

type runserverProcessDrain struct {
	stream      runserverProcessStream
	deliveryErr error
	readErr     error
}

type runserverProcessStop uint8

const (
	runserverProcessRunning runserverProcessStop = iota
	runserverProcessInterrupted
	runserverProcessCanceled
	runserverProcessStreamFailed
)

// executeRunserverProcess owns one long-lived runtime process. It never uses a
// shell, streams both output channels while the child is alive, and reaps the
// direct child exactly once. stdout and stderr must be prompt, non-blocking
// writers; this is true for the CLI's files and its bounded test writers.
func executeRunserverProcess(ctx context.Context, interrupt <-chan struct{}, command Command, stdout, stderr io.Writer, grace time.Duration) runserverProcessResult {
	result := runserverProcessResult{ExitCode: -1}
	if ctx == nil {
		ctx = context.Background()
	}
	if validationErr := validateRunserverProcess(command, stdout, stderr, grace); validationErr != nil {
		result.StartError = validationErr
		return result
	}
	if stop := pendingRunserverProcessStop(ctx, interrupt); stop != runserverProcessRunning {
		setRunserverProcessStop(&result, stop)
		return result
	}

	command = cloneCommand(command)
	child := exec.Command(command.Argv[0], command.Argv[1:]...)
	child.Dir = command.Dir
	child.Env = append([]string(nil), command.Env...)
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		result.StartError = err
		return result
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
		result.StartError = err
		return result
	}
	closeBeforeStart := func() {
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
		_ = stderrReader.Close()
		_ = stderrWriter.Close()
	}
	child.Stdout = stdoutWriter
	child.Stderr = stderrWriter

	if stop := pendingRunserverProcessStop(ctx, interrupt); stop != runserverProcessRunning {
		closeBeforeStart()
		setRunserverProcessStop(&result, stop)
		return result
	}
	if err := child.Start(); err != nil {
		closeBeforeStart()
		result.StartError = err
		return result
	}
	result.Started = true
	if closeErr := stdoutWriter.Close(); closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
		recordRunserverCleanupFailure(&result, closeErr)
	}
	if closeErr := stderrWriter.Close(); closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
		recordRunserverCleanupFailure(&result, closeErr)
	}

	drained := make(chan runserverProcessDrain, 2)
	streamFailed := make(chan struct{}, 1)
	var outputMu sync.Mutex
	go func() {
		deliveryErr, readErr := streamRunserverProcessOutput(stdoutReader, stdout, &outputMu, streamFailed)
		if readErr != nil {
			notifyRunserverProcessStreamFailure(streamFailed)
		}
		drained <- runserverProcessDrain{stream: runserverProcessStdout, deliveryErr: deliveryErr, readErr: readErr}
	}()
	go func() {
		deliveryErr, readErr := streamRunserverProcessOutput(stderrReader, stderr, &outputMu, streamFailed)
		if readErr != nil {
			notifyRunserverProcessStreamFailure(streamFailed)
		}
		drained <- runserverProcessDrain{stream: runserverProcessStderr, deliveryErr: deliveryErr, readErr: readErr}
	}()
	waited := make(chan runserverProcessWait, 1)
	go func() {
		waitErr := child.Wait()
		waited <- runserverProcessWait{state: child.ProcessState, err: waitErr}
	}()

	waitObservation, stop := awaitRunserverProcess(ctx, interrupt, waited, streamFailed)
	setRunserverProcessStop(&result, stop)
	if waitObservation == nil {
		if queued := queuedRunserverProcessWait(waited); queued != nil {
			waitObservation = queued
		}
	}
	if waitObservation == nil {
		result.SIGINTAttempts++
		signalErr := signalRunserverProcessGroup(child.Process.Pid, unix.SIGINT)
		waitObservation = awaitRunserverProcessGrace(waited, grace)
		if signalErr != nil && waitObservation == nil {
			recordRunserverCleanupFailure(&result, signalErr)
		}
	}
	if waitObservation == nil {
		if queued := queuedRunserverProcessWait(waited); queued != nil {
			waitObservation = queued
		}
	}
	if waitObservation == nil {
		result.Forced = true
		result.SIGKILLAttempts++
		if signalErr := signalRunserverProcessGroup(child.Process.Pid, unix.SIGKILL); signalErr != nil {
			recordRunserverCleanupFailure(&result, signalErr)
		}
		observation := <-waited
		waitObservation = &observation
	}
	recordRunserverProcessWait(&result, *waitObservation)

	drainTimedOut := collectRunserverProcessDrains(&result, drained, stdoutReader, stderrReader, grace)
	lingeringGroup := false
	if result.SIGKILLAttempts == 0 {
		if drainTimedOut {
			lingeringGroup = runserverProcessGroupExists(child.Process.Pid)
		} else {
			lingeringGroup = waitRunserverProcessGroupGrace(child.Process.Pid, grace)
		}
	}
	if lingeringGroup {
		result.Forced = true
		result.SIGKILLAttempts++
		if signalErr := signalRunserverProcessGroup(child.Process.Pid, unix.SIGKILL); signalErr != nil {
			recordRunserverCleanupFailure(&result, signalErr)
		}
	}
	if result.SIGKILLAttempts != 0 && waitRunserverProcessGroupGrace(child.Process.Pid, grace) {
		recordRunserverCleanupFailure(&result, fmt.Errorf("projectcheck: runserver process group remains after SIGKILL"))
	}
	if closeErr := stdoutReader.Close(); closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
		recordRunserverCleanupFailure(&result, closeErr)
	}
	if closeErr := stderrReader.Close(); closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
		recordRunserverCleanupFailure(&result, closeErr)
	}
	return result
}

func validateRunserverProcess(command Command, stdout, stderr io.Writer, grace time.Duration) error {
	if len(command.Argv) == 0 || command.Argv[0] == "" {
		return fmt.Errorf("projectcheck: runserver command is empty")
	}
	if len(command.Stdin) != 0 {
		return fmt.Errorf("projectcheck: runserver command stdin is unsupported")
	}
	if stdout == nil || stderr == nil {
		return fmt.Errorf("projectcheck: runserver output writer is nil")
	}
	if grace <= 0 {
		return fmt.Errorf("projectcheck: runserver grace must be positive")
	}
	return nil
}

func pendingRunserverProcessStop(ctx context.Context, interrupt <-chan struct{}) runserverProcessStop {
	if interrupt != nil {
		select {
		case <-interrupt:
			return runserverProcessInterrupted
		default:
		}
	}
	if ctx.Err() != nil {
		return runserverProcessCanceled
	}
	return runserverProcessRunning
}

func setRunserverProcessStop(result *runserverProcessResult, stop runserverProcessStop) {
	switch stop {
	case runserverProcessInterrupted:
		result.Interrupted = true
	case runserverProcessCanceled:
		result.Canceled = true
	}
}

func awaitRunserverProcess(ctx context.Context, interrupt <-chan struct{}, waited <-chan runserverProcessWait, streamFailed <-chan struct{}) (*runserverProcessWait, runserverProcessStop) {
	if stop := pendingRunserverProcessStop(ctx, interrupt); stop != runserverProcessRunning {
		return nil, stop
	}
	select {
	case observation := <-waited:
		return &observation, runserverProcessRunning
	case <-interrupt:
		return nil, runserverProcessInterrupted
	case <-ctx.Done():
		return nil, runserverProcessCanceled
	case <-streamFailed:
		return nil, runserverProcessStreamFailed
	}
}

func queuedRunserverProcessWait(waited <-chan runserverProcessWait) *runserverProcessWait {
	select {
	case observation := <-waited:
		return &observation
	default:
		return nil
	}
}

func awaitRunserverProcessGrace(waited <-chan runserverProcessWait, grace time.Duration) *runserverProcessWait {
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case observation := <-waited:
		return &observation
	case <-timer.C:
		return nil
	}
}

func recordRunserverProcessWait(result *runserverProcessResult, observation runserverProcessWait) {
	result.DirectReaps = 1
	result.WaitError = observation.err
	if observation.state != nil {
		result.ExitCode = observation.state.ExitCode()
	}
	if observation.err == nil {
		return
	}
	var exitError *exec.ExitError
	if !errors.As(observation.err, &exitError) {
		recordRunserverCleanupFailure(result, observation.err)
	}
}

func signalRunserverProcessGroup(pid int, signal unix.Signal) error {
	err := unix.Kill(-pid, signal)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func runserverProcessGroupExists(pid int) bool {
	err := unix.Kill(-pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// waitRunserverProcessGroupGrace returns true only when same-group descendants
// remain after the direct child has been reaped and their cleanup grace has
// expired. Descendants that create a new process group are outside the
// supported development-runtime ownership boundary.
func waitRunserverProcessGroupGrace(pid int, grace time.Duration) bool {
	if !runserverProcessGroupExists(pid) {
		return false
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	poll := time.NewTicker(min(grace, 10*time.Millisecond))
	defer poll.Stop()
	for {
		select {
		case <-poll.C:
			if !runserverProcessGroupExists(pid) {
				return false
			}
		case <-timer.C:
			return runserverProcessGroupExists(pid)
		}
	}
}

func collectRunserverProcessDrains(result *runserverProcessResult, drained <-chan runserverProcessDrain, stdoutReader, stderrReader *os.File, grace time.Duration) bool {
	timer := time.NewTimer(grace)
	defer timer.Stop()
	timedOut := false
	for completed := 0; completed < 2; {
		if !timedOut {
			select {
			case drain := <-drained:
				recordRunserverProcessDrain(result, drain, false)
				completed++
			case <-timer.C:
				timedOut = true
				_ = stdoutReader.Close()
				_ = stderrReader.Close()
			}
			continue
		}
		drain := <-drained
		recordRunserverProcessDrain(result, drain, true)
		completed++
	}
	return timedOut
}

func recordRunserverProcessDrain(result *runserverProcessResult, drain runserverProcessDrain, forcedClose bool) {
	switch drain.stream {
	case runserverProcessStdout:
		result.StdoutError = drain.deliveryErr
	case runserverProcessStderr:
		result.StderrError = drain.deliveryErr
	default:
		recordRunserverCleanupFailure(result, fmt.Errorf("projectcheck: runserver stream identity is invalid"))
	}
	if drain.readErr != nil && !(forcedClose && errors.Is(drain.readErr, os.ErrClosed)) {
		recordRunserverCleanupFailure(result, drain.readErr)
	}
}

func recordRunserverCleanupFailure(result *runserverProcessResult, err error) {
	result.CleanupFailed = true
	result.CleanupError = errors.Join(result.CleanupError, err)
}

// streamRunserverProcessOutput keeps draining after the destination's first
// error so a diagnostic writer cannot fill the child's pipe and deadlock the
// owned process. The first delivery error remains available to the caller.
func streamRunserverProcessOutput(reader io.Reader, writer io.Writer, outputMu *sync.Mutex, streamFailed chan<- struct{}) (error, error) {
	buffer := make([]byte, 32<<10)
	var firstError error
	for {
		read, readErr := reader.Read(buffer)
		if read > 0 && firstError == nil {
			outputMu.Lock()
			written, writeErr := writer.Write(buffer[:read])
			outputMu.Unlock()
			var deliveryErr error
			if written < 0 || written > read {
				deliveryErr = io.ErrShortWrite
			} else if writeErr != nil {
				deliveryErr = writeErr
			} else if written != read {
				deliveryErr = io.ErrShortWrite
			}
			if deliveryErr != nil {
				firstError = deliveryErr
				notifyRunserverProcessStreamFailure(streamFailed)
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return firstError, nil
			}
			return firstError, readErr
		}
	}
}

func notifyRunserverProcessStreamFailure(streamFailed chan<- struct{}) {
	select {
	case streamFailed <- struct{}{}:
	default:
	}
}
