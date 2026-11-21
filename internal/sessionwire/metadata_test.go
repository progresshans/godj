package sessionwire_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/internal/sessionwire"
	"github.com/progresshans/godj/sessions"
)

func TestSessionMetadataUsesHistoricalCanonicalWireWithoutBearer(t *testing.T) {
	const payload = "v1.GM56nm8ZD8gYznqsZ2BnyBjOyTMAaA_IGM58T3-8t8gAAgAFYWxwaGEAAAAFZmlyc3QABHpldGEAAAAM7ZWc6riACnZhbHVl"
	metadata, err := sessionwire.Inspect(payload, sessions.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if value, present := metadata.Value("zeta"); !present || value != "한글\nvalue" {
		t.Fatal("historical metadata value differs")
	}
	created := time.Date(2026, 8, 24, 1, 2, 3, 456, time.FixedZone("wire", 9*60*60))
	if metadata.Expired(created.Add(30*time.Minute)) || !metadata.Expired(created.Add(31*time.Minute)) {
		t.Fatal("historical expiry boundary changed")
	}
	if raw, err := json.Marshal(metadata); err != nil || string(raw) != "{}" {
		t.Fatal("metadata serialized private values", err)
	}
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%w", "%d"} {
		if strings.Contains(fmt.Sprintf(format, metadata), "first") {
			t.Fatal("metadata diagnostic disclosed a value")
		}
	}
	for _, invalid := range []string{"", "v2." + payload[3:], payload + "=", payload + "\n", strings.Repeat("A", sessionwire.MaximumPayloadBytes+1)} {
		value, err := sessionwire.Inspect(invalid, sessions.Limits{})
		if err == nil || !value.Expired(created) {
			t.Fatal("invalid metadata was published")
		}
	}
}
