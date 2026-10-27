package firstaccountwire_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/firstaccountwire"
	"github.com/progresshans/godj/project/firstaccount"
)

const operationID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
const fingerprint = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func requestFixture() firstaccountwire.Request {
	return firstaccountwire.Request{Fingerprint: fingerprint, OperationID: operationID, Values: []firstaccount.Value{
		{Name: "contact", Present: true, Text: []byte("private@example.test")},
		{Name: "region", Present: false},
		{Name: "note", Present: true, Text: []byte{}},
	}, Password: []byte{0, 255, 'p', 'r', 'i', 'v', 'a', 't', 'e', '\n'}}
}

func TestRequestKeepsExactInputsAndOwnsDetachedBuffers(t *testing.T) {
	request := requestFixture()
	document, err := firstaccountwire.EncodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := firstaccountwire.DecodeRequest(document)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Fingerprint != fingerprint || decoded.OperationID != operationID || len(decoded.Values) != 3 || !decoded.Values[0].Present || decoded.Values[1].Present || !decoded.Values[2].Present || len(decoded.Values[2].Text) != 0 || !bytes.Equal(decoded.Password, request.Password) {
		t.Fatal("wire changed scalar presence or raw password meaning")
	}
	for _, value := range []any{request, &request} {
		for _, formatted := range []string{fmt.Sprintf("%v", value), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value)} {
			if strings.Contains(formatted, "private") || strings.Contains(formatted, fingerprint) {
				t.Fatal("wire formatting exposed input")
			}
		}
		encoded, err := json.Marshal(value)
		if err != nil || bytes.Contains(encoded, []byte("private")) || bytes.Contains(encoded, []byte(fingerprint)) {
			t.Fatal("wire JSON exposed input")
		}
	}
	clear(document)
	request.Clear()
	if string(decoded.Values[0].Text) != "private@example.test" || len(decoded.Password) == 0 {
		t.Fatal("decoded inputs borrowed caller buffers")
	}
	profile, password := decoded.Values[0].Text, decoded.Password
	decoded.Clear()
	if !allZero(profile) || !allZero(password) || decoded.Values != nil || decoded.Password != nil || decoded.OperationID != "" {
		t.Fatal("clear retained owned input")
	}
}

func TestRequestChecksWholeFrameAndBounds(t *testing.T) {
	request := requestFixture()
	document, err := firstaccountwire.EncodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	for n := range len(document) {
		if _, err := firstaccountwire.DecodeRequest(document[:n]); err == nil {
			t.Fatalf("truncation at byte %d accepted", n)
		}
	}
	header := len(firstaccountwire.Magic) + 4 + 64 + 64 + 1 + 2
	for _, test := range []struct {
		name   string
		change func([]byte) []byte
	}{
		{"trailing", func(b []byte) []byte { return append(b, 0) }},
		{"magic", func(b []byte) []byte { b[0] = '!'; return b }},
		{"version", func(b []byte) []byte { b[len(firstaccountwire.Magic)-1] = '1'; return b }},
		{"zero_fingerprint", func(b []byte) []byte { copy(b[len(firstaccountwire.Magic)+4:], strings.Repeat("0", 64)); return b }},
		{"invalid_operation", func(b []byte) []byte { b[len(firstaccountwire.Magic)+4+64] = 'B'; return b }},
		{"zero_fields", func(b []byte) []byte { b[header-3] = 0; return b }},
		{"too_many_fields", func(b []byte) []byte { b[header-3] = firstaccount.MaxFields + 1; return b }},
		{"zero_password", func(b []byte) []byte { binary.BigEndian.PutUint16(b[header-2:], 0); return b }},
		{"long_password", func(b []byte) []byte {
			binary.BigEndian.PutUint16(b[header-2:], firstaccountwire.MaxPasswordBytes+1)
			return b
		}},
		{"zero_name", func(b []byte) []byte { b[header] = 0; return b }},
		{"long_name", func(b []byte) []byte { b[header] = firstaccountwire.MaxNameBytes + 1; return b }},
		{"invalid_presence", func(b []byte) []byte { b[header+1] = 3; return b }},
		{"absent_with_text", func(b []byte) []byte { b[header+1] = 0; return b }},
		{"long_text", func(b []byte) []byte {
			binary.BigEndian.PutUint16(b[header+2:], firstaccount.MaxFieldBytes+1)
			return b
		}},
		{"invalid_name", func(b []byte) []byte { b[header+4] = '\n'; return b }},
		{"nul_scalar", func(b []byte) []byte { b[header+4+len("contact")] = 0; return b }},
		{"invalid_utf8", func(b []byte) []byte { b[header+4+len("contact")] = 255; return b }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := firstaccountwire.DecodeRequest(test.change(bytes.Clone(document))); err == nil {
				t.Fatal("malformed frame accepted")
			}
		})
	}
	for _, change := range []func(*firstaccountwire.Request){
		func(r *firstaccountwire.Request) { r.Values[1].Name = r.Values[0].Name },
		func(r *firstaccountwire.Request) { r.Values[0].Present = false },
		func(r *firstaccountwire.Request) { r.Values[0].Text = []byte{0} },
		func(r *firstaccountwire.Request) { r.Values[0].Text = []byte{255} },
		func(r *firstaccountwire.Request) { r.Values = nil },
		func(r *firstaccountwire.Request) { r.Password = nil },
	} {
		bad := requestFixture()
		change(&bad)
		if encoded, err := firstaccountwire.EncodeRequest(bad); err == nil || encoded != nil {
			t.Fatal("invalid input encoded")
		}
	}
	maximum := requestFixture()
	maximum.Password = bytes.Repeat([]byte{255}, firstaccountwire.MaxPasswordBytes)
	maximum.Values = make([]firstaccount.Value, firstaccount.MaxFields)
	for i := range maximum.Values {
		maximum.Values[i] = firstaccount.Value{Name: strings.Repeat("f", 125) + fmt.Sprintf("%03d", i), Present: true}
		if i < firstaccount.MaxInputBytes/firstaccount.MaxFieldBytes {
			maximum.Values[i].Text = bytes.Repeat([]byte{'x'}, firstaccount.MaxFieldBytes)
		}
	}
	encoded, err := firstaccountwire.EncodeRequest(maximum)
	if err != nil || len(encoded) != header+firstaccount.MaxFields*(4+firstaccountwire.MaxNameBytes)+firstaccount.MaxInputBytes+firstaccountwire.MaxPasswordBytes {
		t.Fatal("full legal request bound is not reachable", err)
	}
	if decoded, err := firstaccountwire.DecodeRequest(encoded); err != nil {
		t.Fatal(err)
	} else {
		decoded.Clear()
	}
	maximum.Values[len(maximum.Values)-1].Text = []byte("x")
	if _, err := firstaccountwire.EncodeRequest(maximum); err == nil {
		t.Fatal("aggregate input bound ignored")
	}
}

func allZero(value []byte) bool {
	for _, b := range value {
		if b != 0 {
			return false
		}
	}
	return true
}

type trackedReader struct {
	input        []byte
	calls, reads int
	exposed      []byte
	fail         string
}

func (reader *trackedReader) Read(buffer []byte) (int, error) {
	reader.calls++
	if reader.calls == 1 {
		reader.exposed = buffer
	}
	switch reader.fail {
	case "panic":
		copy(buffer, "private")
		panic("private reader fault")
	case "error":
		copy(buffer, "private")
		return len("private"), errors.New("private reader fault")
	case "negative":
		return -1, nil
	case "overcount":
		return len(buffer) + 1, nil
	case "empty":
		return 0, nil
	}
	if len(reader.input) == 0 {
		return 0, io.EOF
	}
	n := copy(buffer, reader.input[:min(3, len(reader.input))])
	reader.input = reader.input[n:]
	reader.reads += n
	return n, nil
}

func TestReadRequestRequiresEOFAndClearsTemporarySecrets(t *testing.T) {
	document, _ := firstaccountwire.EncodeRequest(requestFixture())
	reader := &trackedReader{input: document}
	decoded, err := firstaccountwire.ReadRequest(reader)
	if err != nil || reader.reads != len(document) || len(decoded.Values) != 3 {
		t.Fatal("fragmented request failed", err)
	}
	decoded.Clear()
	if !allZero(reader.exposed) {
		t.Fatal("reader retained secret buffer")
	}
	for _, mode := range []string{"panic", "error", "negative", "overcount", "empty"} {
		t.Run(mode, func(t *testing.T) {
			reader := &trackedReader{fail: mode}
			request, err := firstaccountwire.ReadRequest(reader)
			if !errors.Is(err, firstaccountwire.ErrInputTransport) || request.Values != nil || strings.Contains(err.Error(), "private") || !allZero(reader.exposed) {
				t.Fatal("reader failure retained input or cause")
			}
			if mode == "empty" && reader.calls != 100 {
				t.Fatal("no-progress reader was not bounded")
			}
		})
	}
	if _, err := firstaccountwire.ReadRequest(io.MultiReader(bytes.NewReader(document), errorReader{})); !errors.Is(err, firstaccountwire.ErrInputTransport) {
		t.Fatal("transport failure after complete frame became success")
	}
	if _, err := firstaccountwire.ReadRequest(bytes.NewReader(bytes.Repeat([]byte{'x'}, firstaccountwire.MaxRequestBytes+1))); !errors.Is(err, firstaccountwire.ErrInvalidRequest) {
		t.Fatal("oversized frame accepted")
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("private trailing transport fault")
}

func FuzzRequest(f *testing.F) {
	document, _ := firstaccountwire.EncodeRequest(requestFixture())
	f.Add(document)
	files, _ := firstaccountwire.EncodeRequest(uploadRequest())
	f.Add(files)
	f.Add([]byte{})
	f.Add([]byte(firstaccountwire.Magic))
	f.Fuzz(func(t *testing.T, document []byte) {
		request, err := firstaccountwire.DecodeRequest(document)
		if err != nil {
			return
		}
		defer request.Clear()
		encoded, err := firstaccountwire.EncodeRequest(request)
		if err != nil || !bytes.Equal(encoded, document) {
			t.Fatal("accepted request has no exact canonical roundtrip")
		}
		clear(encoded)
	})
}
