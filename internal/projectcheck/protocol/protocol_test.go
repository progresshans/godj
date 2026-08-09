package protocol

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

const nonemptyDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestRequestDocumentAndClosedRequestParsing(t *testing.T) {
	t.Parallel()
	want := `{"protocol_version":1,"command":"migrations.check"}`
	first := RequestDocument()
	if string(first) != want {
		t.Fatalf("request = %q, want %q", first, want)
	}
	first[0] = '!'
	if string(RequestDocument()) != want {
		t.Fatal("RequestDocument retained caller mutation")
	}

	cases := []struct {
		name string
		wire string
		code string
	}{
		{name: "valid reordered", wire: `{"command":"migrations.check","protocol_version":1}`},
		{name: "incompatible", wire: `{"protocol_version":2,"command":"migrations.check"}`, code: CodeProjectProtocolIncompatible},
		{name: "missing version", wire: `{"command":"migrations.check"}`, code: CodeInvalidProjectRunnerRequest},
		{name: "duplicate coordinate", wire: `{"protocol_version":2,"protocol_version":1,"command":"migrations.check"}`, code: CodeInvalidProjectRunnerRequest},
		{name: "noncanonical coordinate", wire: `{"protocol_version":1.0,"command":"migrations.check"}`, code: CodeInvalidProjectRunnerRequest},
		{name: "unknown command", wire: `{"protocol_version":1,"command":"other"}`, code: CodeInvalidProjectRunnerRequest},
		{name: "extra member", wire: `{"protocol_version":1,"command":"migrations.check","extra":0}`, code: CodeInvalidProjectRunnerRequest},
		{name: "trailing value", wire: `{"protocol_version":1,"command":"migrations.check"}{}`, code: CodeInvalidProjectRunnerRequest},
		{name: "invalid utf8", wire: "{\"protocol_version\":1,\"command\":\"migrations.check\"}\xff", code: CodeInvalidProjectRunnerRequest},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			failure, failed, err := ReadRequest(strings.NewReader(test.wire))
			if err != nil {
				t.Fatalf("ReadRequest = %v", err)
			}
			if test.code == "" {
				if failed {
					t.Fatalf("valid request failed: %+v", failure)
				}
				return
			}
			if !failed || failure != (Failure{Category: CategoryProtocol, Code: test.code}) {
				t.Fatalf("failure = %+v, %v, want %s", failure, failed, test.code)
			}
		})
	}
}

func TestReadRequestTransportAndSizeFailures(t *testing.T) {
	t.Parallel()
	readerFailure := errors.New("reader failed")
	if _, failed, err := ReadRequest(errorReader{err: readerFailure}); failed || !errors.Is(err, readerFailure) {
		t.Fatalf("reader error = failed %v, err %v", failed, err)
	}
	if _, failed, err := ReadRequest(nil); failed || err == nil {
		t.Fatalf("nil reader = failed %v, err %v", failed, err)
	}
	oversized := bytes.Repeat([]byte{' '}, MaxRequestBytes+1)
	failure, failed, err := ReadRequest(bytes.NewReader(oversized))
	if err != nil || !failed || failure.Code != CodeInvalidProjectRunnerRequest {
		t.Fatalf("oversized request = %+v, %v, %v", failure, failed, err)
	}

	multichunk := &trackingReader{chunks: [][]byte{
		bytes.Repeat([]byte{'x'}, MaxRequestBytes),
		bytes.Repeat([]byte{'y'}, 17),
		[]byte("drained-tail"),
	}}
	failure, failed, err = ReadRequest(multichunk)
	wantConsumed := MaxRequestBytes + 17 + len("drained-tail")
	if err != nil || !failed || failure.Code != CodeInvalidProjectRunnerRequest || multichunk.consumed != wantConsumed || len(multichunk.chunks) != 0 {
		t.Fatalf("drained oversized request = %+v, %v, %v, reads %d, consumed %d", failure, failed, err, multichunk.reads, multichunk.consumed)
	}

	afterCap := errors.New("post-cap failure")
	failingTail := &trackingReader{
		chunks:   [][]byte{bytes.Repeat([]byte{'z'}, MaxRequestBytes+1)},
		finalErr: afterCap,
	}
	if _, failed, err := ReadRequest(failingTail); failed || !errors.Is(err, afterCap) {
		t.Fatalf("post-cap error = failed %v, err %v", failed, err)
	}
}

func TestReadRequestAcceptsActualMaximumMinusOneAndMaximum(t *testing.T) {
	t.Parallel()
	request := RequestDocument()
	for _, size := range []int{MaxRequestBytes - 1, MaxRequestBytes} {
		document := append([]byte(nil), request...)
		document = append(document, bytes.Repeat([]byte{' '}, size-len(document))...)
		if len(document) != size {
			t.Fatalf("request bytes = %d, want %d", len(document), size)
		}
		failure, failed, err := ReadRequest(bytes.NewReader(document))
		if err != nil || failed || failure != (Failure{}) {
			t.Fatalf("ReadRequest(%d bytes) = %+v, %v, %v", size, failure, failed, err)
		}
	}
}

func TestResponseRoundTripAndCoordinatePrecedence(t *testing.T) {
	t.Parallel()
	successes := []Result{
		{DefinitionSetDigest: EmptySetDigest},
		{SourceCount: 1, DefinitionCount: 1, DefinitionSetDigest: nonemptyDigest},
	}
	for _, result := range successes {
		document, err := EncodeResponse(Response{OK: true, Result: result})
		if err != nil {
			t.Fatalf("EncodeResponse(%+v) = %v", result, err)
		}
		response, failure, failed := ParseResponse(document, true)
		if failed || !response.OK || response.Result != result || failure != (Failure{}) {
			t.Fatalf("ParseResponse(%s) = %+v, %+v, %v", document, response, failure, failed)
		}
	}

	linkedFailure := Failure{Category: CategoryDiscovery, Code: CodeUnsafeSourceEntry}
	document, err := EncodeResponse(Response{Failure: linkedFailure})
	if err != nil {
		t.Fatal(err)
	}
	response, failure, failed := ParseResponse(document, true)
	if failed || response.OK || response.Failure != linkedFailure || failure != (Failure{}) {
		t.Fatalf("logical response = %+v, %+v, %v", response, failure, failed)
	}

	incompatible := []byte(`{"protocol_version":2,"status":"ok","result":{"source_count":0,"definition_count":0,"definition_set_digest":"` + EmptySetDigest + `"}}`)
	_, failure, failed = ParseResponse(incompatible, true)
	if !failed || failure.Code != CodeProjectProtocolIncompatible {
		t.Fatalf("incompatible = %+v, %v", failure, failed)
	}
	_, failure, failed = ParseResponse([]byte(`{"protocol_version":2,"protocol_version":1}`), true)
	if !failed || failure.Code != CodeInvalidProjectRunnerResponse {
		t.Fatalf("duplicate coordinate = %+v, %v", failure, failed)
	}
	_, failure, failed = ParseResponse(incompatible, false)
	if !failed || failure.Code != CodeProjectRunnerFailed {
		t.Fatalf("transport precedence = %+v, %v", failure, failed)
	}
}

func TestResponseStrictSchemaAndInvariants(t *testing.T) {
	t.Parallel()
	cases := []string{
		`{"protocol_version":1,"status":"ok","status":"error"}`,
		`{"protocol_version":1,"status":"ok","result":{"source_count":01,"definition_count":1,"definition_set_digest":"` + nonemptyDigest + `"}}`,
		`{"protocol_version":1,"status":"ok","result":{"source_count":1,"definition_count":0,"definition_set_digest":"` + nonemptyDigest + `"}}`,
		`{"protocol_version":1,"status":"ok","result":{"source_count":1,"definition_count":1,"definition_set_digest":"` + EmptySetDigest + `"}}`,
		`{"protocol_version":1,"status":"ok","result":{"source_count":0,"definition_count":0,"definition_set_digest":"` + strings.ToUpper(EmptySetDigest) + `"}}`,
		`{"protocol_version":1,"status":"error","error":{"category":"migration_project_build_error","code":"project_build_failed"}}`,
		`{"protocol_version":1,"status":"error","error":{"category":"migration_definition_source_error","code":"invented"}}`,
		`{"protocol_version":1,"status":"error","error":{"category":"migration_graph_error","code":"duplicate_node","extra":0}}`,
	}
	for _, document := range cases {
		_, failure, failed := ParseResponse([]byte(document), true)
		if !failed || failure != (Failure{Category: CategoryProtocol, Code: CodeInvalidProjectRunnerResponse}) {
			t.Errorf("ParseResponse(%s) = %+v, %v", document, failure, failed)
		}
	}

	invalidValues := []Response{
		{},
		{OK: true, Result: Result{SourceCount: 1, DefinitionCount: 0, DefinitionSetDigest: nonemptyDigest}},
		{OK: true, Result: Result{DefinitionSetDigest: EmptySetDigest}, Failure: Failure{Category: CategoryGraph, Code: "duplicate_node"}},
		{Failure: Failure{Category: CategoryBuild, Code: CodeProjectBuildFailed}},
	}
	for _, response := range invalidValues {
		if _, err := EncodeResponse(response); err == nil {
			t.Errorf("EncodeResponse(%+v) unexpectedly succeeded", response)
		}
	}
}

func TestClosedTaxonomyAndExitCodes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		failure Failure
		exit    int
		linked  bool
	}{
		{Failure{CategoryCommand, CodeInvalidArguments}, 2, false},
		{Failure{CategorySelection, CodeProjectNotFound}, 2, false},
		{Failure{CategorySelection, CodeProjectSelectionFailed}, 3, false},
		{Failure{CategoryBuild, CodeProjectBuildFailed}, 3, false},
		{Failure{CategoryProtocol, CodeInvalidProjectRunnerRequest}, 3, true},
		{Failure{CategoryProcess, CodeProjectInterrupted}, 130, false},
		{Failure{CategoryDiscovery, CodeInvalidSourceRoot}, 2, true},
		{Failure{CategoryDiscovery, CodeUnsafeSourceEntry}, 1, true},
		{Failure{CategoryDiscovery, CodeSourceReadFailed}, 3, true},
		{Failure{CategorySource, "invalid_definition_document"}, 1, true},
		{Failure{CategoryGraph, "duplicate_node"}, 1, true},
		{Failure{CategoryInternal, CodeProjectInternalError}, 3, false},
	}
	for _, test := range cases {
		exit, ok := ExitCode(test.failure)
		if !ok || exit != test.exit {
			t.Errorf("ExitCode(%+v) = %d, %v", test.failure, exit, ok)
		}
		if IsLinkedFailure(test.failure) != test.linked {
			t.Errorf("IsLinkedFailure(%+v) = %v", test.failure, !test.linked)
		}
	}
	unknown := Failure{Category: CategorySource, Code: "invented"}
	if _, ok := ExitCode(unknown); ok || IsLinkedFailure(unknown) {
		t.Fatalf("unknown pair accepted: %+v", unknown)
	}
}

func TestWriteResponseRejectsNilShortAndFailedWriters(t *testing.T) {
	t.Parallel()
	response := Response{OK: true, Result: Result{DefinitionSetDigest: EmptySetDigest}}
	if err := WriteResponse(nil, response); err == nil {
		t.Fatal("nil writer succeeded")
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
			document[index] = byte((length*31 + index*17) & 0xff)
		}
		_, _, _ = ReadRequest(bytes.NewReader(document))
		_, _, _ = ParseResponse(document, true)
	}
}

type errorReader struct{ err error }

func (reader errorReader) Read([]byte) (int, error) { return 0, reader.err }

type shortWriter struct{}

func (shortWriter) Write(value []byte) (int, error) { return len(value) - 1, nil }

type errorWriter struct{ err error }

func (writer errorWriter) Write([]byte) (int, error) { return 0, writer.err }

type trackingReader struct {
	chunks   [][]byte
	finalErr error
	reads    int
	consumed int
}

func (reader *trackingReader) Read(buffer []byte) (int, error) {
	reader.reads++
	if len(reader.chunks) == 0 {
		if reader.finalErr != nil {
			err := reader.finalErr
			reader.finalErr = nil
			return 0, err
		}
		return 0, io.EOF
	}
	chunk := reader.chunks[0]
	if len(chunk) > len(buffer) {
		copy(buffer, chunk[:len(buffer)])
		reader.chunks[0] = chunk[len(buffer):]
		reader.consumed += len(buffer)
		return len(buffer), nil
	}
	reader.chunks = reader.chunks[1:]
	read := copy(buffer, chunk)
	reader.consumed += read
	return read, nil
}

var _ io.Reader = errorReader{}
var _ io.Reader = (*trackingReader)(nil)
var _ io.Writer = shortWriter{}
var _ io.Writer = errorWriter{}
