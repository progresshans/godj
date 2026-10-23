//go:build darwin || linux

package projectgenerate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/codegen"
	"golang.org/x/sys/unix"
)

func TestCheckCleanAndReadOnly(t *testing.T) {
	bundle := projectGenerateTestBundle(t)
	root := t.TempDir()
	writeProjectGenerateTestBundle(t, root, bundle)
	writeProjectGenerateTestFile(t, root, "consumer/consumer.go", []byte("package consumer\n"), 0o644)
	before := snapshotProjectGenerateTestTree(t, root)
	report, err := Check(context.Background(), root, bundle)
	if err != nil {
		t.Fatalf("Check(clean) error = %v", err)
	}
	if !report.Clean() || report.ExpectedSnapshotSHA256 != bundle.SnapshotSHA256() ||
		report.ActualSnapshotSHA256 != bundle.SnapshotSHA256() {
		t.Fatalf("Check(clean) report = %#v", report)
	}
	after := snapshotProjectGenerateTestTree(t, root)
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatalf("Check mutated project\nbefore=%v\nafter=%v", before, after)
	}
}

func TestCheckReportsExactMissingModifiedUnexpectedAndPriorRosterDrift(t *testing.T) {
	bundle := projectGenerateTestBundle(t)
	root := t.TempDir()
	writeProjectGenerateTestBundle(t, root, bundle)
	files := bundle.Files()
	missing := files[0]
	modified := files[1]
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(missing.Path))); err != nil {
		t.Fatal(err)
	}
	writeProjectGenerateTestFile(t, root, modified.Path, []byte("package broken\n"), 0o644)
	unknownPath := "retired/app/zz_godj_unknown.go"
	unknownSource := []byte("package retired\n")
	writeProjectGenerateTestFile(t, root, unknownPath, unknownSource, 0o644)

	report, err := Check(context.Background(), root, bundle)
	requireErrorIs(t, err, ErrGeneratedDrift)
	assertProjectGenerateDrift(t, report, missing.Path, DriftMissing, missing.SHA256, "")
	assertProjectGenerateDrift(t, report, modified.Path, DriftModified, modified.SHA256, sha256Hex([]byte("package broken\n")))
	assertProjectGenerateDrift(t, report, unknownPath, DriftUnexpected, "", sha256Hex(unknownSource))
	if errors.Is(err, ErrGeneratedConflict) {
		t.Fatalf("ordinary exact drift was classified as conflict: %v", err)
	}

	prior := decodeProjectGenerateTestManifest(t)
	prior.GeneratorABI[1].Version = "retired-v0"
	retiredPath := "oldapp/zz_godj_retired.go"
	retiredSource := []byte("package oldapp\n")
	prior.Apps = append(prior.Apps, manifestApp{
		Alias: "oldapp", AppLabel: "oldapp",
		Package:      manifestPackage{PackageName: "oldapp", ImportPath: "example.com/godj-project-bundle/oldapp", Directory: "oldapp"},
		SchemaSHA256: strings.Repeat("3", 64),
	})
	sort.Slice(prior.Apps, func(left, right int) bool { return manifestAppLess(prior.Apps[left], prior.Apps[right]) })
	prior.Files = append(prior.Files, manifestFile{Path: retiredPath, Owner: "app:oldapp", Mode: "0644", SHA256: sha256Hex(retiredSource)})
	sort.Slice(prior.Files, func(left, right int) bool { return prior.Files[left].Path < prior.Files[right].Path })
	writeProjectGenerateTestFile(t, root, generatedManifestRelativePath, encodeProjectGenerateTestManifest(t, prior), 0o644)
	writeProjectGenerateTestFile(t, root, retiredPath, retiredSource, 0o644)
	report, err = Check(context.Background(), root, bundle)
	requireErrorIs(t, err, ErrGeneratedDrift)
	assertProjectGenerateDrift(t, report, retiredPath, DriftUnexpected, "", sha256Hex(retiredSource))
}

func TestCheckInterruptedPublicationIdentifiesBothErrors(t *testing.T) {
	bundle := projectGenerateTestBundle(t)
	root := t.TempDir()
	writeProjectGenerateTestBundle(t, root, bundle)
	writeProjectGenerateTestFile(t, root, publicationJournalRelativePath, []byte("journal"), 0o600)
	writeProjectGenerateTestFile(t, root, publicationTransactionDirectoryPath+"/tx/stage/file", []byte("stage"), 0o600)
	report, err := Check(context.Background(), root, bundle)
	requireErrorIs(t, err, ErrGeneratedDrift)
	requireErrorIs(t, err, ErrPublicationInterrupted)
	if !report.Interrupted {
		t.Fatalf("Interrupted = false; report = %#v", report)
	}
	assertProjectGenerateDriftKind(t, report, publicationJournalRelativePath, DriftInterrupted)
	assertProjectGenerateDriftKind(t, report, publicationTransactionDirectoryPath+"/tx", DriftInterrupted)
}

func TestCheckRejectsFinalAndAncestorSymlinksWithoutReadingOutside(t *testing.T) {
	bundle := projectGenerateTestBundle(t)
	for _, test := range []struct {
		name    string
		prepare func(t *testing.T, root, outside string) string
	}{
		{
			name: "final file",
			prepare: func(t *testing.T, root, outside string) string {
				path := bundle.Files()[0].Path
				if err := os.Remove(filepath.Join(root, filepath.FromSlash(path))); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(outside, "secret.go"), filepath.Join(root, filepath.FromSlash(path))); err != nil {
					t.Fatal(err)
				}
				return path
			},
		},
		{
			name: "ancestor directory",
			prepare: func(t *testing.T, root, outside string) string {
				if err := os.RemoveAll(filepath.Join(root, "authors")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(root, "authors")); err != nil {
					t.Fatal(err)
				}
				return "authors/zz_godj_generated.go"
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			writeProjectGenerateTestBundle(t, root, bundle)
			secret := []byte("outside-secret-that-must-not-be-read")
			writeProjectGenerateTestFile(t, outside, "secret.go", secret, 0o644)
			for _, file := range bundle.Files() {
				if strings.HasPrefix(file.Path, "authors/") {
					writeProjectGenerateTestFile(t, outside, strings.TrimPrefix(file.Path, "authors/"), secret, 0o644)
				}
			}
			driftPath := test.prepare(t, root, outside)
			report, err := Check(context.Background(), root, bundle)
			requireErrorIs(t, err, ErrGeneratedConflict)
			for _, drift := range report.Drifts {
				if drift.ActualSHA256 == sha256Hex(secret) {
					t.Fatalf("Check read outside symlink target: %#v", drift)
				}
			}
			assertProjectGenerateDriftKind(t, report, driftPath, DriftModified)
		})
	}
}

func TestCheckRejectsFIFOsWithoutBlocking(t *testing.T) {
	bundle := projectGenerateTestBundle(t)
	tests := []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{name: "manifest", setup: func(t *testing.T, root string) {
			replaceProjectGenerateTestFileWithFIFO(t, root, generatedManifestRelativePath)
		}},
		{name: "generated file", setup: func(t *testing.T, root string) {
			replaceProjectGenerateTestFileWithFIFO(t, root, bundle.Files()[0].Path)
		}},
		{name: "publication journal", setup: func(t *testing.T, root string) {
			replaceProjectGenerateTestFileWithFIFO(t, root, publicationJournalRelativePath)
		}},
		{name: "nested module marker", setup: func(t *testing.T, root string) {
			replaceProjectGenerateTestFileWithFIFO(t, root, "nested/go.mod")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeProjectGenerateTestBundle(t, root, bundle)
			test.setup(t, root)
			before := snapshotProjectGenerateTestTree(t, root)
			result := checkProjectGenerateWithin(t, root, bundle, 2*time.Second)
			requireErrorIs(t, result.err, ErrGeneratedConflict)
			if after := snapshotProjectGenerateTestTree(t, root); strings.Join(before, "\n") != strings.Join(after, "\n") {
				t.Fatalf("Check(FIFO) mutated project\nbefore=%v\nafter=%v", before, after)
			}
		})
	}
}

func TestCheckBoundsGeneratedFileAndDirectoryReads(t *testing.T) {
	bundle := projectGenerateTestBundle(t)
	root := t.TempDir()
	writeProjectGenerateTestBundle(t, root, bundle)
	large := filepath.Join(root, filepath.FromSlash(bundle.Files()[0].Path))
	if err := os.Truncate(large, maxGeneratedSourceBytes+1); err != nil {
		t.Fatal(err)
	}
	_, err := Check(context.Background(), root, bundle)
	requireErrorIs(t, err, ErrGeneratedConflict)

	denseRoot := t.TempDir()
	for index := 0; index <= maxProjectDirectoryEntries; index++ {
		writeProjectGenerateTestFile(t, denseRoot, "dense/file-"+leftPadProjectGenerateInt(index)+".txt", nil, 0o644)
	}
	if _, err := projectRelativeDirectoryEntries(denseRoot, "dense"); !errors.Is(err, errProjectPathConflict) {
		t.Fatalf("dense directory error = %v, want conflict", err)
	}
}

func TestCheckFindsUnmanifestedGeneratedFileInRetiredDirectory(t *testing.T) {
	bundle := projectGenerateTestBundle(t)
	root := t.TempDir()
	// First adoption: no manifest and no desired files are present, but a file
	// in an app directory retired before the manifest existed must still block.
	retired := "apps/removed/zz_godj_retired.go"
	source := []byte("package removed\n")
	writeProjectGenerateTestFile(t, root, retired, source, 0o644)
	report, err := Check(context.Background(), root, bundle)
	requireErrorIs(t, err, ErrGeneratedDrift)
	assertProjectGenerateDrift(t, report, retired, DriftUnexpected, "", sha256Hex(source))
}

func TestCheckRejectsNonportableReservedGeneratedFilename(t *testing.T) {
	bundle := projectGenerateTestBundle(t)
	root := t.TempDir()
	writeProjectGenerateTestBundle(t, root, bundle)
	rogue := "removed/zz_godj_생성.go"
	writeProjectGenerateTestFile(t, root, rogue, []byte("package removed\n"), 0o644)
	report, err := Check(context.Background(), root, bundle)
	requireErrorIs(t, err, ErrGeneratedDrift)
	requireErrorIs(t, err, ErrGeneratedConflict)
	assertProjectGenerateDriftKind(t, report, rogue, DriftUnexpected)
}

func TestCheckReservedNamespaceSkipsControlVendorNestedModulesAndDirectorySymlinks(t *testing.T) {
	bundle := projectGenerateTestBundle(t)
	root := t.TempDir()
	writeProjectGenerateTestBundle(t, root, bundle)
	for _, filename := range []string{
		".git/cache/zz_godj_copy.go",
		".godj/zz_godj_control.go",
		"vendor/example.com/dependency/zz_godj_generated.go",
		"nested-module/zz_godj_generated.go",
	} {
		writeProjectGenerateTestFile(t, root, filename, []byte("package ignored\n"), 0o644)
	}
	writeProjectGenerateTestFile(t, root, "nested-module/go.mod", []byte("module example.com/nested\n"), 0o644)
	outside := t.TempDir()
	writeProjectGenerateTestFile(t, outside, "zz_godj_secret.go", []byte("outside secret\n"), 0o644)
	if err := os.Symlink(outside, filepath.Join(root, "linked-module")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	report, err := Check(context.Background(), root, bundle)
	if err != nil || !report.Clean() {
		t.Fatalf("Check(excluded subtrees) report=%#v error=%v", report, err)
	}
}

func TestOpenProjectRelativeNeverReadsSwappedParentOrFinalSymlink(t *testing.T) {
	for _, parentSwap := range []bool{false, true} {
		name := "final"
		if parentSwap {
			name = "parent"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			relative := "pkg/zz_godj_generated.go"
			insideSource := []byte("inside")
			outsideSource := []byte("outside-secret")
			writeProjectGenerateTestFile(t, root, relative, insideSource, 0o644)
			writeProjectGenerateTestFile(t, outside, "zz_godj_generated.go", outsideSource, 0o644)

			target := filepath.Join(root, filepath.FromSlash(relative))
			backup := target + ".physical"
			outsideTarget := filepath.Join(outside, "zz_godj_generated.go")
			if parentSwap {
				target = filepath.Join(root, "pkg")
				backup = target + ".physical"
				outsideTarget = outside
			}
			writerErr := make(chan error, 1)
			done := make(chan struct{})
			go func() {
				defer close(done)
				for iteration := 0; iteration < 300; iteration++ {
					if err := os.Rename(target, backup); err != nil {
						writerErr <- err
						return
					}
					if err := os.Symlink(outsideTarget, target); err != nil {
						writerErr <- err
						return
					}
					runtime.Gosched()
					if err := os.Remove(target); err != nil {
						writerErr <- err
						return
					}
					if err := os.Rename(backup, target); err != nil {
						writerErr <- err
						return
					}
				}
			}()
			for {
				contents, _, err := readRegularProjectFile(root, relative)
				if err == nil && string(contents) != string(insideSource) {
					t.Fatalf("read swapped outside contents %q", contents)
				}
				select {
				case <-done:
					select {
					case err := <-writerErr:
						t.Fatal(err)
					default:
					}
					return
				default:
					runtime.Gosched()
				}
			}
		})
	}
}

func assertProjectGenerateDrift(t *testing.T, report CheckReport, path string, kind DriftKind, expected, actual string) {
	t.Helper()
	for _, drift := range report.Drifts {
		if drift.Path == path && drift.Kind == kind {
			if drift.ExpectedSHA256 != expected || drift.ActualSHA256 != actual {
				t.Fatalf("drift %s/%s = %#v, want expected=%q actual=%q", path, kind, drift, expected, actual)
			}
			return
		}
	}
	t.Fatalf("missing drift %s/%s in %#v", path, kind, report.Drifts)
}

func assertProjectGenerateDriftKind(t *testing.T, report CheckReport, path string, kind DriftKind) {
	t.Helper()
	for _, drift := range report.Drifts {
		if drift.Path == path && drift.Kind == kind {
			return
		}
	}
	t.Fatalf("missing drift %s/%s in %#v", path, kind, report.Drifts)
}

func snapshotProjectGenerateTestTree(t *testing.T, root string) []string {
	t.Helper()
	var snapshot []string
	err := filepath.WalkDir(root, func(filename string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, filename)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		line := filepath.ToSlash(relative) + ":" + info.Mode().String()
		if info.Mode().IsRegular() {
			contents, err := os.ReadFile(filename)
			if err != nil {
				return err
			}
			line += ":" + sha256Hex(contents)
		}
		snapshot = append(snapshot, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(snapshot)
	return snapshot
}

func leftPadProjectGenerateInt(value int) string {
	result := "000000" + fmt.Sprint(value)
	return result[len(result)-6:]
}

type projectGenerateCheckResult struct {
	report CheckReport
	err    error
}

func checkProjectGenerateWithin(
	t *testing.T,
	root string,
	bundle codegen.GeneratedBundle,
	maximum time.Duration,
) projectGenerateCheckResult {
	t.Helper()
	completed := make(chan projectGenerateCheckResult, 1)
	go func() {
		report, err := Check(context.Background(), root, bundle)
		completed <- projectGenerateCheckResult{report: report, err: err}
	}()
	select {
	case result := <-completed:
		return result
	case <-time.After(maximum):
		t.Fatalf("Check did not reject a non-regular path within %s", maximum)
		return projectGenerateCheckResult{}
	}
}

func replaceProjectGenerateTestFileWithFIFO(t *testing.T, root, relative string) {
	t.Helper()
	filename := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatalf("create FIFO parent: %v", err)
	}
	if err := os.Remove(filename); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove prior FIFO target: %v", err)
	}
	if err := unix.Mkfifo(filename, 0o600); err != nil {
		t.Fatalf("create FIFO %s: %v", relative, err)
	}
}
