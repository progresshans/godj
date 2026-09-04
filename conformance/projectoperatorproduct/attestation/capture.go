package attestation

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// WriteCapture validates a fresh destination outside the repository, confirms
// that the supplied source binding still matches the repository, encodes the
// exact-producer document, and publishes it without replacing an existing
// path. A hosted workflow owns comparison and later checked-artifact updates.
func WriteCapture(
	repositoryRoot, capturePath string,
	postgresqlObserved, sqliteObserved ObservedFacts,
	postgresql PostgreSQLFingerprint,
	source SourceBinding,
) error {
	resolvedCapture, err := resolveCapturePath(repositoryRoot, capturePath)
	if err != nil {
		return err
	}
	current, err := ComputeSourceBinding(repositoryRoot)
	if err != nil {
		return errors.New("compute source binding before external operator capture")
	}
	if !source.Equal(current) {
		return errors.New("external operator behavioral source changed before capture")
	}
	document, err := MarshalCapture(postgresqlObserved, sqliteObserved, postgresql, source)
	if err != nil {
		return err
	}
	if err := writeCaptureAtomic(resolvedCapture, document); err != nil {
		return errors.New("publish external operator PostgreSQL capture")
	}
	return nil
}

func resolveCapturePath(repositoryRoot, capturePath string) (string, error) {
	if !filepath.IsAbs(capturePath) {
		return "", errors.New("external operator capture must use an absolute temporary path")
	}
	resolvedRepository, err := filepath.EvalSymlinks(repositoryRoot)
	if err != nil {
		return "", errors.New("resolve external operator attestation repository root")
	}
	resolvedDirectory, err := filepath.EvalSymlinks(filepath.Dir(capturePath))
	if err != nil {
		return "", errors.New("resolve external operator attestation capture directory")
	}
	resolvedCapture := filepath.Join(resolvedDirectory, filepath.Base(capturePath))
	relative, err := filepath.Rel(resolvedRepository, resolvedCapture)
	if err != nil {
		return "", errors.New("compare external operator capture and repository paths")
	}
	if relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
		return "", errors.New("external operator capture must remain outside the repository tree")
	}
	if _, err := os.Lstat(resolvedCapture); err == nil {
		return "", errors.New("external operator capture refuses to replace an existing path")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("inspect external operator capture path")
	}
	checked := filepath.Join(
		resolvedRepository,
		"conformance",
		"projectoperatorproduct",
		"attestations",
		FileName,
	)
	if filepath.Clean(resolvedCapture) == filepath.Clean(checked) {
		return "", errors.New("external operator capture cannot write the checked attestation in place")
	}
	return resolvedCapture, nil
}

func writeCaptureAtomic(path string, contents []byte) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	// Link publication is atomic and fails when a destination appears after
	// validation. Rename would silently replace that raced-in file.
	if err := os.Link(temporaryPath, path); err != nil {
		return err
	}
	if err := os.Remove(temporaryPath); err != nil {
		_ = os.Remove(path)
		return err
	}
	removeTemporary = false
	directoryHandle, err := os.Open(directory)
	if err != nil {
		_ = os.Remove(path)
		return err
	}
	syncErr := directoryHandle.Sync()
	closeErr := directoryHandle.Close()
	if err := errors.Join(syncErr, closeErr); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}
