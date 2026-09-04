//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"golang.org/x/sys/unix"
)

type fileIdentity struct {
	device uint64
	inode  uint64
	mode   uint32
}

func identityOf(stat *unix.Stat_t) fileIdentity {
	return fileIdentity{device: uint64(stat.Dev), inode: uint64(stat.Ino), mode: uint32(stat.Mode)}
}

func isMode(mode, kind uint32) bool {
	return mode&unix.S_IFMT == kind
}

type enumeratedEntry struct {
	name string
	mode os.FileMode
}

type discoveryHooks struct {
	beforeProjectRootOpen func(path string)
	afterRootInitialStat  func(parentFD int, name string)
	afterCandidateRead    func(rootFD int, name string)
	readCandidate         func(sourceID string, file *os.File, maximum uint64) ([]byte, error)
	enumerateRoot         func(root string, directory *os.File, yield func([]enumeratedEntry, error) bool) error
}

type retainedRoot struct {
	logical string
	handle  *os.File
}

type candidate struct {
	root     *retainedRoot
	name     string
	sourceID string
	identity fileIdentity
}

type discoveryResult struct {
	sources []definition.Source
	pathHex string
}

func discoverSources(projectRoot string, roots []string, metrics *oracleMetrics, lim limits, hooks discoveryHooks) (discoveryResult, *failure) {
	canonical, primary := canonicalRoots(roots, lim)
	if primary != nil {
		return discoveryResult{}, primary
	}
	physicalRoot, expectedRoot, err := filepathPhysicalDirectory(projectRoot)
	if err != nil {
		return discoveryResult{}, fail("migration_definition_discovery_error", "source_discovery_failed")
	}
	if hooks.beforeProjectRootOpen != nil {
		hooks.beforeProjectRootOpen(physicalRoot)
	}
	rootFD, err := unix.Open(physicalRoot, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return discoveryResult{}, fail("migration_definition_discovery_error", "source_discovery_failed")
	}
	projectHandle := os.NewFile(uintptr(rootFD), physicalRoot)
	if projectHandle == nil {
		_ = unix.Close(rootFD)
		return discoveryResult{}, fail("migration_project_internal_error", "project_internal_error")
	}
	defer projectHandle.Close()
	var openedRoot unix.Stat_t
	if unix.Fstat(rootFD, &openedRoot) != nil || !isMode(uint32(openedRoot.Mode), unix.S_IFDIR) || identityOf(&openedRoot) != expectedRoot || !verifyRetainedDirectory(physicalRoot, projectHandle, expectedRoot) {
		return discoveryResult{}, fail("migration_definition_discovery_error", "source_discovery_failed")
	}

	retained := make([]retainedRoot, 0, len(canonical))
	closeRetained := func() {
		for index := range retained {
			_ = retained[index].handle.Close()
		}
	}
	defer closeRetained()
	for _, logical := range canonical {
		handle, openFailure := openSemanticRoot(projectHandle, logical, hooks)
		if openFailure != nil {
			return discoveryResult{}, openFailure
		}
		retained = append(retained, retainedRoot{logical: logical, handle: handle})
	}
	metrics.RootsOpened += len(retained)

	entries := make([]struct {
		root *retainedRoot
		item enumeratedEntry
	}, 0)
	var entryCount uint64
	for index := range retained {
		root := &retained[index]
		var enumerationFailure *failure
		listErr := enumerateDirectory(root, hooks, func(listed []enumeratedEntry, chunkErr error) bool {
			if chunkErr != nil && !errors.Is(chunkErr, io.EOF) {
				enumerationFailure = fail("migration_definition_discovery_error", "source_discovery_failed")
				return false
			}
			for _, item := range listed {
				updated, exceeded := checkedAdd(entryCount, 1, lim.entries)
				entryCount = updated
				if exceeded {
					enumerationFailure = fail("migration_definition_discovery_error", "source_catalog_limit_exceeded")
					return false
				}
				metrics.DirectoryEntriesSeen++
				entries = append(entries, struct {
					root *retainedRoot
					item enumeratedEntry
				}{root: root, item: item})
			}
			return !errors.Is(chunkErr, io.EOF)
		})
		if listErr != nil {
			return discoveryResult{}, fail("migration_definition_discovery_error", "source_discovery_failed")
		}
		if enumerationFailure != nil {
			return discoveryResult{}, enumerationFailure
		}
	}
	sort.Slice(entries, func(left, right int) bool {
		leftID := definitionSourceID(entries[left].root.logical, entries[left].item.name)
		rightID := definitionSourceID(entries[right].root.logical, entries[right].item.name)
		return bytes.Compare([]byte(leftID), []byte(rightID)) < 0
	})

	candidates := make([]candidate, 0)
	for _, retainedEntry := range entries {
		name := retainedEntry.item.name
		if !isDefinitionCandidate([]byte(name)) {
			continue
		}
		sourceID := definitionSourceID(retainedEntry.root.logical, name)
		pathHex, pathFailure := preflightCandidateSourceID(sourceID, lim)
		if pathFailure != nil {
			return discoveryResult{pathHex: pathHex}, pathFailure
		}
		var stat unix.Stat_t
		if err := unix.Fstatat(int(retainedEntry.root.handle.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			if errors.Is(err, syscall.ENOENT) {
				return discoveryResult{}, fail("migration_definition_discovery_error", "source_read_failed")
			}
			return discoveryResult{}, fail("migration_definition_discovery_error", "source_read_failed")
		}
		if !isMode(uint32(stat.Mode), unix.S_IFREG) {
			return discoveryResult{}, fail("migration_definition_discovery_error", "unsafe_source_entry")
		}
		candidates = append(candidates, candidate{root: retainedEntry.root, name: name, sourceID: sourceID, identity: identityOf(&stat)})
	}
	sort.Slice(candidates, func(left, right int) bool {
		return bytes.Compare([]byte(candidates[left].sourceID), []byte(candidates[right].sourceID)) < 0
	})
	if primary := sourceCountFailure(len(candidates), lim); primary != nil {
		return discoveryResult{}, primary
	}

	sources := make([]definition.Source, 0, len(candidates))
	var batchBytes uint64
	for _, item := range candidates {
		document, readFailure := readStableCandidate(item, batchBytes, lim, hooks)
		if readFailure != nil {
			return discoveryResult{}, readFailure
		}
		batchBytes += uint64(len(document))
		metrics.SourceReads++
		sources = append(sources, definition.Source{SourceID: item.sourceID, Document: document})
	}
	return discoveryResult{sources: sources}, nil
}

func filepathPhysicalDirectory(candidate string) (string, fileIdentity, error) {
	absolute, err := os.Stat(candidate)
	if err != nil || !absolute.IsDir() {
		return "", fileIdentity{}, fmt.Errorf("not directory")
	}
	resolved, err := filepathEval(candidate)
	if err != nil {
		return "", fileIdentity{}, err
	}
	info, err := os.Lstat(resolved)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fileIdentity{}, fmt.Errorf("not physical directory")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fileIdentity{}, fmt.Errorf("directory identity unavailable")
	}
	return resolved, fileIdentity{device: uint64(stat.Dev), inode: uint64(stat.Ino), mode: uint32(stat.Mode)}, nil
}

var filepathEval = filepath.EvalSymlinks

func canonicalRoots(roots []string, lim limits) ([]string, *failure) {
	if len(roots) > lim.roots {
		return nil, fail("migration_definition_discovery_error", "invalid_project_source_config")
	}
	canonical := append([]string(nil), roots...)
	for _, root := range canonical {
		if !utf8.ValidString(root) || root == "" || strings.ContainsAny(root, "\\\x00") || path.IsAbs(root) || path.Clean(root) != root {
			return nil, fail("migration_definition_discovery_error", "invalid_project_source_config")
		}
		for _, component := range strings.Split(root, "/") {
			if component == "" || component == ".." {
				return nil, fail("migration_definition_discovery_error", "invalid_project_source_config")
			}
		}
	}
	sort.Slice(canonical, func(left, right int) bool {
		return bytes.Compare([]byte(canonical[left]), []byte(canonical[right])) < 0
	})
	for index := 1; index < len(canonical); index++ {
		if canonical[index-1] == canonical[index] {
			return nil, fail("migration_definition_discovery_error", "invalid_project_source_config")
		}
	}
	return canonical, nil
}

func duplicateDirectory(handle *os.File) (*os.File, error) {
	fd, err := unix.Openat(int(handle.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	duplicated := os.NewFile(uintptr(fd), handle.Name())
	if duplicated == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("could not wrap directory")
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
		entries, readErr := reader.ReadDir(128)
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

func openSemanticRoot(project *os.File, logical string, hooks discoveryHooks) (*os.File, *failure) {
	current, err := duplicateDirectory(project)
	if err != nil {
		return nil, fail("migration_definition_discovery_error", "source_discovery_failed")
	}
	if logical == "." {
		return current, nil
	}
	for _, component := range strings.Split(logical, "/") {
		exists, err := directoryContainsRawName(current, component)
		if err != nil {
			current.Close()
			return nil, fail("migration_definition_discovery_error", "source_discovery_failed")
		}
		if !exists {
			current.Close()
			return nil, fail("migration_definition_discovery_error", "invalid_source_root")
		}
		var initial unix.Stat_t
		if err := unix.Fstatat(int(current.Fd()), component, &initial, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			current.Close()
			return nil, fail("migration_definition_discovery_error", "source_discovery_failed")
		}
		if !isMode(uint32(initial.Mode), unix.S_IFDIR) {
			current.Close()
			return nil, fail("migration_definition_discovery_error", "invalid_source_root")
		}
		if hooks.afterRootInitialStat != nil {
			hooks.afterRootInitialStat(int(current.Fd()), component)
		}
		childFD, openErr := unix.Openat(int(current.Fd()), component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if openErr != nil {
			current.Close()
			return nil, fail("migration_definition_discovery_error", "source_discovery_failed")
		}
		child := os.NewFile(uintptr(childFD), component)
		if child == nil {
			_ = unix.Close(childFD)
			current.Close()
			return nil, fail("migration_project_internal_error", "project_internal_error")
		}
		var opened unix.Stat_t
		statErr := unix.Fstat(childFD, &opened)
		current.Close()
		if statErr != nil || !isMode(uint32(opened.Mode), unix.S_IFDIR) || identityOf(&opened) != identityOf(&initial) {
			child.Close()
			return nil, fail("migration_definition_discovery_error", "source_discovery_failed")
		}
		current = child
	}
	return current, nil
}

func enumerateDirectory(root *retainedRoot, hooks discoveryHooks, yield func([]enumeratedEntry, error) bool) error {
	reader, err := duplicateDirectory(root.handle)
	if err != nil {
		return err
	}
	defer reader.Close()
	if hooks.enumerateRoot != nil {
		return hooks.enumerateRoot(root.logical, reader, yield)
	}
	for {
		entries, readErr := reader.ReadDir(128)
		chunk := make([]enumeratedEntry, 0, len(entries))
		for _, entry := range entries {
			chunk = append(chunk, enumeratedEntry{name: entry.Name(), mode: entry.Type()})
		}
		if !yield(chunk, readErr) || errors.Is(readErr, io.EOF) {
			return nil
		}
	}
}

func isDefinitionCandidate(name []byte) bool {
	suffix := []byte(".godj.json")
	return len(name) >= len(suffix) && bytes.HasSuffix(name, suffix)
}

func definitionSourceID(root, name string) string {
	if root == "." {
		return name
	}
	return root + "/" + name
}

func preflightCandidateSourceID(sourceID string, lim limits) (string, *failure) {
	pathHex := hex.EncodeToString([]byte(sourceID))
	if len([]byte(sourceID)) > lim.sourceIDBytes {
		return pathHex, fail("migration_definition_discovery_error", "source_catalog_limit_exceeded")
	}
	if !utf8.ValidString(sourceID) {
		return pathHex, fail("migration_definition_discovery_error", "invalid_source_entry")
	}
	return "", nil
}

func sourceCountFailure(actual int, lim limits) *failure {
	if actual > lim.sources {
		return fail("migration_definition_discovery_error", "source_catalog_limit_exceeded")
	}
	return nil
}

func readStableCandidate(item candidate, batchBytes uint64, lim limits, hooks discoveryHooks) ([]byte, *failure) {
	fd, err := unix.Openat(int(item.root.handle.Fd()), item.name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, fail("migration_definition_discovery_error", "unsafe_source_entry")
		}
		return nil, fail("migration_definition_discovery_error", "source_read_failed")
	}
	file := os.NewFile(uintptr(fd), item.sourceID)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fail("migration_project_internal_error", "project_internal_error")
	}
	var opened unix.Stat_t
	if statErr := unix.Fstat(fd, &opened); statErr != nil || !isMode(uint32(opened.Mode), unix.S_IFREG) || identityOf(&opened) != item.identity {
		file.Close()
		return nil, fail("migration_definition_discovery_error", "unsafe_source_entry")
	}
	remaining := uint64(0)
	if batchBytes <= lim.batchBytes {
		remaining = lim.batchBytes - batchBytes
	}
	maximum := lim.documentBytes
	if remaining < maximum {
		maximum = remaining
	}
	var document []byte
	var readErr error
	if hooks.readCandidate != nil {
		document, readErr = hooks.readCandidate(item.sourceID, file, maximum)
	} else {
		document, readErr = readBounded(file, maximum)
	}
	closeErr := file.Close()
	if hooks.afterCandidateRead != nil {
		hooks.afterCandidateRead(int(item.root.handle.Fd()), item.name)
	}
	var current unix.Stat_t
	postErr := unix.Fstatat(int(item.root.handle.Fd()), item.name, &current, unix.AT_SYMLINK_NOFOLLOW)
	if postErr != nil {
		return nil, fail("migration_definition_discovery_error", "source_read_failed")
	}
	if !isMode(uint32(current.Mode), unix.S_IFREG) || identityOf(&current) != item.identity {
		return nil, fail("migration_definition_discovery_error", "unsafe_source_entry")
	}
	if closeErr != nil || readErr != nil {
		return nil, fail("migration_definition_discovery_error", "source_read_failed")
	}
	if uint64(len(document)) > maximum {
		return nil, fail("migration_definition_discovery_error", "source_catalog_limit_exceeded")
	}
	return document, nil
}

func readBounded(reader io.Reader, maximum uint64) ([]byte, error) {
	limit := maximum
	if limit < ^uint64(0) {
		limit++
	}
	if limit > uint64(maxInt()) {
		limit = uint64(maxInt())
	}
	return io.ReadAll(io.LimitReader(reader, int64(limit)))
}

func maxInt() int {
	return int(^uint(0) >> 1)
}

type linkedInvocation struct {
	ProjectRoot string
	Roots       []string
	Request     []byte
	Limits      limits
	Hooks       discoveryHooks
	Metrics     *oracleMetrics
}

type linkedObservation struct {
	Wire          []byte
	FailureDetail *failureContext
	SetPublished  bool
}

func invokeLinked(input linkedInvocation) linkedObservation {
	metrics := input.Metrics
	if metrics == nil {
		metrics = &oracleMetrics{}
	}
	respondFailure := func(primary *failure, detail *failureContext) linkedObservation {
		metrics.RunnerResponseWrites++
		return linkedObservation{Wire: encodeRunnerFailure(primary), FailureDetail: detail}
	}
	if primary := parseRunnerRequest(input.Request, input.Limits); primary != nil {
		return respondFailure(primary, nil)
	}
	metrics.CommandDispatches++
	discovered, primary := discoverSources(input.ProjectRoot, input.Roots, metrics, input.Limits, input.Hooks)
	if primary != nil {
		return respondFailure(primary, nil)
	}
	metrics.LoadCalls++
	loaded, report, loadErr := definition.Load(discovered.sources...)
	metrics.DocumentsReceived = report.DocumentsReceived
	metrics.HeadersValidated = report.HeadersValidated
	metrics.OperationsDecoded = report.OperationsDecoded
	metrics.PlannerConstruction = report.PlannerConstruction
	metrics.DefinitionsPublished = report.DefinitionsPublished
	metrics.DefinitionSetsPublished = report.DefinitionSetsPublished
	if loadErr != nil {
		primary := classifyLoadError(loadErr)
		detail := loadFailureContext(report)
		return respondFailure(primary, detail)
	}
	result := checkResult{
		SourceCount:         len(discovered.sources),
		DefinitionCount:     len(loaded.Definitions()),
		DefinitionSetDigest: loaded.Digest(),
	}
	metrics.RunnerResponseWrites++
	return linkedObservation{Wire: encodeRunnerSuccess(result), SetPublished: true}
}

func classifyLoadError(loadErr error) *failure {
	var sourceError *definition.Error
	if errors.As(loadErr, &sourceError) && sourceError != nil {
		return fail(sourceError.Category, string(sourceError.Code))
	}
	var planningError *migrations.PlanningError
	if errors.As(loadErr, &planningError) && planningError != nil {
		return fail(string(planningError.Category), string(planningError.Code))
	}
	return fail("migration_project_internal_error", "project_internal_error")
}

func loadFailureContext(report definition.LoadReport) *failureContext {
	context, exists := report.Failure()
	if !exists {
		return nil
	}
	graphSources := context.GraphSources()
	formatted := make([]string, len(graphSources))
	for index, source := range graphSources {
		formatted[index] = source.Migration.App + "/" + source.Migration.Name + "@" + source.SourceID
	}
	return &failureContext{
		Stage:          context.Stage,
		SourceID:       context.SourceID,
		JSONPointer:    context.JSONPointer,
		App:            context.App,
		Name:           context.Name,
		OperationIndex: context.OperationIndex,
		Reason:         context.Reason,
		Limit:          context.Limit,
		Maximum:        context.Maximum,
		Actual:         context.Actual,
		GraphSources:   formatted,
	}
}
