package migrationhistory_test

import (
	"bytes"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/db/internal/migrationhistory"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
)

func TestFingerprintDefinitionBindings(t *testing.T) {
	first := "sha256:" + strings.Repeat("01", 32)
	second := "sha256:" + strings.Repeat("02", 32)
	for _, test := range []struct{ a, b, want string }{
		{first, "", "c9ca54f89f0b68e4699919f219f2ee8d39fd7279eddd522b45b66ca129efa53e"},
		{"", first, "f7fd901ec9fb7be14b4bcd4fb3b22aa5af20609469fadbdd9f3a46c367240041"},
		{second, "", "c4aff063969384f8e61ab8bbcff1aeacd7a2c5ba0a9d8655d681c09a60063770"},
		{"", "", "cb09a0ecba1c09bfa23eb5a57eab3ccbe15b79e05c439d55ff58af82267d1989"},
	} {
		records := []migrationbackend.AppliedMigration{
			{App: "b", Name: "0001", HistoryDigest: test.b}, {App: "a", Name: "0001", HistoryDigest: test.a},
		}
		sum := migrationhistory.Fingerprint(records)
		if hex.EncodeToString(sum[:]) != test.want || records[0].App != "b" {
			t.Fatalf("unexpected fingerprint %x or changed input %v", sum, records)
		}
	}
}

func TestDefinitionDigestStorage(t *testing.T) {
	data := bytes.Repeat([]byte{0xab}, 32)
	var stored migrationhistory.StoredDigest
	if err := stored.Scan(data); err != nil || string(stored) != "sha256:"+strings.Repeat("ab", 32) {
		t.Fatal("binary binding", stored, err)
	}
	raw, err := migrationbackend.ParseDefinitionHistoryDigest(string(stored))
	if err != nil || !bytes.Equal(raw, data) {
		t.Fatal("canonical digest did not round trip", err)
	}
	for _, malformed := range []string{"", strings.ToUpper(string(stored)), "sha256:" + strings.Repeat("AB", 32), "sha256:" + strings.Repeat("gg", 32), string(stored) + "00"} {
		if _, err := migrationbackend.ParseDefinitionHistoryDigest(malformed); err == nil {
			t.Fatal("accepted malformed definition digest", malformed)
		}
	}
	for _, value := range []any{[]byte(nil), []byte{}, data[:31], append(data, 0), string(data)} {
		if err := stored.Scan(value); !errors.Is(err, migrationhistory.ErrInvalidStoredDigest) {
			t.Fatalf("accepted invalid stored digest of type %T: %v", value, err)
		}
	}
	if err := stored.Scan(nil); err != nil || stored != "" {
		t.Fatal("SQL NULL did not clear a previous binding", stored, err)
	}
}

func TestFingerprintV1Goldens(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		records []migrationbackend.AppliedMigration
		want    string
	}{
		{name: "empty", want: "af5570f5a1810b7af78caf4bc70a660f0df51e42baf91d4de5b2328de0e83dfc"},
		{
			name:    "alpha",
			records: []migrationbackend.AppliedMigration{{App: "alpha", Name: "0001"}},
			want:    "d082f0a0b67b8c2b5c7efc208270dd2e17c6a346d9b2fa0e572e6396dedff40e",
		},
		{
			name:    "utf8_byte_length",
			records: []migrationbackend.AppliedMigration{{App: "legacy", Name: "ä"}},
			want:    "35e542d7c4bce2ba60aa694f4301300cb1835e58ca14efe54a781ea7ae03e45c",
		},
		{
			name: "canonical_order",
			records: []migrationbackend.AppliedMigration{
				{App: "beta", Name: "0001"},
				{App: "alpha", Name: "0002"},
			},
			want: "10d5c73ea5b12735cec427f16e0b516ca9d8c235754ee9f8e6b8cab4f04e9f4a",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			before := append([]migrationbackend.AppliedMigration(nil), test.records...)
			fingerprint := migrationhistory.Fingerprint(test.records)
			if !reflect.DeepEqual(before, test.records) {
				t.Fatal("fingerprint mutated caller order")
			}
			if got := hex.EncodeToString(fingerprint[:]); got != test.want {
				t.Fatalf("fingerprint = %s, want %s", got, test.want)
			}
		})
	}
}
