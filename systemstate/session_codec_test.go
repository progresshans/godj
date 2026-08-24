package systemstate

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/sessions"
)

func TestSessionPayloadRoundTripCanonicalDigestOnlyAndDetached(t *testing.T) {
	t.Parallel()

	id := mustCodecSessionID(t, "A")
	created := time.Date(2026, time.August, 24, 1, 2, 3, 456, time.FixedZone("codec", 9*60*60))
	values := map[string]string{"zeta": "한글\nvalue", "alpha": "first"}
	record, err := sessions.RestoreRecord(sessions.RecordSnapshot{
		ID:                id,
		Values:            values,
		CreatedAt:         created,
		AccessedAt:        created.Add(time.Minute),
		AbsoluteExpiresAt: created.Add(24 * time.Hour),
		IdleExpiresAt:     created.Add(31 * time.Minute),
	}, sessions.Limits{})
	if err != nil {
		t.Fatalf("RestoreRecord(): %v", err)
	}

	payload, err := encodeSessionPayload(record, sessions.Limits{})
	if err != nil {
		t.Fatalf("encodeSessionPayload(): %v", err)
	}
	const wantPayload = "v1.GM56nm8ZD8gYznqsZ2BnyBjOyTMAaA_IGM58T3-8t8gAAgAFYWxwaGEAAAAFZmlyc3QABHpldGEAAAAM7ZWc6riACnZhbHVl"
	if payload != wantPayload {
		t.Fatalf("payload golden = %q, want %q", payload, wantPayload)
	}
	if !strings.HasPrefix(payload, sessionPayloadPrefix) || strings.Contains(payload, id.Encoded()) {
		t.Fatalf("payload shape contains bearer or misses current version: %q", payload)
	}
	digest, err := sessionDigest(id)
	if err != nil {
		t.Fatalf("sessionDigest(): %v", err)
	}
	const wantDigest = "4159c5d401af09c2a95c8c36a664299a4eae8676e8c406cdfb44fc5511bc2b4d"
	if digest != wantDigest {
		t.Fatalf("digest golden = %q, want %q", digest, wantDigest)
	}
	if len(digest) != 64 || strings.Contains(digest, id.Encoded()) {
		t.Fatalf("digest = %q, want 64-char bearer-free SHA-256", digest)
	}
	if second, _ := sessionDigest(id); second != digest {
		t.Fatalf("sessionDigest() = %q then %q, want deterministic", digest, second)
	}

	values["alpha"] = "mutated"
	decoded, err := decodeSessionPayload(payload, id, sessions.Limits{})
	if err != nil {
		t.Fatalf("decodeSessionPayload(): %v", err)
	}
	if got, _ := decoded.Value("alpha"); got != "first" {
		t.Fatalf("decoded alpha = %q, want detached first", got)
	}
	if !decoded.CreatedAt().Equal(created) || decoded.CreatedAt().Location() != time.UTC {
		t.Fatalf("decoded CreatedAt = %v (%v), want canonical UTC", decoded.CreatedAt(), decoded.CreatedAt().Location())
	}
	if again, err := encodeSessionPayload(decoded, sessions.Limits{}); err != nil || again != payload {
		t.Fatalf("canonical re-encode = (%q,%v), want exact payload", again, err)
	}
}

func TestSessionPayloadRejectsUnknownMalformedNoncanonicalAndOversize(t *testing.T) {
	t.Parallel()

	id := mustCodecSessionID(t, "Q")
	valid := mustCodecPayload(t, id, map[string]string{"alpha": "first", "zetas": "lasts"})
	wire, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(valid, sessionPayloadPrefix))
	if err != nil {
		t.Fatalf("decode valid wire: %v", err)
	}

	// The two equal-length entries can be swapped without changing framing;
	// strict decode rejects the resulting noncanonical key order.
	const headerBytes = 4*8 + 2
	entryBytes := (2 + len("alpha") + 4 + len("first"))
	noncanonical := append([]byte(nil), wire...)
	copy(noncanonical[headerBytes:headerBytes+entryBytes], wire[headerBytes+entryBytes:])
	copy(noncanonical[headerBytes+entryBytes:], wire[headerBytes:headerBytes+entryBytes])

	tests := []struct {
		name    string
		payload string
	}{
		{name: "unknown version", payload: "v2." + strings.TrimPrefix(valid, sessionPayloadPrefix)},
		{name: "invalid base64", payload: sessionPayloadPrefix + "***"},
		{name: "truncated", payload: sessionPayloadPrefix + base64.RawURLEncoding.EncodeToString(wire[:len(wire)-1])},
		{name: "trailing", payload: sessionPayloadPrefix + base64.RawURLEncoding.EncodeToString(append(wire, 0))},
		{name: "noncanonical order", payload: sessionPayloadPrefix + base64.RawURLEncoding.EncodeToString(noncanonical)},
		{name: "oversize", payload: sessionPayloadPrefix + strings.Repeat("A", maxSessionPayloadBytes)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record, err := decodeSessionPayload(test.payload, id, sessions.Limits{})
			if err == nil || record.ID().Valid() {
				t.Fatalf("decodeSessionPayload() = (%v,%v), want zero/error", record, err)
			}
			var classified *Error
			if !errors.As(err, &classified) || classified.Code != CodeCorruptState || classified.Field != "session_payload" {
				t.Fatalf("error = %#v, want corrupt_state/session_payload", err)
			}
			if strings.Contains(err.Error(), test.payload) || strings.Contains(err.Error(), id.Encoded()) {
				t.Fatalf("error exposes stored payload or bearer: %q", err)
			}
		})
	}
}

func TestSessionPayloadHonorsCurrentProfileAndConfiguredBounds(t *testing.T) {
	t.Parallel()

	id := mustCodecSessionID(t, "g")
	if _, err := durableSessionLimits(sessions.Limits{MaxTotalBytes: sessions.DefaultLimits().MaxTotalBytes + 1}); err == nil {
		t.Fatal("durableSessionLimits(widened) error = nil")
	}
	payload := mustCodecPayload(t, id, map[string]string{"principal": "admin"})
	if record, err := decodeSessionPayload(payload, id, sessions.Limits{MaxValueBytes: 4}); err == nil || record.ID().Valid() {
		t.Fatalf("decodeSessionPayload(narrow limit) = (%v,%v), want zero/error", record, err)
	}
	if digest, err := sessionDigest(sessions.ID{}); err == nil || digest != "" {
		t.Fatalf("sessionDigest(zero) = (%q,%v), want empty/error", digest, err)
	}
}

func mustCodecPayload(t *testing.T, id sessions.ID, values map[string]string) string {
	t.Helper()
	created := time.Date(2026, time.August, 24, 0, 0, 0, 0, time.UTC)
	record, err := sessions.RestoreRecord(sessions.RecordSnapshot{
		ID:                id,
		Values:            values,
		CreatedAt:         created,
		AccessedAt:        created,
		AbsoluteExpiresAt: created.Add(time.Hour),
		IdleExpiresAt:     created.Add(30 * time.Minute),
	}, sessions.Limits{})
	if err != nil {
		t.Fatalf("RestoreRecord(): %v", err)
	}
	payload, err := encodeSessionPayload(record, sessions.Limits{})
	if err != nil {
		t.Fatalf("encodeSessionPayload(): %v", err)
	}
	return payload
}

func mustCodecSessionID(t *testing.T, fill string) sessions.ID {
	t.Helper()
	id, err := sessions.ParseID(strings.Repeat(fill, 43))
	if err != nil {
		t.Fatalf("ParseID(): %v", err)
	}
	return id
}
