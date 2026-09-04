//go:build darwin || linux

package projectcheck

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/progresshans/godj/internal/projectcheck/migrateprotocol"
)

// RunMigrate executes one explicit project-linked latest/targeted migrate or
// read-only plan. It owns selection, build, child lifetime, private response
// validation, cleanup, and public publication; it never retries a build or
// migration runner.
func RunMigrate(input MigrateInvocation) MigrateReport {
	input.Args = append([]string(nil), input.Args...)
	arguments, primary := parseMigrateArguments(input.Args)
	if input.Environment == nil {
		input.Environment = append([]string(nil), os.Environ()...)
	} else {
		input.Environment = append([]string(nil), input.Environment...)
	}
	if input.Context == nil {
		input.Context = context.Background()
	}
	if input.Backend == nil {
		input.Backend = processBackend{}
	}

	report := MigrateReport{}
	if terminal := migrateBarrier(input, primary); terminal != nil {
		chooseMigrateFailure(&report, *terminal)
		publishMigrate(input, &report)
		return report
	}

	selected, selectionFailure := selectProject(input.CWD, commandArguments{explicitDescriptor: arguments.explicitDescriptor}, &report.Report)
	if selectionFailure != nil {
		candidate := mapMigrateOuterFailure(*selectionFailure)
		primary = &candidate
	}
	if primary == nil && !verifyRetainedProject(selected) {
		candidate := migrateFailure(migrateprotocol.CategorySelection, migrateprotocol.CodeProjectSelectionFailed)
		primary = &candidate
	}
	if terminal := migrateBarrier(input, primary); terminal != nil {
		if selected.root != nil && selected.close() != nil {
			report.CleanupFailed = 1
			terminal = combineMigrateCleanup(terminal, true)
		}
		chooseMigrateFailure(&report, *terminal)
		publishMigrate(input, &report)
		return report
	}

	workspace, workspaceFailure := createPrivateWorkspaceWithHooks(selected, input.Environment, &report.Report, input.workspace)
	if workspaceFailure != nil {
		candidate := mapMigrateOuterFailure(*workspaceFailure)
		primary = &candidate
		terminal := migrateBarrier(input, primary)
		if selected.close() != nil {
			report.CleanupFailed = 1
		}
		terminal = combineMigrateCleanup(terminal, report.CleanupFailed != 0)
		chooseMigrateFailure(&report, *terminal)
		publishMigrate(input, &report)
		return report
	}

	cleanup := func() {
		report.TempCleanupAttempts++
		cleanupFailed := selected.close() != nil
		if err := workspace.cleanup(); err != nil {
			cleanupFailed = true
			report.ResidualTemp = 1
		}
		if !cleanupFailed {
			return
		}
		report.CleanupFailed = 1
		if !report.HasMigrateFailure || migrateCanceledOrInterrupted(report.MigrateFailure) {
			clearMigrateSuccess(&report)
			report.HasMigrateFailure = true
			report.MigrateFailure = migrateCleanupFailure()
		}
	}
	finish := func() MigrateReport {
		cleanup()
		publishMigrate(input, &report)
		return report
	}

	if terminal := migrateBarrier(input, nil); terminal != nil {
		chooseMigrateFailure(&report, *terminal)
		return finish()
	}
	if !verifyRetainedProject(selected) {
		chooseMigrateFailure(&report, migrateFailure(migrateprotocol.CategorySelection, migrateprotocol.CodeProjectSelectionFailed))
		return finish()
	}

	buildCommand := Command{
		Dir: selected.rootPath,
		Argv: []string{
			"go", "build", "-buildvcs=false", "-mod=readonly", "-o",
			filepath.Join(workspace.root, "godj-project-runner"),
			selected.descriptor.packagePath,
		},
		Env: workspace.environment,
	}
	report.BuildCalls++
	build := input.Backend.Execute(input.Context, input.Interrupt, BuildStage, cloneCommand(buildCommand))
	recordProcess(&report.Report, BuildStage, build)
	clear(build.Stdout)
	build.Stdout = nil
	primary = migrateProcessFailure(BuildStage, build)
	primary = migrateBarrier(input, primary)
	primary = combineMigrateCleanup(primary, build.CleanupFailed)
	if primary != nil {
		chooseMigrateFailure(&report, *primary)
		return finish()
	}

	if !verifyRetainedProject(selected) {
		chooseMigrateFailure(&report, migrateFailure(migrateprotocol.CategorySelection, migrateprotocol.CodeProjectSelectionFailed))
		return finish()
	}
	if terminal := migrateBarrier(input, nil); terminal != nil {
		chooseMigrateFailure(&report, *terminal)
		return finish()
	}
	runnerCommand := Command{
		Dir:   selected.rootPath,
		Argv:  []string{filepath.Join(workspace.root, "godj-project-runner"), migrateprotocol.PrivateArgument},
		Env:   workspace.environment,
		Stdin: append([]byte(nil), arguments.requestDocument...),
	}
	report.RunnerCalls++
	runner := input.Backend.Execute(input.Context, input.Interrupt, MigrateRunnerStage, cloneCommand(runnerCommand))
	recordProcess(&report.Report, MigrateRunnerStage, runner)
	report.RunnerStdoutRetainedBytes = runner.StdoutScalar.RetainedBytes
	report.RunnerStdoutTruncated = runner.StdoutScalar.Truncated
	primary = migrateProcessFailure(MigrateRunnerStage, runner)
	primary = combineMigrateCleanup(primary, runner.CleanupFailed)
	var response migrateprotocol.Response
	if primary == nil {
		if runner.StdoutScalar.Truncated {
			candidate := migrateFailure(migrateprotocol.CategoryProtocol, migrateprotocol.CodeInvalidResponse)
			primary = &candidate
		} else {
			parsed, parseFailure, failed := migrateprotocol.ParseResponse(runner.Stdout, runner.Started && runner.ExitCode == 0)
			if failed {
				candidate := migrateFailure(parseFailure.Category, parseFailure.Code)
				primary = &candidate
			} else {
				response = parsed
				if response.OK && response.Result.Mode != arguments.request.Mode {
					candidate := migrateFailure(migrateprotocol.CategoryProtocol, migrateprotocol.CodeInvalidResponse)
					primary = &candidate
				}
			}
		}
	}
	clear(runner.Stdout)
	runner.Stdout = nil
	if primary != nil {
		chooseMigrateFailure(&report, *primary)
		return finish()
	}
	// A completed, strictly parsed child response is the terminal observation.
	// A later outer cancellation must not erase commit-unknown or another
	// durable migration outcome; the process owner observes cancellation until
	// direct exit and both response drains complete.
	if response.OK {
		switch response.Result.Mode {
		case migrateprotocol.ModeExecute:
			chooseMigrateResult(&report, response.Result.Execute)
		case migrateprotocol.ModePlan:
			chooseMigratePlan(&report, response.Result.Plan)
		default:
			chooseMigrateFailure(&report, migrateFailure(migrateprotocol.CategoryProtocol, migrateprotocol.CodeInvalidResponse))
		}
		return finish()
	}
	if response.Failure.CleanupFailed {
		report.CleanupFailed = 1
	}
	chooseMigrateFailure(&report, response.Failure)
	return finish()
}

func mapMigrateOuterFailure(input Failure) MigrateFailure {
	return migrateFailure(input.Category, input.Code)
}

func migrateProcessFailure(stage ProcessStage, process ProcessResult) *MigrateFailure {
	if process.Failure != nil {
		candidate := mapMigrateOuterFailure(*process.Failure)
		if candidate.Category == migrateprotocol.CategoryProcess {
			switch candidate.Code {
			case migrateprotocol.CodeProjectCanceled, migrateprotocol.CodeProjectInterrupted, migrateprotocol.CodeProjectCleanupFailed:
				return &candidate
			}
		}
		internal := migrateInternalFailure()
		return &internal
	}
	if process.Started && process.ExitCode == 0 {
		return nil
	}
	if stage == BuildStage {
		candidate := migrateFailure(migrateprotocol.CategoryBuild, migrateprotocol.CodeProjectBuildFailed)
		return &candidate
	}
	candidate := migrateFailure(migrateprotocol.CategoryProtocol, migrateprotocol.CodeRunnerFailed)
	return &candidate
}

func migrateBarrier(input MigrateInvocation, primary *MigrateFailure) *MigrateFailure {
	if primary != nil && primary.Category == migrateprotocol.CategoryProcess && primary.Code == migrateprotocol.CodeProjectCleanupFailed {
		return primary
	}
	if input.Interrupt != nil {
		select {
		case <-input.Interrupt:
			candidate := migrateFailure(migrateprotocol.CategoryProcess, migrateprotocol.CodeProjectInterrupted)
			return &candidate
		default:
		}
	}
	if input.Context != nil && input.Context.Err() != nil {
		candidate := migrateFailure(migrateprotocol.CategoryProcess, migrateprotocol.CodeProjectCanceled)
		return &candidate
	}
	return primary
}

func combineMigrateCleanup(primary *MigrateFailure, failed bool) *MigrateFailure {
	if !failed {
		return primary
	}
	if primary == nil || migrateCanceledOrInterrupted(*primary) {
		candidate := migrateCleanupFailure()
		return &candidate
	}
	return primary
}

func migrateCanceledOrInterrupted(input MigrateFailure) bool {
	return input.Category == migrateprotocol.CategoryProcess && (input.Code == migrateprotocol.CodeProjectCanceled || input.Code == migrateprotocol.CodeProjectInterrupted)
}

func migrateCleanupFailure() MigrateFailure {
	return migrateFailure(migrateprotocol.CategoryProcess, migrateprotocol.CodeProjectCleanupFailed)
}

func migrateInternalFailure() MigrateFailure {
	return MigrateFailure{Category: migrateprotocol.CategoryInternal, Code: migrateprotocol.CodeProjectInternalError}
}

func migrateFailure(category, code string) MigrateFailure {
	candidate := MigrateFailure{Category: category, Code: code}
	if _, ok := migrateprotocol.ExitCode(candidate); !ok {
		return migrateInternalFailure()
	}
	return candidate
}

func chooseMigrateFailure(report *MigrateReport, primary MigrateFailure) {
	if report.HasMigrateFailure || report.HasMigrateResult || report.HasMigratePlan {
		return
	}
	if _, ok := migrateprotocol.ExitCode(primary); !ok {
		primary = migrateInternalFailure()
	}
	report.HasMigrateFailure = true
	report.MigrateFailure = primary
}

func chooseMigrateResult(report *MigrateReport, result MigrateResult) {
	if report.HasMigrateFailure || report.HasMigrateResult || report.HasMigratePlan {
		return
	}
	report.HasMigrateResult = true
	report.MigrateResult = result
}

func chooseMigratePlan(report *MigrateReport, plan []MigratePlanRow) {
	if report.HasMigrateFailure || report.HasMigrateResult || report.HasMigratePlan || plan == nil {
		return
	}
	report.HasMigratePlan = true
	report.MigratePlan = append([]MigratePlanRow(nil), plan...)
	if len(plan) == 0 {
		report.MigratePlan = make([]MigratePlanRow, 0)
	}
}

func clearMigrateSuccess(report *MigrateReport) {
	report.HasMigrateResult = false
	report.MigrateResult = MigrateResult{}
	report.HasMigratePlan = false
	report.MigratePlan = nil
}

func publishMigrate(input MigrateInvocation, report *MigrateReport) {
	if !report.HasMigrateFailure && !report.HasMigrateResult && !report.HasMigratePlan {
		chooseMigrateFailure(report, migrateInternalFailure())
	}
	if report.HasMigrateFailure {
		exit, ok := migrateprotocol.ExitCode(report.MigrateFailure)
		if !ok {
			report.MigrateFailure = migrateInternalFailure()
			exit = 3
		}
		report.ExitCode = exit
		report.UserStderrWrites++
		if input.Stderr != nil {
			_, _ = writeOnce(input.Stderr, []byte(report.MigrateFailure.Category+"/"+report.MigrateFailure.Code+"\n"))
		}
		return
	}

	var payload []byte
	var err error
	if report.HasMigratePlan {
		payload, err = migrateprotocol.EncodePublicPlan(report.MigratePlan)
	} else {
		payload, err = json.Marshal(struct {
			SourceCount         int    `json:"source_count"`
			DefinitionCount     int    `json:"definition_count"`
			DefinitionSetDigest string `json:"definition_set_digest"`
		}{
			SourceCount:         report.MigrateResult.SourceCount,
			DefinitionCount:     report.MigrateResult.DefinitionCount,
			DefinitionSetDigest: report.MigrateResult.DefinitionSetDigest,
		})
		payload = append(payload, '\n')
	}
	if err != nil {
		clearMigrateSuccess(report)
		report.HasMigrateFailure = true
		report.MigrateFailure = migrateInternalFailure()
		publishMigrate(input, report)
		return
	}
	report.UserStdoutWrites++
	written, writeErr := writeOnce(input.Stdout, payload)
	if written > 0 && written < len(payload) {
		report.PartialStdoutWrites++
	}
	if writeErr == nil {
		report.ExitCode = 0
		return
	}
	clearMigrateSuccess(report)
	report.HasMigrateFailure = true
	report.MigrateFailure = migrateInternalFailure()
	report.ExitCode = 3
}
