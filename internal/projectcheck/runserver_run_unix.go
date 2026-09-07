//go:build darwin || linux

package projectcheck

import (
	"errors"
	"path/filepath"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/gobuild"
	"github.com/progresshans/godj/internal/projectcheck/protocol"
	"github.com/progresshans/godj/internal/projectgenerate"
	projectgenerateprotocol "github.com/progresshans/godj/internal/projectgenerate/protocol"
)

// RunServer validates one project and runs its separately generated-aware
// development runtime. It never publishes generated files or runs migrations.
func RunServer(input RunServerInvocation) RunServerReport {
	input.Args = append([]string(nil), input.Args...)
	arguments, argumentsOK := parseRunserverArguments(input.Args)
	input.Context, input.Environment, input.Backend = normalizeCommandInput(input.Context, input.Environment, input.Backend)
	input.generation = completeGenerationHooks(input.generation)
	input.runtime = completeRunserverRuntimeHooks(input.runtime)

	report := RunServerReport{}
	var primary *RunServerFailure
	if !argumentsOK {
		candidate := RunServerFailure{Category: RunServerCategoryCommand, Code: RunServerCodeInvalidArguments}
		primary = &candidate
	}
	if terminal := runserverBarrier(input, primary); terminal != nil {
		chooseRunserverFailure(&report, *terminal)
		publishRunserver(input, &report)
		return report
	}

	command := newProjectCommand(input.Context, input.Interrupt, input.Backend, &report.Report,
		commandPolicy[RunServerFailure]{
			selection: mapRunserverSelectionFailure, workspace: mapRunserverWorkspaceFailure,
			barrier: func(primary *RunServerFailure) *RunServerFailure { return runserverBarrier(input, primary) },
			cleanup: combineRunserverCleanup,
			process: func(stage ProcessStage, process ProcessResult) *RunServerFailure {
				if stage == BuildStage {
					return runserverShortProcessFailure(process, RunServerCategoryBuild, RunServerCodeProjectBuildFailed)
				}
				return runserverShortProcessFailure(process, projectgenerateprotocol.CategoryProtocol, projectgenerateprotocol.CodeRunnerFailed)
			},
			afterSelection: func(selected retainedProject) *RunServerFailure {
				if selected.descriptor.runserverPackagePath == "" {
					candidate := RunServerFailure{Category: RunServerCategoryConfiguration, Code: RunServerCodeNotConfigured}
					return &candidate
				}
				if !validateRunserverPackageBoundary(selected) {
					candidate := RunServerFailure{Category: RunServerCategorySelection, Code: RunServerCodeInvalidProjectDescriptor}
					return &candidate
				}
				return nil
			},
		}, commandHooks{})
	finish := func(outcome *RunServerFailure, result *RunServerResult) RunServerReport {
		if command.close() {
			outcome = combineRunserverCleanup(outcome, true)
		}
		if outcome != nil {
			chooseRunserverFailure(&report, *outcome)
		} else if result != nil {
			chooseRunserverResult(&report, *result)
		} else {
			chooseRunserverFailure(&report, runserverInternalFailure())
		}
		publishRunserver(input, &report)
		return report
	}
	if primary = command.open(input.CWD, arguments.explicitDescriptor, input.Environment, input.workspace); primary != nil {
		return finish(primary, nil)
	}
	if primary = command.buildRunner(); primary != nil {
		return finish(primary, nil)
	}
	selected, workspace := command.selected, command.workspace
	runner := command.run(GenerationRunnerStage, projectgenerateprotocol.PrivateArgument, projectgenerateprotocol.RequestDocument())
	response, primary := parseCommandResponse(&runner, command.completed(GenerationRunnerStage, runner),
		RunServerFailure{Category: projectgenerateprotocol.CategoryProtocol, Code: projectgenerateprotocol.CodeInvalidResponse},
		func(document []byte, transportOK bool) (projectgenerateprotocol.Response, RunServerFailure, bool) {
			response, failed, hasFailure := projectgenerateprotocol.ParseResponse(document, transportOK)
			return response, RunServerFailure{Category: failed.Category, Code: failed.Code}, hasFailure
		})
	primary = runserverBarrier(input, primary)
	if primary != nil {
		return finish(primary, nil)
	}
	if !response.OK {
		candidate := RunServerFailure{Category: response.Failure.Category, Code: response.Failure.Code}
		return finish(&candidate, nil)
	}
	if !verifyRetainedProject(selected) {
		candidate := RunServerFailure{Category: RunServerCategorySelection, Code: RunServerCodeProjectSelectionFailed}
		return finish(&candidate, nil)
	}
	if terminal := runserverBarrier(input, nil); terminal != nil {
		return finish(terminal, nil)
	}

	bundle, err := input.generation.generate(response.ProjectSpec)
	if err != nil {
		candidate := RunServerFailure{Category: RunServerCategoryGeneration, Code: RunServerCodeProjectGenerateFailed}
		if terminal := runserverBarrier(input, &candidate); terminal != nil {
			return finish(terminal, nil)
		}
		return finish(&candidate, nil)
	}
	if !verifyRetainedProject(selected) {
		candidate := RunServerFailure{Category: RunServerCategorySelection, Code: RunServerCodeProjectSelectionFailed}
		return finish(&candidate, nil)
	}
	root, err := input.generation.sealRoot(selected.rootPath, uint64(selected.rootIdentity.Dev), uint64(selected.rootIdentity.Ino))
	if err != nil {
		candidate := RunServerFailure{Category: RunServerCategorySelection, Code: RunServerCodeProjectSelectionFailed}
		return finish(&candidate, nil)
	}
	if checkFailure := checkRunserverBundle(input, root, bundle, &report); checkFailure != nil {
		return finish(checkFailure, nil)
	}

	if terminal := runserverBarrier(input, nil); terminal != nil {
		return finish(terminal, nil)
	}
	if !verifyRetainedProject(selected) {
		candidate := RunServerFailure{Category: RunServerCategorySelection, Code: RunServerCodeProjectSelectionFailed}
		return finish(&candidate, nil)
	}
	runtimeBinary := filepath.Join(workspace.root, "godj-project-server")
	report.RuntimeBuildCalls++
	runtimeBuildResult := buildProjectPackage(input.Context, input.Interrupt, input.Backend, selected, workspace,
		selected.descriptor.runserverPackagePath, "godj-project-server", &report.Report)
	primary = runserverShortProcessFailure(runtimeBuildResult, RunServerCategoryBuild, RunServerCodeRuntimeBuildFailed)
	primary = runserverBarrier(input, primary)
	primary = combineRunserverCleanup(primary, runtimeBuildResult.CleanupFailed)
	if primary != nil {
		return finish(primary, nil)
	}
	if !verifyRetainedProject(selected) {
		candidate := RunServerFailure{Category: RunServerCategorySelection, Code: RunServerCodeProjectSelectionFailed}
		return finish(&candidate, nil)
	}
	if checkFailure := checkRunserverBundle(input, root, bundle, &report); checkFailure != nil {
		return finish(checkFailure, nil)
	}
	if terminal := runserverBarrier(input, nil); terminal != nil {
		return finish(terminal, nil)
	}
	if !verifyRetainedProject(selected) {
		candidate := RunServerFailure{Category: RunServerCategorySelection, Code: RunServerCodeProjectSelectionFailed}
		return finish(&candidate, nil)
	}

	report.RuntimeStartCalls++
	live := input.runtime.execute(input.Context, input.Interrupt, Command{
		Dir:  selected.rootPath,
		Argv: []string{runtimeBinary, "serve", "--listen", arguments.address},
		Env:  gobuild.RuntimeEnvironment(input.Environment),
	}, input.Stdout, input.Stderr, input.runtime.grace)
	recordRunserverProcess(&report, live)
	primary = classifyRunserverProcess(live)
	if primary != nil {
		return finish(primary, nil)
	}
	result := RunServerResult{Address: arguments.address}
	return finish(nil, &result)
}

func checkRunserverBundle(input RunServerInvocation, root projectgenerate.ProjectRoot, bundle codegen.GeneratedBundle, report *RunServerReport) *RunServerFailure {
	report.PreflightChecks++
	checked, err := input.generation.check(input.Context, root, bundle)
	report.GeneratedDriftCount += len(checked.Drifts)
	if terminal := runserverBarrier(input, nil); terminal != nil {
		return terminal
	}
	if errors.Is(err, projectgenerate.ErrGeneratedDrift) || !checked.Clean() {
		candidate := RunServerFailure{Category: RunServerCategoryGeneration, Code: RunServerCodeGeneratedBundleStale}
		return &candidate
	}
	if err != nil {
		candidate := RunServerFailure{Category: RunServerCategoryGeneration, Code: RunServerCodeProjectCheckFailed}
		return &candidate
	}
	return nil
}

func mapRunserverSelectionFailure(input Failure) RunServerFailure {
	switch input.Code {
	case RunServerCodeProjectNotFound, RunServerCodeProjectSearchLimitExceeded,
		RunServerCodeInvalidProjectDescriptor, RunServerCodeProjectDescriptorIncompatible,
		RunServerCodeProjectSelectionFailed:
		return RunServerFailure{Category: RunServerCategorySelection, Code: input.Code}
	default:
		return runserverInternalFailure()
	}
}

func mapRunserverWorkspaceFailure(input Failure) RunServerFailure {
	if input.Code == RunServerCodeProjectTemporaryStorageFailed {
		return RunServerFailure{Category: RunServerCategoryBuild, Code: input.Code}
	}
	return runserverInternalFailure()
}

func runserverShortProcessFailure(process ProcessResult, category, code string) *RunServerFailure {
	if process.Failure != nil {
		if process.Failure.Category != protocol.CategoryProcess {
			candidate := runserverInternalFailure()
			return &candidate
		}
		switch process.Failure.Code {
		case protocol.CodeProjectCanceled:
			candidate := RunServerFailure{Category: RunServerCategoryProcess, Code: RunServerCodeProjectCanceled}
			return &candidate
		case protocol.CodeProjectInterrupted:
			candidate := RunServerFailure{Category: RunServerCategoryProcess, Code: RunServerCodeProjectInterrupted}
			return &candidate
		case protocol.CodeProjectCleanupFailed:
			candidate := runserverCleanupFailure()
			return &candidate
		default:
			candidate := runserverInternalFailure()
			return &candidate
		}
	}
	if process.Started && process.ExitCode == 0 {
		return nil
	}
	candidate := RunServerFailure{Category: category, Code: code}
	return &candidate
}

func runserverBarrier(input RunServerInvocation, primary *RunServerFailure) *RunServerFailure {
	if primary != nil && primary.Category == RunServerCategoryProcess && primary.Code == RunServerCodeProjectCleanupFailed {
		return primary
	}
	if input.Interrupt != nil {
		select {
		case <-input.Interrupt:
			candidate := RunServerFailure{Category: RunServerCategoryProcess, Code: RunServerCodeProjectInterrupted}
			return &candidate
		default:
		}
	}
	if input.Context != nil && input.Context.Err() != nil {
		candidate := RunServerFailure{Category: RunServerCategoryProcess, Code: RunServerCodeProjectCanceled}
		return &candidate
	}
	return primary
}

func combineRunserverCleanup(primary *RunServerFailure, failed bool) *RunServerFailure {
	if !failed {
		return primary
	}
	if primary == nil || runserverCanceledOrInterrupted(*primary) {
		candidate := runserverCleanupFailure()
		return &candidate
	}
	return primary
}

func runserverCanceledOrInterrupted(input RunServerFailure) bool {
	return input.Category == RunServerCategoryProcess &&
		(input.Code == RunServerCodeProjectCanceled || input.Code == RunServerCodeProjectInterrupted)
}

func runserverCleanupFailure() RunServerFailure {
	return RunServerFailure{Category: RunServerCategoryProcess, Code: RunServerCodeProjectCleanupFailed}
}

func runserverInternalFailure() RunServerFailure {
	return RunServerFailure{Category: RunServerCategoryInternal, Code: RunServerCodeProjectInternalError}
}

func recordRunserverProcess(report *RunServerReport, process runserverProcessResult) {
	report.DirectChildReaps += process.DirectReaps
	report.GroupSIGINTAttempts += process.SIGINTAttempts
	report.GroupSIGKILLAttempts += process.SIGKILLAttempts
	if process.CleanupFailed {
		report.CleanupFailed = 1
	}
}

func classifyRunserverProcess(process runserverProcessResult) *RunServerFailure {
	if process.StdoutError != nil || process.StderrError != nil {
		candidate := RunServerFailure{Category: RunServerCategoryRuntime, Code: RunServerCodeRuntimeStreamFailed}
		return &candidate
	}
	if process.CleanupFailed {
		candidate := runserverCleanupFailure()
		return &candidate
	}
	if process.Canceled {
		candidate := RunServerFailure{Category: RunServerCategoryProcess, Code: RunServerCodeProjectCanceled}
		return &candidate
	}
	if process.Interrupted {
		if process.Started && !process.Forced && process.ExitCode == 0 && process.WaitError == nil {
			return nil
		}
		candidate := RunServerFailure{Category: RunServerCategoryProcess, Code: RunServerCodeProjectInterrupted}
		return &candidate
	}
	if process.StartError != nil || !process.Started {
		candidate := RunServerFailure{Category: RunServerCategoryRuntime, Code: RunServerCodeRuntimeStartFailed}
		return &candidate
	}
	candidate := RunServerFailure{Category: RunServerCategoryRuntime, Code: RunServerCodeRuntimeExited}
	return &candidate
}

func chooseRunserverFailure(report *RunServerReport, primary RunServerFailure) {
	if report.HasRunServerFailure || report.HasRunServerResult {
		return
	}
	if _, ok := runserverExitCode(primary); !ok {
		primary = runserverInternalFailure()
	}
	report.HasRunServerFailure = true
	report.RunServerFailure = primary
}

func chooseRunserverResult(report *RunServerReport, result RunServerResult) {
	if report.HasRunServerFailure || report.HasRunServerResult {
		return
	}
	report.HasRunServerResult = true
	report.RunServerResult = result
}

func runserverExitCode(failure RunServerFailure) (int, bool) {
	switch failure.Category {
	case RunServerCategoryCommand:
		return exactGenerationCode(failure.Code, 2, RunServerCodeInvalidArguments)
	case RunServerCategorySelection:
		switch failure.Code {
		case RunServerCodeProjectNotFound, RunServerCodeProjectSearchLimitExceeded,
			RunServerCodeInvalidProjectDescriptor, RunServerCodeProjectDescriptorIncompatible:
			return 2, true
		case RunServerCodeProjectSelectionFailed:
			return 3, true
		}
	case RunServerCategoryConfiguration:
		return exactGenerationCode(failure.Code, 2, RunServerCodeNotConfigured)
	case RunServerCategoryBuild:
		return exactGenerationCode(failure.Code, 3, RunServerCodeProjectTemporaryStorageFailed,
			RunServerCodeProjectBuildFailed, RunServerCodeRuntimeBuildFailed)
	case projectgenerateprotocol.CategoryProtocol:
		return exactGenerationCode(failure.Code, 3, projectgenerateprotocol.CodeInvalidRequest, projectgenerateprotocol.CodeInvalidResponse,
			projectgenerateprotocol.CodeRunnerFailed, projectgenerateprotocol.CodeProtocolIncompatible)
	case projectgenerateprotocol.CategoryDeclaration:
		return exactGenerationCode(failure.Code, 1, projectgenerateprotocol.CodeProjectSpecLoadFailed)
	case RunServerCategoryGeneration:
		return exactGenerationCode(failure.Code, 1, RunServerCodeProjectGenerateFailed,
			RunServerCodeGeneratedBundleStale, RunServerCodeProjectCheckFailed)
	case RunServerCategoryRuntime:
		return exactGenerationCode(failure.Code, 3, RunServerCodeRuntimeStartFailed,
			RunServerCodeRuntimeExited, RunServerCodeRuntimeStreamFailed)
	case RunServerCategoryProcess:
		switch failure.Code {
		case RunServerCodeProjectCanceled, RunServerCodeProjectCleanupFailed:
			return 3, true
		case RunServerCodeProjectInterrupted:
			return 130, true
		}
	case RunServerCategoryInternal:
		return exactGenerationCode(failure.Code, 3, RunServerCodeProjectInternalError)
	}
	return 0, false
}

func publishRunserver(input RunServerInvocation, report *RunServerReport) {
	if !report.HasRunServerFailure && !report.HasRunServerResult {
		chooseRunserverFailure(report, runserverInternalFailure())
	}
	if report.HasRunServerFailure {
		exit, ok := runserverExitCode(report.RunServerFailure)
		if !ok {
			report.RunServerFailure = runserverInternalFailure()
			exit = 3
		}
		report.ExitCode = exit
		report.UserStderrWrites++
		if input.Stderr != nil {
			_, _ = writeOnce(input.Stderr, publicFailureDocument(&report.Report, report.RunServerFailure.Category, report.RunServerFailure.Code))
		}
		return
	}
	report.ExitCode = 0
}
