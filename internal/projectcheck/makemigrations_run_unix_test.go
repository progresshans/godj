//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/projectmigration"
	writerprotocol "github.com/progresshans/godj/internal/projectmigration/protocol"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

type makemigrationsRunBackend struct {
	t          *testing.T
	inventory  []byte
	runnerWire []byte
	onStage    func(ProcessStage)
	stages     []ProcessStage
	commands   []Command
}

func (backend *makemigrationsRunBackend) Execute(
	_ context.Context,
	_ <-chan struct{},
	stage ProcessStage,
	command Command,
) ProcessResult {
	backend.t.Helper()
	backend.stages = append(backend.stages, stage)
	backend.commands = append(backend.commands, cloneCommand(command))
	if backend.onStage != nil {
		backend.onStage(stage)
	}
	switch stage {
	case MakemigrationsInventoryStage:
		document := append([]byte(nil), backend.inventory...)
		return ProcessResult{
			Started: true, ExitCode: 0, DirectReaps: 1, Stdout: document,
			StdoutScalar: StreamScalar{RetainedBytes: len(document)},
		}
	case BuildStage:
		return ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1}
	case MakemigrationsRunnerStage:
		return ProcessResult{
			Started: true, ExitCode: 0, DirectReaps: 1, Stdout: backend.runnerWire,
			StdoutScalar: StreamScalar{RetainedBytes: len(backend.runnerWire)},
		}
	default:
		backend.t.Fatalf("unexpected makemigrations stage %d", stage)
		return ProcessResult{}
	}
}

type makemigrationsRunFixture struct {
	root          string
	environment   []string
	inventory     []byte
	snapshot      projectmigration.Snapshot
	migrationPath string
}

func TestRunMakemigrationsModesAndExactProcessContract(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		arguments   []string
		clean       bool
		wantExit    int
		wantResult  bool
		wantStatus  string
		wantFailure MakemigrationsFailure
	}{
		{
			name: "dry-run pending is read-only and successful", arguments: []string{"makemigrations", "--dry-run"},
			wantExit: 0, wantResult: true, wantStatus: "pending",
		},
		{
			name: "check pending exits one", arguments: []string{"makemigrations", "--check"},
			wantExit: 1, wantResult: true, wantStatus: "pending",
		},
		{
			name: "normal pending is not silently published", arguments: []string{"makemigrations"},
			wantExit: 1,
			wantFailure: MakemigrationsFailure{
				Category: MakemigrationsCategoryPublication,
				Code:     MakemigrationsCodePublicationUnavailable,
			},
		},
		{
			name: "normal clean succeeds", arguments: []string{"makemigrations"}, clean: true,
			wantExit: 0, wantResult: true, wantStatus: "clean",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newMakemigrationsRunFixture(t, test.clean)
			wire := encodeMakemigrationsRunResponse(t, fixture.snapshot)
			backend := &makemigrationsRunBackend{t: t, inventory: fixture.inventory, runnerWire: wire}
			before := generationTreeSnapshot(t, fixture.root)
			var stdout, stderr bytes.Buffer
			report := RunMakemigrations(MakemigrationsInvocation{
				Context: context.Background(), CWD: fixture.root, Args: test.arguments,
				Environment: fixture.environment, Stdout: &stdout, Stderr: &stderr, Backend: backend,
			})

			if report.ExitCode != test.wantExit || report.HasMakemigrationsResult != test.wantResult ||
				report.InventoryCalls != 3 || report.BuildCalls != 1 || report.RunnerCalls != 1 ||
				report.IndependentCatalogSnapshots != 3 || report.DirectChildReaps != 5 ||
				report.RunnerResponseWrites != 1 || report.TempCreated != 1 ||
				report.TempCleanupAttempts != 1 || report.CleanupFailed != 0 || report.ResidualTemp != 0 {
				t.Fatalf("report = %+v", report)
			}
			if test.wantResult {
				if report.HasMakemigrationsFailure || report.MakemigrationsResult.Status != test.wantStatus {
					t.Fatalf("result = %+v failure=%+v", report.MakemigrationsResult, report.MakemigrationsFailure)
				}
				if (test.wantStatus == "pending" && report.MakemigrationsResult.CandidateCount == 0) ||
					(test.wantStatus == "clean" && report.MakemigrationsResult.CandidateCount != 0) {
					t.Fatalf("candidate result = %+v", report.MakemigrationsResult)
				}
				if !strings.Contains(stdout.String(), `"status":"`+test.wantStatus+`"`) || stderr.Len() != 0 {
					t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
				}
			} else if !report.HasMakemigrationsFailure || report.MakemigrationsFailure != test.wantFailure ||
				stdout.Len() != 0 || stderr.String() != test.wantFailure.Category+"/"+test.wantFailure.Code+"\n" {
				t.Fatalf("failure report=%+v stdout=%q stderr=%q", report, stdout.String(), stderr.String())
			}
			assertZeroBytes(t, "raw runner response", wire)

			if test.name == "dry-run pending is read-only and successful" {
				after := generationTreeSnapshot(t, fixture.root)
				if !reflect.DeepEqual(before, after) {
					t.Fatalf("dry-run mutated project\nbefore=%v\nafter=%v", before, after)
				}
				assertMakemigrationsProcessContract(t, fixture, backend)
			}
		})
	}
}

func TestRunMakemigrationsRejectsArgumentsBeforeCWD(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	report := RunMakemigrations(MakemigrationsInvocation{
		Context: context.Background(), CWD: filepath.Join(t.TempDir(), "missing"),
		Args: []string{"makemigrations", "--project"}, Stdout: &stdout, Stderr: &stderr,
		Backend: backendFunc(func(context.Context, <-chan struct{}, ProcessStage, Command) ProcessResult {
			t.Fatal("backend called after invalid makemigrations arguments")
			return ProcessResult{}
		}),
	})
	want := MakemigrationsFailure{Category: MakemigrationsCategoryCommand, Code: MakemigrationsCodeInvalidArguments}
	if report.ExitCode != 2 || !report.HasMakemigrationsFailure || report.MakemigrationsFailure != want ||
		report.AncestorDirectoriesInspected != 0 || report.DescriptorReads != 0 ||
		report.InventoryCalls != 0 || report.BuildCalls != 0 || report.RunnerCalls != 0 ||
		stdout.Len() != 0 || stderr.String() != want.Category+"/"+want.Code+"\n" {
		t.Fatalf("report=%+v stdout=%q stderr=%q", report, stdout.String(), stderr.String())
	}
}

func TestRunMakemigrationsFailsClosedOnSnapshotAndInPlaceDrift(t *testing.T) {
	t.Parallel()
	t.Run("child and global snapshot mismatch", func(t *testing.T) {
		t.Parallel()
		fixture := newMakemigrationsRunFixture(t, false)
		result := makemigrationsRunProtocolResult(fixture.snapshot)
		result.ProjectSnapshotSHA256 = strings.Repeat("0", 64)
		wire, err := writerprotocol.EncodeResponse(writerprotocol.Response{OK: true, Result: result})
		if err != nil {
			t.Fatal(err)
		}
		backend := &makemigrationsRunBackend{t: t, inventory: fixture.inventory, runnerWire: wire}
		report, stdout, stderr := executeMakemigrationsRun(t, fixture, []string{"makemigrations", "--dry-run"}, backend, workspaceHooks{})
		assertMakemigrationsSourceConflict(t, report, stdout, stderr, 2, 1, 1)
		assertZeroBytes(t, "mismatched response", wire)
	})

	t.Run("build input disappears after baseline", func(t *testing.T) {
		t.Parallel()
		fixture := newMakemigrationsRunFixture(t, false)
		wire := encodeMakemigrationsRunResponse(t, fixture.snapshot)
		member := filepath.Join(fixture.root, "cmd", "site", "main.go")
		backend := &makemigrationsRunBackend{
			t: t, inventory: fixture.inventory, runnerWire: wire,
			onStage: func(stage ProcessStage) {
				if stage == BuildStage {
					if err := os.Remove(member); err != nil {
						t.Fatal(err)
					}
				}
			},
		}
		report, stdout, stderr := executeMakemigrationsRun(t, fixture, []string{"makemigrations", "--dry-run"}, backend, workspaceHooks{})
		assertMakemigrationsSourceConflict(t, report, stdout, stderr, 2, 0, 0)
	})

	t.Run("retained project root rebounds after baseline", func(t *testing.T) {
		t.Parallel()
		fixture := newMakemigrationsRunFixture(t, false)
		wire := encodeMakemigrationsRunResponse(t, fixture.snapshot)
		moved := fixture.root + "-retained"
		backend := &makemigrationsRunBackend{
			t: t, inventory: fixture.inventory, runnerWire: wire,
			onStage: func(stage ProcessStage) {
				if stage != BuildStage {
					return
				}
				if err := os.Rename(fixture.root, moved); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(fixture.root, 0o700); err != nil {
					t.Fatal(err)
				}
			},
		}
		report, stdout, stderr := executeMakemigrationsRun(t, fixture, []string{"makemigrations", "--dry-run"}, backend, workspaceHooks{})
		assertMakemigrationsSourceConflict(t, report, stdout, stderr, 1, 0, 0)
	})

	t.Run("build input changes in place after build", func(t *testing.T) {
		t.Parallel()
		fixture := newMakemigrationsRunFixture(t, false)
		wire := encodeMakemigrationsRunResponse(t, fixture.snapshot)
		member := filepath.Join(fixture.root, "cmd", "site", "main.go")
		before, err := os.Stat(member)
		if err != nil {
			t.Fatal(err)
		}
		backend := &makemigrationsRunBackend{
			t: t, inventory: fixture.inventory, runnerWire: wire,
			onStage: func(stage ProcessStage) {
				if stage == BuildStage {
					overwriteMakemigrationsMember(t, member, []byte("package main\nvar value=2\n"))
				}
			},
		}
		report, stdout, stderr := executeMakemigrationsRun(t, fixture, []string{"makemigrations", "--dry-run"}, backend, workspaceHooks{})
		assertMakemigrationsSourceConflict(t, report, stdout, stderr, 2, 0, 0)
		after, err := os.Stat(member)
		if err != nil || !os.SameFile(before, after) {
			t.Fatalf("build member was replaced instead of changed in place: before=%v after=%v err=%v", before, after, err)
		}
	})

	t.Run("filesystem catalog changes in place after child response", func(t *testing.T) {
		t.Parallel()
		fixture := newMakemigrationsRunFixture(t, true)
		wire := encodeMakemigrationsRunResponse(t, fixture.snapshot)
		before, err := os.Stat(fixture.migrationPath)
		if err != nil {
			t.Fatal(err)
		}
		inventoryCalls := 0
		backend := &makemigrationsRunBackend{
			t: t, inventory: fixture.inventory, runnerWire: wire,
			onStage: func(stage ProcessStage) {
				if stage == MakemigrationsInventoryStage {
					inventoryCalls++
				}
				if stage == MakemigrationsInventoryStage && inventoryCalls == 3 {
					overwriteMakemigrationsMember(t, fixture.migrationPath, []byte("{\"changed\":true}\n"))
				}
			},
		}
		report, stdout, stderr := executeMakemigrationsRun(t, fixture, []string{"makemigrations", "--dry-run"}, backend, workspaceHooks{})
		assertMakemigrationsSourceConflict(t, report, stdout, stderr, 3, 1, 3)
		after, err := os.Stat(fixture.migrationPath)
		if err != nil || !os.SameFile(before, after) {
			t.Fatalf("catalog member was replaced instead of changed in place: before=%v after=%v err=%v", before, after, err)
		}
		assertZeroBytes(t, "catalog-drift response", wire)
	})
}

func TestRunMakemigrationsObservesInterruptAfterFinalCatalogSnapshot(t *testing.T) {
	t.Parallel()
	fixture := newMakemigrationsRunFixture(t, false)
	wire := encodeMakemigrationsRunResponse(t, fixture.snapshot)
	backend := &makemigrationsRunBackend{t: t, inventory: fixture.inventory, runnerWire: wire}
	interrupt := make(chan struct{})
	var stdout, stderr bytes.Buffer
	report := RunMakemigrations(MakemigrationsInvocation{
		Context: context.Background(), CWD: fixture.root, Args: []string{"makemigrations", "--dry-run"},
		Environment: fixture.environment, Stdout: &stdout, Stderr: &stderr, Interrupt: interrupt, Backend: backend,
		afterFinalCatalogSnapshot: func() { close(interrupt) },
	})
	want := MakemigrationsFailure{Category: MakemigrationsCategoryProcess, Code: MakemigrationsCodeProjectInterrupted}
	if report.ExitCode != 130 || !report.HasMakemigrationsFailure || report.MakemigrationsFailure != want ||
		report.HasMakemigrationsResult || report.InventoryCalls != 3 || report.BuildCalls != 1 || report.RunnerCalls != 1 ||
		report.IndependentCatalogSnapshots != 3 || stdout.Len() != 0 || stderr.String() != want.Category+"/"+want.Code+"\n" {
		t.Fatalf("report=%+v stdout=%q stderr=%q", report, stdout.String(), stderr.String())
	}
	assertZeroBytes(t, "interrupted response", wire)
}

func TestMakemigrationsFinalBarrierPreservesSelectedNonCancelPrimary(t *testing.T) {
	t.Parallel()
	interrupt := make(chan struct{})
	close(interrupt)
	input := MakemigrationsInvocation{Context: context.Background(), Interrupt: interrupt}

	primary := makemigrationsSourceConflict()
	failureReport := MakemigrationsReport{
		HasMakemigrationsFailure: true,
		MakemigrationsFailure:    primary,
	}
	applyMakemigrationsFinalBarrier(input, &failureReport)
	if !failureReport.HasMakemigrationsFailure || failureReport.MakemigrationsFailure != primary || failureReport.HasMakemigrationsResult {
		t.Fatalf("non-cancel primary changed: %+v", failureReport)
	}

	resultReport := MakemigrationsReport{
		HasMakemigrationsResult: true,
		MakemigrationsResult:    MakemigrationsResult{Status: "clean", Candidates: []MakemigrationsCandidate{}},
	}
	applyMakemigrationsFinalBarrier(input, &resultReport)
	want := MakemigrationsFailure{Category: MakemigrationsCategoryProcess, Code: MakemigrationsCodeProjectInterrupted}
	if !resultReport.HasMakemigrationsFailure || resultReport.MakemigrationsFailure != want || resultReport.HasMakemigrationsResult ||
		!reflect.DeepEqual(resultReport.MakemigrationsResult, MakemigrationsResult{}) {
		t.Fatalf("successful result did not observe terminal barrier: %+v", resultReport)
	}
}

func TestRunMakemigrationsZeroizesResponsesAndHonorsCleanupPrecedence(t *testing.T) {
	t.Parallel()
	t.Run("parsed result documents", func(t *testing.T) {
		t.Parallel()
		programmatic := []byte("programmatic-secret")
		candidate := []byte("candidate-secret")
		result := writerprotocol.Result{
			ProgrammaticCatalog: writerprotocol.ProgrammaticCatalog{Sources: []writerprotocol.Source{{Document: programmatic}}},
			Candidates:          []writerprotocol.Candidate{{Document: candidate}},
		}
		clearMakemigrationsProtocolResult(&result)
		assertZeroBytes(t, "programmatic document", programmatic)
		assertZeroBytes(t, "candidate document", candidate)
		if !reflect.DeepEqual(result, writerprotocol.Result{}) {
			t.Fatalf("cleared result = %+v", result)
		}
	})

	t.Run("runner cleanup precedes valid response", func(t *testing.T) {
		t.Parallel()
		fixture := newMakemigrationsRunFixture(t, false)
		wire := encodeMakemigrationsRunResponse(t, fixture.snapshot)
		backend := backendFunc(func(_ context.Context, _ <-chan struct{}, stage ProcessStage, _ Command) ProcessResult {
			switch stage {
			case MakemigrationsInventoryStage:
				document := append([]byte(nil), fixture.inventory...)
				return ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, Stdout: document, StdoutScalar: StreamScalar{RetainedBytes: len(document)}}
			case BuildStage:
				return ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1}
			case MakemigrationsRunnerStage:
				return ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, CleanupFailed: true, Stdout: wire, StdoutScalar: StreamScalar{RetainedBytes: len(wire)}}
			default:
				t.Fatalf("unexpected stage %d", stage)
				return ProcessResult{}
			}
		})
		report, stdout, stderr := executeMakemigrationsRun(t, fixture, []string{"makemigrations", "--dry-run"}, backend, workspaceHooks{})
		want := makemigrationsCleanupFailure()
		if report.ExitCode != 3 || !report.HasMakemigrationsFailure || report.MakemigrationsFailure != want ||
			report.HasMakemigrationsResult || report.CleanupFailed != 1 || report.InventoryCalls != 2 ||
			report.BuildCalls != 1 || report.RunnerCalls != 1 || report.IndependentCatalogSnapshots != 0 ||
			stdout != "" || stderr != want.Category+"/"+want.Code+"\n" {
			t.Fatalf("report=%+v stdout=%q stderr=%q", report, stdout, stderr)
		}
		assertZeroBytes(t, "cleanup-preempted response", wire)
	})

	t.Run("outer cleanup replaces a clean result", func(t *testing.T) {
		t.Parallel()
		fixture := newMakemigrationsRunFixture(t, true)
		wire := encodeMakemigrationsRunResponse(t, fixture.snapshot)
		backend := &makemigrationsRunBackend{t: t, inventory: fixture.inventory, runnerWire: wire}
		report, stdout, stderr := executeMakemigrationsRun(
			t, fixture, []string{"makemigrations"}, backend,
			workspaceHooks{afterRootCreated: func(_ string, base *os.File) { _ = base.Close() }},
		)
		want := makemigrationsCleanupFailure()
		if report.ExitCode != 3 || !report.HasMakemigrationsFailure || report.MakemigrationsFailure != want ||
			report.HasMakemigrationsResult || !reflect.DeepEqual(report.MakemigrationsResult, MakemigrationsResult{}) ||
			report.CleanupFailed != 1 || report.InventoryCalls != 3 || report.IndependentCatalogSnapshots != 3 ||
			stdout != "" || stderr != want.Category+"/"+want.Code+"\n" {
			t.Fatalf("report=%+v stdout=%q stderr=%q", report, stdout, stderr)
		}
		assertZeroBytes(t, "successful response before outer cleanup", wire)
	})
}

func newMakemigrationsRunFixture(t *testing.T, clean bool) makemigrationsRunFixture {
	t.Helper()
	fingerprint := newMakemigrationsFingerprintFixture(t)
	root := fingerprint.root
	writerRoot := filepath.Join(root, "migrations")
	if err := os.Mkdir(writerRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	spec := makemigrationsRunSpec()
	emptyFilesystem := make([]definition.Source, 0)
	emptyProgrammatic := make([]definition.Source, 0)
	snapshot, err := projectmigration.BuildSnapshot(projectmigration.Request{
		ProjectSpec: spec, FilesystemSources: emptyFilesystem,
		ProgrammaticSources: emptyProgrammatic, WriterRoot: "migrations",
	})
	if err != nil {
		t.Fatal(err)
	}
	migrationPath := ""
	if clean {
		candidates := snapshot.Candidates()
		if len(candidates) != 1 {
			t.Fatalf("initial candidates = %d", len(candidates))
		}
		document := candidates[0].Document()
		basename := candidates[0].App() + "_" + candidates[0].Name() + ".godj.json"
		migrationPath = filepath.Join(writerRoot, basename)
		if err := os.WriteFile(migrationPath, document, 0o600); err != nil {
			t.Fatal(err)
		}
		snapshot, err = projectmigration.BuildSnapshot(projectmigration.Request{
			ProjectSpec: spec,
			FilesystemSources: []definition.Source{{
				SourceID: filepath.ToSlash(filepath.Join("migrations", basename)),
				Document: append([]byte(nil), document...),
			}},
			ProgrammaticSources: emptyProgrammatic,
			WriterRoot:          "migrations",
		})
		clear(document)
		if err != nil {
			t.Fatal(err)
		}
		if candidates := snapshot.Candidates(); len(candidates) != 0 {
			t.Fatalf("clean fixture candidates = %d", len(candidates))
		}
	}
	return makemigrationsRunFixture{
		root:          root,
		environment:   newMakemigrationsRunEnvironment(t),
		inventory:     encodeMakemigrationsGoList(t, fingerprint.packages),
		snapshot:      snapshot,
		migrationPath: migrationPath,
	}
}

func newMakemigrationsRunEnvironment(t *testing.T) []string {
	t.Helper()
	root := t.TempDir()
	values := []string{"PATH=" + os.Getenv("PATH"), "NETRC=/explicit/netrc", "GOTELEMETRY=ambient"}
	for _, pair := range []struct{ key, name string }{
		{key: "HOME", name: "home"},
		{key: "XDG_CONFIG_HOME", name: "config"},
		{key: "XDG_CACHE_HOME", name: "cache"},
		{key: "TMPDIR", name: "tmp"},
	} {
		path := filepath.Join(root, pair.name)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		values = append(values, pair.key+"="+path)
	}
	return values
}

func makemigrationsRunSpec() codegen.ProjectSpec {
	return codegen.ProjectSpec{
		Project: codegen.PackageSpec{PackageName: "project", ImportPath: "example.invalid/site/project", Directory: "project"},
		Apps: []codegen.AppSpec{{
			Alias:   "content",
			Package: codegen.PackageSpec{PackageName: "content", ImportPath: "example.invalid/site/content", Directory: "content"},
			Schema: ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "content", Models: []ir.Model{{
				Name: "article", GoName: "Article", Fields: []ir.Field{{
					Name: "title", GoName: "Title", Kind: ir.FieldChar, MaxLength: 200,
				}},
			}}},
		}},
	}
}

func encodeMakemigrationsRunResponse(t *testing.T, snapshot projectmigration.Snapshot) []byte {
	t.Helper()
	document, err := writerprotocol.EncodeResponse(writerprotocol.Response{
		OK: true, Result: makemigrationsRunProtocolResult(snapshot),
	})
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func makemigrationsRunProtocolResult(snapshot projectmigration.Snapshot) writerprotocol.Result {
	filesystem := snapshot.FilesystemSources()
	programmaticSources := snapshot.ProgrammaticSources()
	programmatic := make([]writerprotocol.Source, len(programmaticSources))
	for index := range programmaticSources {
		programmatic[index] = writerprotocol.Source{
			SourceID: programmaticSources[index].SourceID,
			Document: append([]byte(nil), programmaticSources[index].Document...),
		}
	}
	candidates := snapshot.Candidates()
	wireCandidates := make([]writerprotocol.Candidate, len(candidates))
	for index := range candidates {
		wireCandidates[index] = writerprotocol.Candidate{
			App: candidates[index].App(), Name: candidates[index].Name(), Document: candidates[index].Document(),
		}
	}
	return writerprotocol.Result{
		WriterRoot:            snapshot.WriterRoot(),
		ProjectSpec:           snapshot.ProjectSpec(),
		ProjectSpecDigest:     snapshot.ProjectSpecDigest(),
		ProjectSnapshotSHA256: snapshot.GeneratedBundleSnapshotSHA256(),
		FilesystemCatalog: writerprotocol.CatalogSummary{
			SourceCount: len(filesystem), DocumentBytes: definitionDocumentBytes(filesystem),
			Digest: snapshot.FilesystemCatalogDigest(),
		},
		ProgrammaticCatalog: writerprotocol.ProgrammaticCatalog{
			SourceCount: len(programmaticSources), DocumentBytes: definitionDocumentBytes(programmaticSources),
			Digest: snapshot.ProgrammaticCatalogDigest(), Sources: programmatic,
		},
		DefinitionSetDigest: snapshot.ExistingSemanticDigest(),
		Candidates:          wireCandidates,
	}
}

func executeMakemigrationsRun(
	t *testing.T,
	fixture makemigrationsRunFixture,
	arguments []string,
	backend Backend,
	workspace workspaceHooks,
) (MakemigrationsReport, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	report := RunMakemigrations(MakemigrationsInvocation{
		Context: context.Background(), CWD: fixture.root, Args: arguments,
		Environment: fixture.environment, Stdout: &stdout, Stderr: &stderr,
		Backend: backend, workspace: workspace,
	})
	return report, stdout.String(), stderr.String()
}

func assertMakemigrationsProcessContract(t *testing.T, fixture makemigrationsRunFixture, backend *makemigrationsRunBackend) {
	t.Helper()
	wantStages := []ProcessStage{
		MakemigrationsInventoryStage, BuildStage, MakemigrationsInventoryStage,
		MakemigrationsRunnerStage, MakemigrationsInventoryStage,
	}
	if !reflect.DeepEqual(backend.stages, wantStages) || len(backend.commands) != len(wantStages) {
		t.Fatalf("stages=%v commands=%d", backend.stages, len(backend.commands))
	}
	wantInventory := []string{"go", "list", "-deps", "-json", "-mod=readonly", "./cmd/site"}
	for _, index := range []int{0, 2, 4} {
		command := backend.commands[index]
		if command.Dir != fixture.root || !reflect.DeepEqual(command.Argv, wantInventory) || len(command.Stdin) != 0 {
			t.Fatalf("inventory[%d] = %+v", index, command)
		}
	}
	build := backend.commands[1]
	wantBuild := []string{
		"go", "build", "-buildvcs=false", "-mod=readonly", "-o",
		build.Argv[5], "./cmd/site",
	}
	if build.Dir != fixture.root || len(build.Argv) != 7 || !reflect.DeepEqual(build.Argv, wantBuild) ||
		filepath.Base(build.Argv[5]) != "godj-project-runner" || len(build.Stdin) != 0 {
		t.Fatalf("build = %+v", build)
	}
	runner := backend.commands[3]
	if runner.Dir != fixture.root || !reflect.DeepEqual(runner.Argv, []string{build.Argv[5], writerprotocol.PrivateArgument}) ||
		!bytes.Equal(runner.Stdin, writerprotocol.RequestDocument()) {
		t.Fatalf("runner = %+v", runner)
	}
}

func assertMakemigrationsSourceConflict(
	t *testing.T,
	report MakemigrationsReport,
	stdout, stderr string,
	wantInventory, wantRunner, wantSnapshots int,
) {
	t.Helper()
	want := makemigrationsSourceConflict()
	if report.ExitCode != 1 || !report.HasMakemigrationsFailure || report.MakemigrationsFailure != want ||
		report.HasMakemigrationsResult || report.InventoryCalls != wantInventory || report.BuildCalls != 1 ||
		report.RunnerCalls != wantRunner || report.IndependentCatalogSnapshots != wantSnapshots ||
		stdout != "" || stderr != want.Category+"/"+want.Code+"\n" {
		t.Fatalf("report=%+v stdout=%q stderr=%q", report, stdout, stderr)
	}
}

func overwriteMakemigrationsMember(t *testing.T, path string, document []byte) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(document); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func assertZeroBytes(t *testing.T, owner string, document []byte) {
	t.Helper()
	if !bytes.Equal(document, make([]byte, len(document))) {
		t.Fatalf("%s was not zeroized", owner)
	}
}
