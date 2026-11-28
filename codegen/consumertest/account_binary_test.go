package codegen_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/progresshans/godj/internal/testenv"
)

// One instance belongs to one finalized generated module. No binary crosses a
// fixture, toolchain or instrumentation boundary. Each selected branch still
// starts its own process from the consumer package directory.
type accountTestBinary struct {
	root, binary, converter, pkg, source, checksum string
	env                                            []string
}

func accountSourceDigest(root string) (string, error) {
	hash := sha256.New()
	err := fs.WalkDir(os.DirFS(root), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unexpected fixture file kind at %q", name)
		}
		f, err := os.Open(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			return err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		if err = errors.Join(err, f.Close()); err != nil {
			return err
		}
		_, err = fmt.Fprintf(hash, "%x %q\n", h.Sum(nil), name)
		return err
	})
	return fmt.Sprintf("%x", hash.Sum(nil)), err
}

func checkAccountSource(root, expected string) error {
	actual, err := accountSourceDigest(root)
	if err != nil {
		return err
	}
	if actual != expected {
		return errors.New("generated module changed after preparing its test binary")
	}
	return nil
}

func accountExecutableIdentity(t *testing.T, path string) (string, string, string) {
	t.Helper()
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	parent, ok := debug.ReadBuildInfo()
	if !ok {
		t.Fatal("parent test binary has no build information")
	}
	settings := func(info *debug.BuildInfo) map[string]string {
		m := make(map[string]string)
		for _, v := range info.Settings {
			m[v.Key] = v.Value
		}
		return m
	}
	child, host := settings(info), settings(parent)
	if info.GoVersion != runtime.Version() ||
		child["GOOS"] != runtime.GOOS || child["GOARCH"] != runtime.GOARCH || child["CGO_ENABLED"] != host["CGO_ENABLED"] ||
		(child["-race"] == "true") != consumerRaceEnabled || child["-trimpath"] != "true" {
		t.Fatal("prepared account binary differs from fixture coordinate", info.Path)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	_, err = io.Copy(hash, file)
	if err = errors.Join(err, file.Close()); err != nil {
		t.Fatal(err)
	}
	return info.Path, child["CGO_ENABLED"], fmt.Sprintf("%x", hash.Sum(nil))
}

func prepareAccountTestBinary(t *testing.T, root string) *accountTestBinary {
	t.Helper()
	started := time.Now()
	binary := &accountTestBinary{root: root, binary: filepath.Join(t.TempDir(), "consumer.test"), env: generatedTestEnvironment(os.Environ())}
	// Go test -c retains the normal test compilation and its default vet pass.
	build := generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-c", "-o", binary.binary, "./consumer")
	groupCancellation(build)
	runStrictGeneratedCommand(t, build)
	path, cgo, checksum := accountExecutableIdentity(t, binary.binary)
	binary.pkg = strings.TrimSuffix(path, ".test")
	if !strings.HasSuffix(binary.pkg, "/consumer") || path != binary.pkg+".test" {
		t.Fatalf("unexpected account test executable %s", path)
	}
	binary.checksum = checksum
	tool, err := generatedGoCommand(t.Context(), root, "tool", "-n", "test2json").Output()
	if err != nil {
		t.Fatal(err)
	}
	binary.converter = strings.TrimSpace(string(tool))
	if !filepath.IsAbs(binary.converter) {
		t.Fatal("test2json was not resolved by the fixture toolchain")
	}
	// -mod=mod may have finished materializing the fixture's go.mod/go.sum.
	binary.source, err = accountSourceDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("GODJ_ACCOUNT_PREPARED source=%s binary=%s goos=%s goarch=%s cgo=%s race=%t elapsed_ms=%d", binary.source, binary.checksum, runtime.GOOS, runtime.GOARCH, cgo, consumerRaceEnabled, time.Since(started).Milliseconds())
	return binary
}

// The test process and the official converter are direct children. Cancellation
// stops the owned group, and Wait collects both exit statuses even on failure.
func groupCancellation(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 5 * time.Second
}

func (b *accountTestBinary) run(t *testing.T, vendor string, plan accountConsumerTestPlan, retain func([]byte) error) []byte {
	t.Helper()
	if err := checkAccountSource(b.root, b.source); err != nil {
		t.Fatal(err)
	}
	// go test's standard package deadline is ten minutes. The extra minute
	// allows panic diagnostics before forcefully reaping an unresponsive child.
	ctx, cancel := context.WithTimeout(t.Context(), 11*time.Minute)
	defer cancel()
	env := testenv.With(b.env, map[string]string{"GODJ_ACCOUNT_CONSUMER_DATABASE": vendor})
	dir := filepath.Join(b.root, "consumer")
	child := exec.CommandContext(ctx, b.binary, "-test.v=test2json", "-test.count=1", "-test.timeout=10m", "-test.run="+plan.pattern)
	child.Dir, child.Env = dir, env
	groupCancellation(child)
	converter := exec.CommandContext(ctx, b.converter, "-t", "-p", b.pkg)
	converter.Dir, converter.Env = dir, env
	groupCancellation(converter)
	started := time.Now()
	output := collectGeneratedCommand(t, converter, retain, func() error {
		return streamAccountProcess(child, converter)
	})
	if err := completeAccountProcess(output, b.pkg); err != nil {
		t.Fatal(err)
	}
	t.Logf("GODJ_ACCOUNT_EXEC source=%s binary=%s vendor=%s pid=%d elapsed_ms=%d", b.source, b.checksum, vendor, child.Process.Pid, time.Since(started).Milliseconds())
	return output
}

func streamAccountProcess(child, converter *exec.Cmd) error {
	read, write, err := os.Pipe()
	if err != nil {
		return err
	}
	converter.Stdin = read
	child.Stdout, child.Stderr = write, write
	if err = converter.Start(); err != nil {
		return errors.Join(err, read.Close(), write.Close())
	}
	closeRead := read.Close()
	childErr := child.Run()
	closeWrite := write.Close()
	return errors.Join(closeRead, childErr, closeWrite, converter.Wait())
}

func completeAccountProcess(raw []byte, pkg string) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	running, ended := false, false
	seen := map[string]bool{}
	for {
		var event struct{ Package, Action, Test string }
		err := decoder.Decode(&event)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if event.Package != pkg || ended {
			return errors.New("misowned or trailing account child event")
		}
		switch {
		case event.Action == "start" && !running && event.Test == "":
			running = true
		case !running:
			return errors.New("account child event before package start")
		case event.Action == "run" && event.Test != "":
			if _, exists := seen[event.Test]; exists {
				return errors.New("duplicate account child start")
			}
			seen[event.Test] = false
		case event.Action == "pass" && event.Test != "":
			if passed, exists := seen[event.Test]; !exists || passed {
				return errors.New("unexpected account child completion")
			}
			seen[event.Test] = true
		case event.Action == "pass" && event.Test == "":
			ended = true
		case event.Action == "output" || event.Action == "pause" || event.Action == "cont" || event.Action == "attr":
		default:
			return fmt.Errorf("unsuccessful account child event %q", event.Action)
		}
	}
	if !ended || len(seen) == 0 {
		return errors.New("account child output truncated or empty")
	}
	for _, passed := range seen {
		if !passed {
			return errors.New("account child did not finish")
		}
	}
	return nil
}

func TestAccountPreparedFixtureRejectsMutatedSourceAndIncompleteEvents(t *testing.T) {
	root := t.TempDir()
	writeGeneratedTestFile(t, root, "consumer/input.go", []byte("package consumer\n"))
	source, err := accountSourceDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	b := accountTestBinary{root: root, source: source}
	if err := checkAccountSource(b.root, b.source); err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/input.go", []byte("package another\n"))
	if err := checkAccountSource(b.root, b.source); err == nil {
		t.Fatal("prepared binary accepted a changed source")
	}
	records := []string{`{"Package":"p","Action":"start"}`, `{"Package":"p","Action":"run","Test":"TestA"}`, `{"Package":"p","Action":"pass","Test":"TestA"}`, `{"Package":"p","Action":"pass"}`}
	good := strings.Join(records, "\n")
	if err := completeAccountProcess([]byte(good), "p"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.Join(records[:3], "\n"), good + "\n" + records[3], strings.Replace(good, records[2], records[1], 1), strings.Replace(good, `"run"`, `"skip"`, 1), strings.Replace(good, `"p"`, `"q"`, 1)} {
		if err := completeAccountProcess([]byte(bad), "p"); err == nil {
			t.Fatal("invalid account child stream accepted")
		}
	}
}

func TestAccountProcessCancellationReapsFixtureAndConverter(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	marker := filepath.Join(t.TempDir(), "started")
	child := exec.CommandContext(ctx, "/bin/sh", "-c", `printf ready > "$1"; exec sleep 120`, "fixture", marker)
	converter := exec.CommandContext(ctx, "cat")
	groupCancellation(child)
	groupCancellation(converter)
	done := make(chan error, 1)
	go func() { done <- streamAccountProcess(child, converter) }()
	deadline := time.After(10 * time.Second)
	for {
		if data, err := os.ReadFile(marker); err == nil && string(data) == "ready" {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("fixture exited early: %v", err)
		case <-deadline:
			cancel()
			<-done
			t.Fatal("fixture failed to start")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled execution returned success")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("canceled processes did not finish")
	}
	for _, cmd := range []*exec.Cmd{child, converter} {
		if cmd.ProcessState == nil || !errors.Is(syscall.Kill(-cmd.Process.Pid, 0), syscall.ESRCH) {
			t.Fatal("canceled child group was not reaped")
		}
	}
}
