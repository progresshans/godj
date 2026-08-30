//go:build darwin || linux

package projectcheck

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/progresshans/godj/internal/projectcheck/sqlmigrateprotocol"
)

// RunSQLMigrate executes one project-linked exact forward SQL projection. It
// owns pre-I/O argv validation, selection, private build/child lifetime,
// response validation, cleanup and one final public publication. It never
// retries a build, child or terminal write.
func RunSQLMigrate(input SQLMigrateInvocation) SQLMigrateReport {
	input.Args = append([]string(nil), input.Args...)
	arguments, primary := parseSQLMigrateArguments(input.Args)
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

	report := SQLMigrateReport{}
	if terminal := sqlMigrateBarrier(input, primary); terminal != nil {
		chooseSQLMigrateFailure(&report, *terminal)
		publishSQLMigrate(input, &report)
		return report
	}

	selected, selectionFailure := selectProject(
		input.CWD,
		commandArguments{explicitDescriptor: arguments.explicitDescriptor},
		&report.Report,
	)
	if selectionFailure != nil {
		candidate := mapSQLMigrateOuterFailure(*selectionFailure)
		primary = &candidate
	}
	if primary == nil && !verifyRetainedProject(selected) {
		candidate := sqlMigrateFailure(sqlmigrateprotocol.CategorySelection, sqlmigrateprotocol.CodeProjectSelectionFailed)
		primary = &candidate
	}
	if terminal := sqlMigrateBarrier(input, primary); terminal != nil {
		if selected.root != nil && selected.close() != nil {
			report.CleanupFailed = 1
			terminal = combineSQLMigrateCleanup(terminal, true)
		}
		chooseSQLMigrateFailure(&report, *terminal)
		publishSQLMigrate(input, &report)
		return report
	}

	workspace, workspaceFailure := createPrivateWorkspaceWithHooks(
		selected,
		input.Environment,
		&report.Report,
		input.workspace,
	)
	if workspaceFailure != nil {
		candidate := mapSQLMigrateOuterFailure(*workspaceFailure)
		primary = &candidate
		terminal := sqlMigrateBarrier(input, primary)
		if selected.close() != nil {
			report.CleanupFailed = 1
		}
		terminal = combineSQLMigrateCleanup(terminal, report.CleanupFailed != 0)
		chooseSQLMigrateFailure(&report, *terminal)
		publishSQLMigrate(input, &report)
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
		if !report.HasSQLMigrateFailure || sqlMigrateCanceledOrInterrupted(report.SQLMigrateFailure) {
			clearSQLMigrateResult(&report)
			report.HasSQLMigrateFailure = true
			report.SQLMigrateFailure = sqlMigrateCleanupFailure()
		}
	}
	finish := func() SQLMigrateReport {
		cleanup()
		publishSQLMigrate(input, &report)
		return report
	}

	if terminal := sqlMigrateBarrier(input, nil); terminal != nil {
		chooseSQLMigrateFailure(&report, *terminal)
		return finish()
	}
	if !verifyRetainedProject(selected) {
		chooseSQLMigrateFailure(&report, sqlMigrateFailure(
			sqlmigrateprotocol.CategorySelection,
			sqlmigrateprotocol.CodeProjectSelectionFailed,
		))
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
	primary = sqlMigrateProcessFailure(BuildStage, build)
	primary = sqlMigrateBarrier(input, primary)
	primary = combineSQLMigrateCleanup(primary, build.CleanupFailed)
	if primary != nil {
		chooseSQLMigrateFailure(&report, *primary)
		return finish()
	}

	if !verifyRetainedProject(selected) {
		chooseSQLMigrateFailure(&report, sqlMigrateFailure(
			sqlmigrateprotocol.CategorySelection,
			sqlmigrateprotocol.CodeProjectSelectionFailed,
		))
		return finish()
	}
	if terminal := sqlMigrateBarrier(input, nil); terminal != nil {
		chooseSQLMigrateFailure(&report, *terminal)
		return finish()
	}

	runnerCommand := Command{
		Dir:   selected.rootPath,
		Argv:  []string{filepath.Join(workspace.root, "godj-project-runner"), sqlmigrateprotocol.PrivateArgument},
		Env:   workspace.environment,
		Stdin: append([]byte(nil), arguments.requestDocument...),
	}
	report.RunnerCalls++
	runner := input.Backend.Execute(input.Context, input.Interrupt, SQLMigrateRunnerStage, cloneCommand(runnerCommand))
	recordProcess(&report.Report, SQLMigrateRunnerStage, runner)
	report.RunnerStdoutRetainedBytes = runner.StdoutScalar.RetainedBytes
	report.RunnerStdoutTruncated = runner.StdoutScalar.Truncated
	primary = sqlMigrateProcessFailure(SQLMigrateRunnerStage, runner)
	primary = combineSQLMigrateCleanup(primary, runner.CleanupFailed)
	var response sqlmigrateprotocol.Response
	if primary == nil {
		if runner.StdoutScalar.Truncated {
			candidate := sqlMigrateFailure(
				sqlmigrateprotocol.CategorySQLResource,
				sqlmigrateprotocol.CodeRenderedSQLResourceLimit,
			)
			primary = &candidate
		} else {
			parsed, parseFailure, failed := sqlmigrateprotocol.ParseResponse(
				runner.Stdout,
				runner.Started && runner.ExitCode == 0,
			)
			if failed {
				candidate := sqlMigrateFailure(parseFailure.Category, parseFailure.Code)
				primary = &candidate
			} else {
				response = parsed
			}
		}
	}
	clear(runner.Stdout)
	runner.Stdout = nil
	if primary != nil {
		chooseSQLMigrateFailure(&report, *primary)
		return finish()
	}
	// A fully reaped child and strictly parsed response is terminal. A later
	// outer cancellation does not erase it; cleanup still precedes publication.
	if response.OK {
		chooseSQLMigrateResult(&report, response.Result)
		return finish()
	}
	if response.Failure.CleanupFailed {
		report.CleanupFailed = 1
	}
	chooseSQLMigrateFailure(&report, response.Failure)
	return finish()
}

func mapSQLMigrateOuterFailure(input Failure) SQLMigrateFailure {
	return sqlMigrateFailure(input.Category, input.Code)
}

func sqlMigrateProcessFailure(stage ProcessStage, process ProcessResult) *SQLMigrateFailure {
	if process.Failure != nil {
		candidate := mapSQLMigrateOuterFailure(*process.Failure)
		if candidate.Category == sqlmigrateprotocol.CategoryProcess {
			switch candidate.Code {
			case sqlmigrateprotocol.CodeProjectCanceled,
				sqlmigrateprotocol.CodeProjectInterrupted,
				sqlmigrateprotocol.CodeProjectCleanupFailed:
				return &candidate
			}
		}
		internal := sqlMigrateInternalFailure()
		return &internal
	}
	if process.Started && process.ExitCode == 0 {
		return nil
	}
	if stage == BuildStage {
		candidate := sqlMigrateFailure(sqlmigrateprotocol.CategoryBuild, sqlmigrateprotocol.CodeProjectBuildFailed)
		return &candidate
	}
	candidate := sqlMigrateFailure(sqlmigrateprotocol.CategoryProtocol, sqlmigrateprotocol.CodeRunnerFailed)
	return &candidate
}

func sqlMigrateBarrier(input SQLMigrateInvocation, primary *SQLMigrateFailure) *SQLMigrateFailure {
	if primary != nil && primary.Category == sqlmigrateprotocol.CategoryProcess &&
		primary.Code == sqlmigrateprotocol.CodeProjectCleanupFailed {
		return primary
	}
	if input.Interrupt != nil {
		select {
		case <-input.Interrupt:
			candidate := sqlMigrateFailure(sqlmigrateprotocol.CategoryProcess, sqlmigrateprotocol.CodeProjectInterrupted)
			return &candidate
		default:
		}
	}
	if input.Context != nil && input.Context.Err() != nil {
		candidate := sqlMigrateFailure(sqlmigrateprotocol.CategoryProcess, sqlmigrateprotocol.CodeProjectCanceled)
		return &candidate
	}
	return primary
}

func combineSQLMigrateCleanup(primary *SQLMigrateFailure, failed bool) *SQLMigrateFailure {
	if !failed {
		return primary
	}
	if primary == nil || sqlMigrateCanceledOrInterrupted(*primary) {
		candidate := sqlMigrateCleanupFailure()
		return &candidate
	}
	return primary
}

func sqlMigrateCanceledOrInterrupted(input SQLMigrateFailure) bool {
	return input.Category == sqlmigrateprotocol.CategoryProcess &&
		(input.Code == sqlmigrateprotocol.CodeProjectCanceled || input.Code == sqlmigrateprotocol.CodeProjectInterrupted)
}

func sqlMigrateCleanupFailure() SQLMigrateFailure {
	return sqlMigrateFailure(sqlmigrateprotocol.CategoryProcess, sqlmigrateprotocol.CodeProjectCleanupFailed)
}

func sqlMigrateInternalFailure() SQLMigrateFailure {
	return SQLMigrateFailure{Category: sqlmigrateprotocol.CategoryInternal, Code: sqlmigrateprotocol.CodeProjectInternalError}
}

func sqlMigrateFailure(category, code string) SQLMigrateFailure {
	candidate := SQLMigrateFailure{Category: category, Code: code}
	if _, ok := sqlmigrateprotocol.ExitCode(candidate); !ok {
		return sqlMigrateInternalFailure()
	}
	return candidate
}

func chooseSQLMigrateFailure(report *SQLMigrateReport, primary SQLMigrateFailure) {
	if report.HasSQLMigrateFailure || report.HasSQLMigrateResult {
		return
	}
	if _, ok := sqlmigrateprotocol.ExitCode(primary); !ok {
		primary = sqlMigrateInternalFailure()
	}
	report.HasSQLMigrateFailure = true
	report.SQLMigrateFailure = primary
}

func chooseSQLMigrateResult(report *SQLMigrateReport, result SQLMigrateResult) {
	if report.HasSQLMigrateFailure || report.HasSQLMigrateResult || result.Statements == nil {
		return
	}
	report.HasSQLMigrateResult = true
	report.SQLMigrateResult.Statements = append([]string(nil), result.Statements...)
	if len(result.Statements) == 0 {
		report.SQLMigrateResult.Statements = make([]string, 0)
	}
}

func clearSQLMigrateResult(report *SQLMigrateReport) {
	for index := range report.SQLMigrateResult.Statements {
		report.SQLMigrateResult.Statements[index] = ""
	}
	report.SQLMigrateResult = SQLMigrateResult{}
	report.HasSQLMigrateResult = false
}

func publishSQLMigrate(input SQLMigrateInvocation, report *SQLMigrateReport) {
	if !report.HasSQLMigrateFailure && !report.HasSQLMigrateResult {
		chooseSQLMigrateFailure(report, sqlMigrateInternalFailure())
	}
	if report.HasSQLMigrateFailure {
		exit, ok := sqlmigrateprotocol.ExitCode(report.SQLMigrateFailure)
		if !ok {
			report.SQLMigrateFailure = sqlMigrateInternalFailure()
			exit = 3
		}
		report.ExitCode = exit
		report.UserStderrWrites++
		if input.Stderr != nil {
			_, _ = writeOnce(input.Stderr, []byte(
				report.SQLMigrateFailure.Category+"/"+report.SQLMigrateFailure.Code+"\n",
			))
		}
		return
	}

	payload, err := renderSQLMigrate(report.SQLMigrateResult)
	if err != nil {
		clearSQLMigrateResult(report)
		report.HasSQLMigrateFailure = true
		report.SQLMigrateFailure = sqlMigrateInternalFailure()
		publishSQLMigrate(input, report)
		return
	}
	if len(payload) == 0 {
		report.ExitCode = 0
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
	// A terminal write may have exposed a prefix. Never retry and never publish
	// a second stderr record that could be mistaken for part of the SQL stream.
	clearSQLMigrateResult(report)
	report.HasSQLMigrateFailure = true
	report.SQLMigrateFailure = sqlMigrateInternalFailure()
	report.ExitCode = 3
}

func renderSQLMigrate(result SQLMigrateResult) ([]byte, error) {
	if err := sqlmigrateprotocol.ValidateResult(result); err != nil {
		return nil, err
	}
	total := 0
	for _, statement := range result.Statements {
		if total > sqlmigrateprotocol.MaxPublicOutputBytes-2 ||
			len(statement) > sqlmigrateprotocol.MaxPublicOutputBytes-total-2 {
			return nil, errors.New("projectcheck: sqlmigrate output exceeds resource limit")
		}
		total += len(statement) + 2
	}
	output := make([]byte, 0, total)
	for _, statement := range result.Statements {
		output = append(output, statement...)
		output = append(output, ';', '\n')
	}
	return output, nil
}
