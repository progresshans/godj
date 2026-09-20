// Package consumertest_test verifies the published OpenAPI through an unrelated
// generated Go module. The child has no GoDj imports or source replacement.
package consumertest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/internal/gobuild"
	"github.com/progresshans/godj/internal/testenv"
	"github.com/progresshans/godj/internal/wirejson"
)

var requiredConsumerChecks = []string{
	"article_bearer_crud", "article_bearer_patch_presence", "article_bearer_put_defaults", "article_bearer_auth_errors",
	"article_session_csrf_crud", "article_session_invalid_csrf", "helpdesk_session_relations", "helpdesk_session_create_defaults",
	"helpdesk_session_integer_values", "helpdesk_session_multiline_text", "helpdesk_session_datetime_values", "helpdesk_session_calendar_dates", "helpdesk_session_clock_times", "helpdesk_session_durations", "helpdesk_session_read_only_denied", "generated_int64_wire", "generated_response_rejections", "pre_canceled_request",
	"helpdesk_session_choices", "generated_choice_response_domain", "helpdesk_nullable_boolean_presence", "helpdesk_put_patch", "generated_nullable_boolean_wire", "generated_calendar_date_wire", "generated_clock_time_wire", "generated_duration_wire",
}

func TestGeneratedOpenAPIClientContract(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	input, documents, verifyDatabase := newConsumerFixtures(t)
	fixtureRoot := filepath.Join(repositoryRoot(t), "api", "openapi", "consumertest", "testdata", "client")
	module := filepath.Join(t.TempDir(), "consumer")
	if err := copyRegularTree(fixtureRoot, module); err != nil {
		t.Fatal(err)
	}
	locks := make(map[string][]byte)
	for _, name := range []string{"go.mod", "go.sum", "ogen.yml"} {
		locks[name] = readConsumerFile(t, filepath.Join(module, name))
	}
	for _, name := range []string{"articlebearer", "articlesession", "helpdesksession"} {
		actual, found := documents[name]
		if !found || len(actual) == 0 {
			t.Fatalf("actual schema %s is missing", name)
		}
		if !bytes.Equal(actual, readConsumerFile(t, filepath.Join(module, "specs", name+".json"))) {
			t.Fatalf("schema %s differs from the actual API; regenerate the external consumer from the current declaration", name)
		}
		candidate := filepath.Join(t.TempDir(), name)
		command := exec.CommandContext(ctx, "go", "tool", "ogen", "-loglevel", "warn", "-config", "ogen.yml", "-target", candidate,
			"-package", name, filepath.Join("specs", name+".json"))
		command.Dir = module
		command.Env = testenv.OfflineGo(os.Environ(), "-mod=readonly")
		if _, err := runConsumerCommand(command, false); err != nil {
			t.Fatalf("generate %s with the locked tool: %v; prepare dependencies with make api-client-dependencies", name, err)
		}
		if err := sameRegularTree(filepath.Join(module, name), candidate); err != nil {
			t.Fatalf("generated %s drift: %v", name, err)
		}
	}
	flags := "-mod=readonly"
	if consumerRaceEnabled {
		flags += " -race"
	}
	executable := filepath.Join(t.TempDir(), "consumer")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	build := exec.CommandContext(ctx, "go", "build", "-o", executable, "./cmd/consumer")
	build.Dir = module
	build.Env = testenv.OfflineGo(os.Environ(), flags)
	if _, err := runConsumerCommand(build, false); err != nil {
		t.Fatalf("build the external generated client: %v", err)
	}
	for name, before := range locks {
		if !bytes.Equal(before, readConsumerFile(t, filepath.Join(module, name))) {
			t.Fatalf("generation or compilation changed locked input %s", name)
		}
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal("encode the private fixture input")
	}
	client := exec.CommandContext(ctx, executable)
	client.Dir = module
	client.Env = testenv.With(os.Environ(), map[string]string{"GODEBUG": "", "GORACE": ""})
	client.Stdin = bytes.NewReader(encoded)
	output, err := runConsumerCommand(client, true)
	if err != nil {
		t.Fatalf("run the external generated client: %v", err)
	}
	if err := validateConsumerReceipt(output, consumerRaceEnabled); err != nil {
		t.Fatal(err)
	}
	verifyDatabase(t)
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate the OpenAPI consumer source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", ".."))
	if !bytes.Contains(readConsumerFile(t, filepath.Join(root, "go.mod")), []byte("module github.com/progresshans/godj\n")) {
		t.Fatal("OpenAPI consumer source is outside the expected module")
	}
	return root
}

func readConsumerFile(t *testing.T, path string) []byte {
	t.Helper()
	value, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// Compiler output is drained even on failure, but truncated output is never
// accepted as successful evidence. The generator may emit ordinary warnings;
// the consumer executable must keep stderr empty. Credentials travel only on stdin.
func runConsumerCommand(command *exec.Cmd, requireQuietStderr bool) ([]byte, error) {
	var stdout, stderr gobuild.Capture
	command.Stdout, command.Stderr = &stdout, &stderr
	command.WaitDelay = 5 * time.Second
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("process failed: %w: %s", err, gobuild.Summary(stdout.Bytes(), stderr.Bytes(), command.Env))
	}
	if stdout.Len() != len(stdout.Bytes()) || stderr.Len() != len(stderr.Bytes()) {
		return nil, errors.New("process output exceeded its capture limit")
	}
	if requireQuietStderr && stderr.Len() != 0 {
		return nil, fmt.Errorf("process returned diagnostic output: %s", gobuild.Summary(nil, stderr.Bytes(), command.Env))
	}
	return stdout.Bytes(), nil
}

func validateConsumerReceipt(output []byte, race bool) error {
	if err := wirejson.Scan(output, wirejson.Limits{
		Bytes: 4096, ValueDepth: 2, Values: 32, ObjectKeys: 2,
		ArrayValues: len(requiredConsumerChecks), StringBytes: 128, KeyBytes: 16, RejectNull: true,
	}); err != nil {
		return fmt.Errorf("generated client returned invalid completion framing: %w", err)
	}
	var receipt struct {
		Checks []string `json:"checks"`
		Race   *bool    `json:"race"`
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return fmt.Errorf("generated client did not return a valid completion receipt: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("generated client returned trailing output")
	}
	if receipt.Race == nil || *receipt.Race != race {
		return errors.New("generated client was not built in the parent's race mode")
	}
	got := slices.Clone(receipt.Checks)
	want := slices.Clone(requiredConsumerChecks)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		return errors.New("generated client completion receipt omitted, duplicated, or replaced required checks")
	}
	return nil
}

func regularTree(root string) (map[string][]byte, error) {
	files := make(map[string][]byte)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return errors.New("consumer input tree contains a non-regular file")
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		value, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(name)] = value
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errors.New("consumer input tree is empty")
	}
	return files, nil
}

func sameRegularTree(expected, actual string) error {
	want, err := regularTree(expected)
	if err != nil {
		return err
	}
	got, err := regularTree(actual)
	if err != nil {
		return err
	}
	if len(got) != len(want) {
		return errors.New("generated file set differs")
	}
	for name, expected := range want {
		actual, exists := got[name]
		if !exists || !bytes.Equal(expected, actual) {
			return fmt.Errorf("generated file %s differs", name)
		}
	}
	return nil
}

func copyRegularTree(source, destination string) error {
	files, err := regularTree(source)
	if err != nil {
		return err
	}
	for name, contents := range files {
		path := filepath.Join(destination, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, contents, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func TestConsumerReceiptRejectsMissingFalseOrTruncatedCompletion(t *testing.T) {
	complete, err := json.Marshal(struct {
		Checks []string `json:"checks"`
		Race   bool     `json:"race"`
	}{requiredConsumerChecks, consumerRaceEnabled})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateConsumerReceipt(complete, consumerRaceEnabled); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range [][]byte{
		nil, []byte(`{}`), []byte(`{"checks":[],"race":false}`),
		complete[:len(complete)-1], append(slices.Clone(complete), []byte("\n{}")...),
		[]byte(strings.Replace(string(complete), requiredConsumerChecks[0], requiredConsumerChecks[1], 1)),
		[]byte(strings.Replace(string(complete), `"race":`, `"unknown":`, 1)),
		[]byte(strings.Replace(string(complete), `"race":`, `"race":false,"race":`, 1)),
		[]byte(strings.Replace(string(complete), `"checks":`, `"checks":[],"checks":`, 1)),
	} {
		if err := validateConsumerReceipt(invalid, consumerRaceEnabled); err == nil {
			t.Fatal("invalid consumer completion was accepted")
		}
	}
	if err := validateConsumerReceipt(complete, !consumerRaceEnabled); err == nil {
		t.Fatal("child race mismatch was accepted")
	}
}

func TestGeneratedTreeCheckRejectsChangedMissingAndUnexpectedFiles(t *testing.T) {
	root := t.TempDir()
	expected, actual := filepath.Join(root, "expected"), filepath.Join(root, "actual")
	if err := os.Mkdir(expected, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(expected, "client.go"), []byte("package client\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyRegularTree(expected, actual); err != nil {
		t.Fatal(err)
	}
	if err := sameRegularTree(expected, actual); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(actual, "client.go"), []byte("package changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sameRegularTree(expected, actual); err == nil {
		t.Fatal("changed generated code passed drift validation")
	}
	if err := os.Rename(filepath.Join(actual, "client.go"), filepath.Join(actual, "unexpected.go")); err != nil {
		t.Fatal(err)
	}
	if err := sameRegularTree(expected, actual); err == nil {
		t.Fatal("same-size changed generated file roster passed drift validation")
	}
	// Empty files still have identities; missing keys must not equal empty bytes.
	if err := os.WriteFile(filepath.Join(expected, "client.go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(actual, "unexpected.go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sameRegularTree(expected, actual); err == nil {
		t.Fatal("renamed empty generated file passed drift validation")
	}
}

func TestConsumerProcessRejectsFailureDiagnosticsTruncationAndCancellation(t *testing.T) {
	for _, mode := range []string{"exit", "stderr", "overflow", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			if mode == "cancel" {
				cancel()
			}
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestConsumerProcessFixture$")
			command.Env = testenv.With(os.Environ(), map[string]string{"GODJ_API_CONSUMER_PROCESS_FIXTURE": mode})
			if _, err := runConsumerCommand(command, true); err == nil {
				t.Fatal("unsuccessful child evidence was accepted")
			}
		})
	}
}

func TestConsumerProcessFixture(t *testing.T) {
	switch os.Getenv("GODJ_API_CONSUMER_PROCESS_FIXTURE") {
	case "exit":
		os.Exit(3)
	case "stderr":
		_, _ = os.Stderr.WriteString("fixture diagnostic\n")
		os.Exit(0)
	case "overflow":
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), 256<<10))
		os.Exit(0)
	}
}
