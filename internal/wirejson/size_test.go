package wirejson

import (
	"encoding/json"
	"math"
	"testing"
)

func TestSizerMatchesJSONAtExactByteBoundaries(t *testing.T) {
	for _, value := range []any{"", "plain", "<&>\u2028\u2029\n\x00😀", "\xff", int64(math.MinInt64), int64(math.MaxInt64), true, false, []byte(nil), []byte{}, []byte{0, 1, 255}} {
		document, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		for _, maximum := range []int{len(document) - 1, len(document), len(document) + 1} {
			sizer := NewSizer(maximum)
			var accepted bool
			switch value := value.(type) {
			case string:
				accepted = sizer.String(value)
			case int64:
				accepted = sizer.Integer(value)
			case bool:
				accepted = sizer.Boolean(value)
			case []byte:
				accepted = sizer.Bytes(value)
			}
			if accepted != (maximum >= len(document)) || accepted && sizer.Size() != len(document) {
				t.Fatalf("value=%q cap=%d: accepted=%v size=%d", document, maximum, accepted, sizer.Size())
			}
			if !accepted && sizer.Add(0) {
				t.Fatal("failed size measurement resumed")
			}
		}
	}
}

func TestSizerChecksOverflowWithoutLargeAllocations(t *testing.T) {
	for _, maximum := range []int{64 << 20, 96 << 20, math.MaxInt} {
		sizer := NewSizer(maximum)
		if !sizer.Add(maximum-1) || !sizer.Add(1) || sizer.Size() != maximum {
			t.Fatalf("maximum %d rejected", maximum)
		}
		if sizer.Add(1) || sizer.Add(0) || sizer.Size() != maximum {
			t.Fatalf("overflow for maximum %d was not sticky", maximum)
		}
	}
	if NewSizer(-1).Add(0) || NewSizer(1).Add(-1) {
		t.Fatal("negative size accepted")
	}
}
