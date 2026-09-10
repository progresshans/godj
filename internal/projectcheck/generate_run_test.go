//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/projectgenerate"
	projectgenerateprotocol "github.com/progresshans/godj/internal/projectgenerate/protocol"
	"github.com/progresshans/godj/schema/ir"
)

func TestParseGenerationArgumentsExactForms(t *testing.T) {
	t.Parallel()
	valid := []struct {
		argv       []string
		check      bool
		descriptor string
	}{
		{argv: []string{"generate"}},
		{argv: []string{"generate", "--check"}, check: true},
		{argv: []string{"generate", "--project", "godj.toml"}, descriptor: "godj.toml"},
		{argv: []string{"generate", "--check", "--project", "nested/godj.toml"}, check: true, descriptor: "nested/godj.toml"},
	}
	for _, test := range valid {
		arguments, failure := parseGenerationArguments(test.argv)
		if failure != nil || arguments.check != test.check || arguments.explicitDescriptor != test.descriptor {
			t.Fatalf("parse %q = %+v failure=%+v", test.argv, arguments, failure)
		}
	}
	invalid := [][]string{
		nil, {"generate", ""}, {"generate", "--project"}, {"generate", "--project", ""},
		{"generate", "--project", "godj.toml", "--check"}, {"generate", "--check", "--check"},
		{"generate", "--project", "godj.toml", "extra"},
	}
	for _, argv := range invalid {
		_, failure := parseGenerationArguments(argv)
		if failure == nil || *failure != (GenerationFailure{Category: GenerationCategoryCommand, Code: GenerationCodeInvalidArguments}) {
			t.Fatalf("invalid parse %q failure=%+v", argv, failure)
		}
	}
}

func TestRunGenerateRejectsArgumentsBeforeCWDSelection(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	report := RunGenerate(GenerationInvocation{
		Context: context.Background(), CWD: filepath.Join(t.TempDir(), "missing"), Args: []string{"generate", "--project"},
		Stdout: &stdout, Stderr: &stderr,
		Backend: backendFunc(func(context.Context, <-chan struct{}, ProcessStage, Command) ProcessResult {
			t.Fatal("backend called after invalid arguments")
			return ProcessResult{}
		}),
	})
	if report.ExitCode != 2 || report.AncestorDirectoriesInspected != 0 || report.DescriptorReads != 0 || report.BuildCalls != 0 || stdout.Len() != 0 || stderr.String() != GenerationCategoryCommand+"/"+GenerationCodeInvalidArguments+"\n" {
		t.Fatalf("report=%+v stdout=%q stderr=%q", report, stdout.String(), stderr.String())
	}
}

func TestRunGenerateUsesExactBuildAndPrivateRequestAndZeroizesResponse(t *testing.T) {
	t.Parallel()
	fixture := newGlobalFixture(t, 2)
	spec := generationTestSpec()
	wire, err := projectgenerateprotocol.EncodeResponse(projectgenerateprotocol.Response{OK: true, ProjectSpec: spec})
	if err != nil {
		t.Fatal(err)
	}
	responseBacking := wire
	backend := &scriptedBackend{
		build:  ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1},
		runner: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, Stdout: responseBacking, StdoutScalar: StreamScalar{RetainedBytes: len(responseBacking)}},
	}
	var stdout, stderr bytes.Buffer
	report := RunGenerate(GenerationInvocation{
		Context: context.Background(), CWD: fixture.cwd, Args: []string{"generate", "--check"}, Environment: fixture.environment,
		Stdout: &stdout, Stderr: &stderr, Backend: backend,
		generation: generationHooks{check: func(context.Context, projectgenerate.ProjectRoot, codegen.GeneratedBundle) (projectgenerate.CheckReport, error) {
			return projectgenerate.CheckReport{}, nil
		}},
	})
	if report.ExitCode != 0 || !report.HasGenerationResult || report.GenerationResult.Status != "clean" || report.BuildCalls != 1 || report.RunnerCalls != 1 || report.DirectChildReaps != 2 || stderr.Len() != 0 {
		t.Fatalf("report=%+v stdout=%q stderr=%q", report, stdout.String(), stderr.String())
	}
	for index, value := range responseBacking {
		if value != 0 {
			t.Fatalf("response byte %d was not zeroized", index)
		}
	}
	if len(backend.commands) != 2 {
		t.Fatalf("commands=%d", len(backend.commands))
	}
	build := backend.commands[0]
	if !reflect.DeepEqual(build.Argv[:5], []string{"go", "build", "-buildvcs=false", "-mod=readonly", "-o"}) || build.Argv[len(build.Argv)-1] != "./cmd/site" || build.Dir != fixture.project {
		t.Fatalf("build=%+v", build)
	}
	runner := backend.commands[1]
	if runner.Dir != fixture.project || len(runner.Argv) != 2 || runner.Argv[1] != projectgenerateprotocol.PrivateArgument || !bytes.Equal(runner.Stdin, projectgenerateprotocol.RequestDocument()) {
		t.Fatalf("runner=%+v", runner)
	}
	wantPrefix := `{"status":"clean","snapshot_sha256":"`
	if !strings.HasPrefix(stdout.String(), wantPrefix) || !strings.HasSuffix(stdout.String(), `","file_count":12}`+"\n") {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestRunGenerateRunnerCleanupFailurePrecedesResponseParsing(t *testing.T) {
	t.Parallel()
	fixture := newGlobalFixture(t, 0)
	wire, err := projectgenerateprotocol.EncodeResponse(projectgenerateprotocol.Response{OK: true, ProjectSpec: generationTestSpec()})
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
	generateCalls := 0
	var stdout, stderr bytes.Buffer
	report := RunGenerate(GenerationInvocation{
		Context: context.Background(), CWD: fixture.project, Args: []string{"generate"}, Environment: fixture.environment,
		Stdout: &stdout, Stderr: &stderr, Backend: backend,
		generation: generationHooks{generate: func(codegen.ProjectSpec) (codegen.GeneratedBundle, error) {
			generateCalls++
			return codegen.GeneratedBundle{}, nil
		}},
	})
	if report.ExitCode != 3 || report.HasGenerationResult || report.GenerationFailure != generationCleanupFailure() || report.CleanupFailed != 1 || generateCalls != 0 || stdout.Len() != 0 || stderr.String() != GenerationCategoryProcess+"/"+GenerationCodeProjectCleanupFailed+"\n" {
		t.Fatalf("runner cleanup precedence=%+v generate_calls=%d stdout=%q stderr=%q", report, generateCalls, stdout.String(), stderr.String())
	}
	if !bytes.Equal(responseBacking, make([]byte, len(responseBacking))) {
		t.Fatalf("runner response was not zeroized: %q", responseBacking)
	}
}

func TestRunGenerateCheckIsReadOnlyAndReportsOrderedDrift(t *testing.T) {
	t.Parallel()
	fixture := newGlobalFixture(t, 1)
	before := generationTreeSnapshot(t, fixture.project)
	report, stdout, stderr := runGenerationWithResponse(t, fixture, []string{"generate", "--check"}, generationTestSpec(), generationHooks{})
	if report.ExitCode != 1 || !report.HasGenerationResult || report.GenerationResult.Status != "drift" || len(report.GenerationResult.Drifts) == 0 || stderr != "" {
		t.Fatalf("report=%+v stdout=%q stderr=%q", report, stdout, stderr)
	}
	after := generationTreeSnapshot(t, fixture.project)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("check mutated project\nbefore=%v\nafter=%v", before, after)
	}
	previous := ""
	for _, drift := range report.GenerationResult.Drifts {
		key := drift.Path + "\x00" + string(drift.Kind)
		if key < previous {
			t.Fatalf("drifts not ordered: %+v", report.GenerationResult.Drifts)
		}
		previous = key
	}
	if !strings.Contains(stdout, `"status":"drift"`) || !strings.Contains(stdout, `"interrupted":false`) || !strings.Contains(stdout, `"drifts":[`) {
		t.Fatalf("drift output=%q", stdout)
	}
}

func TestRunGenerateRejectsProjectPathReboundBeforeCheckOrPublish(t *testing.T) {
	for _, check := range []bool{true, false} {
		check := check
		name := "publish"
		arguments := []string{"generate"}
		if check {
			name = "check"
			arguments = []string{"generate", "--check"}
		}
		t.Run(name, func(t *testing.T) {
			fixture := newGlobalFixture(t, 0)
			selected := fixture.project + ".selected"
			marker := filepath.Join(fixture.project, "replacement.marker")
			sealCalls := 0
			hooks := generationHooks{
				sealRoot: func(path string, device, inode uint64) (projectgenerate.ProjectRoot, error) {
					sealCalls++
					if err := os.Rename(path, selected); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(marker, []byte("replacement"), 0o600); err != nil {
						t.Fatal(err)
					}
					return projectgenerate.SealProjectRoot(path, device, inode)
				},
				check: func(context.Context, projectgenerate.ProjectRoot, codegen.GeneratedBundle) (projectgenerate.CheckReport, error) {
					t.Fatal("check called for rebound project root")
					return projectgenerate.CheckReport{}, nil
				},
				newVerifier: func(projectgenerate.ProjectRoot, codegen.GeneratedBundle) (projectgenerate.CandidateVerifier, error) {
					t.Fatal("verifier constructed for rebound project root")
					return nil, nil
				},
				publish: func(context.Context, projectgenerate.ProjectRoot, codegen.GeneratedBundle, projectgenerate.CandidateVerifier) error {
					t.Fatal("publish called for rebound project root")
					return nil
				},
			}
			report, stdout, stderr := runGenerationWithResponse(t, fixture, arguments, generationTestSpec(), hooks)
			if sealCalls != 1 || report.ExitCode != 2 || report.GenerationFailure != (GenerationFailure{Category: GenerationCategorySelection, Code: GenerationCodeProjectSelectionFailed}) || stdout != "" || stderr != GenerationCategorySelection+"/"+GenerationCodeProjectSelectionFailed+"\n" {
				t.Fatalf("report=%+v seal_calls=%d stdout=%q stderr=%q", report, sealCalls, stdout, stderr)
			}
			contents, err := os.ReadFile(marker)
			if err != nil || string(contents) != "replacement" {
				t.Fatalf("replacement marker=%q error=%v", contents, err)
			}
			if _, err := os.Lstat(filepath.Join(fixture.project, ".godj")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("rebound root mutated: .godj error=%v", err)
			}
		})
	}
}

func TestRunGenerateCandidateFailurePreservesPriorGeneratedTargets(t *testing.T) {
	t.Parallel()
	fixture := newGlobalFixture(t, 0)
	before := generationTreeSnapshot(t, fixture.project)
	failure := errors.New("private candidate detail")
	hooks := generationHooks{newVerifier: func(projectgenerate.ProjectRoot, codegen.GeneratedBundle) (projectgenerate.CandidateVerifier, error) {
		return projectgenerate.CandidateVerifyFunc(func(context.Context, string) error { return failure }), nil
	}}
	report, stdout, stderr := runGenerationWithResponse(t, fixture, []string{"generate"}, generationTestSpec(), hooks)
	if report.ExitCode != 1 || report.GenerationFailure != (GenerationFailure{Category: GenerationCategoryGeneration, Code: GenerationCodeCandidateVerificationFailed}) || stdout != "" || stderr != GenerationCategoryGeneration+"/"+GenerationCodeCandidateVerificationFailed+"\n" || strings.Contains(stderr, failure.Error()) {
		t.Fatalf("report=%+v stdout=%q stderr=%q", report, stdout, stderr)
	}
	after := generationTreeSnapshot(t, fixture.project)
	for path, value := range before {
		if after[path] != value {
			t.Fatalf("prior path %q changed: before=%q after=%q", path, value, after[path])
		}
	}
	for path := range after {
		if strings.HasSuffix(path, ".go") || path == ".godj/generated-manifest.json" {
			t.Fatalf("failed candidate published %q", path)
		}
	}
}

func TestRunGenerateNestedSelectionAndClosedPublishFailure(t *testing.T) {
	t.Parallel()
	fixture := newGlobalFixture(t, 4)
	secret := errors.New("/private/compiler/path")
	hooks := generationHooks{
		newVerifier: func(projectgenerate.ProjectRoot, codegen.GeneratedBundle) (projectgenerate.CandidateVerifier, error) {
			return projectgenerate.CandidateVerifyFunc(func(context.Context, string) error { return nil }), nil
		},
		publish: func(context.Context, projectgenerate.ProjectRoot, codegen.GeneratedBundle, projectgenerate.CandidateVerifier) error {
			return secret
		},
	}
	report, stdout, stderr := runGenerationWithResponse(t, fixture, []string{"generate"}, generationTestSpec(), hooks)
	if report.ExitCode != 1 || report.AncestorDirectoriesInspected != 5 || report.GenerationFailure.Code != GenerationCodeProjectPublishFailed || stdout != "" || strings.Contains(stderr, secret.Error()) || stderr != GenerationCategoryGeneration+"/"+GenerationCodeProjectPublishFailed+"\n" {
		t.Fatalf("report=%+v stdout=%q stderr=%q", report, stdout, stderr)
	}
}

func TestRunGeneratePostcommitCancellationRemainsSuccess(t *testing.T) {
	t.Parallel()
	fixture := newGlobalFixture(t, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	marker := filepath.Join(fixture.project, "committed.marker")
	hooks := generationHooks{
		newVerifier: func(projectgenerate.ProjectRoot, codegen.GeneratedBundle) (projectgenerate.CandidateVerifier, error) {
			return projectgenerate.CandidateVerifyFunc(func(context.Context, string) error { return nil }), nil
		},
		publish: func(context.Context, projectgenerate.ProjectRoot, codegen.GeneratedBundle, projectgenerate.CandidateVerifier) error {
			if err := os.WriteFile(marker, []byte("committed"), 0o600); err != nil {
				return err
			}
			cancel()
			return nil
		},
	}
	wire, err := projectgenerateprotocol.EncodeResponse(projectgenerateprotocol.Response{OK: true, ProjectSpec: generationTestSpec()})
	if err != nil {
		t.Fatal(err)
	}
	backend := &scriptedBackend{
		build:  ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1},
		runner: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, Stdout: wire, StdoutScalar: StreamScalar{RetainedBytes: len(wire)}},
	}
	var stdout, stderr bytes.Buffer
	report := RunGenerate(GenerationInvocation{
		Context: ctx, CWD: fixture.project, Args: []string{"generate"}, Environment: fixture.environment,
		Stdout: &stdout, Stderr: &stderr, Backend: backend, generation: hooks,
	})
	if report.ExitCode != 0 || report.GenerationResult.Status != "generated" || report.HasGenerationFailure || stderr.Len() != 0 {
		t.Fatalf("postcommit cancellation=%+v stdout=%q stderr=%q", report, stdout.String(), stderr.String())
	}
	if contents, err := os.ReadFile(marker); err != nil || string(contents) != "committed" {
		t.Fatalf("committed marker=%q err=%v", contents, err)
	}
}

func TestRunGenerateCommittedTargetWithOuterCleanupFailureReportsCleanupFailed(t *testing.T) {
	t.Parallel()
	fixture := newGlobalFixture(t, 0)
	marker := filepath.Join(fixture.project, "committed.marker")
	hooks := generationHooks{
		newVerifier: func(projectgenerate.ProjectRoot, codegen.GeneratedBundle) (projectgenerate.CandidateVerifier, error) {
			return projectgenerate.CandidateVerifyFunc(func(context.Context, string) error { return nil }), nil
		},
		publish: func(context.Context, projectgenerate.ProjectRoot, codegen.GeneratedBundle, projectgenerate.CandidateVerifier) error {
			return os.WriteFile(marker, []byte("committed"), 0o600)
		},
	}
	wire, err := projectgenerateprotocol.EncodeResponse(projectgenerateprotocol.Response{OK: true, ProjectSpec: generationTestSpec()})
	if err != nil {
		t.Fatal(err)
	}
	backend := &scriptedBackend{
		build:  ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1},
		runner: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, Stdout: wire, StdoutScalar: StreamScalar{RetainedBytes: len(wire)}},
	}
	var stdout, stderr bytes.Buffer
	report := RunGenerate(GenerationInvocation{
		Context: context.Background(), CWD: fixture.project, Args: []string{"generate"}, Environment: fixture.environment,
		Stdout: &stdout, Stderr: &stderr, Backend: backend, generation: hooks,
		workspace: workspaceHooks{afterRootCreated: func(_ string, base *os.File) { _ = base.Close() }},
	})
	if report.ExitCode != 3 || report.HasGenerationResult || report.GenerationFailure != generationCleanupFailure() || report.CleanupFailed != 1 || stdout.Len() != 0 || stderr.String() != GenerationCategoryProcess+"/"+GenerationCodeProjectCleanupFailed+"\n" {
		t.Fatalf("committed cleanup failure=%+v stdout=%q stderr=%q", report, stdout.String(), stderr.String())
	}
	if contents, err := os.ReadFile(marker); err != nil || string(contents) != "committed" {
		t.Fatalf("committed target was lost: contents=%q err=%v", contents, err)
	}
}

func TestRunGenerateCancellationReapsGenerationRunnerAndCleansWorkspace(t *testing.T) {
	fixture := newGlobalFixture(t, 0)
	ready := filepath.Join(t.TempDir(), "generation-runner-ready")
	backend := &actualStageBackend{stage: GenerationRunnerStage, mode: "ignore", extra: map[string]string{"GODJ_HELPER_READY": ready}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		report GenerationReport
		stdout string
		stderr string
	}
	done := make(chan outcome, 1)
	go func() {
		var stdout, stderr bytes.Buffer
		report := RunGenerate(GenerationInvocation{
			Context: ctx, CWD: fixture.project, Args: []string{"generate"}, Environment: fixture.environment,
			Stdout: &stdout, Stderr: &stderr, Backend: backend,
		})
		done <- outcome{report: report, stdout: stdout.String(), stderr: stderr.String()}
	}()
	waitForFile(t, ready)
	cancel()
	select {
	case got := <-done:
		if got.report.ExitCode != 3 || got.report.GenerationFailure != (GenerationFailure{Category: GenerationCategoryProcess, Code: GenerationCodeProjectCanceled}) || got.report.DirectChildReaps != 2 || got.report.GroupSIGINTAttempts != 1 || got.report.GroupSIGKILLAttempts != 1 || got.report.TempCleanupAttempts != 1 || got.report.ResidualTemp != 0 || got.stdout != "" || got.stderr != GenerationCategoryProcess+"/"+GenerationCodeProjectCanceled+"\n" {
			t.Fatalf("canceled generation=%+v stdout=%q stderr=%q", got.report, got.stdout, got.stderr)
		}
	case <-time.After(ownedProcessGrace + 10*time.Second):
		t.Fatal("generation runner cancellation did not finish")
	}
}

func TestGenerationFailureTaxonomyAndExitCodesAreClosed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		failure GenerationFailure
		exit    int
	}{
		{GenerationFailure{GenerationCategoryCommand, GenerationCodeInvalidArguments}, 2},
		{GenerationFailure{GenerationCategorySelection, GenerationCodeProjectNotFound}, 2},
		{GenerationFailure{GenerationCategoryBuild, GenerationCodeProjectBuildFailed}, 3},
		{GenerationFailure{GenerationCategoryProtocol, projectgenerateprotocol.CodeInvalidResponse}, 3},
		{GenerationFailure{GenerationCategoryDeclaration, projectgenerateprotocol.CodeProjectSpecLoadFailed}, 1},
		{GenerationFailure{GenerationCategoryGeneration, GenerationCodeProjectGenerateFailed}, 1},
		{GenerationFailure{GenerationCategoryGeneration, GenerationCodePublicationRecoveryRequired}, 3},
		{GenerationFailure{GenerationCategoryProcess, GenerationCodeProjectInterrupted}, 130},
		{GenerationFailure{GenerationCategoryInternal, GenerationCodeProjectInternalError}, 3},
	}
	for _, test := range tests {
		if exit, ok := generationExitCode(test.failure); !ok || exit != test.exit {
			t.Fatalf("exit(%+v)=(%d,%v)", test.failure, exit, ok)
		}
	}
	if _, ok := generationExitCode(GenerationFailure{GenerationCategoryGeneration, "raw_detail"}); ok {
		t.Fatal("unknown generation failure accepted")
	}
}

func runGenerationWithResponse(t *testing.T, fixture globalFixture, args []string, spec codegen.ProjectSpec, hooks generationHooks) (GenerationReport, string, string) {
	t.Helper()
	wire, err := projectgenerateprotocol.EncodeResponse(projectgenerateprotocol.Response{OK: true, ProjectSpec: spec})
	if err != nil {
		t.Fatal(err)
	}
	backend := &scriptedBackend{
		build:  ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1},
		runner: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, Stdout: wire, StdoutScalar: StreamScalar{RetainedBytes: len(wire)}},
	}
	var stdout, stderr bytes.Buffer
	report := RunGenerate(GenerationInvocation{
		Context: context.Background(), CWD: fixture.cwd, Args: args, Environment: fixture.environment,
		Stdout: &stdout, Stderr: &stderr, Backend: backend, generation: hooks,
	})
	return report, stdout.String(), stderr.String()
}

func generationTestSpec() codegen.ProjectSpec {
	return codegen.ProjectSpec{
		Project: codegen.PackageSpec{PackageName: "project", ImportPath: "example.com/site/project", Directory: "project"},
		Apps: []codegen.AppSpec{{
			Alias:   "articles",
			Package: codegen.PackageSpec{PackageName: "articles", ImportPath: "example.com/site/articles", Directory: "articles"},
			Schema: ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "articles", Models: []ir.Model{{
				Name: "article", GoName: "Article", Fields: []ir.Field{{Name: "title", GoName: "Title", Kind: ir.FieldChar, MaxLength: 200}},
			}}},
		}},
	}
}

func generationTreeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			result[filepath.ToSlash(relative)+"/"] = info.Mode().String()
			return nil
		}
		contents, err := os.ReadFile(current)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(relative)] = info.Mode().String() + ":" + string(contents)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
