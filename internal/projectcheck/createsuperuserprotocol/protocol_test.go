package createsuperuserprotocol

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestRequestCanonicalRoundTripDetachedAndClear(t *testing.T) {
	t.Parallel()
	request := Request{
		Username: []byte("operator-한"),
		Password: []byte("  pass\tword-비밀  "),
	}
	wantUsername := append([]byte(nil), request.Username...)
	wantPassword := append([]byte(nil), request.Password...)
	document, err := EncodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(document[:len(Magic)]), Magic; got != want {
		t.Fatalf("magic = %q, want %q", got, want)
	}
	if len(document) != requestHeaderBytes+len(request.Username)+len(request.Password) {
		t.Fatalf("frame length = %d", len(document))
	}

	request.Username[0] = '!'
	request.Password[0] = '!'
	decoded, failure, failed := DecodeRequest(document)
	if failed || failure != (Failure{}) || !bytes.Equal(decoded.Username, wantUsername) || !bytes.Equal(decoded.Password, wantPassword) {
		t.Fatalf("DecodeRequest = username %q password length %d failure %+v failed %v", decoded.Username, len(decoded.Password), failure, failed)
	}

	document[requestHeaderBytes] = '?'
	if !bytes.Equal(decoded.Username, wantUsername) || !bytes.Equal(decoded.Password, wantPassword) {
		t.Fatal("decoded request retained frame storage")
	}
	decoded.Username[0] = '#'
	againDocument := mustEncodeRequest(t, Request{Username: wantUsername, Password: wantPassword})
	defer clear(againDocument)
	again, failure, failed := DecodeRequest(againDocument)
	if failed || failure != (Failure{}) || !bytes.Equal(again.Username, wantUsername) || !bytes.Equal(again.Password, wantPassword) {
		t.Fatal("decoded request retained another decode's storage")
	}

	ownedUsername := decoded.Username
	ownedPassword := decoded.Password
	decoded.Clear()
	if decoded.Username != nil || decoded.Password != nil {
		t.Fatalf("Clear retained request slices: %+v", decoded)
	}
	if !allZero(ownedUsername) || !allZero(ownedPassword) {
		t.Fatal("Clear did not clear request-owned backing bytes")
	}
	var nilRequest *Request
	nilRequest.Clear()
	again.Clear()
}

func TestRequestExactLimitsAndOneOver(t *testing.T) {
	t.Parallel()
	exact := Request{
		Username: bytes.Repeat([]byte{'u'}, MaxUsernameBytes),
		Password: bytes.Repeat([]byte{'p'}, MaxPasswordBytes),
	}
	document, err := EncodeRequest(exact)
	if err != nil || len(document) != MaxRequestBytes {
		t.Fatalf("exact frame = %d bytes, %v", len(document), err)
	}
	decoded, failure, failed := DecodeRequest(document)
	if failed || failure != (Failure{}) || !reflect.DeepEqual(decoded, exact) {
		t.Fatalf("exact decode = username %d password %d failure %+v failed %v", len(decoded.Username), len(decoded.Password), failure, failed)
	}
	decoded.Clear()

	invalid := []Request{
		{Username: bytes.Repeat([]byte{'u'}, MaxUsernameBytes+1), Password: []byte("p")},
		{Username: []byte("u"), Password: bytes.Repeat([]byte{'p'}, MaxPasswordBytes+1)},
	}
	for _, request := range invalid {
		if document, err := EncodeRequest(request); err == nil || document != nil {
			clear(document)
			t.Fatalf("over-limit request encoded: username %d password %d err %v", len(request.Username), len(request.Password), err)
		}
	}

	overallOneOver := append(append([]byte(nil), document...), 'x')
	assertRequestFailure(t, overallOneOver, CodeInvalidRequest)
	clear(overallOneOver)
	clear(document)
}

func TestRequestRejectsEveryTruncationTrailingByteAndVersionMismatch(t *testing.T) {
	t.Parallel()
	document := mustEncodeRequest(t, Request{Username: []byte("operator"), Password: []byte("password")})
	defer clear(document)
	for length := 0; length < len(document); length++ {
		assertRequestFailure(t, document[:length], CodeInvalidRequest)
	}
	for _, tail := range [][]byte{{0}, {'x'}, []byte("GODJCSU1")} {
		trailing := append(append([]byte(nil), document...), tail...)
		assertRequestFailure(t, trailing, CodeInvalidRequest)
		clear(trailing)
	}

	incompatible := append([]byte(nil), document...)
	incompatible[len(Magic)-1] = '2'
	assertRequestFailure(t, incompatible, CodeProtocolIncompatible)
	clear(incompatible)

	for _, index := range []int{0, 3, len(Magic) - 2} {
		malformed := append([]byte(nil), document...)
		malformed[index] ^= 1
		assertRequestFailure(t, malformed, CodeInvalidRequest)
		clear(malformed)
	}

	lengthMismatch := append([]byte(nil), document...)
	lengthMismatch[len(Magic)+1]++
	assertRequestFailure(t, lengthMismatch, CodeInvalidRequest)
	clear(lengthMismatch)

	for _, frame := range [][]byte{
		rawFrame(nil, []byte("password")),
		rawFrame([]byte("operator"), nil),
		rawFrame(bytes.Repeat([]byte{'u'}, MaxUsernameBytes+1), []byte("password")),
		rawFrame([]byte("operator"), bytes.Repeat([]byte{'p'}, MaxPasswordBytes+1)),
	} {
		assertRequestFailure(t, frame, CodeInvalidRequest)
		clear(frame)
	}
}

func TestUsernameValidationUTF8TrimAndControls(t *testing.T) {
	t.Parallel()
	valid := [][]byte{
		[]byte("a"),
		[]byte("사용자"),
		[]byte("operator name"),
		[]byte("operator\u00a0name"),
	}
	for _, username := range valid {
		document, err := EncodeRequest(Request{Username: username, Password: []byte("password")})
		if err != nil {
			t.Fatalf("valid username %q: %v", username, err)
		}
		clear(document)
	}

	invalid := [][]byte{
		nil,
		{},
		[]byte(" operator"),
		[]byte("operator "),
		[]byte("\u00a0operator"),
		[]byte("operator\u2003"),
		{0xff},
	}
	for character := 0; character < 0x20; character++ {
		invalid = append(invalid, []byte{'a', byte(character), 'b'})
	}
	invalid = append(invalid, []byte{'a', 0x7f, 'b'})
	for _, username := range invalid {
		assertEncodeRequestErrorRedacted(t, Request{Username: username, Password: []byte("password")})
	}

	for _, username := range [][]byte{{0xff}, []byte("bad\x00name"), []byte(" bad")} {
		frame := rawFrame(username, []byte("password"))
		assertRequestFailure(t, frame, CodeInvalidRequest)
		clear(frame)
	}
}

func TestPasswordValidationUTF8WhitespaceControlsAndPreservation(t *testing.T) {
	t.Parallel()
	valid := [][]byte{
		[]byte("p"),
		[]byte(" 비밀 "),
		[]byte("\tpassword\v"),
		[]byte("pässword"),
		{'p', 0x01, 'q'},
	}
	for _, password := range valid {
		document := mustEncodeRequest(t, Request{Username: []byte("operator"), Password: password})
		decoded, failure, failed := DecodeRequest(document)
		if failed || failure != (Failure{}) || !bytes.Equal(decoded.Password, password) {
			t.Fatalf("password round trip length %d = failure %+v failed %v", len(password), failure, failed)
		}
		decoded.Clear()
		clear(document)
	}

	invalid := [][]byte{
		nil,
		{},
		{0xff},
		[]byte("bad\x00password"),
		[]byte("bad\rpassword"),
		[]byte("bad\npassword"),
		[]byte(" "),
		[]byte("\t\v\f "),
		[]byte("\u00a0\u2003"),
	}
	for _, password := range invalid {
		assertEncodeRequestErrorRedacted(t, Request{Username: []byte("operator"), Password: password})
		frame := rawFrame([]byte("operator"), password)
		assertRequestFailure(t, frame, CodeInvalidRequest)
		clear(frame)
	}
}

func TestExportedInputValidatorsUseTheWireContract(t *testing.T) {
	if !ValidUsername([]byte("operator")) || ValidUsername([]byte(" operator")) {
		t.Fatal("ValidUsername diverged from canonical request validation")
	}
	if !ValidPassword([]byte("  password  ")) || ValidPassword([]byte(" \t ")) {
		t.Fatal("ValidPassword diverged from canonical request validation")
	}
}

func TestReadRequestExactEOFTransportRedactionAndTemporaryClear(t *testing.T) {
	t.Parallel()
	document := mustEncodeRequest(t, Request{Username: []byte("operator-marker"), Password: []byte("password-marker")})
	defer clear(document)

	request, failure, failed, err := ReadRequest(bytes.NewReader(document))
	if err != nil || failed || failure != (Failure{}) || !bytes.Equal(request.Password, []byte("password-marker")) {
		t.Fatalf("ReadRequest = %+v, %+v, %v, %v", request, failure, failed, err)
	}
	request.Clear()

	reader := &retainingReader{document: append([]byte(nil), document...)}
	request, failure, failed, err = ReadRequest(reader)
	if err != nil || failed || failure != (Failure{}) {
		t.Fatalf("retaining ReadRequest = %+v, %+v, %v, %v", request, failure, failed, err)
	}
	for _, character := range reader.borrowed {
		if character != 0 {
			t.Fatal("package-owned read buffer was not cleared")
		}
	}
	request.Clear()
	clear(reader.document)

	var typedNilReaderError *requestPanickingIsError
	for _, test := range []struct {
		name   string
		reader io.Reader
	}{
		{name: "nil", reader: nil},
		{name: "transport", reader: &requestErrorReader{err: errors.New("password-marker")}},
		{name: "typed nil transport", reader: &requestErrorReader{err: typedNilReaderError}},
		{name: "panicking Is transport", reader: &requestErrorReader{err: requestPanickingIsError{}}},
		{name: "panicking Unwrap transport", reader: &requestErrorReader{err: requestPanickingUnwrapError{}}},
		{name: "no progress", reader: requestZeroReader{}},
		{name: "invalid count", reader: requestInvalidCountReader{}},
		{name: "missing eof", reader: &frameThenErrorReader{document: document, err: errors.New("operator-marker")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, failure, failed, err := ReadRequest(test.reader)
			if err == nil || failed || failure != (Failure{}) || request.Username != nil || request.Password != nil {
				t.Fatalf("ReadRequest = %+v, %+v, %v, %v", request, failure, failed, err)
			}
			if strings.Contains(err.Error(), "operator-marker") || strings.Contains(err.Error(), "password-marker") {
				t.Fatalf("transport error leaked a request value: %v", err)
			}
		})
	}

	exactMaximum := mustEncodeRequest(t, Request{
		Username: bytes.Repeat([]byte{'u'}, MaxUsernameBytes),
		Password: bytes.Repeat([]byte{'p'}, MaxPasswordBytes),
	})
	defer clear(exactMaximum)
	oversized := append(append([]byte(nil), exactMaximum...), 'x')
	request, failure, failed, err = ReadRequest(bytes.NewReader(oversized))
	if err != nil || !failed || request.Username != nil || request.Password != nil || failure != (Failure{Category: CategoryProtocol, Code: CodeInvalidRequest}) {
		t.Fatalf("oversized read = %+v, %+v, %v, %v", request, failure, failed, err)
	}
	clear(oversized)
}

func TestRequestFormattingAndErrorsAreRedacted(t *testing.T) {
	t.Parallel()
	username := []byte("unique-username-marker")
	password := []byte("unique-password-marker")
	request := Request{Username: username, Password: password}
	for _, rendered := range []string{fmt.Sprint(request), fmt.Sprintf("%v", request), fmt.Sprintf("%+v", request), fmt.Sprintf("%#v", request)} {
		if rendered != "createsuperuserprotocol.Request{redacted}" || strings.Contains(rendered, string(username)) || strings.Contains(rendered, string(password)) {
			t.Fatalf("request formatting leaked: %q", rendered)
		}
	}

	invalid := Request{Username: username, Password: []byte(" \t ")}
	_, err := EncodeRequest(invalid)
	if err == nil || strings.Contains(err.Error(), string(username)) || strings.Contains(err.Error(), "password") && strings.Contains(err.Error(), "marker") {
		t.Fatalf("invalid request error was not redacted: %v", err)
	}
}

func TestResponseCanonicalRoundTripAndKnownCreatedShape(t *testing.T) {
	t.Parallel()
	success := Response{OK: true, Created: true}
	document, err := EncodeResponse(success)
	want := `{"protocol_version":1,"status":"ok","result":{"created":true}}`
	if err != nil || string(document) != want {
		t.Fatalf("success response = %s, %v, want %s", document, err, want)
	}
	parsed, failure, failed := ParseResponse(document, true)
	if failed || failure != (Failure{}) || parsed != success {
		t.Fatalf("ParseResponse = %+v, %+v, %v", parsed, failure, failed)
	}

	logical := Failure{Category: CategoryState, Code: CodeCredentialAlreadyExists}
	document, err = EncodeResponse(Response{Failure: logical})
	want = `{"protocol_version":1,"status":"error","error":{"category":"system_state_error","code":"credential_already_exists"}}`
	if err != nil || string(document) != want {
		t.Fatalf("logical response = %s, %v, want %s", document, err, want)
	}
	parsed, failure, failed = ParseResponse(document, true)
	if failed || failure != (Failure{}) || parsed != (Response{Failure: logical}) {
		t.Fatalf("logical parse = %+v, %+v, %v", parsed, failure, failed)
	}

	known := Failure{Category: CategoryBackend, Code: CodeBackendCloseFailed, KnownCreated: true}
	document, err = EncodeResponse(Response{Failure: known})
	want = `{"protocol_version":1,"status":"error","error":{"category":"system_state_backend_error","code":"backend_close_failed","known_created":true}}`
	if err != nil || string(document) != want {
		t.Fatalf("known-created response = %s, %v, want %s", document, err, want)
	}
	parsed, failure, failed = ParseResponse(document, true)
	if failed || failure != (Failure{}) || parsed != (Response{Failure: known}) {
		t.Fatalf("known-created parse = %+v, %+v, %v", parsed, failure, failed)
	}

	_, failure, failed = ParseResponse(document, false)
	if !failed || failure != (Failure{Category: CategoryProtocol, Code: CodeRunnerFailed}) {
		t.Fatalf("transport precedence = %+v, %v", failure, failed)
	}
}

func TestResponseRejectsDuplicateUnknownMissingTrailingOversizeAndInvalidUnion(t *testing.T) {
	t.Parallel()
	valid := []byte(`{"protocol_version":1,"status":"ok","result":{"created":true}}`)
	tests := []struct {
		name string
		wire []byte
		code string
	}{
		{name: "empty", wire: nil, code: CodeInvalidResponse},
		{name: "duplicate root", wire: []byte(`{"protocol_version":1,"status":"ok","status":"ok","result":{"created":true}}`), code: CodeInvalidResponse},
		{name: "duplicate result", wire: []byte(`{"protocol_version":1,"status":"ok","result":{"created":true,"created":true}}`), code: CodeInvalidResponse},
		{name: "duplicate error", wire: []byte(`{"protocol_version":1,"status":"error","error":{"category":"system_state_error","code":"corrupt_state","code":"corrupt_state"}}`), code: CodeInvalidResponse},
		{name: "unknown root", wire: []byte(`{"protocol_version":1,"status":"ok","secret":"marker","result":{"created":true}}`), code: CodeInvalidResponse},
		{name: "unknown result", wire: []byte(`{"protocol_version":1,"status":"ok","result":{"created":true,"secret":"marker"}}`), code: CodeInvalidResponse},
		{name: "unknown error", wire: []byte(`{"protocol_version":1,"status":"error","error":{"category":"system_state_error","code":"corrupt_state","message":"marker"}}`), code: CodeInvalidResponse},
		{name: "missing version", wire: []byte(`{"status":"ok","result":{"created":true}}`), code: CodeInvalidResponse},
		{name: "missing status", wire: []byte(`{"protocol_version":1,"result":{"created":true}}`), code: CodeInvalidResponse},
		{name: "missing result", wire: []byte(`{"protocol_version":1,"status":"ok"}`), code: CodeInvalidResponse},
		{name: "missing created", wire: []byte(`{"protocol_version":1,"status":"ok","result":{}}`), code: CodeInvalidResponse},
		{name: "missing category", wire: []byte(`{"protocol_version":1,"status":"error","error":{"code":"corrupt_state"}}`), code: CodeInvalidResponse},
		{name: "missing code", wire: []byte(`{"protocol_version":1,"status":"error","error":{"category":"system_state_error"}}`), code: CodeInvalidResponse},
		{name: "trailing json", wire: append(append([]byte(nil), valid...), []byte(`{}`)...), code: CodeInvalidResponse},
		{name: "trailing text", wire: append(append([]byte(nil), valid...), 'x'), code: CodeInvalidResponse},
		{name: "invalid utf8", wire: append(append([]byte(nil), valid...), 0xff), code: CodeInvalidResponse},
		{name: "unpaired surrogate", wire: []byte(`{"protocol_version":1,"status":"error","error":{"category":"system_state_error","code":"\ud800"}}`), code: CodeInvalidResponse},
		{name: "decimal version", wire: bytes.Replace(valid, []byte(`"protocol_version":1`), []byte(`"protocol_version":1.0`), 1), code: CodeInvalidResponse},
		{name: "exponent version", wire: bytes.Replace(valid, []byte(`"protocol_version":1`), []byte(`"protocol_version":1e0`), 1), code: CodeInvalidResponse},
		{name: "string version", wire: bytes.Replace(valid, []byte(`"protocol_version":1`), []byte(`"protocol_version":"1"`), 1), code: CodeInvalidResponse},
		{name: "incompatible version", wire: bytes.Replace(valid, []byte(`"protocol_version":1`), []byte(`"protocol_version":2`), 1), code: CodeProtocolIncompatible},
		{name: "unknown status", wire: bytes.Replace(valid, []byte(`"status":"ok"`), []byte(`"status":"partial"`), 1), code: CodeInvalidResponse},
		{name: "created false", wire: bytes.Replace(valid, []byte(`"created":true`), []byte(`"created":false`), 1), code: CodeInvalidResponse},
		{name: "created number", wire: bytes.Replace(valid, []byte(`"created":true`), []byte(`"created":1`), 1), code: CodeInvalidResponse},
		{name: "mixed union", wire: []byte(`{"protocol_version":1,"status":"ok","result":{"created":true},"error":{"category":"system_state_error","code":"corrupt_state"}}`), code: CodeInvalidResponse},
		{name: "error result", wire: []byte(`{"protocol_version":1,"status":"error","result":{"created":true},"error":{"category":"system_state_error","code":"corrupt_state"}}`), code: CodeInvalidResponse},
		{name: "known false", wire: []byte(`{"protocol_version":1,"status":"error","error":{"category":"system_state_backend_error","code":"backend_close_failed","known_created":false}}`), code: CodeInvalidResponse},
		{name: "known nonbool", wire: []byte(`{"protocol_version":1,"status":"error","error":{"category":"system_state_backend_error","code":"backend_close_failed","known_created":1}}`), code: CodeInvalidResponse},
		{name: "known before open", wire: []byte(`{"protocol_version":1,"status":"error","error":{"category":"system_state_backend_error","code":"backend_open_failed","known_created":true}}`), code: CodeInvalidResponse},
		{name: "known state failure", wire: []byte(`{"protocol_version":1,"status":"error","error":{"category":"system_state_error","code":"persistence_failure","known_created":true}}`), code: CodeInvalidResponse},
		{name: "credential absent", wire: []byte(`{"protocol_version":1,"status":"error","error":{"category":"system_state_error","code":"credential_absent"}}`), code: CodeInvalidResponse},
		{name: "unknown category", wire: []byte(`{"protocol_version":1,"status":"error","error":{"category":"raw_error","code":"corrupt_state"}}`), code: CodeInvalidResponse},
		{name: "unknown code", wire: []byte(`{"protocol_version":1,"status":"error","error":{"category":"system_state_error","code":"secret-marker"}}`), code: CodeInvalidResponse},
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

	exact := append(append([]byte(nil), valid...), bytes.Repeat([]byte{' '}, MaxResponseBytes-len(valid))...)
	response, failure, failed := ParseResponse(exact, true)
	if failed || failure != (Failure{}) || response != (Response{OK: true, Created: true}) {
		t.Fatalf("exact max response = %+v, %+v, %v", response, failure, failed)
	}
	over := append(append([]byte(nil), exact...), ' ')
	_, failure, failed = ParseResponse(over, true)
	if !failed || failure != (Failure{Category: CategoryProtocol, Code: CodeInvalidResponse}) {
		t.Fatalf("over max response = %+v, %v", failure, failed)
	}
}

func TestEncodeResponseRejectsInvalidAmbiguousAndUntrustedFailures(t *testing.T) {
	t.Parallel()
	invalid := []Response{
		{},
		{OK: true},
		{OK: true, Created: false},
		{OK: true, Created: true, Failure: Failure{Category: CategoryState, Code: CodeCorruptState}},
		{Created: true, Failure: Failure{Category: CategoryState, Code: CodeCorruptState}},
		{Failure: Failure{Category: CategoryState, Code: "credential_absent"}},
		{Failure: Failure{Category: CategoryState, Code: CodePersistenceFailure, KnownCreated: true}},
		{Failure: Failure{Category: CategoryBackend, Code: CodeBackendOpenFailed, KnownCreated: true}},
		{Failure: Failure{Category: CategoryProtocol, Code: CodeRunnerFailed}},
		{Failure: Failure{Category: "raw_error", Code: "secret-marker"}},
	}
	for _, response := range invalid {
		document, err := EncodeResponse(response)
		if err == nil || document != nil || strings.Contains(err.Error(), "secret-marker") {
			clear(document)
			t.Fatalf("EncodeResponse(%+v) = %q, %v", response, document, err)
		}
	}
}

func TestWriteResponseOneWriteShortWriteAndRedactedWriterFailure(t *testing.T) {
	t.Parallel()
	response := Response{OK: true, Created: true}
	var output bytes.Buffer
	if err := WriteResponse(&output, response); err != nil {
		t.Fatal(err)
	}
	if output.String() != `{"protocol_version":1,"status":"ok","result":{"created":true}}` {
		t.Fatalf("WriteResponse = %q", output.String())
	}
	if err := WriteResponse(nil, response); err == nil {
		t.Fatal("nil writer succeeded")
	}
	if err := WriteResponse(shortResponseWriter{}, response); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short writer error = %v", err)
	}
	err := WriteResponse(errorResponseWriter{err: errors.New("secret-writer-marker")}, response)
	if err == nil || strings.Contains(err.Error(), "secret-writer-marker") {
		t.Fatalf("writer cause leaked: %v", err)
	}
}

func assertRequestFailure(t *testing.T, document []byte, code string) {
	t.Helper()
	request, failure, failed := DecodeRequest(document)
	want := Failure{Category: CategoryProtocol, Code: code}
	if !failed || failure != want || request.Username != nil || request.Password != nil {
		request.Clear()
		t.Fatalf("DecodeRequest(%d bytes) = %+v, %+v, %v, want %+v", len(document), request, failure, failed, want)
	}
}

func assertEncodeRequestErrorRedacted(t *testing.T, request Request) {
	t.Helper()
	document, err := EncodeRequest(request)
	if err == nil || document != nil {
		clear(document)
		t.Fatalf("invalid request encoded: username %d password %d err %v", len(request.Username), len(request.Password), err)
	}
	if len(request.Username) > 3 && bytes.Contains([]byte(err.Error()), request.Username) {
		t.Fatalf("username leaked in error: %v", err)
	}
	if len(request.Password) > 3 && bytes.Contains([]byte(err.Error()), request.Password) {
		t.Fatalf("password leaked in error: %v", err)
	}
}

func mustEncodeRequest(t *testing.T, request Request) []byte {
	t.Helper()
	document, err := EncodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func rawFrame(username, password []byte) []byte {
	document := make([]byte, requestHeaderBytes+len(username)+len(password))
	copy(document, Magic)
	document[len(Magic)] = byte(len(username) >> 8)
	document[len(Magic)+1] = byte(len(username))
	document[len(Magic)+2] = byte(len(password) >> 8)
	document[len(Magic)+3] = byte(len(password))
	copy(document[requestHeaderBytes:], username)
	copy(document[requestHeaderBytes+len(username):], password)
	return document
}

func allZero(value []byte) bool {
	for _, character := range value {
		if character != 0 {
			return false
		}
	}
	return true
}

type retainingReader struct {
	document []byte
	borrowed []byte
	done     bool
}

func (reader *retainingReader) Read(target []byte) (int, error) {
	if reader.done {
		return 0, io.EOF
	}
	reader.done = true
	read := copy(target, reader.document)
	reader.borrowed = target[:read]
	return read, nil
}

type requestErrorReader struct{ err error }

func (reader *requestErrorReader) Read([]byte) (int, error) { return 0, reader.err }

type requestPanickingIsError struct{}

func (requestPanickingIsError) Error() string { return "request reader failure" }

func (requestPanickingIsError) Is(error) bool { panic("request reader Is must be contained") }

type requestPanickingUnwrapError struct{}

func (requestPanickingUnwrapError) Error() string { return "request reader failure" }

func (requestPanickingUnwrapError) Unwrap() error { panic("request reader Unwrap must be contained") }

type requestZeroReader struct{}

func (requestZeroReader) Read([]byte) (int, error) { return 0, nil }

type requestInvalidCountReader struct{}

func (requestInvalidCountReader) Read(target []byte) (int, error) { return len(target) + 1, nil }

type frameThenErrorReader struct {
	document []byte
	err      error
	done     bool
}

func (reader *frameThenErrorReader) Read(target []byte) (int, error) {
	if reader.done {
		return 0, reader.err
	}
	reader.done = true
	return copy(target, reader.document), nil
}

type shortResponseWriter struct{}

func (shortResponseWriter) Write(document []byte) (int, error) { return len(document) - 1, nil }

type errorResponseWriter struct{ err error }

func (writer errorResponseWriter) Write([]byte) (int, error) { return 0, writer.err }
