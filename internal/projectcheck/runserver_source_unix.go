//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

// A source plan is detached from its build workspace. Each observation reopens
// and verifies its bound directories, includes newly added compiler inputs and
// embed matches, and hashes bytes rather than trusting a timestamp-only watch.
// Immutable module versions and the selected toolchain retain Go's ownership;
// local replacements, including a development framework, are watched inputs.
type runserverSource struct {
	root     string
	identity unix.Stat_t
	packages []runserverSourcePackage
	static   []string
	graph    []byte
	digest   [sha256.Size]byte
	complete bool
}

type runserverSourcePackage struct {
	directory     string
	embedPatterns []string
	embedFiles    []string
}

var errRunserverSource = errors.New("projectcheck: development source is unavailable or changed")

func captureRunserverSource(input RunServerInvocation, command *projectCommand[RunServerFailure], report *RunServerReport, beforeBundleCheck bool) (*runserverSource, *RunServerFailure) {
	selected, workspace := command.selected, command.workspace
	if !verifyRetainedProject(selected) {
		return nil, runserverSourceFailure(RunServerCodeSourceChanged)
	}
	environment := workspace.buildEnvironment
	if environment == nil {
		environment = workspace.environment
	}
	invocation := Command{Dir: selected.rootPath, Env: environment,
		Argv: []string{"go", "list", "-deps", "-e", "-json", "-mod=readonly", selected.descriptor.packagePath, selected.descriptor.runserverPackagePath}}
	report.SourceInventoryCalls++
	process := input.Backend.Execute(input.Context, input.Interrupt, RunserverInventoryStage, cloneCommand(invocation))
	recordProcess(&report.Report, RunserverInventoryStage, process)
	primary := runserverShortProcessFailure(process, RunServerCategoryBuild, RunServerCodeSourceInventoryFailed)
	primary = combineRunserverCleanup(runserverBarrier(input, primary), process.CleanupFailed)
	defer clear(process.Stdout)
	if primary != nil {
		return nil, primary
	}
	if process.StdoutScalar.Truncated || len(process.Stdout) > int(defaultMakemigrationsBuildInputLimits().goListBytes) {
		return nil, runserverSourceFailure(RunServerCodeSourceInventoryFailed)
	}
	source, err := planRunserverSource(selected, process.Stdout)
	if err == nil {
		source.digest, err = source.observePreparation(input.Context, beforeBundleCheck)
	}
	if err != nil {
		return source, runserverSourceFailure(RunServerCodeSourceInventoryFailed)
	}
	return source, nil
}

func planRunserverSource(selected retainedProject, document []byte) (*runserverSource, error) {
	limits := defaultMakemigrationsBuildInputLimits()
	if len(document) == 0 || len(document) > int(limits.goListBytes) {
		return nil, errRunserverSource
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	var packages []makemigrationsGoListPackage
	for {
		var listed makemigrationsGoListPackage
		if err := decoder.Decode(&listed); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, errRunserverSource
		}
		if len(packages) == limits.packageCount {
			return nil, errRunserverSource
		}
		packages = append(packages, listed)
	}
	if len(packages) == 0 {
		return nil, errRunserverSource
	}
	graph, err := canonicalMakemigrationsDependencyGraph(packages)
	if err != nil {
		return nil, errRunserverSource
	}
	source := &runserverSource{root: selected.rootPath, identity: selected.rootIdentity, graph: graph, complete: true}
	static := map[string]bool{
		filepath.Join(selected.rootPath, descriptorName):                     true,
		filepath.Join(selected.rootPath, ".godj", "generated-manifest.json"): true,
	}
	// Module roots also let a currently missing local package acquire files
	// after a failed build without requiring a change to the importing file.
	modules := map[string]string{}
	for _, listed := range packages {
		module := listed.Module
		if module == nil {
			continue
		}
		effective := module
		if module.Replace != nil {
			effective = module.Replace
		}
		if effective.Dir == "" || (!module.Main && effective.Version != "") {
			continue
		}
		if !validMakemigrationsPhysicalRoot(effective.Dir) {
			return nil, errRunserverSource
		}
		modules[module.Path] = effective.Dir
		static[filepath.Join(effective.Dir, "go.mod")] = true
		static[filepath.Join(effective.Dir, "go.sum")] = true
	}
	seen := map[string]bool{}
	declarationSeen, runtimeSeen := false, false
	for _, listed := range packages {
		if listed.Incomplete || len(listed.Error) != 0 && !bytes.Equal(bytes.TrimSpace(listed.Error), []byte("null")) {
			source.complete = false
		}
		if listed.Standard || listed.Goroot {
			continue
		}
		modulePrefix, moduleDirectory := "", ""
		for name, directory := range modules {
			if (listed.ImportPath == name || strings.HasPrefix(listed.ImportPath, name+"/")) && len(name) > len(modulePrefix) {
				modulePrefix, moduleDirectory = name, directory
			}
		}
		if moduleDirectory == "" {
			if makemigrationsExternalModuleIsContentAddressed(listed.Module) {
				continue
			}
			return nil, errRunserverSource
		}
		directory := listed.Dir
		if directory == "" {
			directory = filepath.Join(moduleDirectory, filepath.FromSlash(strings.TrimPrefix(strings.TrimPrefix(listed.ImportPath, modulePrefix), "/")))
		}
		if !validMakemigrationsPhysicalRoot(directory) || !sameOrDescendant(directory, moduleDirectory) || seen[directory] {
			return nil, errRunserverSource
		}
		seen[directory] = true
		if directory == filepath.Join(selected.rootPath, filepath.FromSlash(selected.descriptor.packagePath)) {
			declarationSeen = true
			source.complete = source.complete && listed.Name == "main"
		}
		if directory == filepath.Join(selected.rootPath, filepath.FromSlash(selected.descriptor.runserverPackagePath)) {
			runtimeSeen = true
			source.complete = source.complete && listed.Name == "main"
		}
		for _, pattern := range listed.EmbedPatterns {
			if !validMakemigrationsEmbedPattern(pattern, limits) {
				return nil, errRunserverSource
			}
		}
		for _, file := range listed.EmbedFiles {
			if !validMakemigrationsRelativePath(filepath.ToSlash(file), false, limits) {
				return nil, errRunserverSource
			}
		}
		source.packages = append(source.packages, runserverSourcePackage{directory: directory, embedPatterns: listed.EmbedPatterns, embedFiles: listed.EmbedFiles})
	}
	if len(source.packages) == 0 || !declarationSeen || !runtimeSeen {
		return nil, errRunserverSource
	}
	for name := range static {
		source.static = append(source.static, name)
	}
	sort.Strings(source.static)
	sort.Slice(source.packages, func(i, j int) bool { return source.packages[i].directory < source.packages[j].directory })
	return source, nil
}

func (source *runserverSource) observe(ctx context.Context) ([sha256.Size]byte, error) {
	return source.observePreparation(ctx, false)
}

// The first snapshot records a pending publication so the existing bundle
// preflight can diagnose it. Only that preflight may accept a clean bundle;
// later snapshots/publication reject any journal. Removing a journal changes
// the digest, so the earlier pending snapshot cannot authorize a new runtime.
func (source *runserverSource) observePreparation(ctx context.Context, beforeBundleCheck bool) ([sha256.Size]byte, error) {
	var zero [sha256.Size]byte
	if source == nil || ctx == nil || ctx.Err() != nil {
		return zero, errRunserverSource
	}
	root, identity, err := openRetainedDirectory(source.root)
	if err != nil {
		return zero, errRunserverSource
	}
	defer root.Close()
	if !sameIdentity(identity, source.identity) {
		return zero, errRunserverSource
	}
	// A publication journal can appear without changing a source file yet.
	// It is a source barrier in its own right, including after candidate startup.
	transactions, transactionErr := openMakemigrationsRetainedRelative(root, ".godj/transactions", true, defaultMakemigrationsBuildInputLimits())
	pendingPublication := false
	if transactionErr == nil {
		entries, readErr := transactions.ReadDir(1)
		closeErr := transactions.Close()
		pendingPublication = len(entries) != 0
		if pendingPublication && !beforeBundleCheck || readErr != nil && !errors.Is(readErr, io.EOF) || closeErr != nil {
			return zero, errRunserverSource
		}
	} else if !errors.Is(transactionErr, errMakemigrationsBuildInputMissing) {
		return zero, errRunserverSource
	}
	files := map[string]bool{}
	entriesLeft := defaultMakemigrationsBuildInputLimits().memberCount
	for _, name := range source.static {
		files[name] = true
	}
	for _, pkg := range source.packages {
		if err := pkg.collect(ctx, files, &entriesLeft); err != nil {
			return zero, errRunserverSource
		}
	}
	if len(files) > defaultMakemigrationsBuildInputLimits().memberCount {
		return zero, errRunserverSource
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	hash := sha256.New()
	_, _ = hash.Write([]byte("godj/development-source/v1\x00"))
	frame := func(value []byte) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write(value)
	}
	frame(source.graph)
	if pendingPublication {
		frame([]byte("pending-publication"))
	} else {
		frame(nil)
	}
	filesystem, _, err := openRetainedDirectory(string(filepath.Separator))
	if err != nil {
		return zero, errRunserverSource
	}
	defer filesystem.Close()
	parents := map[string]*os.File{}
	identities := map[string]unix.Stat_t{}
	defer func() {
		for _, directory := range parents {
			if directory != nil {
				_ = directory.Close()
			}
		}
	}()
	var total uint64
	for _, name := range names {
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}
		frame([]byte(name))
		parentName := filepath.Dir(name)
		directory, known := parents[parentName]
		if !known {
			directory, err = openMakemigrationsRetainedRelative(filesystem, filepath.ToSlash(strings.TrimPrefix(parentName, string(filepath.Separator))), true, defaultMakemigrationsBuildInputLimits())
			if err != nil && !errors.Is(err, errMakemigrationsBuildInputMissing) {
				return zero, errRunserverSource
			}
			parents[parentName] = directory
			if directory != nil {
				var identity unix.Stat_t
				if unix.Fstat(int(directory.Fd()), &identity) != nil {
					return zero, errRunserverSource
				}
				identities[parentName] = identity
			}
		}
		if directory == nil {
			frame(nil)
			frame(nil)
			continue
		}
		contents, mode, present, err := readRunserverSourceFile(directory, filepath.Base(name), &total)
		if err != nil {
			return zero, errRunserverSource
		}
		if !present {
			frame(nil)
			frame(nil)
			continue
		}
		frame([]byte(fmt.Sprint(mode)))
		frame(contents)
	}
	for name, directory := range parents {
		if directory != nil && !verifyRetainedDirectory(name, directory, identities[name]) {
			return zero, errRunserverSource
		}
	}
	if !verifyRetainedDirectory(source.root, root, source.identity) {
		return zero, errRunserverSource
	}
	copy(zero[:], hash.Sum(nil))
	return zero, nil
}

func compilerSourceName(name string) bool {
	if strings.HasSuffix(name, "_test.go") {
		return false
	}
	switch filepath.Ext(name) {
	case ".go", ".c", ".cc", ".cpp", ".cxx", ".h", ".m", ".f", ".F", ".for", ".f90", ".s", ".S", ".swig", ".swigcxx", ".syso":
		return true
	}
	return false
}

func (pkg runserverSourcePackage) collect(ctx context.Context, files map[string]bool, entriesLeft *int) error {
	directory, identity, err := openRetainedDirectory(pkg.directory)
	if errors.Is(err, os.ErrNotExist) {
		// Keep a stable absent marker; its future compiler files change the hash.
		files[filepath.Join(pkg.directory, ".godj-absent-package")] = true
		return nil
	}
	if err != nil {
		return err
	}
	defer directory.Close()
	entries, err := directory.ReadDir(*entriesLeft + 1)
	if err != nil && !errors.Is(err, io.EOF) || len(entries) > *entriesLeft {
		return errRunserverSource
	}
	*entriesLeft -= len(entries)
	for _, entry := range entries {
		if compilerSourceName(entry.Name()) {
			files[filepath.Join(pkg.directory, entry.Name())] = true
		}
	}
	for _, file := range pkg.embedFiles {
		files[filepath.Join(pkg.directory, filepath.FromSlash(file))] = true
	}
	for _, pattern := range pkg.embedPatterns {
		if err := collectRunserverEmbed(ctx, directory, pkg.directory, ".", pattern, false, files, 0, entriesLeft); err != nil {
			return err
		}
	}
	if !verifyRetainedDirectory(pkg.directory, directory, identity) {
		return errRunserverSource
	}
	return nil
}

func collectRunserverEmbed(ctx context.Context, root *os.File, base, relative, pattern string, inherited bool, files map[string]bool, depth int, entriesLeft *int) error {
	limits := defaultMakemigrationsBuildInputLimits()
	if ctx.Err() != nil || depth > limits.pathDepth || len(files) > limits.memberCount {
		return errRunserverSource
	}
	all := strings.HasPrefix(pattern, "all:")
	pattern = strings.TrimPrefix(pattern, "all:")
	directory, err := openMakemigrationsRetainedRelative(root, relative, true, limits)
	if errors.Is(err, errMakemigrationsBuildInputMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	defer directory.Close()
	entries, err := directory.ReadDir(*entriesLeft + 1)
	if err != nil && !errors.Is(err, io.EOF) || len(entries) > *entriesLeft {
		return errRunserverSource
	}
	*entriesLeft -= len(entries)
	for _, entry := range entries {
		name := entry.Name()
		if name == ".git" || name == ".hg" || name == ".svn" || !all && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
			continue
		}
		item := path.Join(relative, name)
		matches, _ := path.Match(pattern, item)
		matches = matches || inherited
		if entry.IsDir() {
			if matches || runserverEmbedPrefix(pattern, item) {
				next := pattern
				if all {
					next = "all:" + next
				}
				if err := collectRunserverEmbed(ctx, root, base, item, next, matches, files, depth+1, entriesLeft); err != nil {
					return err
				}
			}
		} else if matches || entry.Type()&os.ModeSymlink != 0 && runserverEmbedPrefix(pattern, item) {
			files[filepath.Join(base, filepath.FromSlash(item))] = true
		}
	}
	return nil
}

func runserverEmbedPrefix(pattern, directory string) bool {
	wanted, actual := strings.Split(pattern, "/"), strings.Split(directory, "/")
	if len(actual) >= len(wanted) {
		return false
	}
	for index, part := range actual {
		if matches, _ := path.Match(wanted[index], part); !matches {
			return false
		}
	}
	return true
}

func readRunserverSourceFile(parent *os.File, name string, total *uint64) ([]byte, os.FileMode, bool, error) {
	limits := defaultMakemigrationsBuildInputLimits()
	// Resolve each path component without following a symlink. The application
	// tree and mutable dependency roots must remain the actual build inputs.
	file, err := openMakemigrationsRetainedRelative(parent, name, false, limits)
	if errors.Is(err, errMakemigrationsBuildInputMissing) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() < 0 || uint64(before.Size()) > limits.fileBytes {
		return nil, 0, false, errRunserverSource
	}
	contents, err := io.ReadAll(io.LimitReader(file, int64(limits.fileBytes)+1))
	after, statErr := file.Stat()
	if err != nil || statErr != nil || len(contents) != int(before.Size()) || !sameMakemigrationsFileVersion(before, after) || uint64(len(contents)) > limits.documentBytes-*total {
		return nil, 0, false, errRunserverSource
	}
	*total += uint64(len(contents))
	return contents, before.Mode().Perm(), true, nil
}

func runserverSourceFailure(code string) *RunServerFailure {
	return &RunServerFailure{Category: RunServerCategoryBuild, Code: code}
}
