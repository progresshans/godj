//go:build darwin || linux

package projectcheck

import (
	"encoding/json"
	"errors"
	"github.com/progresshans/godj/internal/projectcheck/failurecode"
	"sort"

	"github.com/progresshans/godj/internal/gobuild"
	"github.com/progresshans/godj/internal/projectcheck/protocol"
	"github.com/progresshans/godj/internal/projectgenerate"
	projectgenerateprotocol "github.com/progresshans/godj/internal/projectgenerate/protocol"
)

// RunGenerate executes the separate global project-generation flow. Argument
// validation precedes project/cwd selection and no migration wire type carries
// a ProjectSpec.
func RunGenerate(input GenerationInvocation) GenerationReport {
	input.Args = append([]string(nil), input.Args...)
	arguments, primary := parseGenerationArguments(input.Args)
	input.Context, input.Environment, input.Backend = normalizeCommandInput(input.Context, input.Environment, input.Backend)
	input.generation = completeGenerationHooks(input.generation)

	report := GenerationReport{}
	if terminal := generationBarrier(input, primary); terminal != nil {
		chooseGenerationFailure(&report, *terminal)
		publishGeneration(input, &report)
		return report
	}

	command := newProjectCommand(input.Context, input.Interrupt, input.Backend, &report.Report,
		commandPolicy[GenerationFailure]{
			selection: mapGenerationSelectionFailure, workspace: mapGenerationWorkspaceFailure,
			process: generationProcessFailure, cleanup: combineGenerationCleanup,
			barrier: func(primary *GenerationFailure) *GenerationFailure { return generationBarrier(input, primary) },
		}, commandHooks{})
	finish := func() GenerationReport {
		if command.close() && (!report.HasGenerationFailure || generationCanceledOrInterrupted(report.GenerationFailure)) {
			report.HasGenerationResult = false
			report.GenerationResult = GenerationResult{}
			report.HasGenerationFailure = true
			report.GenerationFailure = generationCleanupFailure()
		}
		publishGeneration(input, &report)
		return report
	}
	if primary = command.open(input.CWD, arguments.explicitDescriptor, input.Environment, input.workspace); primary != nil {
		chooseGenerationFailure(&report, *primary)
		return finish()
	}
	if primary = command.buildRunner(); primary != nil {
		chooseGenerationFailure(&report, *primary)
		return finish()
	}
	selected := command.selected
	runner := command.run(GenerationRunnerStage, projectgenerateprotocol.PrivateArgument, projectgenerateprotocol.RequestDocument())
	response, primary := parseCommandResponse(&runner, command.completed(GenerationRunnerStage, runner),
		GenerationFailure{Category: GenerationCategoryProtocol, Code: projectgenerateprotocol.CodeInvalidResponse},
		func(document []byte, transportOK bool) (projectgenerateprotocol.Response, GenerationFailure, bool) {
			response, failed, hasFailure := projectgenerateprotocol.ParseResponse(document, transportOK)
			return response, GenerationFailure{Category: failed.Category, Code: failed.Code}, hasFailure
		})
	primary = generationBarrier(input, primary)
	if primary != nil {
		chooseGenerationFailure(&report, *primary)
		return finish()
	}
	if !response.OK {
		chooseGenerationFailure(&report, GenerationFailure{Category: response.Failure.Category, Code: response.Failure.Code})
		return finish()
	}
	if !verifyRetainedProject(selected) {
		chooseGenerationFailure(&report, GenerationFailure{Category: GenerationCategorySelection, Code: GenerationCodeProjectSelectionFailed})
		return finish()
	}
	if terminal := generationBarrier(input, nil); terminal != nil {
		chooseGenerationFailure(&report, *terminal)
		return finish()
	}

	bundle, err := input.generation.generate(response.ProjectSpec)
	if err != nil {
		candidate := GenerationFailure{Category: GenerationCategoryGeneration, Code: GenerationCodeProjectGenerateFailed}
		if terminal := generationBarrier(input, &candidate); terminal != nil {
			chooseGenerationFailure(&report, *terminal)
		}
		return finish()
	}
	if !verifyRetainedProject(selected) {
		chooseGenerationFailure(&report, GenerationFailure{Category: GenerationCategorySelection, Code: GenerationCodeProjectSelectionFailed})
		return finish()
	}
	root, err := input.generation.sealRoot(selected.rootPath, uint64(selected.rootIdentity.Dev), uint64(selected.rootIdentity.Ino))
	if err != nil {
		chooseGenerationFailure(&report, GenerationFailure{Category: GenerationCategorySelection, Code: GenerationCodeProjectSelectionFailed})
		return finish()
	}
	result := GenerationResult{SnapshotSHA256: bundle.SnapshotSHA256(), FileCount: len(bundle.Files())}
	if arguments.check {
		checked, checkErr := input.generation.check(input.Context, root, bundle)
		result.Status = "clean"
		result.ActualSnapshotSHA256 = checked.ActualSnapshotSHA256
		result.Interrupted = checked.Interrupted
		result.Drifts = cloneAndSortGenerationDrifts(checked.Drifts)
		if terminal := generationBarrier(input, nil); terminal != nil {
			chooseGenerationFailure(&report, *terminal)
			return finish()
		}
		if !verifyRetainedProject(selected) {
			chooseGenerationFailure(&report, GenerationFailure{Category: GenerationCategorySelection, Code: GenerationCodeProjectSelectionFailed})
			return finish()
		}
		if errors.Is(checkErr, projectgenerate.ErrGeneratedDrift) || !checked.Clean() {
			result.Status = "drift"
			chooseGenerationResult(&report, result)
			return finish()
		}
		if checkErr != nil {
			chooseGenerationFailure(&report, GenerationFailure{Category: GenerationCategoryGeneration, Code: GenerationCodeProjectCheckFailed})
			return finish()
		}
		chooseGenerationResult(&report, result)
		return finish()
	}

	verifier, err := input.generation.newVerifier(root, bundle)
	if err != nil {
		report.BuildDiagnostic = gobuild.Diagnostic(err)
		if terminal := generationBarrier(input, nil); terminal != nil {
			chooseGenerationFailure(&report, *terminal)
		} else {
			chooseGenerationFailure(&report, classifyGenerationError(err, true))
		}
		return finish()
	}
	if !verifyRetainedProject(selected) {
		chooseGenerationFailure(&report, GenerationFailure{Category: GenerationCategorySelection, Code: GenerationCodeProjectSelectionFailed})
		return finish()
	}
	if err := input.generation.publish(input.Context, root, bundle, verifier); err != nil {
		report.BuildDiagnostic = gobuild.Diagnostic(err)
		if terminal := generationBarrier(input, nil); terminal != nil && !errors.Is(err, projectgenerate.ErrPublicationRecoveryRequired) {
			chooseGenerationFailure(&report, *terminal)
		} else {
			chooseGenerationFailure(&report, classifyGenerationError(err, false))
		}
		return finish()
	}
	// Publish owns its post-commit cancellation semantics. Do not reclassify a
	// successful durable commit merely because the caller canceled during its
	// non-cancelable cleanup phase.
	result.Status = "generated"
	chooseGenerationResult(&report, result)
	return finish()
}

func completeGenerationHooks(hooks generationHooks) generationHooks {
	defaults := defaultGenerationHooks()
	if hooks.generate == nil {
		hooks.generate = defaults.generate
	}
	if hooks.sealRoot == nil {
		hooks.sealRoot = defaults.sealRoot
	}
	if hooks.check == nil {
		hooks.check = defaults.check
	}
	if hooks.newVerifier == nil {
		hooks.newVerifier = defaults.newVerifier
	}
	if hooks.publish == nil {
		hooks.publish = defaults.publish
	}
	return hooks
}

func mapGenerationSelectionFailure(input Failure) GenerationFailure {
	switch input.Code {
	case GenerationCodeProjectNotFound, GenerationCodeProjectSearchLimitExceeded,
		GenerationCodeInvalidProjectDescriptor, GenerationCodeProjectDescriptorIncompatible,
		GenerationCodeProjectSelectionFailed:
		return GenerationFailure{Category: GenerationCategorySelection, Code: input.Code}
	default:
		return generationInternalFailure()
	}
}

func mapGenerationWorkspaceFailure(input Failure) GenerationFailure {
	if input.Code == GenerationCodeProjectTemporaryStorageFailed {
		return GenerationFailure{Category: GenerationCategoryBuild, Code: input.Code}
	}
	return generationInternalFailure()
}

func generationProcessFailure(stage ProcessStage, process ProcessResult) *GenerationFailure {
	if process.Failure != nil {
		if process.Failure.Category != protocol.CategoryProcess {
			candidate := generationInternalFailure()
			return &candidate
		}
		switch process.Failure.Code {
		case protocol.CodeProjectCanceled:
			candidate := GenerationFailure{Category: GenerationCategoryProcess, Code: GenerationCodeProjectCanceled}
			return &candidate
		case protocol.CodeProjectInterrupted:
			candidate := GenerationFailure{Category: GenerationCategoryProcess, Code: GenerationCodeProjectInterrupted}
			return &candidate
		case protocol.CodeProjectCleanupFailed:
			candidate := generationCleanupFailure()
			return &candidate
		default:
			candidate := generationInternalFailure()
			return &candidate
		}
	}
	if process.Started && process.ExitCode == 0 {
		return nil
	}
	if stage == BuildStage {
		candidate := GenerationFailure{Category: GenerationCategoryBuild, Code: GenerationCodeProjectBuildFailed}
		return &candidate
	}
	candidate := GenerationFailure{Category: GenerationCategoryProtocol, Code: projectgenerateprotocol.CodeRunnerFailed}
	return &candidate
}

func generationBarrier(input GenerationInvocation, primary *GenerationFailure) *GenerationFailure {
	if primary != nil && primary.Category == GenerationCategoryProcess && primary.Code == GenerationCodeProjectCleanupFailed {
		return primary
	}
	if code := commandInterruption(input.Context, input.Interrupt); code != "" {
		candidate := GenerationFailure{Category: GenerationCategoryProcess, Code: code}
		return &candidate
	}
	return primary
}

func combineGenerationCleanup(primary *GenerationFailure, failed bool) *GenerationFailure {
	if !failed {
		return primary
	}
	if primary == nil || generationCanceledOrInterrupted(*primary) {
		candidate := generationCleanupFailure()
		return &candidate
	}
	return primary
}

func generationCanceledOrInterrupted(input GenerationFailure) bool {
	return input.Category == GenerationCategoryProcess && (input.Code == GenerationCodeProjectCanceled || input.Code == GenerationCodeProjectInterrupted)
}

func generationCleanupFailure() GenerationFailure {
	return GenerationFailure{Category: GenerationCategoryProcess, Code: GenerationCodeProjectCleanupFailed}
}

func generationInternalFailure() GenerationFailure {
	return GenerationFailure{Category: GenerationCategoryInternal, Code: GenerationCodeProjectInternalError}
}

func classifyGenerationError(err error, constructor bool) GenerationFailure {
	switch {
	case errors.Is(err, projectgenerate.ErrPublicationRecoveryRequired), errors.Is(err, projectgenerate.ErrPublicationInterrupted):
		return GenerationFailure{Category: GenerationCategoryGeneration, Code: GenerationCodePublicationRecoveryRequired}
	case errors.Is(err, projectgenerate.ErrCandidateVerification), constructor:
		return GenerationFailure{Category: GenerationCategoryGeneration, Code: GenerationCodeCandidateVerificationFailed}
	default:
		return GenerationFailure{Category: GenerationCategoryGeneration, Code: GenerationCodeProjectPublishFailed}
	}
}

func cloneAndSortGenerationDrifts(input []projectgenerate.Drift) []projectgenerate.Drift {
	result := append([]projectgenerate.Drift(nil), input...)
	sort.Slice(result, func(left, right int) bool {
		if result[left].Path != result[right].Path {
			return result[left].Path < result[right].Path
		}
		return result[left].Kind < result[right].Kind
	})
	return result
}

func chooseGenerationFailure(report *GenerationReport, primary GenerationFailure) {
	if report.HasGenerationFailure || report.HasGenerationResult {
		return
	}
	if _, ok := generationExitCode(primary); !ok {
		primary = generationInternalFailure()
	}
	report.HasGenerationFailure = true
	report.GenerationFailure = primary
}

func chooseGenerationResult(report *GenerationReport, result GenerationResult) {
	if report.HasGenerationFailure || report.HasGenerationResult {
		return
	}
	report.HasGenerationResult = true
	report.GenerationResult = result
}

func generationExitCode(failure GenerationFailure) (int, bool) {
	switch failure.Category {
	case GenerationCategoryCommand:
		return failurecode.Exact(failure.Code, 2, GenerationCodeInvalidArguments)
	case GenerationCategorySelection:
		return failurecode.Selection(failure.Code, 2)
	case GenerationCategoryBuild:
		return failurecode.Exact(failure.Code, 3, GenerationCodeProjectTemporaryStorageFailed, GenerationCodeProjectBuildFailed)
	case GenerationCategoryProtocol:
		return failurecode.Exact(failure.Code, 3,
			projectgenerateprotocol.CodeInvalidResponse, projectgenerateprotocol.CodeRunnerFailed,
			projectgenerateprotocol.CodeProtocolIncompatible)
	case GenerationCategoryDeclaration:
		return failurecode.Exact(failure.Code, 1, projectgenerateprotocol.CodeProjectSpecLoadFailed)
	case GenerationCategoryGeneration:
		switch failure.Code {
		case GenerationCodeProjectGenerateFailed, GenerationCodeProjectCheckFailed,
			GenerationCodeProjectPublishFailed, GenerationCodeCandidateVerificationFailed:
			return 1, true
		case GenerationCodePublicationRecoveryRequired:
			return 3, true
		default:
			return 0, false
		}
	case GenerationCategoryProcess:
		return failurecode.Process(failure.Code)
	case GenerationCategoryInternal:
		return failurecode.Exact(failure.Code, 3, GenerationCodeProjectInternalError)
	default:
		return 0, false
	}
}

func publishGeneration(input GenerationInvocation, report *GenerationReport) {
	if !report.HasGenerationFailure && !report.HasGenerationResult {
		chooseGenerationFailure(report, generationInternalFailure())
	}
	if report.HasGenerationFailure {
		exit, ok := generationExitCode(report.GenerationFailure)
		if !ok {
			report.GenerationFailure = generationInternalFailure()
			exit = 3
		}
		report.ExitCode = exit
		report.UserStderrWrites++
		if input.Stderr != nil {
			_, _ = writeOnce(input.Stderr, publicFailureDocument(&report.Report, report.GenerationFailure.Category, report.GenerationFailure.Code))
		}
		return
	}

	payload, err := encodeGenerationResult(report.GenerationResult)
	if err != nil {
		report.HasGenerationResult = false
		report.GenerationResult = GenerationResult{}
		report.HasGenerationFailure = true
		report.GenerationFailure = generationInternalFailure()
		publishGeneration(input, report)
		return
	}
	report.UserStdoutWrites++
	written, writeErr := writeOnce(input.Stdout, payload)
	if written > 0 && written < len(payload) {
		report.PartialStdoutWrites++
	}
	if writeErr == nil {
		if report.GenerationResult.Status == "drift" {
			report.ExitCode = 1
		} else {
			report.ExitCode = 0
		}
		return
	}
	report.HasGenerationResult = false
	report.GenerationResult = GenerationResult{}
	report.HasGenerationFailure = true
	report.GenerationFailure = generationInternalFailure()
	report.ExitCode = 3
}

func encodeGenerationResult(result GenerationResult) ([]byte, error) {
	var payload []byte
	var err error
	if result.Status == "drift" {
		type driftDocument struct {
			Path           string `json:"path"`
			Kind           string `json:"kind"`
			ExpectedSHA256 string `json:"expected_sha256"`
			ActualSHA256   string `json:"actual_sha256"`
		}
		drifts := make([]driftDocument, len(result.Drifts))
		for index, drift := range result.Drifts {
			drifts[index] = driftDocument{Path: drift.Path, Kind: string(drift.Kind), ExpectedSHA256: drift.ExpectedSHA256, ActualSHA256: drift.ActualSHA256}
		}
		payload, err = json.Marshal(struct {
			Status                 string          `json:"status"`
			ExpectedSnapshotSHA256 string          `json:"expected_snapshot_sha256"`
			ActualSnapshotSHA256   string          `json:"actual_snapshot_sha256"`
			Interrupted            bool            `json:"interrupted"`
			Drifts                 []driftDocument `json:"drifts"`
		}{
			Status:                 result.Status,
			ExpectedSnapshotSHA256: result.SnapshotSHA256,
			ActualSnapshotSHA256:   result.ActualSnapshotSHA256,
			Interrupted:            result.Interrupted,
			Drifts:                 drifts,
		})
	} else {
		payload, err = json.Marshal(struct {
			Status         string `json:"status"`
			SnapshotSHA256 string `json:"snapshot_sha256"`
			FileCount      int    `json:"file_count"`
		}{Status: result.Status, SnapshotSHA256: result.SnapshotSHA256, FileCount: result.FileCount})
	}
	if err != nil {
		return nil, err
	}
	return append(payload, '\n'), nil
}
