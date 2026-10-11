//go:build darwin || linux

package projectcheck

import (
	"context"
	"path/filepath"

	"github.com/progresshans/godj/internal/projectcheck/protocol"
)

// commandPolicy separates command vocabulary and terminal precedence from the
// shared resource/process lifetime. In particular, no barrier runs implicitly
// after response parsing or publication.
type commandPolicy[F any] struct {
	selection      func(Failure) F
	workspace      func(Failure) F
	process        func(ProcessStage, ProcessResult) *F
	barrier        func(*F) *F
	cleanup        func(*F, bool) *F
	afterSelection func(retainedProject) *F
}

type commandHooks struct {
	selectProject  func(string, commandArguments, *Report) (retainedProject, *Failure)
	verifyProject  func(retainedProject) bool
	makeWorkspace  func(retainedProject, []string, *Report, workspaceHooks) (privateWorkspace, *Failure)
	closeProject   func(*retainedProject) error
	closeWorkspace func(*privateWorkspace) error
}

// projectCommand owns one selected root, one workspace and their close order.
// It never owns credentials, filesystem publication, or a foreground runtime.
type projectCommand[F any] struct {
	ctx          context.Context
	interrupt    <-chan struct{}
	backend      Backend
	report       *Report
	policy       commandPolicy[F]
	hooks        commandHooks
	selected     retainedProject
	workspace    privateWorkspace
	hasWorkspace bool
}

func newProjectCommand[F any](ctx context.Context, interrupt <-chan struct{}, backend Backend, report *Report, policy commandPolicy[F], hooks commandHooks) *projectCommand[F] {
	if hooks.selectProject == nil {
		hooks.selectProject = selectProject
	}
	if hooks.verifyProject == nil {
		hooks.verifyProject = verifyRetainedProject
	}
	if hooks.makeWorkspace == nil {
		hooks.makeWorkspace = createPrivateWorkspaceWithHooks
	}
	if hooks.closeProject == nil {
		hooks.closeProject = (*retainedProject).close
	}
	if hooks.closeWorkspace == nil {
		hooks.closeWorkspace = (*privateWorkspace).cleanup
	}
	return &projectCommand[F]{ctx: ctx, interrupt: interrupt, backend: backend, report: report, policy: policy, hooks: hooks}
}

// open leaves every successfully acquired resource with this command, even on
// failure. The caller selects its outcome, then closes before public output.
func (command *projectCommand[F]) open(cwd, explicitDescriptor string, environment []string, hooks workspaceHooks) *F {
	selected, failed := command.hooks.selectProject(cwd, commandArguments{explicitDescriptor: explicitDescriptor}, command.report)
	command.selected = selected
	var primary *F
	if failed != nil {
		candidate := command.policy.selection(*failed)
		primary = &candidate
	} else {
		primary = command.verify()
	}
	if primary == nil && command.policy.afterSelection != nil {
		primary = command.policy.afterSelection(selected)
	}
	if primary = command.policy.barrier(primary); primary != nil {
		return primary
	}
	workspace, failed := command.hooks.makeWorkspace(selected, environment, command.report, hooks)
	if failed != nil {
		candidate := command.policy.workspace(*failed)
		return command.policy.barrier(&candidate)
	}
	command.workspace = workspace
	command.hasWorkspace = true
	return nil
}

func (command *projectCommand[F]) verify() *F {
	if command.hooks.verifyProject(command.selected) {
		return nil
	}
	candidate := command.policy.selection(failure(protocol.CategorySelection, protocol.CodeProjectSelectionFailed))
	return &candidate
}

func (command *projectCommand[F]) ready() *F {
	if primary := command.policy.barrier(nil); primary != nil {
		return primary
	}
	return command.verify()
}

func (command *projectCommand[F]) beforeRunner() *F {
	if primary := command.verify(); primary != nil {
		return primary
	}
	return command.policy.barrier(nil)
}

func (command *projectCommand[F]) build(packagePath, binaryName string) *F {
	// Filesystem verification and writer inventory may have run since ready.
	// Observe cancellation again at the actual child-start boundary.
	if primary := command.policy.barrier(nil); primary != nil {
		return primary
	}
	build := buildProjectPackage(command.ctx, command.interrupt, command.backend, command.selected, command.workspace,
		packagePath, binaryName, command.report)
	primary := command.policy.barrier(command.policy.process(BuildStage, build))
	return command.policy.cleanup(primary, build.CleanupFailed)
}

func (command *projectCommand[F]) buildRunner() *F {
	if primary := command.ready(); primary != nil {
		return primary
	}
	if primary := command.build(command.selected.descriptor.packagePath, "godj-project-runner"); primary != nil {
		return primary
	}
	return command.beforeRunner()
}

func (command *projectCommand[F]) run(stage ProcessStage, privateArgument string, request []byte) ProcessResult {
	child := Command{
		Dir:  command.selected.rootPath,
		Argv: []string{filepath.Join(command.workspace.root, "godj-project-runner"), privateArgument},
		Env:  command.workspace.environment, Stdin: request,
	}
	command.report.RunnerCalls++
	result := command.backend.Execute(command.ctx, command.interrupt, stage, cloneCommand(child))
	recordProcess(command.report, stage, result)
	return result
}

func (command *projectCommand[F]) completed(stage ProcessStage, result ProcessResult) *F {
	return command.policy.cleanup(command.policy.process(stage, result), result.CleanupFailed)
}

func (command *projectCommand[F]) close() bool {
	if command.hasWorkspace {
		return closeCommandWorkspace(command.report,
			func() error { return command.hooks.closeProject(&command.selected) },
			func() error { return command.hooks.closeWorkspace(&command.workspace) })
	}
	if command.selected.root != nil && command.hooks.closeProject(&command.selected) != nil {
		command.report.CleanupFailed = 1
	}
	// Partial workspace cleanup belongs to makeWorkspace and is already
	// recorded even though no complete workspace was transferred to us.
	return command.report.CleanupFailed != 0
}

// parseCommandResponse consumes and clears the bounded child response. The
// command supplies its envelope parser and truncation failure; process/drain
// failure wins before either. Cancellation after this boundary is an explicit
// command policy, never an implicit part of this common operation.
func parseCommandResponse[R, F any](process *ProcessResult, primary *F, truncated F, parse func([]byte, bool) (R, F, bool)) (R, *F) {
	defer func() {
		clear(process.Stdout)
		process.Stdout = nil
	}()
	var zero R
	if primary != nil {
		return zero, primary
	}
	if process.StdoutScalar.Truncated {
		return zero, &truncated
	}
	response, failed, hasFailure := parse(process.Stdout, process.Started && process.ExitCode == 0)
	if hasFailure {
		return zero, &failed
	}
	return response, nil
}
