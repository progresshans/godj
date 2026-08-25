package siteapp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/progresshans/godj/systemstate"
)

func TestConfigDiagnosticsAndJSONNeverExposePassword(t *testing.T) {
	t.Parallel()

	const (
		passwordMarker = "article-site-password-secret-marker"
		backendMarker  = "postgresql://backend-secret-marker"
	)
	config := Config{
		Backend:  configMarkerBackend{URL: backendMarker},
		Username: "admin",
		Password: passwordMarker,
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("json.Marshal(Config): %v", err)
	}
	for _, rendered := range []string{fmt.Sprint(config), fmt.Sprintf("%#v", config), string(encoded)} {
		if strings.Contains(rendered, passwordMarker) || strings.Contains(rendered, backendMarker) {
			t.Fatalf("Config diagnostic exposes a secret: %q", rendered)
		}
	}
	if strings.Contains(string(encoded), `"Password"`) || strings.Contains(string(encoded), `"Backend"`) {
		t.Fatalf("Config JSON publishes a secret-bearing field: %s", encoded)
	}
	var decoded Config
	if err := json.Unmarshal([]byte(`{"Username":"admin","Password":"`+passwordMarker+`","Backend":{"URL":"`+backendMarker+`"}}`), &decoded); err != nil {
		t.Fatalf("json.Unmarshal(Config): %v", err)
	}
	if decoded.Password != "" || decoded.Backend != nil {
		t.Fatal("Config JSON populated a secret-bearing field")
	}
}

type configMarkerBackend struct {
	systemstate.Backend
	URL string
}
