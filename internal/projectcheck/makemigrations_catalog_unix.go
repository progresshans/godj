//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/migrations/definition"
	"golang.org/x/sys/unix"
)

const (
	makemigrationsMaxDirectoryEntries = 65_536
	makemigrationsDirectoryChunk      = 128
)

// captureMakemigrationsFilesystemCatalog independently snapshots the exact
// flat writer-root catalog from the global owner's retained project root. It
// intentionally does not reuse a child-reported digest or path authority.
func captureMakemigrationsFilesystemCatalog(
	ctx context.Context,
	project retainedProject,
	logicalRoot string,
) ([]definition.Source, error) {
	if ctx == nil {
		return nil, errors.New("makemigrations catalog: nil context")
	}
	if err := validateMakemigrationsLogicalRoot(logicalRoot); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !verifyRetainedProject(project) {
		return nil, errors.New("makemigrations catalog: project authority changed")
	}

	root, rootIdentity, err := openMakemigrationsLogicalRoot(project.root, logicalRoot)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	reader, err := duplicateMakemigrationsDirectory(root)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	names := make([]string, 0)
	entriesSeen := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entries, readErr := reader.ReadDir(makemigrationsDirectoryChunk)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, fmt.Errorf("makemigrations catalog: enumerate writer root: %w", readErr)
		}
		for _, entry := range entries {
			if entriesSeen >= makemigrationsMaxDirectoryEntries {
				return nil, errors.New("makemigrations catalog: directory entry limit exceeded")
			}
			entriesSeen++
			if bytes.HasSuffix([]byte(entry.Name()), []byte(".godj.json")) {
				names = append(names, entry.Name())
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
	}

	sort.Slice(names, func(left, right int) bool {
		return bytes.Compare(
			[]byte(makemigrationsSourceID(logicalRoot, names[left])),
			[]byte(makemigrationsSourceID(logicalRoot, names[right])),
		) < 0
	})
	if len(names) > definition.MaxSources {
		return nil, errors.New("makemigrations catalog: source count limit exceeded")
	}

	sources := make([]definition.Source, 0, len(names))
	batchBytes := 0
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sourceID := makemigrationsSourceID(logicalRoot, name)
		if !utf8.ValidString(sourceID) || len([]byte(sourceID)) > definition.MaxSourceIDBytes {
			return nil, errors.New("makemigrations catalog: invalid source identity")
		}
		document, readErr := readMakemigrationsCatalogMember(root, name, batchBytes)
		if readErr != nil {
			return nil, readErr
		}
		batchBytes += len(document)
		sources = append(sources, definition.Source{SourceID: sourceID, Document: document})
	}

	if !verifyRetainedProject(project) || !verifyMakemigrationsLogicalRoot(project.root, logicalRoot, rootIdentity) {
		return nil, errors.New("makemigrations catalog: retained authority changed")
	}
	return sources, nil
}

func validateMakemigrationsLogicalRoot(root string) error {
	if root == "" || !utf8.ValidString(root) || path.IsAbs(root) || path.Clean(root) != root || strings.ContainsAny(root, "\\\x00") {
		return errors.New("makemigrations catalog: invalid writer root")
	}
	for _, component := range strings.Split(root, "/") {
		if component == "" || component == ".." {
			return errors.New("makemigrations catalog: invalid writer root")
		}
	}
	return nil
}

func openMakemigrationsLogicalRoot(project *os.File, logical string) (*os.File, unix.Stat_t, error) {
	if project == nil {
		return nil, unix.Stat_t{}, errors.New("makemigrations catalog: nil retained project")
	}
	current, err := duplicateMakemigrationsDirectory(project)
	if err != nil {
		return nil, unix.Stat_t{}, err
	}
	if logical != "." {
		for _, component := range strings.Split(logical, "/") {
			var initial unix.Stat_t
			if err := unix.Fstatat(int(current.Fd()), component, &initial, unix.AT_SYMLINK_NOFOLLOW); err != nil || initial.Mode&unix.S_IFMT != unix.S_IFDIR {
				_ = current.Close()
				return nil, unix.Stat_t{}, errors.New("makemigrations catalog: invalid writer root")
			}
			fd, err := unix.Openat(int(current.Fd()), component, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
			if err != nil {
				_ = current.Close()
				return nil, unix.Stat_t{}, errors.New("makemigrations catalog: open writer root")
			}
			next := os.NewFile(uintptr(fd), component)
			if next == nil {
				_ = unix.Close(fd)
				_ = current.Close()
				return nil, unix.Stat_t{}, errors.New("makemigrations catalog: retain writer root")
			}
			var opened unix.Stat_t
			if err := unix.Fstat(fd, &opened); err != nil || opened.Mode&unix.S_IFMT != unix.S_IFDIR || !sameIdentity(initial, opened) {
				_ = next.Close()
				_ = current.Close()
				return nil, unix.Stat_t{}, errors.New("makemigrations catalog: writer root changed")
			}
			_ = current.Close()
			current = next
		}
	}
	var retained unix.Stat_t
	if err := unix.Fstat(int(current.Fd()), &retained); err != nil || retained.Mode&unix.S_IFMT != unix.S_IFDIR {
		_ = current.Close()
		return nil, unix.Stat_t{}, errors.New("makemigrations catalog: invalid retained writer root")
	}
	return current, retained, nil
}

func duplicateMakemigrationsDirectory(directory *os.File) (*os.File, error) {
	fd, err := unix.Openat(int(directory.Fd()), ".", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	duplicate := os.NewFile(uintptr(fd), directory.Name())
	if duplicate == nil {
		_ = unix.Close(fd)
		return nil, errors.New("makemigrations catalog: retain directory")
	}
	return duplicate, nil
}

func verifyMakemigrationsLogicalRoot(project *os.File, logical string, expected unix.Stat_t) bool {
	current, actual, err := openMakemigrationsLogicalRoot(project, logical)
	if err != nil {
		return false
	}
	closeErr := current.Close()
	return closeErr == nil && sameIdentity(expected, actual) && expected.Mode&unix.S_IFMT == actual.Mode&unix.S_IFMT
}

func readMakemigrationsCatalogMember(root *os.File, name string, batchBytes int) ([]byte, error) {
	remainingBatch := definition.MaxBatchBytes - batchBytes
	if remainingBatch < 0 {
		remainingBatch = 0
	}
	maximum := definition.MaxDocumentBytes
	if remainingBatch < maximum {
		maximum = remainingBatch
	}
	document, _, present, err := readMakemigrationsRegularAt(root, name, maximum)
	if err != nil || !present {
		clear(document)
		return nil, errors.New("makemigrations catalog: source read failed")
	}
	return document, nil
}

func makemigrationsSourceID(root, name string) string {
	if root == "." {
		return name
	}
	return root + "/" + name
}
