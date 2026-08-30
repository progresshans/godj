//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"unicode"
	"unicode/utf8"

	"github.com/progresshans/godj/internal/projectcheck/showmigrationsprotocol"
)

// RunShowMigrations executes one project-linked read-only migration-status
// listing. It owns selection, build, child lifetime, strict response parsing,
// cleanup, and one terminal public publication. It never retries.
func RunShowMigrations(input ShowMigrationsInvocation) ShowMigrationsReport {
	input.Args = append([]string(nil), input.Args...)
	arguments, primary := parseShowMigrationsArguments(input.Args)
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

	report := ShowMigrationsReport{}
	if terminal := showMigrationsBarrier(input, primary); terminal != nil {
		chooseShowMigrationsFailure(&report, *terminal)
		publishShowMigrations(input, &report)
		return report
	}

	selected, selectionFailure := selectProject(
		input.CWD,
		commandArguments{explicitDescriptor: arguments.explicitDescriptor},
		&report.Report,
	)
	if selectionFailure != nil {
		candidate := mapShowMigrationsOuterFailure(*selectionFailure)
		primary = &candidate
	}
	if primary == nil && !verifyRetainedProject(selected) {
		candidate := showMigrationsFailure(
			showmigrationsprotocol.CategorySelection,
			showmigrationsprotocol.CodeProjectSelectionFailed,
		)
		primary = &candidate
	}
	if terminal := showMigrationsBarrier(input, primary); terminal != nil {
		if selected.root != nil && selected.close() != nil {
			report.CleanupFailed = 1
			terminal = combineShowMigrationsCleanup(terminal, true)
		}
		chooseShowMigrationsFailure(&report, *terminal)
		publishShowMigrations(input, &report)
		return report
	}

	workspace, workspaceFailure := createPrivateWorkspaceWithHooks(
		selected,
		input.Environment,
		&report.Report,
		input.workspace,
	)
	if workspaceFailure != nil {
		candidate := mapShowMigrationsOuterFailure(*workspaceFailure)
		primary = &candidate
		terminal := showMigrationsBarrier(input, primary)
		if selected.close() != nil {
			report.CleanupFailed = 1
		}
		terminal = combineShowMigrationsCleanup(terminal, report.CleanupFailed != 0)
		chooseShowMigrationsFailure(&report, *terminal)
		publishShowMigrations(input, &report)
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
		if !report.HasShowMigrationsFailure || showMigrationsCanceledOrInterrupted(report.ShowMigrationsFailure) {
			report.HasShowMigrationsResult = false
			report.ShowMigrationsResult = ShowMigrationsResult{}
			report.HasShowMigrationsFailure = true
			report.ShowMigrationsFailure = showMigrationsCleanupFailure()
		}
	}
	finish := func() ShowMigrationsReport {
		cleanup()
		publishShowMigrations(input, &report)
		return report
	}

	if terminal := showMigrationsBarrier(input, nil); terminal != nil {
		chooseShowMigrationsFailure(&report, *terminal)
		return finish()
	}
	if !verifyRetainedProject(selected) {
		chooseShowMigrationsFailure(&report, showMigrationsFailure(
			showmigrationsprotocol.CategorySelection,
			showmigrationsprotocol.CodeProjectSelectionFailed,
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
	primary = showMigrationsProcessFailure(BuildStage, build)
	primary = showMigrationsBarrier(input, primary)
	primary = combineShowMigrationsCleanup(primary, build.CleanupFailed)
	if primary != nil {
		chooseShowMigrationsFailure(&report, *primary)
		return finish()
	}

	if !verifyRetainedProject(selected) {
		chooseShowMigrationsFailure(&report, showMigrationsFailure(
			showmigrationsprotocol.CategorySelection,
			showmigrationsprotocol.CodeProjectSelectionFailed,
		))
		return finish()
	}
	if terminal := showMigrationsBarrier(input, nil); terminal != nil {
		chooseShowMigrationsFailure(&report, *terminal)
		return finish()
	}
	runnerCommand := Command{
		Dir:   selected.rootPath,
		Argv:  []string{filepath.Join(workspace.root, "godj-project-runner"), showmigrationsprotocol.PrivateArgument},
		Env:   workspace.environment,
		Stdin: showmigrationsprotocol.RequestDocument(),
	}
	report.RunnerCalls++
	runner := input.Backend.Execute(
		input.Context,
		input.Interrupt,
		ShowMigrationsRunnerStage,
		cloneCommand(runnerCommand),
	)
	recordProcess(&report.Report, ShowMigrationsRunnerStage, runner)
	primary = showMigrationsProcessFailure(ShowMigrationsRunnerStage, runner)
	primary = combineShowMigrationsCleanup(primary, runner.CleanupFailed)
	var response showmigrationsprotocol.Response
	if primary == nil {
		if runner.StdoutScalar.Truncated {
			candidate := showMigrationsFailure(
				showmigrationsprotocol.CategoryProtocol,
				showmigrationsprotocol.CodeInvalidResponse,
			)
			primary = &candidate
		} else {
			parsed, parseFailure, failed := showmigrationsprotocol.ParseResponse(
				runner.Stdout,
				runner.Started && runner.ExitCode == 0,
			)
			if failed {
				candidate := showMigrationsFailure(parseFailure.Category, parseFailure.Code)
				primary = &candidate
			} else {
				response = parsed
			}
		}
	}
	clear(runner.Stdout)
	runner.Stdout = nil
	if primary != nil {
		chooseShowMigrationsFailure(&report, *primary)
		return finish()
	}
	// A fully reaped child and strictly parsed response is the terminal
	// point-in-time snapshot. A cancellation observed after this point must not
	// erase the already closed read outcome.
	if response.OK {
		chooseShowMigrationsResult(&report, response.Result)
		return finish()
	}
	if response.Failure.CleanupFailed {
		report.CleanupFailed = 1
	}
	chooseShowMigrationsFailure(&report, response.Failure)
	return finish()
}

func mapShowMigrationsOuterFailure(input Failure) ShowMigrationsFailure {
	return showMigrationsFailure(input.Category, input.Code)
}

func showMigrationsProcessFailure(stage ProcessStage, process ProcessResult) *ShowMigrationsFailure {
	if process.Failure != nil {
		candidate := mapShowMigrationsOuterFailure(*process.Failure)
		if candidate.Category == showmigrationsprotocol.CategoryProcess {
			switch candidate.Code {
			case showmigrationsprotocol.CodeProjectCanceled,
				showmigrationsprotocol.CodeProjectInterrupted,
				showmigrationsprotocol.CodeProjectCleanupFailed:
				return &candidate
			}
		}
		internal := showMigrationsInternalFailure()
		return &internal
	}
	if process.Started && process.ExitCode == 0 {
		return nil
	}
	if stage == BuildStage {
		candidate := showMigrationsFailure(
			showmigrationsprotocol.CategoryBuild,
			showmigrationsprotocol.CodeProjectBuildFailed,
		)
		return &candidate
	}
	candidate := showMigrationsFailure(
		showmigrationsprotocol.CategoryProtocol,
		showmigrationsprotocol.CodeRunnerFailed,
	)
	return &candidate
}

func showMigrationsBarrier(input ShowMigrationsInvocation, primary *ShowMigrationsFailure) *ShowMigrationsFailure {
	if primary != nil && primary.Category == showmigrationsprotocol.CategoryProcess && primary.Code == showmigrationsprotocol.CodeProjectCleanupFailed {
		return primary
	}
	if input.Interrupt != nil {
		select {
		case <-input.Interrupt:
			candidate := showMigrationsFailure(
				showmigrationsprotocol.CategoryProcess,
				showmigrationsprotocol.CodeProjectInterrupted,
			)
			return &candidate
		default:
		}
	}
	if input.Context != nil && input.Context.Err() != nil {
		candidate := showMigrationsFailure(
			showmigrationsprotocol.CategoryProcess,
			showmigrationsprotocol.CodeProjectCanceled,
		)
		return &candidate
	}
	return primary
}

func combineShowMigrationsCleanup(primary *ShowMigrationsFailure, failed bool) *ShowMigrationsFailure {
	if !failed {
		return primary
	}
	if primary == nil || showMigrationsCanceledOrInterrupted(*primary) {
		candidate := showMigrationsCleanupFailure()
		return &candidate
	}
	return primary
}

func showMigrationsCanceledOrInterrupted(input ShowMigrationsFailure) bool {
	return input.Category == showmigrationsprotocol.CategoryProcess &&
		(input.Code == showmigrationsprotocol.CodeProjectCanceled || input.Code == showmigrationsprotocol.CodeProjectInterrupted)
}

func showMigrationsCleanupFailure() ShowMigrationsFailure {
	return showMigrationsFailure(
		showmigrationsprotocol.CategoryProcess,
		showmigrationsprotocol.CodeProjectCleanupFailed,
	)
}

func showMigrationsInternalFailure() ShowMigrationsFailure {
	return ShowMigrationsFailure{
		Category: showmigrationsprotocol.CategoryInternal,
		Code:     showmigrationsprotocol.CodeProjectInternalError,
	}
}

func showMigrationsFailure(category, code string) ShowMigrationsFailure {
	candidate := ShowMigrationsFailure{Category: category, Code: code}
	if _, ok := showmigrationsprotocol.ExitCode(candidate); !ok {
		return showMigrationsInternalFailure()
	}
	return candidate
}

func chooseShowMigrationsFailure(report *ShowMigrationsReport, primary ShowMigrationsFailure) {
	if report.HasShowMigrationsFailure || report.HasShowMigrationsResult {
		return
	}
	if _, ok := showmigrationsprotocol.ExitCode(primary); !ok {
		primary = showMigrationsInternalFailure()
	}
	report.HasShowMigrationsFailure = true
	report.ShowMigrationsFailure = primary
}

func chooseShowMigrationsResult(report *ShowMigrationsReport, result ShowMigrationsResult) {
	if report.HasShowMigrationsFailure || report.HasShowMigrationsResult {
		return
	}
	report.HasShowMigrationsResult = true
	report.ShowMigrationsResult = result
}

func publishShowMigrations(input ShowMigrationsInvocation, report *ShowMigrationsReport) {
	if !report.HasShowMigrationsFailure && !report.HasShowMigrationsResult {
		chooseShowMigrationsFailure(report, showMigrationsInternalFailure())
	}
	if report.HasShowMigrationsFailure {
		exit, ok := showmigrationsprotocol.ExitCode(report.ShowMigrationsFailure)
		if !ok {
			report.ShowMigrationsFailure = showMigrationsInternalFailure()
			exit = 3
		}
		report.ExitCode = exit
		report.UserStderrWrites++
		if input.Stderr != nil {
			_, _ = writeOnce(input.Stderr, []byte(
				report.ShowMigrationsFailure.Category+"/"+report.ShowMigrationsFailure.Code+"\n",
			))
		}
		return
	}

	payload, err := renderShowMigrations(report.ShowMigrationsResult)
	if err != nil {
		report.HasShowMigrationsResult = false
		report.ShowMigrationsResult = ShowMigrationsResult{}
		report.HasShowMigrationsFailure = true
		report.ShowMigrationsFailure = showMigrationsInternalFailure()
		publishShowMigrations(input, report)
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
	report.HasShowMigrationsResult = false
	report.ShowMigrationsResult = ShowMigrationsResult{}
	report.HasShowMigrationsFailure = true
	report.ShowMigrationsFailure = showMigrationsInternalFailure()
	report.ExitCode = 3
}

func renderShowMigrations(result ShowMigrationsResult) ([]byte, error) {
	// Reuse the strict protocol validator at this final trust boundary so direct
	// in-process Backend implementations cannot smuggle a non-canonical result
	// into the public renderer.
	if _, err := showmigrationsprotocol.EncodeResponse(showmigrationsprotocol.Response{OK: true, Result: result}); err != nil {
		return nil, err
	}
	if len(result.Rows) == 0 {
		return []byte("(no migrations)\n"), nil
	}
	var output bytes.Buffer
	previousApp := ""
	for _, row := range result.Rows {
		if row.App != previousApp {
			output.WriteString(escapeShowMigrationsApp(row.App))
			output.WriteByte('\n')
			previousApp = row.App
		}
		output.WriteByte(' ')
		switch row.Status {
		case showmigrationsprotocol.StatusApplied:
			output.WriteString("[X] ")
		case showmigrationsprotocol.StatusUnapplied:
			output.WriteString("[ ] ")
		case showmigrationsprotocol.StatusUnknown:
			output.WriteString("[?] ")
		default:
			return nil, errors.New("projectcheck: invalid showmigrations status") // unreachable after strict validation
		}
		output.WriteString(escapeShowMigrationsIdentity(row.Name))
		output.WriteByte('\n')
	}
	return output.Bytes(), nil
}

func escapeShowMigrationsIdentity(value string) string {
	quoted := strconv.QuoteToGraphic(value)
	return quoted[1 : len(quoted)-1]
}

func escapeShowMigrationsApp(value string) string {
	first, size := utf8.DecodeRuneInString(value)
	if size != 0 && unicode.IsSpace(first) {
		return escapeShowMigrationsLeadingSpace(first) + escapeShowMigrationsIdentity(value[size:])
	}
	return escapeShowMigrationsIdentity(value)
}

func escapeShowMigrationsLeadingSpace(value rune) string {
	switch {
	case value <= 0x7f:
		return fmt.Sprintf(`\x%02x`, value)
	case value <= 0xffff:
		return fmt.Sprintf(`\u%04x`, value)
	default:
		return fmt.Sprintf(`\U%08x`, value)
	}
}
