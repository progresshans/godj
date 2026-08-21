//go:build darwin || linux

package projectgenerate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/progresshans/godj/codegen"
)

var (
	errProjectPathMissing  = errors.New("project path is missing")
	errProjectPathConflict = errors.New("project path is not a regular physical path")
)

const maxGeneratedSourceBytes = 64 << 20

// Check compares one immutable generated bundle with the committed project
// tree. It never creates, removes, renames or repairs a project path.
func Check(ctx context.Context, projectRoot string, bundle codegen.GeneratedBundle) (CheckReport, error) {
	return CheckRoot(ctx, ProjectRoot{absolute: projectRoot}, bundle)
}

// CheckRoot compares a bundle against the exact physical project identity
// sealed by the selector. It remains read-only even when the pathname is
// rebound while the scan is in progress.
func CheckRoot(ctx context.Context, projectRoot ProjectRoot, bundle codegen.GeneratedBundle) (report CheckReport, resultErr error) {
	if ctx == nil {
		return report, fmt.Errorf("check generated project: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	expectedManifest, err := validateGeneratedBundle(bundle)
	if err != nil {
		return report, err
	}
	report.ExpectedSnapshotSHA256 = expectedManifest.SnapshotSHA256
	rootSeal, err := resolveProjectRoot(projectRoot)
	if err != nil {
		return report, fmt.Errorf("check generated project: %w: %v", ErrGeneratedConflict, err)
	}
	root := rootSeal.absolute
	defer func() {
		if err := verifyProjectRoot(rootSeal); err != nil {
			resultErr = fmt.Errorf("check generated project: %w: selected project root changed", ErrGeneratedConflict)
		}
	}()

	conflict := false
	interrupted, interruptedDrifts, interruptedConflict, err := inspectInterruptedPublication(ctx, root)
	if err != nil {
		return report, err
	}
	report.Interrupted = interrupted
	report.Drifts = append(report.Drifts, interruptedDrifts...)
	conflict = conflict || interruptedConflict

	expectedManifestBytes := bundle.Manifest()
	expectedManifestSHA := sha256Hex(expectedManifestBytes)
	actualManifestBytes, actualManifestMode, manifestReadErr := readRegularProjectFileBounded(root, generatedManifestRelativePath, maxCommittedManifestBytes)
	var actualManifest committedManifest
	actualManifestValid := false
	switch {
	case errors.Is(manifestReadErr, errProjectPathMissing):
		report.Drifts = append(report.Drifts, Drift{
			Path:           generatedManifestRelativePath,
			Kind:           DriftManifest,
			ExpectedSHA256: expectedManifestSHA,
		})
	case manifestReadErr != nil:
		conflict = true
		report.Drifts = append(report.Drifts, Drift{
			Path:           generatedManifestRelativePath,
			Kind:           DriftManifest,
			ExpectedSHA256: expectedManifestSHA,
			ActualSHA256:   sha256Hex(actualManifestBytes),
		})
	default:
		decoded, decodeErr := decodeCommittedManifest(actualManifestBytes)
		if decodeErr == nil {
			actualManifest = decoded
			actualManifestValid = true
			report.ActualSnapshotSHA256 = decoded.SnapshotSHA256
		}
		actualSHA := sha256Hex(actualManifestBytes)
		if decodeErr != nil || actualManifestMode.Perm() != 0o644 || actualManifestMode != actualManifestMode.Perm() || actualSHA != expectedManifestSHA {
			report.Drifts = append(report.Drifts, Drift{
				Path:           generatedManifestRelativePath,
				Kind:           DriftManifest,
				ExpectedSHA256: expectedManifestSHA,
				ActualSHA256:   actualSHA,
			})
		}
	}
	if actualManifestValid && actualManifest.SnapshotSHA256 != expectedManifest.SnapshotSHA256 {
		report.Drifts = append(report.Drifts, Drift{
			Path:           generatedManifestRelativePath,
			Kind:           DriftSnapshot,
			ExpectedSHA256: expectedManifest.SnapshotSHA256,
			ActualSHA256:   actualManifest.SnapshotSHA256,
		})
	}

	expectedFiles := make(map[string]codegen.GeneratedFile, len(bundle.Files()))
	for _, file := range bundle.Files() {
		expectedFiles[file.Path] = file
		if err := ctx.Err(); err != nil {
			return report, err
		}
		contents, mode, readErr := readRegularProjectFile(root, file.Path)
		switch {
		case errors.Is(readErr, errProjectPathMissing):
			report.Drifts = append(report.Drifts, Drift{Path: file.Path, Kind: DriftMissing, ExpectedSHA256: file.SHA256})
		case readErr != nil:
			conflict = true
			report.Drifts = append(report.Drifts, Drift{
				Path:           file.Path,
				Kind:           DriftModified,
				ExpectedSHA256: file.SHA256,
				ActualSHA256:   sha256Hex(contents),
			})
		default:
			actualSHA := sha256Hex(contents)
			if mode.Perm() != 0o644 || mode != mode.Perm() || actualSHA != file.SHA256 {
				report.Drifts = append(report.Drifts, Drift{
					Path:           file.Path,
					Kind:           DriftModified,
					ExpectedSHA256: file.SHA256,
					ActualSHA256:   actualSHA,
				})
			}
		}
	}

	unexpected := make(map[string]Drift)
	if actualManifestValid {
		for _, file := range actualManifest.Files {
			if _, current := expectedFiles[file.Path]; current {
				continue
			}
			actualSHA, exists, pathConflict := inspectProjectFileSHA(root, file.Path)
			if !exists && !pathConflict {
				continue
			}
			conflict = conflict || pathConflict
			unexpected[file.Path] = Drift{Path: file.Path, Kind: DriftUnexpected, ActualSHA256: actualSHA}
		}
	}
	allowed := make(map[string]struct{}, len(expectedFiles))
	for filename := range expectedFiles {
		allowed[filename] = struct{}{}
	}
	if actualManifestValid {
		for _, file := range actualManifest.Files {
			allowed[file.Path] = struct{}{}
		}
	}
	extras, scanConflict, scanErr := scanReservedGeneratedNamespace(ctx, root, allowed)
	if scanErr != nil {
		return report, scanErr
	}
	conflict = conflict || scanConflict
	for _, drift := range extras {
		unexpected[drift.Path] = drift
	}
	for _, path := range sortedDriftKeys(unexpected) {
		report.Drifts = append(report.Drifts, unexpected[path])
	}

	sort.Slice(report.Drifts, func(left, right int) bool {
		if report.Drifts[left].Path != report.Drifts[right].Path {
			return report.Drifts[left].Path < report.Drifts[right].Path
		}
		return report.Drifts[left].Kind < report.Drifts[right].Kind
	})
	if report.Clean() {
		return report, nil
	}
	causes := []error{ErrGeneratedDrift}
	if report.Interrupted {
		causes = append(causes, ErrPublicationInterrupted)
	}
	if conflict {
		causes = append(causes, ErrGeneratedConflict)
	}
	return report, fmt.Errorf("check generated project: %w", errors.Join(causes...))
}

func inspectInterruptedPublication(ctx context.Context, root string) (bool, []Drift, bool, error) {
	var drifts []Drift
	conflict := false
	journal, err := openProjectRelative(root, publicationJournalRelativePath, false)
	if err == nil {
		if closeErr := journal.Close(); closeErr != nil {
			return false, nil, false, fmt.Errorf("inspect publication journal: %w", closeErr)
		}
		actualSHA, pathConflict := observedProjectFileSHA(root, publicationJournalRelativePath)
		conflict = conflict || pathConflict
		drifts = append(drifts, Drift{Path: publicationJournalRelativePath, Kind: DriftInterrupted, ActualSHA256: actualSHA})
	} else if !errors.Is(err, errProjectPathMissing) {
		conflict = true
		drifts = append(drifts, Drift{Path: publicationJournalRelativePath, Kind: DriftInterrupted})
	}
	if err := ctx.Err(); err != nil {
		return false, nil, false, err
	}

	entries, readErr := projectRelativeDirectoryEntries(root, publicationTransactionDirectoryPath)
	switch {
	case errors.Is(readErr, errProjectPathMissing):
	case readErr != nil:
		conflict = true
		drifts = append(drifts, Drift{Path: publicationTransactionDirectoryPath, Kind: DriftInterrupted})
	default:
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return false, nil, false, err
			}
			relative := publicationTransactionDirectoryPath + "/" + entry.Name()
			drifts = append(drifts, Drift{Path: relative, Kind: DriftInterrupted})
		}
	}
	return len(drifts) != 0, drifts, conflict, nil
}

func observedProjectFileSHA(root, relative string) (string, bool) {
	sha, _, conflict := inspectProjectFileSHA(root, relative)
	return sha, conflict
}

func inspectProjectFileSHA(root, relative string) (string, bool, bool) {
	contents, _, err := readRegularProjectFile(root, relative)
	if err == nil {
		return sha256Hex(contents), true, false
	}
	if errors.Is(err, errProjectPathMissing) {
		return "", false, false
	}
	return sha256Hex(contents), false, true
}

func canonicalProjectRoot(candidate string) (string, error) {
	if candidate == "" {
		return "", fmt.Errorf("project root is empty")
	}
	absolute, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	absolute = filepath.Clean(absolute)
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("project root is not a physical directory")
	}
	physical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	return filepath.Clean(physical), nil
}

func confinedProjectPath(root, relative string) (string, error) {
	if relative == "." {
		return root, nil
	}
	if !validManifestFilePath(relative) {
		return "", fmt.Errorf("invalid project-relative path %q", relative)
	}
	absolute := filepath.Join(root, filepath.FromSlash(relative))
	rel, err := filepath.Rel(root, absolute)
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("project-relative path %q escapes root", relative)
	}
	return absolute, nil
}

func readRegularProjectFile(root, relative string) ([]byte, fs.FileMode, error) {
	return readRegularProjectFileBounded(root, relative, 0)
}

func readRegularProjectFileBounded(root, relative string, maximum int64) ([]byte, fs.FileMode, error) {
	file, err := openProjectRelative(root, relative, false)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("%w: changed file %s", errProjectPathConflict, relative)
	}
	if maximum == 0 {
		maximum = maxGeneratedSourceBytes
	}
	if opened.Size() > maximum {
		return nil, opened.Mode(), fmt.Errorf("%w: file %s exceeds resource limit", errProjectPathConflict, relative)
	}
	reader := io.LimitReader(file, maximum+1)
	contents, err := io.ReadAll(reader)
	if err != nil {
		return contents, opened.Mode(), fmt.Errorf("%w: read %s: %v", errProjectPathConflict, relative, err)
	}
	if int64(len(contents)) > maximum {
		return nil, opened.Mode(), fmt.Errorf("%w: file %s exceeds resource limit", errProjectPathConflict, relative)
	}
	return contents, opened.Mode(), nil
}

func sortedDriftKeys(values map[string]Drift) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
