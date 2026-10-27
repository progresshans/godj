package firstaccountwire_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/firstaccountwire"
	"github.com/progresshans/godj/project/firstaccount"
)

func uploadRequest() firstaccountwire.Request {
	request := requestFixture()
	request.Values = append(request.Values,
		firstaccount.Value{Name: "document", Present: true, Upload: &firstaccount.Upload{Name: "비공개.bin", Content: []byte{0, 255, 1, '\n'}}},
		firstaccount.Value{Name: "empty", Present: true, Upload: &firstaccount.Upload{Name: "empty.txt"}},
		firstaccount.Value{Name: "omitted"})
	return request
}

func TestRequestUploadsPreserveBinaryEmptyOmissionAndOwnedLifetime(t *testing.T) {
	request := uploadRequest()
	document, err := firstaccountwire.EncodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := firstaccountwire.ReadRequest(bytes.NewReader(document))
	if err != nil {
		t.Fatal(err)
	}
	request.Clear()
	clear(document)
	if len(decoded.Values) != 6 || decoded.Values[3].Upload.Name != "비공개.bin" || !bytes.Equal(decoded.Values[3].Upload.Content, []byte{0, 255, 1, '\n'}) || decoded.Values[4].Upload == nil || len(decoded.Values[4].Upload.Content) != 0 || decoded.Values[5].Present || decoded.Values[5].Upload != nil {
		t.Fatal("file transport lost exact bytes, empty upload or omission")
	}
	for _, value := range []any{decoded, decoded.Values[3], decoded.Values[3].Upload} {
		encoded, err := json.Marshal(value)
		if err != nil || strings.Contains(fmt.Sprintf("%#v", value)+string(encoded), "비공개") {
			t.Fatal("diagnostics exposed upload", err)
		}
	}
	content := decoded.Values[3].Upload.Content
	decoded.Clear()
	if !allZero(content) || decoded.Values != nil {
		t.Fatal("clear retained upload bytes")
	}
}

func TestRequestUploadEnvelopeRejectsMixedKindsAndForgedLengths(t *testing.T) {
	for _, mutate := range []func(*firstaccountwire.Request){
		func(r *firstaccountwire.Request) { r.Values[3].Text = []byte("reference") },
		func(r *firstaccountwire.Request) { r.Values[3].Present = false },
		func(r *firstaccountwire.Request) { r.Values[3].Upload.Name = "../private.txt" },
		func(r *firstaccountwire.Request) { r.Values[3].Upload.Name = "bad\x00name" },
		func(r *firstaccountwire.Request) { r.Values[3].Upload.Name = strings.Repeat("a", 256) },
	} {
		request := uploadRequest()
		mutate(&request)
		if document, err := firstaccountwire.EncodeRequest(request); err == nil || document != nil {
			t.Fatal("mixed or unsafe upload encoded")
		}
		request.Clear()
	}
	request := firstaccountwire.Request{Fingerprint: fingerprint, OperationID: operationID, Password: []byte("password"), Values: []firstaccount.Value{{Name: "file", Present: true, Upload: &firstaccount.Upload{Name: "x.bin", Content: []byte("private")}}}}
	document, err := firstaccountwire.EncodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	header := len(firstaccountwire.Magic) + 4 + 64 + 64 + 1 + 2
	for _, mutate := range []func([]byte){
		func(b []byte) { binary.BigEndian.PutUint32(b[len(firstaccountwire.Magic):], uint32(len(b)+1)) },
		func(b []byte) { binary.BigEndian.PutUint32(b[len(firstaccountwire.Magic):], ^uint32(0)) },
		func(b []byte) { binary.BigEndian.PutUint16(b[header+2:], 1) },
		func(b []byte) { binary.BigEndian.PutUint16(b[header+4+4:], firstaccount.MaxUploadNameBytes+1) },
		func(b []byte) { binary.BigEndian.PutUint32(b[header+4+4+2:], ^uint32(0)) },
	} {
		bad := bytes.Clone(document)
		mutate(bad)
		if _, err := firstaccountwire.DecodeRequest(bad); err == nil {
			t.Fatal("forged upload frame decoded")
		}
		if _, err := firstaccountwire.ReadRequest(bytes.NewReader(bad)); err == nil {
			t.Fatal("forged upload frame read")
		}
	}
	for n := range len(document) {
		if _, err := firstaccountwire.DecodeRequest(document[:n]); err == nil {
			t.Fatal("truncated file frame accepted", n)
		}
	}
	first := make([]byte, firstaccount.MaxUploadBytes/2+1)
	request.Values = []firstaccount.Value{{Name: "one", Present: true, Upload: &firstaccount.Upload{Name: "one.bin", Content: first}}, {Name: "two", Present: true, Upload: &firstaccount.Upload{Name: "two.bin", Content: first}}}
	if _, err := firstaccountwire.EncodeRequest(request); err == nil {
		t.Fatal("aggregate file bound ignored")
	}
	request.Clear()
}
