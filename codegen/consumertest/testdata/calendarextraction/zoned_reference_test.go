package consumer

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"example.com/godj-project-bundle/planning"
	"example.com/godj-project-bundle/project"
	"example.com/godj-project-bundle/records"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/timezone"
)

type zonedFixture struct {
	Name, Zone string
	Instants   []*string
}
type zonedFailureInput struct {
	Name, Zone, At, Function string
	Corrupt                  bool
}
type zonedInput struct {
	ProviderInstants []string `json:"provider_instants"`
	Operations       []string
	Fixtures         []zonedFixture
	Failures         []zonedFailureInput
}
type zonedWallConversion struct {
	Wall, Instant string
	Offset        int `json:"offset_seconds"`
	Fold          int
}
type zonedStatement struct {
	calendarReferenceStatement
	Columns     []string
	Conversions []*zonedWallConversion `json:"wall_conversions"`
}
type zonedCase struct {
	Name, Zone string
	Result     json.RawMessage
	Error      *calendarReferenceFailure
	Statements []zonedStatement
	Fields     map[string]string `json:"output_fields"`
	Before     [][][]*string     `json:"native_before"`
	After      [][][]*string     `json:"native_after"`
	Unchanged  bool              `json:"storage_unchanged"`
}
type zonedDocument struct {
	Provider []struct {
		Name   string
		Values []struct {
			Local, Error string
			Offset       *int `json:"offset_seconds"`
		}
	}
	Cases []zonedCase
}

func readZonedDocument(t *testing.T, vendor string) zonedDocument {
	t.Helper()
	data, err := os.ReadFile("zoned-django61-" + vendor + ".json")
	check(t, err)
	var document zonedDocument
	check(t, json.Unmarshal(data, &document))
	if len(document.Cases) != 316 || len(document.Provider) != 598 {
		t.Fatal("zoned reference document is incomplete")
	}
	return document
}

func TestZonedCalendarReference(t *testing.T) {
	data, err := os.ReadFile("zoned-input.json")
	check(t, err)
	var input zonedInput
	check(t, json.Unmarshal(data, &input))
	documents := map[string]zonedDocument{"sqlite": readZonedDocument(t, "sqlite"), "postgres": readZonedDocument(t, "postgres")}
	find := func(vendor, name string) zonedCase {
		for _, record := range documents[vendor].Cases {
			if record.Name == name {
				return record
			}
		}
		t.Fatal("missing named-zone reference", vendor, name)
		return zonedCase{}
	}
	t.Run("pinned_provider", func(t *testing.T) {
		if !reflect.DeepEqual(documents["sqlite"].Provider, documents["postgres"].Provider) {
			t.Fatal("independent ZoneInfo data changed between native observers")
		}
		names, err := timezone.Names()
		check(t, err)
		for index, zone := range documents["sqlite"].Provider {
			if zone.Name != names[index] || len(zone.Values) != len(input.ProviderInstants) {
				t.Fatal("provider inventory differs")
			}
			location, err := calendarZone(t, zone.Name).Location()
			check(t, err)
			for index, observation := range zone.Values {
				local := instant(input.ProviderInstants[index]).In(location)
				if local.Year() < 1 || local.Year() > 9999 {
					if observation.Error != "OverflowError" || observation.Local != "" || observation.Offset != nil {
						t.Fatal("provider local overflow differs", zone.Name, input.ProviderInstants[index], observation)
					}
					continue
				}
				_, offset := local.Zone()
				if observation.Error != "" || observation.Offset == nil || offset != *observation.Offset || local.Format("2006-01-02T15:04:05.000000") != observation.Local {
					t.Fatal("independent provider value differs", zone.Name, input.ProviderInstants[index], local, observation)
				}
			}
		}
	})
	for _, fixture := range input.Fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			withCalendarBackends(t, func(t *testing.T, backend calendarBackend, vendor string, physical calendarPhysical) {
				seedZonedFixture(t, backend, fixture.Instants)
				before := physical.Snapshot()
				for _, operation := range input.Operations {
					t.Run(operation, func(t *testing.T) {
						record := find(vendor, fixture.Name+"/"+operation)
						expected, err := validateZonedCase(vendor, fixture, operation, record, find(vendor, fixture.Name+"/trunc_hour"), find(vendor, fixture.Name+"/year"))
						check(t, err)
						var value any
						var failure error
						rollbackCalendar(t, backend, func(session db.Session) {
							value, failure = observeZonedCalendar(t, session, vendor, calendarZone(t, fixture.Zone), operation)
						})
						check(t, failure)
						assertCalendarJSON(t, value, string(expected))
						if physical.Snapshot() != before {
							t.Fatal("zoned comparison changed physical models or relations", operation)
						}
						if operation == "trunc_hour" {
							checkZonedReferenceMutations(t, vendor, fixture, record)
						}
					})
				}
			})
		})
	}
	for _, input := range input.Failures {
		t.Run("failure_"+input.Name, func(t *testing.T) {
			withCalendarBackends(t, func(t *testing.T, backend calendarBackend, vendor string, physical calendarPhysical) {
				seed := seedZonedFixture(t, backend, []*string{new(input.At), new("2024-06-01T00:00:00Z"), nil})
				if input.Corrupt {
					literal := "'2026-02-30 00:00:00'"
					if vendor == "postgres" {
						literal = "TIMESTAMPTZ '10000-01-01 00:00:00+00'"
					}
					check(t, physical.Update("event", "at", literal, seed[0].ID))
				}
				before := physical.Snapshot()
				record := find(vendor, "failure/"+input.Name)
				var value any
				var failure error
				rollbackCalendar(t, backend, func(session db.Session) {
					value, failure = observeZonedCalendar(t, session, vendor, calendarZone(t, input.Zone), input.Function)
				})
				checkZonedFailure(t, vendor, input, record, value, failure)
				if physical.Snapshot() != before {
					t.Fatal("zoned boundary comparison changed physical storage")
				}
			})
		})
	}
}

func seedZonedFixture(t *testing.T, backend db.Mutator, instants []*string) []records.Event {
	t.Helper()
	north, err := planning.ScheduleObjects.Create(t.Context(), backend, planning.NewScheduleCreate("north").WithAnchor(instant(*instants[0])).WithDay(date("2000-01-01")).WithWall(clock.Time{}))
	check(t, err)
	_, err = planning.ScheduleObjects.Create(t.Context(), backend, planning.NewScheduleCreate("unused"))
	check(t, err)
	rows := make([]records.Event, 0, len(instants))
	for index, text := range instants {
		bucket := "first"
		if index%2 != 0 {
			bucket = "second"
		}
		value := records.NewEventCreate(fmt.Sprintf("e%02d", index), bucket, index%2 == 0, instant("2024-02-29T00:00:00Z")).WithDay(calendar.Date{Year: 2000 + index, Month: 1, Day: 1}).WithWall(clock.Time{Hour: 12, Minute: 34, Second: 56, Microsecond: index}).WithPart(int64(100 + index))
		if text != nil {
			value = value.WithAt(instant(*text))
		}
		if index%3 != 2 {
			value = value.WithScheduleID(north.ID)
		}
		saved, err := records.EventObjects.Create(t.Context(), backend, value)
		check(t, err)
		rows = append(rows, saved)
		if index < 2 {
			detail := records.NewDetailCreate(saved.ID).WithDay(date("2000-01-01")).WithWall(clock.Time{})
			if text != nil {
				detail = detail.WithAt(instant(*text))
			}
			_, err = records.DetailObjects.Create(t.Context(), backend, detail)
			check(t, err)
		}
	}
	return rows
}

func readZonedScalar[T calendarValue](t *testing.T, session db.Session, vendor string, typed orm.ScalarExpression[records.Event, T], dynamic orm.DynamicExpression, filters ...orm.Predicate[records.Event]) (any, error) {
	t.Helper()
	var first, second []map[string]any
	firstError := db.WithSavepoint(t.Context(), session, func(reader db.Session) error {
		var err error
		first, err = orm.SelectInto(t.Context(), records.EventObjects.Using(reader).Filter(filters...).OrderBy(records.EventFields.Code.Asc()), calendarProjection(typed))
		return err
	})
	secondError := db.WithSavepoint(t.Context(), session, func(reader db.Session) error {
		api, err := project.UsingSession(reader)
		if err != nil {
			return err
		}
		expression, err := api.RecordsEvent.Expression(dynamic)
		if err != nil {
			return err
		}
		second, err = project.SelectRecordsEventInto(t.Context(), api.RecordsEvent.Filter(filters...).OrderBy(records.EventFields.Code.Asc()), orm.Project2(records.EventFields.Code, expression, func(code string, value query.Value) map[string]any {
			return map[string]any{"code": code, "value": dynamicCalendar(value)}
		}))
		return err
	})
	if (firstError == nil) != (secondError == nil) || !reflect.DeepEqual(first, second) {
		t.Fatal("zoned typed/dynamic outcome differs", first, second, firstError, secondError)
	}
	if firstError != nil {
		nativeCalendarError(t, vendor, firstError, "22007")
		nativeCalendarError(t, vendor, secondError, "22007")
	}
	return first, firstError
}

func observeZonedCalendar(t *testing.T, reader db.Session, vendor string, zone timezone.Zone, name string) (any, error) {
	f := records.EventFields
	base := records.EventObjects.Using(reader).OrderBy(f.Code.Asc())
	at := orm.DateTime(orm.F(f.At))
	zoned, dynamic := at.InZone(zone), orm.DynamicF("at").InZone(zone)
	partNames := map[string]string{"year": "Year", "iso_year": "ISOYear", "quarter": "Quarter", "month": "Month", "week": "Week", "week_day": "WeekDay", "iso_week_day": "ISOWeekDay", "day": "Day", "hour": "Hour", "minute": "Minute", "second": "Second"}
	if part, ok := partNames[name]; ok {
		return readZonedScalar(t, reader, vendor, zonedCalendarParts(zoned)[part].ScalarExpression, dynamicZonedCalendarParts(dynamic)[part])
	}
	if strings.HasPrefix(name, "trunc_") {
		unit := map[string]string{"trunc_year": "Year", "trunc_quarter": "Quarter", "trunc_month": "Month", "trunc_week": "Week", "trunc_day": "Day", "trunc_hour": "Hour", "trunc_minute": "Minute", "trunc_second": "Second"}[name]
		return readZonedScalar(t, reader, vendor, zonedCalendarTruncations(zoned)[unit].ScalarExpression, dynamicZonedCalendarTruncations(dynamic)[unit])
	}
	switch name {
	case "date", "filter_date":
		var filters []orm.Predicate[records.Event]
		if name == "filter_date" {
			filters = append(filters, zoned.Date().GreaterThanOrEqual(date("2024-01-01")))
		}
		return readZonedScalar(t, reader, vendor, zoned.Date().ScalarExpression, dynamic.Date(), filters...)
	case "time":
		return readZonedScalar(t, reader, vendor, zoned.Time().ScalarExpression, dynamic.Time())
	case "composed_date_year":
		return readZonedScalar(t, reader, vendor, zoned.Date().Year().ScalarExpression, dynamic.Date().Year())
	case "group_date":
		day := zoned.Date()
		grouped, err := orm.GroupBy(base.OrderBy(), orm.Project1(day, func(value orm.Optional[calendar.Date]) any { return optionalCalendar(value) }), orm.Aggregate4(orm.CountRows[records.Event](), orm.Count(f.ID).Where(f.Enabled.Exact(true)), orm.Min(at), orm.Max(at), func(total, opened int64, first, last orm.Optional[time.Time]) map[string]any {
			return map[string]any{"total": total, "opened": opened, "first": optionalCalendar(first), "last": optionalCalendar(last)}
		}), func(value any, row map[string]any) map[string]any { row["value"] = value; return row })
		if err != nil {
			return nil, err
		}
		return grouped.OrderBy(orm.GroupKey(day).Asc().NullsLast()).All(t.Context())
	case "related_date":
		relations, err := project.BindRelations()
		if err != nil {
			return nil, err
		}
		return orm.SelectInto(t.Context(), base, orm.Project3(f.Code, orm.DateTime(relations.RecordsEvent.Schedule.Anchor).InZone(zone).Date(), orm.DateTime(relations.RecordsEvent.Detail.At).InZone(zone).Date(), func(code string, anchor, detail orm.Optional[calendar.Date]) map[string]any {
			return map[string]any{"code": code, "anchor": optionalCalendar(anchor), "detail_date": optionalCalendar(detail)}
		}))
	case "collection_date":
		relations, err := project.BindRelations()
		if err != nil {
			return nil, err
		}
		events := relations.PlanningSchedule.Events
		day := orm.DateTime(events.At).InZone(zone).Date()
		grouped, err := orm.GroupBy(planning.ScheduleObjects.Using(reader), orm.Project1(planning.ScheduleFields.Code, func(code string) string { return code }), orm.Aggregate4(orm.Count(events.ID), orm.Count(day).Distinct(), orm.Min(day), orm.Max(orm.DateTime(events.At)), func(total, days int64, first orm.Optional[calendar.Date], last orm.Optional[time.Time]) map[string]any {
			return map[string]any{"total": total, "days": days, "first": optionalCalendar(first), "last": optionalCalendar(last)}
		}), func(code string, row map[string]any) map[string]any { row["code"] = code; return row })
		if err != nil {
			return nil, err
		}
		return grouped.OrderBy(planning.ScheduleFields.Code.Asc()).All(t.Context())
	case "update_calendar", "update_truncated":
		assignments := []orm.UpdateAssignment[records.Event]{orm.AssignExpression(f.Day, zoned.Date()), orm.AssignExpression(f.Wall, zoned.Time()), orm.AssignExpression(f.Part, orm.Date(orm.F(f.Day)).Year())}
		if name == "update_truncated" {
			assignments = []orm.UpdateAssignment[records.Event]{orm.AssignExpression(f.At, zoned.TruncHour()), orm.AssignExpression(f.Part, zoned.Year())}
		}
		count, err := base.Update(t.Context(), assignments...)
		if err != nil {
			return nil, err
		}
		values, err := base.Fresh().All(t.Context())
		if err != nil {
			return nil, err
		}
		rows := make([]map[string]any, 0, len(values))
		for _, row := range values {
			rows = append(rows, map[string]any{"code": row.Code, "at": pointerCalendar(row.At), "day": pointerCalendar(row.Day), "wall": pointerCalendar(row.Wall), "part": pointerCalendar(row.Part)})
		}
		return map[string]any{"changed": count, "rows": rows}, nil
	case "case_year":
		return orm.SelectInto(t.Context(), base, calendarProjection(orm.Case(orm.Value[records.Event](int64(0)), orm.When(f.Code.Exact("never"), zoned.Year()))))
	case "filtered_year":
		return orm.AggregateInto(t.Context(), base, orm.Aggregate1(orm.Sum(zoned.Year()).Where(orm.Not(f.Code.Exact("e00"))), func(value orm.Optional[int64]) map[string]any {
			return map[string]any{"value": optionalCalendar(value)}
		}))
	default:
		return nil, fmt.Errorf("unknown zoned calendar operation %q", name)
	}
}

func zonedFields(operation string) map[string]string {
	switch operation {
	case "date", "filter_date":
		return map[string]string{"value": "DateField"}
	case "time":
		return map[string]string{"value": "TimeField"}
	case "group_date":
		return map[string]string{"value": "DateField", "total": "IntegerField", "opened": "IntegerField", "first": "DateTimeField", "last": "DateTimeField"}
	case "related_date":
		return map[string]string{"anchor": "DateField", "detail_date": "DateField"}
	case "collection_date":
		return map[string]string{"total": "IntegerField", "days": "IntegerField", "first": "DateField", "last": "DateTimeField"}
	case "update_calendar", "update_truncated":
		return map[string]string{}
	default:
		if strings.HasPrefix(operation, "trunc_") {
			return map[string]string{"value": "DateTimeField"}
		}
		return map[string]string{"value": "IntegerField"}
	}
}

func validateZonedPhysical(vendor string, snapshot [][][]*string, events int) error {
	if len(snapshot) != 3 {
		return errors.New("zoned physical table inventory differs")
	}
	kinds := [][]string{
		{"bigint", "character varying", "timestamp with time zone", "date", "time without time zone"},
		{"bigint", "character varying", "character varying", "boolean", "timestamp with time zone", "timestamp with time zone", "date", "time without time zone", "bigint", "bigint"},
		{"bigint", "bigint", "timestamp with time zone", "date", "time without time zone"},
	}
	for table, count := range []int{2, events, 2} {
		if len(snapshot[table]) != count {
			return errors.New("zoned physical row count differs")
		}
		for _, row := range snapshot[table] {
			if len(row) != len(kinds[table])*2 {
				return errors.New("zoned physical column count differs")
			}
			for column, kind := range kinds[table] {
				if row[column*2] == nil {
					return errors.New("zoned physical type missing")
				}
				actual := *row[column*2]
				if vendor == "postgres" {
					if actual != kind {
						return fmt.Errorf("zoned physical PostgreSQL type differs: %s/%s", actual, kind)
					}
					continue
				}
				want := "text"
				if kind == "bigint" || kind == "boolean" {
					want = "integer"
				}
				if actual != want && actual != "null" {
					return errors.New("zoned physical SQLite type differs")
				}
				if row[column*2+1] == nil || actual == "null" && *row[column*2+1] != "NULL" {
					return errors.New("zoned physical SQLite value missing")
				}
			}
		}
	}
	return nil
}

func validateZonedSQL(vendor, zone string, statement zonedStatement) error {
	if strings.TrimSpace(statement.SQL) == "" {
		return errors.New("zoned native SQL missing")
	}
	want := zone
	if zone == "Etc/GMT+5" {
		want = "-05"
		if vendor == "postgres" {
			want = "+05:00"
		}
	}
	bound := false
	for _, parameter := range statement.Parameters {
		var text string
		if parameter.Type == "str" && json.Unmarshal(parameter.Value, &text) == nil && text == want {
			bound = true
		}
	}
	if !bound || vendor == "postgres" && !strings.Contains(statement.SQL, "AT TIME ZONE %s") || vendor == "sqlite" && !strings.Contains(statement.SQL, "django_datetime_") {
		return errors.New("native query lost its explicit timezone binding")
	}
	return nil
}

func zonedStoredValue(vendor, field string, row []*string) (json.RawMessage, error) {
	index := map[string]int{"code": 3, "at": 11, "day": 13, "wall": 15, "part": 17}[field]
	if row[index] == nil || vendor == "sqlite" && *row[index-1] == "null" {
		return json.RawMessage("null"), nil
	}
	text := *row[index]
	if field == "part" {
		value, err := strconv.ParseInt(text, 10, 64)
		if err != nil || strconv.FormatInt(value, 10) != text {
			return nil, errors.New("stored zoned integer is not canonical")
		}
		return json.RawMessage(text), nil
	}
	if vendor == "sqlite" {
		if len(text) < 2 || text[0] != '\'' || text[len(text)-1] != '\'' {
			return nil, errors.New("stored zoned text lost SQL quoting")
		}
		text = strings.ReplaceAll(text[1:len(text)-1], "''", "'")
	}
	switch field {
	case "at":
		if vendor == "postgres" {
			if !strings.HasSuffix(text, "+00") {
				return nil, errors.New("stored zoned instant lost UTC offset")
			}
			text = strings.TrimSuffix(text, "+00")
		}
		value, err := time.Parse("2006-01-02 15:04:05.999999", text)
		if err != nil || value.Year() < 1 || value.Year() > 9999 || value.Nanosecond()%1000 != 0 {
			return nil, errors.New("stored zoned instant is outside its domain")
		}
		text = value.Format("2006-01-02T15:04:05.000000Z")
	case "day":
		if _, err := calendar.Parse(text); err != nil {
			return nil, err
		}
	case "wall":
		value, err := time.Parse("15:04:05.999999", text)
		if err != nil || value.Nanosecond()%1000 != 0 {
			return nil, errors.New("stored zoned clock is outside its domain")
		}
		text = (clock.Time{Hour: value.Hour(), Minute: value.Minute(), Second: value.Second(), Microsecond: value.Nanosecond() / 1000}).String()
	}
	return json.Marshal(text)
}

func zonedNativeValue(vendor, kind string, numeric bool, raw calendarNativeValue, conversion *zonedWallConversion) (json.RawMessage, error) {
	invalid := func() (json.RawMessage, error) { return nil, fmt.Errorf("native zoned value lost its %s domain", kind) }
	if raw.Type == "NoneType" {
		if string(raw.Value) != "null" || raw.Aware || conversion != nil {
			return invalid()
		}
		return json.RawMessage("null"), nil
	}
	if kind == "IntegerField" {
		want := "int"
		if vendor == "postgres" && numeric {
			want = "Decimal"
		}
		if raw.Type != want || raw.Aware || conversion != nil {
			return invalid()
		}
		if want == "Decimal" {
			var text string
			if json.Unmarshal(raw.Value, &text) != nil {
				return invalid()
			}
			value, ok := new(big.Rat).SetString(text)
			if !ok || !value.IsInt() || !value.Num().IsInt64() {
				return invalid()
			}
			return json.RawMessage(value.Num().String()), nil
		}
		value, err := strconv.ParseInt(string(raw.Value), 10, 64)
		if err != nil || strconv.FormatInt(value, 10) != string(raw.Value) {
			return invalid()
		}
		return raw.Value, nil
	}
	var text string
	if json.Unmarshal(raw.Value, &text) != nil {
		return invalid()
	}
	if kind == "code" {
		if raw.Type != "str" || raw.Aware || conversion != nil {
			return invalid()
		}
		return raw.Value, nil
	}
	if kind == "DateField" {
		want := "str"
		if vendor == "postgres" {
			want = "date"
		}
		value, err := calendar.Parse(text)
		if raw.Type != want || raw.Aware || conversion != nil || err != nil || value.String() != text {
			return invalid()
		}
		return raw.Value, nil
	}
	if kind == "TimeField" {
		want := "str"
		if vendor == "postgres" {
			want = "time"
		}
		value, err := time.Parse("15:04:05.999999", text)
		if raw.Type != want || raw.Aware || conversion != nil || err != nil || value.Nanosecond()%1000 != 0 {
			return invalid()
		}
		wall := clock.Time{Hour: value.Hour(), Minute: value.Minute(), Second: value.Second(), Microsecond: value.Nanosecond() / 1000}
		if vendor == "postgres" && value.Format("15:04:05.000000") != text || vendor == "sqlite" && wall.String() != text {
			return invalid()
		}
		return json.Marshal(wall.String())
	}
	if kind != "DateTimeField" {
		return invalid()
	}
	if vendor == "postgres" && raw.Type != "datetime" || vendor == "sqlite" && raw.Type != "str" {
		return invalid()
	}
	if conversion != nil {
		layout := "2006-01-02 15:04:05"
		if vendor == "postgres" {
			layout = "2006-01-02T15:04:05.000000"
		}
		wall, err := time.Parse(layout, text)
		if raw.Aware || err != nil || wall.Format(layout) != text || conversion.Fold != 0 || conversion.Offset < -86400 || conversion.Offset > 86400 || conversion.Wall != wall.Format("2006-01-02T15:04:05.000000") {
			return invalid()
		}
		value := wall.Add(-time.Duration(conversion.Offset) * time.Second)
		if value.Year() < 1 || value.Year() > 9999 || conversion.Instant != value.Format("2006-01-02T15:04:05.000000Z") {
			return invalid()
		}
		return json.Marshal(conversion.Instant)
	}
	if vendor == "postgres" {
		value, err := time.Parse(time.RFC3339Nano, text)
		_, offset := value.Zone()
		if !raw.Aware || err != nil || offset != 0 || value.Nanosecond()%1000 != 0 || value.Format("2006-01-02T15:04:05.000000-07:00") != text {
			return invalid()
		}
		return json.Marshal(value.UTC().Format("2006-01-02T15:04:05.000000Z"))
	}
	value, err := time.Parse("2006-01-02 15:04:05.999999", text)
	if raw.Aware || err != nil || value.Nanosecond()%1000 != 0 || value.Format("2006-01-02 15:04:05.999999") != text {
		return invalid()
	}
	return json.Marshal(value.UTC().Format("2006-01-02T15:04:05.000000Z"))
}

func validateZonedRead(vendor, operation string, record zonedCase) error {
	if record.Error != nil || len(record.Result) == 0 || len(record.Statements) != 1 || !maps.Equal(record.Fields, zonedFields(operation)) {
		return errors.New("zoned read lacks its complete success and field contract")
	}
	statement := record.Statements[0]
	if err := validateZonedSQL(vendor, record.Zone, statement); err != nil {
		return err
	}
	if statement.Error != nil || statement.NativeError != nil || statement.Rows == nil || !strings.HasPrefix(statement.SQL, "SELECT") {
		return errors.New("zoned SELECT lacks a successful native replay")
	}
	columns := []string{"code", "value"}
	switch operation {
	case "group_date":
		columns = []string{"value", "total", "opened", "first", "last"}
	case "related_date":
		columns = []string{"code", "anchor", "detail_date"}
	case "collection_date":
		columns = []string{"code", "total", "days", "first", "last"}
	case "filtered_year":
		columns = []string{"value"}
	}
	if !slices.Equal(statement.Columns, columns) {
		return errors.New("zoned native column identity differs")
	}
	var rows []map[string]json.RawMessage
	if operation == "filtered_year" {
		var row map[string]json.RawMessage
		if err := json.Unmarshal(record.Result, &row); err != nil {
			return err
		}
		rows = []map[string]json.RawMessage{row}
	} else if err := json.Unmarshal(record.Result, &rows); err != nil {
		return err
	}
	if rows == nil || len(rows) != len(statement.Rows) || vendor == "postgres" && statement.Rowcount != int64(len(rows)) || vendor == "sqlite" && statement.Rowcount != -1 {
		return errors.New("zoned native row count differs")
	}
	truncated := strings.HasPrefix(operation, "trunc_")
	if truncated && len(statement.Conversions) != len(rows) || !truncated && statement.Conversions != nil {
		return errors.New("zoned wall conversion inventory differs")
	}
	for index, row := range rows {
		if len(row) != len(columns) || len(statement.Rows[index]) != len(columns) {
			return errors.New("zoned native result width differs")
		}
		for column, name := range columns {
			observed, found := row[name]
			if !found {
				return errors.New("zoned converted output missing")
			}
			kind := record.Fields[name]
			if name == "code" {
				kind = "code"
			}
			var conversion *zonedWallConversion
			if truncated && name == "value" {
				conversion = statement.Conversions[index]
				if string(observed) != "null" && conversion == nil {
					return errors.New("zoned truncation lost its fold conversion")
				}
			}
			numeric := kind == "IntegerField" && operation != "group_date" && operation != "collection_date"
			value, err := zonedNativeValue(vendor, kind, numeric, statement.Rows[index][column], conversion)
			if err != nil {
				return err
			}
			if !jsonEqual(value, observed) {
				return errors.New("zoned native and converted values differ")
			}
		}
	}
	return nil
}

func validateZonedCase(vendor string, fixture zonedFixture, operation string, record, truncated, years zonedCase) (json.RawMessage, error) {
	if record.Name != fixture.Name+"/"+operation || record.Zone != fixture.Zone || !record.Unchanged || record.Error != nil || !maps.Equal(record.Fields, zonedFields(operation)) {
		return nil, errors.New("zoned case identity or success differs")
	}
	if err := validateZonedPhysical(vendor, record.Before, len(fixture.Instants)); err != nil {
		return nil, err
	}
	if err := validateZonedPhysical(vendor, record.After, len(fixture.Instants)); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(operation, "update_") {
		if !reflect.DeepEqual(record.Before, record.After) {
			return nil, errors.New("independent zoned read changed storage")
		}
		return record.Result, validateZonedRead(vendor, operation, record)
	}
	if len(record.Statements) != 2 || !strings.HasPrefix(record.Statements[0].SQL, "UPDATE") || !strings.HasPrefix(record.Statements[1].SQL, "SELECT") || record.Statements[0].Rowcount != int64(len(fixture.Instants)) {
		return nil, errors.New("zoned UPDATE lost its statement/count boundary")
	}
	if err := validateZonedSQL(vendor, record.Zone, record.Statements[0]); err != nil {
		return nil, err
	}
	for _, statement := range record.Statements {
		if statement.Error != nil || statement.NativeError != nil || statement.Rows != nil || statement.Conversions != nil {
			return nil, errors.New("zoned UPDATE was replayed or failed")
		}
	}
	changed := map[int]bool{13: true, 15: true, 17: true}
	if operation == "update_truncated" {
		changed = map[int]bool{11: true, 17: true}
	}
	for table, rows := range record.Before {
		for index, row := range rows {
			for column, value := range row {
				if table == 1 && (changed[column] || column%2 == 0 && changed[column+1]) {
					continue
				}
				if !reflect.DeepEqual(value, record.After[table][index][column]) {
					return nil, errors.New("zoned UPDATE changed an unassigned physical column or relation")
				}
			}
		}
	}
	var result struct {
		Changed int64                        `json:"changed"`
		Rows    []map[string]json.RawMessage `json:"rows"`
	}
	if err := json.Unmarshal(record.Result, &result); err != nil {
		return nil, err
	}
	if result.Changed != int64(len(fixture.Instants)) || len(result.Rows) != len(fixture.Instants) {
		return nil, errors.New("zoned UPDATE converted rows differ")
	}
	storedRows := map[string][]*string{}
	for _, row := range record.After[1] {
		code, err := zonedStoredValue(vendor, "code", row)
		if err != nil {
			return nil, err
		}
		if _, duplicate := storedRows[string(code)]; duplicate {
			return nil, errors.New("duplicate physical zoned event code")
		}
		storedRows[string(code)] = row
	}
	var hours, parts []map[string]json.RawMessage
	if operation == "update_truncated" {
		if err := validateZonedRead(vendor, "trunc_hour", truncated); err != nil {
			return nil, err
		}
		if err := validateZonedRead(vendor, "year", years); err != nil {
			return nil, err
		}
		if json.Unmarshal(truncated.Result, &hours) != nil || json.Unmarshal(years.Result, &parts) != nil || len(hours) != len(result.Rows) || len(parts) != len(result.Rows) {
			return nil, errors.New("zoned UPDATE lacks independent original-row observations")
		}
	}
	for index, row := range result.Rows {
		if len(row) != 5 {
			return nil, errors.New("zoned UPDATE row width differs")
		}
		storedRow, found := storedRows[string(row["code"])]
		if !found {
			return nil, errors.New("zoned UPDATE row missing from physical storage")
		}
		for _, name := range []string{"code", "at", "day", "wall", "part"} {
			if _, ok := row[name]; !ok {
				return nil, errors.New("zoned UPDATE omitted a column")
			}
			stored, err := zonedStoredValue(vendor, name, storedRow)
			if err != nil {
				return nil, err
			}
			if !jsonEqual(stored, row[name]) {
				return nil, errors.New("zoned UPDATE result differs from its physical storage")
			}
		}
		code, _ := json.Marshal(fmt.Sprintf("e%02d", index))
		if !jsonEqual(code, row["code"]) {
			return nil, errors.New("zoned UPDATE order differs")
		}
		if operation == "update_calendar" {
			var original any
			if text := fixture.Instants[index]; text != nil {
				original = wireCalendar(instant(*text))
			}
			expected, _ := json.Marshal(original)
			if !jsonEqual(expected, row["at"]) || string(row["part"]) != strconv.Itoa(2000+index) {
				return nil, errors.New("zoned UPDATE lost original field values")
			}
		} else {
			if !jsonEqual(code, hours[index]["code"]) || !jsonEqual(code, parts[index]["code"]) || !jsonEqual(row["part"], parts[index]["value"]) {
				return nil, errors.New("zoned UPDATE lost original-row year")
			}
			day, _ := json.Marshal(fmt.Sprintf("%04d-01-01", 2000+index))
			wall, _ := json.Marshal((clock.Time{Hour: 12, Minute: 34, Second: 56, Microsecond: index}).String())
			if !jsonEqual(day, row["day"]) || !jsonEqual(wall, row["wall"]) {
				return nil, errors.New("zoned UPDATE changed date or clock")
			}
			var expected any
			if conversion := truncated.Statements[0].Conversions[index]; conversion != nil {
				expected = conversion.Wall + "Z"
			}
			stored, _ := json.Marshal(expected)
			if !jsonEqual(stored, row["at"]) {
				return nil, errors.New("Django truncated UPDATE no longer stores its native UTC-interpreted wall value")
			}
			// GoDj assigns the declared UTC instant. Compare against the direct
			// independently converted Trunc observation; retain the original
			// Django UPDATE document as the explicit external difference.
			row["at"] = hours[index]["value"]
		}
	}
	if operation == "update_calendar" {
		return record.Result, nil
	}
	return json.Marshal(result)
}

func checkZonedReferenceMutations(t *testing.T, vendor string, fixture zonedFixture, record zonedCase) {
	t.Helper()
	for name, mutate := range map[string]func(*zonedCase){
		"zone":           func(v *zonedCase) { v.Zone = "unknown" },
		"field":          func(v *zonedCase) { v.Fields["value"] = "DateField" },
		"native rows":    func(v *zonedCase) { v.Statements[0].Rows = nil },
		"native type":    func(v *zonedCase) { v.Statements[0].Rows[0][1].Type = "date" },
		"native value":   func(v *zonedCase) { v.Statements[0].Rows[0][1].Value = json.RawMessage(`"bad"`) },
		"columns":        func(v *zonedCase) { v.Statements[0].Columns[1] = "other" },
		"parameters":     func(v *zonedCase) { v.Statements[0].Parameters = nil },
		"fold":           func(v *zonedCase) { v.Statements[0].Conversions[0].Fold = 1 },
		"offset":         func(v *zonedCase) { v.Statements[0].Conversions[0].Offset++ },
		"conversion":     func(v *zonedCase) { v.Statements[0].Conversions[0].Instant = "2000-01-01T00:00:00.000000Z" },
		"NULL":           func(v *zonedCase) { v.Statements[0].Rows[len(v.Statements[0].Rows)-1][1].Type = "str" },
		"physical table": func(v *zonedCase) { v.Before = v.Before[:2] },
		"storage":        func(v *zonedCase) { v.After[1][0][3] = new("changed") },
		"result":         func(v *zonedCase) { v.Result = json.RawMessage(`[]`) },
	} {
		data, err := json.Marshal(record)
		check(t, err)
		var changed zonedCase
		check(t, json.Unmarshal(data, &changed))
		mutate(&changed)
		if _, err := validateZonedCase(vendor, fixture, "trunc_hour", changed, zonedCase{}, zonedCase{}); err == nil {
			t.Fatal("zoned reference mutation accepted", name)
		}
	}
}

func checkZonedFailure(t *testing.T, vendor string, input zonedFailureInput, record zonedCase, value any, productError error) {
	t.Helper()
	if record.Name != "failure/"+input.Name || record.Zone != input.Zone || !record.Unchanged || !maps.Equal(record.Fields, zonedFields(input.Function)) {
		t.Fatal("zoned failure contract differs", record)
	}
	check(t, validateZonedPhysical(vendor, record.Before, 3))
	if len(record.Statements) != 1 {
		t.Fatal("zoned failure lacks one statement")
	}
	check(t, validateZonedSQL(vendor, record.Zone, record.Statements[0]))
	if input.Name == "unselected_corruption" || input.Name == "filtered_corruption" {
		check(t, validateZonedRead(vendor, input.Function, record))
		check(t, productError)
		if !reflect.DeepEqual(record.Before, record.After) {
			t.Fatal("excluded zoned corruption changed storage")
		}
		want := `[{"code":"e00","value":0},{"code":"e01","value":0},{"code":"e02","value":0}]`
		if input.Name == "filtered_corruption" {
			want = `{"value":2024}`
		}
		if !jsonEqual(record.Result, json.RawMessage(want)) {
			t.Fatal("Django evaluated excluded zoned corruption", string(record.Result))
		}
		assertCalendarJSON(t, value, want)
		return
	}
	nativeCalendarError(t, vendor, productError, "22007")
	assertCalendarJSON(t, value, `null`)
	if input.Name == "corrupt_month" || vendor == "postgres" && (input.Name == "lower_year" || input.Name == "upper_year") {
		check(t, validateZonedRead(vendor, input.Function, record))
		if !reflect.DeepEqual(record.Before, record.After) {
			t.Fatal("successful upstream extraction changed storage")
		}
		first, second := "null", "5"
		if vendor == "postgres" {
			first = "12"
		}
		if input.Name == "lower_year" {
			first, second = "-1", "2024"
		}
		if input.Name == "upper_year" {
			first, second = "10000", "2024"
		}
		want := `[{"code":"e00","value":` + first + `},{"code":"e01","value":` + second + `},{"code":"e02","value":null}]`
		if !jsonEqual(record.Result, json.RawMessage(want)) {
			t.Fatal("closed upstream domain difference changed", input.Name, string(record.Result), want)
		}
		return
	}
	kind, stage, nativeStage := "DataError", "sql", "native_execute"
	if vendor == "postgres" {
		kind, stage, nativeStage = "DataError", "conversion", "native_fetch"
	}
	if input.Name == "inverse_lower" {
		kind, stage, nativeStage = "OverflowError", "conversion", "zoneinfo_conversion"
	}
	if record.Error == nil || record.Error.Type != kind || record.Error.Stage != stage || record.Error.SQLState != "" || record.Error.Message == "" || len(record.Result) != 0 || record.After != nil {
		t.Fatal("zoned upstream failure stage differs", input.Name, record.Error, string(record.Result))
	}
	statement := record.Statements[0]
	if stage == "sql" {
		if statement.Error == nil || statement.Error.Type != kind || statement.Error.Stage != stage || statement.Error.SQLState != "" {
			t.Fatal("zoned SQL failure lost native execute error")
		}
	} else if statement.Error != nil {
		t.Fatal("zoned conversion failure became a SQL execution failure")
	}
	if statement.NativeError == nil || statement.NativeError.Type != kind || statement.NativeError.Stage != nativeStage || statement.NativeError.SQLState != "" {
		t.Fatal("zoned native replay error differs", input.Name, statement.NativeError)
	}
}
