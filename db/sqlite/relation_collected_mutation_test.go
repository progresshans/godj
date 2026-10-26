package sqlite

import (
	"errors"
	"github.com/progresshans/godj/query"
	"reflect"
	"testing"
)

func TestSQLiteCollectedSetNullTargetsExactRowsAndRejectsInvalidKeys(t *testing.T) {
	fk := query.NewFieldRef("owner", "owner_id", query.FieldInteger, true)
	pk := query.NewFieldRef("id", "record_id", query.FieldInteger, false)
	plan := query.NewRelationSetNullRowsPlan("child", fk, query.Integer(0), pk, []int64{-1, 0, 5})
	statement, arguments, err := compileRelationSetNull(plan)
	if err != nil || statement != `UPDATE "child" SET "owner_id" = NULL WHERE "owner_id" = ? AND "record_id" IN (?, ?, ?)` || !reflect.DeepEqual(arguments, []any{int64(0), int64(-1), int64(0), int64(5)}) {
		t.Fatal("collected key and FK predicates were not both parameterized", statement, arguments, err)
	}
	cases := []struct {
		name    string
		primary query.FieldRef
		keys    []int64
	}{
		{"empty_name", query.NewFieldRef("", "record_id", query.FieldInteger, false), []int64{1}},
		{"nul_name", query.NewFieldRef("id\x00", "record_id", query.FieldInteger, false), []int64{1}},
		{"empty_column", query.NewFieldRef("id", "", query.FieldInteger, false), []int64{1}},
		{"nul_column", query.NewFieldRef("id", "record_id\x00", query.FieldInteger, false), []int64{1}},
		{"text_key", query.NewFieldRef("id", "record_id", query.FieldString, false), []int64{1}},
		{"nullable_key", query.NewFieldRef("id", "record_id", query.FieldInteger, true), []int64{1}},
		{"same_name", query.NewFieldRef("owner", "record_id", query.FieldInteger, false), []int64{1}},
		{"same_column", query.NewFieldRef("id", "owner_id", query.FieldInteger, false), []int64{1}},
		{"nil_keys", pk, nil}, {"empty_keys", pk, []int64{}}, {"duplicate_keys", pk, []int64{1, 1}},
		{"excess_keys", pk, make([]int64, query.MaximumRelationSetNullRows+1)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			plan := query.NewRelationSetNullRowsPlan("child", fk, query.Integer(0), test.primary, test.keys)
			statement, arguments, err := compileRelationSetNull(plan)
			if statement != "" || arguments != nil || !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
				t.Fatal("invalid collected plan reached a statement", statement, arguments, err)
			}
		})
	}
}
