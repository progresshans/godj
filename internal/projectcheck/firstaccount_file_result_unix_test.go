//go:build darwin || linux

package projectcheck

import (
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/firstaccountprotocol"
	"github.com/progresshans/godj/internal/firstaccountwire"
)

func TestFirstAccountFileReportsPreserveCommitUnknownCleanupAndHandoff(t *testing.T) {
	operation := strings.Repeat("e", 64)
	for _, outcome := range []firstaccountwire.Outcome{firstaccountwire.Created, firstaccountwire.NotCreated, firstaccountwire.Unknown} {
		for _, cleanup := range []bool{false, true} {
			code := firstaccountwire.Code("")
			if outcome == firstaccountwire.NotCreated {
				code = firstaccountwire.PersistenceFailed
			}
			if outcome == firstaccountwire.Unknown {
				code = firstaccountwire.OutcomeUnknown
			}
			document, err := firstaccountwire.EncodeResponse(firstaccountwire.Response{Version: firstaccountprotocol.Version, OperationID: operation, Outcome: outcome, Code: code, CleanupFailed: cleanup, FileReportFailed: true})
			if err != nil {
				t.Fatal(err)
			}
			process := createsuperuserProcessResult{started: true, exited: true, directReaps: 1, requestWriteAttempts: 1, requestBytesWritten: 100, response: document}
			process.stdoutScalar.RetainedBytes = len(document)
			result := firstAccountProcessOutcome(process, 100, operation)
			if result.Status != string(outcome) || !result.FileReportFailed || result.CleanupFailed != cleanup || result.OperationID != operation {
				t.Fatal("report delivery erased durable state", result)
			}
			if outcome == firstaccountwire.Created && result.Code != "created_file_report_failed" && result.Code != "created_cleanup_failed" {
				t.Fatal("confirmed report failure became retryable", result)
			}
		}
	}
	for _, exit := range []int{firstaccountwire.KnownCreatedFileReportResponseFailureExitCode, firstaccountwire.KnownCreatedCleanupFileReportResponseFailureExitCode} {
		process := createsuperuserProcessResult{started: true, exited: true, directReaps: 1, exitCode: exit, requestWriteAttempts: 1, requestBytesWritten: 100}
		result := firstAccountProcessOutcome(process, 100, operation)
		if result.Status != "created" || !result.FileReportFailed || result.CleanupFailed != (exit == firstaccountwire.KnownCreatedCleanupFileReportResponseFailureExitCode) {
			t.Fatal("private exit lost report failure", result)
		}
		process.requestBytesWritten = 99
		result = firstAccountProcessOutcome(process, 100, operation)
		if result.Status == "created" || result.FileReportFailed {
			t.Fatal("partial handoff authenticated private result", result)
		}
	}
}
