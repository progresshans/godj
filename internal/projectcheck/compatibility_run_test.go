//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/projectgenerate"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/internal/projectcheck/createsuperuserprotocol"
	"github.com/progresshans/godj/internal/projectcheck/failurecode"
	"github.com/progresshans/godj/internal/projectcheck/migrateprotocol"
	"github.com/progresshans/godj/internal/projectcheck/protocol"
	"github.com/progresshans/godj/internal/projectcheck/showmigrationsprotocol"
	"github.com/progresshans/godj/internal/projectcheck/sqlmigrateprotocol"
	"github.com/progresshans/godj/project/compatibility"
	"github.com/progresshans/godj/version"
)

type compatibilityRunBackend struct {
	info       compatibility.Info
	probe      ProcessResult
	inventory  []byte
	commands   []Command
	stages     []ProcessStage
	afterProbe func()
	unexpected int
}

func (b *compatibilityRunBackend) ToolInfo() compatibility.Info { return b.info }
func (b *compatibilityRunBackend) Execute(_ context.Context, _ <-chan struct{}, stage ProcessStage, command Command) ProcessResult {
	b.stages = append(b.stages, stage)
	b.commands = append(b.commands, cloneCommand(command))
	switch stage {
	case BuildStage:
		return ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1}
	case CompatibilityStage:
		if b.afterProbe != nil {
			b.afterProbe()
		}
		return b.probe
	case MakemigrationsInventoryStage:
		data := append([]byte(nil), b.inventory...)
		return ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, Stdout: data, StdoutScalar: StreamScalar{RetainedBytes: len(data)}}
	default:
		b.unexpected++
		return ProcessResult{ExitCode: 1}
	}
}

func TestEveryGlobalCommandRejectsIncompatibleDeclarationBeforeInvocation(t *testing.T) {
	for _, name := range []string{"migrations", "generate", "makemigrations", "migrate", "showmigrations", "sqlmigrate", "createsuperuser", "runserver", "commands"} {
		t.Run(name, func(t *testing.T) {
			fixture := newMakemigrationsRunFixture(t, true)
			if err := os.MkdirAll(filepath.Join(fixture.root, "cmd/runtime"), 0700); err != nil {
				t.Fatal(err)
			}
			descriptor := "format_version = 1\n[project]\npackage = \"./cmd/site\"\nrunserver_package = \"./cmd/runtime\"\ncommands_package = \"./cmd/runtime\"\n"
			if err := os.WriteFile(filepath.Join(fixture.root, "godj.toml"), []byte(descriptor), 0600); err != nil {
				t.Fatal(err)
			}
			info := fixtureToolInfo()
			linked := info
			linked.Contract.Schema++
			wire, err := json.Marshal(linked)
			if err != nil {
				t.Fatal(err)
			}
			wire = append(wire, '\n')
			b := &compatibilityRunBackend{info: info, inventory: fixture.inventory, probe: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, Stdout: wire, StdoutScalar: StreamScalar{RetainedBytes: len(wire)}, StderrScalar: StreamScalar{RetainedBytes: 100, Truncated: true}}}
			var out, diag bytes.Buffer
			before := generationTreeSnapshot(t, fixture.root)
			var report Report
			var code string
			switch name {
			case "migrations":
				r := Run(Invocation{Context: t.Context(), CWD: fixture.root, Args: []string{"migrations", "check"}, Environment: fixture.environment, Backend: b, Stdout: &out, Stderr: &diag})
				report = r
				code = r.Failure.Code
			case "generate":
				r := RunGenerate(GenerationInvocation{Context: t.Context(), CWD: fixture.root, Args: []string{name}, Environment: fixture.environment, Backend: b, Stdout: &out, Stderr: &diag})
				report = r.Report
				code = r.GenerationFailure.Code
			case "makemigrations":
				r := RunMakemigrations(MakemigrationsInvocation{Context: t.Context(), CWD: fixture.root, Args: []string{name}, Environment: fixture.environment, Backend: b, Stdout: &out, Stderr: &diag})
				report = r.Report
				code = r.MakemigrationsFailure.Code
			case "migrate":
				r := RunMigrate(MigrateInvocation{Context: t.Context(), CWD: fixture.root, Args: []string{name}, Environment: fixture.environment, Backend: b, Stdout: &out, Stderr: &diag})
				report = r.Report
				code = r.MigrateFailure.Code
			case "showmigrations":
				r := RunShowMigrations(ShowMigrationsInvocation{Context: t.Context(), CWD: fixture.root, Args: []string{name}, Environment: fixture.environment, Backend: b, Stdout: &out, Stderr: &diag})
				report = r.Report
				code = r.ShowMigrationsFailure.Code
			case "sqlmigrate":
				r := RunSQLMigrate(SQLMigrateInvocation{Context: t.Context(), CWD: fixture.root, Args: []string{name, "blog", "0001_initial"}, Environment: fixture.environment, Backend: b, Stdout: &out, Stderr: &diag})
				report = r.Report
				code = r.SQLMigrateFailure.Code
			case "createsuperuser":
				r := runCreatesuperuser(CreatesuperuserInvocation{Context: t.Context(), CWD: fixture.root, Args: []string{name}, Environment: fixture.environment, Backend: b, Stdout: &out, Stderr: &diag}, createsuperuserRunHooks{
					readTerminal: func(context.Context, <-chan struct{}, *os.File, io.Writer, *CreatesuperuserReport) ([]byte, []byte, *CreatesuperuserFailure) {
						t.Error("incompatible launcher read credentials")
						return nil, nil, nil
					},
					executeSensitiveProcess: func(context.Context, <-chan struct{}, createsuperuserProcessCommand, []byte) createsuperuserProcessResult {
						t.Error("incompatible launcher entered sensitive child")
						return createsuperuserProcessResult{}
					},
				})
				report = r.Report
				code = r.CreatesuperuserFailure.Code
				if r.TerminalChecks != 0 || r.SensitiveRequestWriteAttempts != 0 || r.KnownCreated {
					t.Fatal("operator I/O preceded compatibility", r)
				}
			case "runserver":
				r := RunServer(RunServerInvocation{Context: t.Context(), CWD: fixture.root, Args: []string{name, "--no-reload"}, Environment: fixture.environment, Backend: b, Stdout: &out, Stderr: &diag, runtime: runserverRuntimeHooks{execute: func(context.Context, <-chan struct{}, Command, io.Writer, io.Writer, time.Duration) runserverProcessResult {
					t.Error("incompatible server started")
					return runserverProcessResult{}
				}}})
				report = r.Report
				code = r.RunServerFailure.Code
			case "commands":
				r := RunManagement(ManagementInvocation{Context: t.Context(), CWD: fixture.root, Args: []string{name}, Environment: fixture.environment, Backend: b, Stdout: &out, Stderr: &diag})
				report = r.Report
				code = r.FailureCode
				if r.CommandStarted {
					t.Fatal("incompatible command started")
				}
			}
			if code != "framework_incompatible" || report.CompatibilityChecks != 1 || report.CompatibilityResponseWrites != 1 || report.BuildCalls != 1 || report.RunnerCalls != 0 || b.unexpected != 0 || out.Len() != 0 || report.CleanupFailed != 0 || report.ResidualTemp != 0 || report.TempCleanupAttempts != 1 {
				t.Fatalf("compatibility did not guard %s: %+v code=%s stderr=%q", name, report, code, diag.String())
			}
			if !reflect.DeepEqual(before, generationTreeSnapshot(t, fixture.root)) {
				t.Fatal("incompatible command changed source or data")
			}
			probe := b.commands[len(b.commands)-1]
			if len(probe.Argv) != 2 || probe.Argv[1] != compatibility.PrivateArgument || len(probe.Stdin) != 0 || !strings.HasSuffix(probe.Argv[0], "/godj-project-runner") {
				t.Fatalf("invalid metadata probe: %+v", probe)
			}
			for _, v := range wire {
				if v != 0 {
					t.Fatal("private metadata buffer retained")
				}
			}
		})
	}
}

func TestCompatibilityProbeFailuresAreClosedAndCleanedBeforeCommand(t *testing.T) {
	for _, name := range []string{"unknown tool", "start failure", "nonzero exit", "truncated", "malformed", "unknown field", "cleanup", "canceled", "interrupted"} {
		t.Run(name, func(t *testing.T) {
			fixture := newGlobalFixture(t, 0)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			interrupt := make(chan struct{})
			b := &compatibilityRunBackend{info: fixtureToolInfo(), probe: fixtureCompatibilityProcess()}
			want := Failure{Category: failurecode.CategoryCompatibility, Code: "compatibility_probe_failed"}
			wantChecks := 1
			switch name {
			case "unknown tool":
				b.info.Framework = version.Info{Module: version.Module, Origin: version.Unknown}
				want.Code = "framework_identity_unknown"
				wantChecks = 0
			case "start failure":
				b.probe = ProcessResult{ExitCode: -1}
			case "nonzero exit":
				b.probe.ExitCode = 9
			case "truncated":
				b.probe.StdoutScalar.Truncated = true
				want.Code = "compatibility_response_invalid"
			case "malformed":
				b.probe.Stdout = []byte("private-secret-invalid-json")
				want.Code = "compatibility_response_invalid"
			case "unknown field":
				b.probe.Stdout = bytes.Replace(b.probe.Stdout, []byte(`"format_version":1`), []byte(`"format_version":1,"secret":"private-marker"`), 1)
				want.Code = "compatibility_response_invalid"
			case "cleanup":
				b.probe.CleanupFailed = true
				b.afterProbe = cancel
				want = Failure{Category: protocol.CategoryProcess, Code: protocol.CodeProjectCleanupFailed}
			case "canceled":
				b.afterProbe = cancel
				want = Failure{Category: protocol.CategoryProcess, Code: protocol.CodeProjectCanceled}
			case "interrupted":
				b.afterProbe = func() { close(interrupt) }
				want = Failure{Category: protocol.CategoryProcess, Code: protocol.CodeProjectInterrupted}
			}
			var out, diag bytes.Buffer
			report := Run(Invocation{Context: ctx, Interrupt: interrupt, CWD: fixture.project, Args: []string{"migrations", "check"}, Environment: fixture.environment, Backend: b, Stdout: &out, Stderr: &diag})
			exit, ok := protocol.ExitCode(want)
			if !ok || report.Failure != want || report.ExitCode != exit || report.CompatibilityChecks != wantChecks || report.RunnerCalls != 0 || b.unexpected != 0 || report.TempCleanupAttempts != 1 || report.ResidualTemp != 0 || out.Len() != 0 || diag.String() != want.Category+"/"+want.Code+"\n" {
				t.Fatalf("probe outcome=%+v stderr=%q", report, diag.String())
			}
		})
	}
}

func TestCompatibilityFailuresNeverBecomeLinkedCommandResponses(t *testing.T) {
	for _, code := range []string{"framework_incompatible", "framework_identity_unknown", "compatibility_probe_failed", "compatibility_response_invalid"} {
		f := Failure{Category: failurecode.CategoryCompatibility, Code: code}
		if migrateprotocol.IsLinkedFailure(migrateprotocol.Failure{Category: f.Category, Code: f.Code}) || sqlmigrateprotocol.IsLinkedFailure(sqlmigrateprotocol.Failure{Category: f.Category, Code: f.Code}) {
			t.Fatal("DB command accepted a launcher failure from its child")
		}
		if _, err := showmigrationsprotocol.EncodeResponse(showmigrationsprotocol.Response{Failure: showmigrationsprotocol.Failure{Category: f.Category, Code: f.Code}}); err == nil {
			t.Fatal("status child accepted a launcher failure")
		}

		if _, ok := protocol.ExitCode(f); !ok || protocol.IsLinkedFailure(f) {
			t.Fatal("global compatibility taxonomy escaped its owner", f)
		}
		if _, err := protocol.EncodeResponse(protocol.Response{Failure: f}); err == nil {
			t.Fatal("linked response carried a launcher failure")
		}
		if _, err := createsuperuserprotocol.EncodeResponse(createsuperuserprotocol.Response{Failure: CreatesuperuserFailure{Category: f.Category, Code: f.Code}}); err == nil {
			t.Fatal("sensitive response carried a launcher failure")
		}
	}
}

func TestCompatibilityProcessBoundsMetadataAndReapsDescendants(t *testing.T) {
	t.Run("bounded metadata and diagnostics", func(t *testing.T) {
		command := helperCommand("emit", map[string]string{"GODJ_HELPER_STDOUT_BYTES": strconv.Itoa(compatibility.MaxBytes + 1), "GODJ_HELPER_STDERR_BYTES": strconv.Itoa(maxDiagnosticBytes + 1), "GODJ_HELPER_EXIT": "0"})
		process := NativeBackend{}.Execute(t.Context(), nil, CompatibilityStage, command)
		defer clear(process.Stdout)
		if !process.Started || process.ExitCode != 0 || process.DirectReaps != 1 || process.CleanupFailed || len(process.Stdout) != compatibility.MaxBytes || !process.StdoutScalar.Truncated || process.StderrScalar.RetainedBytes != maxDiagnosticBytes || !process.StderrScalar.Truncated {
			t.Fatalf("unbounded metadata process: %+v", process)
		}
	})
	t.Run("descendant pipe is joined before return", func(t *testing.T) {
		ready := filepath.Join(t.TempDir(), "descendant")
		holderReady := filepath.Join(t.TempDir(), "holder-ready")
		command := helperCommand("spawn-holder", map[string]string{"GODJ_HELPER_READY": ready, "GODJ_HELPER_HOLDER_READY": holderReady})
		done := make(chan ProcessResult, 1)
		go func() { done <- NativeBackend{}.Execute(t.Context(), nil, CompatibilityStage, command) }()
		group, descendant := migrateHelperProcessPair(t, ready)
		waitForFile(t, holderReady)
		t.Cleanup(func() {
			if runserverProcessGroupExists(group) {
				_ = unix.Kill(-group, unix.SIGKILL)
			}
			_ = unix.Kill(descendant, unix.SIGKILL)
		})
		select {
		case process := <-done:
			defer clear(process.Stdout)
			if !process.Started || process.ExitCode != 0 || process.DirectReaps != 1 || process.CleanupFailed || process.SIGKILLAttempts != 1 || runserverProcessGroupExists(group) {
				t.Fatalf("metadata returned before descendant cleanup: %+v", process)
			}
		case <-time.After(7 * time.Second):
			t.Fatal("metadata descendant held the response pipe")
		}
	})
}

func TestRuntimeCompatibilityFailurePrecedesServerStart(t *testing.T) {
	fixture := newRunserverFixture(t, 0)
	linked := fixtureToolInfo()
	linked.Contract.Commands++
	wire, err := json.Marshal(linked)
	if err != nil {
		t.Fatal(err)
	}
	b := &runserverSequenceBackend{
		results: []ProcessResult{{Started: true, ExitCode: 0, DirectReaps: 1}, {Started: true, ExitCode: 0, DirectReaps: 1, Stdout: runserverProjectSpecWire(t)}, {Started: true, ExitCode: 0, DirectReaps: 1}},
		probes:  []ProcessResult{fixtureCompatibilityProcess(), {Started: true, ExitCode: 0, DirectReaps: 1, Stdout: wire}},
	}
	var out, diag bytes.Buffer
	before := generationTreeSnapshot(t, fixture.project)
	report := RunServer(RunServerInvocation{Context: t.Context(), CWD: fixture.project, Args: []string{"runserver", "--no-reload"}, Environment: fixture.environment, Backend: b, Stdout: &out, Stderr: &diag,
		generation: generationHooks{check: func(context.Context, projectgenerate.ProjectRoot, codegen.GeneratedBundle) (projectgenerate.CheckReport, error) {
			return projectgenerate.CheckReport{}, nil
		}},
		runtime: runserverRuntimeHooks{execute: func(context.Context, <-chan struct{}, Command, io.Writer, io.Writer, time.Duration) runserverProcessResult {
			t.Error("mismatched runtime started")
			return runserverProcessResult{}
		}},
	})
	if report.ExitCode != 2 || report.RunServerFailure.Code != "framework_incompatible" || report.CompatibilityChecks != 2 || report.DirectChildReaps != 5 || report.RuntimeStartCalls != 0 || report.RuntimeBuildCalls != 1 || report.PreflightChecks != 1 || out.Len() != 0 || diag.String() != failurecode.CategoryCompatibility+"/framework_incompatible\n" {
		t.Fatalf("runtime compatibility = %+v stderr=%q", report, diag.String())
	}
	if !reflect.DeepEqual(before, generationTreeSnapshot(t, fixture.project)) {
		t.Fatal("runtime mismatch changed the project")
	}
}
