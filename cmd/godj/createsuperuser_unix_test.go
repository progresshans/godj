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
	"golang.org/x/sys/unix"
	"golang.org/x/term"
	"runtime"
)

const (
	// The actual command must restore the PTY, discard queued secret input and
	// remove its private build workspace before exit. This finite harness budget
	// is not a product latency contract.
	createsuperuserActualInterruptExitTimeout = 30 * time.Second

	// The first product prompt follows a cold project-runner build. Hosted
	// Linux/amd64 has observed a 371-second initial runner build. Keep that
	// compilation budget separate from the prompt and interrupt-exit budgets;
	// neither secret-input handling nor terminal restoration gets extra time.
	createsuperuserActualColdStartPromptTimeout = 10 * time.Minute
	createsuperuserActualPromptTimeout          = 30 * time.Second
	createsuperuserVINTRHarnessPromptTimeout    = 5 * time.Second
)

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
	readCreatesuperuserPTYUntil(t, master, &transcript, "ready", createsuperuserVINTRHarnessPromptTimeout)
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
	want := "{\"operation_id\":\"\",\"status\":\"not_created\",\"code\":\"invalid_arguments\",\"cleanup_failed\":false,\"file_report_failed\":false}\n"
	if exit != 2 || stdout.Len() != 0 || stderr.String() != want || strings.Contains(stderr.String(), secretMarker) {
		t.Fatalf("createsuperuser dispatch exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
}

func TestActualGodjCreatesuperuserInterruptRestoresPTYAndDiscardsQueuedSecret(t *testing.T) {
	fixture := newFirstAccountProcessFixture(t)

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
			readCreatesuperuserPTYUntil(t, master, &transcript, `"Username": `, createsuperuserActualColdStartPromptTimeout)
			if written, err := master.Write([]byte("operator\n")); err != nil || written != len("operator\n") {
				t.Fatalf("write username = %d, %v", written, err)
			}
			readCreatesuperuserPTYUntil(t, master, &transcript, `Use model default for "Email"? [Y/n]: `, createsuperuserActualPromptTimeout)
			if _, err := master.Write([]byte("y\n")); err != nil {
				t.Fatal(err)
			}
			readCreatesuperuserPTYUntil(t, master, &transcript, "Password: ", createsuperuserActualPromptTimeout)

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
			wantFailure := `"status":"not_created","code":"project_interrupted","cleanup_failed":false`
			if !strings.Contains(transcript.String(), wantFailure) {
				t.Fatalf("PTY transcript = %q; want failure %q", transcript.String(), wantFailure)
			}
		})
	}
}

func newFirstAccountProcessFixture(t *testing.T) processFixture {
	t.Helper()
	fixture := newProcessFixture(t)
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate framework source")
	}
	repository, err := filepath.EvalSymlinks(filepath.Join(filepath.Dir(source), "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	result := fixture.run(t, fixture.universe, nil, "startproject", "example.com/selected-operator", "selected", "--framework-source", repository)
	if result.exit != 0 {
		t.Fatalf("prepare selected public project: exit=%d stdout=%q stderr=%q", result.exit, result.stdout, result.stderr)
	}
	fixture.project = filepath.Join(fixture.universe, "selected")
	fixture.nested = fixture.project
	return fixture
}

func readCreatesuperuserPTYUntil(t *testing.T, terminal *os.File, transcript *bytes.Buffer, marker string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
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
