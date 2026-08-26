//go:build darwin || linux

package restart_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/conformance/systemstate/attestation"
	"github.com/progresshans/godj/conformance/systemstate/multiruntimeworker"
)

// TestSystemStatePostgresTwoProcessCoordinationRestartSentinel is the required
// Linux producer for the source-bound SYS-020 PostgreSQL backend facts. It
// writes canonical evidence only to an explicit absolute temporary path; the
// hosted workflow owns byte comparison with the checked artifact.
func TestSystemStatePostgresTwoProcessCoordinationRestartSentinel(t *testing.T) {
	databaseURL := restartPostgresTestURL(t)
	schema := createRestartPostgresSchema(t, databaseURL)
	repository := restartRepositoryRoot(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	before, err := attestation.ComputeSourceBinding(repository)
	if err != nil {
		t.Fatal("compute pre-run PostgreSQL attestation source binding")
	}
	executable := buildRestartMultiRuntimeWorker(t, ctx, repository, t.TempDir())
	database, err := multiruntimeworker.NewPostgresDatabase(databaseURL, schema)
	if err != nil {
		t.Fatal("configure PostgreSQL multi-runtime worker")
	}
	facts, err := multiruntimeworker.RunScenario(ctx, executable, database)
	if err != nil {
		t.Fatalf("run PostgreSQL two-process coordination scenario: %v", err)
	}
	after, err := attestation.ComputeSourceBinding(repository)
	if err != nil {
		t.Fatal("compute post-run PostgreSQL attestation source binding")
	}
	if !before.Equal(after) {
		t.Fatal("PostgreSQL attestation behavioral source changed during live execution")
	}

	observed := attestation.ObservedFacts{
		WriterProcesses:   int64(facts.WriterProcesses),
		SameSchema:        facts.SameSchema,
		BarrierLinearized: facts.BarrierLinearized,
		RestartPreserved:  facts.RestartPreserved,
		DivergenceCount:   int64(restartBoolCount(facts.Divergence)),
		LossCount:         int64(restartBoolCount(facts.Loss)),
		DriftCount:        int64(restartBoolCount(facts.Drift)),
		SecretOccurrences: int64(facts.SecretOccurrences),
	}
	if capturePath := os.Getenv(postgresAttestationCaptureEnv); capturePath != "" {
		writePostgresAttestationCapture(t, repository, capturePath, observed, after)
	}

	if facts.WriterProcesses != 2 || !facts.SameSchema || !facts.BarrierLinearized ||
		facts.HolderCallbackInvocations != 1 || facts.ContenderCallbackInvocations != 1 ||
		!facts.RestartPreserved || facts.DurableEvents != 2 || facts.Divergence || facts.Loss || facts.Drift ||
		facts.SecretOccurrences != 0 {
		t.Fatalf("PostgreSQL two-process normalized facts failed: %+v", facts)
	}
}

func buildRestartMultiRuntimeWorker(
	t *testing.T,
	ctx context.Context,
	repository, temporary string,
) string {
	t.Helper()
	executable := filepath.Join(temporary, "system-state-multiruntime-worker")
	buildCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(
		buildCtx,
		"go",
		"build",
		"-buildvcs=false",
		"-mod=readonly",
		"-trimpath",
		"-o",
		executable,
		"./conformance/systemstate/multiruntimeworker/cmd",
	)
	command.Dir = repository
	command.Env = postgresAttestationWorkerBuildEnvironment(os.Environ())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build multi-runtime worker: %v; output bytes=%d", err, len(output))
	}
	info, err := os.Stat(executable)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 || info.Size() == 0 {
		t.Fatalf("multi-runtime worker build artifact is not an executable regular file")
	}
	return executable
}

func postgresAttestationWorkerBuildEnvironment(base []string) []string {
	blocked := map[string]struct{}{
		"CGO_ENABLED":  {},
		"GO111MODULE":  {},
		"GO386":        {},
		"GOAMD64":      {},
		"GOARCH":       {},
		"GOARM":        {},
		"GOARM64":      {},
		"GOCACHEPROG":  {},
		"GODEBUG":      {},
		"GOENV":        {},
		"GOEXPERIMENT": {},
		"GOFIPS140":    {},
		"GOFLAGS":      {},
		"GOMIPS":       {},
		"GOMIPS64":     {},
		"GOOS":         {},
		"GOPPC64":      {},
		"GORISCV64":    {},
		"GOROOT":       {},
		"GOTOOLCHAIN":  {},
		"GOWASM":       {},
		"GOWORK":       {},
	}
	filtered := make([]string, 0, len(base))
	for _, entry := range base {
		name, _, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		if _, excluded := blocked[name]; excluded {
			continue
		}
		filtered = append(filtered, entry)
	}
	return restartEnvironment(filtered, map[string]string{
		"CGO_ENABLED": "0",
		"GO111MODULE": "on",
		"GOAMD64":     "v1",
		"GOARCH":      attestation.ProducerArch,
		"GOENV":       "off",
		"GOFLAGS":     "",
		"GOOS":        attestation.ProducerOS,
		"GOTOOLCHAIN": "local",
		"GOWORK":      "off",
	})
}

func writePostgresAttestationCapture(
	t *testing.T,
	repository, capturePath string,
	observed attestation.ObservedFacts,
	source attestation.SourceBinding,
) {
	t.Helper()
	resolvedCapture, err := resolvePostgresAttestationCapture(repository, capturePath)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := attestation.MarshalCapture(observed, source)
	if err != nil {
		t.Fatal("encode PostgreSQL live attestation capture")
	}
	if err := writeRestartCaptureAtomic(resolvedCapture, contents); err != nil {
		t.Fatal("write PostgreSQL live attestation capture")
	}
}

func resolvePostgresAttestationCapture(repository, capturePath string) (string, error) {
	if !filepath.IsAbs(capturePath) {
		return "", errors.New(postgresAttestationCaptureEnv + " must be an absolute temporary path")
	}
	resolvedRepository, err := filepath.EvalSymlinks(repository)
	if err != nil {
		return "", errors.New("resolve PostgreSQL attestation repository root")
	}
	resolvedDirectory, err := filepath.EvalSymlinks(filepath.Dir(capturePath))
	if err != nil {
		return "", errors.New("resolve PostgreSQL attestation capture directory")
	}
	resolvedCapture := filepath.Join(resolvedDirectory, filepath.Base(capturePath))
	relative, err := filepath.Rel(resolvedRepository, resolvedCapture)
	if err != nil {
		return "", errors.New("compare PostgreSQL attestation capture and repository paths")
	}
	if relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
		return "", errors.New("live PostgreSQL sentinel capture must remain outside the repository tree")
	}
	if _, err := os.Lstat(resolvedCapture); err == nil {
		return "", errors.New("live PostgreSQL sentinel refuses to replace an existing capture path")
	} else if !os.IsNotExist(err) {
		return "", errors.New("inspect PostgreSQL attestation capture path")
	}
	checked := filepath.Join(
		repository,
		"conformance",
		"systemstate",
		"attestations",
		attestation.FileName,
	)
	if filepath.Clean(resolvedCapture) == checked {
		return "", errors.New("live PostgreSQL sentinel cannot write the checked attestation in place")
	}
	return resolvedCapture, nil
}

func writeRestartCaptureAtomic(path string, contents []byte) error {
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
	// Link publication is atomic and fails when the destination appeared after
	// the earlier validation. Rename would replace that raced-in path.
	if err := os.Link(temporaryPath, path); err != nil {
		return err
	}
	if err := os.Remove(temporaryPath); err != nil {
		_ = os.Remove(path)
		return err
	}
	removeTemporary = false
	return nil
}

func restartBoolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}

func TestResolvePostgresAttestationCaptureRejectsRepositoryAndReplacementPaths(t *testing.T) {
	repository := restartRepositoryRoot(t)
	directory := t.TempDir()
	fresh := filepath.Join(directory, "fresh-attestation.json")
	resolved, err := resolvePostgresAttestationCapture(repository, fresh)
	resolvedDirectory, resolveErr := filepath.EvalSymlinks(directory)
	if err != nil || resolveErr != nil || resolved != filepath.Join(resolvedDirectory, filepath.Base(fresh)) {
		t.Fatalf("fresh external capture = (%q, %v), want exact path/nil", resolved, err)
	}

	inside := filepath.Join(repository, "conformance", "systemstate", "capture.json")
	if _, err := resolvePostgresAttestationCapture(repository, inside); err == nil ||
		!strings.Contains(err.Error(), "outside the repository") {
		t.Fatalf("repository capture error = %v", err)
	}

	symlink := filepath.Join(directory, "repository-link")
	if err := os.Symlink(repository, symlink); err != nil {
		t.Fatal("create repository capture symlink")
	}
	if _, err := resolvePostgresAttestationCapture(repository, filepath.Join(symlink, "capture.json")); err == nil ||
		!strings.Contains(err.Error(), "outside the repository") {
		t.Fatalf("symlinked repository capture error = %v", err)
	}

	existing := filepath.Join(directory, "existing-attestation.json")
	if err := os.WriteFile(existing, []byte("existing"), 0o600); err != nil {
		t.Fatal("create existing capture sentinel")
	}
	if _, err := resolvePostgresAttestationCapture(repository, existing); err == nil ||
		!strings.Contains(err.Error(), "refuses to replace") {
		t.Fatalf("existing capture error = %v", err)
	}

	if _, err := resolvePostgresAttestationCapture(repository, "relative.json"); err == nil ||
		!strings.Contains(err.Error(), "absolute temporary path") {
		t.Fatalf("relative capture error = %v", err)
	}
}

func TestWriteRestartCaptureAtomicDoesNotReplaceRacedDestination(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "capture.json")
	if err := os.WriteFile(destination, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeRestartCaptureAtomic(destination, []byte("replacement")); err == nil {
		t.Fatal("writeRestartCaptureAtomic replaced an existing destination")
	}
	contents, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "existing" {
		t.Fatalf("destination contents = %q, want existing", contents)
	}
}

func TestPostgresAttestationWorkerBuildEnvironmentRejectsAmbientOverrides(t *testing.T) {
	got := restartEnvironmentMap(postgresAttestationWorkerBuildEnvironment([]string{
		"KEEP=bounded",
		"CGO_ENABLED=1",
		"GO111MODULE=off",
		"GOAMD64=v4",
		"GOARCH=386",
		"GOCACHEPROG=/tmp/cache-helper",
		"GODEBUG=gotypesalias=0",
		"GOENV=/tmp/ambient-goenv",
		"GOEXPERIMENT=fieldtrack",
		"GOFIPS140=v1.0.0",
		"GOFLAGS=-mod=mod",
		"GOOS=plan9",
		"GOROOT=/tmp/ambient-goroot",
		"GOTOOLCHAIN=auto",
		"GOWORK=/tmp/ambient.go.work",
	}))
	want := map[string]string{
		"KEEP":        "bounded",
		"CGO_ENABLED": "0",
		"GO111MODULE": "on",
		"GOAMD64":     "v1",
		"GOARCH":      attestation.ProducerArch,
		"GOENV":       "off",
		"GOFLAGS":     "",
		"GOOS":        attestation.ProducerOS,
		"GOTOOLCHAIN": "local",
		"GOWORK":      "off",
	}
	if len(got) != len(want) {
		t.Fatalf("worker build environment = %#v, want %#v", got, want)
	}
	for name, value := range want {
		if got[name] != value {
			t.Fatalf("worker build environment %s = %q, want %q", name, got[name], value)
		}
	}
}
