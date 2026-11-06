//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/projectcheck/protocol"
)

func TestStartAppCreatesOwnedSourceWithoutChangingProjectRegistration(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// A source scaffold can be created before a usable project or module.
	for _, name := range []string{"godj.toml", "go.mod", "site.sqlite3"} {
		if err := os.WriteFile(filepath.Join(parent, name), []byte("existing private content"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var stdout, stderr bytes.Buffer
	input := StartAppInvocation{Context: t.Context(), CWD: parent, Args: []string{"startapp", "line_items"}, Stdout: &stdout, Stderr: &stderr}
	report := RunStartApp(input)
	var result struct {
		FormatVersion int    `json:"format_version"`
		Status        string `json:"status"`
		AppLabel      string `json:"app_label"`
		Directory     string `json:"directory"`
		Installed     bool   `json:"installed"`
	}
	if report.ExitCode != 0 || !report.Publication.Published || !report.Publication.Durable || stderr.Len() != 0 || json.Unmarshal(stdout.Bytes(), &result) != nil || result.FormatVersion != 1 || result.Status != "created" || result.AppLabel != "line_items" || result.Directory != filepath.Join(parent, "line_items") || result.Installed {
		t.Fatal("source creation claimed an installed or incomplete app", report)
	}
	for _, name := range []string{"godj.toml", "go.mod", "site.sqlite3"} {
		data, err := os.ReadFile(filepath.Join(parent, name))
		if err != nil || string(data) != "existing private content" {
			t.Fatal("app creation changed existing project data", name, err)
		}
	}
	definition := filepath.Join(result.Directory, "modeldef/schema.go")
	original, err := os.ReadFile(definition)
	if err != nil || !bytes.Contains(original, []byte(`AppLabel: "line_items"`)) {
		t.Fatal("missing app declaration", err)
	}
	stdout.Reset()
	stderr.Reset()
	retry := RunStartApp(input)
	after, err := os.ReadFile(definition)
	if retry.ExitCode != 1 || retry.FailureCode != "destination_exists" || retry.Publication.Published || stdout.Len() != 0 || err != nil || !bytes.Equal(after, original) {
		t.Fatal("retry replaced app source", retry, err)
	}
	assertStartAppNoStage(t, parent)
}

func TestStartAppRejectsInvalidInputBeforeFilesystem(t *testing.T) {
	for _, args := range [][]string{nil, {"startapp"}, {"startapp", "../private"}, {"startapp", "MixedCase"}, {"startapp", "with-dash"}, {"startapp", strings.Repeat("x", 64)}, {"startapp", "line_items", "--private-option"}, {"startapp", "line_items", "dir", "extra"}} {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		var stdout, stderr bytes.Buffer
		report := RunStartApp(StartAppInvocation{Context: ctx, CWD: "not-an-absolute-directory", Args: args, Stdout: &stdout, Stderr: &stderr})
		if report.ExitCode != 2 || report.FailureCode != "invalid_arguments" || report.Publication.Published || stdout.Len() != 0 || stderr.String() != "startapp_error/invalid_arguments\n" {
			t.Fatal("invalid app input reached filesystem or exposed data", report)
		}
	}
}

func TestStartAppCancellationAndOutputFailureRespectPublication(t *testing.T) {
	for _, mode := range []string{"before", "interrupt", "missing-output", "short-output", "after"} {
		t.Run(mode, func(t *testing.T) {
			parent := t.TempDir()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			interrupt := make(chan struct{})
			var stdout io.Writer = io.Discard
			if mode == "before" {
				cancel()
			} else if mode == "interrupt" {
				close(interrupt)
			} else if mode == "missing-output" {
				stdout = nil
			} else if mode == "short-output" {
				stdout = &shortWriter{}
			} else {
				stdout = startAppResultWriter{before: cancel}
			}
			report := RunStartApp(StartAppInvocation{Context: ctx, CWD: parent, Args: []string{"startapp", "inventory", "new-app"}, Stdout: stdout, Stderr: io.Discard, Interrupt: interrupt})
			published := mode == "short-output" || mode == "after"
			if report.Publication.Published != published || report.Publication.Durable != published {
				t.Fatal("publication status lost", report)
			}
			wantCode, wantExit := map[string]string{"before": protocol.CodeProjectCanceled, "interrupt": protocol.CodeProjectInterrupted, "missing-output": "output_unavailable", "short-output": "published_output_failed", "after": ""}[mode], map[string]int{"before": 130, "interrupt": 130, "missing-output": 1, "short-output": 1, "after": 0}[mode]
			if report.FailureCode != wantCode || report.ExitCode != wantExit {
				t.Fatal("known publication became retryable or cancellation was lost", report)
			}
			_, err := os.Stat(filepath.Join(parent, "new-app/modeldef/schema.go"))
			if published && err != nil || !published && !errors.Is(err, os.ErrNotExist) {
				t.Fatal("filesystem state disagrees with publication", err)
			}
			assertStartAppNoStage(t, parent)
		})
	}
}

type startAppResultWriter struct{ before func() }

func (writer startAppResultWriter) Write(data []byte) (int, error) {
	writer.before()
	return len(data), nil
}

func assertStartAppNoStage(t *testing.T, parent string) {
	t.Helper()
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".godj-startapp-") {
			t.Fatal("owned app staging directory survived")
		}
	}
}
