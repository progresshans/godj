package zonerules

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestPinnedCatalogMatchesSourceAndLoadsEveryEntry(t *testing.T) {
	raw, err := os.ReadFile("source.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Version     string `json:"iana_version"`
		SHA256      string `json:"sha256"`
		Bytes       int    `json:"bytes"`
		Zones       int    `json:"zones"`
		NamesSHA256 string `json:"names_sha256"`
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	names, err := Names()
	if err != nil {
		t.Fatal(err)
	}
	if source.Version != Version || source.SHA256 != SHA256 || source.Bytes != len(embeddedData) || source.Zones != len(names) {
		t.Fatalf("source metadata differs from embedded catalog: %+v, bytes=%d, zones=%d", source, len(embeddedData), len(names))
	}
	if digest := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(names, "\n")+"\n"))); digest != source.NamesSHA256 {
		t.Fatalf("name inventory hash = %s, source = %s", digest, source.NamesSHA256)
	}
	if !slices.IsSorted(names) || len(slices.Compact(slices.Clone(names))) != len(names) {
		t.Fatal("catalog names are not unique and sorted")
	}
	for _, name := range names {
		location, err := Borrow(name)
		if err != nil || location == nil || location.String() != name {
			t.Fatalf("load %q: %v %v", name, location, err)
		}
	}
}

func TestCorruptCatalogFailsClosed(t *testing.T) {
	changed := []byte(embeddedData)
	changed[len(changed)/2] ^= 1
	for label, raw := range map[string]string{
		"empty": "", "truncated": embeddedData[:len(embeddedData)-1],
		"changed": string(changed), "oversize": strings.Repeat("x", maximumDataBytes+1),
	} {
		t.Run(label, func(t *testing.T) {
			catalog, err := readCatalog(raw)
			if catalog != nil || !errors.Is(err, ErrUnavailable) {
				t.Fatalf("corrupt data produced %v, %v", catalog, err)
			}
		})
	}
}
