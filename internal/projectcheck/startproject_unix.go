//go:build darwin || linux

package projectcheck

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/progresshans/godj/internal/projectscaffold"
	"github.com/progresshans/godj/version"
)

type StartProjectInvocation struct {
	Context           context.Context
	CWD               string
	Args, Environment []string
	Stdout, Stderr    io.Writer
	Interrupt         <-chan struct{}
	Backend           Backend
}

type StartProjectReport struct {
	ExitCode          int
	FailureCode       string
	Publication       projectscaffold.Result
	BuildDiagnostic   string
	GenerationFailure GenerationFailure
}

type startProjectArguments struct {
	project     projectscaffold.Project
	destination string
}

func parseStartProjectArguments(args []string, installed version.Info) (startProjectArguments, bool) {
	if len(args) != 3 && len(args) != 5 || len(args) == 0 || args[0] != "startproject" {
		return startProjectArguments{}, false
	}
	if projectscaffold.ValidateModule(args[1]) != nil || !scaffoldPath(args[2]) {
		return startProjectArguments{}, false
	}
	result := startProjectArguments{project: projectscaffold.Project{Module: args[1]}, destination: args[2]}
	if len(args) == 5 {
		switch args[3] {
		case "--framework-source":
			if !scaffoldPath(args[4]) {
				return startProjectArguments{}, false
			}
			result.project.FrameworkSource = args[4]
		case "--framework-version":
			result.project.FrameworkVersion = args[4]
		default:
			return startProjectArguments{}, false
		}
	} else if installed.Module == version.Module && installed.Origin == version.ModuleVersion {
		result.project.FrameworkVersion = installed.Version
	}
	// Development source resolution is deferred until after argument checks.
	validation := result.project
	if validation.FrameworkSource != "" {
		validation.FrameworkSource = string(filepath.Separator) + "explicit-development-source"
	}
	if _, err := projectscaffold.RenderProject(validation); err != nil {
		return startProjectArguments{}, false
	}
	return result, true
}

// RunStartProject creates a new directory only after declaration generation,
// module resolution and runtime compilation succeed. It never starts a site,
// applies migrations or creates credentials.
func RunStartProject(input StartProjectInvocation) StartProjectReport {
	input.Args = append([]string(nil), input.Args...)
	arguments, valid := parseStartProjectArguments(input.Args, version.Current())
	input.Context, input.Environment, input.Backend = normalizeCommandInput(input.Context, input.Environment, input.Backend)
	report := StartProjectReport{}
	fail := func(code string, exit int) StartProjectReport {
		report.FailureCode, report.ExitCode = code, exit
		if input.Stderr != nil {
			diagnostic := "startproject_error/" + code + "\n"
			if code == "candidate_generation_failed" && report.GenerationFailure.Code != "" {
				diagnostic += report.GenerationFailure.Category + "/" + report.GenerationFailure.Code + "\n"
			}
			if (code == "candidate_build_failed" || code == "candidate_generation_failed") && report.BuildDiagnostic != "" {
				diagnostic += report.BuildDiagnostic + "\n"
			}
			_, _ = io.WriteString(input.Stderr, diagnostic)
		}
		return report
	}
	if !valid {
		return fail("invalid_arguments_or_framework_dependency", 2)
	}
	if input.Stdout == nil {
		return fail("output_unavailable", 1)
	}
	if code := commandInterruption(input.Context, input.Interrupt); code != "" {
		return fail(code, 130)
	}
	cwd := input.CWD
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return fail("working_directory_unavailable", 1)
		}
	}
	if !filepath.IsAbs(cwd) {
		return fail("invalid_working_directory", 2)
	}
	resolve := func(value string) string {
		if filepath.IsAbs(value) {
			return filepath.Clean(value)
		}
		return filepath.Join(cwd, value)
	}
	arguments.destination = resolve(arguments.destination)
	if arguments.project.FrameworkSource != "" {
		physical, err := filepath.EvalSymlinks(resolve(arguments.project.FrameworkSource))
		if err != nil {
			return fail("framework_source_unavailable", 1)
		}
		info, err := os.Stat(filepath.Join(physical, "go.mod"))
		if err != nil || !info.Mode().IsRegular() {
			return fail("framework_source_unavailable", 1)
		}
		arguments.project.FrameworkSource = physical
	}
	ctx, join := commandInterruptContext(input.Context, input.Interrupt)
	defer join()
	publication, err := projectscaffold.PublishProject(ctx, arguments.destination, arguments.project, func(ctx context.Context, candidate string) error {
		return prepareScaffoldProject(ctx, candidate, input, &report)
	})
	report.Publication = publication
	if err != nil {
		code, exit := scaffoldFailure(input.Context, input.Interrupt, publication, err)
		return fail(code, exit)
	}
	document, _ := json.Marshal(struct {
		FormatVersion int    `json:"format_version"`
		Status        string `json:"status"`
		Directory     string `json:"directory"`
	}{1, "created", publication.Directory})
	document = append(document, '\n')
	if n, err := input.Stdout.Write(document); err != nil || n != len(document) {
		return fail("published_output_failed", 1)
	}
	return report
}

type startProjectPreparationError struct{ code string }

func (err *startProjectPreparationError) Error() string { return "project preparation: " + err.code }
func (err *startProjectPreparationError) Is(target error) bool {
	want, ok := target.(*startProjectPreparationError)
	return ok && err != nil && want != nil && err.code == want.code
}

func prepareScaffoldProject(ctx context.Context, candidate string, input StartProjectInvocation, report *StartProjectReport) (result error) {
	failure := func(code string) error { return &startProjectPreparationError{code: code} }
	barrier := func(primary *string) *string {
		if code := commandInterruption(ctx, input.Interrupt); code != "" {
			return &code
		}
		return primary
	}
	var calls Report
	command := newProjectCommand(ctx, input.Interrupt, input.Backend, &calls, commandPolicy[string]{
		compatibility: func(code string) string { return code },
		selection:     func(Failure) string { return "candidate_selection_failed" },
		workspace:     func(Failure) string { return "build_workspace_failed" },
		barrier:       barrier,
		process: func(_ ProcessStage, process ProcessResult) *string {
			if process.Failure == nil && process.Started && process.ExitCode == 0 {
				return nil
			}
			code := "candidate_build_failed"
			return barrier(&code)
		},
		cleanup: func(primary *string, failed bool) *string {
			if failed {
				code := "build_cleanup_failed"
				return &code
			}
			return primary
		},
	}, commandHooks{})
	defer func() {
		if command.close() {
			result = errors.Join(result, failure("build_cleanup_failed"))
		}
	}()
	if code := command.open(candidate, "", input.Environment, workspaceHooks{}); code != nil {
		return failure(*code)
	}
	run := func(arguments ...string) error {
		if code := command.ready(); code != nil {
			return failure(*code)
		}
		process := input.Backend.Execute(ctx, input.Interrupt, BuildStage, cloneCommand(Command{
			Dir: candidate, Argv: append([]string{"go"}, arguments...), Env: command.workspace.buildEnvironment,
		}))
		recordProcess(&calls, BuildStage, process)
		clear(process.Stdout)
		process.Stdout = nil
		if process.BuildDiagnostic != "" {
			report.BuildDiagnostic = process.BuildDiagnostic
		}
		if code := command.completed(BuildStage, process); code != nil {
			return failure(*code)
		}
		if code := command.ready(); code != nil {
			return failure(*code)
		}
		return nil
	}
	// Bootstrap only the declaration graph: runtime imports do not exist yet.
	if err := run("list", "-mod=mod", "-deps", "./cmd/projectrunner"); err != nil {
		return err
	}
	generate := func(check bool) error {
		arguments := []string{"generate"}
		if check {
			arguments = append(arguments, "--check")
		}
		generated := RunGenerate(GenerationInvocation{
			// Retain the cache policy already selected for this preparation.
			// The nested command still creates its own private HOME/module/temp
			// roots. A cold invocation retains this outer private cache instead
			// of acquiring the caller's reusable one.
			Context: ctx, CWD: candidate, Args: arguments, Environment: command.workspace.buildEnvironment,
			Stdout: io.Discard, Stderr: io.Discard, Interrupt: input.Interrupt, Backend: input.Backend,
		})
		if generated.BuildDiagnostic != "" {
			report.BuildDiagnostic = generated.BuildDiagnostic
		}
		if generated.HasGenerationFailure {
			report.GenerationFailure = generated.GenerationFailure
		}
		// A nested command's failed process/workspace cleanup must survive a
		// concurrent cancellation at this outer publication boundary.
		if generated.CleanupFailed != 0 || generated.GenerationFailure.Code == GenerationCodeProjectCleanupFailed {
			return failure("build_cleanup_failed")
		}
		if generated.ExitCode != 0 || !generated.HasGenerationResult {
			return failure("candidate_generation_failed")
		}
		return nil
	}
	if err := generate(false); err != nil {
		return err
	}
	if err := run("mod", "tidy"); err != nil {
		return err
	}
	if code := command.build("./cmd/site", "godj-scaffold-site"); code != nil {
		report.BuildDiagnostic = calls.BuildDiagnostic
		return failure(*code)
	}
	if code := command.checkCompatibility(filepath.Join(command.workspace.root, "godj-scaffold-site")); code != nil {
		return failure(*code)
	}
	if code := command.build("./cmd/commands", "godj-scaffold-commands"); code != nil {
		report.BuildDiagnostic = calls.BuildDiagnostic
		return failure(*code)
	}
	if code := command.checkCompatibility(filepath.Join(command.workspace.root, "godj-scaffold-commands")); code != nil {
		return failure(*code)
	}
	return generate(true)
}
