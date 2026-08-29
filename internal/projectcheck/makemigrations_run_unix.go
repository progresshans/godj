//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"reflect"

	"github.com/progresshans/godj/internal/projectcheck/protocol"
	"github.com/progresshans/godj/internal/projectmigration"
	writerprotocol "github.com/progresshans/godj/internal/projectmigration/protocol"
	"github.com/progresshans/godj/migrations/definition"
)

// RunMakemigrations executes the global, DB-free migration writer. The project
// child supplies immutable declaration/catalog snapshots; the global owner
// independently fingerprints build inputs, re-reads the filesystem catalog,
// serializes normal writers, freshly replans under the lock, and publishes a
// dependency-valid durable prefix with no-replace appends.
func RunMakemigrations(input MakemigrationsInvocation) MakemigrationsReport {
	input.Args = append([]string(nil), input.Args...)
	arguments, validArguments := parseMakemigrationsArguments(input.Args)
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

	report := MakemigrationsReport{}
	var primary *MakemigrationsFailure
	if !validArguments {
		candidate := MakemigrationsFailure{Category: MakemigrationsCategoryCommand, Code: MakemigrationsCodeInvalidArguments}
		primary = &candidate
	}
	if terminal := makemigrationsBarrier(input, primary); terminal != nil {
		chooseMakemigrationsFailure(&report, *terminal)
		publishMakemigrations(input, &report)
		return report
	}

	selected, selectionFailure := selectProject(
		input.CWD,
		commandArguments{explicitDescriptor: arguments.explicitDescriptor},
		&report.Report,
	)
	if selectionFailure != nil {
		candidate := mapMakemigrationsSelectionFailure(*selectionFailure)
		primary = &candidate
	}
	if primary == nil && !verifyRetainedProject(selected) {
		candidate := MakemigrationsFailure{Category: MakemigrationsCategorySelection, Code: MakemigrationsCodeProjectSelectionFailed}
		primary = &candidate
	}
	if terminal := makemigrationsBarrier(input, primary); terminal != nil {
		if selected.root != nil && selected.close() != nil {
			report.CleanupFailed = 1
			terminal = combineMakemigrationsCleanup(terminal, true)
		}
		chooseMakemigrationsFailure(&report, *terminal)
		publishMakemigrations(input, &report)
		return report
	}

	workspace, workspaceFailure := createPrivateWorkspaceWithHooks(selected, input.Environment, &report.Report, input.workspace)
	if workspaceFailure != nil {
		candidate := mapMakemigrationsWorkspaceFailure(*workspaceFailure)
		primary = &candidate
		terminal := makemigrationsBarrier(input, primary)
		if selected.close() != nil {
			report.CleanupFailed = 1
		}
		terminal = combineMakemigrationsCleanup(terminal, report.CleanupFailed != 0)
		chooseMakemigrationsFailure(&report, *terminal)
		publishMakemigrations(input, &report)
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
		if !report.HasMakemigrationsFailure || makemigrationsCanceledOrInterrupted(report.MakemigrationsFailure) {
			report.HasMakemigrationsResult = false
			report.MakemigrationsResult = MakemigrationsResult{}
			report.HasMakemigrationsFailure = true
			report.MakemigrationsFailure = makemigrationsCleanupFailure()
		}
	}
	finish := func() MakemigrationsReport {
		cleanup()
		applyMakemigrationsFinalBarrier(input, &report)
		publishMakemigrations(input, &report)
		return report
	}

	if terminal := makemigrationsBarrier(input, nil); terminal != nil {
		chooseMakemigrationsFailure(&report, *terminal)
		return finish()
	}
	if !verifyRetainedProject(selected) {
		chooseMakemigrationsFailure(&report, MakemigrationsFailure{Category: MakemigrationsCategorySelection, Code: MakemigrationsCodeProjectSelectionFailed})
		return finish()
	}

	baseline, primary := captureMakemigrationsBuildInput(input, selected, workspace.environment, &report)
	if primary = makemigrationsBarrier(input, primary); primary != nil {
		chooseMakemigrationsFailure(&report, *primary)
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
	recordMakemigrationsProcess(&report, BuildStage, build)
	clear(build.Stdout)
	build.Stdout = nil
	primary = makemigrationsProcessFailure(BuildStage, build)
	primary = makemigrationsBarrier(input, primary)
	primary = combineMakemigrationsCleanup(primary, build.CleanupFailed)
	if primary != nil {
		chooseMakemigrationsFailure(&report, *primary)
		return finish()
	}

	postBuild, primary := captureMakemigrationsBuildInput(input, selected, workspace.environment, &report)
	primary = normalizeMakemigrationsCASFailure(makemigrationsBarrier(input, primary))
	if primary != nil {
		chooseMakemigrationsFailure(&report, *primary)
		return finish()
	}
	if !equalMakemigrationsBuildInputFingerprint(baseline, postBuild) {
		chooseMakemigrationsFailure(&report, makemigrationsSourceConflict())
		return finish()
	}

	if !verifyRetainedProject(selected) {
		chooseMakemigrationsFailure(&report, makemigrationsSourceConflict())
		return finish()
	}
	runnerCommand := Command{
		Dir:   selected.rootPath,
		Argv:  []string{filepath.Join(workspace.root, "godj-project-runner"), writerprotocol.PrivateArgument},
		Env:   workspace.environment,
		Stdin: writerprotocol.RequestDocument(),
	}
	response, primary := executeMakemigrationsRunner(input, runnerCommand, &report)
	if primary != nil {
		chooseMakemigrationsFailure(&report, *primary)
		return finish()
	}

	independent, primary := independentlyVerifyMakemigrationsResponse(input, selected, response, &report)
	clearMakemigrationsProtocolResult(&response)
	if primary = makemigrationsBarrier(input, primary); primary != nil {
		chooseMakemigrationsFailure(&report, *primary)
		return finish()
	}

	finalBuildInput, primary := captureMakemigrationsBuildInput(input, selected, workspace.environment, &report)
	primary = normalizeMakemigrationsCASFailure(makemigrationsBarrier(input, primary))
	if primary != nil {
		chooseMakemigrationsFailure(&report, *primary)
		return finish()
	}
	if !equalMakemigrationsBuildInputFingerprint(baseline, finalBuildInput) {
		chooseMakemigrationsFailure(&report, makemigrationsSourceConflict())
		return finish()
	}
	if primary := verifyFinalMakemigrationsFilesystemCatalog(input, selected, independent, &report); primary != nil {
		chooseMakemigrationsFailure(&report, *primary)
		return finish()
	}
	if arguments.mode != makemigrationsModeNormal {
		if primary := diagnoseMakemigrationsRecovery(input, selected, independent); primary != nil {
			chooseMakemigrationsFailure(&report, *primary)
			return finish()
		}
		if primary := preflightMakemigrationsPhysicalPublication(input, selected, baseline, independent, &report); primary != nil {
			chooseMakemigrationsFailure(&report, *primary)
			return finish()
		}
		chooseMakemigrationsResult(&report, makeMakemigrationsResult(independent))
		return finish()
	}

	fresh, primary := publishMakemigrationsNormal(
		input, selected, workspace.environment, runnerCommand, baseline, independent, &report,
	)
	if primary != nil {
		chooseMakemigrationsFailure(&report, *primary)
		return finish()
	}
	result := makeMakemigrationsResult(fresh)
	if result.CandidateCount != 0 {
		result.Status = "generated"
	}
	chooseMakemigrationsResult(&report, result)
	return finish()
}

func executeMakemigrationsRunner(
	input MakemigrationsInvocation,
	command Command,
	report *MakemigrationsReport,
) (writerprotocol.Result, *MakemigrationsFailure) {
	if terminal := makemigrationsBarrier(input, nil); terminal != nil {
		return writerprotocol.Result{}, terminal
	}
	report.RunnerCalls++
	runner := input.Backend.Execute(input.Context, input.Interrupt, MakemigrationsRunnerStage, cloneCommand(command))
	recordMakemigrationsProcess(report, MakemigrationsRunnerStage, runner)
	primary := makemigrationsProcessFailure(MakemigrationsRunnerStage, runner)
	primary = combineMakemigrationsCleanup(primary, runner.CleanupFailed)
	var response writerprotocol.Response
	if primary == nil {
		if runner.StdoutScalar.Truncated {
			candidate := MakemigrationsFailure{Category: writerprotocol.CategoryProtocol, Code: writerprotocol.CodeInvalidResponse}
			primary = &candidate
		} else {
			parsed, parseFailure, failed := writerprotocol.ParseResponse(runner.Stdout, runner.Started && runner.ExitCode == 0)
			if failed {
				candidate := MakemigrationsFailure{Category: parseFailure.Category, Code: parseFailure.Code}
				primary = &candidate
			} else {
				response = parsed
			}
		}
	}
	clear(runner.Stdout)
	runner.Stdout = nil
	primary = makemigrationsBarrier(input, primary)
	if primary != nil {
		clearMakemigrationsProtocolResult(&response.Result)
		return writerprotocol.Result{}, primary
	}
	if !response.OK {
		candidate := MakemigrationsFailure{Category: response.Failure.Category, Code: response.Failure.Code}
		return writerprotocol.Result{}, &candidate
	}
	return response.Result, nil
}

func verifyFinalMakemigrationsFilesystemCatalog(
	input MakemigrationsInvocation,
	selected retainedProject,
	snapshot projectmigration.Snapshot,
	report *MakemigrationsReport,
) *MakemigrationsFailure {
	if terminal := makemigrationsBarrier(input, nil); terminal != nil {
		return terminal
	}
	report.IndependentCatalogSnapshots++
	current, err := captureMakemigrationsFilesystemCatalog(input.Context, selected, snapshot.WriterRoot())
	want := snapshot.FilesystemSources()
	matches := err == nil && equalDefinitionSources(want, current)
	clearDefinitionSources(want)
	clearDefinitionSources(current)
	if input.afterFinalCatalogSnapshot != nil {
		input.afterFinalCatalogSnapshot()
	}
	if terminal := makemigrationsBarrier(input, nil); terminal != nil {
		return terminal
	}
	if matches {
		return nil
	}
	if terminal := makemigrationsBarrier(input, nil); terminal != nil {
		return terminal
	}
	candidate := makemigrationsSourceConflict()
	return &candidate
}

func captureMakemigrationsBuildInput(
	input MakemigrationsInvocation,
	selected retainedProject,
	environment []string,
	report *MakemigrationsReport,
) (makemigrationsBuildInputFingerprint, *MakemigrationsFailure) {
	if !verifyRetainedProject(selected) {
		candidate := MakemigrationsFailure{Category: MakemigrationsCategorySelection, Code: MakemigrationsCodeProjectSelectionFailed}
		return makemigrationsBuildInputFingerprint{}, &candidate
	}
	if terminal := makemigrationsBarrier(input, nil); terminal != nil {
		return makemigrationsBuildInputFingerprint{}, terminal
	}
	command := Command{
		Dir:  selected.rootPath,
		Argv: []string{"go", "list", "-deps", "-json", "-mod=readonly", selected.descriptor.packagePath},
		Env:  environment,
	}
	report.InventoryCalls++
	process := input.Backend.Execute(input.Context, input.Interrupt, MakemigrationsInventoryStage, cloneCommand(command))
	recordMakemigrationsProcess(report, MakemigrationsInventoryStage, process)
	primary := makemigrationsProcessFailure(MakemigrationsInventoryStage, process)
	primary = combineMakemigrationsCleanup(primary, process.CleanupFailed)
	if primary == nil && process.StdoutScalar.Truncated {
		candidate := MakemigrationsFailure{Category: MakemigrationsCategoryBuild, Code: MakemigrationsCodeProjectInventoryFailed}
		primary = &candidate
	}
	if primary != nil {
		clear(process.Stdout)
		return makemigrationsBuildInputFingerprint{}, primary
	}
	fingerprint, err := computeMakemigrationsBuildInputFingerprint(selected, process.Stdout)
	clear(process.Stdout)
	if err != nil {
		candidate := MakemigrationsFailure{Category: MakemigrationsCategorySource, Code: MakemigrationsCodeSourceFingerprintFailed}
		return makemigrationsBuildInputFingerprint{}, &candidate
	}
	return fingerprint, nil
}

func independentlyVerifyMakemigrationsResponse(
	input MakemigrationsInvocation,
	selected retainedProject,
	response writerprotocol.Result,
	report *MakemigrationsReport,
) (projectmigration.Snapshot, *MakemigrationsFailure) {
	if !verifyRetainedProject(selected) {
		candidate := makemigrationsSourceConflict()
		return projectmigration.Snapshot{}, &candidate
	}
	if terminal := makemigrationsBarrier(input, nil); terminal != nil {
		return projectmigration.Snapshot{}, terminal
	}

	report.IndependentCatalogSnapshots++
	first, err := captureMakemigrationsFilesystemCatalog(input.Context, selected, response.WriterRoot)
	if err != nil {
		if terminal := makemigrationsBarrier(input, nil); terminal != nil {
			return projectmigration.Snapshot{}, terminal
		}
		candidate := makemigrationsSourceConflict()
		return projectmigration.Snapshot{}, &candidate
	}
	programmatic := make([]definition.Source, len(response.ProgrammaticCatalog.Sources))
	for index := range response.ProgrammaticCatalog.Sources {
		programmatic[index] = definition.Source{
			SourceID: response.ProgrammaticCatalog.Sources[index].SourceID,
			Document: append([]byte(nil), response.ProgrammaticCatalog.Sources[index].Document...),
		}
	}
	snapshot, err := projectmigration.BuildSnapshot(projectmigration.Request{
		ProjectSpec:         response.ProjectSpec,
		FilesystemSources:   first,
		ProgrammaticSources: programmatic,
		WriterRoot:          response.WriterRoot,
	})
	clearDefinitionSources(programmatic)
	if err != nil || !makemigrationsResponseMatchesSnapshot(response, snapshot) {
		clearDefinitionSources(first)
		candidate := makemigrationsSourceConflict()
		return projectmigration.Snapshot{}, &candidate
	}

	report.IndependentCatalogSnapshots++
	second, err := captureMakemigrationsFilesystemCatalog(input.Context, selected, response.WriterRoot)
	if err != nil || !equalDefinitionSources(first, second) {
		clearDefinitionSources(first)
		clearDefinitionSources(second)
		if terminal := makemigrationsBarrier(input, nil); terminal != nil {
			return projectmigration.Snapshot{}, terminal
		}
		candidate := makemigrationsSourceConflict()
		return projectmigration.Snapshot{}, &candidate
	}
	clearDefinitionSources(first)
	clearDefinitionSources(second)
	return snapshot, nil
}

func makemigrationsResponseMatchesSnapshot(response writerprotocol.Result, snapshot projectmigration.Snapshot) bool {
	if !snapshot.Initialized() || response.WriterRoot != snapshot.WriterRoot() ||
		!reflect.DeepEqual(response.ProjectSpec, snapshot.ProjectSpec()) ||
		response.ProjectSpecDigest != snapshot.ProjectSpecDigest() ||
		response.ProjectSnapshotSHA256 != snapshot.GeneratedBundleSnapshotSHA256() ||
		response.DefinitionSetDigest != snapshot.ExistingSemanticDigest() {
		return false
	}
	filesystem := snapshot.FilesystemSources()
	programmatic := snapshot.ProgrammaticSources()
	if response.FilesystemCatalog.SourceCount != len(filesystem) ||
		response.FilesystemCatalog.DocumentBytes != definitionDocumentBytes(filesystem) ||
		response.FilesystemCatalog.Digest != snapshot.FilesystemCatalogDigest() ||
		response.ProgrammaticCatalog.SourceCount != len(programmatic) ||
		response.ProgrammaticCatalog.DocumentBytes != definitionDocumentBytes(programmatic) ||
		response.ProgrammaticCatalog.Digest != snapshot.ProgrammaticCatalogDigest() ||
		!equalProtocolAndDefinitionSources(response.ProgrammaticCatalog.Sources, programmatic) {
		clearDefinitionSources(filesystem)
		clearDefinitionSources(programmatic)
		return false
	}
	clearDefinitionSources(filesystem)
	clearDefinitionSources(programmatic)
	candidates := snapshot.Candidates()
	if len(response.Candidates) != len(candidates) {
		return false
	}
	for index := range candidates {
		document := candidates[index].Document()
		matches := bytes.Equal(response.Candidates[index].Document, document)
		clear(document)
		if response.Candidates[index].App != candidates[index].App() ||
			response.Candidates[index].Name != candidates[index].Name() || !matches {
			return false
		}
	}
	return true
}

func makeMakemigrationsResult(snapshot projectmigration.Snapshot) MakemigrationsResult {
	candidates := snapshot.Candidates()
	result := MakemigrationsResult{
		Status:         "clean",
		CandidateCount: len(candidates),
		Candidates:     make([]MakemigrationsCandidate, len(candidates)),
	}
	if len(candidates) != 0 {
		result.Status = "pending"
	}
	for index := range candidates {
		basename, err := writerprotocol.CandidateTargetBasename(candidates[index].App(), candidates[index].Name())
		if err != nil {
			return MakemigrationsResult{}
		}
		relative := basename
		if snapshot.WriterRoot() != "." {
			relative = path.Join(snapshot.WriterRoot(), basename)
		}
		document := candidates[index].Document()
		digest := sha256.Sum256(document)
		clear(document)
		result.Candidates[index] = MakemigrationsCandidate{
			App:      candidates[index].App(),
			Name:     candidates[index].Name(),
			Path:     relative,
			SourceID: relative,
			SHA256:   hex.EncodeToString(digest[:]),
		}
	}
	return result
}

func makemigrationsProcessFailure(stage ProcessStage, process ProcessResult) *MakemigrationsFailure {
	if process.Failure != nil {
		if process.Failure.Category != protocol.CategoryProcess {
			candidate := makemigrationsInternalFailure()
			return &candidate
		}
		switch process.Failure.Code {
		case protocol.CodeProjectCanceled:
			candidate := MakemigrationsFailure{Category: MakemigrationsCategoryProcess, Code: MakemigrationsCodeProjectCanceled}
			return &candidate
		case protocol.CodeProjectInterrupted:
			candidate := MakemigrationsFailure{Category: MakemigrationsCategoryProcess, Code: MakemigrationsCodeProjectInterrupted}
			return &candidate
		case protocol.CodeProjectCleanupFailed:
			candidate := makemigrationsCleanupFailure()
			return &candidate
		default:
			candidate := makemigrationsInternalFailure()
			return &candidate
		}
	}
	if process.Started && process.ExitCode == 0 {
		return nil
	}
	var candidate MakemigrationsFailure
	switch stage {
	case MakemigrationsInventoryStage:
		candidate = MakemigrationsFailure{Category: MakemigrationsCategoryBuild, Code: MakemigrationsCodeProjectInventoryFailed}
	case BuildStage:
		candidate = MakemigrationsFailure{Category: MakemigrationsCategoryBuild, Code: MakemigrationsCodeProjectBuildFailed}
	case MakemigrationsRunnerStage:
		candidate = MakemigrationsFailure{Category: writerprotocol.CategoryProtocol, Code: writerprotocol.CodeRunnerFailed}
	default:
		candidate = makemigrationsInternalFailure()
	}
	return &candidate
}

func recordMakemigrationsProcess(report *MakemigrationsReport, stage ProcessStage, process ProcessResult) {
	report.DirectChildReaps += process.DirectReaps
	report.GroupSIGINTAttempts += process.SIGINTAttempts
	report.GroupSIGKILLAttempts += process.SIGKILLAttempts
	if process.CleanupFailed {
		report.CleanupFailed = 1
	}
	report.RawDiagnosticsDiscarded = true
	switch stage {
	case BuildStage:
		report.BuildStdoutRetainedBytes = process.StdoutScalar.RetainedBytes
		report.BuildStdoutTruncated = process.StdoutScalar.Truncated
		report.BuildStderrRetainedBytes = process.StderrScalar.RetainedBytes
		report.BuildStderrTruncated = process.StderrScalar.Truncated
	case MakemigrationsRunnerStage:
		report.RunnerStderrRetainedBytes = process.StderrScalar.RetainedBytes
		report.RunnerStderrTruncated = process.StderrScalar.Truncated
		if process.StdoutScalar.RetainedBytes != 0 || process.StdoutScalar.Truncated {
			report.RunnerResponseWrites++
		}
	}
}

func mapMakemigrationsSelectionFailure(input Failure) MakemigrationsFailure {
	switch input.Code {
	case MakemigrationsCodeProjectNotFound, MakemigrationsCodeProjectSearchLimitExceeded,
		MakemigrationsCodeInvalidProjectDescriptor, MakemigrationsCodeProjectDescriptorIncompatible,
		MakemigrationsCodeProjectSelectionFailed:
		return MakemigrationsFailure{Category: MakemigrationsCategorySelection, Code: input.Code}
	default:
		return makemigrationsInternalFailure()
	}
}

func mapMakemigrationsWorkspaceFailure(input Failure) MakemigrationsFailure {
	if input.Code == MakemigrationsCodeProjectTemporaryStorageFailed {
		return MakemigrationsFailure{Category: MakemigrationsCategoryBuild, Code: input.Code}
	}
	return makemigrationsInternalFailure()
}

func makemigrationsBarrier(input MakemigrationsInvocation, primary *MakemigrationsFailure) *MakemigrationsFailure {
	if primary != nil && primary.Category == MakemigrationsCategoryProcess && primary.Code == MakemigrationsCodeProjectCleanupFailed {
		return primary
	}
	if input.Interrupt != nil {
		select {
		case <-input.Interrupt:
			candidate := MakemigrationsFailure{Category: MakemigrationsCategoryProcess, Code: MakemigrationsCodeProjectInterrupted}
			return &candidate
		default:
		}
	}
	if input.Context != nil && input.Context.Err() != nil {
		candidate := MakemigrationsFailure{Category: MakemigrationsCategoryProcess, Code: MakemigrationsCodeProjectCanceled}
		return &candidate
	}
	return primary
}

func combineMakemigrationsCleanup(primary *MakemigrationsFailure, failed bool) *MakemigrationsFailure {
	if !failed {
		return primary
	}
	if primary == nil || makemigrationsCanceledOrInterrupted(*primary) {
		candidate := makemigrationsCleanupFailure()
		return &candidate
	}
	return primary
}

func makemigrationsCanceledOrInterrupted(input MakemigrationsFailure) bool {
	return input.Category == MakemigrationsCategoryProcess &&
		(input.Code == MakemigrationsCodeProjectCanceled || input.Code == MakemigrationsCodeProjectInterrupted)
}

func makemigrationsCleanupFailure() MakemigrationsFailure {
	return MakemigrationsFailure{Category: MakemigrationsCategoryProcess, Code: MakemigrationsCodeProjectCleanupFailed}
}

func makemigrationsSourceConflict() MakemigrationsFailure {
	return MakemigrationsFailure{Category: MakemigrationsCategorySource, Code: MakemigrationsCodeSourceConflict}
}

func normalizeMakemigrationsCASFailure(primary *MakemigrationsFailure) *MakemigrationsFailure {
	if primary == nil || primary.Category == MakemigrationsCategoryProcess || primary.Category == MakemigrationsCategoryInternal {
		return primary
	}
	candidate := makemigrationsSourceConflict()
	return &candidate
}

func applyMakemigrationsFinalBarrier(input MakemigrationsInvocation, report *MakemigrationsReport) {
	// A successful per-file directory fsync is the migration publication commit
	// point. A late cancellation cannot reclassify that durable outcome.
	if report.PublishedCandidates != 0 {
		return
	}
	if report.HasMakemigrationsFailure && !makemigrationsCanceledOrInterrupted(report.MakemigrationsFailure) {
		return
	}
	var existing *MakemigrationsFailure
	if report.HasMakemigrationsFailure {
		candidate := report.MakemigrationsFailure
		existing = &candidate
	}
	terminal := makemigrationsBarrier(input, existing)
	if terminal == nil || (existing != nil && *terminal == *existing) {
		return
	}
	report.HasMakemigrationsResult = false
	report.MakemigrationsResult = MakemigrationsResult{}
	report.HasMakemigrationsFailure = false
	report.MakemigrationsFailure = MakemigrationsFailure{}
	chooseMakemigrationsFailure(report, *terminal)
}

func makemigrationsInternalFailure() MakemigrationsFailure {
	return MakemigrationsFailure{Category: MakemigrationsCategoryInternal, Code: MakemigrationsCodeProjectInternalError}
}

func chooseMakemigrationsFailure(report *MakemigrationsReport, primary MakemigrationsFailure) {
	if report.HasMakemigrationsFailure || report.HasMakemigrationsResult {
		return
	}
	if _, ok := makemigrationsExitCode(primary); !ok {
		primary = makemigrationsInternalFailure()
	}
	report.HasMakemigrationsFailure = true
	report.MakemigrationsFailure = primary
}

func chooseMakemigrationsResult(report *MakemigrationsReport, result MakemigrationsResult) {
	if report.HasMakemigrationsFailure || report.HasMakemigrationsResult {
		return
	}
	report.HasMakemigrationsResult = true
	report.MakemigrationsResult = cloneMakemigrationsResult(result)
}

func makemigrationsExitCode(failure MakemigrationsFailure) (int, bool) {
	switch failure.Category {
	case MakemigrationsCategoryCommand:
		return exactMakemigrationsCode(failure.Code, 2, MakemigrationsCodeInvalidArguments)
	case MakemigrationsCategorySelection:
		return exactMakemigrationsCode(failure.Code, 2,
			MakemigrationsCodeProjectNotFound, MakemigrationsCodeProjectSearchLimitExceeded,
			MakemigrationsCodeInvalidProjectDescriptor, MakemigrationsCodeProjectDescriptorIncompatible,
			MakemigrationsCodeProjectSelectionFailed)
	case MakemigrationsCategoryBuild:
		return exactMakemigrationsCode(failure.Code, 3,
			MakemigrationsCodeProjectTemporaryStorageFailed, MakemigrationsCodeProjectInventoryFailed,
			MakemigrationsCodeProjectBuildFailed)
	case MakemigrationsCategorySource:
		switch failure.Code {
		case MakemigrationsCodeSourceFingerprintFailed:
			return 3, true
		case MakemigrationsCodeSourceConflict:
			return 1, true
		default:
			return 0, false
		}
	case MakemigrationsCategoryPublication:
		switch failure.Code {
		case MakemigrationsCodePublicationUnavailable:
			return 1, true
		case MakemigrationsCodePublicationFailed, MakemigrationsCodePublicationRecoveryRequired:
			return 3, true
		default:
			return 0, false
		}
	case writerprotocol.CategoryProtocol:
		return exactMakemigrationsCode(failure.Code, 3,
			writerprotocol.CodeInvalidRequest, writerprotocol.CodeProtocolIncompatible,
			writerprotocol.CodeInvalidResponse, writerprotocol.CodeRunnerFailed)
	case writerprotocol.CategoryDeclaration, writerprotocol.CategoryDiscovery,
		writerprotocol.CategorySource, writerprotocol.CategoryGraph,
		writerprotocol.CategoryDetection, writerprotocol.CategoryCandidate:
		if writerprotocol.IsLinkedFailure(writerprotocol.Failure{Category: failure.Category, Code: failure.Code}) {
			return 1, true
		}
		return 0, false
	case MakemigrationsCategoryProcess:
		switch failure.Code {
		case MakemigrationsCodeProjectCanceled, MakemigrationsCodeProjectCleanupFailed:
			return 3, true
		case MakemigrationsCodeProjectInterrupted:
			return 130, true
		default:
			return 0, false
		}
	case MakemigrationsCategoryInternal:
		return exactMakemigrationsCode(failure.Code, 3, MakemigrationsCodeProjectInternalError)
	default:
		return 0, false
	}
}

func exactMakemigrationsCode(code string, exit int, allowed ...string) (int, bool) {
	for _, candidate := range allowed {
		if code == candidate {
			return exit, true
		}
	}
	return 0, false
}

func publishMakemigrations(input MakemigrationsInvocation, report *MakemigrationsReport) {
	if !report.HasMakemigrationsFailure && !report.HasMakemigrationsResult {
		chooseMakemigrationsFailure(report, makemigrationsInternalFailure())
	}
	if report.HasMakemigrationsFailure {
		exit, ok := makemigrationsExitCode(report.MakemigrationsFailure)
		if !ok {
			report.MakemigrationsFailure = makemigrationsInternalFailure()
			exit = 3
		}
		report.ExitCode = exit
		report.UserStderrWrites++
		if input.Stderr != nil {
			_, _ = writeOnce(input.Stderr, []byte(report.MakemigrationsFailure.Category+"/"+report.MakemigrationsFailure.Code+"\n"))
		}
		return
	}

	payload, err := encodeMakemigrationsResult(report.MakemigrationsResult)
	if err != nil {
		report.HasMakemigrationsResult = false
		report.MakemigrationsResult = MakemigrationsResult{}
		report.HasMakemigrationsFailure = true
		report.MakemigrationsFailure = makemigrationsInternalFailure()
		publishMakemigrations(input, report)
		return
	}
	report.UserStdoutWrites++
	written, writeErr := writeOnce(input.Stdout, payload)
	if written > 0 && written < len(payload) {
		report.PartialStdoutWrites++
	}
	if writeErr == nil {
		arguments, valid := parseMakemigrationsArguments(input.Args)
		if valid && report.MakemigrationsResult.Status == "pending" && arguments.mode == makemigrationsModeCheck {
			report.ExitCode = 1
		} else {
			report.ExitCode = 0
		}
		return
	}
	report.HasMakemigrationsResult = false
	report.MakemigrationsResult = MakemigrationsResult{}
	report.HasMakemigrationsFailure = true
	report.MakemigrationsFailure = makemigrationsInternalFailure()
	report.ExitCode = 3
}

func encodeMakemigrationsResult(result MakemigrationsResult) ([]byte, error) {
	type candidateDocument struct {
		App      string `json:"app"`
		Name     string `json:"name"`
		Path     string `json:"path"`
		SourceID string `json:"source_id"`
		SHA256   string `json:"sha256"`
	}
	candidates := make([]candidateDocument, len(result.Candidates))
	for index := range result.Candidates {
		candidates[index] = candidateDocument(result.Candidates[index])
	}
	payload, err := json.Marshal(struct {
		Status         string              `json:"status"`
		CandidateCount int                 `json:"candidate_count"`
		Candidates     []candidateDocument `json:"candidates"`
	}{
		Status:         result.Status,
		CandidateCount: result.CandidateCount,
		Candidates:     candidates,
	})
	if err != nil {
		return nil, err
	}
	return append(payload, '\n'), nil
}

func cloneMakemigrationsResult(input MakemigrationsResult) MakemigrationsResult {
	return MakemigrationsResult{
		Status:         input.Status,
		CandidateCount: input.CandidateCount,
		Candidates:     append([]MakemigrationsCandidate(nil), input.Candidates...),
	}
}

func definitionDocumentBytes(sources []definition.Source) int {
	total := 0
	for index := range sources {
		total += len(sources[index].Document)
	}
	return total
}

func equalProtocolAndDefinitionSources(left []writerprotocol.Source, right []definition.Source) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].SourceID != right[index].SourceID || !bytes.Equal(left[index].Document, right[index].Document) {
			return false
		}
	}
	return true
}

func equalDefinitionSources(left, right []definition.Source) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].SourceID != right[index].SourceID || !bytes.Equal(left[index].Document, right[index].Document) {
			return false
		}
	}
	return true
}

func clearDefinitionSources(sources []definition.Source) {
	for index := range sources {
		clear(sources[index].Document)
	}
}

func clearMakemigrationsProtocolResult(result *writerprotocol.Result) {
	if result == nil {
		return
	}
	for index := range result.ProgrammaticCatalog.Sources {
		clear(result.ProgrammaticCatalog.Sources[index].Document)
	}
	for index := range result.Candidates {
		clear(result.Candidates[index].Document)
	}
	*result = writerprotocol.Result{}
}
