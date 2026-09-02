package createsuperuserprotocol

const (
	CategoryCommand   = "operator_project_command_error"
	CategorySelection = "operator_project_selection_error"
	CategoryInput     = "operator_terminal_input_error"
	CategoryBuild     = "operator_project_build_error"
	CategoryProcess   = "operator_project_process_error"
)

const (
	CodeInvalidArguments              = "invalid_arguments"
	CodeProjectNotFound               = "project_not_found"
	CodeProjectSearchLimitExceeded    = "project_search_limit_exceeded"
	CodeInvalidProjectDescriptor      = "invalid_project_descriptor"
	CodeProjectDescriptorIncompatible = "project_descriptor_incompatible"
	CodeProjectSelectionFailed        = "project_selection_failed"
	CodeProjectTemporaryStorageFailed = "project_temporary_storage_failed"
	CodeProjectBuildFailed            = "project_build_failed"

	CodeInputNotTerminal    = "input_not_terminal"
	CodeInvalidUsername     = "invalid_username"
	CodeInvalidPassword     = "invalid_password"
	CodePasswordMismatch    = "password_mismatch"
	CodeInputReadFailed     = "input_read_failed"
	CodeTerminalStateFailed = "terminal_state_failed"

	CodeProjectCanceled                       = "project_canceled"
	CodeProjectCleanupFailed                  = "project_cleanup_failed"
	CodeProjectInterrupted                    = "project_interrupted"
	CodeSensitiveInputTransportFailed         = "sensitive_input_transport_failed"
	CodeOperatorProvisionOutcomeUnknown       = "operator_provision_outcome_unknown"
	CodeOperatorCreatedWorkspaceCleanupFailed = "operator_created_workspace_cleanup_failed"

	CodeOperatorCreatedBackendCleanupFailed = "operator_created_backend_cleanup_failed"
	CodeOperatorCreatedOutputFailed         = "operator_created_output_failed"
)

var publicSuccessDocument = []byte("{\"status\":\"created\"}\n")

// PublicSuccessDocument returns a fresh copy of the sole successful public
// createsuperuser result. Callers must publish it with one write attempt.
func PublicSuccessDocument() []byte {
	return append([]byte(nil), publicSuccessDocument...)
}

// PublicFailureFromLinked converts a strictly parsed linked failure to its
// detail-free public identity. A durable create followed by backend cleanup
// failure receives a distinct code so callers cannot accidentally retry it.
func PublicFailureFromLinked(input Failure) (Failure, bool) {
	if !IsLinkedFailure(input) {
		return Failure{}, false
	}
	if input.KnownCreated {
		return Failure{
			Category: CategoryBackend,
			Code:     CodeOperatorCreatedBackendCleanupFailed,
		}, true
	}
	output := Failure{Category: input.Category, Code: input.Code}
	if _, ok := ExitCode(output); !ok {
		return Failure{}, false
	}
	return output, true
}

// ExitCode returns the exact public exit for one closed, known-created-free
// taxonomy value. Private known-created metadata must first pass through
// PublicFailureFromLinked.
func ExitCode(failure Failure) (int, bool) {
	if failure.KnownCreated {
		return 0, false
	}
	switch failure.Category {
	case CategoryCommand:
		return exactPublicCode(failure.Code, 2, CodeInvalidArguments)
	case CategorySelection:
		switch failure.Code {
		case CodeProjectNotFound,
			CodeProjectSearchLimitExceeded,
			CodeInvalidProjectDescriptor,
			CodeProjectDescriptorIncompatible:
			return 2, true
		case CodeProjectSelectionFailed:
			return 3, true
		}
	case CategoryInput:
		switch failure.Code {
		case CodeInputNotTerminal, CodeInvalidUsername, CodeInvalidPassword, CodePasswordMismatch:
			return 2, true
		case CodeInputReadFailed, CodeTerminalStateFailed:
			return 3, true
		}
	case CategoryBuild:
		return exactPublicCode(failure.Code, 3, CodeProjectTemporaryStorageFailed, CodeProjectBuildFailed)
	case CategoryProtocol:
		return exactPublicCode(
			failure.Code,
			3,
			CodeInvalidRequest,
			CodeRunnerFailed,
			CodeProtocolIncompatible,
			CodeInvalidResponse,
		)
	case CategoryProcess:
		switch failure.Code {
		case CodeProjectCanceled,
			CodeProjectCleanupFailed,
			CodeSensitiveInputTransportFailed,
			CodeOperatorProvisionOutcomeUnknown,
			CodeOperatorCreatedWorkspaceCleanupFailed:
			return 3, true
		case CodeProjectInterrupted:
			return 130, true
		}
	case CategoryState:
		switch failure.Code {
		case CodeInvalidInput:
			return 2, true
		case CodeSchemaUnavailable,
			CodeInvalidCardinality,
			CodeCorruptState,
			CodeCredentialAlreadyExists,
			CodeCredentialPolicyMismatch:
			return 1, true
		case CodeInvalidConfig, CodePersistenceFailure:
			return 3, true
		}
	case CategoryBackend:
		return exactPublicCode(
			failure.Code,
			3,
			CodeBackendOpenFailed,
			CodeInvalidBackend,
			CodeBackendCloseFailed,
			CodeOperatorCreatedBackendCleanupFailed,
		)
	case CategoryInternal:
		return exactPublicCode(failure.Code, 3, CodeProjectInternalError, CodeOperatorCreatedOutputFailed)
	}
	return 0, false
}

func exactPublicCode(input string, exit int, values ...string) (int, bool) {
	for _, value := range values {
		if input == value {
			return exit, true
		}
	}
	return 0, false
}
