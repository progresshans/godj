package consumer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"reflect"
	"strings"
	"testing"
)

type calendarArithmeticExpected struct {
	Name             string
	Fields           map[string]string
	GodjError        bool `json:"godj_error"`
	Godj             json.RawMessage
	SQLite, Postgres calendarArithmeticBackendExpected
}
type calendarArithmeticBackendExpected struct {
	Result json.RawMessage
	Rows   [][]calendarNativeValue `json:"native_rows"`
	Error  *struct {
		Type, Stage, SQLState string
		NativeStage           string `json:"native_stage"`
	}
}

func calendarArithmeticFields(name string) map[string]string {
	switch name {
	case "arith_date_add", "arith_date_reverse", "arith_date_subtract", "arith_composed_start", "arith_filter_start", "arith_related_start", "arith_date_fraction", "arith_invalid_date_storage", "arith_unselected_corrupt_date":
		return map[string]string{"value": "DateTimeField"}
	case "arith_time_add", "arith_time_reverse", "arith_time_subtract", "arith_case_time", "arith_time_fraction", "arith_clock_24_storage":
		return map[string]string{"value": "TimeField"}
	case "arith_date_difference", "arith_date_instant_difference", "arith_instant_date_difference", "arith_time_difference":
		return map[string]string{"value": "DurationField"}
	case "arith_collection_start":
		return map[string]string{"first": "DateTimeField", "last": "DateTimeField", "count": "IntegerField"}
	case "arith_aggregate_difference":
		return map[string]string{"first": "DurationField", "last": "DurationField", "total": "DurationField"}
	case "arith_filtered_corrupt_date":
		return map[string]string{"first": "DateTimeField"}
	case "update_arithmetic_calendar":
		return nil
	default:
		return map[string]string{"unknown_arithmetic_case": name}
	}
}

func readCalendarArithmeticExpectations(t *testing.T) map[string]calendarArithmeticExpected {
	t.Helper()
	data, err := os.ReadFile("arithmetic-expectations.json")
	check(t, err)
	var document struct {
		Kind  string
		Input string `json:"input_sha256"`
		Cases []calendarArithmeticExpected
	}
	check(t, json.Unmarshal(data, &document))
	input, err := os.ReadFile("input.json")
	check(t, err)
	digest := sha256.Sum256(input)
	if document.Kind != "calendar-arithmetic-expectations-v1" || document.Input != hex.EncodeToString(digest[:]) || len(document.Cases) != 23 {
		t.Fatal("calendar arithmetic expectations are not bound to these synthetic inputs")
	}
	values := make(map[string]calendarArithmeticExpected, len(document.Cases))
	for _, value := range document.Cases {
		if _, duplicate := values[value.Name]; duplicate || !maps.Equal(value.Fields, calendarArithmeticFields(value.Name)) {
			t.Fatal("arithmetic expectations duplicate or change a result kind", value.Name)
		}
		values[value.Name] = value
	}
	return values
}

// The assertions describe exact expected differences, not a waiver by type or
// prefix. Both the converted result and the independent native replay must match
// the entire literal record. This function returns an error so mutated evidence
// can test the comparison boundary without invoking a second product execution.
func validateCalendarArithmeticReference(vendor string, record calendarReferenceCase, expected calendarArithmeticExpected) error {
	if record.Name != expected.Name || !maps.Equal(record.Fields, expected.Fields) || !record.Unchanged {
		return errors.New("calendar arithmetic identity, field domain or storage preservation differs")
	}
	want := expected.SQLite
	if vendor == "postgres" {
		want = expected.Postgres
	} else if vendor != "sqlite" {
		return errors.New("unknown calendar arithmetic backend")
	}
	if want.Error != nil {
		if record.Error == nil || record.Error.Type != want.Error.Type || record.Error.Stage != want.Error.Stage || record.Error.SQLState != want.Error.SQLState || record.Error.Message == "" || len(record.Result) != 0 || len(record.Statements) != 1 {
			return errors.New("calendar arithmetic reference failure changed type or stage")
		}
		statement := record.Statements[0]
		if statement.SQL == "" || statement.Error != nil || statement.NativeError == nil || statement.NativeError.Type != want.Error.Type || statement.NativeError.Stage != want.Error.NativeStage || statement.NativeError.SQLState != want.Error.SQLState || statement.NativeError.Message == "" || statement.Rows != nil {
			return errors.New("calendar arithmetic failure lost its independent native fetch")
		}
		return nil
	}
	if record.Error != nil || !jsonEqual(record.Result, want.Result) {
		return fmt.Errorf("calendar arithmetic converted result differs: %s", record.Name)
	}
	update := record.Name == "update_arithmetic_calendar"
	count := 1
	if update {
		count = 2
	}
	if len(record.Statements) != count {
		return errors.New("calendar arithmetic statement count differs")
	}
	for index, statement := range record.Statements {
		verb := "SELECT "
		if update && index == 0 {
			verb = "UPDATE "
		}
		if !strings.HasPrefix(statement.SQL, verb) || statement.Error != nil || statement.NativeError != nil {
			return errors.New("calendar arithmetic lost its successful native statement")
		}
		rowcount := int64(-1)
		if update && index == 0 || vendor == "postgres" {
			rowcount = int64(len(want.Rows))
			if update {
				rowcount = 11
			}
		}
		if statement.Rowcount != rowcount {
			return errors.New("calendar arithmetic native rowcount differs")
		}
		if update {
			if statement.Rows != nil {
				return errors.New("write replay was unexpectedly fabricated")
			}
		} else if !reflect.DeepEqual(statement.Rows, want.Rows) {
			return fmt.Errorf("calendar arithmetic native values, types, offsets or NULL identity differ: %s", record.Name)
		}
	}
	if update && reflect.DeepEqual(record.NativeBefore, record.NativeAfter) {
		return errors.New("calendar arithmetic update has no stored-row effect")
	}
	return nil
}

func verifyCalendarArithmeticReference(t *testing.T, vendor string, record calendarReferenceCase, value any, failure error, expectations map[string]calendarArithmeticExpected) bool {
	t.Helper()
	if !strings.HasPrefix(record.Name, "arith_") && record.Name != "update_arithmetic_calendar" {
		return false
	}
	expected, found := expectations[record.Name]
	if !found {
		t.Fatal("arithmetic case has no closed comparison contract", record.Name)
	}
	check(t, validateCalendarArithmeticReference(vendor, record, expected))
	if expected.GodjError {
		nativeCalendarError(t, vendor, failure, "22007")
	} else {
		check(t, failure)
	}
	assertCalendarJSON(t, value, string(expected.Godj))
	if record.Name == "arith_date_add" {
		original, err := json.Marshal(record)
		check(t, err)
		for name, change := range map[string]func(*calendarReferenceCase){
			"missing replay":        func(r *calendarReferenceCase) { r.Statements[0].Rows = nil },
			"changed native kind":   func(r *calendarReferenceCase) { r.Statements[0].Rows[0][1].Type = "date" },
			"invented offset":       func(r *calendarReferenceCase) { r.Statements[0].Rows[0][1].Aware = true },
			"changed native value":  func(r *calendarReferenceCase) { r.Statements[0].Rows[0][1].Value = json.RawMessage(`"2000-01-01"`) },
			"lost NULL":             func(r *calendarReferenceCase) { r.Statements[0].Rows[8][1] = r.Statements[0].Rows[0][1] },
			"changed output domain": func(r *calendarReferenceCase) { r.Fields["value"] = "DateField" },
			"changed result":        func(r *calendarReferenceCase) { r.Result = json.RawMessage(`[]`) },
			"missing SQL":           func(r *calendarReferenceCase) { r.Statements[0].SQL = "" },
			"changed rowcount":      func(r *calendarReferenceCase) { r.Statements[0].Rowcount++ },
		} {
			var changed calendarReferenceCase
			check(t, json.Unmarshal(original, &changed))
			change(&changed)
			if err := validateCalendarArithmeticReference(vendor, changed, expected); err == nil {
				t.Fatal("calendar arithmetic accepted altered independent evidence", name)
			}
		}
		t.Log("calendar arithmetic comparison rejected 9 altered native records")
	}
	return true
}
