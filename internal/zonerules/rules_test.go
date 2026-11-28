package zonerules

import (
	"testing"
	"time"
)

func TestAllPinnedRulesMatchIndependentGoReaderAndFutureCycles(t *testing.T) {
	names, err := Names()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			rules, err := Load(name)
			if err != nil {
				t.Fatal(err)
			}
			location, err := Borrow(name)
			if err != nil {
				t.Fatal(err)
			}
			compare := func(second int64) {
				t.Helper()
				_, want := time.Unix(second, 0).In(location).Zone()
				got, err := rules.OffsetAt(second)
				if err != nil || got != want {
					t.Fatalf("offset at %s = %d, want %d: %v", time.Unix(second, 0).UTC(), got, want, err)
				}
			}
			for transition := range rules.Transitions() {
				for _, delta := range []int64{-1, 0, 1} {
					second := transition.Start + delta
					if second >= minimumEpoch {
						compare(second)
					}
					if transition.Start >= CycleStart && transition.Start < CycleStart+CycleSeconds {
						for cycle := int64(1); cycle <= 19; cycle++ {
							repeated := second + cycle*CycleSeconds
							if time.Unix(repeated, 0).UTC().Year() <= 9999 {
								compare(repeated)
							}
						}
					}
				}
			}
			// Independent calendar probes also catch a missing whole recurrence,
			// not only a wrong boundary in a transition already emitted above.
			for year := 2399; year <= 2801; year++ {
				for month := time.January; month <= time.December; month++ {
					compare(time.Date(year, month, 1, 0, 0, 0, 0, time.UTC).Unix())
				}
			}
			for _, year := range []int{1, 100, 400, 1600, 1800, 1900, 2000, 2024, 2100, 9999} {
				for _, month := range []time.Month{time.January, time.March, time.July, time.December} {
					compare(time.Date(year, month, 1, 0, 0, 0, 0, time.UTC).Unix())
				}
			}
		})
	}
}

func TestFoldZeroUsesPretransitionOffsetForGapAndOverlap(t *testing.T) {
	for _, test := range []struct{ zone, civil, utc string }{
		{"America/New_York", "2024-11-03T01:30:00Z", "2024-11-03T05:30:00Z"},
		{"America/New_York", "2024-11-03T02:00:00Z", "2024-11-03T07:00:00Z"},
		{"America/New_York", "2024-03-10T02:30:00Z", "2024-03-10T07:30:00Z"},
		{"America/New_York", "2024-03-10T03:00:00Z", "2024-03-10T07:00:00Z"},
		{"Australia/Lord_Howe", "2024-10-06T02:15:00Z", "2024-10-05T15:45:00Z"},
		{"Australia/Lord_Howe", "2024-04-07T01:45:00Z", "2024-04-06T14:45:00Z"},
		{"Pacific/Apia", "2011-12-30T12:00:00Z", "2011-12-30T22:00:00Z"},
		{"Europe/Dublin", "2024-10-27T01:30:00Z", "2024-10-27T00:30:00Z"},
		{"Europe/Paris", "1900-01-01T00:00:00Z", "1899-12-31T23:50:39Z"},
		{"UTC", "0001-01-01T00:00:00Z", "0001-01-01T00:00:00Z"},
		{"UTC", "9999-12-31T23:59:59Z", "9999-12-31T23:59:59Z"},
	} {
		t.Run(test.zone+"/"+test.civil, func(t *testing.T) {
			rules, err := Load(test.zone)
			if err != nil {
				t.Fatal(err)
			}
			civil, err := time.Parse(time.RFC3339, test.civil)
			if err != nil {
				t.Fatal(err)
			}
			offset, err := rules.OffsetForCivil(civil.Unix())
			if err != nil {
				t.Fatal(err)
			}
			got := civil.Add(-time.Duration(offset) * time.Second).Format(time.RFC3339)
			if got != test.utc {
				t.Fatalf("fold=0 = %s, want %s", got, test.utc)
			}
		})
	}
}

func TestRulesReturnValueCopiesAndInvalidDataFailsClosed(t *testing.T) {
	rules, err := Load("Asia/Seoul")
	if err != nil {
		t.Fatal(err)
	}
	for transition := range rules.Transitions() {
		transition.Offset = 0
		break
	}
	at := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	if offset, err := rules.OffsetAt(at); err != nil || offset != 32400 {
		t.Fatalf("owned transition mutation changed rule data: %d %v", offset, err)
	}
	if _, err := (Rules{}).OffsetAt(at); err == nil {
		t.Fatal("empty rules inferred UTC")
	}
	if _, err := rules.OffsetForCivil(minimumEpoch - 1); err == nil {
		t.Fatal("unbounded civil input accepted")
	}
	if _, err := Load("Local"); err == nil {
		t.Fatal("rules consulted ambient local zone")
	}
	for _, raw := range [][]byte{nil, []byte("TZif"), make([]byte, 44), append([]byte("TZif2"), make([]byte, 39)...)} {
		if _, _, err := readTZif(raw); err == nil {
			t.Fatal("truncated/malformed TZif accepted")
		}
	}
	for _, text := range []string{"", "UTC", "UT0", "UTC168", "UTC0DST,M0.1.0,M11.1.0", "UTC0DST,J0,J365", "UTC0DST,366,0", "UTC0DST,M3.2.0/168,M11.1.0", "UTC0DST,M3.2.0,M11.1.0junk", "UTC0DST,M3.2.0/1:60,M11.1.0"} {
		if _, err := parseRecurrence(text); err == nil {
			t.Fatalf("invalid recurrence accepted: %q", text)
		}
	}
}

func TestPOSIXJulianAndSignedExtendedRules(t *testing.T) {
	for _, test := range []struct {
		text string
		year int
		want string
	}{
		{"J60", 2024, "2024-03-01T02:00:00Z"},
		{"59", 2024, "2024-02-29T02:00:00Z"},
		{"J60", 2100, "2100-03-01T02:00:00Z"},
		{"59", 2100, "2100-03-01T02:00:00Z"},
		{"M3.4.4/50", 2024, "2024-03-30T02:00:00Z"},
		{"M3.5.0/-1", 2024, "2024-03-30T23:00:00Z"},
	} {
		p := footerParser{rest: test.text}
		rule, ok := p.rule()
		if !ok || p.rest != "" {
			t.Fatal("valid rule rejected", test.text)
		}
		if got := time.Unix(rule.instant(test.year, 0), 0).UTC().Format(time.RFC3339); got != test.want {
			t.Fatalf("%s: %s, want %s", test.text, got, test.want)
		}
	}
}
