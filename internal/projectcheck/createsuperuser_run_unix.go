//go:build darwin || linux

package projectcheck

import (
	"context"
	"io"
	"os"
	"path/filepath"

	"github.com/progresshans/godj/internal/projectcheck/createsuperuserprotocol"
	projectprotocol "github.com/progresshans/godj/internal/projectcheck/protocol"
)

// createsuperuserRunHooks keeps focused orchestration tests away from an actual
// terminal and sensitive child. Production leaves every hook nil and therefore
// uses the retained project, private workspace, terminal and owned-process
// implementations directly.
type createsuperuserRunHooks struct {
	afterArgumentValidation func()
	afterProjectSelection   func()
	selectProject           func(string, commandArguments, *Report) (retainedProject, *Failure)
	verifyRetainedProject   func(retainedProject) bool
	createPrivateWorkspace  func(retainedProject, []string, *Report, workspaceHooks) (privateWorkspace, *Failure)
	readTerminal            func(context.Context, <-chan struct{}, *os.File, io.Writer, *CreatesuperuserReport) ([]byte, []byte, *CreatesuperuserFailure)
	encodeRequest           func(createsuperuserprotocol.Request) ([]byte, error)
	executeSensitiveProcess func(context.Context, <-chan struct{}, createsuperuserProcessCommand, []byte) createsuperuserProcessResult
	closeProject            func(*retainedProject) error
	cleanupWorkspace        func(*privateWorkspace) error
	beforePublicPublication func()
}

// RunCreatesuperuser executes one project-linked explicit operator provision.
// The generic Backend is used only for the secret-free Go build. Terminal
// credentials are passed exactly once to the separately owned sensitive child;
// no stage is retried.
func RunCreatesuperuser(input CreatesuperuserInvocation) CreatesuperuserReport {
	return runCreatesuperuser(input, createsuperuserRunHooks{})
}

func runCreatesuperuser(input CreatesuperuserInvocation, hooks createsuperuserRunHooks) CreatesuperuserReport {
	// Exact argv is the first operation that can fail. In particular, do not
	// inspect cwd, snapshot the ambient environment, select a project, build, or
	// touch the terminal for a rejected form.
	input.Args = append([]string(nil), input.Args...)
	arguments, argumentFailure := parseCreatesuperuserArguments(input.Args)
	report := CreatesuperuserReport{}
	if argumentFailure != nil {
		chooseCreatesuperuserFailure(&report, *argumentFailure)
		publishCreatesuperuser(input, &report, hooks.beforePublicPublication)
		return report
	}
	if hooks.afterArgumentValidation != nil {
		hooks.afterArgumentValidation()
	}

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

	selectProjectCall := hooks.selectProject
	if selectProjectCall == nil {
		selectProjectCall = selectProject
	}
	verifyProjectCall := hooks.verifyRetainedProject
	if verifyProjectCall == nil {
		verifyProjectCall = verifyRetainedProject
	}
	createWorkspaceCall := hooks.createPrivateWorkspace
	if createWorkspaceCall == nil {
		createWorkspaceCall = createPrivateWorkspaceWithHooks
	}
	readTerminalCall := hooks.readTerminal
	if readTerminalCall == nil {
		readTerminalCall = readCreatesuperuserTerminal
	}
	encodeRequestCall := hooks.encodeRequest
	if encodeRequestCall == nil {
		encodeRequestCall = createsuperuserprotocol.EncodeRequest
	}
	executeSensitiveCall := hooks.executeSensitiveProcess
	if executeSensitiveCall == nil {
		executeSensitiveCall = executeOwnedCreatesuperuserProcess
	}
	closeProjectCall := hooks.closeProject
	if closeProjectCall == nil {
		closeProjectCall = func(project *retainedProject) error { return project.close() }
	}
	cleanupWorkspaceCall := hooks.cleanupWorkspace
	if cleanupWorkspaceCall == nil {
		cleanupWorkspaceCall = func(workspace *privateWorkspace) error { return workspace.cleanup() }
	}

	var primary *CreatesuperuserFailure
	if terminal := createsuperuserBarrier(input, nil); terminal != nil {
		chooseCreatesuperuserFailure(&report, *terminal)
		publishCreatesuperuser(input, &report, hooks.beforePublicPublication)
		return report
	}

	selected, selectionFailure := selectProjectCall(
		input.CWD,
		commandArguments{explicitDescriptor: arguments.explicitDescriptor},
		&report.Report,
	)
	if selectionFailure != nil {
		candidate := mapCreatesuperuserOuterFailure(*selectionFailure)
		primary = &candidate
	}
	if primary == nil && !verifyProjectCall(selected) {
		candidate := createsuperuserFailure(
			createsuperuserprotocol.CategorySelection,
			createsuperuserprotocol.CodeProjectSelectionFailed,
		)
		primary = &candidate
	}
	if primary == nil && hooks.afterProjectSelection != nil {
		hooks.afterProjectSelection()
	}
	primary = createsuperuserBarrier(input, primary)
	if primary != nil {
		cleanupFailed := selected.root != nil && closeProjectCall(&selected) != nil
		if cleanupFailed {
			report.CleanupFailed = 1
		}
		primary = combineCreatesuperuserCleanup(primary, cleanupFailed)
		chooseCreatesuperuserFailure(&report, *primary)
		publishCreatesuperuser(input, &report, hooks.beforePublicPublication)
		return report
	}

	workspace, workspaceFailure := createWorkspaceCall(
		selected,
		input.Environment,
		&report.Report,
		input.workspace,
	)
	if workspaceFailure != nil {
		candidate := mapCreatesuperuserOuterFailure(*workspaceFailure)
		primary = &candidate
		primary = createsuperuserBarrier(input, primary)
		cleanupFailed := closeProjectCall(&selected) != nil
		if cleanupFailed {
			report.CleanupFailed = 1
		}
		primary = combineCreatesuperuserCleanup(primary, cleanupFailed || report.CleanupFailed != 0)
		chooseCreatesuperuserFailure(&report, *primary)
		publishCreatesuperuser(input, &report, hooks.beforePublicPublication)
		return report
	}

	cleanup := func() {
		report.TempCleanupAttempts++
		cleanupFailed := closeProjectCall(&selected) != nil
		if err := cleanupWorkspaceCall(&workspace); err != nil {
			cleanupFailed = true
			report.ResidualTemp = 1
		}
		if !cleanupFailed {
			return
		}
		report.CleanupFailed = 1
		if report.KnownCreated {
			replaceCreatesuperuserOutcomeWithFailure(&report, createsuperuserFailure(
				createsuperuserprotocol.CategoryProcess,
				createsuperuserprotocol.CodeOperatorCreatedWorkspaceCleanupFailed,
			))
			return
		}
		if !report.HasCreatesuperuserFailure || createsuperuserCanceledOrInterrupted(report.CreatesuperuserFailure) {
			replaceCreatesuperuserOutcomeWithFailure(&report, createsuperuserCleanupFailure())
		}
	}
	finish := func() CreatesuperuserReport {
		// Public output is terminal: every retained descriptor/workspace resource is
		// closed first, including for logical and known-created outcomes.
		cleanup()
		publishCreatesuperuser(input, &report, hooks.beforePublicPublication)
		return report
	}

	if terminal := createsuperuserBarrier(input, nil); terminal != nil {
		chooseCreatesuperuserFailure(&report, *terminal)
		return finish()
	}
	if !verifyProjectCall(selected) {
		chooseCreatesuperuserFailure(&report, createsuperuserFailure(
			createsuperuserprotocol.CategorySelection,
			createsuperuserprotocol.CodeProjectSelectionFailed,
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
	primary = createsuperuserBuildProcessFailure(build)
	primary = createsuperuserBarrier(input, primary)
	primary = combineCreatesuperuserCleanup(primary, build.CleanupFailed)
	if primary != nil {
		chooseCreatesuperuserFailure(&report, *primary)
		return finish()
	}

	// The actual terminal is not consulted until one retained project has built
	// successfully. Verify the retained descriptor immediately before and after
	// the complete no-echo interaction.
	if !verifyProjectCall(selected) {
		chooseCreatesuperuserFailure(&report, createsuperuserFailure(
			createsuperuserprotocol.CategorySelection,
			createsuperuserprotocol.CodeProjectSelectionFailed,
		))
		return finish()
	}
	if terminal := createsuperuserBarrier(input, nil); terminal != nil {
		chooseCreatesuperuserFailure(&report, *terminal)
		return finish()
	}
	username, password, inputFailure := readTerminalCall(
		input.Context,
		input.Interrupt,
		input.Stdin,
		input.Stderr,
		&report,
	)
	retainedAfterTerminal := verifyProjectCall(selected)
	if inputFailure != nil {
		primary = inputFailure
	} else if !retainedAfterTerminal {
		candidate := createsuperuserFailure(
			createsuperuserprotocol.CategorySelection,
			createsuperuserprotocol.CodeProjectSelectionFailed,
		)
		primary = &candidate
	}
	primary = createsuperuserBarrier(input, primary)
	if primary != nil {
		clear(username)
		clear(password)
		username = nil
		password = nil
		chooseCreatesuperuserFailure(&report, *primary)
		return finish()
	}

	request := createsuperuserprotocol.Request{Username: username, Password: password}
	requestDocument, encodeErr := encodeRequestCall(request)
	// EncodeRequest returns an owned frame. The terminal buffers have no further
	// purpose and are cleared before the frame can reach the process owner.
	request.Clear()
	username = nil
	password = nil
	if encodeErr != nil || len(requestDocument) == 0 || len(requestDocument) > createsuperuserprotocol.MaxRequestBytes {
		clear(requestDocument)
		requestDocument = nil
		chooseCreatesuperuserFailure(&report, createsuperuserInternalFailure())
		return finish()
	}

	runnerCommand := newCreatesuperuserProcessCommand(
		selected.rootPath,
		[]string{filepath.Join(workspace.root, "godj-project-runner"), createsuperuserprotocol.PrivateArgument},
		workspace.environment,
	)
	report.RunnerCalls++
	expectedRequestBytes := len(requestDocument)
	ownedRequest := requestDocument
	requestDocument = nil
	runner := executeSensitiveCall(input.Context, input.Interrupt, runnerCommand, ownedRequest)
	// The production owner clears this buffer itself immediately after its sole
	// write attempt. This second clear closes the test-hook boundary as well.
	clear(ownedRequest)
	ownedRequest = nil
	recordCreatesuperuserProcess(&report, runner)
	if _, knownCreated := createsuperuserProcessKnownCreatedFailure(runner, expectedRequestBytes); knownCreated {
		report.KnownCreated = true
	}

	primary = classifyCreatesuperuserProcess(runner, expectedRequestBytes)
	primary = combineCreatesuperuserCleanup(primary, runner.cleanupFailed)
	if primary != nil {
		runner.discardResponse()
		chooseCreatesuperuserFailure(&report, *primary)
		return finish()
	}

	responseDocument := runner.takeResponse()
	response, responseFailure, responseFailed := createsuperuserprotocol.ParseResponse(responseDocument, true)
	clear(responseDocument)
	responseDocument = nil
	runner.discardResponse()
	if responseFailed {
		if createsuperuserProcessMayHaveProvisioned(runner, expectedRequestBytes) {
			chooseCreatesuperuserFailure(&report, createsuperuserProvisionOutcomeUnknownFailure())
		} else {
			chooseCreatesuperuserFailure(&report, createsuperuserFailure(responseFailure.Category, responseFailure.Code))
		}
		return finish()
	}

	// A fully reaped, strictly parsed private response is terminal. Do not let a
	// later outer cancellation erase a known durable create or persistence-unknown
	// outcome. Outer resource cleanup still runs before publication.
	if response.OK {
		report.KnownCreated = true
		chooseCreatesuperuserResult(&report)
		return finish()
	}
	report.KnownCreated = response.Failure.KnownCreated
	publicFailure, ok := createsuperuserprotocol.PublicFailureFromLinked(response.Failure)
	if !ok {
		chooseCreatesuperuserFailure(&report, createsuperuserInternalFailure())
		return finish()
	}
	chooseCreatesuperuserFailure(&report, publicFailure)
	return finish()
}

func mapCreatesuperuserOuterFailure(input Failure) CreatesuperuserFailure {
	switch input.Category {
	case projectprotocol.CategorySelection:
		switch input.Code {
		case projectprotocol.CodeProjectNotFound,
			projectprotocol.CodeProjectSearchLimitExceeded,
			projectprotocol.CodeInvalidProjectDescriptor,
			projectprotocol.CodeProjectDescriptorIncompatible,
			projectprotocol.CodeProjectSelectionFailed:
			return createsuperuserFailure(createsuperuserprotocol.CategorySelection, input.Code)
		}
	case projectprotocol.CategoryBuild:
		switch input.Code {
		case projectprotocol.CodeProjectTemporaryStorageFailed, projectprotocol.CodeProjectBuildFailed:
			return createsuperuserFailure(createsuperuserprotocol.CategoryBuild, input.Code)
		}
	case projectprotocol.CategoryProcess:
		switch input.Code {
		case projectprotocol.CodeProjectCanceled,
			projectprotocol.CodeProjectCleanupFailed,
			projectprotocol.CodeProjectInterrupted:
			return createsuperuserFailure(createsuperuserprotocol.CategoryProcess, input.Code)
		}
	case projectprotocol.CategoryInternal:
		if input.Code == projectprotocol.CodeProjectInternalError {
			return createsuperuserInternalFailure()
		}
	}
	return createsuperuserInternalFailure()
}

func createsuperuserBuildProcessFailure(process ProcessResult) *CreatesuperuserFailure {
	if process.Failure != nil {
		candidate := mapCreatesuperuserOuterFailure(*process.Failure)
		if candidate.Category == createsuperuserprotocol.CategoryBuild ||
			candidate.Category == createsuperuserprotocol.CategoryProcess {
			return &candidate
		}
		internal := createsuperuserInternalFailure()
		return &internal
	}
	if process.Started && process.ExitCode == 0 {
		return nil
	}
	candidate := createsuperuserFailure(
		createsuperuserprotocol.CategoryBuild,
		createsuperuserprotocol.CodeProjectBuildFailed,
	)
	return &candidate
}

func classifyCreatesuperuserProcess(
	process createsuperuserProcessResult,
	expectedRequestBytes int,
) *CreatesuperuserFailure {
	if candidate, knownCreated := createsuperuserProcessKnownCreatedFailure(process, expectedRequestBytes); knownCreated {
		return &candidate
	}
	if process.failure != nil {
		if createsuperuserProcessMayHaveProvisioned(process, expectedRequestBytes) {
			candidate := createsuperuserProvisionOutcomeUnknownFailure()
			return &candidate
		}
		candidate := *process.failure
		if candidate.KnownCreated || candidate.Category != createsuperuserprotocol.CategoryProcess {
			internal := createsuperuserInternalFailure()
			return &internal
		}
		switch candidate.Code {
		case createsuperuserprotocol.CodeProjectCanceled,
			createsuperuserprotocol.CodeProjectCleanupFailed,
			createsuperuserprotocol.CodeProjectInterrupted,
			createsuperuserprotocol.CodeSensitiveInputTransportFailed:
			return &candidate
		default:
			internal := createsuperuserInternalFailure()
			return &internal
		}
	}
	if process.cleanupFailed {
		if createsuperuserProcessMayHaveProvisioned(process, expectedRequestBytes) {
			candidate := createsuperuserProvisionOutcomeUnknownFailure()
			return &candidate
		}
		candidate := createsuperuserCleanupFailure()
		return &candidate
	}
	// The process owner deliberately terminates a child as soon as either
	// private stream exceeds its bound. That termination may produce a nonzero
	// exit and it deliberately discards any retained response, but the primary
	// product fact remains an invalid bounded protocol document rather than a
	// generic runner failure or a synthetic scalar mismatch.
	if process.stdoutScalar.Truncated || process.stderrScalar.Truncated {
		if createsuperuserProcessMayHaveProvisioned(process, expectedRequestBytes) {
			candidate := createsuperuserProvisionOutcomeUnknownFailure()
			return &candidate
		}
		candidate := createsuperuserFailure(
			createsuperuserprotocol.CategoryProtocol,
			createsuperuserprotocol.CodeInvalidResponse,
		)
		return &candidate
	}
	if !process.started || !process.exited || process.exitCode != 0 {
		if createsuperuserProcessMayHaveProvisioned(process, expectedRequestBytes) {
			candidate := createsuperuserProvisionOutcomeUnknownFailure()
			return &candidate
		}
		candidate := createsuperuserFailure(
			createsuperuserprotocol.CategoryProtocol,
			createsuperuserprotocol.CodeRunnerFailed,
		)
		return &candidate
	}
	// A zero exit alone is not sufficient proof that the one sensitive child
	// consumed the complete private request and produced the retained response.
	// These owner-observed scalars are the non-secret evidence for that exact
	// transfer/reap boundary; inconsistent synthetic or production results fail
	// closed before any response bytes can be trusted.
	if expectedRequestBytes <= 0 ||
		process.directReaps != 1 ||
		process.requestWriteAttempts != 1 ||
		process.requestBytesWritten != expectedRequestBytes ||
		process.stdoutScalar.RetainedBytes != len(process.response) {
		internal := createsuperuserInternalFailure()
		return &internal
	}
	return nil
}

// createsuperuserProcessMayHaveProvisioned is deliberately conservative. Once
// the one complete request has crossed into a started child, the parent cannot
// prove whether a missing/invalid terminal response happened before or after
// the database mutation. The caller must reconcile instead of retrying.
func createsuperuserProcessMayHaveProvisioned(
	process createsuperuserProcessResult,
	expectedRequestBytes int,
) bool {
	return process.started && expectedRequestBytes > 0 &&
		process.requestWriteAttempts == 1 &&
		process.requestBytesWritten == expectedRequestBytes
}

// createsuperuserProcessKnownCreatedFailure accepts only a normal reserved exit
// produced by the canonical project runner after linked provisioning reported
// a committed insert and its response write failed. Later stream or descendant
// cleanup facts do not erase that direct-child outcome. A signaled, partially
// written, or unreaped direct child can only be outcome-unknown.
func createsuperuserProcessKnownCreatedFailure(
	process createsuperuserProcessResult,
	expectedRequestBytes int,
) (CreatesuperuserFailure, bool) {
	if !createsuperuserProcessMayHaveProvisioned(process, expectedRequestBytes) ||
		!process.exited || process.directReaps != 1 {
		return CreatesuperuserFailure{}, false
	}
	switch process.exitCode {
	case createsuperuserprotocol.KnownCreatedResponseFailureExitCode:
		return createsuperuserFailure(
			createsuperuserprotocol.CategoryInternal,
			createsuperuserprotocol.CodeOperatorCreatedOutputFailed,
		), true
	case createsuperuserprotocol.KnownCreatedBackendCleanupResponseFailureExitCode:
		return createsuperuserFailure(
			createsuperuserprotocol.CategoryBackend,
			createsuperuserprotocol.CodeOperatorCreatedBackendCleanupFailed,
		), true
	default:
		return CreatesuperuserFailure{}, false
	}
}

func createsuperuserProvisionOutcomeUnknownFailure() CreatesuperuserFailure {
	return createsuperuserFailure(
		createsuperuserprotocol.CategoryProcess,
		createsuperuserprotocol.CodeOperatorProvisionOutcomeUnknown,
	)
}

func recordCreatesuperuserProcess(report *CreatesuperuserReport, process createsuperuserProcessResult) {
	report.DirectChildReaps += process.directReaps
	report.GroupSIGINTAttempts += process.sigintAttempts
	report.GroupSIGKILLAttempts += process.sigkillAttempts
	if process.cleanupFailed {
		report.CleanupFailed = 1
	}
	report.RawDiagnosticsDiscarded = true
	report.RunnerStdoutRetainedBytes = process.stdoutScalar.RetainedBytes
	report.RunnerStdoutTruncated = process.stdoutScalar.Truncated
	report.RunnerStderrRetainedBytes = process.stderrScalar.RetainedBytes
	report.RunnerStderrTruncated = process.stderrScalar.Truncated
	if process.stdoutScalar.RetainedBytes != 0 || process.stdoutScalar.Truncated {
		report.RunnerResponseWrites++
	}
	report.SensitiveRequestWriteAttempts += process.requestWriteAttempts
	report.SensitiveRequestBytesWritten += process.requestBytesWritten
}

func createsuperuserBarrier(input CreatesuperuserInvocation, primary *CreatesuperuserFailure) *CreatesuperuserFailure {
	if primary != nil && primary.Category == createsuperuserprotocol.CategoryProcess &&
		primary.Code == createsuperuserprotocol.CodeProjectCleanupFailed {
		return primary
	}
	// Terminal restoration is input-resource cleanup. Once observed it must not
	// be hidden by a concurrently delivered cancellation or interrupt.
	if primary != nil && primary.Category == createsuperuserprotocol.CategoryInput &&
		primary.Code == createsuperuserprotocol.CodeTerminalStateFailed {
		return primary
	}
	if input.Interrupt != nil {
		select {
		case <-input.Interrupt:
			candidate := createsuperuserFailure(
				createsuperuserprotocol.CategoryProcess,
				createsuperuserprotocol.CodeProjectInterrupted,
			)
			return &candidate
		default:
		}
	}
	if input.Context != nil && input.Context.Err() != nil {
		candidate := createsuperuserFailure(
			createsuperuserprotocol.CategoryProcess,
			createsuperuserprotocol.CodeProjectCanceled,
		)
		return &candidate
	}
	return primary
}

func combineCreatesuperuserCleanup(primary *CreatesuperuserFailure, failed bool) *CreatesuperuserFailure {
	if !failed {
		return primary
	}
	if primary == nil || createsuperuserCanceledOrInterrupted(*primary) {
		candidate := createsuperuserCleanupFailure()
		return &candidate
	}
	return primary
}

func createsuperuserCanceledOrInterrupted(input CreatesuperuserFailure) bool {
	return input.Category == createsuperuserprotocol.CategoryProcess &&
		(input.Code == createsuperuserprotocol.CodeProjectCanceled ||
			input.Code == createsuperuserprotocol.CodeProjectInterrupted)
}

func createsuperuserCleanupFailure() CreatesuperuserFailure {
	return createsuperuserFailure(
		createsuperuserprotocol.CategoryProcess,
		createsuperuserprotocol.CodeProjectCleanupFailed,
	)
}

func createsuperuserInternalFailure() CreatesuperuserFailure {
	return CreatesuperuserFailure{
		Category: createsuperuserprotocol.CategoryInternal,
		Code:     createsuperuserprotocol.CodeProjectInternalError,
	}
}

func chooseCreatesuperuserFailure(report *CreatesuperuserReport, primary CreatesuperuserFailure) {
	if report.HasCreatesuperuserFailure || report.HasCreatesuperuserResult {
		return
	}
	if _, ok := createsuperuserprotocol.ExitCode(primary); !ok {
		primary = createsuperuserInternalFailure()
	}
	report.HasCreatesuperuserFailure = true
	report.CreatesuperuserFailure = primary
}

func chooseCreatesuperuserResult(report *CreatesuperuserReport) {
	if report.HasCreatesuperuserFailure || report.HasCreatesuperuserResult {
		return
	}
	report.HasCreatesuperuserResult = true
}

func replaceCreatesuperuserOutcomeWithFailure(report *CreatesuperuserReport, primary CreatesuperuserFailure) {
	report.HasCreatesuperuserResult = false
	report.HasCreatesuperuserFailure = false
	report.CreatesuperuserFailure = CreatesuperuserFailure{}
	chooseCreatesuperuserFailure(report, primary)
}

func publishCreatesuperuser(
	input CreatesuperuserInvocation,
	report *CreatesuperuserReport,
	beforePublication func(),
) {
	if !report.HasCreatesuperuserFailure && !report.HasCreatesuperuserResult {
		chooseCreatesuperuserFailure(report, createsuperuserInternalFailure())
	}
	if report.HasCreatesuperuserResult && !report.KnownCreated {
		replaceCreatesuperuserOutcomeWithFailure(report, createsuperuserInternalFailure())
	}
	if beforePublication != nil {
		beforePublication()
	}
	if report.HasCreatesuperuserFailure {
		exit, ok := createsuperuserprotocol.ExitCode(report.CreatesuperuserFailure)
		if !ok {
			report.CreatesuperuserFailure = createsuperuserInternalFailure()
			exit = 3
		}
		report.ExitCode = exit
		report.UserStderrWrites++
		if input.Stderr != nil {
			_, _ = writeOnce(input.Stderr, []byte(
				report.CreatesuperuserFailure.Category+"/"+report.CreatesuperuserFailure.Code+"\n",
			))
		}
		return
	}

	payload := createsuperuserprotocol.PublicSuccessDocument()
	payloadLength := len(payload)
	report.UserStdoutWrites++
	written, writeErr := writeOnce(input.Stdout, payload)
	if written > 0 && written < payloadLength {
		report.PartialStdoutWrites++
	}
	clear(payload)
	if writeErr == nil && written == payloadLength {
		report.ExitCode = 0
		return
	}
	replaceCreatesuperuserOutcomeWithFailure(report, createsuperuserFailure(
		createsuperuserprotocol.CategoryInternal,
		createsuperuserprotocol.CodeOperatorCreatedOutputFailed,
	))
	report.ExitCode = 3
	report.UserStderrWrites++
	if input.Stderr != nil {
		_, _ = writeOnce(input.Stderr, []byte(
			report.CreatesuperuserFailure.Category+"/"+report.CreatesuperuserFailure.Code+"\n",
		))
	}
}
