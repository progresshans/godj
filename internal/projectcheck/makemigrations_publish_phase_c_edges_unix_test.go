//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	writerprotocol "github.com/progresshans/godj/internal/projectmigration/protocol"
	"golang.org/x/sys/unix"
)

type phaseCLockWaitContext struct {
	done     <-chan struct{}
	waiting  chan struct{}
	waitOnce sync.Once
}

type phaseCTempMutatingInventoryBackend struct {
	delegate *liveMakemigrationsBackend
	root     string
	mutated  bool
	temp     string
	bytes    []byte
}

func (backend *phaseCTempMutatingInventoryBackend) Execute(
	ctx context.Context,
	interrupt <-chan struct{},
	stage ProcessStage,
	command Command,
) ProcessResult {
	result := backend.delegate.Execute(ctx, interrupt, stage, command)
	if stage != MakemigrationsInventoryStage || backend.mutated {
		return result
	}
	names := phaseCReservedTempNamesFromRoot(backend.root)
	if len(names) != 1 {
		return result
	}
	backend.mutated = true
	backend.temp = names[0]
	path := filepath.Join(backend.root, "migrations", backend.temp)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err == nil && file != nil {
		_, err = file.Write(backend.bytes)
	}
	if err == nil && file != nil {
		err = file.Sync()
	}
	if file != nil {
		err = errors.Join(err, file.Close())
	}
	if err != nil {
		backend.delegate.mu.Lock()
		if backend.delegate.err == nil {
			backend.delegate.err = err
		}
		backend.delegate.mu.Unlock()
	}
	return result
}

func (ctx *phaseCLockWaitContext) Deadline() (time.Time, bool) {
	return time.Time{}, false
}

func (ctx *phaseCLockWaitContext) Done() <-chan struct{} {
	ctx.waitOnce.Do(func() { close(ctx.waiting) })
	return ctx.done
}

func (ctx *phaseCLockWaitContext) Err() error {
	select {
	case <-ctx.done:
		return context.Canceled
	default:
		return nil
	}
}

func (ctx *phaseCLockWaitContext) Value(any) any {
	return nil
}

func TestMakemigrationsInterruptWhileWaitingForWriterLockDoesNotMutate(t *testing.T) {
	fixture := newMakemigrationsRunFixture(t, false)
	before := generationTreeSnapshot(t, fixture.root)

	var selectionReport Report
	selected, selectionFailure := selectProject(fixture.root, commandArguments{}, &selectionReport)
	if selectionFailure != nil {
		t.Fatalf("select project: %+v", *selectionFailure)
	}
	defer selected.close()
	root, rootFailure := retainMakemigrationsWriterRoot(selected, "migrations")
	if rootFailure != nil {
		t.Fatalf("retain writer root: %+v", *rootFailure)
	}
	defer root.close()

	holder, err := os.Open(filepath.Join(fixture.root, "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if err := unix.Flock(int(holder.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatalf("hold writer lock: %v", err)
	}
	defer unix.Flock(int(holder.Fd()), unix.LOCK_UN)

	terminal := make(chan struct{})
	ctx := &phaseCLockWaitContext{done: terminal, waiting: make(chan struct{})}
	type lockOutcome struct {
		lock    *makemigrationsWriterLock
		failure *MakemigrationsFailure
	}
	outcomes := make(chan lockOutcome, 1)
	var report MakemigrationsReport
	go func() {
		lock, failure := acquireMakemigrationsWriterLock(
			MakemigrationsInvocation{Context: ctx, Interrupt: terminal},
			root,
			makemigrationsPublicationHooks{},
			&report,
		)
		outcomes <- lockOutcome{lock: lock, failure: failure}
	}()

	select {
	case outcome := <-outcomes:
		if outcome.lock != nil {
			_ = outcome.lock.release()
		}
		t.Fatalf("lock acquisition returned before entering the contention wait: %+v", outcome.failure)
	case <-ctx.waiting:
	}
	close(terminal)

	var outcome lockOutcome
	select {
	case outcome = <-outcomes:
	case <-time.After(2 * time.Second):
		t.Fatal("interrupt did not release the contended writer-lock wait")
	}
	if outcome.lock != nil {
		_ = outcome.lock.release()
		t.Fatal("writer lock was acquired while the directory remained externally locked")
	}
	want := MakemigrationsFailure{
		Category: MakemigrationsCategoryProcess,
		Code:     MakemigrationsCodeProjectInterrupted,
	}
	if outcome.failure == nil || *outcome.failure != want || report.WriterLockAcquisitions != 0 {
		t.Fatalf("failure=%+v report=%+v", outcome.failure, report)
	}
	if after := generationTreeSnapshot(t, fixture.root); !reflect.DeepEqual(before, after) {
		t.Fatalf("contended lock wait mutated the project\nbefore=%v\nafter=%v", before, after)
	}
}

func TestMakemigrationsCleanupWithSecondReservedTempRemovesOnlyOwnedTemp(t *testing.T) {
	fixture := newMakemigrationsRunFixture(t, false)
	spec := makemigrationsRunSpec()
	snapshot, err := liveMakemigrationsSnapshot(fixture.root, spec)
	if err != nil {
		t.Fatal(err)
	}
	candidate := snapshot.Candidates()[0]
	document := candidate.Document()
	defer clear(document)
	target, err := writerprotocol.CandidateTargetBasename(candidate.App(), candidate.Name())
	if err != nil {
		t.Fatal(err)
	}
	ownedTemp := makemigrationsTempBasename(target, document)
	injectedTemp := makemigrationsTempPrefix + strings.Repeat("f", 64)
	if injectedTemp == ownedTemp {
		injectedTemp = makemigrationsTempPrefix + strings.Repeat("e", 64)
	}
	injectedDocument := []byte("foreign reserved temp")

	backend := &liveMakemigrationsBackend{inventory: fixture.inventory, root: fixture.root, spec: spec}
	injected := false
	hookFailure := errors.New("phase-c injected pre-rename failure")
	report, _, stderr := runLiveMakemigrations(
		fixture, backend, context.Background(), nil,
		makemigrationsPublicationHooks{after: func(step makemigrationsPublicationStep, _ string, _ int) error {
			if step != makemigrationsStepBeforeRename || injected {
				return nil
			}
			injected = true
			writeSyncedMakemigrationsFile(t, filepath.Join(fixture.root, "migrations", injectedTemp), injectedDocument)
			if err := syncDirectoryPath(filepath.Join(fixture.root, "migrations")); err != nil {
				return err
			}
			return hookFailure
		}},
	)
	want := makemigrationsRecoveryRequired()
	if backend.Error() != nil || !injected || report.ExitCode != 3 || !report.HasMakemigrationsFailure ||
		report.MakemigrationsFailure != want || report.PublicationRenames != 0 || report.PublishedCandidates != 0 ||
		stderr != want.Category+"/"+want.Code+"\n" {
		t.Fatalf("report=%+v backend=%v injected=%t stderr=%q", report, backend.Error(), injected, stderr)
	}
	if _, err := os.Lstat(filepath.Join(fixture.root, "migrations", ownedTemp)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned temp was not removed: %v", err)
	}
	actual, err := os.ReadFile(filepath.Join(fixture.root, "migrations", injectedTemp))
	if err != nil || !bytes.Equal(actual, injectedDocument) {
		t.Fatalf("injected temp changed: err=%v bytes=%q", err, actual)
	}
	if got := phaseCReservedTempNames(t, fixture.root); !reflect.DeepEqual(got, []string{injectedTemp}) {
		t.Fatalf("reserved temps=%v want=[%s]", got, injectedTemp)
	}
	if _, err := os.Lstat(filepath.Join(fixture.root, "migrations", target)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target unexpectedly exists: %v", err)
	}
}

func TestMakemigrationsTempCleanupDetectsSameBasenameRecreation(t *testing.T) {
	fixture := newMakemigrationsRunFixture(t, false)
	spec := makemigrationsRunSpec()
	snapshot, err := liveMakemigrationsSnapshot(fixture.root, spec)
	if err != nil {
		t.Fatal(err)
	}
	candidate := snapshot.Candidates()[0]
	document := candidate.Document()
	defer clear(document)
	target, err := writerprotocol.CandidateTargetBasename(candidate.App(), candidate.Name())
	if err != nil {
		t.Fatal(err)
	}
	temp := makemigrationsTempBasename(target, document)
	replacement := []byte("replacement with the owned basename")
	hookFailure := errors.New("phase-c injected temp failure")
	recreated := false

	backend := &liveMakemigrationsBackend{inventory: fixture.inventory, root: fixture.root, spec: spec}
	report, _, stderr := runLiveMakemigrations(
		fixture, backend, context.Background(), nil,
		makemigrationsPublicationHooks{
			after: func(step makemigrationsPublicationStep, _ string, _ int) error {
				if step == makemigrationsStepTempFsynced {
					return hookFailure
				}
				return nil
			},
			syncDirectory: func(directory *os.File, step makemigrationsPublicationStep, _ int) error {
				if step == makemigrationsSyncTempCleanup && !recreated {
					recreated = true
					writeSyncedMakemigrationsFile(t, filepath.Join(fixture.root, "migrations", temp), replacement)
				}
				return directory.Sync()
			},
		},
	)
	want := makemigrationsRecoveryRequired()
	if backend.Error() != nil || !recreated || report.ExitCode != 3 || !report.HasMakemigrationsFailure ||
		report.MakemigrationsFailure != want || report.PublicationRenames != 0 || report.PublishedCandidates != 0 ||
		stderr != want.Category+"/"+want.Code+"\n" {
		t.Fatalf("report=%+v backend=%v recreated=%t stderr=%q", report, backend.Error(), recreated, stderr)
	}
	actual, err := os.ReadFile(filepath.Join(fixture.root, "migrations", temp))
	if err != nil || !bytes.Equal(actual, replacement) {
		t.Fatalf("recreated temp not preserved: err=%v bytes=%q", err, actual)
	}
	if got := phaseCReservedTempNames(t, fixture.root); !reflect.DeepEqual(got, []string{temp}) {
		t.Fatalf("reserved temps=%v want=[%s]", got, temp)
	}
	if _, err := os.Lstat(filepath.Join(fixture.root, "migrations", target)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target unexpectedly exists: %v", err)
	}
}

func TestMakemigrationsCommitDirectorySyncErrorsPreserveRenamedTarget(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "EINTR", err: unix.EINTR},
		{name: "EIO", err: unix.EIO},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMakemigrationsRunFixture(t, false)
			spec := makemigrationsRunSpec()
			snapshot, err := liveMakemigrationsSnapshot(fixture.root, spec)
			if err != nil {
				t.Fatal(err)
			}
			candidate := snapshot.Candidates()[0]
			document := candidate.Document()
			defer clear(document)
			target, err := writerprotocol.CandidateTargetBasename(candidate.App(), candidate.Name())
			if err != nil {
				t.Fatal(err)
			}

			backend := &liveMakemigrationsBackend{inventory: fixture.inventory, root: fixture.root, spec: spec}
			report, _, stderr := runLiveMakemigrations(
				fixture, backend, context.Background(), nil,
				makemigrationsPublicationHooks{syncDirectory: func(directory *os.File, step makemigrationsPublicationStep, _ int) error {
					if step == makemigrationsSyncCandidateCommitted {
						return test.err
					}
					return directory.Sync()
				}},
			)
			want := makemigrationsRecoveryRequired()
			if backend.Error() != nil || report.ExitCode != 3 || !report.HasMakemigrationsFailure ||
				report.MakemigrationsFailure != want || report.PublicationRenames != 1 || report.PublishedCandidates != 0 ||
				stderr != want.Category+"/"+want.Code+"\n" {
				t.Fatalf("report=%+v backend=%v stderr=%q", report, backend.Error(), stderr)
			}
			path := filepath.Join(fixture.root, "migrations", target)
			actual, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(actual, document) {
				t.Fatalf("renamed target changed: err=%v", err)
			}
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
				t.Fatalf("renamed target mode=%v err=%v", info, err)
			}
			assertNoMakemigrationsReservedTemps(t, fixture.root)
		})
	}
}

func TestMakemigrationsUnsupportedNoReplaceDoesNotFallback(t *testing.T) {
	for _, syscallError := range []error{unix.ENOSYS, unix.EINVAL, unix.EOPNOTSUPP} {
		t.Run(syscallError.Error(), func(t *testing.T) {
			fixture := newMakemigrationsRunFixture(t, false)
			backend := &liveMakemigrationsBackend{inventory: fixture.inventory, root: fixture.root, spec: makemigrationsRunSpec()}
			renames := 0
			report, _, stderr := runLiveMakemigrations(
				fixture, backend, context.Background(), nil,
				makemigrationsPublicationHooks{renameNoReplace: func(int, string, int, string) error {
					renames++
					return syscallError
				}},
			)
			want := makemigrationsPublicationFailed()
			if backend.Error() != nil || renames != 1 || report.ExitCode != 3 || !report.HasMakemigrationsFailure ||
				report.MakemigrationsFailure != want || report.PublicationRenames != 0 || report.PublishedCandidates != 0 ||
				stderr != want.Category+"/"+want.Code+"\n" {
				t.Fatalf("report=%+v backend=%v renames=%d stderr=%q", report, backend.Error(), renames, stderr)
			}
			assertNoPublishedMakemigrationsTargets(t, fixture.root)
			assertNoMakemigrationsReservedTemps(t, fixture.root)
		})
	}
}

func TestMakemigrationsFinalCASRejectsReservedTempAfterCommittedCandidate(t *testing.T) {
	fixture := newMakemigrationsRunFixture(t, false)
	spec := makemigrationsRunSpec()
	snapshot, err := liveMakemigrationsSnapshot(fixture.root, spec)
	if err != nil {
		t.Fatal(err)
	}
	candidate := snapshot.Candidates()[0]
	document := candidate.Document()
	defer clear(document)
	target, err := writerprotocol.CandidateTargetBasename(candidate.App(), candidate.Name())
	if err != nil {
		t.Fatal(err)
	}
	injectedTemp := makemigrationsTempPrefix + strings.Repeat("d", 64)
	injectedDocument := []byte("post-commit reserved residue")
	injected := false

	backend := &liveMakemigrationsBackend{inventory: fixture.inventory, root: fixture.root, spec: spec}
	report, _, stderr := runLiveMakemigrations(
		fixture, backend, context.Background(), nil,
		makemigrationsPublicationHooks{after: func(step makemigrationsPublicationStep, _ string, _ int) error {
			if step != makemigrationsStepCandidateCommitted || injected {
				return nil
			}
			injected = true
			writeSyncedMakemigrationsFile(t, filepath.Join(fixture.root, "migrations", injectedTemp), injectedDocument)
			return syncDirectoryPath(filepath.Join(fixture.root, "migrations"))
		}},
	)
	want := makemigrationsRecoveryRequired()
	if backend.Error() != nil || !injected || report.ExitCode != 3 || !report.HasMakemigrationsFailure ||
		report.MakemigrationsFailure != want || report.PublicationRenames != 1 || report.PublishedCandidates != 1 ||
		stderr != want.Category+"/"+want.Code+"\n" {
		t.Fatalf("report=%+v backend=%v injected=%t stderr=%q", report, backend.Error(), injected, stderr)
	}
	actualTarget, err := os.ReadFile(filepath.Join(fixture.root, "migrations", target))
	if err != nil || !bytes.Equal(actualTarget, document) {
		t.Fatalf("committed target changed: err=%v", err)
	}
	actualTemp, err := os.ReadFile(filepath.Join(fixture.root, "migrations", injectedTemp))
	if err != nil || !bytes.Equal(actualTemp, injectedDocument) {
		t.Fatalf("injected residue changed: err=%v", err)
	}
}

func TestMakemigrationsPreRenameCASDetectsSameInodeSameLengthTempRewrite(t *testing.T) {
	fixture := newMakemigrationsRunFixture(t, false)
	spec := makemigrationsRunSpec()
	snapshot, err := liveMakemigrationsSnapshot(fixture.root, spec)
	if err != nil {
		t.Fatal(err)
	}
	candidate := snapshot.Candidates()[0]
	document := candidate.Document()
	defer clear(document)
	target, err := writerprotocol.CandidateTargetBasename(candidate.App(), candidate.Name())
	if err != nil {
		t.Fatal(err)
	}
	replacement := bytes.Repeat([]byte{'x'}, len(document))
	delegate := &liveMakemigrationsBackend{inventory: fixture.inventory, root: fixture.root, spec: spec}
	backend := &phaseCTempMutatingInventoryBackend{delegate: delegate, root: fixture.root, bytes: replacement}
	report, _, stderr := runLiveMakemigrations(
		fixture, backend, context.Background(), nil, makemigrationsPublicationHooks{},
	)
	want := makemigrationsRecoveryRequired()
	if delegate.Error() != nil || !backend.mutated || report.ExitCode != 3 || !report.HasMakemigrationsFailure ||
		report.MakemigrationsFailure != want || report.PublicationRenames != 0 || report.PublishedCandidates != 0 ||
		stderr != want.Category+"/"+want.Code+"\n" {
		t.Fatalf("report=%+v backend=%v mutated=%t stderr=%q", report, delegate.Error(), backend.mutated, stderr)
	}
	actual, err := os.ReadFile(filepath.Join(fixture.root, "migrations", backend.temp))
	if err != nil || !bytes.Equal(actual, replacement) {
		t.Fatalf("mutated temp changed or disappeared: err=%v", err)
	}
	if _, err := os.Lstat(filepath.Join(fixture.root, "migrations", target)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target unexpectedly exists: %v", err)
	}
}

func TestMakemigrationsReadOnlyModesDiagnoseReservedTempWithoutMutation(t *testing.T) {
	for _, mode := range [][]string{{"makemigrations", "--dry-run"}, {"makemigrations", "--check"}} {
		for _, residue := range []string{"owned", "wrong-digest", "fifo"} {
			t.Run(strings.TrimPrefix(mode[1], "--")+"/"+residue, func(t *testing.T) {
				fixture := newMakemigrationsRunFixture(t, false)
				candidate := fixture.snapshot.Candidates()[0]
				document := candidate.Document()
				defer clear(document)
				target, err := writerprotocol.CandidateTargetBasename(candidate.App(), candidate.Name())
				if err != nil {
					t.Fatal(err)
				}
				temp := makemigrationsTempBasename(target, document)
				if residue == "wrong-digest" {
					temp = makemigrationsTempPrefix + strings.Repeat("b", 64)
					if temp == makemigrationsTempBasename(target, document) {
						temp = makemigrationsTempPrefix + strings.Repeat("a", 64)
					}
				}
				path := filepath.Join(fixture.root, "migrations", temp)
				if residue == "fifo" {
					if err := unix.Mkfifo(path, 0o600); err != nil {
						t.Fatal(err)
					}
				} else {
					writeSyncedMakemigrationsFile(t, path, document)
				}
				beforeInfo, err := os.Lstat(path)
				if err != nil {
					t.Fatal(err)
				}
				var before map[string]string
				if residue != "fifo" {
					before = generationTreeSnapshot(t, fixture.root)
				}
				wire := encodeMakemigrationsRunResponse(t, fixture.snapshot)
				backend := &makemigrationsRunBackend{t: t, inventory: fixture.inventory, runnerWire: wire}
				report, stdout, stderr := executeMakemigrationsRun(t, fixture, mode, backend, workspaceHooks{})
				want := makemigrationsRecoveryRequired()
				if report.ExitCode != 3 || !report.HasMakemigrationsFailure || report.MakemigrationsFailure != want ||
					report.WriterLockAcquisitions != 0 || report.PublicationRenames != 0 ||
					report.PublicationDirectorySyncs != 0 || stdout != "" || stderr != want.Category+"/"+want.Code+"\n" {
					t.Fatalf("report=%+v stdout=%q stderr=%q", report, stdout, stderr)
				}
				afterInfo, err := os.Lstat(path)
				if err != nil || !os.SameFile(beforeInfo, afterInfo) || beforeInfo.Mode() != afterInfo.Mode() || beforeInfo.Size() != afterInfo.Size() {
					t.Fatalf("read-only diagnosis changed residue identity: before=%v after=%v err=%v", beforeInfo, afterInfo, err)
				}
				if residue != "fifo" {
					if after := generationTreeSnapshot(t, fixture.root); !reflect.DeepEqual(before, after) {
						t.Fatalf("read-only diagnosis mutated residue\nbefore=%v\nafter=%v", before, after)
					}
				}
				if _, err := os.Lstat(filepath.Join(fixture.root, "migrations", target)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("read-only diagnosis published target: %v", err)
				}
			})
		}
	}
}

func TestMakemigrationsSourceDriftBeforeSecondRenamePreservesDurablePrefix(t *testing.T) {
	fixture := newMakemigrationsRunFixture(t, false)
	spec := crossAppMakemigrationsSpec()
	want, err := liveMakemigrationsSnapshot(fixture.root, spec)
	if err != nil {
		t.Fatal(err)
	}
	candidates := want.Candidates()
	sourcePath := filepath.Join(fixture.root, "cmd", "site", "main.go")
	original, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	changed := bytes.Replace(original, []byte("value=1"), []byte("value=2"), 1)
	if bytes.Equal(changed, original) || len(changed) != len(original) {
		t.Fatal("source mutation fixture did not preserve length")
	}
	mutated := false
	backend := &liveMakemigrationsBackend{inventory: fixture.inventory, root: fixture.root, spec: spec}
	report, _, stderr := runLiveMakemigrations(
		fixture, backend, context.Background(), nil,
		makemigrationsPublicationHooks{after: func(step makemigrationsPublicationStep, _ string, index int) error {
			if step != makemigrationsStepCandidateCommitted || index != 0 || mutated {
				return nil
			}
			mutated = true
			return phaseCOverwriteAndSync(sourcePath, changed)
		}},
	)
	wantFailure := makemigrationsRecoveryRequired()
	if backend.Error() != nil || !mutated || report.ExitCode != 3 || !report.HasMakemigrationsFailure ||
		report.MakemigrationsFailure != wantFailure || report.PublicationRenames != 1 || report.PublishedCandidates != 1 ||
		stderr != wantFailure.Category+"/"+wantFailure.Code+"\n" {
		t.Fatalf("report=%+v backend=%v mutated=%t stderr=%q", report, backend.Error(), mutated, stderr)
	}
	assertPublishedMakemigrationsCandidates(t, fixture.root, candidates[:1])
	assertNoMakemigrationsReservedTemps(t, fixture.root)
	firstInfo := publishedMakemigrationsInfo(t, fixture.root)
	if err := phaseCOverwriteAndSync(sourcePath, original); err != nil {
		t.Fatal(err)
	}
	resumeBackend := &liveMakemigrationsBackend{inventory: fixture.inventory, root: fixture.root, spec: spec}
	resume, _, resumeStderr := runLiveMakemigrations(
		fixture, resumeBackend, context.Background(), nil, makemigrationsPublicationHooks{},
	)
	if resumeBackend.Error() != nil || resume.ExitCode != 0 || !resume.HasMakemigrationsResult ||
		resume.MakemigrationsResult.Status != "generated" || resume.PublicationRenames != 1 ||
		resume.PublishedCandidates != 1 || resumeStderr != "" {
		t.Fatalf("resume=%+v backend=%v stderr=%q", resume, resumeBackend.Error(), resumeStderr)
	}
	assertMakemigrationsPublishedPrefixPreserved(t, firstInfo, publishedMakemigrationsInfo(t, fixture.root))
	assertPublishedMakemigrationsCandidates(t, fixture.root, candidates)
}

func TestMakemigrationsInterruptAfterRenameKeepsRecoveryRequiredPrecedence(t *testing.T) {
	for _, step := range []makemigrationsPublicationStep{makemigrationsStepRenameReturned, makemigrationsStepDirectoryFsynced} {
		t.Run(string(step), func(t *testing.T) {
			fixture := newMakemigrationsRunFixture(t, false)
			interrupt := make(chan struct{})
			interrupted := false
			backend := &liveMakemigrationsBackend{inventory: fixture.inventory, root: fixture.root, spec: makemigrationsRunSpec()}
			report, _, stderr := runLiveMakemigrations(
				fixture, backend, context.Background(), interrupt,
				makemigrationsPublicationHooks{after: func(actual makemigrationsPublicationStep, _ string, index int) error {
					if actual == step && index == 0 && !interrupted {
						interrupted = true
						close(interrupt)
					}
					return nil
				}},
			)
			want := makemigrationsRecoveryRequired()
			if backend.Error() != nil || !interrupted || report.ExitCode != 3 || !report.HasMakemigrationsFailure ||
				report.MakemigrationsFailure != want || report.PublicationRenames != 1 || report.PublishedCandidates != 1 ||
				stderr != want.Category+"/"+want.Code+"\n" {
				t.Fatalf("report=%+v backend=%v interrupted=%t stderr=%q", report, backend.Error(), interrupted, stderr)
			}
			assertNoMakemigrationsReservedTemps(t, fixture.root)
			if _, err := strictLiveMakemigrationsState(fixture.root); err != nil {
				t.Fatalf("durable target is not strict after interrupt: %v", err)
			}
		})
	}
}

func TestMakemigrationsWriterRootRebindMutatesOnlyRetainedDirectoryAndResumes(t *testing.T) {
	fixture := newMakemigrationsRunFixture(t, false)
	spec := makemigrationsRunSpec()
	migrationRoot := filepath.Join(fixture.root, "migrations")
	retainedRoot := filepath.Join(fixture.root, "migrations-retained")
	sentinelPath := filepath.Join(migrationRoot, "sentinel")
	rebound := false
	backend := &liveMakemigrationsBackend{inventory: fixture.inventory, root: fixture.root, spec: spec}
	report, _, stderr := runLiveMakemigrations(
		fixture, backend, context.Background(), nil,
		makemigrationsPublicationHooks{after: func(step makemigrationsPublicationStep, _ string, index int) error {
			if step != makemigrationsStepBeforeRename || index != 0 || rebound {
				return nil
			}
			rebound = true
			if err := os.Rename(migrationRoot, retainedRoot); err != nil {
				return err
			}
			if err := os.Mkdir(migrationRoot, 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(sentinelPath, []byte("replacement-root"), 0o600); err != nil {
				return err
			}
			return syncDirectoryPath(fixture.root)
		}},
	)
	want := makemigrationsRecoveryRequired()
	if backend.Error() != nil || !rebound || report.ExitCode != 3 || !report.HasMakemigrationsFailure ||
		report.MakemigrationsFailure != want || report.PublicationRenames != 0 || report.PublishedCandidates != 0 ||
		stderr != want.Category+"/"+want.Code+"\n" {
		t.Fatalf("report=%+v backend=%v rebound=%t stderr=%q", report, backend.Error(), rebound, stderr)
	}
	if document, err := os.ReadFile(sentinelPath); err != nil || string(document) != "replacement-root" {
		t.Fatalf("replacement root sentinel changed: %q err=%v", document, err)
	}
	if entries, err := os.ReadDir(migrationRoot); err != nil || len(entries) != 1 || entries[0].Name() != "sentinel" {
		t.Fatalf("replacement root entries=%v err=%v", entries, err)
	}
	if got := phaseCReservedTempNamesFromDirectory(t, retainedRoot); len(got) != 1 {
		t.Fatalf("retained root temps=%v", got)
	}
	if err := os.Remove(sentinelPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(migrationRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(retainedRoot, migrationRoot); err != nil {
		t.Fatal(err)
	}
	if err := syncDirectoryPath(fixture.root); err != nil {
		t.Fatal(err)
	}
	resumeBackend := &liveMakemigrationsBackend{inventory: fixture.inventory, root: fixture.root, spec: spec}
	resume, _, resumeStderr := runLiveMakemigrations(
		fixture, resumeBackend, context.Background(), nil, makemigrationsPublicationHooks{},
	)
	if resumeBackend.Error() != nil || resume.ExitCode != 0 || !resume.HasMakemigrationsResult ||
		resume.MakemigrationsResult.Status != "generated" || resume.OwnedTempRecoveries != 1 ||
		resume.PublicationRenames != 1 || resume.PublishedCandidates != 1 || resumeStderr != "" {
		t.Fatalf("resume=%+v backend=%v stderr=%q", resume, resumeBackend.Error(), resumeStderr)
	}
	assertNoMakemigrationsReservedTemps(t, fixture.root)
}

func phaseCOverwriteAndSync(path string, document []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(document)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	return errors.Join(writeErr, file.Close())
}

func phaseCReservedTempNames(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), makemigrationsTempPrefix) {
			names = append(names, entry.Name())
		}
	}
	return names
}

func phaseCReservedTempNamesFromDirectory(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), makemigrationsTempPrefix) {
			names = append(names, entry.Name())
		}
	}
	return names
}

func phaseCReservedTempNamesFromRoot(root string) []string {
	entries, err := os.ReadDir(filepath.Join(root, "migrations"))
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), makemigrationsTempPrefix) {
			names = append(names, entry.Name())
		}
	}
	return names
}
