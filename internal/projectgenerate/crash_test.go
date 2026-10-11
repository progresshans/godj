//go:build darwin || linux

package projectgenerate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

const (
	publicationCrashHelperEnvironment = "GODJ_PUBLICATION_CRASH_HELPER"
	publicationCrashRootEnvironment   = "GODJ_PUBLICATION_CRASH_ROOT"
	publicationCrashStepEnvironment   = "GODJ_PUBLICATION_CRASH_STEP"
	publicationCrashReadyEnvironment  = "GODJ_PUBLICATION_CRASH_READY"
)

func TestPublishRecoversAfterProcessCrashAtPrecommitAndPostcommitBoundaries(t *testing.T) {
	tests := []struct {
		name        string
		step        publicationStep
		initial     bool
		wantNext    bool
		wantNoFiles bool
	}{
		{name: "created directory rolls back", step: publicationStepDirectoryMade, initial: true, wantNoFiles: true},
		{name: "prior backup rolls back", step: publicationStepPriorBackedUp},
		{name: "installed file rolls back", step: publicationStepNextInstalled},
		{name: "manifest backup rolls back", step: publicationStepManifestBackedUp},
		{name: "renamed manifest recovers as next", step: publicationStepManifestRenamed, wantNext: true},
		{name: "committed manifest wins", step: publicationStepManifestCommit, wantNext: true},
		{name: "partial transaction cleanup resumes", step: publicationStepTransactionEntry, wantNext: true},
		{name: "committed cleanup resumes", step: publicationStepTransactionClean, wantNext: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			prior := projectGenerateTestBundle(t)
			next := changedProjectGenerateTestBundle(t)
			if test.initial {
				next = prior
			} else if err := Publish(context.Background(), root, prior, publicationTestVerifier(t, prior, nil)); err != nil {
				t.Fatalf("Publish(prior) error = %v", err)
			}
			crashPublicationProcess(t, root, test.step)

			if test.wantNext {
				var verified bool
				err := Publish(context.Background(), root, next, CandidateVerifyFunc(func(context.Context, string) error {
					verified = true
					return errors.New("unchanged committed bundle must not verify again")
				}))
				if err != nil {
					t.Fatalf("Publish(recover committed) error = %v", err)
				}
				if verified {
					t.Fatal("committed crash recovery redundantly verified unchanged bundle")
				}
				assertPublishedBundle(t, root, next)
				assertPublicationControlClean(t, root)
				return
			}

			verificationFailure := errors.New("stop after recovery")
			err := Publish(context.Background(), root, next, CandidateVerifyFunc(func(context.Context, string) error {
				return verificationFailure
			}))
			if !errors.Is(err, ErrCandidateVerification) || !errors.Is(err, verificationFailure) {
				t.Fatalf("Publish(recover precommit) error = %v, want candidate failure", err)
			}
			if test.wantNoFiles {
				for _, file := range next.Files() {
					if _, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(file.Path))); !errors.Is(statErr, os.ErrNotExist) {
						t.Fatalf("file %s remains after first-publication crash rollback: %v", file.Path, statErr)
					}
				}
				for _, directory := range []string{"authors", "blog", "project"} {
					if _, statErr := os.Lstat(filepath.Join(root, directory)); !errors.Is(statErr, os.ErrNotExist) {
						t.Fatalf("directory %s remains after crash rollback: %v", directory, statErr)
					}
				}
			} else {
				assertPublishedBundle(t, root, prior)
			}
			assertPublicationControlClean(t, root)
		})
	}
}

func TestPublicationCrashHelper(t *testing.T) {
	if os.Getenv(publicationCrashHelperEnvironment) != "1" {
		t.Skip("subprocess helper")
	}
	root := os.Getenv(publicationCrashRootEnvironment)
	ready := os.Getenv(publicationCrashReadyEnvironment)
	step := publicationStep(os.Getenv(publicationCrashStepEnvironment))
	if root == "" || ready == "" || step == "" {
		t.Fatal("crash helper environment is incomplete")
	}
	bundle := changedProjectGenerateTestBundle(t)
	if step == publicationStepDirectoryMade {
		bundle = projectGenerateTestBundle(t)
	}
	err := publishWithHooks(context.Background(), root, bundle, publicationTestVerifier(t, bundle, nil), publicationHooks{
		after: func(actual publicationStep, relative string, index int) error {
			if actual != step {
				return nil
			}
			if err := os.WriteFile(ready, []byte(fmt.Sprintf("%s\n%s\n%d\n", actual, relative, index)), 0o600); err != nil {
				return err
			}
			select {}
		},
	})
	t.Fatalf("crash helper publication returned: %v", err)
}

func crashPublicationProcess(t *testing.T, root string, step publicationStep) {
	t.Helper()
	ready := filepath.Join(t.TempDir(), "ready")
	command := exec.Command(os.Args[0], "-test.run=^TestPublicationCrashHelper$", "-test.v")
	command.Env = append(os.Environ(),
		publicationCrashHelperEnvironment+"=1",
		publicationCrashRootEnvironment+"="+root,
		publicationCrashStepEnvironment+"="+string(step),
		publicationCrashReadyEnvironment+"="+ready,
	)
	output := new(crashOutputBuffer)
	command.Stdout = output
	command.Stderr = output
	if err := command.Start(); err != nil {
		t.Fatalf("start crash helper: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			_ = command.Process.Kill()
			_, _ = command.Process.Wait()
			t.Fatalf("stat crash helper ready file: %v", err)
		}
		if time.Now().After(deadline) {
			_ = command.Process.Kill()
			_, _ = command.Process.Wait()
			t.Fatalf("crash helper did not reach %s\n%s", step, output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatalf("kill crash helper: %v", err)
	}
	if err := command.Wait(); err == nil {
		t.Fatalf("crash helper exited successfully instead of being killed\n%s", output.String())
	}
}

type crashOutputBuffer struct {
	mu       sync.Mutex
	contents []byte
}

func (buffer *crashOutputBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	buffer.contents = append(buffer.contents, value...)
	return len(value), nil
}

func (buffer *crashOutputBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return string(buffer.contents)
}
