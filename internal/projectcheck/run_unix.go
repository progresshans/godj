//go:build darwin || linux

package projectcheck

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/progresshans/godj/internal/projectcheck/protocol"
)

// Run executes one global project-check invocation and returns observations
// collected at its actual global callsites. It owns publication but never
// exits the process.
func Run(input Invocation) Report {
	input.Args = append([]string(nil), input.Args...)
	input.Context, input.Environment, input.Backend = normalizeCommandInput(input.Context, input.Environment, input.Backend)

	report := Report{}
	arguments, primary := parseArguments(input.Args)
	if terminal := barrierFailure(input, primary); terminal != nil {
		chooseFailure(&report, *terminal)
		publish(input, &report)
		return report
	}

	mapOuter := func(input Failure) Failure { return input }
	command := newProjectCommand(input.Context, input.Interrupt, input.Backend, &report,
		commandPolicy[Failure]{
			selection: mapOuter, workspace: mapOuter, process: processFailure,
			cleanup: combineProcessCleanup,
			barrier: func(primary *Failure) *Failure { return barrierFailure(input, primary) },
		}, commandHooks{})
	finish := func() Report {
		if command.close() && (!report.HasFailure ||
			report.Failure.Code == protocol.CodeProjectCanceled || report.Failure.Code == protocol.CodeProjectInterrupted) {
			report.HasResult = false
			report.Result = Result{}
			report.HasFailure = true
			report.Failure = failure(protocol.CategoryProcess, protocol.CodeProjectCleanupFailed)
		}
		publish(input, &report)
		return report
	}
	if primary = command.open(input.CWD, arguments.explicitDescriptor, input.Environment, input.workspace); primary != nil {
		chooseFailure(&report, *primary)
		return finish()
	}
	if primary = command.buildRunner(); primary != nil {
		chooseFailure(&report, *primary)
		return finish()
	}

	runner := command.run(RunnerStage, protocol.PrivateArgument, protocol.RequestDocument())
	response, primary := parseCommandResponse(&runner, command.completed(RunnerStage, runner),
		failure(protocol.CategoryProtocol, protocol.CodeInvalidProjectRunnerResponse), protocol.ParseResponse)
	primary = barrierFailure(input, primary)
	if primary != nil {
		chooseFailure(&report, *primary)
	} else if response.OK {
		chooseResult(&report, response.Result)
	} else {
		chooseFailure(&report, failure(response.Failure.Category, response.Failure.Code))
	}
	return finish()
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
		report.BuildDiagnostic = process.BuildDiagnostic
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
	if code := commandInterruption(input.Context, input.Interrupt); code != "" {
		candidate := Failure{Category: protocol.CategoryProcess, Code: code}
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
			_, _ = writeOnce(input.Stderr, publicFailureDocument(report, report.Failure.Category, report.Failure.Code))
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
