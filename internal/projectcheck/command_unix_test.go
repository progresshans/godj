//go:build darwin || linux

package projectcheck

import (
	"testing"

	"github.com/progresshans/godj/internal/projectcheck/protocol"
)

func TestMigrationCommandsRetainDistinctRunnerAndOuterFailureClassification(t *testing.T) {
	commands := []struct {
		name string
		run  func(ProcessStage, ProcessResult) Failure
	}{
		{"migrate", func(stage ProcessStage, result ProcessResult) Failure {
			if failure := migrateProcessFailure(stage, result); failure != nil {
				return Failure{Category: failure.Category, Code: failure.Code}
			}
			return Failure{}
		}},
		{"sqlmigrate", func(stage ProcessStage, result ProcessResult) Failure {
			if failure := sqlMigrateProcessFailure(stage, result); failure != nil {
				return Failure{Category: failure.Category, Code: failure.Code}
			}
			return Failure{}
		}},
		{"showmigrations", func(stage ProcessStage, result ProcessResult) Failure {
			if failure := showMigrationsProcessFailure(stage, result); failure != nil {
				return Failure{Category: failure.Category, Code: failure.Code}
			}
			return Failure{}
		}},
	}
	for _, command := range commands {
		t.Run(command.name, func(t *testing.T) {
			for _, stage := range []ProcessStage{BuildStage, RunnerStage} {
				if got := command.run(stage, ProcessResult{Started: true}); got != (Failure{}) {
					t.Fatalf("successful process: %+v", got)
				}
				want := Failure{Category: protocol.CategoryBuild, Code: protocol.CodeProjectBuildFailed}
				if stage == RunnerStage {
					want = Failure{Category: protocol.CategoryProtocol, Code: "project_" + command.name + "_runner_failed"}
				}
				for _, result := range []ProcessResult{{}, {Started: true, ExitCode: 1}} {
					if got := command.run(stage, result); got != want {
						t.Fatalf("stage %v failed process: %+v, want %+v", stage, got, want)
					}
				}
				for _, code := range []string{protocol.CodeProjectCanceled, protocol.CodeProjectInterrupted, protocol.CodeProjectCleanupFailed, "unknown"} {
					input := Failure{Category: protocol.CategoryProcess, Code: code}
					want := input
					if code == "unknown" {
						want = Failure{Category: protocol.CategoryInternal, Code: protocol.CodeProjectInternalError}
					}
					if got := command.run(stage, ProcessResult{Started: true, Failure: &input}); got != want {
						t.Fatalf("outer process failure: %+v, want %+v", got, want)
					}
				}
				for _, category := range []string{protocol.CategoryBuild, protocol.CategoryProtocol, "unknown"} {
					input := Failure{Category: category, Code: protocol.CodeProjectCanceled}
					got := command.run(stage, ProcessResult{Started: true, Failure: &input})
					if got != (Failure{Category: protocol.CategoryInternal, Code: protocol.CodeProjectInternalError}) {
						t.Fatalf("non-process outer failure was trusted: %+v", got)
					}
				}
			}
		})
	}
}
