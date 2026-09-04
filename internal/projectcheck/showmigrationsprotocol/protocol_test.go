package showmigrationsprotocol

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestRequestDocumentAndStrictRequestParsing(t *testing.T) {
	t.Parallel()
	want := `{"protocol_version":1,"command":"migrations.show"}`
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
		{name: "valid reordered", wire: `{"command":"migrations.show","protocol_version":1}`},
		{name: "incompatible", wire: `{"protocol_version":2,"command":"migrations.show"}`, code: CodeProtocolIncompatible},
		{name: "missing version", wire: `{"command":"migrations.show"}`, code: CodeInvalidRequest},
		{name: "duplicate", wire: `{"protocol_version":2,"protocol_version":1,"command":"migrations.show"}`, code: CodeInvalidRequest},
		{name: "decimal version", wire: `{"protocol_version":1.0,"command":"migrations.show"}`, code: CodeInvalidRequest},
		{name: "exponent version", wire: `{"protocol_version":1e0,"command":"migrations.show"}`, code: CodeInvalidRequest},
		{name: "negative version", wire: `{"protocol_version":-1,"command":"migrations.show"}`, code: CodeInvalidRequest},
		{name: "string version", wire: `{"protocol_version":"1","command":"migrations.show"}`, code: CodeInvalidRequest},
		{name: "unknown command", wire: `{"protocol_version":1,"command":"migrations.migrate"}`, code: CodeInvalidRequest},
		{name: "unknown member", wire: `{"protocol_version":1,"command":"migrations.show","dsn":"secret"}`, code: CodeInvalidRequest},
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
	if _, failed, err := ReadRequest(zeroReader{}); failed || !errors.Is(err, io.ErrNoProgress) {
		t.Fatalf("no-progress reader = failed %v err %v", failed, err)
	}
	if _, failed, err := ReadRequest(invalidCountReader{}); failed || err == nil {
		t.Fatalf("invalid-count reader = failed %v err %v", failed, err)
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

func TestResponseRoundTripCanonicalRowsAndTransportPrecedence(t *testing.T) {
	t.Parallel()
	rows := []Row{
		{App: "alpha", Name: "0002_dependency_first", Status: StatusApplied},
		{App: "alpha", Name: "0001_name_order_is_not_authority", Status: StatusUnapplied},
		{App: "alpha", Name: "legacy_a", Status: StatusUnknown},
		{App: "alpha", Name: "legacy_z", Status: StatusUnknown},
		{App: "blog", Name: "0001_article", Status: StatusApplied},
	}
	document, err := EncodeResponse(Response{OK: true, Result: Result{Rows: rows}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"protocol_version":1,"status":"ok","result":{"rows":[{"app":"alpha","name":"0002_dependency_first","status":"applied"},{"app":"alpha","name":"0001_name_order_is_not_authority","status":"unapplied"},{"app":"alpha","name":"legacy_a","status":"unknown"},{"app":"alpha","name":"legacy_z","status":"unknown"},{"app":"blog","name":"0001_article","status":"applied"}]}}`
	if string(document) != want {
		t.Fatalf("success response = %s, want %s", document, want)
	}
	response, failure, failed := ParseResponse(document, true)
	if failed || failure != (Failure{}) || !response.OK || !equalRows(response.Result.Rows, rows) {
		t.Fatalf("ParseResponse(%s) = %+v, %+v, %v", document, response, failure, failed)
	}
	response.Result.Rows[0].App = "mutated"
	again, _, failed := ParseResponse(document, true)
	if failed || again.Result.Rows[0].App != "alpha" {
		t.Fatalf("parsed rows retained caller mutation: %+v, %v", again, failed)
	}

	empty, err := EncodeResponse(Response{OK: true})
	if err != nil || string(empty) != `{"protocol_version":1,"status":"ok","result":{"rows":[]}}` {
		t.Fatalf("empty response = %s, %v", empty, err)
	}

	linked := Failure{Category: CategoryRecorder, Code: "read_failed", CleanupFailed: true}
	document, err = EncodeResponse(Response{Failure: linked})
	if err != nil {
		t.Fatal(err)
	}
	want = `{"protocol_version":1,"status":"error","error":{"category":"migration_recorder_error","code":"read_failed","cleanup_failed":true}}`
	if string(document) != want {
		t.Fatalf("failure response = %s, want %s", document, want)
	}
	response, failure, failed = ParseResponse(document, true)
	if failed || failure != (Failure{}) || response.OK || response.Failure != linked {
		t.Fatalf("logical failure = %+v, %+v, %v", response, failure, failed)
	}
	_, failure, failed = ParseResponse(document, false)
	if !failed || failure != (Failure{Category: CategoryProtocol, Code: CodeRunnerFailed}) {
		t.Fatalf("transport precedence = %+v, %v", failure, failed)
	}
}

func TestResponseRejectsMalformedShapeRowsOrderAndStatuses(t *testing.T) {
	t.Parallel()
	valid, err := EncodeResponse(Response{OK: true, Result: Result{Rows: []Row{
		{App: "alpha", Name: "0001", Status: StatusApplied},
		{App: "alpha", Name: "legacy", Status: StatusUnknown},
		{App: "blog", Name: "0001", Status: StatusUnapplied},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		wire []byte
		code string
	}{
		{name: "duplicate root key", wire: bytes.Replace(valid, []byte(`"status":"ok"`), []byte(`"status":"ok","status":"ok"`), 1), code: CodeInvalidResponse},
		{name: "duplicate row key", wire: bytes.Replace(valid, []byte(`"app":"alpha"`), []byte(`"app":"alpha","app":"alpha"`), 1), code: CodeInvalidResponse},
		{name: "unknown root key", wire: bytes.Replace(valid, []byte(`"result":`), []byte(`"secret":"dsn","result":`), 1), code: CodeInvalidResponse},
		{name: "unknown result key", wire: bytes.Replace(valid, []byte(`"rows":`), []byte(`"count":3,"rows":`), 1), code: CodeInvalidResponse},
		{name: "unknown row key", wire: bytes.Replace(valid, []byte(`"status":"applied"`), []byte(`"detail":"secret","status":"applied"`), 1), code: CodeInvalidResponse},
		{name: "trailing value", wire: append(append([]byte(nil), valid...), []byte(`{}`)...), code: CodeInvalidResponse},
		{name: "decimal version", wire: bytes.Replace(valid, []byte(`"protocol_version":1`), []byte(`"protocol_version":1.0`), 1), code: CodeInvalidResponse},
		{name: "exponent version", wire: bytes.Replace(valid, []byte(`"protocol_version":1`), []byte(`"protocol_version":1e0`), 1), code: CodeInvalidResponse},
		{name: "incompatible version", wire: bytes.Replace(valid, []byte(`"protocol_version":1`), []byte(`"protocol_version":2`), 1), code: CodeProtocolIncompatible},
		{name: "unknown outer status", wire: bytes.Replace(valid, []byte(`"status":"ok"`), []byte(`"status":"partial"`), 1), code: CodeInvalidResponse},
		{name: "ambiguous success error", wire: bytes.Replace(valid, []byte(`"result":`), []byte(`"error":{},"result":`), 1), code: CodeInvalidResponse},
		{name: "rows null", wire: []byte(`{"protocol_version":1,"status":"ok","result":{"rows":null}}`), code: CodeInvalidResponse},
		{name: "row not object", wire: []byte(`{"protocol_version":1,"status":"ok","result":{"rows":[null]}}`), code: CodeInvalidResponse},
		{name: "row field not string", wire: []byte(`{"protocol_version":1,"status":"ok","result":{"rows":[{"app":1,"name":"0001","status":"applied"}]}}`), code: CodeInvalidResponse},
		{name: "empty app", wire: []byte(`{"protocol_version":1,"status":"ok","result":{"rows":[{"app":"","name":"0001","status":"applied"}]}}`), code: CodeInvalidResponse},
		{name: "empty name", wire: []byte(`{"protocol_version":1,"status":"ok","result":{"rows":[{"app":"alpha","name":"","status":"applied"}]}}`), code: CodeInvalidResponse},
		{name: "unknown row status", wire: []byte(`{"protocol_version":1,"status":"ok","result":{"rows":[{"app":"alpha","name":"0001","status":"pending"}]}}`), code: CodeInvalidResponse},
		{name: "duplicate identity different status", wire: []byte(`{"protocol_version":1,"status":"ok","result":{"rows":[{"app":"alpha","name":"0001","status":"applied"},{"app":"alpha","name":"0001","status":"unapplied"}]}}`), code: CodeInvalidResponse},
		{name: "descending app", wire: []byte(`{"protocol_version":1,"status":"ok","result":{"rows":[{"app":"blog","name":"0001","status":"applied"},{"app":"alpha","name":"0001","status":"applied"}]}}`), code: CodeInvalidResponse},
		{name: "split app group", wire: []byte(`{"protocol_version":1,"status":"ok","result":{"rows":[{"app":"alpha","name":"0001","status":"applied"},{"app":"blog","name":"0001","status":"applied"},{"app":"alpha","name":"0002","status":"applied"}]}}`), code: CodeInvalidResponse},
		{name: "known after unknown", wire: []byte(`{"protocol_version":1,"status":"ok","result":{"rows":[{"app":"alpha","name":"legacy","status":"unknown"},{"app":"alpha","name":"0001","status":"unapplied"}]}}`), code: CodeInvalidResponse},
		{name: "unknown names descending", wire: []byte(`{"protocol_version":1,"status":"ok","result":{"rows":[{"app":"alpha","name":"legacy_z","status":"unknown"},{"app":"alpha","name":"legacy_a","status":"unknown"}]}}`), code: CodeInvalidResponse},
		{name: "invalid utf8", wire: append(append([]byte(nil), valid...), 0xff), code: CodeInvalidResponse},
		{name: "oversized", wire: bytes.Repeat([]byte{'x'}, MaxResponseBytes+1), code: CodeInvalidResponse},
		{name: "missing cleanup observation", wire: []byte(`{"protocol_version":1,"status":"error","error":{"category":"migration_recorder_error","code":"read_failed"}}`), code: CodeInvalidResponse},
		{name: "close without cleanup failure", wire: []byte(`{"protocol_version":1,"status":"error","error":{"category":"migration_backend_error","code":"backend_close_failed","cleanup_failed":false}}`), code: CodeInvalidResponse},
		{name: "preopen failure with cleanup", wire: []byte(`{"protocol_version":1,"status":"error","error":{"category":"migration_definition_source_error","code":"invalid_definition_document","cleanup_failed":true}}`), code: CodeInvalidResponse},
		{name: "unknown taxonomy", wire: []byte(`{"protocol_version":1,"status":"error","error":{"category":"migration_recorder_error","code":"secret text","cleanup_failed":false}}`), code: CodeInvalidResponse},
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
}

func TestEncodeResponseRejectsInvalidAndAmbiguousValues(t *testing.T) {
	t.Parallel()
	invalidUTF8 := string([]byte{0xff})
	invalid := []Response{
		{},
		{OK: true, Result: Result{Rows: []Row{{App: "", Name: "0001", Status: StatusApplied}}}},
		{OK: true, Result: Result{Rows: []Row{{App: "alpha", Name: "", Status: StatusApplied}}}},
		{OK: true, Result: Result{Rows: []Row{{App: invalidUTF8, Name: "0001", Status: StatusApplied}}}},
		{OK: true, Result: Result{Rows: []Row{{App: "alpha", Name: invalidUTF8, Status: StatusApplied}}}},
		{OK: true, Result: Result{Rows: []Row{{App: "alpha", Name: "0001", Status: "pending"}}}},
		{OK: true, Result: Result{Rows: []Row{{App: "blog", Name: "0001", Status: StatusApplied}, {App: "alpha", Name: "0001", Status: StatusApplied}}}},
		{OK: true, Result: Result{Rows: []Row{{App: "alpha", Name: "legacy", Status: StatusUnknown}, {App: "alpha", Name: "0001", Status: StatusApplied}}}},
		{OK: true, Result: Result{Rows: []Row{{App: "alpha", Name: "legacy_z", Status: StatusUnknown}, {App: "alpha", Name: "legacy_a", Status: StatusUnknown}}}},
		{OK: true, Result: Result{Rows: []Row{{App: "alpha", Name: "0001", Status: StatusApplied}, {App: "alpha", Name: "0001", Status: StatusUnapplied}}}},
		{OK: true, Failure: Failure{Category: CategoryRecorder, Code: "read_failed"}},
		{Result: Result{Rows: []Row{}}, Failure: Failure{Category: CategoryRecorder, Code: "read_failed"}},
		{Result: Result{Rows: []Row{{App: "alpha", Name: "0001", Status: StatusApplied}}}, Failure: Failure{Category: CategoryRecorder, Code: "read_failed"}},
		{Failure: Failure{Category: CategoryBackend, Code: CodeBackendCloseFailed}},
		{Failure: Failure{Category: CategorySource, Code: "invalid_definition_document", CleanupFailed: true}},
	}
	for _, response := range invalid {
		if _, err := EncodeResponse(response); err == nil {
			t.Errorf("EncodeResponse(%+v) unexpectedly succeeded", response)
		}
	}
}

func TestRowCountAndResponseByteBoundaries(t *testing.T) {
	rows := make([]Row, MaxRows)
	for index := range rows {
		rows[index] = Row{App: "alpha", Name: fmt.Sprintf("migration_%04d", index), Status: StatusApplied}
	}
	document, err := EncodeResponse(Response{OK: true, Result: Result{Rows: rows}})
	if err != nil {
		t.Fatalf("MaxRows encode = %v", err)
	}
	response, failure, failed := ParseResponse(document, true)
	if failed || failure != (Failure{}) || len(response.Result.Rows) != MaxRows {
		t.Fatalf("MaxRows parse = rows %d failure %+v failed %v", len(response.Result.Rows), failure, failed)
	}

	tooMany := append(append([]Row(nil), rows...), Row{App: "alpha", Name: "overflow", Status: StatusApplied})
	if _, err := EncodeResponse(Response{OK: true, Result: Result{Rows: tooMany}}); err == nil {
		t.Fatal("MaxRows+1 encoded")
	}
	oversizedRows := append(append([]byte(nil), document[:len(document)-3]...), []byte(`,{"app":"alpha","name":"overflow","status":"applied"}]}}`)...)
	if _, failure, failed := ParseResponse(oversizedRows, true); !failed || failure.Code != CodeInvalidResponse {
		t.Fatalf("MaxRows+1 parsed = %+v, %v", failure, failed)
	}

	base, err := EncodeResponse(Response{OK: true, Result: Result{Rows: []Row{{App: "alpha", Name: "x", Status: StatusApplied}}}})
	if err != nil {
		t.Fatal(err)
	}
	exactName := strings.Repeat("x", 1+MaxResponseBytes-len(base))
	exact, err := EncodeResponse(Response{OK: true, Result: Result{Rows: []Row{{App: "alpha", Name: exactName, Status: StatusApplied}}}})
	if err != nil || len(exact) != MaxResponseBytes {
		t.Fatalf("exact response boundary = bytes %d err %v", len(exact), err)
	}
	parsed, failure, failed := ParseResponse(exact, true)
	if failed || failure != (Failure{}) || len(parsed.Result.Rows) != 1 || len(parsed.Result.Rows[0].Name) != len(exactName) {
		t.Fatalf("exact response parse = %+v, %+v, %v", parsed.Failure, failure, failed)
	}
	if _, err := EncodeResponse(Response{OK: true, Result: Result{Rows: []Row{{App: "alpha", Name: exactName + "x", Status: StatusApplied}}}}); err == nil {
		t.Fatal("MaxResponseBytes+1 encoded")
	}
	if _, failure, failed := ParseResponse(append(exact, ' '), true); !failed || failure.Code != CodeInvalidResponse {
		t.Fatalf("MaxResponseBytes+1 parsed = %+v, %v", failure, failed)
	}
}

func TestClosedTaxonomyLinkedBoundaryCleanupAndExitCodes(t *testing.T) {
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
		{failure: Failure{Category: CategoryHistory, Code: "inconsistent_applied_history"}, exit: 1, linked: true},
		{failure: Failure{Category: CategoryHistory, Code: "inconsistent_applied_history", CleanupFailed: true}, exit: 1, linked: true},
		{failure: Failure{Category: CategoryRecorder, Code: "read_failed"}, exit: 3, linked: true},
		{failure: Failure{Category: CategoryRecorder, Code: "read_failed", CleanupFailed: true}, exit: 3, linked: true},
		{failure: Failure{Category: CategoryCapability, Code: "revision_fence_adoption_required"}, exit: 1, linked: true},
		{failure: Failure{Category: CategoryTransaction, Code: "history_revision_contended"}, exit: 3, linked: true},
		{failure: Failure{Category: CategoryConflict, Code: "stale_history_revision"}, exit: 3, linked: true},
		{failure: Failure{Category: CategoryBackend, Code: CodeBackendOpenFailed}, exit: 3, linked: true},
		{failure: Failure{Category: CategoryBackend, Code: CodeBackendOpenFailed, CleanupFailed: true}, exit: 3, linked: true},
		{failure: Failure{Category: CategoryBackend, Code: CodeInvalidBackend, CleanupFailed: true}, exit: 3, linked: true},
		{failure: Failure{Category: CategoryBackend, Code: CodeBackendCloseFailed, CleanupFailed: true}, exit: 3, linked: true},
		{failure: Failure{Category: CategoryInternal, Code: CodeProjectInternalError}, exit: 3, linked: true},
		{failure: Failure{Category: CategoryInternal, Code: CodeProjectInternalError, CleanupFailed: true}, exit: 3, linked: true},
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
		{Category: CategoryProtocol, Code: CodeInvalidRequest, CleanupFailed: true},
		{Category: CategoryDiscovery, Code: CodeSourceReadFailed, CleanupFailed: true},
	} {
		if IsLinkedFailure(invalid) {
			t.Errorf("invalid linked failure accepted: %+v", invalid)
		}
	}
}

func TestWriteResponseRejectsNilShortAndFailedWriters(t *testing.T) {
	t.Parallel()
	response := Response{OK: true}
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

func equalRows(left, right []Row) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

type errorReader struct{ err error }

func (reader errorReader) Read([]byte) (int, error) { return 0, reader.err }

type zeroReader struct{}

func (zeroReader) Read([]byte) (int, error) { return 0, nil }

type invalidCountReader struct{}

func (invalidCountReader) Read([]byte) (int, error) { return -1, nil }

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
