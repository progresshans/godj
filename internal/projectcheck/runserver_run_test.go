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
	"time"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/projectgenerate"
	projectgenerateprotocol "github.com/progresshans/godj/internal/projectgenerate/protocol"
)

type runserverSequenceBackend struct {
	commands []Command
	stages   []ProcessStage
	results  []ProcessResult
}

func (backend *runserverSequenceBackend) Execute(_ context.Context, _ <-chan struct{}, stage ProcessStage, command Command) ProcessResult {
	backend.stages = append(backend.stages, stage)
	backend.commands = append(backend.commands, cloneCommand(command))
	if len(backend.results) == 0 {
		return ProcessResult{}
	}
	result := backend.results[0]
	backend.results = backend.results[1:]
	return result
}

func TestRunServerRejectsArgumentsBeforeCWDSelection(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	report := RunServer(RunServerInvocation{
		Context: context.Background(), CWD: filepath.Join(t.TempDir(), "missing"), Args: []string{"runserver", "--project"},
		Stdout: &stdout, Stderr: &stderr,
		Backend: backendFunc(func(context.Context, <-chan struct{}, ProcessStage, Command) ProcessResult {
			t.Fatal("backend called after invalid arguments")
			return ProcessResult{}
		}),
	})
	if report.ExitCode != 2 || report.RunServerFailure != (RunServerFailure{Category: RunServerCategoryCommand, Code: RunServerCodeInvalidArguments}) || report.AncestorDirectoriesInspected != 0 || report.DescriptorReads != 0 || report.BuildCalls != 0 || stdout.Len() != 0 || stderr.String() != RunServerCategoryCommand+"/"+RunServerCodeInvalidArguments+"\n" {
		t.Fatalf("invalid runserver report=%+v stdout=%q stderr=%q", report, stdout.String(), stderr.String())
	}
}

func TestRunServerRequiresOptionalRuntimeCapabilityBeforeWorkspace(t *testing.T) {
	t.Parallel()
	fixture := newGlobalFixture(t, 0)
	var stdout, stderr bytes.Buffer
	report := RunServer(RunServerInvocation{
		Context: context.Background(), CWD: fixture.project, Args: []string{"runserver"}, Environment: fixture.environment,
		Stdout: &stdout, Stderr: &stderr,
		Backend: backendFunc(func(context.Context, <-chan struct{}, ProcessStage, Command) ProcessResult {
			t.Fatal("backend called without runserver_package")
			return ProcessResult{}
		}),
	})
	if report.ExitCode != 2 || report.RunServerFailure != (RunServerFailure{Category: RunServerCategoryConfiguration, Code: RunServerCodeNotConfigured}) || report.BuildCalls != 0 || report.TempCreated != 0 || report.RuntimeStartCalls != 0 || stdout.Len() != 0 || stderr.String() != RunServerCategoryConfiguration+"/"+RunServerCodeNotConfigured+"\n" {
		t.Fatalf("unconfigured runserver=%+v stdout=%q stderr=%q", report, stdout.String(), stderr.String())
	}
}

func TestRunServerRequiresDistinctRealPackageDirectoriesBeforeWorkspace(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name           string
		runtimePackage func(*testing.T, string) string
	}{
		{
			name: "symlink alias",
			runtimePackage: func(t *testing.T, project string) string {
				t.Helper()
				if err := os.Symlink("site", filepath.Join(project, "cmd", "site-alias")); err != nil {
					t.Fatal(err)
				}
				return "./cmd/site-alias"
			},
		},
		{
			name: "case alias",
			runtimePackage: func(t *testing.T, project string) string {
				t.Helper()
				return "./cmd/" + runserverCaseAliasForDirectory(t, filepath.Join(project, "cmd"), "site")
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRunserverFixture(t, 0)
			runtimePackage := test.runtimePackage(t, fixture.project)
			document := "format_version = 1\n[project]\npackage = \"./cmd/site\"\nrunserver_package = \"" + runtimePackage + "\"\n"
			if err := os.WriteFile(filepath.Join(fixture.project, descriptorName), []byte(document), 0o600); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			report := RunServer(RunServerInvocation{
				Context: context.Background(), CWD: fixture.project, Args: []string{"runserver"}, Environment: fixture.environment,
				Stdout: &stdout, Stderr: &stderr,
				Backend: backendFunc(func(context.Context, <-chan struct{}, ProcessStage, Command) ProcessResult {
					t.Fatal("backend called for aliased runserver packages")
					return ProcessResult{}
				}),
			})
			if report.ExitCode != 2 || report.RunServerFailure != (RunServerFailure{Category: RunServerCategorySelection, Code: RunServerCodeInvalidProjectDescriptor}) || report.TempCreated != 0 || report.BuildCalls != 0 || report.RuntimeStartCalls != 0 || stdout.Len() != 0 || stderr.String() != RunServerCategorySelection+"/"+RunServerCodeInvalidProjectDescriptor+"\n" {
				t.Fatalf("aliased package report=%+v stdout=%q stderr=%q", report, stdout.String(), stderr.String())
			}
		})
	}
}

func runserverCaseAliasForDirectory(t *testing.T, parent, name string) string {
	t.Helper()
	original, err := os.Stat(filepath.Join(parent, name))
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < len(name); index++ {
		candidate := []byte(name)
		switch {
		case candidate[index] >= 'a' && candidate[index] <= 'z':
			candidate[index] -= 'a' - 'A'
		case candidate[index] >= 'A' && candidate[index] <= 'Z':
			candidate[index] += 'a' - 'A'
		default:
			continue
		}
		alias := string(candidate)
		aliased, statErr := os.Stat(filepath.Join(parent, alias))
		if statErr == nil && alias != name && os.SameFile(original, aliased) {
			return alias
		}
	}
	t.Skip("filesystem does not provide a case-insensitive package alias")
	return ""
}

func TestRunServerUsesOneSelectionTwoPreflightsAndAmbientRuntimeEnvironment(t *testing.T) {
	t.Parallel()
	fixture := newRunserverFixture(t, 2)
	wire := runserverProjectSpecWire(t)
	backend := &runserverSequenceBackend{results: []ProcessResult{
		{Started: true, ExitCode: 0, DirectReaps: 1},
		{Started: true, ExitCode: 0, DirectReaps: 1, Stdout: wire, StdoutScalar: StreamScalar{RetainedBytes: len(wire)}},
		{Started: true, ExitCode: 0, DirectReaps: 1},
	}}
	checks := 0
	var runtimeCommand Command
	var stdout, stderr bytes.Buffer
	report := RunServer(RunServerInvocation{
		Context: context.Background(), CWD: fixture.cwd,
		Args:        []string{"runserver", "--project", filepath.Join(fixture.project, descriptorName), "--addr", "127.0.0.1:0"},
		Environment: fixture.environment, Stdout: &stdout, Stderr: &stderr, Backend: backend,
		generation: generationHooks{check: func(context.Context, projectgenerate.ProjectRoot, codegen.GeneratedBundle) (projectgenerate.CheckReport, error) {
			checks++
			return projectgenerate.CheckReport{}, nil
		}},
		runtime: runserverRuntimeHooks{grace: 25 * time.Millisecond, execute: func(_ context.Context, _ <-chan struct{}, command Command, output, _ io.Writer, grace time.Duration) runserverProcessResult {
			runtimeCommand = cloneCommand(command)
			if grace != 25*time.Millisecond {
				t.Fatalf("runtime grace=%s", grace)
			}
			_, _ = io.WriteString(output, "runtime-ready\n")
			return runserverProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, Interrupted: true}
		}},
	})
	if report.ExitCode != 0 || !report.HasRunServerResult || report.HasRunServerFailure || report.RunServerResult.Address != "127.0.0.1:0" || report.BuildCalls != 2 || report.RunnerCalls != 1 || report.RuntimeBuildCalls != 1 || report.RuntimeStartCalls != 1 || report.PreflightChecks != 2 || checks != 2 || report.DirectChildReaps != 4 || report.TempCreated != 1 || report.TempCleanupAttempts != 1 || report.CleanupFailed != 0 || report.ResidualTemp != 0 {
		t.Fatalf("successful runserver=%+v checks=%d", report, checks)
	}
	if stdout.String() != "runtime-ready\n" || stderr.Len() != 0 {
		t.Fatalf("runtime output stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if len(backend.commands) != 3 || !reflect.DeepEqual(backend.stages, []ProcessStage{BuildStage, GenerationRunnerStage, BuildStage}) {
		t.Fatalf("short process stages=%v commands=%+v", backend.stages, backend.commands)
	}
	declarationBuild, runner, runtimeBuild := backend.commands[0], backend.commands[1], backend.commands[2]
	if declarationBuild.Dir != fixture.project || declarationBuild.Argv[len(declarationBuild.Argv)-1] != "./cmd/site" || runtimeBuild.Dir != fixture.project || runtimeBuild.Argv[len(runtimeBuild.Argv)-1] != "./cmd/runtime" {
		t.Fatalf("build commands declaration=%+v runtime=%+v", declarationBuild, runtimeBuild)
	}
	if len(runner.Argv) != 2 || runner.Argv[1] != projectgenerateprotocol.PrivateArgument || !bytes.Equal(runner.Stdin, projectgenerateprotocol.RequestDocument()) {
		t.Fatalf("runner command=%+v", runner)
	}
	if runtimeCommand.Dir != fixture.project || len(runtimeCommand.Argv) != 4 || runtimeCommand.Argv[1] != "serve" || runtimeCommand.Argv[2] != "--listen" || runtimeCommand.Argv[3] != "127.0.0.1:0" || !reflect.DeepEqual(runtimeCommand.Env, fixture.environment) {
		t.Fatalf("runtime command=%+v", runtimeCommand)
	}
	if reflect.DeepEqual(declarationBuild.Env, runtimeCommand.Env) {
		t.Fatal("isolated build environment leaked into project runtime")
	}
	if _, err := os.Stat(filepath.Dir(runtimeCommand.Argv[0])); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("private runtime workspace remains: %v", err)
	}
}

func TestRunServerStaleBundleStopsBeforeRuntimeBuildAndPreservesProject(t *testing.T) {
	t.Parallel()
	fixture := newRunserverFixture(t, 0)
	before := generationTreeSnapshot(t, fixture.project)
	wire := runserverProjectSpecWire(t)
	backend := &runserverSequenceBackend{results: []ProcessResult{
		{Started: true, ExitCode: 0, DirectReaps: 1},
		{Started: true, ExitCode: 0, DirectReaps: 1, Stdout: wire, StdoutScalar: StreamScalar{RetainedBytes: len(wire)}},
	}}
	runtimeCalls := 0
	var stdout, stderr bytes.Buffer
	report := RunServer(RunServerInvocation{
		Context: context.Background(), CWD: fixture.project, Args: []string{"runserver"}, Environment: fixture.environment,
		Stdout: &stdout, Stderr: &stderr, Backend: backend,
		generation: generationHooks{check: func(context.Context, projectgenerate.ProjectRoot, codegen.GeneratedBundle) (projectgenerate.CheckReport, error) {
			return projectgenerate.CheckReport{Drifts: []projectgenerate.Drift{{Path: "generated/example.go", Kind: projectgenerate.DriftMissing}}}, projectgenerate.ErrGeneratedDrift
		}},
		runtime: runserverRuntimeHooks{execute: func(context.Context, <-chan struct{}, Command, io.Writer, io.Writer, time.Duration) runserverProcessResult {
			runtimeCalls++
			return runserverProcessResult{}
		}},
	})
	if report.ExitCode != 1 || report.RunServerFailure != (RunServerFailure{Category: RunServerCategoryGeneration, Code: RunServerCodeGeneratedBundleStale}) || report.BuildCalls != 1 || report.RuntimeBuildCalls != 0 || report.RuntimeStartCalls != 0 || runtimeCalls != 0 || report.PreflightChecks != 1 || report.GeneratedDriftCount != 1 || stdout.Len() != 0 || stderr.String() != RunServerCategoryGeneration+"/"+RunServerCodeGeneratedBundleStale+"\n" {
		t.Fatalf("stale runserver=%+v runtime_calls=%d stdout=%q stderr=%q", report, runtimeCalls, stdout.String(), stderr.String())
	}
	if after := generationTreeSnapshot(t, fixture.project); !reflect.DeepEqual(before, after) {
		t.Fatalf("stale preflight mutated project\nbefore=%v\nafter=%v", before, after)
	}
}

func TestRunServerSecondPreflightStopsRuntimeAfterReadonlyBuild(t *testing.T) {
	t.Parallel()
	fixture := newRunserverFixture(t, 0)
	wire := runserverProjectSpecWire(t)
	backend := &runserverSequenceBackend{results: []ProcessResult{
		{Started: true, ExitCode: 0, DirectReaps: 1},
		{Started: true, ExitCode: 0, DirectReaps: 1, Stdout: wire, StdoutScalar: StreamScalar{RetainedBytes: len(wire)}},
		{Started: true, ExitCode: 0, DirectReaps: 1},
	}}
	checks := 0
	report := RunServer(RunServerInvocation{
		Context: context.Background(), CWD: fixture.project, Args: []string{"runserver"}, Environment: fixture.environment,
		Stdout: io.Discard, Stderr: io.Discard, Backend: backend,
		generation: generationHooks{check: func(context.Context, projectgenerate.ProjectRoot, codegen.GeneratedBundle) (projectgenerate.CheckReport, error) {
			checks++
			if checks == 1 {
				return projectgenerate.CheckReport{}, nil
			}
			return projectgenerate.CheckReport{Drifts: []projectgenerate.Drift{{Path: "generated/example.go", Kind: projectgenerate.DriftModified}}}, projectgenerate.ErrGeneratedDrift
		}},
		runtime: runserverRuntimeHooks{execute: func(context.Context, <-chan struct{}, Command, io.Writer, io.Writer, time.Duration) runserverProcessResult {
			t.Fatal("runtime started after second preflight drift")
			return runserverProcessResult{}
		}},
	})
	if report.ExitCode != 1 || report.RunServerFailure.Code != RunServerCodeGeneratedBundleStale || report.BuildCalls != 2 || report.RuntimeBuildCalls != 1 || report.RuntimeStartCalls != 0 || report.PreflightChecks != 2 || checks != 2 {
		t.Fatalf("second preflight=%+v checks=%d", report, checks)
	}
}

func TestRunServerRuntimeBuildFailureStopsBeforeRuntimeAndPreservesProject(t *testing.T) {
	t.Parallel()
	fixture := newRunserverFixture(t, 0)
	before := generationTreeSnapshot(t, fixture.project)
	wire := runserverProjectSpecWire(t)
	backend := &runserverSequenceBackend{results: []ProcessResult{
		{Started: true, ExitCode: 0, DirectReaps: 1},
		{Started: true, ExitCode: 0, DirectReaps: 1, Stdout: wire, StdoutScalar: StreamScalar{RetainedBytes: len(wire)}},
		{Started: true, ExitCode: 1, DirectReaps: 1},
	}}
	var stdout, stderr bytes.Buffer
	report := RunServer(RunServerInvocation{
		Context: context.Background(), CWD: fixture.project, Args: []string{"runserver"}, Environment: fixture.environment,
		Stdout: &stdout, Stderr: &stderr, Backend: backend,
		generation: generationHooks{check: func(context.Context, projectgenerate.ProjectRoot, codegen.GeneratedBundle) (projectgenerate.CheckReport, error) {
			return projectgenerate.CheckReport{}, nil
		}},
		runtime: runserverRuntimeHooks{execute: func(context.Context, <-chan struct{}, Command, io.Writer, io.Writer, time.Duration) runserverProcessResult {
			t.Fatal("runtime started after runtime build failure")
			return runserverProcessResult{}
		}},
	})
	if report.ExitCode != 3 || report.RunServerFailure != (RunServerFailure{Category: RunServerCategoryBuild, Code: RunServerCodeRuntimeBuildFailed}) || report.BuildCalls != 2 || report.RuntimeBuildCalls != 1 || report.RunnerCalls != 1 || report.PreflightChecks != 1 || report.RuntimeStartCalls != 0 || report.TempCreated != 1 || report.TempCleanupAttempts != 1 || report.CleanupFailed != 0 || report.ResidualTemp != 0 || stdout.Len() != 0 || stderr.String() != RunServerCategoryBuild+"/"+RunServerCodeRuntimeBuildFailed+"\n" {
		t.Fatalf("runtime build failure=%+v stdout=%q stderr=%q", report, stdout.String(), stderr.String())
	}
	if after := generationTreeSnapshot(t, fixture.project); !reflect.DeepEqual(before, after) {
		t.Fatalf("runtime build failure mutated project\nbefore=%v\nafter=%v", before, after)
	}
}

func TestClassifyRunserverProcessClosedOutcomes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   runserverProcessResult
		want *RunServerFailure
	}{
		{name: "clean interrupt", in: runserverProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, Interrupted: true}},
		{name: "prestart", in: runserverProcessResult{StartError: errors.New("private")}, want: &RunServerFailure{Category: RunServerCategoryRuntime, Code: RunServerCodeRuntimeStartFailed}},
		{name: "prestart interrupt", in: runserverProcessResult{Interrupted: true}, want: &RunServerFailure{Category: RunServerCategoryProcess, Code: RunServerCodeProjectInterrupted}},
		{name: "prestart cancel", in: runserverProcessResult{Canceled: true}, want: &RunServerFailure{Category: RunServerCategoryProcess, Code: RunServerCodeProjectCanceled}},
		{name: "unexpected zero", in: runserverProcessResult{Started: true, ExitCode: 0, DirectReaps: 1}, want: &RunServerFailure{Category: RunServerCategoryRuntime, Code: RunServerCodeRuntimeExited}},
		{name: "forced interrupt", in: runserverProcessResult{Started: true, ExitCode: -1, DirectReaps: 1, Interrupted: true, Forced: true}, want: &RunServerFailure{Category: RunServerCategoryProcess, Code: RunServerCodeProjectInterrupted}},
		{name: "canceled", in: runserverProcessResult{Started: true, DirectReaps: 1, Canceled: true}, want: &RunServerFailure{Category: RunServerCategoryProcess, Code: RunServerCodeProjectCanceled}},
		{name: "stream", in: runserverProcessResult{Started: true, StdoutError: errors.New("private"), CleanupFailed: true}, want: &RunServerFailure{Category: RunServerCategoryRuntime, Code: RunServerCodeRuntimeStreamFailed}},
		{name: "cleanup", in: runserverProcessResult{Started: true, CleanupFailed: true}, want: &RunServerFailure{Category: RunServerCategoryProcess, Code: RunServerCodeProjectCleanupFailed}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := classifyRunserverProcess(test.in)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("classify=%+v want=%+v", got, test.want)
			}
		})
	}
}

func TestRunServerPublishesClosedRuntimeOutcomes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		process     runserverProcessResult
		wantExit    int
		wantFailure *RunServerFailure
	}{
		{name: "clean interrupt", process: runserverProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, Interrupted: true}, wantExit: 0},
		{name: "forced interrupt", process: runserverProcessResult{Started: true, ExitCode: -1, DirectReaps: 1, Interrupted: true, Forced: true, SIGINTAttempts: 1, SIGKILLAttempts: 1}, wantExit: 130, wantFailure: &RunServerFailure{Category: RunServerCategoryProcess, Code: RunServerCodeProjectInterrupted}},
		{name: "context cancellation", process: runserverProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, Canceled: true, SIGINTAttempts: 1}, wantExit: 3, wantFailure: &RunServerFailure{Category: RunServerCategoryProcess, Code: RunServerCodeProjectCanceled}},
		{name: "start", process: runserverProcessResult{StartError: errors.New("private start detail")}, wantExit: 3, wantFailure: &RunServerFailure{Category: RunServerCategoryRuntime, Code: RunServerCodeRuntimeStartFailed}},
		{name: "unexpected exit", process: runserverProcessResult{Started: true, ExitCode: 7, DirectReaps: 1, WaitError: errors.New("private wait detail")}, wantExit: 3, wantFailure: &RunServerFailure{Category: RunServerCategoryRuntime, Code: RunServerCodeRuntimeExited}},
		{name: "stream", process: runserverProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, StdoutError: errors.New("private stream detail")}, wantExit: 3, wantFailure: &RunServerFailure{Category: RunServerCategoryRuntime, Code: RunServerCodeRuntimeStreamFailed}},
		{name: "cleanup", process: runserverProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, CleanupFailed: true}, wantExit: 3, wantFailure: &RunServerFailure{Category: RunServerCategoryProcess, Code: RunServerCodeProjectCleanupFailed}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newRunserverFixture(t, 0)
			backend := successfulRunserverSequenceBackend(t)
			var stdout, stderr bytes.Buffer
			report := RunServer(RunServerInvocation{
				Context: context.Background(), CWD: fixture.project, Args: []string{"runserver"}, Environment: fixture.environment,
				Stdout: &stdout, Stderr: &stderr, Backend: backend,
				generation: generationHooks{check: func(context.Context, projectgenerate.ProjectRoot, codegen.GeneratedBundle) (projectgenerate.CheckReport, error) {
					return projectgenerate.CheckReport{}, nil
				}},
				runtime: runserverRuntimeHooks{execute: func(context.Context, <-chan struct{}, Command, io.Writer, io.Writer, time.Duration) runserverProcessResult {
					return test.process
				}},
			})
			if report.ExitCode != test.wantExit || stdout.Len() != 0 {
				t.Fatalf("report=%+v stdout=%q stderr=%q", report, stdout.String(), stderr.String())
			}
			if test.wantFailure == nil {
				if !report.HasRunServerResult || report.HasRunServerFailure || stderr.Len() != 0 {
					t.Fatalf("clean outcome=%+v stderr=%q", report, stderr.String())
				}
				return
			}
			wantStderr := test.wantFailure.Category + "/" + test.wantFailure.Code + "\n"
			if !report.HasRunServerFailure || report.RunServerFailure != *test.wantFailure || report.HasRunServerResult || stderr.String() != wantStderr {
				t.Fatalf("failure outcome=%+v want=%+v stderr=%q", report, *test.wantFailure, stderr.String())
			}
			for _, secret := range []string{"private start detail", "private wait detail", "private stream detail"} {
				if strings.Contains(stderr.String(), secret) {
					t.Fatalf("runtime diagnostic leaked %q in %q", secret, stderr.String())
				}
			}
		})
	}
}

func TestRunServerWorkspaceCleanupFailureOverridesCleanRuntime(t *testing.T) {
	t.Parallel()
	fixture := newRunserverFixture(t, 0)
	backend := successfulRunserverSequenceBackend(t)
	var stderr bytes.Buffer
	report := RunServer(RunServerInvocation{
		Context: context.Background(), CWD: fixture.project, Args: []string{"runserver"}, Environment: fixture.environment,
		Stdout: io.Discard, Stderr: &stderr, Backend: backend,
		workspace: workspaceHooks{afterRootCreated: func(_ string, base *os.File) { _ = base.Close() }},
		generation: generationHooks{check: func(context.Context, projectgenerate.ProjectRoot, codegen.GeneratedBundle) (projectgenerate.CheckReport, error) {
			return projectgenerate.CheckReport{}, nil
		}},
		runtime: runserverRuntimeHooks{execute: func(context.Context, <-chan struct{}, Command, io.Writer, io.Writer, time.Duration) runserverProcessResult {
			return runserverProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, Interrupted: true}
		}},
	})
	if report.ExitCode != 3 || report.RunServerFailure != (RunServerFailure{Category: RunServerCategoryProcess, Code: RunServerCodeProjectCleanupFailed}) || report.CleanupFailed != 1 || report.ResidualTemp != 1 || stderr.String() != RunServerCategoryProcess+"/"+RunServerCodeProjectCleanupFailed+"\n" {
		t.Fatalf("cleanup precedence=%+v stderr=%q", report, stderr.String())
	}
}

func TestRunserverExitCodeRetainsEveryLinkedDeclarationFailure(t *testing.T) {
	t.Parallel()
	for _, failure := range []RunServerFailure{
		{Category: projectgenerateprotocol.CategoryProtocol, Code: projectgenerateprotocol.CodeInvalidRequest},
		{Category: projectgenerateprotocol.CategoryProtocol, Code: projectgenerateprotocol.CodeProtocolIncompatible},
		{Category: projectgenerateprotocol.CategoryDeclaration, Code: projectgenerateprotocol.CodeProjectSpecLoadFailed},
	} {
		exit, ok := runserverExitCode(failure)
		if !ok || (failure.Category == projectgenerateprotocol.CategoryDeclaration && exit != 1) || (failure.Category == projectgenerateprotocol.CategoryProtocol && exit != 3) {
			t.Fatalf("runserverExitCode(%+v) = %d, %t", failure, exit, ok)
		}
	}
}

func newRunserverFixture(t *testing.T, nestedDepth int) globalFixture {
	t.Helper()
	fixture := newGlobalFixture(t, nestedDepth)
	for _, directory := range []string{"site", "runtime"} {
		if err := os.MkdirAll(filepath.Join(fixture.project, "cmd", directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	document := "format_version = 1\n\n[project]\npackage = \"./cmd/site\"\nrunserver_package = \"./cmd/runtime\"\n"
	if err := os.WriteFile(filepath.Join(fixture.project, descriptorName), []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func runserverProjectSpecWire(t *testing.T) []byte {
	t.Helper()
	wire, err := projectgenerateprotocol.EncodeResponse(projectgenerateprotocol.Response{OK: true, ProjectSpec: generationTestSpec()})
	if err != nil {
		t.Fatal(err)
	}
	return wire
}

func successfulRunserverSequenceBackend(t *testing.T) *runserverSequenceBackend {
	t.Helper()
	wire := runserverProjectSpecWire(t)
	return &runserverSequenceBackend{results: []ProcessResult{
		{Started: true, ExitCode: 0, DirectReaps: 1},
		{Started: true, ExitCode: 0, DirectReaps: 1, Stdout: wire, StdoutScalar: StreamScalar{RetainedBytes: len(wire)}},
		{Started: true, ExitCode: 0, DirectReaps: 1},
	}}
}
