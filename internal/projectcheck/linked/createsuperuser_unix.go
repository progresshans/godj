//go:build darwin || linux

package linked

import (
	"context"
	"errors"
	"io"
	"reflect"

	"github.com/progresshans/godj/internal/projectcheck/createsuperuserprotocol"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/systemstate"
)

// SystemStateBackend is the complete project-owned database resource for one
// explicit operator-provisioning invocation. Its outer Close remains separate
// from the database-coordinated transaction owned by systemstate.
type SystemStateBackend interface {
	systemstate.Backend
	Close() error
}

// CreatesuperuserConfig contains only the project-owned system-state opener
// and immutable credential policy. It contains no username or raw password.
type CreatesuperuserConfig struct {
	OpenSystemStateBackend func(context.Context) (SystemStateBackend, error)
	CredentialPolicy       systemstate.CredentialPolicy
}

// CreatesuperuserReport records only observations made by this invocation.
// KnownCreated is local outcome metadata used when response publication fails;
// it never causes the runner to retry or synthesize a successful response.
type CreatesuperuserReport struct {
	CommandDispatches      int
	BackendOpenCalls       int
	ProvisionCalls         int
	BackendCloseCalls      int
	BackendCleanupFailures int
	RunnerResponseWrites   int
	KnownCreated           bool
}

type createsuperuserDependencies struct {
	provisionOperator   func(context.Context, systemstate.Backend, systemstate.ProvisionOperatorConfig) error
	beforeResponseWrite func()
}

// RunCreatesuperuser serves the current private operator-provisioning wire.
// Completed product failures are emitted as closed protocol responses. Caller
// I/O failures, cancellation, and invalid invocation boundaries remain Go
// errors and never include backend or secret-bearing causes.
func RunCreatesuperuser(
	ctx context.Context,
	config CreatesuperuserConfig,
	argv []string,
	stdin io.Reader,
	stdout io.Writer,
) (CreatesuperuserReport, error) {
	owned := CreatesuperuserConfig{
		OpenSystemStateBackend: config.OpenSystemStateBackend,
		CredentialPolicy:       config.CredentialPolicy,
	}
	arguments := append([]string(nil), argv...)
	return runCreatesuperuser(ctx, owned, arguments, stdin, stdout, createsuperuserDependencies{
		provisionOperator: systemstate.ProvisionOperator,
	})
}

func runCreatesuperuser(
	ctx context.Context,
	config CreatesuperuserConfig,
	argv []string,
	stdin io.Reader,
	stdout io.Writer,
	dependencies createsuperuserDependencies,
) (CreatesuperuserReport, error) {
	var report CreatesuperuserReport
	if ctx == nil {
		return report, errors.New("project linked createsuperuser: nil context")
	}
	if stdin == nil {
		return report, errors.New("project linked createsuperuser: nil stdin")
	}
	if stdout == nil {
		return report, errors.New("project linked createsuperuser: nil stdout")
	}
	if len(argv) != 1 || argv[0] != createsuperuserprotocol.PrivateArgument {
		return report, errors.New("project linked createsuperuser: invalid private argv")
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}

	request, requestFailure, requestFailed, err := createsuperuserprotocol.ReadRequest(stdin)
	if err != nil {
		return report, err
	}
	if requestFailed {
		return completeCreatesuperuserResponse(
			ctx,
			dependencies,
			stdout,
			report,
			createsuperuserprotocol.Response{Failure: requestFailure},
			true,
		)
	}
	defer request.Clear()
	report.CommandDispatches = 1
	if err := ctx.Err(); err != nil {
		return report, err
	}

	if config.OpenSystemStateBackend == nil {
		request.Clear()
		return completeCreatesuperuserResponse(ctx, dependencies, stdout, report, createsuperuserprotocol.Response{Failure: createsuperuserprotocol.Failure{
			Category: createsuperuserprotocol.CategoryBackend,
			Code:     createsuperuserprotocol.CodeInvalidBackend,
		}}, true)
	}

	report.BackendOpenCalls++
	opened, openErr := config.OpenSystemStateBackend(ctx)
	acquired := !isNilSystemStateBackend(opened)
	if openErr != nil {
		if acquired {
			closeSystemStateBackend(opened, &report)
		}
		request.Clear()
		if !isNilLinkedValue(openErr) {
			if cancellation := canonicalContextError(openErr); cancellation != nil {
				return report, cancellation
			}
		}
		return completeCreatesuperuserResponse(ctx, dependencies, stdout, report, createsuperuserprotocol.Response{Failure: createsuperuserprotocol.Failure{
			Category: createsuperuserprotocol.CategoryBackend,
			Code:     createsuperuserprotocol.CodeBackendOpenFailed,
		}}, false)
	}
	if !acquired {
		request.Clear()
		return completeCreatesuperuserResponse(ctx, dependencies, stdout, report, createsuperuserprotocol.Response{Failure: createsuperuserprotocol.Failure{
			Category: createsuperuserprotocol.CategoryBackend,
			Code:     createsuperuserprotocol.CodeInvalidBackend,
		}}, false)
	}

	if dependencies.provisionOperator == nil {
		closeSystemStateBackend(opened, &report)
		request.Clear()
		return report, errors.New("project linked createsuperuser: nil provision dependency")
	}

	// Conversion is the unavoidable string-based PasswordHasher boundary.
	// Keep the secret in clearable transport buffers while the backend opens,
	// convert only immediately before the single provisioning call, then drop
	// all mutable and local string/config references as soon as that call ends.
	username := string(request.Username)
	password := string(request.Password)
	request.Clear()
	provisionConfig := systemstate.ProvisionOperatorConfig{
		Username:         username,
		Password:         password,
		CredentialPolicy: config.CredentialPolicy,
	}
	report.ProvisionCalls++
	provisionErr := dependencies.provisionOperator(ctx, opened, provisionConfig)
	provisionConfig.Username = ""
	provisionConfig.Password = ""
	username = ""
	password = ""

	closeErr := closeSystemStateBackend(opened, &report)
	if provisionErr == nil {
		report.KnownCreated = true
		if closeErr == nil {
			return completeCreatesuperuserResponse(ctx, dependencies, stdout, report, createsuperuserprotocol.Response{
				OK:      true,
				Created: true,
			}, false)
		}
		return completeCreatesuperuserResponse(ctx, dependencies, stdout, report, createsuperuserprotocol.Response{Failure: createsuperuserprotocol.Failure{
			Category:     createsuperuserprotocol.CategoryBackend,
			Code:         createsuperuserprotocol.CodeBackendCloseFailed,
			KnownCreated: true,
		}}, false)
	}
	if isNilLinkedValue(provisionErr) {
		return completeCreatesuperuserResponse(
			ctx,
			dependencies,
			stdout,
			report,
			createsuperuserprotocol.Response{Failure: createsuperuserInternalFailure()},
			false,
		)
	}
	// A preserved database outcome-unknown marker is a terminal reconciliation
	// requirement even when its cause chain also contains cancellation. Treating
	// that chain as an ordinary confirmed rollback would allow an operator to
	// retry a create whose durable outcome is unknown.
	if !createsuperuserStateOutcomeUnknown(provisionErr) {
		if cancellation := canonicalContextError(provisionErr); cancellation != nil {
			return report, cancellation
		}
	}
	failure := classifyCreatesuperuserStateFailure(provisionErr)
	return completeCreatesuperuserResponse(
		ctx,
		dependencies,
		stdout,
		report,
		createsuperuserprotocol.Response{Failure: failure},
		false,
	)
}

func createsuperuserStateOutcomeUnknown(err error) bool {
	if !safeLinkedErrorIs(err, &systemstate.Error{Code: systemstate.CodePersistence}) {
		return false
	}
	return safeLinkedErrorIs(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) ||
		safeLinkedErrorIs(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown})
}

func closeSystemStateBackend(backend SystemStateBackend, report *CreatesuperuserReport) error {
	report.BackendCloseCalls++
	err := backend.Close()
	if err != nil {
		report.BackendCleanupFailures++
	}
	return err
}

func completeCreatesuperuserResponse(
	ctx context.Context,
	dependencies createsuperuserDependencies,
	writer io.Writer,
	report CreatesuperuserReport,
	response createsuperuserprotocol.Response,
	honorCancellation bool,
) (CreatesuperuserReport, error) {
	if dependencies.beforeResponseWrite != nil {
		dependencies.beforeResponseWrite()
	}
	if honorCancellation {
		if err := ctx.Err(); err != nil {
			return report, err
		}
	}
	report.RunnerResponseWrites++
	if err := createsuperuserprotocol.WriteResponse(writer, response); err != nil {
		return report, err
	}
	return report, nil
}

func classifyCreatesuperuserStateFailure(err error) createsuperuserprotocol.Failure {
	var stateError *systemstate.Error
	if !safeLinkedErrorAs(err, &stateError) || stateError == nil {
		return createsuperuserInternalFailure()
	}
	failure := createsuperuserprotocol.Failure{Category: createsuperuserprotocol.CategoryState}
	switch stateError.Code {
	case systemstate.CodeInvalidConfig:
		failure.Code = createsuperuserprotocol.CodeInvalidConfig
	case systemstate.CodeInvalidInput:
		failure.Code = createsuperuserprotocol.CodeInvalidInput
	case systemstate.CodeSchemaUnavailable:
		failure.Code = createsuperuserprotocol.CodeSchemaUnavailable
	case systemstate.CodeCardinality:
		failure.Code = createsuperuserprotocol.CodeInvalidCardinality
	case systemstate.CodeCorruptState:
		failure.Code = createsuperuserprotocol.CodeCorruptState
	case systemstate.CodePersistence:
		failure.Code = createsuperuserprotocol.CodePersistenceFailure
	case systemstate.CodeCredentialAlreadyExists:
		failure.Code = createsuperuserprotocol.CodeCredentialAlreadyExists
	case systemstate.CodeCredentialPolicyMismatch:
		failure.Code = createsuperuserprotocol.CodeCredentialPolicyMismatch
	default:
		// credential_absent is impossible for ProvisionOperator and is not a
		// linked product refusal. Unknown future codes fail closed as internal.
		return createsuperuserInternalFailure()
	}
	if !createsuperuserprotocol.IsLinkedFailure(failure) {
		return createsuperuserInternalFailure()
	}
	return failure
}

func createsuperuserInternalFailure() createsuperuserprotocol.Failure {
	return createsuperuserprotocol.Failure{
		Category: createsuperuserprotocol.CategoryInternal,
		Code:     createsuperuserprotocol.CodeProjectInternalError,
	}
}

func canonicalContextError(err error) error {
	switch {
	case safeLinkedErrorIs(err, context.Canceled):
		return context.Canceled
	case safeLinkedErrorIs(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	default:
		return nil
	}
}

func isNilSystemStateBackend(value SystemStateBackend) bool {
	return isNilLinkedValue(value)
}

func isNilLinkedValue(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func safeLinkedErrorIs(err, target error) (matched bool) {
	if isNilLinkedValue(err) {
		return false
	}
	defer func() {
		if recover() != nil {
			matched = false
		}
	}()
	return errors.Is(err, target)
}

func safeLinkedErrorAs(err error, target any) (matched bool) {
	if isNilLinkedValue(err) {
		return false
	}
	defer func() {
		if recover() != nil {
			matched = false
		}
	}()
	return errors.As(err, target)
}
