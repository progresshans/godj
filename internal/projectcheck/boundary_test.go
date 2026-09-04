//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/projectcheck/protocol"
	projectgenerateprotocol "github.com/progresshans/godj/internal/projectgenerate/protocol"
	"github.com/progresshans/godj/schema/ir"
)

func TestDescriptorByteCapActualGlobalTriplet(t *testing.T) {
	t.Parallel()
	for _, size := range []int{maxDescriptorBytes - 1, maxDescriptorBytes, maxDescriptorBytes + 1} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			fixture := newGlobalFixture(t, 0)
			if err := os.WriteFile(fixture.project+string(os.PathSeparator)+descriptorName, descriptorOfSize(t, size), 0o600); err != nil {
				t.Fatal(err)
			}
			wire, err := protocol.EncodeResponse(protocol.Response{OK: true, Result: protocol.Result{DefinitionSetDigest: protocol.EmptySetDigest}})
			if err != nil {
				t.Fatal(err)
			}
			backend := &scriptedBackend{
				build:  ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1},
				runner: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, Stdout: wire, StdoutScalar: StreamScalar{RetainedBytes: len(wire)}},
			}
			var stdout, stderr bytes.Buffer
			report := Run(Invocation{
				Context: context.Background(), CWD: fixture.project, Args: []string{"migrations", "check"}, Environment: fixture.environment,
				Stdout: &stdout, Stderr: &stderr, Backend: backend,
			})
			if size <= maxDescriptorBytes {
				if report.ExitCode != 0 || report.DescriptorReads != 1 || report.BuildCalls != 1 || report.RunnerCalls != 1 {
					t.Fatalf("descriptor %d = %+v", size, report)
				}
				return
			}
			if report.ExitCode != 2 || report.Failure.Code != protocol.CodeInvalidProjectDescriptor || report.DescriptorReads != 1 || report.BuildCalls != 0 || report.RunnerCalls != 0 || stdout.Len() != 0 {
				t.Fatalf("descriptor %d = %+v stdout=%q", size, report, stdout.String())
			}
		})
	}
}

func TestAncestorCapActualGlobalTriplet(t *testing.T) {
	t.Parallel()
	for _, inspections := range []int{maxAncestors - 1, maxAncestors, maxAncestors + 1} {
		t.Run(strconv.Itoa(inspections), func(t *testing.T) {
			fixture := newGlobalFixture(t, inspections-1)
			wire, err := protocol.EncodeResponse(protocol.Response{OK: true, Result: protocol.Result{DefinitionSetDigest: protocol.EmptySetDigest}})
			if err != nil {
				t.Fatal(err)
			}
			backend := &scriptedBackend{
				build:  ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1},
				runner: ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, Stdout: wire, StdoutScalar: StreamScalar{RetainedBytes: len(wire)}},
			}
			var stdout, stderr bytes.Buffer
			report := Run(Invocation{
				Context: context.Background(), CWD: fixture.cwd, Args: []string{"migrations", "check"}, Environment: fixture.environment,
				Stdout: &stdout, Stderr: &stderr, Backend: backend,
			})
			if inspections <= maxAncestors {
				if report.ExitCode != 0 || report.AncestorDirectoriesInspected != inspections || report.DescriptorReads != 1 || report.BuildCalls != 1 || report.RunnerCalls != 1 {
					t.Fatalf("ancestors %d = %+v", inspections, report)
				}
				return
			}
			if report.ExitCode != 2 || report.Failure.Code != protocol.CodeProjectSearchLimitExceeded || report.AncestorDirectoriesInspected != maxAncestors || report.DescriptorReads != 0 || report.BuildCalls != 0 || report.RunnerCalls != 0 || stdout.Len() != 0 {
				t.Fatalf("ancestors %d = %+v stdout=%q", inspections, report, stdout.String())
			}
		})
	}
}

func TestRunnerResponseCapActualGlobalProcessTriplet(t *testing.T) {
	t.Parallel()
	for _, size := range []int{protocol.MaxResponseBytes - 1, protocol.MaxResponseBytes, protocol.MaxResponseBytes + 1} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			fixture := newGlobalFixture(t, 0)
			wire, err := protocol.EncodeResponse(protocol.Response{OK: true, Result: protocol.Result{DefinitionSetDigest: protocol.EmptySetDigest}})
			if err != nil {
				t.Fatal(err)
			}
			if len(wire) > size {
				t.Fatalf("canonical response length %d exceeds target %d", len(wire), size)
			}
			wire = append(wire, bytes.Repeat([]byte{' '}, size-len(wire))...)
			backend := &actualStageBackend{
				stage: RunnerStage,
				mode:  "wire-stderr",
				extra: map[string]string{"GODJ_HELPER_WIRE": string(wire)},
			}
			var stdout, stderr bytes.Buffer
			report := Run(Invocation{
				Context: context.Background(), CWD: fixture.project, Args: []string{"migrations", "check"}, Environment: fixture.environment,
				Stdout: &stdout, Stderr: &stderr, Backend: backend,
			})
			if size <= protocol.MaxResponseBytes {
				if report.ExitCode != 0 || report.HasFailure || !report.HasResult || report.BuildCalls != 1 || report.RunnerCalls != 1 || report.RunnerResponseWrites != 1 || report.UserStdoutWrites != 1 || report.UserStderrWrites != 0 {
					t.Fatalf("response %d = %+v", size, report)
				}
			} else if report.ExitCode != 3 || report.Failure.Code != protocol.CodeInvalidProjectRunnerResponse || report.BuildCalls != 1 || report.RunnerCalls != 1 || report.RunnerResponseWrites != 1 || report.UserStdoutWrites != 0 || report.UserStderrWrites != 1 {
				t.Fatalf("response %d = %+v", size, report)
			}
			if got := backend.last.StdoutScalar.RetainedBytes; got != min(size, protocol.MaxResponseBytes) || backend.last.StdoutScalar.Truncated != (size > protocol.MaxResponseBytes) {
				t.Fatalf("response %d scalar = %+v", size, backend.last.StdoutScalar)
			}
		})
	}
}

func TestGenerationRunnerResponseCapExactMaxAndMaxPlusOne(t *testing.T) {
	for _, size := range []int{projectgenerateprotocol.MaxResponseBytes, projectgenerateprotocol.MaxResponseBytes + 1} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			fixture := newGlobalFixture(t, 0)
			wire, err := projectgenerateprotocol.EncodeResponse(projectgenerateprotocol.Response{OK: true, ProjectSpec: codegen.ProjectSpec{
				Project: codegen.PackageSpec{PackageName: "project", ImportPath: "example.com/site/project", Directory: "project"},
				Apps: []codegen.AppSpec{{
					Alias: "articles", Package: codegen.PackageSpec{PackageName: "articles", ImportPath: "example.com/site/articles", Directory: "articles"},
					Schema: ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "articles"},
				}},
			}})
			if err != nil {
				t.Fatal(err)
			}
			backend := &actualStageBackend{
				stage: GenerationRunnerStage,
				mode:  "generation-wire",
				extra: map[string]string{
					"GODJ_HELPER_WIRE":         string(wire),
					"GODJ_HELPER_STDOUT_BYTES": strconv.Itoa(size),
				},
			}
			var stdout, stderr bytes.Buffer
			report := RunGenerate(GenerationInvocation{
				Context: context.Background(), CWD: fixture.project, Args: []string{"generate", "--check"}, Environment: fixture.environment,
				Stdout: &stdout, Stderr: &stderr, Backend: backend,
			})
			if report.ExitCode != 3 || report.GenerationFailure != (GenerationFailure{Category: GenerationCategoryProtocol, Code: projectgenerateprotocol.CodeInvalidResponse}) || report.BuildCalls != 1 || report.RunnerCalls != 1 {
				t.Fatalf("response %d = %+v stdout=%q stderr=%q", size, report, stdout.String(), stderr.String())
			}
			if got := backend.last.StdoutScalar.RetainedBytes; got != projectgenerateprotocol.MaxResponseBytes || backend.last.StdoutScalar.Truncated != (size > projectgenerateprotocol.MaxResponseBytes) {
				t.Fatalf("response %d scalar=%+v", size, backend.last.StdoutScalar)
			}
		})
	}
}

func TestDiagnosticCapsActualGlobalProcessTriplets(t *testing.T) {
	t.Parallel()
	for _, stream := range []string{"build_stdout", "build_stderr", "runner_stderr"} {
		stream := stream
		t.Run(stream, func(t *testing.T) {
			for _, size := range []int{maxDiagnosticBytes - 1, maxDiagnosticBytes, maxDiagnosticBytes + 1} {
				t.Run(strconv.Itoa(size), func(t *testing.T) {
					fixture := newGlobalFixture(t, 0)
					backend := diagnosticBackend(t, stream, size)
					var stdout, stderr bytes.Buffer
					report := Run(Invocation{
						Context: context.Background(), CWD: fixture.project, Args: []string{"migrations", "check"}, Environment: fixture.environment,
						Stdout: &stdout, Stderr: &stderr, Backend: backend,
					})
					retained := min(size, maxDiagnosticBytes)
					truncated := size > maxDiagnosticBytes
					switch stream {
					case "build_stdout":
						if report.ExitCode != 3 || report.Failure.Code != protocol.CodeProjectBuildFailed || report.BuildCalls != 1 || report.RunnerCalls != 0 || report.BuildStdoutRetainedBytes != retained || report.BuildStdoutTruncated != truncated {
							t.Fatalf("%s %d = %+v", stream, size, report)
						}
					case "build_stderr":
						if report.ExitCode != 3 || report.Failure.Code != protocol.CodeProjectBuildFailed || report.BuildCalls != 1 || report.RunnerCalls != 0 || report.BuildStderrRetainedBytes != retained || report.BuildStderrTruncated != truncated {
							t.Fatalf("%s %d = %+v", stream, size, report)
						}
					case "runner_stderr":
						if report.ExitCode != 0 || report.BuildCalls != 1 || report.RunnerCalls != 1 || report.RunnerStderrRetainedBytes != retained || report.RunnerStderrTruncated != truncated || report.UserStdoutWrites != 1 || report.UserStderrWrites != 0 {
							t.Fatalf("%s %d = %+v", stream, size, report)
						}
					}
					if !report.RawDiagnosticsDiscarded || report.TempCleanupAttempts != 1 || report.ResidualTemp != 0 {
						t.Fatalf("%s %d finalization = %+v", stream, size, report)
					}
				})
			}
		})
	}
}

type actualStageBackend struct {
	stage ProcessStage
	mode  string
	extra map[string]string
	last  ProcessResult
}

func (backend *actualStageBackend) Execute(ctx context.Context, interrupt <-chan struct{}, stage ProcessStage, command Command) ProcessResult {
	if stage != backend.stage {
		return ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1}
	}
	helper := productHelperCommand(command, backend.mode, backend.extra)
	backend.last = processBackend{}.Execute(ctx, interrupt, stage, helper)
	return backend.last
}

func diagnosticBackend(t *testing.T, stream string, size int) *actualStageBackend {
	t.Helper()
	switch stream {
	case "build_stdout":
		return &actualStageBackend{
			stage: BuildStage,
			mode:  "emit",
			extra: map[string]string{"GODJ_HELPER_STDOUT_BYTES": strconv.Itoa(size), "GODJ_HELPER_EXIT": "7"},
		}
	case "build_stderr":
		return &actualStageBackend{
			stage: BuildStage,
			mode:  "emit",
			extra: map[string]string{"GODJ_HELPER_STDERR_BYTES": strconv.Itoa(size), "GODJ_HELPER_EXIT": "7"},
		}
	case "runner_stderr":
		wire, err := protocol.EncodeResponse(protocol.Response{OK: true, Result: protocol.Result{DefinitionSetDigest: protocol.EmptySetDigest}})
		if err != nil {
			t.Fatal(err)
		}
		return &actualStageBackend{
			stage: RunnerStage,
			mode:  "wire-stderr",
			extra: map[string]string{"GODJ_HELPER_STDERR_BYTES": strconv.Itoa(size), "GODJ_HELPER_WIRE": string(wire)},
		}
	default:
		t.Fatalf("unknown diagnostic stream %q", stream)
		return nil
	}
}

func productHelperCommand(product Command, mode string, extra map[string]string) Command {
	environment := environmentValues(product.Env)
	environment["GODJ_OWNED_PROCESS_HELPER"] = mode
	for key, value := range extra {
		environment[key] = value
	}
	return Command{
		Dir:  product.Dir,
		Argv: []string{os.Args[0], "-test.run=^TestOwnedProcessHelper$"},
		Env:  sortedEnvironment(environment),
	}
}

func descriptorOfSize(t *testing.T, size int) []byte {
	t.Helper()
	base := canonicalDescriptor()
	remaining := size - len(base)
	if remaining < 2 {
		t.Fatalf("descriptor size %d is smaller than canonical base", size)
	}
	document := append([]byte(nil), base...)
	document = append(document, '#')
	document = append(document, strings.Repeat("x", remaining-2)...)
	document = append(document, '\n')
	if len(document) != size {
		t.Fatalf("descriptor size = %d, want %d", len(document), size)
	}
	return document
}
