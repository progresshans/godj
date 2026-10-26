//go:build darwin || linux

package projectcheck

import (
	"context"

	"github.com/progresshans/godj/internal/firstaccountwire"
)

func executeFirstAccountProcess(ctx context.Context, interrupt <-chan struct{}, command createsuperuserProcessCommand,
	request []byte, operationID string,
) createsuperuserProcessResult {
	return executeOwnedSensitiveProcess(ctx, interrupt, command, request, createsuperuserOwnedProcessGrace,
		createsuperuserProcessHooks{}, sensitiveProcessPolicy{
			requestMaximum: firstaccountwire.MaxRequestBytes, responseMaximum: firstaccountwire.MaxResponseBytes,
			completedResponse: func(document []byte) bool {
				_, err := firstaccountwire.DecodeResponse(document, operationID)
				return err == nil
			},
		})
}

func firstAccountProcessOutcome(process createsuperuserProcessResult, expected int, operationID string) FirstAccountResult {
	result := FirstAccountResult{OperationID: operationID, Status: string(firstaccountwire.NotCreated), CleanupFailed: process.cleanupFailed}
	possible := createsuperuserProcessMayHaveProvisioned(process, expected)
	// Inconsistent transfer observations from a started owner cannot establish
	// noncreation. They also cannot authenticate a claimed success or exit code.
	uncertainTransfer := process.started && (expected <= 0 || process.requestBytesWritten < 0 || process.requestBytesWritten > expected ||
		process.requestWriteAttempts < 0 || process.requestWriteAttempts > 1 || process.requestWriteAttempts == 0 && process.requestBytesWritten != 0)
	if possible && process.exited && process.directReaps == 1 {
		switch process.exitCode {
		case firstaccountwire.KnownCreatedResponseFailureExitCode:
			result.Status, result.Code = string(firstaccountwire.Created), "created_output_failed"
			return result
		case firstaccountwire.KnownCreatedCleanupResponseFailureExitCode:
			result.Status, result.Code, result.CleanupFailed = string(firstaccountwire.Created), "created_cleanup_failed", true
			return result
		}
	}
	valid := possible && process.exited && process.directReaps == 1 && process.exitCode == 0 && process.failure == nil &&
		!process.cleanupFailed && !process.stdoutScalar.Truncated && !process.stderrScalar.Truncated &&
		process.stdoutScalar.RetainedBytes == len(process.response)
	if valid {
		response, err := firstaccountwire.DecodeResponse(process.response, operationID)
		if err == nil {
			result.Status, result.Code, result.CleanupFailed = string(response.Outcome), string(response.Code), response.CleanupFailed
			if result.Status == string(firstaccountwire.Created) && result.CleanupFailed {
				result.Code = "created_cleanup_failed"
			}
			return result
		}
	}
	if possible || uncertainTransfer {
		result.Status, result.Code = string(firstaccountwire.Unknown), "outcome_unknown"
		return result
	}
	result.Code = "input_transport_failed"
	if process.failure != nil {
		switch process.failure.Code {
		case "project_canceled", "project_interrupted", "project_cleanup_failed":
			result.Code = process.failure.Code
		}
	}
	return result
}
