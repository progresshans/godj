//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/creack/pty"
	"github.com/progresshans/godj/project/firstaccount"
	"golang.org/x/term"
)

func TestFirstAccountLocalFilesUseOneBoundedRegularHandle(t *testing.T) {
	directory := t.TempDir()
	content := []byte{0, 255, '\n', 's', 'e', 'c', 'r', 'e', 't'}
	if err := os.WriteFile(filepath.Join(directory, "비공개.bin"), content, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "empty"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(directory, "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("비공개.bin", filepath.Join(directory, "linked.bin")); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, path, code string
		maximum          int
		want             []byte
	}{
		{"relative", "비공개.bin", "", len(content), content},
		{"absolute", filepath.Join(directory, "비공개.bin"), "", len(content), content},
		{"linked_regular", "linked.bin", "", len(content), content},
		{"empty", "empty", "", 0, nil},
		{"bound", "비공개.bin", "invalid_input", len(content) - 1, nil},
		{"missing", "missing", "input_read_failed", 64, nil},
		{"directory", directory, "invalid_input", 64, nil},
		{"fifo", "pipe", "invalid_input", 64, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			upload, code := readFirstAccountFile(t.Context(), nil, directory, []byte(test.path), test.maximum)
			if code != test.code {
				t.Fatal("local file outcome", code)
			}
			if code != "" {
				if upload != nil {
					t.Fatal("failed read published partial bytes")
				}
				return
			}
			if upload == nil || upload.Name != filepath.Base(test.path) || !bytes.Equal(upload.Content, test.want) {
				t.Fatal("file snapshot lost bytes or basename")
			}
			if strings.Contains(fmt.Sprintf("%#v", upload), directory) {
				t.Fatal("local path escaped metadata")
			}
			clear(upload.Content)
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if value, code := readFirstAccountFile(ctx, nil, directory, []byte("missing"), 64); code != "project_canceled" || value != nil {
		t.Fatal("canceled read reached file acquisition", code)
	}
	interrupt := make(chan struct{})
	close(interrupt)
	if value, code := readFirstAccountFile(t.Context(), interrupt, directory, []byte("pipe"), 64); code != "project_interrupted" || value != nil {
		t.Fatal("interrupted read reached file acquisition", code)
	}
}

func terminalFileDescription(t *testing.T) firstaccount.Description {
	t.Helper()
	description, _ := firstAccountParentDescription(t)
	description.Fields = append(description.Fields[:1:1],
		firstaccount.Field{Name: "document", Label: "Private document", Kind: "file", Required: true, Nullable: true, Upload: true, MaxBytes: firstaccount.MaxFieldBytes, Choices: []firstaccount.Choice{}},
		firstaccount.Field{Name: "empty", Label: "Empty document", Kind: "file", Upload: true, MaxBytes: firstaccount.MaxFieldBytes, Choices: []firstaccount.Choice{}},
		firstaccount.Field{Name: "omitted", Label: "Optional image", Kind: "image", Upload: true, MaxBytes: firstaccount.MaxFieldBytes, Choices: []firstaccount.Choice{}},
	)
	description.MaxFileBytes, description.MaxFilesBytes, description.Fingerprint = 32, 40, ""
	document, err := json.Marshal(description)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(append([]byte("godj.first-account-command.v2\x00"), document...))
	description.Fingerprint = hex.EncodeToString(digest[:])
	if err := firstaccount.ValidateDescription(description); err != nil {
		t.Fatal(err)
	}
	return description
}

func TestFirstAccountTerminalFilesKeepEmptyOmissionAndRestoreOnFailure(t *testing.T) {
	for _, mode := range []string{"success", "file_bound", "aggregate_bound", "missing", "canceled_after_file"} {
		t.Run(mode, func(t *testing.T) {
			directory := t.TempDir()
			payload := bytes.Repeat([]byte{'s'}, 24)
			if err := os.WriteFile(filepath.Join(directory, "private.bin"), payload, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "empty.bin"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			description := terminalFileDescription(t)
			master, slave, err := pty.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer master.Close()
			defer slave.Close()
			before, err := term.GetState(int(slave.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			prompts := newObservedPromptWriter()
			done := make(chan firstAccountTerminalResult, 1)
			go func() {
				var result firstAccountTerminalResult
				result.request, result.code = readFirstAccountTerminal(ctx, nil, slave, prompts, description, &result.report, directory)
				done <- result
			}()
			waitForPrompt(t, prompts, firstAccountFieldPrompt(description.Fields[0]))
			writePTYInput(t, master, "private-user"+"\n")
			waitForPrompt(t, prompts, `"Private document" (file path): `)
			path := "private.bin"
			if mode == "file_bound" {
				if err := os.WriteFile(filepath.Join(directory, path), bytes.Repeat([]byte{'x'}, 33), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "missing" {
				path = "missing.bin"
			}
			writePTYInput(t, master, path+"\n")
			if mode != "file_bound" && mode != "missing" {
				waitForPrompt(t, prompts, firstAccountOmissionPrompt(description.Fields[2]))
				if mode == "canceled_after_file" {
					cancel()
				} else {
					writePTYInput(t, master, "n"+"\n")
					waitForPrompt(t, prompts, `"Empty document" (file path): `)
					path = "empty.bin"
					if mode == "aggregate_bound" {
						path = "private.bin"
					}
					writePTYInput(t, master, path+"\n")
					if mode == "success" {
						waitForPrompt(t, prompts, firstAccountOmissionPrompt(description.Fields[3]))
						writePTYInput(t, master, "y"+"\n")
						waitForPrompt(t, prompts, "Password: ")
						writePTYInput(t, master, "private-password"+"\n")
						waitForPrompt(t, prompts, "Password (again): ")
						writePTYInput(t, master, "private-password"+"\n")
					}
				}
			}
			result := awaitTerminalResult(t, done)
			defer result.request.Clear()
			want := ""
			switch mode {
			case "file_bound", "aggregate_bound":
				want = "invalid_input"
			case "missing":
				want = "input_read_failed"
			case "canceled_after_file":
				want = "project_canceled"
			}
			if result.code != want {
				t.Fatal("file terminal outcome", result.code)
			}
			if mode == "success" {
				if firstaccount.ValidateValues(description, result.request.Values) != nil || result.request.Values[2].Upload == nil || len(result.request.Values[2].Upload.Content) != 0 || result.request.Values[3].Present || !bytes.Equal(result.request.Values[1].Upload.Content, payload) || result.report.FileReads != 2 || result.report.FileBytes != 24 {
					t.Fatal("terminal lost typed file input")
				}
			} else if len(result.request.Values) != 0 || len(result.request.Password) != 0 {
				t.Fatal("terminal failure retained private input")
			}
			after, err := term.GetState(int(slave.Fd()))
			if err != nil || !reflect.DeepEqual(before, after) || result.report.RestoreAttempts != 1 {
				t.Fatal("file input did not restore terminal", err)
			}
			if strings.Contains(prompts.String(), "private.bin") || strings.Contains(prompts.String(), "private-password") {
				t.Fatal("redirected output exposed private input")
			}
		})
	}
}
