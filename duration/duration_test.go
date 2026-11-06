package duration_test

import (
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/progresshans/godj/duration"
)

func TestSignedMicrosecondDurationCanonicalEndpoints(t *testing.T) {
	for _, test := range []struct {
		microseconds int64
		text         string
	}{
		{0, "00:00:00"}, {1, "00:00:00.000001"}, {-1, "-1 23:59:59.999999"},
		{86400000000, "1 00:00:00"}, {-86400000000, "-1 00:00:00"},
		{math.MaxInt64, "106751991 04:00:54.775807"}, {math.MinInt64, "-106751992 19:59:05.224192"},
	} {
		value := duration.FromMicroseconds(test.microseconds)
		if value.String() != test.text {
			t.Fatalf("duration %d = %s, want %s", test.microseconds, value, test.text)
		}
		parsed, err := duration.Parse(test.text)
		if err != nil || parsed != value {
			t.Fatalf("duration round trip: %v", err)
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(encoded, &parsed); err != nil || parsed != value {
			t.Fatal("duration JSON lost precision")
		}
	}
	for _, text := range []string{"106751991 04:00:54.775808", "-106751992 19:59:05.224191"} {
		if value, err := duration.Parse(text); err != nil {
			t.Fatal(err)
		} else if _, err := value.TotalMicroseconds(); !errors.Is(err, duration.ErrMicrosecondRange) {
			t.Fatal("backend duration range was not distinguished from model range")
		}
	}
	seed := uint64(0x91cb843fea07259d)
	for index := 0; index < 10000; index++ {
		seed = seed*6364136223846793005 + 1442695040888963407
		value := duration.FromMicroseconds(int64(seed))
		if micros, err := value.TotalMicroseconds(); err != nil || micros != int64(seed) {
			t.Fatal("int64 conversion corrupted signed duration")
		}
		parsed, err := duration.Parse(value.String())
		if err != nil || parsed != value {
			t.Fatalf("signed domain round trip: %v %v", value, err)
		}
	}
}

func TestDurationDecodePreservesValueOnFailureAndDistinguishesNull(t *testing.T) {
	value := duration.FromMicroseconds(1)
	for _, text := range []string{`null`, `0`, `false`, `"0 00:00:00"`, `"-0 00:00:00"`, `"+1 00:00:00"`, `"01 00:00:00"`, `"-00:00:00.000001"`, `"00:00:00.000000"`, `"PT1S"`, `"00:00"`, `"1 24:00:00"`, `"1000000000 00:00:00"`} {
		before := value
		if err := json.Unmarshal([]byte(text), &value); err == nil || value != before {
			t.Fatal("failed decode published a duration")
		}
	}
	var nullable *duration.Duration
	if err := json.Unmarshal([]byte(`null`), &nullable); err != nil || nullable != nil {
		t.Fatal("null became zero duration")
	}
	if err := json.Unmarshal([]byte(`"00:00:00"`), &nullable); err != nil || nullable == nil || *nullable != (duration.Duration{}) {
		t.Fatal("zero duration lost presence")
	}
	var absent *duration.Duration
	if absent.UnmarshalText([]byte("00:00:00")) == nil || absent.UnmarshalJSON([]byte(`"00:00:00"`)) == nil {
		t.Fatal("nil receiver accepted")
	}
}

func TestDurationModelRangeAndNormalizationAreIndependentOfStorage(t *testing.T) {
	for _, text := range []string{"999999999 23:59:59.999999", "-999999999 00:00:00"} {
		value, err := duration.Parse(text)
		if err != nil || !value.Valid() || value.String() != text {
			t.Fatal("full model duration range lost")
		}
		if _, err := value.TotalMicroseconds(); !errors.Is(err, duration.ErrMicrosecondRange) {
			t.Fatal("wide model duration acquired an int64 representation")
		}
	}
	for _, value := range []duration.Duration{{Days: 1000000000}, {Days: -1000000000}, {Microseconds: -1}, {Microseconds: duration.MicrosecondsPerDay}} {
		if value.Valid() {
			t.Fatal("non-normalized literal accepted")
		}
		if _, err := json.Marshal(value); !errors.Is(err, duration.ErrInvalid) {
			t.Fatal("invalid duration encoded")
		}
	}
	for _, test := range []struct {
		days, micros int64
		want         duration.Duration
	}{
		{0, -1, duration.Duration{Days: -1, Microseconds: duration.MicrosecondsPerDay - 1}},
		{1, -duration.MicrosecondsPerDay, duration.Duration{}},
		{-1000000000, duration.MicrosecondsPerDay, duration.Duration{Days: duration.MinDays}},
		{1000000000, -1, duration.Duration{Days: duration.MaxDays, Microseconds: duration.MicrosecondsPerDay - 1}},
	} {
		value, err := duration.New(test.days, test.micros)
		if err != nil || value != test.want {
			t.Fatal("duration component normalization changed")
		}
	}
	for _, days := range []int64{math.MinInt64, math.MaxInt64} {
		if _, err := duration.New(days, math.MaxInt64); !errors.Is(err, duration.ErrRange) {
			t.Fatal("day overflow accepted")
		}
	}
}
