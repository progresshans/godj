//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"errors"
	"fmt"
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
	input.Context, input.Environment, input.Backend = normalizeCommandInput(input.Context, input.Environment, input.Backend)

	report := ShowMigrationsReport{}
	if terminal := showMigrationsBarrier(input, primary); terminal != nil {
		chooseShowMigrationsFailure(&report, *terminal)
		publishShowMigrations(input, &report)
		return report
	}

	command := newProjectCommand(input.Context, input.Interrupt, input.Backend, &report.Report,
		commandPolicy[ShowMigrationsFailure]{
			selection: mapShowMigrationsOuterFailure, workspace: mapShowMigrationsOuterFailure,
			process: showMigrationsProcessFailure, cleanup: combineShowMigrationsCleanup,
			barrier: func(primary *ShowMigrationsFailure) *ShowMigrationsFailure {
				return showMigrationsBarrier(input, primary)
			},
		}, commandHooks{})
	finish := func() ShowMigrationsReport {
		if command.close() && (!report.HasShowMigrationsFailure || showMigrationsCanceledOrInterrupted(report.ShowMigrationsFailure)) {
			report.HasShowMigrationsResult = false
			report.ShowMigrationsResult = ShowMigrationsResult{}
			report.HasShowMigrationsFailure = true
			report.ShowMigrationsFailure = showMigrationsCleanupFailure()
		}
		publishShowMigrations(input, &report)
		return report
	}
	if primary = command.open(input.CWD, arguments.explicitDescriptor, input.Environment, input.workspace); primary != nil {
		chooseShowMigrationsFailure(&report, *primary)
		return finish()
	}
	if primary = command.buildRunner(); primary != nil {
		chooseShowMigrationsFailure(&report, *primary)
		return finish()
	}

	runner := command.run(ShowMigrationsRunnerStage, showmigrationsprotocol.PrivateArgument, showmigrationsprotocol.RequestDocument())
	response, primary := parseCommandResponse(&runner, command.completed(ShowMigrationsRunnerStage, runner),
		showMigrationsFailure(showmigrationsprotocol.CategoryProtocol, showmigrationsprotocol.CodeInvalidResponse), showmigrationsprotocol.ParseResponse)
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
	return migrationCommandProcessFailure(stage, process, showmigrationsprotocol.CodeRunnerFailed, showMigrationsFailure)
}

func showMigrationsBarrier(input ShowMigrationsInvocation, primary *ShowMigrationsFailure) *ShowMigrationsFailure {
	if primary != nil && primary.Category == showmigrationsprotocol.CategoryProcess && primary.Code == showmigrationsprotocol.CodeProjectCleanupFailed {
		return primary
	}
	if code := commandInterruption(input.Context, input.Interrupt); code != "" {
		candidate := ShowMigrationsFailure{Category: showmigrationsprotocol.CategoryProcess, Code: code}
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
			_, _ = writeOnce(input.Stderr, publicFailureDocument(&report.Report, report.ShowMigrationsFailure.Category, report.ShowMigrationsFailure.Code))
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
