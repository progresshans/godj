package attestationio

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

// SourcePolicy remains with the attestation owner. These predicates decide
// which files affect that observation and where a symlink could hide them.
type SourcePolicy struct {
	Name                      string
	MaxFiles                  int
	MaxBytes                  int64
	DirectoryExcluded         func(string) bool
	PathOwned                 func(string) bool
	SymlinkMayHideOwnedSource func(string) bool
}

type SourceInventory struct {
	FileCount    int64
	PayloadBytes int64
	SHA256       string
}

// BindSource hashes sorted, mode-aware and length-aware file facts without
// loading a capture or expected artifact. It rejects nonregular/unsafe entries
// and source changes observed while reading, within the owner's resource caps.
func BindSource(repositoryRoot string, policy SourcePolicy) (SourceInventory, error) {
	if policy.MaxFiles <= 0 || policy.MaxBytes <= 0 || policy.Name == "" || policy.DirectoryExcluded == nil || policy.PathOwned == nil || policy.SymlinkMayHideOwnedSource == nil {
		return SourceInventory{}, errors.New("source inventory requires explicit ownership and limits")
	}
	root, err := filepath.Abs(repositoryRoot)
	if err != nil {
		return SourceInventory{}, errors.New("resolve repository root")
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		return SourceInventory{}, errors.New("inspect repository root")
	}
	if !rootInfo.IsDir() {
		return SourceInventory{}, errors.New("repository root is not a directory")
	}

	entries := make([]sourceEntry, 0, 128)
	var payloadBytes int64
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk %s scope", policy.Name)
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
			if policy.DirectoryExcluded(relative) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 && policy.SymlinkMayHideOwnedSource(relative) {
			return fmt.Errorf("%s directory %q is a symbolic link", policy.Name, relative)
		}
		if !policy.PathOwned(relative) {
			return nil
		}
		if !utf8.ValidString(relative) || strings.ContainsAny(relative, "\x00\n") {
			return fmt.Errorf("%s path is not frame-safe UTF-8", policy.Name)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s path %q is a symbolic link", policy.Name, relative)
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect %s path %q", policy.Name, relative)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s path %q is not a regular file", policy.Name, relative)
		}
		if info.Size() < 0 || info.Size() > policy.MaxBytes-payloadBytes {
			return fmt.Errorf("%s scope exceeds its byte limit", policy.Name)
		}
		contents, err := ReadSourceFile(path, info.Size())
		if err != nil {
			return fmt.Errorf("read %s path %q: %w", policy.Name, relative, err)
		}
		digest := sha256.Sum256(contents)
		entries = append(entries, sourceEntry{
			path:   relative,
			mode:   normalizedGitMode(info.Mode()),
			size:   info.Size(),
			sha256: hex.EncodeToString(digest[:]),
		})
		payloadBytes += info.Size()
		if len(entries) > policy.MaxFiles {
			return fmt.Errorf("%s scope exceeds its file limit", policy.Name)
		}
		return nil
	})
	if err != nil {
		return SourceInventory{}, err
	}
	if len(entries) == 0 {
		return SourceInventory{}, fmt.Errorf("%s scope is empty", policy.Name)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	hasher := sha256.New()
	for _, entry := range entries {
		if _, err := io.WriteString(hasher, entry.frame()); err != nil {
			return SourceInventory{}, fmt.Errorf("hash %s inventory", policy.Name)
		}
	}
	return SourceInventory{FileCount: int64(len(entries)), PayloadBytes: payloadBytes, SHA256: hex.EncodeToString(hasher.Sum(nil))}, nil
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

func normalizedGitMode(mode fs.FileMode) string {
	if mode.Perm()&0o111 != 0 {
		return "100755"
	}
	return "100644"
}
