package firstaccountwire_test

import (
	"bytes"
	"testing"

	"github.com/progresshans/godj/internal/firstaccountwire"
)

func TestFileReportFailurePreservesDatabaseOutcomeAndRequiresCanonicalField(t *testing.T) {
	for _, outcome := range []firstaccountwire.Outcome{firstaccountwire.Created, firstaccountwire.NotCreated, firstaccountwire.Unknown} {
		code := firstaccountwire.Code("")
		if outcome == firstaccountwire.NotCreated {
			code = firstaccountwire.PersistenceFailed
		}
		if outcome == firstaccountwire.Unknown {
			code = firstaccountwire.OutcomeUnknown
		}
		for _, cleanup := range []bool{false, true} {
			response := firstaccountwire.Response{Version: firstaccountwire.Version, OperationID: operationID, Outcome: outcome, Code: code, CleanupFailed: cleanup, FileReportFailed: true}
			document, err := firstaccountwire.EncodeResponse(response)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := firstaccountwire.DecodeResponse(document, operationID)
			if err != nil || parsed != response {
				t.Fatal("report delivery erased database/cleanup state", err)
			}
			for _, bad := range [][]byte{
				bytes.Replace(document, []byte(`,"file_report_failed":true`), nil, 1),
				bytes.Replace(document, []byte(`"file_report_failed":true`), []byte(`"file_report_failed":null`), 1),
				bytes.Replace(document, []byte(`"file_report_failed":true`), []byte(`"file_report_failed":true,"file_report_failed":true`), 1),
			} {
				if _, err := firstaccountwire.DecodeResponse(bad, operationID); err == nil {
					t.Fatal("invalid report state accepted")
				}
			}
		}
	}
	if _, err := firstaccountwire.EncodeResponse(firstaccountwire.Response{Version: firstaccountwire.Version, Outcome: firstaccountwire.NotCreated, Code: firstaccountwire.InvalidRequest, FileReportFailed: true}); err == nil {
		t.Fatal("pre-input rejection claimed a publication report")
	}
}
