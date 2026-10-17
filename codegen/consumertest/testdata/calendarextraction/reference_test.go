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
	"strings"
	"testing"
	"time"

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
	if strings.HasPrefix(name, "trunc_datetime_") {
		return map[string]string{"value": "DateTimeField"}
	}
	if strings.HasPrefix(name, "trunc_date_") {
		return map[string]string{"value": "DateField"}
	}
	if strings.HasPrefix(name, "trunc_time_") {
		return map[string]string{"value": "TimeField"}
	}
	switch name {
	case "invalid_date_hour", "invalid_duration_year", "update_calendar", "update_filtered",
		"invalid_trunc_date_hour", "invalid_trunc_time_year", "invalid_trunc_duration_day", "update_truncated_calendar", "update_truncated_filtered":
		return nil
	case "trunc_clock_from_instant", "trunc_case_time", "trunc_clock_24_storage":
		return map[string]string{"value": "TimeField"}
	case "trunc_filter_month", "trunc_distinct_month", "trunc_case_date", "trunc_invalid_date_storage", "trunc_outside_date_storage", "trunc_unselected_corrupt_date":
		return map[string]string{"value": "DateField"}
	case "trunc_invalid_datetime_storage", "trunc_infinite_datetime_storage":
		return map[string]string{"value": "DateTimeField"}
	case "trunc_group_month", "trunc_group_week":
		return map[string]string{"value": "DateField", "total": "IntegerField", "first": "DateTimeField", "last": "DateTimeField"}
	case "trunc_aggregate":
		return map[string]string{"first": "DateField", "last": "DateTimeField", "count": "IntegerField"}
	case "trunc_filtered_aggregate":
		return map[string]string{"first": "DateField", "last": "TimeField"}
	case "trunc_related":
		return map[string]string{"anchor": "DateTimeField", "schedule_day": "DateField", "detail_at": "DateTimeField", "detail_time": "TimeField"}
	case "trunc_collection":
		return map[string]string{"first": "DateTimeField", "months": "IntegerField", "last": "TimeField"}
	case "trunc_filtered_corrupt_date":
		return map[string]string{"first": "DateField"}
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
		return map[string]string{"anchor": "DateField", "schedule_year": "IntegerField", "schedule_second": "IntegerField", "detail_date": "DateField", "detail_day": "IntegerField"}
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
			case "DateTimeField":
				nativeType = "datetime"
			}
			if kind == "DateField" && strings.HasPrefix(record.Name, "trunc_") {
				// Django's date_trunc SQL returns a native timestamp and its
				// DateField converter takes the date. GoDj explicitly casts the
				// SQL result to Date; compare the observed public domain.
				nativeType = "datetime"
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
		if kind == "DateTimeField" && strings.HasPrefix(record.Name, "trunc_") {
			var text string
			check(t, json.Unmarshal(raw.Value, &text))
			layout := "2006-01-02 15:04:05"
			if vendor == "postgres" {
				layout = "2006-01-02T15:04:05.000000"
			}
			value, err := time.Parse(layout, text)
			if err != nil || value.Format(layout) != text || raw.Aware {
				t.Fatal("truncated native instant lost its exact UTC-wall representation", record.Name, raw, err)
			}
			data, err := json.Marshal(value.UTC().Format("2006-01-02T15:04:05.000000Z"))
			check(t, err)
			if !jsonEqual(data, want) {
				t.Fatal("truncated instant conversion changed its value", record.Name, index)
			}
		} else if kind == "DateField" && nativeType == "datetime" {
			var text string
			check(t, json.Unmarshal(raw.Value, &text))
			layout := "2006-01-02T15:04:05.000000"
			if raw.Aware {
				layout += "-07:00"
			}
			value, err := time.Parse(layout, text)
			_, offset := value.Zone()
			if err != nil || value.Format(layout) != text || offset != 0 || value.Hour() != 0 || value.Minute() != 0 || value.Second() != 0 || value.Nanosecond() != 0 {
				t.Fatal("native truncated Date acquired a clock or non-UTC offset", record.Name, raw, err)
			}
			data, err := json.Marshal(value.Format("2006-01-02"))
			check(t, err)
			if !jsonEqual(data, want) {
				t.Fatal("truncated Date conversion changed its day", record.Name, index)
			}
		} else if nativeType == "Decimal" {
			var text string
			check(t, json.Unmarshal(raw.Value, &text))
			// EXTRACT(SECOND) retains numeric scale even after DATE_TRUNC,
			// so an integral native value can be written as "59.000000".
			// Compare its exact value without rounding a fractional result.
			value, ok := new(big.Rat).SetString(text)
			if !ok || !value.IsInt() || !value.Num().IsInt64() {
				t.Fatal("native calendar Decimal is not an exact int64", record.Name, index, text)
			}
			if !jsonEqual(json.RawMessage(value.Num().String()), want) {
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

// PostgreSQL's outer CASE/aggregate converter doesn't inherit the Trunc converter:
// truncated Date stays an aware midnight datetime, and truncated DateTime stays
// a naive UTC-wall datetime. Keep those observations intact and adapt only the
// declared UTC-profile cases, after checking their independent raw rows.
func calendarTruncationReferenceResult(vendor string, record calendarReferenceCase) (json.RawMessage, error) {
	if vendor != "postgres" {
		return record.Result, nil
	}
	type parameter struct{ kind, value string }
	var columns []string
	var parameters []parameter
	zones, rowCount, rowset := 0, 1, false
	switch record.Name {
	case "trunc_aggregate":
		columns = []string{"first", "last", "count"}
		parameters = []parameter{{"str", "week"}, {"str", "day"}, {"str", "UTC"}, {"str", "hour"}}
		zones = 1
	case "trunc_filtered_aggregate":
		columns = []string{"first", "last"}
		parameters = []parameter{{"str", "month"}, {"str", "minute"}}
	case "trunc_collection":
		columns = []string{"code", "first", "months", "last"}
		parameters = []parameter{{"str", "month"}, {"str", "UTC"}, {"str", "month"}, {"str", "hour"}}
		zones, rowCount, rowset = 1, 4, true
	case "trunc_case_date":
		columns = []string{"code", "value"}
		parameters = []parameter{{"str", "month"}, {"str", "week"}}
		rowCount, rowset = 11, true
	case "trunc_unselected_corrupt_date":
		columns = []string{"code", "value"}
		parameters = []parameter{{"str", "never"}, {"str", "year"}, {"date", "2000-01-01"}}
		rowCount, rowset = 11, true
	case "trunc_filtered_corrupt_date":
		columns = []string{"first"}
		parameters = []parameter{{"str", "month"}, {"str", "a"}}
	default:
		return record.Result, nil
	}
	if record.Error != nil || len(record.Statements) != 1 || !maps.Equal(record.Fields, calendarReferenceFields(record.Name)) {
		return nil, errors.New("wrapped Trunc lacks its successful field/SQL contract")
	}
	statement := record.Statements[0]
	if statement.Error != nil || statement.NativeError != nil || len(statement.Parameters) != len(parameters) || strings.Count(statement.SQL, "AT TIME ZONE %s") != zones {
		return nil, errors.New("wrapped Trunc changed its explicit UTC query")
	}
	for index, want := range parameters {
		data, _ := json.Marshal(want.value)
		if statement.Parameters[index].Type != want.kind || statement.Parameters[index].Aware || !jsonEqual(statement.Parameters[index].Value, data) {
			return nil, errors.New("wrapped Trunc parameter differs")
		}
	}
	var rows []map[string]json.RawMessage
	if rowset {
		if err := json.Unmarshal(record.Result, &rows); err != nil {
			return nil, err
		}
	} else {
		var row map[string]json.RawMessage
		if err := json.Unmarshal(record.Result, &row); err != nil {
			return nil, err
		}
		rows = []map[string]json.RawMessage{row}
	}
	if len(rows) != rowCount || len(statement.Rows) != rowCount || statement.Rowcount != int64(rowCount) {
		return nil, errors.New("wrapped Trunc native row count differs")
	}
	for index, row := range rows {
		if len(row) != len(columns) || len(statement.Rows[index]) != len(columns) {
			return nil, errors.New("truncated aggregate row width differs")
		}
		for column, name := range columns {
			observed, exists := row[name]
			if !exists {
				return nil, errors.New("truncated aggregate omitted an output")
			}
			raw := statement.Rows[index][column]
			if string(observed) == "null" {
				if raw.Type != "NoneType" || string(raw.Value) != "null" || raw.Aware {
					return nil, errors.New("truncated aggregate NULL differs from native value")
				}
				continue
			}
			kind := record.Fields[name]
			if name == "code" || kind == "IntegerField" {
				wantType := "int"
				if name == "code" {
					wantType = "str"
				}
				if raw.Type != wantType || raw.Aware || !jsonEqual(raw.Value, observed) {
					return nil, errors.New("truncated aggregate changed a code or count")
				}
				continue
			}
			var nativeText, converted string
			if json.Unmarshal(raw.Value, &nativeText) != nil || json.Unmarshal(observed, &converted) != nil {
				return nil, errors.New("truncated aggregate temporal value is not a string")
			}
			if kind == "TimeField" {
				value, err := time.Parse("15:04:05.000000", nativeText)
				if raw.Type != "time" || raw.Aware || err != nil || value.Format("15:04:05.000000") != nativeText || strings.TrimSuffix(nativeText, ".000000") != converted {
					return nil, errors.New("truncated aggregate clock lost its native domain")
				}
				continue
			}
			layout := "2006-01-02T15:04:05.000000"
			wantAware := kind == "DateField"
			if wantAware {
				layout += "-07:00"
			} else if kind != "DateTimeField" {
				return nil, fmt.Errorf("unknown truncated aggregate field %q", kind)
			}
			value, err := time.Parse(layout, nativeText)
			_, offset := value.Zone()
			if raw.Type != "datetime" || raw.Aware != wantAware || err != nil || value.Year() < 1 || value.Year() > 9999 || value.Format(layout) != nativeText || offset != 0 {
				return nil, errors.New("truncated aggregate datetime lost its exact native representation")
			}
			normalized := value.Format("2006-01-02T15:04:05.000000Z")
			wantConverted := nativeText
			if wantAware {
				wantConverted = normalized
				if value.Hour() != 0 || value.Minute() != 0 || value.Second() != 0 || value.Nanosecond() != 0 {
					return nil, errors.New("truncated Date aggregate contains a clock")
				}
				normalized = value.Format("2006-01-02")
			}
			if converted != wantConverted {
				return nil, errors.New("truncated aggregate conversion differs from native replay")
			}
			row[name], err = json.Marshal(normalized)
			if err != nil {
				return nil, err
			}
		}
	}
	if rowset {
		return json.Marshal(rows)
	}
	return json.Marshal(rows[0])
}

func checkCalendarTruncationReferenceGuards(t *testing.T, vendor string, record calendarReferenceCase) {
	t.Helper()
	if vendor != "postgres" {
		return
	}
	if slices.Contains([]string{"trunc_case_date", "trunc_unselected_corrupt_date", "trunc_filtered_corrupt_date"}, record.Name) {
		checkCalendarWrappedDateReferenceGuards(t, record)
		return
	}
	if record.Name != "trunc_aggregate" {
		return
	}
	original, err := json.Marshal(record)
	check(t, err)
	for name, corrupt := range map[string]func(*calendarReferenceCase){
		"missing native row":     func(record *calendarReferenceCase) { record.Statements[0].Rows = nil },
		"native kind":            func(record *calendarReferenceCase) { record.Statements[0].Rows[0][1].Type = "str" },
		"invented native offset": func(record *calendarReferenceCase) { record.Statements[0].Rows[0][1].Aware = true },
		"different native value": func(record *calendarReferenceCase) {
			record.Statements[0].Rows[0][1].Value = json.RawMessage(`"9999-12-31T00:00:01.000000"`)
		},
		"different query timezone": func(record *calendarReferenceCase) {
			record.Statements[0].Parameters[2].Value = json.RawMessage(`"Asia/Seoul"`)
		},
		"wrong output kind": func(record *calendarReferenceCase) { record.Fields["first"] = "DateTimeField" },
		"invented converted offset": func(record *calendarReferenceCase) {
			record.Result = json.RawMessage(`{"first":"0001-01-01T00:00:00.000000Z","last":"9999-12-31T00:00:00.000000Z","count":10}`)
		},
		"lost NULL identity": func(record *calendarReferenceCase) {
			record.Result = json.RawMessage(`{"first":"0001-01-01T00:00:00.000000Z","last":null,"count":10}`)
		},
		"Date contains a clock": func(record *calendarReferenceCase) {
			record.Statements[0].Rows[0][0].Value = json.RawMessage(`"0001-01-01T12:00:00.000000+00:00"`)
			record.Result = json.RawMessage(`{"first":"0001-01-01T12:00:00.000000Z","last":"9999-12-31T00:00:00.000000","count":10}`)
		},
	} {
		var changed calendarReferenceCase
		check(t, json.Unmarshal(original, &changed))
		corrupt(&changed)
		if _, err := calendarTruncationReferenceResult(vendor, changed); err == nil {
			t.Fatal("truncated aggregate comparison accepted corrupted evidence:", name)
		}
	}
	t.Log("calendar truncation comparison guards rejected 9 altered records")
}

func checkCalendarWrappedDateReferenceGuards(t *testing.T, record calendarReferenceCase) {
	t.Helper()
	column, name, rowset := 1, "value", true
	if record.Name == "trunc_filtered_corrupt_date" {
		column, name, rowset = 0, "first", false
	}
	setResult := func(record *calendarReferenceCase, value json.RawMessage) {
		if rowset {
			var rows []map[string]json.RawMessage
			check(t, json.Unmarshal(record.Result, &rows))
			rows[0][name] = value
			data, err := json.Marshal(rows)
			check(t, err)
			record.Result = data
		} else {
			var row map[string]json.RawMessage
			check(t, json.Unmarshal(record.Result, &row))
			row[name] = value
			data, err := json.Marshal(row)
			check(t, err)
			record.Result = data
		}
	}
	mutations := map[string]func(*calendarReferenceCase){
		"missing native row": func(record *calendarReferenceCase) { record.Statements[0].Rows = nil },
		"wrong rowcount":     func(record *calendarReferenceCase) { record.Statements[0].Rowcount++ },
		"native kind":        func(record *calendarReferenceCase) { record.Statements[0].Rows[0][column].Type = "date" },
		"lost native offset": func(record *calendarReferenceCase) { record.Statements[0].Rows[0][column].Aware = false },
		"non-UTC native offset": func(record *calendarReferenceCase) {
			raw := &record.Statements[0].Rows[0][column]
			raw.Value = json.RawMessage(strings.Replace(string(raw.Value), "+00:00", "+09:00", 1))
		},
		"parameter kind": func(record *calendarReferenceCase) { record.Statements[0].Parameters[0].Type = "date" },
		"parameter value": func(record *calendarReferenceCase) {
			record.Statements[0].Parameters[0].Value = json.RawMessage(`"different"`)
		},
		"wrong output kind": func(record *calendarReferenceCase) { record.Fields[name] = "DateTimeField" },
		"changed converted value": func(record *calendarReferenceCase) {
			setResult(record, json.RawMessage(`"1988-02-29T00:00:00.000000Z"`))
		},
		"lost NULL identity": func(record *calendarReferenceCase) { setResult(record, json.RawMessage(`null`)) },
		"Date contains a clock": func(record *calendarReferenceCase) {
			record.Statements[0].Rows[0][column].Value = json.RawMessage(`"2000-01-01T12:00:00.000000+00:00"`)
			setResult(record, json.RawMessage(`"2000-01-01T12:00:00.000000Z"`))
		},
	}
	original, err := json.Marshal(record)
	check(t, err)
	for name, corrupt := range mutations {
		var changed calendarReferenceCase
		check(t, json.Unmarshal(original, &changed))
		corrupt(&changed)
		if _, err := calendarTruncationReferenceResult("postgres", changed); err == nil {
			t.Fatal("wrapped Date comparison accepted corrupted evidence:", name)
		}
	}
	t.Logf("calendar truncation comparison guards rejected %d altered records", len(mutations))
}

func verifyCalendarTruncationDifference(t *testing.T, vendor string, record calendarReferenceCase, value any, failure error) bool {
	t.Helper()
	if strings.HasPrefix(record.Name, "invalid_trunc_") {
		kind := "ValueError"
		if record.Name == "invalid_trunc_duration_day" {
			kind = "TypeError"
		}
		requireCalendarReferenceError(t, record, kind, "expression", "")
		if !errors.Is(failure, &query.Error{Code: query.CodeUnsupported}) {
			t.Fatal("invalid truncation signature reached I/O or changed its preflight category", failure)
		}
		assertCalendarJSON(t, value, `null`)
		return true
	}
	if !slices.Contains([]string{"trunc_invalid_date_storage", "trunc_outside_date_storage", "trunc_invalid_datetime_storage", "trunc_infinite_datetime_storage", "trunc_clock_24_storage"}, record.Name) {
		return false
	}
	nativeCalendarError(t, vendor, failure, "22007")
	assertCalendarJSON(t, value, `null`)
	if vendor == "postgres" && record.Name != "trunc_clock_24_storage" {
		// These timestamp results exist in PostgreSQL but not in Python's
		// datetime range. Record the failed driver conversion and independent
		// native fetch rather than turning them into SQL NULL or an empty set.
		if record.Error == nil || record.Error.Type != "DataError" || record.Error.Stage != "conversion" || record.Error.SQLState != "" || record.Error.Message == "" || len(record.Result) != 0 || len(record.Statements) != 1 {
			t.Fatal("native truncation range error changed stage", record.Name, record.Error)
		}
		statement := record.Statements[0]
		if statement.Error != nil || statement.NativeError == nil || statement.NativeError.Type != "DataError" || statement.NativeError.Stage != "native_fetch" || statement.NativeError.SQLState != "" || statement.NativeError.Message == "" || statement.Rows != nil {
			t.Fatal("truncation range error lost its independent native fetch", record.Name, statement)
		}
		return true
	}
	if record.Error != nil {
		t.Fatal("corrupt truncation reference unexpectedly failed", record.Name, record.Error)
	}
	first := "null"
	if vendor == "postgres" {
		first = `"00:00:00"`
	}
	var suffix string
	switch record.Name {
	case "trunc_invalid_date_storage", "trunc_outside_date_storage":
		suffix = `,{"code":"b","value":"2000-01-01"},{"code":"c","value":"2019-12-30"},{"code":"d","value":"2000-02-29"},{"code":"e","value":"1900-03-01"},{"code":"f","value":"2400-02-29"},{"code":"g","value":"0001-01-01"},{"code":"h","value":"9999-12-31"},{"code":"i","value":null},{"code":"j","value":"2021-01-01"},{"code":"k","value":"2024-03-03"}]`
	case "trunc_invalid_datetime_storage", "trunc_infinite_datetime_storage":
		suffix = `,{"code":"b","value":"2021-01-01T00:00:00.000000Z"},{"code":"c","value":"2019-01-01T00:00:00.000000Z"},{"code":"d","value":"2000-01-01T00:00:00.000000Z"},{"code":"e","value":"1900-01-01T00:00:00.000000Z"},{"code":"f","value":"2400-01-01T00:00:00.000000Z"},{"code":"g","value":"0001-01-01T00:00:00.000000Z"},{"code":"h","value":"9999-01-01T00:00:00.000000Z"},{"code":"i","value":null},{"code":"j","value":"2021-01-01T00:00:00.000000Z"},{"code":"k","value":null}]`
	case "trunc_clock_24_storage":
		suffix = `,{"code":"b","value":"00:00:00"},{"code":"c","value":"12:00:00"},{"code":"d","value":"23:00:00"},{"code":"e","value":"00:00:00"},{"code":"f","value":"00:00:00"},{"code":"g","value":"00:00:00"},{"code":"h","value":"23:00:00"},{"code":"i","value":null},{"code":"j","value":"00:00:00"},{"code":"k","value":"00:00:00"}]`
	}
	assertCalendarJSON(t, record.Result, `[{"code":"a","value":`+first+`}`+suffix)
	validateCalendarScalarReplay(t, vendor, record)
	return true
}

func TestCalendarReference(t *testing.T) {
	withCalendarBackends(t, func(t *testing.T, backend calendarBackend, vendor string, physical calendarPhysical) {
		data, err := os.ReadFile("django61-" + vendor + ".json")
		check(t, err)
		var reference struct{ Cases []calendarReferenceCase }
		check(t, json.Unmarshal(data, &reference))
		if len(reference.Cases) != 95 {
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
				if len(record.Statements) == 0 && !slices.Contains([]string{"empty_aggregate", "empty_group", "invalid_date_hour", "invalid_duration_year", "invalid_trunc_date_hour", "invalid_trunc_time_year", "invalid_trunc_duration_day"}, record.Name) {
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
				if slices.Contains([]string{"invalid_date_storage", "outside_date_storage", "invalid_datetime_storage", "infinite_datetime_storage", "clock_24_storage", "unselected_corrupt_date", "filtered_corrupt_date"}, strings.TrimPrefix(record.Name, "trunc_")) {
					withCalendarCorruption(t, physical, seed, vendor, record.Name, run)
				} else {
					run()
				}
				if physical.Snapshot() != before {
					t.Fatal("calendar observation changed model/relation storage", record.Name)
				}
				if verifyCalendarTruncationDifference(t, vendor, record, value, failure) {
					return
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
				expected, err := calendarTruncationReferenceResult(vendor, record)
				check(t, err)
				assertCalendarJSON(t, value, string(expected))
				checkCalendarTruncationReferenceGuards(t, vendor, record)
				validateCalendarScalarReplay(t, vendor, record)
			})
		}
	})
}
