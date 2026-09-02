//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/progresshans/godj/internal/projectcheck/createsuperuserprotocol"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// The actual command must restore the PTY, discard queued secret input and
// remove its private build workspace before exit. This finite harness budget
// is not a product latency contract.
const createsuperuserActualInterruptExitTimeout = 30 * time.Second

func TestPTYVINTRHarnessSignalsForegroundProcess(t *testing.T) {
	command := exec.Command(
		"/bin/sh",
		"-c",
		`trap 'exit 42' INT; stty -echo -icanon isig intr '^C'; printf 'ready'; while :; do sleep 1; done`,
	)
	master, err := pty.Start(command)
	if err != nil {
		t.Fatalf("start VINTR harness: %v", err)
	}
	defer master.Close()

	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	reaped := false
	defer func() {
		if reaped {
			return
		}
		_ = master.Close()
		_ = command.Process.Kill()
		select {
		case <-waited:
		case <-time.After(5 * time.Second):
		}
	}()

	var transcript bytes.Buffer
	readCreatesuperuserPTYUntil(t, master, &transcript, "ready")
	const queued = "queued-vintr-harness-input"
	if written, err := master.Write([]byte(queued)); err != nil || written != len(queued) {
		t.Fatalf("write harness queued input = %d, %v", written, err)
	}
	if written, err := master.Write([]byte{3}); err != nil || written != 1 {
		t.Fatalf("write harness VINTR = %d, %v", written, err)
	}
	select {
	case err := <-waited:
		reaped = true
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) || exitError.ExitCode() != 42 {
			t.Fatalf("VINTR harness wait = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("VINTR did not signal the foreground PTY harness")
	}
}

func TestExecuteDispatchesCreatesuperuserBeforeProjectOrTerminalIO(t *testing.T) {
	t.Parallel()

	const secretMarker = "invalid-argv-secret-marker"
	var stdout, stderr bytes.Buffer
	exit := execute(
		context.Background(),
		filepath.Join(t.TempDir(), "missing"),
		[]string{"createsuperuser", "--password", secretMarker},
		nil,
		nil,
		&stdout,
		&stderr,
		nil,
	)
	want := createsuperuserprotocol.CategoryCommand + "/" + createsuperuserprotocol.CodeInvalidArguments + "\n"
	if exit != 2 || stdout.Len() != 0 || stderr.String() != want || strings.Contains(stderr.String(), secretMarker) {
		t.Fatalf("createsuperuser dispatch exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
}

func TestActualGodjCreatesuperuserInterruptRestoresPTYAndDiscardsQueuedSecret(t *testing.T) {
	fixture := newProcessFixture(t)

	for _, test := range []struct {
		name           string
		controllingPTY bool
		interrupt      func(*testing.T, *exec.Cmd, *os.File)
	}{
		{
			name: "process SIGINT",
			interrupt: func(t *testing.T, command *exec.Cmd, _ *os.File) {
				t.Helper()
				if err := command.Process.Signal(os.Interrupt); err != nil {
					t.Fatalf("signal createsuperuser: %v", err)
				}
			},
		},
		{
			name:           "terminal VINTR",
			controllingPTY: true,
			interrupt: func(t *testing.T, _ *exec.Cmd, master *os.File) {
				t.Helper()
				if written, err := master.Write([]byte{3}); err != nil || written != 1 {
					t.Fatalf("write terminal VINTR = %d, %v", written, err)
				}
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			command := exec.Command(fixture.godj, "createsuperuser")
			command.Dir = fixture.project
			command.Env = fixture.environment(nil)
			command.WaitDelay = 5 * time.Second

			var master, slave *os.File
			var originalState *term.State
			var err error
			if test.controllingPTY {
				master, err = pty.Start(command)
			} else {
				master, slave, err = pty.Open()
				if err == nil {
					originalState, err = term.GetState(int(slave.Fd()))
				}
				if err == nil {
					command.Stdin = slave
					command.Stdout = slave
					command.Stderr = slave
					err = command.Start()
				}
			}
			if err != nil {
				t.Fatalf("start createsuperuser on PTY: %v", err)
			}
			defer func() {
				_ = master.Close()
				if slave != nil {
					_ = slave.Close()
				}
			}()
			waited := make(chan error, 1)
			go func() { waited <- command.Wait() }()
			reaped := false
			defer func() {
				if reaped {
					return
				}
				if slave != nil {
					_ = slave.Close()
				}
				_ = master.Close()
				_ = command.Process.Kill()
				select {
				case <-waited:
				case <-time.After(5 * time.Second):
				}
			}()

			var transcript bytes.Buffer
			readCreatesuperuserPTYUntil(t, master, &transcript, "Username: ")
			if written, err := master.Write([]byte("operator\n")); err != nil || written != len("operator\n") {
				t.Fatalf("write username = %d, %v", written, err)
			}
			readCreatesuperuserPTYUntil(t, master, &transcript, "Password: ")

			secret := []byte("queued-interrupt-secret-marker")
			if written, err := master.Write(secret); err != nil || written != len(secret) {
				t.Fatalf("write partial secret = %d, %v", written, err)
			}
			postOutput := make(chan []byte, 1)
			go collectCreatesuperuserPTY(master, postOutput)
			interruptStarted := time.Now()
			test.interrupt(t, command, master)

			select {
			case err := <-waited:
				reaped = true
				var exitError *exec.ExitError
				if !errors.As(err, &exitError) || exitError.ExitCode() != 130 {
					t.Fatalf("createsuperuser interrupt wait = %v", err)
				}
			case <-time.After(createsuperuserActualInterruptExitTimeout):
				alive := syscall.Kill(command.Process.Pid, 0) == nil
				workspaceCount := -1
				if entries, err := os.ReadDir(fixture.scratch); err == nil {
					workspaceCount = len(entries)
				}
				t.Fatalf(
					"createsuperuser did not exit after interrupt: elapsed=%s alive=%t scratch_entries=%d transcript_bytes=%d",
					time.Since(interruptStarted).Round(time.Millisecond), alive, workspaceCount, transcript.Len(),
				)
			}

			if slave != nil {
				restoredState, err := term.GetState(int(slave.Fd()))
				if err != nil {
					t.Fatalf("read restored terminal state: %v", err)
				}
				if !reflect.DeepEqual(originalState, restoredState) {
					t.Fatalf("terminal state was not restored exactly\nbefore=%#v\nafter=%#v", originalState, restoredState)
				}

				const sentinel = "restored-input-sentinel\n"
				if written, err := master.Write([]byte(sentinel)); err != nil || written != len(sentinel) {
					t.Fatalf("write post-restore sentinel = %d, %v", written, err)
				}
				input := readCreatesuperuserPTYRecord(t, slave, len(secret)+len(sentinel)+16, 3*time.Second)
				if string(input) != sentinel {
					t.Fatalf("restored input queue = %q; want only %q", input, sentinel)
				}

				if err := slave.Close(); err != nil {
					t.Fatalf("close retained PTY slave: %v", err)
				}
			}
			select {
			case output := <-postOutput:
				_, _ = transcript.Write(output)
			case <-time.After(3 * time.Second):
				t.Fatal("PTY output collector did not terminate")
			}
			if strings.Contains(transcript.String(), string(secret)) {
				t.Fatalf("PTY transcript leaked partial secret: %q", transcript.String())
			}
			wantFailure := createsuperuserprotocol.CategoryProcess + "/" + createsuperuserprotocol.CodeProjectInterrupted
			if !strings.Contains(transcript.String(), wantFailure) {
				t.Fatalf("PTY transcript = %q; want failure %q", transcript.String(), wantFailure)
			}
		})
	}
}

func readCreatesuperuserPTYUntil(t *testing.T, terminal *os.File, transcript *bytes.Buffer, marker string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	buffer := make([]byte, 256)
	for !strings.Contains(transcript.String(), marker) {
		read, err := readCreatesuperuserPTYChunk(terminal, buffer, time.Until(deadline))
		if read > 0 {
			_, _ = transcript.Write(buffer[:read])
		}
		if err != nil {
			t.Fatalf("read PTY until %q: %v; transcript=%q", marker, err, transcript.String())
		}
	}
}

func collectCreatesuperuserPTY(terminal *os.File, complete chan<- []byte) {
	var transcript bytes.Buffer
	buffer := make([]byte, 256)
	for {
		read, err := readCreatesuperuserPTYChunk(terminal, buffer, time.Second)
		if read > 0 {
			_, _ = transcript.Write(buffer[:read])
		}
		if read == 0 && err == nil {
			complete <- transcript.Bytes()
			return
		}
		if err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
			continue
		}
		complete <- transcript.Bytes()
		return
	}
}

func readCreatesuperuserPTYRecord(t *testing.T, terminal *os.File, maximum int, timeout time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(timeout)
	record := make([]byte, 0, maximum)
	buffer := make([]byte, maximum)
	for len(record) < maximum {
		read, err := readCreatesuperuserPTYChunk(terminal, buffer[:maximum-len(record)], time.Until(deadline))
		if read > 0 {
			record = append(record, buffer[:read]...)
			if bytes.Contains(record, []byte{'\n'}) {
				return record
			}
		}
		if err != nil {
			t.Fatalf("read restored PTY input: %v; input=%q", err, record)
		}
	}
	t.Fatalf("restored PTY input exceeded %d bytes: %q", maximum, record)
	return nil
}

func readCreatesuperuserPTYChunk(terminal *os.File, destination []byte, timeout time.Duration) (int, error) {
	if timeout <= 0 {
		return 0, os.ErrDeadlineExceeded
	}
	milliseconds := int((timeout + time.Millisecond - 1) / time.Millisecond)
	poll := []unix.PollFd{{Fd: int32(terminal.Fd()), Events: unix.POLLIN}}
	for {
		ready, err := unix.Poll(poll, milliseconds)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if ready == 0 {
			return 0, os.ErrDeadlineExceeded
		}
		if poll[0].Revents&(unix.POLLERR|unix.POLLNVAL) != 0 ||
			poll[0].Revents&unix.POLLHUP != 0 && poll[0].Revents&unix.POLLIN == 0 {
			return 0, syscall.EIO
		}
		return unix.Read(int(terminal.Fd()), destination)
	}
}
