package sqlite_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/query"
)

func TestSQLiteFieldReferenceComparisonsExecuteWithLiteralParity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend, err := sqlite.OpenMemory(ctx, "field-reference-comparisons-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := backend.Close(); closeErr != nil {
			t.Errorf("Close() error = %v", closeErr)
		}
	})
	if _, err := backend.ExecContext(ctx, `CREATE TABLE "comparison_row" (
		"id" INTEGER PRIMARY KEY,
		"left_value" INTEGER NULL,
		"right_value" INTEGER NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.ExecContext(ctx, `INSERT INTO "comparison_row" VALUES
		(1, NULL, NULL),
		(2, NULL, 1),
		(3, 1, NULL),
		(4, 1, 1),
		(5, 1, 2),
		(6, 2, 1),
		(7, 0, 1)`); err != nil {
		t.Fatal(err)
	}

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	left := query.NewFieldRef("left", "left_value", query.FieldInteger, true)
	right := query.NewFieldRef("right", "right_value", query.FieldInteger, true)
	source := []query.FieldRef{id, left, right}
	projection, err := query.NewProjectionResult(id)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		condition query.Condition
		want      []int64
	}{
		{name: "literal exact", condition: query.NewCondition(left, query.LookupExact, query.Integer(1)), want: []int64{3, 4, 5}},
		{name: "literal greater than", condition: query.NewCondition(left, query.LookupGreaterThan, query.Integer(1)), want: []int64{6}},
		{name: "literal greater than or equal", condition: query.NewCondition(left, query.LookupGreaterThanOrEqual, query.Integer(1)), want: []int64{3, 4, 5, 6}},
		{name: "literal less than", condition: query.NewCondition(left, query.LookupLessThan, query.Integer(1)), want: []int64{7}},
		{name: "literal less than or equal", condition: query.NewCondition(left, query.LookupLessThanOrEqual, query.Integer(1)), want: []int64{3, 4, 5, 7}},
		{name: "field exact", condition: sqliteFieldCondition(t, left, query.LookupExact, right), want: []int64{4}},
		{name: "field greater than", condition: sqliteFieldCondition(t, left, query.LookupGreaterThan, right), want: []int64{6}},
		{name: "field greater than or equal", condition: sqliteFieldCondition(t, left, query.LookupGreaterThanOrEqual, right), want: []int64{4, 6}},
		{name: "field less than", condition: sqliteFieldCondition(t, left, query.LookupLessThan, right), want: []int64{5, 7}},
		{name: "field less than or equal", condition: sqliteFieldCondition(t, left, query.LookupLessThanOrEqual, right), want: []int64{4, 5, 7}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := query.NewPlan("comparison_row", source).
				WithConditions(test.condition).
				WithOrderings(query.NewOrdering(id, query.Ascending))
			plan, err = plan.WithResultShape(projection)
			if err != nil {
				t.Fatal(err)
			}
			got := sqliteFieldReferenceIDs(t, ctx, backend, plan)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("IDs = %v, want %v", got, test.want)
			}
		})
	}
}

func TestSQLiteFieldReferenceNullableNegationParityExecutes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend, err := sqlite.OpenMemory(ctx, "field-reference-negation-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := backend.Close(); closeErr != nil {
			t.Errorf("Close() error = %v", closeErr)
		}
	})
	if _, err := backend.ExecContext(ctx, `CREATE TABLE "comparison_row" (
		"id" INTEGER PRIMARY KEY,
		"left_value" INTEGER NULL,
		"right_value" INTEGER NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.ExecContext(ctx, `INSERT INTO "comparison_row" VALUES
		(1, NULL, NULL),
		(2, NULL, 1),
		(3, 1, NULL),
		(4, 1, 1),
		(5, 1, 2),
		(6, 2, 1),
		(7, 0, 1)`); err != nil {
		t.Fatal(err)
	}

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	left := query.NewFieldRef("left", "left_value", query.FieldInteger, true)
	right := query.NewFieldRef("right", "right_value", query.FieldInteger, true)
	projection, err := query.NewProjectionResult(id)
	if err != nil {
		t.Fatal(err)
	}
	exactField := sqliteFieldCondition(t, left, query.LookupExact, right)
	orderedField := sqliteFieldCondition(t, left, query.LookupGreaterThan, right)
	for _, test := range []struct {
		name      string
		condition query.Condition
		negations int
		want      []int64
	}{
		{name: "field exact odd", condition: exactField, negations: 1, want: []int64{1, 2, 3, 5, 6, 7}},
		{name: "field exact even", condition: exactField, negations: 2, want: []int64{4}},
		{name: "field exact triple", condition: exactField, negations: 3, want: []int64{1, 2, 3, 5, 6, 7}},
		{name: "field ordered odd", condition: orderedField, negations: 1, want: []int64{1, 2, 3, 4, 5, 7}},
		{name: "literal ordered odd", condition: query.NewCondition(left, query.LookupGreaterThan, query.Integer(1)), negations: 1, want: []int64{1, 2, 3, 4, 5, 7}},
	} {
		t.Run(test.name, func(t *testing.T) {
			expression := sqliteTestExpression(t, test.condition)
			for index := 0; index < test.negations; index++ {
				expression = sqliteTestNot(t, expression)
			}
			plan, err := query.NewPlan("comparison_row", []query.FieldRef{id, left, right}).
				WithOrderings(query.NewOrdering(id, query.Ascending)).
				WithWhere(expression)
			if err != nil {
				t.Fatal(err)
			}
			plan, err = plan.WithResultShape(projection)
			if err != nil {
				t.Fatal(err)
			}
			got := sqliteFieldReferenceIDs(t, ctx, backend, plan)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("IDs = %v, want %v", got, test.want)
			}
		})
	}
}

func TestSQLiteFieldReferenceMissingSourceRejectsBeforeIO(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend, err := sqlite.OpenMemory(ctx, "field-reference-pre-io-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	left := query.NewFieldRef("left", "left_value", query.FieldInteger, false)
	foreign := query.NewFieldRef("foreign", "foreign_value", query.FieldInteger, false)
	condition := sqliteFieldCondition(t, left, query.LookupExact, foreign)
	if _, err := backend.Query(ctx, query.NewPlan("missing_table", []query.FieldRef{left}).WithConditions(condition)); !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
		t.Fatalf("Query() error = %v, want query/invalid_plan", err)
	}
	if backend.QueryCount() != 0 {
		t.Fatalf("query count = %d, want 0", backend.QueryCount())
	}
}

func sqliteFieldReferenceIDs(t *testing.T, ctx context.Context, backend *sqlite.Backend, plan query.Plan) []int64 {
	t.Helper()
	rows, err := backend.Query(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Errorf("Close() error = %v", closeErr)
		}
	}()
	var identifiers []int64
	for rows.Next() {
		var identifier int64
		if err := rows.Scan(&identifier); err != nil {
			t.Fatal(err)
		}
		identifiers = append(identifiers, identifier)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return identifiers
}
