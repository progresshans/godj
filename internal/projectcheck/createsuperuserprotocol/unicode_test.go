package createsuperuserprotocol

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrivateRequestCarriesFullWidthUTF8AtItsBoundedByteEnvelope(t *testing.T) {
	for _, username := range []string{strings.Repeat("한", 150), strings.Repeat("\U000105c0", 256)} {
		input := Request{Username: []byte(username), Password: []byte("  secret  ")}
		encoded, err := EncodeRequest(input)
		if err != nil {
			t.Fatal(err)
		}
		decoded, failure, failed := DecodeRequest(encoded)
		if failed || !bytes.Equal(decoded.Username, input.Username) || !bytes.Equal(decoded.Password, input.Password) {
			t.Fatal("private UTF-8 frame changed credentials", failure)
		}
		decoded.Clear()
		input.Clear()
		clear(encoded)
	}
	if ValidUsername([]byte(strings.Repeat("\U000105c0", 257))) {
		t.Fatal("private username frame exceeded byte envelope")
	}
}
