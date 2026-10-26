//go:build darwin || linux

package projectcheck

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"time"

	"github.com/progresshans/godj/codegen"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/internal/firstaccountwire"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/project/firstaccount"
	"github.com/progresshans/godj/schema/ir"
)

// FirstAccountInvocation uses the generated-aware commands package in the
// retained project. Neither arguments nor environment carry profile/password
// input. The declaration-only project runner remains free of generated imports.
type FirstAccountInvocation struct {
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

// FirstAccountResult preserves the parent's operation identity and durable
// outcome separately from resource cleanup and public delivery. A failure or
// a missing receipt must never be treated as permission to retry this input.
type FirstAccountResult struct {
	OperationID   string `json:"operation_id"`
	Status        string `json:"status"`
	Code          string `json:"code"`
	CleanupFailed bool   `json:"cleanup_failed"`
}

type FirstAccountReport struct {
	RunServerReport
	Terminal                      FirstAccountTerminalReport
	Result                        FirstAccountResult
	DescriptionCalls              int
	SensitiveRequestWriteAttempts int
	SensitiveRequestBytesWritten  int
	SensitiveResponseBytes        int
	SensitiveResponseTruncated    bool
	PublicationFailed             bool
}

type firstAccountParentHooks struct {
	prepare       func(RunServerInvocation, string, runtimeTarget, *RunServerReport) (preparedProjectRuntime, *RunServerFailure)
	terminal      func(context.Context, <-chan struct{}, *os.File, io.Writer, firstaccount.Description, *FirstAccountTerminalReport) (firstaccountwire.Request, string)
	operationID   func() (string, error)
	process       func(context.Context, <-chan struct{}, createsuperuserProcessCommand, []byte, string) createsuperuserProcessResult
	beforePublish func()
}

// RunFirstAccount executes the selected-model operator owner. It checks the
// declaration/runtime ABI and complete generated bundle before reading a TTY,
// obtains the actual typed field description, then hands one bounded secret
// frame to that exact runtime. It performs no automatic retry or adoption.
// The public createsuperuser command uses this owner with the host's explicitly
// selected account runtime and independent domain/audit/session installation.
func RunFirstAccount(input FirstAccountInvocation) FirstAccountReport {
	return runFirstAccount(input, firstAccountParentHooks{})
}

func runFirstAccount(input FirstAccountInvocation, hooks firstAccountParentHooks) FirstAccountReport {
	report := FirstAccountReport{Result: FirstAccountResult{Status: string(firstaccountwire.NotCreated)}}
	input.Args = append([]string(nil), input.Args...)
	arguments, invalid := parseCreatesuperuserArguments(input.Args)
	if invalid != nil {
		report.Result.Code = "invalid_arguments"
		publishFirstAccount(input, &report, hooks.beforePublish)
		return report
	}
	input.Context, input.Environment, input.Backend = normalizeCommandInput(input.Context, input.Environment, input.Backend)
	if hooks.prepare == nil {
		hooks.prepare = prepareProjectRuntime
	}
	if hooks.terminal == nil {
		hooks.terminal = readFirstAccountTerminal
	}
	if hooks.operationID == nil {
		hooks.operationID = newFirstAccountOperationID
	}
	if hooks.process == nil {
		hooks.process = executeFirstAccountProcess
	}
	runtimeInput := RunServerInvocation{Context: input.Context, CWD: input.CWD, Environment: input.Environment,
		Stdout: input.Stdout, Stderr: input.Stderr, Interrupt: input.Interrupt, Backend: input.Backend,
		workspace: input.workspace, generation: completeGenerationHooks(input.generation)}
	prepared, failed := hooks.prepare(runtimeInput, arguments.explicitDescriptor, runtimeCommands, &report.RunServerReport)
	finish := func() FirstAccountReport {
		if prepared.command != nil && prepared.command.close() {
			report.Result.CleanupFailed = true
			if report.Result.Status == string(firstaccountwire.Created) && report.Result.Code == "" {
				report.Result.Code = "created_cleanup_failed"
			} else if report.Result.Code == "" {
				report.Result.Code = "project_cleanup_failed"
			}
		}
		publishFirstAccount(input, &report, hooks.beforePublish)
		return report
	}
	preparationFailure := func(failure *RunServerFailure) {
		if _, valid := runserverExitCode(*failure); !valid {
			report.Result.Code = "internal_error"
		} else if failure.Code == RunServerCodeNotConfigured {
			report.Result.Code = "first_account_not_configured"
		} else {
			report.Result.Code = failure.Code
		}
	}
	if failed != nil {
		preparationFailure(failed)
		return finish()
	}
	if prepared.command == nil || prepared.binary == "" {
		report.Result.Code = "internal_error"
		return finish()
	}
	if prepared.projectSpec.Account == nil {
		report.Result.Code = "first_account_not_configured"
		return finish()
	}
	if failure := prepared.command.ready(); failure != nil {
		preparationFailure(failure)
		return finish()
	}
	description, code := describeFirstAccount(input, prepared, &report)
	if code != "" {
		report.Result.Code = code
		return finish()
	}
	if !firstAccountDescriptionMatchesProject(description, prepared.projectSpec) {
		report.Result.Code = "first_account_source_mismatch"
		return finish()
	}
	if failure := prepared.command.ready(); failure != nil {
		preparationFailure(failure)
		return finish()
	}
	request, code := hooks.terminal(input.Context, input.Interrupt, input.Stdin, input.Stderr, description, &report.Terminal)
	defer request.Clear()
	if code != "" {
		report.Result.Code = firstAccountInputCode(code)
		request.Clear()
		return finish()
	}
	if failure := prepared.command.ready(); failure != nil {
		preparationFailure(failure)
		request.Clear()
		return finish()
	}
	if !firstAccountInputMatches(description, request) {
		report.Result.Code = "invalid_input"
		request.Clear()
		return finish()
	}
	operationID, err := hooks.operationID()
	if err != nil || !firstaccountwire.ValidDigest(operationID) {
		report.Result.Code = "internal_error"
		request.Clear()
		return finish()
	}
	report.Result.OperationID = operationID
	request.OperationID = operationID
	frame, err := firstaccountwire.EncodeRequest(request)
	request.Clear()
	if err != nil {
		report.Result.Code = "invalid_input"
		clear(frame)
		return finish()
	}
	if failure := prepared.command.ready(); failure != nil {
		clear(frame)
		preparationFailure(failure)
		return finish()
	}
	command := newCreatesuperuserProcessCommand(prepared.command.selected.rootPath,
		[]string{prepared.binary, firstaccountwire.ProvisionArgument}, prepared.command.workspace.environment)
	report.RunnerCalls++
	expected := len(frame)
	process := hooks.process(input.Context, input.Interrupt, command, frame, operationID)
	clear(frame)
	frame = nil
	report.DirectChildReaps += process.directReaps
	report.GroupSIGINTAttempts += process.sigintAttempts
	report.GroupSIGKILLAttempts += process.sigkillAttempts
	report.SensitiveRequestWriteAttempts = process.requestWriteAttempts
	report.SensitiveRequestBytesWritten = process.requestBytesWritten
	report.SensitiveResponseBytes = process.stdoutScalar.RetainedBytes
	report.SensitiveResponseTruncated = process.stdoutScalar.Truncated
	report.RunnerStderrRetainedBytes = process.stderrScalar.RetainedBytes
	report.RunnerStderrTruncated = process.stderrScalar.Truncated
	report.RawDiagnosticsDiscarded = true
	if process.cleanupFailed {
		report.CleanupFailed = 1
	}
	report.Result = firstAccountProcessOutcome(process, expected, operationID)
	process.discardResponse()
	// This result already describes a completed operation. There is deliberately
	// no cancellation, authority, source or model check after this boundary.
	return finish()
}

func newFirstAccountOperationID() (string, error) {
	var entropy [32]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(entropy[:]), nil
}

func firstAccountDescriptionMatchesProject(description firstaccount.Description, spec codegen.ProjectSpec) bool {
	if spec.Account == nil {
		return false
	}
	schemas := make([]ir.Schema, len(spec.Apps))
	for i, app := range spec.Apps {
		schemas[i] = app.Schema
	}
	project, err := orm.BindProject(schemas...)
	if err != nil {
		return false
	}
	source, err := project.SourceSHA256()
	if err != nil || source != description.SourceSHA256 {
		return false
	}
	role, err := accountmodel.RoleFingerprint(*spec.Account, schemas...)
	return err == nil && role == description.RoleSHA256 && description.Login == spec.Account.Login &&
		description.Model == spec.Account.Model.AppLabel+"."+spec.Account.Model.ModelName
}

func firstAccountInputMatches(description firstaccount.Description, request firstaccountwire.Request) bool {
	if request.Fingerprint != description.Fingerprint || len(request.Values) != len(description.Fields) ||
		len(request.Password) == 0 || len(request.Password) > description.MaxPasswordBytes {
		return false
	}
	for i, value := range request.Values {
		if value.Name != description.Fields[i].Name || len(value.Text) > description.Fields[i].MaxBytes {
			return false
		}
	}
	return true
}

func describeFirstAccount(input FirstAccountInvocation, prepared preparedProjectRuntime, report *FirstAccountReport) (firstaccount.Description, string) {
	ctx, cancel := context.WithTimeout(input.Context, 15*time.Second)
	defer cancel()
	report.DescriptionCalls++
	report.RunnerCalls++
	process := input.Backend.Execute(ctx, input.Interrupt, FirstAccountDescriptionStage, cloneCommand(Command{
		Dir: prepared.command.selected.rootPath, Argv: []string{prepared.binary, firstaccountwire.DescribeArgument},
		Env: prepared.command.workspace.environment,
	}))
	recordProcess(&report.Report, FirstAccountDescriptionStage, process)
	defer clear(process.Stdout)
	if process.CleanupFailed {
		return firstaccount.Description{}, "project_cleanup_failed"
	}
	if code := commandInterruption(input.Context, input.Interrupt); code != "" {
		return firstaccount.Description{}, code
	}
	if ctx.Err() != nil || process.Failure != nil || !process.Started || process.ExitCode != 0 || process.DirectReaps != 1 ||
		process.StdoutScalar.Truncated || process.StderrScalar.Truncated || process.StdoutScalar.RetainedBytes != len(process.Stdout) {
		return firstaccount.Description{}, "first_account_description_failed"
	}
	response, err := firstaccountwire.DecodeDescription(process.Stdout)
	if err != nil {
		return firstaccount.Description{}, "first_account_description_failed"
	}
	if response.Description == nil {
		return firstaccount.Description{}, string(response.Code)
	}
	return *response.Description, ""
}

func firstAccountInputCode(code string) string {
	switch code {
	case "invalid_config", "input_not_terminal", "terminal_state_failed", "input_read_failed", "invalid_input",
		"invalid_password", "password_mismatch", "project_canceled", "project_interrupted":
		return code
	default:
		return "internal_error"
	}
}

func publishFirstAccount(input FirstAccountInvocation, report *FirstAccountReport, before func()) {
	if before != nil {
		before()
	}
	report.ExitCode = 3
	if report.Result.Status == string(firstaccountwire.Created) && report.Result.Code == "" && !report.Result.CleanupFailed {
		report.ExitCode = 0
	} else if !report.Result.CleanupFailed && report.Result.Status == string(firstaccountwire.NotCreated) {
		switch report.Result.Code {
		case "invalid_arguments", "invalid_input", "invalid_password", "password_mismatch", "input_not_terminal":
			report.ExitCode = 2
		case "account_domain_not_fresh":
			report.ExitCode = 1
		case "project_interrupted":
			report.ExitCode = 130
		}
	}
	document, err := json.Marshal(report.Result)
	if err != nil {
		report.PublicationFailed, report.ExitCode = true, 3
		return
	}
	document = append(document, '\n')
	writer := input.Stderr
	if report.ExitCode == 0 {
		writer = input.Stdout
		report.UserStdoutWrites++
	} else {
		report.UserStderrWrites++
	}
	if !completeCreatesuperuserWrite(writer, document) {
		report.PublicationFailed, report.ExitCode = true, 3
	}
}
