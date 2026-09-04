//go:build darwin || linux

package linked

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/internal/projectcheck/createsuperuserprotocol"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/systemstate"
)

func TestRunCreatesuperuserRejectsInvalidBoundaryBeforeRequestOrBackend(t *testing.T) {
	t.Parallel()

	valid := mustCreatesuperuserRequest(t, "operator", "password-marker")
	defer clear(valid)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name   string
		ctx    context.Context
		argv   []string
		stdin  io.Reader
		stdout io.Writer
	}{
		{name: "nil context", argv: []string{createsuperuserprotocol.PrivateArgument}, stdin: bytes.NewReader(valid), stdout: io.Discard},
		{name: "nil stdin", ctx: context.Background(), argv: []string{createsuperuserprotocol.PrivateArgument}, stdout: io.Discard},
		{name: "nil stdout", ctx: context.Background(), argv: []string{createsuperuserprotocol.PrivateArgument}, stdin: bytes.NewReader(valid)},
		{name: "missing argv", ctx: context.Background(), stdin: bytes.NewReader(valid), stdout: io.Discard},
		{name: "extra argv", ctx: context.Background(), argv: []string{createsuperuserprotocol.PrivateArgument, "extra"}, stdin: bytes.NewReader(valid), stdout: io.Discard},
		{name: "wrong argv", ctx: context.Background(), argv: []string{"createsuperuser"}, stdin: bytes.NewReader(valid), stdout: io.Discard},
		{name: "canceled", ctx: canceled, argv: []string{createsuperuserprotocol.PrivateArgument}, stdin: bytes.NewReader(valid), stdout: io.Discard},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			openCalls := 0
			_, err := RunCreatesuperuser(test.ctx, CreatesuperuserConfig{
				OpenSystemStateBackend: func(context.Context) (SystemStateBackend, error) {
					openCalls++
					return &createsuperuserTestBackend{}, nil
				},
			}, test.argv, test.stdin, test.stdout)
			if err == nil || openCalls != 0 {
				t.Fatalf("boundary error = %v, backend opens = %d", err, openCalls)
			}
			if strings.Contains(err.Error(), "password-marker") {
				t.Fatalf("boundary error leaked secret: %v", err)
			}
		})
	}
}

func TestRunCreatesuperuserReadsAndValidatesCompleteRequestBeforeBackendOpen(t *testing.T) {
	t.Parallel()

	valid := mustCreatesuperuserRequest(t, "operator", "password-marker")
	defer clear(valid)
	requests := [][]byte{
		{},
		append([]byte(nil), valid[:len(valid)-1]...),
		append(append([]byte(nil), valid...), 'x'),
		bytes.Repeat([]byte{'x'}, createsuperuserprotocol.MaxRequestBytes+1),
	}
	for index, request := range requests {
		openCalls := 0
		output := new(bytes.Buffer)
		report, err := RunCreatesuperuser(context.Background(), CreatesuperuserConfig{
			OpenSystemStateBackend: func(context.Context) (SystemStateBackend, error) {
				openCalls++
				return &createsuperuserTestBackend{}, nil
			},
		}, []string{createsuperuserprotocol.PrivateArgument}, bytes.NewReader(request), output)
		response := parseCreatesuperuserResponse(t, output.Bytes())
		if err != nil || response.Failure.Category != createsuperuserprotocol.CategoryProtocol ||
			(response.Failure.Code != createsuperuserprotocol.CodeInvalidRequest &&
				response.Failure.Code != createsuperuserprotocol.CodeProtocolIncompatible) ||
			openCalls != 0 || report.CommandDispatches != 0 || report.BackendOpenCalls != 0 ||
			report.RunnerResponseWrites != 1 {
			t.Fatalf("request %d = response %+v report %+v opens %d err %v", index, response, report, openCalls, err)
		}
		clear(request)
	}
}

func TestRunCreatesuperuserOneOpenProvisionCloseAndSuccess(t *testing.T) {
	t.Parallel()

	backend := &createsuperuserTestBackend{}
	permission, err := auth.NewPermission("article.change")
	if err != nil {
		t.Fatal(err)
	}
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{
		ID:          "operator-principal",
		Active:      true,
		Permissions: []auth.Permission{permission},
	})
	if err != nil {
		t.Fatal(err)
	}
	hasher := new(auth.PBKDF2)
	policy := systemstate.CredentialPolicy{Principal: principal, PasswordHasher: hasher}
	var gotUsername, gotPassword string
	provisionCalls := 0
	output := new(bytes.Buffer)
	report, err := runCreatesuperuser(context.Background(), CreatesuperuserConfig{
		OpenSystemStateBackend: func(context.Context) (SystemStateBackend, error) {
			return backend, nil
		},
		CredentialPolicy: policy,
	}, []string{createsuperuserprotocol.PrivateArgument}, bytes.NewReader(
		mustCreatesuperuserRequest(t, "operator-marker", "  password-marker  "),
	), output, createsuperuserDependencies{
		provisionOperator: func(_ context.Context, gotBackend systemstate.Backend, config systemstate.ProvisionOperatorConfig) error {
			provisionCalls++
			if gotBackend != backend {
				t.Fatal("provision received a different backend")
			}
			gotUsername = config.Username
			gotPassword = config.Password
			permissions := config.CredentialPolicy.Principal.Permissions()
			if config.CredentialPolicy.Principal.ID() != principal.ID() ||
				config.CredentialPolicy.Principal.Active() != principal.Active() ||
				len(permissions) != 1 || permissions[0] != permission ||
				config.CredentialPolicy.PasswordHasher != hasher {
				t.Fatalf("provision policy did not preserve project meaning: %+v", config.CredentialPolicy)
			}
			return nil
		},
	})
	response := parseCreatesuperuserResponse(t, output.Bytes())
	if err != nil || response != (createsuperuserprotocol.Response{OK: true, Created: true}) ||
		gotUsername != "operator-marker" || gotPassword != "  password-marker  " ||
		provisionCalls != 1 || backend.closeCalls != 1 ||
		report.CommandDispatches != 1 || report.BackendOpenCalls != 1 || report.ProvisionCalls != 1 ||
		report.BackendCloseCalls != 1 || report.BackendCleanupFailures != 0 ||
		report.RunnerResponseWrites != 1 || !report.KnownCreated {
		t.Fatalf("success response %+v report %+v provision=%d close=%d err=%v", response, report, provisionCalls, backend.closeCalls, err)
	}
	if bytes.Contains(output.Bytes(), []byte("operator-marker")) || bytes.Contains(output.Bytes(), []byte("password-marker")) {
		t.Fatalf("success output leaked input: %q", output.Bytes())
	}
}

func TestRunCreatesuperuserConfirmedSuccessWinsOuterCancellation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		closeErr    error
		wantSuccess bool
		wantFailure createsuperuserprotocol.Failure
	}{
		{name: "successful close", wantSuccess: true},
		{
			name:     "close failure",
			closeErr: errors.New("close-password-marker"),
			wantFailure: createsuperuserprotocol.Failure{
				Category:     createsuperuserprotocol.CategoryBackend,
				Code:         createsuperuserprotocol.CodeBackendCloseFailed,
				KnownCreated: true,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			backend := &createsuperuserTestBackend{closeErr: test.closeErr}
			output := new(bytes.Buffer)
			report, err := runCreatesuperuser(ctx, CreatesuperuserConfig{
				OpenSystemStateBackend: func(context.Context) (SystemStateBackend, error) { return backend, nil },
			}, []string{createsuperuserprotocol.PrivateArgument}, bytes.NewReader(
				mustCreatesuperuserRequest(t, "operator-marker", "password-marker"),
			), output, createsuperuserDependencies{
				provisionOperator: func(context.Context, systemstate.Backend, systemstate.ProvisionOperatorConfig) error {
					cancel()
					return nil
				},
			})
			response := parseCreatesuperuserResponse(t, output.Bytes())
			if err != nil || response.OK != test.wantSuccess || response.Failure != test.wantFailure ||
				backend.closeCalls != 1 || report.ProvisionCalls != 1 || report.BackendCloseCalls != 1 ||
				report.RunnerResponseWrites != 1 || !report.KnownCreated {
				t.Fatalf("terminal arbitration response %+v report %+v close=%d err=%v", response, report, backend.closeCalls, err)
			}
		})
	}
}

func TestRunCreatesuperuserMapsOnlyAllowedSystemStateFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		code     systemstate.ErrorCode
		wantCode string
	}{
		{name: "invalid config", code: systemstate.CodeInvalidConfig, wantCode: createsuperuserprotocol.CodeInvalidConfig},
		{name: "invalid input", code: systemstate.CodeInvalidInput, wantCode: createsuperuserprotocol.CodeInvalidInput},
		{name: "schema", code: systemstate.CodeSchemaUnavailable, wantCode: createsuperuserprotocol.CodeSchemaUnavailable},
		{name: "cardinality", code: systemstate.CodeCardinality, wantCode: createsuperuserprotocol.CodeInvalidCardinality},
		{name: "corrupt", code: systemstate.CodeCorruptState, wantCode: createsuperuserprotocol.CodeCorruptState},
		{name: "persistence", code: systemstate.CodePersistence, wantCode: createsuperuserprotocol.CodePersistenceFailure},
		{name: "already", code: systemstate.CodeCredentialAlreadyExists, wantCode: createsuperuserprotocol.CodeCredentialAlreadyExists},
		{name: "policy", code: systemstate.CodeCredentialPolicyMismatch, wantCode: createsuperuserprotocol.CodeCredentialPolicyMismatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := &createsuperuserTestBackend{closeErr: errors.New("cleanup-secret")}
			calls := 0
			output := new(bytes.Buffer)
			report, err := runCreatesuperuser(context.Background(), CreatesuperuserConfig{
				OpenSystemStateBackend: func(context.Context) (SystemStateBackend, error) { return backend, nil },
			}, []string{createsuperuserprotocol.PrivateArgument}, bytes.NewReader(
				mustCreatesuperuserRequest(t, "operator-marker", "password-marker"),
			), output, createsuperuserDependencies{
				provisionOperator: func(context.Context, systemstate.Backend, systemstate.ProvisionOperatorConfig) error {
					calls++
					return &systemstate.Error{Code: test.code, Detail: "password-marker", Cause: errors.New("backend-secret")}
				},
			})
			response := parseCreatesuperuserResponse(t, output.Bytes())
			want := createsuperuserprotocol.Failure{Category: createsuperuserprotocol.CategoryState, Code: test.wantCode}
			if err != nil || response.Failure != want || calls != 1 || backend.closeCalls != 1 ||
				report.ProvisionCalls != 1 || report.BackendCloseCalls != 1 || report.BackendCleanupFailures != 1 ||
				report.KnownCreated {
				t.Fatalf("mapped response %+v want %+v report %+v calls=%d close=%d err=%v", response, want, report, calls, backend.closeCalls, err)
			}
			if bytes.Contains(output.Bytes(), []byte("marker")) || bytes.Contains(output.Bytes(), []byte("secret")) {
				t.Fatalf("failure leaked cause or input: %q", output.Bytes())
			}
		})
	}
}

func TestRunCreatesuperuserRejectsImpossibleAndUnknownStateFailuresAsClosedInternal(t *testing.T) {
	t.Parallel()

	var typedNil *query.Error
	for _, failure := range []error{
		&systemstate.Error{Code: systemstate.CodeCredentialAbsent, Detail: "password-marker"},
		errors.New("password-marker"),
		typedNil,
		createsuperuserPanickingError{},
	} {
		backend := &createsuperuserTestBackend{}
		output := new(bytes.Buffer)
		report, err := runCreatesuperuser(context.Background(), CreatesuperuserConfig{
			OpenSystemStateBackend: func(context.Context) (SystemStateBackend, error) { return backend, nil },
		}, []string{createsuperuserprotocol.PrivateArgument}, bytes.NewReader(
			mustCreatesuperuserRequest(t, "operator-marker", "password-marker"),
		), output, createsuperuserDependencies{
			provisionOperator: func(context.Context, systemstate.Backend, systemstate.ProvisionOperatorConfig) error { return failure },
		})
		response := parseCreatesuperuserResponse(t, output.Bytes())
		want := createsuperuserprotocol.Failure{Category: createsuperuserprotocol.CategoryInternal, Code: createsuperuserprotocol.CodeProjectInternalError}
		if err != nil || response.Failure != want || report.ProvisionCalls != 1 || backend.closeCalls != 1 || report.KnownCreated {
			t.Fatalf("internal response %+v report %+v close=%d err=%v", response, report, backend.closeCalls, err)
		}
		if bytes.Contains(output.Bytes(), []byte("marker")) {
			t.Fatalf("internal failure leaked cause: %q", output.Bytes())
		}
	}
}

func TestRunCreatesuperuserOpenerAndCloseOwnership(t *testing.T) {
	t.Parallel()

	typedNil := (*createsuperuserTestBackend)(nil)
	var typedNilOpenError *query.Error
	openSecret := errors.New("postgres://user:password-marker@example.invalid/private")
	tests := []struct {
		name                string
		opener              func(context.Context) (SystemStateBackend, error)
		wantCode            string
		wantOpenCalls       int
		wantCloseCalls      int
		wantCleanupFailures int
	}{
		{name: "nil opener", wantCode: createsuperuserprotocol.CodeInvalidBackend},
		{name: "nil backend", opener: func(context.Context) (SystemStateBackend, error) { return nil, nil }, wantCode: createsuperuserprotocol.CodeInvalidBackend, wantOpenCalls: 1},
		{name: "typed nil", opener: func(context.Context) (SystemStateBackend, error) { return typedNil, nil }, wantCode: createsuperuserprotocol.CodeInvalidBackend, wantOpenCalls: 1},
		{name: "typed nil open error", opener: func(context.Context) (SystemStateBackend, error) { return nil, typedNilOpenError }, wantCode: createsuperuserprotocol.CodeBackendOpenFailed, wantOpenCalls: 1},
		{name: "panicking open error", opener: func(context.Context) (SystemStateBackend, error) { return nil, createsuperuserPanickingError{} }, wantCode: createsuperuserprotocol.CodeBackendOpenFailed, wantOpenCalls: 1},
		{name: "nil with open error", opener: func(context.Context) (SystemStateBackend, error) { return nil, openSecret }, wantCode: createsuperuserprotocol.CodeBackendOpenFailed, wantOpenCalls: 1},
		{
			name: "acquired with open error closes",
			opener: func(context.Context) (SystemStateBackend, error) {
				return &createsuperuserTestBackend{closeErr: errors.New("cleanup-password-marker")}, openSecret
			},
			wantCode: createsuperuserprotocol.CodeBackendOpenFailed, wantOpenCalls: 1, wantCloseCalls: 1, wantCleanupFailures: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := new(bytes.Buffer)
			report, err := runCreatesuperuser(context.Background(), CreatesuperuserConfig{
				OpenSystemStateBackend: test.opener,
			}, []string{createsuperuserprotocol.PrivateArgument}, bytes.NewReader(
				mustCreatesuperuserRequest(t, "operator-marker", "password-marker"),
			), output, createsuperuserDependencies{provisionOperator: systemstate.ProvisionOperator})
			response := parseCreatesuperuserResponse(t, output.Bytes())
			want := createsuperuserprotocol.Failure{Category: createsuperuserprotocol.CategoryBackend, Code: test.wantCode}
			if err != nil || response.Failure != want || report.BackendOpenCalls != test.wantOpenCalls ||
				report.BackendCloseCalls != test.wantCloseCalls || report.BackendCleanupFailures != test.wantCleanupFailures ||
				report.ProvisionCalls != 0 || report.KnownCreated {
				t.Fatalf("opener response %+v want %+v report %+v err=%v", response, want, report, err)
			}
			if bytes.Contains(output.Bytes(), []byte("marker")) || bytes.Contains(output.Bytes(), []byte("private")) {
				t.Fatalf("opener response leaked cause/input: %q", output.Bytes())
			}
		})
	}
}

func TestRunCreatesuperuserKnownCreatedCloseFailureAndNoRetry(t *testing.T) {
	t.Parallel()

	backend := &createsuperuserTestBackend{closeErr: errors.New("close-password-marker")}
	provisionCalls := 0
	output := new(bytes.Buffer)
	report, err := runCreatesuperuser(context.Background(), CreatesuperuserConfig{
		OpenSystemStateBackend: func(context.Context) (SystemStateBackend, error) { return backend, nil },
	}, []string{createsuperuserprotocol.PrivateArgument}, bytes.NewReader(
		mustCreatesuperuserRequest(t, "operator-marker", "password-marker"),
	), output, createsuperuserDependencies{
		provisionOperator: func(context.Context, systemstate.Backend, systemstate.ProvisionOperatorConfig) error {
			provisionCalls++
			return nil
		},
	})
	response := parseCreatesuperuserResponse(t, output.Bytes())
	want := createsuperuserprotocol.Failure{
		Category:     createsuperuserprotocol.CategoryBackend,
		Code:         createsuperuserprotocol.CodeBackendCloseFailed,
		KnownCreated: true,
	}
	if err != nil || response.Failure != want || provisionCalls != 1 || backend.closeCalls != 1 ||
		report.ProvisionCalls != 1 || report.BackendCloseCalls != 1 || report.BackendCleanupFailures != 1 || !report.KnownCreated {
		t.Fatalf("known-created response %+v want %+v report %+v calls=%d close=%d err=%v", response, want, report, provisionCalls, backend.closeCalls, err)
	}
	if bytes.Contains(output.Bytes(), []byte("marker")) {
		t.Fatalf("known-created response leaked cause/input: %q", output.Bytes())
	}

	persistenceBackend := &createsuperuserTestBackend{}
	persistenceCalls := 0
	output.Reset()
	report, err = runCreatesuperuser(context.Background(), CreatesuperuserConfig{
		OpenSystemStateBackend: func(context.Context) (SystemStateBackend, error) { return persistenceBackend, nil },
	}, []string{createsuperuserprotocol.PrivateArgument}, bytes.NewReader(
		mustCreatesuperuserRequest(t, "operator", "password"),
	), output, createsuperuserDependencies{
		provisionOperator: func(context.Context, systemstate.Backend, systemstate.ProvisionOperatorConfig) error {
			persistenceCalls++
			return &systemstate.Error{Code: systemstate.CodePersistence}
		},
	})
	response = parseCreatesuperuserResponse(t, output.Bytes())
	if err != nil || response.Failure.Code != createsuperuserprotocol.CodePersistenceFailure ||
		persistenceCalls != 1 || persistenceBackend.closeCalls != 1 || report.ProvisionCalls != 1 || report.KnownCreated {
		t.Fatalf("persistence no-retry response %+v report %+v calls=%d close=%d err=%v", response, report, persistenceCalls, persistenceBackend.closeCalls, err)
	}
}

func TestRunCreatesuperuserCancellationAfterAcquisitionClosesAndReturnsContextError(t *testing.T) {
	t.Parallel()

	backend := &createsuperuserTestBackend{closeErr: errors.New("cleanup-password-marker")}
	canceled, cancel := context.WithCancel(context.Background())
	report, err := runCreatesuperuser(canceled, CreatesuperuserConfig{
		OpenSystemStateBackend: func(context.Context) (SystemStateBackend, error) { return backend, nil },
	}, []string{createsuperuserprotocol.PrivateArgument}, bytes.NewReader(
		mustCreatesuperuserRequest(t, "operator-marker", "password-marker"),
	), io.Discard, createsuperuserDependencies{
		provisionOperator: func(context.Context, systemstate.Backend, systemstate.ProvisionOperatorConfig) error {
			cancel()
			return context.Canceled
		},
	})
	if !errors.Is(err, context.Canceled) || backend.closeCalls != 1 || report.BackendCloseCalls != 1 ||
		report.BackendCleanupFailures != 1 || report.RunnerResponseWrites != 0 || report.KnownCreated {
		t.Fatalf("cancellation report %+v close=%d err=%v", report, backend.closeCalls, err)
	}
}

func TestRunCreatesuperuserOutcomeUnknownBeatsCancellationButConfirmedRollbackCancellationWins(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		provisionErr    error
		wantPersistence bool
	}{
		{
			name: "commit outcome unknown",
			provisionErr: &systemstate.Error{
				Code:  systemstate.CodePersistence,
				Field: "credential",
				Cause: errors.Join(context.Canceled, &query.Error{
					Category: query.CategoryBackend,
					Code:     query.CodeCommitOutcomeUnknown,
				}),
			},
			wantPersistence: true,
		},
		{
			name: "transaction outcome unknown",
			provisionErr: &systemstate.Error{
				Code:  systemstate.CodePersistence,
				Field: "credential",
				Cause: errors.Join(context.Canceled, &query.Error{
					Category: query.CategoryBackend,
					Code:     query.CodeTransactionOutcomeUnknown,
				}),
			},
			wantPersistence: true,
		},
		{
			name:         "confirmed rollback cancellation",
			provisionErr: errors.Join(context.Canceled, errors.New("confirmed rollback marker")),
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(context.Background())
			backend := &createsuperuserTestBackend{}
			output := new(bytes.Buffer)
			report, err := runCreatesuperuser(ctx, CreatesuperuserConfig{
				OpenSystemStateBackend: func(context.Context) (SystemStateBackend, error) { return backend, nil },
			}, []string{createsuperuserprotocol.PrivateArgument}, bytes.NewReader(
				mustCreatesuperuserRequest(t, "operator-marker", "password-marker"),
			), output, createsuperuserDependencies{
				provisionOperator: func(context.Context, systemstate.Backend, systemstate.ProvisionOperatorConfig) error {
					cancel()
					return test.provisionErr
				},
			})

			if backend.closeCalls != 1 || report.ProvisionCalls != 1 || report.BackendCloseCalls != 1 || report.KnownCreated {
				t.Fatalf("outcome arbitration report %+v close=%d err=%v", report, backend.closeCalls, err)
			}
			if test.wantPersistence {
				response := parseCreatesuperuserResponse(t, output.Bytes())
				want := createsuperuserprotocol.Failure{
					Category: createsuperuserprotocol.CategoryState,
					Code:     createsuperuserprotocol.CodePersistenceFailure,
				}
				if err != nil || response.Failure != want || report.RunnerResponseWrites != 1 {
					t.Fatalf("unknown outcome response %+v want %+v report %+v err=%v", response, want, report, err)
				}
				return
			}
			if !errors.Is(err, context.Canceled) || output.Len() != 0 || report.RunnerResponseWrites != 0 {
				t.Fatalf("confirmed rollback cancellation output=%q report=%+v err=%v", output.Bytes(), report, err)
			}
		})
	}
}

func TestRunCreatesuperuserResponseWriteFailureIsOneAttemptAndPreservesLocalOutcome(t *testing.T) {
	t.Parallel()

	backend := &createsuperuserTestBackend{}
	writer := &createsuperuserShortWriter{}
	report, err := runCreatesuperuser(context.Background(), CreatesuperuserConfig{
		OpenSystemStateBackend: func(context.Context) (SystemStateBackend, error) { return backend, nil },
	}, []string{createsuperuserprotocol.PrivateArgument}, bytes.NewReader(
		mustCreatesuperuserRequest(t, "operator-marker", "password-marker"),
	), writer, createsuperuserDependencies{
		provisionOperator: func(context.Context, systemstate.Backend, systemstate.ProvisionOperatorConfig) error { return nil },
	})
	if err == nil || writer.calls != 1 || backend.closeCalls != 1 || report.RunnerResponseWrites != 1 ||
		report.ProvisionCalls != 1 || !report.KnownCreated {
		t.Fatalf("write failure report %+v writes=%d close=%d err=%v", report, writer.calls, backend.closeCalls, err)
	}
	if strings.Contains(err.Error(), "operator-marker") || strings.Contains(err.Error(), "password-marker") {
		t.Fatalf("write error leaked input: %v", err)
	}
}

type createsuperuserTestBackend struct {
	systemstate.Backend
	closeCalls int
	closeErr   error
}

type createsuperuserPanickingError struct{}

func (createsuperuserPanickingError) Error() string { return "createsuperuser dependency failure" }
func (createsuperuserPanickingError) Is(error) bool {
	panic("createsuperuser dependency Is must be contained")
}
func (createsuperuserPanickingError) Unwrap() error {
	panic("createsuperuser dependency Unwrap must be contained")
}

func (backend *createsuperuserTestBackend) Close() error {
	backend.closeCalls++
	return backend.closeErr
}

type createsuperuserShortWriter struct {
	calls int
}

func (writer *createsuperuserShortWriter) Write(document []byte) (int, error) {
	writer.calls++
	if len(document) == 0 {
		return 0, nil
	}
	return len(document) - 1, nil
}

func mustCreatesuperuserRequest(t *testing.T, username, password string) []byte {
	t.Helper()
	document, err := createsuperuserprotocol.EncodeRequest(createsuperuserprotocol.Request{
		Username: []byte(username),
		Password: []byte(password),
	})
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func parseCreatesuperuserResponse(t *testing.T, document []byte) createsuperuserprotocol.Response {
	t.Helper()
	response, failure, failed := createsuperuserprotocol.ParseResponse(document, true)
	if failed || failure != (createsuperuserprotocol.Failure{}) {
		t.Fatalf("ParseResponse(%q) = %+v, %+v, %v", document, response, failure, failed)
	}
	return response
}
