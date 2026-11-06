package timezone_test

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/progresshans/godj/timezone"
)

func TestExplicitZoneIgnoresEnvironmentAndPreservesHistoricalTransitions(t *testing.T) {
	t.Setenv("TZ", "Etc/GMT+11")
	t.Setenv("ZONEINFO", t.TempDir())
	for _, test := range []struct {
		zone, instant, civil string
		offset               int
	}{
		{"UTC", "2024-01-01T00:00:00Z", "2024-01-01 00:00:00", 0},
		{"Asia/Seoul", "2024-01-01T16:00:00Z", "2024-01-02 01:00:00", 9 * 3600},
		{"Europe/Paris", "1900-01-01T00:00:00Z", "1900-01-01 00:09:21", 561},
		{"America/New_York", "2024-03-10T06:59:59Z", "2024-03-10 01:59:59", -5 * 3600},
		{"America/New_York", "2024-03-10T07:00:00Z", "2024-03-10 03:00:00", -4 * 3600},
		{"America/New_York", "2024-11-03T05:30:00Z", "2024-11-03 01:30:00", -4 * 3600},
		{"America/New_York", "2024-11-03T06:30:00Z", "2024-11-03 01:30:00", -5 * 3600},
		{"Australia/Lord_Howe", "2024-10-05T15:29:59Z", "2024-10-06 01:59:59", 10*3600 + 30*60},
		{"Australia/Lord_Howe", "2024-10-05T15:30:00Z", "2024-10-06 02:30:00", 11 * 3600},
		{"Pacific/Apia", "2011-12-30T09:59:59Z", "2011-12-29 23:59:59", -10 * 3600},
		{"Pacific/Apia", "2011-12-30T10:00:00Z", "2011-12-31 00:00:00", 14 * 3600},
	} {
		t.Run(test.zone+"/"+test.instant, func(t *testing.T) {
			zone, err := timezone.Parse(test.zone)
			if err != nil {
				t.Fatal(err)
			}
			location, err := zone.Location()
			if err != nil {
				t.Fatal(err)
			}
			instant, err := time.Parse(time.RFC3339, test.instant)
			if err != nil {
				t.Fatal(err)
			}
			local := instant.In(location)
			_, offset := local.Zone()
			if local.Format("2006-01-02 15:04:05") != test.civil || offset != test.offset {
				t.Fatalf("local = %v (%d), want %s (%d)", local, offset, test.civil, test.offset)
			}
		})
	}
}

func TestZoneRejectsAmbientNamesAndInvalidInputWithoutReplacingReceiver(t *testing.T) {
	for _, name := range []string{"", "Local", "local", "utc", " UTC", "UTC ", "/UTC", "../UTC", "Asia//Seoul", "Asia/./Seoul", "Asia\\Seoul", "UTC\x00", "+09:00", "Missing/Zone", strings.Repeat("x", timezone.MaximumNameBytes+1)} {
		if zone, err := timezone.Parse(name); zone != (timezone.Zone{}) || !errors.Is(err, timezone.ErrInvalid) {
			t.Fatalf("Parse(%q) = %v, %v", name, zone, err)
		}
		value := timezone.UTC()
		if err := value.UnmarshalText([]byte(name)); !errors.Is(err, timezone.ErrInvalid) || value != timezone.UTC() {
			t.Fatalf("invalid text changed receiver: %v, %v", value, err)
		}
	}
	for _, raw := range []string{"null", "false", "123", "{}", "[]", `""`, `"Local"`, `"UTC" trailing`, `"` + strings.Repeat("x", timezone.MaximumNameBytes*6+1) + `"`} {
		value := timezone.UTC()
		if err := json.Unmarshal([]byte(raw), &value); err == nil || value != timezone.UTC() {
			t.Fatalf("invalid JSON changed receiver: %v, %v", value, err)
		}
	}
	var zero timezone.Zone
	if zero.Valid() || zero.Validate() == nil {
		t.Fatal("zero Zone became UTC")
	}
	if _, err := zero.MarshalText(); err == nil {
		t.Fatal("zero text marshal succeeded")
	}
	if _, err := zero.MarshalJSON(); err == nil {
		t.Fatal("zero JSON marshal succeeded")
	}
	if _, err := zero.Location(); err == nil {
		t.Fatal("zero location succeeded")
	}
	var absent *timezone.Zone
	if absent.UnmarshalText([]byte("UTC")) == nil || absent.UnmarshalJSON([]byte(`"UTC"`)) == nil {
		t.Fatal("nil receiver accepted input")
	}
}

func TestZoneOwnsInputLocationsAndNamesAcrossConcurrentCallers(t *testing.T) {
	raw := []byte("Asia/Seoul")
	var zone timezone.Zone
	if err := zone.UnmarshalText(raw); err != nil {
		t.Fatal(err)
	}
	raw[0] = 'X'
	if zone.Name() != "Asia/Seoul" {
		t.Fatal("zone retained caller bytes")
	}
	names, err := timezone.Names()
	if err != nil {
		t.Fatal(err)
	}
	names[0] = "bad"
	next, err := timezone.Names()
	if err != nil || next[0] == "bad" {
		t.Fatalf("names retained caller slice: %v", err)
	}
	var workers sync.WaitGroup
	for range 24 {
		workers.Go(func() {
			for range 16 {
				location, err := zone.Location()
				if err != nil {
					t.Error(err)
					return
				}
				_, offset := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).In(location).Zone()
				if offset != 9*3600 {
					t.Errorf("shared location mutated: %d", offset)
				}
				*location = *time.UTC
				text, err := zone.MarshalText()
				if err != nil {
					t.Error(err)
					return
				}
				text[0] = 'X'
				encoded, err := json.Marshal(zone)
				if err != nil {
					t.Error(err)
					return
				}
				var decoded timezone.Zone
				if err := json.Unmarshal(encoded, &decoded); err != nil || decoded != zone {
					t.Errorf("JSON roundtrip = %v, %v", decoded, err)
				}
			}
		})
	}
	workers.Wait()
	if zone.Name() != "Asia/Seoul" || zone.String() != "Asia/Seoul" {
		t.Fatal("caller changed zone name")
	}
}
