//go:build darwin || linux

package projectscaffold

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestProject() Project {
	return Project{Module: "example.com/new-project", FrameworkVersion: "v0.1.0"}
}

func TestProjectPublicationPreservesExistingDestinations(t *testing.T) {
	for _, kind := range []string{"file", "empty-directory", "populated-directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			parent := t.TempDir()
			destination := filepath.Join(parent, "project")
			witness := filepath.Join(parent, "user-content")
			writeTestFile(t, witness, "keep external content")
			switch kind {
			case "file":
				writeTestFile(t, destination, "keep file")
			case "empty-directory", "populated-directory":
				if err := os.Mkdir(destination, 0o700); err != nil {
					t.Fatal(err)
				}
				if kind == "populated-directory" {
					writeTestFile(t, filepath.Join(destination, "user.go"), "keep source")
				}
			case "symlink":
				if err := os.Symlink(witness, destination); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.Lstat(destination)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			result, err := PublishProject(t.Context(), destination, newTestProject(), func(context.Context, string) error { calls++; return nil })
			if !errors.Is(err, &Error{Code: "destination_exists"}) || result.Published || result.Durable || calls != 0 {
				t.Fatal("existing destination was admitted", result, err)
			}
			after, err := os.Lstat(destination)
			if err != nil || !os.SameFile(before, after) {
				t.Fatal("destination identity changed", err)
			}
			assertFile(t, witness, "keep external content")
			if kind == "file" {
				assertFile(t, destination, "keep file")
			}
			if kind == "populated-directory" {
				assertFile(t, filepath.Join(destination, "user.go"), "keep source")
			}
			assertNoStage(t, parent)
		})
	}
}

func TestProjectPublicationFailureLeavesNoPartialProject(t *testing.T) {
	for _, mode := range []string{"error", "panic", "goexit", "cancel", "candidate-symlink"} {
		t.Run(mode, func(t *testing.T) {
			parent := t.TempDir()
			outside := filepath.Join(parent, "outside")
			writeTestFile(t, outside, "keep outside")
			destination := filepath.Join(parent, "project")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cause := errors.New("private preparation detail")
			result, err := PublishProject(ctx, destination, newTestProject(), func(_ context.Context, stage string) error {
				if _, err := os.Stat(filepath.Join(stage, "cmd/site/main.go")); err != nil {
					return err
				}
				switch mode {
				case "error":
					return cause
				case "panic":
					panic("private panic value")
				case "goexit":
					runtime.Goexit()
				case "cancel":
					cancel()
				case "candidate-symlink":
					return os.Symlink(outside, filepath.Join(stage, "unexpected"))
				}
				return nil
			})
			if err == nil || result.Published || result.Durable {
				t.Fatal("incomplete candidate was published", result, err)
			}
			if mode == "error" && !errors.Is(err, cause) {
				t.Fatal("primary error was lost")
			}
			if strings.Contains(err.Error(), "private") {
				t.Fatal("preparation detail leaked")
			}
			if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("partial project survived", err)
			}
			assertFile(t, outside, "keep outside")
			assertNoStage(t, parent)
		})
	}
}

func TestProjectPublicationJoinsCanceledPreparation(t *testing.T) {
	parent := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered, canceled, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	result := make(chan error, 1)
	go func() {
		defer close(done)
		_, err := PublishProject(ctx, filepath.Join(parent, "project"), newTestProject(), func(ctx context.Context, stage string) error {
			close(entered)
			<-ctx.Done()
			close(canceled)
			<-release
			if _, err := os.Stat(filepath.Join(stage, "go.mod")); err != nil {
				return errors.New("candidate removed before preparation returned")
			}
			return ctx.Err()
		})
		result <- err
	}()
	t.Cleanup(func() { unblock(); cancel(); waitPublication(t, done) })
	waitPublication(t, entered)
	cancel()
	waitPublication(t, canceled)
	select {
	case err := <-result:
		t.Fatal("canceled preparation was abandoned", err)
	case <-time.After(30 * time.Millisecond):
	}
	unblock()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation was lost", err)
	}
	waitPublication(t, done)
	assertNoStage(t, parent)
}

func TestProjectPublicationRejectsParentAndStagingReplacement(t *testing.T) {
	for _, mode := range []string{"parent", "stage", "target"} {
		t.Run(mode, func(t *testing.T) {
			outer := t.TempDir()
			parent := filepath.Join(outer, "parent")
			if err := os.Mkdir(parent, 0o700); err != nil {
				t.Fatal(err)
			}
			var stagePath, witness string
			hook := func() {
				switch mode {
				case "parent":
					if err := os.Rename(parent, filepath.Join(outer, "original-parent")); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(parent, 0o700); err != nil {
						t.Fatal(err)
					}
					witness = filepath.Join(parent, "user-content")
				case "stage":
					if err := os.Rename(stagePath, filepath.Join(parent, "original-stage")); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(stagePath, 0o700); err != nil {
						t.Fatal(err)
					}
					witness = filepath.Join(stagePath, "user-content")
				case "target":
					witness = filepath.Join(parent, "project")
				}
				writeTestFile(t, witness, "keep concurrent content")
			}
			result, err := publishProject(t.Context(), filepath.Join(parent, "project"), newTestProject(), func(_ context.Context, stage string) error { stagePath = stage; return nil }, publicationHooks{beforeCommit: hook})
			if err == nil || result.Published || result.Durable {
				t.Fatal("changed destination was published", result, err)
			}
			assertFile(t, witness, "keep concurrent content")
			if mode != "target" {
				if _, err := os.Lstat(filepath.Join(parent, "project")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("replacement parent received a project", err)
				}
			}
		})
	}
}

func TestConcurrentProjectPublicationHasOneCompleteWinner(t *testing.T) {
	parent := t.TempDir()
	destination := filepath.Join(parent, "project")
	ready, release := make(chan struct{}, 2), make(chan struct{})
	type outcome struct {
		result Result
		err    error
	}
	results := make(chan outcome, 2)
	var calls atomic.Int32
	var done sync.WaitGroup
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { unblock(); cancel(); done.Wait() })
	for range 2 {
		done.Go(func() {
			result, err := PublishProject(ctx, destination, newTestProject(), func(_ context.Context, stage string) error {
				calls.Add(1)
				ready <- struct{}{}
				<-release
				return os.WriteFile(filepath.Join(stage, "prepared"), []byte("complete"), 0o600)
			})
			results <- outcome{result, err}
		})
	}
	waitPublication(t, ready)
	waitPublication(t, ready)
	unblock()
	winners := 0
	for range 2 {
		item := <-results
		if item.err == nil {
			if !item.result.Published || !item.result.Durable {
				t.Fatal("success has no durable publication")
			}
			winners++
		} else if !errors.Is(item.err, &Error{Code: "publication_conflict"}) || item.result.Published {
			t.Fatal("loser outcome differs", item.err)
		}
	}
	if winners != 1 || calls.Load() != 2 {
		t.Fatal("concurrent publication did not select exactly one candidate")
	}
	assertFile(t, filepath.Join(destination, "prepared"), "complete")
	if _, err := os.Stat(filepath.Join(destination, "cmd/site/main.go")); err != nil {
		t.Fatal("published source incomplete", err)
	}
	assertNoStage(t, parent)
}

func TestProjectPublicationKeepsCommittedOutcomeOnLateCancellation(t *testing.T) {
	parent := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result, err := publishProject(ctx, filepath.Join(parent, "project"), newTestProject(), func(context.Context, string) error { return nil }, publicationHooks{afterCommit: cancel})
	if err != nil || !result.Published || !result.Durable || ctx.Err() == nil {
		t.Fatal("committed outcome was replaced by cancellation", result, err)
	}
	assertNoStage(t, parent)
}

func writeTestFile(t *testing.T, name, value string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}
func assertFile(t *testing.T, name, expected string) {
	t.Helper()
	actual, err := os.ReadFile(name)
	if err != nil || string(actual) != expected {
		t.Fatal("owned content differs", err)
	}
}
func assertNoStage(t *testing.T, parent string) {
	t.Helper()
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".godj-startproject-") {
			t.Fatal("private stage remains")
		}
	}
}
func waitPublication(t *testing.T, channel <-chan struct{}) {
	t.Helper()
	select {
	case <-channel:
	case <-time.After(5 * time.Second):
		t.Fatal("publication did not finish its owned work")
	}
}
