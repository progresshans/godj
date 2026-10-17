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

	"github.com/progresshans/godj/internal/projectcheck/createsuperuserprotocol"
	"golang.org/x/sys/unix"
)

const createsuperuserOwnedProcessGrace = 15 * time.Second

// createsuperuserProcessCommand deliberately has no stdin field. The global
// command may place the one private request only on the separately owned pipe
// below; argv, environment, the working directory and generic Command cloning
// are never secret transports.
type createsuperuserProcessCommand struct {
	dir  string
	argv []string
	env  []string
}

func newCreatesuperuserProcessCommand(dir string, argv, environment []string) createsuperuserProcessCommand {
	return createsuperuserProcessCommand{
		dir:  dir,
		argv: append([]string(nil), argv...),
		env:  append([]string(nil), environment...),
	}
}

// createsuperuserProcessResult retains the bounded private response only until
// its caller takes or discards it. String and GoString intentionally expose
// only scalars so untrusted child output can never enter diagnostics through
// formatting.
type createsuperuserProcessResult struct {
	response             []byte
	exitCode             int
	exited               bool
	started              bool
	directReaps          int
	stdoutScalar         StreamScalar
	stderrScalar         StreamScalar
	failure              *CreatesuperuserFailure
	cleanupFailed        bool
	sigintAttempts       int
	sigkillAttempts      int
	requestWriteAttempts int
	requestBytesWritten  int
}

func (result createsuperuserProcessResult) String() string {
	failure := "none"
	if result.failure != nil {
		failure = result.failure.Category + "/" + result.failure.Code
	}
	return fmt.Sprintf(
		"createsuperuserProcessResult{started:%t exited:%t exit:%d reaps:%d stdout:%d/%t stderr:%d/%t failure:%s cleanup_failed:%t sigint:%d sigkill:%d request_writes:%d request_bytes:%d}",
		result.started,
		result.exited,
		result.exitCode,
		result.directReaps,
		result.stdoutScalar.RetainedBytes,
		result.stdoutScalar.Truncated,
		result.stderrScalar.RetainedBytes,
		result.stderrScalar.Truncated,
		failure,
		result.cleanupFailed,
		result.sigintAttempts,
		result.sigkillAttempts,
		result.requestWriteAttempts,
		result.requestBytesWritten,
	)
}

func (result createsuperuserProcessResult) GoString() string {
	return result.String()
}

// takeResponse transfers the sole bounded response buffer to the caller. The
// caller owns clearing it immediately after strict protocol parsing.
func (result *createsuperuserProcessResult) takeResponse() []byte {
	if result == nil {
		return nil
	}
	document := result.response
	result.response = nil
	return document
}

func (result *createsuperuserProcessResult) discardResponse() {
	if result == nil {
		return
	}
	clear(result.response)
	result.response = nil
}

type createsuperuserProcessHooks struct {
	beforeRequestWrite   func()
	writeRequest         func(io.Writer, []byte) (int, error)
	afterRequestClear    func()
	beforeTerminalReturn func()
}

type createsuperuserProcessDrain struct {
	stdout bool
	prefix []byte
	scalar StreamScalar
	err    error
}

type createsuperuserProcessTerminal struct {
	beforeReturn func()
}

func (terminal *createsuperuserProcessTerminal) complete(waitComplete, drainComplete bool, processGroup int) bool {
	if !waitComplete || !drainComplete || runserverProcessGroupExists(processGroup) {
		return false
	}
	if terminal.beforeReturn != nil {
		beforeReturn := terminal.beforeReturn
		terminal.beforeReturn = nil
		beforeReturn()
	}
	return true
}

func executeOwnedCreatesuperuserProcess(
	ctx context.Context,
	interrupt <-chan struct{},
	command createsuperuserProcessCommand,
	request []byte,
) createsuperuserProcessResult {
	return executeOwnedCreatesuperuserProcessWithHooks(
		ctx,
		interrupt,
		command,
		request,
		createsuperuserOwnedProcessGrace,
		createsuperuserProcessHooks{},
	)
}

func executeOwnedCreatesuperuserProcessWithHooks(
	ctx context.Context,
	interrupt <-chan struct{},
	command createsuperuserProcessCommand,
	request []byte,
	grace time.Duration,
	hooks createsuperuserProcessHooks,
) createsuperuserProcessResult {
	result := createsuperuserProcessResult{exitCode: -1}
	requestCleared := false
	clearRequest := func() {
		if requestCleared {
			return
		}
		clear(request)
		request = nil
		requestCleared = true
		if hooks.afterRequestClear != nil {
			hooks.afterRequestClear()
		}
	}
	defer clearRequest()

	if ctx == nil || len(command.argv) == 0 || command.argv[0] == "" || grace <= 0 {
		return result
	}
	if len(request) == 0 || len(request) > createsuperuserprotocol.MaxRequestBytes {
		result.failure = createsuperuserProcessFailure(createsuperuserprotocol.CodeSensitiveInputTransportFailed)
		return result
	}
	if primary := pendingCreatesuperuserProcessFailure(ctx, interrupt); primary != nil {
		result.failure = primary
		return result
	}

	ownedCommand := newCreatesuperuserProcessCommand(command.dir, command.argv, command.env)
	child := exec.Command(ownedCommand.argv[0], ownedCommand.argv[1:]...)
	child.Dir = ownedCommand.dir
	child.Env = ownedCommand.env
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		result.failure = createsuperuserProcessFailure(createsuperuserprotocol.CodeSensitiveInputTransportFailed)
		return result
	}
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		closeCreatesuperuserProcessFile(&result, stdinReader)
		closeCreatesuperuserProcessFile(&result, stdinWriter)
		classifyUnstartedCreatesuperuserCleanup(&result)
		return result
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		closeCreatesuperuserProcessFile(&result, stdinReader)
		closeCreatesuperuserProcessFile(&result, stdinWriter)
		closeCreatesuperuserProcessFile(&result, stdoutReader)
		closeCreatesuperuserProcessFile(&result, stdoutWriter)
		classifyUnstartedCreatesuperuserCleanup(&result)
		return result
	}

	child.Stdin = stdinReader
	child.Stdout = stdoutWriter
	child.Stderr = stderrWriter
	if primary := pendingCreatesuperuserProcessFailure(ctx, interrupt); primary != nil {
		closeCreatesuperuserProcessFile(&result, stdinReader)
		closeCreatesuperuserProcessFile(&result, stdinWriter)
		closeCreatesuperuserProcessFile(&result, stdoutReader)
		closeCreatesuperuserProcessFile(&result, stdoutWriter)
		closeCreatesuperuserProcessFile(&result, stderrReader)
		closeCreatesuperuserProcessFile(&result, stderrWriter)
		result.failure = primary
		return result
	}
	if err := child.Start(); err != nil {
		closeCreatesuperuserProcessFile(&result, stdinReader)
		closeCreatesuperuserProcessFile(&result, stdinWriter)
		closeCreatesuperuserProcessFile(&result, stdoutReader)
		closeCreatesuperuserProcessFile(&result, stdoutWriter)
		closeCreatesuperuserProcessFile(&result, stderrReader)
		closeCreatesuperuserProcessFile(&result, stderrWriter)
		classifyUnstartedCreatesuperuserCleanup(&result)
		return result
	}
	result.started = true
	closeCreatesuperuserProcessFile(&result, stdinReader)
	closeCreatesuperuserProcessFile(&result, stdoutWriter)
	closeCreatesuperuserProcessFile(&result, stderrWriter)

	drains := make(chan createsuperuserProcessDrain, 2)
	overflows := make(chan struct{}, 2)
	drained := make(chan struct{})
	var drainers sync.WaitGroup
	drainers.Add(2)
	go func() {
		defer drainers.Done()
		drains <- drainCreatesuperuserProcessStream(
			stdoutReader,
			createsuperuserprotocol.MaxResponseBytes,
			true,
			overflows,
		)
	}()
	go func() {
		defer drainers.Done()
		drains <- drainCreatesuperuserProcessStream(
			stderrReader,
			maxDiagnosticBytes,
			false,
			overflows,
		)
	}()
	go func() {
		drainers.Wait()
		close(drained)
	}()

	waited := make(chan error, 1)
	go func() { waited <- child.Wait() }()

	var primary *CreatesuperuserFailure
	requestWriteComplete := false
	if pending := pendingCreatesuperuserProcessFailure(ctx, interrupt); pending != nil {
		primary = pending
		clearRequest()
		closeCreatesuperuserProcessFile(&result, stdinWriter)
	} else {
		writer := hooks.writeRequest
		if writer == nil {
			writer = func(destination io.Writer, document []byte) (int, error) {
				return destination.Write(document)
			}
		}
		if hooks.beforeRequestWrite != nil {
			hooks.beforeRequestWrite()
		}
		result.requestWriteAttempts = 1
		written, writeErr := writer(stdinWriter, request)
		if written >= 0 && written <= len(request) {
			result.requestBytesWritten = written
		}
		completeWrite := writeErr == nil && written == len(request)
		clearRequest()
		closeErr := stdinWriter.Close()
		if closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
			result.cleanupFailed = true
		}
		if !completeWrite || closeErr != nil {
			primary = createsuperuserProcessFailure(createsuperuserprotocol.CodeSensitiveInputTransportFailed)
		} else {
			requestWriteComplete = true
		}
	}

	var waitErr error
	waitComplete := false
	drainComplete := false
	terminal := createsuperuserProcessTerminal{beforeReturn: hooks.beforeTerminalReturn}
	postExitTimedOut := false
	outputLimitExceeded := false
	needsTermination := false
	for primary == nil && !needsTermination {
		waitErr, waitComplete = reconcileCreatesuperuserProcessWait(child.Process, waited, waitErr, waitComplete)
		drainComplete = reconcileCreatesuperuserProcessDrain(drained, drainComplete)
		if terminal.complete(waitComplete, drainComplete, child.Process.Pid) {
			break
		}
		if queuedCreatesuperuserProcessOverflow(overflows) {
			outputLimitExceeded = true
			needsTermination = true
			primary = pendingCreatesuperuserProcessFailure(ctx, interrupt)
		}
		if needsTermination {
			break
		}
		if waitComplete {
			postExitTimedOut, primary = awaitCreatesuperuserPostExit(
				ctx,
				interrupt,
				drained,
				overflows,
				child.Process.Pid,
				&drainComplete,
				&outputLimitExceeded,
				grace,
				&terminal,
			)
			needsTermination = outputLimitExceeded
			break
		}
		if pending := pendingCreatesuperuserProcessFailure(ctx, interrupt); pending != nil {
			primary = pending
			break
		}
		select {
		case waitErr = <-waited:
			waitComplete = true
		case <-drained:
			drainComplete = true
		case <-overflows:
			outputLimitExceeded = true
			needsTermination = true
			primary = pendingCreatesuperuserProcessFailure(ctx, interrupt)
		case <-interrupt:
			primary = createsuperuserProcessFailure(createsuperuserprotocol.CodeProjectInterrupted)
		case <-ctx.Done():
			primary = pendingCreatesuperuserProcessFailure(ctx, interrupt)
		}
		if primary != nil || needsTermination {
			waitErr, waitComplete = reconcileCreatesuperuserProcessWait(child.Process, waited, waitErr, waitComplete)
			drainComplete = reconcileCreatesuperuserProcessDrain(drained, drainComplete)
			if terminal.complete(waitComplete, drainComplete, child.Process.Pid) {
				primary = nil
				needsTermination = false
			}
		}
	}

	forcedClose := false
	if primary != nil || needsTermination {
		waitErr, waitComplete, drainComplete, forcedClose = cleanCanceledCreatesuperuserProcess(
			child.Process,
			waited,
			drained,
			stdoutReader,
			stderrReader,
			waitErr,
			waitComplete,
			drainComplete,
			grace,
			&result,
		)
	} else if postExitTimedOut {
		drainComplete, forcedClose = cleanPostExitCreatesuperuserProcess(
			child.Process.Pid,
			drained,
			stdoutReader,
			stderrReader,
			drainComplete,
			grace,
			&result,
		)
	}

	if !waitComplete {
		waitErr = <-waited
		waitComplete = true
	}
	if !drainComplete {
		<-drained
		drainComplete = true
	}
	_ = waitComplete
	_ = drainComplete
	closeCreatesuperuserProcessFile(&result, stdoutReader)
	closeCreatesuperuserProcessFile(&result, stderrReader)

	for count := 0; count < 2; count++ {
		drain := <-drains
		if drain.err != nil && !(forcedClose && errors.Is(drain.err, os.ErrClosed)) {
			result.cleanupFailed = true
		}
		if drain.stdout {
			result.response = drain.prefix
			result.stdoutScalar = drain.scalar
		} else {
			clear(drain.prefix)
			result.stderrScalar = drain.scalar
		}
	}

	if result.sigkillAttempts != 0 && waitRunserverProcessGroupGrace(child.Process.Pid, min(grace, time.Second)) {
		result.cleanupFailed = true
	}
	result.directReaps = 1
	result.exitCode, result.exited = createsuperuserProcessExit(waitErr)
	if createsuperuserCancellationCompletedWithResponse(
		primary,
		requestWriteComplete,
		waitComplete,
		drainComplete,
		forcedClose,
		outputLimitExceeded,
		child.Process.Pid,
		result,
	) {
		primary = nil
	}
	if primary != nil {
		result.failure = primary
	} else if result.cleanupFailed {
		result.failure = createsuperuserProcessFailure(createsuperuserprotocol.CodeProjectCleanupFailed)
	}
	// A nonzero transport or an over-limit document cannot be a parseable
	// private response. Clear it here instead of making the caller retain raw
	// bytes that it is required to reject without inspection.
	if result.failure != nil || result.cleanupFailed || result.exitCode != 0 ||
		result.stdoutScalar.Truncated || result.stderrScalar.Truncated || outputLimitExceeded {
		result.discardResponse()
	}
	return result
}

func createsuperuserCancellationCompletedWithResponse(
	primary *CreatesuperuserFailure,
	requestWriteComplete bool,
	waitComplete bool,
	drainComplete bool,
	forcedClose bool,
	outputLimitExceeded bool,
	processGroup int,
	result createsuperuserProcessResult,
) bool {
	if primary == nil || primary.Category != createsuperuserprotocol.CategoryProcess ||
		(primary.Code != createsuperuserprotocol.CodeProjectCanceled &&
			primary.Code != createsuperuserprotocol.CodeProjectInterrupted) ||
		!requestWriteComplete || !waitComplete || !drainComplete || forcedClose || outputLimitExceeded ||
		result.cleanupFailed || result.exitCode != 0 || result.directReaps != 1 ||
		result.sigintAttempts != 1 || result.sigkillAttempts != 0 ||
		result.stdoutScalar.Truncated || result.stderrScalar.Truncated ||
		runserverProcessGroupExists(processGroup) {
		return false
	}
	_, _, responseFailed := createsuperuserprotocol.ParseResponse(result.response, true)
	return !responseFailed
}

func queuedCreatesuperuserProcessOverflow(overflows <-chan struct{}) bool {
	select {
	case <-overflows:
		return true
	default:
		return false
	}
}

func pendingCreatesuperuserProcessFailure(ctx context.Context, interrupt <-chan struct{}) *CreatesuperuserFailure {
	if interrupt != nil {
		select {
		case <-interrupt:
			return createsuperuserProcessFailure(createsuperuserprotocol.CodeProjectInterrupted)
		default:
		}
	}
	if ctx != nil && ctx.Err() != nil {
		return createsuperuserProcessFailure(createsuperuserprotocol.CodeProjectCanceled)
	}
	return nil
}

func createsuperuserProcessFailure(code string) *CreatesuperuserFailure {
	failure := CreatesuperuserFailure{Category: createsuperuserprotocol.CategoryProcess, Code: code}
	if _, ok := createsuperuserprotocol.ExitCode(failure); !ok {
		failure = CreatesuperuserFailure{
			Category: createsuperuserprotocol.CategoryInternal,
			Code:     createsuperuserprotocol.CodeProjectInternalError,
		}
	}
	return &failure
}

func closeCreatesuperuserProcessFile(result *createsuperuserProcessResult, file *os.File) {
	if file == nil {
		return
	}
	if err := file.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		result.cleanupFailed = true
	}
}

func classifyUnstartedCreatesuperuserCleanup(result *createsuperuserProcessResult) {
	if result != nil && result.cleanupFailed && result.failure == nil {
		result.failure = createsuperuserProcessFailure(createsuperuserprotocol.CodeProjectCleanupFailed)
	}
}

func drainCreatesuperuserProcessStream(
	reader *os.File,
	maximum int,
	retain bool,
	overflows chan<- struct{},
) createsuperuserProcessDrain {
	result := createsuperuserProcessDrain{stdout: retain}
	if reader == nil || maximum < 0 || overflows == nil {
		result.err = errors.New("project createsuperuser process: invalid stream")
		return result
	}
	defer reader.Close()
	buffer := make([]byte, min(maximum+1, 32<<10))
	defer clear(buffer)
	if retain {
		result.prefix = make([]byte, 0, min(maximum, 4<<10))
	}
	observed := 0
	for {
		read, readErr := reader.Read(buffer)
		if read > 0 {
			before := observed
			if before < maximum {
				observed += read
				if observed > maximum {
					observed = maximum
				}
			}
			if retain && before < maximum {
				kept := read
				if before+kept > maximum {
					kept = maximum - before
				}
				result.prefix = append(result.prefix, buffer[:kept]...)
			}
			if !result.scalar.Truncated && (before >= maximum || read > maximum-before) {
				result.scalar = StreamScalar{RetainedBytes: maximum, Truncated: true}
				overflows <- struct{}{}
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				result.err = readErr
			}
			if !result.scalar.Truncated {
				result.scalar = StreamScalar{RetainedBytes: observed}
			}
			return result
		}
	}
}

func reconcileCreatesuperuserProcessWait(process *os.Process, waited <-chan error, current error, complete bool) (error, bool) {
	if complete {
		return current, true
	}
	return reconcileOwnedProcessWait(process, waited, current)
}

func reconcileCreatesuperuserProcessDrain(drained <-chan struct{}, complete bool) bool {
	if complete {
		return true
	}
	select {
	case <-drained:
		return true
	default:
		return false
	}
}

func awaitCreatesuperuserPostExit(
	ctx context.Context,
	interrupt <-chan struct{},
	drained <-chan struct{},
	overflows <-chan struct{},
	processGroup int,
	drainComplete *bool,
	outputLimitExceeded *bool,
	grace time.Duration,
	terminal *createsuperuserProcessTerminal,
) (bool, *CreatesuperuserFailure) {
	if reconcileCreatesuperuserProcessTerminal(drained, processGroup, drainComplete, terminal) {
		return false, nil
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	poll := time.NewTicker(min(grace, 10*time.Millisecond))
	defer poll.Stop()
	for {
		if reconcileCreatesuperuserProcessTerminal(drained, processGroup, drainComplete, terminal) {
			return false, nil
		}
		var drainChannel <-chan struct{}
		if !*drainComplete {
			drainChannel = drained
		}
		select {
		case <-drainChannel:
			*drainComplete = true
		case <-overflows:
			if reconcileCreatesuperuserProcessTerminal(drained, processGroup, drainComplete, terminal) {
				return false, nil
			}
			*outputLimitExceeded = true
			if pending := pendingCreatesuperuserProcessFailure(ctx, interrupt); pending != nil {
				return false, pending
			}
			return false, nil
		case <-poll.C:
		case <-interrupt:
			if reconcileCreatesuperuserProcessTerminal(drained, processGroup, drainComplete, terminal) {
				return false, nil
			}
			return false, createsuperuserProcessFailure(createsuperuserprotocol.CodeProjectInterrupted)
		case <-ctx.Done():
			if reconcileCreatesuperuserProcessTerminal(drained, processGroup, drainComplete, terminal) {
				return false, nil
			}
			return false, pendingCreatesuperuserProcessFailure(ctx, interrupt)
		case <-timer.C:
			if reconcileCreatesuperuserProcessTerminal(drained, processGroup, drainComplete, terminal) {
				return false, nil
			}
			return true, nil
		}
	}
}

func reconcileCreatesuperuserProcessTerminal(
	drained <-chan struct{},
	processGroup int,
	drainComplete *bool,
	terminal *createsuperuserProcessTerminal,
) bool {
	*drainComplete = reconcileCreatesuperuserProcessDrain(drained, *drainComplete)
	return terminal.complete(true, *drainComplete, processGroup)
}

func cleanCanceledCreatesuperuserProcess(
	process *os.Process,
	waited <-chan error,
	drained <-chan struct{},
	stdoutReader *os.File,
	stderrReader *os.File,
	waitErr error,
	waitComplete bool,
	drainComplete bool,
	grace time.Duration,
	result *createsuperuserProcessResult,
) (error, bool, bool, bool) {
	waitErr, waitComplete = reconcileCreatesuperuserProcessWait(process, waited, waitErr, waitComplete)
	drainComplete = reconcileCreatesuperuserProcessDrain(drained, drainComplete)
	processGroup := process.Pid
	if !waitComplete || !drainComplete || runserverProcessGroupExists(processGroup) {
		result.sigintAttempts++
		signalErr := signalRunserverProcessGroup(processGroup, unix.SIGINT)
		waitErr, waitComplete, drainComplete = awaitCreatesuperuserCancellationGrace(
			process,
			waited,
			drained,
			waitErr,
			waitComplete,
			drainComplete,
			grace,
		)
		// An output-limit observation and a clean direct exit can race. A
		// failed signal is cleanup-significant only if the owned process/group
		// did not reach terminal state during the grace reconciliation.
		if signalErr != nil && (!waitComplete || runserverProcessGroupExists(processGroup)) {
			result.cleanupFailed = true
		}
	}
	waitErr, waitComplete = reconcileCreatesuperuserProcessWait(process, waited, waitErr, waitComplete)
	drainComplete = reconcileCreatesuperuserProcessDrain(drained, drainComplete)
	if !waitComplete || !drainComplete || runserverProcessGroupExists(processGroup) {
		result.sigkillAttempts++
		if err := signalRunserverProcessGroup(processGroup, unix.SIGKILL); err != nil {
			result.cleanupFailed = true
		}
	}
	if !waitComplete {
		waitErr = <-waited
		waitComplete = true
	}
	forcedClose := false
	if !drainComplete {
		drainComplete = awaitCreatesuperuserDrain(drained, min(grace, time.Second))
	}
	if !drainComplete {
		forcedClose = true
		closeCreatesuperuserProcessFile(result, stdoutReader)
		closeCreatesuperuserProcessFile(result, stderrReader)
		<-drained
		drainComplete = true
	}
	return waitErr, waitComplete, drainComplete, forcedClose
}

func awaitCreatesuperuserCancellationGrace(
	process *os.Process,
	waited <-chan error,
	drained <-chan struct{},
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
		waitErr, waitComplete = reconcileCreatesuperuserProcessWait(process, waited, waitErr, waitComplete)
		drainComplete = reconcileCreatesuperuserProcessDrain(drained, drainComplete)
		if waitComplete && drainComplete && !runserverProcessGroupExists(process.Pid) {
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

func cleanPostExitCreatesuperuserProcess(
	processGroup int,
	drained <-chan struct{},
	stdoutReader *os.File,
	stderrReader *os.File,
	drainComplete bool,
	grace time.Duration,
	result *createsuperuserProcessResult,
) (bool, bool) {
	groupExisted := runserverProcessGroupExists(processGroup)
	if groupExisted {
		result.sigkillAttempts++
		if err := signalRunserverProcessGroup(processGroup, unix.SIGKILL); err != nil {
			result.cleanupFailed = true
		}
	}
	if !drainComplete {
		drainComplete = awaitCreatesuperuserDrain(drained, min(grace, time.Second))
	}
	forcedClose := false
	if !drainComplete {
		forcedClose = true
		if !groupExisted {
			result.cleanupFailed = true
		}
		closeCreatesuperuserProcessFile(result, stdoutReader)
		closeCreatesuperuserProcessFile(result, stderrReader)
		<-drained
		drainComplete = true
	}
	return drainComplete, forcedClose
}

func awaitCreatesuperuserDrain(drained <-chan struct{}, grace time.Duration) bool {
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-drained:
		return true
	case <-timer.C:
		return false
	}
}

func createsuperuserProcessExit(waitErr error) (int, bool) {
	if waitErr == nil {
		return 0, true
	}
	var exitError *exec.ExitError
	if errors.As(waitErr, &exitError) && exitError.ProcessState != nil {
		return exitError.ExitCode(), exitError.ProcessState.Exited()
	}
	return 1, false
}
