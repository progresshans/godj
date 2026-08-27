package migrateprotocol

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

const nonemptyDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestRequestDocumentAndStrictRequestParsing(t *testing.T) {
	t.Parallel()
	want := `{"protocol_version":1,"command":"migrations.migrate"}`
	first := RequestDocument()
	if string(first) != want {
		t.Fatalf("request = %q, want %q", first, want)
	}
	first[0] = '!'
	if string(RequestDocument()) != want {
		t.Fatal("RequestDocument retained caller mutation")
	}

	tests := []struct {
		name string
		wire string
		code string
	}{
		{name: "valid", wire: want},
		{name: "valid reordered", wire: `{"command":"migrations.migrate","protocol_version":1}`},
		{name: "incompatible", wire: `{"protocol_version":2,"command":"migrations.migrate"}`, code: CodeProtocolIncompatible},
		{name: "missing version", wire: `{"command":"migrations.migrate"}`, code: CodeInvalidRequest},
		{name: "duplicate", wire: `{"protocol_version":2,"protocol_version":1,"command":"migrations.migrate"}`, code: CodeInvalidRequest},
		{name: "noncanonical number", wire: `{"protocol_version":1.0,"command":"migrations.migrate"}`, code: CodeInvalidRequest},
		{name: "unknown command", wire: `{"protocol_version":1,"command":"migrations.check"}`, code: CodeInvalidRequest},
		{name: "unknown member", wire: `{"protocol_version":1,"command":"migrations.migrate","dsn":"secret"}`, code: CodeInvalidRequest},
		{name: "trailing", wire: want + `{}`, code: CodeInvalidRequest},
		{name: "invalid utf8", wire: want + "\xff", code: CodeInvalidRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			failure, failed, err := ReadRequest(strings.NewReader(test.wire))
			if err != nil {
				t.Fatalf("ReadRequest = %v", err)
			}
			if test.code == "" {
				if failed || failure != (Failure{}) {
					t.Fatalf("valid request = %+v, %v", failure, failed)
				}
				return
			}
			wantFailure := Failure{Category: CategoryProtocol, Code: test.code}
			if !failed || failure != wantFailure {
				t.Fatalf("failure = %+v, %v, want %+v", failure, failed, wantFailure)
			}
		})
	}
}

func TestReadRequestTransportAndResourceBoundaries(t *testing.T) {
	t.Parallel()
	readerErr := errors.New("reader failed")
	if _, failed, err := ReadRequest(errorReader{err: readerErr}); failed || !errors.Is(err, readerErr) {
		t.Fatalf("reader error = failed %v err %v", failed, err)
	}
	if _, failed, err := ReadRequest(nil); failed || err == nil {
		t.Fatalf("nil reader = failed %v err %v", failed, err)
	}

	request := RequestDocument()
	for _, size := range []int{MaxRequestBytes - 1, MaxRequestBytes} {
		document := append([]byte(nil), request...)
		document = append(document, bytes.Repeat([]byte{' '}, size-len(document))...)
		failure, failed, err := ReadRequest(bytes.NewReader(document))
		if err != nil || failed || failure != (Failure{}) {
			t.Fatalf("maximum request %d = %+v, %v, %v", size, failure, failed, err)
		}
	}

	oversized := &trackingReader{chunks: [][]byte{
		bytes.Repeat([]byte{'x'}, MaxRequestBytes),
		bytes.Repeat([]byte{'y'}, 17),
		[]byte("drained-tail"),
	}}
	failure, failed, err := ReadRequest(oversized)
	wantConsumed := MaxRequestBytes + 17 + len("drained-tail")
	if err != nil || !failed || failure.Code != CodeInvalidRequest || oversized.consumed != wantConsumed || len(oversized.chunks) != 0 {
		t.Fatalf("oversized request = %+v, %v, %v, consumed %d", failure, failed, err, oversized.consumed)
	}

	tailErr := errors.New("tail failed")
	failingTail := &trackingReader{chunks: [][]byte{bytes.Repeat([]byte{'z'}, MaxRequestBytes+1)}, finalErr: tailErr}
	if _, failed, err := ReadRequest(failingTail); failed || !errors.Is(err, tailErr) {
		t.Fatalf("post-cap transport error = failed %v err %v", failed, err)
	}
}

func TestResponseRoundTripAndTransportPrecedence(t *testing.T) {
	t.Parallel()
	results := []Result{
		{DefinitionSetDigest: EmptySetDigest},
		{SourceCount: 1, DefinitionCount: 2, DefinitionSetDigest: nonemptyDigest},
	}
	for _, result := range results {
		document, err := EncodeResponse(Response{OK: true, Result: result})
		if err != nil {
			t.Fatalf("EncodeResponse(%+v) = %v", result, err)
		}
		response, failure, failed := ParseResponse(document, true)
		if failed || failure != (Failure{}) || !response.OK || response.Result != result {
			t.Fatalf("ParseResponse(%s) = %+v, %+v, %v", document, response, failure, failed)
		}
	}

	linked := Failure{Category: CategoryTransaction, Code: "commit_outcome_unknown", CleanupFailed: true}
	document, err := EncodeResponse(Response{Failure: linked})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"protocol_version":1,"status":"error","error":{"category":"migration_transaction_error","code":"commit_outcome_unknown","cleanup_failed":true}}`
	if string(document) != want {
		t.Fatalf("failure response = %s, want %s", document, want)
	}
	response, failure, failed := ParseResponse(document, true)
	if failed || failure != (Failure{}) || response.OK || response.Failure != linked {
		t.Fatalf("logical failure = %+v, %+v, %v", response, failure, failed)
	}
	_, failure, failed = ParseResponse(document, false)
	if !failed || failure != (Failure{Category: CategoryProtocol, Code: CodeRunnerFailed}) {
		t.Fatalf("transport precedence = %+v, %v", failure, failed)
	}
}

func TestResponseStrictShapeVersionAndSemanticBounds(t *testing.T) {
	t.Parallel()
	valid, err := EncodeResponse(Response{OK: true, Result: Result{SourceCount: 1, DefinitionCount: 2, DefinitionSetDigest: nonemptyDigest}})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		wire []byte
		code string
	}{
		{name: "duplicate", wire: bytes.Replace(valid, []byte(`"status":"ok"`), []byte(`"status":"ok","status":"ok"`), 1), code: CodeInvalidResponse},
		{name: "unknown", wire: bytes.Replace(valid, []byte(`"result":`), []byte(`"secret":"dsn","result":`), 1), code: CodeInvalidResponse},
		{name: "trailing", wire: append(append([]byte(nil), valid...), []byte(`{}`)...), code: CodeInvalidResponse},
		{name: "noncanonical count", wire: bytes.Replace(valid, []byte(`"source_count":1`), []byte(`"source_count":1.0`), 1), code: CodeInvalidResponse},
		{name: "negative count", wire: bytes.Replace(valid, []byte(`"source_count":1`), []byte(`"source_count":-1`), 1), code: CodeInvalidResponse},
		{name: "zero source nonzero definitions", wire: bytes.Replace(valid, []byte(`"source_count":1`), []byte(`"source_count":0`), 1), code: CodeInvalidResponse},
		{name: "empty digest with definitions", wire: bytes.Replace(valid, []byte(nonemptyDigest), []byte(EmptySetDigest), 1), code: CodeInvalidResponse},
		{name: "uppercase digest", wire: bytes.Replace(valid, []byte(nonemptyDigest), []byte(strings.ToUpper(nonemptyDigest)), 1), code: CodeInvalidResponse},
		{name: "invalid utf8", wire: append(append([]byte(nil), valid...), 0xff), code: CodeInvalidResponse},
		{name: "oversized", wire: bytes.Repeat([]byte{'x'}, MaxResponseBytes+1), code: CodeInvalidResponse},
		{name: "incompatible", wire: bytes.Replace(valid, []byte(`"protocol_version":1`), []byte(`"protocol_version":2`), 1), code: CodeProtocolIncompatible},
		{name: "missing cleanup observation", wire: []byte(`{"protocol_version":1,"status":"error","error":{"category":"migration_backend_error","code":"backend_close_failed"}}`), code: CodeInvalidResponse},
		{name: "close without cleanup failure", wire: []byte(`{"protocol_version":1,"status":"error","error":{"category":"migration_backend_error","code":"backend_close_failed","cleanup_failed":false}}`), code: CodeInvalidResponse},
		{name: "preflight with cleanup failure", wire: []byte(`{"protocol_version":1,"status":"error","error":{"category":"migration_definition_source_error","code":"invalid_definition_document","cleanup_failed":true}}`), code: CodeInvalidResponse},
		{name: "unknown taxonomy", wire: []byte(`{"protocol_version":1,"status":"error","error":{"category":"migration_transaction_error","code":"secret text","cleanup_failed":false}}`), code: CodeInvalidResponse},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, failure, failed := ParseResponse(test.wire, true)
			want := Failure{Category: CategoryProtocol, Code: test.code}
			if !failed || failure != want {
				t.Fatalf("ParseResponse = %+v, %v, want %+v", failure, failed, want)
			}
		})
	}

	invalid := []Response{
		{},
		{OK: true, Result: Result{SourceCount: 1, DefinitionCount: 2, DefinitionSetDigest: EmptySetDigest}},
		{OK: true, Result: Result{DefinitionSetDigest: EmptySetDigest}, Failure: Failure{Category: CategoryTransaction, Code: "commit_failed"}},
		{Failure: Failure{Category: CategoryBackend, Code: CodeBackendCloseFailed}},
		{Failure: Failure{Category: CategorySource, Code: "invalid_definition_document", CleanupFailed: true}},
	}
	for _, response := range invalid {
		if _, err := EncodeResponse(response); err == nil {
			t.Errorf("EncodeResponse(%+v) unexpectedly succeeded", response)
		}
	}
}

func TestClosedTaxonomyLinkedBoundaryAndExitCodes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		failure Failure
		exit    int
		linked  bool
	}{
		{failure: Failure{Category: CategoryCommand, Code: CodeInvalidArguments}, exit: 2},
		{failure: Failure{Category: CategorySelection, Code: CodeProjectNotFound}, exit: 2},
		{failure: Failure{Category: CategoryBuild, Code: CodeProjectBuildFailed}, exit: 3},
		{failure: Failure{Category: CategoryProtocol, Code: CodeInvalidRequest}, exit: 3, linked: true},
		{failure: Failure{Category: CategoryProtocol, Code: CodeInvalidResponse}, exit: 3},
		{failure: Failure{Category: CategoryProcess, Code: CodeProjectInterrupted}, exit: 130},
		{failure: Failure{Category: CategoryDiscovery, Code: CodeUnsafeSourceEntry}, exit: 1, linked: true},
		{failure: Failure{Category: CategorySource, Code: "invalid_definition_document"}, exit: 1, linked: true},
		{failure: Failure{Category: CategoryGraph, Code: "dependency_cycle"}, exit: 1, linked: true},
		{failure: Failure{Category: CategoryState, Code: "invalid_state"}, exit: 1, linked: true},
		{failure: Failure{Category: CategoryCapability, Code: "revision_fence_unsupported"}, exit: 1, linked: true},
		{failure: Failure{Category: CategoryExecution, Code: "operation_failed"}, exit: 3, linked: true},
		{failure: Failure{Category: CategoryRecorder, Code: "read_failed"}, exit: 3, linked: true},
		{failure: Failure{Category: CategoryRecorder, Code: "record_failed"}, exit: 3, linked: true},
		{failure: Failure{Category: CategoryTransaction, Code: CodeRollbackFailed}, exit: 3, linked: true},
		{failure: Failure{Category: CategoryPlan, Code: "invalid_execution_plan"}, exit: 1, linked: true},
		{failure: Failure{Category: CategoryHistory, Code: "inconsistent_applied_history"}, exit: 1, linked: true},
		{failure: Failure{Category: CategoryConflict, Code: "stale_history_revision"}, exit: 3, linked: true},
		{failure: Failure{Category: CategoryBackend, Code: CodeBackendOpenFailed}, exit: 3, linked: true},
		{failure: Failure{Category: CategoryBackend, Code: CodeBackendOpenFailed, CleanupFailed: true}, exit: 3, linked: true},
		{failure: Failure{Category: CategoryBackend, Code: CodeBackendCloseFailed, CleanupFailed: true}, exit: 3, linked: true},
		{failure: Failure{Category: CategoryInternal, Code: CodeProjectInternalError}, exit: 3, linked: true},
	}
	for _, test := range tests {
		exit, ok := ExitCode(test.failure)
		if !ok || exit != test.exit {
			t.Errorf("ExitCode(%+v) = %d, %v, want %d", test.failure, exit, ok, test.exit)
		}
		if got := IsLinkedFailure(test.failure); got != test.linked {
			t.Errorf("IsLinkedFailure(%+v) = %v, want %v", test.failure, got, test.linked)
		}
	}
	for _, invalid := range []Failure{
		{Category: CategorySource, Code: "invented"},
		{Category: CategoryBackend, Code: CodeBackendCloseFailed},
		{Category: CategoryBackend, Code: CodeInvalidBackend, CleanupFailed: true},
		{Category: CategoryProtocol, Code: CodeInvalidRequest, CleanupFailed: true},
	} {
		if IsLinkedFailure(invalid) {
			t.Errorf("invalid linked failure accepted: %+v", invalid)
		}
	}
}

func TestWriteResponseRejectsNilShortAndFailedWriters(t *testing.T) {
	t.Parallel()
	response := Response{OK: true, Result: Result{DefinitionSetDigest: EmptySetDigest}}
	if err := WriteResponse(nil, response); err == nil {
		t.Fatal("nil writer accepted")
	}
	if err := WriteResponse(shortWriter{}, response); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short writer error = %v", err)
	}
	want := errors.New("write failed")
	if err := WriteResponse(errorWriter{err: want}, response); !errors.Is(err, want) {
		t.Fatalf("failed writer error = %v", err)
	}
}

func TestArbitraryProtocolBytesDoNotPanic(t *testing.T) {
	t.Parallel()
	for length := 0; length <= 512; length++ {
		document := make([]byte, length)
		for index := range document {
			document[index] = byte((length*31 + index*17) % 256)
		}
		_, _, _ = ParseResponse(document, true)
		_, _, _ = ReadRequest(bytes.NewReader(document))
	}
}

type errorReader struct{ err error }

func (reader errorReader) Read([]byte) (int, error) { return 0, reader.err }

type trackingReader struct {
	chunks   [][]byte
	finalErr error
	consumed int
}

func (reader *trackingReader) Read(buffer []byte) (int, error) {
	if len(reader.chunks) == 0 {
		if reader.finalErr != nil {
			err := reader.finalErr
			reader.finalErr = nil
			return 0, err
		}
		return 0, io.EOF
	}
	chunk := reader.chunks[0]
	read := copy(buffer, chunk)
	reader.consumed += read
	if read == len(chunk) {
		reader.chunks = reader.chunks[1:]
	} else {
		reader.chunks[0] = chunk[read:]
	}
	return read, nil
}

type errorWriter struct{ err error }

func (writer errorWriter) Write([]byte) (int, error) { return 0, writer.err }

type shortWriter struct{}

func (shortWriter) Write(value []byte) (int, error) { return len(value) - 1, nil }
