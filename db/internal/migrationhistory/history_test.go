package migrationhistory_test

import (
	"encoding/hex"
	"github.com/progresshans/godj/db/internal/migrationhistory"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"reflect"
	"testing"
)

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
