//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/progresshans/godj/internal/projectcheck/protocol"
	"github.com/progresshans/godj/version"
)

func TestStartProjectSelectsOnlyAnExplicitOrInstalledDependency(t *testing.T) {
	base := []string{"startproject", "example.com/site", "new-site"}
	installed := version.Info{Module: version.Module, Origin: version.ModuleVersion, Version: "v0.1.0"}
	selected, ok := parseStartProjectArguments(base, installed)
	if !ok || selected.project.FrameworkVersion != installed.Version || selected.project.FrameworkSource != "" {
		t.Fatal("fixed installed dependency was not selected")
	}
	for _, origin := range []version.Origin{version.Unknown, version.Development, version.LocalReplacement, version.ModuleReplacement} {
		installed.Origin = origin
		if _, ok := parseStartProjectArguments(base, installed); ok {
			t.Fatal("development/replacement source was inferred without explicit selection", origin)
		}
		for _, dependency := range [][]string{{"--framework-version", "v0.2.0"}, {"--framework-source", "../explicit-checkout"}} {
			args := append(append([]string(nil), base...), dependency...)
			before := append([]string(nil), args...)
			if _, ok := parseStartProjectArguments(args, installed); !ok || !reflect.DeepEqual(args, before) {
				t.Fatal("explicit dependency rejected or arguments changed")
			}
		}
	}
}

func TestStartProjectRejectsArgumentsBeforeFilesystemAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	interrupted := make(chan struct{})
	close(interrupted)
	for _, args := range [][]string{
		nil,
		{"startproject"},
		{"startproject", "../private-module", "target", "--framework-version", "v0.1.0"},
		{"startproject", "example.com/site", "--private-option", "--framework-version", "v0.1.0"},
		{"startproject", "example.com/site", "target", "--framework-version", "latest"},
		{"startproject", "example.com/site", "target", "--framework-source", "private\nsource"},
		{"startproject", "example.com/site", "target", "--unknown", "private-value"},
		{"startproject", "example.com/site", "target", "--framework-version", "v0.1.0", "extra"},
	} {
		backend := &scriptedBackend{}
		var stdout, stderr bytes.Buffer
		report := RunStartProject(StartProjectInvocation{
			Context: ctx, CWD: "relative-and-unavailable", Args: args, Stdout: &stdout, Stderr: &stderr,
			Environment: []string{"private=do-not-expose"}, Interrupt: interrupted, Backend: backend,
		})
		if report.ExitCode != 2 || report.FailureCode != "invalid_arguments_or_framework_dependency" || report.Publication.Published || len(backend.commands) != 0 || stdout.Len() != 0 || stderr.String() != "startproject_error/invalid_arguments_or_framework_dependency\n" {
			t.Fatal("invalid input reached project work or leaked its value")
		}
	}
}

func TestStartProjectRequiresOutputAndRejectsPreexistingCancellation(t *testing.T) {
	args := []string{"startproject", "example.com/site", "target", "--framework-version", "v0.1.0"}
	for _, mode := range []string{"no-output", "canceled", "interrupted"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			interrupt := make(chan struct{})
			var output io.Writer = io.Discard
			code, exit := "output_unavailable", 1
			switch mode {
			case "no-output":
				output = nil
			case "canceled":
				cancel()
				code, exit = protocol.CodeProjectCanceled, 130
			case "interrupted":
				close(interrupt)
				code, exit = protocol.CodeProjectInterrupted, 130
			}
			backend := &scriptedBackend{}
			report := RunStartProject(StartProjectInvocation{Context: ctx, CWD: "relative-and-unavailable", Args: args, Stdout: output, Stderr: io.Discard, Interrupt: interrupt, Backend: backend})
			if report.ExitCode != exit || report.FailureCode != code || len(backend.commands) != 0 || report.Publication.Published {
				t.Fatal("pre-admission failure reached filesystem work", report)
			}
		})
	}
}

func TestStartProjectBuildFailurePreservesExistingFilesAndCleansCandidate(t *testing.T) {
	fixture := newGlobalFixture(t, 0)
	before, err := os.ReadFile(filepath.Join(fixture.project, "godj.toml"))
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("private-build-output")
	backend := &scriptedBackend{build: ProcessResult{Started: true, ExitCode: 1, DirectReaps: 1, Stdout: raw, BuildDiagnostic: "candidate build rejected"}}
	var stdout, stderr bytes.Buffer
	report := RunStartProject(StartProjectInvocation{
		Context: t.Context(), CWD: fixture.project, Args: []string{"startproject", "example.com/site", "target", "--framework-version", "v0.1.0"},
		Environment: fixture.environment, Stdout: &stdout, Stderr: &stderr, Backend: backend,
	})
	if report.ExitCode != 1 || report.FailureCode != "candidate_build_failed" || report.Publication.Published || stdout.Len() != 0 || stderr.String() != "startproject_error/candidate_build_failed\ncandidate build rejected\n" || len(backend.commands) != 1 {
		t.Fatal("failed candidate returned a created result", report)
	}
	command := backend.commands[0]
	if !reflect.DeepEqual(command.Argv, []string{"go", "list", "-mod=mod", "-deps", "./cmd/projectrunner"}) || !strings.HasPrefix(filepath.Base(command.Dir), ".godj-startproject-") || !bytes.Equal(raw, make([]byte, len(raw))) {
		t.Fatal("bootstrap ran a runtime or retained raw output")
	}
	after, err := os.ReadFile(filepath.Join(fixture.project, "godj.toml"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("failed scaffold changed the parent project")
	}
	assertStartProjectCandidateAbsent(t, fixture.project)
	for _, key := range []string{"TMPDIR", "GOTMPDIR", "GOCACHE", "GOMODCACHE", "HOME"} {
		name := environmentValues(command.Env)[key]
		if name == "" {
			t.Fatal("private build environment omitted an owned path", key)
		}
		if _, err := os.Lstat(name); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("candidate build workspace survived", key, err)
		}
	}
}

type startProjectBackendFunc func(context.Context, <-chan struct{}, ProcessStage, Command) ProcessResult

func (call startProjectBackendFunc) Execute(ctx context.Context, interrupt <-chan struct{}, stage ProcessStage, command Command) ProcessResult {
	if stage == CompatibilityStage {
		return fixtureCompatibilityProcess()
	}
	return call(ctx, interrupt, stage, command)
}

func TestStartProjectNestedGenerationCleanupFailurePrecedesCancellation(t *testing.T) {
	fixture := newGlobalFixture(t, 0)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	backend := startProjectBackendFunc(func(context.Context, <-chan struct{}, ProcessStage, Command) ProcessResult {
		calls++
		if calls == 1 {
			return ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1}
		}
		cancel()
		return ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, CleanupFailed: true}
	})
	var stdout, stderr bytes.Buffer
	report := RunStartProject(StartProjectInvocation{Context: ctx, CWD: fixture.project,
		Args:        []string{"startproject", "example.com/site", "target", "--framework-version", "v0.1.0"},
		Environment: fixture.environment, Stdout: &stdout, Stderr: &stderr, Backend: backend})
	if calls != 2 || report.ExitCode != 1 || report.FailureCode != "build_cleanup_failed" || report.Publication.Published || stdout.Len() != 0 || stderr.String() != "startproject_error/build_cleanup_failed\n" {
		t.Fatal("outer cancellation hid nested cleanup failure", report)
	}
	assertStartProjectCandidateAbsent(t, fixture.project)
}

func TestStartProjectCancellationJoinsPreparationBeforeRemovingSources(t *testing.T) {
	fixture := newGlobalFixture(t, 0)
	ctx, cancel := context.WithCancel(t.Context())
	entered, observed, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	result := make(chan StartProjectReport, 1)
	backend := startProjectBackendFunc(func(ctx context.Context, _ <-chan struct{}, stage ProcessStage, command Command) ProcessResult {
		close(entered)
		<-ctx.Done()
		close(observed)
		<-release
		if stage != BuildStage {
			return ProcessResult{Failure: &Failure{Code: "unexpected_runtime"}}
		}
		if _, err := os.Stat(filepath.Join(command.Dir, "go.mod")); err != nil {
			return ProcessResult{BuildDiagnostic: "candidate removed before preparation returned"}
		}
		return ProcessResult{Started: true, ExitCode: 1, DirectReaps: 1}
	})
	go func() {
		defer close(done)
		result <- RunStartProject(StartProjectInvocation{Context: ctx, CWD: fixture.project,
			Args:        []string{"startproject", "example.com/site", "target", "--framework-version", "v0.1.0"},
			Environment: fixture.environment, Stdout: io.Discard, Stderr: io.Discard, Backend: backend})
	}()
	t.Cleanup(func() { cancel(); unblock(); awaitStartProjectSignal(t, done) })
	awaitStartProjectSignal(t, entered)
	cancel()
	awaitStartProjectSignal(t, observed)
	select {
	case <-done:
		t.Fatal("canceled preparation was abandoned")
	case <-time.After(30 * time.Millisecond):
	}
	unblock()
	awaitStartProjectSignal(t, done)
	report := <-result
	if report.ExitCode != 130 || report.FailureCode != protocol.CodeProjectCanceled || report.Publication.Published || report.BuildDiagnostic != "" {
		t.Fatal("cancellation or source lifetime was lost", report)
	}
	assertStartProjectCandidateAbsent(t, fixture.project)
}

func awaitStartProjectSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("startproject callback did not reach its ownership boundary")
	}
}

func assertStartProjectCandidateAbsent(t *testing.T, parent string) {
	t.Helper()
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == "target" || strings.HasPrefix(entry.Name(), ".godj-startproject-") {
			t.Fatal("failed scaffold retained a partial destination or stage")
		}
	}
}
