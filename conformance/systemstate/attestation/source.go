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
	maxSourceFiles = 4096
	maxSourceBytes = 128 << 20
)

var exactSourcePaths = map[string]struct{}{
	".github/workflows/ci.yml":                                  {},
	"Makefile":                                                  {},
	"admin/site_templates/delete.html":                          {},
	"admin/site_templates/form.html":                            {},
	"admin/site_templates/history.html":                         {},
	"admin/site_templates/index.html":                           {},
	"admin/site_templates/list.html":                            {},
	"admin/site_templates/login.html":                           {},
	"admin/site_templates/logout.html":                          {},
	"conformance/contracts/system-state-manifest.json":          {},
	"examples/article/testdata/postgres/0001_initial.godj.json": {},
	"examples/article/webapp/templates/article_list.html":       {},
	"go.mod": {},
	"go.sum": {},
	"systemstate/testdata/0001_initial.godj.json": {},
}

var productSourcePrefixes = []string{
	"admin/",
	"api/",
	"apps/",
	"auth/",
	"db/",
	"examples/article/",
	"forms/",
	"migrations/",
	"orm/",
	"query/",
	"schema/",
	"serializers/",
	"sessions/",
	"settings/",
	"systemstate/",
	"templates/",
	"validation/",
	"web/",
}

var embeddedAssetPrefixes = []string{
	"admin/site_templates/",
	"examples/article/webapp/templates/",
}

var conformanceSourcePrefixes = []string{
	"conformance/cmd/godjcheck/",
	"conformance/internal/protocol/",
	"conformance/runners/godj/",
	"conformance/systemstate/",
}

// ComputeSourceBinding hashes a fixed repository-relative behavioral source
// inventory. The inventory excludes documentation, reference oracles, fixtures,
// and checked attestations, so those files cannot create a self-reference.
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

	entries := make([]sourceEntry, 0, 128)
	var payloadBytes int64
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errors.New("walk behavioral source scope")
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
			return fmt.Errorf("behavioral source directory %q is a symbolic link", relative)
		}
		if !sourcePathOwned(relative) {
			return nil
		}
		if !utf8.ValidString(relative) || strings.ContainsAny(relative, "\x00\n") {
			return errors.New("behavioral source path is not frame-safe UTF-8")
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("behavioral source path %q is a symbolic link", relative)
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect behavioral source path %q", relative)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("behavioral source path %q is not a regular file", relative)
		}
		if info.Size() < 0 || info.Size() > maxSourceBytes-payloadBytes {
			return errors.New("behavioral source scope exceeds its byte limit")
		}
		contents, err := readSourceFile(path, info.Size())
		if err != nil {
			return fmt.Errorf("read behavioral source path %q: %w", relative, err)
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
			return errors.New("behavioral source scope exceeds its file limit")
		}
		return nil
	})
	if err != nil {
		return SourceBinding{}, err
	}
	if len(entries) == 0 {
		return SourceBinding{}, errors.New("behavioral source scope is empty")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	hasher := sha256.New()
	for _, entry := range entries {
		if _, err := io.WriteString(hasher, entry.frame()); err != nil {
			return SourceBinding{}, errors.New("hash behavioral source inventory")
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
	for _, prefix := range embeddedAssetPrefixes {
		if strings.HasPrefix(path, prefix) && filepath.Ext(path) == ".html" {
			return true
		}
	}
	if filepath.Ext(path) != ".go" {
		return false
	}
	for _, prefix := range productSourcePrefixes {
		if strings.HasPrefix(path, prefix) {
			return !strings.HasSuffix(path, "_test.go") && !pathComponentExcluded(path)
		}
	}
	for _, prefix := range conformanceSourcePrefixes {
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		if pathComponentExcluded(path) {
			return false
		}
		if !strings.HasSuffix(path, "_test.go") {
			return true
		}
		// The distinct-process restart sentinel is test-owned product code. Its
		// test files therefore belong to the live-attestation source binding.
		return strings.HasPrefix(path, "conformance/systemstate/restart/")
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
	for _, prefix := range productSourcePrefixes {
		if strings.HasPrefix(prefix, directory) {
			return true
		}
		if strings.HasPrefix(directory, prefix) && !pathComponentExcluded(directory+"hidden.go") {
			return true
		}
	}
	for _, prefix := range conformanceSourcePrefixes {
		if strings.HasPrefix(prefix, directory) {
			return true
		}
		if strings.HasPrefix(directory, prefix) && !pathComponentExcluded(directory+"hidden.go") {
			return true
		}
	}
	return false
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
		path == "conformance/systemstate/attestations"
}

func pathComponentExcluded(path string) bool {
	return strings.Contains(path, "/testdata/") ||
		strings.HasPrefix(path, "conformance/oracles/") ||
		strings.HasPrefix(path, "conformance/fixtures/") ||
		strings.HasPrefix(path, "conformance/systemstate/attestations/")
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
