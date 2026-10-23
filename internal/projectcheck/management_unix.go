//go:build darwin || linux

package projectcheck

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/internal/gobuild"
	management "github.com/progresshans/godj/project/command"
	"github.com/progresshans/godj/project/compatibility"
)

type ManagementInvocation struct {
	Context           context.Context
	CWD               string
	Args, Environment []string
	Stdin             *os.File
	Stdout, Stderr    io.Writer
	Interrupt         <-chan struct{}
	Backend           Backend
	workspace         workspaceHooks
	generation        generationHooks
}

type ManagementReport struct {
	RunServerReport
	CompatibilityChecks int
	CommandStarted      bool
	CommandExitCode     int
	FailureCode         string
}

func parseManagement(argv []string) (descriptor string, child []string, ok bool) {
	if len(argv) == 0 || len(argv) > management.MaxArguments+4 {
		return "", nil, false
	}
	total := 0
	for _, arg := range argv {
		total += len(arg)
		if len(arg) > management.MaxArgumentBytes || total > management.MaxInputBytes || !utf8.ValidString(arg) || strings.IndexByte(arg, 0) >= 0 {
			return "", nil, false
		}
	}
	args := append([]string(nil), argv[1:]...)
	// Project selection precedes command values. Values after the name belong
	// to the project, including a project-defined --project option.
	if len(args) > 0 && args[0] == "--project" {
		if len(args) < 2 || args[1] == "" {
			return "", nil, false
		}
		descriptor = args[1]
		args = args[2:]
	}
	switch argv[0] {
	case "commands":
		if len(args) == 0 {
			return descriptor, []string{"list"}, true
		}
	case "check":
		if len(args) == 0 {
			return descriptor, []string{"check"}, true
		}
	case "help":
		if len(args) == 1 && managementName(args[0]) {
			return descriptor, []string{"help", args[0]}, true
		}
	case "command":
		if len(args) >= 1 && managementName(args[0]) {
			return descriptor, append([]string{"run"}, args...), true
		}
	}
	return "", nil, false
}

func managementName(name string) bool {
	if len(name) == 0 || len(name) > 63 || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	return strings.Trim(name, "abcdefghijklmnopqrstuvwxyz0123456789_-") == ""
}

func RunManagement(input ManagementInvocation) ManagementReport {
	report := ManagementReport{CommandExitCode: -1}
	descriptor, child, valid := parseManagement(input.Args)
	input.Context, input.Environment, input.Backend = normalizeCommandInput(input.Context, input.Environment, input.Backend)
	fail := func(code string, exit int) ManagementReport {
		report.FailureCode = code
		report.ExitCode = exit
		if input.Stderr != nil {
			doc := publicFailureDocument(&report.Report, "project_command_error", code)
			n, err := input.Stderr.Write(doc)
			if err != nil || n != len(doc) {
				report.FailureCode = "output_failed"
				report.ExitCode = 1
			}
		}
		return report
	}
	if !valid || input.Stdout == nil || input.Stderr == nil {
		return fail("invalid_arguments", 2)
	}
	if code := commandInterruption(input.Context, input.Interrupt); code != "" {
		return fail(code, 1)
	}
	runtimeInput := RunServerInvocation{Context: input.Context, CWD: input.CWD, Environment: input.Environment,
		Stdout: input.Stdout, Stderr: input.Stderr, Interrupt: input.Interrupt, Backend: input.Backend,
		workspace: input.workspace, generation: completeGenerationHooks(input.generation)}
	prepared, failure := prepareProjectRuntime(runtimeInput, descriptor, runtimeCommands, &report.RunServerReport)
	finish := func(code string, exit int) ManagementReport {
		if prepared.command.close() {
			if code == "" {
				code = "project_cleanup_failed"
			}
			exit = 1
		}
		if code != "" {
			return fail(code, exit)
		}
		report.ExitCode = exit
		return report
	}
	if failure != nil {
		code := failure.Code
		if code == RunServerCodeNotConfigured {
			code = "commands_not_configured"
		}
		return finish(code, 1)
	}
	// The same private binaries are probed and then used. No project handler
	// or DB opener is involved in either compatibility probe.
	for _, binary := range []string{filepath.Join(prepared.command.workspace.root, "godj-project-runner"), prepared.binary} {
		if terminal := prepared.command.ready(); terminal != nil {
			return finish(terminal.Code, 1)
		}
		report.CompatibilityChecks++
		probe := input.Backend.Execute(input.Context, input.Interrupt, CompatibilityStage, Command{
			Dir: prepared.command.selected.rootPath, Argv: []string{binary, compatibility.PrivateArgument}, Env: prepared.command.workspace.environment,
		})
		recordProcess(&report.Report, CompatibilityStage, probe)
		if probe.CleanupFailed {
			clear(probe.Stdout)
			return finish("project_cleanup_failed", 1)
		}
		if !probe.Started || probe.ExitCode != 0 || probe.Failure != nil || probe.StdoutScalar.Truncated {
			clear(probe.Stdout)
			return finish("compatibility_probe_failed", 1)
		}
		linked, err := compatibility.Parse(probe.Stdout)
		clear(probe.Stdout)
		if err != nil {
			return finish("compatibility_response_invalid", 1)
		}
		if compatibility.Check(compatibility.Current(), linked) != nil {
			return finish("framework_incompatible", 1)
		}
	}
	if terminal := prepared.command.ready(); terminal != nil {
		return finish(terminal.Code, 1)
	}
	live := executeStreamingProcess(input.Context, input.Interrupt, Command{
		Dir: prepared.command.selected.rootPath, Argv: append([]string{prepared.binary}, child...), Env: gobuild.RuntimeEnvironment(input.Environment),
	}, input.Stdin, input.Stdout, input.Stderr, defaultRunserverGrace)
	recordRunserverProcess(&report.RunServerReport, live)
	report.CommandStarted = live.Started
	report.CommandExitCode = live.ExitCode
	if live.CleanupFailed {
		return finish("project_cleanup_failed", 1)
	}
	if live.StdoutError != nil || live.StderrError != nil {
		return finish("output_failed", 1)
	}
	if live.StartError != nil || !live.Started {
		return finish("command_start_failed", 1)
	}
	if live.Interrupted {
		return finish("project_interrupted", 1)
	}
	if live.Canceled {
		return finish("project_canceled", 1)
	}
	if live.Forced || live.ExitCode < 0 || live.ExitCode > 125 {
		return finish("command_aborted", 1)
	}
	// A completed handler owns its public exit. Never translate nonzero into
	// rollback, rerun it, or duplicate its stderr as a generic runner error.
	return finish("", live.ExitCode)
}
