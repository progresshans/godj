package consumer

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// Closed observations distinguish native execution from Django's conversion.
// Every intended difference fixes both results and the raw carrier/error. A
// changed converter, missing replay, or unexpected backend error must fail.
func durationScalingReference(vendor string, record temporalReferenceCase) (expected string, fails, owned bool, err error) {
	original := `[{"code":"a","value":"00:00:00.000001"},{"code":"b","value":"-1 23:59:59.999999"},{"code":"c","value":null},{"code":"d","value":"00:00:00"},{"code":"e","value":"00:00:00.000001"},{"code":"f","value":"2 03:00:00.000003"}]`
	twice := `[{"code":"a","value":"00:00:00.000002"},{"code":"b","value":"-1 23:59:59.999998"},{"code":"c","value":null},{"code":"d","value":"00:00:00"},{"code":"e","value":"00:00:00.000002"},{"code":"f","value":"4 06:00:00.000006"}]`
	half := `[{"code":"a","value":"00:00:00"},{"code":"b","value":"00:00:00"},{"code":"c","value":null},{"code":"d","value":"00:00:00"},{"code":"e","value":"00:00:00"},{"code":"f","value":"1 01:30:00.000002"}]`
	zero := `[{"code":"a","value":"00:00:00"}]`
	var converted, nativeKind string
	var codes []string
	var numbers []*float64
	number := func(value float64) *float64 { return &value }
	switch record.Name {
	case "scale_integer", "scale_integer_reverse":
		expected, converted, nativeKind = twice, twice, "int"
		codes = []string{"a", "b", "c", "d", "e", "f"}
		numbers = []*float64{number(2), number(-2), nil, number(0), number(2), number(367200000006)}
	case "scale_divide_restore":
		expected, converted, nativeKind = original, original, "float"
		codes = []string{"a", "b", "c", "d", "e", "f"}
		numbers = []*float64{number(1), number(-1), nil, number(0), number(1), number(183600000003)}
		if vendor == "postgres" {
			converted = `[{"code":"a","value":"00:00:00"},{"code":"b","value":"00:00:00"},{"code":"c","value":null},{"code":"d","value":"00:00:00"},{"code":"e","value":"00:00:00"},{"code":"f","value":"2 03:00:00.000003"}]`
			numbers[0], numbers[1], numbers[4] = number(0), number(0), number(0)
		}
	case "scale_half_final":
		expected, converted, nativeKind = half, half, "float"
		codes = []string{"a", "b", "c", "d", "e", "f"}
		numbers = []*float64{number(0.5), number(-0.5), nil, number(0), number(0.5), number(91800000001.5)}
		if vendor == "postgres" {
			numbers[0], numbers[1], numbers[4], numbers[5] = number(0), number(0), number(0), number(91800000002)
		}
	case "scale_float_tie", "scale_decimal_tie":
		expected, converted, nativeKind = zero, zero, "float"
		codes = []string{"a"}
		numbers = []*float64{number(0.5)}
		if record.Name == "scale_float_tie" {
			expected = `[{"code":"a","value":"00:00:00.000001"}]`
		}
		if vendor == "postgres" {
			numbers[0] = number(0)
		}
	case "fraction_sum", "fraction_distinct_sum":
		expected, converted, nativeKind = `{"total":"00:00:00.000001"}`, `{"total":"00:00:00.000001"}`, "float"
		numbers = []*float64{number(1)}
		if record.Name == "fraction_distinct_sum" {
			expected, converted = `{"total":"00:00:00"}`, `{"total":"00:00:00"}`
			numbers[0] = number(0.5)
		}
		if vendor == "postgres" {
			converted = `{"total":"00:00:00"}`
			numbers[0] = number(0)
		}
	case "fraction_shift":
		expected = `[{"code":"a","value":"2024-02-29T00:00:00.000002Z"}]`
		converted = `[{"code":"a","value":null}]`
		if vendor == "postgres" {
			converted = `[{"code":"a","value":"2024-02-29T00:00:00.000001Z"}]`
		}
	case "scale_zero_divisor":
		expected, fails = "null", true
	case "duration_ratio":
		expected = `[{"code":"a","value":0.5},{"code":"b","value":-1.1574074074074074074e-11},{"code":"c","value":null},{"code":"d","value":null},{"code":"e","value":0.5},{"code":"f","value":183600000003}]`
		converted = `[{"code":"a","value":0},{"code":"b","value":0},{"code":"c","value":null},{"code":"d","value":null},{"code":"e","value":0},{"code":"f","value":183600000003}]`
		nativeKind = "int"
		codes = []string{"a", "b", "c", "d", "e", "f"}
		numbers = []*float64{number(0), number(0), nil, nil, number(0), number(183600000003)}
	default:
		return "", false, false, nil
	}
	owned = true
	reject := func(detail string) (string, bool, bool, error) {
		return "", false, true, fmt.Errorf("duration reference %s/%s: %s", vendor, record.Name, detail)
	}
	if vendor != "sqlite" && vendor != "postgres" {
		return reject("unknown backend")
	}
	if !record.Unchanged || !maps.Equal(record.Fields, temporalReferenceFields(record.Name)) || len(record.Statements) != 1 || strings.TrimSpace(record.Statements[0].SQL) == "" {
		return reject("missing source, fields or native statement")
	}
	statement := record.Statements[0]
	if fails || record.Name == "duration_ratio" && vendor == "postgres" {
		kind, state := "OperationalError", ""
		if vendor == "postgres" {
			kind, state = "DataError", "22012"
			if record.Name == "duration_ratio" {
				kind, state = "ProgrammingError", "42883"
			}
		}
		if record.Error == nil || record.Error.Type != kind || record.Error.Stage != "sql" || record.Error.SQLState != state || record.Error.Message == "" || len(record.Result) != 0 || statement.Rows != nil {
			return reject("native failure or its conversion stage changed")
		}
		for index, failure := range []*temporalReferenceFailure{statement.Error, statement.NativeError} {
			stage := "sql"
			if index == 1 {
				stage = "native_execute"
			}
			if failure == nil || failure.Type != kind || failure.SQLState != state || failure.Stage != stage || failure.Message == "" {
				return reject("missing original or replayed SQL failure")
			}
		}
		return expected, fails, true, nil
	}
	if record.Error != nil || statement.Error != nil || statement.NativeError != nil || len(record.NativeAfter) != 3 {
		return reject("unexpected error or incomplete native storage snapshot")
	}
	var actual, want any
	if json.Unmarshal(record.Result, &actual) != nil || json.Unmarshal([]byte(converted), &want) != nil || !reflect.DeepEqual(actual, want) {
		return reject("Django converted result changed")
	}
	if record.Name == "scale_decimal_tie" {
		found := 0
		for _, parameter := range statement.Parameters {
			if parameter.Type == "Decimal" {
				var text string
				if json.Unmarshal(parameter.Value, &text) != nil || text != "0.1" {
					return reject("Decimal factor lost its exact input")
				}
				found++
			}
		}
		if found != 1 {
			return reject("Decimal parameter missing or duplicated")
		}
	}
	if record.Name == "fraction_shift" {
		if len(statement.Rows) != 1 || len(statement.Rows[0]) != 2 || !durationNativeCode(statement.Rows[0][0], "a") {
			return reject("native instant row identity changed")
		}
		value := statement.Rows[0][1]
		if vendor == "sqlite" {
			if value.Type != "NoneType" || string(value.Value) != "null" {
				return reject("native fractional shift no longer returns NULL")
			}
		} else {
			var text string
			if value.Type != "datetime" || !value.Aware || json.Unmarshal(value.Value, &text) != nil || text != "2024-02-29T00:00:00.000001+00:00" {
				return reject("native interval rounding changed its aware instant")
			}
		}
		return expected, false, true, nil
	}
	if len(statement.Rows) != len(numbers) {
		return reject("native row count changed")
	}
	if vendor == "postgres" {
		nativeKind = "timedelta"
	}
	for index, expectedNumber := range numbers {
		columns, valueIndex := 1, 0
		if codes != nil {
			columns, valueIndex = 2, 1
		}
		row := statement.Rows[index]
		if len(row) != columns || codes != nil && !durationNativeCode(row[0], codes[index]) {
			return reject("native columns, identity or ordering changed")
		}
		if !durationNativeNumber(row[valueIndex], nativeKind, expectedNumber) {
			return reject("native type or exact numeric value changed")
		}
	}
	return expected, false, true, nil
}

func durationNativeCode(value temporalNativeValue, expected string) bool {
	var actual string
	return value.Type == "str" && json.Unmarshal(value.Value, &actual) == nil && actual == expected
}

func durationNativeNumber(value temporalNativeValue, kind string, expected *float64) bool {
	if expected == nil {
		return value.Type == "NoneType" && string(value.Value) == "null"
	}
	if value.Type != kind {
		return false
	}
	switch kind {
	case "int":
		var number int64
		return json.Unmarshal(value.Value, &number) == nil && float64(number) == *expected
	case "float":
		var text string
		if json.Unmarshal(value.Value, &text) != nil {
			return false
		}
		number, err := strconv.ParseFloat(text, 64)
		bits, bitsErr := strconv.ParseFloat(value.Binary64, 64)
		return err == nil && bitsErr == nil && math.Float64bits(number) == math.Float64bits(*expected) && math.Float64bits(bits) == math.Float64bits(number)
	case "timedelta":
		return value.Days >= -100 && value.Days <= 100 && value.Seconds >= 0 && value.Seconds < 86400 && value.Microseconds >= 0 && value.Microseconds < 1000000 && float64(value.Days*86400000000+value.Seconds*1000000+value.Microseconds) == *expected
	default:
		return false
	}
}

func testDurationScalingReferenceIntegrity(t *testing.T) {
	valid := temporalReferenceCase{
		Name: "scale_float_tie", Unchanged: true,
		Fields:      map[string]string{"value": "DurationField"},
		Result:      json.RawMessage(`[{"code":"a","value":"00:00:00"}]`),
		NativeAfter: make([][][]temporalNativeValue, 3),
		Statements: []temporalReferenceStatement{{
			SQL: "SELECT code, django_format_dtdelta('*', span, 0.1) FROM events",
			Rows: [][]temporalNativeValue{{
				{Type: "str", Value: json.RawMessage(`"a"`)},
				{Type: "float", Value: json.RawMessage(`"0.5"`), Binary64: "0x1.0000000000000p-1"},
			}},
		}},
	}
	if _, _, owned, err := durationScalingReference("sqlite", valid); err != nil || !owned {
		t.Fatal("valid independent fixture rejected", err)
	}
	mutations := []func(*temporalReferenceCase){
		func(v *temporalReferenceCase) { v.Unchanged = false },
		func(v *temporalReferenceCase) { v.Fields["value"] = "FloatField" },
		func(v *temporalReferenceCase) { v.Result = json.RawMessage(`[{"code":"a","value":"00:00:00.000001"}]`) },
		func(v *temporalReferenceCase) { v.NativeAfter = nil },
		func(v *temporalReferenceCase) { v.Statements[0].SQL = "" },
		func(v *temporalReferenceCase) { v.Statements[0].Rows = nil },
		func(v *temporalReferenceCase) {
			v.Statements[0].Rows = append(v.Statements[0].Rows, v.Statements[0].Rows[0])
		},
		func(v *temporalReferenceCase) { v.Statements[0].Rows[0][0].Value = json.RawMessage(`"b"`) },
		func(v *temporalReferenceCase) { v.Statements[0].Rows[0][1].Type = "int" },
		func(v *temporalReferenceCase) { v.Statements[0].Rows[0][1].Value = json.RawMessage(`"1"`) },
		func(v *temporalReferenceCase) { v.Statements[0].Rows[0][1].Binary64 = "0x1.0000000000000p+0" },
		func(v *temporalReferenceCase) { v.Error = &temporalReferenceFailure{Type: "OperationalError"} },
		func(v *temporalReferenceCase) {
			v.Statements[0].NativeError = &temporalReferenceFailure{Type: "OperationalError"}
		},
	}
	for index, mutate := range mutations {
		data, err := json.Marshal(valid)
		check(t, err)
		var candidate temporalReferenceCase
		check(t, json.Unmarshal(data, &candidate))
		mutate(&candidate)
		if _, _, _, err := durationScalingReference("sqlite", candidate); err == nil {
			t.Fatal("altered reference accepted", index)
		}
	}
	t.Logf("duration reference rejected %d altered native/conversion records", len(mutations))
}
