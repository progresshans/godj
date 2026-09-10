//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/progresshans/godj/internal/projectcheck/createsuperuserprotocol"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

func TestReadCreatesuperuserTerminalUsesExactPromptsPreservesPasswordAndDoesNotPersistInputToRedirectedStderr(t *testing.T) {
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

	prompts := newObservedPromptWriter()

	type result struct {
		username []byte
		password []byte
		failure  *CreatesuperuserFailure
		report   CreatesuperuserReport
	}
	resultReady := make(chan result, 1)
	go func() {
		var report CreatesuperuserReport
		username, password, failure := readCreatesuperuserTerminal(
			context.Background(), nil, slave, prompts, &report,
		)
		resultReady <- result{username: username, password: password, failure: failure, report: report}
	}()

	waitForPrompt(t, prompts, "Username: ")
	writePTYInput(t, master, "operator-marker\n")
	waitForPrompt(t, prompts, "Password: ")
	writePTYInput(t, master, "  password-marker  \n")
	waitForPrompt(t, prompts, "Password (again): ")
	writePTYInput(t, master, "  password-marker  \n")

	got := awaitTerminalResult(t, resultReady)
	if got.failure != nil || !bytes.Equal(got.username, []byte("operator-marker")) ||
		!bytes.Equal(got.password, []byte("  password-marker  ")) {
		t.Fatalf("terminal result = username %q password-len %d failure %+v", got.username, len(got.password), got.failure)
	}
	if got.report.TerminalChecks != 1 || got.report.UsernamePromptWrites != 1 ||
		got.report.PasswordPromptWrites != 1 || got.report.ConfirmationPromptWrites != 1 ||
		got.report.TerminalRestoreAttempts != 1 {
		t.Fatalf("terminal report = %+v", got.report)
	}
	if prompt := prompts.String(); prompt != "Username: \nPassword: \nPassword (again): \n" {
		t.Fatalf("prompts = %q", prompt)
	}
	after, err := term.GetState(int(slave.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("terminal state was not restored after successful input")
	}
	clear(got.username)
	clear(got.password)
	echoed := readAvailablePTY(t, master)
	if bytes.Contains(echoed, []byte("operator-marker")) || bytes.Contains(echoed, []byte("password-marker")) {
		t.Fatalf("PTY echoed input while stderr was redirected: %q", echoed)
	}
}

func TestReadCreatesuperuserTerminalEchoesOnlyUsernameWhenStderrIsTheSameTerminal(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	type result struct {
		username []byte
		password []byte
		failure  *CreatesuperuserFailure
	}
	resultReady := make(chan result, 1)
	go func() {
		var report CreatesuperuserReport
		username, password, failure := readCreatesuperuserTerminal(
			context.Background(), nil, slave, slave, &report,
		)
		resultReady <- result{username: username, password: password, failure: failure}
	}()

	transcript := readPTYUntil(t, master, nil, "Username: ")
	writePTYInput(t, master, "visible-operator\n")
	transcript = readPTYUntil(t, master, transcript, "Password: ")
	writePTYInput(t, master, "hidden-password\n")
	transcript = readPTYUntil(t, master, transcript, "Password (again): ")
	writePTYInput(t, master, "hidden-password\n")
	transcript = readPTYMore(t, master, transcript)
	got := awaitTerminalResult(t, resultReady)
	transcript = append(transcript, readAvailablePTY(t, master)...)
	if got.failure != nil || !bytes.Equal(got.username, []byte("visible-operator")) ||
		!bytes.Equal(got.password, []byte("hidden-password")) {
		t.Fatalf("terminal result = username %q password-len %d failure %+v", got.username, len(got.password), got.failure)
	}
	clear(got.username)
	clear(got.password)
	if !bytes.Contains(transcript, []byte("visible-operator")) {
		t.Fatalf("same-terminal username was not visible: %q", transcript)
	}
	if bytes.Contains(transcript, []byte("hidden-password")) {
		t.Fatalf("same-terminal transcript exposed password: %q", transcript)
	}
}

func TestReadCreatesuperuserTerminalAppliesPrivateProfileBeforeTheFirstPromptAndRestoresIt(t *testing.T) {
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

	prompts := newObservedPromptWriter()
	interrupt := make(chan struct{})
	type result struct {
		failure *CreatesuperuserFailure
		report  CreatesuperuserReport
	}
	resultReady := make(chan result, 1)
	go func() {
		var report CreatesuperuserReport
		username, password, failure := readCreatesuperuserTerminal(
			context.Background(), interrupt, slave, prompts, &report,
		)
		clear(username)
		clear(password)
		resultReady <- result{failure: failure, report: report}
	}()

	waitForPrompt(t, prompts, "Username: ")
	requireCreatesuperuserPrivateTerminalProfile(t, slave)
	close(interrupt)
	got := awaitTerminalResult(t, resultReady)
	if got.failure == nil || got.failure.Code != createsuperuserprotocol.CodeProjectInterrupted ||
		got.report.TerminalRestoreAttempts != 1 {
		t.Fatalf("private-profile interruption = %+v report %+v", got.failure, got.report)
	}
	after, err := term.GetState(int(slave.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("terminal profile was not restored exactly")
	}
}

func TestReadCreatesuperuserTerminalBackspaceRemovesOneCompleteUTF8Rune(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	prompts := newObservedPromptWriter()
	type result struct {
		username []byte
		password []byte
		failure  *CreatesuperuserFailure
	}
	resultReady := make(chan result, 1)
	go func() {
		var report CreatesuperuserReport
		username, password, failure := readCreatesuperuserTerminal(
			context.Background(), nil, slave, prompts, &report,
		)
		resultReady <- result{username: username, password: password, failure: failure}
	}()

	waitForPrompt(t, prompts, "Username: ")
	writePTYInput(t, master, "운영자界\x7f\n")
	waitForPrompt(t, prompts, "Password: ")
	writePTYInput(t, master, "utf8-password\n")
	waitForPrompt(t, prompts, "Password (again): ")
	writePTYInput(t, master, "utf8-password\n")
	got := awaitTerminalResult(t, resultReady)
	if got.failure != nil || !bytes.Equal(got.username, []byte("운영자")) ||
		!bytes.Equal(got.password, []byte("utf8-password")) {
		t.Fatalf("UTF-8 backspace result = username %q password-len %d failure %+v", got.username, len(got.password), got.failure)
	}
	clear(got.username)
	clear(got.password)
}

func TestReadCreatesuperuserTerminalDoesNotEchoIdentityToADifferentTerminal(t *testing.T) {
	inputMaster, inputSlave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer inputMaster.Close()
	defer inputSlave.Close()
	outputMaster, outputSlave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer outputMaster.Close()
	defer outputSlave.Close()

	type result struct {
		username []byte
		password []byte
		failure  *CreatesuperuserFailure
	}
	resultReady := make(chan result, 1)
	go func() {
		var report CreatesuperuserReport
		username, password, failure := readCreatesuperuserTerminal(
			context.Background(), nil, inputSlave, outputSlave, &report,
		)
		resultReady <- result{username: username, password: password, failure: failure}
	}()

	transcript := readPTYUntil(t, outputMaster, nil, "Username: ")
	writePTYInput(t, inputMaster, "private-operator-identity\n")
	transcript = readPTYUntil(t, outputMaster, transcript, "Password: ")
	writePTYInput(t, inputMaster, "private-password\n")
	transcript = readPTYUntil(t, outputMaster, transcript, "Password (again): ")
	writePTYInput(t, inputMaster, "private-password\n")
	got := awaitTerminalResult(t, resultReady)
	transcript = append(transcript, readAvailablePTY(t, outputMaster)...)
	if got.failure != nil || !bytes.Equal(got.username, []byte("private-operator-identity")) ||
		!bytes.Equal(got.password, []byte("private-password")) {
		t.Fatalf("two-terminal result = username %q password-len %d failure %+v", got.username, len(got.password), got.failure)
	}
	clear(got.username)
	clear(got.password)
	if bytes.Contains(transcript, []byte("private-operator-identity")) || bytes.Contains(transcript, []byte("private-password")) {
		t.Fatalf("different-terminal transcript persisted identity or secret: %q", transcript)
	}
}

func TestReadCreatesuperuserTerminalDisablesKernelEchoBeforeAcceptingPrebufferedLines(t *testing.T) {
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

	prompts := newObservedPromptWriter()
	type result struct {
		username []byte
		password []byte
		failure  *CreatesuperuserFailure
		report   CreatesuperuserReport
	}
	resultReady := make(chan result, 1)
	go func() {
		var report CreatesuperuserReport
		username, password, failure := readCreatesuperuserTerminal(
			context.Background(), nil, slave, prompts, &report,
		)
		resultReady <- result{username: username, password: password, failure: failure, report: report}
	}()

	waitForPrompt(t, prompts, "Username: ")
	writePTYInput(t, master, "pasted-operator\npasted-password\npasted-password\ntrailing-secret\n")
	got := awaitTerminalResult(t, resultReady)
	if got.failure != nil || !bytes.Equal(got.username, []byte("pasted-operator")) ||
		!bytes.Equal(got.password, []byte("pasted-password")) {
		t.Fatalf("terminal result = username %q password-len %d failure %+v", got.username, len(got.password), got.failure)
	}
	if got.report.TerminalRestoreAttempts != 1 {
		t.Fatalf("terminal report = %+v", got.report)
	}
	after, err := term.GetState(int(slave.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("terminal state was not restored after prebuffered input")
	}
	clear(got.username)
	clear(got.password)
	requireRestoredPTYInput(t, slave, master, "sentinel-after-restore\n")
	transcript := append([]byte(prompts.String()), readAvailablePTY(t, master)...)
	if bytes.Contains(transcript, []byte("pasted-password")) {
		t.Fatalf("prebuffered password material was rendered: %q", transcript)
	}
	if bytes.Contains(transcript, []byte("trailing-secret")) {
		t.Fatalf("trailing private input survived terminal restore: %q", transcript)
	}
	if !bytes.Contains(transcript, []byte("sentinel-after-restore")) {
		t.Fatalf("restored terminal did not echo new input: %q", transcript)
	}
}

func TestReadCreatesuperuserTerminalDiscardsQueuedSecretsAfterInvalidUsername(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	prompts := newObservedPromptWriter()
	type result struct {
		failure *CreatesuperuserFailure
		report  CreatesuperuserReport
	}
	resultReady := make(chan result, 1)
	go func() {
		var report CreatesuperuserReport
		username, password, failure := readCreatesuperuserTerminal(
			context.Background(), nil, slave, prompts, &report,
		)
		clear(username)
		clear(password)
		resultReady <- result{failure: failure, report: report}
	}()

	waitForPrompt(t, prompts, "Username: ")
	writePTYInput(t, master, " invalid-operator\nqueued-password\nqueued-password\n")
	got := awaitTerminalResult(t, resultReady)
	want := CreatesuperuserFailure{
		Category: createsuperuserprotocol.CategoryInput,
		Code:     createsuperuserprotocol.CodeInvalidUsername,
	}
	if got.failure == nil || *got.failure != want || got.report.PasswordPromptWrites != 0 ||
		got.report.TerminalRestoreAttempts != 1 {
		t.Fatalf("invalid username result = %+v report %+v", got.failure, got.report)
	}
	requireRestoredPTYInput(t, slave, master, "sentinel-invalid-restore\n")
	transcript := append([]byte(prompts.String()), readAvailablePTY(t, master)...)
	if bytes.Contains(transcript, []byte("queued-password")) {
		t.Fatalf("queued password survived invalid-input restore: %q", transcript)
	}
	if !bytes.Contains(transcript, []byte("sentinel-invalid-restore")) {
		t.Fatalf("restored terminal did not echo new input: %q", transcript)
	}
}

func TestReadCreatesuperuserTerminalStopsAtTheBoundAndFlushesWithoutWaitingForNewline(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	prompts := newObservedPromptWriter()
	type result struct {
		failure *CreatesuperuserFailure
		report  CreatesuperuserReport
	}
	resultReady := make(chan result, 1)
	go func() {
		var report CreatesuperuserReport
		username, password, failure := readCreatesuperuserTerminal(
			context.Background(), nil, slave, prompts, &report,
		)
		clear(username)
		clear(password)
		resultReady <- result{failure: failure, report: report}
	}()

	waitForPrompt(t, prompts, "Username: ")
	writePTYInput(t, master, strings.Repeat("u", createsuperuserprotocol.MaxUsernameBytes+1))
	got := awaitTerminalResult(t, resultReady)
	want := CreatesuperuserFailure{
		Category: createsuperuserprotocol.CategoryInput,
		Code:     createsuperuserprotocol.CodeInvalidUsername,
	}
	if got.failure == nil || *got.failure != want || got.report.TerminalRestoreAttempts != 1 {
		t.Fatalf("overflow result = %+v report %+v", got.failure, got.report)
	}
	requireRestoredPTYInput(t, slave, master, "sentinel-overflow-restore\n")
	if transcript := append([]byte(prompts.String()), readAvailablePTY(t, master)...); !bytes.Contains(transcript, []byte("sentinel-overflow-restore")) {
		t.Fatalf("restored terminal did not echo after bounded overflow: %q", transcript)
	}
}

func TestReadCreatesuperuserTerminalRestoresEchoAfterInterrupt(t *testing.T) {
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
	prompts := newObservedPromptWriter()
	interrupt := make(chan struct{})
	type result struct {
		failure *CreatesuperuserFailure
		report  CreatesuperuserReport
	}
	resultReady := make(chan result, 1)
	go func() {
		var report CreatesuperuserReport
		username, password, failure := readCreatesuperuserTerminal(
			context.Background(), interrupt, slave, prompts, &report,
		)
		clear(username)
		clear(password)
		resultReady <- result{failure: failure, report: report}
	}()
	waitForPrompt(t, prompts, "Username: ")
	writePTYInput(t, master, "operator\n")
	waitForPrompt(t, prompts, "Password: ")
	writePTYInput(t, master, "queued-interrupt-secret")
	close(interrupt)
	got := awaitTerminalResult(t, resultReady)
	want := CreatesuperuserFailure{
		Category: createsuperuserprotocol.CategoryProcess,
		Code:     createsuperuserprotocol.CodeProjectInterrupted,
	}
	if got.failure == nil || *got.failure != want || got.report.TerminalRestoreAttempts != 1 ||
		got.report.ConfirmationPromptWrites != 0 {
		t.Fatalf("interrupted terminal result = %+v report %+v", got.failure, got.report)
	}
	after, err := term.GetState(int(slave.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("terminal state was not restored after interrupt")
	}
	requireRestoredPTYInput(t, slave, master, "sentinel-interrupt-restore\n")
	transcript := append([]byte(prompts.String()), readAvailablePTY(t, master)...)
	if bytes.Contains(transcript, []byte("queued-interrupt-secret")) {
		t.Fatalf("queued password survived interrupt restore: %q", transcript)
	}
	if !bytes.Contains(transcript, []byte("sentinel-interrupt-restore")) {
		t.Fatalf("restored terminal did not echo new input: %q", transcript)
	}
}

func TestReadCreatesuperuserTerminalRejectsNonTerminalBeforePrompts(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	var prompts bytes.Buffer
	var report CreatesuperuserReport
	username, password, failure := readCreatesuperuserTerminal(
		context.Background(), nil, reader, &prompts, &report,
	)
	want := CreatesuperuserFailure{
		Category: createsuperuserprotocol.CategoryInput,
		Code:     createsuperuserprotocol.CodeInputNotTerminal,
	}
	if username != nil || password != nil || failure == nil || *failure != want ||
		prompts.Len() != 0 || report.TerminalChecks != 1 || report.UsernamePromptWrites != 0 {
		t.Fatalf("non-terminal result = %q/%q/%+v prompts %q report %+v", username, password, failure, prompts.Bytes(), report)
	}
}

type observedPromptWriter struct {
	mu      sync.Mutex
	buffer  bytes.Buffer
	written chan struct{}
}

func newObservedPromptWriter() *observedPromptWriter {
	return &observedPromptWriter{written: make(chan struct{}, 16)}
}

func (writer *observedPromptWriter) Write(document []byte) (int, error) {
	writer.mu.Lock()
	written, err := writer.buffer.Write(document)
	writer.mu.Unlock()
	select {
	case writer.written <- struct{}{}:
	default:
	}
	return written, err
}

func (writer *observedPromptWriter) String() string {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.buffer.String()
}

func waitForPrompt(t *testing.T, writer *observedPromptWriter, marker string) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		if bytes.Contains([]byte(writer.String()), []byte(marker)) {
			return
		}
		select {
		case <-writer.written:
		case <-deadline.C:
			t.Fatalf("timed out waiting for prompt %q in %q", marker, writer.String())
		}
	}
}

func writePTYInput(t *testing.T, writer io.Writer, input string) {
	t.Helper()
	written, err := io.WriteString(writer, input)
	if err != nil || written != len(input) {
		t.Fatalf("write PTY input = %d, %v", written, err)
	}
}

func readPTYUntil(t *testing.T, reader *os.File, retained []byte, marker string) []byte {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !bytes.Contains(retained, []byte(marker)) {
		poll := []unix.PollFd{{Fd: int32(reader.Fd()), Events: unix.POLLIN}}
		ready, err := unix.Poll(poll, 50)
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			t.Fatal(err)
		}
		if ready == 0 {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for PTY output %q in %q", marker, retained)
			}
			continue
		}
		buffer := make([]byte, 4<<10)
		read, err := unix.Read(int(reader.Fd()), buffer)
		if err != nil {
			t.Fatal(err)
		}
		retained = append(retained, buffer[:read]...)
	}
	return retained
}

func readPTYMore(t *testing.T, reader *os.File, retained []byte) []byte {
	t.Helper()
	initial := len(retained)
	deadline := time.Now().Add(5 * time.Second)
	for len(retained) == initial {
		poll := []unix.PollFd{{Fd: int32(reader.Fd()), Events: unix.POLLIN}}
		ready, err := unix.Poll(poll, 50)
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			t.Fatal(err)
		}
		if ready == 0 {
			if time.Now().After(deadline) {
				t.Fatal("timed out waiting for additional PTY output")
			}
			continue
		}
		buffer := make([]byte, 4<<10)
		read, err := unix.Read(int(reader.Fd()), buffer)
		if err != nil {
			t.Fatal(err)
		}
		retained = append(retained, buffer[:read]...)
	}
	return retained
}

func requireRestoredPTYInput(t *testing.T, slave, master *os.File, sentinel string) {
	t.Helper()
	writePTYInput(t, master, sentinel)
	deadline := time.Now().Add(5 * time.Second)
	for {
		poll := []unix.PollFd{{Fd: int32(slave.Fd()), Events: unix.POLLIN}}
		ready, err := unix.Poll(poll, 50)
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			t.Fatal(err)
		}
		if ready == 0 {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for restored terminal input %q", sentinel)
			}
			continue
		}
		buffer := make([]byte, 4<<10)
		read, err := unix.Read(int(slave.Fd()), buffer)
		if err != nil {
			t.Fatal(err)
		}
		if got := string(buffer[:read]); got != sentinel {
			t.Fatalf("restored terminal input = %q, want exact sentinel %q", got, sentinel)
		}
		return
	}
}

func readAvailablePTY(t *testing.T, reader *os.File) []byte {
	t.Helper()
	fd := int(reader.Fd())
	// File.Fd restores descriptors registered with Go's poller to blocking
	// mode on Linux. Cache it before enabling non-blocking reads so a later
	// Fd call cannot undo the setting and hang an empty PTY drain.
	if err := unix.SetNonblock(fd, true); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := unix.SetNonblock(fd, false); err != nil {
			t.Errorf("restore blocking PTY output: %v", err)
		}
	}()
	buffer := make([]byte, 4<<10)
	retained := make([]byte, 0, 4<<10)
	for {
		read, err := unix.Read(fd, buffer)
		if read > 0 {
			retained = append(retained, buffer[:read]...)
		}
		if err == nil {
			if read == 0 {
				return retained
			}
			continue
		}
		if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) {
			return retained
		}
		t.Fatalf("read PTY output: %v", err)
	}
}

func awaitTerminalResult[T any](t *testing.T, ready <-chan T) T {
	t.Helper()
	select {
	case result := <-ready:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for terminal result")
		var zero T
		return zero
	}
}
