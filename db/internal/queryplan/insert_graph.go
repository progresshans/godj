package queryplan

import (
	"slices"

	"github.com/progresshans/godj/query"
)

// InsertGraphRow contains encoded values in the exact scalar field order.
// Dialects own table qualification, parameter syntax and statement boundaries.
type InsertGraphRow struct {
	Insert    query.InsertPlan
	Columns   []string
	Key       string
	KeyIndex  int
	Expected  int64
	Arguments []any
}

func PrepareInsertGraph(plan query.InsertGraphPlan, parameterLimit int, validateValue func(query.FieldRef, query.Value) error, quoteIdentifier func(string) (string, error), columnKey func(string) string, encodeValue func(query.Value) (any, error)) ([]InsertGraphRow, error) {
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	if parameterLimit <= 0 || plan.ValueCount() > parameterLimit {
		return nil, invalidPlan("complete insert graph exceeds the backend parameter limit")
	}
	result := make([]InsertGraphRow, 0, plan.RowCount())
	for _, insert := range plan.Inserts() {
		key, _ := insert.ReturningKey()
		assignments := insert.Assignments()
		fields := make([]query.FieldRef, len(assignments))
		values := make([]query.Value, len(assignments))
		keyIndex := -1
		for index, assignment := range assignments {
			fields[index], values[index] = assignment.Field(), assignment.Value()
			if assignment.Field() == key {
				keyIndex = index
			}
		}
		bulk, err := query.NewBulkInsertPlan(insert.Table(), fields, [][]query.Value{values}, key, query.BulkConflict{})
		if err != nil {
			return nil, err
		}
		parts, err := PrepareBulkInsert(bulk, parameterLimit, validateValue, quoteIdentifier, columnKey, encodeValue)
		if err != nil {
			return nil, err
		}
		if keyIndex < 0 {
			return nil, invalidPlan("insert graph has no explicit primary key")
		}
		expected, ok := values[keyIndex].Integer()
		if !ok {
			return nil, invalidPlan("insert graph primary key is not an integer")
		}
		result = append(result, InsertGraphRow{Insert: insert, Columns: parts.Columns, Key: parts.Key,
			KeyIndex: keyIndex, Expected: expected, Arguments: parts.Arguments})
	}
	return result, nil
}

// ValidateInsertGraphKeys never substitutes expected keys for native results.
// The same check applies to RETURNING/trigger receipts and post-statement rows.
func ValidateInsertGraphKeys(rows []InsertGraphRow, keys []int64) error {
	if len(rows) != len(keys) {
		return returningRowCount(len(rows), len(rows), len(keys))
	}
	expected := make([]int64, len(rows))
	for index, row := range rows {
		expected[index] = row.Expected
	}
	if !slices.Equal(keys, expected) {
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeUnexpectedRows,
			Detail: "native insert graph did not preserve every requested primary key"}
	}
	return nil
}
