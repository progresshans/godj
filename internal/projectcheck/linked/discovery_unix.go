//go:build darwin || linux

package linked

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/progresshans/godj/internal/projectcheck/protocol"
	"github.com/progresshans/godj/migrations/definition"
	"golang.org/x/sys/unix"
)

const (
	maxDirectoryEntries = 65_536
	maxDocumentBytes    = 1 << 20
	maxBatchBytes       = 16 << 20
	readDirChunk        = 128
)

type systemDependencies struct {
	beforeProjectRootOpen func(string)
	afterRootInitialStat  func(parentFD int, name string)
	afterCandidateRead    func(rootFD int, name string)
	beforeResponseWrite   func()
	readCandidate         func(sourceID string, file *os.File, maximum uint64) ([]byte, error)
	enumerateRoot         func(root string, directory *os.File, yield func([]directoryEntry, error) bool) error
}

type identity struct {
	device uint64
	inode  uint64
	mode   uint32
}

type retainedRoot struct {
	logical string
	handle  *os.File
}

type directoryEntry struct {
	name string
}

type retainedEntry struct {
	root *retainedRoot
	name string
}

type candidate struct {
	root     *retainedRoot
	name     string
	sourceID string
	identity identity
}

func discover(
	ctx context.Context,
	projectRoot string,
	roots []string,
	report *Report,
	dependencies systemDependencies,
) ([]definition.Source, protocol.Failure, bool, error) {
	canonical, failure, failed := canonicalRoots(roots)
	if failed {
		return nil, failure, true, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, protocol.Failure{}, false, err
	}

	physicalRoot, expected, err := physicalDirectory(projectRoot)
	if err != nil {
		return nil, discoveryFailure(protocol.CodeSourceDiscoveryFailed), true, nil
	}
	if dependencies.beforeProjectRootOpen != nil {
		dependencies.beforeProjectRootOpen(physicalRoot)
	}
	project, err := openRetainedProjectRoot(physicalRoot, expected)
	if err != nil {
		return nil, discoveryFailure(protocol.CodeSourceDiscoveryFailed), true, nil
	}
	defer project.Close()

	retained := make([]retainedRoot, 0, len(canonical))
	defer func() {
		for index := range retained {
			_ = retained[index].handle.Close()
		}
	}()
	for _, logical := range canonical {
		if err := ctx.Err(); err != nil {
			return nil, protocol.Failure{}, false, err
		}
		handle, failure, failed, invariantErr := openSemanticRoot(project, logical, dependencies)
		if invariantErr != nil {
			return nil, protocol.Failure{}, false, invariantErr
		}
		if failed {
			return nil, failure, true, nil
		}
		retained = append(retained, retainedRoot{logical: logical, handle: handle})
	}
	report.RootsOpened += len(retained)

	entries := make([]retainedEntry, 0)
	entryCount := 0
	for index := range retained {
		if err := ctx.Err(); err != nil {
			return nil, protocol.Failure{}, false, err
		}
		root := &retained[index]
		var enumerationFailure protocol.Failure
		failed := false
		err := enumerateDirectory(root, dependencies, func(chunk []directoryEntry, chunkErr error) bool {
			if chunkErr != nil && !errors.Is(chunkErr, io.EOF) {
				enumerationFailure = discoveryFailure(protocol.CodeSourceDiscoveryFailed)
				failed = true
				return false
			}
			for _, entry := range chunk {
				updated, exceeded := addDirectoryEntry(entryCount)
				if exceeded {
					enumerationFailure = discoveryFailure(protocol.CodeSourceCatalogLimitExceeded)
					failed = true
					return false
				}
				entryCount = updated
				report.DirectoryEntriesSeen++
				entries = append(entries, retainedEntry{root: root, name: entry.name})
			}
			return !errors.Is(chunkErr, io.EOF)
		})
		if err != nil {
			return nil, discoveryFailure(protocol.CodeSourceDiscoveryFailed), true, nil
		}
		if failed {
			return nil, enumerationFailure, true, nil
		}
	}

	sort.Slice(entries, func(left, right int) bool {
		return bytes.Compare([]byte(sourceID(entries[left].root.logical, entries[left].name)), []byte(sourceID(entries[right].root.logical, entries[right].name))) < 0
	})
	candidates := make([]candidate, 0)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, protocol.Failure{}, false, err
		}
		if !isDefinitionCandidate(entry.name) {
			continue
		}
		identifier := sourceID(entry.root.logical, entry.name)
		if sourceIDBytesExceeded(identifier) {
			return nil, discoveryFailure(protocol.CodeSourceCatalogLimitExceeded), true, nil
		}
		if !utf8.ValidString(identifier) {
			return nil, discoveryFailure(protocol.CodeInvalidSourceEntry), true, nil
		}
		var initial unix.Stat_t
		if err := unix.Fstatat(int(entry.root.handle.Fd()), entry.name, &initial, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return nil, discoveryFailure(protocol.CodeSourceReadFailed), true, nil
		}
		if !modeIs(uint32(initial.Mode), unix.S_IFREG) {
			return nil, discoveryFailure(protocol.CodeUnsafeSourceEntry), true, nil
		}
		candidates = append(candidates, candidate{
			root:     entry.root,
			name:     entry.name,
			sourceID: identifier,
			identity: identityOf(&initial),
		})
	}
	if sourceCountExceeded(len(candidates)) {
		return nil, discoveryFailure(protocol.CodeSourceCatalogLimitExceeded), true, nil
	}

	sources := make([]definition.Source, 0, len(candidates))
	var batchBytes uint64
	for _, item := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, protocol.Failure{}, false, err
		}
		document, failure, failed, invariantErr := readStableCandidate(item, batchBytes, dependencies)
		if invariantErr != nil {
			return nil, protocol.Failure{}, false, invariantErr
		}
		if failed {
			return nil, failure, true, nil
		}
		batchBytes += uint64(len(document))
		report.SourceReads++
		sources = append(sources, definition.Source{SourceID: item.sourceID, Document: document})
	}
	return sources, protocol.Failure{}, false, nil
}

func canonicalRoots(roots []string) ([]string, protocol.Failure, bool) {
	if len(roots) > maxRoots {
		return nil, discoveryFailure(protocol.CodeInvalidProjectSourceConfig), true
	}
	canonical := append([]string(nil), roots...)
	for _, root := range canonical {
		if !utf8.ValidString(root) || root == "" || strings.ContainsAny(root, "\\\x00") || path.IsAbs(root) || path.Clean(root) != root {
			return nil, discoveryFailure(protocol.CodeInvalidProjectSourceConfig), true
		}
		for _, component := range strings.Split(root, "/") {
			if component == "" || component == ".." {
				return nil, discoveryFailure(protocol.CodeInvalidProjectSourceConfig), true
			}
		}
	}
	sort.Slice(canonical, func(left, right int) bool {
		return bytes.Compare([]byte(canonical[left]), []byte(canonical[right])) < 0
	})
	for index := 1; index < len(canonical); index++ {
		if canonical[index] == canonical[index-1] {
			return nil, discoveryFailure(protocol.CodeInvalidProjectSourceConfig), true
		}
	}
	return canonical, protocol.Failure{}, false
}

func physicalDirectory(candidate string) (string, identity, error) {
	if candidate == "" {
		workingDirectory, err := os.Getwd()
		if err != nil {
			return "", identity{}, err
		}
		resolved, err := filepath.EvalSymlinks(workingDirectory)
		if err != nil {
			return "", identity{}, err
		}
		candidate, err = filepath.Abs(resolved)
		if err != nil {
			return "", identity{}, err
		}
	}
	if !filepath.IsAbs(candidate) || filepath.Clean(candidate) != candidate {
		return "", identity{}, errors.New("project root is not clean absolute path")
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", identity{}, err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil || filepath.Clean(resolved) != candidate {
		return "", identity{}, errors.New("project root is not physical")
	}
	info, err := os.Lstat(resolved)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", identity{}, errors.New("project root is not a physical directory")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", identity{}, errors.New("project root identity unavailable")
	}
	return resolved, identity{device: uint64(stat.Dev), inode: uint64(stat.Ino), mode: uint32(stat.Mode)}, nil
}

func openRetainedProjectRoot(physical string, expected identity) (*os.File, error) {
	fd, err := unix.Open(physical, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	handle := os.NewFile(uintptr(fd), physical)
	if handle == nil {
		_ = unix.Close(fd)
		return nil, errors.New("could not wrap project directory")
	}
	var opened unix.Stat_t
	if err := unix.Fstat(fd, &opened); err != nil || !modeIs(uint32(opened.Mode), unix.S_IFDIR) || identityOf(&opened) != expected {
		_ = handle.Close()
		return nil, errors.New("project directory identity changed")
	}
	info, err := os.Lstat(physical)
	if err != nil {
		_ = handle.Close()
		return nil, err
	}
	current, ok := info.Sys().(*syscall.Stat_t)
	if !ok || (identity{device: uint64(current.Dev), inode: uint64(current.Ino), mode: uint32(current.Mode)}) != expected {
		_ = handle.Close()
		return nil, errors.New("project directory changed after open")
	}
	return handle, nil
}

func openSemanticRoot(project *os.File, logical string, dependencies systemDependencies) (*os.File, protocol.Failure, bool, error) {
	current, err := duplicateDirectory(project)
	if err != nil {
		return nil, discoveryFailure(protocol.CodeSourceDiscoveryFailed), true, nil
	}
	if logical == "." {
		return current, protocol.Failure{}, false, nil
	}
	for _, component := range strings.Split(logical, "/") {
		exists, err := directoryContainsRawName(current, component)
		if err != nil {
			_ = current.Close()
			return nil, discoveryFailure(protocol.CodeSourceDiscoveryFailed), true, nil
		}
		if !exists {
			_ = current.Close()
			return nil, discoveryFailure(protocol.CodeInvalidSourceRoot), true, nil
		}
		var initial unix.Stat_t
		if err := unix.Fstatat(int(current.Fd()), component, &initial, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			_ = current.Close()
			return nil, discoveryFailure(protocol.CodeSourceDiscoveryFailed), true, nil
		}
		if !modeIs(uint32(initial.Mode), unix.S_IFDIR) {
			_ = current.Close()
			return nil, discoveryFailure(protocol.CodeInvalidSourceRoot), true, nil
		}
		if dependencies.afterRootInitialStat != nil {
			dependencies.afterRootInitialStat(int(current.Fd()), component)
		}
		childFD, err := unix.Openat(int(current.Fd()), component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			_ = current.Close()
			return nil, discoveryFailure(protocol.CodeSourceDiscoveryFailed), true, nil
		}
		child := os.NewFile(uintptr(childFD), component)
		if child == nil {
			_ = unix.Close(childFD)
			_ = current.Close()
			return nil, protocol.Failure{}, false, errors.New("project linked: could not wrap source root")
		}
		var opened unix.Stat_t
		statErr := unix.Fstat(childFD, &opened)
		_ = current.Close()
		if statErr != nil || !modeIs(uint32(opened.Mode), unix.S_IFDIR) || identityOf(&opened) != identityOf(&initial) {
			_ = child.Close()
			return nil, discoveryFailure(protocol.CodeSourceDiscoveryFailed), true, nil
		}
		current = child
	}
	return current, protocol.Failure{}, false, nil
}

func duplicateDirectory(handle *os.File) (*os.File, error) {
	fd, err := unix.Openat(int(handle.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	duplicated := os.NewFile(uintptr(fd), handle.Name())
	if duplicated == nil {
		_ = unix.Close(fd)
		return nil, errors.New("could not wrap duplicated directory")
	}
	return duplicated, nil
}

func directoryContainsRawName(handle *os.File, name string) (bool, error) {
	reader, err := duplicateDirectory(handle)
	if err != nil {
		return false, err
	}
	defer reader.Close()
	for {
		entries, readErr := reader.ReadDir(readDirChunk)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return false, readErr
		}
		for _, entry := range entries {
			if entry.Name() == name {
				return true, nil
			}
		}
		if errors.Is(readErr, io.EOF) {
			return false, nil
		}
	}
}

func enumerateDirectory(root *retainedRoot, dependencies systemDependencies, yield func([]directoryEntry, error) bool) error {
	reader, err := duplicateDirectory(root.handle)
	if err != nil {
		return err
	}
	defer reader.Close()
	if dependencies.enumerateRoot != nil {
		return dependencies.enumerateRoot(root.logical, reader, yield)
	}
	for {
		entries, readErr := reader.ReadDir(readDirChunk)
		chunk := make([]directoryEntry, len(entries))
		for index, entry := range entries {
			chunk[index] = directoryEntry{name: entry.Name()}
		}
		if !yield(chunk, readErr) || errors.Is(readErr, io.EOF) {
			return nil
		}
	}
}

func readStableCandidate(item candidate, batchBytes uint64, dependencies systemDependencies) ([]byte, protocol.Failure, bool, error) {
	fd, err := unix.Openat(int(item.root.handle.Fd()), item.name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, discoveryFailure(protocol.CodeUnsafeSourceEntry), true, nil
		}
		return nil, discoveryFailure(protocol.CodeSourceReadFailed), true, nil
	}
	file := os.NewFile(uintptr(fd), item.sourceID)
	if file == nil {
		_ = unix.Close(fd)
		return nil, protocol.Failure{}, false, errors.New("project linked: could not wrap source file")
	}
	var opened unix.Stat_t
	if err := unix.Fstat(fd, &opened); err != nil || !modeIs(uint32(opened.Mode), unix.S_IFREG) || identityOf(&opened) != item.identity {
		_ = file.Close()
		return nil, discoveryFailure(protocol.CodeUnsafeSourceEntry), true, nil
	}

	maximum := candidateReadMaximum(batchBytes)
	var document []byte
	var readErr error
	if dependencies.readCandidate != nil {
		document, readErr = dependencies.readCandidate(item.sourceID, file, maximum)
	} else {
		document, readErr = readBounded(file, maximum)
	}
	closeErr := file.Close()
	if dependencies.afterCandidateRead != nil {
		dependencies.afterCandidateRead(int(item.root.handle.Fd()), item.name)
	}
	var current unix.Stat_t
	postErr := unix.Fstatat(int(item.root.handle.Fd()), item.name, &current, unix.AT_SYMLINK_NOFOLLOW)
	if postErr != nil {
		return nil, discoveryFailure(protocol.CodeSourceReadFailed), true, nil
	}
	if !modeIs(uint32(current.Mode), unix.S_IFREG) || identityOf(&current) != item.identity {
		return nil, discoveryFailure(protocol.CodeUnsafeSourceEntry), true, nil
	}
	if closeErr != nil || readErr != nil {
		return nil, discoveryFailure(protocol.CodeSourceReadFailed), true, nil
	}
	if uint64(len(document)) > maximum {
		return nil, discoveryFailure(protocol.CodeSourceCatalogLimitExceeded), true, nil
	}
	return document, protocol.Failure{}, false, nil
}

func readBounded(reader io.Reader, maximum uint64) ([]byte, error) {
	limit := maximum + 1
	if limit > uint64(maxInt()) {
		limit = uint64(maxInt())
	}
	return io.ReadAll(io.LimitReader(reader, int64(limit)))
}

func maxInt() int { return int(^uint(0) >> 1) }

func identityOf(stat *unix.Stat_t) identity {
	return identity{device: uint64(stat.Dev), inode: uint64(stat.Ino), mode: uint32(stat.Mode)}
}

func modeIs(mode, kind uint32) bool { return mode&unix.S_IFMT == kind }

func isDefinitionCandidate(name string) bool {
	return bytes.HasSuffix([]byte(name), []byte(".godj.json"))
}

func sourceID(root, name string) string {
	if root == "." {
		return name
	}
	return root + "/" + name
}

func addDirectoryEntry(current int) (int, bool) {
	if current >= maxDirectoryEntries {
		return current, true
	}
	return current + 1, false
}

func sourceCountExceeded(actual int) bool {
	return actual > definition.MaxSources
}

func sourceIDBytesExceeded(identifier string) bool {
	return len([]byte(identifier)) > definition.MaxSourceIDBytes
}

func candidateReadMaximum(batchBytes uint64) uint64 {
	if batchBytes >= maxBatchBytes {
		return 0
	}
	remaining := uint64(maxBatchBytes) - batchBytes
	if remaining > maxDocumentBytes {
		return maxDocumentBytes
	}
	return remaining
}

func discoveryFailure(code string) protocol.Failure {
	return protocol.Failure{Category: protocol.CategoryDiscovery, Code: code}
}
