package systemstate

import (
	"strings"
	"testing"
)

func TestHistoricalUsernameLimitsFollowIRAfterPinnedNormalization(t *testing.T) {
	for _, input := range []string{strings.Repeat("한", 150), strings.Repeat("\U000105c0", 256), strings.Repeat("x", 256)} {
		if got, err := normalizeIdentityUsername(input); err != nil || got != input {
			t.Fatal("storage character range rejected", len(input), err)
		}
	}
	for _, input := range []string{strings.Repeat("x", 257), strings.Repeat("한", 257), strings.Repeat("\U000105c0", 257), strings.Repeat("ﬃ", 86)} {
		if got, err := normalizeIdentityUsername(input); err == nil || got != "" {
			t.Fatal("normalization escaped storage limit", err)
		}
	}
	if got, err := normalizeIdentityUsername("\U0001ccd6\U0001ccd7"); err != nil || got != "AB" {
		t.Fatal("Unicode 16 normalization missing", got, err)
	}
}
