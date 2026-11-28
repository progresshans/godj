package runservercontrol

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestControlMessagesRejectMalformedTruncatedAndWrongPhase(t *testing.T) {
	for _, message := range []string{Prepared, Activate, Active, Retire, Retired} {
		var output bytes.Buffer
		if err := Send(&output, message); err != nil || Expect(&output, message) != nil || output.Len() != 0 {
			t.Fatal("control round trip", err)
		}
		for _, invalid := range []string{message[:len(message)-1], strings.Replace(message, "/1", "/2", 1), "private-secret-invalid\n"} {
			if Expect(strings.NewReader(invalid), message) == nil {
				t.Fatal("invalid control message accepted")
			}
		}
	}
	if !errors.Is(Expect(strings.NewReader(Activate), Prepared), ErrProtocol) {
		t.Fatal("wrong control phase accepted")
	}
	if !errors.Is(Send(shortWriter{}, Prepared), io.ErrShortWrite) {
		t.Fatal("partial control delivery accepted")
	}
}

type shortWriter struct{}

func (shortWriter) Write(data []byte) (int, error) { return len(data) - 1, nil }
