package sqlite_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/query"
)

func TestSQLiteCompilerRendersLiteralAndFieldComparisonOperators(t *testing.T) {
	t.Parallel()

	left := query.NewFieldRef("left", "left_value", query.FieldInteger, false)
	right := query.NewFieldRef("right", "right_value", query.FieldInteger, false)
	for _, test := range []struct {
		name     string
		lookup   query.Lookup
		operator string
	}{
		{name: "exact", lookup: query.LookupExact, operator: "="},
		{name: "greater than", lookup: query.LookupGreaterThan, operator: ">"},
		{name: "greater than or equal", lookup: query.LookupGreaterThanOrEqual, operator: ">="},
		{name: "less than", lookup: query.LookupLessThan, operator: "<"},
		{name: "less than or equal", lookup: query.LookupLessThanOrEqual, operator: "<="},
	} {
		t.Run(test.name+" literal", func(t *testing.T) {
			t.Parallel()
			plan := query.NewPlan("comparison_row", []query.FieldRef{left, right}).WithConditions(
				query.NewCondition(left, test.lookup, query.Integer(7)),
			)
			statement, arguments, err := sqlite.Compile(plan)
			if err != nil {
				t.Fatalf("Compile() error = %v", err)
			}
			wantSQL := `SELECT "left_value", "right_value" FROM "comparison_row" WHERE "left_value" ` + test.operator + ` ?`
			if statement != wantSQL {
				t.Fatalf("SQL = %q\nwant  %q", statement, wantSQL)
			}
			if want := []any{int64(7)}; !reflect.DeepEqual(arguments, want) {
				t.Fatalf("arguments = %#v, want %#v", arguments, want)
			}
		})

		t.Run(test.name+" field", func(t *testing.T) {
			t.Parallel()
			condition, err := query.NewFieldCondition(left, test.lookup, right)
			if err != nil {
				t.Fatalf("NewFieldCondition() error = %v", err)
			}
			plan := query.NewPlan("comparison_row", []query.FieldRef{left, right}).WithConditions(condition)
			statement, arguments, err := sqlite.Compile(plan)
			if err != nil {
				t.Fatalf("Compile() error = %v", err)
			}
			wantSQL := `SELECT "left_value", "right_value" FROM "comparison_row" WHERE "left_value" ` + test.operator + ` "right_value"`
			if statement != wantSQL {
				t.Fatalf("SQL = %q\nwant  %q", statement, wantSQL)
			}
			if len(arguments) != 0 {
				t.Fatalf("arguments = %#v, want none", arguments)
			}
		})
	}
}

func TestSQLiteFieldReferenceCompilerPreservesMixedDFSArgumentsAcrossResultShapes(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	low := query.NewFieldRef("low", "low_value", query.FieldInteger, false)
	high := query.NewFieldRef("high", "high_value", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	peerTitle := query.NewFieldRef("peer_title", "peer_title", query.FieldString, true)
	source := []query.FieldRef{id, low, high, title, peerTitle}

	highAtLeastLow := sqliteFieldCondition(t, high, query.LookupGreaterThanOrEqual, low)
	titleMatchesPeer := sqliteFieldCondition(t, title, query.LookupExact, peerTitle)
	where := sqliteTestAnd(t,
		sqliteTestExpression(t, query.NewCondition(low, query.LookupGreaterThan, query.Integer(1))),
		sqliteTestExpression(t, highAtLeastLow),
		sqliteTestOr(t,
			sqliteTestExpression(t, titleMatchesPeer),
			sqliteTestExpression(t, query.NewCondition(id, query.LookupLessThanOrEqual, query.Integer(9))),
		),
		sqliteTestExpression(t, query.NewCondition(title, query.LookupLessThan, query.String("z"))),
	)
	base, err := query.NewPlan("comparison_row", source).WithWhere(where)
	if err != nil {
		t.Fatalf("WithWhere() error = %v", err)
	}
	wantWhere := `WHERE ("low_value" > ? AND "high_value" >= "low_value" AND ("title" = "peer_title" OR "id" <= ?) AND "title" < ?)`
	wantArguments := []any{int64(1), int64(9), "z"}

	projection, err := query.NewProjectionResult(id, title)
	if err != nil {
		t.Fatal(err)
	}
	aggregate, err := query.NewAggregateResult(query.CountAllResult(), query.MaxResult(id))
	if err != nil {
		t.Fatal(err)
	}
	projected, err := base.WithResultShape(projection)
	if err != nil {
		t.Fatal(err)
	}
	directAggregate, err := base.WithResultShape(aggregate)
	if err != nil {
		t.Fatal(err)
	}
	derivedAggregate, err := base.WithDistinct().WithResultShape(aggregate)
	if err != nil {
		t.Fatal(err)
	}
	derivedAggregate, err = derivedAggregate.WithLimit(3)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name          string
		plan          query.Plan
		wantArguments []any
	}{
		{name: "model", plan: base, wantArguments: wantArguments},
		{name: "projection", plan: projected, wantArguments: wantArguments},
		{name: "direct aggregate", plan: directAggregate, wantArguments: wantArguments},
		{name: "derived aggregate", plan: derivedAggregate, wantArguments: append(append([]any(nil), wantArguments...), int64(3))},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			statement, arguments, compileErr := sqlite.Compile(test.plan)
			if compileErr != nil {
				t.Fatalf("Compile() error = %v", compileErr)
			}
			if !strings.Contains(statement, wantWhere) {
				t.Fatalf("SQL = %q, want shared predicate %q", statement, wantWhere)
			}
			if !reflect.DeepEqual(arguments, test.wantArguments) {
				t.Fatalf("arguments = %#v, want %#v", arguments, test.wantArguments)
			}
		})
	}
}

func TestSQLiteFieldReferenceCompilerGuardsNullableOperandsAtOddNegationParity(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	left := query.NewFieldRef("left", "left_value", query.FieldInteger, true)
	right := query.NewFieldRef("right", "right_value", query.FieldInteger, true)
	fixed := query.NewFieldRef("fixed", "fixed_value", query.FieldInteger, false)
	source := []query.FieldRef{id, left, right, fixed}

	tests := []struct {
		name      string
		condition query.Condition
		negations int
		wantWhere string
	}{
		{
			name:      "both operands",
			condition: sqliteFieldCondition(t, left, query.LookupExact, right),
			negations: 1,
			wantWhere: `WHERE NOT ("left_value" = "right_value" AND "left_value" IS NOT NULL AND "right_value" IS NOT NULL)`,
		},
		{
			name:      "left operand only",
			condition: sqliteFieldCondition(t, left, query.LookupGreaterThan, fixed),
			negations: 1,
			wantWhere: `WHERE NOT ("left_value" > "fixed_value" AND "left_value" IS NOT NULL)`,
		},
		{
			name:      "right operand only",
			condition: sqliteFieldCondition(t, fixed, query.LookupLessThan, right),
			negations: 1,
			wantWhere: `WHERE NOT ("fixed_value" < "right_value" AND "right_value" IS NOT NULL)`,
		},
		{
			name:      "identical operand deduplicated",
			condition: sqliteFieldCondition(t, left, query.LookupLessThanOrEqual, left),
			negations: 1,
			wantWhere: `WHERE NOT ("left_value" <= "left_value" AND "left_value" IS NOT NULL)`,
		},
		{
			name:      "even parity",
			condition: sqliteFieldCondition(t, left, query.LookupGreaterThanOrEqual, right),
			negations: 2,
			wantWhere: `WHERE NOT (NOT ("left_value" >= "right_value"))`,
		},
		{
			name:      "triple odd parity",
			condition: sqliteFieldCondition(t, left, query.LookupLessThan, right),
			negations: 3,
			wantWhere: `WHERE NOT (NOT (NOT ("left_value" < "right_value" AND "left_value" IS NOT NULL AND "right_value" IS NOT NULL)))`,
		},
		{
			name:      "ordered literal",
			condition: query.NewCondition(left, query.LookupGreaterThan, query.Integer(3)),
			negations: 1,
			wantWhere: `WHERE NOT ("left_value" > ? AND "left_value" IS NOT NULL)`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			expression := sqliteTestExpression(t, test.condition)
			for index := 0; index < test.negations; index++ {
				expression = sqliteTestNot(t, expression)
			}
			plan, err := query.NewPlan("comparison_row", source).WithWhere(expression)
			if err != nil {
				t.Fatalf("WithWhere() error = %v", err)
			}
			statement, arguments, err := sqlite.Compile(plan)
			if err != nil {
				t.Fatalf("Compile() error = %v", err)
			}
			if !strings.Contains(statement, test.wantWhere) {
				t.Fatalf("SQL = %q, want predicate %q", statement, test.wantWhere)
			}
			wantArgumentCount := 0
			if test.name == "ordered literal" {
				wantArgumentCount = 1
			}
			if len(arguments) != wantArgumentCount {
				t.Fatalf("arguments = %#v, want %d", arguments, wantArgumentCount)
			}
		})
	}
}

func TestSQLiteFieldReferenceCompilerBindsRootAliasForRelationProjection(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	peerTitle := query.NewFieldRef("peer_title", "peer_title", query.FieldString, true)
	authorID := query.NewFieldRef("author", "author_id", query.FieldInteger, false)
	targetID := query.NewFieldRef("id", "id", query.FieldInteger, false)
	targetName := query.NewFieldRef("name", "name", query.FieldString, false)
	condition := sqliteFieldCondition(t, title, query.LookupExact, peerTitle)
	plan := query.NewPlan("blog_post", []query.FieldRef{id, title, peerTitle, authorID}).WithConditions(condition)
	plan, err := plan.WithRelationProjection(forwardProjection(t, authorID, targetID, []query.FieldRef{targetID, targetName}))
	if err != nil {
		t.Fatalf("WithRelationProjection() error = %v", err)
	}

	statement, arguments, err := sqlite.Compile(plan)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	want := `SELECT "t0"."id", "t0"."title", "t0"."peer_title", "t0"."author_id", "t1"."id", "t1"."name" FROM "blog_post" AS "t0" INNER JOIN "authors_author" AS "t1" ON "t0"."author_id" = "t1"."id" WHERE "t0"."title" = "t0"."peer_title"`
	if statement != want {
		t.Fatalf("SQL = %q\nwant  %q", statement, want)
	}
	if len(arguments) != 0 {
		t.Fatalf("arguments = %#v, want none", arguments)
	}
}

func TestSQLiteFieldReferenceCompilerRejectsRHSOutsideSource(t *testing.T) {
	t.Parallel()

	left := query.NewFieldRef("left", "left_value", query.FieldInteger, false)
	foreign := query.NewFieldRef("foreign", "foreign_value", query.FieldInteger, false)
	condition := sqliteFieldCondition(t, left, query.LookupExact, foreign)
	statement, arguments, err := sqlite.Compile(
		query.NewPlan("comparison_row", []query.FieldRef{left}).WithConditions(condition),
	)
	if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
		t.Fatalf("Compile() error = %v, want query/invalid_plan", err)
	}
	if statement != "" || arguments != nil {
		t.Fatalf("Compile() = (%q, %#v), want empty output on error", statement, arguments)
	}
}

func sqliteFieldCondition(t *testing.T, left query.FieldRef, lookup query.Lookup, right query.FieldRef) query.Condition {
	t.Helper()
	condition, err := query.NewFieldCondition(left, lookup, right)
	if err != nil {
		t.Fatalf("NewFieldCondition() error = %v", err)
	}
	return condition
}
