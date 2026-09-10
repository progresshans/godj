//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/projectcheck/createsuperuserprotocol"
)

type createsuperuserBuildBackend struct {
	calls    int
	stages   []ProcessStage
	commands []Command
	result   ProcessResult
	before   func(ProcessStage, Command)
}

func (backend *createsuperuserBuildBackend) Execute(
	_ context.Context,
	_ <-chan struct{},
	stage ProcessStage,
	command Command,
) ProcessResult {
	if backend.before != nil {
		backend.before(stage, command)
	}
	backend.calls++
	backend.stages = append(backend.stages, stage)
	backend.commands = append(backend.commands, cloneCommand(command))
	return backend.result
}

type createsuperuserRunWriter struct {
	bytes.Buffer
	calls   int
	short   bool
	failure error
	before  func()
}

func (writer *createsuperuserRunWriter) Write(document []byte) (int, error) {
	writer.calls++
	if writer.before != nil {
		writer.before()
	}
	if writer.failure != nil {
		return 0, writer.failure
	}
	if writer.short && len(document) > 0 {
		return writer.Buffer.Write(document[:len(document)-1])
	}
	return writer.Buffer.Write(document)
}

func TestRunCreatesuperuserRejectsExactArgumentsBeforeProjectBuildOrTerminal(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	interrupt := make(chan struct{})
	close(interrupt)
	backend := &createsuperuserBuildBackend{}
	var stdout, stderr bytes.Buffer
	hookCalls := 0
	report := runCreatesuperuser(CreatesuperuserInvocation{
		Context:   ctx,
		CWD:       filepath.Join(t.TempDir(), "missing-cwd"),
		Args:      []string{"createsuperuser", "--password", "argument-secret-marker"},
		Stdin:     nil,
		Stdout:    &stdout,
		Stderr:    &stderr,
		Interrupt: interrupt,
		Backend:   backend,
	}, createsuperuserRunHooks{
		selectProject: func(string, commandArguments, *Report) (retainedProject, *Failure) {
			hookCalls++
			t.Fatal("project selection ran after invalid arguments")
			return retainedProject{}, nil
		},
		readTerminal: func(context.Context, <-chan struct{}, *os.File, io.Writer, *CreatesuperuserReport) ([]byte, []byte, *CreatesuperuserFailure) {
			hookCalls++
			t.Fatal("terminal ran after invalid arguments")
			return nil, nil, nil
		},
		executeSensitiveProcess: func(context.Context, <-chan struct{}, createsuperuserProcessCommand, []byte) createsuperuserProcessResult {
			hookCalls++
			t.Fatal("sensitive process ran after invalid arguments")
			return createsuperuserProcessResult{}
		},
	})
	want := CreatesuperuserFailure{
		Category: createsuperuserprotocol.CategoryCommand,
		Code:     createsuperuserprotocol.CodeInvalidArguments,
	}
	if hookCalls != 0 || backend.calls != 0 || report.ExitCode != 2 || !report.HasCreatesuperuserFailure ||
		report.HasCreatesuperuserResult || report.CreatesuperuserFailure != want || report.BuildCalls != 0 ||
		report.RunnerCalls != 0 || report.TerminalChecks != 0 || report.TempCreated != 0 ||
		report.AncestorDirectoriesInspected != 0 || report.DescriptorReads != 0 {
		t.Fatalf("invalid argument boundary = %+v hooks=%d backend=%d", report, hookCalls, backend.calls)
	}
	if stdout.Len() != 0 || stderr.String() != want.Category+"/"+want.Code+"\n" ||
		strings.Contains(stderr.String(), "argument-secret-marker") {
		t.Fatalf("invalid argument output stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRunCreatesuperuserOrdersBuildTerminalSensitiveOwnerCleanupAndPublication(t *testing.T) {
	t.Parallel()

	fixture := newGlobalFixture(t, 1)
	var events []string
	var requestBacking []byte
	buildBacking := []byte("discarded-build-diagnostic-marker")
	backend := &createsuperuserBuildBackend{result: ProcessResult{
		Started:      true,
		ExitCode:     0,
		DirectReaps:  1,
		Stdout:       buildBacking,
		StdoutScalar: StreamScalar{RetainedBytes: len(buildBacking)},
		StderrScalar: StreamScalar{RetainedBytes: 7},
	}}
	backend.before = func(stage ProcessStage, _ Command) {
		if stage == BuildStage {
			events = append(events, "build")
		}
	}
	responseBacking := mustCreatesuperuserRunResponse(t, createsuperuserprotocol.Response{OK: true, Created: true})
	usernameBacking := []byte("operator-run-username-marker")
	passwordBacking := []byte("operator-run-password-marker")
	var workspaceRoot string
	runnerCalls := 0
	output := &createsuperuserRunWriter{before: func() {
		events = append(events, "stdout-write")
		if _, err := os.Lstat(workspaceRoot); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("workspace still existed at public write: %v", err)
		}
	}}
	var stderr bytes.Buffer
	hooks := createsuperuserRunHooks{
		selectProject: func(cwd string, arguments commandArguments, report *Report) (retainedProject, *Failure) {
			events = append(events, "select")
			return selectProject(cwd, arguments, report)
		},
		verifyRetainedProject: func(project retainedProject) bool {
			events = append(events, "verify")
			return verifyRetainedProject(project)
		},
		createPrivateWorkspace: func(project retainedProject, environment []string, report *Report, workspaceHooks workspaceHooks) (privateWorkspace, *Failure) {
			events = append(events, "workspace")
			workspace, failure := createPrivateWorkspaceWithHooks(project, environment, report, workspaceHooks)
			workspaceRoot = workspace.root
			return workspace, failure
		},
		readTerminal: func(_ context.Context, _ <-chan struct{}, _ *os.File, _ io.Writer, report *CreatesuperuserReport) ([]byte, []byte, *CreatesuperuserFailure) {
			events = append(events, "terminal")
			report.TerminalChecks++
			report.UsernamePromptWrites++
			report.PasswordPromptWrites++
			report.ConfirmationPromptWrites++
			return usernameBacking, passwordBacking, nil
		},
		encodeRequest: func(request createsuperuserprotocol.Request) ([]byte, error) {
			events = append(events, "encode")
			if createsuperuserRunAllZero(usernameBacking) || createsuperuserRunAllZero(passwordBacking) {
				t.Fatal("terminal buffers were cleared before request encoding")
			}
			return createsuperuserprotocol.EncodeRequest(request)
		},
		executeSensitiveProcess: func(_ context.Context, _ <-chan struct{}, command createsuperuserProcessCommand, document []byte) createsuperuserProcessResult {
			events = append(events, "runner")
			runnerCalls++
			if !createsuperuserRunAllZero(usernameBacking) || !createsuperuserRunAllZero(passwordBacking) {
				t.Fatal("terminal buffers were retained at sensitive process handoff")
			}
			flatCommand := command.dir + strings.Join(command.argv, "\x00") + strings.Join(command.env, "\x00")
			if strings.Contains(flatCommand, "operator-run-username-marker") || strings.Contains(flatCommand, "operator-run-password-marker") {
				t.Fatalf("sensitive input entered command metadata: %q", flatCommand)
			}
			if command.dir != fixture.project || len(command.argv) != 2 || command.argv[1] != createsuperuserprotocol.PrivateArgument {
				t.Fatalf("sensitive command = %+v", command)
			}
			request, failure, failed := createsuperuserprotocol.DecodeRequest(document)
			if failed || failure != (CreatesuperuserFailure{}) || string(request.Username) != "operator-run-username-marker" ||
				string(request.Password) != "operator-run-password-marker" {
				t.Fatalf("sensitive request = %s failure=%+v failed=%t", request.String(), failure, failed)
			}
			requestLength := len(document)
			requestBacking = document
			request.Clear()
			clear(document)
			return createsuperuserProcessResult{
				response:             responseBacking,
				started:              true,
				exited:               true,
				exitCode:             0,
				directReaps:          1,
				stdoutScalar:         StreamScalar{RetainedBytes: len(responseBacking)},
				stderrScalar:         StreamScalar{RetainedBytes: 11},
				sigintAttempts:       3,
				sigkillAttempts:      4,
				requestWriteAttempts: 1,
				requestBytesWritten:  requestLength,
			}
		},
		closeProject: func(project *retainedProject) error {
			events = append(events, "close-project")
			return project.close()
		},
		cleanupWorkspace: func(workspace *privateWorkspace) error {
			events = append(events, "cleanup-workspace")
			return workspace.cleanup()
		},
		beforePublicPublication: func() {
			events = append(events, "before-public")
			if _, err := os.Lstat(workspaceRoot); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("workspace still existed before publication: %v", err)
			}
		},
	}
	report := runCreatesuperuser(CreatesuperuserInvocation{
		Context:     context.Background(),
		CWD:         fixture.cwd,
		Args:        []string{"createsuperuser"},
		Environment: fixture.environment,
		Stdout:      output,
		Stderr:      &stderr,
		Backend:     backend,
	}, hooks)
	wantEvents := []string{
		"select", "verify", "workspace", "verify", "build", "verify", "terminal", "verify",
		"encode", "runner", "close-project", "cleanup-workspace", "before-public", "stdout-write",
	}
	// The backend is the event source for the only generic process invocation.
	if len(events) >= 5 && events[4] != "build" {
		t.Fatalf("unexpected pre-build event sequence: %v", events)
	}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("orchestration order = %v, want %v", events, wantEvents)
	}
	if backend.calls != 1 || !reflect.DeepEqual(backend.stages, []ProcessStage{BuildStage}) || len(backend.commands) != 1 {
		t.Fatalf("generic backend calls=%d stages=%v commands=%d", backend.calls, backend.stages, len(backend.commands))
	}
	buildCommand := backend.commands[0]
	if buildCommand.Dir != fixture.project || len(buildCommand.Stdin) != 0 ||
		!reflect.DeepEqual(buildCommand.Argv[:5], []string{"go", "build", "-buildvcs=false", "-mod=readonly", "-o"}) ||
		buildCommand.Argv[len(buildCommand.Argv)-1] != "./cmd/site" {
		t.Fatalf("secret-free build command = %+v", buildCommand)
	}
	if runnerCalls != 1 || report.ExitCode != 0 || !report.HasCreatesuperuserResult || report.HasCreatesuperuserFailure ||
		!report.KnownCreated || report.BuildCalls != 1 || report.RunnerCalls != 1 || report.TerminalChecks != 1 ||
		report.DirectChildReaps != 2 || report.GroupSIGINTAttempts != 3 || report.GroupSIGKILLAttempts != 4 ||
		report.RunnerResponseWrites != 1 || report.RunnerStdoutRetainedBytes != len(responseBacking) ||
		report.RunnerStdoutTruncated || report.RunnerStderrRetainedBytes != 11 || report.RunnerStderrTruncated ||
		report.SensitiveRequestWriteAttempts != 1 || report.SensitiveRequestBytesWritten == 0 ||
		report.TempCreated != 1 || report.TempCleanupAttempts != 1 || report.CleanupFailed != 0 ||
		report.ResidualTemp != 0 || report.UserStdoutWrites != 1 || report.UserStderrWrites != 0 ||
		!report.RawDiagnosticsDiscarded {
		t.Fatalf("successful createsuperuser report = %+v runner_calls=%d", report, runnerCalls)
	}
	if output.calls != 1 || !bytes.Equal(output.Bytes(), createsuperuserprotocol.PublicSuccessDocument()) || stderr.Len() != 0 {
		t.Fatalf("public success writes=%d stdout=%q stderr=%q", output.calls, output.String(), stderr.String())
	}
	if !createsuperuserRunAllZero(buildBacking) || !createsuperuserRunAllZero(requestBacking) || !createsuperuserRunAllZero(responseBacking) ||
		!createsuperuserRunAllZero(usernameBacking) || !createsuperuserRunAllZero(passwordBacking) {
		t.Fatal("one or more owned diagnostic, response, or terminal buffers were not cleared")
	}
}

func TestRunCreatesuperuserBuildFailureNeverTouchesTerminalOrSensitiveOwner(t *testing.T) {
	t.Parallel()

	fixture := newGlobalFixture(t, 0)
	backend := &createsuperuserBuildBackend{result: ProcessResult{Started: true, ExitCode: 9, DirectReaps: 1}}
	terminalCalls := 0
	runnerCalls := 0
	var stdout, stderr bytes.Buffer
	report := runCreatesuperuser(CreatesuperuserInvocation{
		Context: context.Background(), CWD: fixture.project, Args: []string{"createsuperuser"},
		Environment: fixture.environment, Stdout: &stdout, Stderr: &stderr, Backend: backend,
	}, createsuperuserRunHooks{
		readTerminal: func(context.Context, <-chan struct{}, *os.File, io.Writer, *CreatesuperuserReport) ([]byte, []byte, *CreatesuperuserFailure) {
			terminalCalls++
			return nil, nil, nil
		},
		executeSensitiveProcess: func(context.Context, <-chan struct{}, createsuperuserProcessCommand, []byte) createsuperuserProcessResult {
			runnerCalls++
			return createsuperuserProcessResult{}
		},
	})
	want := CreatesuperuserFailure{
		Category: createsuperuserprotocol.CategoryBuild,
		Code:     createsuperuserprotocol.CodeProjectBuildFailed,
	}
	if terminalCalls != 0 || runnerCalls != 0 || backend.calls != 1 || report.ExitCode != 3 ||
		report.CreatesuperuserFailure != want || report.BuildCalls != 1 || report.RunnerCalls != 0 ||
		report.TerminalChecks != 0 || report.TempCleanupAttempts != 1 || report.UserStdoutWrites != 0 ||
		report.UserStderrWrites != 1 || stdout.Len() != 0 || stderr.String() != want.Category+"/"+want.Code+"\n" {
		t.Fatalf("build-before-terminal failure = %+v terminal=%d runner=%d stdout=%q stderr=%q", report, terminalCalls, runnerCalls, stdout.String(), stderr.String())
	}
}

func TestRunCreatesuperuserReverifiesRetainedProjectAroundTerminal(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name          string
		failVerifyAt  int
		wantTerminal  int
		wantClearedIO bool
	}{
		{name: "before terminal", failVerifyAt: 3},
		{name: "after terminal", failVerifyAt: 4, wantTerminal: 1, wantClearedIO: true},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newGlobalFixture(t, 0)
			backend := &createsuperuserBuildBackend{result: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1}}
			verifyCalls := 0
			terminalCalls := 0
			runnerCalls := 0
			username := []byte("reverify-username-marker")
			password := []byte("reverify-password-marker")
			var stdout, stderr bytes.Buffer
			report := runCreatesuperuser(CreatesuperuserInvocation{
				Context: context.Background(), CWD: fixture.project, Args: []string{"createsuperuser"},
				Environment: fixture.environment, Stdout: &stdout, Stderr: &stderr, Backend: backend,
			}, createsuperuserRunHooks{
				verifyRetainedProject: func(project retainedProject) bool {
					verifyCalls++
					return verifyCalls != test.failVerifyAt && verifyRetainedProject(project)
				},
				readTerminal: func(context.Context, <-chan struct{}, *os.File, io.Writer, *CreatesuperuserReport) ([]byte, []byte, *CreatesuperuserFailure) {
					terminalCalls++
					return username, password, nil
				},
				executeSensitiveProcess: func(context.Context, <-chan struct{}, createsuperuserProcessCommand, []byte) createsuperuserProcessResult {
					runnerCalls++
					return createsuperuserProcessResult{}
				},
			})
			want := CreatesuperuserFailure{
				Category: createsuperuserprotocol.CategorySelection,
				Code:     createsuperuserprotocol.CodeProjectSelectionFailed,
			}
			if verifyCalls != test.failVerifyAt || terminalCalls != test.wantTerminal || runnerCalls != 0 ||
				backend.calls != 1 || report.ExitCode != 3 || report.CreatesuperuserFailure != want ||
				report.BuildCalls != 1 || report.RunnerCalls != 0 || stdout.Len() != 0 ||
				stderr.String() != want.Category+"/"+want.Code+"\n" {
				t.Fatalf("%s reverify = %+v verifies=%d terminal=%d runner=%d stdout=%q stderr=%q", test.name, report, verifyCalls, terminalCalls, runnerCalls, stdout.String(), stderr.String())
			}
			if test.wantClearedIO && (!createsuperuserRunAllZero(username) || !createsuperuserRunAllZero(password)) {
				t.Fatal("post-terminal retained-project failure did not clear terminal buffers")
			}
		})
	}
}

func TestRunCreatesuperuserStrictResponseMappingClearingAndNoRetry(t *testing.T) {
	t.Parallel()

	knownLinked := createsuperuserprotocol.Failure{
		Category:     createsuperuserprotocol.CategoryBackend,
		Code:         createsuperuserprotocol.CodeBackendCloseFailed,
		KnownCreated: true,
	}
	knownWire := mustCreatesuperuserRunResponse(t, createsuperuserprotocol.Response{Failure: knownLinked})
	knownBacking := knownWire
	knownProcess := createsuperuserProcessResult{
		response: knownBacking, started: true, exited: true, exitCode: 0, directReaps: 1,
		stdoutScalar:         StreamScalar{RetainedBytes: len(knownBacking)},
		requestWriteAttempts: 1,
	}
	report, stdout, stderr, buildCalls, runnerCalls := runCreatesuperuserScripted(t, knownProcess, nil, nil)
	wantKnown := CreatesuperuserFailure{
		Category: createsuperuserprotocol.CategoryBackend,
		Code:     createsuperuserprotocol.CodeOperatorCreatedBackendCleanupFailed,
	}
	if report.ExitCode != 3 || report.CreatesuperuserFailure != wantKnown || !report.KnownCreated ||
		report.HasCreatesuperuserResult || buildCalls != 1 || runnerCalls != 1 || stdout.Len() != 0 ||
		stderr.String() != wantKnown.Category+"/"+wantKnown.Code+"\n" || !createsuperuserRunAllZero(knownBacking) {
		t.Fatalf("known-created linked mapping = %+v build=%d runner=%d stdout=%q stderr=%q wire=%q", report, buildCalls, runnerCalls, stdout.String(), stderr.String(), knownBacking)
	}

	wantUnknown := CreatesuperuserFailure{
		Category: createsuperuserprotocol.CategoryProcess,
		Code:     createsuperuserprotocol.CodeOperatorProvisionOutcomeUnknown,
	}
	successWire := mustCreatesuperuserRunResponse(t, createsuperuserprotocol.Response{OK: true, Created: true})
	for _, test := range []struct {
		name                string
		process             createsuperuserProcessResult
		wantStdoutTruncated bool
		wantStderrTruncated bool
	}{
		{
			name: "stdout overflow terminated child with nonzero exit",
			process: createsuperuserProcessResult{
				started:      true,
				exitCode:     137,
				directReaps:  1,
				stdoutScalar: StreamScalar{RetainedBytes: createsuperuserprotocol.MaxResponseBytes, Truncated: true},
			},
			wantStdoutTruncated: true,
		},
		{
			name: "stderr overflow discarded valid response with zero exit",
			process: createsuperuserProcessResult{
				started:      true,
				exitCode:     0,
				directReaps:  1,
				stdoutScalar: StreamScalar{RetainedBytes: len(successWire)},
				stderrScalar: StreamScalar{RetainedBytes: maxDiagnosticBytes, Truncated: true},
			},
			wantStderrTruncated: true,
		},
		{
			name: "stderr overflow terminated child with nonzero exit",
			process: createsuperuserProcessResult{
				started:      true,
				exitCode:     137,
				directReaps:  1,
				stdoutScalar: StreamScalar{RetainedBytes: len(successWire)},
				stderrScalar: StreamScalar{RetainedBytes: maxDiagnosticBytes, Truncated: true},
			},
			wantStderrTruncated: true,
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			report, stdout, stderr, buildCalls, runnerCalls := runCreatesuperuserScripted(t, test.process, nil, nil)
			if report.ExitCode != 3 || report.CreatesuperuserFailure != wantUnknown || report.KnownCreated ||
				buildCalls != 1 || runnerCalls != 1 || report.RunnerStdoutTruncated != test.wantStdoutTruncated ||
				report.RunnerStderrTruncated != test.wantStderrTruncated || stdout.Len() != 0 ||
				stderr.String() != wantUnknown.Category+"/"+wantUnknown.Code+"\n" {
				t.Fatalf("bounded response precedence = %+v build=%d runner=%d stdout=%q stderr=%q", report, buildCalls, runnerCalls, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunCreatesuperuserPreservesKnownCreatedAndUnknownLostResponseOutcomes(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		exitCode int
		want     CreatesuperuserFailure
	}{
		{
			name:     "reserved output exit proves a known create",
			exitCode: createsuperuserprotocol.KnownCreatedResponseFailureExitCode,
			want: CreatesuperuserFailure{
				Category: createsuperuserprotocol.CategoryInternal,
				Code:     createsuperuserprotocol.CodeOperatorCreatedOutputFailed,
			},
		},
		{
			name:     "reserved backend cleanup exit preserves its subtype",
			exitCode: createsuperuserprotocol.KnownCreatedBackendCleanupResponseFailureExitCode,
			want: CreatesuperuserFailure{
				Category: createsuperuserprotocol.CategoryBackend,
				Code:     createsuperuserprotocol.CodeOperatorCreatedBackendCleanupFailed,
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			process := createsuperuserProcessResult{
				started:         true,
				exited:          true,
				exitCode:        test.exitCode,
				directReaps:     1,
				sigkillAttempts: 1,
				stderrScalar:    StreamScalar{RetainedBytes: maxDiagnosticBytes, Truncated: true},
				cleanupFailed:   true,
				failure: &CreatesuperuserFailure{
					Category: createsuperuserprotocol.CategoryProcess,
					Code:     createsuperuserprotocol.CodeProjectInterrupted,
				},
			}
			report, stdout, stderr, buildCalls, runnerCalls := runCreatesuperuserScripted(t, process, nil, nil)
			if report.ExitCode != 3 || report.CreatesuperuserFailure != test.want || !report.KnownCreated ||
				report.HasCreatesuperuserResult || buildCalls != 1 || runnerCalls != 1 || stdout.Len() != 0 ||
				stderr.String() != test.want.Category+"/"+test.want.Code+"\n" {
				t.Fatalf("known-created lost response = %+v build=%d runner=%d stdout=%q stderr=%q", report, buildCalls, runnerCalls, stdout.String(), stderr.String())
			}
		})
	}

	for _, test := range []struct {
		name    string
		process createsuperuserProcessResult
	}{
		{
			name: "nonzero child after complete request",
			process: createsuperuserProcessResult{
				started:     true,
				exited:      true,
				exitCode:    1,
				directReaps: 1,
			},
		},
		{
			name: "canceled child after complete request",
			process: createsuperuserProcessResult{
				started:     true,
				exitCode:    -1,
				directReaps: 1,
				failure: &CreatesuperuserFailure{
					Category: createsuperuserprotocol.CategoryProcess,
					Code:     createsuperuserprotocol.CodeProjectCanceled,
				},
			},
		},
		{
			name: "signal-shaped reserved number is not a normal known-created exit",
			process: createsuperuserProcessResult{
				started:     true,
				exited:      false,
				exitCode:    createsuperuserprotocol.KnownCreatedResponseFailureExitCode,
				directReaps: 1,
			},
		},
		{
			name: "zero child with malformed terminal response",
			process: createsuperuserProcessResult{
				response:     []byte(`{"protocol_version":1,"status":"ok"}`),
				started:      true,
				exited:       true,
				exitCode:     0,
				directReaps:  1,
				stdoutScalar: StreamScalar{RetainedBytes: len(`{"protocol_version":1,"status":"ok"}`)},
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			report, stdout, stderr, _, _ := runCreatesuperuserScripted(t, test.process, nil, nil)
			want := CreatesuperuserFailure{
				Category: createsuperuserprotocol.CategoryProcess,
				Code:     createsuperuserprotocol.CodeOperatorProvisionOutcomeUnknown,
			}
			if report.ExitCode != 3 || report.CreatesuperuserFailure != want || report.KnownCreated ||
				report.HasCreatesuperuserResult || stdout.Len() != 0 ||
				stderr.String() != want.Category+"/"+want.Code+"\n" {
				t.Fatalf("unknown lost response = %+v stdout=%q stderr=%q", report, stdout.String(), stderr.String())
			}
		})
	}
}

func TestClassifyCreatesuperuserProcessFullAndPartialTransportFailureOwnership(t *testing.T) {
	t.Parallel()

	const expectedRequestBytes = 97
	for _, test := range []struct {
		name    string
		written int
		want    CreatesuperuserFailure
	}{
		{
			name:    "full write with transport error is outcome unknown",
			written: expectedRequestBytes,
			want:    createsuperuserProvisionOutcomeUnknownFailure(),
		},
		{
			name:    "partial write cannot dispatch a strict frame",
			written: expectedRequestBytes - 1,
			want: CreatesuperuserFailure{
				Category: createsuperuserprotocol.CategoryProcess,
				Code:     createsuperuserprotocol.CodeSensitiveInputTransportFailed,
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			process := createsuperuserProcessResult{
				started:              true,
				exitCode:             -1,
				directReaps:          1,
				requestWriteAttempts: 1,
				requestBytesWritten:  test.written,
				failure: &CreatesuperuserFailure{
					Category: createsuperuserprotocol.CategoryProcess,
					Code:     createsuperuserprotocol.CodeSensitiveInputTransportFailed,
				},
			}
			failure := classifyCreatesuperuserProcess(process, expectedRequestBytes)
			if failure == nil || *failure != test.want {
				t.Fatalf("transport ownership = %+v, want %+v", failure, test.want)
			}
		})
	}
}

func TestClassifyCreatesuperuserProcessRequiresHonestTerminalScalars(t *testing.T) {
	t.Parallel()

	response := mustCreatesuperuserRunResponse(t, createsuperuserprotocol.Response{OK: true, Created: true})
	expectedRequestBytes := 97
	honest := createsuperuserProcessResult{
		response:             response,
		started:              true,
		exited:               true,
		exitCode:             0,
		directReaps:          1,
		stdoutScalar:         StreamScalar{RetainedBytes: len(response)},
		requestWriteAttempts: 1,
		requestBytesWritten:  expectedRequestBytes,
	}
	if failure := classifyCreatesuperuserProcess(honest, expectedRequestBytes); failure != nil {
		t.Fatalf("honest terminal scalars were rejected: %+v", failure)
	}

	tests := []struct {
		name   string
		mutate func(*createsuperuserProcessResult) int
		want   CreatesuperuserFailure
	}{
		{
			name: "missing direct reap",
			mutate: func(process *createsuperuserProcessResult) int {
				process.directReaps = 0
				return expectedRequestBytes
			},
			want: createsuperuserInternalFailure(),
		},
		{
			name: "duplicate direct reap",
			mutate: func(process *createsuperuserProcessResult) int {
				process.directReaps = 2
				return expectedRequestBytes
			},
			want: createsuperuserInternalFailure(),
		},
		{
			name: "missing request write",
			mutate: func(process *createsuperuserProcessResult) int {
				process.requestWriteAttempts = 0
				return expectedRequestBytes
			},
			want: createsuperuserInternalFailure(),
		},
		{
			name: "duplicate request write",
			mutate: func(process *createsuperuserProcessResult) int {
				process.requestWriteAttempts = 2
				return expectedRequestBytes
			},
			want: createsuperuserInternalFailure(),
		},
		{
			name: "partial request write",
			mutate: func(process *createsuperuserProcessResult) int {
				process.requestBytesWritten--
				return expectedRequestBytes
			},
			want: createsuperuserInternalFailure(),
		},
		{
			name: "response retention mismatch",
			mutate: func(process *createsuperuserProcessResult) int {
				process.stdoutScalar.RetainedBytes--
				return expectedRequestBytes
			},
			want: createsuperuserInternalFailure(),
		},
		{
			name: "missing expected request length",
			mutate: func(*createsuperuserProcessResult) int {
				return 0
			},
			want: createsuperuserInternalFailure(),
		},
		{
			name: "cleanup failure",
			mutate: func(process *createsuperuserProcessResult) int {
				process.cleanupFailed = true
				return expectedRequestBytes
			},
			want: createsuperuserProvisionOutcomeUnknownFailure(),
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			process := honest
			expected := test.mutate(&process)
			failure := classifyCreatesuperuserProcess(process, expected)
			if failure == nil || *failure != test.want {
				t.Fatalf("scalar failure = %+v, want %+v", failure, test.want)
			}
		})
	}
}

func TestRunCreatesuperuserKnownCreatedWorkspaceAndOutputFailures(t *testing.T) {
	t.Parallel()

	t.Run("outer cleanup replaces success before publication", func(t *testing.T) {
		t.Parallel()
		process := createsuperuserProcessResult{
			started:     true,
			exited:      true,
			exitCode:    createsuperuserprotocol.KnownCreatedResponseFailureExitCode,
			directReaps: 1,
		}
		cleanupCalls := 0
		cleanup := func(workspace *privateWorkspace) error {
			cleanupCalls++
			if err := workspace.cleanup(); err != nil {
				t.Fatal(err)
			}
			return errors.New("outer-cleanup-secret-marker")
		}
		report, stdout, stderr, buildCalls, runnerCalls := runCreatesuperuserScripted(t, process, cleanup, nil)
		want := CreatesuperuserFailure{
			Category: createsuperuserprotocol.CategoryProcess,
			Code:     createsuperuserprotocol.CodeOperatorCreatedWorkspaceCleanupFailed,
		}
		if cleanupCalls != 1 || report.ExitCode != 3 || report.CreatesuperuserFailure != want ||
			!report.KnownCreated || report.HasCreatesuperuserResult || report.CleanupFailed != 1 ||
			report.ResidualTemp != 1 || buildCalls != 1 || runnerCalls != 1 || stdout.Len() != 0 ||
			stderr.String() != want.Category+"/"+want.Code+"\n" || strings.Contains(stderr.String(), "secret-marker") {
			t.Fatalf("known-created workspace cleanup = %+v cleanup=%d build=%d runner=%d stdout=%q stderr=%q", report, cleanupCalls, buildCalls, runnerCalls, stdout.String(), stderr.String())
		}
	})

	for _, test := range []struct {
		name        string
		writer      *createsuperuserRunWriter
		wantBytes   int
		wantPartial int
	}{
		{
			name:        "short public success write is terminal known-created failure",
			writer:      &createsuperuserRunWriter{short: true},
			wantBytes:   len(createsuperuserprotocol.PublicSuccessDocument()) - 1,
			wantPartial: 1,
		},
		{
			name:      "errored public success write is terminal known-created failure",
			writer:    &createsuperuserRunWriter{failure: errors.New("output-secret-marker")},
			wantBytes: 0,
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			wire := mustCreatesuperuserRunResponse(t, createsuperuserprotocol.Response{OK: true, Created: true})
			process := createsuperuserProcessResult{
				response: wire, started: true, exited: true, exitCode: 0, directReaps: 1,
				stdoutScalar: StreamScalar{RetainedBytes: len(wire)},
			}
			report, _, stderr, buildCalls, runnerCalls := runCreatesuperuserScripted(t, process, nil, test.writer)
			want := CreatesuperuserFailure{
				Category: createsuperuserprotocol.CategoryInternal,
				Code:     createsuperuserprotocol.CodeOperatorCreatedOutputFailed,
			}
			if report.ExitCode != 3 || report.CreatesuperuserFailure != want || !report.KnownCreated ||
				report.HasCreatesuperuserResult || report.UserStdoutWrites != 1 || report.PartialStdoutWrites != test.wantPartial ||
				report.UserStderrWrites != 1 || test.writer.calls != 1 || test.writer.Len() != test.wantBytes ||
				stderr.String() != want.Category+"/"+want.Code+"\n" || buildCalls != 1 || runnerCalls != 1 {
				t.Fatalf("known-created output failure = %+v writes=%d/%d build=%d runner=%d stderr=%q", report, test.writer.calls, test.writer.Len(), buildCalls, runnerCalls, stderr.String())
			}
		})
	}
}

func TestCreatesuperuserCancellationInterruptAndCleanupPrecedence(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	canceled := createsuperuserBarrier(CreatesuperuserInvocation{Context: ctx}, nil)
	if canceled == nil || canceled.Category != createsuperuserprotocol.CategoryProcess || canceled.Code != createsuperuserprotocol.CodeProjectCanceled {
		t.Fatalf("cancellation barrier = %+v", canceled)
	}
	interrupt := make(chan struct{})
	close(interrupt)
	interrupted := createsuperuserBarrier(CreatesuperuserInvocation{Context: ctx, Interrupt: interrupt}, nil)
	if interrupted == nil || interrupted.Code != createsuperuserprotocol.CodeProjectInterrupted {
		t.Fatalf("interrupt precedence = %+v", interrupted)
	}
	cleanup := createsuperuserCleanupFailure()
	if got := createsuperuserBarrier(CreatesuperuserInvocation{Context: ctx, Interrupt: interrupt}, &cleanup); got != &cleanup {
		t.Fatalf("terminal cleanup was replaced by cancellation/interrupt: %+v", got)
	}
	terminalRestore := CreatesuperuserFailure{
		Category: createsuperuserprotocol.CategoryInput,
		Code:     createsuperuserprotocol.CodeTerminalStateFailed,
	}
	if got := createsuperuserBarrier(CreatesuperuserInvocation{Context: ctx, Interrupt: interrupt}, &terminalRestore); got != &terminalRestore {
		t.Fatalf("terminal restoration failure was replaced by cancellation/interrupt: %+v", got)
	}
	for _, primary := range []CreatesuperuserFailure{*canceled, *interrupted} {
		got := combineCreatesuperuserCleanup(&primary, true)
		if got == nil || *got != cleanup {
			t.Fatalf("cleanup did not replace cancellation/interrupt %+v: %+v", primary, got)
		}
	}
	logical := CreatesuperuserFailure{
		Category: createsuperuserprotocol.CategoryState,
		Code:     createsuperuserprotocol.CodeCredentialAlreadyExists,
	}
	if got := combineCreatesuperuserCleanup(&logical, true); got != &logical {
		t.Fatalf("cleanup replaced closed logical outcome: %+v", got)
	}
}

func runCreatesuperuserScripted(
	t *testing.T,
	process createsuperuserProcessResult,
	cleanup func(*privateWorkspace) error,
	output io.Writer,
) (CreatesuperuserReport, *bytes.Buffer, *bytes.Buffer, int, int) {
	t.Helper()
	fixture := newGlobalFixture(t, 0)
	backend := &createsuperuserBuildBackend{result: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1}}
	var stdout, stderr bytes.Buffer
	if output == nil {
		output = &stdout
	}
	runnerCalls := 0
	hooks := createsuperuserRunHooks{
		readTerminal: func(context.Context, <-chan struct{}, *os.File, io.Writer, *CreatesuperuserReport) ([]byte, []byte, *CreatesuperuserFailure) {
			return []byte("scripted-operator"), []byte("scripted-password-marker"), nil
		},
		executeSensitiveProcess: func(_ context.Context, _ <-chan struct{}, _ createsuperuserProcessCommand, request []byte) createsuperuserProcessResult {
			runnerCalls++
			process.requestWriteAttempts = 1
			process.requestBytesWritten = len(request)
			clear(request)
			return process
		},
		cleanupWorkspace: cleanup,
	}
	report := runCreatesuperuser(CreatesuperuserInvocation{
		Context: context.Background(), CWD: fixture.project, Args: []string{"createsuperuser"},
		Environment: fixture.environment, Stdout: output, Stderr: &stderr, Backend: backend,
	}, hooks)
	return report, &stdout, &stderr, backend.calls, runnerCalls
}

func mustCreatesuperuserRunResponse(t *testing.T, response createsuperuserprotocol.Response) []byte {
	t.Helper()
	document, err := createsuperuserprotocol.EncodeResponse(response)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func createsuperuserRunAllZero(document []byte) bool {
	for _, value := range document {
		if value != 0 {
			return false
		}
	}
	return true
}
