//go:build darwin || linux

package projectgenerate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const (
	maxProjectDirectoryEntries      = 8192
	maxProjectTreeEntries           = 65536
	maxProjectTreeDepth             = 64
	maxProjectTreePathBytes         = 16 << 20
	maxReservedGeneratedSourceBytes = 256 << 20
)

func openPhysicalProjectRoot(root string) (*os.File, error) {
	before, err := os.Lstat(root)
	if err != nil || before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
		return nil, fmt.Errorf("%w: project root is not a physical directory", errProjectPathConflict)
	}
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("%w: open project root: %v", errProjectPathConflict, err)
	}
	file := os.NewFile(uintptr(fd), root)
	after, statErr := file.Stat()
	if statErr != nil || !os.SameFile(before, after) {
		_ = file.Close()
		return nil, fmt.Errorf("%w: project root identity changed", errProjectPathConflict)
	}
	return file, nil
}

func openProjectRelative(root, relative string, directory bool) (*os.File, error) {
	if relative != "." && !validPhysicalProjectRelativePath(relative) {
		return nil, fmt.Errorf("%w: invalid project-relative path %q", errProjectPathConflict, relative)
	}
	current, err := openPhysicalProjectRoot(root)
	if err != nil {
		return nil, err
	}
	if relative == "." {
		if directory {
			return current, nil
		}
		_ = current.Close()
		return nil, fmt.Errorf("%w: project root is not a regular file", errProjectPathConflict)
	}
	components := strings.Split(relative, "/")
	for index, component := range components {
		last := index == len(components)-1
		var expected unix.Stat_t
		if last && !directory {
			if statErr := unix.Fstatat(int(current.Fd()), component, &expected, unix.AT_SYMLINK_NOFOLLOW); statErr != nil {
				_ = current.Close()
				if errors.Is(statErr, unix.ENOENT) {
					return nil, errProjectPathMissing
				}
				return nil, fmt.Errorf("%w: inspect %q: %v", errProjectPathConflict, relative, statErr)
			}
			if expected.Mode&unix.S_IFMT != unix.S_IFREG {
				_ = current.Close()
				return nil, fmt.Errorf("%w: wrong path kind %q", errProjectPathConflict, relative)
			}
		}
		flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW
		if !last || directory {
			flags |= unix.O_DIRECTORY
		} else {
			// A blocking read-only open of a FIFO waits for a writer before the
			// caller can inspect its kind or observe context cancellation. The
			// nofollow fstatat above rejects an already non-regular entry, while
			// O_NONBLOCK closes the substitution race before the post-open fstat.
			flags |= unix.O_NONBLOCK
		}
		fd, openErr := unix.Openat(int(current.Fd()), component, flags, 0)
		if openErr != nil {
			_ = current.Close()
			if errors.Is(openErr, unix.ENOENT) {
				return nil, errProjectPathMissing
			}
			return nil, fmt.Errorf("%w: open %q: %v", errProjectPathConflict, relative, openErr)
		}
		next := os.NewFile(uintptr(fd), filepath.Join(root, filepath.FromSlash(strings.Join(components[:index+1], "/"))))
		_ = current.Close()
		if last && !directory {
			var opened unix.Stat_t
			if statErr := unix.Fstat(fd, &opened); statErr != nil || opened.Mode&unix.S_IFMT != unix.S_IFREG ||
				opened.Dev != expected.Dev || opened.Ino != expected.Ino {
				_ = next.Close()
				return nil, fmt.Errorf("%w: path identity changed %q", errProjectPathConflict, relative)
			}
		}
		current = next
	}
	info, err := current.Stat()
	if err != nil || (directory && !info.IsDir()) || (!directory && !info.Mode().IsRegular()) {
		_ = current.Close()
		return nil, fmt.Errorf("%w: wrong path kind %q", errProjectPathConflict, relative)
	}
	return current, nil
}

func validPhysicalProjectRelativePath(value string) bool {
	if value == "" || value == "." || len(value) > maxProjectTreePathBytes || !utf8.ValidString(value) ||
		strings.IndexByte(value, 0) >= 0 || strings.Contains(value, "\\") || strings.HasPrefix(value, "/") || path.Clean(value) != value {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func projectRelativeDirectoryEntries(root, relative string) ([]fs.DirEntry, error) {
	directory, err := openProjectRelative(root, relative, true)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	entries, err := directory.ReadDir(maxProjectDirectoryEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: read directory %q: %v", errProjectPathConflict, relative, err)
	}
	if len(entries) > maxProjectDirectoryEntries {
		return nil, fmt.Errorf("%w: directory %q exceeds resource limit", errProjectPathConflict, relative)
	}
	return entries, nil
}

func projectRelativePathExists(root, relative string, directory bool) (bool, error) {
	file, err := openProjectRelative(root, relative, directory)
	if errors.Is(err, errProjectPathMissing) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, file.Close()
}

// scanReservedGeneratedNamespace walks the physical project tree without
// following symlinks and reports generated-source namespace members not owned
// by either the desired or prior manifest. Publication uses the same scanner
// so an unmanifested zz_godj_*.go file can never be silently overwritten.
func scanReservedGeneratedNamespace(
	ctx context.Context,
	projectRoot string,
	allowed map[string]struct{},
) ([]Drift, bool, error) {
	if ctx == nil {
		return nil, false, fmt.Errorf("scan reserved generated namespace: context is nil")
	}
	for filename := range allowed {
		if !validManifestFilePath(filename) || !validGeneratedFilename(path.Base(filename)) {
			return nil, false, fmt.Errorf("scan reserved generated namespace: invalid allowed path %q", filename)
		}
	}
	type pendingDirectory struct {
		path  string
		depth int
	}
	pending := []pendingDirectory{{path: ".", depth: 0}}
	entriesSeen := 0
	pathBytesSeen := 0
	sourceBytesSeen := int64(0)
	var drifts []Drift
	conflict := false
	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		entries, err := projectRelativeDirectoryEntries(projectRoot, current.path)
		if err != nil {
			conflict = true
			drifts = append(drifts, Drift{Path: current.path, Kind: DriftUnexpected})
			continue
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, false, err
			}
			name := entry.Name()
			if current.path == "." && (name == ".git" || name == ".godj" || name == "vendor") {
				continue
			}
			relative := name
			if current.path != "." {
				relative = current.path + "/" + name
			}
			entriesSeen++
			pathBytesSeen += len(relative)
			if entriesSeen > maxProjectTreeEntries || pathBytesSeen > maxProjectTreePathBytes {
				return append(drifts, Drift{Path: relative, Kind: DriftUnexpected}), true, nil
			}
			reserved := strings.HasPrefix(name, "zz_godj_") && strings.HasSuffix(name, ".go")
			if reserved && !validGeneratedFilename(name) {
				drifts = append(drifts, Drift{Path: relative, Kind: DriftUnexpected})
				conflict = true
				continue
			}
			entryInfo, infoErr := entry.Info()
			if infoErr != nil {
				drifts = append(drifts, Drift{Path: relative, Kind: DriftUnexpected})
				conflict = true
				continue
			}
			if entryInfo.Mode()&os.ModeSymlink != 0 {
				if reserved {
					drifts = append(drifts, Drift{Path: relative, Kind: DriftUnexpected})
					conflict = true
				}
				continue
			}
			if entryInfo.IsDir() {
				if reserved || current.depth >= maxProjectTreeDepth {
					drifts = append(drifts, Drift{Path: relative, Kind: DriftUnexpected})
					conflict = true
					continue
				}
				if nestedModule, markerErr := hasPhysicalNestedModuleMarker(projectRoot, relative); markerErr != nil {
					drifts = append(drifts, Drift{Path: relative + "/go.mod", Kind: DriftUnexpected})
					conflict = true
					continue
				} else if nestedModule {
					continue
				}
				pending = append(pending, pendingDirectory{path: relative, depth: current.depth + 1})
				continue
			}
			if !reserved {
				continue
			}
			if _, owned := allowed[relative]; owned {
				continue
			}
			contents, _, readErr := readRegularProjectFile(projectRoot, relative)
			if readErr != nil {
				drifts = append(drifts, Drift{Path: relative, Kind: DriftUnexpected})
				conflict = true
				continue
			}
			sourceBytesSeen += int64(len(contents))
			if sourceBytesSeen > maxReservedGeneratedSourceBytes {
				drifts = append(drifts, Drift{Path: relative, Kind: DriftUnexpected})
				conflict = true
				continue
			}
			drifts = append(drifts, Drift{Path: relative, Kind: DriftUnexpected, ActualSHA256: sha256Hex(contents)})
		}
	}
	sort.Slice(drifts, func(left, right int) bool { return drifts[left].Path < drifts[right].Path })
	return drifts, conflict, nil
}

func hasPhysicalNestedModuleMarker(projectRoot, directory string) (bool, error) {
	marker := directory + "/go.mod"
	file, err := openProjectRelative(projectRoot, marker, false)
	if errors.Is(err, errProjectPathMissing) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, file.Close()
}
