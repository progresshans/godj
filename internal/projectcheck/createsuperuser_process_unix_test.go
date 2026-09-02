//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/internal/projectcheck/createsuperuserprotocol"
	"golang.org/x/sys/unix"
)

const createsuperuserProcessHelperEnvironment = "GODJ_CREATESUPERUSER_PROCESS_HELPER"

func TestCreatesuperuserProcessHelper(t *testing.T) {
	mode := os.Getenv(createsuperuserProcessHelperEnvironment)
	if mode == "" {
		return
	}
	switch mode {
	case "respond":
		document := readCreatesuperuserHelperRequest()
		verifyCreatesuperuserHelperRequest(document)
		if os.Getenv("GODJ_CREATESUPERUSER_HELPER_STDERR_REQUEST") == "1" {
			_, _ = os.Stderr.Write(document)
		}
		clear(document)
		wire := os.Getenv("GODJ_CREATESUPERUSER_HELPER_WIRE")
		_, _ = io.WriteString(os.Stdout, wire)
		os.Exit(0)
	case "emit":
		document := readCreatesuperuserHelperRequest()
		verifyCreatesuperuserHelperRequest(document)
		if os.Getenv("GODJ_CREATESUPERUSER_HELPER_STDOUT_REQUEST") == "1" {
			_, _ = os.Stdout.Write(document)
		}
		if os.Getenv("GODJ_CREATESUPERUSER_HELPER_STDERR_REQUEST") == "1" {
			_, _ = os.Stderr.Write(document)
		}
		clear(document)
		stdoutBytes, _ := strconv.Atoi(os.Getenv("GODJ_CREATESUPERUSER_HELPER_STDOUT_BYTES"))
		stderrBytes, _ := strconv.Atoi(os.Getenv("GODJ_CREATESUPERUSER_HELPER_STDERR_BYTES"))
		_, _ = io.CopyN(os.Stdout, strings.NewReader(strings.Repeat("x", stdoutBytes)), int64(stdoutBytes))
		_, _ = io.CopyN(os.Stderr, strings.NewReader(strings.Repeat("y", stderrBytes)), int64(stderrBytes))
		exitCode, _ := strconv.Atoi(os.Getenv("GODJ_CREATESUPERUSER_HELPER_EXIT"))
		os.Exit(exitCode)
	case "overflow-hold":
		document := readCreatesuperuserHelperRequest()
		verifyCreatesuperuserHelperRequest(document)
		// Exercise the process owner rather than relying on the kernel's default
		// broken-pipe or interrupt termination. The owner must observe its own
		// cap, interrupt this child, exhaust the grace, and force-reap it.
		signal.Ignore(os.Interrupt, unix.SIGPIPE)
		_, _ = os.Stdout.Write(document)
		clear(document)
		_, _ = io.CopyN(os.Stdout, strings.NewReader(strings.Repeat("x", 8<<20)), 8<<20)
		for {
			time.Sleep(time.Second)
		}
	case "graceful":
		document := readCreatesuperuserHelperRequest()
		verifyCreatesuperuserHelperRequest(document)
		clear(document)
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, os.Interrupt)
		defer signal.Stop(signals)
		if err := publishHelperReady(strconv.Itoa(os.Getpid())); err != nil {
			os.Exit(96)
		}
		<-signals
		delay, _ := time.ParseDuration(os.Getenv("GODJ_CREATESUPERUSER_HELPER_EXIT_DELAY"))
		if delay > 0 {
			time.Sleep(delay)
		}
		os.Exit(0)
	case "respond-after-interrupt":
		document := readCreatesuperuserHelperRequest()
		verifyCreatesuperuserHelperRequest(document)
		clear(document)
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, os.Interrupt)
		defer signal.Stop(signals)
		if err := publishHelperReady(strconv.Itoa(os.Getpid())); err != nil {
			os.Exit(96)
		}
		<-signals
		_, _ = io.WriteString(os.Stdout, os.Getenv("GODJ_CREATESUPERUSER_HELPER_WIRE"))
		os.Exit(0)
	case "ignore":
		document := readCreatesuperuserHelperRequest()
		verifyCreatesuperuserHelperRequest(document)
		clear(document)
		signal.Ignore(os.Interrupt)
		if err := publishHelperReady(strconv.Itoa(os.Getpid())); err != nil {
			os.Exit(96)
		}
		for {
			time.Sleep(time.Second)
		}
	case "close-input":
		_ = os.Stdin.Close()
		if err := publishHelperReady(strconv.Itoa(os.Getpid())); err != nil {
			os.Exit(96)
		}
		for {
			time.Sleep(time.Second)
		}
	case "spawn-holder":
		document := readCreatesuperuserHelperRequest()
		verifyCreatesuperuserHelperRequest(document)
		clear(document)
		_, _ = io.WriteString(os.Stdout, os.Getenv("GODJ_CREATESUPERUSER_HELPER_WIRE"))
		environment := environmentValues(os.Environ())
		environment[createsuperuserProcessHelperEnvironment] = "hold"
		if holderReady := os.Getenv("GODJ_HELPER_HOLDER_READY"); holderReady != "" {
			environment["GODJ_HELPER_READY"] = holderReady
		}
		child := exec.Command(os.Args[0], "-test.run=^TestCreatesuperuserProcessHelper$")
		child.Env = sortedEnvironment(environment)
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(97)
		}
		if ready := os.Getenv("GODJ_HELPER_READY"); ready != "" {
			payload := strconv.Itoa(os.Getpid()) + "," + strconv.Itoa(child.Process.Pid)
			if err := publishHelperReady(payload); err != nil {
				os.Exit(96)
			}
		}
		os.Exit(0)
	case "hold":
		signal.Ignore(os.Interrupt)
		if err := publishHelperReady(strconv.Itoa(os.Getpid())); err != nil {
			os.Exit(96)
		}
		for {
			time.Sleep(time.Second)
		}
	default:
		os.Exit(98)
	}
}

func TestCreatesuperuserProcessWritesOneRequestClearsItAndRetainsOnlyCanonicalResponse(t *testing.T) {
	marker := []byte("sensitive-process-password-marker")
	frame := createsuperuserProcessFrame(t, []byte("operator"), marker)
	frameLength := len(frame)
	digest := sha256.Sum256(frame)
	wire, err := createsuperuserprotocol.EncodeResponse(createsuperuserprotocol.Response{OK: true, Created: true})
	if err != nil {
		t.Fatal(err)
	}
	command := createsuperuserProcessHelperCommand("respond", map[string]string{
		"GODJ_CREATESUPERUSER_HELPER_REQUEST_SHA256": hex.EncodeToString(digest[:]),
		"GODJ_CREATESUPERUSER_HELPER_WIRE":           string(wire),
		"GODJ_CREATESUPERUSER_HELPER_STDERR_REQUEST": "1",
	})
	assertCreatesuperuserCommandOmitsBytes(t, command, marker)

	writeCalls := 0
	clearCalls := 0
	result := executeOwnedCreatesuperuserProcessWithHooks(
		context.Background(),
		nil,
		command,
		frame,
		time.Second,
		createsuperuserProcessHooks{
			writeRequest: func(writer io.Writer, document []byte) (int, error) {
				writeCalls++
				return writer.Write(document)
			},
			afterRequestClear: func() {
				clearCalls++
				if !allZeroCreatesuperuserBytes(frame) {
					t.Error("owned request was not cleared immediately after its write attempt")
				}
			},
		},
	)
	if writeCalls != 1 || clearCalls != 1 || result.requestWriteAttempts != 1 || result.requestBytesWritten != frameLength {
		t.Fatalf("request ownership = writes %d/%d clear %d bytes %d/%d", writeCalls, result.requestWriteAttempts, clearCalls, result.requestBytesWritten, frameLength)
	}
	if !result.started || !result.exited || result.exitCode != 0 || result.directReaps != 1 || result.failure != nil || result.cleanupFailed || result.sigintAttempts != 0 || result.sigkillAttempts != 0 {
		t.Fatalf("completed sensitive process = %+v", result)
	}
	if result.stdoutScalar != (StreamScalar{RetainedBytes: len(wire)}) || result.stderrScalar != (StreamScalar{RetainedBytes: frameLength}) {
		t.Fatalf("stream scalars = stdout %+v stderr %+v", result.stdoutScalar, result.stderrScalar)
	}
	formatted := fmt.Sprintf("%+v %#v", result, result)
	if strings.Contains(formatted, string(marker)) || strings.Contains(formatted, string(wire)) {
		t.Fatalf("formatted result retained raw child/request bytes: %q", formatted)
	}
	response := result.takeResponse()
	defer clear(response)
	if !bytes.Equal(response, wire) || result.response != nil {
		t.Fatalf("owned response transfer = %q remaining=%d", response, len(result.response))
	}
}

func TestCreatesuperuserProcessDiscardResponseClearsOwnedBuffer(t *testing.T) {
	owned := []byte("untrusted-private-response-secret")
	retainedView := owned
	result := createsuperuserProcessResult{response: owned}
	result.discardResponse()
	if result.response != nil || !allZeroCreatesuperuserBytes(retainedView) {
		t.Fatal("discarded private response was not cleared")
	}
}

func TestCreatesuperuserProcessClearsRequestBeforePreCanceledReturn(t *testing.T) {
	frame := createsuperuserProcessFrame(t, []byte("operator"), []byte("pre-cancel-secret"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	clearCalls := 0
	result := executeOwnedCreatesuperuserProcessWithHooks(
		ctx,
		nil,
		createsuperuserProcessHelperCommand("respond", nil),
		frame,
		time.Second,
		createsuperuserProcessHooks{afterRequestClear: func() {
			clearCalls++
			if !allZeroCreatesuperuserBytes(frame) {
				t.Error("pre-canceled request was not cleared")
			}
		}},
	)
	if clearCalls != 1 || result.started || result.directReaps != 0 || result.requestWriteAttempts != 0 || result.failure == nil || result.failure.Category != createsuperuserprotocol.CategoryProcess || result.failure.Code != createsuperuserprotocol.CodeProjectCanceled {
		t.Fatalf("pre-canceled sensitive process = %+v clear=%d", result, clearCalls)
	}
}

func TestCreatesuperuserProcessStartFailureIsSecretFreeAndLeftForCallerRunnerClassification(t *testing.T) {
	frame := createsuperuserProcessFrame(t, []byte("operator"), []byte("start-failure-secret"))
	result := executeOwnedCreatesuperuserProcessWithHooks(
		context.Background(),
		nil,
		newCreatesuperuserProcessCommand("", []string{filepath.Join(t.TempDir(), "missing-runner")}, nil),
		frame,
		time.Second,
		createsuperuserProcessHooks{},
	)
	if !allZeroCreatesuperuserBytes(frame) || result.started || result.directReaps != 0 || result.requestWriteAttempts != 0 || result.failure != nil || result.response != nil || result.exitCode != -1 {
		t.Fatalf("start failure boundary = %+v response=%d", result, len(result.response))
	}
	if strings.Contains(fmt.Sprintf("%+v %#v", result, result), "start-failure-secret") {
		t.Fatal("start failure retained secret")
	}
}

func TestCreatesuperuserProcessTreatsShortAndFailedPipeWritesAsRedactedTransportFailures(t *testing.T) {
	for _, test := range []struct {
		name        string
		write       func(io.Writer, []byte) (int, error)
		wantWritten func(int) int
	}{
		{
			name: "short",
			write: func(writer io.Writer, document []byte) (int, error) {
				return writer.Write(document[:len(document)-1])
			},
			wantWritten: func(length int) int { return length - 1 },
		},
		{
			name: "error",
			write: func(_ io.Writer, _ []byte) (int, error) {
				return 0, errors.New("raw-pipe-cause-secret")
			},
			wantWritten: func(int) int { return 0 },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			frame := createsuperuserProcessFrame(t, []byte("operator"), []byte("short-write-secret"))
			length := len(frame)
			result := executeOwnedCreatesuperuserProcessWithHooks(
				context.Background(),
				nil,
				createsuperuserProcessHelperCommand("respond", nil),
				frame,
				75*time.Millisecond,
				createsuperuserProcessHooks{writeRequest: test.write},
			)
			if !allZeroCreatesuperuserBytes(frame) || !result.started || result.directReaps != 1 || result.requestWriteAttempts != 1 || result.requestBytesWritten != test.wantWritten(length) {
				t.Fatalf("failed request transport lifecycle = %+v", result)
			}
			if result.failure == nil || result.failure.Category != createsuperuserprotocol.CategoryProcess || result.failure.Code != createsuperuserprotocol.CodeSensitiveInputTransportFailed || result.response != nil {
				t.Fatalf("failed request transport outcome = %+v response=%d", result, len(result.response))
			}
			formatted := fmt.Sprintf("%+v %#v", result, result)
			if strings.Contains(formatted, "short-write-secret") || strings.Contains(formatted, "raw-pipe-cause-secret") {
				t.Fatalf("transport failure leaked raw material: %q", formatted)
			}
		})
	}
}

func TestCreatesuperuserProcessOwnPipeFailureIsOneAttemptAndClearsRequest(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "stdin-closed")
	frame := createsuperuserProcessFrame(t, []byte("operator"), []byte("actual-pipe-secret"))
	result := executeOwnedCreatesuperuserProcessWithHooks(
		context.Background(),
		nil,
		createsuperuserProcessHelperCommand("close-input", map[string]string{"GODJ_HELPER_READY": ready}),
		frame,
		time.Second,
		createsuperuserProcessHooks{beforeRequestWrite: func() { waitForFile(t, ready) }},
	)
	if !allZeroCreatesuperuserBytes(frame) || !result.started || result.directReaps != 1 || result.requestWriteAttempts != 1 || result.requestBytesWritten != 0 {
		t.Fatalf("actual failed-pipe lifecycle = %+v", result)
	}
	if result.failure == nil || result.failure.Category != createsuperuserprotocol.CategoryProcess || result.failure.Code != createsuperuserprotocol.CodeSensitiveInputTransportFailed || result.response != nil {
		t.Fatalf("actual failed-pipe outcome = %+v response=%d", result, len(result.response))
	}
	if strings.Contains(fmt.Sprintf("%+v %#v", result, result), "actual-pipe-secret") {
		t.Fatal("actual pipe failure leaked secret")
	}
}

func TestCreatesuperuserProcessBoundsStdoutAtProtocolLimitAndDiscardsRawStderr(t *testing.T) {
	marker := []byte("stderr-secret-marker")
	frame := createsuperuserProcessFrame(t, []byte("operator"), marker)
	frameLength := len(frame)
	digest := sha256.Sum256(frame)
	command := createsuperuserProcessHelperCommand("emit", map[string]string{
		"GODJ_CREATESUPERUSER_HELPER_REQUEST_SHA256": hex.EncodeToString(digest[:]),
		"GODJ_CREATESUPERUSER_HELPER_STDOUT_BYTES":   strconv.Itoa(createsuperuserprotocol.MaxResponseBytes + 1),
		"GODJ_CREATESUPERUSER_HELPER_STDERR_REQUEST": "1",
	})
	result := executeOwnedCreatesuperuserProcessWithHooks(context.Background(), nil, command, frame, time.Second, createsuperuserProcessHooks{})
	if !result.started || result.directReaps != 1 || result.failure != nil || result.cleanupFailed {
		t.Fatalf("bounded output process = %+v", result)
	}
	if result.stdoutScalar != (StreamScalar{RetainedBytes: createsuperuserprotocol.MaxResponseBytes, Truncated: true}) || result.response != nil {
		t.Fatalf("stdout bound = %+v response=%d", result.stdoutScalar, len(result.response))
	}
	if result.stderrScalar != (StreamScalar{RetainedBytes: frameLength}) {
		t.Fatalf("stderr scalar = %+v", result.stderrScalar)
	}
	if strings.Contains(fmt.Sprintf("%+v %#v", result, result), string(marker)) {
		t.Fatal("formatted bounded output result leaked stderr/request marker")
	}
	result.discardResponse()
}

func TestCreatesuperuserProcessBoundsDiscardedStderrWithOneByteOverflowDetection(t *testing.T) {
	frame := createsuperuserProcessFrame(t, []byte("operator"), []byte("stderr-bound-secret"))
	digest := sha256.Sum256(frame)
	wire, err := createsuperuserprotocol.EncodeResponse(createsuperuserprotocol.Response{OK: true, Created: true})
	if err != nil {
		t.Fatal(err)
	}
	command := createsuperuserProcessHelperCommand("emit", map[string]string{
		"GODJ_CREATESUPERUSER_HELPER_REQUEST_SHA256": hex.EncodeToString(digest[:]),
		"GODJ_CREATESUPERUSER_HELPER_STDOUT_BYTES":   strconv.Itoa(len(wire)),
		"GODJ_CREATESUPERUSER_HELPER_STDERR_BYTES":   strconv.Itoa(maxDiagnosticBytes + 1),
	})
	result := executeOwnedCreatesuperuserProcessWithHooks(context.Background(), nil, command, frame, time.Second, createsuperuserProcessHooks{})
	if !result.started || result.directReaps != 1 || result.failure != nil || result.cleanupFailed {
		t.Fatalf("bounded stderr process = %+v", result)
	}
	if result.stderrScalar != (StreamScalar{RetainedBytes: maxDiagnosticBytes, Truncated: true}) {
		t.Fatalf("stderr bound = %+v", result.stderrScalar)
	}
	result.discardResponse()
}

func TestCreatesuperuserProcessTerminatesAndReapsChildThatIgnoresOverCapOutputSignals(t *testing.T) {
	marker := []byte("over-cap-private-response-secret")
	frame := createsuperuserProcessFrame(t, []byte("operator"), marker)
	digest := sha256.Sum256(frame)
	command := createsuperuserProcessHelperCommand("overflow-hold", map[string]string{
		"GODJ_CREATESUPERUSER_HELPER_REQUEST_SHA256": hex.EncodeToString(digest[:]),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan createsuperuserProcessResult, 1)
	grace := 75 * time.Millisecond
	started := time.Now()
	go func() {
		done <- executeOwnedCreatesuperuserProcessWithHooks(
			ctx,
			nil,
			command,
			frame,
			grace,
			createsuperuserProcessHooks{},
		)
	}()

	var result createsuperuserProcessResult
	select {
	case result = <-done:
	case <-time.After(3 * time.Second):
		cancel()
		select {
		case result = <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("over-cap sensitive child escaped bounded cancellation cleanup")
		}
		t.Fatal("over-cap sensitive child was not terminated by its output bound")
	}
	if elapsed := time.Since(started); elapsed >= 3*time.Second {
		t.Fatalf("over-cap sensitive child exceeded bounded cleanup: %s", elapsed)
	}
	if !allZeroCreatesuperuserBytes(frame) || !result.started || result.exited || result.exitCode == 0 ||
		result.directReaps != 1 || result.failure != nil || result.cleanupFailed ||
		result.sigintAttempts != 1 || result.sigkillAttempts != 1 {
		t.Fatalf("over-cap sensitive child lifecycle = %+v", result)
	}
	wantStdout := StreamScalar{RetainedBytes: createsuperuserprotocol.MaxResponseBytes, Truncated: true}
	if result.stdoutScalar != wantStdout || result.stderrScalar != (StreamScalar{}) || result.response != nil {
		t.Fatalf("over-cap sensitive output = stdout %+v stderr %+v response=%d", result.stdoutScalar, result.stderrScalar, len(result.response))
	}
	formatted := fmt.Sprintf("%+v %#v", result, result)
	if strings.Contains(formatted, string(marker)) {
		t.Fatalf("over-cap sensitive child published private output: %q", formatted)
	}
}

func TestCreatesuperuserProcessDiscardsRawStdoutFromNonzeroRunner(t *testing.T) {
	marker := []byte("nonzero-stdout-secret")
	frame := createsuperuserProcessFrame(t, []byte("operator"), marker)
	frameLength := len(frame)
	digest := sha256.Sum256(frame)
	command := createsuperuserProcessHelperCommand("emit", map[string]string{
		"GODJ_CREATESUPERUSER_HELPER_REQUEST_SHA256": hex.EncodeToString(digest[:]),
		"GODJ_CREATESUPERUSER_HELPER_STDOUT_REQUEST": "1",
		"GODJ_CREATESUPERUSER_HELPER_EXIT":           "7",
	})
	result := executeOwnedCreatesuperuserProcessWithHooks(context.Background(), nil, command, frame, time.Second, createsuperuserProcessHooks{})
	if !result.started || !result.exited || result.exitCode != 7 || result.directReaps != 1 || result.failure != nil || result.cleanupFailed || result.response != nil || result.stdoutScalar != (StreamScalar{RetainedBytes: frameLength}) {
		t.Fatalf("nonzero runner boundary = %+v response=%d", result, len(result.response))
	}
	if strings.Contains(fmt.Sprintf("%+v %#v", result, result), string(marker)) {
		t.Fatal("nonzero runner retained raw stdout secret")
	}
}

func TestCreatesuperuserProcessInterruptAllowsGracefulGroupExitAndReapsOnce(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	frame := createsuperuserProcessFrame(t, []byte("operator"), []byte("graceful-secret"))
	digest := sha256.Sum256(frame)
	command := createsuperuserProcessHelperCommand("graceful", map[string]string{
		"GODJ_CREATESUPERUSER_HELPER_REQUEST_SHA256": hex.EncodeToString(digest[:]),
		"GODJ_HELPER_READY":                          ready,
		"GODJ_CREATESUPERUSER_HELPER_EXIT_DELAY":     "25ms",
	})
	interrupt := make(chan struct{})
	done := make(chan createsuperuserProcessResult, 1)
	// The race runtime can delay helper signal delivery substantially on hosted
	// macOS runners; keep the product assertion cooperative while leaving ample
	// room below the owner's 15-second production grace.
	grace := 5 * time.Second
	go func() {
		done <- executeOwnedCreatesuperuserProcessWithHooks(context.Background(), interrupt, command, frame, grace, createsuperuserProcessHooks{})
	}()
	waitForFile(t, ready)
	started := time.Now()
	close(interrupt)
	result := <-done
	if result.failure == nil || result.failure.Code != createsuperuserprotocol.CodeProjectInterrupted || result.sigintAttempts != 1 || result.sigkillAttempts != 0 || result.directReaps != 1 || !result.started || result.cleanupFailed {
		t.Fatalf("graceful interrupt = %+v", result)
	}
	if elapsed := time.Since(started); elapsed >= grace {
		t.Fatalf("graceful interrupt exhausted grace: %s", elapsed)
	}
}

func TestCreatesuperuserProcessPreservesCompleteResponsePublishedDuringInterruptGrace(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "response-after-interrupt-ready")
	marker := []byte("response-after-interrupt-private-secret")
	frame := createsuperuserProcessFrame(t, []byte("operator"), marker)
	digest := sha256.Sum256(frame)
	wire, err := createsuperuserprotocol.EncodeResponse(createsuperuserprotocol.Response{OK: true, Created: true})
	if err != nil {
		t.Fatal(err)
	}
	command := createsuperuserProcessHelperCommand("respond-after-interrupt", map[string]string{
		"GODJ_CREATESUPERUSER_HELPER_REQUEST_SHA256": hex.EncodeToString(digest[:]),
		"GODJ_CREATESUPERUSER_HELPER_WIRE":           string(wire),
		"GODJ_HELPER_READY":                          ready,
	})
	interrupt := make(chan struct{})
	done := make(chan createsuperuserProcessResult, 1)
	go func() {
		done <- executeOwnedCreatesuperuserProcessWithHooks(
			context.Background(),
			interrupt,
			command,
			frame,
			5*time.Second,
			createsuperuserProcessHooks{},
		)
	}()
	waitForFile(t, ready)
	close(interrupt)

	var result createsuperuserProcessResult
	select {
	case result = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("complete response was not reconciled during interrupt grace")
	}
	if !allZeroCreatesuperuserBytes(frame) || !result.started || result.exitCode != 0 ||
		result.directReaps != 1 || result.failure != nil || result.cleanupFailed ||
		result.sigintAttempts != 1 || result.sigkillAttempts != 0 ||
		result.stdoutScalar != (StreamScalar{RetainedBytes: len(wire)}) ||
		result.stderrScalar != (StreamScalar{}) {
		t.Fatalf("response-after-interrupt lifecycle = %+v", result)
	}
	response := result.takeResponse()
	defer clear(response)
	if !bytes.Equal(response, wire) || result.response != nil {
		t.Fatalf("response-after-interrupt wire = %q remaining=%d", response, len(result.response))
	}
	formatted := fmt.Sprintf("%+v %#v", result, result)
	if strings.Contains(formatted, string(marker)) || strings.Contains(formatted, string(wire)) {
		t.Fatalf("response-after-interrupt formatting exposed private bytes: %q", formatted)
	}
}

func TestCreatesuperuserProcessKeepsInterruptWhenGraceResponseIsPartial(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "partial-after-interrupt-ready")
	marker := []byte("partial-after-interrupt-private-secret")
	frame := createsuperuserProcessFrame(t, []byte("operator"), marker)
	digest := sha256.Sum256(frame)
	partial := `{"protocol_version":1,"status":"ok"`
	command := createsuperuserProcessHelperCommand("respond-after-interrupt", map[string]string{
		"GODJ_CREATESUPERUSER_HELPER_REQUEST_SHA256": hex.EncodeToString(digest[:]),
		"GODJ_CREATESUPERUSER_HELPER_WIRE":           partial,
		"GODJ_HELPER_READY":                          ready,
	})
	interrupt := make(chan struct{})
	done := make(chan createsuperuserProcessResult, 1)
	go func() {
		done <- executeOwnedCreatesuperuserProcessWithHooks(
			context.Background(),
			interrupt,
			command,
			frame,
			5*time.Second,
			createsuperuserProcessHooks{},
		)
	}()
	waitForFile(t, ready)
	close(interrupt)

	var result createsuperuserProcessResult
	select {
	case result = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("partial response was not reconciled during interrupt grace")
	}
	wantFailure := CreatesuperuserFailure{
		Category: createsuperuserprotocol.CategoryProcess,
		Code:     createsuperuserprotocol.CodeProjectInterrupted,
	}
	if !allZeroCreatesuperuserBytes(frame) || !result.started || result.exitCode != 0 ||
		result.directReaps != 1 || result.failure == nil || *result.failure != wantFailure ||
		result.cleanupFailed || result.sigintAttempts != 1 || result.sigkillAttempts != 0 ||
		result.stdoutScalar != (StreamScalar{RetainedBytes: len(partial)}) || result.response != nil {
		t.Fatalf("partial-response interrupt lifecycle = %+v response=%d", result, len(result.response))
	}
	formatted := fmt.Sprintf("%+v %#v", result, result)
	if strings.Contains(formatted, string(marker)) || strings.Contains(formatted, partial) {
		t.Fatalf("partial-response interrupt formatting exposed private bytes: %q", formatted)
	}
}

func TestCreatesuperuserProcessCancellationEscalatesAfterGraceAndReapsOnce(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	frame := createsuperuserProcessFrame(t, []byte("operator"), []byte("forced-secret"))
	digest := sha256.Sum256(frame)
	command := createsuperuserProcessHelperCommand("ignore", map[string]string{
		"GODJ_CREATESUPERUSER_HELPER_REQUEST_SHA256": hex.EncodeToString(digest[:]),
		"GODJ_HELPER_READY":                          ready,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan createsuperuserProcessResult, 1)
	grace := 75 * time.Millisecond
	go func() {
		done <- executeOwnedCreatesuperuserProcessWithHooks(ctx, nil, command, frame, grace, createsuperuserProcessHooks{})
	}()
	waitForFile(t, ready)
	started := time.Now()
	cancel()
	result := <-done
	if result.failure == nil || result.failure.Code != createsuperuserprotocol.CodeProjectCanceled || result.sigintAttempts != 1 || result.sigkillAttempts != 1 || result.directReaps != 1 || !result.started || result.cleanupFailed {
		t.Fatalf("forced cancellation = %+v", result)
	}
	if elapsed := time.Since(started); elapsed < grace {
		t.Fatalf("forced cancellation skipped grace: %s", elapsed)
	}
}

func TestCreatesuperuserProcessKillsQuietDescendantHoldingResponsePipesAfterDirectExit(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "process-pair")
	holderReady := filepath.Join(t.TempDir(), "holder-ready")
	frame := createsuperuserProcessFrame(t, []byte("operator"), []byte("held-pipe-secret"))
	digest := sha256.Sum256(frame)
	wire, err := createsuperuserprotocol.EncodeResponse(createsuperuserprotocol.Response{OK: true, Created: true})
	if err != nil {
		t.Fatal(err)
	}
	command := createsuperuserProcessHelperCommand("spawn-holder", map[string]string{
		"GODJ_CREATESUPERUSER_HELPER_REQUEST_SHA256": hex.EncodeToString(digest[:]),
		"GODJ_CREATESUPERUSER_HELPER_WIRE":           string(wire),
		"GODJ_HELPER_READY":                          ready,
		"GODJ_HELPER_HOLDER_READY":                   holderReady,
	})
	done := make(chan createsuperuserProcessResult, 1)
	grace := 75 * time.Millisecond
	go func() {
		done <- executeOwnedCreatesuperuserProcessWithHooks(context.Background(), nil, command, frame, grace, createsuperuserProcessHooks{})
	}()
	groupPID, descendantPID := createsuperuserProcessPair(t, ready)
	waitForFile(t, holderReady)
	t.Cleanup(func() {
		if runserverProcessGroupExists(groupPID) {
			_ = unix.Kill(-groupPID, unix.SIGKILL)
		}
		_ = unix.Kill(descendantPID, unix.SIGKILL)
	})

	select {
	case result := <-done:
		if !result.started || !result.exited || result.exitCode != 0 || result.directReaps != 1 || result.failure != nil || result.cleanupFailed || result.sigintAttempts != 0 || result.sigkillAttempts != 1 {
			t.Fatalf("held-pipe descendant cleanup = %+v", result)
		}
		if runserverProcessGroupExists(groupPID) {
			t.Fatalf("createsuperuser process group %d remains", groupPID)
		}
		response := result.takeResponse()
		defer clear(response)
		if !bytes.Equal(response, wire) {
			t.Fatalf("held-pipe response = %q", response)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("held-pipe descendant exceeded bounded cleanup")
	}
}

func TestCreatesuperuserProcessCompletedResponseWinsConcurrentCancellation(t *testing.T) {
	frame := createsuperuserProcessFrame(t, []byte("operator"), []byte("terminal-race-secret"))
	digest := sha256.Sum256(frame)
	wire, err := createsuperuserprotocol.EncodeResponse(createsuperuserprotocol.Response{OK: true, Created: true})
	if err != nil {
		t.Fatal(err)
	}
	command := createsuperuserProcessHelperCommand("respond", map[string]string{
		"GODJ_CREATESUPERUSER_HELPER_REQUEST_SHA256": hex.EncodeToString(digest[:]),
		"GODJ_CREATESUPERUSER_HELPER_WIRE":           string(wire),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	terminalReady := make(chan struct{})
	releaseTerminal := make(chan struct{})
	done := make(chan createsuperuserProcessResult, 1)
	go func() {
		done <- executeOwnedCreatesuperuserProcessWithHooks(ctx, nil, command, frame, time.Second, createsuperuserProcessHooks{
			beforeTerminalReturn: func() {
				close(terminalReady)
				<-releaseTerminal
			},
		})
	}()
	select {
	case <-terminalReady:
	case <-time.After(5 * time.Second):
		t.Fatal("sensitive process did not reach terminal boundary")
	}
	cancel()
	close(releaseTerminal)
	result := <-done
	if !result.started || !result.exited || result.exitCode != 0 || result.directReaps != 1 || result.failure != nil || result.cleanupFailed || result.sigintAttempts != 0 || result.sigkillAttempts != 0 {
		t.Fatalf("terminal response arbitration = %+v", result)
	}
	result.discardResponse()
}

func readCreatesuperuserHelperRequest() []byte {
	document, err := io.ReadAll(io.LimitReader(os.Stdin, int64(createsuperuserprotocol.MaxRequestBytes+1)))
	if err != nil || len(document) == 0 || len(document) > createsuperuserprotocol.MaxRequestBytes {
		clear(document)
		os.Exit(99)
	}
	return document
}

func verifyCreatesuperuserHelperRequest(document []byte) {
	wantDigest := os.Getenv("GODJ_CREATESUPERUSER_HELPER_REQUEST_SHA256")
	if wantDigest != "" {
		digest := sha256.Sum256(document)
		if hex.EncodeToString(digest[:]) != wantDigest {
			clear(document)
			os.Exit(94)
		}
	}
	request, failure, failed := createsuperuserprotocol.DecodeRequest(document)
	if failed || failure != (createsuperuserprotocol.Failure{}) {
		clear(document)
		os.Exit(93)
	}
	defer request.Clear()
	for _, argument := range os.Args {
		if createsuperuserStringContainsBytes(argument, request.Password) {
			clear(document)
			os.Exit(92)
		}
	}
	for _, entry := range os.Environ() {
		if createsuperuserStringContainsBytes(entry, request.Password) {
			clear(document)
			os.Exit(91)
		}
	}
	workingDirectory, err := os.Getwd()
	if err != nil || createsuperuserStringContainsBytes(workingDirectory, request.Password) {
		clear(document)
		os.Exit(90)
	}
}

func createsuperuserStringContainsBytes(value string, target []byte) bool {
	if len(target) == 0 || len(target) > len(value) {
		return false
	}
	for offset := 0; offset+len(target) <= len(value); offset++ {
		matches := true
		for index := range target {
			if value[offset+index] != target[index] {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

func createsuperuserProcessFrame(t *testing.T, username, password []byte) []byte {
	t.Helper()
	request := createsuperuserprotocol.Request{
		Username: append([]byte(nil), username...),
		Password: append([]byte(nil), password...),
	}
	defer request.Clear()
	frame, err := createsuperuserprotocol.EncodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

func createsuperuserProcessHelperCommand(mode string, extra map[string]string) createsuperuserProcessCommand {
	environment := environmentValues(os.Environ())
	environment[createsuperuserProcessHelperEnvironment] = mode
	for key, value := range extra {
		environment[key] = value
	}
	return newCreatesuperuserProcessCommand(
		"",
		[]string{os.Args[0], "-test.run=^TestCreatesuperuserProcessHelper$"},
		sortedEnvironment(environment),
	)
}

func assertCreatesuperuserCommandOmitsBytes(t *testing.T, command createsuperuserProcessCommand, target []byte) {
	t.Helper()
	if createsuperuserStringContainsBytes(command.dir, target) {
		t.Fatal("secret appears in child working directory")
	}
	for _, value := range command.argv {
		if createsuperuserStringContainsBytes(value, target) {
			t.Fatal("secret appears in child argv")
		}
	}
	for _, value := range command.env {
		if createsuperuserStringContainsBytes(value, target) {
			t.Fatal("secret appears in child environment")
		}
	}
}

func allZeroCreatesuperuserBytes(document []byte) bool {
	for _, value := range document {
		if value != 0 {
			return false
		}
	}
	return true
}

func createsuperuserProcessPair(t *testing.T, path string) (int, int) {
	t.Helper()
	waitForFile(t, path)
	document, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(document), ",")
	if len(parts) != 2 {
		t.Fatalf("invalid createsuperuser helper pair %q", document)
	}
	groupPID, err := strconv.Atoi(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	descendantPID, err := strconv.Atoi(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	return groupPID, descendantPID
}
