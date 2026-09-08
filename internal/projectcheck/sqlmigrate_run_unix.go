//go:build darwin || linux

package projectcheck

import (
	"errors"

	"github.com/progresshans/godj/internal/projectcheck/sqlmigrateprotocol"
)

// RunSQLMigrate executes one project-linked exact forward SQL projection. It
// owns pre-I/O argv validation, selection, private build/child lifetime,
// response validation, cleanup and one final public publication. It never
// retries a build, child or terminal write.
func RunSQLMigrate(input SQLMigrateInvocation) SQLMigrateReport {
	input.Args = append([]string(nil), input.Args...)
	arguments, primary := parseSQLMigrateArguments(input.Args)
	input.Context, input.Environment, input.Backend = normalizeCommandInput(input.Context, input.Environment, input.Backend)

	report := SQLMigrateReport{}
	if terminal := sqlMigrateBarrier(input, primary); terminal != nil {
		chooseSQLMigrateFailure(&report, *terminal)
		publishSQLMigrate(input, &report)
		return report
	}

	command := newProjectCommand(input.Context, input.Interrupt, input.Backend, &report.Report,
		commandPolicy[SQLMigrateFailure]{
			selection: mapSQLMigrateOuterFailure, workspace: mapSQLMigrateOuterFailure,
			process: sqlMigrateProcessFailure, cleanup: combineSQLMigrateCleanup,
			barrier: func(primary *SQLMigrateFailure) *SQLMigrateFailure { return sqlMigrateBarrier(input, primary) },
		}, commandHooks{})
	finish := func() SQLMigrateReport {
		if command.close() && (!report.HasSQLMigrateFailure || sqlMigrateCanceledOrInterrupted(report.SQLMigrateFailure)) {
			clearSQLMigrateResult(&report)
			report.HasSQLMigrateFailure = true
			report.SQLMigrateFailure = sqlMigrateCleanupFailure()
		}
		publishSQLMigrate(input, &report)
		return report
	}
	if primary = command.open(input.CWD, arguments.explicitDescriptor, input.Environment, input.workspace); primary != nil {
		chooseSQLMigrateFailure(&report, *primary)
		return finish()
	}
	if primary = command.buildRunner(); primary != nil {
		chooseSQLMigrateFailure(&report, *primary)
		return finish()
	}

	runner := command.run(SQLMigrateRunnerStage, sqlmigrateprotocol.PrivateArgument, arguments.requestDocument)
	report.RunnerStdoutRetainedBytes = runner.StdoutScalar.RetainedBytes
	report.RunnerStdoutTruncated = runner.StdoutScalar.Truncated
	response, primary := parseCommandResponse(&runner, command.completed(SQLMigrateRunnerStage, runner),
		sqlMigrateFailure(sqlmigrateprotocol.CategorySQLResource, sqlmigrateprotocol.CodeRenderedSQLResourceLimit), sqlmigrateprotocol.ParseResponse)
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
	if code := commandInterruption(input.Context, input.Interrupt); code != "" {
		candidate := SQLMigrateFailure{Category: sqlmigrateprotocol.CategoryProcess, Code: code}
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
			_, _ = writeOnce(input.Stderr, publicFailureDocument(&report.Report, report.SQLMigrateFailure.Category, report.SQLMigrateFailure.Code))
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
