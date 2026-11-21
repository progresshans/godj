// Package runner serves the selected-account operator protocol in the project's
// generated-aware runtime. It owns one acquisition, one attempt and one close.
// The declaration runner must not import this runtime or generated model code.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"

	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/internal/callback"
	"github.com/progresshans/godj/internal/firstaccountprotocol"
	"github.com/progresshans/godj/internal/firstaccountwire"
	"github.com/progresshans/godj/project/firstaccount"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

var callbackAborted = errors.New("first account runner: callback aborted")

// Error is an invocation/publication failure, not a submitted value or backend
// cause. Product outcomes travel in the bounded response. A process entrypoint
// must use ExitCode and keep SIGPIPE observable until Serve has returned.
type Error struct {
	code             string
	outcome          firstaccountwire.Outcome
	cleanupFailed    bool
	fileReportFailed bool
}

func (err *Error) Error() string {
	if err == nil {
		return "<nil>"
	}
	switch err.code {
	case "invalid_invocation", "output_failed":
		return "first account runner: " + err.code
	default:
		return "first account runner: internal_error"
	}
}
func (err Error) Format(state fmt.State, _ rune) { _, _ = state.Write([]byte(err.Error())) }

// ExitCode preserves a known commit even if its response could not be written.
// These private exits must never be interpreted as an ordinary retryable
// failure. The parent still requires the complete single request handoff.
func (err *Error) ExitCode() int {
	if err == nil {
		return 0
	}
	if err.outcome == firstaccountwire.Created {
		if err.fileReportFailed {
			if err.cleanupFailed {
				return firstaccountwire.KnownCreatedCleanupFileReportResponseFailureExitCode
			}
			return firstaccountwire.KnownCreatedFileReportResponseFailureExitCode
		}
		if err.cleanupFailed {
			return firstaccountwire.KnownCreatedCleanupResponseFailureExitCode
		}
		return firstaccountwire.KnownCreatedResponseFailureExitCode
	}
	return 1
}

// Serve recognizes only its two private commands; ordinary management argv is
// left to its caller. The description is resource-free. Provision reads one
// bounded secret frame, cleans the actual ModelForm before opening a DB, and
// closes every returned resource before its sole result publication.
// No ordinary command handler, logger, argv parser or retry sees the input.
func Serve(ctx context.Context, command firstaccount.Command, argv []string, stdin io.Reader, stdout io.Writer) (handled bool, err error) {
	if len(argv) == 0 || argv[0] != firstaccountprotocol.DescribeArgument && argv[0] != firstaccountprotocol.ProvisionArgument {
		return false, nil
	}
	if len(argv) != 1 || nilValue(ctx) || nilValue(stdout) || argv[0] == firstaccountprotocol.ProvisionArgument && nilValue(stdin) {
		return true, &Error{code: "invalid_invocation"}
	}
	if argv[0] == firstaccountprotocol.DescribeArgument {
		return true, describe(ctx, command, stdout)
	}
	return true, provision(ctx, command, stdin, stdout)
}

func describe(ctx context.Context, command firstaccount.Command, stdout io.Writer) error {
	response := firstaccountwire.DescriptionResponse{Version: firstaccountprotocol.Version}
	if ctx.Err() != nil {
		response.Code = firstaccountwire.Canceled
	} else {
		description, err := command.Description()
		if err != nil {
			response.Code = firstaccountwire.InvalidConfig
		} else {
			response.Description = &description
		}
	}
	document, err := firstaccountwire.EncodeDescription(response)
	if err != nil {
		return &Error{code: "internal_error"}
	}
	return publish(stdout, document, firstaccountwire.NotCreated, false, false)
}

func provision(ctx context.Context, command firstaccount.Command, stdin io.Reader, stdout io.Writer) error {
	response := firstaccountwire.Response{Version: firstaccountprotocol.Version, Outcome: firstaccountwire.NotCreated}
	complete := func() error {
		document, err := firstaccountwire.EncodeResponse(response)
		if err != nil {
			return &Error{code: "internal_error", outcome: response.Outcome, cleanupFailed: response.CleanupFailed, fileReportFailed: response.FileReportFailed}
		}
		return publish(stdout, document, response.Outcome, response.CleanupFailed, response.FileReportFailed)
	}
	if ctx.Err() != nil {
		response.Code = firstaccountwire.Canceled
		return complete()
	}
	request, err := callback.Invoke(func() (firstaccountwire.Request, error) {
		return firstaccountwire.ReadRequest(stdin)
	}, firstaccountwire.ErrInputTransport)
	if err != nil {
		response.Code = firstaccountwire.InputTransportFailed
		if err == firstaccountwire.ErrInvalidRequest {
			response.Code = firstaccountwire.InvalidRequest
		}
		return complete()
	}
	defer request.Clear()
	response.OperationID = request.OperationID
	description, err := command.Description()
	if err != nil {
		request.Clear()
		response.Code = firstaccountwire.InvalidConfig
		return complete()
	}
	if len(request.Password) > description.MaxPasswordBytes {
		request.Clear()
		response.Code = firstaccountwire.InvalidInput
		return complete()
	}
	prepared, err := callback.Invoke(func() (firstaccount.Prepared, error) {
		return command.Prepare(ctx, request.Fingerprint, request.Values)
	}, callbackAborted)
	// Preparation owns detached scalars and expiring upload buffers. Clear the
	// transport copies before acquiring a backend; retain only the password.
	for i := range request.Values {
		request.Values[i].Clear()
	}
	request.Values = nil
	if err != nil {
		request.Clear()
		response.Code = classify(err, firstaccountwire.InvalidInput)
		return complete()
	}
	defer prepared.Clear()
	backend, err := callback.Invoke(func() (firstaccount.Backend, error) { return command.Open(ctx) }, callbackAborted)
	closeBackend := func() {
		if nilValue(backend) {
			return
		}
		_, err := callback.Invoke(func() (struct{}, error) { return struct{}{}, backend.Close() }, callbackAborted)
		response.CleanupFailed = err != nil
		backend = nil
	}
	if err != nil {
		request.Clear()
		response.Code = classify(err, firstaccountwire.OpenFailed)
		closeBackend()
		return complete()
	}
	// Conversion happens only at the string-based password policy boundary. The
	// joined callback owns that reference until it returns, including its defers.
	password := string(request.Password)
	request.Clear()
	result, provisionErr := callback.Invoke(func() (firstaccount.Result, error) {
		return command.Provision(ctx, backend, response.OperationID, prepared, password)
	}, callbackAborted)
	password = ""
	response.FileReportFailed = result.ReportErr() != nil
	if provisionErr == nil {
		// A nil error is a confirmed commit; do not let later cancellation/cleanup
		// downgrade it. An impossible source/receipt mismatch requires inspection.
		if result.Ownership.OperationID() != response.OperationID || result.Ownership.RoleFingerprint() != description.RoleSHA256 || result.Ownership.EvidenceSHA256() != description.EvidenceSHA256 || result.Ownership.Sequence() != 1 {
			response.Outcome, response.Code = firstaccountwire.Unknown, firstaccountwire.OutcomeUnknown
		} else {
			response.Outcome = firstaccountwire.Created
		}
	} else if uncertain(provisionErr) {
		response.Outcome, response.Code = firstaccountwire.Unknown, firstaccountwire.OutcomeUnknown
	} else {
		response.Code = classify(provisionErr, firstaccountwire.PersistenceFailed)
	}
	closeBackend()
	return complete()
}

// Joining the classifier also contains hostile error Is/Unwrap implementations.
// A classifier abort after entering provision cannot assert confirmed rollback.
func uncertain(err error) bool {
	if err == callbackAborted || nilValue(err) {
		return true
	}
	unknown, failed := callback.Invoke(func() (bool, error) {
		return errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeOutcomeUnknown}) || errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) || errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}), nil
	}, callbackAborted)
	return failed != nil || unknown
}

func classify(err error, fallback firstaccountwire.Code) firstaccountwire.Code {
	if err == callbackAborted || nilValue(err) {
		return firstaccountwire.InternalError
	}
	code, failed := callback.Invoke(func() (firstaccountwire.Code, error) {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return firstaccountwire.Canceled, nil
		}
		if errors.Is(err, &firstaccount.Error{Code: firstaccount.InvalidConfig}) || errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeInvalidConfig}) {
			return firstaccountwire.InvalidConfig, nil
		}
		if errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeNotFresh}) {
			return firstaccountwire.NotFresh, nil
		}
		if _, rejected := validation.Rejected(err); rejected {
			return firstaccountwire.InvalidInput, nil
		}
		if errors.Is(err, &firstaccount.Error{Code: firstaccount.InvalidInput}) || errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeInvalidInput}) {
			return firstaccountwire.InvalidInput, nil
		}
		return fallback, nil
	}, callbackAborted)
	if failed != nil {
		return firstaccountwire.InternalError
	}
	return code
}

func publish(stdout io.Writer, document []byte, outcome firstaccountwire.Outcome, cleanup, fileReport bool) error {
	_, err := callback.Invoke(func() (struct{}, error) { return struct{}{}, firstaccountwire.Write(stdout, document) }, firstaccountwire.ErrOutputTransport)
	if err != nil {
		return &Error{code: "output_failed", outcome: outcome, cleanupFailed: cleanup, fileReportFailed: fileReport}
	}
	return nil
}

func nilValue(value any) bool {
	if value == nil {
		return true
	}
	switch reflect.ValueOf(value).Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflect.ValueOf(value).IsNil()
	}
	return false
}
