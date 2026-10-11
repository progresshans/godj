package attestation

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolveCapturePathRejectsRepositorySymlinkAndReplacementPaths(t *testing.T) {
	root := t.TempDir()
	repository := filepath.Join(root, "repository")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	captureDirectory := filepath.Join(root, "captures")
	if err := os.MkdirAll(captureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(captureDirectory, "fresh.json")
	resolved, err := resolveCapturePath(repository, fresh)
	resolvedDirectory, resolveErr := filepath.EvalSymlinks(captureDirectory)
	if err != nil || resolveErr != nil || resolved != filepath.Join(resolvedDirectory, "fresh.json") {
		t.Fatalf("fresh capture = (%q, %v), want exact resolved path", resolved, err)
	}

	inside := filepath.Join(repository, "capture.json")
	if _, err := resolveCapturePath(repository, inside); err == nil || !strings.Contains(err.Error(), "outside the repository") {
		t.Fatalf("repository capture error = %v", err)
	}

	repositoryLink := filepath.Join(captureDirectory, "repository-link")
	if err := os.Symlink(repository, repositoryLink); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	if _, err := resolveCapturePath(repository, filepath.Join(repositoryLink, "capture.json")); err == nil || !strings.Contains(err.Error(), "outside the repository") {
		t.Fatalf("symlinked repository capture error = %v", err)
	}

	existing := filepath.Join(captureDirectory, "existing.json")
	if err := os.WriteFile(existing, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveCapturePath(repository, existing); err == nil || !strings.Contains(err.Error(), "refuses to replace") {
		t.Fatalf("existing capture error = %v", err)
	}
	if _, err := resolveCapturePath(repository, "relative.json"); err == nil || !strings.Contains(err.Error(), "absolute temporary path") {
		t.Fatalf("relative capture error = %v", err)
	}
}

func TestWriteCaptureAtomicDoesNotReplaceRacedDestination(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "capture.json")
	if err := os.WriteFile(destination, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeCaptureAtomic(destination, []byte("replacement")); err == nil {
		t.Fatal("writeCaptureAtomic replaced an existing destination")
	}
	contents, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "existing" {
		t.Fatalf("destination = %q, want existing", contents)
	}
}

func TestWriteCaptureRejectsSourceMutationBeforeProducerCheck(t *testing.T) {
	repository := seedSourceRepository(t)
	source, err := ComputeSourceBinding(repository)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(repository, "systemstate", "changed.go"), []byte("package systemstate\n"), 0o644)
	destination := filepath.Join(t.TempDir(), "capture.json")
	err = WriteCapture(
		repository,
		destination,
		successFacts(BackendPostgreSQL),
		successFacts(BackendSQLite),
		ExpectedPostgreSQLFingerprint(),
		source,
	)
	if err == nil || !strings.Contains(err.Error(), "source changed before capture") {
		t.Fatalf("mutated source capture error = %v", err)
	}
	if _, statErr := os.Lstat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("mutated source created capture: %v", statErr)
	}
}

func TestWriteCaptureRequiresExactProducerAndPublishesOnlyFreshCanonicalPath(t *testing.T) {
	repository := seedSourceRepository(t)
	source, err := ComputeSourceBinding(repository)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "capture.json")
	err = WriteCapture(
		repository,
		destination,
		successFacts(BackendPostgreSQL),
		successFacts(BackendSQLite),
		ExpectedPostgreSQLFingerprint(),
		source,
	)
	exact := runtime.Version() == ProducerGo && runtime.GOOS == ProducerOS && runtime.GOARCH == ProducerArch
	if !exact {
		if err == nil || !strings.Contains(err.Error(), "exact go1.26.5 linux/amd64 producer") {
			t.Fatalf("non-producer WriteCapture error = %v", err)
		}
		if _, statErr := os.Lstat(destination); !os.IsNotExist(statErr) {
			t.Fatalf("non-producer created capture: %v", statErr)
		}
		return
	}
	if err != nil {
		t.Fatalf("exact producer WriteCapture: %v", err)
	}
	info, err := os.Lstat(destination)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o644 {
		t.Fatalf("capture metadata = (%v, %v)", info, err)
	}
	document, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(document)
	if err != nil || decoded.PostgreSQLFacts().Observed() != successFacts(BackendPostgreSQL) ||
		decoded.SQLiteFacts().Observed() != successFacts(BackendSQLite) || !decoded.SourceBinding().Equal(source) {
		t.Fatalf("published capture = (%+v, %+v, %v)", decoded.PostgreSQLFacts().Observed(), decoded.SQLiteFacts().Observed(), err)
	}
}
