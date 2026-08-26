package siteapp

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/systemstate"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

func TestConfigDiagnosticsAndJSONNeverExposeSecrets(t *testing.T) {
	t.Parallel()

	const (
		passwordMarker = "article-site-password-secret-marker"
		backendMarker  = "postgresql://backend-secret-marker"
		csrfKeyMarker  = "siteapp-csrf-key-secret-marker!!"
	)
	keyRing, err := websessionauth.NewCSRFKeyRing([]byte(csrfKeyMarker))
	if err != nil {
		t.Fatalf("NewCSRFKeyRing(): %v", err)
	}
	config := NewConfig(configMarkerBackend{URL: backendMarker}, "admin", passwordMarker).
		WithCSRFKeyRing(keyRing)
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("json.Marshal(Config): %v", err)
	}
	if string(encoded) != `"siteapp.Config{redacted}"` {
		t.Fatalf("Config JSON diagnostic = %s, want explicit redaction", encoded)
	}
	rendered := []string{string(encoded)}
	for _, diagnostic := range []string{fmt.Sprint(config), fmt.Sprint(&config)} {
		if diagnostic != configDiagnostic {
			t.Fatalf("Config default diagnostic = %q, want %q", diagnostic, configDiagnostic)
		}
		rendered = append(rendered, diagnostic)
	}
	for _, format := range []string{
		"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%o", "%p", "%w",
		"%020d", "%#[1]v",
	} {
		for _, diagnostic := range []string{fmt.Sprintf(format, config), fmt.Sprintf(format, &config)} {
			// fmt reserves invalid/special %p and %w representations before
			// consulting Formatter. Opaque state makes those paths secret-free.
			if format != "%p" && format != "%w" && diagnostic != configDiagnostic {
				t.Fatalf("Config format %q diagnostic = %q, want %q", format, diagnostic, configDiagnostic)
			}
			rendered = append(rendered, diagnostic)
		}
	}
	wrapFormat := "wrapped config: %w"
	wrapped := fmt.Errorf(wrapFormat, config).Error()
	rendered = append(rendered, wrapped)
	for _, diagnostic := range rendered {
		for _, marker := range append(
			append(secretMarkerEncodings(passwordMarker), secretMarkerEncodings(backendMarker)...),
			secretMarkerEncodings(csrfKeyMarker)...,
		) {
			if strings.Contains(diagnostic, marker) {
				t.Fatalf("Config diagnostic exposes encoded secret marker %q: %q", marker, diagnostic)
			}
		}
	}
	if strings.Contains(string(encoded), `"Password"`) || strings.Contains(string(encoded), `"Backend"`) ||
		strings.Contains(string(encoded), `"CSRFKeyRing"`) {
		t.Fatalf("Config JSON publishes a secret-bearing field: %s", encoded)
	}
	var decoded Config
	if err := json.Unmarshal([]byte(`{"Username":"admin","Password":"`+passwordMarker+`","Backend":{"URL":"`+backendMarker+`"}}`), &decoded); err != nil {
		t.Fatalf("json.Unmarshal(Config): %v", err)
	}
	if decoded.state != nil {
		t.Fatal("Config JSON populated a secret-bearing field")
	}
}

func TestNewConfigPreservesStartupValidationOrder(t *testing.T) {
	t.Parallel()

	if application, err := New(nil, NewConfig(nil, "admin", "password")); application != nil ||
		err == nil || err.Error() != "article site application: context is nil" {
		t.Fatalf("New(nil context) = (%v, %v)", application, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if application, err := New(canceled, NewConfig(nil, "admin", "password")); application != nil ||
		err == nil || err != context.Canceled {
		t.Fatalf("New(canceled context) = (%v, %v)", application, err)
	}
	for _, test := range []struct {
		name   string
		config Config
		want   string
	}{
		{name: "zero config", config: Config{}, want: "article site application: configured username is empty"},
		{name: "empty username", config: NewConfig(nil, "  ", "password"), want: "article site application: configured username is empty"},
		{name: "empty password", config: NewConfig(nil, "admin", "  "), want: "article site application: configured password is empty"},
	} {
		t.Run(test.name, func(t *testing.T) {
			application, err := New(context.Background(), test.config)
			if application != nil || err == nil || err.Error() != test.want {
				t.Fatalf("New() = (%v, %v), want nil/%q", application, err, test.want)
			}
		})
	}
}

func secretMarkerEncodings(value string) []string {
	material := []byte(value)
	decimal := make([]string, len(material))
	octal := make([]string, len(material))
	for index, character := range material {
		decimal[index] = strconv.Itoa(int(character))
		octal[index] = strconv.FormatUint(uint64(character), 8)
	}
	return []string{
		value,
		hex.EncodeToString(material),
		strings.ToUpper(hex.EncodeToString(material)),
		base64.StdEncoding.EncodeToString(material),
		base64.RawStdEncoding.EncodeToString(material),
		base64.URLEncoding.EncodeToString(material),
		base64.RawURLEncoding.EncodeToString(material),
		strings.Join(decimal, " "),
		strings.Join(octal, " "),
	}
}

type configMarkerBackend struct {
	systemstate.Backend
	URL string
}
