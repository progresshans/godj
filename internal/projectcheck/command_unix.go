//go:build darwin || linux

package projectcheck

import (
	"context"
	"os"
	"path/filepath"

	"github.com/progresshans/godj/internal/projectcheck/protocol"
)

// Common mechanics stop before command-specific outcome classification. In
// particular, migrate and operator publication retain their durable outcomes
// when a later cancellation arrives.
func normalizeCommandInput(ctx context.Context, environment []string, backend Backend) (context.Context, []string, Backend) {
	if environment == nil {
		environment = os.Environ()
	}
	environment = append([]string(nil), environment...)
	if ctx == nil {
		ctx = context.Background()
	}
	if backend == nil {
		backend = processBackend{}
	}
	return ctx, environment, backend
}

func closeCommandWorkspace(report *Report, closeProject, cleanupWorkspace func() error) bool {
	report.TempCleanupAttempts++
	failed := closeProject() != nil
	if err := cleanupWorkspace(); err != nil {
		failed = true
		report.ResidualTemp = 1
	}
	if failed {
		report.CleanupFailed = 1
	}
	return failed
}

func buildProjectPackage(
	ctx context.Context,
	interrupt <-chan struct{},
	backend Backend,
	selected retainedProject,
	workspace privateWorkspace,
	packagePath, binaryName string,
	report *Report,
) ProcessResult {
	environment := workspace.buildEnvironment
	if environment == nil {
		environment = workspace.environment
	}
	command := Command{
		Dir: selected.rootPath,
		Argv: []string{
			"go", "build", "-buildvcs=false", "-mod=readonly", "-o",
			filepath.Join(workspace.root, binaryName), packagePath,
		},
		Env: environment,
	}
	report.BuildCalls++
	result := backend.Execute(ctx, interrupt, BuildStage, cloneCommand(command))
	recordProcess(report, BuildStage, result)
	clear(result.Stdout)
	result.Stdout = nil
	return result
}

// The machine-readable category/code and one-write boundary stay unchanged.
// A build failure may carry additional sanitized, bounded human diagnostics.
func publicFailureDocument(report *Report, category, code string) []byte {
	document := category + "/" + code + "\n"
	if code == "project_build_failed" || code == "project_runtime_build_failed" || code == "project_generate_candidate_verification_failed" {
		if report.BuildDiagnostic != "" {
			document += report.BuildDiagnostic + "\n"
		}
	}
	return []byte(document)
}

// commandInterruption observes interrupt before cancellation. Callers decide
// whether an existing cleanup failure or a completed result takes precedence.
func commandInterruption(ctx context.Context, interrupt <-chan struct{}) string {
	if interrupt != nil {
		select {
		case <-interrupt:
			return protocol.CodeProjectInterrupted
		default:
		}
	}
	if ctx != nil && ctx.Err() != nil {
		return protocol.CodeProjectCanceled
	}
	return ""
}

// Migration commands share only outer child-execution classification. Their
// runner codes, response grammars and durable publication decisions stay local.
func migrationCommandProcessFailure[F any](stage ProcessStage, process ProcessResult, runnerCode string, makeFailure func(string, string) F) *F {
	category, code := protocol.CategoryInternal, protocol.CodeProjectInternalError
	if process.Failure != nil {
		if process.Failure.Category == protocol.CategoryProcess {
			switch process.Failure.Code {
			case protocol.CodeProjectCanceled, protocol.CodeProjectInterrupted, protocol.CodeProjectCleanupFailed:
				category, code = process.Failure.Category, process.Failure.Code
			}
		}
	} else if process.Started && process.ExitCode == 0 {
		return nil
	} else if stage == BuildStage {
		category, code = protocol.CategoryBuild, protocol.CodeProjectBuildFailed
	} else {
		category, code = protocol.CategoryProtocol, runnerCode
	}
	candidate := makeFailure(category, code)
	return &candidate
}
