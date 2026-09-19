package createsuperuserprotocol

import (
	"bytes"
	"testing"
)

func TestPublicSuccessDocumentIsExactAndFresh(t *testing.T) {
	first := PublicSuccessDocument()
	second := PublicSuccessDocument()
	if !bytes.Equal(first, []byte("{\"status\":\"created\"}\n")) || !bytes.Equal(second, first) {
		t.Fatalf("public success = %q / %q", first, second)
	}
	first[0] = 'x'
	if bytes.Equal(first, second) || !bytes.Equal(second, []byte("{\"status\":\"created\"}\n")) {
		t.Fatal("PublicSuccessDocument returned shared mutable storage")
	}
}

func TestPublicFailureTaxonomyAndExitCodesAreClosed(t *testing.T) {
	tests := []struct {
		failure Failure
		exit    int
	}{
		{Failure{Category: CategoryCommand, Code: CodeInvalidArguments}, 2},
		{Failure{Category: CategorySelection, Code: CodeProjectNotFound}, 2},
		{Failure{Category: CategorySelection, Code: CodeProjectSearchLimitExceeded}, 2},
		{Failure{Category: CategorySelection, Code: CodeInvalidProjectDescriptor}, 2},
		{Failure{Category: CategorySelection, Code: CodeProjectDescriptorIncompatible}, 2},
		{Failure{Category: CategorySelection, Code: CodeProjectSelectionFailed}, 3},
		{Failure{Category: CategoryInput, Code: CodeInputNotTerminal}, 2},
		{Failure{Category: CategoryInput, Code: CodeInvalidUsername}, 2},
		{Failure{Category: CategoryInput, Code: CodeInvalidPassword}, 2},
		{Failure{Category: CategoryInput, Code: CodePasswordMismatch}, 2},
		{Failure{Category: CategoryInput, Code: CodeInputReadFailed}, 3},
		{Failure{Category: CategoryInput, Code: CodeTerminalStateFailed}, 3},
		{Failure{Category: CategoryBuild, Code: CodeProjectTemporaryStorageFailed}, 3},
		{Failure{Category: CategoryBuild, Code: CodeProjectBuildFailed}, 3},
		{Failure{Category: CategoryProtocol, Code: CodeInvalidRequest}, 3},
		{Failure{Category: CategoryProtocol, Code: CodeRunnerFailed}, 3},
		{Failure{Category: CategoryProtocol, Code: CodeProtocolIncompatible}, 3},
		{Failure{Category: CategoryProtocol, Code: CodeInvalidResponse}, 3},
		{Failure{Category: CategoryProcess, Code: CodeProjectCanceled}, 3},
		{Failure{Category: CategoryProcess, Code: CodeProjectCleanupFailed}, 3},
		{Failure{Category: CategoryProcess, Code: CodeProjectInterrupted}, 130},
		{Failure{Category: CategoryProcess, Code: CodeSensitiveInputTransportFailed}, 3},
		{Failure{Category: CategoryProcess, Code: CodeOperatorProvisionOutcomeUnknown}, 3},
		{Failure{Category: CategoryProcess, Code: CodeOperatorCreatedWorkspaceCleanupFailed}, 3},
		{Failure{Category: CategoryState, Code: CodeInvalidConfig}, 3},
		{Failure{Category: CategoryState, Code: CodeInvalidInput}, 2},
		{Failure{Category: CategoryState, Code: CodeSchemaUnavailable}, 1},
		{Failure{Category: CategoryState, Code: CodeInvalidCardinality}, 1},
		{Failure{Category: CategoryState, Code: CodeCorruptState}, 1},
		{Failure{Category: CategoryState, Code: CodePersistenceFailure}, 3},
		{Failure{Category: CategoryState, Code: CodeCredentialAlreadyExists}, 1},
		{Failure{Category: CategoryState, Code: CodeCredentialPolicyMismatch}, 1},
		{Failure{Category: CategoryBackend, Code: CodeBackendOpenFailed}, 3},
		{Failure{Category: CategoryBackend, Code: CodeInvalidBackend}, 3},
		{Failure{Category: CategoryBackend, Code: CodeBackendCloseFailed}, 3},
		{Failure{Category: CategoryBackend, Code: CodeOperatorCreatedBackendCleanupFailed}, 3},
		{Failure{Category: CategoryInternal, Code: CodeProjectInternalError}, 3},
		{Failure{Category: CategoryInternal, Code: CodeOperatorCreatedOutputFailed}, 3},
	}
	for _, test := range tests {
		if exit, ok := ExitCode(test.failure); !ok || exit != test.exit {
			t.Errorf("ExitCode(%+v) = %d, %v; want %d, true", test.failure, exit, ok, test.exit)
		}
	}
	invalid := []Failure{
		{},
		{Category: CategoryCommand, Code: "unknown"},
		{Category: "unknown", Code: CodeInvalidArguments},
		{Category: CategoryBackend, Code: CodeBackendCloseFailed, KnownCreated: true},
		{Category: CategoryState, Code: "credential_absent"},
	}
	for _, failure := range invalid {
		if exit, ok := ExitCode(failure); ok || exit != 0 {
			t.Errorf("ExitCode(%+v) = %d, %v; want 0, false", failure, exit, ok)
		}
	}
}

func TestPublicFailureFromLinkedPreservesRefusalAndNamesKnownCreatedCleanup(t *testing.T) {
	tests := []struct {
		input Failure
		want  Failure
	}{
		{
			input: Failure{Category: CategoryState, Code: CodeCredentialAlreadyExists},
			want:  Failure{Category: CategoryState, Code: CodeCredentialAlreadyExists},
		},
		{
			input: Failure{Category: CategoryBackend, Code: CodeBackendCloseFailed, KnownCreated: true},
			want:  Failure{Category: CategoryBackend, Code: CodeOperatorCreatedBackendCleanupFailed},
		},
	}
	for _, test := range tests {
		got, ok := PublicFailureFromLinked(test.input)
		if !ok || got != test.want {
			t.Errorf("PublicFailureFromLinked(%+v) = %+v, %v; want %+v, true", test.input, got, ok, test.want)
		}
	}
	invalid := []Failure{
		{Category: CategoryCommand, Code: CodeInvalidArguments},
		{Category: CategoryState, Code: "credential_absent"},
		{Category: CategoryBackend, Code: CodeBackendOpenFailed, KnownCreated: true},
	}
	for _, failure := range invalid {
		if got, ok := PublicFailureFromLinked(failure); ok || got != (Failure{}) {
			t.Errorf("PublicFailureFromLinked(%+v) = %+v, %v; want zero, false", failure, got, ok)
		}
	}
}
