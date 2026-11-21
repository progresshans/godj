package wirejson

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
)

func TestScanAndDecodePreserveDistinctProtocolBudgets(t *testing.T) {
	base := Limits{Bytes: 256, ValueDepth: 2}
	for _, test := range []struct {
		name     string
		document string
		limits   Limits
		want     error
		invalid  bool
	}{
		{"value depth includes scalar", `{"a":{"b":0}}`, base, nil, false},
		{"empty container at value depth", `{"a":{"b":{}}}`, base, nil, false},
		{"value too deep", `{"a":{"b":{"c":0}}}`, base, ErrDepth, true},
		{"container depth excludes scalar", `{"a":{"b":0}}`, Limits{Bytes: 256, Containers: 2}, nil, false},
		{"empty container exceeds depth", `{"a":{"b":{}}}`, Limits{Bytes: 256, Containers: 2}, ErrDepth, true},
		{"exact value budget", `{"a":[]}`, Limits{Bytes: 256, ValueDepth: 2, Values: 2}, nil, false},
		{"value overflow", `{"a":[0]}`, Limits{Bytes: 256, ValueDepth: 2, Values: 2}, ErrResource, true},
		{"object keys", `{"a":0,"b":0}`, Limits{Bytes: 256, ValueDepth: 2, ObjectKeys: 1}, ErrResource, true},
		{"array entries", `{"a":[0,1]}`, Limits{Bytes: 256, ValueDepth: 2, ArrayValues: 1}, ErrResource, true},
		{"decoded string bytes", `{"a":"\ud83d\ude00"}`, Limits{Bytes: 256, ValueDepth: 2, StringBytes: 3}, ErrResource, true},
		{"key bytes", `{"aa":0}`, Limits{Bytes: 256, ValueDepth: 2, KeyBytes: 1}, ErrResource, true},
		{"document bytes", `{"a":0}`, Limits{Bytes: 6, ValueDepth: 2}, ErrResource, true},
		{"duplicate key", `{"a":0,"a":1}`, base, nil, true},
		{"escaped duplicate key", `{"a":0,"\u0061":1}`, base, nil, true},
		{"unpaired high surrogate", `{"a":"\ud800"}`, base, nil, true},
		{"unpaired low surrogate", `{"a":"\udfff"}`, base, nil, true},
		{"invalid UTF8", "{\"a\":\"\xff\"}", base, nil, true},
		{"trailing value", `{} {}`, base, nil, true},
		{"arrays forbidden", `{"a":[]}`, Limits{Bytes: 256, ValueDepth: 2, RejectArrays: true}, nil, true},
		{"null forbidden", `{"a":null}`, Limits{Bytes: 256, ValueDepth: 2, RejectNull: true}, nil, true},
		{"null permitted", `{"a":null}`, base, nil, false},
		{"depth must be bounded", `{}`, Limits{Bytes: 256}, nil, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, decodedErr := DecodeObject([]byte(test.document), test.limits)
			for _, err := range []error{decodedErr, Scan([]byte(test.document), test.limits)} {
				if (err != nil) != test.invalid {
					t.Fatalf("error=%v, want invalid=%v", err, test.invalid)
				}
				if errors.Is(err, ErrResource) != (test.want == ErrResource) || errors.Is(err, ErrDepth) != (test.want == ErrDepth) {
					t.Fatalf("error class=%v, want %v", err, test.want)
				}
			}
		})
	}
}

func TestUintRejectsNoncanonicalAndOverflowingNumbers(t *testing.T) {
	for _, spelling := range []string{"", "00", "01", "-0", "-1", "+1", "1.0", "1e0", " 1", "18446744073709551616"} {
		if _, ok := Uint(json.Number(spelling), math.MaxUint64); ok {
			t.Fatalf("accepted %q", spelling)
		}
	}
	if value, ok := Uint(json.Number("18446744073709551615"), math.MaxUint64); !ok || value != math.MaxUint64 {
		t.Fatal("maximum uint64 rejected")
	}
	if _, ok := Uint("1", 1); ok {
		t.Fatal("string accepted as JSON number")
	}
	if _, ok := Uint(json.Number("2"), 1); ok {
		t.Fatal("protocol maximum ignored")
	}
}
