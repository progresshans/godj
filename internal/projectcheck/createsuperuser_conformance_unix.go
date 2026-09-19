//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/progresshans/godj/internal/projectcheck/createsuperuserprotocol"
)

// CreatesuperuserOwnershipConformanceInput is the narrow in-process adapter
// used to observe the real global orchestration around one real private child
// implementation. It is internal to this module and is not a product command
// extension point.
type CreatesuperuserOwnershipConformanceInput struct {
	Context                 context.Context
	Username                []byte
	Password                []byte
	AfterArgumentValidation func()
	AfterProjectSelection   func()
	BeforePublicPublication func()
	ExecutePrivate          func(context.Context, []byte) ([]byte, error)
}

func (CreatesuperuserOwnershipConformanceInput) String() string {
	return "projectcheck.CreatesuperuserOwnershipConformanceInput{redacted}"
}

func (CreatesuperuserOwnershipConformanceInput) GoString() string {
	return "projectcheck.CreatesuperuserOwnershipConformanceInput{redacted}"
}

// RunCreatesuperuserOwnershipConformance executes runCreatesuperuser itself;
// only OS selection/build/TTY/process mechanics are replaced by bounded local
// adapters. The callbacks are invoked at the actual orchestration callsites,
// and ExecutePrivate receives the exact encoded frame once.
func RunCreatesuperuserOwnershipConformance(
	input CreatesuperuserOwnershipConformanceInput,
) (CreatesuperuserReport, error) {
	if input.Context == nil {
		return CreatesuperuserReport{}, errors.New("createsuperuser ownership conformance: nil context")
	}
	if err := input.Context.Err(); err != nil {
		return CreatesuperuserReport{}, err
	}
	if input.ExecutePrivate == nil {
		return CreatesuperuserReport{}, errors.New("createsuperuser ownership conformance: nil private executor")
	}
	username := append([]byte(nil), input.Username...)
	password := append([]byte(nil), input.Password...)
	defer clear(username)
	defer clear(password)

	backend := &createsuperuserOwnershipConformanceBackend{}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var privateErr error
	report := runCreatesuperuser(CreatesuperuserInvocation{
		Context: input.Context,
		CWD:     "/godj-createsuperuser-conformance-project",
		Args:    []string{"createsuperuser"},
		Stdout:  &stdout,
		Stderr:  &stderr,
		Backend: backend,
	}, createsuperuserRunHooks{
		afterArgumentValidation: input.AfterArgumentValidation,
		afterProjectSelection:   input.AfterProjectSelection,
		selectProject: func(string, commandArguments, *Report) (retainedProject, *Failure) {
			return retainedProject{
				rootPath: "/godj-createsuperuser-conformance-project",
				descriptor: projectDescriptor{
					formatVersion: 1,
					packagePath:   "./cmd/projectrunner",
				},
			}, nil
		},
		verifyRetainedProject: func(retainedProject) bool { return true },
		createPrivateWorkspace: func(
			_ retainedProject,
			_ []string,
			report *Report,
			_ workspaceHooks,
		) (privateWorkspace, *Failure) {
			report.TempCreated++
			return privateWorkspace{root: "/godj-createsuperuser-conformance-workspace"}, nil
		},
		readTerminal: func(
			_ context.Context,
			_ <-chan struct{},
			_ *os.File,
			_ io.Writer,
			report *CreatesuperuserReport,
		) ([]byte, []byte, *CreatesuperuserFailure) {
			report.TerminalChecks++
			report.UsernamePromptWrites++
			report.PasswordPromptWrites++
			report.ConfirmationPromptWrites++
			return username, password, nil
		},
		executeSensitiveProcess: func(
			ctx context.Context,
			_ <-chan struct{},
			_ createsuperuserProcessCommand,
			document []byte,
		) createsuperuserProcessResult {
			requestBytes := len(document)
			request := append([]byte(nil), document...)
			response, err := input.ExecutePrivate(ctx, request)
			clear(request)
			if err != nil {
				privateErr = err
				clear(response)
				return createsuperuserProcessResult{
					started:              true,
					exited:               true,
					exitCode:             1,
					directReaps:          1,
					requestWriteAttempts: 1,
					requestBytesWritten:  requestBytes,
				}
			}
			return createsuperuserProcessResult{
				response:             response,
				started:              true,
				exited:               true,
				exitCode:             0,
				directReaps:          1,
				stdoutScalar:         StreamScalar{RetainedBytes: len(response)},
				requestWriteAttempts: 1,
				requestBytesWritten:  requestBytes,
			}
		},
		closeProject:            func(*retainedProject) error { return nil },
		cleanupWorkspace:        func(*privateWorkspace) error { return nil },
		beforePublicPublication: input.BeforePublicPublication,
	})
	if privateErr != nil {
		return report, fmt.Errorf("createsuperuser ownership conformance: private execution: %w", privateErr)
	}
	if backend.calls != 1 || backend.unexpectedStage || !report.HasCreatesuperuserResult ||
		report.HasCreatesuperuserFailure || !report.KnownCreated || report.ExitCode != 0 {
		return report, fmt.Errorf("createsuperuser ownership conformance: global orchestration drifted: %+v", report)
	}
	want := createsuperuserprotocol.PublicSuccessDocument()
	defer clear(want)
	if !bytes.Equal(stdout.Bytes(), want) || stderr.Len() != 0 {
		return report, errors.New("createsuperuser ownership conformance: public output drifted")
	}
	return report, nil
}

type createsuperuserOwnershipConformanceBackend struct {
	calls           int
	unexpectedStage bool
}

func (backend *createsuperuserOwnershipConformanceBackend) Execute(
	_ context.Context,
	_ <-chan struct{},
	stage ProcessStage,
	_ Command,
) ProcessResult {
	backend.calls++
	if stage != BuildStage {
		backend.unexpectedStage = true
	}
	return ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1}
}
