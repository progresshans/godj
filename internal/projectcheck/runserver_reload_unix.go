//go:build darwin || linux

package projectcheck

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/progresshans/godj/internal/gobuild"
	"github.com/progresshans/godj/internal/runservercontrol"
	"golang.org/x/sys/unix"
)

const runserverSourcePoll = time.Second
const runserverReadyTimeout = 30 * time.Second

type synchronizedRunserverWriter struct {
	mu     *sync.Mutex
	writer io.Writer
}

func (writer synchronizedRunserverWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.writer.Write(data)
}

type managedRunserver struct {
	control net.Conn
	cancel  context.CancelFunc
	grace   time.Duration
	done    chan struct{}
	result  runserverProcessResult // published by close(done)
}

type runserverCandidate struct {
	prepared preparedProjectRuntime
	process  *managedRunserver
	report   RunServerReport
	failure  *RunServerFailure
	cancel   context.CancelFunc
	retired  bool
}

func newManagedRunserver(ctx context.Context, input RunServerInvocation, prepared preparedProjectRuntime, listener *net.TCPListener) (*managedRunserver, error) {
	listenerFile, err := listener.File()
	if err != nil {
		return nil, err
	}
	// Match os.Pipe's fork exclusion while marking both socket ends close-on-exec.
	// Only the explicitly supplied child end is subsequently inherited.
	syscall.ForkLock.RLock()
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err == nil {
		unix.CloseOnExec(pair[0])
		unix.CloseOnExec(pair[1])
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		_ = listenerFile.Close()
		return nil, err
	}
	parentFile := os.NewFile(uintptr(pair[0]), "godj-parent-control")
	childFile := os.NewFile(uintptr(pair[1]), "godj-child-control")
	control, err := net.FileConn(parentFile)
	closeErr := parentFile.Close()
	if err != nil || closeErr != nil {
		if control != nil {
			_ = control.Close()
		}
		_ = childFile.Close()
		_ = listenerFile.Close()
		return nil, errors.Join(err, closeErr)
	}
	lifetime, cancel := context.WithCancel(ctx)
	process := &managedRunserver{control: control, cancel: cancel, grace: input.runtime.grace, done: make(chan struct{})}
	command := Command{Dir: prepared.command.selected.rootPath,
		Argv: []string{prepared.binary, "serve", "--listen", listener.Addr().String(), runservercontrol.Argument},
		Env:  gobuild.RuntimeEnvironment(input.Environment)}
	go func() {
		process.result = executeStreamingProcessWithFiles(lifetime, input.Interrupt, command, nil, input.Stdout, input.Stderr, input.runtime.grace, []*os.File{listenerFile, childFile})
		_ = control.Close()
		close(process.done)
	}()
	return process, nil
}

func (process *managedRunserver) exchange(ctx context.Context, send, expect string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := process.control.SetDeadline(time.Now().Add(runserverReadyTimeout)); err != nil {
		return err
	}
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = process.control.Close(); close(closed) })
	var err error
	if send != "" {
		err = runservercontrol.Send(process.control, send)
	}
	if err == nil {
		err = runservercontrol.Expect(process.control, expect)
	}
	if !stop() {
		<-closed
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if reset := process.control.SetDeadline(time.Time{}); err == nil {
		err = reset
	}
	return err
}

func (process *managedRunserver) stop(retired bool) runserverProcessResult {
	defer process.cancel()
	if retired {
		// Retire already asked the child to drain and exit. An extra SIGINT
		// can hit after signal.NotifyContext has restored the default handler
		// but before the OS has reported normal exit. Join that cooperative
		// shutdown first; use cancellation only if its owned grace expires.
		timer := time.NewTimer(process.grace)
		select {
		case <-process.done:
		case <-timer.C:
			process.cancel()
		}
		timer.Stop()
	} else {
		process.cancel()
	}
	<-process.done
	_ = process.control.Close()
	return process.result
}

func prepareRunserverCandidate(input RunServerInvocation, explicit string, listener *net.TCPListener, cancel context.CancelFunc) *runserverCandidate {
	candidate := &runserverCandidate{cancel: cancel}
	input.sourceFence = true
	candidate.prepared, candidate.failure = prepareProjectRuntime(input, explicit, runtimeServer, &candidate.report)
	if candidate.failure != nil || listener == nil {
		return candidate
	}
	startRunserverCandidate(input, candidate, listener)
	return candidate
}

func startRunserverCandidate(input RunServerInvocation, candidate *runserverCandidate, listener *net.TCPListener) {
	var err error
	candidate.report.RuntimeStartCalls++
	candidate.process, err = newManagedRunserver(input.Context, input, candidate.prepared, listener)
	if err == nil {
		err = candidate.process.exchange(input.Context, "", runservercontrol.Prepared)
	}
	if err != nil {
		candidate.failure = &RunServerFailure{Category: RunServerCategoryRuntime, Code: RunServerCodeRuntimeReadinessFailed}
		return
	}
	current, err := candidate.prepared.source.observe(input.Context)
	if err != nil || current != candidate.prepared.source.digest {
		candidate.failure = runserverSourceFailure(RunServerCodeSourceChanged)
	}
}

// Finish every candidate before starting another. There can be one active and
// one prepared generation; rapid source changes coalesce while a canceled build
// or retired runtime is being joined. Cleanup is never an unowned background job.
func finishRunserverCandidate(candidate *runserverCandidate, report *RunServerReport) bool {
	if candidate == nil {
		return false
	}
	defer candidate.cancel()
	failed := false
	if candidate.process != nil {
		result := candidate.process.stop(candidate.retired)
		recordRunserverProcess(&candidate.report, result)
		failed = result.CleanupFailed || result.StdoutError != nil || result.StderrError != nil
		// A canceled candidate may fail startup. A published generation must
		// report its stop/DB cleanup error instead of calling retirement success.
		if candidate.report.ReloadPublished != 0 && (result.Forced || result.ExitCode != 0 || result.WaitError != nil) {
			failed = true
		}
	}
	if candidate.prepared.command != nil && candidate.prepared.command.close() {
		failed = true
	}
	mergeRunserverReport(report, candidate.report)
	return failed
}

func runReloadingServer(input RunServerInvocation, arguments runserverArguments) RunServerReport {
	report := RunServerReport{}
	if input.Stdout == nil || input.Stderr == nil {
		chooseRunserverFailure(&report, RunServerFailure{Category: RunServerCategoryRuntime, Code: RunServerCodeRuntimeStreamFailed})
		publishRunserver(input, &report)
		return report
	}
	var outputMu sync.Mutex
	input.Stdout = synchronizedRunserverWriter{mu: &outputMu, writer: input.Stdout}
	input.Stderr = synchronizedRunserverWriter{mu: &outputMu, writer: input.Stderr}
	ctx, cancel := context.WithCancel(input.Context)
	defer cancel()
	input.Context = ctx
	// Retain the listener across all generations. A child receives a duplicate
	// descriptor, so no TCP proxy changes request authority or client identity.
	var listener *net.TCPListener
	defer func() {
		if listener != nil {
			_ = listener.Close()
		}
	}()
	address := arguments.address
	var active *runserverCandidate
	var building <-chan *runserverCandidate
	var cancelBuild context.CancelFunc
	var source *runserverSource
	var observed [sha256.Size]byte
	var observeFailed bool
	var root string
	var identity unix.Stat_t
	var primary *RunServerFailure
	dirty := true
	first := true
	var retryAfter time.Time
	explicit := arguments.explicitDescriptor
	startBuild := func() {
		attemptContext, stop := context.WithCancel(ctx)
		cancelBuild = stop
		attempt := input
		attempt.Context = attemptContext
		output := make(chan *runserverCandidate, 1)
		building = output
		report.ReloadAttempts++
		dirty = false
		go func() { output <- prepareRunserverCandidate(attempt, explicit, listener, stop) }()
	}
	ticker := time.NewTicker(runserverSourcePoll)
	defer ticker.Stop()
	defer func() {
		if cancelBuild != nil {
			cancelBuild()
		}
	}()
	for primary == nil {
		if terminal := runserverBarrier(input, nil); terminal != nil {
			// A clean operator interrupt after readiness remains exit 0. Context
			// cancellation and interruptions before the first runtime stay distinct.
			if active == nil || terminal.Code != RunServerCodeProjectInterrupted {
				primary = terminal
			}
			break
		}
		if dirty && building == nil && !time.Now().Before(retryAfter) {
			startBuild()
		}
		var exited <-chan struct{}
		if active != nil {
			exited = active.process.done
		}
		select {
		case <-ctx.Done():
			primary = &RunServerFailure{Category: RunServerCategoryProcess, Code: RunServerCodeProjectCanceled}
		case <-input.Interrupt:
			if active == nil {
				primary = &RunServerFailure{Category: RunServerCategoryProcess, Code: RunServerCodeProjectInterrupted}
			}
			goto finished
		case <-exited:
			primary = classifyRunserverProcess(active.process.result)
			if primary == nil {
				primary = &RunServerFailure{Category: RunServerCategoryRuntime, Code: RunServerCodeRuntimeExited}
			}
		case candidate := <-building:
			building = nil
			cancelBuild = nil
			if root != "" && candidate.prepared.command != nil {
				selected := candidate.prepared.command.selected
				if selected.rootPath != root || !sameIdentity(selected.rootIdentity, identity) {
					candidate.failure = runserverSourceFailure(RunServerCodeSourceChanged)
				}
			}
			if dirty || candidate.prepared.command.ctx.Err() != nil {
				candidate.failure = runserverSourceFailure(RunServerCodeSourceChanged)
			}
			if candidate.failure == nil && candidate.process == nil {
				var listen net.ListenConfig
				opened, err := listen.Listen(ctx, "tcp4", arguments.address)
				if err != nil {
					candidate.failure = pointerRunserverFailure(RunServerFailure{Category: RunServerCategoryRuntime, Code: RunServerCodeListenerFailed})
				} else {
					listener = opened.(*net.TCPListener)
					address = listener.Addr().String()
					attempt := input
					attempt.Context = candidate.prepared.command.ctx
					startRunserverCandidate(attempt, candidate, listener)
				}
			}
			if candidate.failure == nil {
				current, observeErr := candidate.prepared.source.observe(ctx)
				if observeErr != nil || current != candidate.prepared.source.digest {
					candidate.failure = runserverSourceFailure(RunServerCodeSourceChanged)
				}
				select {
				case <-candidate.process.done:
					candidate.failure = pointerRunserverFailure(RunServerFailure{Category: RunServerCategoryRuntime, Code: RunServerCodeRuntimeReadinessFailed})
				default:
				}
			}
			if candidate.failure != nil {
				report.ReloadRejected++
				if candidate.prepared.source != nil && !dirty {
					source = candidate.prepared.source
					var err error
					observed, err = source.observe(ctx)
					observeFailed = err != nil
					if !first && !observeFailed && observed != source.digest {
						dirty = true
						retryAfter = time.Now().Add(runserverSourcePoll)
					}
				}
				failure := *candidate.failure
				if finishRunserverCandidate(candidate, &report) {
					primary = pointerRunserverFailure(runserverCleanupFailure())
					continue
				}
				if first {
					primary = &failure
					continue
				}
				if !dirty && !writeRunserverReloadFailure(input, candidate, failure) {
					primary = pointerRunserverFailure(RunServerFailure{Category: RunServerCategoryRuntime, Code: RunServerCodeRuntimeStreamFailed})
				}
				continue
			}
			if root == "" {
				root = candidate.prepared.command.selected.rootPath
				identity = candidate.prepared.command.selected.rootIdentity
				explicit = filepath.Join(root, descriptorName)
			}
			// This is the publication fence. The old process keeps accepting
			// until every candidate startup/source check has succeeded. Retire
			// acknowledges listener close, while its existing requests still run.
			if active != nil {
				if err := active.process.exchange(ctx, runservercontrol.Retire, runservercontrol.Retired); err != nil {
					_ = finishRunserverCandidate(candidate, &report)
					primary = pointerRunserverFailure(RunServerFailure{Category: RunServerCategoryRuntime, Code: RunServerCodeRuntimeControlFailed})
					continue
				}
				active.retired = true
			}
			if err := candidate.process.exchange(ctx, runservercontrol.Activate, runservercontrol.Active); err != nil {
				_ = finishRunserverCandidate(candidate, &report)
				primary = pointerRunserverFailure(RunServerFailure{Category: RunServerCategoryRuntime, Code: RunServerCodeRuntimeControlFailed})
				continue
			}
			candidate.report.ReloadPublished = 1
			old := active
			active = candidate
			source, observed, observeFailed = candidate.prepared.source, candidate.prepared.source.digest, false
			if old != nil {
				const published = "godj runserver reloaded\n"
				if n, err := io.WriteString(input.Stdout, published); err != nil || n != len(published) {
					primary = pointerRunserverFailure(RunServerFailure{Category: RunServerCategoryRuntime, Code: RunServerCodeRuntimeStreamFailed})
				}
				if finishRunserverCandidate(old, &report) {
					primary = pointerRunserverFailure(runserverCleanupFailure())
				}
			}
			first = false
		case <-ticker.C:
			if source == nil {
				continue
			}
			current, observeErr := source.observe(ctx)
			failed := observeErr != nil
			if failed != observeFailed || !failed && current != observed {
				observeFailed, observed, dirty = failed, current, true
				if cancelBuild != nil {
					cancelBuild()
				}
			}
		}
	}
finished:
	if terminal := runserverBarrier(input, primary); terminal != nil {
		if active != nil && terminal.Code == RunServerCodeProjectInterrupted {
			primary = nil
		} else {
			primary = terminal
		}
	}
	if cancelBuild != nil {
		cancelBuild()
		candidate := <-building
		if finishRunserverCandidate(candidate, &report) {
			primary = pointerRunserverFailure(runserverCleanupFailure())
		}
		building, cancelBuild = nil, nil
	}
	if finishRunserverCandidate(active, &report) {
		primary = pointerRunserverFailure(runserverCleanupFailure())
	}
	if primary != nil {
		chooseRunserverFailure(&report, *primary)
	} else {
		chooseRunserverResult(&report, RunServerResult{Address: address})
	}
	publishRunserver(input, &report)
	return report
}

func pointerRunserverFailure(value RunServerFailure) *RunServerFailure { return &value }

func writeRunserverReloadFailure(input RunServerInvocation, candidate *runserverCandidate, failed RunServerFailure) bool {
	document := []byte("project_runserver_reload_rejected/" + failed.Code + "\n")
	if failed.Code == RunServerCodeProjectBuildFailed || failed.Code == RunServerCodeRuntimeBuildFailed {
		if diagnostic := candidate.report.BuildDiagnostic; diagnostic != "" {
			document = append(document, []byte(diagnostic+"\n")...)
		}
	}
	n, err := input.Stderr.Write(document)
	return err == nil && n == len(document)
}

// Preparation and cleanup counters are merged only after their single owner
// has joined. Runtime/application observations are never inferred from starts.
func mergeRunserverReport(total *RunServerReport, part RunServerReport) {
	total.CompatibilityChecks += part.CompatibilityChecks
	total.CompatibilityResponseWrites += part.CompatibilityResponseWrites
	total.BuildCalls += part.BuildCalls
	total.RunnerCalls += part.RunnerCalls
	total.AncestorDirectoriesInspected += part.AncestorDirectoriesInspected
	total.DescriptorReads += part.DescriptorReads
	total.UserStdoutWrites += part.UserStdoutWrites
	total.UserStderrWrites += part.UserStderrWrites
	total.PartialStdoutWrites += part.PartialStdoutWrites
	total.RunnerResponseWrites += part.RunnerResponseWrites
	total.TempCreated += part.TempCreated
	total.TempCleanupAttempts += part.TempCleanupAttempts
	total.CleanupFailed = max(total.CleanupFailed, part.CleanupFailed)
	total.ResidualTemp = max(total.ResidualTemp, part.ResidualTemp)
	total.DirectChildReaps += part.DirectChildReaps
	total.GroupSIGINTAttempts += part.GroupSIGINTAttempts
	total.GroupSIGKILLAttempts += part.GroupSIGKILLAttempts
	total.RuntimeBuildCalls += part.RuntimeBuildCalls
	total.RuntimeStartCalls += part.RuntimeStartCalls
	total.PreflightChecks += part.PreflightChecks
	total.GeneratedDriftCount += part.GeneratedDriftCount
	total.SourceInventoryCalls += part.SourceInventoryCalls
	total.ReloadPublished += part.ReloadPublished
	total.BuildDiagnostic = part.BuildDiagnostic
	total.RawDiagnosticsDiscarded = total.RawDiagnosticsDiscarded || part.RawDiagnosticsDiscarded
	total.CompatibilityStdoutRetainedBytes += part.CompatibilityStdoutRetainedBytes
	total.CompatibilityStdoutTruncated = total.CompatibilityStdoutTruncated || part.CompatibilityStdoutTruncated
	total.CompatibilityStderrRetainedBytes += part.CompatibilityStderrRetainedBytes
	total.CompatibilityStderrTruncated = total.CompatibilityStderrTruncated || part.CompatibilityStderrTruncated
	total.BuildStdoutRetainedBytes += part.BuildStdoutRetainedBytes
	total.BuildStdoutTruncated = total.BuildStdoutTruncated || part.BuildStdoutTruncated
	total.BuildStderrRetainedBytes += part.BuildStderrRetainedBytes
	total.BuildStderrTruncated = total.BuildStderrTruncated || part.BuildStderrTruncated
	total.RunnerStderrRetainedBytes += part.RunnerStderrRetainedBytes
	total.RunnerStderrTruncated = total.RunnerStderrTruncated || part.RunnerStderrTruncated
}
