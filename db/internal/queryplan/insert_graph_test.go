package queryplan

import (
	"errors"
	"testing"

	"github.com/progresshans/godj/query"
)

func TestInsertGraphKeysRequireActualCompleteOrderedResults(t *testing.T) {
	rows := []InsertGraphRow{{Expected: -3}, {Expected: 0}, {Expected: 9007199254740993}}
	for _, test := range []struct {
		name string
		keys []int64
		ok   bool
	}{
		{"exact", []int64{-3, 0, 9007199254740993}, true},
		{"empty", nil, false},
		{"partial", []int64{-3, 0}, false},
		{"extra", []int64{-3, 0, 9007199254740993, 4}, false},
		{"reordered", []int64{0, -3, 9007199254740993}, false},
		{"duplicate", []int64{-3, -3, 9007199254740993}, false},
		{"rounded", []int64{-3, 0, 9007199254740992}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateInsertGraphKeys(rows, test.keys)
			if test.ok && err != nil || !test.ok && !errors.Is(err, &query.Error{Code: query.CodeUnexpectedRows}) {
				t.Fatal("native result validation", err)
			}
		})
	}
}
