package consumer

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type calendarReferenceFailure struct{ Type, Message, Stage, SQLState string }
type calendarNativeValue struct {
	Type  string
	Value json.RawMessage
	Aware bool
}
type calendarReferenceStatement struct {
	SQL         string
	Parameters  []calendarNativeValue
	Rowcount    int64
	Rows        [][]calendarNativeValue `json:"native_rows"`
	Error       *calendarReferenceFailure
	NativeError *calendarReferenceFailure `json:"native_error"`
}
type calendarReferenceCase struct {
	Name          string
	Result        json.RawMessage
	Error         *calendarReferenceFailure
	Statements    []calendarReferenceStatement
	Fields        map[string]string `json:"output_fields"`
	Before, After json.RawMessage
	NativeBefore  [][][]*string `json:"native_before"`
	NativeAfter   [][][]*string `json:"native_after"`
	Unchanged     bool          `json:"storage_unchanged"`
}

func calendarReferenceFields(name string) map[string]string {
	switch name {
	case "invalid_date_hour", "invalid_duration_year", "update_calendar", "update_filtered":
		return nil
	case "datetime_date", "case_date", "date_after_shift", "filter_date_range", "distinct_dates":
		return map[string]string{"value": "DateField"}
	case "datetime_time", "case_time", "order_clock":
		return map[string]string{"value": "TimeField"}
	case "literal_components":
		return map[string]string{"year": "IntegerField", "iso_year": "IntegerField", "clock": "TimeField"}
	case "group_by_date":
		return map[string]string{"value": "DateField", "total": "IntegerField", "opened": "IntegerField", "first": "TimeField", "last": "TimeField"}
	case "group_iso_week":
		return map[string]string{"year": "IntegerField", "week": "IntegerField", "total": "IntegerField"}
	case "aggregate_parts":
		return map[string]string{"years": "IntegerField", "seconds": "FloatField", "first": "DateField", "last": "TimeField"}
	case "filtered_aggregate":
		return map[string]string{"years": "IntegerField", "dates": "IntegerField"}
	case "related_parts":
		return map[string]string{"anchor": "DateField", "schedule_year": "IntegerField", "schedule_second": "IntegerField", "detail": "DateField", "detail_day": "IntegerField"}
	case "collection_years":
		return map[string]string{"years": "IntegerField", "opened": "IntegerField", "first": "DateField"}
	case "literal_aggregate", "filtered_corrupt_date":
		return map[string]string{"years": "IntegerField"}
	case "empty_aggregate":
		return map[string]string{"years": "IntegerField", "first": "DateField", "count": "IntegerField"}
	case "empty_group":
		return map[string]string{"value": "DateField", "total": "IntegerField"}
	default:
		return map[string]string{"value": "IntegerField"}
	}
}

func validateCalendarPhysical(t *testing.T, snapshot [][][]*string) {
	t.Helper()
	if len(snapshot) != 3 {
		t.Fatal("calendar reference lost a physical table")
	}
	for table, count := range []int{4, 11, 3} {
		if len(snapshot[table]) != count {
			t.Fatal("calendar physical row count changed", table, len(snapshot[table]))
		}
		columns := 10
		if table == 1 {
			columns = 20
		}
		for _, row := range snapshot[table] {
			if len(row) != columns {
				t.Fatal("calendar physical column count changed", table, len(row))
			}
			for index := 0; index < len(row); index += 2 {
				if row[index] == nil || *row[index] == "" {
					t.Fatal("calendar physical type missing")
				}
			}
		}
	}
}

func requireCalendarReferenceError(t *testing.T, record calendarReferenceCase, kind, stage, state string) {
	t.Helper()
	if record.Error == nil || record.Error.Type != kind || record.Error.Stage != stage || record.Error.SQLState != state || record.Error.Message == "" || len(record.Result) != 0 {
		t.Fatal("independent calendar error differs", record.Name, record.Error, string(record.Result))
	}
	if stage == "expression" {
		if len(record.Statements) != 0 {
			t.Fatal("expression failure executed SQL", record.Name)
		}
		return
	}
	if len(record.Statements) != 1 {
		t.Fatal("calendar error lacks one native statement", record.Name)
	}
	statement := record.Statements[0]
	if stage == "sql" {
		if statement.Error == nil || statement.NativeError == nil || statement.Error.Type != kind || statement.Error.Stage != "sql" || statement.Error.SQLState != state || statement.NativeError.Type != kind || statement.NativeError.Stage != "native_execute" || statement.NativeError.SQLState != state {
			t.Fatal("calendar SQL failure lost native cause", record.Name, statement)
		}
	} else if stage == "conversion" {
		if statement.Error != nil || statement.NativeError != nil || len(statement.Rows) != 11 {
			t.Fatal("calendar conversion error replaced native rows", record.Name, statement)
		}
	} else {
		t.Fatal("unknown calendar error stage")
	}
}

// Native replay and the converted value are checked independently. In
// particular PostgreSQL numeric EXTRACT must become an integral int, and the
// Date/Time driver types must retain the separate calendar/clock domains.
func validateCalendarScalarReplay(t *testing.T, vendor string, record calendarReferenceCase) {
	t.Helper()
	if record.Error != nil || len(record.Fields) != 1 || record.Fields["value"] == "" {
		return
	}
	var converted []map[string]json.RawMessage
	check(t, json.Unmarshal(record.Result, &converted))
	if len(record.Statements) != 1 || len(record.Statements[0].Rows) != len(converted) {
		t.Fatal("calendar scalar replay row count differs", record.Name)
	}
	for index, row := range record.Statements[0].Rows {
		valueColumn := 0
		if code, ok := converted[index]["code"]; ok {
			valueColumn = 1
			if len(row) != 2 || row[0].Type != "str" || !jsonEqual(row[0].Value, code) {
				t.Fatal("calendar native code/order changed", record.Name, index)
			}
		} else if len(row) != 1 {
			t.Fatal("calendar scalar replay width differs", record.Name)
		}
		raw, want := row[valueColumn], converted[index]["value"]
		if string(want) == "null" {
			if raw.Type != "NoneType" || string(raw.Value) != "null" {
				t.Fatal("calendar NULL lost its native identity", record.Name, index)
			}
			continue
		}
		kind := record.Fields["value"]
		nativeType := "str"
		if vendor == "postgres" {
			switch kind {
			case "IntegerField":
				nativeType = "Decimal"
			case "DateField":
				nativeType = "date"
			case "TimeField":
				nativeType = "time"
			}
		} else if kind == "IntegerField" {
			nativeType = "int"
		}
		// CASE's Value(0) is an int on PostgreSQL as well. Extract itself stays numeric.
		if record.Name == "unselected_corrupt_date" && vendor == "postgres" {
			nativeType = "Decimal"
		}
		if raw.Type != nativeType {
			t.Fatal("calendar replay changed native domain", record.Name, index, raw.Type, nativeType)
		}
		if nativeType == "Decimal" {
			var text string
			check(t, json.Unmarshal(raw.Value, &text))
			value, err := strconv.ParseInt(text, 10, 64)
			check(t, err)
			if !jsonEqual(json.RawMessage(strconv.FormatInt(value, 10)), want) {
				t.Fatal("numeric calendar conversion changed", record.Name, index)
			}
		} else if nativeType == "time" {
			var text string
			check(t, json.Unmarshal(raw.Value, &text))
			text = strings.TrimSuffix(text, ".000000")
			data, err := json.Marshal(text)
			check(t, err)
			if raw.Aware || !jsonEqual(data, want) {
				t.Fatal("clock acquired an offset or lost precision", record.Name, index)
			}
		} else if !jsonEqual(raw.Value, want) {
			t.Fatal("calendar native/converted values differ", record.Name, index, string(raw.Value), string(want))
		}
	}
}
func jsonEqual(a, b json.RawMessage) bool {
	var first, second any
	return json.Unmarshal(a, &first) == nil && json.Unmarshal(b, &second) == nil && reflect.DeepEqual(first, second)
}

func TestCalendarReference(t *testing.T) {
	withCalendarBackends(t, func(t *testing.T, backend calendarBackend, vendor string, physical calendarPhysical) {
		data, err := os.ReadFile("django61-" + vendor + ".json")
		check(t, err)
		var reference struct{ Cases []calendarReferenceCase }
		check(t, json.Unmarshal(data, &reference))
		if len(reference.Cases) != 55 {
			t.Fatal("calendar reference incomplete")
		}
		seed := seedCalendar(t, backend)
		before := physical.Snapshot()
		for _, record := range reference.Cases {
			t.Run(record.Name, func(t *testing.T) {
				if !record.Unchanged || !maps.Equal(record.Fields, calendarReferenceFields(record.Name)) {
					t.Fatal("calendar output kinds or storage record differ", record.Name, record.Fields)
				}
				validateCalendarPhysical(t, record.NativeBefore)
				if record.Error == nil {
					validateCalendarPhysical(t, record.NativeAfter)
					if !strings.HasPrefix(record.Name, "update_") && !reflect.DeepEqual(record.NativeBefore, record.NativeAfter) {
						t.Fatal("independent read changed storage", record.Name)
					}
				}
				if len(record.Statements) == 0 && !slices.Contains([]string{"empty_aggregate", "empty_group", "invalid_date_hour", "invalid_duration_year"}, record.Name) {
					t.Fatal("calendar case has no SQL evidence", record.Name)
				}
				for _, statement := range record.Statements {
					if strings.TrimSpace(statement.SQL) == "" {
						t.Fatal("calendar SQL is empty")
					}
					for _, parameter := range statement.Parameters {
						if !slices.Contains([]string{"NoneType", "bool", "int", "Int4", "Int8", "str", "datetime", "date", "time", "timedelta"}, parameter.Type) {
							t.Fatal("unknown calendar parameter type", parameter.Type)
						}
					}
					if record.Error == nil && (statement.Error != nil || statement.NativeError != nil) {
						t.Fatal("successful calendar result contains native error", record.Name)
					}
					if record.Error == nil && strings.HasPrefix(statement.SQL, "SELECT") && !strings.HasPrefix(record.Name, "update_") && statement.Rows == nil {
						t.Fatal("calendar SELECT lacks native replay", record.Name)
					}
				}
				var value any
				var failure error
				run := func() {
					rollbackCalendar(t, backend, func(session db.Session) { value, failure = observeCalendar(t, session, record.Name) })
				}
				if slices.Contains([]string{"invalid_date_storage", "outside_date_storage", "invalid_datetime_storage", "infinite_datetime_storage", "clock_24_storage", "unselected_corrupt_date", "filtered_corrupt_date"}, record.Name) {
					withCalendarCorruption(t, physical, seed, vendor, record.Name, run)
				} else {
					run()
				}
				if physical.Snapshot() != before {
					t.Fatal("calendar observation changed model/relation storage", record.Name)
				}
				switch record.Name {
				case "invalid_date_hour", "invalid_duration_year":
					requireCalendarReferenceError(t, record, "ValueError", "expression", "")
					if !errors.Is(failure, &query.Error{Code: query.CodeUnsupported}) {
						t.Fatal("invalid calendar signature lost preflight error", failure)
					}
					assertCalendarJSON(t, value, `null`)
					return
				case "invalid_time_year":
					kind, state := "OperationalError", ""
					if vendor == "postgres" {
						kind, state = "NotSupportedError", "0A000"
					}
					requireCalendarReferenceError(t, record, kind, "sql", state)
					if !errors.Is(failure, &query.Error{Code: query.CodeUnsupported}) {
						t.Fatal("invalid clock signature reached execution", failure)
					}
					assertCalendarJSON(t, value, `null`)
					return
				case "invalid_date_storage", "outside_date_storage", "invalid_datetime_storage", "infinite_datetime_storage", "clock_24_storage":
					nativeCalendarError(t, vendor, failure, "22007")
					assertCalendarJSON(t, value, `null`)
					if vendor == "postgres" && record.Name == "infinite_datetime_storage" {
						requireCalendarReferenceError(t, record, "OverflowError", "conversion", "")
						raw := record.Statements[0].Rows[0]
						if len(raw) != 2 || raw[1].Type != "Decimal" || string(raw[1].Value) != `"Infinity"` {
							t.Fatal("native infinity extraction was hidden", raw)
						}
						return
					}
					if record.Error != nil {
						t.Fatal("independent corrupt extraction unexpectedly failed", record.Name, record.Error)
					}
					first := "null"
					if vendor == "postgres" {
						switch record.Name {
						case "invalid_date_storage", "invalid_datetime_storage":
							first = "1"
						case "outside_date_storage":
							first = "-1"
						case "clock_24_storage":
							first = "24"
						}
					}
					suffix := map[string]string{
						"invalid_date_storage":      `,{"code":"b","value":1},{"code":"c","value":12},{"code":"d","value":2},{"code":"e","value":3},{"code":"f","value":2},{"code":"g","value":1},{"code":"h","value":12},{"code":"i","value":null},{"code":"j","value":1},{"code":"k","value":3}]`,
						"outside_date_storage":      `,{"code":"b","value":2000},{"code":"c","value":2019},{"code":"d","value":2000},{"code":"e","value":1900},{"code":"f","value":2400},{"code":"g","value":1},{"code":"h","value":9999},{"code":"i","value":null},{"code":"j","value":2021},{"code":"k","value":2024}]`,
						"invalid_datetime_storage":  `,{"code":"b","value":1},{"code":"c","value":12},{"code":"d","value":2},{"code":"e","value":3},{"code":"f","value":2},{"code":"g","value":1},{"code":"h","value":12},{"code":"i","value":null},{"code":"j","value":1},{"code":"k","value":null}]`,
						"infinite_datetime_storage": `,{"code":"b","value":2021},{"code":"c","value":2019},{"code":"d","value":2000},{"code":"e","value":1900},{"code":"f","value":2400},{"code":"g","value":1},{"code":"h","value":9999},{"code":"i","value":null},{"code":"j","value":2021},{"code":"k","value":null}]`,
						"clock_24_storage":          `,{"code":"b","value":0},{"code":"c","value":12},{"code":"d","value":23},{"code":"e","value":0},{"code":"f","value":0},{"code":"g","value":0},{"code":"h","value":23},{"code":"i","value":null},{"code":"j","value":0},{"code":"k","value":0}]`,
					}[record.Name]
					assertCalendarJSON(t, record.Result, `[{"code":"a","value":`+first+`}`+suffix)
					validateCalendarScalarReplay(t, vendor, record)
					return
				}
				check(t, failure)
				if record.Error != nil {
					t.Fatal("calendar reference failed unexpectedly", record.Name, record.Error)
				}
				assertCalendarJSON(t, value, string(record.Result))
				validateCalendarScalarReplay(t, vendor, record)
			})
		}
	})
}
