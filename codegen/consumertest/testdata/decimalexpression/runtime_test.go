package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"example.com/godj-project-bundle/ledger"
	"example.com/godj-project-bundle/project"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type decimalReadProbe struct {
	decimalBackend
	mu     sync.Mutex
	plans  []query.Plan
	closed atomic.Int64
	fault  string
}

func (p *decimalReadProbe) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	p.mu.Lock()
	p.plans = append(p.plans, plan)
	p.mu.Unlock()
	rows, err := p.decimalBackend.Query(ctx, plan)
	if err != nil {
		return rows, err
	}
	return &decimalRows{Rows: rows, owner: p}, nil
}
func (p *decimalReadProbe) calls() int { p.mu.Lock(); defer p.mu.Unlock(); return len(p.plans) }

type decimalRows struct {
	db.Rows
	owner *decimalReadProbe
}

func (rows *decimalRows) Scan(values ...any) error {
	if rows.owner.fault == "scan" {
		return errors.New("decimal scan failure")
	}
	return rows.Rows.Scan(values...)
}
func (rows *decimalRows) Err() error {
	if rows.owner.fault == "iteration" {
		return errors.Join(rows.Rows.Err(), errors.New("decimal iteration failure"))
	}
	return rows.Rows.Err()
}
func (rows *decimalRows) Close() error {
	rows.owner.closed.Add(1)
	err := rows.Rows.Close()
	if rows.owner.fault == "close" {
		return errors.Join(err, errors.New("decimal close failure"))
	}
	return err
}

func assertDecimalJSON(t *testing.T, actual any, expected string) {
	t.Helper()
	data, err := json.Marshal(actual)
	check(t, err)
	var left, right any
	check(t, json.Unmarshal(data, &left))
	check(t, json.Unmarshal([]byte(expected), &right))
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("decimal result = %s, want %s", data, expected)
	}
}
func nativeDecimalError(t *testing.T, vendor string, err error, postgresState string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected decimal failure")
	}
	if vendor == "postgres" {
		var native *pgconn.PgError
		if !errors.As(err, &native) || native.Code != postgresState {
			t.Fatal("native Decimal SQLSTATE lost", err)
		}
	} else if !errors.Is(err, &query.Error{Code: query.CodeInvalidValue}) {
		t.Fatal("exact Decimal error classification lost", err)
	}
}

func rollbackDecimal(t *testing.T, backend decimalBackend, callback func(db.Session)) {
	t.Helper()
	rollback := errors.New("test-owned decimal rollback")
	err := backend.Atomic(t.Context(), func(session db.Session) error { callback(session); return rollback })
	if !errors.Is(err, rollback) {
		t.Fatal("decimal rollback receipt lost", err)
	}
}

func TestDecimalRuntime(t *testing.T) {
	withDecimalBackends(t, func(t *testing.T, backend decimalBackend, vendor string, physical decimalPhysical) {
		seed := seedDecimal(t, backend)
		before := decimalStorage(t, backend)
		nativeBefore := physical.Snapshot()
		relations, err := project.BindRelations()
		check(t, err)
		f := ledger.EntryFields
		amount := orm.Decimal(orm.F(f.Amount))
		cost := amount.Multiply(d("1.25")).Add(d("0.5"))
		t.Run("typed_dynamic_and_exact_values", func(t *testing.T) {
			probe := &decimalReadProbe{decimalBackend: backend}
			base := ledger.EntryObjects.Using(probe).OrderBy(f.Code.Asc())
			typed, err := orm.SelectInto(t.Context(), base, decimalProjection(cost))
			check(t, err)
			api, err := project.Using(probe)
			check(t, err)
			value, err := api.LedgerEntry.Expression(orm.DynamicF("amount").Multiply(d("1.25")).Add(d("0.5")))
			check(t, err)
			dynamic, err := project.SelectLedgerEntryInto(t.Context(), api.LedgerEntry.OrderBy(f.Code.Asc()),
				orm.Project2(f.Code, value, func(code string, value query.Value) map[string]any {
					return map[string]any{"code": code, "value": dynamicDecimal(value)}
				}))
			check(t, err)
			if !reflect.DeepEqual(typed, dynamic) || !probe.plans[0].Equal(probe.plans[1]) {
				t.Fatal("decimal typed/dynamic AST or decoder diverged", typed, dynamic)
			}
			assertDecimalJSON(t, typed, `[{"code":"a","value":"16.125"},{"code":"b","value":"-8.5625"},{"code":"c","value":null},{"code":"d","value":"0.5"},{"code":"e","value":"16.125"},{"code":"f","value":"125.5"}]`)
			for _, name := range []string{"add", "subtract", "negate", "related_arithmetic", "reverse_arithmetic", "comparison_rhs", "order_page"} {
				_, err := observeDecimal(t, backend, name)
				check(t, err)
			}
			values, err := observeDecimal(t, backend, "large_literal")
			check(t, err)
			assertDecimalJSON(t, values, `[{"code":"a","value":"9007199254740993.1"}]`)
			for _, operand := range []any{int64(1), float64(1), "1"} {
				if _, err := api.LedgerEntry.Expression(orm.DynamicF("amount").Add(operand)); err == nil {
					t.Fatal("implicit Decimal conversion accepted", operand)
				}
			}
		})
		t.Run("aggregates_having_and_result_stages", func(t *testing.T) {
			values, err := observeDecimal(t, backend, "computed_aggregate")
			check(t, err)
			assertDecimalJSON(t, values, `{"total":"149.6875","average":"29.9375","minimum":"-8.5625","maximum":"125.5","present":5}`)
			values, err = observeDecimal(t, backend, "distinct_filtered_aggregate")
			check(t, err)
			assertDecimalJSON(t, values, `{"total":"141.625","average":"70.8125"}`)
			for _, name := range []string{"empty_aggregate", "all_null_aggregate"} {
				values, err := observeDecimal(t, backend, name)
				check(t, err)
				assertDecimalJSON(t, values, `{"total":null,"average":null,"minimum":null,"maximum":null,"present":0}`)
			}
			values, err = observeDecimal(t, backend, "groups_having")
			check(t, err)
			assertDecimalJSON(t, values, `[{"bucket":"first","total":"32.25","average":"16.125"},{"bucket":"second","total":"125.5","average":"125.5"}]`)
			key := amount.Multiply(d("0"))
			keyed, err := orm.GroupBy(ledger.EntryObjects.Using(backend), orm.Project1(key, func(value orm.Optional[decimal.Decimal]) any { return optionalDecimal(value) }), orm.Aggregate1(orm.CountRows[ledger.Entry](), func(count int64) int64 { return count }), func(key any, count int64) map[string]any { return map[string]any{"key": key, "count": count} })
			check(t, err)
			keyRows, err := keyed.OrderBy(key.Asc().NullsFirst()).All(t.Context())
			check(t, err)
			assertDecimalJSON(t, keyRows, `[{"key":null,"count":1},{"key":"0","count":5}]`)
			probe := &decimalReadProbe{decimalBackend: backend}
			api, err := project.Using(probe)
			check(t, err)
			dynamic, err := api.LedgerEntry.Expression(orm.DynamicF("amount").Multiply(d("1.25")).Add(d("0.5")))
			check(t, err)
			typed, err := orm.AggregateInto(t.Context(), ledger.EntryObjects.Using(probe), orm.Aggregate2(orm.Sum(cost), orm.Avg(cost), func(a, b orm.Optional[decimal.Decimal]) []any { return []any{optionalDecimal(a), optionalDecimal(b)} }))
			check(t, err)
			untyped, err := project.AggregateLedgerEntryInto(t.Context(), api.LedgerEntry, orm.Aggregate2(dynamic.Sum(), dynamic.Avg(), func(a, b query.Value) []any { return []any{dynamicDecimal(a), dynamicDecimal(b)} }))
			check(t, err)
			if !reflect.DeepEqual(typed, untyped) || !probe.plans[0].Equal(probe.plans[1]) {
				t.Fatal("computed Decimal aggregate AST diverged")
			}
			selected, err := orm.SelectQuery(ledger.EntryObjects.Using(probe).OrderBy(f.Code.Asc()), decimalProjection(cost))
			check(t, err)
			stage, err := orm.Derive(selected)
			check(t, err)
			code := orm.SelectedField(stage, f.Code)
			computed := orm.Decimal(orm.OptionalOperand(orm.SelectedField(stage, cost))).Add(d("1"))
			result, err := orm.SelectResults(stage, orm.Project2(code, computed, func(code string, value orm.Optional[decimal.Decimal]) map[string]any {
				return map[string]any{"code": code, "value": optionalDecimal(value)}
			}))
			check(t, err)
			result = result.Filter(computed.GreaterThan(d("16"))).OrderBy(computed.Desc(), code.Asc())
			start := probe.calls()
			page, err := result.Page(t.Context(), 2, 1)
			check(t, err)
			if page.Total != 3 || probe.calls() != start+1 {
				t.Fatal("computed decimal page lost one-statement total", page)
			}
			assertDecimalJSON(t, page.Rows, `[{"code":"a","value":"17.125"},{"code":"e","value":"17.125"}]`)
			past, err := result.Page(t.Context(), 2, 9)
			check(t, err)
			if past.Total != 3 || past.Rows == nil || len(past.Rows) != 0 {
				t.Fatal("decimal past-end page lost total", past)
			}
			predicate, err := stage.Predicate(orm.DynamicColumn(1).Add(d("1")).GreaterThan(d("16")))
			check(t, err)
			predicateRows, err := result.Filter(predicate).All(t.Context())
			check(t, err)
			if len(predicateRows) != 3 {
				t.Fatal("dynamic selected Decimal arithmetic", predicateRows)
			}
			other, err := orm.Derive(selected)
			check(t, err)
			foreign := orm.Decimal(orm.OptionalOperand(orm.SelectedField(other, cost)))
			start = probe.calls()
			if rows, err := result.Filter(orm.ExactExpressions(computed, foreign)).All(t.Context()); err == nil || rows != nil || probe.calls() != start {
				t.Fatal("same-shaped Decimal result crossed scope", err)
			}
			values, err = observeDecimal(t, backend, "derived_aggregate")
			check(t, err)
			assertDecimalJSON(t, values, `{"total":"149.6875","maximum":"125.5"}`)
		})
		t.Run("related_source_precision_and_scope", func(t *testing.T) {
			probe := &decimalReadProbe{decimalBackend: backend}
			base := ledger.EntryObjects.Using(probe)
			api, err := project.Using(probe)
			check(t, err)
			for _, value := range []orm.DynamicExpression{orm.DynamicF("rate__entries__amount"), orm.DynamicF("missing"), orm.DynamicF("amount").Add(orm.DynamicF("enabled"))} {
				if _, err := api.LedgerEntry.Expression(value); err == nil {
					t.Fatal("unowned or wrong-kind decimal expression accepted")
				}
			}
			forged := orm.NewNullableDecimalField[ledger.Entry](ir.Field{Name: "amount", GoName: "Amount", Column: "amount", Kind: ir.FieldDecimal, Nullable: true, Decimal: &ir.DecimalSpec{MaxDigits: 15, DecimalPlaces: 2}})
			if rows, err := orm.SelectInto(t.Context(), base, decimalProjection(orm.Decimal(orm.F(forged)).Add(d("0")))); err == nil || rows != nil || probe.calls() != 0 {
				t.Fatal("forged Decimal precision reached I/O", err)
			}
			for _, value := range []orm.ScalarOperand[ledger.Entry, decimal.Decimal]{relations.LedgerEntry.Rate.Factor, amount.AddExpression(relations.LedgerEntry.Rate.Factor), orm.Case(amount, orm.When(relations.LedgerEntry.Rate.Code.Exact("never"), relations.LedgerEntry.Rate.Flat))} {
				count, err := base.Filter(f.ID.In()).Update(t.Context(), orm.AssignExpression(f.Amount, value))
				if count != 0 || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
					t.Fatal("related Decimal assignment survived empty-source validation", count, err)
				}
			}
			wide := orm.F(f.Fine)
			if count, err := base.Filter(f.ID.In()).Update(t.Context(), orm.AssignExpression(f.Amount, wide)); err == nil || count != 0 {
				t.Fatal("direct field precision restrictions disappeared")
			}
			// A computed intermediate may widen and reduce before assignment.
			rollbackDecimal(t, backend, func(session db.Session) {
				q := ledger.EntryObjects.Using(session)
				value := amount.Add(d("10000000000000")).Subtract(d("10000000000000"))
				count, err := q.Update(t.Context(), orm.AssignExpression(f.Amount, value))
				check(t, err)
				if count != 6 {
					t.Fatal("widened Decimal cancellation did not match all rows", count)
				}
				if !reflect.DeepEqual(before, decimalStorage(t, session)) {
					t.Fatal("Decimal cancellation lost exact digits")
				}
			})
		})
		t.Run("assignment_precision_and_atomicity", func(t *testing.T) {
			for _, name := range []string{"update_add", "update_old_row", "update_null"} {
				rollbackDecimal(t, backend, func(session db.Session) {
					value, err := observeDecimal(t, session, name)
					check(t, err)
					if value.(map[string]any)["count"] != int64(6) {
						t.Fatal("decimal UPDATE matched count", value)
					}
				})
			}
			for _, factor := range []string{"1.5", "1000000000000"} {
				stored := physical.Snapshot()
				count, err := ledger.EntryObjects.Using(backend).Update(t.Context(), orm.AssignExpression(f.Amount, amount.Multiply(d(factor))))
				if count != 0 {
					t.Fatal("failed Decimal assignment published count", count)
				}
				nativeDecimalError(t, vendor, err, "22P02")
				if physical.Snapshot() != stored {
					t.Fatal("invalid Decimal target changed part of the statement")
				}
			}
			rollbackDecimal(t, backend, func(session db.Session) {
				q := ledger.EntryObjects.Using(session).OrderBy(f.Code.Asc())
				held, err := q.All(t.Context())
				check(t, err)
				sibling := q.Filter()
				siblingHeld, err := sibling.All(t.Context())
				check(t, err)
				count, err := q.UpdateDynamic(t.Context(), orm.DynamicUpdateInput{Field: "amount", Value: orm.DynamicF("amount").Add(d("0.25"))})
				check(t, err)
				if count != 6 {
					t.Fatal(count)
				}
				fresh, err := q.All(t.Context())
				check(t, err)
				again, err := sibling.All(t.Context())
				check(t, err)
				if held[0].Amount.String() != "12.5" || siblingHeld[0].Amount.String() != "12.5" || again[0].Amount.String() != "12.5" || fresh[0].Amount.String() != "12.75" {
					t.Fatal("Decimal update cache ownership", held, fresh, again)
				}
				count, err = q.Update(t.Context(), orm.AssignExpression(f.Amount, amount.Multiply(d("1.5"))))
				if count != 0 || err == nil {
					t.Fatal("Decimal rounding accepted inside a parent transaction", count, err)
				}
				afterFailure, err := q.Fresh().All(t.Context())
				check(t, err)
				if !reflect.DeepEqual(fresh, afterFailure) {
					t.Fatal("failed savepoint changed prior parent writes")
				}
				count, err = q.Filter(f.Code.Exact("b")).Update(t.Context(), orm.Assign(f.Amount, d("2.5")))
				check(t, err)
				if count != 1 {
					t.Fatal("decimal savepoint poisoned parent", count)
				}
			})
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			count, err := ledger.EntryObjects.Using(backend).Update(ctx, orm.AssignExpression(f.Amount, amount.Add(d("1"))))
			if count != 0 || !errors.Is(err, context.Canceled) {
				t.Fatal("canceled Decimal write published count", count, err)
			}
		})
		t.Run("case_and_null_evaluation", func(t *testing.T) {
			for _, name := range []string{"case_null", "case_unselected_zero"} {
				_, err := observeDecimal(t, backend, name)
				check(t, err)
			}
			base := ledger.EntryObjects.Using(backend).OrderBy(f.Code.Asc())
			value := amount.AddExpression(orm.NullValue[ledger.Entry, decimal.Decimal]())
			values, err := orm.SelectInto(t.Context(), base, decimalProjection(value))
			check(t, err)
			for _, row := range values {
				if row["value"] != nil {
					t.Fatal("Decimal NULL became zero", values)
				}
			}
			api, err := project.Using(backend)
			check(t, err)
			dynamic, err := api.LedgerEntry.Expression(orm.DynamicF("amount").Add(nil))
			check(t, err)
			dynamicRows, err := project.SelectLedgerEntryInto(t.Context(), api.LedgerEntry.OrderBy(f.Code.Asc()), orm.Project2(f.Code, dynamic, func(code string, value query.Value) map[string]any {
				return map[string]any{"code": code, "value": dynamicDecimal(value)}
			}))
			check(t, err)
			if !reflect.DeepEqual(values, dynamicRows) {
				t.Fatal("typed/untyped Decimal NULL operands diverged")
			}
			// Direct invalid literals in every assignment branch remain preflight errors.
			invalid := orm.Case(amount, orm.When(f.Code.Exact("never"), orm.Value[ledger.Entry](d("0.001"))))
			if count, err := base.Filter(f.ID.In()).Update(t.Context(), orm.AssignExpression(f.Amount, invalid)); count != 0 || err == nil {
				t.Fatal("unselected invalid assignment literal accepted")
			}
		})
		t.Run("cache_context_cursor_and_session", func(t *testing.T) {
			probe := &decimalReadProbe{decimalBackend: backend}
			selected, err := orm.SelectQuery(ledger.EntryObjects.Using(probe).OrderBy(f.Code.Asc()), decimalProjection(cost))
			check(t, err)
			rows, err := selected.All(t.Context())
			check(t, err)
			rows[0]["value"] = "caller-owned"
			var wait sync.WaitGroup
			failures := make(chan error, 8)
			for range 8 {
				wait.Add(1)
				go func() {
					defer wait.Done()
					rows, err := selected.All(t.Context())
					if err == nil && (len(rows) != 6 || rows[0]["value"] != "16.125") {
						err = errors.New("Decimal cached result changed")
					}
					failures <- err
				}()
			}
			wait.Wait()
			close(failures)
			for err := range failures {
				check(t, err)
			}
			if probe.calls() != 1 {
				t.Fatal("Decimal cache performed duplicate reads", probe.calls())
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if rows, err := selected.All(ctx); rows != nil || !errors.Is(err, context.Canceled) || probe.calls() != 1 {
				t.Fatal("warm Decimal cache ignored cancellation", err)
			}
			for _, fault := range []string{"scan", "iteration", "close"} {
				failed := &decimalReadProbe{decimalBackend: backend, fault: fault}
				built := 0
				rows, err := orm.SelectInto(t.Context(), ledger.EntryObjects.Using(failed), orm.Project1(cost, func(value orm.Optional[decimal.Decimal]) orm.Optional[decimal.Decimal] { built++; return value }))
				if rows != nil || err == nil || built != 0 || failed.closed.Load() != 1 {
					t.Fatal("failed decimal cursor published prefix or leaked", fault, err, built)
				}
			}
			var escaped orm.ResultQuery[ledger.Entry, map[string]any]
			check(t, backend.Atomic(t.Context(), func(session db.Session) error {
				var err error
				escaped, err = orm.SelectQuery(ledger.EntryObjects.Using(session), decimalProjection(cost))
				if err != nil {
					return err
				}
				_, err = escaped.All(t.Context())
				return err
			}))
			if rows, err := escaped.All(t.Context()); rows != nil || err == nil {
				t.Fatal("Decimal result cache escaped a closed borrowed session")
			}
		})
		t.Run("physical_corruption_and_intermediate_bounds", func(t *testing.T) {
			base := ledger.EntryObjects.Using(backend).Filter(f.Code.Exact("a"))
			invalid := "'not-a-decimal'"
			if vendor == "postgres" {
				invalid = "'NaN'::numeric"
			}
			check(t, physical.Update("entry", "amount", invalid, seed.entries["a"].ID))
			for _, expression := range []orm.DecimalExpression[ledger.Entry]{amount.Add(d("0")), amount.Multiply(d("0")), amount.Negate(), amount.AddExpression(orm.NullValue[ledger.Entry, decimal.Decimal]())} {
				rows, err := orm.SelectInto(t.Context(), base, decimalProjection(expression))
				if rows != nil {
					t.Fatal("invalid physical Decimal decoded")
				}
				nativeDecimalError(t, vendor, err, "22P02")
			}
			stored := physical.Snapshot()
			count, err := base.Update(t.Context(), orm.AssignExpression(f.Amount, amount.Multiply(d("0"))))
			if count != 0 || err == nil || physical.Snapshot() != stored {
				t.Fatal("corrupt source was hidden or changed by arithmetic", count, err)
			}
			// FILTER owns contribution membership and does not validate excluded rows.
			safe, err := orm.AggregateInto(t.Context(), base, orm.Aggregate1(orm.Sum(amount.Add(d("0"))).Where(f.Code.Exact("never")), func(value orm.Optional[decimal.Decimal]) orm.Optional[decimal.Decimal] { return value }))
			check(t, err)
			if safe.Valid() {
				t.Fatal("excluded corrupt Decimal contributed")
			}
			branch := orm.Decimal(orm.Case(orm.Value[ledger.Entry](d("5")), orm.When(f.Code.Exact("never"), amount)))
			values, err := orm.SelectInto(t.Context(), base, decimalProjection(branch))
			check(t, err)
			assertDecimalJSON(t, values, `[{"code":"a","value":"5"}]`)
			_, err = base.Update(t.Context(), orm.Assign(f.Amount, *seed.entries["a"].Amount))
			check(t, err)
			for _, expression := range []orm.DecimalExpression[ledger.Entry]{
				orm.Decimal(orm.Value[ledger.Entry](d("1e1000"))).Multiply(d("10")).Multiply(d("0")),
				orm.Decimal(orm.Value[ledger.Entry](d("1e-1000"))).Multiply(d("0.1")).Add(d("1")),
				orm.Decimal(orm.Value[ledger.Entry](d(strings.Repeat("9", 1000)))).Add(d("0.1")).Subtract(d("0.1")),
			} {
				rows, err := orm.SelectInto(t.Context(), base.Filter(expression.Exact(d("0"))), decimalProjection(expression))
				if rows != nil {
					t.Fatal("out-of-domain intermediate decoded")
				}
				nativeDecimalError(t, vendor, err, "22P02")
			}
			// Global SUM precision must fail before HAVING can hide its result.
			huge := orm.Decimal(orm.Value[ledger.Entry](d(strings.Repeat("9", 1000))))
			total := orm.Sum(huge)
			grouped, err := orm.GroupBy(ledger.EntryObjects.Using(backend), orm.Project1(f.Bucket, func(value string) string { return value }), orm.Aggregate1(total, func(value orm.Optional[decimal.Decimal]) orm.Optional[decimal.Decimal] { return value }), func(_ string, value orm.Optional[decimal.Decimal]) orm.Optional[decimal.Decimal] { return value })
			check(t, err)
			if rows, err := grouped.Having(total.Exact(orm.Some(d("0")))).All(t.Context()); err == nil || rows != nil {
				t.Fatal("HAVING hid invalid computed Decimal sum", err)
			}
			// Source field precision also applies to selected columns after Derive.
			check(t, physical.SetType("entry", "amount", "numeric"))
			tooLarge := d("1000000000000")
			raw := "1000000000000"
			if vendor == "sqlite" {
				encoded, err := query.Decimal(tooLarge).DatabaseValue()
				check(t, err)
				raw = fmt.Sprintf("X'%x'", encoded)
			}
			check(t, physical.Update("entry", "amount", raw, seed.entries["a"].ID))
			selected, err := orm.SelectQuery(base, orm.Project1(f.Amount, func(value *decimal.Decimal) *decimal.Decimal { return value }))
			check(t, err)
			stage, err := orm.Derive(selected)
			check(t, err)
			operand := orm.Decimal(orm.NullableOperand(orm.SelectedField(stage, f.Amount))).Multiply(d("0"))
			result, err := orm.SelectResults(stage, orm.Project1(operand, func(value orm.Optional[decimal.Decimal]) orm.Optional[decimal.Decimal] { return value }))
			check(t, err)
			if values, err := result.All(t.Context()); err == nil || values != nil {
				t.Fatal("selected source field lost its declared Decimal precision", err)
			}
			_, err = base.Update(t.Context(), orm.Assign(f.Amount, *seed.entries["a"].Amount))
			check(t, err)
			check(t, physical.SetType("entry", "amount", "numeric(14,2)"))
		})
		t.Run("native_division_scale_and_wide_values", func(t *testing.T) {
			values, err := observeDecimal(t, backend, "divide_integer")
			check(t, err)
			assertDecimalJSON(t, values, `[{"code":"a","value":"33.3333333333333333"}]`)
			values, err = observeDecimal(t, backend, "remainder")
			check(t, err)
			assertDecimalJSON(t, values, `[{"code":"a","value":"0.2"},{"code":"b","value":"-0.05"},{"code":"c","value":null},{"code":"d","value":"0"},{"code":"e","value":"0.2"},{"code":"f","value":"0.1"}]`)
			for _, name := range []string{"divide_zero", "remainder_zero"} {
				values, err := observeDecimal(t, backend, name)
				if vendor == "postgres" {
					if reflect.ValueOf(values).IsValid() && !reflect.ValueOf(values).IsNil() {
						t.Fatal(values)
					}
					nativeDecimalError(t, vendor, err, "22012")
				} else {
					check(t, err)
					assertDecimalJSON(t, values, `[{"code":"a","value":null}]`)
				}
			}
			values, err = observeDecimal(t, backend, "divide_native_scale")
			check(t, err)
			want := "0.0" + strings.Repeat("3", 19)
			if vendor == "postgres" {
				want = "0.0" + strings.Repeat("3", 999)
			}
			assertDecimalJSON(t, values, `[{"code":"a","value":"`+want+`"}]`)
			values, err = observeDecimal(t, backend, "wide_product")
			check(t, err)
			assertDecimalJSON(t, values, `[{"code":"a","value":"0.00001"}]`)
		})
		t.Run("resource_budgets", func(t *testing.T) {
			probe := &decimalReadProbe{decimalBackend: backend}
			value := amount
			for range query.MaximumScalarDepth {
				value = value.Add(d("1"))
			}
			if rows, err := orm.SelectInto(t.Context(), ledger.EntryObjects.Using(probe), decimalProjection(value)); rows != nil || err == nil || probe.calls() != 0 {
				t.Fatal("Decimal expression depth bound was bypassed", err)
			}
			value = amount
			for range 8 {
				value = value.Add(d("1"))
			}
			rows, err := orm.SelectInto(t.Context(), ledger.EntryObjects.Using(probe).Filter(f.Code.Exact("a")), decimalProjection(value))
			check(t, err)
			assertDecimalJSON(t, rows, `[{"code":"a","value":"20.5"}]`)

		})
		if nativeBefore != physical.Snapshot() {
			t.Fatal("Decimal runtime changed native stored columns")
		}
		if !reflect.DeepEqual(before, decimalStorage(t, backend)) {
			t.Fatal("Decimal reads or rolled-back writes changed stored state")
		}
	})
}
