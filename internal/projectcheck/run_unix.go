//go:build darwin || linux

package projectcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/progresshans/godj/internal/projectcheck/protocol"
)

// Run executes one global project-check invocation and returns observations
// collected at its actual global callsites. It owns publication but never
// exits the process.
func Run(input Invocation) Report {
	input.Args = append([]string(nil), input.Args...)
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

	report := Report{}
	arguments, primary := parseArguments(input.Args)
	if terminal := barrierFailure(input, primary); terminal != nil {
		chooseFailure(&report, *terminal)
		publish(input, &report)
		return report
	}

	selected, primary := selectProject(input.CWD, arguments, &report)
	if primary == nil && !verifyRetainedProject(selected) {
		candidate := failure(protocol.CategorySelection, protocol.CodeProjectSelectionFailed)
		primary = &candidate
	}
	if terminal := barrierFailure(input, primary); terminal != nil {
		if selected.root != nil {
			if err := selected.close(); err != nil {
				report.CleanupFailed = 1
				if terminal.Category == protocol.CategoryProcess && (terminal.Code == protocol.CodeProjectCanceled || terminal.Code == protocol.CodeProjectInterrupted) {
					cleanupFailure := failure(protocol.CategoryProcess, protocol.CodeProjectCleanupFailed)
					terminal = &cleanupFailure
				}
			}
		}
		chooseFailure(&report, *terminal)
		publish(input, &report)
		return report
	}

	workspace, primary := createPrivateWorkspaceWithHooks(selected, input.Environment, &report, input.workspace)
	if primary != nil {
		terminal := barrierFailure(input, primary)
		if err := selected.close(); err != nil {
			report.CleanupFailed = 1
			if terminal.Category == protocol.CategoryProcess && (terminal.Code == protocol.CodeProjectCanceled || terminal.Code == protocol.CodeProjectInterrupted) {
				cleanupFailure := failure(protocol.CategoryProcess, protocol.CodeProjectCleanupFailed)
				terminal = &cleanupFailure
			}
		}
		terminal = combineProcessCleanup(terminal, report.CleanupFailed != 0)
		chooseFailure(&report, *terminal)
		publish(input, &report)
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
		if !report.HasFailure || report.Failure.Code == protocol.CodeProjectCanceled || report.Failure.Code == protocol.CodeProjectInterrupted {
			report.HasResult = false
			report.Result = Result{}
			report.HasFailure = true
			report.Failure = failure(protocol.CategoryProcess, protocol.CodeProjectCleanupFailed)
		}
	}
	if terminal := barrierFailure(input, nil); terminal != nil {
		chooseFailure(&report, *terminal)
		cleanup()
		publish(input, &report)
		return report
	}

	if !verifyRetainedProject(selected) {
		chooseFailure(&report, failure(protocol.CategorySelection, protocol.CodeProjectSelectionFailed))
		cleanup()
		publish(input, &report)
		return report
	}
	if terminal := barrierFailure(input, nil); terminal != nil {
		chooseFailure(&report, *terminal)
		cleanup()
		publish(input, &report)
		return report
	}
	buildCommand := Command{
		Dir: selected.rootPath,
		Argv: []string{
			"go", "build", "-mod=readonly", "-o",
			filepath.Join(workspace.root, "godj-project-runner"),
			selected.descriptor.packagePath,
		},
		Env: workspace.environment,
	}
	report.BuildCalls++
	build := input.Backend.Execute(input.Context, input.Interrupt, BuildStage, cloneCommand(buildCommand))
	recordProcess(&report, BuildStage, build)
	clear(build.Stdout)
	build.Stdout = nil
	primary = processFailure(BuildStage, build)
	primary = barrierFailure(input, primary)
	primary = combineProcessCleanup(primary, build.CleanupFailed)
	if primary != nil {
		chooseFailure(&report, *primary)
		cleanup()
		publish(input, &report)
		return report
	}

	if !verifyRetainedProject(selected) {
		chooseFailure(&report, failure(protocol.CategorySelection, protocol.CodeProjectSelectionFailed))
		cleanup()
		publish(input, &report)
		return report
	}
	if terminal := barrierFailure(input, nil); terminal != nil {
		chooseFailure(&report, *terminal)
		cleanup()
		publish(input, &report)
		return report
	}
	runnerCommand := Command{
		Dir:   selected.rootPath,
		Argv:  []string{filepath.Join(workspace.root, "godj-project-runner"), protocol.PrivateArgument},
		Env:   workspace.environment,
		Stdin: protocol.RequestDocument(),
	}
	report.RunnerCalls++
	runner := input.Backend.Execute(input.Context, input.Interrupt, RunnerStage, cloneCommand(runnerCommand))
	recordProcess(&report, RunnerStage, runner)
	primary = processFailure(RunnerStage, runner)
	var response protocol.Response
	if primary == nil {
		if runner.StdoutScalar.Truncated {
			candidate := failure(protocol.CategoryProtocol, protocol.CodeInvalidProjectRunnerResponse)
			primary = &candidate
		} else {
			parsed, parseFailure, failed := protocol.ParseResponse(runner.Stdout, runner.Started && runner.ExitCode == 0)
			if failed {
				candidate := failure(parseFailure.Category, parseFailure.Code)
				primary = &candidate
			} else {
				response = parsed
			}
		}
	}
	clear(runner.Stdout)
	primary = barrierFailure(input, primary)
	primary = combineProcessCleanup(primary, runner.CleanupFailed)
	if primary != nil {
		chooseFailure(&report, *primary)
	} else if response.OK {
		chooseResult(&report, response.Result)
	} else {
		chooseFailure(&report, failure(response.Failure.Category, response.Failure.Code))
	}
	cleanup()
	publish(input, &report)
	return report
}

func processFailure(stage ProcessStage, process ProcessResult) *Failure {
	if process.Failure != nil {
		candidate := *process.Failure
		if _, ok := protocol.ExitCode(candidate); !ok {
			internal := failure(protocol.CategoryInternal, protocol.CodeProjectInternalError)
			return &internal
		}
		if candidate.Category == protocol.CategoryProcess {
			switch candidate.Code {
			case protocol.CodeProjectCanceled, protocol.CodeProjectInterrupted, protocol.CodeProjectCleanupFailed:
				return &candidate
			}
		}
		if stage == BuildStage && candidate.Category == protocol.CategoryBuild && (candidate.Code == protocol.CodeProjectBuildFailed || candidate.Code == protocol.CodeProjectTemporaryStorageFailed) {
			return &candidate
		}
		if stage == RunnerStage && candidate.Category == protocol.CategoryProtocol && candidate.Code == protocol.CodeProjectRunnerFailed {
			return &candidate
		}
		internal := failure(protocol.CategoryInternal, protocol.CodeProjectInternalError)
		return &internal
	}
	if !process.Started {
		if stage == BuildStage {
			candidate := failure(protocol.CategoryBuild, protocol.CodeProjectBuildFailed)
			return &candidate
		}
		candidate := failure(protocol.CategoryProtocol, protocol.CodeProjectRunnerFailed)
		return &candidate
	}
	if process.ExitCode != 0 {
		if stage == BuildStage {
			candidate := failure(protocol.CategoryBuild, protocol.CodeProjectBuildFailed)
			return &candidate
		}
		candidate := failure(protocol.CategoryProtocol, protocol.CodeProjectRunnerFailed)
		return &candidate
	}
	return nil
}

func recordProcess(report *Report, stage ProcessStage, process ProcessResult) {
	report.DirectChildReaps += process.DirectReaps
	report.GroupSIGINTAttempts += process.SIGINTAttempts
	report.GroupSIGKILLAttempts += process.SIGKILLAttempts
	if process.CleanupFailed {
		report.CleanupFailed = 1
	}
	report.RawDiagnosticsDiscarded = true
	if stage == BuildStage {
		report.BuildStdoutRetainedBytes = process.StdoutScalar.RetainedBytes
		report.BuildStdoutTruncated = process.StdoutScalar.Truncated
		report.BuildStderrRetainedBytes = process.StderrScalar.RetainedBytes
		report.BuildStderrTruncated = process.StderrScalar.Truncated
		return
	}
	report.RunnerStderrRetainedBytes = process.StderrScalar.RetainedBytes
	report.RunnerStderrTruncated = process.StderrScalar.Truncated
	if process.StdoutScalar.RetainedBytes != 0 || process.StdoutScalar.Truncated {
		report.RunnerResponseWrites++
	}
}

func barrierFailure(input Invocation, primary *Failure) *Failure {
	if primary != nil && primary.Category == protocol.CategoryProcess && primary.Code == protocol.CodeProjectCleanupFailed {
		return primary
	}
	if input.Interrupt != nil {
		select {
		case <-input.Interrupt:
			candidate := failure(protocol.CategoryProcess, protocol.CodeProjectInterrupted)
			return &candidate
		default:
		}
	}
	if input.Context != nil && input.Context.Err() != nil {
		candidate := failure(protocol.CategoryProcess, protocol.CodeProjectCanceled)
		return &candidate
	}
	return primary
}

func combineProcessCleanup(primary *Failure, failed bool) *Failure {
	if !failed {
		return primary
	}
	if primary == nil || (primary.Category == protocol.CategoryProcess && (primary.Code == protocol.CodeProjectCanceled || primary.Code == protocol.CodeProjectInterrupted)) {
		candidate := failure(protocol.CategoryProcess, protocol.CodeProjectCleanupFailed)
		return &candidate
	}
	return primary
}

func chooseFailure(report *Report, primary Failure) {
	if report.HasFailure || report.HasResult {
		return
	}
	if _, ok := protocol.ExitCode(primary); !ok {
		primary = failure(protocol.CategoryInternal, protocol.CodeProjectInternalError)
	}
	report.HasFailure = true
	report.Failure = primary
}

func chooseResult(report *Report, result Result) {
	if report.HasFailure || report.HasResult {
		return
	}
	report.HasResult = true
	report.Result = result
}

func publish(input Invocation, report *Report) {
	if !report.HasFailure && !report.HasResult {
		chooseFailure(report, failure(protocol.CategoryInternal, protocol.CodeProjectInternalError))
	}
	if report.HasFailure {
		exit, ok := protocol.ExitCode(report.Failure)
		if !ok {
			report.Failure = failure(protocol.CategoryInternal, protocol.CodeProjectInternalError)
			exit = 3
		}
		report.ExitCode = exit
		report.UserStderrWrites++
		if input.Stderr != nil {
			_, _ = writeOnce(input.Stderr, []byte(report.Failure.Category+"/"+report.Failure.Code+"\n"))
		}
		return
	}
	payload, err := json.Marshal(struct {
		SourceCount         int    `json:"source_count"`
		DefinitionCount     int    `json:"definition_count"`
		DefinitionSetDigest string `json:"definition_set_digest"`
	}{
		SourceCount:         report.Result.SourceCount,
		DefinitionCount:     report.Result.DefinitionCount,
		DefinitionSetDigest: report.Result.DefinitionSetDigest,
	})
	if err != nil {
		report.HasResult = false
		report.Result = Result{}
		report.HasFailure = true
		report.Failure = failure(protocol.CategoryInternal, protocol.CodeProjectInternalError)
		publish(input, report)
		return
	}
	payload = append(payload, '\n')
	report.UserStdoutWrites++
	written, writeErr := writeOnce(input.Stdout, payload)
	if written > 0 && written < len(payload) {
		report.PartialStdoutWrites++
	}
	if writeErr == nil {
		report.ExitCode = 0
		return
	}
	report.HasResult = false
	report.Result = Result{}
	report.HasFailure = true
	report.Failure = failure(protocol.CategoryInternal, protocol.CodeProjectInternalError)
	report.ExitCode = 3
}

func writeOnce(writer io.Writer, payload []byte) (int, error) {
	if writer == nil {
		return 0, fmt.Errorf("projectcheck: nil output writer")
	}
	written, err := writer.Write(payload)
	if written < 0 || written > len(payload) {
		return 0, io.ErrShortWrite
	}
	if written != len(payload) && err == nil {
		err = io.ErrShortWrite
	}
	return written, err
}
