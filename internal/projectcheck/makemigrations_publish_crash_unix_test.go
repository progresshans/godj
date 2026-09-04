//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/internal/projectmigration"
	writerprotocol "github.com/progresshans/godj/internal/projectmigration/protocol"
	"github.com/progresshans/godj/schema/ir"
)

const (
	makemigrationsCrashHelperEnvironment    = "GODJ_MAKEMIGRATIONS_CRASH_HELPER"
	makemigrationsCrashRootEnvironment      = "GODJ_MAKEMIGRATIONS_CRASH_ROOT"
	makemigrationsCrashInventoryEnvironment = "GODJ_MAKEMIGRATIONS_CRASH_INVENTORY"
	makemigrationsCrashStepEnvironment      = "GODJ_MAKEMIGRATIONS_CRASH_STEP"
	makemigrationsCrashIndexEnvironment     = "GODJ_MAKEMIGRATIONS_CRASH_INDEX"
	makemigrationsCrashReadyEnvironment     = "GODJ_MAKEMIGRATIONS_CRASH_READY"
)

func TestMakemigrationsSIGKILLLeavesRecoverableStrictPrefix(t *testing.T) {
	tests := []struct {
		name              string
		step              makemigrationsPublicationStep
		index             int
		wantVisible       int
		wantTemp          bool
		wantResumePublish int
		wantResumeStatus  string
	}{
		{name: "temp created", step: makemigrationsStepTempCreated, index: 0, wantTemp: true, wantResumePublish: 2, wantResumeStatus: "generated"},
		{name: "temp mid write", step: makemigrationsStepTempWriteProgress, index: 0, wantTemp: true, wantResumePublish: 2, wantResumeStatus: "generated"},
		{name: "temp fsynced", step: makemigrationsStepTempFsynced, index: 0, wantTemp: true, wantResumePublish: 2, wantResumeStatus: "generated"},
		{name: "rename returned", step: makemigrationsStepRenameReturned, index: 0, wantVisible: 1, wantResumePublish: 1, wantResumeStatus: "generated"},
		{name: "first directory fsynced", step: makemigrationsStepDirectoryFsynced, index: 0, wantVisible: 1, wantResumePublish: 1, wantResumeStatus: "generated"},
		{name: "final directory fsynced", step: makemigrationsStepDirectoryFsynced, index: 1, wantVisible: 2, wantResumeStatus: "clean"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMakemigrationsRunFixture(t, false)
			spec := crossAppMakemigrationsSpec()
			want, err := liveMakemigrationsSnapshot(fixture.root, spec)
			if err != nil {
				t.Fatal(err)
			}
			candidates := want.Candidates()
			if len(candidates) != 2 {
				t.Fatalf("candidates=%d", len(candidates))
			}

			crashMakemigrationsProcess(t, fixture, test.step, test.index)
			if test.wantTemp {
				document := candidates[0].Document()
				target, err := writerprotocol.CandidateTargetBasename(candidates[0].App(), candidates[0].Name())
				if err != nil {
					clear(document)
					t.Fatal(err)
				}
				wantTemp := makemigrationsTempBasename(target, document)
				clear(document)
				if got := phaseCReservedTempNames(t, fixture.root); len(got) != 1 || got[0] != wantTemp {
					t.Fatalf("crash temp roster=%v want=[%s]", got, wantTemp)
				}
			} else {
				assertNoMakemigrationsReservedTemps(t, fixture.root)
			}
			assertPublishedMakemigrationsCandidates(t, fixture.root, candidates[:test.wantVisible])
			if test.wantVisible == 0 {
				assertNoPublishedMakemigrationsTargets(t, fixture.root)
			} else if _, err := strictLiveMakemigrationsState(fixture.root); err != nil {
				t.Fatalf("strict crash prefix: %v", err)
			}
			before := publishedMakemigrationsInfo(t, fixture.root)

			backend := &liveMakemigrationsBackend{inventory: fixture.inventory, root: fixture.root, spec: spec}
			resume, _, stderr := runLiveMakemigrations(
				fixture, backend, context.Background(), nil, makemigrationsPublicationHooks{},
			)
			if backend.Error() != nil || resume.ExitCode != 0 || !resume.HasMakemigrationsResult ||
				resume.MakemigrationsResult.Status != test.wantResumeStatus ||
				resume.PublishedCandidates != test.wantResumePublish || resume.PublicationRenames != test.wantResumePublish ||
				stderr != "" {
				t.Fatalf("resume=%+v backend=%v stderr=%q", resume, backend.Error(), stderr)
			}
			if test.wantTemp && resume.OwnedTempRecoveries != 1 {
				t.Fatalf("owned temp recoveries=%d", resume.OwnedTempRecoveries)
			}
			assertNoMakemigrationsReservedTemps(t, fixture.root)
			assertPublishedMakemigrationsCandidates(t, fixture.root, candidates)
			assertMakemigrationsPublishedPrefixPreserved(t, before, publishedMakemigrationsInfo(t, fixture.root))
			state, err := strictLiveMakemigrationsState(fixture.root)
			if err != nil || !state.Equal(want.DesiredState()) {
				t.Fatalf("resumed final state err=%v", err)
			}
		})
	}
}

func TestMakemigrationsIncompleteTempRecoveryRequiresFreshCandidatePrefix(t *testing.T) {
	for _, test := range []struct {
		name    string
		residue func([]byte) []byte
		wantOK  bool
	}{
		{
			name: "matching partial prefix",
			residue: func(document []byte) []byte {
				return append([]byte(nil), document[:len(document)/2]...)
			},
			wantOK: true,
		},
		{
			name: "wrong partial bytes",
			residue: func(document []byte) []byte {
				residue := append([]byte(nil), document[:len(document)/2]...)
				residue[0] ^= 0xff
				return residue
			},
		},
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
			if len(document) < 2 {
				t.Fatal("partial-write fixture document is too short")
			}
			target, err := writerprotocol.CandidateTargetBasename(candidate.App(), candidate.Name())
			if err != nil {
				t.Fatal(err)
			}
			temp := makemigrationsTempBasename(target, document)
			residue := test.residue(document)
			defer clear(residue)
			writeSyncedMakemigrationsFile(t, filepath.Join(fixture.root, "migrations", temp), residue)

			backend := &liveMakemigrationsBackend{inventory: fixture.inventory, root: fixture.root, spec: spec}
			report, _, stderr := runLiveMakemigrations(
				fixture, backend, context.Background(), nil, makemigrationsPublicationHooks{},
			)
			if backend.Error() != nil {
				t.Fatal(backend.Error())
			}
			if test.wantOK {
				if report.ExitCode != 0 || !report.HasMakemigrationsResult ||
					report.MakemigrationsResult.Status != "generated" || report.OwnedTempRecoveries != 1 ||
					report.PublicationRenames != 1 || report.PublishedCandidates != 1 || stderr != "" {
					t.Fatalf("report=%+v stderr=%q", report, stderr)
				}
				assertNoMakemigrationsReservedTemps(t, fixture.root)
				assertPublishedMakemigrationsCandidates(t, fixture.root, []projectmigration.Candidate{candidate})
				return
			}
			want := makemigrationsRecoveryRequired()
			if report.ExitCode != 3 || !report.HasMakemigrationsFailure || report.MakemigrationsFailure != want ||
				report.OwnedTempRecoveries != 0 || report.PublicationRenames != 0 || report.PublishedCandidates != 0 ||
				stderr != want.Category+"/"+want.Code+"\n" {
				t.Fatalf("report=%+v stderr=%q", report, stderr)
			}
			actual, err := os.ReadFile(filepath.Join(fixture.root, "migrations", temp))
			if err != nil || !bytes.Equal(actual, residue) {
				t.Fatalf("unknown residue changed: err=%v bytes=%x", err, actual)
			}
			if _, err := os.Lstat(filepath.Join(fixture.root, "migrations", target)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("target unexpectedly exists: %v", err)
			}
		})
	}
}

func TestMakemigrationsExactOwnedTempCanBeRemovedAfterDesiredSchemaChanges(t *testing.T) {
	fixture := newMakemigrationsRunFixture(t, false)
	oldSnapshot, err := liveMakemigrationsSnapshot(fixture.root, makemigrationsRunSpec())
	if err != nil {
		t.Fatal(err)
	}
	oldCandidate := oldSnapshot.Candidates()[0]
	oldDocument := oldCandidate.Document()
	defer clear(oldDocument)
	target, err := writerprotocol.CandidateTargetBasename(oldCandidate.App(), oldCandidate.Name())
	if err != nil {
		t.Fatal(err)
	}
	oldTemp := makemigrationsTempBasename(target, oldDocument)
	writeSyncedMakemigrationsFile(t, filepath.Join(fixture.root, "migrations", oldTemp), oldDocument)

	newSpec := makemigrationsRunSpec()
	newSpec.Apps[0].Schema.Models[0].Fields = append(newSpec.Apps[0].Schema.Models[0].Fields, ir.Field{
		Name: "subtitle", GoName: "Subtitle", Kind: ir.FieldChar, Nullable: true, MaxLength: 120,
	})
	newSnapshot, err := liveMakemigrationsSnapshot(fixture.root, newSpec)
	if err != nil {
		t.Fatal(err)
	}
	newCandidate := newSnapshot.Candidates()[0]
	newDocument := newCandidate.Document()
	defer clear(newDocument)
	if bytes.Equal(oldDocument, newDocument) || makemigrationsTempBasename(target, newDocument) == oldTemp {
		t.Fatal("schema change did not alter deterministic candidate identity")
	}

	backend := &liveMakemigrationsBackend{inventory: fixture.inventory, root: fixture.root, spec: newSpec}
	report, _, stderr := runLiveMakemigrations(
		fixture, backend, context.Background(), nil, makemigrationsPublicationHooks{},
	)
	if backend.Error() != nil || report.ExitCode != 0 || !report.HasMakemigrationsResult ||
		report.MakemigrationsResult.Status != "generated" || report.OwnedTempRecoveries != 1 ||
		report.PublicationRenames != 1 || report.PublishedCandidates != 1 || stderr != "" {
		t.Fatalf("report=%+v backend=%v stderr=%q", report, backend.Error(), stderr)
	}
	assertNoMakemigrationsReservedTemps(t, fixture.root)
	assertPublishedMakemigrationsCandidates(t, fixture.root, []projectmigration.Candidate{newCandidate})
}

func TestMakemigrationsFreshPlanDoesNotOverwriteCatalogChangeDuringRecovery(t *testing.T) {
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
	prefix := append([]byte(nil), document[:len(document)/2]...)
	defer clear(prefix)
	tempPath := filepath.Join(fixture.root, "migrations", temp)
	targetPath := filepath.Join(fixture.root, "migrations", target)
	writeSyncedMakemigrationsFile(t, tempPath, prefix)

	changed := false
	backend := &liveMakemigrationsBackend{inventory: fixture.inventory, root: fixture.root, spec: spec}
	report, _, stderr := runLiveMakemigrations(
		fixture, backend, context.Background(), nil,
		makemigrationsPublicationHooks{after: func(step makemigrationsPublicationStep, _ string, _ int) error {
			if step != makemigrationsStepSecondSnapshot || changed {
				return nil
			}
			changed = true
			return writeSyncedMakemigrationsFileAtPath(targetPath, document)
		}},
	)
	want := makemigrationsRecoveryRequired()
	if backend.Error() != nil || !changed || report.ExitCode != 3 || !report.HasMakemigrationsFailure ||
		report.MakemigrationsFailure != want || report.OwnedTempRecoveries != 0 || report.PublicationRenames != 0 ||
		report.PublishedCandidates != 0 || stderr != want.Category+"/"+want.Code+"\n" {
		t.Fatalf("report=%+v backend=%v changed=%t stderr=%q", report, backend.Error(), changed, stderr)
	}
	actualTarget, err := os.ReadFile(targetPath)
	if err != nil || !bytes.Equal(actualTarget, document) {
		t.Fatalf("changed catalog target overwritten: err=%v bytes=%x", err, actualTarget)
	}
	actualTemp, err := os.ReadFile(tempPath)
	if err != nil || !bytes.Equal(actualTemp, prefix) {
		t.Fatalf("unowned incomplete temp changed: err=%v bytes=%x", err, actualTemp)
	}
}

func writeSyncedMakemigrationsFileAtPath(path string, document []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(document)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	return errors.Join(writeErr, file.Close())
}

func assertMakemigrationsPublishedPrefixPreserved(t *testing.T, before, after map[string]os.FileInfo) {
	t.Helper()
	for name, beforeInfo := range before {
		afterInfo, exists := after[name]
		if !exists || !os.SameFile(beforeInfo, afterInfo) || beforeInfo.Mode() != afterInfo.Mode() || beforeInfo.Size() != afterInfo.Size() {
			t.Fatalf("durable prefix member changed: %s", name)
		}
	}
}

func TestMakemigrationsCrashHelper(t *testing.T) {
	if os.Getenv(makemigrationsCrashHelperEnvironment) != "1" {
		t.Skip("makemigrations crash subprocess helper")
	}
	root := os.Getenv(makemigrationsCrashRootEnvironment)
	inventoryPath := os.Getenv(makemigrationsCrashInventoryEnvironment)
	ready := os.Getenv(makemigrationsCrashReadyEnvironment)
	step := makemigrationsPublicationStep(os.Getenv(makemigrationsCrashStepEnvironment))
	index, err := strconv.Atoi(os.Getenv(makemigrationsCrashIndexEnvironment))
	if root == "" || inventoryPath == "" || ready == "" || step == "" || err != nil {
		t.Fatal("incomplete makemigrations crash helper environment")
	}
	inventory, err := os.ReadFile(inventoryPath)
	if err != nil {
		t.Fatal(err)
	}
	backend := &liveMakemigrationsBackend{inventory: inventory, root: root, spec: crossAppMakemigrationsSpec()}
	publication := makemigrationsPublicationHooks{after: func(actual makemigrationsPublicationStep, target string, actualIndex int) error {
		if actual != step || actualIndex != index {
			return nil
		}
		if err := writeMakemigrationsCrashReady(ready, actual, target, actualIndex); err != nil {
			return err
		}
		select {}
	}}
	if step == makemigrationsStepTempWriteProgress {
		publication.writeTemp = func(file *os.File, document []byte, target string, actualIndex int) error {
			if actualIndex != index || len(document) < 2 {
				return errors.New("invalid makemigrations partial-write crash fixture")
			}
			prefix := len(document) / 2
			if err := writeMakemigrationsAll(file, document[:prefix]); err != nil {
				return err
			}
			if err := writeMakemigrationsCrashReady(ready, step, target, actualIndex); err != nil {
				return err
			}
			select {}
		}
	}
	report := RunMakemigrations(MakemigrationsInvocation{
		Context: context.Background(), CWD: root, Args: []string{"makemigrations"},
		Environment: os.Environ(), Stdout: io.Discard, Stderr: io.Discard, Backend: backend,
		publication: publication,
	})
	t.Fatalf("crash helper returned: report=%+v backend=%v", report, backend.Error())
}

func crashMakemigrationsProcess(
	t *testing.T,
	fixture makemigrationsRunFixture,
	step makemigrationsPublicationStep,
	index int,
) {
	t.Helper()
	control := t.TempDir()
	inventoryPath := filepath.Join(control, "inventory.json")
	ready := filepath.Join(control, "ready")
	if err := os.WriteFile(inventoryPath, fixture.inventory, 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestMakemigrationsCrashHelper$", "-test.v")
	command.Dir = fixture.root
	command.Env = makemigrationsCrashEnvironment(map[string]string{
		makemigrationsCrashHelperEnvironment:    "1",
		makemigrationsCrashRootEnvironment:      fixture.root,
		makemigrationsCrashInventoryEnvironment: inventoryPath,
		makemigrationsCrashStepEnvironment:      string(step),
		makemigrationsCrashIndexEnvironment:     strconv.Itoa(index),
		makemigrationsCrashReadyEnvironment:     ready,
		"TMPDIR":                                control,
	})
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			_ = command.Process.Kill()
			_, _ = command.Process.Wait()
			t.Fatalf("inspect crash ready file: %v", err)
		}
		if time.Now().After(deadline) {
			_ = command.Process.Kill()
			_, _ = command.Process.Wait()
			t.Fatalf("helper did not reach %s[%d]: %s", step, index, output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatalf("crash helper exited successfully: %s", output.String())
	}
}

func writeMakemigrationsCrashReady(path string, step makemigrationsPublicationStep, target string, index int) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(string(step) + "\n" + target + "\n" + strconv.Itoa(index) + "\n")
	if writeErr == nil {
		writeErr = file.Sync()
	}
	return errors.Join(writeErr, file.Close())
}

func makemigrationsCrashEnvironment(overrides map[string]string) []string {
	result := make([]string, 0, len(os.Environ())+len(overrides))
	for _, value := range os.Environ() {
		key := value
		if separator := strings.IndexByte(value, '='); separator >= 0 {
			key = value[:separator]
		}
		if _, replaced := overrides[key]; !replaced {
			result = append(result, value)
		}
	}
	for key, value := range overrides {
		result = append(result, key+"="+value)
	}
	return result
}
