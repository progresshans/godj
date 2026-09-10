package bearerauth

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestTokenRedactsEveryDiagnosticSurface(t *testing.T) {
	t.Parallel()

	const raw = "gdj-bearer-token-redaction-canary"
	token := newToken(raw)
	if token.Encoded() != raw {
		t.Fatal("Encoded did not preserve verifier material")
	}
	if (Token{}).Encoded() != "" {
		t.Fatal("zero Token contains credential material")
	}
	assertTokenRedacted(t, token, raw)
	retained := token
	token.release()
	if token.Encoded() != "" || retained.Encoded() != "" {
		t.Fatal("released Token copy retained accessible credential material")
	}
	assertTokenRedacted(t, token, raw)
}

func assertTokenRedacted(t *testing.T, token Token, raw string) {
	t.Helper()
	rendered := []string{
		fmt.Sprint(token),
		fmt.Sprintf("%v", token),
		fmt.Sprintf("%+v", token),
		fmt.Sprintf("%#v", token),
		fmt.Sprintf("%s", token),
		fmt.Sprintf("%q", token),
		fmt.Sprintf("%x", token),
		fmt.Sprintf("%d", token),
		fmt.Sprintf("%#v", &token),
	}
	encoded, err := json.Marshal(token)
	if err != nil {
		t.Fatal("Token JSON serialization failed")
	}
	rendered = append(rendered, string(encoded))
	for _, value := range rendered {
		if strings.Contains(value, raw) || !strings.Contains(value, "redacted") {
			t.Fatal("Token diagnostic is not fixed-redacted")
		}
	}
}
