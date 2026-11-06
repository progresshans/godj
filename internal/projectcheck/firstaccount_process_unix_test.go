//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/internal/firstaccountprotocol"
	"github.com/progresshans/godj/internal/firstaccountwire"
	"github.com/progresshans/godj/internal/projectcheck/createsuperuserprotocol"
	"github.com/progresshans/godj/project/firstaccount"
)

func firstAccountResponse(t *testing.T, operation string, outcome firstaccountwire.Outcome, cleanup bool) []byte {
	t.Helper()
	code := firstaccountwire.Code("")
	if outcome == firstaccountwire.Unknown {
		code = firstaccountwire.OutcomeUnknown
	} else if outcome == firstaccountwire.NotCreated {
		code = firstaccountwire.InvalidInput
	}
	document, err := firstaccountwire.EncodeResponse(firstaccountwire.Response{Version: firstaccountprotocol.Version,
		OperationID: operation, Outcome: outcome, Code: code, CleanupFailed: cleanup})
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func TestFirstAccountProcessOutcomeRequiresItsCompleteHandoffAndOperation(t *testing.T) {
	operation := strings.Repeat("e", 64)
	for _, test := range []struct {
		name, status, code string
		change             func(*createsuperuserProcessResult)
		cleanup            bool
	}{
		{"created", "created", "", nil, false},
		{"unknown", "unknown", "outcome_unknown", func(p *createsuperuserProcessResult) {
			p.response = firstAccountResponse(t, operation, firstaccountwire.Unknown, false)
		}, false},
		{"rejected", "not_created", "invalid_input", func(p *createsuperuserProcessResult) {
			p.response = firstAccountResponse(t, operation, firstaccountwire.NotCreated, false)
		}, false},
		{"created_cleanup", "created", "created_cleanup_failed", func(p *createsuperuserProcessResult) {
			p.response = firstAccountResponse(t, operation, firstaccountwire.Created, true)
		}, true},
		{"unknown_cleanup", "unknown", "outcome_unknown", func(p *createsuperuserProcessResult) {
			p.response = firstAccountResponse(t, operation, firstaccountwire.Unknown, true)
		}, true},
		{"foreign_operation", "unknown", "outcome_unknown", func(p *createsuperuserProcessResult) {
			p.response = firstAccountResponse(t, strings.Repeat("f", 64), firstaccountwire.Created, false)
		}, false},
		{"invalid_response", "unknown", "outcome_unknown", func(p *createsuperuserProcessResult) { p.response = []byte("private invalid response") }, false},
		{"missing_response", "unknown", "outcome_unknown", func(p *createsuperuserProcessResult) { p.response = nil }, false},
		{"transport_cleanup", "unknown", "outcome_unknown", func(p *createsuperuserProcessResult) { p.cleanupFailed = true }, true},
		{"unreaped", "unknown", "outcome_unknown", func(p *createsuperuserProcessResult) { p.directReaps = 0 }, false},
		{"signaled", "unknown", "outcome_unknown", func(p *createsuperuserProcessResult) { p.exited, p.exitCode = false, -1 }, false},
		{"truncated", "unknown", "outcome_unknown", func(p *createsuperuserProcessResult) { p.stdoutScalar.Truncated = true }, false},
		{"stderr_truncated", "unknown", "outcome_unknown", func(p *createsuperuserProcessResult) { p.stderrScalar.Truncated = true }, false},
		{"private_exit", "created", "created_output_failed", func(p *createsuperuserProcessResult) {
			p.exitCode, p.response = firstaccountwire.KnownCreatedResponseFailureExitCode, nil
		}, false},
		{"private_exit_cleanup", "created", "created_cleanup_failed", func(p *createsuperuserProcessResult) {
			p.exitCode, p.response = firstaccountwire.KnownCreatedCleanupResponseFailureExitCode, nil
		}, true},
		{"private_exit_partial", "not_created", "input_transport_failed", func(p *createsuperuserProcessResult) {
			p.exitCode, p.requestBytesWritten = firstaccountwire.KnownCreatedResponseFailureExitCode, 23
		}, false},
		{"partial", "not_created", "input_transport_failed", func(p *createsuperuserProcessResult) { p.requestBytesWritten = 23 }, false},
		{"unstarted", "not_created", "input_transport_failed", func(p *createsuperuserProcessResult) {
			p.started, p.exited, p.directReaps, p.requestWriteAttempts, p.requestBytesWritten = false, false, 0, 0, 0
		}, false},
		{"repeated_write", "unknown", "outcome_unknown", func(p *createsuperuserProcessResult) { p.requestWriteAttempts = 2 }, false},
		{"inconsistent_write", "unknown", "outcome_unknown", func(p *createsuperuserProcessResult) { p.requestWriteAttempts = 0 }, false},
		{"canceled_after_handoff", "unknown", "outcome_unknown", func(p *createsuperuserProcessResult) {
			p.failure = createsuperuserProcessFailure(createsuperuserprotocol.CodeProjectCanceled)
		}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			process := createsuperuserProcessResult{started: true, exited: true, directReaps: 1, exitCode: 0,
				requestWriteAttempts: 1, requestBytesWritten: 24, response: firstAccountResponse(t, operation, firstaccountwire.Created, false)}
			if test.change != nil {
				test.change(&process)
			}
			process.stdoutScalar.RetainedBytes = len(process.response)
			result := firstAccountProcessOutcome(process, 24, operation)
			if result.OperationID != operation || result.Status != test.status || result.Code != test.code || result.CleanupFailed != test.cleanup {
				t.Fatal("unsafe transfer or completion classification", result)
			}
			wire, err := json.Marshal(result)
			if err != nil || bytes.Contains(wire, []byte("private invalid response")) {
				t.Fatal("private child output escaped")
			}
		})
	}
}

func TestSensitiveRequestWriteCannotBlockCancellationOrItsDeadline(t *testing.T) {
	for _, name := range []string{"context", "interrupt", "deadline"} {
		t.Run(name, func(t *testing.T) {
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			defer writer.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			interrupt := make(chan struct{})
			entered := make(chan struct{})
			joined := make(chan struct{})
			result := make(chan error, 1)
			// Larger than the pipe's bounded capacity; nobody reads the other end.
			request := bytes.Repeat([]byte("private"), 1<<20)
			defer clear(request)
			grace := 2 * time.Second
			if name == "deadline" {
				grace = 100 * time.Millisecond
			}
			go func() {
				_, err := writeSensitiveProcessRequest(ctx, interrupt, writer, request, grace, func(pipe io.Writer, document []byte) (int, error) {
					defer close(joined)
					close(entered)
					return pipe.Write(document)
				})
				result <- err
			}()
			<-entered
			switch name {
			case "context":
				cancel()
			case "interrupt":
				close(interrupt)
			}
			select {
			case err := <-result:
				if err == nil {
					t.Fatal("unread pipe accepted unbounded input")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("sensitive input prevented cancellation or exceeded its write bound")
			}
			select {
			case <-joined:
			default:
				t.Fatal("owner returned before the writer released the secret")
			}
		})
	}
}

func TestFirstAccountProcessLargeFrameOwnsActualPipeAndClearsInput(t *testing.T) {
	operation := strings.Repeat("e", 64)
	request := firstaccountwire.Request{OperationID: operation, Fingerprint: strings.Repeat("a", 64), Password: []byte("private-frame-password")}
	for i := 0; i < 16; i++ {
		request.Values = append(request.Values, firstaccount.Value{Name: "field" + string(rune('a'+i)), Present: true, Text: bytes.Repeat([]byte("v"), 4096)})
	}
	request.Values = append(request.Values, firstaccount.Value{Name: "document", Present: true, Upload: &firstaccount.Upload{Name: "binary.dat", Content: bytes.Repeat([]byte{0, 255, 10}, 1<<19)}})
	frame, err := firstaccountwire.EncodeRequest(request)
	request.Clear()
	if err != nil {
		t.Fatal(err)
	}
	expected := len(frame)
	if expected <= 64<<10 {
		t.Fatal("fixture does not cross the pipe-size boundary")
	}
	command := firstAccountProcessHelperCommand("respond", t.TempDir())
	process := executeFirstAccountProcess(t.Context(), nil, command, frame, operation)
	if !allZeroCreatesuperuserBytes(frame) || process.requestWriteAttempts != 1 || process.requestBytesWritten != expected || process.directReaps != 1 || process.cleanupFailed {
		t.Fatal("large frame ownership failed", process)
	}
	result := firstAccountProcessOutcome(process, expected, operation)
	process.discardResponse()
	if result.Status != "created" || result.Code != "" {
		t.Fatal("large frame did not reach the exact selected operation", result)
	}
}

func TestFirstAccountProcessClosesBlockedInputAndReapsChild(t *testing.T) {
	operation := strings.Repeat("e", 64)
	request := firstaccountwire.Request{OperationID: operation, Fingerprint: strings.Repeat("a", 64), Password: bytes.Repeat([]byte("p"), 4096)}
	for i := 0; i < 16; i++ {
		request.Values = append(request.Values, firstaccount.Value{Name: "field" + string(rune('a'+i)), Present: true, Text: bytes.Repeat([]byte("v"), 4096)})
	}
	request.Values = append(request.Values, firstaccount.Value{Name: "document", Present: true, Upload: &firstaccount.Upload{Name: "binary.dat", Content: bytes.Repeat([]byte{0, 255, 10}, 1<<19)}})
	frame, err := firstaccountwire.EncodeRequest(request)
	request.Clear()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	command := firstAccountProcessHelperCommand("blocked", root)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan createsuperuserProcessResult, 1)
	writing := make(chan struct{})
	go func() {
		done <- executeOwnedSensitiveProcess(ctx, nil, command, frame, 100*time.Millisecond, createsuperuserProcessHooks{beforeRequestWrite: func() {
			// Keep cold child startup outside the short blocked-write probe.
			// Cancellation must still release this hook when the helper fails.
			for {
				if _, err := os.Stat(filepath.Join(root, "ready")); err == nil {
					break
				}
				select {
				case <-ctx.Done():
					close(writing)
					return
				case <-time.After(10 * time.Millisecond):
				}
			}
			close(writing)
		}}, sensitiveProcessPolicy{
			requestMaximum: firstaccountwire.MaxRequestBytes, responseMaximum: firstaccountwire.MaxResponseBytes,
			completedResponse: func(document []byte) bool {
				_, err := firstaccountwire.DecodeResponse(document, operation)
				return err == nil
			},
		})
	}()
	joined := false
	defer func() {
		cancel()
		if !joined {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("blocked process owner did not join after test failure")
			}
		}
	}()
	waitForFile(t, filepath.Join(root, "ready"))
	<-writing
	cancel()
	process := awaitTerminalResult(t, done)
	joined = true
	if process.directReaps != 1 || !process.started || !allZeroCreatesuperuserBytes(frame) || process.response != nil || process.requestWriteAttempts != 1 {
		t.Fatal("blocked write lost ownership or its secret buffer", process)
	}
	pidBytes, err := os.ReadFile(filepath.Join(root, "ready"))
	pid, parseErr := strconv.Atoi(string(pidBytes))
	if err != nil || parseErr != nil || runserverProcessGroupExists(pid) {
		t.Fatal("blocked input retained a child process group")
	}
}

func firstAccountProcessHelperCommand(mode, root string) createsuperuserProcessCommand {
	environment := environmentValues(os.Environ())
	environment["GODJ_FIRST_ACCOUNT_PROCESS_HELPER"] = mode
	environment["GODJ_FIRST_ACCOUNT_PROCESS_ROOT"] = root
	return newCreatesuperuserProcessCommand(root, []string{os.Args[0], "-test.run=^TestFirstAccountProcessHelper$"}, sortedEnvironment(environment))
}

func TestFirstAccountProcessHelper(t *testing.T) {
	mode := os.Getenv("GODJ_FIRST_ACCOUNT_PROCESS_HELPER")
	if mode == "" {
		return
	}
	if mode == "blocked" {
		if err := os.WriteFile(filepath.Join(os.Getenv("GODJ_FIRST_ACCOUNT_PROCESS_ROOT"), "ready"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			os.Exit(90)
		}
		for {
			time.Sleep(time.Second)
		}
	}
	if mode != "respond" {
		os.Exit(89)
	}
	request, err := firstaccountwire.ReadRequest(os.Stdin)
	if err != nil {
		os.Exit(91)
	}
	for _, value := range append(append([]string(nil), os.Args...), os.Environ()...) {
		if len(request.Password) > 0 && strings.Contains(value, string(request.Password)) {
			os.Exit(92)
		}
	}
	operation := request.OperationID
	request.Clear()
	response, err := firstaccountwire.EncodeResponse(firstaccountwire.Response{Version: firstaccountprotocol.Version, OperationID: operation, Outcome: firstaccountwire.Created})
	if err != nil || firstaccountwire.Write(os.Stdout, response) != nil {
		os.Exit(93)
	}
	os.Exit(0)
}
