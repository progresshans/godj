package consumer

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/db"
)

// The native document is produced independently in this same CI owner. These
// explicit deviations preserve both observations rather than turning SQLite's
// binary/NUMERIC arithmetic or native target rounding into GoDj expectations.
func TestDecimalReference(t *testing.T) {
	withDecimalBackends(t, func(t *testing.T, backend decimalBackend, vendor string, physical decimalPhysical) {
		data, err := os.ReadFile("django61-" + vendor + ".json")
		check(t, err)
		var reference struct {
			Cases []struct {
				Name   string
				Result json.RawMessage
				SQL    []string
				Error  *struct{ Type, SQLState string }
			}
		}
		check(t, json.Unmarshal(data, &reference))
		seedDecimal(t, backend)
		before := physical.Snapshot()
		for _, record := range reference.Cases {
			t.Run(record.Name, func(t *testing.T) {
				if len(record.SQL) == 0 {
					t.Fatal("native decimal case has no SQL evidence")
				}
				var actual any
				var failure error
				rollback := errors.New("reference observation rollback")
				err := backend.Atomic(t.Context(), func(session db.Session) error {
					actual, failure = observeDecimal(t, session, record.Name)
					return rollback
				})
				if !errors.Is(err, rollback) {
					t.Fatal("reference transaction failed to roll back", err)
				}
				if physical.Snapshot() != before {
					t.Fatal("decimal reference altered native storage")
				}
				if record.Name == "update_rounding" || record.Name == "update_overflow" {
					nativeDecimalError(t, vendor, failure, "22P02")
					if actual != nil {
						t.Fatal("invalid Decimal target published a result", actual)
					}
					if record.Name == "update_rounding" {
						if record.Error != nil {
							t.Fatal("native rounding was not observed", record.Error)
						}
						assertDecimalJSON(t, json.RawMessage(record.Result), `{"count":6,"rows":[{"code":"a","amount":"18.75","balance":"100"},{"code":"b","amount":"-10.88","balance":"200"},{"code":"c","amount":null,"balance":"0"},{"code":"d","amount":"0","balance":"-0.5"},{"code":"e","amount":"18.75","balance":"1"},{"code":"f","amount":"150","balance":"3"}]}`)
					} else if record.Error == nil {
						t.Fatal("native overflow failure absent")
					}
					return
				}
				if vendor == "postgres" && (record.Name == "divide_zero" || record.Name == "remainder_zero") {
					nativeDecimalError(t, vendor, failure, "22012")
					if record.Error == nil || record.Error.Type != "DataError" || record.Error.SQLState != "22012" {
						t.Fatal("independent zero division cause differs")
					}
					return
				}
				check(t, failure)
				if record.Error != nil {
					t.Fatal("unowned native Decimal observation error", record.Error)
				}
				if vendor == "postgres" && record.Name == "divide_integer" {
					// Django/psycopg renders integral Decimal literals without a
					// NUMERIC cast: PostgreSQL infers integer / integer. GoDj keeps
					// the explicit Decimal kind regardless of its original spelling.
					assertDecimalJSON(t, json.RawMessage(record.Result), `[{"code":"a","value":"33"}]`)
					assertDecimalJSON(t, actual, `[{"code":"a","value":"33.3333333333333333"}]`)
					return
				}
				if vendor == "sqlite" {
					var expectedNative, expectedExact string
					switch record.Name {
					case "divide":
						expectedNative = `[{"code":"a","value":"4.16666666666667"},{"code":"b","value":"-2.41666666666667"},{"code":"c","value":null},{"code":"d","value":"0"},{"code":"e","value":"4.16666666666667"},{"code":"f","value":"33.3333333333333"}]`
						expectedExact = `[{"code":"a","value":"4.1666666666666667"},{"code":"b","value":"-2.4166666666666667"},{"code":"c","value":null},{"code":"d","value":"0"},{"code":"e","value":"4.1666666666666667"},{"code":"f","value":"33.3333333333333333"}]`
					case "divide_integer", "divide_decimal_spelling":
						expectedNative = `[{"code":"a","value":"33.3333333333333"}]`
						expectedExact = `[{"code":"a","value":"33.3333333333333333"}]`
					case "divide_native_scale":
						expectedNative = `[{"code":"a","value":"0.0` + strings.Repeat("3", 15) + `"}]`
						expectedExact = `[{"code":"a","value":"0.0` + strings.Repeat("3", 19) + `"}]`
					case "remainder":
						expectedNative = `[{"code":"a","value":null},{"code":"b","value":null},{"code":"c","value":null},{"code":"d","value":null},{"code":"e","value":null},{"code":"f","value":null}]`
						expectedExact = `[{"code":"a","value":"0.2"},{"code":"b","value":"-0.05"},{"code":"c","value":null},{"code":"d","value":"0"},{"code":"e","value":"0.2"},{"code":"f","value":"0.1"}]`
					case "large_literal":
						expectedNative = `[{"code":"a","value":"9007199254740990"}]`
						expectedExact = `[{"code":"a","value":"9007199254740993.1"}]`
					}
					if expectedNative != "" {
						assertDecimalJSON(t, json.RawMessage(record.Result), expectedNative)
						assertDecimalJSON(t, actual, expectedExact)
						return
					}
				}
				assertDecimalJSON(t, actual, string(record.Result))
			})
		}
		if !reflect.DeepEqual(before, physical.Snapshot()) {
			t.Fatal("decimal reference did not preserve all columns")
		}
	})
}
