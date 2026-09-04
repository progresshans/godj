package sqlmigrateprotocol

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestEncodeAndReadRequestCanonicalExactIdentity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		request Request
		want    string
	}{
		{
			request: Request{App: "blog", Name: "0001_article"},
			want:    `{"protocol_version":1,"command":"migrations.sql","app":"blog","name":"0001_article"}`,
		},
		{
			request: Request{App: "blog", Name: "zero"},
			want:    `{"protocol_version":1,"command":"migrations.sql","app":"blog","name":"zero"}`,
		},
		{
			request: Request{App: "blog", Name: "이름\x00with-control"},
			want:    `{"protocol_version":1,"command":"migrations.sql","app":"blog","name":"이름\u0000with-control"}`,
		},
	}
	for _, test := range tests {
		document, err := EncodeRequest(test.request)
		if err != nil || string(document) != test.want {
			t.Fatalf("EncodeRequest(%+v) = %q, %v, want %q", test.request, document, err, test.want)
		}
		request, failure, failed, err := ReadRequest(bytes.NewReader(document))
		if err != nil || failed || failure != (Failure{}) || request != test.request {
			t.Fatalf("ReadRequest(%s) = %+v, %+v, %v, %v", document, request, failure, failed, err)
		}
	}

	reordered := `{"name":"0002","app":"blog","command":"migrations.sql","protocol_version":1}`
	request, failure, failed, err := ReadRequest(strings.NewReader(reordered))
	if err != nil || failed || failure != (Failure{}) || request != (Request{App: "blog", Name: "0002"}) {
		t.Fatalf("reordered request = %+v, %+v, %v, %v", request, failure, failed, err)
	}
}

func TestRequestRejectsStrictShapeIdentityAndSurrogateViolations(t *testing.T) {
	t.Parallel()
	valid := `{"protocol_version":1,"command":"migrations.sql","app":"blog","name":"0001"}`
	tests := []struct {
		name string
		wire string
		code string
	}{
		{name: "incompatible", wire: strings.Replace(valid, `"protocol_version":1`, `"protocol_version":2`, 1), code: CodeProtocolIncompatible},
		{name: "missing version", wire: `{"command":"migrations.sql","app":"blog","name":"0001"}`, code: CodeInvalidRequest},
		{name: "duplicate", wire: strings.Replace(valid, `"app":"blog"`, `"app":"blog","app":"other"`, 1), code: CodeInvalidRequest},
		{name: "decimal version", wire: strings.Replace(valid, `"protocol_version":1`, `"protocol_version":1.0`, 1), code: CodeInvalidRequest},
		{name: "wrong command", wire: strings.Replace(valid, `migrations.sql`, `migrations.migrate`, 1), code: CodeInvalidRequest},
		{name: "unknown member", wire: strings.Replace(valid, `"name":`, `"dsn":"secret","name":`, 1), code: CodeInvalidRequest},
		{name: "noncanonical app uppercase", wire: strings.Replace(valid, `"blog"`, `"Blog"`, 1), code: CodeInvalidRequest},
		{name: "noncanonical app dash", wire: strings.Replace(valid, `"blog"`, `"blog-app"`, 1), code: CodeInvalidRequest},
		{name: "empty name", wire: strings.Replace(valid, `"0001"`, `""`, 1), code: CodeInvalidRequest},
		{name: "leading dash name", wire: strings.Replace(valid, `"0001"`, `"-0001"`, 1), code: CodeInvalidRequest},
		{name: "reserved latest", wire: strings.Replace(valid, `"0001"`, `"latest"`, 1), code: CodeInvalidRequest},
		{name: "unpaired high surrogate", wire: strings.Replace(valid, `"0001"`, `"\ud800"`, 1), code: CodeInvalidRequest},
		{name: "unpaired low surrogate", wire: strings.Replace(valid, `"0001"`, `"\udfff"`, 1), code: CodeInvalidRequest},
		{name: "trailing value", wire: valid + `{}`, code: CodeInvalidRequest},
		{name: "invalid utf8", wire: valid + "\xff", code: CodeInvalidRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, failure, failed, err := ReadRequest(strings.NewReader(test.wire))
			want := Failure{Category: CategoryProtocol, Code: test.code}
			if err != nil || !failed || request != (Request{}) || failure != want {
				t.Fatalf("ReadRequest = %+v, %+v, %v, %v, want %+v", request, failure, failed, err, want)
			}
		})
	}

	paired := strings.Replace(valid, `"0001"`, `"\ud83d\ude00"`, 1)
	request, failure, failed, err := ReadRequest(strings.NewReader(paired))
	if err != nil || failed || failure != (Failure{}) || request.Name != "😀" {
		t.Fatalf("paired surrogate = %+v, %+v, %v, %v", request, failure, failed, err)
	}
}

func TestRequestIdentityAndTransportResourceBoundaries(t *testing.T) {
	for _, request := range []Request{
		{},
		{App: "Blog", Name: "0001"},
		{App: "blog", Name: ""},
		{App: "blog", Name: "latest"},
		{App: "blog", Name: "-reverse"},
		{App: strings.Repeat("a", MaxIdentityBytes+1), Name: "0001"},
		{App: "blog", Name: strings.Repeat("n", MaxIdentityBytes+1)},
		{App: "blog", Name: string([]byte{0xff})},
	} {
		if _, err := EncodeRequest(request); err == nil {
			t.Errorf("EncodeRequest(%+v) unexpectedly succeeded", request)
		}
	}

	exact := Request{App: strings.Repeat("a", MaxIdentityBytes), Name: strings.Repeat("n", MaxIdentityBytes)}
	document, err := EncodeRequest(exact)
	if err != nil || len(document) > MaxRequestBytes {
		t.Fatalf("exact identities encoded to %d bytes: %v", len(document), err)
	}
	request, failure, failed, err := ReadRequest(bytes.NewReader(document))
	if err != nil || failed || failure != (Failure{}) || request != exact {
		t.Fatalf("exact identities = app %d name %d failure %+v failed %v err %v", len(request.App), len(request.Name), failure, failed, err)
	}

	readerErr := errors.New("reader failed")
	if _, _, failed, err := ReadRequest(sqlErrorReader{err: readerErr}); failed || !errors.Is(err, readerErr) {
		t.Fatalf("reader error = failed %v err %v", failed, err)
	}
	if _, _, failed, err := ReadRequest(nil); failed || err == nil {
		t.Fatalf("nil reader = failed %v err %v", failed, err)
	}
	if _, _, failed, err := ReadRequest(sqlZeroReader{}); failed || !errors.Is(err, io.ErrNoProgress) {
		t.Fatalf("zero reader = failed %v err %v", failed, err)
	}

	oversized := &sqlTrackingReader{chunks: [][]byte{
		bytes.Repeat([]byte{'x'}, MaxRequestBytes),
		[]byte("overflow"),
		[]byte("tail"),
	}}
	_, failure, failed, err = ReadRequest(oversized)
	if err != nil || !failed || failure != (Failure{Category: CategoryProtocol, Code: CodeInvalidRequest}) || len(oversized.chunks) == 0 {
		t.Fatalf("oversized request = %+v, %v, %v, remaining %d", failure, failed, err, len(oversized.chunks))
	}
}

func TestResponseCanonicalRoundTripOwnershipAndTransportPrecedence(t *testing.T) {
	t.Parallel()
	statements := []string{`CREATE TABLE "article" ("id" integer)`, "ALTER TABLE \"article\"\nADD COLUMN \"title\" text"}
	document, err := EncodeResponse(Response{OK: true, Result: Result{Statements: statements}})
	want := `{"protocol_version":1,"status":"ok","result":{"statements":["CREATE TABLE \"article\" (\"id\" integer)","ALTER TABLE \"article\"\nADD COLUMN \"title\" text"]}}`
	if err != nil || string(document) != want {
		t.Fatalf("success response = %s, %v, want %s", document, err, want)
	}
	response, failure, failed := ParseResponse(document, true)
	if failed || failure != (Failure{}) || !response.OK || !reflect.DeepEqual(response.Result.Statements, statements) {
		t.Fatalf("ParseResponse = %+v, %+v, %v", response, failure, failed)
	}
	response.Result.Statements[0] = "mutated"
	again, failure, failed := ParseResponse(document, true)
	if failed || failure != (Failure{}) || again.Result.Statements[0] != statements[0] {
		t.Fatalf("parsed result retained mutation: %+v, %+v, %v", again, failure, failed)
	}

	empty, err := EncodeResponse(Response{OK: true, Result: Result{Statements: []string{}}})
	if err != nil || string(empty) != `{"protocol_version":1,"status":"ok","result":{"statements":[]}}` {
		t.Fatalf("empty response = %s, %v", empty, err)
	}
	parsedEmpty, failure, failed := ParseResponse(empty, true)
	if failed || failure != (Failure{}) || !parsedEmpty.OK || parsedEmpty.Result.Statements == nil || len(parsedEmpty.Result.Statements) != 0 {
		t.Fatalf("parsed empty = %+v, %+v, %v", parsedEmpty, failure, failed)
	}

	logical := Failure{Category: CategorySQLRender, Code: CodeRenderFailed}
	document, err = EncodeResponse(Response{Failure: logical})
	want = `{"protocol_version":1,"status":"error","error":{"category":"migration_sql_render_error","code":"render_failed","cleanup_failed":false}}`
	if err != nil || string(document) != want {
		t.Fatalf("failure response = %s, %v, want %s", document, err, want)
	}
	response, failure, failed = ParseResponse(document, true)
	if failed || failure != (Failure{}) || response.OK || response.Failure != logical {
		t.Fatalf("logical response = %+v, %+v, %v", response, failure, failed)
	}
	_, failure, failed = ParseResponse(document, false)
	if !failed || failure != (Failure{Category: CategoryProtocol, Code: CodeRunnerFailed}) {
		t.Fatalf("transport precedence = %+v, %v", failure, failed)
	}
}

func TestResponseRejectsMalformedShapeAndStatementSemantics(t *testing.T) {
	t.Parallel()
	valid := []byte(`{"protocol_version":1,"status":"ok","result":{"statements":["SELECT 1"]}}`)
	tests := []struct {
		name string
		wire []byte
		code string
	}{
		{name: "duplicate root", wire: bytes.Replace(valid, []byte(`"status":"ok"`), []byte(`"status":"ok","status":"ok"`), 1), code: CodeInvalidResponse},
		{name: "unknown root", wire: bytes.Replace(valid, []byte(`"result":`), []byte(`"secret":"dsn","result":`), 1), code: CodeInvalidResponse},
		{name: "statements null", wire: []byte(`{"protocol_version":1,"status":"ok","result":{"statements":null}}`), code: CodeInvalidResponse},
		{name: "statement nonstring", wire: []byte(`{"protocol_version":1,"status":"ok","result":{"statements":[1]}}`), code: CodeInvalidResponse},
		{name: "empty statement", wire: []byte(`{"protocol_version":1,"status":"ok","result":{"statements":[""]}}`), code: CodeInvalidResponse},
		{name: "semicolon", wire: []byte(`{"protocol_version":1,"status":"ok","result":{"statements":["SELECT 1;"]}}`), code: CodeInvalidResponse},
		{name: "leading space", wire: []byte(`{"protocol_version":1,"status":"ok","result":{"statements":[" SELECT 1"]}}`), code: CodeInvalidResponse},
		{name: "trailing newline", wire: []byte(`{"protocol_version":1,"status":"ok","result":{"statements":["SELECT 1\n"]}}`), code: CodeInvalidResponse},
		{name: "internal tab", wire: []byte(`{"protocol_version":1,"status":"ok","result":{"statements":["SELECT\t1"]}}`), code: CodeInvalidResponse},
		{name: "unpaired surrogate", wire: []byte(`{"protocol_version":1,"status":"ok","result":{"statements":["\ud800"]}}`), code: CodeInvalidResponse},
		{name: "incompatible", wire: bytes.Replace(valid, []byte(`"protocol_version":1`), []byte(`"protocol_version":2`), 1), code: CodeProtocolIncompatible},
		{name: "trailing", wire: append(append([]byte(nil), valid...), []byte(`{}`)...), code: CodeInvalidResponse},
		{name: "invalid utf8", wire: append(append([]byte(nil), valid...), 0xff), code: CodeInvalidResponse},
		{name: "missing cleanup", wire: []byte(`{"protocol_version":1,"status":"error","error":{"category":"migration_sql_render_error","code":"render_failed"}}`), code: CodeInvalidResponse},
		{name: "unknown taxonomy", wire: []byte(`{"protocol_version":1,"status":"error","error":{"category":"migration_sql_render_error","code":"secret","cleanup_failed":false}}`), code: CodeInvalidResponse},
		{name: "linked cleanup", wire: []byte(`{"protocol_version":1,"status":"error","error":{"category":"migration_sql_render_error","code":"render_failed","cleanup_failed":true}}`), code: CodeInvalidResponse},
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

func TestStatementCountBodyAndEnvelopeResourceBoundaries(t *testing.T) {
	statements := make([]string, MaxStatements)
	for index := range statements {
		statements[index] = "X"
	}
	document, err := EncodeResponse(Response{OK: true, Result: Result{Statements: statements}})
	if err != nil {
		t.Fatalf("MaxStatements encode = %v", err)
	}
	response, failure, failed := ParseResponse(document, true)
	if failed || failure != (Failure{}) || len(response.Result.Statements) != MaxStatements {
		t.Fatalf("MaxStatements parse = %d, %+v, %v", len(response.Result.Statements), failure, failed)
	}
	tooMany := append(append([]string(nil), statements...), "X")
	if _, err := EncodeResponse(Response{OK: true, Result: Result{Statements: tooMany}}); err == nil {
		t.Fatal("MaxStatements+1 encoded")
	}

	exactBody := strings.Repeat("X", MaxStatementBodyBytes)
	document, err = EncodeResponse(Response{OK: true, Result: Result{Statements: []string{exactBody}}})
	if err != nil {
		t.Fatalf("exact body encode = %v", err)
	}
	response, failure, failed = ParseResponse(document, true)
	if failed || failure != (Failure{}) || len(response.Result.Statements) != 1 || len(response.Result.Statements[0]) != MaxStatementBodyBytes {
		t.Fatalf("exact body parse = %d, %+v, %v", len(response.Result.Statements), failure, failed)
	}
	if _, err := EncodeResponse(Response{OK: true, Result: Result{Statements: []string{exactBody + "X"}}}); err == nil {
		t.Fatal("body one-over encoded")
	}

	resourceBeforeSemantic := []byte(`{"protocol_version":1,"status":"ok","result":{"statements":[";","` + exactBody + `"]}}`)
	_, failure, failed = ParseResponse(resourceBeforeSemantic, true)
	if !failed || failure != (Failure{Category: CategorySQLResource, Code: CodeRenderedSQLResourceLimit}) {
		t.Fatalf("resource precedence = %+v, %v", failure, failed)
	}

	exactEnvelope := bytes.Repeat([]byte{' '}, MaxResponseBytes)
	_, failure, failed = ParseResponse(exactEnvelope, true)
	if !failed || failure != (Failure{Category: CategoryProtocol, Code: CodeInvalidResponse}) {
		t.Fatalf("exact malformed envelope = %+v, %v", failure, failed)
	}
	_, failure, failed = ParseResponse(append(exactEnvelope, ' '), true)
	if !failed || failure != (Failure{Category: CategorySQLResource, Code: CodeRenderedSQLResourceLimit}) {
		t.Fatalf("one-over envelope = %+v, %v", failure, failed)
	}
}

func TestWorstCaseEscapingAndMaximumStatementFramingFitPrivateEnvelope(t *testing.T) {
	statements := make([]string, MaxStatements)
	statements[0] = strings.Repeat("<", MaxStatementBodyBytes-MaxStatements+1)
	for index := 1; index < len(statements); index++ {
		statements[index] = "<"
	}
	document, err := EncodeResponse(Response{OK: true, Result: Result{Statements: statements}})
	if err != nil {
		t.Fatalf("worst-case private response encode = %v", err)
	}
	staticEmpty := len(`{"protocol_version":1,"status":"ok","result":{"statements":[]}}`)
	wantBytes := staticEmpty + 6*MaxStatementBodyBytes + 3*MaxStatements - 1
	if len(document) != wantBytes || wantBytes > MaxResponseBytes {
		t.Fatalf("worst-case private response = %d bytes, derived %d, cap %d", len(document), wantBytes, MaxResponseBytes)
	}
	response, failure, failed := ParseResponse(document, true)
	if failed || failure != (Failure{}) || !response.OK || len(response.Result.Statements) != MaxStatements {
		t.Fatalf("worst-case private response parse = statements %d failure %+v failed %v", len(response.Result.Statements), failure, failed)
	}
	for index, statement := range response.Result.Statements {
		if len(statement) == 0 || strings.Trim(statement, "<") != "" {
			t.Fatalf("statement %d did not round-trip", index)
		}
	}
}

func TestEncodeValidateTaxonomyAndSingleWrite(t *testing.T) {
	t.Parallel()
	invalid := []Response{
		{},
		{OK: true},
		{OK: true, Result: Result{Statements: []string{"SELECT 1"}}, Failure: Failure{Category: CategorySQLRender, Code: CodeRenderFailed}},
		{Result: Result{Statements: []string{}}, Failure: Failure{Category: CategorySQLRender, Code: CodeRenderFailed}},
		{Failure: Failure{Category: CategoryCommand, Code: CodeInvalidArguments}},
		{Failure: Failure{Category: CategorySQLRender, Code: CodeRenderFailed, CleanupFailed: true}},
	}
	for _, response := range invalid {
		if _, err := EncodeResponse(response); err == nil {
			t.Errorf("EncodeResponse(%+v) unexpectedly succeeded", response)
		}
	}
	if err := ValidateResult(Result{Statements: []string{}}); err != nil {
		t.Fatalf("empty result rejected: %v", err)
	}
	if err := ValidateResult(Result{}); err == nil {
		t.Fatal("nil result accepted")
	}

	writer := &sqlCountingWriter{}
	response := Response{OK: true, Result: Result{Statements: []string{"SELECT 1"}}}
	if err := WriteResponse(writer, response); err != nil || writer.calls != 1 {
		t.Fatalf("WriteResponse = calls %d err %v", writer.calls, err)
	}
	if err := WriteResponse(sqlShortWriter{}, response); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short writer = %v", err)
	}
	wantErr := errors.New("write failed")
	if err := WriteResponse(sqlErrorWriter{err: wantErr}, response); !errors.Is(err, wantErr) {
		t.Fatalf("error writer = %v", err)
	}
	if err := WriteResponse(nil, response); err == nil {
		t.Fatal("nil writer accepted")
	}

	taxonomy := []struct {
		failure Failure
		exit    int
		linked  bool
	}{
		{Failure{Category: CategoryCommand, Code: CodeInvalidArguments}, 2, false},
		{Failure{Category: CategoryProtocol, Code: CodeInvalidRequest}, 3, true},
		{Failure{Category: CategorySource, Code: "invalid_definition_document"}, 1, true},
		{Failure{Category: CategoryGraph, Code: "dependency_cycle"}, 1, true},
		{Failure{Category: CategoryState, Code: "invalid_state"}, 1, true},
		{Failure{Category: CategoryCapability, Code: "unsupported_operation"}, 1, true},
		{Failure{Category: CategoryExecution, Code: "operation_failed"}, 3, true},
		{Failure{Category: CategoryPlan, Code: "target_not_found"}, 1, true},
		{Failure{Category: CategorySQLRender, Code: CodeRenderFailed}, 3, true},
		{Failure{Category: CategorySQLResource, Code: CodeRenderedSQLResourceLimit}, 1, true},
		{Failure{Category: CategoryProcess, Code: CodeProjectInterrupted}, 130, false},
	}
	for _, test := range taxonomy {
		exit, ok := ExitCode(test.failure)
		if !ok || exit != test.exit || IsLinkedFailure(test.failure) != test.linked {
			t.Errorf("taxonomy %+v = exit %d/%v linked %v", test.failure, exit, ok, IsLinkedFailure(test.failure))
		}
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
		_, _, _, _ = ReadRequest(bytes.NewReader(document))
	}
}

type sqlErrorReader struct{ err error }

func (reader sqlErrorReader) Read([]byte) (int, error) { return 0, reader.err }

type sqlZeroReader struct{}

func (sqlZeroReader) Read([]byte) (int, error) { return 0, nil }

type sqlTrackingReader struct {
	chunks [][]byte
}

func (reader *sqlTrackingReader) Read(output []byte) (int, error) {
	if len(reader.chunks) == 0 {
		return 0, io.EOF
	}
	chunk := reader.chunks[0]
	read := copy(output, chunk)
	if read == len(chunk) {
		reader.chunks = reader.chunks[1:]
	} else {
		reader.chunks[0] = chunk[read:]
	}
	return read, nil
}

type sqlCountingWriter struct {
	bytes.Buffer
	calls int
}

func (writer *sqlCountingWriter) Write(payload []byte) (int, error) {
	writer.calls++
	return writer.Buffer.Write(payload)
}

type sqlShortWriter struct{}

func (sqlShortWriter) Write(payload []byte) (int, error) { return len(payload) - 1, nil }

type sqlErrorWriter struct{ err error }

func (writer sqlErrorWriter) Write([]byte) (int, error) { return 0, writer.err }
