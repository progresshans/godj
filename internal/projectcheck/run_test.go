//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/projectcheck/protocol"
)

type scriptedBackend struct {
	commands []Command
	build    ProcessResult
	runner   ProcessResult
}

func (backend *scriptedBackend) Execute(_ context.Context, _ <-chan struct{}, stage ProcessStage, command Command) ProcessResult {
	backend.commands = append(backend.commands, cloneCommand(command))
	if stage == BuildStage {
		return backend.build
	}
	return backend.runner
}

type shortWriter struct {
	bytes.Buffer
}

func (writer *shortWriter) Write(payload []byte) (int, error) {
	if len(payload) < 2 {
		return writer.Buffer.Write(payload)
	}
	return writer.Buffer.Write(payload[:len(payload)/2])
}

func TestRunSuccessUsesClosedCommandsPrivateEnvironmentAndSinglePublication(t *testing.T) {
	t.Parallel()
	fixture := newGlobalFixture(t, 3)
	wire, err := protocol.EncodeResponse(protocol.Response{OK: true, Result: protocol.Result{
		SourceCount: 0, DefinitionCount: 0, DefinitionSetDigest: protocol.EmptySetDigest,
	}})
	if err != nil {
		t.Fatal(err)
	}
	backend := &scriptedBackend{
		build: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1},
		runner: ProcessResult{
			Started: true, ExitCode: 0, DirectReaps: 1, Stdout: wire,
			StdoutScalar: StreamScalar{RetainedBytes: len(wire)},
		},
	}
	var stdout, stderr bytes.Buffer
	report := Run(Invocation{
		Context: context.Background(), CWD: fixture.cwd, Args: []string{"migrations", "check"},
		Environment: fixture.environment, Stdout: &stdout, Stderr: &stderr, Backend: backend,
	})
	if report.ExitCode != 0 || report.HasFailure || !report.HasResult || report.BuildCalls != 1 || report.RunnerCalls != 1 || report.RunnerResponseWrites != 1 || report.AncestorDirectoriesInspected != 4 || report.DescriptorReads != 1 || report.UserStdoutWrites != 1 || report.UserStderrWrites != 0 || report.DirectChildReaps != 2 {
		t.Fatalf("success report = %+v", report)
	}
	wantOutput := `{"source_count":0,"definition_count":0,"definition_set_digest":"` + protocol.EmptySetDigest + `"}` + "\n"
	if stdout.String() != wantOutput || stderr.Len() != 0 {
		t.Fatalf("public output stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if len(backend.commands) != 2 {
		t.Fatalf("commands = %d", len(backend.commands))
	}
	build := backend.commands[0]
	wantBuildPrefix := []string{"go", "build", "-buildvcs=false", "-mod=readonly", "-o"}
	if !reflect.DeepEqual(build.Argv[:5], wantBuildPrefix) || build.Argv[len(build.Argv)-1] != "./cmd/site" || build.Dir != fixture.project {
		t.Fatalf("build command = %+v", build)
	}
	runner := backend.commands[1]
	if len(runner.Argv) != 2 || runner.Argv[1] != protocol.PrivateArgument || runner.Dir != fixture.project || !bytes.Equal(runner.Stdin, protocol.RequestDocument()) {
		t.Fatalf("runner command = %+v", runner)
	}
	environment := environmentValues(build.Env)
	for _, key := range []string{"TMPDIR", "GOTMPDIR", "GOCACHE", "GOMODCACHE", "HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "TEST_TELEMETRY_DIR"} {
		if !sameOrDescendant(environment[key], filepath.Dir(runner.Argv[0])) {
			t.Fatalf("%s=%q is not private", key, environment[key])
		}
		if _, err := os.Stat(environment[key]); !os.IsNotExist(err) {
			t.Fatalf("private directory %s remained: %v", environment[key], err)
		}
	}
	if environment["GOWORK"] != "off" || environment["GOTOOLCHAIN"] != "local" || environment["GOENV"] != "off" || environment["GOFLAGS"] != "-modcacherw" || environment["GOCACHEPROG"] != "" || environment["NETRC"] != "/explicit/netrc" || environment["GOTELEMETRY"] != "ambient" {
		t.Fatalf("child environment = %#v", environment)
	}
	if report.TempCreated != 1 || report.TempCleanupAttempts != 1 || report.CleanupFailed != 0 || report.ResidualTemp != 0 || !report.RawDiagnosticsDiscarded {
		t.Fatalf("cleanup report = %+v", report)
	}
}

func TestRunRunnerCleanupFailurePrecedesResponseParsing(t *testing.T) {
	t.Parallel()
	fixture := newGlobalFixture(t, 0)
	wire, err := protocol.EncodeResponse(protocol.Response{OK: true, Result: protocol.Result{
		SourceCount: 0, DefinitionCount: 0, DefinitionSetDigest: protocol.EmptySetDigest,
	}})
	if err != nil {
		t.Fatal(err)
	}
	responseBacking := wire
	backend := &scriptedBackend{
		build: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1},
		runner: ProcessResult{
			Started: true, ExitCode: 0, DirectReaps: 1, CleanupFailed: true,
			Stdout: responseBacking, StdoutScalar: StreamScalar{RetainedBytes: len(responseBacking)},
		},
	}
	var stdout, stderr bytes.Buffer
	report := Run(Invocation{
		Context: context.Background(), CWD: fixture.project, Args: []string{"migrations", "check"},
		Environment: fixture.environment, Stdout: &stdout, Stderr: &stderr, Backend: backend,
	})
	if report.ExitCode != 3 || report.HasResult || report.Failure != failure(protocol.CategoryProcess, protocol.CodeProjectCleanupFailed) || report.CleanupFailed != 1 || stdout.Len() != 0 || stderr.String() != protocol.CategoryProcess+"/"+protocol.CodeProjectCleanupFailed+"\n" {
		t.Fatalf("runner cleanup precedence=%+v stdout=%q stderr=%q", report, stdout.String(), stderr.String())
	}
	if !bytes.Equal(responseBacking, make([]byte, len(responseBacking))) {
		t.Fatalf("runner response was not zeroized: %q", responseBacking)
	}
}

func TestRunPrecedenceFailuresAndRawDiscard(t *testing.T) {
	t.Parallel()
	var invalidStdout, invalidStderr bytes.Buffer
	invalid := Run(Invocation{
		Context: context.Background(), CWD: filepath.Join(t.TempDir(), "absent"), Args: []string{"bad"},
		Stdout: &invalidStdout, Stderr: &invalidStderr,
		Backend: backendFunc(func(context.Context, <-chan struct{}, ProcessStage, Command) ProcessResult {
			t.Fatal("backend called after invalid arguments")
			return ProcessResult{}
		}),
	})
	if invalid.ExitCode != 2 || invalid.Failure.Code != protocol.CodeInvalidArguments || invalid.AncestorDirectoriesInspected != 0 || invalid.BuildCalls != 0 || invalidStderr.String() != protocol.CategoryCommand+"/"+protocol.CodeInvalidArguments+"\n" {
		t.Fatalf("invalid arguments = %+v stderr=%q", invalid, invalidStderr.String())
	}

	fixture := newGlobalFixture(t, 0)
	buildRaw := []byte("private compiler path")
	backend := &scriptedBackend{build: ProcessResult{
		Started: true, ExitCode: 1, DirectReaps: 1, Stdout: buildRaw,
		StdoutScalar: StreamScalar{RetainedBytes: len(buildRaw)},
		StderrScalar: StreamScalar{RetainedBytes: 12},
	}}
	var stdout, stderr bytes.Buffer
	failed := Run(Invocation{
		Context: context.Background(), CWD: fixture.project, Args: []string{"migrations", "check"},
		Environment: fixture.environment, Stdout: &stdout, Stderr: &stderr, Backend: backend,
	})
	if failed.ExitCode != 3 || failed.Failure.Code != protocol.CodeProjectBuildFailed || failed.BuildCalls != 1 || failed.RunnerCalls != 0 || stdout.Len() != 0 || strings.Contains(stderr.String(), "compiler") {
		t.Fatalf("build failure = %+v stdout=%q stderr=%q", failed, stdout.String(), stderr.String())
	}
	if !bytes.Equal(buildRaw, make([]byte, len(buildRaw))) {
		t.Fatalf("build raw bytes were not zeroized: %q", buildRaw)
	}
}

func TestRunInvalidResponseAndShortPublication(t *testing.T) {
	t.Parallel()
	fixture := newGlobalFixture(t, 0)
	invalidWire := []byte(`{"protocol_version":1,"status":"ok","status":"ok","result":{"source_count":0,"definition_count":0,"definition_set_digest":"` + protocol.EmptySetDigest + `"}}`)
	backend := &scriptedBackend{
		build:  ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1},
		runner: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, Stdout: invalidWire, StdoutScalar: StreamScalar{RetainedBytes: len(invalidWire)}},
	}
	var stdout, stderr bytes.Buffer
	report := Run(Invocation{Context: context.Background(), CWD: fixture.project, Args: []string{"migrations", "check"}, Environment: fixture.environment, Stdout: &stdout, Stderr: &stderr, Backend: backend})
	if report.ExitCode != 3 || report.Failure.Code != protocol.CodeInvalidProjectRunnerResponse || report.RunnerResponseWrites != 1 || stdout.Len() != 0 {
		t.Fatalf("invalid response = %+v", report)
	}
	if !bytes.Equal(invalidWire, make([]byte, len(invalidWire))) {
		t.Fatalf("runner raw bytes were not zeroized")
	}

	wire, _ := protocol.EncodeResponse(protocol.Response{OK: true, Result: protocol.Result{DefinitionSetDigest: protocol.EmptySetDigest}})
	short := &shortWriter{}
	backend = &scriptedBackend{
		build:  ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1},
		runner: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, Stdout: wire, StdoutScalar: StreamScalar{RetainedBytes: len(wire)}},
	}
	report = Run(Invocation{Context: context.Background(), CWD: fixture.project, Args: []string{"migrations", "check"}, Environment: fixture.environment, Stdout: short, Stderr: io.Discard, Backend: backend})
	if report.ExitCode != 3 || !report.HasFailure || report.Failure.Code != protocol.CodeProjectInternalError || report.PartialStdoutWrites != 1 || report.UserStdoutWrites != 1 {
		t.Fatalf("short publication = %+v", report)
	}
}

func TestRunnerTransportFailurePrecedesValidLookingWireAndOverflow(t *testing.T) {
	t.Parallel()
	fixture := newGlobalFixture(t, 0)
	wire, _ := protocol.EncodeResponse(protocol.Response{OK: true, Result: protocol.Result{DefinitionSetDigest: protocol.EmptySetDigest}})
	backend := &scriptedBackend{
		build: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1},
		runner: ProcessResult{
			Started: true, ExitCode: 7, DirectReaps: 1, Stdout: wire,
			StdoutScalar: StreamScalar{RetainedBytes: len(wire), Truncated: true},
		},
	}
	var stdout, stderr bytes.Buffer
	report := Run(Invocation{
		Context: context.Background(), CWD: fixture.project, Args: []string{"migrations", "check"}, Environment: fixture.environment,
		Stdout: &stdout, Stderr: &stderr, Backend: backend,
	})
	if report.ExitCode != 3 || report.Failure.Code != protocol.CodeProjectRunnerFailed || report.RunnerResponseWrites != 1 || !bytes.Equal(wire, make([]byte, len(wire))) {
		t.Fatalf("nonzero transport precedence = %+v wire=%q", report, wire)
	}
}

func TestRunCancellationAfterWorkspaceCreationAlwaysCleans(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		wantCode string
		wantExit int
		invoke   func(context.CancelFunc, chan struct{})
	}{
		{name: "context", wantCode: protocol.CodeProjectCanceled, wantExit: 3, invoke: func(cancel context.CancelFunc, _ chan struct{}) { cancel() }},
		{name: "interrupt", wantCode: protocol.CodeProjectInterrupted, wantExit: 130, invoke: func(_ context.CancelFunc, interrupt chan struct{}) { close(interrupt) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newGlobalFixture(t, 0)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			interrupt := make(chan struct{})
			backend := backendFunc(func(context.Context, <-chan struct{}, ProcessStage, Command) ProcessResult {
				test.invoke(cancel, interrupt)
				return ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1}
			})
			var stdout, stderr bytes.Buffer
			report := Run(Invocation{
				Context: ctx, CWD: fixture.project, Args: []string{"migrations", "check"}, Environment: fixture.environment,
				Stdout: &stdout, Stderr: &stderr, Interrupt: interrupt, Backend: backend,
			})
			if report.ExitCode != test.wantExit || report.Failure.Code != test.wantCode || report.TempCreated != 1 || report.TempCleanupAttempts != 1 || report.CleanupFailed != 0 || report.ResidualTemp != 0 || report.BuildCalls != 1 || report.RunnerCalls != 0 {
				t.Fatalf("barrier cancellation = %+v", report)
			}
		})
	}
}

type stagedCancellationContext struct {
	context.Context
	cancelAt int
	checks   int
}

func (ctx *stagedCancellationContext) Err() error {
	ctx.checks++
	if ctx.checks >= ctx.cancelAt {
		return context.Canceled
	}
	return nil
}

func TestRunChecksCancellationImmediatelyBeforeEachStage(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		cancelAt   int
		wantBuild  int
		wantRunner int
	}{
		{name: "before build", cancelAt: 4, wantBuild: 0, wantRunner: 0},
		{name: "before runner", cancelAt: 6, wantBuild: 1, wantRunner: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newGlobalFixture(t, 0)
			ctx := &stagedCancellationContext{Context: context.Background(), cancelAt: test.cancelAt}
			backend := &scriptedBackend{build: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1}}
			var stdout, stderr bytes.Buffer
			report := Run(Invocation{
				Context: ctx, CWD: fixture.project, Args: []string{"migrations", "check"}, Environment: fixture.environment,
				Stdout: &stdout, Stderr: &stderr, Backend: backend,
			})
			if report.ExitCode != 3 || !report.HasFailure || report.Failure.Code != protocol.CodeProjectCanceled || report.BuildCalls != test.wantBuild || report.RunnerCalls != test.wantRunner || len(backend.commands) != test.wantBuild || report.TempCreated != 1 || report.TempCleanupAttempts != 1 || report.CleanupFailed != 0 || report.ResidualTemp != 0 || stdout.Len() != 0 {
				t.Fatalf("stage barrier report = %+v commands=%d stdout=%q stderr=%q checks=%d", report, len(backend.commands), stdout.String(), stderr.String(), ctx.checks)
			}
		})
	}
}

func TestPrivateWorkspacePhysicalBoundariesAndDefault(t *testing.T) {
	t.Run("captured TMPDIR absence uses platform default and live paths are 0700", func(t *testing.T) {
		fixture := newGlobalFixture(t, 0)
		environment := withoutEnvironmentKey(fixture.environment, "TMPDIR")
		wire, err := protocol.EncodeResponse(protocol.Response{OK: true, Result: protocol.Result{DefinitionSetDigest: protocol.EmptySetDigest}})
		if err != nil {
			t.Fatal(err)
		}
		backend := backendFunc(func(_ context.Context, _ <-chan struct{}, stage ProcessStage, command Command) ProcessResult {
			if stage == BuildStage {
				root := filepath.Dir(command.Argv[5])
				paths := []string{root}
				values := environmentValues(command.Env)
				for _, key := range []string{"TMPDIR", "GOTMPDIR", "GOCACHE", "GOMODCACHE", "HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "TEST_TELEMETRY_DIR"} {
					paths = append(paths, values[key])
				}
				for _, path := range paths {
					info, statErr := os.Stat(path)
					if statErr != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
						t.Fatalf("private path %q mode=%v err=%v", path, info, statErr)
					}
				}
				return ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1}
			}
			return ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, Stdout: append([]byte(nil), wire...), StdoutScalar: StreamScalar{RetainedBytes: len(wire)}}
		})
		var stdout, stderr bytes.Buffer
		report := Run(Invocation{Context: context.Background(), CWD: fixture.project, Args: []string{"migrations", "check"}, Environment: environment, Stdout: &stdout, Stderr: &stderr, Backend: backend})
		if report.ExitCode != 0 || !report.HasResult || report.TempCreated != 1 || report.TempCleanupAttempts != 1 || report.CleanupFailed != 0 || report.BuildCalls != 1 || report.RunnerCalls != 1 || stderr.Len() != 0 {
			t.Fatalf("default temp report = %+v stdout=%q stderr=%q", report, stdout.String(), stderr.String())
		}
	})

	t.Run("protected roots may be configured through a physical symlink", func(t *testing.T) {
		fixture := newGlobalFixture(t, 0)
		values := environmentValues(fixture.environment)
		link := filepath.Join(filepath.Dir(values["HOME"]), "home-link")
		if err := os.Symlink(values["HOME"], link); err != nil {
			t.Fatal(err)
		}
		environment := replaceEnvironmentValue(fixture.environment, "HOME", link)
		backend := successfulScriptedBackend(t)
		var stdout, stderr bytes.Buffer
		report := Run(Invocation{Context: context.Background(), CWD: fixture.project, Args: []string{"migrations", "check"}, Environment: environment, Stdout: &stdout, Stderr: &stderr, Backend: backend})
		if report.ExitCode != 0 || !report.HasResult || report.TempCreated != 1 || report.BuildCalls != 1 || report.RunnerCalls != 1 {
			t.Fatalf("symlinked protected root = %+v stderr=%q", report, stderr.String())
		}
	})

	for _, boundary := range []struct {
		name string
		key  string
		path func(globalFixture, map[string]string) string
	}{
		{name: "project same", key: "TMPDIR", path: func(f globalFixture, _ map[string]string) string { return f.project }},
		{name: "project descendant", key: "TMPDIR", path: func(f globalFixture, _ map[string]string) string { return filepath.Join(f.project, "private-tmp") }},
		{name: "HOME same", key: "TMPDIR", path: func(_ globalFixture, v map[string]string) string { return v["HOME"] }},
		{name: "HOME descendant", key: "TMPDIR", path: func(_ globalFixture, v map[string]string) string { return filepath.Join(v["HOME"], "private-tmp") }},
		{name: "XDG config descendant", key: "TMPDIR", path: func(_ globalFixture, v map[string]string) string {
			return filepath.Join(v["XDG_CONFIG_HOME"], "private-tmp")
		}},
		{name: "XDG cache descendant", key: "TMPDIR", path: func(_ globalFixture, v map[string]string) string {
			return filepath.Join(v["XDG_CACHE_HOME"], "private-tmp")
		}},
	} {
		t.Run(boundary.name, func(t *testing.T) {
			fixture := newGlobalFixture(t, 0)
			values := environmentValues(fixture.environment)
			candidate := boundary.path(fixture, values)
			if err := os.MkdirAll(candidate, 0o700); err != nil {
				t.Fatal(err)
			}
			environment := replaceEnvironmentValue(fixture.environment, boundary.key, candidate)
			var stderr bytes.Buffer
			report := Run(Invocation{Context: context.Background(), CWD: fixture.project, Args: []string{"migrations", "check"}, Environment: environment, Stderr: &stderr, Backend: backendFunc(func(context.Context, <-chan struct{}, ProcessStage, Command) ProcessResult {
				t.Fatal("backend called for protected temp boundary")
				return ProcessResult{}
			})})
			if report.ExitCode != 3 || report.Failure.Code != protocol.CodeProjectTemporaryStorageFailed || report.TempCreated != 0 || report.BuildCalls != 0 || report.RunnerCalls != 0 {
				t.Fatalf("protected boundary report = %+v stderr=%q", report, stderr.String())
			}
		})
	}

	t.Run("macOS case alias cannot bypass project containment", func(t *testing.T) {
		if runtime.GOOS != "darwin" {
			t.Skip("case-insensitive default filesystem gate is macOS-specific")
		}
		fixture := newGlobalFixture(t, 0)
		candidate := filepath.Join(fixture.project, "case-tmp")
		if err := os.Mkdir(candidate, 0o700); err != nil {
			t.Fatal(err)
		}
		alias := caseAliasForExistingDirectory(t, candidate)
		environment := replaceEnvironmentValue(fixture.environment, "TMPDIR", alias)
		var stderr bytes.Buffer
		report := Run(Invocation{Context: context.Background(), CWD: fixture.project, Args: []string{"migrations", "check"}, Environment: environment, Stderr: &stderr, Backend: backendFunc(func(context.Context, <-chan struct{}, ProcessStage, Command) ProcessResult {
			t.Fatal("backend called for case-aliased project temp")
			return ProcessResult{}
		})})
		if report.ExitCode != 3 || report.Failure.Code != protocol.CodeProjectTemporaryStorageFailed || report.TempCreated != 0 || report.BuildCalls != 0 {
			t.Fatalf("case-alias report = %+v stderr=%q alias=%q", report, stderr.String(), alias)
		}
	})

	t.Run("project containment follows retained identity after pathname replacement", func(t *testing.T) {
		fixture := newGlobalFixture(t, 0)
		retainedPath := filepath.Join(filepath.Dir(fixture.project), "retained-project")
		candidate := filepath.Join(retainedPath, "private-tmp")
		environment := replaceEnvironmentValue(fixture.environment, "TMPDIR", candidate)
		var hookErr error
		var replaced bool
		var stderr bytes.Buffer
		report := Run(Invocation{
			Context: context.Background(), CWD: fixture.project, Args: []string{"migrations", "check"}, Environment: environment,
			Stderr: &stderr,
			Backend: backendFunc(func(context.Context, <-chan struct{}, ProcessStage, Command) ProcessResult {
				t.Fatal("backend called after retained-root containment failure")
				return ProcessResult{}
			}),
			workspace: workspaceHooks{beforeContainment: func() {
				if err := os.Rename(fixture.project, retainedPath); err != nil {
					hookErr = err
					return
				}
				replaced = true
				if err := os.MkdirAll(candidate, 0o700); err != nil {
					hookErr = err
					return
				}
				if err := os.Mkdir(fixture.project, 0o700); err != nil {
					hookErr = err
				}
			}},
		})
		if replaced {
			_ = os.RemoveAll(fixture.project)
			if err := os.Rename(retainedPath, fixture.project); err != nil && hookErr == nil {
				hookErr = err
			}
		}
		if hookErr != nil {
			t.Fatal(hookErr)
		}
		if report.ExitCode != 3 || report.Failure.Code != protocol.CodeProjectTemporaryStorageFailed || report.TempCreated != 0 || report.BuildCalls != 0 {
			t.Fatalf("retained-root containment = %+v stderr=%q", report, stderr.String())
		}
	})
}

func TestPartialWorkspaceCleanupFailureOverridesCancellation(t *testing.T) {
	for _, test := range []struct {
		name   string
		invoke func(context.CancelFunc, chan struct{})
	}{
		{name: "context", invoke: func(cancel context.CancelFunc, _ chan struct{}) { cancel() }},
		{name: "interrupt", invoke: func(_ context.CancelFunc, interrupt chan struct{}) { close(interrupt) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newGlobalFixture(t, 0)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			interrupt := make(chan struct{})
			var partialRoot string
			var basePath string
			var hookErr error
			var stdout, stderr bytes.Buffer
			report := Run(Invocation{
				Context: ctx, CWD: fixture.project, Args: []string{"migrations", "check"}, Environment: fixture.environment,
				Stdout: &stdout, Stderr: &stderr, Interrupt: interrupt,
				Backend: backendFunc(func(context.Context, <-chan struct{}, ProcessStage, Command) ProcessResult {
					t.Fatal("backend called after partial workspace failure")
					return ProcessResult{}
				}),
				workspace: workspaceHooks{afterRootCreated: func(root string, base *os.File) {
					partialRoot = root
					basePath = filepath.Dir(root)
					if err := os.WriteFile(filepath.Join(root, "tmp"), []byte("blocks directory creation"), 0o600); err != nil {
						hookErr = err
					}
					if err := os.Chmod(basePath, 0o500); err != nil && hookErr == nil {
						hookErr = err
					}
					_ = base.Close()
					test.invoke(cancel, interrupt)
				}},
			})
			if basePath != "" {
				if err := os.Chmod(basePath, 0o700); err != nil && hookErr == nil {
					hookErr = err
				}
			}
			if partialRoot != "" {
				_ = os.RemoveAll(partialRoot)
			}
			if hookErr != nil {
				t.Fatal(hookErr)
			}
			if report.ExitCode != 3 || report.Failure.Code != protocol.CodeProjectCleanupFailed || report.TempCreated != 1 || report.TempCleanupAttempts != 1 || report.CleanupFailed != 1 || report.ResidualTemp != 1 || report.BuildCalls != 0 || report.RunnerCalls != 0 || stdout.Len() != 0 {
				t.Fatalf("partial cleanup precedence = %+v stdout=%q stderr=%q", report, stdout.String(), stderr.String())
			}
		})
	}
}

func TestProcessCleanupFailurePrecedenceIsObservable(t *testing.T) {
	t.Parallel()
	canceled := failure(protocol.CategoryProcess, protocol.CodeProjectCanceled)
	for _, test := range []struct {
		name     string
		process  ProcessResult
		wantCode string
	}{
		{
			name:     "replaces success",
			process:  ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, CleanupFailed: true},
			wantCode: protocol.CodeProjectCleanupFailed,
		},
		{
			name:     "replaces cancellation",
			process:  ProcessResult{Started: true, ExitCode: 1, DirectReaps: 1, Failure: &canceled, CleanupFailed: true},
			wantCode: protocol.CodeProjectCleanupFailed,
		},
		{
			name:     "preserves non-cancel primary",
			process:  ProcessResult{Started: true, ExitCode: 1, DirectReaps: 1, CleanupFailed: true},
			wantCode: protocol.CodeProjectBuildFailed,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newGlobalFixture(t, 0)
			backend := &scriptedBackend{build: test.process}
			var stdout, stderr bytes.Buffer
			report := Run(Invocation{
				Context: context.Background(), CWD: fixture.project, Args: []string{"migrations", "check"}, Environment: fixture.environment,
				Stdout: &stdout, Stderr: &stderr, Backend: backend,
			})
			if report.ExitCode != 3 || report.Failure.Code != test.wantCode || report.CleanupFailed != 1 || report.TempCleanupAttempts != 1 {
				t.Fatalf("process cleanup precedence = %+v", report)
			}
		})
	}
}

func TestImplicitStableNonRegularDescriptorIsInvalid(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			switch kind {
			case "directory":
				if err := os.Mkdir(filepath.Join(root, descriptorName), 0o700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := filepath.Join(t.TempDir(), "descriptor")
				if err := os.WriteFile(target, canonicalDescriptor(), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(root, descriptorName)); err != nil {
					t.Fatal(err)
				}
			}
			var stdout, stderr bytes.Buffer
			report := Run(Invocation{Context: context.Background(), CWD: root, Args: []string{"migrations", "check"}, Stdout: &stdout, Stderr: &stderr})
			if report.ExitCode != 2 || report.Failure.Code != protocol.CodeInvalidProjectDescriptor || report.BuildCalls != 0 || report.DescriptorReads != 0 {
				t.Fatalf("%s descriptor = %+v", kind, report)
			}
		})
	}
}

type backendFunc func(context.Context, <-chan struct{}, ProcessStage, Command) ProcessResult

func (function backendFunc) Execute(ctx context.Context, interrupt <-chan struct{}, stage ProcessStage, command Command) ProcessResult {
	return function(ctx, interrupt, stage, command)
}

type globalFixture struct {
	project     string
	cwd         string
	environment []string
}

func newGlobalFixture(t *testing.T, nestedDepth int) globalFixture {
	t.Helper()
	universe := t.TempDir()
	physicalUniverse, err := filepath.EvalSymlinks(universe)
	if err != nil {
		t.Fatal(err)
	}
	universe = physicalUniverse
	project := filepath.Join(universe, "project")
	if err := os.Mkdir(project, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, descriptorName), canonicalDescriptor(), 0o600); err != nil {
		t.Fatal(err)
	}
	cwd := project
	for index := 0; index < nestedDepth; index++ {
		cwd = filepath.Join(cwd, "nested")
		if err := os.Mkdir(cwd, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	paths := map[string]string{}
	for _, name := range []string{"home", "config", "cache", "tmp"} {
		path := filepath.Join(universe, name)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		paths[name] = path
	}
	return globalFixture{
		project: project,
		cwd:     cwd,
		environment: []string{
			"PATH=" + os.Getenv("PATH"),
			"HOME=" + paths["home"],
			"XDG_CONFIG_HOME=" + paths["config"],
			"XDG_CACHE_HOME=" + paths["cache"],
			"TMPDIR=" + paths["tmp"],
			"NETRC=/explicit/netrc",
			"GOTELEMETRY=ambient",
		},
	}
}

func canonicalDescriptor() []byte {
	return []byte("format_version = 1\n\n[project]\npackage = \"./cmd/site\"\n")
}

func withoutEnvironmentKey(entries []string, key string) []string {
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		name, _, ok := strings.Cut(entry, "=")
		if ok && name == key {
			continue
		}
		result = append(result, entry)
	}
	return result
}

func replaceEnvironmentValue(entries []string, key, value string) []string {
	result := withoutEnvironmentKey(entries, key)
	return append(result, key+"="+value)
}

func successfulScriptedBackend(t *testing.T) *scriptedBackend {
	t.Helper()
	wire, err := protocol.EncodeResponse(protocol.Response{OK: true, Result: protocol.Result{DefinitionSetDigest: protocol.EmptySetDigest}})
	if err != nil {
		t.Fatal(err)
	}
	return &scriptedBackend{
		build: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1},
		runner: ProcessResult{
			Started: true, ExitCode: 0, DirectReaps: 1, Stdout: wire,
			StdoutScalar: StreamScalar{RetainedBytes: len(wire)},
		},
	}
}

func caseAliasForExistingDirectory(t *testing.T, path string) string {
	t.Helper()
	original, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < len(path); index++ {
		character := path[index]
		var replacement byte
		switch {
		case character >= 'a' && character <= 'z':
			replacement = character - ('a' - 'A')
		case character >= 'A' && character <= 'Z':
			replacement = character + ('a' - 'A')
		default:
			continue
		}
		candidate := path[:index] + string(replacement) + path[index+1:]
		aliased, statErr := os.Stat(candidate)
		if statErr == nil && os.SameFile(original, aliased) && candidate != path {
			return candidate
		}
	}
	t.Skip("filesystem does not provide a case-insensitive alias for the fixture path")
	return ""
}
