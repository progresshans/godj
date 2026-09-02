package attestation

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	maxSourceFiles = 8192
	maxSourceBytes = 256 << 20
)

var exactSourcePaths = map[string]struct{}{
	".github/workflows/ci.yml": {},
	"Makefile":                 {},
	"conformance/contracts/system-state-manifest.json":                         {},
	"conformance/runners/godj/gdj0045_system_state_scenarios.go":               {},
	"conformance/runners/godj/gdj0046_system_state_multi_runtime_scenarios.go": {},
	"conformance/runners/godj/gdj0046_system_state_two_process_execution.go":   {},
	"conformance/runners/godj/inputs.go":                                       {},
	"conformance/runners/godj/runner.go":                                       {},
	"examples/article/godj.toml":                                               {},
	"go.mod":                                                                   {},
	"go.sum":                                                                   {},
}

var productSourcePrefixes = []string{
	"admin/",
	"api/",
	"apps/",
	"auth/",
	"codegen/",
	"db/",
	"forms/",
	"migrations/",
	"orm/",
	"project/",
	"query/",
	"schema/",
	"serializers/",
	"sessions/",
	"settings/",
	"systemstate/",
	"templates/",
	"validation/",
	"web/",
	"examples/article/",
}

var commandAndInternalSourcePrefixes = []string{
	"cmd/godj/",
	"internal/migrationautodetect/",
	"internal/projectcheck/",
	"internal/projectgenerate/",
	"internal/projectmigration/",
	"internal/projectspec/",
}

var conformanceConsumerSourcePrefixes = []string{
	"conformance/cmd/godjcheck/",
	"conformance/internal/protocol/",
}

var embeddedAssetPrefixes = []string{
	"admin/site_templates/",
	"examples/article/webapp/templates/",
}

var migrationDataPrefixes = []string{
	"examples/article/migrations/",
	"examples/article/testdata/postgres/",
	"systemstate/testdata/",
}

const harnessSourcePrefix = "conformance/projectoperatorproduct/"
const checkedAttestationPrefix = "conformance/projectoperatorproduct/attestations/"

// ComputeSourceBinding hashes the fixed repository-relative source inventory
// that can affect the SYS-029 external product observation. This includes the
// actual product test harness (including its _test.go files), global godj and
// projectcheck behavior, project/systemstate and Article behavior, their public
// runtime dependencies, the GDJ-0055 consumer/loader/protocol/manifest path,
// dependency locks, Makefile, and hosted workflow. The checked evidence
// directory is excluded to avoid a digest self-reference.
func ComputeSourceBinding(repositoryRoot string) (SourceBinding, error) {
	root, err := filepath.Abs(repositoryRoot)
	if err != nil {
		return SourceBinding{}, errors.New("resolve repository root")
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		return SourceBinding{}, errors.New("inspect repository root")
	}
	if !rootInfo.IsDir() {
		return SourceBinding{}, errors.New("repository root is not a directory")
	}

	entries := make([]sourceEntry, 0, 256)
	var payloadBytes int64
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errors.New("walk external operator behavioral source scope")
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return errors.New("derive repository-relative source path")
		}
		relative = filepath.ToSlash(relative)
		if relative == "." {
			return nil
		}
		if entry.IsDir() {
			if sourceDirectoryExcluded(relative) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 && sourceSymlinkMayHideOwnedSource(relative) {
			return fmt.Errorf("external operator behavioral source directory %q is a symbolic link", relative)
		}
		if !sourcePathOwned(relative) {
			return nil
		}
		if !utf8.ValidString(relative) || strings.ContainsAny(relative, "\x00\n") {
			return errors.New("external operator behavioral source path is not frame-safe UTF-8")
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("external operator behavioral source path %q is a symbolic link", relative)
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect external operator behavioral source path %q", relative)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("external operator behavioral source path %q is not a regular file", relative)
		}
		if info.Size() < 0 || info.Size() > maxSourceBytes-payloadBytes {
			return errors.New("external operator behavioral source scope exceeds its byte limit")
		}
		contents, err := readSourceFile(path, info.Size())
		if err != nil {
			return fmt.Errorf("read external operator behavioral source path %q: %w", relative, err)
		}
		digest := sha256.Sum256(contents)
		entries = append(entries, sourceEntry{
			path:   relative,
			mode:   normalizedGitMode(info.Mode()),
			size:   info.Size(),
			sha256: hex.EncodeToString(digest[:]),
		})
		payloadBytes += info.Size()
		if len(entries) > maxSourceFiles {
			return errors.New("external operator behavioral source scope exceeds its file limit")
		}
		return nil
	})
	if err != nil {
		return SourceBinding{}, err
	}
	if len(entries) == 0 {
		return SourceBinding{}, errors.New("external operator behavioral source scope is empty")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	hasher := sha256.New()
	for _, entry := range entries {
		if _, err := io.WriteString(hasher, entry.frame()); err != nil {
			return SourceBinding{}, errors.New("hash external operator behavioral source inventory")
		}
	}
	return newSourceBinding(int64(len(entries)), payloadBytes, hex.EncodeToString(hasher.Sum(nil)))
}

type sourceEntry struct {
	path   string
	mode   string
	size   int64
	sha256 string
}

func (entry sourceEntry) frame() string {
	return entry.path + "\x00" + entry.mode + "\x00" + strconv.FormatInt(entry.size, 10) + "\x00" + entry.sha256 + "\n"
}

func sourcePathOwned(path string) bool {
	if _, exact := exactSourcePaths[path]; exact {
		return true
	}
	if strings.HasPrefix(path, checkedAttestationPrefix) {
		return false
	}
	for _, prefix := range embeddedAssetPrefixes {
		if strings.HasPrefix(path, prefix) && filepath.Ext(path) == ".html" {
			return true
		}
	}
	for _, prefix := range migrationDataPrefixes {
		if strings.HasPrefix(path, prefix) && strings.HasSuffix(path, ".godj.json") {
			return true
		}
	}
	if filepath.Ext(path) != ".go" {
		return false
	}
	if strings.HasPrefix(path, harnessSourcePrefix) {
		// Unlike ordinary product tests, this package is the executable
		// product sentinel itself. Its root tests and production attestation
		// codec are behavioral source. Attestation unit tests are excluded so
		// a post-capture checked-size lock cannot create a digest self-cycle.
		if strings.HasPrefix(path, harnessSourcePrefix+"attestation/") && strings.HasSuffix(path, "_test.go") {
			return false
		}
		return !pathComponentExcluded(path)
	}
	for _, prefix := range conformanceConsumerSourcePrefixes {
		if strings.HasPrefix(path, prefix) {
			return !strings.HasSuffix(path, "_test.go") && !pathComponentExcluded(path)
		}
	}
	if strings.HasPrefix(path, "conformance/runners/godj/") {
		return strings.HasPrefix(filepath.Base(path), "gdj0055_") && !pathComponentExcluded(path)
	}
	for _, prefix := range productSourcePrefixes {
		if strings.HasPrefix(path, prefix) {
			return !strings.HasSuffix(path, "_test.go") && !pathComponentExcluded(path)
		}
	}
	for _, prefix := range commandAndInternalSourcePrefixes {
		if strings.HasPrefix(path, prefix) {
			return !strings.HasSuffix(path, "_test.go") && !pathComponentExcluded(path)
		}
	}
	return false
}

func sourceSymlinkMayHideOwnedSource(path string) bool {
	if sourceDirectoryExcluded(path) {
		return false
	}
	directory := strings.TrimSuffix(path, "/") + "/"
	for exact := range exactSourcePaths {
		if strings.HasPrefix(exact, directory) {
			return true
		}
	}
	for _, prefix := range embeddedAssetPrefixes {
		if sourceDirectoryRelated(directory, prefix) {
			return true
		}
	}
	for _, prefix := range migrationDataPrefixes {
		if sourceDirectoryRelated(directory, prefix) {
			return true
		}
	}
	for _, prefix := range productSourcePrefixes {
		if sourceDirectoryRelated(directory, prefix) && !pathComponentExcluded(directory+"hidden.go") {
			return true
		}
	}
	for _, prefix := range commandAndInternalSourcePrefixes {
		if sourceDirectoryRelated(directory, prefix) && !pathComponentExcluded(directory+"hidden.go") {
			return true
		}
	}
	for _, prefix := range conformanceConsumerSourcePrefixes {
		if sourceDirectoryRelated(directory, prefix) && !pathComponentExcluded(directory+"hidden.go") {
			return true
		}
	}
	if sourceDirectoryRelated(directory, "conformance/runners/godj/") && !pathComponentExcluded(directory+"gdj0055_hidden.go") {
		return true
	}
	return sourceDirectoryRelated(directory, harnessSourcePrefix) && !pathComponentExcluded(directory+"hidden.go")
}

func sourceDirectoryRelated(directory, prefix string) bool {
	return strings.HasPrefix(directory, prefix) || strings.HasPrefix(prefix, directory)
}

func sourceDirectoryExcluded(path string) bool {
	if path == ".git" || path == "docs" || path == "work" || path == "vendor" || strings.HasSuffix(path, "/__pycache__") {
		return true
	}
	return path == "conformance/oracles" ||
		path == "conformance/fixtures" ||
		path == strings.TrimSuffix(checkedAttestationPrefix, "/")
}

func pathComponentExcluded(path string) bool {
	return strings.Contains(path, "/testdata/") ||
		strings.HasPrefix(path, "conformance/oracles/") ||
		strings.HasPrefix(path, "conformance/fixtures/") ||
		strings.HasPrefix(path, checkedAttestationPrefix)
}

func normalizedGitMode(mode fs.FileMode) string {
	if mode.Perm()&0o111 != 0 {
		return "100755"
	}
	return "100644"
}

func readSourceFile(path string, expectedSize int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, expectedSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(contents)) != expectedSize {
		return nil, errors.New("source file changed while it was being read")
	}
	return contents, nil
}
