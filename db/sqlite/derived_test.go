package sqlite

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/internal/querytest"
	"github.com/progresshans/godj/query"
)

func TestCompileDerivedResults(t *testing.T) { querytest.DerivedCompiler(t, Compile, false) }

func TestCompileResultPages(t *testing.T) { querytest.ResultPageCompiler(t, Compile, false) }

func TestResultPageKeepsNativeDuplicatesHiddenOrderingAndEmptyTotal(t *testing.T) {
	backend, err := Open(t.Context(), "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "result_page.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := backend.ExecContext(t.Context(), `CREATE TABLE "GODJ_PAGE_ROWS" ("godj_page_slice" INTEGER, "value" INTEGER); INSERT INTO "GODJ_PAGE_ROWS" VALUES (1,1),(2,7),(3,7),(4,NULL)`); err != nil {
		t.Fatal(err)
	}
	id := query.NewFieldRef("id", "godj_page_slice", query.FieldInteger, false)
	value := query.NewFieldRef("value", "value", query.FieldInteger, true)
	shape, err := query.NewProjectionResult(query.FieldResult(value))
	if err != nil {
		t.Fatal(err)
	}
	input, err := query.NewPlan("GODJ_PAGE_ROWS", []query.FieldRef{id, value}).WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	input = input.WithOrderings(query.NewOrdering(id, query.Descending))
	for _, test := range []struct {
		name                 string
		limit, offset, count int
	}{{"duplicates", 2, 1, 2}, {"zero_limit", 0, 0, 0}, {"past_end", 2, 8, 0}} {
		t.Run(test.name, func(t *testing.T) {
			plan, err := query.NewPagePlan(input, test.limit, test.offset)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := backend.Query(t.Context(), plan)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			seen, presentRows := 0, 0
			for rows.Next() {
				seen++
				var total int64
				var present, value sql.NullInt64
				if err := rows.Scan(&total, &present, &value); err != nil {
					t.Fatal(err)
				}
				if total != 4 {
					t.Fatal("page changed duplicate cardinality", total)
				}
				if test.count == 0 {
					if present.Valid || value.Valid {
						t.Fatal("empty page lost sentinel", present, value)
					}
				} else {
					if !present.Valid || present.Int64 != 1 || !value.Valid || value.Int64 != 7 {
						t.Fatal("private order changed or leaked", present, value)
					}
					presentRows++
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			if seen != max(1, test.count) || presentRows != test.count {
				t.Fatal("page row count", seen, presentRows)
			}
		})
	}
}

func TestDerivedResultsKeepNativeStageOrderAndEmptyAggregate(t *testing.T) {
	backend, err := Open(t.Context(), "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "derived.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := backend.ExecContext(t.Context(), `CREATE TABLE "GODJ_RESULTS" ("godj_results_0" INTEGER); INSERT INTO "GODJ_RESULTS" VALUES (1),(2),(3),(NULL)`); err != nil {
		t.Fatal(err)
	}
	field := query.NewFieldRef("amount", "godj_results_0", query.FieldInteger, true)
	shape, err := query.NewProjectionResult(query.FieldResult(field))
	if err != nil {
		t.Fatal(err)
	}
	input, err := query.NewPlan("GODJ_RESULTS", []query.FieldRef{field}).WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	input = input.WithOrderings(query.NewOrdering(field, query.Descending))
	input, err = input.WithLimit(2)
	if err != nil {
		t.Fatal(err)
	}
	plan, scope, err := query.NewDerivedPlan(input)
	if err != nil {
		t.Fatal(err)
	}
	value := querytest.DerivedColumn(t, scope, 0)
	condition, err := query.NewScalarCondition(value, query.LookupLessThan, query.Integer(2))
	if err != nil {
		t.Fatal(err)
	}
	plan = querytest.Conditions(t, plan, condition)
	rows, err := backend.Query(t.Context(), plan)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("outer filter moved before input LIMIT")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	outer, outputScope, err := query.NewDerivedPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := query.ScalarResult(querytest.DerivedColumn(t, outputScope, 0))
	if err != nil {
		t.Fatal(err)
	}
	sum, err := query.SumResult(selected)
	if err != nil {
		t.Fatal(err)
	}
	aggregate, err := query.NewAggregateResult(query.CountAllResult(), sum)
	if err != nil {
		t.Fatal(err)
	}
	outer, err = outer.WithResultShape(aggregate)
	if err != nil {
		t.Fatal(err)
	}
	rows, err = backend.Query(t.Context(), outer)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("empty derived input lost aggregate row", rows.Err())
	}
	var count int64
	var total sql.NullInt64
	if err := rows.Scan(&count, &total); err != nil {
		t.Fatal(err)
	}
	if count != 0 || total.Valid || rows.Next() {
		t.Fatal("empty derived aggregate changed COUNT/NULL", count, total)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDerivedGroupPageCannotCaptureAnInputSource(t *testing.T) {
	backend, err := Open(t.Context(), "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "page.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := backend.ExecContext(t.Context(), `CREATE TABLE "godj_groups" ("godj_page" INTEGER); INSERT INTO "godj_groups" VALUES (1),(1),(2),(NULL)`); err != nil {
		t.Fatal(err)
	}
	field := query.NewFieldRef("rank", "godj_page", query.FieldInteger, true)
	selection, err := query.NewProjectionResult(query.FieldResult(field))
	if err != nil {
		t.Fatal(err)
	}
	input, err := query.NewPlan("godj_groups", []query.FieldRef{field}).WithResultShape(selection)
	if err != nil {
		t.Fatal(err)
	}
	plan, scope, err := query.NewDerivedPlan(input)
	if err != nil {
		t.Fatal(err)
	}
	key, err := query.ScalarResult(querytest.DerivedColumn(t, scope, 0))
	if err != nil {
		t.Fatal(err)
	}
	shape, err := query.NewGroupedResult([]query.ResultExpression{key}, []query.ResultExpression{query.CountAllResult()})
	if err != nil {
		t.Fatal(err)
	}
	plan, err = plan.WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = plan.WithLimit(0)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = plan.WithGroupMode(query.GroupPage)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := backend.Query(t.Context(), plan)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("empty page lost its totals", rows.Err())
	}
	var total int64
	var present, rank, count sql.NullInt64
	if err := rows.Scan(&total, &present, &rank, &count); err != nil {
		t.Fatal(err)
	}
	if total != 3 || present.Valid || rank.Valid || count.Valid || rows.Next() {
		t.Fatal("page did not preserve NULL group or zero limit", total, present, rank, count)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDerivedResultsPreserveMissingSourceError(t *testing.T) {
	backend, err := Open(t.Context(), "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "missing.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	field := query.NewFieldRef("id", "id", query.FieldInteger, false)
	shape, err := query.NewProjectionResult(query.FieldResult(field))
	if err != nil {
		t.Fatal(err)
	}
	input, err := query.NewPlan("missing_derived_source", []query.FieldRef{field}).WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := query.NewDerivedPlan(input)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := query.NewDerivedPlan(first)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := backend.Query(t.Context(), second)
	if rows != nil {
		rows.Close()
		t.Fatal("missing source returned rows")
	}
	if !errors.Is(err, &query.Error{Code: query.CodeMissingTable}) {
		t.Fatal("derived result lost physical source classification", err)
	}
}
