package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"example.com/godj-project-bundle/planning"
	"example.com/godj-project-bundle/project"
	"example.com/godj-project-bundle/records"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	modernsqlite "modernc.org/sqlite"
)

type calendarReadProbe struct {
	calendarBackend
	mu     sync.Mutex
	plans  []query.Plan
	closed atomic.Int64
	fault  string
}

func (probe *calendarReadProbe) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	probe.mu.Lock()
	probe.plans = append(probe.plans, plan)
	probe.mu.Unlock()
	rows, err := probe.calendarBackend.Query(ctx, plan)
	if err != nil {
		return rows, err
	}
	return &calendarRows{Rows: rows, owner: probe}, nil
}
func (probe *calendarReadProbe) calls() int {
	probe.mu.Lock()
	defer probe.mu.Unlock()
	return len(probe.plans)
}

type calendarRows struct {
	db.Rows
	owner *calendarReadProbe
}

func (rows *calendarRows) Scan(values ...any) error {
	if rows.owner.fault == "scan" {
		return errors.New("calendar scan failure")
	}
	return rows.Rows.Scan(values...)
}
func (rows *calendarRows) Err() error {
	if rows.owner.fault == "iteration" {
		return errors.Join(rows.Rows.Err(), errors.New("calendar iteration failure"))
	}
	return rows.Rows.Err()
}
func (rows *calendarRows) Close() error {
	rows.owner.closed.Add(1)
	err := rows.Rows.Close()
	if rows.owner.fault == "close" {
		return errors.Join(err, errors.New("calendar close failure"))
	}
	return err
}

func assertCalendarJSON(t *testing.T, actual any, expected string) {
	t.Helper()
	data, err := json.Marshal(actual)
	check(t, err)
	var left, right any
	check(t, json.Unmarshal(data, &left))
	check(t, json.Unmarshal([]byte(expected), &right))
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("calendar result = %s, want %s", data, expected)
	}
}

func nativeCalendarError(t *testing.T, vendor string, err error, postgresState string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected calendar failure")
	}
	if vendor == "postgres" {
		var native *pgconn.PgError
		if !errors.As(err, &native) || native.Code != postgresState {
			t.Fatal("native calendar SQLSTATE lost", err)
		}
	} else {
		var native *modernsqlite.Error
		if !errors.Is(err, &query.Error{Code: query.CodeInvalidValue}) || !errors.As(err, &native) {
			t.Fatal("calendar classification or native SQLite cause lost", err)
		}
	}
}

func rollbackCalendar(t *testing.T, backend calendarBackend, callback func(db.Session)) {
	t.Helper()
	rollback := errors.New("test-owned calendar rollback")
	err := backend.Atomic(t.Context(), func(session db.Session) error { callback(session); return rollback })
	if !errors.Is(err, rollback) {
		t.Fatal("calendar rollback receipt lost", err)
	}
}

func assertCalendarParity[T calendarValue](t *testing.T, backend calendarBackend, typed orm.ScalarExpression[records.Event, T], expression orm.DynamicExpression) {
	t.Helper()
	probe := &calendarReadProbe{calendarBackend: backend}
	api, err := project.Using(probe)
	check(t, err)
	first, err := orm.SelectInto(t.Context(), records.EventObjects.Using(probe).OrderBy(records.EventFields.Code.Asc()), calendarProjection(typed))
	check(t, err)
	dynamic, err := api.RecordsEvent.Expression(expression)
	check(t, err)
	second, err := project.SelectRecordsEventInto(t.Context(), api.RecordsEvent.OrderBy(records.EventFields.Code.Asc()), orm.Project2(records.EventFields.Code, dynamic, func(code string, value query.Value) map[string]any {
		return map[string]any{"code": code, "value": dynamicCalendar(value)}
	}))
	check(t, err)
	if !reflect.DeepEqual(first, second) || probe.calls() != 2 || !probe.plans[0].Equal(probe.plans[1]) {
		t.Fatal("typed and dynamic calendar query/results differ", first, second)
	}
}

func dynamicPart(value orm.DynamicExpression, name string) orm.DynamicExpression {
	switch name {
	case "year":
		return value.Year()
	case "iso_year":
		return value.ISOYear()
	case "quarter":
		return value.Quarter()
	case "month":
		return value.Month()
	case "week":
		return value.Week()
	case "week_day":
		return value.WeekDay()
	case "iso_week_day":
		return value.ISOWeekDay()
	case "day":
		return value.Day()
	case "hour":
		return value.Hour()
	case "minute":
		return value.Minute()
	case "second":
		return value.Second()
	default:
		panic("unknown synthetic calendar part")
	}
}

func withCalendarCorruption(t *testing.T, physical calendarPhysical, seed calendarSeed, vendor, name string, run func()) {
	t.Helper()
	name = strings.TrimPrefix(strings.TrimPrefix(name, "trunc_"), "arith_")
	column, bad, restore := "day", "'not-a-date'", "'2021-01-01'"
	if vendor == "postgres" {
		bad = "DATE '10000-01-01'"
	}
	switch name {
	case "outside_date_storage":
		bad = "'0000-01-01'"
		if vendor == "postgres" {
			bad = "DATE '0001-01-01 BC'"
		}
	case "invalid_datetime_storage", "infinite_datetime_storage":
		column, restore = "at", "'2020-12-31 23:30:59.999999'"
		bad = "'2026-02-30 00:00:00'"
		if vendor == "postgres" {
			bad = "TIMESTAMPTZ '10000-01-01 00:00:00+00'"
			restore = "TIMESTAMPTZ '2020-12-31 23:30:59.999999+00'"
		}
		if name == "infinite_datetime_storage" {
			bad = "'infinity'"
			if vendor == "postgres" {
				bad = "TIMESTAMPTZ 'infinity'"
			}
		}
	case "clock_24_storage":
		column, bad, restore = "wall", "'24:00:00'", "'23:59:59.999999'"
	case "invalid_date_storage", "unselected_corrupt_date", "filtered_corrupt_date":
	default:
		t.Fatal("unknown corrupt fixture", name)
	}
	check(t, physical.Update("event", column, bad, seed.events["a"].ID))
	defer func() { check(t, physical.Update("event", column, restore, seed.events["a"].ID)) }()
	run()
}

func TestCalendarRuntime(t *testing.T) {
	withCalendarBackends(t, func(t *testing.T, backend calendarBackend, vendor string, physical calendarPhysical) {
		seed := seedCalendar(t, backend)
		before := physical.Snapshot()
		f := records.EventFields
		at, day, clockValue := orm.DateTime(orm.F(f.At)), orm.Date(orm.F(f.Day)), orm.Time(orm.F(f.Wall))
		t.Run("typed_dynamic_and_exact_values", func(t *testing.T) {
			assertCalendarParity(t, backend, at.Date().ScalarExpression, orm.DynamicF("at").Date())
			assertCalendarParity(t, backend, at.Time().ScalarExpression, orm.DynamicF("at").Time())
			parts := calendarParts(at, day, clockValue)
			for _, name := range slices.Sorted(maps.Keys(parts)) {
				var expression orm.DynamicExpression
				switch {
				case name == "composed_date_year":
					expression = orm.DynamicF("at").Date().Year()
				case name == "composed_time_second":
					expression = orm.DynamicF("at").Time().Second()
				case strings.HasPrefix(name, "datetime_"):
					expression = dynamicPart(orm.DynamicF("at"), strings.TrimPrefix(name, "datetime_"))
				case strings.HasPrefix(name, "date_"):
					expression = dynamicPart(orm.DynamicF("day"), strings.TrimPrefix(name, "date_"))
				case strings.HasPrefix(name, "time_"):
					expression = dynamicPart(orm.DynamicF("wall"), strings.TrimPrefix(name, "time_"))
				default:
					t.Fatal(name)
				}
				assertCalendarParity(t, backend, parts[name].ScalarExpression, expression)
			}
			for _, test := range []struct{ name, want string }{
				{"datetime_date", `[{"code":"a","value":"2020-12-31"},{"code":"b","value":"2021-01-01"},{"code":"c","value":"2019-12-30"},{"code":"d","value":"2000-02-29"},{"code":"e","value":"1900-03-01"},{"code":"f","value":"2400-02-29"},{"code":"g","value":"0001-01-01"},{"code":"h","value":"9999-12-31"},{"code":"i","value":null},{"code":"j","value":"2021-01-01"},{"code":"k","value":null}]`},
				{"datetime_time", `[{"code":"a","value":"23:30:59.999999"},{"code":"b","value":"00:30:00"},{"code":"c","value":"12:34:56.000001"},{"code":"d","value":"23:59:59.999999"},{"code":"e","value":"00:00:00"},{"code":"f","value":"00:00:00.000001"},{"code":"g","value":"00:00:00"},{"code":"h","value":"23:59:59.999999"},{"code":"i","value":null},{"code":"j","value":"00:30:00.000001"},{"code":"k","value":null}]`},
				{"datetime_iso_year", `[{"code":"a","value":2020},{"code":"b","value":2020},{"code":"c","value":2020},{"code":"d","value":2000},{"code":"e","value":1900},{"code":"f","value":2400},{"code":"g","value":1},{"code":"h","value":9999},{"code":"i","value":null},{"code":"j","value":2020},{"code":"k","value":null}]`},
				{"datetime_second", `[{"code":"a","value":59},{"code":"b","value":0},{"code":"c","value":56},{"code":"d","value":59},{"code":"e","value":0},{"code":"f","value":0},{"code":"g","value":0},{"code":"h","value":59},{"code":"i","value":null},{"code":"j","value":0},{"code":"k","value":null}]`},
			} {
				value, err := observeCalendar(t, backend, test.name)
				check(t, err)
				assertCalendarJSON(t, value, test.want)
			}
			probe := &calendarReadProbe{calendarBackend: backend}
			api, err := project.Using(probe)
			check(t, err)
			for _, expression := range []orm.DynamicExpression{orm.DynamicF("day").Hour(), orm.DynamicF("wall").Year(), orm.DynamicF("part").Date(), orm.DynamicF("code").Year(), orm.DynamicValue(nil).Date(), orm.DynamicValue("2020-01-01").Year(), orm.DynamicValue(duration.FromMicroseconds(1)).Year(), orm.DynamicF("at").Year().Year()} {
				if _, err := api.RecordsEvent.Expression(expression); err == nil {
					t.Fatal("invalid calendar signature accepted")
				}
			}
			if probe.calls() != 0 {
				t.Fatal("invalid signature reached I/O")
			}
		})
		t.Run("filter_order_and_page", func(t *testing.T) {
			probe := &calendarReadProbe{calendarBackend: backend}
			value := at.Date()
			source := records.EventObjects.Using(probe).Filter(value.IsNull(false)).OrderBy(value.Asc(), f.Code.Asc())
			selected, err := orm.SelectQuery(source, calendarProjection(value.ScalarExpression))
			check(t, err)
			page, err := selected.Page(t.Context(), 3, 2)
			check(t, err)
			if page.Total != 9 || probe.calls() != 1 {
				t.Fatal("calendar page lost one-statement total", page, probe.calls())
			}
			assertCalendarJSON(t, page.Rows, `[{"code":"d","value":"2000-02-29"},{"code":"c","value":"2019-12-30"},{"code":"a","value":"2020-12-31"}]`)
			empty, err := selected.Page(t.Context(), 3, 99)
			check(t, err)
			if empty.Total != 9 || empty.Rows == nil || len(empty.Rows) != 0 {
				t.Fatal("empty calendar page lost total", empty)
			}
			valueRows, err := observeCalendar(t, backend, "filter_date_range")
			check(t, err)
			assertCalendarJSON(t, valueRows, `[{"code":"a","value":"2020-12-31"},{"code":"b","value":"2021-01-01"},{"code":"j","value":"2021-01-01"}]`)
			grouped, err := orm.GroupBy(records.EventObjects.Using(probe), orm.Project1(value, func(value orm.Optional[calendar.Date]) any { return optionalCalendar(value) }), orm.Aggregate1(orm.CountRows[records.Event](), func(total int64) int64 { return total }), func(value any, total int64) map[string]any { return map[string]any{"value": value, "total": total} })
			check(t, err)
			counted, err := grouped.Having(orm.GroupKey(value).GreaterThanOrEqual(orm.Some(date("2020-01-01")))).OrderBy(orm.GroupKey(value).Asc()).Page(t.Context(), 2, 1)
			check(t, err)
			if counted.Total != 4 {
				t.Fatal("calendar HAVING total", counted)
			}
			assertCalendarJSON(t, counted.Rows, `[{"value":"2021-01-01","total":2},{"value":"2400-02-29","total":1}]`)
		})
		t.Run("aggregates_and_empty", func(t *testing.T) {
			for _, test := range []struct{ name, want string }{
				{"aggregate_parts", `{"years":26385,"seconds":23.3,"first":"0001-01-01","last":"23:59:59.999999"}`},
				{"filtered_aggregate", `{"years":12486,"dates":6}`}, {"literal_aggregate", `{"years":22044}`},
				{"empty_aggregate", `{"years":null,"first":null,"count":0}`}, {"empty_group", `[]`},
			} {
				value, err := observeCalendar(t, backend, test.name)
				check(t, err)
				assertCalendarJSON(t, value, test.want)
			}
			probe := &calendarReadProbe{calendarBackend: backend}
			api, err := project.Using(probe)
			check(t, err)
			dynamic, err := api.RecordsEvent.Expression(orm.DynamicF("day").Year())
			check(t, err)
			first, err := orm.AggregateInto(t.Context(), records.EventObjects.Using(probe), orm.Aggregate1(orm.Sum(day.Year()), func(value orm.Optional[int64]) any { return optionalCalendar(value) }))
			check(t, err)
			second, err := project.AggregateRecordsEventInto(t.Context(), api.RecordsEvent, orm.Aggregate1(dynamic.Sum(), func(value query.Value) any { return dynamicCalendar(value) }))
			check(t, err)
			if !reflect.DeepEqual(first, second) || !probe.plans[0].Equal(probe.plans[1]) {
				t.Fatal("calendar aggregate facades differ", first, second)
			}
		})
		t.Run("related_and_collection_scope", func(t *testing.T) {
			relations, err := project.BindRelations()
			check(t, err)
			assertCalendarParity(t, backend, orm.DateTime(relations.RecordsEvent.Schedule.Anchor).Date().ScalarExpression, orm.DynamicF("schedule__anchor").Date())
			assertCalendarParity(t, backend, orm.Date(relations.RecordsEvent.Detail.Day).WeekDay().ScalarExpression, orm.DynamicF("detail__day").WeekDay())
			values, err := observeCalendar(t, backend, "collection_years")
			check(t, err)
			assertCalendarJSON(t, values, `[{"code":"empty","years":1900,"opened":null,"first":"1900-03-01"},{"code":"future","years":12400,"opened":2401,"first":"0001-01-01"},{"code":"north","years":10081,"opened":8060,"first":"2000-02-29"},{"code":"unused","years":null,"opened":null,"first":null}]`)
			probe := &calendarReadProbe{calendarBackend: backend}
			value := orm.DateTime(relations.PlanningSchedule.Events.At).Date()
			rows, err := orm.SelectInto(t.Context(), planning.ScheduleObjects.Using(probe), orm.Project1(value, func(value orm.Optional[calendar.Date]) any { return optionalCalendar(value) }))
			if err == nil || rows != nil || probe.calls() != 0 {
				t.Fatal("collection calendar escaped aggregate cardinality", err)
			}
			if count, err := records.EventObjects.Using(probe).Update(t.Context(), orm.AssignExpression(f.Day, orm.DateTime(relations.RecordsEvent.Schedule.Anchor).Date())); count != 0 || err == nil || probe.calls() != 0 {
				t.Fatal("joined calendar assignment was accepted", count, err)
			}
		})
		t.Run("selected_result_stages", func(t *testing.T) {
			probe := &calendarReadProbe{calendarBackend: backend}
			selected, err := orm.SelectQuery(records.EventObjects.Using(probe).OrderBy(f.Code.Asc()), calendarProjection(at.Date().ScalarExpression))
			check(t, err)
			stage, err := orm.Derive(selected)
			check(t, err)
			value := orm.Date(orm.OptionalOperand(orm.SelectedField(stage, at.Date()))).Year()
			result, err := orm.SelectResults(stage, orm.Project1(value, func(value orm.Optional[int64]) any { return optionalCalendar(value) }))
			check(t, err)
			predicate, err := stage.Predicate(orm.DynamicColumn(1).Year().GreaterThanOrEqual(int64(2021)))
			check(t, err)
			values, err := result.Filter(predicate).All(t.Context())
			check(t, err)
			assertCalendarJSON(t, values, `[2021,2400,9999,2021]`)
			other, err := orm.Derive(selected)
			check(t, err)
			foreign := orm.Date(orm.OptionalOperand(orm.SelectedField(other, at.Date()))).Year()
			count := probe.calls()
			if rows, err := result.Filter(orm.ExactExpressions(value, foreign)).All(t.Context()); rows != nil || err == nil || probe.calls() != count {
				t.Fatal("calendar extraction discarded result scope", err)
			}
			aggregate, err := orm.AggregateResults(stage, orm.Aggregate1(orm.Sum(value), func(value orm.Optional[int64]) any { return optionalCalendar(value) }))
			check(t, err)
			total, err := aggregate.One(t.Context())
			check(t, err)
			assertCalendarJSON(t, total, `24381`)
			dyn, err := stage.Expression(orm.DynamicColumn(1).Year())
			check(t, err)
			dynamic, err := orm.SelectResults(stage, orm.Project1(dyn, func(value query.Value) any { return dynamicCalendar(value) }))
			check(t, err)
			typedRows, err := result.All(t.Context())
			check(t, err)
			dynamicRows, err := dynamic.All(t.Context())
			check(t, err)
			if !reflect.DeepEqual(typedRows, dynamicRows) {
				t.Fatal("selected calendar facades differ")
			}
		})
		t.Run("assignment_and_atomicity", func(t *testing.T) {
			rollbackCalendar(t, backend, func(session db.Session) {
				q := records.EventObjects.Using(session).OrderBy(f.Code.Asc())
				held, err := q.All(t.Context())
				check(t, err)
				sibling := q.Filter()
				_, err = sibling.All(t.Context())
				check(t, err)
				count, err := q.UpdateDynamic(t.Context(), orm.DynamicUpdateInput{Field: "day", Value: orm.DynamicF("at").Date()}, orm.DynamicUpdateInput{Field: "wall", Value: orm.DynamicF("at").Time()}, orm.DynamicUpdateInput{Field: "part", Value: orm.DynamicF("day").Year()})
				check(t, err)
				fresh, err := q.All(t.Context())
				check(t, err)
				cached, err := sibling.All(t.Context())
				check(t, err)
				if count != 11 || !reflect.DeepEqual(held, cached) || fresh[0].Day == nil || fresh[0].Day.String() != "2020-12-31" || fresh[0].Part == nil || *fresh[0].Part != 2021 || fresh[0].Wall == nil || fresh[0].Wall.String() != "23:30:59.999999" {
					t.Fatal("calendar assignment lost old row or cache ownership", count, fresh)
				}
				count, err = q.Update(t.Context(), orm.Assign(f.Code, "duplicate"), orm.AssignExpression(f.Day, at.Date()))
				if err == nil || count != 0 {
					t.Fatal("failed calendar UPDATE published count", count, err)
				}
				if vendor == "postgres" {
					var native *pgconn.PgError
					if !errors.As(err, &native) || native.Code != "23505" {
						t.Fatal("unique failure lost native cause", err)
					}
				} else {
					var native *modernsqlite.Error
					if !errors.As(err, &native) || native.Code() != 2067 {
						t.Fatal("unique failure lost native cause", err)
					}
				}
				after, err := q.Fresh().All(t.Context())
				check(t, err)
				if !reflect.DeepEqual(fresh, after) {
					t.Fatal("failed calendar statement left earlier writes")
				}
				count, err = q.Filter(f.Code.Exact("k")).Update(t.Context(), orm.AssignExpression(f.Day, orm.DateTime(orm.NullValue[records.Event, time.Time]()).Date()))
				check(t, err)
				if count != 1 {
					t.Fatal("failed statement poisoned parent session")
				}
			})
			// 'h' overflows after earlier rows could have been updated; the extraction
			// must validate the intermediate instant before publishing a smaller day.
			count, err := records.EventObjects.Using(backend).Update(t.Context(), orm.AssignExpression(f.Day, at.Add(duration.FromMicroseconds(1)).Date()))
			if count != 0 {
				t.Fatal("overflow calendar write published count", count)
			}
			nativeCalendarError(t, vendor, err, "22007")
			if physical.Snapshot() != before {
				t.Fatal("late calendar overflow retained a prefix")
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if count, err := records.EventObjects.Using(backend).Update(ctx, orm.AssignExpression(f.Day, at.Date())); count != 0 || !errors.Is(err, context.Canceled) {
				t.Fatal("canceled calendar write lost context", count, err)
			}
		})
		t.Run("case_null_and_corruption", func(t *testing.T) {
			assertCalendarParity(t, backend, orm.DateTime(orm.NullValue[records.Event, time.Time]()).Date().ScalarExpression, orm.DynamicNull(query.FieldDateTime).Date())
			assertCalendarParity(t, backend, orm.DateTime(orm.NullValue[records.Event, time.Time]()).Time().ScalarExpression, orm.DynamicNull(query.FieldDateTime).Time())
			assertCalendarParity(t, backend, orm.Date(orm.NullValue[records.Event, calendar.Date]()).Year().ScalarExpression, orm.DynamicNull(query.FieldDate).Year())
			assertCalendarParity(t, backend, orm.Time(orm.NullValue[records.Event, clock.Time]()).Second().ScalarExpression, orm.DynamicNull(query.FieldTime).Second())
			for _, name := range []string{"invalid_date_storage", "outside_date_storage", "invalid_datetime_storage", "infinite_datetime_storage", "clock_24_storage", "unselected_corrupt_date", "filtered_corrupt_date"} {
				withCalendarCorruption(t, physical, seed, vendor, name, func() {
					corrupt := physical.Snapshot()
					value, err := observeCalendar(t, backend, name)
					if name == "unselected_corrupt_date" {
						check(t, err)
						for _, row := range value.([]map[string]any) {
							if row["value"] != int64(0) {
								t.Fatal("unselected corrupt branch was evaluated", row)
							}
						}
					} else if name == "filtered_corrupt_date" {
						check(t, err)
						assertCalendarJSON(t, value, `{"years":24364}`)
					} else {
						nativeCalendarError(t, vendor, err, "22007")
						assertCalendarJSON(t, value, `null`)
					}
					if physical.Snapshot() != corrupt {
						t.Fatal("calendar read changed corrupted physical storage", name)
					}
				})
			}
			for _, name := range []string{"case_date", "case_time"} {
				value, err := observeCalendar(t, backend, name)
				check(t, err)
				rows := value.([]map[string]any)
				for _, index := range []int{8, 10} {
					want := "2000-01-01"
					if name == "case_time" {
						want = "00:00:00"
					}
					if rows[index]["value"] != want {
						t.Fatal("calendar CASE lost NULL fallback", rows[index])
					}
				}
			}
		})
		t.Run("cache_context_and_session", func(t *testing.T) {
			probe := &calendarReadProbe{calendarBackend: backend}
			selected, err := orm.SelectQuery(records.EventObjects.Using(probe).OrderBy(f.Code.Asc()), calendarProjection(at.Date().ScalarExpression))
			check(t, err)
			rows, err := selected.All(t.Context())
			check(t, err)
			rows[0]["value"] = "caller-owned"
			var wait sync.WaitGroup
			failures := make(chan error, 8)
			for range 8 {
				wait.Go(func() {
					rows, err := selected.All(t.Context())
					if err == nil && (len(rows) != 11 || rows[0]["value"] != "2020-12-31") {
						err = errors.New("calendar cache ownership lost")
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
				t.Fatal("calendar cache duplicated reads", probe.calls())
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if rows, err := selected.All(ctx); rows != nil || !errors.Is(err, context.Canceled) {
				t.Fatal("calendar warm cache ignored cancellation", err)
			}
			var escaped orm.ResultQuery[records.Event, map[string]any]
			check(t, backend.Atomic(t.Context(), func(session db.Session) error {
				var err error
				escaped, err = orm.SelectQuery(records.EventObjects.Using(session), calendarProjection(at.Date().ScalarExpression))
				if err != nil {
					return err
				}
				_, err = escaped.All(t.Context())
				return err
			}))
			if rows, err := escaped.All(t.Context()); rows != nil || err == nil {
				t.Fatal("calendar cache escaped its session")
			}
		})
		t.Run("native_failure_cleanup", func(t *testing.T) {
			for _, fault := range []string{"scan", "iteration", "close"} {
				probe := &calendarReadProbe{calendarBackend: backend, fault: fault}
				built := 0
				rows, err := orm.SelectInto(t.Context(), records.EventObjects.Using(probe), orm.Project1(at.Date(), func(value orm.Optional[calendar.Date]) any { built++; return optionalCalendar(value) }))
				if rows != nil || err == nil || built != 0 || probe.closed.Load() != 1 {
					t.Fatal("calendar failure published a prefix or leaked", fault, built, err)
				}
			}
			// Even a following false predicate cannot hide an invalid intermediate.
			value := orm.DateTime(orm.Value[records.Event](instant("9999-12-31T23:59:59.999999Z"))).Add(duration.FromMicroseconds(1)).Date().Year()
			rows, err := orm.SelectInto(t.Context(), records.EventObjects.Using(backend).Filter(value.Exact(2000)), calendarProjection(value.ScalarExpression))
			if rows != nil {
				t.Fatal("invalid calendar intermediate published rows")
			}
			nativeCalendarError(t, vendor, err, "22007")
			if _, err := records.EventObjects.Using(backend).All(t.Context()); err != nil {
				t.Fatal("calendar failure leaked a connection", err)
			}
		})
		t.Run("resource_budgets", func(t *testing.T) {
			probe := &calendarReadProbe{calendarBackend: backend}
			value := at
			for range query.MaximumScalarDepth {
				value = value.Add(duration.FromMicroseconds(1))
			}
			if rows, err := orm.SelectInto(t.Context(), records.EventObjects.Using(probe), calendarProjection(value.Date().ScalarExpression)); rows != nil || err == nil || probe.calls() != 0 {
				t.Fatal("calendar operand depth bypassed", err)
			}
			wide := at.Year()
			for range 12 {
				wide = orm.AddExpressions(wide, wide)
			}
			if rows, err := orm.SelectInto(t.Context(), records.EventObjects.Using(probe), calendarProjection(wide.ScalarExpression)); rows != nil || err == nil || probe.calls() != 0 {
				t.Fatal("calendar node budget bypassed", err)
			}
			rows, err := orm.SelectInto(t.Context(), records.EventObjects.Using(probe).Filter(f.Code.Exact("a")), calendarProjection(at.Date().Year().ScalarExpression))
			check(t, err)
			assertCalendarJSON(t, rows, `[{"code":"a","value":2020}]`)
		})
		checkCalendarTruncation(t, backend, vendor, physical, seed, before)
		checkCalendarArithmetic(t, backend, vendor, physical, seed, before)
		if physical.Snapshot() != before {
			t.Fatal("calendar runtime changed physical model/relation storage")
		}
	})
}
