//go:build darwin || linux

package godj

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	productcheck "github.com/progresshans/godj/internal/projectcheck"
	"github.com/progresshans/godj/internal/projectcheck/sqlmigrateprotocol"
)

const (
	migrationSQLRenderingArgvProbeCases       = 3
	migrationSQLRenderingArgvProbeMaxArgs     = 5
	migrationSQLRenderingArgvProbeStatement   = "SELECT 1"
	migrationSQLRenderingArgvProbeOutput      = migrationSQLRenderingArgvProbeStatement + ";\n"
	migrationSQLRenderingArgvProbeDefault     = "discovered_default"
	migrationSQLRenderingArgvProbeExplicit    = "./godj.toml"
	migrationSQLRenderingArgvProbePackagePath = "./cmd/site"
)

// migrationSQLRenderingArgvProbeEvidence contains no filesystem paths or
// mutable references. arguments returns a fresh copy for protocol rendering.
type migrationSQLRenderingArgvProbeEvidence struct {
	caseName       string
	form           string
	publicArgs     [migrationSQLRenderingArgvProbeMaxArgs]string
	publicArgCount int
	literalZero    bool

	app     string
	name    string
	project string
	output  string
	success bool
	stages  [2]string

	ancestorDirectoriesInspected int
	descriptorReads              int
	buildCalls                   int
	runnerCalls                  int
	runnerRequestReads           int
	directChildReaps             int
	runnerResponseWrites         int
	tempCreated                  int
	tempCleanupAttempts          int
	cleanupFailures              int
	residualTemp                 int
	stdoutWrites                 int
	stderrWrites                 int
}

func (evidence migrationSQLRenderingArgvProbeEvidence) arguments() []string {
	if evidence.publicArgCount < 0 || evidence.publicArgCount > len(evidence.publicArgs) {
		return nil
	}
	return append([]string(nil), evidence.publicArgs[:evidence.publicArgCount]...)
}

type migrationSQLRenderingArgvProbeCase struct {
	caseName            string
	form                string
	args                []string
	literalZero         bool
	ancestorInspections int
}

// migrationSQLRenderingProbeAcceptedArgv exercises the actual global
// RunSQLMigrate kernel while replacing only process execution. The fixed-size
// return value and scalar/fixed-array fields keep the published evidence
// detached from the temporary project, commands and private wire.
func migrationSQLRenderingProbeAcceptedArgv(
	ctx context.Context,
) (evidence [migrationSQLRenderingArgvProbeCases]migrationSQLRenderingArgvProbeEvidence, resultErr error) {
	if ctx == nil {
		return evidence, errors.New("migration SQL argv probe context is nil")
	}
	if err := ctx.Err(); err != nil {
		return evidence, err
	}
	fixture, err := newMigrationSQLRenderingArgvProbeFixture()
	if err != nil {
		return evidence, err
	}
	defer func() {
		if cleanupErr := fixture.close(); cleanupErr != nil {
			evidence = [migrationSQLRenderingArgvProbeCases]migrationSQLRenderingArgvProbeEvidence{}
			resultErr = errors.Join(resultErr, fmt.Errorf("clean migration SQL argv probe fixture: %w", cleanupErr))
		}
	}()

	cases := [migrationSQLRenderingArgvProbeCases]migrationSQLRenderingArgvProbeCase{
		{
			caseName: "implicit", form: "implicit",
			args: []string{"sqlmigrate", "blog", "0002_render_sql"}, ancestorInspections: 1,
		},
		{
			caseName: "explicit", form: "explicit",
			args: []string{"sqlmigrate", "blog", "0002_render_sql", "--project", migrationSQLRenderingArgvProbeExplicit},
		},
		{
			caseName: "literal_zero", form: "implicit", literalZero: true,
			args: []string{"sqlmigrate", "blog", "zero"}, ancestorInspections: 1,
		},
	}
	for index, probeCase := range cases {
		if err := ctx.Err(); err != nil {
			return evidence, err
		}
		observed, err := migrationSQLRenderingRunArgvProbe(ctx, fixture, probeCase)
		if err != nil {
			return evidence, fmt.Errorf("migration SQL argv probe %s: %w", probeCase.caseName, err)
		}
		evidence[index] = observed
	}
	return evidence, nil
}

type migrationSQLRenderingArgvProbeFixture struct {
	universe string
	project  string
	home     string
	temp     string
}

func newMigrationSQLRenderingArgvProbeFixture() (migrationSQLRenderingArgvProbeFixture, error) {
	temporaryUniverse, err := os.MkdirTemp("", "godj-sql-rendering-argv-")
	if err != nil {
		return migrationSQLRenderingArgvProbeFixture{}, fmt.Errorf("create migration SQL argv probe fixture: %w", err)
	}
	universe, err := filepath.EvalSymlinks(temporaryUniverse)
	if err == nil {
		universe, err = filepath.Abs(universe)
	}
	if err != nil {
		return migrationSQLRenderingArgvProbeFixture{}, errors.Join(
			fmt.Errorf("resolve migration SQL argv probe fixture: %w", err),
			os.RemoveAll(temporaryUniverse),
		)
	}
	fixture := migrationSQLRenderingArgvProbeFixture{
		universe: universe,
		project:  filepath.Join(universe, "project"),
		home:     filepath.Join(universe, "home"),
		temp:     filepath.Join(universe, "tmp"),
	}
	fail := func(primary error) (migrationSQLRenderingArgvProbeFixture, error) {
		return migrationSQLRenderingArgvProbeFixture{}, errors.Join(primary, os.RemoveAll(universe))
	}
	for _, directory := range []string{fixture.project, fixture.home, fixture.temp} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			return fail(fmt.Errorf("create migration SQL argv probe directory: %w", err))
		}
	}
	if filepath.Dir(fixture.project) != fixture.universe || filepath.Dir(fixture.home) != fixture.universe ||
		filepath.Dir(fixture.temp) != fixture.universe || fixture.project == fixture.home ||
		fixture.project == fixture.temp || fixture.home == fixture.temp {
		return fail(errors.New("migration SQL argv probe directories are not distinct siblings"))
	}
	descriptor := []byte("format_version = 1\n\n[project]\npackage = \"" + migrationSQLRenderingArgvProbePackagePath + "\"\n")
	if err := os.WriteFile(filepath.Join(fixture.project, "godj.toml"), descriptor, 0o600); err != nil {
		return fail(fmt.Errorf("write migration SQL argv probe descriptor: %w", err))
	}
	return fixture, nil
}

func (fixture migrationSQLRenderingArgvProbeFixture) close() error {
	if fixture.universe == "" {
		return errors.New("migration SQL argv probe fixture has no cleanup root")
	}
	return os.RemoveAll(fixture.universe)
}

func (fixture migrationSQLRenderingArgvProbeFixture) environment() []string {
	return []string{
		"GOENV=off",
		"GOPROXY=off",
		"GOSUMDB=off",
		"GOTELEMETRY=off",
		"GOTOOLCHAIN=local",
		"GOWORK=off",
		"HOME=" + fixture.home,
		"PATH=" + os.Getenv("PATH"),
		"TMPDIR=" + fixture.temp,
	}
}

type migrationSQLRenderingArgvProbeBackend struct {
	wire []byte

	calls        int
	stages       [2]productcheck.ProcessStage
	commands     [2]productcheck.Command
	request      sqlmigrateprotocol.Request
	requestReads int
	returnedWire []byte
	err          error
}

func (backend *migrationSQLRenderingArgvProbeBackend) Execute(
	_ context.Context,
	_ <-chan struct{},
	stage productcheck.ProcessStage,
	command productcheck.Command,
) productcheck.ProcessResult {
	if backend.calls >= len(backend.stages) {
		backend.err = errors.Join(backend.err, errors.New("migration SQL argv probe process called too many times"))
		return productcheck.ProcessResult{}
	}
	index := backend.calls
	backend.calls++
	backend.stages[index] = stage
	backend.commands[index] = migrationSQLRenderingCloneArgvProbeCommand(command)

	switch stage {
	case productcheck.BuildStage:
		return productcheck.ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1}
	case productcheck.SQLMigrateRunnerStage:
		request, failure, failed, err := sqlmigrateprotocol.ReadRequest(bytes.NewReader(command.Stdin))
		backend.requestReads++
		if err != nil {
			backend.err = errors.Join(backend.err, fmt.Errorf("read migration SQL argv probe runner request: %w", err))
		} else if failed || failure != (sqlmigrateprotocol.Failure{}) {
			backend.err = errors.Join(backend.err, errors.New("migration SQL argv probe runner request was rejected"))
		} else {
			backend.request = request
		}
		backend.returnedWire = append([]byte(nil), backend.wire...)
		return productcheck.ProcessResult{
			Started: true, ExitCode: 0, DirectReaps: 1,
			Stdout:       backend.returnedWire,
			StdoutScalar: productcheck.StreamScalar{RetainedBytes: len(backend.returnedWire)},
		}
	default:
		backend.err = errors.Join(backend.err, errors.New("migration SQL argv probe observed an unexpected process stage"))
		return productcheck.ProcessResult{}
	}
}

func migrationSQLRenderingCloneArgvProbeCommand(command productcheck.Command) productcheck.Command {
	return productcheck.Command{
		Dir:   command.Dir,
		Argv:  append([]string(nil), command.Argv...),
		Env:   append([]string(nil), command.Env...),
		Stdin: append([]byte(nil), command.Stdin...),
	}
}

func migrationSQLRenderingRunArgvProbe(
	ctx context.Context,
	fixture migrationSQLRenderingArgvProbeFixture,
	probeCase migrationSQLRenderingArgvProbeCase,
) (migrationSQLRenderingArgvProbeEvidence, error) {
	wire, err := sqlmigrateprotocol.EncodeResponse(sqlmigrateprotocol.Response{
		OK:     true,
		Result: sqlmigrateprotocol.Result{Statements: []string{migrationSQLRenderingArgvProbeStatement}},
	})
	if err != nil {
		return migrationSQLRenderingArgvProbeEvidence{}, fmt.Errorf("encode migration SQL argv probe response: %w", err)
	}
	backend := &migrationSQLRenderingArgvProbeBackend{wire: wire}
	var stdout, stderr bytes.Buffer
	report := productcheck.RunSQLMigrate(productcheck.SQLMigrateInvocation{
		Context:     ctx,
		CWD:         fixture.project,
		Args:        append([]string(nil), probeCase.args...),
		Environment: fixture.environment(),
		Stdout:      &stdout,
		Stderr:      &stderr,
		Backend:     backend,
	})
	if backend.err != nil {
		return migrationSQLRenderingArgvProbeEvidence{}, backend.err
	}
	if err := migrationSQLRenderingValidateArgvProbeReport(report, probeCase, len(wire), stdout.String(), stderr.String()); err != nil {
		return migrationSQLRenderingArgvProbeEvidence{}, err
	}
	if err := migrationSQLRenderingValidateArgvProbeCommands(fixture, probeCase, backend); err != nil {
		return migrationSQLRenderingArgvProbeEvidence{}, err
	}
	if !migrationSQLRenderingArgvProbeAllZero(backend.returnedWire) {
		return migrationSQLRenderingArgvProbeEvidence{}, errors.New("migration SQL argv probe private response was not discarded")
	}
	entries, err := os.ReadDir(fixture.temp)
	if err != nil {
		return migrationSQLRenderingArgvProbeEvidence{}, fmt.Errorf("inspect migration SQL argv probe cleanup: %w", err)
	}
	if len(entries) != 0 {
		return migrationSQLRenderingArgvProbeEvidence{}, errors.New("migration SQL argv probe left a private workspace")
	}

	project, err := migrationSQLRenderingDeriveArgvProbeProject(probeCase.args, fixture, backend.commands[1])
	if err != nil {
		return migrationSQLRenderingArgvProbeEvidence{}, err
	}
	if len(probeCase.args) > migrationSQLRenderingArgvProbeMaxArgs {
		return migrationSQLRenderingArgvProbeEvidence{}, errors.New("migration SQL argv probe public argv exceeds evidence capacity")
	}
	observed := migrationSQLRenderingArgvProbeEvidence{
		caseName: probeCase.caseName, form: probeCase.form, publicArgCount: len(probeCase.args), literalZero: probeCase.literalZero,
		app: backend.request.App, name: backend.request.Name, project: project,
		output: stdout.String(), success: true, stages: [2]string{"build", "sqlmigrate_runner"},
		ancestorDirectoriesInspected: report.AncestorDirectoriesInspected,
		descriptorReads:              report.DescriptorReads, buildCalls: report.BuildCalls, runnerCalls: report.RunnerCalls,
		runnerRequestReads: backend.requestReads, directChildReaps: report.DirectChildReaps,
		runnerResponseWrites: report.RunnerResponseWrites, tempCreated: report.TempCreated,
		tempCleanupAttempts: report.TempCleanupAttempts, cleanupFailures: report.CleanupFailed,
		residualTemp: report.ResidualTemp, stdoutWrites: report.UserStdoutWrites, stderrWrites: report.UserStderrWrites,
	}
	copy(observed.publicArgs[:], probeCase.args)
	return observed, nil
}

func migrationSQLRenderingValidateArgvProbeReport(
	report productcheck.SQLMigrateReport,
	probeCase migrationSQLRenderingArgvProbeCase,
	wireBytes int,
	stdout string,
	stderr string,
) error {
	if report.ExitCode != 0 || !report.HasSQLMigrateResult || report.HasSQLMigrateFailure ||
		report.SQLMigrateFailure != (sqlmigrateprotocol.Failure{}) ||
		len(report.SQLMigrateResult.Statements) != 1 || report.SQLMigrateResult.Statements[0] != migrationSQLRenderingArgvProbeStatement {
		return errors.New("migration SQL argv probe did not produce the exact successful result")
	}
	if report.AncestorDirectoriesInspected != probeCase.ancestorInspections || report.DescriptorReads != 1 {
		return errors.New("migration SQL argv probe did not perform the exact project selection")
	}
	if report.BuildCalls != 1 || report.RunnerCalls != 1 || report.RunnerResponseWrites != 1 ||
		report.RunnerStdoutRetainedBytes != wireBytes || report.RunnerStdoutTruncated || report.DirectChildReaps != 2 {
		return errors.New("migration SQL argv probe did not perform the exact build and runner lifecycle")
	}
	if report.TempCreated != 1 || report.TempCleanupAttempts != 1 || report.CleanupFailed != 0 || report.ResidualTemp != 0 {
		return errors.New("migration SQL argv probe did not complete exact private workspace cleanup")
	}
	if report.UserStdoutWrites != 1 || report.UserStderrWrites != 0 || report.PartialStdoutWrites != 0 ||
		stdout != migrationSQLRenderingArgvProbeOutput || stderr != "" {
		return errors.New("migration SQL argv probe did not publish the exact canonical output")
	}
	if report.GroupSIGINTAttempts != 0 || report.GroupSIGKILLAttempts != 0 ||
		report.BuildStdoutRetainedBytes != 0 || report.BuildStdoutTruncated ||
		report.BuildStderrRetainedBytes != 0 || report.BuildStderrTruncated ||
		report.RunnerStderrRetainedBytes != 0 || report.RunnerStderrTruncated || !report.RawDiagnosticsDiscarded {
		return errors.New("migration SQL argv probe process accounting was not closed")
	}
	return nil
}

func migrationSQLRenderingValidateArgvProbeCommands(
	fixture migrationSQLRenderingArgvProbeFixture,
	probeCase migrationSQLRenderingArgvProbeCase,
	backend *migrationSQLRenderingArgvProbeBackend,
) error {
	if backend.calls != 2 || backend.stages != ([2]productcheck.ProcessStage{
		productcheck.BuildStage,
		productcheck.SQLMigrateRunnerStage,
	}) {
		return errors.New("migration SQL argv probe process stages were not exact")
	}
	build := backend.commands[0]
	runner := backend.commands[1]
	if build.Dir != fixture.project || runner.Dir != fixture.project || len(build.Stdin) != 0 {
		return errors.New("migration SQL argv probe selected an unexpected project command root")
	}
	if len(build.Argv) != 7 || build.Argv[0] != "go" || build.Argv[1] != "build" ||
		build.Argv[2] != "-buildvcs=false" || build.Argv[3] != "-mod=readonly" || build.Argv[4] != "-o" ||
		build.Argv[6] != migrationSQLRenderingArgvProbePackagePath {
		return errors.New("migration SQL argv probe build command was not exact")
	}
	runnerBinary := build.Argv[5]
	workspaceRoot := filepath.Dir(runnerBinary)
	if !filepath.IsAbs(runnerBinary) || filepath.Base(runnerBinary) != "godj-project-runner" ||
		filepath.Dir(workspaceRoot) != fixture.temp {
		return errors.New("migration SQL argv probe runner was not isolated under the sibling temporary directory")
	}
	if len(runner.Argv) != 2 || runner.Argv[0] != runnerBinary || runner.Argv[1] != sqlmigrateprotocol.PrivateArgument ||
		!slices.Equal(build.Env, runner.Env) {
		return errors.New("migration SQL argv probe runner command was not exact")
	}
	childEnvironment := migrationSQLRenderingArgvProbeEnvironment(runner.Env)
	if childEnvironment["HOME"] != filepath.Join(workspaceRoot, "home") ||
		childEnvironment["TMPDIR"] != filepath.Join(workspaceRoot, "tmp") ||
		childEnvironment["GOWORK"] != "off" || childEnvironment["GOTOOLCHAIN"] != "local" ||
		childEnvironment["GOENV"] != "off" {
		return errors.New("migration SQL argv probe runner environment was not isolated")
	}
	if backend.requestReads != 1 || backend.request != (sqlmigrateprotocol.Request{App: probeCase.args[1], Name: probeCase.args[2]}) {
		return errors.New("migration SQL argv probe runner request did not match public argv")
	}
	return nil
}

func migrationSQLRenderingArgvProbeEnvironment(entries []string) map[string]string {
	values := make(map[string]string, len(entries))
	for _, entry := range entries {
		key, value, ok := strings.Cut(entry, "=")
		if ok && key != "" {
			values[key] = value
		}
	}
	return values
}

func migrationSQLRenderingDeriveArgvProbeProject(
	args []string,
	fixture migrationSQLRenderingArgvProbeFixture,
	runner productcheck.Command,
) (string, error) {
	if runner.Dir != fixture.project {
		return "", errors.New("migration SQL argv probe selected an unexpected project")
	}
	switch len(args) {
	case 3:
		if args[0] != "sqlmigrate" || args[1] == "" || args[2] == "" {
			return "", errors.New("migration SQL argv probe implicit form is invalid")
		}
		return migrationSQLRenderingArgvProbeDefault, nil
	case 5:
		if args[0] != "sqlmigrate" || args[1] == "" || args[2] == "" || args[3] != "--project" ||
			args[4] != migrationSQLRenderingArgvProbeExplicit ||
			filepath.Clean(filepath.Join(fixture.project, args[4])) != filepath.Join(fixture.project, "godj.toml") {
			return "", errors.New("migration SQL argv probe explicit form is invalid")
		}
		return migrationSQLRenderingArgvProbeExplicit, nil
	default:
		return "", errors.New("migration SQL argv probe accepted form is invalid")
	}
}

func migrationSQLRenderingArgvProbeAllZero(document []byte) bool {
	for _, value := range document {
		if value != 0 {
			return false
		}
	}
	return true
}
