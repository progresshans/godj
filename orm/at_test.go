package orm

import (
	"context"
	"math"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

func TestAtUsesEffectiveOffsetWithoutWideningSourceSlice(t *testing.T) {
	for _, test := range []struct {
		name                                string
		offset, limit, index                int
		wantOffset, wantLimit, wantPosition int
		wantID                              int64
	}{
		{"indexed", 2, 4, 1, 3, 1, 1, 4},
		{"outside source limit", 2, 1, 1, 2, 0, 0, 0},
		{"maximum offset", math.MaxInt32, 4, 1, math.MaxInt32, 2, 2, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			var issued *cacheTestRows
			backend := &cacheTestBackend{query: func(_ int, _ context.Context, plan query.Plan) (db.Rows, error) {
				offset, _ := plan.Offset()
				limit, _ := plan.Limit()
				if offset != test.wantOffset || limit != test.wantLimit || !plan.Distinct() {
					t.Fatalf("At plan offset=%d limit=%d distinct=%v", offset, limit, plan.Distinct())
				}
				issued = rowsForIDs(1, 2, 3, 4, 5, 6)
				if offset != math.MaxInt32 {
					issued.values = issued.values[offset:]
				}
				issued.values = issued.values[:min(limit, len(issued.values))]
				return issued, nil
			}}
			metadata := (cacheTestDescriptor{}).Metadata()
			querySet := newCacheTestManager().Using(backend).OrderBy(NewIntegerField[cacheTestModel](metadata.Fields[0]).Asc()).Distinct()
			querySet, _ = querySet.Offset(test.offset)
			querySet, _ = querySet.Limit(test.limit)
			original := querySet.Plan()
			value, found, err := querySet.At(t.Context(), test.index)
			if err != nil || found != (test.wantID != 0) || value.ID != test.wantID || issued.position != test.wantPosition || issued.closeCalls.Load() != 1 {
				t.Fatalf("At = %+v, %v, %v; rows=%+v", value, found, err, issued)
			}
			if !original.Equal(querySet.Plan()) {
				t.Fatal("At mutated the source plan")
			}
			if _, ready := querySet.evaluation.cachedValues(); ready {
				t.Fatal("At populated the full result cache")
			}
		})
	}
}
