//go:build darwin || linux

package linked

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/projectcheck/protocol"
	"github.com/progresshans/godj/migrations/definition"
	"golang.org/x/sys/unix"
)

func TestActualRootCountBoundaries(t *testing.T) {
	t.Parallel()
	root := newProjectRoot(t)
	roots := make([]string, maxRoots)
	for index := range roots {
		roots[index] = fmt.Sprintf("root-%03d", index)
		if err := os.Mkdir(filepath.Join(root, roots[index]), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	response, report, err := invoke(t, root, roots[:maxRoots-1], protocol.RequestDocument(), nil)
	if err != nil || !response.OK || response.Result.SourceCount != 0 || report.RootsOpened != maxRoots-1 || report.SourceReads != 0 || report.LoadCalls != 1 || report.RunnerResponseWrites != 1 {
		t.Fatalf("root maximum-1 = %+v, %+v, %v", response, report, err)
	}
	response, report, err = invoke(t, root, roots, protocol.RequestDocument(), nil)
	if err != nil || !response.OK || report.RootsOpened != maxRoots || report.LoadCalls != 1 || report.SourceReads != 0 || report.DefinitionSetsPublished != 1 {
		t.Fatalf("root maximum = %+v, %+v, %v", response, report, err)
	}
	response, report, err = invoke(t, root, append(append([]string(nil), roots...), "overflow"), protocol.RequestDocument(), nil)
	if err != nil || response.Failure.Code != protocol.CodeInvalidProjectSourceConfig || report.RootsOpened != 0 || report.LoadCalls != 0 || report.RunnerResponseWrites != 1 {
		t.Fatalf("root maximum+1 = %+v, %+v, %v", response, report, err)
	}
}

func TestActualDirectoryEntryCountBoundaries(t *testing.T) {
	t.Parallel()
	root := newProjectRoot(t, "migrations")
	entries := make([]directoryEntry, maxDirectoryEntries+1)
	for index := range entries {
		entries[index] = directoryEntry{name: "ignored"}
	}

	response, report, err := invoke(t, root, []string{"migrations"}, protocol.RequestDocument(), systemDependencies{
		enumerateRoot: fixedEnumeration(entries[:maxDirectoryEntries-1]),
	})
	if err != nil || !response.OK || response.Result.SourceCount != 0 || report.DirectoryEntriesSeen != maxDirectoryEntries-1 || report.SourceReads != 0 || report.LoadCalls != 1 || report.RunnerResponseWrites != 1 {
		t.Fatalf("entry maximum-1 = %+v, %+v, %v", response, report, err)
	}
	response, report, err = invoke(t, root, []string{"migrations"}, protocol.RequestDocument(), systemDependencies{
		enumerateRoot: fixedEnumeration(entries[:maxDirectoryEntries]),
	})
	if err != nil || !response.OK || report.DirectoryEntriesSeen != maxDirectoryEntries || report.LoadCalls != 1 || report.SourceReads != 0 {
		t.Fatalf("entry maximum = %+v, %+v, %v", response, report, err)
	}
	response, report, err = invoke(t, root, []string{"migrations"}, protocol.RequestDocument(), systemDependencies{
		enumerateRoot: fixedEnumeration(entries),
	})
	if err != nil || response.Failure.Code != protocol.CodeSourceCatalogLimitExceeded || report.DirectoryEntriesSeen != maxDirectoryEntries || report.LoadCalls != 0 || report.SourceReads != 0 {
		t.Fatalf("entry maximum+1 = %+v, %+v, %v", response, report, err)
	}
}

func TestActualSourceIDByteBoundaries(t *testing.T) {
	t.Parallel()
	root := newProjectRoot(t)
	suffix := ".godj.json"
	filename := strings.Repeat("n", 200-len(suffix)) + suffix
	shortFilename := filename[1:]
	logical := logicalPathWithLength(definition.MaxSourceIDBytes - 1 - len(filename))
	final := createRootAt(t, root, logical)
	t.Cleanup(func() { removeDeepRootAt(t, root, logical, shortFilename, filename) })
	writeFileAt(t, final, shortFilename, migrationDocument("alpha", "0000", nil))
	if actual := len(logical + "/" + shortFilename); actual != definition.MaxSourceIDBytes-1 {
		t.Fatalf("maximum-1 SourceID bytes = %d", actual)
	}
	response, report, err := invoke(t, root, []string{logical}, protocol.RequestDocument(), nil)
	if err != nil || !response.OK || response.Result.SourceCount != 1 || report.SourceReads != 1 || report.LoadCalls != 1 || report.RunnerResponseWrites != 1 {
		t.Fatalf("SourceID maximum-1 = %+v, %+v, %v", response, report, err)
	}
	if err := unix.Unlinkat(int(final.Fd()), shortFilename, 0); err != nil {
		t.Fatal(err)
	}
	writeFileAt(t, final, filename, migrationDocument("alpha", "0001", nil))
	if err := final.Close(); err != nil {
		t.Fatal(err)
	}
	if actual := len(logical + "/" + filename); actual != definition.MaxSourceIDBytes {
		t.Fatalf("equal SourceID bytes = %d", actual)
	}

	response, report, err = invoke(t, root, []string{logical}, protocol.RequestDocument(), nil)
	if err != nil || !response.OK || response.Result.SourceCount != 1 || report.SourceReads != 1 || report.LoadCalls != 1 {
		t.Fatalf("SourceID maximum = %+v, %+v, %v", response, report, err)
	}
	overflowName := "x" + filename
	response, report, err = invoke(t, root, []string{logical}, protocol.RequestDocument(), systemDependencies{
		enumerateRoot: fixedEnumeration([]directoryEntry{{name: overflowName}}),
	})
	if err != nil || response.Failure.Code != protocol.CodeSourceCatalogLimitExceeded || report.SourceReads != 0 || report.LoadCalls != 0 {
		t.Fatalf("SourceID maximum+1 = %+v, %+v, %v", response, report, err)
	}
}

func TestActualSourceCountBoundaries(t *testing.T) {
	t.Parallel()
	root := newProjectRoot(t, "migrations")
	target := filepath.Join(root, "shared-definition.json")
	writeFile(t, target, migrationDocument("alpha", "0001", nil))
	for index := 0; index <= definition.MaxSources; index++ {
		name := fmt.Sprintf("%04d.godj.json", index)
		if err := os.Link(target, filepath.Join(root, "migrations", name)); err != nil {
			t.Fatal(err)
		}
	}

	response, report, err := invoke(t, root, []string{"migrations"}, protocol.RequestDocument(), nil)
	if err != nil || response.Failure.Code != protocol.CodeSourceCatalogLimitExceeded || report.DirectoryEntriesSeen != definition.MaxSources+1 || report.SourceReads != 0 || report.LoadCalls != 0 {
		t.Fatalf("source maximum+1 = %+v, %+v, %v", response, report, err)
	}
	if err := os.Remove(filepath.Join(root, "migrations", fmt.Sprintf("%04d.godj.json", definition.MaxSources))); err != nil {
		t.Fatal(err)
	}
	response, report, err = invoke(t, root, []string{"migrations"}, protocol.RequestDocument(), nil)
	if err != nil || response.Failure.Category != protocol.CategoryGraph || response.Failure.Code != "duplicate_node" || report.SourceReads != definition.MaxSources || report.LoadCalls != 1 || report.DocumentsReceived != definition.MaxSources || report.DefinitionsPublished != 0 || !report.HasLoadFailure {
		t.Fatalf("source maximum = %+v, %+v, %v", response, report, err)
	}
	if err := os.Remove(filepath.Join(root, "migrations", fmt.Sprintf("%04d.godj.json", definition.MaxSources-1))); err != nil {
		t.Fatal(err)
	}
	response, report, err = invoke(t, root, []string{"migrations"}, protocol.RequestDocument(), nil)
	if err != nil || response.OK || response.Failure.Category != protocol.CategoryGraph || response.Failure.Code != "duplicate_node" || report.SourceReads != definition.MaxSources-1 || report.LoadCalls != 1 || report.DocumentsReceived != definition.MaxSources-1 || report.DefinitionsPublished != 0 || report.RunnerResponseWrites != 1 {
		t.Fatalf("source maximum-1 = %+v, %+v, %v", response, report, err)
	}
}

func TestActualDocumentByteBoundariesAndPostReadPrecedence(t *testing.T) {
	t.Parallel()
	root := newProjectRoot(t, "migrations")
	filename := "source.godj.json"
	path := filepath.Join(root, "migrations", filename)
	writeFile(t, path, paddedDocument(t, "alpha", "0001", maxDocumentBytes-1))

	response, report, err := invoke(t, root, []string{"migrations"}, protocol.RequestDocument(), nil)
	if err != nil || !response.OK || response.Result.SourceCount != 1 || report.SourceReads != 1 || report.LoadCalls != 1 || report.DefinitionsPublished != 1 || report.RunnerResponseWrites != 1 {
		t.Fatalf("document maximum-1 = %+v, %+v, %v", response, report, err)
	}
	writeFile(t, path, paddedDocument(t, "alpha", "0001", maxDocumentBytes))
	response, report, err = invoke(t, root, []string{"migrations"}, protocol.RequestDocument(), nil)
	if err != nil || !response.OK || report.SourceReads != 1 || report.LoadCalls != 1 || report.DefinitionsPublished != 1 {
		t.Fatalf("document maximum = %+v, %+v, %v", response, report, err)
	}
	writeFile(t, path, paddedDocument(t, "alpha", "0001", maxDocumentBytes+1))
	response, report, err = invoke(t, root, []string{"migrations"}, protocol.RequestDocument(), nil)
	if err != nil || response.Failure.Code != protocol.CodeSourceCatalogLimitExceeded || report.SourceReads != 0 || report.LoadCalls != 0 {
		t.Fatalf("document maximum+1 = %+v, %+v, %v", response, report, err)
	}

	replaced := false
	response, report, err = invoke(t, root, []string{"migrations"}, protocol.RequestDocument(), systemDependencies{
		afterCandidateRead: func(_ int, name string) {
			if replaced || name != filename {
				return
			}
			replaced = true
			if renameErr := os.Rename(path, path+".old"); renameErr != nil {
				t.Fatalf("rename oversized source: %v", renameErr)
			}
			if symlinkErr := os.Symlink(filename+".old", path); symlinkErr != nil {
				t.Fatalf("replace oversized source: %v", symlinkErr)
			}
		},
	})
	if err != nil || response.Failure.Code != protocol.CodeUnsafeSourceEntry || report.SourceReads != 0 || report.LoadCalls != 0 {
		t.Fatalf("document post-read precedence = %+v, %+v, %v", response, report, err)
	}
}

func TestActualBatchByteBoundariesAndPostReadPrecedence(t *testing.T) {
	t.Parallel()
	root := newProjectRoot(t, "migrations")
	wantSources := maxBatchBytes / maxDocumentBytes
	lastName := fmt.Sprintf("%02d.godj.json", wantSources-1)
	for index := 0; index < wantSources; index++ {
		name := fmt.Sprintf("%02d.godj.json", index)
		size := maxDocumentBytes
		if index == wantSources-1 {
			size--
		}
		document := paddedDocument(t, fmt.Sprintf("app%02d", index), "0001", size)
		writeFile(t, filepath.Join(root, "migrations", name), document)
	}

	response, report, err := invoke(t, root, []string{"migrations"}, protocol.RequestDocument(), nil)
	if err != nil || !response.OK || response.Result.SourceCount != wantSources || report.SourceReads != wantSources || report.LoadCalls != 1 || report.DefinitionsPublished != wantSources || report.RunnerResponseWrites != 1 {
		t.Fatalf("batch maximum-1 = %+v, %+v, %v", response, report, err)
	}
	writeFile(t, filepath.Join(root, "migrations", lastName), paddedDocument(t, fmt.Sprintf("app%02d", wantSources-1), "0001", maxDocumentBytes))
	response, report, err = invoke(t, root, []string{"migrations"}, protocol.RequestDocument(), nil)
	if err != nil || !response.OK || response.Result.SourceCount != wantSources || report.SourceReads != wantSources || report.LoadCalls != 1 || report.DefinitionsPublished != wantSources {
		t.Fatalf("batch maximum = %+v, %+v, %v", response, report, err)
	}
	overflowName := "zz_overflow.godj.json"
	overflowPath := filepath.Join(root, "migrations", overflowName)
	writeFile(t, overflowPath, []byte{'{'})
	response, report, err = invoke(t, root, []string{"migrations"}, protocol.RequestDocument(), nil)
	if err != nil || response.Failure.Code != protocol.CodeSourceCatalogLimitExceeded || report.SourceReads != wantSources || report.LoadCalls != 0 || report.DefinitionsPublished != 0 {
		t.Fatalf("batch maximum+1 = %+v, %+v, %v", response, report, err)
	}

	replaced := false
	response, report, err = invoke(t, root, []string{"migrations"}, protocol.RequestDocument(), systemDependencies{
		afterCandidateRead: func(_ int, name string) {
			if replaced || name != overflowName {
				return
			}
			replaced = true
			if renameErr := os.Rename(overflowPath, overflowPath+".old"); renameErr != nil {
				t.Fatalf("rename batch overflow source: %v", renameErr)
			}
			if symlinkErr := os.Symlink(overflowName+".old", overflowPath); symlinkErr != nil {
				t.Fatalf("replace batch overflow source: %v", symlinkErr)
			}
		},
	})
	if err != nil || response.Failure.Code != protocol.CodeUnsafeSourceEntry || report.SourceReads != wantSources || report.LoadCalls != 0 {
		t.Fatalf("batch post-read precedence = %+v, %+v, %v", response, report, err)
	}
}

func fixedEnumeration(entries []directoryEntry) func(string, *os.File, func([]directoryEntry, error) bool) error {
	owned := append([]directoryEntry(nil), entries...)
	return func(_ string, _ *os.File, yield func([]directoryEntry, error) bool) error {
		yield(owned, io.EOF)
		return nil
	}
}

func logicalPathWithLength(length int) string {
	components := (length + 200) / 201
	characters := length - (components - 1)
	base := characters / components
	remainder := characters % components
	parts := make([]string, components)
	for index := range parts {
		componentLength := base
		if index < remainder {
			componentLength++
		}
		parts[index] = strings.Repeat("r", componentLength)
	}
	return strings.Join(parts, "/")
}

func createRootAt(t *testing.T, projectRoot, logical string) *os.File {
	t.Helper()
	fd, err := unix.Open(projectRoot, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		t.Fatal(err)
	}
	current := os.NewFile(uintptr(fd), projectRoot)
	if current == nil {
		_ = unix.Close(fd)
		t.Fatal("wrap project root")
	}
	for _, component := range strings.Split(logical, "/") {
		if err := unix.Mkdirat(int(current.Fd()), component, 0o755); err != nil {
			_ = current.Close()
			t.Fatal(err)
		}
		childFD, err := unix.Openat(int(current.Fd()), component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			_ = current.Close()
			t.Fatal(err)
		}
		child := os.NewFile(uintptr(childFD), component)
		if child == nil {
			_ = unix.Close(childFD)
			_ = current.Close()
			t.Fatal("wrap nested root")
		}
		if err := current.Close(); err != nil {
			_ = child.Close()
			t.Fatal(err)
		}
		current = child
	}
	return current
}

func writeFileAt(t *testing.T, directory *os.File, name string, document []byte) {
	t.Helper()
	fd, err := unix.Openat(int(directory.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		t.Fatal("wrap source file")
	}
	written, writeErr := file.Write(document)
	closeErr := file.Close()
	if writeErr != nil || written != len(document) || closeErr != nil {
		t.Fatalf("write source file = %d/%d, %v, close %v", written, len(document), writeErr, closeErr)
	}
}

func removeDeepRootAt(t *testing.T, projectRoot, logical string, filenames ...string) {
	t.Helper()
	projectFD, err := unix.Open(projectRoot, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		t.Errorf("open deep-root cleanup project: %v", err)
		return
	}
	handles := []*os.File{os.NewFile(uintptr(projectFD), projectRoot)}
	components := strings.Split(logical, "/")
	for _, component := range components {
		parent := handles[len(handles)-1]
		childFD, openErr := unix.Openat(int(parent.Fd()), component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if openErr != nil {
			t.Errorf("open deep-root cleanup component: %v", openErr)
			break
		}
		handles = append(handles, os.NewFile(uintptr(childFD), component))
	}
	if len(handles) == len(components)+1 {
		for _, filename := range filenames {
			if err := unix.Unlinkat(int(handles[len(handles)-1].Fd()), filename, 0); err != nil && err != unix.ENOENT {
				t.Errorf("remove deep source: %v", err)
			}
		}
	}
	for index := len(handles) - 1; index >= 1; index-- {
		_ = handles[index].Close()
		if err := unix.Unlinkat(int(handles[index-1].Fd()), components[index-1], unix.AT_REMOVEDIR); err != nil {
			t.Errorf("remove deep root component: %v", err)
		}
	}
	_ = handles[0].Close()
}

func paddedDocument(t *testing.T, app, name string, size int) []byte {
	t.Helper()
	document := migrationDocument(app, name, nil)
	if len(document) > size {
		t.Fatalf("base document bytes = %d, target %d", len(document), size)
	}
	return append(document, bytes.Repeat([]byte{' '}, size-len(document))...)
}
