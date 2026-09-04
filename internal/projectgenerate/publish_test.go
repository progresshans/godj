//go:build darwin || linux

package projectgenerate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/codegen"
	"golang.org/x/sys/unix"
)

func TestPublishFirstGenerationIdempotentAndGeneratedOnlyDirectories(t *testing.T) {
	root := t.TempDir()
	bundle := projectGenerateTestBundle(t)
	var verifies atomic.Int32
	verifier := publicationTestVerifier(t, bundle, &verifies)

	if err := Publish(context.Background(), root, bundle, verifier); err != nil {
		t.Fatalf("Publish(first) error = %v", err)
	}
	assertPublishedBundle(t, root, bundle)
	assertPublicationControlClean(t, root)
	if got := verifies.Load(); got != 1 {
		t.Fatalf("candidate verifier calls = %d, want 1", got)
	}
	report, err := Check(context.Background(), root, bundle)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if !report.Clean() {
		t.Fatalf("Check() report = %+v, want clean", report)
	}

	if err := Publish(context.Background(), root, bundle, verifier); err != nil {
		t.Fatalf("Publish(idempotent) error = %v", err)
	}
	if got := verifies.Load(); got != 1 {
		t.Fatalf("idempotent publication verifier calls = %d, want 1", got)
	}
	assertPublishedBundle(t, root, bundle)
	assertPublicationControlClean(t, root)
}

func TestPublishAdoptsExactUnownedFilesWithoutReplacingThem(t *testing.T) {
	root := t.TempDir()
	bundle := projectGenerateTestBundle(t)
	before := make(map[string]os.FileInfo, len(bundle.Files()))
	for _, file := range bundle.Files() {
		writeProjectGenerateTestFile(t, root, file.Path, file.Source(), file.Mode)
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(file.Path)))
		if err != nil {
			t.Fatalf("stat unowned %s: %v", file.Path, err)
		}
		before[file.Path] = info
	}

	if err := Publish(context.Background(), root, bundle, publicationTestVerifier(t, bundle, nil)); err != nil {
		t.Fatalf("Publish(adopt) error = %v", err)
	}
	for _, file := range bundle.Files() {
		after, err := os.Stat(filepath.Join(root, filepath.FromSlash(file.Path)))
		if err != nil {
			t.Fatalf("stat adopted %s: %v", file.Path, err)
		}
		if !os.SameFile(before[file.Path], after) {
			t.Errorf("adopted file %s was replaced", file.Path)
		}
	}
	assertPublishedBundle(t, root, bundle)
	assertPublicationControlClean(t, root)
}

func TestPublishVerificationFailureLeavesExactPriorBundle(t *testing.T) {
	root := t.TempDir()
	prior := projectGenerateTestBundle(t)
	next := changedProjectGenerateTestBundle(t)
	if err := Publish(context.Background(), root, prior, publicationTestVerifier(t, prior, nil)); err != nil {
		t.Fatalf("Publish(prior) error = %v", err)
	}
	verificationFailure := errors.New("candidate rejected")
	err := Publish(context.Background(), root, next, CandidateVerifyFunc(func(context.Context, string) error {
		return verificationFailure
	}))
	if !errors.Is(err, ErrCandidateVerification) || !errors.Is(err, verificationFailure) {
		t.Fatalf("Publish(next) error = %v, want candidate failure", err)
	}
	assertPublishedBundle(t, root, prior)
	assertPublicationControlClean(t, root)
}

func TestPublishRejectsVerifierStageMutationBeforeTargetMutation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(string, codegen.GeneratedBundle) error
	}{
		{name: "generated source", mutate: func(candidateRoot string, bundle codegen.GeneratedBundle) error {
			return os.WriteFile(filepath.Join(candidateRoot, filepath.FromSlash(bundle.Files()[0].Path)), []byte("package changed\n"), 0o644)
		}},
		{name: "manifest", mutate: func(candidateRoot string, _ codegen.GeneratedBundle) error {
			return os.WriteFile(filepath.Join(candidateRoot, filepath.FromSlash(generatedManifestRelativePath)), []byte("{}\n"), 0o644)
		}},
		{name: "unexpected regular", mutate: func(candidateRoot string, _ codegen.GeneratedBundle) error {
			return os.WriteFile(filepath.Join(candidateRoot, "unexpected"), []byte("unexpected\n"), 0o600)
		}},
		{name: "unexpected fifo", mutate: func(candidateRoot string, _ codegen.GeneratedBundle) error {
			return unix.Mkfifo(filepath.Join(candidateRoot, "unexpected"), 0o600)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			prior := projectGenerateTestBundle(t)
			next := changedProjectGenerateTestBundle(t)
			if err := Publish(context.Background(), root, prior, publicationTestVerifier(t, prior, nil)); err != nil {
				t.Fatalf("Publish(prior) error = %v", err)
			}
			err := Publish(context.Background(), root, next, CandidateVerifyFunc(func(_ context.Context, candidateRoot string) error {
				return test.mutate(candidateRoot, next)
			}))
			if !errors.Is(err, ErrPublicationRecoveryRequired) && !errors.Is(err, ErrGeneratedConflict) {
				t.Fatalf("Publish(mutated stage) error = %v, want conflict/recovery required", err)
			}
			assertPublishedBundle(t, root, prior)
		})
	}
}

func TestPublishRejectsReservedNamespaceDriftEvenWhenSnapshotIsUnchanged(t *testing.T) {
	root := t.TempDir()
	bundle := projectGenerateTestBundle(t)
	if err := Publish(context.Background(), root, bundle, publicationTestVerifier(t, bundle, nil)); err != nil {
		t.Fatalf("Publish(first) error = %v", err)
	}
	rogue := "retired/zz_godj_retired.go"
	writeProjectGenerateTestFile(t, root, rogue, []byte("package retired\n"), 0o644)
	beforeManifest := mustReadPublicationFile(t, root, generatedManifestRelativePath)
	err := Publish(context.Background(), root, bundle, publicationTestVerifier(t, bundle, nil))
	if !errors.Is(err, ErrGeneratedConflict) {
		t.Fatalf("Publish(rogue) error = %v, want ErrGeneratedConflict", err)
	}
	if got := mustReadPublicationFile(t, root, rogue); !bytes.Equal(got, []byte("package retired\n")) {
		t.Fatalf("rogue file changed: %q", got)
	}
	if got := mustReadPublicationFile(t, root, generatedManifestRelativePath); !bytes.Equal(got, beforeManifest) {
		t.Fatal("manifest changed while rejecting reserved namespace drift")
	}
	assertPublicationControlClean(t, root)
}

func TestPublishSecondReservedNamespacePreflightCatchesDriftCreatedDuringVerification(t *testing.T) {
	root := t.TempDir()
	prior := projectGenerateTestBundle(t)
	next := changedProjectGenerateTestBundle(t)
	if err := Publish(context.Background(), root, prior, publicationTestVerifier(t, prior, nil)); err != nil {
		t.Fatalf("Publish(prior) error = %v", err)
	}
	rogue := "retired/zz_godj_during_verify.go"
	err := Publish(context.Background(), root, next, CandidateVerifyFunc(func(context.Context, string) error {
		writeProjectGenerateTestFile(t, root, rogue, []byte("package retired\n"), 0o644)
		return nil
	}))
	if !errors.Is(err, ErrGeneratedConflict) {
		t.Fatalf("Publish(drift during verify) error = %v, want conflict", err)
	}
	assertPublishedBundle(t, root, prior)
	if got := mustReadPublicationFile(t, root, rogue); !bytes.Equal(got, []byte("package retired\n")) {
		t.Fatalf("rogue file changed: %q", got)
	}
	assertPublicationControlClean(t, root)
}

func TestPublishPreservesConcurrentExactNextFileThatWasNeverInstalled(t *testing.T) {
	root := t.TempDir()
	bundle := projectGenerateTestBundle(t)
	snapshot := snapshotPublicationBundleForTest(t, bundle)
	directories, err := publicationBundleDirectories(snapshot.files)
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range directories {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(directory)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	file := bundle.Files()[0]
	target := filepath.Join(root, filepath.FromSlash(file.Path))
	err = Publish(context.Background(), root, bundle, CandidateVerifyFunc(func(_ context.Context, candidateRoot string) error {
		if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("target unexpectedly present before concurrent insertion: %v", err)
		}
		staged, err := os.ReadFile(filepath.Join(candidateRoot, filepath.FromSlash(file.Path)))
		if err != nil {
			return err
		}
		return os.WriteFile(target, staged, file.Mode)
	}))
	if !errors.Is(err, ErrPublicationRecoveryRequired) {
		t.Fatalf("Publish(concurrent exact-next target) error = %v, want recovery required", err)
	}
	contents, readErr := os.ReadFile(target)
	if readErr != nil || !bytes.Equal(contents, file.Source()) {
		t.Fatalf("concurrent exact-next target was removed or changed: contents=%q error=%v", contents, readErr)
	}
	if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(publicationJournalRelativePath))); err != nil {
		t.Fatalf("recovery authority missing: %v", err)
	}
}

func TestPublishSerializesWritersAndCanceledWaiterDoesNotMutate(t *testing.T) {
	root := t.TempDir()
	bundle := projectGenerateTestBundle(t)
	locked := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- publishWithHooks(context.Background(), root, bundle, publicationTestVerifier(t, bundle, nil), publicationHooks{
			after: func(step publicationStep, _ string, _ int) error {
				if step == publicationStepLocked {
					close(locked)
					<-release
				}
				return nil
			},
		})
	}()
	select {
	case <-locked:
	case <-time.After(5 * time.Second):
		t.Fatal("first publisher did not acquire lock")
	}
	waiterContext, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	waiterErr := Publish(waiterContext, root, bundle, publicationTestVerifier(t, bundle, nil))
	if !errors.Is(waiterErr, context.DeadlineExceeded) {
		t.Fatalf("waiting Publish() error = %v, want deadline exceeded", waiterErr)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first Publish() error = %v", err)
	}
	assertPublishedBundle(t, root, bundle)
	assertPublicationControlClean(t, root)
}

func TestPublishFaultBoundariesResolveToExactPriorOrCommittedNext(t *testing.T) {
	precommit := []publicationStep{
		publicationStepJournalDurable,
		publicationStepStageFileDurable,
		publicationStepCandidateValid,
		publicationStepPriorCASValid,
		publicationStepPriorBackedUp,
		publicationStepNextInstalled,
		publicationStepManifestBackedUp,
	}
	for _, step := range precommit {
		t.Run(string(step), func(t *testing.T) {
			root := t.TempDir()
			prior := projectGenerateTestBundle(t)
			next := changedProjectGenerateTestBundle(t)
			if err := Publish(context.Background(), root, prior, publicationTestVerifier(t, prior, nil)); err != nil {
				t.Fatalf("Publish(prior) error = %v", err)
			}
			fault := fmt.Errorf("fault at %s", step)
			fired := false
			err := publishWithHooks(context.Background(), root, next, publicationTestVerifier(t, next, nil), publicationHooks{
				after: func(actual publicationStep, _ string, _ int) error {
					if actual == step && !fired {
						fired = true
						return fault
					}
					return nil
				},
			})
			if !fired {
				t.Fatalf("fault boundary %s was not reached", step)
			}
			if !errors.Is(err, fault) {
				t.Fatalf("Publish(fault) error = %v, want %v", err, fault)
			}
			assertPublishedBundle(t, root, prior)
			assertPublicationControlClean(t, root)
		})
	}

	t.Run(string(publicationStepManifestCommit), func(t *testing.T) {
		root := t.TempDir()
		prior := projectGenerateTestBundle(t)
		next := changedProjectGenerateTestBundle(t)
		if err := Publish(context.Background(), root, prior, publicationTestVerifier(t, prior, nil)); err != nil {
			t.Fatalf("Publish(prior) error = %v", err)
		}
		fault := errors.New("fault after commit")
		err := publishWithHooks(context.Background(), root, next, publicationTestVerifier(t, next, nil), publicationHooks{
			after: func(actual publicationStep, _ string, _ int) error {
				if actual == publicationStepManifestCommit {
					return fault
				}
				return nil
			},
		})
		if err != nil {
			t.Fatalf("Publish(postcommit fault) error = %v, want committed success", err)
		}
		assertPublishedBundle(t, root, next)
		assertPublicationControlClean(t, root)
	})
}

func TestPublishReportsSuccessWhenRecoveryDurablyAcceptsExactNextManifest(t *testing.T) {
	root := t.TempDir()
	prior := projectGenerateTestBundle(t)
	next := changedProjectGenerateTestBundle(t)
	if err := Publish(context.Background(), root, prior, publicationTestVerifier(t, prior, nil)); err != nil {
		t.Fatalf("Publish(prior) error = %v", err)
	}
	fault := errors.New("ordinary fault after prior manifest backup")
	err := publishWithHooks(context.Background(), root, next, publicationTestVerifier(t, next, nil), publicationHooks{
		after: func(step publicationStep, _ string, _ int) error {
			if step != publicationStepManifestBackedUp {
				return nil
			}
			manifest := filepath.Join(root, filepath.FromSlash(generatedManifestRelativePath))
			if err := os.WriteFile(manifest, next.Manifest(), 0o644); err != nil {
				return err
			}
			return fault
		},
	})
	if err != nil {
		t.Fatalf("Publish(exact concurrent next manifest) error = %v, want committed success", err)
	}
	assertPublishedBundle(t, root, next)
	assertPublicationControlClean(t, root)
}

func TestPublishPostcommitCancellationStillFinishesCleanup(t *testing.T) {
	root := t.TempDir()
	prior := projectGenerateTestBundle(t)
	next := changedProjectGenerateTestBundle(t)
	if err := Publish(context.Background(), root, prior, publicationTestVerifier(t, prior, nil)); err != nil {
		t.Fatalf("Publish(prior) error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	err := publishWithHooks(ctx, root, next, publicationTestVerifier(t, next, nil), publicationHooks{
		after: func(step publicationStep, _ string, _ int) error {
			if step == publicationStepManifestCommit {
				cancel()
				return ctx.Err()
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Publish(postcommit cancellation) error = %v, want committed success", err)
	}
	assertPublishedBundle(t, root, next)
	assertPublicationControlClean(t, root)
}

func TestPublishPrecommitCancellationAtFinalInstallAndManifestBackupRestoresPrior(t *testing.T) {
	for _, boundary := range []publicationStep{publicationStepNextInstalled, publicationStepManifestBackedUp} {
		t.Run(string(boundary), func(t *testing.T) {
			root := t.TempDir()
			prior := projectGenerateTestBundle(t)
			next := manyChangedProjectGenerateTestBundle(t)
			if err := Publish(context.Background(), root, prior, publicationTestVerifier(t, prior, nil)); err != nil {
				t.Fatalf("Publish(prior) error = %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fired := false
			err := publishWithHooks(ctx, root, next, publicationTestVerifier(t, next, nil), publicationHooks{
				after: func(step publicationStep, _ string, index int) error {
					if step != boundary {
						return nil
					}
					if boundary == publicationStepNextInstalled && index != len(next.Files())-1 {
						return nil
					}
					fired = true
					cancel()
					return nil
				},
			})
			if !fired {
				t.Fatalf("cancellation boundary %s was not reached", boundary)
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Publish(precommit cancellation) error = %v, want context.Canceled", err)
			}
			assertPublishedBundle(t, root, prior)
			assertPublicationControlClean(t, root)
		})
	}
}

func TestPublishNextInvocationFinishesRecoverableCommittedCleanupFault(t *testing.T) {
	root := t.TempDir()
	prior := projectGenerateTestBundle(t)
	next := changedProjectGenerateTestBundle(t)
	if err := Publish(context.Background(), root, prior, publicationTestVerifier(t, prior, nil)); err != nil {
		t.Fatalf("Publish(prior) error = %v", err)
	}
	cleanupFault := errors.New("stop after transaction cleanup")
	err := publishWithHooks(context.Background(), root, next, publicationTestVerifier(t, next, nil), publicationHooks{
		after: func(step publicationStep, _ string, _ int) error {
			if step == publicationStepTransactionClean {
				return cleanupFault
			}
			return nil
		},
	})
	if !errors.Is(err, ErrPublicationRecoveryRequired) {
		t.Fatalf("Publish(cleanup fault) error = %v, want recovery required", err)
	}
	assertPublishedBundle(t, root, next)
	if _, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(publicationJournalRelativePath))); statErr != nil {
		t.Fatalf("committed journal missing at injected cleanup boundary: %v", statErr)
	}
	var verified bool
	err = Publish(context.Background(), root, next, CandidateVerifyFunc(func(context.Context, string) error {
		verified = true
		return errors.New("recovered unchanged bundle must not verify")
	}))
	if err != nil {
		t.Fatalf("Publish(next invocation recovery) error = %v", err)
	}
	if verified {
		t.Fatal("next invocation reverified already committed snapshot")
	}
	assertPublishedBundle(t, root, next)
	assertPublicationControlClean(t, root)
}

func TestPublishRecoveryCompletesPartiallyRemovedCommittedTransaction(t *testing.T) {
	for _, removed := range []string{"backup", "stage"} {
		t.Run(removed, func(t *testing.T) {
			root := t.TempDir()
			prior := projectGenerateTestBundle(t)
			next := changedProjectGenerateTestBundle(t)
			if err := Publish(context.Background(), root, prior, publicationTestVerifier(t, prior, nil)); err != nil {
				t.Fatalf("Publish(prior) error = %v", err)
			}
			var transactionRoot string
			err := publishWithHooks(context.Background(), root, next, publicationTestVerifier(t, next, nil), publicationHooks{
				after: func(step publicationStep, _ string, _ int) error {
					if step != publicationStepManifestCommit {
						return nil
					}
					entries, readErr := os.ReadDir(filepath.Join(root, filepath.FromSlash(publicationTransactionDirectoryPath)))
					if readErr != nil || len(entries) != 1 {
						return fmt.Errorf("find transaction: entries=%v err=%w", entries, readErr)
					}
					transactionRoot = filepath.Join(root, filepath.FromSlash(publicationTransactionDirectoryPath), entries[0].Name())
					return os.WriteFile(filepath.Join(transactionRoot, "unexpected"), []byte("stop cleanup\n"), 0o600)
				},
			})
			if !errors.Is(err, ErrPublicationRecoveryRequired) || transactionRoot == "" {
				t.Fatalf("Publish(cleanup interruption) error = %v transaction=%q", err, transactionRoot)
			}
			if err := os.Remove(filepath.Join(transactionRoot, "unexpected")); err != nil {
				t.Fatalf("remove injected cleanup blocker: %v", err)
			}
			if err := os.RemoveAll(filepath.Join(transactionRoot, removed)); err != nil {
				t.Fatalf("simulate crash after removing %s: %v", removed, err)
			}
			verified := false
			err = Publish(context.Background(), root, next, CandidateVerifyFunc(func(context.Context, string) error {
				verified = true
				return errors.New("committed bundle must not be reverified")
			}))
			if err != nil {
				t.Fatalf("Publish(partial cleanup recovery) error = %v", err)
			}
			if verified {
				t.Fatal("partial committed cleanup recovery reverified bundle")
			}
			assertPublishedBundle(t, root, next)
			assertPublicationControlClean(t, root)
		})
	}
}

func TestPublishStageAndRenameFaultsAtFirstMiddleAndLastIndexRestoreExactPrior(t *testing.T) {
	prior := projectGenerateTestBundle(t)
	next := manyChangedProjectGenerateTestBundle(t)
	indexes := []int{0, len(next.Files()) / 2, len(next.Files()) - 1}
	for _, step := range []publicationStep{publicationStepStageFileDurable, publicationStepPriorBackedUp, publicationStepNextInstalled} {
		for _, wantedIndex := range indexes {
			t.Run(fmt.Sprintf("%s/%02d", step, wantedIndex), func(t *testing.T) {
				root := t.TempDir()
				if err := Publish(context.Background(), root, prior, publicationTestVerifier(t, prior, nil)); err != nil {
					t.Fatalf("Publish(prior) error = %v", err)
				}
				fault := fmt.Errorf("fault at %s index %d", step, wantedIndex)
				fired := false
				err := publishWithHooks(context.Background(), root, next, publicationTestVerifier(t, next, nil), publicationHooks{
					after: func(actual publicationStep, _ string, index int) error {
						if actual == step && index == wantedIndex {
							fired = true
							return fault
						}
						return nil
					},
				})
				if !fired {
					t.Fatalf("fault boundary %s[%d] was not reached", step, wantedIndex)
				}
				if !errors.Is(err, fault) {
					t.Fatalf("Publish(fault) error = %v, want %v", err, fault)
				}
				assertPublishedBundle(t, root, prior)
				assertPublicationControlClean(t, root)
			})
		}
	}

	t.Run("stage manifest last", func(t *testing.T) {
		root := t.TempDir()
		if err := Publish(context.Background(), root, prior, publicationTestVerifier(t, prior, nil)); err != nil {
			t.Fatalf("Publish(prior) error = %v", err)
		}
		fault := errors.New("fault at staged manifest")
		err := publishWithHooks(context.Background(), root, next, publicationTestVerifier(t, next, nil), publicationHooks{
			after: func(actual publicationStep, relative string, index int) error {
				if actual == publicationStepStageFileDurable && relative == generatedManifestRelativePath && index == len(next.Files()) {
					return fault
				}
				return nil
			},
		})
		if !errors.Is(err, fault) {
			t.Fatalf("Publish(staged manifest fault) error = %v, want %v", err, fault)
		}
		assertPublishedBundle(t, root, prior)
		assertPublicationControlClean(t, root)
	})
}

func TestPublishDirectoryCreationFaultAtEveryIndexRemovesOnlyOwnedEmptyDirectories(t *testing.T) {
	bundle := nestedProjectGenerateTestBundle(t)
	directories, err := publicationBundleDirectories(snapshotPublicationBundleForTest(t, bundle).files)
	if err != nil {
		t.Fatalf("publicationBundleDirectories() error = %v", err)
	}
	for wantedIndex, wantedDirectory := range directories {
		t.Run(fmt.Sprintf("%02d_%s", wantedIndex, wantedDirectory), func(t *testing.T) {
			root := t.TempDir()
			fault := fmt.Errorf("fault after directory %s", wantedDirectory)
			fired := false
			err := publishWithHooks(context.Background(), root, bundle, publicationTestVerifier(t, bundle, nil), publicationHooks{
				after: func(step publicationStep, relative string, index int) error {
					if step == publicationStepDirectoryMade && index == wantedIndex {
						fired = true
						return fault
					}
					return nil
				},
			})
			if !fired || !errors.Is(err, fault) {
				t.Fatalf("Publish(directory fault) fired=%t error=%v, want %v", fired, err, fault)
			}
			for _, directory := range directories {
				if _, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(directory))); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("directory %s remains after rollback: %v", directory, statErr)
				}
			}
			assertPublicationControlClean(t, root)
		})
	}
}

func TestPublishTwoSuccessfulWritersSerializeToSecondSnapshot(t *testing.T) {
	root := t.TempDir()
	first := projectGenerateTestBundle(t)
	second := changedProjectGenerateTestBundle(t)
	locked := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	secondStarted := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		firstDone <- publishWithHooks(context.Background(), root, first, publicationTestVerifier(t, first, nil), publicationHooks{
			after: func(step publicationStep, _ string, _ int) error {
				if step == publicationStepLocked {
					close(locked)
					<-release
				}
				return nil
			},
		})
	}()
	select {
	case <-locked:
	case <-time.After(5 * time.Second):
		t.Fatal("first writer did not acquire lock")
	}
	go func() {
		close(secondStarted)
		secondDone <- Publish(context.Background(), root, second, publicationTestVerifier(t, second, nil))
	}()
	<-secondStarted
	select {
	case err := <-secondDone:
		t.Fatalf("second writer completed before first released lock: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first writer error = %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second writer error = %v", err)
	}
	assertPublishedBundle(t, root, second)
	assertPublicationControlClean(t, root)
}

func TestPublishSimultaneousFirstWritersShareControlDirectoryCreation(t *testing.T) {
	for attempt := 0; attempt < 12; attempt++ {
		root := t.TempDir()
		bundle := projectGenerateTestBundle(t)
		start := make(chan struct{})
		results := make(chan error, 2)
		for writer := 0; writer < 2; writer++ {
			go func() {
				<-start
				results <- Publish(context.Background(), root, bundle, publicationTestVerifier(t, bundle, nil))
			}()
		}
		close(start)
		for writer := 0; writer < 2; writer++ {
			if err := <-results; err != nil {
				t.Fatalf("attempt %d simultaneous writer %d error = %v", attempt, writer, err)
			}
		}
		assertPublishedBundle(t, root, bundle)
	}
}

func TestPublishLockPathReplacementFailsBeforeJournalOrGeneratedMutation(t *testing.T) {
	root := t.TempDir()
	bundle := projectGenerateTestBundle(t)
	fired := false
	err := publishWithHooks(context.Background(), root, bundle, publicationTestVerifier(t, bundle, nil), publicationHooks{
		after: func(step publicationStep, _ string, _ int) error {
			if step != publicationStepLocked || fired {
				return nil
			}
			fired = true
			lockPath := filepath.Join(root, filepath.FromSlash(publicationLockRelativePath))
			if err := os.Rename(lockPath, lockPath+".displaced"); err != nil {
				return err
			}
			return os.WriteFile(lockPath, []byte("replacement lock\n"), 0o600)
		},
	})
	if !fired || !errors.Is(err, ErrGeneratedConflict) {
		t.Fatalf("Publish(lock replacement) fired=%t error=%v, want conflict", fired, err)
	}
	if _, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(publicationJournalRelativePath))); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("journal created after lock replacement: %v", statErr)
	}
	for _, file := range bundle.Files() {
		if _, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(file.Path))); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("generated file %s created after lock replacement: %v", file.Path, statErr)
		}
	}
}

func TestPublishCancellationRollsBackGeneratedOnlyDirectories(t *testing.T) {
	root := t.TempDir()
	bundle := projectGenerateTestBundle(t)
	ctx, cancel := context.WithCancel(context.Background())
	err := publishWithHooks(ctx, root, bundle, publicationTestVerifier(t, bundle, nil), publicationHooks{
		after: func(step publicationStep, _ string, _ int) error {
			if step == publicationStepDirectoryMade {
				cancel()
				return ctx.Err()
			}
			return nil
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Publish(canceled) error = %v, want context canceled", err)
	}
	for _, directory := range []string{"authors", "blog", "project"} {
		if _, err := os.Lstat(filepath.Join(root, directory)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("generated-only directory %s remains after rollback: %v", directory, err)
		}
	}
	assertPublicationControlClean(t, root)
}

func TestPublishPreservesUserEntryAddedToCreatedDirectoryAndRequiresRecovery(t *testing.T) {
	root := t.TempDir()
	bundle := projectGenerateTestBundle(t)
	var userPath string
	fault := errors.New("stop after user insertion")
	err := publishWithHooks(context.Background(), root, bundle, publicationTestVerifier(t, bundle, nil), publicationHooks{
		after: func(step publicationStep, relative string, _ int) error {
			if step != publicationStepDirectoryMade || userPath != "" {
				return nil
			}
			userPath = filepath.Join(root, filepath.FromSlash(relative), "user.go")
			if err := os.WriteFile(userPath, []byte("package user\n"), 0o644); err != nil {
				return err
			}
			return fault
		},
	})
	if !errors.Is(err, ErrPublicationRecoveryRequired) {
		t.Fatalf("Publish(user insertion) error = %v, want recovery required", err)
	}
	if got, readErr := os.ReadFile(userPath); readErr != nil || !bytes.Equal(got, []byte("package user\n")) {
		t.Fatalf("user entry was not preserved: bytes=%q err=%v", got, readErr)
	}
	if _, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(publicationJournalRelativePath))); statErr != nil {
		t.Fatalf("journal missing after ambiguous rollback: %v", statErr)
	}
}

func TestPublishDirectorySwapFailsClosedBeforeWritingReplacementDirectory(t *testing.T) {
	root := t.TempDir()
	prior := projectGenerateTestBundle(t)
	next := changedProjectGenerateTestBundle(t)
	if err := Publish(context.Background(), root, prior, publicationTestVerifier(t, prior, nil)); err != nil {
		t.Fatalf("Publish(prior) error = %v", err)
	}
	replacementSentinel := []byte("replacement directory\n")
	fired := false
	err := publishWithHooks(context.Background(), root, next, publicationTestVerifier(t, next, nil), publicationHooks{
		after: func(step publicationStep, _ string, _ int) error {
			if step != publicationStepPriorCASValid || fired {
				return nil
			}
			fired = true
			if err := os.Rename(filepath.Join(root, "authors"), filepath.Join(root, "authors-displaced")); err != nil {
				return err
			}
			if err := os.Mkdir(filepath.Join(root, "authors"), 0o755); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(root, "authors", "sentinel.txt"), replacementSentinel, 0o644)
		},
	})
	if !fired {
		t.Fatal("directory swap hook was not reached")
	}
	if !errors.Is(err, ErrPublicationRecoveryRequired) && !errors.Is(err, ErrGeneratedConflict) {
		t.Fatalf("Publish(directory swap) error = %v, want conflict/recovery required", err)
	}
	if got := mustReadPublicationFile(t, root, "authors/sentinel.txt"); !bytes.Equal(got, replacementSentinel) {
		t.Fatalf("replacement directory sentinel changed: %q", got)
	}
	entries, readErr := os.ReadDir(filepath.Join(root, "authors"))
	if readErr != nil || len(entries) != 1 || entries[0].Name() != "sentinel.txt" {
		t.Fatalf("publisher wrote into replacement directory: entries=%v err=%v", entries, readErr)
	}
}

func TestPublishCommittedCleanupRejectsUnexpectedOrNonregularTransactionEntries(t *testing.T) {
	tests := []struct {
		name   string
		inject func(string) (string, error)
	}{
		{name: "unexpected regular", inject: func(txRoot string) (string, error) {
			name := filepath.Join(txRoot, "unexpected")
			return name, os.WriteFile(name, []byte("user bytes"), 0o644)
		}},
		{name: "unexpected symlink", inject: func(txRoot string) (string, error) {
			outside := filepath.Join(filepath.Dir(filepath.Dir(txRoot)), "outside")
			if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
				return "", err
			}
			name := filepath.Join(txRoot, "unexpected")
			return name, os.Symlink(outside, name)
		}},
		{name: "unexpected fifo", inject: func(txRoot string) (string, error) {
			name := filepath.Join(txRoot, "unexpected")
			return name, unix.Mkfifo(name, 0o600)
		}},
		{name: "expected backup member replaced by symlink", inject: func(txRoot string) (string, error) {
			var candidate string
			walkErr := filepath.WalkDir(filepath.Join(txRoot, "backup"), func(filename string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if candidate == "" && entry.Type().IsRegular() && filepath.Ext(filename) == ".old" {
					candidate = filename
				}
				return nil
			})
			if walkErr != nil || candidate == "" {
				return "", fmt.Errorf("find retained backup member: candidate=%q err=%v", candidate, walkErr)
			}
			outside := filepath.Join(filepath.Dir(filepath.Dir(txRoot)), "backup-outside")
			if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
				return "", err
			}
			if err := os.Remove(candidate); err != nil {
				return "", err
			}
			return candidate, os.Symlink(outside, candidate)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			prior := projectGenerateTestBundle(t)
			next := changedProjectGenerateTestBundle(t)
			if err := Publish(context.Background(), root, prior, publicationTestVerifier(t, prior, nil)); err != nil {
				t.Fatalf("Publish(prior) error = %v", err)
			}
			var injected string
			err := publishWithHooks(context.Background(), root, next, publicationTestVerifier(t, next, nil), publicationHooks{
				after: func(step publicationStep, _ string, _ int) error {
					if step != publicationStepManifestCommit {
						return nil
					}
					entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(publicationTransactionDirectoryPath)))
					if err != nil || len(entries) != 1 {
						return fmt.Errorf("find transaction: entries=%v err=%w", entries, err)
					}
					injected, err = test.inject(filepath.Join(root, filepath.FromSlash(publicationTransactionDirectoryPath), entries[0].Name()))
					return err
				},
			})
			if !errors.Is(err, ErrPublicationRecoveryRequired) {
				t.Fatalf("Publish(cleanup fault) error = %v, want recovery required", err)
			}
			assertPublishedBundle(t, root, next)
			if _, statErr := os.Lstat(injected); statErr != nil {
				t.Fatalf("unexpected transaction entry was removed: %v", statErr)
			}
			retryErr := Publish(context.Background(), root, next, CandidateVerifyFunc(func(context.Context, string) error {
				return errors.New("recovery-required transaction must not verify")
			}))
			if !errors.Is(retryErr, ErrPublicationRecoveryRequired) {
				t.Fatalf("Publish(retry cleanup fault) error = %v, want recovery required", retryErr)
			}
			if _, statErr := os.Lstat(injected); statErr != nil {
				t.Fatalf("retry removed ambiguous transaction entry: %v", statErr)
			}
			if _, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(publicationJournalRelativePath))); statErr != nil {
				t.Fatalf("retry removed durable recovery authority: %v", statErr)
			}
		})
	}
}

func TestPublishRecoveryRevalidatesTargetAndJournalAfterTransactionCleanup(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, string, codegen.GeneratedBundle) string
	}{
		{
			name: "generated target",
			mutate: func(t *testing.T, root string, next codegen.GeneratedBundle) string {
				t.Helper()
				target := filepath.Join(root, filepath.FromSlash(next.Files()[0].Path))
				if err := os.WriteFile(target, []byte("package usermutation\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				return target
			},
		},
		{
			name: "publication journal",
			mutate: func(t *testing.T, root string, _ codegen.GeneratedBundle) string {
				t.Helper()
				journal := filepath.Join(root, filepath.FromSlash(publicationJournalRelativePath))
				displaced := journal + ".displaced"
				if err := os.Rename(journal, displaced); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(journal, []byte("replacement journal\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				return displaced
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			prior := projectGenerateTestBundle(t)
			next := changedProjectGenerateTestBundle(t)
			if err := Publish(context.Background(), root, prior, publicationTestVerifier(t, prior, nil)); err != nil {
				t.Fatalf("Publish(prior) error = %v", err)
			}
			var preserved string
			err := publishWithHooks(context.Background(), root, next, publicationTestVerifier(t, next, nil), publicationHooks{
				after: func(step publicationStep, _ string, _ int) error {
					if step == publicationStepTransactionClean {
						preserved = test.mutate(t, root, next)
					}
					return nil
				},
			})
			if !errors.Is(err, ErrPublicationRecoveryRequired) {
				t.Fatalf("Publish(cleanup mutation) error = %v, want recovery required", err)
			}
			if _, err := os.Lstat(preserved); err != nil {
				t.Fatalf("cleanup mutation was removed: %v", err)
			}
			if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(publicationJournalRelativePath))); err != nil {
				t.Fatalf("journal recovery authority was removed: %v", err)
			}
		})
	}
}

func TestObserveRegularAtRejectsFIFOWithoutBlocking(t *testing.T) {
	directoryName := t.TempDir()
	fifo := filepath.Join(directoryName, "value")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("create FIFO: %v", err)
	}
	directory, err := os.Open(directoryName)
	if err != nil {
		t.Fatalf("open directory: %v", err)
	}
	defer directory.Close()
	done := make(chan error, 1)
	go func() {
		_, _, observeErr := observeRegularAt(directory, "value", maximumPublicationFileBytes)
		done <- observeErr
	}()
	select {
	case observeErr := <-done:
		if observeErr == nil {
			t.Fatal("observeRegularAt accepted FIFO")
		}
	case <-time.After(time.Second):
		t.Fatal("observeRegularAt blocked on FIFO")
	}
}

func TestAcquirePublicationLockRejectsFIFOWithoutBlocking(t *testing.T) {
	projectRoot := t.TempDir()
	root, err := openPublicationRoot(projectRoot)
	if err != nil {
		t.Fatalf("open publication root: %v", err)
	}
	defer root.close()
	lockPath := filepath.Join(projectRoot, filepath.FromSlash(publicationLockRelativePath))
	if err := unix.Mkfifo(lockPath, 0o600); err != nil {
		t.Fatalf("create lock FIFO: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, lockErr := acquirePublicationLock(context.Background(), root)
		done <- lockErr
	}()
	select {
	case lockErr := <-done:
		if lockErr == nil {
			t.Fatal("acquirePublicationLock accepted FIFO")
		}
	case <-time.After(time.Second):
		t.Fatal("acquirePublicationLock blocked on FIFO")
	}
}

func TestVerifyReservedGeneratedPreflightRejectsReboundProjectRoot(t *testing.T) {
	projectRoot := t.TempDir()
	root, err := openPublicationRoot(projectRoot)
	if err != nil {
		t.Fatalf("open publication root: %v", err)
	}
	defer root.close()
	retained := projectRoot + "-retained"
	defer os.RemoveAll(retained)
	if err := os.Rename(projectRoot, retained); err != nil {
		t.Fatalf("rename retained root: %v", err)
	}
	if err := os.Mkdir(projectRoot, 0o755); err != nil {
		t.Fatalf("create replacement root: %v", err)
	}
	err = verifyReservedGeneratedPreflight(context.Background(), root, map[string]struct{}{})
	if !errors.Is(err, ErrGeneratedConflict) {
		t.Fatalf("verifyReservedGeneratedPreflight(rebound root) error = %v, want conflict", err)
	}
}

func TestRecoverExistingPublicationRemovesBoundedPartialJournalOrphan(t *testing.T) {
	projectRoot := t.TempDir()
	root, err := openPublicationRoot(projectRoot)
	if err != nil {
		t.Fatalf("open publication root: %v", err)
	}
	defer root.close()
	transactionID := "00112233445566778899aabbccddeeff"
	tx, err := root.createTransaction(transactionID)
	if err != nil {
		t.Fatalf("create transaction: %v", err)
	}
	if err := writeExclusiveRegularAt(tx.root, "journal.tmp", []byte("{\"format_version\":"), 0o600); err != nil {
		t.Fatalf("write partial journal: %v", err)
	}
	if err := tx.close(); err != nil {
		t.Fatalf("close interrupted transaction: %v", err)
	}
	if err := recoverExistingPublication(root); err != nil {
		t.Fatalf("recover partial pre-journal transaction: %v", err)
	}
	transactions, err := root.transactionNames()
	if err != nil || len(transactions) != 0 {
		t.Fatalf("transactions after recovery = %v, err=%v", transactions, err)
	}
}

func TestPublishRejectsSymlinkAndNonregularPathsWithoutFollowingThem(t *testing.T) {
	bundle := projectGenerateTestBundle(t)
	first := bundle.Files()[0]
	tests := []struct {
		name  string
		setup func(t *testing.T, root, outside string)
	}{
		{name: "project root symlink", setup: func(t *testing.T, root, outside string) {
			t.Helper()
			if err := os.Remove(root); err != nil {
				t.Fatalf("remove temporary root: %v", err)
			}
			if err := os.Symlink(outside, root); err != nil {
				t.Fatalf("symlink project root: %v", err)
			}
		}},
		{name: "metadata directory symlink", setup: func(t *testing.T, root, outside string) {
			t.Helper()
			if err := os.Symlink(outside, filepath.Join(root, ".godj")); err != nil {
				t.Fatalf("symlink metadata directory: %v", err)
			}
		}},
		{name: "lock symlink", setup: func(t *testing.T, root, outside string) {
			t.Helper()
			if err := os.Mkdir(filepath.Join(root, ".godj"), 0o700); err != nil {
				t.Fatalf("create metadata directory: %v", err)
			}
			if err := os.Symlink(filepath.Join(outside, "outside.txt"), filepath.Join(root, filepath.FromSlash(publicationLockRelativePath))); err != nil {
				t.Fatalf("symlink publication lock: %v", err)
			}
		}},
		{name: "lock fifo", setup: func(t *testing.T, root, _ string) {
			t.Helper()
			if err := os.Mkdir(filepath.Join(root, ".godj"), 0o700); err != nil {
				t.Fatalf("create metadata directory: %v", err)
			}
			if err := unix.Mkfifo(filepath.Join(root, filepath.FromSlash(publicationLockRelativePath)), 0o600); err != nil {
				t.Fatalf("create publication lock FIFO: %v", err)
			}
		}},
		{name: "package directory symlink", setup: func(t *testing.T, root, outside string) {
			t.Helper()
			if err := os.Symlink(outside, filepath.Join(root, "authors")); err != nil {
				t.Fatalf("symlink package directory: %v", err)
			}
		}},
		{name: "generated file symlink", setup: func(t *testing.T, root, outside string) {
			t.Helper()
			if err := os.MkdirAll(filepath.Dir(filepath.Join(root, filepath.FromSlash(first.Path))), 0o755); err != nil {
				t.Fatalf("create generated parent: %v", err)
			}
			if err := os.Symlink(filepath.Join(outside, "outside.txt"), filepath.Join(root, filepath.FromSlash(first.Path))); err != nil {
				t.Fatalf("symlink generated file: %v", err)
			}
		}},
		{name: "generated fifo", setup: func(t *testing.T, root, _ string) {
			t.Helper()
			filename := filepath.Join(root, filepath.FromSlash(first.Path))
			if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
				t.Fatalf("create generated parent: %v", err)
			}
			if err := unix.Mkfifo(filename, 0o600); err != nil {
				t.Fatalf("create generated FIFO: %v", err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			outsideFile := filepath.Join(outside, "outside.txt")
			outsideBytes := []byte("outside must remain exact\n")
			if err := os.WriteFile(outsideFile, outsideBytes, 0o644); err != nil {
				t.Fatalf("write outside file: %v", err)
			}
			test.setup(t, root, outside)
			err := Publish(context.Background(), root, bundle, publicationTestVerifier(t, bundle, nil))
			if !errors.Is(err, ErrGeneratedConflict) {
				t.Fatalf("Publish(unsafe path) error = %v, want conflict", err)
			}
			if got, readErr := os.ReadFile(outsideFile); readErr != nil || !bytes.Equal(got, outsideBytes) {
				t.Fatalf("outside file changed: bytes=%q err=%v", got, readErr)
			}
		})
	}
}

func TestPublishRemovesOnlyExactPriorOwnedStaleGeneratedFile(t *testing.T) {
	for _, modified := range []bool{false, true} {
		name := "exact"
		if modified {
			name = "modified"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			bundle := projectGenerateTestBundle(t)
			writeProjectGenerateTestBundle(t, root, bundle)
			stalePath := "project/zz_godj_retired.go"
			staleBytes := []byte("package project\n\nconst retired = true\n")
			writeProjectGenerateTestFile(t, root, stalePath, staleBytes, 0o644)
			manifest, err := decodeCommittedManifest(bundle.Manifest())
			if err != nil {
				t.Fatalf("decode manifest: %v", err)
			}
			manifest.GeneratorABI = append(manifest.GeneratorABI, manifestABI{
				Role: "project.retired", Filename: "zz_godj_retired.go", Version: "godj-codegen-retired-v0",
			})
			manifest.Files = append(manifest.Files, manifestFile{
				Path: stalePath, Owner: "project", Mode: "0644", SHA256: digestBytes(staleBytes),
			})
			sort.Slice(manifest.Files, func(left, right int) bool { return manifest.Files[left].Path < manifest.Files[right].Path })
			priorDocument, err := json.Marshal(manifest)
			if err != nil {
				t.Fatalf("encode prior manifest: %v", err)
			}
			priorDocument = append(priorDocument, '\n')
			writeProjectGenerateTestFile(t, root, generatedManifestRelativePath, priorDocument, 0o644)
			if modified {
				writeProjectGenerateTestFile(t, root, stalePath, []byte("package project\n// user edit\n"), 0o644)
			}

			err = Publish(context.Background(), root, bundle, publicationTestVerifier(t, bundle, nil))
			if modified {
				if !errors.Is(err, ErrGeneratedConflict) {
					t.Fatalf("Publish(modified stale) error = %v, want conflict", err)
				}
				if got := mustReadPublicationFile(t, root, stalePath); !bytes.Equal(got, []byte("package project\n// user edit\n")) {
					t.Fatalf("modified stale file changed: %q", got)
				}
				if got := mustReadPublicationFile(t, root, generatedManifestRelativePath); !bytes.Equal(got, priorDocument) {
					t.Fatal("prior manifest changed while rejecting modified stale file")
				}
				return
			}
			if err != nil {
				t.Fatalf("Publish(exact stale) error = %v", err)
			}
			if _, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(stalePath))); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("exact prior-owned stale file remains: %v", statErr)
			}
			assertPublishedBundle(t, root, bundle)
			assertPublicationControlClean(t, root)
		})
	}
}

func TestPublishStaleDeleteRenameFaultRestoresExactPriorOwnedFile(t *testing.T) {
	root := t.TempDir()
	bundle := projectGenerateTestBundle(t)
	stalePath := "project/zz_godj_retired.go"
	staleBytes := []byte("package project\n\nconst retired = true\n")
	priorDocument := writeRetiredPublicationTestBundle(t, root, bundle, stalePath, staleBytes)
	fault := errors.New("fault after stale backup")
	fired := false
	err := publishWithHooks(context.Background(), root, bundle, publicationTestVerifier(t, bundle, nil), publicationHooks{
		after: func(step publicationStep, relative string, _ int) error {
			if step == publicationStepPriorBackedUp && relative == stalePath {
				fired = true
				return fault
			}
			return nil
		},
	})
	if !fired || !errors.Is(err, fault) {
		t.Fatalf("Publish(stale backup fault) fired=%t error=%v, want %v", fired, err, fault)
	}
	if got := mustReadPublicationFile(t, root, stalePath); !bytes.Equal(got, staleBytes) {
		t.Fatalf("stale file was not restored exactly: %q", got)
	}
	if got := mustReadPublicationFile(t, root, generatedManifestRelativePath); !bytes.Equal(got, priorDocument) {
		t.Fatal("prior manifest was not restored after stale backup fault")
	}
	assertPublicationControlClean(t, root)
}

func TestPublicationJournalStrictRoundTripAndRejectsUnownedMutation(t *testing.T) {
	digest := digestBytes([]byte("value"))
	journal := publicationJournal{
		FormatVersion:  publicationJournalFormatVersion,
		TransactionID:  "00112233445566778899aabbccddeeff",
		SnapshotSHA256: digest,
		PriorManifest:  journalManifestState{},
		NextManifest:   journalManifestState{Present: true, SHA256: digest},
		Directories:    []journalDirectory{{Path: "generated", PriorPresent: false}},
		Files: []journalFile{{
			Path:  "generated/zz_godj_generated.go",
			Prior: journalFileState{Present: true, Owned: false, SHA256: digest, Mode: 0o644},
			Next:  journalFileState{Present: true, Owned: true, SHA256: digest, Mode: 0o644},
		}},
	}
	document, err := encodePublicationJournal(journal)
	if err != nil {
		t.Fatalf("encodePublicationJournal() error = %v", err)
	}
	decoded, err := decodePublicationJournal(document)
	if err != nil {
		t.Fatalf("decodePublicationJournal() error = %v", err)
	}
	canonical, err := encodePublicationJournal(decoded)
	if err != nil || !bytes.Equal(canonical, document) {
		t.Fatalf("journal round trip differs: err=%v", err)
	}

	mutated := journal
	mutated.Files = append([]journalFile(nil), journal.Files...)
	mutated.Files[0].Next.SHA256 = digestBytes([]byte("different"))
	if _, err := encodePublicationJournal(mutated); err == nil {
		t.Fatal("journal accepted mutation of unowned prior file")
	}
	ownedWithoutManifest := journal
	ownedWithoutManifest.Files = append([]journalFile(nil), journal.Files...)
	ownedWithoutManifest.Files[0].Prior.Owned = true
	if _, err := encodePublicationJournal(ownedWithoutManifest); err == nil {
		t.Fatal("journal accepted owned prior file without a prior manifest")
	}
	duplicate := bytes.Replace(document, []byte(`{"format_version":1`), []byte(`{"format_version":1,"format_version":1`), 1)
	if _, err := decodePublicationJournal(duplicate); err == nil {
		t.Fatal("journal accepted duplicate JSON member")
	}
	unsafePath := journal
	unsafePath.Files = append([]journalFile(nil), journal.Files...)
	unsafePath.Files[0].Path = "generated/user.go"
	if _, err := encodePublicationJournal(unsafePath); err == nil {
		t.Fatal("journal accepted non-generated target path")
	}
	missingDirectory := journal
	missingDirectory.Directories = nil
	if _, err := encodePublicationJournal(missingDirectory); err == nil {
		t.Fatal("journal accepted directory inventory differing from next files")
	}
	deep := []byte(`[[[[[[[[[[null]]]]]]]]]]`)
	if _, err := decodePublicationJournal(deep); err == nil {
		t.Fatal("journal accepted excessive JSON depth")
	}
}

func TestRecoveryJournalBindingRejectsManifestAndSnapshotDrift(t *testing.T) {
	rootName := t.TempDir()
	priorBundle := projectGenerateTestBundle(t)
	nextBundle := changedProjectGenerateTestBundle(t)
	writeProjectGenerateTestBundle(t, rootName, priorBundle)
	root, err := openPublicationRoot(rootName)
	if err != nil {
		t.Fatalf("open publication root: %v", err)
	}
	defer root.close()
	next, err := snapshotPublicationBundle(nextBundle)
	if err != nil {
		t.Fatalf("snapshot next bundle: %v", err)
	}
	journal, _, err := capturePublicationJournal(root, "00112233445566778899aabbccddeeff", next)
	if err != nil {
		t.Fatalf("capture publication journal: %v", err)
	}
	priorManifest, err := decodeCommittedManifest(priorBundle.Manifest())
	if err != nil {
		t.Fatalf("decode prior manifest: %v", err)
	}
	nextManifest, err := decodeCommittedManifest(nextBundle.Manifest())
	if err != nil {
		t.Fatalf("decode next manifest: %v", err)
	}
	if err := validateRecoveryJournalManifestBinding(journal, &priorManifest, &nextManifest, true); err != nil {
		t.Fatalf("valid journal binding error = %v", err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*publicationJournal)
	}{
		{name: "snapshot", mutate: func(value *publicationJournal) { value.SnapshotSHA256 = digestBytes([]byte("different")) }},
		{name: "prior hash", mutate: func(value *publicationJournal) {
			for index := range value.Files {
				if value.Files[index].Prior.Owned {
					value.Files[index].Prior.SHA256 = digestBytes([]byte("different-prior"))
					return
				}
			}
		}},
		{name: "next hash", mutate: func(value *publicationJournal) {
			for index := range value.Files {
				if value.Files[index].Next.Present {
					value.Files[index].Next.SHA256 = digestBytes([]byte("different-next"))
					return
				}
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			mutated := journal
			mutated.Files = append([]journalFile(nil), journal.Files...)
			test.mutate(&mutated)
			if err := validateRecoveryJournalManifestBinding(mutated, &priorManifest, &nextManifest, true); err == nil {
				t.Fatal("journal binding accepted semantic drift")
			}
		})
	}
}

func TestPublishRejectsManifestModeDriftAndRecoveryRetainsAuthority(t *testing.T) {
	root := t.TempDir()
	bundle := projectGenerateTestBundle(t)
	if err := Publish(context.Background(), root, bundle, publicationTestVerifier(t, bundle, nil)); err != nil {
		t.Fatalf("Publish(first) error = %v", err)
	}
	manifestPath := filepath.Join(root, filepath.FromSlash(generatedManifestRelativePath))
	if err := os.Chmod(manifestPath, 0o600); err != nil {
		t.Fatalf("chmod manifest: %v", err)
	}
	if err := Publish(context.Background(), root, bundle, publicationTestVerifier(t, bundle, nil)); !errors.Is(err, ErrGeneratedConflict) {
		t.Fatalf("Publish(chmod manifest) error = %v, want conflict", err)
	}

	if err := os.Chmod(manifestPath, 0o644); err != nil {
		t.Fatalf("restore manifest mode: %v", err)
	}
	next := changedProjectGenerateTestBundle(t)
	var injected string
	err := publishWithHooks(context.Background(), root, next, publicationTestVerifier(t, next, nil), publicationHooks{
		after: func(step publicationStep, _ string, _ int) error {
			if step != publicationStepManifestCommit {
				return nil
			}
			entries, readErr := os.ReadDir(filepath.Join(root, filepath.FromSlash(publicationTransactionDirectoryPath)))
			if readErr != nil || len(entries) != 1 {
				return fmt.Errorf("find transaction: entries=%v err=%w", entries, readErr)
			}
			injected = filepath.Join(root, filepath.FromSlash(publicationTransactionDirectoryPath), entries[0].Name(), "unexpected")
			return os.WriteFile(injected, []byte("retain journal\n"), 0o600)
		},
	})
	if !errors.Is(err, ErrPublicationRecoveryRequired) {
		t.Fatalf("Publish(cleanup interruption) error = %v, want recovery required", err)
	}
	if err := os.Chmod(manifestPath, 0o600); err != nil {
		t.Fatalf("chmod committed manifest: %v", err)
	}
	err = Publish(context.Background(), root, next, publicationTestVerifier(t, next, nil))
	if !errors.Is(err, ErrPublicationRecoveryRequired) {
		t.Fatalf("Publish(recover chmod manifest) error = %v, want recovery required", err)
	}
	if _, statErr := os.Lstat(injected); statErr != nil {
		t.Fatalf("mode-drift recovery removed ambiguous transaction entry: %v", statErr)
	}
	if _, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(publicationJournalRelativePath))); statErr != nil {
		t.Fatalf("mode-drift recovery removed journal authority: %v", statErr)
	}
}

func changedProjectGenerateTestBundle(t *testing.T) codegen.GeneratedBundle {
	t.Helper()
	spec := projectGenerateTestSpec()
	spec.Apps[1].Schema.Models[0].Fields[0].MaxLength++
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatalf("GenerateProject(changed) error = %v", err)
	}
	return bundle
}

func manyChangedProjectGenerateTestBundle(t *testing.T) codegen.GeneratedBundle {
	t.Helper()
	spec := projectGenerateTestSpec()
	spec.Project.PackageName = "projectnext"
	for index := range spec.Apps {
		spec.Apps[index].Package.PackageName += "next"
	}
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatalf("GenerateProject(many changed) error = %v", err)
	}
	return bundle
}

func nestedProjectGenerateTestBundle(t *testing.T) codegen.GeneratedBundle {
	t.Helper()
	spec := projectGenerateTestSpec()
	spec.Project.Directory = "generated/project"
	for index := range spec.Apps {
		spec.Apps[index].Package.Directory = "generated/" + spec.Apps[index].Schema.AppLabel
	}
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatalf("GenerateProject(nested directories) error = %v", err)
	}
	return bundle
}

func snapshotPublicationBundleForTest(t *testing.T, bundle codegen.GeneratedBundle) publicationBundle {
	t.Helper()
	snapshot, err := snapshotPublicationBundle(bundle)
	if err != nil {
		t.Fatalf("snapshotPublicationBundle() error = %v", err)
	}
	return snapshot
}

func writeRetiredPublicationTestBundle(
	t *testing.T,
	root string,
	bundle codegen.GeneratedBundle,
	stalePath string,
	staleBytes []byte,
) []byte {
	t.Helper()
	writeProjectGenerateTestBundle(t, root, bundle)
	writeProjectGenerateTestFile(t, root, stalePath, staleBytes, 0o644)
	manifest, err := decodeCommittedManifest(bundle.Manifest())
	if err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	manifest.GeneratorABI = append(manifest.GeneratorABI, manifestABI{
		Role: "project.retired", Filename: filepath.Base(stalePath), Version: "godj-codegen-retired-v0",
	})
	manifest.Files = append(manifest.Files, manifestFile{
		Path: stalePath, Owner: "project", Mode: "0644", SHA256: digestBytes(staleBytes),
	})
	sort.Slice(manifest.Files, func(left, right int) bool { return manifest.Files[left].Path < manifest.Files[right].Path })
	document, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("encode prior manifest: %v", err)
	}
	document = append(document, '\n')
	writeProjectGenerateTestFile(t, root, generatedManifestRelativePath, document, 0o644)
	return document
}

func publicationTestVerifier(t *testing.T, bundle codegen.GeneratedBundle, calls *atomic.Int32) CandidateVerifier {
	t.Helper()
	return CandidateVerifyFunc(func(ctx context.Context, candidateRoot string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if calls != nil {
			calls.Add(1)
		}
		for _, file := range bundle.Files() {
			filename := filepath.Join(candidateRoot, filepath.FromSlash(file.Path))
			contents, err := os.ReadFile(filename)
			if err != nil {
				return fmt.Errorf("read candidate %s: %w", file.Path, err)
			}
			info, err := os.Stat(filename)
			if err != nil || info.Mode().Perm() != file.Mode || !bytes.Equal(contents, file.Source()) {
				return fmt.Errorf("candidate file %s differs", file.Path)
			}
		}
		manifest := filepath.Join(candidateRoot, filepath.FromSlash(generatedManifestRelativePath))
		contents, err := os.ReadFile(manifest)
		if err != nil || !bytes.Equal(contents, bundle.Manifest()) {
			return fmt.Errorf("candidate manifest differs: %w", err)
		}
		return nil
	})
}

func assertPublishedBundle(t *testing.T, root string, bundle codegen.GeneratedBundle) {
	t.Helper()
	for _, file := range bundle.Files() {
		contents := mustReadPublicationFile(t, root, file.Path)
		if !bytes.Equal(contents, file.Source()) {
			t.Errorf("published file %s differs", file.Path)
		}
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(file.Path)))
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != file.Mode {
			t.Errorf("published file %s mode/kind differs: info=%v err=%v", file.Path, info, err)
		}
	}
	if got := mustReadPublicationFile(t, root, generatedManifestRelativePath); !bytes.Equal(got, bundle.Manifest()) {
		t.Error("published manifest differs")
	}
}

func assertPublicationControlClean(t *testing.T, root string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(publicationJournalRelativePath))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("publication journal remains: %v", err)
	}
	transactions := filepath.Join(root, filepath.FromSlash(publicationTransactionDirectoryPath))
	entries, err := os.ReadDir(transactions)
	if err != nil {
		t.Fatalf("read transaction directory: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, len(entries))
		for index, entry := range entries {
			names[index] = entry.Name()
		}
		sort.Strings(names)
		t.Fatalf("publication transactions remain: %v", names)
	}
}

func mustReadPublicationFile(t *testing.T, root, relative string) []byte {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatalf("read %s: %v", relative, err)
	}
	return contents
}
