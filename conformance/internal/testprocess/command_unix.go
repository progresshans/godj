//go:build darwin || linux

package testprocess

import (
	"errors"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// CommandLimits belongs to the consumer: setup and product commands may have
// different time and output budgets while sharing process ownership machinery.
type CommandLimits struct {
	Timeout time.Duration
	Output  int
}

type CommandResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// Run retains bounded output and returns the actual exit code. Timeout,
// truncated output, incomplete Wait and surviving process groups fail the test.
func Run(t testing.TB, limits CommandLimits, directory string, environment []string, name string, arguments ...string) CommandResult {
	t.Helper()
	if limits.Timeout <= 0 || limits.Output <= 0 {
		t.Fatal("external command requires positive resource limits")
	}
	stdout, stderr := NewBuffer(limits.Output), NewBuffer(limits.Output)
	command := exec.Command(name, arguments...)
	command.Dir = directory
	command.Env = append([]string(nil), environment...)
	command.Stdout, command.Stderr = stdout, stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		t.Fatalf("start %s: %v", filepath.Base(name), err)
	}
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	timer := time.NewTimer(limits.Timeout)
	defer timer.Stop()
	var waitErr error
	select {
	case waitErr = <-waited:
	case <-timer.C:
		groups, discoveryErr := OwnedGroups(command.Process.Pid)
		killErr := KillGroups(groups, command.Process.Pid)
		waitErr = Wait(waited, 5*time.Second)
		absenceErr := WaitAbsent(groups, 2*time.Second)
		t.Fatalf("%s timed out: %v", filepath.Base(name), errors.Join(discoveryErr, killErr, waitErr, absenceErr))
	}
	if stdout.Truncated() || stderr.Truncated() {
		t.Fatalf("%s exceeded output limit", filepath.Base(name))
	}
	result := CommandResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if waitErr != nil {
		var exitError *exec.ExitError
		if !errors.As(waitErr, &exitError) {
			t.Fatalf("run %s: %v", filepath.Base(name), waitErr)
		}
		result.ExitCode = exitError.ExitCode()
	}
	if err := WaitAbsent([]int{command.Process.Pid}, 2*time.Second); err != nil {
		t.Fatalf("wait for external root process group: %v", err)
	}
	return result
}
