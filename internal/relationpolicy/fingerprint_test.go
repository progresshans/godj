package relationpolicy

import (
	"bytes"
	"crypto/sha256"
	"testing"
)

func TestFingerprintFramingSeparatesAdjacentValues(t *testing.T) {
	left, right := sha256.New(), sha256.New()
	writeValues(left, "ab", "c")
	writeValues(right, "a", "bc")
	if bytes.Equal(left.Sum(nil), right.Sum(nil)) {
		t.Fatal("length-delimited fingerprint values collided")
	}
}
