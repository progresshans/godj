package createsuperuserprotocol

import (
	"bytes"
	"testing"
)

func FuzzDecodeRequest(f *testing.F) {
	for _, document := range [][]byte{
		mustSeedRequest(Request{Username: []byte("operator"), Password: []byte("password")}),
		mustSeedRequest(Request{Username: []byte("사용자"), Password: []byte("  비밀  ")}),
		mustSeedRequest(Request{Username: bytes.Repeat([]byte{'u'}, MaxUsernameBytes), Password: bytes.Repeat([]byte{'p'}, MaxPasswordBytes)}),
		{},
		[]byte(Magic),
		[]byte("GODJCSU2\x00\x01\x00\x01up"),
		[]byte("GODJCSU1\x00\x01\x00\x01upx"),
		[]byte("GODJCSU1\x00\x01\x00\x01\xffp"),
	} {
		f.Add(document)
	}

	f.Fuzz(func(t *testing.T, document []byte) {
		request, failure, failed := DecodeRequest(document)
		defer request.Clear()
		if failed {
			if request.Username != nil || request.Password != nil || failure.Category != CategoryProtocol ||
				(failure.Code != CodeInvalidRequest && failure.Code != CodeProtocolIncompatible) {
				t.Fatalf("invalid failure shape: request %+v failure %+v", request, failure)
			}
			return
		}
		if failure != (Failure{}) {
			t.Fatalf("accepted request returned failure %+v", failure)
		}
		canonical, err := EncodeRequest(request)
		if err != nil {
			t.Fatalf("accepted request did not encode: %v", err)
		}
		defer clear(canonical)
		if !bytes.Equal(canonical, document) {
			t.Fatalf("accepted frame was not canonical: got %d want %d bytes", len(canonical), len(document))
		}
	})
}

func FuzzEncodeDecodeRequest(f *testing.F) {
	f.Add([]byte("operator"), []byte("password"))
	f.Add([]byte("사용자"), []byte("  비밀  "))
	f.Add(bytes.Repeat([]byte{'u'}, MaxUsernameBytes), bytes.Repeat([]byte{'p'}, MaxPasswordBytes))
	f.Add([]byte(" user"), []byte("password"))
	f.Add([]byte("operator"), []byte(" \t\u00a0 "))
	f.Add([]byte{0xff}, []byte("password"))

	f.Fuzz(func(t *testing.T, username, password []byte) {
		request := Request{
			Username: append([]byte(nil), username...),
			Password: append([]byte(nil), password...),
		}
		defer request.Clear()
		document, err := EncodeRequest(request)
		if err != nil {
			if document != nil {
				clear(document)
				t.Fatal("failed encode returned request bytes")
			}
			return
		}
		defer clear(document)
		decoded, failure, failed := DecodeRequest(document)
		defer decoded.Clear()
		if failed || failure != (Failure{}) || !bytes.Equal(decoded.Username, username) || !bytes.Equal(decoded.Password, password) {
			t.Fatalf("round trip = username %d password %d failure %+v failed %v", len(decoded.Username), len(decoded.Password), failure, failed)
		}
	})
}

func FuzzParseResponse(f *testing.F) {
	for _, document := range [][]byte{
		[]byte(`{"protocol_version":1,"status":"ok","result":{"created":true}}`),
		[]byte(`{"protocol_version":1,"status":"error","error":{"category":"system_state_error","code":"credential_already_exists"}}`),
		[]byte(`{"protocol_version":1,"status":"error","error":{"category":"system_state_backend_error","code":"backend_close_failed","known_created":true}}`),
		[]byte(`{"protocol_version":1,"status":"ok","status":"ok","result":{"created":true}}`),
		[]byte(`{"protocol_version":2,"status":"ok","result":{"created":true}}`),
		{},
		bytes.Repeat([]byte{'x'}, MaxResponseBytes+1),
	} {
		f.Add(document, true)
	}
	f.Add([]byte(`{"protocol_version":1,"status":"ok","result":{"created":true}}`), false)

	f.Fuzz(func(t *testing.T, document []byte, transportOK bool) {
		response, failure, failed := ParseResponse(document, transportOK)
		if failed {
			if response != (Response{}) || failure.Category != CategoryProtocol ||
				(failure.Code != CodeRunnerFailed && failure.Code != CodeProtocolIncompatible && failure.Code != CodeInvalidResponse) {
				t.Fatalf("invalid parser failure: response %+v failure %+v", response, failure)
			}
			return
		}
		if failure != (Failure{}) {
			t.Fatalf("accepted response returned failure %+v", failure)
		}
		canonical, err := EncodeResponse(response)
		if err != nil {
			t.Fatalf("accepted response did not encode: %v", err)
		}
		parsed, parseFailure, parseFailed := ParseResponse(canonical, true)
		if parseFailed || parseFailure != (Failure{}) || parsed != response {
			t.Fatalf("canonical response did not reparse: %+v %+v %v", parsed, parseFailure, parseFailed)
		}
	})
}

func mustSeedRequest(request Request) []byte {
	document, err := EncodeRequest(request)
	if err != nil {
		panic(err)
	}
	return document
}
