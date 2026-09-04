//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const ownedProcessGrace = 2 * time.Second

var isolatedEnvironmentKeys = map[string]struct{}{
	"TMPDIR": {}, "GOWORK": {}, "GOTOOLCHAIN": {}, "GOFLAGS": {}, "GOENV": {},
	"GOCACHE": {}, "GOCACHEPROG": {}, "GOMODCACHE": {}, "GOTMPDIR": {}, "HOME": {},
	"XDG_CONFIG_HOME": {}, "XDG_CACHE_HOME": {}, "TEST_TELEMETRY_DIR": {},
}

func createPrivateWorkspace(projectRoot string, ambient []string, observed *observation) (privateWorkspace, *failure) {
	ambientMap := environmentMap(ambient)
	tempBase, primary := resolveTempBase(ambientMap)
	if primary != nil {
		return privateWorkspace{}, primary
	}
	protected := make([]string, 0, 3)
	for _, key := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME"} {
		value := ambientMap[key]
		if key == "HOME" && value == "" {
			return privateWorkspace{}, fail("migration_project_build_error", "project_temporary_storage_failed")
		}
		if value == "" {
			continue
		}
		physical, err := physicalRealDirectory(value)
		if err != nil {
			return privateWorkspace{}, fail("migration_project_build_error", "project_temporary_storage_failed")
		}
		protected = append(protected, physical)
	}
	physicalProject, err := physicalRealDirectory(projectRoot)
	if err != nil {
		return privateWorkspace{}, fail("migration_project_build_error", "project_temporary_storage_failed")
	}
	physicalBase, err := physicalRealDirectory(tempBase)
	if err != nil || sameOrDescendant(physicalBase, physicalProject) {
		return privateWorkspace{}, fail("migration_project_build_error", "project_temporary_storage_failed")
	}
	for _, root := range protected {
		if sameOrDescendant(physicalBase, root) {
			return privateWorkspace{}, fail("migration_project_build_error", "project_temporary_storage_failed")
		}
	}
	baseHandle, _, baseErr := openRetainedPhysicalDirectory(physicalBase)
	if baseErr != nil {
		return privateWorkspace{}, fail("migration_project_build_error", "project_temporary_storage_failed")
	}
	root, err := os.MkdirTemp(physicalBase, "godj-projectcheck-")
	if err != nil || os.Chmod(root, 0o700) != nil {
		if root != "" {
			_ = os.RemoveAll(root)
		}
		_ = baseHandle.Close()
		return privateWorkspace{}, fail("migration_project_build_error", "project_temporary_storage_failed")
	}
	if sameOrDescendant(root, physicalProject) {
		_ = os.RemoveAll(root)
		_ = baseHandle.Close()
		return privateWorkspace{}, fail("migration_project_build_error", "project_temporary_storage_failed")
	}
	for _, protectedRoot := range protected {
		if sameOrDescendant(root, protectedRoot) {
			_ = os.RemoveAll(root)
			_ = baseHandle.Close()
			return privateWorkspace{}, fail("migration_project_build_error", "project_temporary_storage_failed")
		}
	}
	paths := map[string]string{
		"TMPDIR":             "tmp",
		"GOTMPDIR":           "gotmp",
		"GOCACHE":            "gocache",
		"GOMODCACHE":         "gomodcache",
		"HOME":               "home",
		"XDG_CONFIG_HOME":    "xdg-config",
		"XDG_CACHE_HOME":     "xdg-cache",
		"TEST_TELEMETRY_DIR": "telemetry",
	}
	for _, relative := range paths {
		directory := filepath.Join(root, relative)
		if err := os.Mkdir(directory, 0o700); err != nil {
			_ = os.RemoveAll(root)
			_ = baseHandle.Close()
			return privateWorkspace{}, fail("migration_project_build_error", "project_temporary_storage_failed")
		}
	}
	child := make(map[string]string)
	for key, value := range ambientMap {
		if _, isolated := isolatedEnvironmentKeys[key]; !isolated {
			child[key] = value
		}
	}
	child["GOWORK"] = "off"
	child["GOTOOLCHAIN"] = "local"
	child["GOENV"] = "off"
	child["GOFLAGS"] = ""
	child["GOCACHEPROG"] = ""
	for key, relative := range paths {
		child[key] = filepath.Join(root, relative)
	}
	observed.Feasibility.TempCreated++
	rootName := filepath.Base(root)
	return privateWorkspace{
		Root: root,
		Env:  sortedEnvironment(child),
		Cleanup: func() error {
			return removeAndVerifyRetained(baseHandle, rootName, root)
		},
	}, nil
}

func resolveTempBase(ambient map[string]string) (string, *failure) {
	candidate := ambient["TMPDIR"]
	if candidate == "" {
		candidate = os.TempDir()
	}
	physical, err := physicalRealDirectory(candidate)
	if err != nil {
		return "", fail("migration_project_build_error", "project_temporary_storage_failed")
	}
	return physical, nil
}

func environmentMap(environment []string) map[string]string {
	result := make(map[string]string)
	for _, entry := range environment {
		key, value, exists := strings.Cut(entry, "=")
		if exists {
			result[key] = value
		}
	}
	return result
}

func sortedEnvironment(environment map[string]string) []string {
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, len(keys))
	for index, key := range keys {
		result[index] = key + "=" + environment[key]
	}
	return result
}

func physicalRealDirectory(candidate string) (string, error) {
	initial, err := os.Lstat(candidate)
	if err != nil || initial.Mode()&os.ModeSymlink != 0 || !initial.IsDir() {
		return "", fmt.Errorf("not a real directory")
	}
	physical, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(physical)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("not a physical directory")
	}
	return physical, nil
}

func sameOrDescendant(candidate, parent string) bool {
	if candidate == parent {
		return true
	}
	relative, err := filepath.Rel(parent, candidate)
	return err == nil && relative != ".." && !filepath.IsAbs(relative) && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

type ownedProcessInput struct {
	Context       context.Context
	Interrupt     <-chan struct{}
	Spec          commandSpec
	StdoutMaximum int
	StderrMaximum int
	RetainStdout  bool
	Grace         time.Duration
	afterWait     func()
	afterStdout   func()
	signalGroup   func(int, unix.Signal) error
	closeRead     func(*os.File) error
}

type firstWriteObserver struct {
	target  io.Writer
	once    sync.Once
	observe func()
}

func (writer *firstWriteObserver) Write(payload []byte) (int, error) {
	written, err := writer.target.Write(payload)
	if written > 0 && writer.observe != nil {
		writer.once.Do(writer.observe)
	}
	return written, err
}

type ownedProcessObservation struct {
	Result          childResult
	Failure         *failure
	PID             int
	StdoutScalar    diagnosticScalar
	StderrScalar    diagnosticScalar
	SIGINTAttempts  int
	SIGKILLAttempts int
	DirectReaps     int
	DrainersJoined  int
	LaunchFailed    bool
	CleanupFailed   bool
	RawDiscarded    bool
}

func runOwnedProcess(input ownedProcessInput) ownedProcessObservation {
	observed := ownedProcessObservation{}
	if input.Context == nil {
		input.Context = context.Background()
	}
	if input.Grace == 0 {
		input.Grace = ownedProcessGrace
	}
	if input.signalGroup == nil {
		input.signalGroup = func(processGroup int, signal unix.Signal) error {
			return unix.Kill(processGroup, signal)
		}
	}
	if input.closeRead == nil {
		input.closeRead = func(file *os.File) error { return file.Close() }
	}
	command := exec.Command(input.Spec.Argv[0], input.Spec.Argv[1:]...)
	command.Dir = input.Spec.Dir
	command.Env = input.Spec.Env
	command.Stdin = bytes.NewReader(input.Spec.Stdin)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		observed.LaunchFailed = true
		return observed
	}
	stderr, stderrWriter, err := os.Pipe()
	if err != nil {
		stdout.Close()
		stdoutWriter.Close()
		observed.LaunchFailed = true
		return observed
	}
	command.Stdout = stdoutWriter
	command.Stderr = stderrWriter
	if err := command.Start(); err != nil {
		stdout.Close()
		stdoutWriter.Close()
		stderr.Close()
		stderrWriter.Close()
		observed.LaunchFailed = true
		return observed
	}
	_ = stdoutWriter.Close()
	_ = stderrWriter.Close()
	observed.PID = command.Process.Pid
	stdoutCapture := newCappedCapture(input.StdoutMaximum)
	stderrCapture := newCappedCapture(input.StderrMaximum)
	drainContext, stopDrain := context.WithCancel(context.Background())
	defer stopDrain()
	var drainGroup sync.WaitGroup
	drainErrors := make(chan error, 2)
	drainGroup.Add(2)
	go func() {
		defer drainGroup.Done()
		destination := io.Writer(stdoutCapture)
		if input.afterStdout != nil {
			destination = &firstWriteObserver{target: stdoutCapture, observe: input.afterStdout}
		}
		drainErrors <- drainInto(drainContext, stdout, destination)
	}()
	go func() {
		defer drainGroup.Done()
		drainErrors <- drainInto(drainContext, stderr, stderrCapture)
	}()
	drainersDone := make(chan struct{})
	go func() {
		drainGroup.Wait()
		close(drainersDone)
	}()
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()

	interruptReady := func() bool {
		if input.Interrupt == nil {
			return false
		}
		select {
		case <-input.Interrupt:
			return true
		default:
			return false
		}
	}
	var waitErr error
	var cancellation *failure
	waitReceived := false
	drainersJoined := false
	sampleCancellation := func() *failure {
		if interruptReady() {
			return fail("migration_project_process_error", "project_interrupted")
		}
		if input.Context.Err() != nil {
			return fail("migration_project_process_error", "project_canceled")
		}
		return nil
	}
	for cancellation == nil && !(waitReceived && drainersJoined) {
		if ready := sampleCancellation(); ready != nil {
			cancellation = ready
			break
		}
		select {
		case waitErr = <-waited:
			waitReceived = true
			if input.afterWait != nil {
				input.afterWait()
			}
			cancellation = sampleCancellation()
		case <-drainersDone:
			drainersJoined = true
			cancellation = sampleCancellation()
		case <-input.Interrupt:
			cancellation = fail("migration_project_process_error", "project_interrupted")
		case <-input.Context.Done():
			cancellation = sampleCancellation()
		}
	}
	if cancellation != nil && !waitReceived {
		observed.SIGINTAttempts++
		if err := input.signalGroup(-command.Process.Pid, unix.SIGINT); err != nil && !errors.Is(err, syscall.ESRCH) {
			observed.CleanupFailed = true
		}
		timer := time.NewTimer(input.Grace)
		<-timer.C
		observed.SIGKILLAttempts++
		if err := input.signalGroup(-command.Process.Pid, unix.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			observed.CleanupFailed = true
		}
		stopDrain()
		if err := input.closeRead(stdout); err != nil {
			observed.CleanupFailed = true
		}
		if err := input.closeRead(stderr); err != nil {
			observed.CleanupFailed = true
		}
		waitErr = <-waited
		waitReceived = true
	} else if cancellation != nil {
		stopDrain()
		if err := input.closeRead(stdout); err != nil {
			observed.CleanupFailed = true
		}
		if err := input.closeRead(stderr); err != nil {
			observed.CleanupFailed = true
		}
	}
	if cancellation != nil && !drainersJoined {
		<-drainersDone
		drainersJoined = true
	}
	if cancellation != nil && observed.Failure == nil {
		observed.Failure = cancellation
	}
	if !drainersJoined {
		<-drainersDone
	}
	if cancellation == nil {
		if err := input.closeRead(stdout); err != nil {
			observed.CleanupFailed = true
		}
		if err := input.closeRead(stderr); err != nil {
			observed.CleanupFailed = true
		}
	}
	for index := 0; index < 2; index++ {
		drainErr := <-drainErrors
		if drainErr != nil && !(cancellation != nil && (errors.Is(drainErr, context.Canceled) || errors.Is(drainErr, os.ErrClosed))) {
			observed.CleanupFailed = true
		}
	}
	observed.DrainersJoined = 2
	observed.DirectReaps = 1
	retainStdout := input.RetainStdout && cancellation == nil && !observed.CleanupFailed && waitErr == nil
	var stdoutPrefix []byte
	var stdoutScalar diagnosticScalar
	if retainStdout {
		stdoutPrefix, stdoutScalar = stdoutCapture.takeAndDiscard()
	} else {
		stdoutScalar = stdoutCapture.snapshotAndDiscard()
	}
	stderrScalar := stderrCapture.snapshotAndDiscard()
	observed.StdoutScalar = stdoutScalar
	observed.StderrScalar = stderrScalar
	observed.RawDiscarded = true
	if retainStdout {
		observed.Result.Stdout = stdoutPrefix
	}
	observed.Result.Stderr = nil
	if waitErr == nil {
		observed.Result.Exit = 0
	} else {
		var exitError *exec.ExitError
		if errors.As(waitErr, &exitError) {
			observed.Result.Exit = exitError.ExitCode()
		} else {
			observed.Result.Exit = 1
		}
	}
	return observed
}

func TestProjectCheckOwnedProcessHelper(t *testing.T) {
	mode := os.Getenv("GODJ_PROJECTCHECK_HELPER")
	if mode == "" {
		return
	}
	switch mode {
	case "emit":
		stdoutBytes, _ := strconv.Atoi(os.Getenv("GODJ_HELPER_STDOUT"))
		stderrBytes, _ := strconv.Atoi(os.Getenv("GODJ_HELPER_STDERR"))
		_, _ = io.CopyN(os.Stdout, zeroReader{}, int64(stdoutBytes))
		_, _ = io.CopyN(os.Stderr, oneReader{}, int64(stderrBytes))
	case "ignore":
		signalIgnoreInterrupt()
		_, _ = io.WriteString(os.Stdout, os.Getenv("GODJ_HELPER_PREFIX"))
		if ready := os.Getenv("GODJ_HELPER_READY"); ready != "" {
			_ = os.WriteFile(ready, []byte(strconv.Itoa(os.Getpid())), 0o600)
		}
		for {
			time.Sleep(time.Second)
		}
	case "exit":
		code, _ := strconv.Atoi(os.Getenv("GODJ_HELPER_EXIT"))
		os.Exit(code)
	case "wire-padding":
		_, _ = io.WriteString(os.Stdout, os.Getenv("GODJ_HELPER_WIRE"))
		padding, _ := strconv.Atoi(os.Getenv("GODJ_HELPER_PADDING"))
		_, _ = io.CopyN(os.Stdout, zeroReader{}, int64(padding))
		if code, _ := strconv.Atoi(os.Getenv("GODJ_HELPER_EXIT")); code != 0 {
			os.Exit(code)
		}
	case "spawn-fd-holder":
		environment := environmentMap(os.Environ())
		environment["GODJ_PROJECTCHECK_HELPER"] = "hold-fd"
		child := exec.Command(os.Args[0], "-test.run=^TestProjectCheckOwnedProcessHelper$")
		child.Env = sortedEnvironment(environment)
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(96)
		}
		if ready := os.Getenv("GODJ_HELPER_READY"); ready != "" {
			_ = os.WriteFile(ready, []byte(strconv.Itoa(child.Process.Pid)), 0o600)
		}
	case "hold-fd":
		for {
			time.Sleep(time.Second)
		}
	default:
		os.Exit(97)
	}
}

func signalIgnoreInterrupt() {
	signal.Ignore(os.Interrupt)
}

type zeroReader struct{}

func (zeroReader) Read(payload []byte) (int, error) {
	for index := range payload {
		payload[index] = 'x'
	}
	return len(payload), nil
}

type oneReader struct{}

func (oneReader) Read(payload []byte) (int, error) {
	for index := range payload {
		payload[index] = 'y'
	}
	return len(payload), nil
}

func helperSpec(mode string, extra map[string]string) commandSpec {
	environment := environmentMap(os.Environ())
	environment["GODJ_PROJECTCHECK_HELPER"] = mode
	for key, value := range extra {
		environment[key] = value
	}
	return commandSpec{
		Argv: []string{os.Args[0], "-test.run=^TestProjectCheckOwnedProcessHelper$"},
		Env:  sortedEnvironment(environment),
	}
}

type concreteExecBackend struct {
	Limits            limits
	Interrupt         <-chan struct{}
	Grace             time.Duration
	LastBuild         commandSpec
	LastRunner        commandSpec
	afterRunnerStdout func()
}

func (backend *concreteExecBackend) Run(ctx context.Context, stage string, spec commandSpec, observed *observation) childResult {
	input := ownedProcessInput{
		Context:       ctx,
		Interrupt:     backend.Interrupt,
		Spec:          spec,
		StderrMaximum: backend.Limits.diagnosticBytes,
		Grace:         backend.Grace,
	}
	switch stage {
	case "build":
		backend.LastBuild = cloneCommandSpec(spec)
		input.StdoutMaximum = backend.Limits.diagnosticBytes
	case "runner":
		backend.LastRunner = cloneCommandSpec(spec)
		input.StdoutMaximum = backend.Limits.responseBytes
		input.RetainStdout = true
		input.afterStdout = backend.afterRunnerStdout
	default:
		return childResult{Exit: 1}
	}
	process := runOwnedProcess(input)
	if process.CleanupFailed {
		observed.Feasibility.CleanupFailed = 1
	}
	observed.Feasibility.GroupSIGINTAttempts += process.SIGINTAttempts
	observed.Feasibility.GroupSIGKILLAttempts += process.SIGKILLAttempts
	if stage == "build" {
		observed.Feasibility.Diagnostics["build_stdout"] = process.StdoutScalar
		observed.Feasibility.Diagnostics["build_stderr"] = process.StderrScalar
		primary := process.Failure
		if process.LaunchFailed {
			primary = fail("migration_project_build_error", "project_build_failed")
		}
		return childResult{Exit: process.Result.Exit, Failure: primary, DirectReaps: process.DirectReaps, CleanupFailed: process.CleanupFailed}
	}
	observed.Feasibility.Diagnostics["runner_stderr"] = process.StderrScalar
	if process.StdoutScalar.RetainedBytes != 0 || process.StdoutScalar.Truncated {
		observed.Metrics.RunnerResponseWrites++
	}
	primary := process.Failure
	if process.LaunchFailed {
		primary = fail("migration_project_protocol_error", "project_runner_failed")
	}
	return childResult{Stdout: process.Result.Stdout, Exit: process.Result.Exit, StdoutOverflow: process.StdoutScalar.Truncated, Failure: primary, DirectReaps: process.DirectReaps, CleanupFailed: process.CleanupFailed}
}

func TestPrivateWorkspaceEnvironmentAndContainment(t *testing.T) {
	t.Parallel()
	universe := t.TempDir()
	project := filepath.Join(universe, "project")
	home := filepath.Join(universe, "home-original")
	config := filepath.Join(universe, "config-original")
	cache := filepath.Join(universe, "cache-original")
	tempBase := filepath.Join(universe, "tmp-base")
	for _, directory := range []string{project, home, config, cache, tempBase} {
		mustMkdir(t, directory)
	}
	ambient := []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + home, "XDG_CONFIG_HOME=" + config, "XDG_CACHE_HOME=" + cache,
		"TMPDIR=" + tempBase, "GOWORK=ambient", "GOTOOLCHAIN=auto", "GOFLAGS=-mod=mod",
		"GOENV=ambient", "GOCACHE=ambient", "GOCACHEPROG=ambient", "GOMODCACHE=ambient", "GOTMPDIR=ambient",
		"TEST_TELEMETRY_DIR=ambient", "NETRC=/explicit/netrc", "GOTELEMETRY=ambient-value",
	}
	observed := observation{Feasibility: feasibilityMetrics{Diagnostics: map[string]diagnosticScalar{}}}
	workspace, primary := createPrivateWorkspace(project, ambient, &observed)
	if primary != nil {
		t.Fatalf("create private workspace: %v", primary)
	}
	environment := environmentMap(workspace.Env)
	if environment["GOWORK"] != "off" || environment["GOTOOLCHAIN"] != "local" || environment["GOENV"] != "off" || environment["GOFLAGS"] != "" || environment["GOCACHEPROG"] != "" {
		t.Fatalf("fixed Go environment = %#v", environment)
	}
	if environment["NETRC"] != "/explicit/netrc" || environment["GOTELEMETRY"] != "ambient-value" {
		t.Fatalf("admitted explicit environment was silently scrubbed: %#v", environment)
	}
	for _, key := range []string{"TMPDIR", "GOTMPDIR", "GOCACHE", "GOMODCACHE", "HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "TEST_TELEMETRY_DIR"} {
		value := environment[key]
		if !sameOrDescendant(value, workspace.Root) {
			t.Fatalf("%s = %q outside private root %q", key, value, workspace.Root)
		}
		info, err := os.Stat(value)
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Fatalf("%s private directory mode = %v, %v", key, info, err)
		}
	}
	rootInfo, err := os.Stat(workspace.Root)
	if err != nil || rootInfo.Mode().Perm() != 0o700 || observed.Feasibility.TempCreated != 1 {
		t.Fatalf("private root = %v, %v metrics=%+v", rootInfo, err, observed.Feasibility)
	}
	if err := workspace.Cleanup(); err != nil {
		t.Fatalf("cleanup private workspace: %v", err)
	}
	if _, err := os.Stat(workspace.Root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("private root remained: %v", err)
	}

	rejected := observation{Feasibility: feasibilityMetrics{Diagnostics: map[string]diagnosticScalar{}}}
	unsafeAmbient := append([]string(nil), ambient...)
	for index := range unsafeAmbient {
		if strings.HasPrefix(unsafeAmbient[index], "TMPDIR=") {
			unsafeAmbient[index] = "TMPDIR=" + home
		}
	}
	if _, primary := createPrivateWorkspace(project, unsafeAmbient, &rejected); primary == nil || primary.Code != "project_temporary_storage_failed" || rejected.Feasibility.TempCreated != 0 {
		t.Fatalf("HOME temp containment = %v metrics=%+v", primary, rejected.Feasibility)
	}
	symlinkBase := filepath.Join(universe, "temp-link")
	if err := os.Symlink(tempBase, symlinkBase); err != nil {
		t.Fatal(err)
	}
	symlinkAmbient := append([]string(nil), ambient...)
	for index := range symlinkAmbient {
		if strings.HasPrefix(symlinkAmbient[index], "TMPDIR=") {
			symlinkAmbient[index] = "TMPDIR=" + symlinkBase
		}
	}
	if _, primary := createPrivateWorkspace(project, symlinkAmbient, &rejected); primary == nil || primary.Code != "project_temporary_storage_failed" {
		t.Fatalf("symlink temp base = %v", primary)
	}
}

func TestWorkspaceCleanupRequiresAbsenceFromRetainedParent(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		factory func(project, base, home string, observed *observation) (privateWorkspace, *failure)
	}{
		{
			name: "concrete",
			factory: func(project, base, home string, observed *observation) (privateWorkspace, *failure) {
				ambient := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "TMPDIR=" + base}
				return createPrivateWorkspace(project, ambient, observed)
			},
		},
		{
			name: "in-process-harness",
			factory: func(project, base, _ string, observed *observation) (privateWorkspace, *failure) {
				return successfulTestWorkspace(base)(project, observed)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			universe := t.TempDir()
			project := filepath.Join(universe, "project")
			base := filepath.Join(universe, "base")
			home := filepath.Join(universe, "home")
			for _, directory := range []string{project, base, home} {
				mustMkdir(t, directory)
			}
			observed := observation{Feasibility: feasibilityMetrics{Diagnostics: map[string]diagnosticScalar{}}}
			workspace, primary := test.factory(project, base, home, &observed)
			if primary != nil {
				t.Fatalf("create workspace: %v", primary)
			}
			rootName := filepath.Base(workspace.Root)
			movedBase := base + "-moved"
			if err := os.Rename(base, movedBase); err != nil {
				t.Fatal(err)
			}
			mustMkdir(t, base)
			if err := workspace.Cleanup(); err == nil {
				t.Fatal("cleanup reported success while the private root remained in its retained parent")
			}
			retainedRoot := filepath.Join(movedBase, rootName)
			if info, err := os.Stat(retainedRoot); err != nil || !info.IsDir() {
				t.Fatalf("retained private root evidence = %v, %v", info, err)
			}
			if err := os.RemoveAll(movedBase); err != nil {
				t.Fatalf("remove retained test fixture: %v", err)
			}
		})
	}
}

func TestConcreteBackendMapsLaunchFailureByStageAndDoesNotClaimReap(t *testing.T) {
	t.Parallel()
	backend := &concreteExecBackend{Limits: contractLimits(), Grace: 40 * time.Millisecond}
	spec := commandSpec{Argv: []string{filepath.Join(t.TempDir(), "missing-executable")}, Env: os.Environ()}
	for _, test := range []struct {
		stage    string
		category string
		code     string
	}{
		{"build", "migration_project_build_error", "project_build_failed"},
		{"runner", "migration_project_protocol_error", "project_runner_failed"},
	} {
		observed := observation{Feasibility: feasibilityMetrics{Diagnostics: map[string]diagnosticScalar{}}}
		result := backend.Run(context.Background(), test.stage, spec, &observed)
		if result.Failure == nil || result.Failure.Category != test.category || result.Failure.Code != test.code || result.DirectReaps != 0 {
			t.Fatalf("%s launch failure = result=%+v feasibility=%+v", test.stage, result, observed.Feasibility)
		}
	}
	direct := runOwnedProcess(ownedProcessInput{Context: context.Background(), Spec: spec, StdoutMaximum: 1, StderrMaximum: 1})
	if !direct.LaunchFailed || direct.DirectReaps != 0 || direct.PID != 0 {
		t.Fatalf("launch failure process observation = %+v", direct)
	}
}

func TestActualBuildPrivateRunnerMagicArgvAndDefinitionLoadEndToEnd(t *testing.T) {
	repositoryRoot := repositoryRoot(t)
	project := t.TempDir()
	tempBase := t.TempDir()
	home := t.TempDir()
	writeDescriptor(t, project)
	writeDefinition(t, project, "migrations/0001_initial.godj.json", oneCreateModelDocument())
	mustMkdir(t, filepath.Join(project, "cmd", "mysite"))
	goMod := "module example.invalid/projectcheck-fixture\n\ngo 1.26.0\n\nrequire github.com/progresshans/godj v0.0.0\n\nreplace github.com/progresshans/godj => " + repositoryRoot + "\n"
	if err := os.WriteFile(filepath.Join(project, "go.mod"), []byte(goMod), 0o600); err != nil {
		t.Fatal(err)
	}
	mainSource := `package main

import (
	"fmt"
	"io"
	"os"

	"github.com/progresshans/godj/migrations/definition"
)

func main() {
	if len(os.Args) != 2 || os.Args[1] != "__godj_project_runner_v1" {
		os.Exit(9)
	}
	request, err := io.ReadAll(os.Stdin)
	if err != nil || string(request) != "{\"protocol_version\":1,\"command\":\"migrations.check\"}" {
		fmt.Print("{\"protocol_version\":1,\"status\":\"error\",\"error\":{\"category\":\"migration_project_protocol_error\",\"code\":\"invalid_project_runner_request\"}}")
		return
	}
	document, err := os.ReadFile("migrations/0001_initial.godj.json")
	if err != nil {
		os.Exit(8)
	}
	set, _, err := definition.Load(definition.Source{SourceID: "migrations/0001_initial.godj.json", Document: document})
	if err != nil {
		os.Exit(7)
	}
	fmt.Printf("{\"protocol_version\":1,\"status\":\"ok\",\"result\":{\"source_count\":1,\"definition_count\":%d,\"definition_set_digest\":%q}}", len(set.Definitions()), set.Digest())
}
`
	parsedMain, err := parser.ParseFile(token.NewFileSet(), "main.go", mainSource, 0)
	if err != nil {
		t.Fatal(err)
	}
	loadCalls := 0
	ast.Inspect(parsedMain, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		identifier, identifierOK := selector.X.(*ast.Ident)
		if identifierOK && identifier.Name == "definition" && selector.Sel.Name == "Load" {
			loadCalls++
		}
		return true
	})
	if loadCalls != 1 {
		t.Fatalf("generated linked runner definition.Load calls = %d, want 1", loadCalls)
	}
	if err := os.WriteFile(filepath.Join(project, "cmd", "mysite", "main.go"), []byte(mainSource), 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshotProjectTree(t, project)
	lim := contractLimits()
	backend := &concreteExecBackend{Limits: lim, Grace: 100 * time.Millisecond}
	ambient := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "TMPDIR=" + tempBase}
	observed := runGlobal(globalInvocation{
		Context: context.Background(), CWD: project, Argv: []string{"migrations", "check"}, Limits: lim,
		Deps: globalDependencies{
			Backend: backend,
			CreateWorkspace: func(projectRoot string, observed *observation) (privateWorkspace, *failure) {
				return createPrivateWorkspace(projectRoot, ambient, observed)
			},
		},
	})
	if observed.Failure != nil || observed.Result == nil || observed.Result.DefinitionSetDigest != oneModelDigest || observed.Result.DefinitionCount != 1 || observed.Metrics.BuildCalls != 1 || observed.Metrics.RunnerCalls != 1 || observed.Metrics.RunnerResponseWrites != 1 {
		t.Fatalf("actual end-to-end observation = result=%+v failure=%v metrics=%+v feasibility=%+v stderr=%q", observed.Result, observed.Failure, observed.Metrics, observed.Feasibility, observed.Stderr)
	}
	wantBuildPrefix := []string{"go", "build", "-mod=readonly", "-o"}
	physicalProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(backend.LastBuild.Argv) != 6 || !reflect.DeepEqual(backend.LastBuild.Argv[:4], wantBuildPrefix) || backend.LastBuild.Argv[5] != "./cmd/mysite" || backend.LastBuild.Dir != physicalProject {
		t.Fatalf("actual build spec = %+v", backend.LastBuild)
	}
	if len(backend.LastRunner.Argv) != 2 || backend.LastRunner.Argv[1] != "__godj_project_runner_v1" || backend.LastRunner.Dir != physicalProject || string(backend.LastRunner.Stdin) != `{"protocol_version":1,"command":"migrations.check"}` {
		t.Fatalf("actual runner spec = %+v", backend.LastRunner)
	}
	privateRoot := filepath.Dir(backend.LastRunner.Argv[0])
	if _, err := os.Stat(privateRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("private runner root remained after cleanup: %s: %v", privateRoot, err)
	}
	if observed.Feasibility.RawDiagnostics != nil || observed.Feasibility.Diagnostics["build_stdout"].RetainedBytes != 0 || observed.Feasibility.Diagnostics["runner_stderr"].RetainedBytes != 0 {
		t.Fatalf("diagnostic raw/counters = %+v", observed.Feasibility)
	}
	after := snapshotProjectTree(t, project)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("-mod=readonly build rewrote project tree\nbefore=%v\nafter=%v", before, after)
	}
	if _, err := os.Stat(filepath.Join(project, "go.sum")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("go build published go.sum: %v", err)
	}
}

func snapshotProjectTree(t *testing.T, root string) map[string]string {
	t.Helper()
	result := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			result[filepath.ToSlash(relative)+"/"] = info.Mode().String()
			return nil
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(relative)] = fmt.Sprintf("%s:%x", info.Mode().String(), sha256.Sum256(payload))
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot project tree: %v", err)
	}
	return result
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	working, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join(working, "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("resolve repository root %s: %v", root, err)
	}
	return root
}

func TestOwnedProcessCapsAndDrainsEachStream(t *testing.T) {
	t.Parallel()
	const maximum = 4 << 10
	observed := runOwnedProcess(ownedProcessInput{
		Context:       context.Background(),
		Spec:          helperSpec("emit", map[string]string{"GODJ_HELPER_STDOUT": strconv.Itoa(maximum + 17), "GODJ_HELPER_STDERR": strconv.Itoa(maximum + 31)}),
		StdoutMaximum: maximum,
		StderrMaximum: maximum,
		RetainStdout:  true,
		Grace:         40 * time.Millisecond,
	})
	if observed.Failure != nil || observed.Result.Exit != 0 || observed.StdoutScalar != (diagnosticScalar{RetainedBytes: maximum, Truncated: true}) || observed.StderrScalar != (diagnosticScalar{RetainedBytes: maximum, Truncated: true}) {
		t.Fatalf("capped process = %+v", observed)
	}
	if len(observed.Result.Stdout) != maximum || observed.Result.Stderr != nil || observed.DirectReaps != 1 || observed.DrainersJoined != 2 || !observed.RawDiscarded {
		t.Fatalf("drain/reap observation = %+v", observed)
	}
}

func TestBuildDiagnosticsJoinFinalizeAndDiscardWithoutRawCopy(t *testing.T) {
	t.Parallel()
	const maximum = 32
	observed := runOwnedProcess(ownedProcessInput{
		Context:       context.Background(),
		Spec:          helperSpec("emit", map[string]string{"GODJ_HELPER_STDOUT": "17", "GODJ_HELPER_STDERR": "19"}),
		StdoutMaximum: maximum,
		StderrMaximum: maximum,
		RetainStdout:  false,
		Grace:         40 * time.Millisecond,
	})
	if observed.Failure != nil || observed.Result.Exit != 0 || observed.DrainersJoined != 2 || observed.DirectReaps != 1 || !observed.RawDiscarded {
		t.Fatalf("build diagnostic lifecycle = %+v", observed)
	}
	if observed.StdoutScalar.RetainedBytes < 17 || observed.StdoutScalar.RetainedBytes > maximum || observed.StdoutScalar.Truncated || observed.StderrScalar != (diagnosticScalar{RetainedBytes: 19}) || observed.Result.Stdout != nil || observed.Result.Stderr != nil {
		t.Fatalf("build diagnostics leaked raw bytes or lost scalar = %+v", observed)
	}
}

func TestOwnedProcessCancellationForwardsEscalatesReapsAndJoins(t *testing.T) {
	t.Parallel()
	ready := filepath.Join(t.TempDir(), "ready")
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan ownedProcessObservation, 1)
	go func() {
		result <- runOwnedProcess(ownedProcessInput{
			Context:       ctx,
			Spec:          helperSpec("ignore", map[string]string{"GODJ_HELPER_READY": ready}),
			StdoutMaximum: 1024,
			StderrMaximum: 1024,
			Grace:         40 * time.Millisecond,
		})
	}()
	waitForFile(t, ready)
	cancel()
	observed := <-result
	if observed.Failure == nil || observed.Failure.Code != "project_canceled" || observed.SIGINTAttempts != 1 || observed.SIGKILLAttempts != 1 || observed.DirectReaps != 1 || observed.DrainersJoined != 2 {
		t.Fatalf("cancel observation = %+v", observed)
	}
	if err := unix.Kill(observed.PID, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("direct child %d was not reaped: %v", observed.PID, err)
	}
}

func TestProcessFinalizationFailureOverridesCancelButNotOrdinaryPrimary(t *testing.T) {
	t.Parallel()
	ready := filepath.Join(t.TempDir(), "ready")
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan ownedProcessObservation, 1)
	var signalCalls int
	var closeCalls int
	go func() {
		result <- runOwnedProcess(ownedProcessInput{
			Context:       ctx,
			Spec:          helperSpec("ignore", map[string]string{"GODJ_HELPER_READY": ready}),
			StdoutMaximum: 16,
			StderrMaximum: 16,
			Grace:         40 * time.Millisecond,
			signalGroup: func(group int, signal unix.Signal) error {
				signalCalls++
				actual := unix.Kill(group, signal)
				if signalCalls == 1 {
					return syscall.EPERM
				}
				return actual
			},
			closeRead: func(file *os.File) error {
				closeCalls++
				actual := file.Close()
				if closeCalls == 1 {
					return syscall.EIO
				}
				return actual
			},
		})
	}()
	waitForFile(t, ready)
	cancel()
	process := <-result
	if process.Failure == nil || process.Failure.Code != "project_canceled" || !process.CleanupFailed || process.DirectReaps != 1 || process.Result.Stdout != nil || process.Result.Stderr != nil {
		t.Fatalf("injected process finalization = %+v", process)
	}
	if combined := combineProcessFinalization(process.Failure, process.CleanupFailed); combined.Code != "project_cleanup_failed" {
		t.Fatalf("cleanup did not override cancellation: %v", combined)
	}
	ordinary := fail("migration_project_build_error", "project_build_failed")
	if combined := combineProcessFinalization(ordinary, true); combined.Code != "project_build_failed" {
		t.Fatalf("cleanup overrode ordinary primary: %v", combined)
	}
}

func TestRunnerResponseWriteMetricSurvivesCanceledRawDiscard(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	stdoutObserved := make(chan struct{})
	backend := &concreteExecBackend{
		Limits:            contractLimits(),
		Grace:             40 * time.Millisecond,
		afterRunnerStdout: func() { close(stdoutObserved) },
	}
	observed := &observation{Feasibility: feasibilityMetrics{Diagnostics: map[string]diagnosticScalar{}}}
	result := make(chan childResult, 1)
	go func() {
		result <- backend.Run(ctx, "runner", helperSpec("ignore", map[string]string{"GODJ_HELPER_PREFIX": "wire"}), observed)
	}()
	select {
	case <-stdoutObserved:
	case <-time.After(10 * time.Second):
		cancel()
		child := <-result
		t.Fatalf("runner stdout was not captured before cancellation: child=%+v", child)
	}
	cancel()
	child := <-result
	if child.Failure == nil || child.Failure.Code != "project_canceled" || child.Stdout != nil || observed.Metrics.RunnerResponseWrites != 1 || observed.Feasibility.Diagnostics["runner_stderr"].RetainedBytes != 0 {
		t.Fatalf("canceled runner response observation = child=%+v metrics=%+v feasibility=%+v", child, observed.Metrics, observed.Feasibility)
	}
}

func TestOwnedProcessHandledInterruptWinsReadyCancellation(t *testing.T) {
	t.Parallel()
	ready := filepath.Join(t.TempDir(), "ready")
	ctx, cancel := context.WithCancel(context.Background())
	interrupt := make(chan struct{})
	result := make(chan ownedProcessObservation, 1)
	go func() {
		result <- runOwnedProcess(ownedProcessInput{
			Context:       ctx,
			Interrupt:     interrupt,
			Spec:          helperSpec("ignore", map[string]string{"GODJ_HELPER_READY": ready}),
			StdoutMaximum: 1024,
			StderrMaximum: 1024,
			Grace:         40 * time.Millisecond,
		})
	}()
	waitForFile(t, ready)
	close(interrupt)
	cancel()
	observed := <-result
	if observed.Failure == nil || observed.Failure.Code != "project_interrupted" || observed.Failure.ExitCode != 130 {
		t.Fatalf("interrupt/cancel race = %+v", observed)
	}
}

func TestOwnedProcessWaitBarrierResamplesInterruptBeforeTerminalCommit(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	interrupt := make(chan struct{})
	observed := runOwnedProcess(ownedProcessInput{
		Context:       ctx,
		Interrupt:     interrupt,
		Spec:          helperSpec("exit", map[string]string{"GODJ_HELPER_EXIT": "0"}),
		StdoutMaximum: 16,
		StderrMaximum: 16,
		Grace:         40 * time.Millisecond,
		afterWait: func() {
			cancel()
			close(interrupt)
		},
	})
	if observed.Failure == nil || observed.Failure.Code != "project_interrupted" || observed.SIGINTAttempts != 0 || observed.SIGKILLAttempts != 0 || observed.DirectReaps != 1 {
		t.Fatalf("wait/cancel/interrupt barrier = %+v", observed)
	}
}

func TestCancellationAfterDirectReapClosesParentPipesAndJoinsDrainers(t *testing.T) {
	t.Parallel()
	ready := filepath.Join(t.TempDir(), "descendant-ready")
	ctx, cancel := context.WithCancel(context.Background())
	waitSeen := make(chan struct{})
	result := make(chan ownedProcessObservation, 1)
	go func() {
		result <- runOwnedProcess(ownedProcessInput{
			Context:       ctx,
			Spec:          helperSpec("spawn-fd-holder", map[string]string{"GODJ_HELPER_READY": ready}),
			StdoutMaximum: 16,
			StderrMaximum: 16,
			Grace:         40 * time.Millisecond,
			afterWait: func() {
				close(waitSeen)
			},
		})
	}()
	waitForFile(t, ready)
	<-waitSeen
	payload, err := os.ReadFile(ready)
	if err != nil {
		t.Fatal(err)
	}
	descendantPID, err := strconv.Atoi(string(payload))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Kill(descendantPID, unix.SIGKILL) })
	cancel()
	select {
	case observed := <-result:
		if observed.Failure == nil || observed.Failure.Code != "project_canceled" || observed.SIGINTAttempts != 0 || observed.SIGKILLAttempts != 0 || observed.DirectReaps != 1 || observed.DrainersJoined != 2 {
			t.Fatalf("post-reap cancellation = %+v", observed)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("post-reap cancellation remained blocked on descendant-held pipe")
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("helper did not publish readiness at %s", path)
}
