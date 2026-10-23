package consumer

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/duration"
)

type temporalReferenceFailure struct {
	Type, Message, Stage, SQLState string
}

type temporalNativeValue struct {
	Type, Binary64              string
	Value                       json.RawMessage
	Days, Seconds, Microseconds int64
	Aware                       bool
}

type temporalReferenceStatement struct {
	SQL         string
	Parameters  []temporalNativeValue
	Rowcount    int64
	Rows        [][]temporalNativeValue `json:"native_rows"`
	Error       *temporalReferenceFailure
	NativeError *temporalReferenceFailure `json:"native_error"`
}

type temporalReferenceCase struct {
	Name        string
	Result      json.RawMessage
	Error       *temporalReferenceFailure
	Statements  []temporalReferenceStatement
	Fields      map[string]string         `json:"output_fields"`
	NativeAfter [][][]temporalNativeValue `json:"native_after"`
	Unchanged   bool                      `json:"storage_unchanged"`
}

func temporalReferenceFields(name string) map[string]string {
	switch name {
	case "computed_aggregate", "empty_aggregate", "all_null_aggregate":
		return map[string]string{"total": "DurationField", "average": "DurationField", "minimum": "DurationField", "maximum": "DurationField", "present": "IntegerField"}
	case "distinct_filtered_span", "groups_having", "mean_reaggregate":
		return map[string]string{"total": "DurationField", "average": "DurationField"}
	case "derived_duration":
		return map[string]string{"total": "DurationField", "maximum": "DurationField"}
	case "fraction_sum", "fraction_distinct_sum":
		return map[string]string{"total": "DurationField"}
	case "duration_ratio":
		return map[string]string{"value": "FloatField"}
	case "scale_integer", "scale_integer_reverse", "scale_divide_restore", "scale_half_final", "scale_float_tie", "scale_decimal_tie", "scale_zero_divisor":
		return map[string]string{"value": "DurationField"}
	case "collection_duration":
		return map[string]string{"total": "DurationField", "present": "IntegerField"}
	case "mean_fractional_shift", "mean_integral_shift", "mean_zero_shift", "mean_null_shift", "mean_reverse_shift":
		return map[string]string{"period": "DurationField", "value": "DateTimeField"}
	case "datetime_difference", "duration_add", "duration_subtract", "duration_negate", "null_difference", "large_datetime_difference", "negative_datetime_difference", "duration_int64_overflow", "duration_full_model_literal", "duration_model_overflow", "mean_add_duration", "mean_negate_duration":
		return map[string]string{"value": "DurationField"}
	default:
		if strings.HasPrefix(name, "update_") {
			return nil
		}
		return map[string]string{"value": "DateTimeField"}
	}
}

func requireTemporalReferenceError(t *testing.T, record temporalReferenceCase, stage string, types ...string) {
	t.Helper()
	if record.Error == nil || !slices.Contains(types, record.Error.Type) || record.Error.Stage != stage || record.Error.Message == "" || record.Error.SQLState != "" || len(record.Result) != 0 {
		t.Fatal("independent temporal failure differs", record.Name, record.Error, string(record.Result))
	}
	if stage == "sql" {
		found := false
		for _, statement := range record.Statements {
			if statement.Error != nil {
				found = true
				if statement.Error.Type != record.Error.Type || statement.Error.Stage != "sql" || statement.Error.Message != record.Error.Message {
					t.Fatal("temporal SQL failure lost its captured cause", record.Name)
				}
			}
		}
		if !found {
			t.Fatal("independent temporal SQL failure has no failing statement", record.Name)
		}
	} else {
		for _, statement := range record.Statements {
			if statement.Error != nil {
				t.Fatal("conversion failure misclassified SQL execution", record.Name)
			}
		}
	}
	if !strings.HasPrefix(record.Name, "update_") {
		if len(record.Statements) != 1 || record.Statements[0].NativeError == nil {
			t.Fatal("range failure lacks independent raw replay failure", record.Name)
		}
		raw := record.Statements[0].NativeError
		wantStage := "native_fetch"
		if stage == "sql" {
			wantStage = "native_execute"
		}
		if !slices.Contains(types, raw.Type) || raw.Stage != wantStage || raw.SQLState != "" || raw.Message == "" {
			t.Fatal("temporal raw failure stage differs", record.Name, raw)
		}
	}
}

func temporalNativeFloat(t *testing.T, record temporalReferenceCase, column int, exact string) {
	t.Helper()
	if len(record.Statements) != 1 || len(record.Statements[0].Rows) != 1 || len(record.Statements[0].Rows[0]) <= column {
		t.Fatal("temporal native scalar row missing", record.Name)
	}
	value := record.Statements[0].Rows[0][column]
	if value.Type != "float" || value.Binary64 != exact {
		t.Fatal("temporal native fractional value differs", record.Name, value.Type, value.Binary64)
	}
}

// Observations are emitted by the independent pinned runtime in this exact CI
// owner. Successful parity compares converted values. Each intended difference
// also checks the native type, SQL/error stage, and both implementations' result.
func TestTemporalReference(t *testing.T) {
	t.Run("scaling_reference_integrity", testDurationScalingReferenceIntegrity)
	withTemporalBackends(t, func(t *testing.T, backend temporalBackend, vendor string, physical temporalPhysical) {
		data, err := os.ReadFile("django61-" + vendor + ".json")
		check(t, err)
		var reference struct{ Cases []temporalReferenceCase }
		check(t, json.Unmarshal(data, &reference))
		if len(reference.Cases) != 55 {
			t.Fatal("independent temporal inventory incomplete")
		}
		seedTemporal(t, backend)
		before := physical.Snapshot()
		for _, record := range reference.Cases {
			t.Run(record.Name, func(t *testing.T) {
				if !record.Unchanged {
					t.Fatal("independent temporal case altered storage", record.Name)
				}
				if !maps.Equal(record.Fields, temporalReferenceFields(record.Name)) {
					t.Fatal("independent temporal resolved output kinds differ", record.Name, record.Fields)
				}
				if len(record.Statements) == 0 && record.Name != "empty_aggregate" {
					t.Fatal("temporal case has no native SQL evidence", record.Name)
				}
				if record.Error == nil && len(record.NativeAfter) != 3 {
					t.Fatal("successful temporal case lacks all native post-operation tables", record.Name)
				}
				for _, statement := range record.Statements {
					if strings.TrimSpace(statement.SQL) == "" {
						t.Fatal("empty captured temporal SQL")
					}
					for _, parameter := range statement.Parameters {
						if parameter.Type == "Decimal" {
							var value string
							if record.Name != "scale_decimal_tie" || json.Unmarshal(parameter.Value, &value) != nil || value != "0.1" {
								t.Fatal("unexpected native Decimal factor", record.Name)
							}
							continue
						}
						// Pinned psycopg preserves the Int4 wrapper for Django's
						// negation multiplier. Keep its native identity and range.
						if parameter.Type == "Int4" {
							var value *int32
							if vendor != "postgres" || json.Unmarshal(parameter.Value, &value) != nil || value == nil {
								t.Fatal("invalid native PostgreSQL Int4 parameter")
							}
							continue
						}
						if !slices.Contains([]string{"int", "float", "str", "NoneType", "bool", "datetime", "timedelta", "bytes"}, parameter.Type) {
							t.Fatal("temporal parameter has no closed native type", parameter.Type)
						}
					}
					if record.Error == nil && (statement.Error != nil || statement.NativeError != nil) {
						t.Fatal("successful temporal observation contains native error")
					}
					if record.Error == nil && strings.HasPrefix(strings.TrimSpace(statement.SQL), "SELECT") && !strings.HasPrefix(record.Name, "update_") && statement.Rows == nil {
						t.Fatal("read observation lacks its raw native replay", record.Name)
					}
				}
				var actual any
				var failure error
				rollbackTemporal(t, backend, func(session db.Session) { actual, failure = observeTemporal(t, session, record.Name) })
				if physical.Snapshot() != before {
					t.Fatal("GoDj temporal observation changed native storage")
				}
				expected, fails, owned, err := durationScalingReference(vendor, record)
				if owned {
					check(t, err)
					if fails {
						nativeTemporalError(t, vendor, failure, "22012")
					} else {
						check(t, failure)
					}
					assertTemporalJSON(t, actual, expected)
					return
				}

				switch record.Name {
				case "datetime_upper_overflow", "datetime_lower_overflow":
					nativeTemporalError(t, vendor, failure, "22007")
					assertTemporalJSON(t, actual, "null")
					if vendor == "sqlite" {
						requireTemporalReferenceError(t, record, "sql", "DataError")
					} else {
						requireTemporalReferenceError(t, record, "conversion", "DataError")
					}
					return
				case "duration_full_model_literal", "duration_model_overflow":
					if vendor == "sqlite" {
						if !errors.Is(failure, duration.ErrMicrosecondRange) {
							t.Fatal("SQLite model literal lost its narrower native-range error", failure)
						}
						assertTemporalJSON(t, actual, "null")
						requireTemporalReferenceError(t, record, "sql", "OverflowError")
						return
					}
					if record.Name == "duration_model_overflow" {
						nativeTemporalError(t, vendor, failure, "22007")
						assertTemporalJSON(t, actual, "null")
						requireTemporalReferenceError(t, record, "conversion", "OverflowError", "DataError")
						return
					}
				case "duration_int64_overflow":
					if vendor == "sqlite" {
						nativeTemporalError(t, vendor, failure, "22007")
						assertTemporalJSON(t, actual, "null")
						if record.Error != nil {
							t.Fatal("SQLite native integer-to-REAL promotion was not observed", record.Error)
						}
						temporalNativeFloat(t, record, 1, "0x1.0000000000000p+63")
						assertTemporalJSON(t, record.Result, `[{"code":"a","value":"106751991 04:00:54.775808"}]`)
						return
					}
				case "mean_fractional_shift", "mean_integral_shift", "mean_reverse_shift":
					if vendor == "sqlite" {
						check(t, failure)
						if record.Error != nil {
							t.Fatal("SQLite mean shift unexpectedly failed", record.Error)
						}
						exact, final := "0x1.8000000000000p+0", "9999-01-01T00:00:00.000002Z"
						if record.Name == "mean_integral_shift" {
							exact, final = "0x1.0000000000000p+1", "9999-01-01T00:00:00.000003Z"
						}
						temporalNativeFloat(t, record, 1, exact)
						if len(record.Statements[0].Rows[0]) != 3 || record.Statements[0].Rows[0][2].Type != "NoneType" {
							t.Fatal("SQLite native mean did not produce the observed NULL shift")
						}
						assertTemporalJSON(t, record.Result, `[{"bucket":"first","period":"00:00:00.000002","value":null}]`)
						assertTemporalJSON(t, actual, `[{"bucket":"first","period":"00:00:00.000002","value":"`+final+`"}]`)
						return
					}
				case "mean_zero_shift":
					if vendor == "sqlite" {
						check(t, failure)
						requireTemporalReferenceError(t, record, "sql", "OperationalError")
						assertTemporalJSON(t, actual, `[{"bucket":"first","period":"00:00:00","value":"9999-01-01T00:00:00.000001Z"}]`)
						return
					}
				case "update_datetime_overflow":
					nativeTemporalError(t, vendor, failure, "22007")
					assertTemporalJSON(t, actual, "null")
					if vendor == "sqlite" {
						requireTemporalReferenceError(t, record, "sql", "DataError")
					} else {
						requireTemporalReferenceError(t, record, "conversion", "DataError")
						if len(record.Statements) != 2 || record.Statements[0].Rowcount != 6 || !strings.HasPrefix(record.Statements[0].SQL, "UPDATE") {
							t.Fatal("PostgreSQL range difference did not reach native UPDATE before conversion")
						}
					}
					return
				case "update_duration_overflow":
					if vendor == "sqlite" {
						nativeTemporalError(t, vendor, failure, "22007")
						assertTemporalJSON(t, actual, "null")
						if record.Error != nil {
							t.Fatal("SQLite native overflowing UPDATE was not observed", record.Error)
						}
						if len(record.NativeAfter[1]) != 6 || len(record.NativeAfter[1][5]) != 9 {
							t.Fatal("native overflowing UPDATE row inventory differs")
						}
						stored := record.NativeAfter[1][5][6]
						if stored.Type != "float" || stored.Binary64 != "0x1.0000000000000p+63" {
							t.Fatal("SQLite overflowing UPDATE did not retain its REAL storage class", stored.Type, stored.Binary64)
						}
						var converted struct {
							Count int64
							Rows  []struct{ Code, Span string }
						}
						check(t, json.Unmarshal(record.Result, &converted))
						if converted.Count != 6 || len(converted.Rows) != 6 || converted.Rows[5].Code != "f" || converted.Rows[5].Span != "106751991 04:00:54.775808" {
							t.Fatal("native UPDATE conversion differs", string(record.Result))
						}
						return
					}
				}
				check(t, failure)
				if record.Error != nil {
					t.Fatal("unowned temporal reference error", record.Name, record.Error)
				}
				assertTemporalJSON(t, actual, string(record.Result))
			})
		}
		if physical.Snapshot() != before {
			t.Fatal("temporal reference did not preserve all native tables")
		}
	})
}
