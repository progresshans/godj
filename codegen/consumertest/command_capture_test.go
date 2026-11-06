package codegen_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/progresshans/godj/internal/gobuild"
)

// Keep diagnostic prefixes on disk while the child is still running. A parent
// package timeout does not run deferred callbacks and must not erase the only
// record of which generated child started. These files are diagnostics; the
// complete parent/child inventories still decide whether execution passed.
func captureGeneratedCommand(t *testing.T, command *exec.Cmd) (io.Writer, io.Writer, func(error)) {
	t.Helper()
	root := os.Getenv("GODJ_GENERATED_CONSUMER_CAPTURE_DIR")
	if root == "" {
		return io.Discard, io.Discard, func(error) {}
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp(root, fmt.Sprintf("%x-", sha256.Sum256([]byte(t.Name()))))
	if err != nil {
		t.Fatal(err)
	}
	writeJSON := func(name string, value any) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, name), append(data, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeJSON("started.json", struct {
		Test      string    `json:"test"`
		Arguments []string  `json:"arguments"`
		Started   time.Time `json:"started_at"`
	}{t.Name(), command.Args[1:], time.Now().UTC()})
	open := func(name string, maximum int) *generatedArtifactWriter {
		t.Helper()
		file, err := os.OpenFile(filepath.Join(directory, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = file.Close() })
		return &generatedArtifactWriter{file: file, remaining: maximum}
	}
	stdout := open("stdout.jsonl", generatedTestOutputLimit)
	stderr := open("stderr.txt", gobuild.MaxDiagnosticBytes)
	t.Logf("generated command started; diagnostic capture=%s", filepath.Base(directory))
	return stdout, stderr, func(runErr error) {
		t.Helper()
		for _, stream := range []*generatedArtifactWriter{stdout, stderr} {
			if err := stream.file.Close(); err != nil {
				t.Fatal("close generated command diagnostic", err)
			}
		}
		writeJSON("finished.json", struct {
			Finished time.Time `json:"finished_at"`
			RunError bool      `json:"run_error"`
			Stdout   int       `json:"stdout_bytes"`
			Stderr   int       `json:"stderr_bytes"`
		}{time.Now().UTC(), runErr != nil, stdout.total, stderr.total})
	}
}

type generatedArtifactWriter struct {
	file      *os.File
	remaining int
	total     int
}

func (capture *generatedArtifactWriter) Write(data []byte) (int, error) {
	size := len(data)
	capture.total += size
	retained := min(size, capture.remaining)
	written, err := capture.file.Write(data[:retained])
	capture.remaining -= written
	if err != nil {
		return written, err
	}
	if written != retained {
		return written, io.ErrShortWrite
	}
	return size, nil
}

func TestGeneratedCommandCaptureSurvivesBeforeCompletionAndBoundsOutput(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GODJ_GENERATED_CONSUMER_CAPTURE_DIR", root)
	command := exec.Command("go", "test", "-json", "./consumer")
	stdout, stderr, finish := captureGeneratedCommand(t, command)
	record := []byte("{\"Action\":\"run\",\"Test\":\"TestPending\"}\n")
	if _, err := stdout.Write(record); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatal("missing live capture", entries, err)
	}
	directory := filepath.Join(root, entries[0].Name())
	if data, err := os.ReadFile(filepath.Join(directory, "stdout.jsonl")); err != nil || string(data) != string(record) {
		t.Fatal("unfinished child's events were not retained", err)
	}
	if _, err := os.Stat(filepath.Join(directory, "finished.json")); !os.IsNotExist(err) {
		t.Fatal("pending command was represented as finished", err)
	}
	if n, err := stdout.Write(make([]byte, generatedTestOutputLimit)); err != nil || n != generatedTestOutputLimit {
		t.Fatal("stdout overflow was not drained", n, err)
	}
	if n, err := stderr.Write(make([]byte, gobuild.MaxDiagnosticBytes+1)); err != nil || n != gobuild.MaxDiagnosticBytes+1 {
		t.Fatal("stderr overflow was not drained", n, err)
	}
	finish(fmt.Errorf("interrupted child"))
	for name, size := range map[string]int{"stdout.jsonl": generatedTestOutputLimit, "stderr.txt": gobuild.MaxDiagnosticBytes} {
		info, err := os.Stat(filepath.Join(directory, name))
		if err != nil || info.Size() != int64(size) {
			t.Fatal("diagnostic exceeded its bound", name, err)
		}
	}
	var result struct {
		RunError bool `json:"run_error"`
		Stdout   int  `json:"stdout_bytes"`
		Stderr   int  `json:"stderr_bytes"`
	}
	data, err := os.ReadFile(filepath.Join(directory, "finished.json"))
	if err != nil || json.Unmarshal(data, &result) != nil || !result.RunError || result.Stdout != len(record)+generatedTestOutputLimit || result.Stderr != gobuild.MaxDiagnosticBytes+1 {
		t.Fatal("incomplete output was represented as success", result, err)
	}
	_, _, secondFinish := captureGeneratedCommand(t, command)
	secondFinish(nil)
	entries, err = os.ReadDir(root)
	if err != nil || len(entries) != 2 {
		t.Fatal("later invocation replaced earlier diagnostics", err)
	}
}
