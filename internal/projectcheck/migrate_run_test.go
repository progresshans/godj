//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/projectcheck/migrateprotocol"
)

func TestParseMigrateArgumentsExactV2Forms(t *testing.T) {
	t.Parallel()
	descriptor := "./godj.toml"
	tests := []struct {
		argv       []string
		descriptor string
		request    migrateprotocol.Request
	}{
		{argv: []string{"migrate"}, request: migrateprotocol.Request{Mode: migrateprotocol.ModeExecute, Target: migrateprotocol.Target{Kind: migrateprotocol.TargetLatest}}},
		{argv: []string{"migrate", "--project", descriptor}, descriptor: descriptor, request: migrateprotocol.Request{Mode: migrateprotocol.ModeExecute, Target: migrateprotocol.Target{Kind: migrateprotocol.TargetLatest}}},
		{argv: []string{"migrate", "--plan"}, request: migrateprotocol.Request{Mode: migrateprotocol.ModePlan, Target: migrateprotocol.Target{Kind: migrateprotocol.TargetLatest}}},
		{argv: []string{"migrate", "--plan", "--project", descriptor}, descriptor: descriptor, request: migrateprotocol.Request{Mode: migrateprotocol.ModePlan, Target: migrateprotocol.Target{Kind: migrateprotocol.TargetLatest}}},
		{argv: []string{"migrate", "blog", "0002_editor"}, request: migrateprotocol.Request{Mode: migrateprotocol.ModeExecute, Target: migrateprotocol.Target{Kind: migrateprotocol.TargetNamed, App: "blog", Name: "0002_editor"}}},
		{argv: []string{"migrate", "blog", "0002_editor", "--project", descriptor}, descriptor: descriptor, request: migrateprotocol.Request{Mode: migrateprotocol.ModeExecute, Target: migrateprotocol.Target{Kind: migrateprotocol.TargetNamed, App: "blog", Name: "0002_editor"}}},
		{argv: []string{"migrate", "blog", "0002_editor", "--plan"}, request: migrateprotocol.Request{Mode: migrateprotocol.ModePlan, Target: migrateprotocol.Target{Kind: migrateprotocol.TargetNamed, App: "blog", Name: "0002_editor"}}},
		{argv: []string{"migrate", "blog", "0002_editor", "--plan", "--project", descriptor}, descriptor: descriptor, request: migrateprotocol.Request{Mode: migrateprotocol.ModePlan, Target: migrateprotocol.Target{Kind: migrateprotocol.TargetNamed, App: "blog", Name: "0002_editor"}}},
		{argv: []string{"migrate", "blog", "zero"}, request: migrateprotocol.Request{Mode: migrateprotocol.ModeExecute, Target: migrateprotocol.Target{Kind: migrateprotocol.TargetZero, App: "blog"}}},
	}
	for _, test := range tests {
		arguments, failure := parseMigrateArguments(test.argv)
		if failure != nil || arguments.explicitDescriptor != test.descriptor || arguments.request != test.request {
			t.Fatalf("parseMigrateArguments(%q) = %+v, %+v", test.argv, arguments, failure)
		}
		request, parseFailure, failed, err := migrateprotocol.ReadRequest(bytes.NewReader(arguments.requestDocument))
		if err != nil || failed || parseFailure != (migrateprotocol.Failure{}) || request != test.request {
			t.Fatalf("encoded request for %q = %+v, %+v, %v, %v", test.argv, request, parseFailure, failed, err)
		}
	}
}

type migrateScriptedBackend struct {
	stages   []ProcessStage
	commands []Command
	build    ProcessResult
	runner   ProcessResult
}

func (backend *migrateScriptedBackend) Execute(_ context.Context, _ <-chan struct{}, stage ProcessStage, command Command) ProcessResult {
	backend.stages = append(backend.stages, stage)
	backend.commands = append(backend.commands, cloneCommand(command))
	if stage == BuildStage {
		return backend.build
	}
	return backend.runner
}

func TestRunMigrateSuccessUsesSeparateProtocolAndSingleRunner(t *testing.T) {
	t.Parallel()
	fixture := newGlobalFixture(t, 2)
	wire, err := migrateprotocol.EncodeResponse(migrateprotocol.Response{OK: true, Result: migrateprotocol.Result{
		Mode: migrateprotocol.ModeExecute,
		Execute: migrateprotocol.ExecuteResult{
			DefinitionSetDigest: migrateprotocol.EmptySetDigest,
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	backend := &migrateScriptedBackend{
		build: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1},
		runner: ProcessResult{
			Started: true, ExitCode: 0, DirectReaps: 1, Stdout: append([]byte(nil), wire...),
			StdoutScalar: StreamScalar{RetainedBytes: len(wire)},
		},
	}
	var stdout, stderr bytes.Buffer
	report := RunMigrate(MigrateInvocation{
		Context: context.Background(), CWD: fixture.cwd, Args: []string{"migrate"}, Environment: fixture.environment,
		Stdout: &stdout, Stderr: &stderr, Backend: backend,
	})
	if report.ExitCode != 0 || report.HasMigrateFailure || !report.HasMigrateResult || report.BuildCalls != 1 || report.RunnerCalls != 1 || report.RunnerResponseWrites != 1 || report.DirectChildReaps != 2 || report.UserStdoutWrites != 1 || report.UserStderrWrites != 0 || report.TempCreated != 1 || report.TempCleanupAttempts != 1 || report.CleanupFailed != 0 || report.ResidualTemp != 0 {
		t.Fatalf("migrate success report = %+v", report)
	}
	wantOutput := `{"source_count":0,"definition_count":0,"definition_set_digest":"` + migrateprotocol.EmptySetDigest + `"}` + "\n"
	if stdout.String() != wantOutput || stderr.Len() != 0 {
		t.Fatalf("migrate public output stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if !reflect.DeepEqual(backend.stages, []ProcessStage{BuildStage, MigrateRunnerStage}) || len(backend.commands) != 2 {
		t.Fatalf("migrate stages=%v commands=%d", backend.stages, len(backend.commands))
	}
	build := backend.commands[0]
	if !reflect.DeepEqual(build.Argv[:5], []string{"go", "build", "-buildvcs=false", "-mod=readonly", "-o"}) || build.Argv[len(build.Argv)-1] != "./cmd/site" || build.Dir != fixture.project {
		t.Fatalf("migrate build command = %+v", build)
	}
	runner := backend.commands[1]
	if len(runner.Argv) != 2 || runner.Argv[1] != migrateprotocol.PrivateArgument || runner.Dir != fixture.project || !bytes.Equal(runner.Stdin, migrateprotocol.RequestDocument()) {
		t.Fatalf("migrate runner command = %+v", runner)
	}
	if filepath.Dir(runner.Argv[0]) == fixture.project || !report.RawDiagnosticsDiscarded {
		t.Fatalf("runner was not isolated: %+v report=%+v", runner, report)
	}
}

func TestRunMigrateExactArgumentsPrecedeProjectSelection(t *testing.T) {
	t.Parallel()
	for index, arguments := range [][]string{
		{},
		{"migrate", "--project"},
		{"migrate", "--project", ""},
		{"migrate", "--project", "--plan"},
		{"migrate", "--plan", "--project", "--project"},
		{"migrate", "--project", "godj.toml", "extra"},
		{"migrate", "--check"},
		{"migrate", "blog"},
		{"migrate", "--project", "godj.toml", "--plan"},
		{"migrate", "--plan", "blog", "0001"},
		{"migrate", "blog", "0001", "--project", "godj.toml", "--plan"},
		{"migrate", "blog", "0001", "--project", "--plan"},
		{"migrate", "--unknown", "0001"},
		{"migrate", "-x", "0001"},
		{"migrate", "blog", "--unknown"},
		{"migrate", "blog", "-x"},
		{"migrate", string([]byte{0xff}), "0001"},
		{"migrate", "blog", strings.Repeat("x", migrateprotocol.MaxIdentityBytes+1)},
	} {
		arguments := append([]string(nil), arguments...)
		t.Run(fmt.Sprintf("case_%02d", index), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			report := RunMigrate(MigrateInvocation{
				Context: context.Background(), CWD: filepath.Join(t.TempDir(), "absent"), Args: arguments,
				Stdout: &stdout, Stderr: &stderr,
				Backend: backendFunc(func(context.Context, <-chan struct{}, ProcessStage, Command) ProcessResult {
					t.Fatal("backend called after invalid migrate arguments")
					return ProcessResult{}
				}),
			})
			if report.ExitCode != 2 || !report.HasMigrateFailure || report.MigrateFailure.Category != migrateprotocol.CategoryCommand || report.MigrateFailure.Code != migrateprotocol.CodeInvalidArguments || report.AncestorDirectoriesInspected != 0 || report.DescriptorReads != 0 || report.BuildCalls != 0 || report.RunnerCalls != 0 || stdout.Len() != 0 || stderr.String() != migrateprotocol.CategoryCommand+"/"+migrateprotocol.CodeInvalidArguments+"\n" {
				t.Fatalf("invalid migrate arguments %q = %+v stdout=%q stderr=%q", arguments, report, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunMigratePlanPublishesCanonicalRowsAndExactRequest(t *testing.T) {
	t.Parallel()
	fixture := newGlobalFixture(t, 0)
	rows := []migrateprotocol.PlanRow{
		{App: "authors", Name: "0001_author", Direction: migrateprotocol.DirectionForward},
		{App: "blog", Name: "0002_\u003cscript\u003e", Direction: migrateprotocol.DirectionForward},
	}
	wire, err := migrateprotocol.EncodeResponse(migrateprotocol.Response{OK: true, Result: migrateprotocol.Result{
		Mode: migrateprotocol.ModePlan,
		Plan: rows,
	}})
	if err != nil {
		t.Fatal(err)
	}
	backend := &migrateScriptedBackend{
		build: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1},
		runner: ProcessResult{
			Started: true, ExitCode: 0, DirectReaps: 1, Stdout: append([]byte(nil), wire...),
			StdoutScalar: StreamScalar{RetainedBytes: len(wire)},
		},
	}
	var stdout, stderr bytes.Buffer
	report := RunMigrate(MigrateInvocation{
		Context: context.Background(), CWD: fixture.project,
		Args: []string{"migrate", "blog", "0002_editor", "--plan"}, Environment: fixture.environment,
		Stdout: &stdout, Stderr: &stderr, Backend: backend,
	})
	if report.ExitCode != 0 || !report.HasMigratePlan || report.HasMigrateResult || report.HasMigrateFailure ||
		!reflect.DeepEqual(report.MigratePlan, rows) || report.BuildCalls != 1 || report.RunnerCalls != 1 ||
		report.UserStdoutWrites != 1 || report.UserStderrWrites != 0 || stderr.Len() != 0 {
		t.Fatalf("plan report = %+v stdout=%q stderr=%q", report, stdout.String(), stderr.String())
	}
	wantOutput, err := migrateprotocol.EncodePublicPlan(rows)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stdout.Bytes(), wantOutput) || bytes.Contains(stdout.Bytes(), []byte(`\u003c`)) {
		t.Fatalf("plan output = %q, want %q", stdout.Bytes(), wantOutput)
	}
	wantRequest, err := migrateprotocol.EncodeRequest(migrateprotocol.Request{
		Mode:   migrateprotocol.ModePlan,
		Target: migrateprotocol.Target{Kind: migrateprotocol.TargetNamed, App: "blog", Name: "0002_editor"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(backend.commands) != 2 || !bytes.Equal(backend.commands[1].Stdin, wantRequest) {
		t.Fatalf("private plan request = %q, want %q", backend.commands[1].Stdin, wantRequest)
	}
	rows[0].App = "mutated"
	if report.MigratePlan[0].App != "authors" {
		t.Fatal("report retained response plan alias")
	}
}

func TestRunMigrateRejectsSuccessModeMismatch(t *testing.T) {
	t.Parallel()
	fixture := newGlobalFixture(t, 0)
	wire, err := migrateprotocol.EncodeResponse(migrateprotocol.Response{OK: true, Result: migrateprotocol.Result{
		Mode: migrateprotocol.ModePlan,
		Plan: []migrateprotocol.PlanRow{},
	}})
	if err != nil {
		t.Fatal(err)
	}
	backend := &migrateScriptedBackend{
		build:  ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1},
		runner: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, Stdout: wire, StdoutScalar: StreamScalar{RetainedBytes: len(wire)}},
	}
	var stdout, stderr bytes.Buffer
	report := RunMigrate(MigrateInvocation{
		Context: context.Background(), CWD: fixture.project, Args: []string{"migrate"}, Environment: fixture.environment,
		Stdout: &stdout, Stderr: &stderr, Backend: backend,
	})
	if report.ExitCode != 3 || !report.HasMigrateFailure || report.HasMigrateResult || report.HasMigratePlan ||
		report.MigrateFailure != (migrateprotocol.Failure{Category: migrateprotocol.CategoryProtocol, Code: migrateprotocol.CodeInvalidResponse}) ||
		stdout.Len() != 0 || stderr.String() != migrateprotocol.CategoryProtocol+"/"+migrateprotocol.CodeInvalidResponse+"\n" {
		t.Fatalf("mode mismatch = %+v stdout=%q stderr=%q", report, stdout.String(), stderr.String())
	}
}

func TestRunMigrateRejectsExecuteSuccessForPlanRequest(t *testing.T) {
	t.Parallel()
	fixture := newGlobalFixture(t, 0)
	wire, err := migrateprotocol.EncodeResponse(migrateprotocol.Response{OK: true, Result: migrateprotocol.Result{
		Mode: migrateprotocol.ModeExecute,
		Execute: migrateprotocol.ExecuteResult{
			DefinitionSetDigest: migrateprotocol.EmptySetDigest,
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	backend := &migrateScriptedBackend{
		build:  ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1},
		runner: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, Stdout: wire, StdoutScalar: StreamScalar{RetainedBytes: len(wire)}},
	}
	var stdout, stderr bytes.Buffer
	report := RunMigrate(MigrateInvocation{
		Context: context.Background(), CWD: fixture.project, Args: []string{"migrate", "--plan"}, Environment: fixture.environment,
		Stdout: &stdout, Stderr: &stderr, Backend: backend,
	})
	if report.ExitCode != 3 || !report.HasMigrateFailure || report.HasMigrateResult || report.HasMigratePlan ||
		report.MigrateFailure != (migrateprotocol.Failure{Category: migrateprotocol.CategoryProtocol, Code: migrateprotocol.CodeInvalidResponse}) ||
		stdout.Len() != 0 || stderr.String() != migrateprotocol.CategoryProtocol+"/"+migrateprotocol.CodeInvalidResponse+"\n" {
		t.Fatalf("reverse mode mismatch = %+v stdout=%q stderr=%q", report, stdout.String(), stderr.String())
	}
}

func TestRunMigrateEmptyPlanAndShortPublication(t *testing.T) {
	t.Parallel()
	fixture := newGlobalFixture(t, 0)
	wire, err := migrateprotocol.EncodeResponse(migrateprotocol.Response{OK: true, Result: migrateprotocol.Result{
		Mode: migrateprotocol.ModePlan,
		Plan: []migrateprotocol.PlanRow{},
	}})
	if err != nil {
		t.Fatal(err)
	}
	newBackend := func() *migrateScriptedBackend {
		return &migrateScriptedBackend{
			build:  ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1},
			runner: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, Stdout: append([]byte(nil), wire...), StdoutScalar: StreamScalar{RetainedBytes: len(wire)}},
		}
	}

	var stdout, stderr bytes.Buffer
	report := RunMigrate(MigrateInvocation{
		Context: context.Background(), CWD: fixture.project, Args: []string{"migrate", "--plan"}, Environment: fixture.environment,
		Stdout: &stdout, Stderr: &stderr, Backend: newBackend(),
	})
	if report.ExitCode != 0 || !report.HasMigratePlan || report.MigratePlan == nil || len(report.MigratePlan) != 0 ||
		report.HasMigrateResult || report.HasMigrateFailure || stdout.String() != "{\"plan\":[]}\n" || stderr.Len() != 0 {
		t.Fatalf("empty plan = %+v stdout=%q stderr=%q", report, stdout.String(), stderr.String())
	}

	short := &shortWriter{}
	shortReport := RunMigrate(MigrateInvocation{
		Context: context.Background(), CWD: fixture.project, Args: []string{"migrate", "--plan"}, Environment: fixture.environment,
		Stdout: short, Stderr: &bytes.Buffer{}, Backend: newBackend(),
	})
	if shortReport.ExitCode != 3 || !shortReport.HasMigrateFailure || shortReport.HasMigratePlan || shortReport.MigratePlan != nil ||
		shortReport.HasMigrateResult || shortReport.MigrateFailure != migrateInternalFailure() ||
		shortReport.UserStdoutWrites != 1 || shortReport.PartialStdoutWrites != 1 {
		t.Fatalf("short plan publication = %+v", shortReport)
	}
}

func TestRunMigratePlanCleanupFailureDiscardsPlan(t *testing.T) {
	t.Parallel()
	fixture := newGlobalFixture(t, 0)
	wire, err := migrateprotocol.EncodeResponse(migrateprotocol.Response{OK: true, Result: migrateprotocol.Result{
		Mode: migrateprotocol.ModePlan,
		Plan: []migrateprotocol.PlanRow{},
	}})
	if err != nil {
		t.Fatal(err)
	}
	backend := &migrateScriptedBackend{
		build:  ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1},
		runner: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, Stdout: wire, StdoutScalar: StreamScalar{RetainedBytes: len(wire)}},
	}
	var stdout, stderr bytes.Buffer
	report := RunMigrate(MigrateInvocation{
		Context: context.Background(), CWD: fixture.project, Args: []string{"migrate", "--plan"}, Environment: fixture.environment,
		Stdout: &stdout, Stderr: &stderr, Backend: backend,
		workspace: workspaceHooks{afterRootCreated: func(_ string, base *os.File) { _ = base.Close() }},
	})
	if report.ExitCode != 3 || !report.HasMigrateFailure || report.HasMigrateResult || report.HasMigratePlan || report.MigratePlan != nil ||
		report.MigrateFailure != migrateCleanupFailure() || report.CleanupFailed != 1 || stdout.Len() != 0 ||
		stderr.String() != migrateprotocol.CategoryProcess+"/"+migrateprotocol.CodeProjectCleanupFailed+"\n" {
		t.Fatalf("plan cleanup failure = %+v stdout=%q stderr=%q", report, stdout.String(), stderr.String())
	}
}

func TestRunMigrateExplicitProjectAndLogicalFailureAreSingleAttempt(t *testing.T) {
	t.Parallel()
	fixture := newGlobalFixture(t, 0)
	logical := migrateprotocol.Failure{
		Category: migrateprotocol.CategoryBackend, Code: migrateprotocol.CodeBackendOpenFailed, CleanupFailed: true,
	}
	wire, err := migrateprotocol.EncodeResponse(migrateprotocol.Response{Failure: logical})
	if err != nil {
		t.Fatal(err)
	}
	backend := &migrateScriptedBackend{
		build: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1},
		runner: ProcessResult{
			Started: true, ExitCode: 0, DirectReaps: 1, Stdout: append([]byte(nil), wire...),
			StdoutScalar: StreamScalar{RetainedBytes: len(wire)},
		},
	}
	var stdout, stderr bytes.Buffer
	report := RunMigrate(MigrateInvocation{
		Context: context.Background(), CWD: filepath.Dir(fixture.project),
		Args:        []string{"migrate", "--project", filepath.Join(fixture.project, descriptorName)},
		Environment: fixture.environment, Stdout: &stdout, Stderr: &stderr, Backend: backend,
	})
	if report.ExitCode != 3 || !report.HasMigrateFailure || report.HasMigrateResult || report.MigrateFailure != logical || report.BuildCalls != 1 || report.RunnerCalls != 1 || len(backend.stages) != 2 || report.CleanupFailed != 1 || stdout.Len() != 0 || stderr.String() != logical.Category+"/"+logical.Code+"\n" {
		t.Fatalf("logical migrate failure = %+v stages=%v stdout=%q stderr=%q", report, backend.stages, stdout.String(), stderr.String())
	}
}

func TestRunMigrateCompletedCommitUnknownPrecedesLateCancellation(t *testing.T) {
	t.Parallel()
	fixture := newGlobalFixture(t, 0)
	logical := migrateprotocol.Failure{Category: migrateprotocol.CategoryTransaction, Code: "commit_outcome_unknown"}
	wire, err := migrateprotocol.EncodeResponse(migrateprotocol.Response{Failure: logical})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runnerCalls := 0
	backend := backendFunc(func(_ context.Context, _ <-chan struct{}, stage ProcessStage, _ Command) ProcessResult {
		if stage == BuildStage {
			return ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1}
		}
		runnerCalls++
		cancel()
		return ProcessResult{
			Started: true, ExitCode: 0, DirectReaps: 1, Stdout: append([]byte(nil), wire...),
			StdoutScalar: StreamScalar{RetainedBytes: len(wire)},
		}
	})
	var stdout, stderr bytes.Buffer
	report := RunMigrate(MigrateInvocation{
		Context: ctx, CWD: fixture.project, Args: []string{"migrate"}, Environment: fixture.environment,
		Stdout: &stdout, Stderr: &stderr, Backend: backend,
	})
	if report.ExitCode != 3 || !report.HasMigrateFailure || report.MigrateFailure != logical || runnerCalls != 1 || stdout.Len() != 0 || stderr.String() != logical.Category+"/"+logical.Code+"\n" {
		t.Fatalf("commit unknown precedence = %+v runner_calls=%d stdout=%q stderr=%q", report, runnerCalls, stdout.String(), stderr.String())
	}
}

func TestRunMigrateTransportAndCleanupPrecedeResponseBytes(t *testing.T) {
	t.Parallel()
	fixture := newGlobalFixture(t, 0)
	wire, err := migrateprotocol.EncodeResponse(migrateprotocol.Response{OK: true, Result: migrateprotocol.Result{
		Mode:    migrateprotocol.ModeExecute,
		Execute: migrateprotocol.ExecuteResult{DefinitionSetDigest: migrateprotocol.EmptySetDigest},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name        string
		runner      ProcessResult
		wantCode    string
		wantCleanup int
	}{
		{
			name: "nonzero transport before valid truncated wire",
			runner: ProcessResult{
				Started: true, ExitCode: 7, DirectReaps: 1, Stdout: append([]byte(nil), wire...),
				StdoutScalar: StreamScalar{RetainedBytes: len(wire), Truncated: true},
			},
			wantCode: migrateprotocol.CodeRunnerFailed,
		},
		{
			name: "owner cleanup before valid wire",
			runner: ProcessResult{
				Started: true, ExitCode: 0, DirectReaps: 1, CleanupFailed: true, Stdout: append([]byte(nil), wire...),
				StdoutScalar: StreamScalar{RetainedBytes: len(wire)},
			},
			wantCode: migrateprotocol.CodeProjectCleanupFailed, wantCleanup: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			backing := test.runner.Stdout
			backend := &migrateScriptedBackend{build: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1}, runner: test.runner}
			var stdout, stderr bytes.Buffer
			report := RunMigrate(MigrateInvocation{
				Context: context.Background(), CWD: fixture.project, Args: []string{"migrate"}, Environment: fixture.environment,
				Stdout: &stdout, Stderr: &stderr, Backend: backend,
			})
			if report.ExitCode != 3 || !report.HasMigrateFailure || report.MigrateFailure.Code != test.wantCode || report.CleanupFailed != test.wantCleanup || report.RunnerCalls != 1 || stdout.Len() != 0 {
				t.Fatalf("%s = %+v stdout=%q stderr=%q", test.name, report, stdout.String(), stderr.String())
			}
			if !bytes.Equal(backing, make([]byte, len(backing))) {
				t.Fatalf("%s retained raw response: %q", test.name, backing)
			}
		})
	}
}

func TestRunMigrateShortPublicationFailsClosed(t *testing.T) {
	t.Parallel()
	fixture := newGlobalFixture(t, 0)
	wire, err := migrateprotocol.EncodeResponse(migrateprotocol.Response{OK: true, Result: migrateprotocol.Result{
		Mode:    migrateprotocol.ModeExecute,
		Execute: migrateprotocol.ExecuteResult{DefinitionSetDigest: migrateprotocol.EmptySetDigest},
	}})
	if err != nil {
		t.Fatal(err)
	}
	backend := &migrateScriptedBackend{
		build:  ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1},
		runner: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, Stdout: wire, StdoutScalar: StreamScalar{RetainedBytes: len(wire)}},
	}
	short := &shortWriter{}
	report := RunMigrate(MigrateInvocation{
		Context: context.Background(), CWD: fixture.project, Args: []string{"migrate"}, Environment: fixture.environment,
		Stdout: short, Stderr: &bytes.Buffer{}, Backend: backend,
	})
	if report.ExitCode != 3 || !report.HasMigrateFailure || report.HasMigrateResult || report.MigrateFailure.Category != migrateprotocol.CategoryInternal || report.MigrateFailure.Code != migrateprotocol.CodeProjectInternalError || report.UserStdoutWrites != 1 || report.PartialStdoutWrites != 1 {
		t.Fatalf("short migrate publication = %+v", report)
	}
}
