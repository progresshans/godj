//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

type selectionHooks struct {
	virtualFilesystemRoot string
	afterMarkerScan       func(parentFD int, name string)
	afterDescriptorStat   func(parentFD int, name string)
	afterAncestorScan     func(path string, directoryFD int, markerFound bool)
	beforeExplicitOpen    func(path string)
}

type selectedProject struct {
	Root       string
	Descriptor descriptor
	identity   fileIdentity
}

func selectProject(cwd string, arguments commandArguments, metrics *oracleMetrics, lim limits, hooks selectionHooks) (selectedProject, *failure) {
	physicalCWD, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return selectedProject{}, fail("migration_project_selection_error", "project_selection_failed")
	}
	info, err := os.Stat(physicalCWD)
	if err != nil || !info.IsDir() {
		return selectedProject{}, fail("migration_project_selection_error", "project_selection_failed")
	}
	if arguments.ExplicitDescriptor != "" {
		candidate := arguments.ExplicitDescriptor
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(physicalCWD, candidate)
		}
		candidate = filepath.Clean(candidate)
		if filepath.Base(candidate) != "godj.toml" {
			return selectedProject{}, fail("migration_project_selection_error", "invalid_project_descriptor")
		}
		parent := filepath.Dir(candidate)
		physicalParent, resolveErr := filepath.EvalSymlinks(parent)
		if resolveErr != nil {
			if errors.Is(resolveErr, os.ErrNotExist) || errors.Is(resolveErr, syscall.ENOTDIR) {
				return selectedProject{}, fail("migration_project_selection_error", "invalid_project_descriptor")
			}
			return selectedProject{}, fail("migration_project_selection_error", "project_selection_failed")
		}
		initialParent, initialErr := os.Lstat(physicalParent)
		if initialErr != nil {
			return selectedProject{}, fail("migration_project_selection_error", "project_selection_failed")
		}
		if !initialParent.IsDir() {
			return selectedProject{}, fail("migration_project_selection_error", "invalid_project_descriptor")
		}
		if initialParent.Mode()&os.ModeSymlink != 0 {
			return selectedProject{}, fail("migration_project_selection_error", "project_selection_failed")
		}
		if hooks.beforeExplicitOpen != nil {
			hooks.beforeExplicitOpen(physicalParent)
		}
		handle, identity, openErr := openRetainedPhysicalDirectory(physicalParent)
		if openErr != nil {
			return selectedProject{}, fail("migration_project_selection_error", "project_selection_failed")
		}
		if !sameOSFileIdentity(initialParent, identity) {
			handle.Close()
			return selectedProject{}, fail("migration_project_selection_error", "project_selection_failed")
		}
		defer handle.Close()
		exists, scanErr := directoryContainsRawName(handle, "godj.toml")
		if scanErr != nil {
			return selectedProject{}, fail("migration_project_selection_error", "project_selection_failed")
		}
		if !exists {
			return selectedProject{}, fail("migration_project_selection_error", "invalid_project_descriptor")
		}
		if hooks.afterMarkerScan != nil {
			hooks.afterMarkerScan(int(handle.Fd()), "godj.toml")
		}
		selected, primary := readSelectedDescriptor(physicalParent, handle, metrics, lim, hooks)
		if !verifyRetainedDirectory(physicalParent, handle, identity) {
			return selectedProject{}, fail("migration_project_selection_error", "project_selection_failed")
		}
		selected.identity = identity
		return selected, primary
	}

	current := physicalCWD
	handle, identity, openErr := openRetainedPhysicalDirectory(current)
	if openErr != nil {
		return selectedProject{}, fail("migration_project_selection_error", "project_selection_failed")
	}
	for inspected := 0; inspected < lim.ancestors; inspected++ {
		metrics.AncestorDirectoriesInspected++
		exists, scanErr := directoryContainsRawName(handle, "godj.toml")
		if scanErr != nil {
			handle.Close()
			return selectedProject{}, fail("migration_project_selection_error", "project_selection_failed")
		}
		if hooks.afterAncestorScan != nil {
			hooks.afterAncestorScan(current, int(handle.Fd()), exists)
		}
		if !verifyRetainedDirectory(current, handle, identity) {
			handle.Close()
			return selectedProject{}, fail("migration_project_selection_error", "project_selection_failed")
		}
		if exists {
			if hooks.afterMarkerScan != nil {
				hooks.afterMarkerScan(int(handle.Fd()), "godj.toml")
			}
			selected, primary := readSelectedDescriptor(current, handle, metrics, lim, hooks)
			if !verifyRetainedDirectory(current, handle, identity) {
				primary = fail("migration_project_selection_error", "project_selection_failed")
			}
			selected.identity = identity
			handle.Close()
			return selected, primary
		}
		parent := filepath.Dir(current)
		atFilesystemRoot := parent == current || (hooks.virtualFilesystemRoot != "" && samePhysicalPath(current, hooks.virtualFilesystemRoot))
		if atFilesystemRoot {
			handle.Close()
			return selectedProject{}, fail("migration_project_selection_error", "project_not_found")
		}
		if inspected+1 == lim.ancestors {
			handle.Close()
			return selectedProject{}, fail("migration_project_selection_error", "project_search_limit_exceeded")
		}
		parentFD, parentErr := unix.Openat(int(handle.Fd()), "..", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if parentErr != nil {
			handle.Close()
			return selectedProject{}, fail("migration_project_selection_error", "project_selection_failed")
		}
		parentHandle := os.NewFile(uintptr(parentFD), parent)
		if parentHandle == nil {
			_ = unix.Close(parentFD)
			handle.Close()
			return selectedProject{}, fail("migration_project_internal_error", "project_internal_error")
		}
		var parentStat unix.Stat_t
		if unix.Fstat(parentFD, &parentStat) != nil || !isMode(uint32(parentStat.Mode), unix.S_IFDIR) || !verifyRetainedDirectory(parent, parentHandle, identityOf(&parentStat)) {
			parentHandle.Close()
			handle.Close()
			return selectedProject{}, fail("migration_project_selection_error", "project_selection_failed")
		}
		handle.Close()
		handle = parentHandle
		identity = identityOf(&parentStat)
		current = parent
	}
	handle.Close()
	return selectedProject{}, fail("migration_project_internal_error", "project_internal_error")
}

func openRetainedPhysicalDirectory(path string) (*os.File, fileIdentity, error) {
	initial, err := os.Lstat(path)
	if err != nil || !initial.IsDir() || initial.Mode()&os.ModeSymlink != 0 {
		return nil, fileIdentity{}, errors.New("invalid physical directory")
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fileIdentity{}, err
	}
	handle := os.NewFile(uintptr(fd), path)
	if handle == nil {
		_ = unix.Close(fd)
		return nil, fileIdentity{}, errors.New("could not retain directory")
	}
	var opened unix.Stat_t
	if err := unix.Fstat(fd, &opened); err != nil || !isMode(uint32(opened.Mode), unix.S_IFDIR) {
		handle.Close()
		return nil, fileIdentity{}, errors.New("could not stat retained directory")
	}
	identity := identityOf(&opened)
	if !sameOSFileIdentity(initial, identity) {
		handle.Close()
		return nil, fileIdentity{}, errors.New("directory identity changed")
	}
	return handle, identity, nil
}

func sameOSFileIdentity(info os.FileInfo, identity fileIdentity) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return uint64(stat.Dev) == identity.device && uint64(stat.Ino) == identity.inode
}

func verifyRetainedDirectory(path string, handle *os.File, expected fileIdentity) bool {
	var retained unix.Stat_t
	if unix.Fstat(int(handle.Fd()), &retained) != nil || identityOf(&retained) != expected {
		return false
	}
	current, err := os.Lstat(path)
	return err == nil && current.IsDir() && current.Mode()&os.ModeSymlink == 0 && sameOSFileIdentity(current, expected)
}

func samePhysicalPath(left, right string) bool {
	leftResolved, leftErr := filepath.EvalSymlinks(left)
	rightResolved, rightErr := filepath.EvalSymlinks(right)
	return leftErr == nil && rightErr == nil && leftResolved == rightResolved
}

func readSelectedDescriptor(root string, parent *os.File, metrics *oracleMetrics, lim limits, hooks selectionHooks) (selectedProject, *failure) {
	var initial unix.Stat_t
	if err := unix.Fstatat(int(parent.Fd()), "godj.toml", &initial, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return selectedProject{}, fail("migration_project_selection_error", "project_selection_failed")
	}
	if !isMode(uint32(initial.Mode), unix.S_IFREG) {
		return selectedProject{}, fail("migration_project_selection_error", "invalid_project_descriptor")
	}
	if hooks.afterDescriptorStat != nil {
		hooks.afterDescriptorStat(int(parent.Fd()), "godj.toml")
	}
	fd, openErr := unix.Openat(int(parent.Fd()), "godj.toml", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if openErr != nil {
		return selectedProject{}, fail("migration_project_selection_error", "project_selection_failed")
	}
	file := os.NewFile(uintptr(fd), filepath.Join(root, "godj.toml"))
	if file == nil {
		_ = unix.Close(fd)
		return selectedProject{}, fail("migration_project_internal_error", "project_internal_error")
	}
	var opened unix.Stat_t
	if statErr := unix.Fstat(fd, &opened); statErr != nil || !isMode(uint32(opened.Mode), unix.S_IFREG) || identityOf(&opened) != identityOf(&initial) {
		file.Close()
		return selectedProject{}, fail("migration_project_selection_error", "project_selection_failed")
	}
	document, readErr := io.ReadAll(io.LimitReader(file, int64(lim.descriptorBytes)+1))
	closeErr := file.Close()
	var current unix.Stat_t
	postErr := unix.Fstatat(int(parent.Fd()), "godj.toml", &current, unix.AT_SYMLINK_NOFOLLOW)
	if postErr != nil || !isMode(uint32(current.Mode), unix.S_IFREG) || identityOf(&current) != identityOf(&initial) || closeErr != nil || readErr != nil {
		return selectedProject{}, fail("migration_project_selection_error", "project_selection_failed")
	}
	metrics.DescriptorReads++
	parsed, primary := parseDescriptor(document, lim)
	if primary != nil {
		return selectedProject{}, primary
	}
	return selectedProject{Root: root, Descriptor: parsed}, nil
}

type commandSpec struct {
	Dir   string
	Argv  []string
	Env   []string
	Stdin []byte
}

type childResult struct {
	Stdout         []byte
	Stderr         []byte
	Exit           int
	StdoutOverflow bool
	Failure        *failure
	DirectReaps    int
	CleanupFailed  bool
}

type childBackend interface {
	Run(context.Context, string, commandSpec, *observation) childResult
}

type privateWorkspace struct {
	Root    string
	Env     []string
	Cleanup func() error
}

type globalDependencies struct {
	Backend         childBackend
	CreateWorkspace func(projectRoot string, observed *observation) (privateWorkspace, *failure)
	SelectionHooks  selectionHooks
	Interrupted     func() bool
	Publication     publicationHarness
}

type globalInvocation struct {
	Context context.Context
	CWD     string
	Argv    []string
	Limits  limits
	Deps    globalDependencies
}

func runGlobal(input globalInvocation) observation {
	observed := observation{Feasibility: feasibilityMetrics{Diagnostics: map[string]diagnosticScalar{}}, publication: input.Deps.Publication}
	arguments, primary := parseArguments(input.Argv)
	if terminal := barrierFailure(input, primary); terminal != nil {
		observed.choose(terminal, nil)
		observed.publish()
		return observed
	}
	selected, primary := selectProject(input.CWD, arguments, &observed.Metrics, input.Limits, input.Deps.SelectionHooks)
	if primary == nil && !verifySelectedProject(selected) {
		primary = fail("migration_project_selection_error", "project_selection_failed")
	}
	if terminal := barrierFailure(input, primary); terminal != nil {
		observed.choose(terminal, nil)
		observed.publish()
		return observed
	}
	workspace, primary := input.Deps.CreateWorkspace(selected.Root, &observed)
	if terminal := barrierFailure(input, primary); terminal != nil {
		observed.choose(terminal, nil)
		if primary == nil {
			recordPublicationEvent(&observed, "cleanup.start")
			observed.Feasibility.TempCleanupAttempts++
			if err := workspace.Cleanup(); err != nil {
				observed.Feasibility.CleanupFailed = 1
				observed.Feasibility.ResidualTemp = 1
				observed.Result = nil
				observed.Failure = fail("migration_project_process_error", "project_cleanup_failed")
			}
			recordPublicationEvent(&observed, "cleanup.complete")
		}
		observed.publish()
		return observed
	}
	cleanup := func() {
		recordPublicationEvent(&observed, "cleanup.start")
		observed.Feasibility.TempCleanupAttempts++
		if err := workspace.Cleanup(); err != nil {
			observed.Feasibility.CleanupFailed = 1
			observed.Feasibility.ResidualTemp = 1
			if observed.Failure == nil || observed.Failure.Code == "project_interrupted" || observed.Failure.Code == "project_canceled" {
				observed.Result = nil
				observed.Failure = fail("migration_project_process_error", "project_cleanup_failed")
			}
		}
		observed.Feasibility.RawDiagnostics = nil
		recordPublicationEvent(&observed, "cleanup.complete")
	}

	build := commandSpec{
		Dir:  selected.Root,
		Argv: []string{"go", "build", "-mod=readonly", "-o", filepath.Join(workspace.Root, "godj-project-runner"), selected.Descriptor.Package},
		Env:  workspace.Env,
	}
	if !verifySelectedProject(selected) {
		observed.choose(barrierFailure(input, fail("migration_project_selection_error", "project_selection_failed")), nil)
		cleanup()
		observed.publish()
		return observed
	}
	observed.Metrics.BuildCalls++
	buildResult := input.Deps.Backend.Run(input.Context, "build", build, &observed)
	observed.Feasibility.DirectChildReaps += buildResult.DirectReaps
	zeroBytes(buildResult.Stdout)
	zeroBytes(buildResult.Stderr)
	buildResult.Stdout = nil
	buildResult.Stderr = nil
	var buildPrimary *failure
	if buildResult.Failure != nil {
		buildPrimary = backendFailureForStage("build", buildResult.Failure)
	} else if buildResult.Exit != 0 {
		buildPrimary = fail("migration_project_build_error", "project_build_failed")
	}
	buildTerminal := barrierFailure(input, buildPrimary)
	buildTerminal = combineProcessFinalization(buildTerminal, buildResult.CleanupFailed)
	if buildTerminal != nil {
		observed.choose(buildTerminal, nil)
		cleanup()
		observed.publish()
		return observed
	}

	runner := commandSpec{
		Dir:   selected.Root,
		Argv:  []string{filepath.Join(workspace.Root, "godj-project-runner"), "__godj_project_runner_v1"},
		Env:   workspace.Env,
		Stdin: []byte(`{"protocol_version":1,"command":"migrations.check"}`),
	}
	if !verifySelectedProject(selected) {
		observed.choose(barrierFailure(input, fail("migration_project_selection_error", "project_selection_failed")), nil)
		cleanup()
		observed.publish()
		return observed
	}
	observed.Metrics.RunnerCalls++
	runnerResult := input.Deps.Backend.Run(input.Context, "runner", runner, &observed)
	observed.Feasibility.DirectChildReaps += runnerResult.DirectReaps
	zeroBytes(runnerResult.Stderr)
	runnerResult.Stderr = nil
	var result *checkResult
	if runnerResult.Failure != nil {
		primary = backendFailureForStage("runner", runnerResult.Failure)
	} else if runnerResult.Exit != 0 {
		primary = fail("migration_project_protocol_error", "project_runner_failed")
	} else if runnerResult.StdoutOverflow {
		primary = fail("migration_project_protocol_error", "invalid_project_runner_response")
	} else {
		result, primary = parseRunnerResponse(runnerResult.Stdout, runnerResult.Exit, input.Limits)
	}
	zeroBytes(runnerResult.Stdout)
	runnerResult.Stdout = nil
	runnerTerminal := barrierFailure(input, primary)
	runnerTerminal = combineProcessFinalization(runnerTerminal, runnerResult.CleanupFailed)
	if runnerTerminal != nil {
		observed.choose(runnerTerminal, nil)
	} else {
		observed.choose(nil, result)
	}
	cleanup()
	observed.publish()
	return observed
}

func recordPublicationEvent(observed *observation, event string) {
	if observed.publication.event != nil {
		observed.publication.event(event)
	}
}

func backendFailureForStage(stage string, candidate *failure) *failure {
	if candidate == nil || exitFor(candidate.Category, candidate.Code) < 0 {
		return fail("migration_project_internal_error", "project_internal_error")
	}
	if candidate.Category == "migration_project_process_error" {
		switch candidate.Code {
		case "project_canceled", "project_interrupted", "project_cleanup_failed":
			return fail(candidate.Category, candidate.Code)
		}
	}
	switch stage {
	case "build":
		if candidate.Category == "migration_project_build_error" && (candidate.Code == "project_build_failed" || candidate.Code == "project_temporary_storage_failed") {
			return fail(candidate.Category, candidate.Code)
		}
	case "runner":
		if candidate.Category == "migration_project_protocol_error" && candidate.Code == "project_runner_failed" {
			return fail(candidate.Category, candidate.Code)
		}
	}
	return fail("migration_project_internal_error", "project_internal_error")
}

func canceledAtBarrier(input globalInvocation) *failure {
	if input.Deps.Interrupted != nil && input.Deps.Interrupted() {
		return fail("migration_project_process_error", "project_interrupted")
	}
	if input.Context != nil && input.Context.Err() != nil {
		return fail("migration_project_process_error", "project_canceled")
	}
	return nil
}

func barrierFailure(input globalInvocation, primary *failure) *failure {
	if primary != nil && primary.Category == "migration_project_process_error" && primary.Code == "project_cleanup_failed" {
		return primary
	}
	if canceled := canceledAtBarrier(input); canceled != nil {
		return canceled
	}
	return primary
}

func combineProcessFinalization(primary *failure, cleanupFailed bool) *failure {
	if !cleanupFailed {
		return primary
	}
	if primary == nil || (primary.Category == "migration_project_process_error" && (primary.Code == "project_canceled" || primary.Code == "project_interrupted")) {
		return fail("migration_project_process_error", "project_cleanup_failed")
	}
	return primary
}

func verifySelectedProject(selected selectedProject) bool {
	info, err := os.Lstat(selected.Root)
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 && sameOSFileIdentity(info, selected.identity)
}

type inProcessBackend struct {
	Roots        []string
	Limits       limits
	Hooks        discoveryHooks
	BuildFailure bool
	RunnerWire   []byte
	LastBuild    commandSpec
	LastRunner   commandSpec
}

func (backend *inProcessBackend) Run(_ context.Context, stage string, spec commandSpec, observed *observation) childResult {
	switch stage {
	case "build":
		backend.LastBuild = cloneCommandSpec(spec)
		if backend.BuildFailure {
			return childResult{Exit: 1, Stderr: []byte("synthetic compiler diagnostic"), DirectReaps: 1}
		}
		return childResult{DirectReaps: 1}
	case "runner":
		backend.LastRunner = cloneCommandSpec(spec)
		if backend.RunnerWire != nil {
			observed.Metrics.RunnerResponseWrites++
			return childResult{Stdout: append([]byte(nil), backend.RunnerWire...), DirectReaps: 1}
		}
		linked := invokeLinked(linkedInvocation{
			ProjectRoot: spec.Dir,
			Roots:       backend.Roots,
			Request:     spec.Stdin,
			Limits:      backend.Limits,
			Hooks:       backend.Hooks,
			Metrics:     &observed.Metrics,
		})
		if linked.FailureDetail != nil {
			copy := *linked.FailureDetail
			observed.Metrics.Failure = &copy
		}
		return childResult{Stdout: linked.Wire, DirectReaps: 1}
	default:
		return childResult{Exit: 1}
	}
}

func cloneCommandSpec(spec commandSpec) commandSpec {
	return commandSpec{
		Dir:   spec.Dir,
		Argv:  append([]string(nil), spec.Argv...),
		Env:   append([]string(nil), spec.Env...),
		Stdin: append([]byte(nil), spec.Stdin...),
	}
}

func successfulTestWorkspace(base string) func(string, *observation) (privateWorkspace, *failure) {
	return func(projectRoot string, observed *observation) (privateWorkspace, *failure) {
		parent, _, parentErr := openRetainedPhysicalDirectory(base)
		if parentErr != nil {
			return privateWorkspace{}, fail("migration_project_build_error", "project_temporary_storage_failed")
		}
		root, err := os.MkdirTemp(base, "godj-projectcheck-")
		if err != nil {
			_ = parent.Close()
			return privateWorkspace{}, fail("migration_project_build_error", "project_temporary_storage_failed")
		}
		if err := os.Chmod(root, 0o700); err != nil {
			_ = os.RemoveAll(root)
			_ = parent.Close()
			return privateWorkspace{}, fail("migration_project_build_error", "project_temporary_storage_failed")
		}
		if samePhysicalPath(root, projectRoot) || pathWithin(root, projectRoot) {
			_ = os.RemoveAll(root)
			_ = parent.Close()
			return privateWorkspace{}, fail("migration_project_build_error", "project_temporary_storage_failed")
		}
		observed.Feasibility.TempCreated++
		name := filepath.Base(root)
		return privateWorkspace{
			Root: root,
			Env:  []string{"GOWORK=off", "GOTOOLCHAIN=local", "GOENV=off", "GOFLAGS=", "GOCACHEPROG="},
			Cleanup: func() error {
				return removeAndVerifyRetained(parent, name, root)
			},
		}, nil
	}
}

func removeAndVerifyRetained(parent *os.File, name, path string) error {
	removeErr := os.RemoveAll(path)
	var current unix.Stat_t
	postErr := unix.Fstatat(int(parent.Fd()), name, &current, unix.AT_SYMLINK_NOFOLLOW)
	closeErr := parent.Close()
	if removeErr != nil {
		return removeErr
	}
	if postErr == nil {
		return errors.New("workspace remains in retained parent")
	}
	if !errors.Is(postErr, syscall.ENOENT) {
		return postErr
	}
	return closeErr
}

func pathWithin(candidate, parent string) bool {
	relative, err := filepath.Rel(parent, candidate)
	return err == nil && relative != "." && relative != ".." && !filepath.IsAbs(relative) && !bytes.HasPrefix([]byte(relative), []byte(".."+string(filepath.Separator)))
}
