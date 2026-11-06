package queryplan_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strconv"
	"testing"

	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/query"
)

type updateResult struct {
	count int64
	err   error
}

func (r updateResult) LastInsertId() (int64, error) { return 0, errors.New("not an insert") }
func (r updateResult) RowsAffected() (int64, error) { return r.count, r.err }

type updateExecutor struct {
	calls  int
	result sql.Result
	err    error
	cancel context.CancelFunc
}

func (e *updateExecutor) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	e.calls++
	if e.cancel != nil {
		e.cancel()
	}
	return e.result, e.err
}

func TestBulkUpdateReservesPredicateBudgetAndBindsCompiledSource(t *testing.T) {
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	amount := query.NewFieldRef("amount", "amount", query.FieldInteger, false)
	base := query.NewPlan("items", []query.FieldRef{id, amount})
	spec, err := query.NewBulkUpdateSpec(base, []query.FieldRef{amount}, id)
	if err != nil {
		t.Fatal(err)
	}
	quote := func(value string) (string, error) { return `"` + value + `"`, nil }
	selection := func(query.Plan) (queryplan.UpdateSource, error) {
		return queryplan.UpdateSource{Selection: `SELECT "id" FROM "items" WHERE "amount" IN (?,?,?,?)`, Arguments: []any{1, 2, 3, 4}}, nil
	}
	parts, err := queryplan.PrepareBulkUpdate(spec, 10, selection, quote, quote, nil)
	if err != nil || parts.BatchSize != 3 {
		t.Fatal("predicate budget was not reserved", parts.BatchSize, err)
	}
	for _, limit := range []int{0, 1, 4, 5} {
		if _, err := queryplan.PrepareBulkUpdate(spec, limit, selection, quote, quote, nil); err == nil {
			t.Fatal("exhausted parameter budget admitted", limit)
		}
	}
	for _, mode := range []string{"too_many_rows", "changed_source"} {
		t.Run(mode, func(t *testing.T) {
			actual := spec
			keys := []int64{1, 2, 3, 4}
			rows := [][]query.Value{{query.Integer(1)}, {query.Integer(2)}, {query.Integer(3)}, {query.Integer(4)}}
			if mode == "changed_source" {
				source, err := base.WithConditions(query.NewCondition(amount, query.LookupExact, query.Integer(7)))
				if err != nil {
					t.Fatal(err)
				}
				actual, err = query.NewBulkUpdateSpec(source, []query.FieldRef{amount}, id)
				if err != nil {
					t.Fatal(err)
				}
				keys, rows = keys[:1], rows[:1]
			}
			plan, err := query.NewBulkUpdatePlan(actual, keys, rows)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			statement, args, err := queryplan.CompileBulkUpdate(plan, parts, func(query.FieldRef, query.Value) error { calls++; return nil }, func(query.Value) (any, error) { calls++; return nil, nil }, func(index int) string { return "$" + strconv.Itoa(index) })
			if err == nil || statement != "" || args != nil || calls != 0 {
				t.Fatal("foreign prepared source or excessive batch consumed values", statement, args, calls, err)
			}
		})
	}
	parts, err = queryplan.PrepareBulkUpdate(spec, 1<<30, selection, quote, quote, nil)
	if err != nil || parts.BatchSize != query.MaximumBulkValues/2 {
		t.Fatal("native budget escaped portable input bound", parts.BatchSize, err)
	}
	if _, err = queryplan.PrepareBulkUpdate(spec, 10, func(query.Plan) (queryplan.UpdateSource, error) {
		return queryplan.UpdateSource{}, errors.New("uncompilable predicate")
	}, quote, quote, nil); err == nil {
		t.Fatal("invalid source got a batch limit")
	}
}

func TestBulkUpdateDiscardsUnconfirmedOrImpossibleCounts(t *testing.T) {
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	amount := query.NewFieldRef("amount", "amount", query.FieldInteger, false)
	source := query.NewPlan("items", []query.FieldRef{id, amount})
	spec, err := query.NewBulkUpdateSpec(source, []query.FieldRef{amount}, id)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := query.NewBulkUpdatePlan(spec, []int64{1, 1, 2}, [][]query.Value{{query.Integer(11)}, {query.Integer(12)}, {query.Integer(22)}})
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("native write or result failure")
	for _, mode := range []string{"two", "zero", "one", "negative", "too_many", "nil", "metadata_error", "execution_error", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			executor := &updateExecutor{result: driver.RowsAffected(2)}
			want := int64(0)
			switch mode {
			case "two":
				want = 2
			case "one":
				want = 1
				executor.result = driver.RowsAffected(1)
			case "zero":
				executor.result = driver.RowsAffected(0)
			case "negative":
				executor.result = driver.RowsAffected(-1)
			case "too_many":
				executor.result = driver.RowsAffected(3)
			case "nil":
				executor.result = nil
			case "metadata_error":
				executor.result = updateResult{count: 2, err: failure}
			case "execution_error":
				executor.err = failure
			case "canceled":
				executor.cancel = cancel
			}
			count, err := queryplan.ExecuteBulkUpdate(ctx, executor, plan, "authored UPDATE", nil, func(err error) error { return err })
			if count != want || executor.calls != 1 {
				t.Fatal(count, want, executor.calls, err)
			}
			if mode == "two" || mode == "one" || mode == "zero" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("unconfirmed count became success")
			}
			if (mode == "execution_error" || mode == "metadata_error") && !errors.Is(err, failure) {
				t.Fatal("native cause lost", err)
			}
			if mode == "execution_error" && err != failure {
				t.Fatal("confirmed native error unnecessarily wrapped")
			}
			if mode == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost", err)
			}
		})
	}
	condition, err := query.NewInCondition(id, nil)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := source.WithConditions(condition)
	if err != nil {
		t.Fatal(err)
	}
	emptySpec, err := query.NewBulkUpdateSpec(empty, []query.FieldRef{amount}, id)
	if err != nil {
		t.Fatal(err)
	}
	emptyPlan, err := query.NewBulkUpdatePlan(emptySpec, plan.Keys(), plan.Rows())
	if err != nil {
		t.Fatal(err)
	}
	executor := &updateExecutor{err: failure}
	if count, err := queryplan.ExecuteBulkUpdate(t.Context(), executor, emptyPlan, "validated empty UPDATE", nil, func(err error) error { return err }); count != 0 || err != nil || executor.calls != 0 {
		t.Fatal("known-empty predicate performed a write", count, err)
	}
}
