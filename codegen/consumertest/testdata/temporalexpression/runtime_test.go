package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"example.com/godj-project-bundle/planning"
	"example.com/godj-project-bundle/project"
	"example.com/godj-project-bundle/records"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	modernsqlite "modernc.org/sqlite"
)

type temporalReadProbe struct {
	temporalBackend
	mu     sync.Mutex
	plans  []query.Plan
	closed atomic.Int64
	fault  string
}

func (probe *temporalReadProbe) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	probe.mu.Lock()
	probe.plans = append(probe.plans, plan)
	probe.mu.Unlock()
	rows, err := probe.temporalBackend.Query(ctx, plan)
	if err != nil {
		return rows, err
	}
	return &temporalRows{Rows: rows, owner: probe}, nil
}
func (probe *temporalReadProbe) calls() int {
	probe.mu.Lock()
	defer probe.mu.Unlock()
	return len(probe.plans)
}

type temporalRows struct {
	db.Rows
	owner *temporalReadProbe
}

func (rows *temporalRows) Scan(values ...any) error {
	if rows.owner.fault == "scan" {
		return errors.New("temporal scan failure")
	}
	return rows.Rows.Scan(values...)
}
func (rows *temporalRows) Err() error {
	if rows.owner.fault == "iteration" {
		return errors.Join(rows.Rows.Err(), errors.New("temporal iteration failure"))
	}
	return rows.Rows.Err()
}
func (rows *temporalRows) Close() error {
	rows.owner.closed.Add(1)
	err := rows.Rows.Close()
	if rows.owner.fault == "close" {
		return errors.Join(err, errors.New("temporal close failure"))
	}
	return err
}

func assertTemporalJSON(t *testing.T, actual any, expected string) {
	t.Helper()
	data, err := json.Marshal(actual)
	check(t, err)
	var left, right any
	check(t, json.Unmarshal(data, &left))
	check(t, json.Unmarshal([]byte(expected), &right))
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("temporal result = %s, want %s", data, expected)
	}
}

func nativeTemporalError(t *testing.T, vendor string, err error, postgresState string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected temporal failure")
	}
	if vendor == "postgres" {
		var native *pgconn.PgError
		if !errors.As(err, &native) || native.Code != postgresState {
			t.Fatal("native temporal SQLSTATE lost", err)
		}
	} else {
		var native *modernsqlite.Error
		if !errors.Is(err, &query.Error{Code: query.CodeInvalidValue}) || !errors.As(err, &native) {
			t.Fatal("temporal classification or native SQLite cause lost", err)
		}
	}
}

func rollbackTemporal(t *testing.T, backend temporalBackend, callback func(db.Session)) {
	t.Helper()
	rollback := errors.New("test-owned temporal rollback")
	err := backend.Atomic(t.Context(), func(session db.Session) error { callback(session); return rollback })
	if !errors.Is(err, rollback) {
		t.Fatal("temporal rollback receipt lost", err)
	}
}

func TestTemporalRuntime(t *testing.T) {
	withTemporalBackends(t, func(t *testing.T, backend temporalBackend, vendor string, physical temporalPhysical) {
		seed := seedTemporal(t, backend)
		before, nativeBefore := temporalStorage(t, backend), physical.Snapshot()
		relations, err := project.BindRelations()
		check(t, err)
		f := records.EventFields
		tick := duration.FromMicroseconds(1)
		finish := orm.DateTime(orm.F(f.Finish))
		span := orm.Duration(orm.F(f.Span))
		shift := finish.AddExpression(span)
		difference := finish.DifferenceExpression(orm.F(f.Start))
		shiftRows := `[{"code":"a","value":"2024-02-29T00:00:00.000002Z"},{"code":"b","value":"2024-02-28T23:59:59.999999Z"},{"code":"c","value":null},{"code":"d","value":"2026-10-15T00:00:00.000000Z"},{"code":"e","value":"2024-02-29T00:00:00.000002Z"},{"code":"f","value":"2026-10-24T15:00:00.000006Z"}]`
		t.Run("typed_dynamic_and_exact_values", func(t *testing.T) {
			probe := &temporalReadProbe{temporalBackend: backend}
			api, err := project.Using(probe)
			check(t, err)
			for _, pair := range []struct {
				typed   orm.DateTimeExpression[records.Event]
				dynamic orm.DynamicExpression
			}{
				{shift, orm.DynamicF("finish").Add(orm.DynamicF("span"))},
				{span.AddToExpression(finish), orm.DynamicF("span").Add(orm.DynamicF("finish"))},
				{finish.SubtractExpression(span), orm.DynamicF("finish").Subtract(orm.DynamicF("span"))},
			} {
				start := probe.calls()
				typed, err := orm.SelectInto(t.Context(), records.EventObjects.Using(probe).OrderBy(f.Code.Asc()), temporalProjection(pair.typed.ScalarExpression))
				check(t, err)
				value, err := api.RecordsEvent.Expression(pair.dynamic)
				check(t, err)
				dynamic, err := project.SelectRecordsEventInto(t.Context(), api.RecordsEvent.OrderBy(f.Code.Asc()), orm.Project2(f.Code, value, func(code string, value query.Value) map[string]any {
					return map[string]any{"code": code, "value": dynamicTemporal(value)}
				}))
				check(t, err)
				if !reflect.DeepEqual(typed, dynamic) || probe.calls() != start+2 || !probe.plans[start].Equal(probe.plans[start+1]) {
					t.Fatal("typed/dynamic temporal plan or result differs", typed, dynamic)
				}
			}
			value, err := observeTemporal(t, backend, "datetime_add_duration")
			check(t, err)
			assertTemporalJSON(t, value, shiftRows)
			for _, pair := range []struct{ name, expected string }{
				{"datetime_difference", `[{"code":"a","value":"00:00:00.000002"},{"code":"b","value":"-1 00:00:00"},{"code":"c","value":null},{"code":"d","value":"00:00:00"},{"code":"e","value":"00:00:00.000002"},{"code":"f","value":"7 00:00:00.000002"}]`},
				{"duration_add", `[{"code":"a","value":"00:00:00.000003"},{"code":"b","value":"23:59:59.999999"},{"code":"c","value":null},{"code":"d","value":null},{"code":"e","value":"00:00:00.000003"},{"code":"f","value":"2 03:00:00.000004"}]`},
				{"large_datetime_difference", `[{"code":"a","value":"3652058 23:59:59.999999"}]`},
				{"negative_datetime_difference", `[{"code":"a","value":"-3652059 00:00:00.000001"}]`},
			} {
				value, err := observeTemporal(t, backend, pair.name)
				check(t, err)
				assertTemporalJSON(t, value, pair.expected)
			}
			start := probe.calls()
			for _, input := range []orm.DynamicExpression{
				orm.DynamicF("span").Add(int64(1)), orm.DynamicF("span").Add(float64(1)), orm.DynamicF("span").Add(time.Second),
				orm.DynamicF("span").Add("00:00:00.000001"), orm.DynamicF("finish").Add(orm.DynamicF("start")),
				orm.DynamicF("span").Subtract(orm.DynamicF("finish")), orm.DynamicF("span").Multiply(orm.DynamicF("other")),
				orm.DynamicF("span").Add(nil), orm.DynamicF("finish").Add(nil),
			} {
				if _, err := api.RecordsEvent.Expression(input); err == nil {
					t.Fatal("implicit temporal kind or unsupported signature accepted")
				}
			}
			if probe.calls() != start {
				t.Fatal("invalid temporal signature reached native I/O")
			}
		})
		t.Run("filter_order_and_page", func(t *testing.T) {
			probe := &temporalReadProbe{temporalBackend: backend}
			selected, err := orm.SelectQuery(records.EventObjects.Using(probe).Filter(shift.IsNull(false)).OrderBy(shift.Desc(), f.Code.Asc()), temporalProjection(shift.ScalarExpression))
			check(t, err)
			page, err := selected.Page(t.Context(), 3, 1)
			check(t, err)
			if page.Total != 5 || probe.calls() != 1 {
				t.Fatal("temporal page did not retain one-statement total", page.Total, probe.calls())
			}
			assertTemporalJSON(t, page.Rows, `[{"code":"d","value":"2026-10-15T00:00:00.000000Z"},{"code":"a","value":"2024-02-29T00:00:00.000002Z"},{"code":"e","value":"2024-02-29T00:00:00.000002Z"}]`)
			past, err := selected.Page(t.Context(), 3, 99)
			check(t, err)
			if past.Total != 5 || past.Rows == nil || len(past.Rows) != 0 {
				t.Fatal("empty temporal page lost its total", past)
			}
			value, err := observeTemporal(t, backend, "compare_computed")
			check(t, err)
			assertTemporalJSON(t, value, `[{"code":"a","value":"2024-02-29T00:00:00.000002Z"},{"code":"e","value":"2024-02-29T00:00:00.000002Z"},{"code":"f","value":"2026-10-24T15:00:00.000006Z"}]`)
			stage, err := orm.Derive(selected)
			check(t, err)
			valueAfter := orm.DateTime(orm.OptionalOperand(orm.SelectedField(stage, shift))).Subtract(tick)
			result, err := orm.SelectResults(stage, orm.Project1(valueAfter, func(value orm.Optional[time.Time]) any { return optionalTemporal(value) }))
			check(t, err)
			other, err := orm.Derive(selected)
			check(t, err)
			foreign := orm.DateTime(orm.OptionalOperand(orm.SelectedField(other, shift)))
			start := probe.calls()
			if rows, err := result.Filter(orm.ExactExpressions(valueAfter, foreign)).All(t.Context()); err == nil || rows != nil || probe.calls() != start {
				t.Fatal("same-shaped temporal result crossed its source scope", err)
			}
			predicate, err := stage.Predicate(orm.DynamicColumn(1).Subtract(tick).GreaterThan(instant("2026-01-01T00:00:00Z")))
			check(t, err)
			rows, err := result.Filter(predicate).All(t.Context())
			check(t, err)
			if len(rows) != 2 {
				t.Fatal("selected temporal dynamic predicate changed membership", rows)
			}
		})
		t.Run("aggregates_and_empty", func(t *testing.T) {
			for _, pair := range []struct{ name, expected string }{
				{"computed_aggregate", `{"total":"6 00:00:00.000006","average":"1 04:48:00.000001","minimum":"-1 00:00:00","maximum":"7 00:00:00.000002","present":5}`},
				{"empty_aggregate", `{"total":null,"average":null,"minimum":null,"maximum":null,"present":0}`},
				{"all_null_aggregate", `{"total":null,"average":null,"minimum":null,"maximum":null,"present":0}`},
				{"distinct_filtered_span", `{"total":"2 03:00:00.000007","average":"1 01:30:00.000004"}`},
				{"groups_having", `[{"bucket":"second","total":"7 00:00:00.000002","average":"3 12:00:00.000001"}]`},
				{"derived_duration", `{"total":"3 03:00:00.000009","maximum":"2 03:00:00.000004"}`},
			} {
				values, err := observeTemporal(t, backend, pair.name)
				check(t, err)
				assertTemporalJSON(t, values, pair.expected)
			}
			probe := &temporalReadProbe{temporalBackend: backend}
			api, err := project.Using(probe)
			check(t, err)
			value, err := api.RecordsEvent.Expression(orm.DynamicF("finish").Subtract(orm.DynamicF("start")))
			check(t, err)
			typed, err := orm.AggregateInto(t.Context(), records.EventObjects.Using(probe), orm.Aggregate2(orm.Sum(difference), orm.Avg(difference), func(a, b orm.Optional[duration.Duration]) []any {
				return []any{optionalTemporal(a), optionalTemporal(b)}
			}))
			check(t, err)
			dynamic, err := project.AggregateRecordsEventInto(t.Context(), api.RecordsEvent, orm.Aggregate2(value.Sum(), value.Avg(), func(a, b query.Value) []any { return []any{dynamicTemporal(a), dynamicTemporal(b)} }))
			check(t, err)
			if !reflect.DeepEqual(typed, dynamic) || !probe.plans[0].Equal(probe.plans[1]) {
				t.Fatal("typed/dynamic computed Duration aggregate differs", typed, dynamic)
			}
		})
		t.Run("related_and_collection_scope", func(t *testing.T) {
			values, err := observeTemporal(t, backend, "collection_duration")
			check(t, err)
			assertTemporalJSON(t, values, `[{"code":"empty","total":null,"present":0},{"code":"missing","total":"00:00:00.000002","present":1},{"code":"negative","total":"6 00:00:00.000002","present":2},{"code":"normal","total":"00:00:00.000002","present":2}]`)
			values, err = observeTemporal(t, backend, "collection_shift_min")
			check(t, err)
			assertTemporalJSON(t, values, `[{"code":"empty","value":null},{"code":"missing","value":"2024-02-29T00:00:00.000002Z"},{"code":"negative","value":"2024-02-28T23:59:59.999999Z"},{"code":"normal","value":"2024-02-29T00:00:00.000002Z"}]`)
			probe := &temporalReadProbe{temporalBackend: backend}
			api, err := project.Using(probe)
			check(t, err)
			for _, pair := range []struct {
				typed orm.DateTimeExpression[records.Event]
				path  string
			}{
				{finish.AddExpression(relations.RecordsEvent.Schedule.Offset), "schedule__offset"},
				{finish.AddExpression(relations.RecordsEvent.Detail.Offset), "detail__offset"},
			} {
				start := probe.calls()
				typed, err := orm.SelectInto(t.Context(), records.EventObjects.Using(probe).OrderBy(f.Code.Asc()), temporalProjection(pair.typed.ScalarExpression))
				check(t, err)
				value, err := api.RecordsEvent.Expression(orm.DynamicF("finish").Add(orm.DynamicF(pair.path)))
				check(t, err)
				dynamic, err := project.SelectRecordsEventInto(t.Context(), api.RecordsEvent.OrderBy(f.Code.Asc()), orm.Project2(f.Code, value, func(code string, value query.Value) map[string]any {
					return map[string]any{"code": code, "value": dynamicTemporal(value)}
				}))
				check(t, err)
				if !reflect.DeepEqual(typed, dynamic) || !probe.plans[start].Equal(probe.plans[start+1]) {
					t.Fatal("related temporal source changed between facades")
				}
			}
			start := probe.calls()
			collection := relations.PlanningSchedule.Events
			if rows, err := orm.SelectInto(t.Context(), planning.ScheduleObjects.Using(probe), orm.Project1(orm.Duration(collection.Span).Add(tick), func(value orm.Optional[duration.Duration]) any { return optionalTemporal(value) })); err == nil || rows != nil || probe.calls() != start {
				t.Fatal("collection expression escaped aggregate cardinality", err)
			}
			forged := orm.NewNullableDurationField[records.Event](ir.Field{Name: "span", GoName: "Span", Column: "other", Kind: ir.FieldDuration, Nullable: true})
			if rows, err := orm.SelectInto(t.Context(), records.EventObjects.Using(probe), temporalProjection(finish.AddExpression(orm.F(forged)).ScalarExpression)); err == nil || rows != nil || probe.calls() != start {
				t.Fatal("forged temporal source metadata reached I/O", err)
			}
			for _, value := range []orm.ScalarOperand[records.Event, time.Time]{relations.RecordsEvent.Schedule.Anchor, finish.AddExpression(relations.RecordsEvent.Schedule.Offset), orm.Case(finish, orm.When(f.Code.Exact("never"), relations.RecordsEvent.Detail.Occurred))} {
				if count, err := records.EventObjects.Using(probe).Filter(f.ID.In()).Update(t.Context(), orm.AssignExpression(f.Finish, value)); count != 0 || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
					t.Fatal("related temporal assignment survived empty-source validation", count, err)
				}
			}
		})
		t.Run("native_mean_and_result_stages", func(t *testing.T) {
			for _, name := range []string{"mean_fractional_shift", "mean_integral_shift", "mean_zero_shift", "mean_null_shift", "mean_reverse_shift", "mean_add_duration", "mean_negate_duration", "mean_reaggregate"} {
				rollbackTemporal(t, backend, func(session db.Session) {
					value, err := observeTemporal(t, session, name)
					check(t, err)
					want := `[{"bucket":"first","period":"00:00:00.000002","value":"9999-01-01T00:00:00.000002Z"}]`
					if vendor == "postgres" || name == "mean_integral_shift" {
						want = `[{"bucket":"first","period":"00:00:00.000002","value":"9999-01-01T00:00:00.000003Z"}]`
					}
					switch name {
					case "mean_zero_shift":
						want = `[{"bucket":"first","period":"00:00:00","value":"9999-01-01T00:00:00.000001Z"}]`
					case "mean_null_shift":
						want = `[{"bucket":"first","period":null,"value":null}]`
					case "mean_add_duration":
						want = `[{"bucket":"first","value":"00:00:00.000002"}]`
						if vendor == "postgres" {
							want = `[{"bucket":"first","value":"00:00:00.000003"}]`
						}
					case "mean_negate_duration":
						want = `[{"bucket":"first","value":"-1 23:59:59.999998"}]`
					case "mean_reaggregate":
						want = `{"total":"00:00:00.000002","average":"00:00:00.000002"}`
					}
					assertTemporalJSON(t, value, want)
				})
			}
		})
		t.Run("assignment_and_atomicity", func(t *testing.T) {
			rollbackTemporal(t, backend, func(session db.Session) {
				q := records.EventObjects.Using(session).OrderBy(f.Code.Asc())
				held, err := q.All(t.Context())
				check(t, err)
				sibling := q.Filter()
				_, err = sibling.All(t.Context())
				check(t, err)
				count, err := q.UpdateDynamic(t.Context(), orm.DynamicUpdateInput{Field: "finish", Value: orm.DynamicF("finish").Add(orm.DynamicF("span"))}, orm.DynamicUpdateInput{Field: "span", Value: orm.DynamicF("finish").Subtract(orm.DynamicF("start"))})
				check(t, err)
				fresh, err := q.All(t.Context())
				check(t, err)
				again, err := sibling.All(t.Context())
				check(t, err)
				if count != 6 || !reflect.DeepEqual(held, again) || fresh[0].Finish == nil || temporalValue(*fresh[0].Finish) != "2024-02-29T00:00:00.000002Z" || fresh[0].Span == nil || *fresh[0].Span != duration.FromMicroseconds(2) {
					t.Fatal("temporal UPDATE lost original-row values or cache ownership", count, fresh)
				}
				// A late row fails after earlier rows would otherwise be written.
				count, err = q.Filter(f.Code.Exact("f")).Update(t.Context(), orm.Assign(f.Finish, instant("9999-12-31T23:59:59.999999Z")))
				check(t, err)
				prior, err := q.Fresh().All(t.Context())
				check(t, err)
				count, err = q.Update(t.Context(), orm.AssignExpression(f.Finish, finish.Add(tick)))
				if count != 0 {
					t.Fatal("failed temporal UPDATE published count", count)
				}
				nativeTemporalError(t, vendor, err, "22007")
				after, err := q.Fresh().All(t.Context())
				check(t, err)
				if !reflect.DeepEqual(prior, after) {
					t.Fatal("failing temporal savepoint changed earlier parent writes")
				}
				count, err = q.Filter(f.Code.Exact("b")).Update(t.Context(), orm.AssignNull(f.Finish))
				check(t, err)
				if count != 1 {
					t.Fatal("temporal failure poisoned its live parent transaction")
				}
				_, err = q.Filter(f.Code.Exact("f")).Update(t.Context(), orm.Assign(f.Finish, *seed.events["f"].Finish))
				check(t, err)
				prior, err = q.Fresh().All(t.Context())
				check(t, err)
				count, err = q.Update(t.Context(), orm.Assign(f.Code, "duplicate"), orm.AssignExpression(f.Finish, finish.Add(tick)))
				if count != 0 || err == nil {
					t.Fatal("constraint failure published a temporal write", count, err)
				}
				if vendor == "postgres" {
					var native *pgconn.PgError
					if !errors.As(err, &native) || native.Code != "23505" {
						t.Fatal("temporal constraint failure lost its unique cause", err)
					}
				} else {
					var native *modernsqlite.Error
					if !errors.As(err, &native) || native.Code() != 2067 {
						t.Fatal("temporal constraint failure lost its unique cause", err)
					}
				}
				after, err = q.Fresh().All(t.Context())
				check(t, err)
				if !reflect.DeepEqual(prior, after) {
					t.Fatal("constraint failure retained partial temporal writes")
				}
			})
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if count, err := records.EventObjects.Using(backend).Update(ctx, orm.AssignExpression(f.Finish, shift)); count != 0 || !errors.Is(err, context.Canceled) {
				t.Fatal("canceled temporal update published a count", count, err)
			}
		})
		t.Run("case_null_and_corruption", func(t *testing.T) {
			for _, name := range []string{"typed_null_duration", "typed_null_datetime", "null_difference", "case_fallback", "case_unselected_overflow"} {
				values, err := observeTemporal(t, backend, name)
				check(t, err)
				if name == "typed_null_duration" || name == "typed_null_datetime" || name == "null_difference" {
					assertTemporalJSON(t, values, `[{"code":"a","value":null},{"code":"b","value":null},{"code":"c","value":null},{"code":"d","value":null},{"code":"e","value":null},{"code":"f","value":null}]`)
				}
			}
			api, err := project.Using(backend)
			check(t, err)
			null, err := api.RecordsEvent.Expression(orm.DynamicF("finish").Add(orm.DynamicNull(query.FieldDuration)))
			check(t, err)
			nulls, err := project.SelectRecordsEventInto(t.Context(), api.RecordsEvent, orm.Project1(null, func(value query.Value) bool { return value.IsNull() }))
			check(t, err)
			if len(nulls) != 6 {
				t.Fatal("typed dynamic NULL lost rows")
			}
			for _, value := range nulls {
				if !value {
					t.Fatal("dynamic temporal NULL became zero")
				}
			}
			bad, restore := "0.5", "1"
			if vendor == "postgres" {
				bad, restore = "INTERVAL '1 month'", "INTERVAL '1 microsecond'"
			}
			check(t, physical.Update("event", "span", bad, seed.events["a"].ID))
			base := records.EventObjects.Using(backend).Filter(f.Code.Exact("a"))
			stored := physical.Snapshot()
			for _, value := range []orm.DurationExpression[records.Event]{span.Add(tick), span.AddExpression(orm.NullValue[records.Event, duration.Duration]()), span.Negate()} {
				rows, err := orm.SelectInto(t.Context(), base, temporalProjection(value.ScalarExpression))
				if rows != nil {
					t.Fatal("corrupt Duration published rows")
				}
				nativeTemporalError(t, vendor, err, "22007")
			}
			if count, err := base.Update(t.Context(), orm.AssignExpression(f.Finish, shift)); count != 0 || err == nil || physical.Snapshot() != stored {
				t.Fatal("corrupt temporal source was hidden or changed", count, err)
			}
			safe, err := orm.AggregateInto(t.Context(), base, orm.Aggregate1(orm.Sum(span.Add(tick)).Where(f.Code.Exact("never")), func(value orm.Optional[duration.Duration]) bool { return value.Valid() }))
			check(t, err)
			if safe {
				t.Fatal("FILTER-excluded corrupt period contributed")
			}
			branch := orm.Duration(orm.Case(orm.Value[records.Event](tick), orm.When(f.Code.Exact("never"), span)))
			values, err := orm.SelectInto(t.Context(), base, temporalProjection(branch.ScalarExpression))
			check(t, err)
			assertTemporalJSON(t, values, `[{"code":"a","value":"00:00:00.000001"}]`)
			check(t, physical.Update("event", "span", restore, seed.events["a"].ID))
			bad, restore = "'not-a-time'", "'2024-02-29 00:00:00.000001'"
			if vendor == "postgres" {
				bad, restore = "TIMESTAMPTZ 'infinity'", "TIMESTAMPTZ '2024-02-29 00:00:00.000001+00'"
			}
			check(t, physical.Update("event", "finish", bad, seed.events["a"].ID))
			rows, err := orm.SelectInto(t.Context(), base, temporalProjection(finish.AddExpression(orm.NullValue[records.Event, duration.Duration]()).ScalarExpression))
			if rows != nil {
				t.Fatal("NULL hid invalid source instant")
			}
			nativeTemporalError(t, vendor, err, "22007")
			check(t, physical.Update("event", "finish", restore, seed.events["a"].ID))
			upper := orm.DateTime(orm.Value[records.Event](instant("9999-12-31T23:59:59.999999Z"))).Add(tick).Subtract(tick)
			rows, err = orm.SelectInto(t.Context(), base.Filter(upper.Exact(instant("2000-01-01T00:00:00Z"))), temporalProjection(upper.ScalarExpression))
			if rows != nil {
				t.Fatal("invalid intermediate escaped through cancellation or WHERE")
			}
			nativeTemporalError(t, vendor, err, "22007")
		})
		t.Run("cache_context_and_session", func(t *testing.T) {
			probe := &temporalReadProbe{temporalBackend: backend}
			selected, err := orm.SelectQuery(records.EventObjects.Using(probe).OrderBy(f.Code.Asc()), temporalProjection(shift.ScalarExpression))
			check(t, err)
			rows, err := selected.All(t.Context())
			check(t, err)
			rows[0]["value"] = "caller-owned"
			var wait sync.WaitGroup
			failures := make(chan error, 8)
			for range 8 {
				wait.Go(func() {
					rows, err := selected.All(t.Context())
					if err == nil && (len(rows) != 6 || rows[0]["value"] != "2024-02-29T00:00:00.000002Z") {
						err = errors.New("temporal cached result changed")
					}
					failures <- err
				})
			}
			wait.Wait()
			close(failures)
			for err := range failures {
				check(t, err)
			}
			if probe.calls() != 1 {
				t.Fatal("temporal cache duplicated reads", probe.calls())
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if rows, err := selected.All(ctx); rows != nil || !errors.Is(err, context.Canceled) || probe.calls() != 1 {
				t.Fatal("warm temporal cache ignored cancellation", err)
			}
			var escaped orm.ResultQuery[records.Event, map[string]any]
			check(t, backend.Atomic(t.Context(), func(session db.Session) error {
				var err error
				escaped, err = orm.SelectQuery(records.EventObjects.Using(session), temporalProjection(shift.ScalarExpression))
				if err != nil {
					return err
				}
				_, err = escaped.All(t.Context())
				return err
			}))
			if rows, err := escaped.All(t.Context()); rows != nil || err == nil {
				t.Fatal("temporal cache escaped a closed session")
			}
		})
		t.Run("native_failure_cleanup", func(t *testing.T) {
			for _, fault := range []string{"scan", "iteration", "close"} {
				probe := &temporalReadProbe{temporalBackend: backend, fault: fault}
				built := 0
				rows, err := orm.SelectInto(t.Context(), records.EventObjects.Using(probe), orm.Project1(shift, func(value orm.Optional[time.Time]) any { built++; return optionalTemporal(value) }))
				if rows != nil || err == nil || built != 0 || probe.closed.Load() != 1 {
					t.Fatal("temporal cursor failure published a prefix or leaked", fault, built, err)
				}
			}
			for _, name := range []string{"datetime_upper_overflow", "datetime_lower_overflow", "duration_int64_overflow", "duration_full_model_literal", "duration_model_overflow"} {
				value, err := observeTemporal(t, backend, name)
				if vendor == "postgres" && (name == "duration_int64_overflow" || name == "duration_full_model_literal") {
					check(t, err)
					want := "106751991 04:00:54.775808"
					if name == "duration_full_model_literal" {
						want = "999999999 23:59:59.999999"
					}
					assertTemporalJSON(t, value, `[{"code":"a","value":"`+want+`"}]`)
					continue
				}
				if err == nil {
					t.Fatal("out-of-range temporal result succeeded", name, value)
				}
				assertTemporalJSON(t, value, "null")
				if vendor == "sqlite" && (name == "duration_full_model_literal" || name == "duration_model_overflow") {
					if !errors.Is(err, duration.ErrMicrosecondRange) {
						t.Fatal("SQLite literal lost narrower-range cause", err)
					}
				} else {
					nativeTemporalError(t, vendor, err, "22007")
				}
			}
			if _, err := records.EventObjects.Using(backend).All(t.Context()); err != nil {
				t.Fatal("temporal native failure leaked a connection", err)
			}
		})
		t.Run("resource_budgets", func(t *testing.T) {
			probe := &temporalReadProbe{temporalBackend: backend}
			value := shift
			for range query.MaximumScalarDepth {
				value = value.Add(tick)
			}
			if rows, err := orm.SelectInto(t.Context(), records.EventObjects.Using(probe), temporalProjection(value.ScalarExpression)); rows != nil || err == nil || probe.calls() != 0 {
				t.Fatal("temporal expression depth bound was bypassed", err)
			}
			wide := span
			for range 12 {
				wide = wide.AddExpression(wide)
			}
			if rows, err := orm.SelectInto(t.Context(), records.EventObjects.Using(probe), temporalProjection(wide.ScalarExpression)); rows != nil || err == nil || probe.calls() != 0 {
				t.Fatal("temporal node budget was bypassed", err)
			}
			value = finish
			for range 8 {
				value = value.Add(tick)
			}
			rows, err := orm.SelectInto(t.Context(), records.EventObjects.Using(probe).Filter(f.Code.Exact("a")), temporalProjection(value.ScalarExpression))
			check(t, err)
			assertTemporalJSON(t, rows, `[{"code":"a","value":"2024-02-29T00:00:00.000009Z"}]`)
		})
		checkDurationScaling(t, backend, vendor, physical)
		if nativeBefore != physical.Snapshot() || !reflect.DeepEqual(before, temporalStorage(t, backend)) {
			t.Fatal("temporal runtime changed native stored columns or model values")
		}
	})
}
