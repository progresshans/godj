//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/firstaccountwire"
	"github.com/progresshans/godj/project/compatibility"
	"github.com/progresshans/godj/project/firstaccount"
	"github.com/progresshans/godj/schema/ir"
)

type firstAccountDescriptionBackend struct {
	t        *testing.T
	document []byte
	calls    int
}

func (*firstAccountDescriptionBackend) ToolInfo() compatibility.Info { return fixtureToolInfo() }
func (backend *firstAccountDescriptionBackend) Execute(_ context.Context, _ <-chan struct{}, stage ProcessStage, command Command) ProcessResult {
	backend.calls++
	if stage != FirstAccountDescriptionStage || len(command.Argv) != 2 || command.Argv[1] != firstaccountwire.DescribeArgument || len(command.Stdin) != 0 {
		backend.t.Fatal("description used an ordinary or sensitive command", stage)
	}
	document := bytes.Clone(backend.document)
	return ProcessResult{Started: true, DirectReaps: 1, ExitCode: 0, Stdout: document, StdoutScalar: StreamScalar{RetainedBytes: len(document)}}
}

func TestFirstAccountParentRejectsArgumentsBeforeProjectOrInput(t *testing.T) {
	backend := &firstAccountDescriptionBackend{t: t}
	var out, diagnostic bytes.Buffer
	report := RunFirstAccount(FirstAccountInvocation{Context: t.Context(), CWD: "/missing/private-project",
		Args: []string{"createsuperuser", "--password", "private-argument"}, Backend: backend, Stdout: &out, Stderr: &diagnostic})
	if report.ExitCode != 2 || report.Result.Status != "not_created" || report.Result.Code != "invalid_arguments" ||
		backend.calls != 0 || report.AncestorDirectoriesInspected != 0 || report.Terminal.Checks != 0 || out.Len() != 0 || strings.Contains(diagnostic.String(), "private-") {
		t.Fatal("invalid arguments reached project or terminal work", report)
	}
}

func TestFirstAccountParentChecksSourceClearsInputsAndPreservesCompletion(t *testing.T) {
	for _, test := range []struct {
		name, status, code      string
		exit, terminal, process int
		cleanup, outputFailure  bool
	}{
		{"created", "created", "", 0, 1, 1, false, false},
		{"late_cancel", "created", "", 0, 1, 1, false, false},
		{"cleanup", "created", "created_cleanup_failed", 3, 1, 1, true, false},
		{"output", "created", "", 3, 1, 1, false, true},
		{"stale_bundle", "not_created", "generated_bundle_stale", 3, 0, 0, false, false},
		{"no_account", "not_created", "first_account_not_configured", 3, 0, 0, false, false},
		{"foreign_role", "not_created", "first_account_source_mismatch", 3, 0, 0, false, false},
		{"foreign_source", "not_created", "first_account_source_mismatch", 3, 0, 0, false, false},
		{"bad_description", "not_created", "first_account_description_failed", 3, 0, 0, false, false},
		{"wrong_input_field", "not_created", "invalid_input", 2, 1, 0, false, false},
		{"wrong_input_source", "not_created", "invalid_input", 2, 1, 0, false, false},
		{"terminal_failure", "not_created", "invalid_input", 2, 1, 0, false, false},
		{"private_terminal_failure", "not_created", "internal_error", 3, 1, 0, false, false},
		{"source_changed", "not_created", "project_selection_failed", 3, 1, 0, false, false},
		{"entropy", "not_created", "internal_error", 3, 1, 0, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			description, spec := firstAccountParentDescription(t)
			document, err := firstaccountwire.EncodeDescription(firstaccountwire.DescriptionResponse{Version: firstaccountwire.Version, Description: &description})
			if err != nil {
				t.Fatal(err)
			}
			backend := &firstAccountDescriptionBackend{t: t, document: document}
			if test.name == "bad_description" {
				backend.document = []byte("private-description-output")
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			terminalCalls, processCalls := 0, 0
			validProject := true
			closed := []string{}
			var profile, password, transferred []byte
			assertCleared := func() {
				t.Helper()
				for _, buffer := range [][]byte{profile, password, transferred} {
					if !allZeroCreatesuperuserBytes(buffer) {
						t.Fatal("private input survived into cleanup or public publication")
					}
				}
			}
			stdout, stderr := &createsuperuserRunWriter{}, &createsuperuserRunWriter{}
			for _, writer := range []*createsuperuserRunWriter{stdout, stderr} {
				writer.before = func() {
					assertCleared()
					if !reflect.DeepEqual(closed, []string{"project", "workspace"}) {
						t.Fatal("public result preceded resource cleanup", closed)
					}
				}
			}
			stdout.short = test.outputFailure
			hooks := firstAccountParentHooks{
				prepare: func(input RunServerInvocation, _ string, target runtimeTarget, report *RunServerReport) (preparedProjectRuntime, *RunServerFailure) {
					if target != runtimeCommands {
						t.Fatal("account command used the declaration-only runtime")
					}
					command := newProjectCommand(input.Context, input.Interrupt, input.Backend, &report.Report,
						commandPolicy[RunServerFailure]{selection: mapRunserverSelectionFailure, barrier: func(f *RunServerFailure) *RunServerFailure {
							if code := commandInterruption(input.Context, input.Interrupt); code != "" {
								return &RunServerFailure{Category: RunServerCategoryProcess, Code: code}
							}
							return f
						}}, commandHooks{verifyProject: func(retainedProject) bool { return validProject },
							closeProject: func(*retainedProject) error { assertCleared(); closed = append(closed, "project"); return nil },
							closeWorkspace: func(*privateWorkspace) error {
								assertCleared()
								closed = append(closed, "workspace")
								if test.cleanup {
									return errors.New("private cleanup")
								}
								return nil
							}})
					command.selected.rootPath = "/owned/project"
					command.workspace = privateWorkspace{root: "/owned/private", environment: []string{"MODE=safe"}}
					command.hasWorkspace = true
					report.TempCreated++
					switch test.name {
					case "no_account":
						spec.Account = nil
					case "foreign_role":
						spec.Account.AuthenticateNormalization = ir.AccountUnicode16NFKC
					case "foreign_source":
						for i := range spec.Apps {
							for j := range spec.Apps[i].Schema.Models {
								for k := range spec.Apps[i].Schema.Models[j].Fields {
									field := &spec.Apps[i].Schema.Models[j].Fields[k]
									if field.Name == "first_name" {
										field.MaxLength++
									}
								}
							}
						}
					}
					prepared := preparedProjectRuntime{command: command, binary: "/owned/private/runtime", projectSpec: spec}
					if test.name == "stale_bundle" {
						return prepared, &RunServerFailure{Category: RunServerCategoryGeneration, Code: RunServerCodeGeneratedBundleStale}
					}
					return prepared, nil
				},
				terminal: func(context.Context, <-chan struct{}, *os.File, io.Writer, firstaccount.Description, *FirstAccountTerminalReport, string) (firstaccountwire.Request, string) {
					terminalCalls++
					profile, password = []byte("private-profile"), []byte("private-password")
					request := firstaccountwire.Request{Fingerprint: description.Fingerprint, Password: password}
					for i, field := range description.Fields {
						value := firstaccount.Value{Name: field.Name}
						if i == 0 {
							value.Present, value.Text = true, profile
						}
						request.Values = append(request.Values, value)
					}
					switch test.name {
					case "terminal_failure":
						return request, "invalid_input"
					case "private_terminal_failure":
						return request, "private terminal cause"
					case "wrong_input_field":
						request.Values[0].Name = "foreign"
					case "wrong_input_source":
						request.Fingerprint = strings.Repeat("f", 64)
					case "source_changed":
						validProject = false
					}
					return request, ""
				},
				operationID: func() (string, error) {
					if test.name == "entropy" {
						return "", errors.New("private entropy failure")
					}
					return strings.Repeat("e", 64), nil
				},
				process: func(_ context.Context, _ <-chan struct{}, command createsuperuserProcessCommand, frame []byte, operation string) createsuperuserProcessResult {
					processCalls++
					assertCleared()
					if len(command.argv) != 2 || command.argv[1] != firstaccountwire.ProvisionArgument || !reflect.DeepEqual(command.env, []string{"MODE=safe"}) {
						t.Fatal("secret operation used an ordinary transport")
					}
					request, err := firstaccountwire.DecodeRequest(frame)
					if err != nil || request.OperationID != operation || request.Fingerprint != description.Fingerprint || string(request.Values[0].Text) != "private-profile" || string(request.Password) != "private-password" {
						t.Fatal("exact parent operation or input lost", err)
					}
					request.Clear()
					transferred = frame
					response := firstAccountResponse(t, operation, firstaccountwire.Created, false)
					if test.name == "late_cancel" {
						cancel()
					}
					return createsuperuserProcessResult{started: true, exited: true, exitCode: 0, directReaps: 1,
						requestWriteAttempts: 1, requestBytesWritten: len(frame), response: response, stdoutScalar: StreamScalar{RetainedBytes: len(response)}}
				},
			}
			report := runFirstAccount(FirstAccountInvocation{Context: ctx, CWD: "/owned/project", Args: []string{"createsuperuser"},
				Backend: backend, Stdout: stdout, Stderr: stderr}, hooks)
			assertCleared()
			if report.ExitCode != test.exit || report.Result.Status != test.status || report.Result.Code != test.code ||
				report.Result.CleanupFailed != test.cleanup || report.PublicationFailed != test.outputFailure || terminalCalls != test.terminal || processCalls != test.process || stdout.calls+stderr.calls != 1 {
				t.Fatal("parent changed authority, completion or publication", report, terminalCalls, processCalls)
			}
			for _, output := range []string{stdout.String(), stderr.String(), fmt.Sprint(report)} {
				for _, marker := range []string{"private-profile", "private-password", "private cleanup", "private entropy", "private-description", "private terminal"} {
					if strings.Contains(output, marker) {
						t.Fatal("parent published private input or execution text")
					}
				}
			}
		})
	}
}
