//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/projectcheck/sqlmigrateprotocol"
)

type sqlMigrateScriptedBackend struct {
	stages   []ProcessStage
	commands []Command
	build    ProcessResult
	runner   ProcessResult
}

func (backend *sqlMigrateScriptedBackend) Execute(
	_ context.Context,
	_ <-chan struct{},
	stage ProcessStage,
	command Command,
) ProcessResult {
	backend.stages = append(backend.stages, stage)
	backend.commands = append(backend.commands, cloneCommand(command))
	if stage == BuildStage {
		return backend.build
	}
	return backend.runner
}

func TestParseSQLMigrateArgumentsExactFormsAndLiteralZero(t *testing.T) {
	t.Parallel()
	tests := []struct {
		argv       []string
		descriptor string
		request    sqlmigrateprotocol.Request
	}{
		{
			argv:    []string{"sqlmigrate", "blog", "0001_article"},
			request: sqlmigrateprotocol.Request{App: "blog", Name: "0001_article"},
		},
		{
			argv:       []string{"sqlmigrate", "blog", "zero", "--project", "./godj.toml"},
			descriptor: "./godj.toml",
			request:    sqlmigrateprotocol.Request{App: "blog", Name: "zero"},
		},
	}
	for _, test := range tests {
		arguments, failure := parseSQLMigrateArguments(test.argv)
		if failure != nil || arguments.explicitDescriptor != test.descriptor || arguments.request != test.request {
			t.Fatalf("parseSQLMigrateArguments(%q) = %+v, %+v", test.argv, arguments, failure)
		}
		request, parseFailure, failed, err := sqlmigrateprotocol.ReadRequest(bytes.NewReader(arguments.requestDocument))
		if err != nil || failed || parseFailure != (sqlmigrateprotocol.Failure{}) || request != test.request {
			t.Fatalf("encoded request %q = %+v, %+v, %v, %v", test.argv, request, parseFailure, failed, err)
		}
	}
}

func TestRunSQLMigrateUsesSeparateProtocolAndCanonicalSinglePublication(t *testing.T) {
	t.Parallel()
	fixture := newGlobalFixture(t, 2)
	result := sqlmigrateprotocol.Result{Statements: []string{
		`CREATE TABLE "article" ("id" integer)`,
		"ALTER TABLE \"article\"\nADD COLUMN \"title\" text",
	}}
	wire, err := sqlmigrateprotocol.EncodeResponse(sqlmigrateprotocol.Response{OK: true, Result: result})
	if err != nil {
		t.Fatal(err)
	}
	responseBacking := append([]byte(nil), wire...)
	backend := &sqlMigrateScriptedBackend{
		build: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1},
		runner: ProcessResult{
			Started: true, ExitCode: 0, DirectReaps: 1,
			Stdout: responseBacking, StdoutScalar: StreamScalar{RetainedBytes: len(responseBacking)},
		},
	}
	var stdout, stderr bytes.Buffer
	report := RunSQLMigrate(SQLMigrateInvocation{
		Context: context.Background(), CWD: fixture.cwd,
		Args:        []string{"sqlmigrate", "blog", "0001_article"},
		Environment: fixture.environment,
		Stdout:      &stdout,
		Stderr:      &stderr,
		Backend:     backend,
	})
	if report.ExitCode != 0 || report.HasSQLMigrateFailure || !report.HasSQLMigrateResult ||
		report.BuildCalls != 1 || report.RunnerCalls != 1 || report.RunnerResponseWrites != 1 ||
		report.RunnerStdoutRetainedBytes != len(wire) || report.RunnerStdoutTruncated ||
		report.DirectChildReaps != 2 || report.UserStdoutWrites != 1 || report.UserStderrWrites != 0 ||
		report.TempCreated != 1 || report.TempCleanupAttempts != 1 || report.CleanupFailed != 0 ||
		report.ResidualTemp != 0 || !report.RawDiagnosticsDiscarded {
		t.Fatalf("sqlmigrate success report = %+v", report)
	}
	want := "CREATE TABLE \"article\" (\"id\" integer);\nALTER TABLE \"article\"\nADD COLUMN \"title\" text;\n"
	if stdout.String() != want || stderr.Len() != 0 {
		t.Fatalf("public output stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if !reflect.DeepEqual(backend.stages, []ProcessStage{BuildStage, SQLMigrateRunnerStage}) || len(backend.commands) != 2 {
		t.Fatalf("stages=%v commands=%d", backend.stages, len(backend.commands))
	}
	runner := backend.commands[1]
	wantRequest, err := sqlmigrateprotocol.EncodeRequest(sqlmigrateprotocol.Request{App: "blog", Name: "0001_article"})
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.Argv) != 2 || runner.Argv[1] != sqlmigrateprotocol.PrivateArgument ||
		runner.Dir != fixture.project || !bytes.Equal(runner.Stdin, wantRequest) || filepath.Dir(runner.Argv[0]) == fixture.project {
		t.Fatalf("runner command = %+v", runner)
	}
	if !bytes.Equal(responseBacking, make([]byte, len(responseBacking))) {
		t.Fatal("raw private SQL response was not zeroized")
	}
}

func TestRunSQLMigrateEmptyResultPerformsNoPublicWrite(t *testing.T) {
	t.Parallel()
	fixture := newGlobalFixture(t, 0)
	wire, err := sqlmigrateprotocol.EncodeResponse(sqlmigrateprotocol.Response{
		OK: true, Result: sqlmigrateprotocol.Result{Statements: []string{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	backend := &sqlMigrateScriptedBackend{
		build: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1},
		runner: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, Stdout: wire,
			StdoutScalar: StreamScalar{RetainedBytes: len(wire)}},
	}
	stdout := &sqlMigrateCountingWriter{}
	var stderr bytes.Buffer
	report := RunSQLMigrate(SQLMigrateInvocation{
		Context: context.Background(), CWD: fixture.project,
		Args: []string{"sqlmigrate", "blog", "zero"}, Environment: fixture.environment,
		Stdout: stdout, Stderr: &stderr, Backend: backend,
	})
	if report.ExitCode != 0 || !report.HasSQLMigrateResult || report.SQLMigrateResult.Statements == nil ||
		len(report.SQLMigrateResult.Statements) != 0 || report.UserStdoutWrites != 0 || stdout.calls != 0 ||
		stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("empty result = %+v calls=%d stdout=%q stderr=%q", report, stdout.calls, stdout.String(), stderr.String())
	}
}

func TestRunSQLMigrateInvalidArgumentsPrecedeEveryAcquisition(t *testing.T) {
	t.Parallel()
	invalid := [][]string{
		{},
		{"sqlmigrate"},
		{"sqlmigrate", "blog"},
		{"sqlmigrate", "blog", "latest"},
		{"sqlmigrate", "blog", "0001", "--backwards"},
		{"sqlmigrate", "--project", "godj.toml", "blog", "0001"},
		{"sqlmigrate", "blog", "0001", "--project"},
		{"sqlmigrate", "blog", "0001", "--project", "-descriptor"},
		{"sqlmigrate", "-blog", "0001"},
		{"sqlmigrate", "blog", "-0001"},
		{"sqlmigrate", "Blog", "0001"},
		{"sqlmigrate", "blog-app", "0001"},
		{"sqlmigrate", "blog", "0001", "--project", "godj.toml", "extra"},
		{"sqlmigrate", string([]byte{0xff}), "0001"},
		{"sqlmigrate", "blog", strings.Repeat("x", sqlmigrateprotocol.MaxIdentityBytes+1)},
	}
	for index, arguments := range invalid {
		arguments := append([]string(nil), arguments...)
		t.Run(fmt.Sprintf("case_%02d", index), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			report := RunSQLMigrate(SQLMigrateInvocation{
				Context: context.Background(), CWD: filepath.Join(t.TempDir(), "missing"), Args: arguments,
				Stdout: &stdout, Stderr: &stderr,
				Backend: backendFunc(func(context.Context, <-chan struct{}, ProcessStage, Command) ProcessResult {
					t.Fatal("backend called after invalid sqlmigrate arguments")
					return ProcessResult{}
				}),
			})
			want := sqlmigrateprotocol.Failure{Category: sqlmigrateprotocol.CategoryCommand, Code: sqlmigrateprotocol.CodeInvalidArguments}
			if report.ExitCode != 2 || !report.HasSQLMigrateFailure || report.SQLMigrateFailure != want ||
				report.AncestorDirectoriesInspected != 0 || report.DescriptorReads != 0 || report.BuildCalls != 0 ||
				report.RunnerCalls != 0 || stdout.Len() != 0 || stderr.String() != want.Category+"/"+want.Code+"\n" {
				t.Fatalf("invalid args %q = %+v stdout=%q stderr=%q", arguments, report, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunSQLMigrateFailureTruncationAndTerminalWriteSemantics(t *testing.T) {
	t.Parallel()
	fixture := newGlobalFixture(t, 0)
	logical := sqlmigrateprotocol.Failure{Category: sqlmigrateprotocol.CategoryCapability, Code: "unsupported_operation"}
	wire, err := sqlmigrateprotocol.EncodeResponse(sqlmigrateprotocol.Response{Failure: logical})
	if err != nil {
		t.Fatal(err)
	}
	backend := &sqlMigrateScriptedBackend{
		build: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1},
		runner: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, Stdout: wire,
			StdoutScalar: StreamScalar{RetainedBytes: len(wire)},
			StderrScalar: StreamScalar{RetainedBytes: len("secret child diagnostic")}},
	}
	var stdout, stderr bytes.Buffer
	report := RunSQLMigrate(SQLMigrateInvocation{
		Context: context.Background(), CWD: fixture.project,
		Args: []string{"sqlmigrate", "blog", "0001"}, Environment: fixture.environment,
		Stdout: &stdout, Stderr: &stderr, Backend: backend,
	})
	if report.ExitCode != 1 || !report.HasSQLMigrateFailure || report.SQLMigrateFailure != logical ||
		stdout.Len() != 0 || stderr.String() != logical.Category+"/"+logical.Code+"\n" ||
		report.RunnerStderrRetainedBytes != len("secret child diagnostic") {
		t.Fatalf("logical failure = %+v stdout=%q stderr=%q", report, stdout.String(), stderr.String())
	}

	backend = &sqlMigrateScriptedBackend{
		build: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1},
		runner: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1,
			Stdout:       append([]byte(nil), wire...),
			StdoutScalar: StreamScalar{RetainedBytes: len(wire), Truncated: true}},
	}
	stdout.Reset()
	stderr.Reset()
	report = RunSQLMigrate(SQLMigrateInvocation{
		Context: context.Background(), CWD: fixture.project,
		Args: []string{"sqlmigrate", "blog", "0001"}, Environment: fixture.environment,
		Stdout: &stdout, Stderr: &stderr, Backend: backend,
	})
	wantResource := sqlmigrateprotocol.Failure{Category: sqlmigrateprotocol.CategorySQLResource, Code: sqlmigrateprotocol.CodeRenderedSQLResourceLimit}
	if report.ExitCode != 1 || report.SQLMigrateFailure != wantResource || !report.RunnerStdoutTruncated ||
		stdout.Len() != 0 || stderr.String() != wantResource.Category+"/"+wantResource.Code+"\n" {
		t.Fatalf("truncated response = %+v stdout=%q stderr=%q", report, stdout.String(), stderr.String())
	}

	successWire, err := sqlmigrateprotocol.EncodeResponse(sqlmigrateprotocol.Response{
		OK: true, Result: sqlmigrateprotocol.Result{Statements: []string{"SELECT 1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	successWireTemplate := append([]byte(nil), successWire...)
	backend = &sqlMigrateScriptedBackend{
		build: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1},
		runner: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, Stdout: append([]byte(nil), successWireTemplate...),
			StdoutScalar: StreamScalar{RetainedBytes: len(successWireTemplate)}},
	}
	short := &shortWriter{}
	stderr.Reset()
	report = RunSQLMigrate(SQLMigrateInvocation{
		Context: context.Background(), CWD: fixture.project,
		Args: []string{"sqlmigrate", "blog", "0001"}, Environment: fixture.environment,
		Stdout: short, Stderr: &stderr, Backend: backend,
	})
	if report.ExitCode != 3 || !report.HasSQLMigrateFailure || report.HasSQLMigrateResult ||
		report.SQLMigrateFailure != sqlMigrateInternalFailure() || report.UserStdoutWrites != 1 ||
		report.PartialStdoutWrites != 1 || report.UserStderrWrites != 0 || stderr.Len() != 0 || short.Len() == 0 {
		t.Fatalf("short publication = %+v stdout=%q stderr=%q", report, short.String(), stderr.String())
	}

	failedWriter := &sqlMigrateErrorWriter{err: errors.New("terminal failed")}
	stderr.Reset()
	backend.runner.Stdout = append([]byte(nil), successWireTemplate...)
	report = RunSQLMigrate(SQLMigrateInvocation{
		Context: context.Background(), CWD: fixture.project,
		Args: []string{"sqlmigrate", "blog", "0001"}, Environment: fixture.environment,
		Stdout: failedWriter, Stderr: &stderr, Backend: backend,
	})
	if report.ExitCode != 3 || failedWriter.calls != 1 || report.UserStdoutWrites != 1 ||
		report.UserStderrWrites != 0 || report.PartialStdoutWrites != 0 || stderr.Len() != 0 {
		t.Fatalf("failed publication = %+v calls=%d stderr=%q", report, failedWriter.calls, stderr.String())
	}
}

func TestRunSQLMigrateCancellationAndClosedResponseBoundary(t *testing.T) {
	t.Run("pre-canceled before selection", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var stdout, stderr bytes.Buffer
		report := RunSQLMigrate(SQLMigrateInvocation{
			Context: ctx, CWD: filepath.Join(t.TempDir(), "missing"),
			Args: []string{"sqlmigrate", "blog", "0001"}, Stdout: &stdout, Stderr: &stderr,
			Backend: backendFunc(func(context.Context, <-chan struct{}, ProcessStage, Command) ProcessResult {
				t.Fatal("backend called after cancellation")
				return ProcessResult{}
			}),
		})
		want := sqlmigrateprotocol.Failure{Category: sqlmigrateprotocol.CategoryProcess, Code: sqlmigrateprotocol.CodeProjectCanceled}
		if report.ExitCode != 3 || report.SQLMigrateFailure != want || report.BuildCalls != 0 ||
			report.RunnerCalls != 0 || stdout.Len() != 0 || stderr.String() != want.Category+"/"+want.Code+"\n" {
			t.Fatalf("pre-canceled = %+v stdout=%q stderr=%q", report, stdout.String(), stderr.String())
		}
	})

	t.Run("parsed response precedes later cancellation", func(t *testing.T) {
		fixture := newGlobalFixture(t, 0)
		wire, err := sqlmigrateprotocol.EncodeResponse(sqlmigrateprotocol.Response{
			OK: true, Result: sqlmigrateprotocol.Result{Statements: []string{"SELECT 1"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		backend := backendFunc(func(_ context.Context, _ <-chan struct{}, stage ProcessStage, _ Command) ProcessResult {
			switch stage {
			case BuildStage:
				return ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1}
			case SQLMigrateRunnerStage:
				cancel()
				return ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, Stdout: wire,
					StdoutScalar: StreamScalar{RetainedBytes: len(wire)}}
			default:
				t.Fatalf("unexpected stage %d", stage)
				return ProcessResult{}
			}
		})
		var stdout, stderr bytes.Buffer
		report := RunSQLMigrate(SQLMigrateInvocation{
			Context: ctx, CWD: fixture.project,
			Args: []string{"sqlmigrate", "blog", "0001"}, Environment: fixture.environment,
			Stdout: &stdout, Stderr: &stderr, Backend: backend,
		})
		if !errors.Is(ctx.Err(), context.Canceled) || report.ExitCode != 0 || !report.HasSQLMigrateResult ||
			report.HasSQLMigrateFailure || stdout.String() != "SELECT 1;\n" || stderr.Len() != 0 {
			t.Fatalf("closed response = %+v stdout=%q stderr=%q ctx=%v", report, stdout.String(), stderr.String(), ctx.Err())
		}
	})
}

func TestRenderSQLMigrateBoundsAndOwnedProcessPolicy(t *testing.T) {
	t.Parallel()
	empty, err := renderSQLMigrate(SQLMigrateResult{Statements: []string{}})
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty render = %#v, %v", empty, err)
	}
	body := strings.Repeat("X", sqlmigrateprotocol.MaxStatementBodyBytes)
	output, err := renderSQLMigrate(SQLMigrateResult{Statements: []string{body}})
	if err != nil || len(output) != len(body)+2 || !bytes.HasSuffix(output, []byte(";\n")) {
		t.Fatalf("exact render = bytes %d err %v", len(output), err)
	}
	if _, err := renderSQLMigrate(SQLMigrateResult{Statements: []string{body + "X"}}); err == nil {
		t.Fatal("one-over public body rendered")
	}
	policy, ok := ownedResponseProcessPolicyForStage(SQLMigrateRunnerStage)
	if !ok || policy.stdoutMaximum != sqlmigrateprotocol.MaxResponseBytes ||
		policy.stderrMaximum != maxDiagnosticBytes || policy.grace != ownedProcessGrace {
		t.Fatalf("SQL process policy = %+v, %v", policy, ok)
	}
}

type sqlMigrateCountingWriter struct {
	bytes.Buffer
	calls int
}

func (writer *sqlMigrateCountingWriter) Write(payload []byte) (int, error) {
	writer.calls++
	return writer.Buffer.Write(payload)
}

type sqlMigrateErrorWriter struct {
	err   error
	calls int
}

func (writer *sqlMigrateErrorWriter) Write([]byte) (int, error) {
	writer.calls++
	return 0, writer.err
}

var _ io.Writer = (*sqlMigrateCountingWriter)(nil)
var _ io.Writer = (*sqlMigrateErrorWriter)(nil)
