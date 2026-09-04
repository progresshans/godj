//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/projectmigration"
	writerprotocol "github.com/progresshans/godj/internal/projectmigration/protocol"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
	"golang.org/x/sys/unix"
)

type liveMakemigrationsBackend struct {
	inventory []byte
	root      string
	spec      codegen.ProjectSpec

	mu          sync.Mutex
	runnerCalls int
	err         error
}

func (backend *liveMakemigrationsBackend) Execute(
	_ context.Context,
	_ <-chan struct{},
	stage ProcessStage,
	_ Command,
) ProcessResult {
	switch stage {
	case MakemigrationsInventoryStage:
		document := append([]byte(nil), backend.inventory...)
		return ProcessResult{
			Started: true, ExitCode: 0, DirectReaps: 1, Stdout: document,
			StdoutScalar: StreamScalar{RetainedBytes: len(document)},
		}
	case BuildStage:
		return ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1}
	case MakemigrationsRunnerStage:
		backend.mu.Lock()
		backend.runnerCalls++
		backend.mu.Unlock()
		snapshot, err := liveMakemigrationsSnapshot(backend.root, backend.spec)
		if err == nil {
			var document []byte
			document, err = writerprotocol.EncodeResponse(writerprotocol.Response{
				OK: true, Result: makemigrationsRunProtocolResult(snapshot),
			})
			if err == nil {
				return ProcessResult{
					Started: true, ExitCode: 0, DirectReaps: 1, Stdout: document,
					StdoutScalar: StreamScalar{RetainedBytes: len(document)},
				}
			}
		}
		backend.mu.Lock()
		if backend.err == nil {
			backend.err = err
		}
		backend.mu.Unlock()
		return ProcessResult{Started: true, ExitCode: 1, DirectReaps: 1}
	default:
		backend.mu.Lock()
		if backend.err == nil {
			backend.err = fmt.Errorf("unexpected process stage %d", stage)
		}
		backend.mu.Unlock()
		return ProcessResult{Started: true, ExitCode: 1, DirectReaps: 1}
	}
}

func (backend *liveMakemigrationsBackend) Error() error {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return backend.err
}

func TestRunMakemigrationsNormalPublishesCrossAppDependencyPrefixAndRepeat(t *testing.T) {
	fixture := newMakemigrationsRunFixture(t, false)
	spec := crossAppMakemigrationsSpec()
	want, err := liveMakemigrationsSnapshot(fixture.root, spec)
	if err != nil {
		t.Fatal(err)
	}
	wantCandidates := want.Candidates()
	if len(wantCandidates) != 2 {
		t.Fatalf("candidates = %d", len(wantCandidates))
	}

	backend := &liveMakemigrationsBackend{inventory: fixture.inventory, root: fixture.root, spec: spec}
	commits := make([]string, 0, 2)
	var hookErr error
	report, stdout, stderr := runLiveMakemigrations(
		fixture, backend, context.Background(), nil,
		makemigrationsPublicationHooks{after: func(step makemigrationsPublicationStep, target string, _ int) error {
			if step != makemigrationsStepDirectoryFsynced {
				return nil
			}
			commits = append(commits, target)
			_, hookErr = strictLiveMakemigrationsState(fixture.root)
			return hookErr
		}},
	)
	if backend.Error() != nil || hookErr != nil {
		t.Fatalf("backend=%v hook=%v", backend.Error(), hookErr)
	}
	if report.ExitCode != 0 || !report.HasMakemigrationsResult || report.MakemigrationsResult.Status != "generated" ||
		report.PublishedCandidates != 2 || report.PublicationRenames != 2 || stderr != "" || !strings.Contains(stdout, `"status":"generated"`) {
		t.Fatalf("report=%+v stdout=%q stderr=%q", report, stdout, stderr)
	}
	wantOrder := []string{"zeta_0001_initial.godj.json", "alpha_0001_initial.godj.json"}
	if !equalStrings(commits, wantOrder) {
		t.Fatalf("commit order = %v, want %v", commits, wantOrder)
	}
	assertPublishedMakemigrationsCandidates(t, fixture.root, wantCandidates)
	state, err := strictLiveMakemigrationsState(fixture.root)
	if err != nil || !state.Equal(want.DesiredState()) {
		t.Fatalf("final state err=%v", err)
	}
	assertNoMakemigrationsReservedTemps(t, fixture.root)

	beforeTree := generationTreeSnapshot(t, fixture.root)
	beforeInfo := publishedMakemigrationsInfo(t, fixture.root)
	repeatBackend := &liveMakemigrationsBackend{inventory: fixture.inventory, root: fixture.root, spec: spec}
	repeat, repeatStdout, repeatStderr := runLiveMakemigrations(
		fixture, repeatBackend, context.Background(), nil, makemigrationsPublicationHooks{},
	)
	if repeatBackend.Error() != nil || repeat.ExitCode != 0 || !repeat.HasMakemigrationsResult ||
		repeat.MakemigrationsResult.Status != "clean" || repeat.RunnerCalls != 2 ||
		repeat.WriterLockAcquisitions != 1 || repeat.PublicationRenames != 0 || repeat.PublishedCandidates != 0 ||
		repeatStderr != "" || !strings.Contains(repeatStdout, `"status":"clean"`) {
		t.Fatalf("repeat=%+v backend=%v stdout=%q stderr=%q", repeat, repeatBackend.Error(), repeatStdout, repeatStderr)
	}
	afterTree := generationTreeSnapshot(t, fixture.root)
	if !reflect.DeepEqual(beforeTree, afterTree) {
		t.Fatalf("repeat mutated tree\nbefore=%v\nafter=%v", beforeTree, afterTree)
	}
	assertSamePublishedMakemigrationsInfo(t, beforeInfo, publishedMakemigrationsInfo(t, fixture.root))
}

func TestMakemigrationsDirectoryFsyncFailureRequiresRecoveryAndAdoption(t *testing.T) {
	fixture := newMakemigrationsRunFixture(t, false)
	spec := makemigrationsRunSpec()
	initial, err := liveMakemigrationsSnapshot(fixture.root, spec)
	if err != nil {
		t.Fatal(err)
	}
	candidate := initial.Candidates()[0]
	target := candidate.App() + "_" + candidate.Name() + ".godj.json"
	targetPath := filepath.Join(fixture.root, "migrations", target)

	backend := &liveMakemigrationsBackend{inventory: fixture.inventory, root: fixture.root, spec: spec}
	failCommitSync := true
	report, _, stderr := runLiveMakemigrations(
		fixture, backend, context.Background(), nil,
		makemigrationsPublicationHooks{syncDirectory: func(directory *os.File, step makemigrationsPublicationStep, index int) error {
			if step == makemigrationsSyncCandidateCommitted && index == 0 && failCommitSync {
				failCommitSync = false
				return unix.EIO
			}
			return directory.Sync()
		}},
	)
	wantFailure := makemigrationsRecoveryRequired()
	if backend.Error() != nil || report.ExitCode != 3 || !report.HasMakemigrationsFailure ||
		report.MakemigrationsFailure != wantFailure || report.PublicationRenames != 1 ||
		report.PublishedCandidates != 0 || stderr != wantFailure.Category+"/"+wantFailure.Code+"\n" {
		t.Fatalf("report=%+v backend=%v stderr=%q", report, backend.Error(), stderr)
	}
	before, err := os.Stat(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	assertNoMakemigrationsReservedTemps(t, fixture.root)

	resumeBackend := &liveMakemigrationsBackend{inventory: fixture.inventory, root: fixture.root, spec: spec}
	resume, _, resumeStderr := runLiveMakemigrations(
		fixture, resumeBackend, context.Background(), nil, makemigrationsPublicationHooks{},
	)
	after, err := os.Stat(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if resumeBackend.Error() != nil || resume.ExitCode != 0 || !resume.HasMakemigrationsResult ||
		resume.MakemigrationsResult.Status != "clean" || resume.PublicationRenames != 0 ||
		resume.PublishedCandidates != 0 || resumeStderr != "" || !os.SameFile(before, after) {
		t.Fatalf("resume=%+v backend=%v stderr=%q same=%t", resume, resumeBackend.Error(), resumeStderr, os.SameFile(before, after))
	}
}

func TestMakemigrationsCancellationBeforeRenameCleansOwnedTemp(t *testing.T) {
	fixture := newMakemigrationsRunFixture(t, false)
	backend := &liveMakemigrationsBackend{inventory: fixture.inventory, root: fixture.root, spec: makemigrationsRunSpec()}
	ctx, cancel := context.WithCancel(context.Background())
	report, _, stderr := runLiveMakemigrations(
		fixture, backend, ctx, nil,
		makemigrationsPublicationHooks{after: func(step makemigrationsPublicationStep, _ string, _ int) error {
			if step == makemigrationsStepTempFsynced {
				cancel()
			}
			return nil
		}},
	)
	want := MakemigrationsFailure{Category: MakemigrationsCategoryProcess, Code: MakemigrationsCodeProjectCanceled}
	if backend.Error() != nil || report.ExitCode != 3 || !report.HasMakemigrationsFailure || report.MakemigrationsFailure != want ||
		report.PublicationRenames != 0 || report.PublishedCandidates != 0 || stderr != want.Category+"/"+want.Code+"\n" {
		t.Fatalf("report=%+v backend=%v stderr=%q", report, backend.Error(), stderr)
	}
	assertNoPublishedMakemigrationsTargets(t, fixture.root)
	assertNoMakemigrationsReservedTemps(t, fixture.root)
}

func TestMakemigrationsRecoveryRemovesExactOwnedTempThenFreshlyPublishes(t *testing.T) {
	fixture := newMakemigrationsRunFixture(t, false)
	spec := makemigrationsRunSpec()
	snapshot, err := liveMakemigrationsSnapshot(fixture.root, spec)
	if err != nil {
		t.Fatal(err)
	}
	candidate := snapshot.Candidates()[0]
	document := candidate.Document()
	target := candidate.App() + "_" + candidate.Name() + ".godj.json"
	temp := makemigrationsTempBasename(target, document)
	tempPath := filepath.Join(fixture.root, "migrations", temp)
	writeSyncedMakemigrationsFile(t, tempPath, document)
	clear(document)

	backend := &liveMakemigrationsBackend{inventory: fixture.inventory, root: fixture.root, spec: spec}
	report, _, stderr := runLiveMakemigrations(
		fixture, backend, context.Background(), nil, makemigrationsPublicationHooks{},
	)
	if backend.Error() != nil || report.ExitCode != 0 || !report.HasMakemigrationsResult ||
		report.MakemigrationsResult.Status != "generated" || report.OwnedTempRecoveries != 1 ||
		report.PublicationRenames != 1 || report.PublishedCandidates != 1 || stderr != "" {
		t.Fatalf("report=%+v backend=%v stderr=%q", report, backend.Error(), stderr)
	}
	if _, err := os.Lstat(tempPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned temp still exists: %v", err)
	}
	assertPublishedMakemigrationsCandidates(t, fixture.root, []projectmigration.Candidate{candidate})
}

func TestMakemigrationsNoReplaceEEXISTPreservesDestinationAndRemovesOwnedTemp(t *testing.T) {
	fixture := newMakemigrationsRunFixture(t, false)
	spec := makemigrationsRunSpec()
	snapshot, err := liveMakemigrationsSnapshot(fixture.root, spec)
	if err != nil {
		t.Fatal(err)
	}
	candidate := snapshot.Candidates()[0]
	document := candidate.Document()
	target := candidate.App() + "_" + candidate.Name() + ".godj.json"
	targetPath := filepath.Join(fixture.root, "migrations", target)

	backend := &liveMakemigrationsBackend{inventory: fixture.inventory, root: fixture.root, spec: spec}
	injected := false
	report, _, stderr := runLiveMakemigrations(
		fixture, backend, context.Background(), nil,
		makemigrationsPublicationHooks{renameNoReplace: func(_ int, _ string, targetDirectory int, targetName string) error {
			if injected {
				return unix.EEXIST
			}
			injected = true
			if err := writeSyncedMakemigrationsFileAt(targetDirectory, targetName, document); err != nil {
				return err
			}
			if err := unix.Fsync(targetDirectory); err != nil {
				return err
			}
			return unix.EEXIST
		}},
	)
	want := makemigrationsSourceConflict()
	if backend.Error() != nil || report.ExitCode != 1 || !report.HasMakemigrationsFailure || report.MakemigrationsFailure != want ||
		report.PublicationRenames != 0 || report.PublishedCandidates != 0 || stderr != want.Category+"/"+want.Code+"\n" {
		t.Fatalf("report=%+v backend=%v stderr=%q", report, backend.Error(), stderr)
	}
	actual, err := os.ReadFile(targetPath)
	if err != nil || !bytes.Equal(actual, document) {
		t.Fatalf("destination changed err=%v", err)
	}
	before, err := os.Stat(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	assertNoMakemigrationsReservedTemps(t, fixture.root)

	resumeBackend := &liveMakemigrationsBackend{inventory: fixture.inventory, root: fixture.root, spec: spec}
	resume, _, resumeStderr := runLiveMakemigrations(
		fixture, resumeBackend, context.Background(), nil, makemigrationsPublicationHooks{},
	)
	after, err := os.Stat(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if resumeBackend.Error() != nil || resume.ExitCode != 0 || !resume.HasMakemigrationsResult ||
		resume.MakemigrationsResult.Status != "clean" || resume.PublicationRenames != 0 ||
		resumeStderr != "" || !os.SameFile(before, after) {
		t.Fatalf("resume=%+v backend=%v stderr=%q", resume, resumeBackend.Error(), resumeStderr)
	}
	clear(document)
}

func TestMakemigrationsPhysicalCatalogReplacementConflictsBeforeRenameAndFreshRunResumes(t *testing.T) {
	fixture := newMakemigrationsRunFixture(t, true)
	spec := makemigrationsRunSpec()
	spec.Apps[0].Schema.Models[0].Fields = append(spec.Apps[0].Schema.Models[0].Fields, ir.Field{
		Name: "subtitle", GoName: "Subtitle", Kind: ir.FieldChar, Nullable: true, MaxLength: 120,
	})
	pending, err := liveMakemigrationsSnapshot(fixture.root, spec)
	if err != nil {
		t.Fatal(err)
	}
	candidate := pending.Candidates()[0]
	targetPath := filepath.Join(fixture.root, "migrations", candidate.App()+"_"+candidate.Name()+".godj.json")
	original, err := os.ReadFile(fixture.migrationPath)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(fixture.migrationPath)
	if err != nil {
		t.Fatal(err)
	}

	backend := &liveMakemigrationsBackend{inventory: fixture.inventory, root: fixture.root, spec: spec}
	replaced := false
	report, _, stderr := runLiveMakemigrations(
		fixture, backend, context.Background(), nil,
		makemigrationsPublicationHooks{after: func(step makemigrationsPublicationStep, _ string, _ int) error {
			if step != makemigrationsStepBeforeRename || replaced {
				return nil
			}
			replaced = true
			replacement := fixture.migrationPath + ".replacement"
			writeSyncedMakemigrationsFile(t, replacement, original)
			if err := os.Rename(replacement, fixture.migrationPath); err != nil {
				return err
			}
			return syncDirectoryPath(filepath.Dir(fixture.migrationPath))
		}},
	)
	want := makemigrationsSourceConflict()
	after, err := os.Stat(fixture.migrationPath)
	if err != nil {
		t.Fatal(err)
	}
	if backend.Error() != nil || report.ExitCode != 1 || !report.HasMakemigrationsFailure || report.MakemigrationsFailure != want ||
		report.PublicationRenames != 0 || report.PublishedCandidates != 0 || stderr != want.Category+"/"+want.Code+"\n" ||
		os.SameFile(before, after) {
		t.Fatalf("report=%+v backend=%v stderr=%q same-inode=%t", report, backend.Error(), stderr, os.SameFile(before, after))
	}
	if _, err := os.Lstat(targetPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale target exists: %v", err)
	}
	assertNoMakemigrationsReservedTemps(t, fixture.root)

	resumeBackend := &liveMakemigrationsBackend{inventory: fixture.inventory, root: fixture.root, spec: spec}
	resume, _, resumeStderr := runLiveMakemigrations(
		fixture, resumeBackend, context.Background(), nil, makemigrationsPublicationHooks{},
	)
	if resumeBackend.Error() != nil || resume.ExitCode != 0 || !resume.HasMakemigrationsResult ||
		resume.MakemigrationsResult.Status != "generated" || resume.PublicationRenames != 1 ||
		resume.PublishedCandidates != 1 || resumeStderr != "" {
		t.Fatalf("resume=%+v backend=%v stderr=%q", resume, resumeBackend.Error(), resumeStderr)
	}
}

func TestRunMakemigrationsConcurrentWritersUseLockedSecondSnapshot(t *testing.T) {
	fixture := newMakemigrationsRunFixture(t, false)
	spec := makemigrationsRunSpec()
	barrier := NewMakemigrationsConformanceFinalCatalogBarrier()
	backends := []*liveMakemigrationsBackend{
		{inventory: fixture.inventory, root: fixture.root, spec: spec},
		{inventory: fixture.inventory, root: fixture.root, spec: spec},
	}
	type outcome struct {
		report MakemigrationsReport
		stderr string
		err    error
	}
	outcomes := make(chan outcome, 2)
	for _, backend := range backends {
		backend := backend
		go func() {
			report, _, stderr, runErr := runLiveMakemigrationsFinalCatalogBarrier(
				fixture, backend, context.Background(), nil, makemigrationsPublicationHooks{}, barrier,
			)
			outcomes <- outcome{report: report, stderr: stderr, err: runErr}
		}()
	}
	generated := 0
	clean := 0
	published := 0
	for range backends {
		outcome := <-outcomes
		if outcome.err != nil || outcome.report.ExitCode != 0 || !outcome.report.HasMakemigrationsResult || outcome.stderr != "" ||
			outcome.report.RunnerCalls != 2 || outcome.report.WriterLockAcquisitions != 1 {
			t.Fatalf("outcome=%+v stderr=%q err=%v", outcome.report, outcome.stderr, outcome.err)
		}
		switch outcome.report.MakemigrationsResult.Status {
		case "generated":
			generated++
		case "clean":
			clean++
		default:
			t.Fatalf("status=%q", outcome.report.MakemigrationsResult.Status)
		}
		published += outcome.report.PublishedCandidates
	}
	if barrier.arrivalCount() != 2 {
		t.Fatalf("final catalog snapshots=%d, want 2", barrier.arrivalCount())
	}
	for _, backend := range backends {
		if backend.Error() != nil {
			t.Fatal(backend.Error())
		}
	}
	if generated != 1 || clean != 1 || published != 1 {
		t.Fatalf("generated=%d clean=%d published=%d", generated, clean, published)
	}
	assertNoMakemigrationsReservedTemps(t, fixture.root)
	if _, err := strictLiveMakemigrationsState(fixture.root); err != nil {
		t.Fatal(err)
	}
}

func TestRunMakemigrationsFinalCatalogBarrierAbortsPeerBeforePublication(t *testing.T) {
	fixture := newMakemigrationsRunFixture(t, false)
	spec := makemigrationsRunSpec()
	barrier := NewMakemigrationsConformanceFinalCatalogBarrier()
	type outcome struct {
		report MakemigrationsReport
		err    error
	}
	first := make(chan outcome, 1)
	go func() {
		report, _, _, err := runLiveMakemigrationsFinalCatalogBarrier(
			fixture,
			&liveMakemigrationsBackend{inventory: fixture.inventory, root: fixture.root, spec: spec},
			context.Background(),
			nil,
			makemigrationsPublicationHooks{},
			barrier,
		)
		first <- outcome{report: report, err: err}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for barrier.arrivalCount() != 1 {
		select {
		case early := <-first:
			t.Fatalf("first writer exited before the final catalog barrier: report=%+v err=%v", early.report, early.err)
		default:
		}
		if time.Now().After(deadline) {
			barrier.abort()
			t.Fatal("first writer did not reach the final catalog barrier")
		}
		runtime.Gosched()
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	secondReport, _, _, secondErr := runLiveMakemigrationsFinalCatalogBarrier(
		fixture,
		&liveMakemigrationsBackend{inventory: fixture.inventory, root: fixture.root, spec: spec},
		canceled,
		nil,
		makemigrationsPublicationHooks{},
		barrier,
	)
	firstOutcome := <-first
	for name, current := range map[string]outcome{
		"first":  firstOutcome,
		"second": {report: secondReport, err: secondErr},
	} {
		if current.err == nil || current.report.WriterLockAcquisitions != 0 ||
			current.report.PublicationRenames != 0 || current.report.PublishedCandidates != 0 {
			t.Fatalf("%s writer report=%+v err=%v", name, current.report, current.err)
		}
	}
	assertNoMakemigrationsReservedTemps(t, fixture.root)
	if sources, err := visibleMakemigrationsSources(fixture.root); err != nil {
		t.Fatal(err)
	} else if len(sources) != 0 {
		clearDefinitionSources(sources)
		t.Fatalf("aborted pair published %d migration sources", len(sources))
	}
}

func TestMakemigrationsEmbedOverlapPreflightCoversTargetTempAndDirectoryPattern(t *testing.T) {
	fixture := newMakemigrationsRunFixture(t, false)
	snapshot, err := liveMakemigrationsSnapshot(fixture.root, makemigrationsRunSpec())
	if err != nil {
		t.Fatal(err)
	}
	candidates := snapshot.Candidates()
	tests := []struct {
		pattern string
		want    bool
	}{
		{pattern: "migrations/*.godj.json", want: true},
		{pattern: "migrations/.godj-makemigrations-tmp-v1-*", want: true},
		{pattern: "migrations", want: true},
		{pattern: "assets/*.txt", want: false},
	}
	for _, test := range tests {
		build := makemigrationsBuildInputFingerprint{embedPatterns: []makemigrationsEmbedPattern{{
			packageDirectory: ".", pattern: test.pattern,
		}}}
		if got := makemigrationsCandidatesOverlapEmbedPatterns(build, "migrations", candidates); got != test.want {
			t.Fatalf("pattern %q overlap=%t want=%t", test.pattern, got, test.want)
		}
	}
}

func TestMakemigrationsReadOnlyModesRejectEmbedOverlapWithoutMutation(t *testing.T) {
	for _, arguments := range [][]string{{"makemigrations", "--dry-run"}, {"makemigrations", "--check"}} {
		t.Run(strings.TrimPrefix(arguments[1], "--"), func(t *testing.T) {
			fixture := newMakemigrationsRunFixture(t, false)
			inventory := makemigrationsInventoryWithEmbedPattern(t, fixture.root, fixture.inventory, "migrations/*.godj.json")
			wire := encodeMakemigrationsRunResponse(t, fixture.snapshot)
			backend := &makemigrationsRunBackend{t: t, inventory: inventory, runnerWire: wire}
			before := generationTreeSnapshot(t, fixture.root)
			report, stdout, stderr := executeMakemigrationsRun(t, fixture, arguments, backend, workspaceHooks{})
			want := makemigrationsPublicationFailed()
			if report.ExitCode != 3 || !report.HasMakemigrationsFailure || report.MakemigrationsFailure != want ||
				report.WriterLockAcquisitions != 0 || report.PublicationRenames != 0 ||
				report.PublicationDirectorySyncs != 0 || stdout != "" || stderr != want.Category+"/"+want.Code+"\n" {
				t.Fatalf("report=%+v stdout=%q stderr=%q", report, stdout, stderr)
			}
			if after := generationTreeSnapshot(t, fixture.root); !reflect.DeepEqual(before, after) {
				t.Fatalf("read-only embed preflight mutated project\nbefore=%v\nafter=%v", before, after)
			}
		})
	}
}

func TestMakemigrationsDirectoryHeadroomBoundary(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		existing   int
		candidates int
		want       bool
	}{
		{name: "empty", want: true},
		{name: "exact maximum", existing: makemigrationsMaxDirectoryEntries - writerprotocol.MaxCandidates, candidates: writerprotocol.MaxCandidates, want: true},
		{name: "one over combined", existing: makemigrationsMaxDirectoryEntries - writerprotocol.MaxCandidates + 1, candidates: writerprotocol.MaxCandidates},
		{name: "existing over maximum", existing: makemigrationsMaxDirectoryEntries + 1},
		{name: "negative existing", existing: -1},
		{name: "negative candidates", candidates: -1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := makemigrationsDirectoryHasHeadroom(test.existing, test.candidates); got != test.want {
				t.Fatalf("headroom(%d, %d) = %v, want %v", test.existing, test.candidates, got, test.want)
			}
		})
	}
}

func makemigrationsInventoryWithEmbedPattern(t *testing.T, root string, document []byte, pattern string) []byte {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(document))
	packages := make([]makemigrationsGoListPackage, 0, 1)
	for {
		var listed makemigrationsGoListPackage
		if err := decoder.Decode(&listed); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("decode go-list fixture: %v", err)
		}
		packages = append(packages, listed)
	}
	if len(packages) == 0 {
		t.Fatal("empty go-list fixture")
	}
	writeMakemigrationsFixtureFile(t, root, "embed_owner.go", []byte("package projectroot\n"), 0o640)
	packages = append(packages, makemigrationsGoListPackage{
		Dir: root, ImportPath: "example.invalid/project", Name: "projectroot",
		GoFiles: []string{"embed_owner.go"}, EmbedPatterns: []string{pattern},
	})
	return encodeMakemigrationsGoList(t, packages)
}

func liveMakemigrationsSnapshot(root string, spec codegen.ProjectSpec) (projectmigration.Snapshot, error) {
	writerRoot := filepath.Join(root, "migrations")
	entries, err := os.ReadDir(writerRoot)
	if err != nil {
		return projectmigration.Snapshot{}, err
	}
	sources := make([]definition.Source, 0, len(entries))
	defer clearDefinitionSources(sources)
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".godj.json") {
			continue
		}
		info, err := os.Lstat(filepath.Join(writerRoot, entry.Name()))
		if err != nil || !info.Mode().IsRegular() {
			return projectmigration.Snapshot{}, fmt.Errorf("unsafe live source %q", entry.Name())
		}
		document, err := os.ReadFile(filepath.Join(writerRoot, entry.Name()))
		if err != nil {
			return projectmigration.Snapshot{}, err
		}
		sources = append(sources, definition.Source{
			SourceID: filepath.ToSlash(filepath.Join("migrations", entry.Name())), Document: document,
		})
	}
	sort.Slice(sources, func(left, right int) bool { return sources[left].SourceID < sources[right].SourceID })
	return projectmigration.BuildSnapshot(projectmigration.Request{
		ProjectSpec: spec, FilesystemSources: sources,
		ProgrammaticSources: make([]definition.Source, 0), WriterRoot: "migrations",
	})
}

func runLiveMakemigrations(
	fixture makemigrationsRunFixture,
	backend Backend,
	ctx context.Context,
	interrupt <-chan struct{},
	publication makemigrationsPublicationHooks,
) (MakemigrationsReport, string, string) {
	var stdout, stderr bytes.Buffer
	report := RunMakemigrations(MakemigrationsInvocation{
		Context: ctx, Interrupt: interrupt, CWD: fixture.root, Args: []string{"makemigrations"},
		Environment: fixture.environment, Stdout: &stdout, Stderr: &stderr,
		Backend: backend, publication: publication,
	})
	return report, stdout.String(), stderr.String()
}

func runLiveMakemigrationsFinalCatalogBarrier(
	fixture makemigrationsRunFixture,
	backend Backend,
	ctx context.Context,
	interrupt <-chan struct{},
	publication makemigrationsPublicationHooks,
	barrier *MakemigrationsConformanceFinalCatalogBarrier,
) (MakemigrationsReport, string, string, error) {
	var stdout, stderr bytes.Buffer
	report, err := RunMakemigrationsConformanceFinalCatalog(MakemigrationsInvocation{
		Context: ctx, Interrupt: interrupt, CWD: fixture.root, Args: []string{"makemigrations"},
		Environment: fixture.environment, Stdout: &stdout, Stderr: &stderr,
		Backend: backend, publication: publication,
	}, barrier)
	return report, stdout.String(), stderr.String(), err
}

func strictLiveMakemigrationsState(root string) (migrations.ProjectState, error) {
	sources, err := visibleMakemigrationsSources(root)
	if err != nil {
		return migrations.ProjectState{}, err
	}
	defer clearDefinitionSources(sources)
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		return migrations.ProjectState{}, err
	}
	reconstructor, err := migrations.NewStateReconstructor(loaded.Definitions()...)
	if err != nil {
		return migrations.ProjectState{}, err
	}
	return reconstructor.Reconstruct(migrations.LatestStateRequest())
}

func visibleMakemigrationsSources(root string) ([]definition.Source, error) {
	directory := filepath.Join(root, "migrations")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	sources := make([]definition.Source, 0, len(entries))
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".godj.json") {
			continue
		}
		document, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			clearDefinitionSources(sources)
			return nil, err
		}
		sources = append(sources, definition.Source{
			SourceID: filepath.ToSlash(filepath.Join("migrations", entry.Name())), Document: document,
		})
	}
	sort.Slice(sources, func(left, right int) bool { return sources[left].SourceID < sources[right].SourceID })
	return sources, nil
}

func assertPublishedMakemigrationsCandidates(t *testing.T, root string, candidates []projectmigration.Candidate) {
	t.Helper()
	for index := range candidates {
		document := candidates[index].Document()
		path := filepath.Join(root, "migrations", candidates[index].App()+"_"+candidates[index].Name()+".godj.json")
		actual, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(actual, document) {
			clear(document)
			t.Fatalf("candidate[%d] bytes err=%v", index, err)
		}
		clear(document)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Fatalf("candidate[%d] mode=%v err=%v", index, info, err)
		}
	}
}

func assertNoPublishedMakemigrationsTargets(t *testing.T, root string) {
	t.Helper()
	sources, err := visibleMakemigrationsSources(root)
	if err != nil {
		t.Fatal(err)
	}
	defer clearDefinitionSources(sources)
	if len(sources) != 0 {
		t.Fatalf("published sources = %d", len(sources))
	}
}

func assertNoMakemigrationsReservedTemps(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), makemigrationsTempPrefix) {
			t.Fatalf("reserved temp remains: %s", entry.Name())
		}
	}
}

func publishedMakemigrationsInfo(t *testing.T, root string) map[string]os.FileInfo {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	result := make(map[string]os.FileInfo)
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".godj.json") {
			continue
		}
		info, err := os.Stat(filepath.Join(root, "migrations", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		result[entry.Name()] = info
	}
	return result
}

func assertSamePublishedMakemigrationsInfo(t *testing.T, before, after map[string]os.FileInfo) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatalf("published roster changed: before=%d after=%d", len(before), len(after))
	}
	for name, beforeInfo := range before {
		afterInfo, exists := after[name]
		if !exists || !os.SameFile(beforeInfo, afterInfo) || beforeInfo.Mode() != afterInfo.Mode() || beforeInfo.Size() != afterInfo.Size() {
			t.Fatalf("published member changed: %s", name)
		}
	}
}

func writeSyncedMakemigrationsFile(t *testing.T, path string, document []byte) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Chmod(0o600); err == nil {
		err = writeMakemigrationsAll(file, document)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("write synced file: %v / %v", err, closeErr)
	}
}

func writeSyncedMakemigrationsFileAt(directory int, name string, document []byte) error {
	fd, err := unix.Openat(directory, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		return errors.New("retain injected destination")
	}
	err = file.Chmod(0o600)
	if err == nil {
		err = writeMakemigrationsAll(file, document)
	}
	if err == nil {
		err = file.Sync()
	}
	return errors.Join(err, file.Close())
}

func syncDirectoryPath(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

func crossAppMakemigrationsSpec() codegen.ProjectSpec {
	authors := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "zeta",
		Models: []ir.Model{{
			Name: "author", GoName: "Author",
			Fields: []ir.Field{{Name: "name", GoName: "Name", Kind: ir.FieldChar, MaxLength: 200}},
		}},
	}
	content := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "alpha",
		Models: []ir.Model{{
			Name: "entry", GoName: "Entry",
			Fields: []ir.Field{
				{Name: "title", GoName: "Title", Kind: ir.FieldChar, MaxLength: 200},
				{
					Name: "author", GoName: "AuthorID", Kind: ir.FieldForeignKey, Nullable: true,
					Relation: &ir.ForeignKeyRelation{
						Target:      ir.ModelIdentity{AppLabel: "zeta", ModelName: "author"},
						Cardinality: ir.RelationManyToOne, Reverse: ir.ReverseRelation{Name: "entries"},
						OnDelete: ir.DeleteProtect,
					},
				},
			},
		}},
	}
	return codegen.ProjectSpec{
		Project: codegen.PackageSpec{PackageName: "project", ImportPath: "example.invalid/site/project", Directory: "project"},
		Apps: []codegen.AppSpec{
			{Alias: "alpha", Package: codegen.PackageSpec{PackageName: "alpha", ImportPath: "example.invalid/site/alpha", Directory: "alpha"}, Schema: content},
			{Alias: "zeta", Package: codegen.PackageSpec{PackageName: "zeta", ImportPath: "example.invalid/site/zeta", Directory: "zeta"}, Schema: authors},
		},
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
