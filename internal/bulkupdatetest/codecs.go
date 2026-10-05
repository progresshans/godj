package bulkupdatetest

import (
	"database/sql"
	"testing"
	"time"

	"github.com/progresshans/godj/binaryvalue"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/uuid"
)

// CheckCodecs exercises CASE type inference with every persisted scalar,
// including two SQL NULLs. Raw database predicates use authored literals and
// never the product encoder/decoder to determine whether storage is correct.
func CheckCodecs(t *testing.T, backend Backend, database *sql.DB, quoteTable func(string) string, postgres bool) {
	t.Helper()
	number, err := decimal.Parse("1234.567")
	if err != nil {
		t.Fatal(err)
	}
	identifier, err := uuid.Parse("00112233-4455-6677-8899-aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}
	binary, err := binaryvalue.FromBytes([]byte{0, 97, 255})
	if err != nil {
		t.Fatal(err)
	}
	document, err := jsonvalue.Parse([]byte(`{"k":[1,2]}`))
	if err != nil {
		t.Fatal(err)
	}
	jsonNull, err := jsonvalue.Parse([]byte(`null`))
	if err != nil {
		t.Fatal(err)
	}
	date, err := calendar.Parse("2024-02-29")
	if err != nil {
		t.Fatal(err)
	}
	wall, err := clock.Parse("12:34:56.123456")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name                                     string
		kind                                     query.FieldKind
		value                                    query.Value
		sqliteType, pgType, sqliteMatch, pgMatch string
	}{
		{"integer", query.FieldInteger, query.Integer(9223372036854775807), "INTEGER", "BIGINT", "value=9223372036854775807", "value=9223372036854775807"},
		{"string", query.FieldString, query.String("literal ' <한글>"), "TEXT", "TEXT", "value='literal '' <한글>'", "value='literal '' <한글>'"},
		{"boolean", query.FieldBoolean, query.Boolean(true), "INTEGER", "BOOLEAN", "value=1", "value IS TRUE"},
		{"float", query.FieldFloat, query.Float(1.25), "REAL", "DOUBLE PRECISION", "value=1.25", "value=1.25"},
		{"decimal", query.FieldDecimal, query.Decimal(number), "BLOB", "NUMERIC(12,3)", "value=X'02800000033132333435363700'", "value=1234.567"},
		{"uuid", query.FieldUUID, query.UUID(identifier), "TEXT", "UUID", "value='00112233445566778899aabbccddeeff'", "value='00112233-4455-6677-8899-aabbccddeeff'"},
		{"binary", query.FieldBinary, query.Binary(binary), "BLOB", "BYTEA", "value=X'0061ff'", "value=decode('0061ff','hex')"},
		{"json", query.FieldJSON, query.JSON(document), "TEXT", "JSONB", `value='{"k":[1,2]}'`, `value='{"k":[1,2]}'::jsonb`},
		{"json_null", query.FieldJSON, query.JSON(jsonNull), "TEXT", "JSONB", `value='null'`, `value='null'::jsonb`},
		{"duration", query.FieldDuration, query.Duration(duration.FromMicroseconds(-1234567)), "INTEGER", "INTERVAL", "value=-1234567", "value=interval '-1.234567 seconds'"},
		{"date", query.FieldDate, query.Date(date), "TEXT", "DATE", "value='2024-02-29'", "value=date '2024-02-29'"},
		{"time", query.FieldTime, query.Time(wall), "TEXT", "TIME(6)", "value='12:34:56.123456'", "value=time '12:34:56.123456'"},
		{"datetime", query.FieldDateTime, query.DateTime(time.Date(2026, 10, 2, 12, 34, 56, 123456000, time.FixedZone("input", 9*3600))), "TEXT", "TIMESTAMPTZ(6)", "value='2026-10-02 03:34:56.123456'", "value=timestamptz '2026-10-02 03:34:56.123456+00'"},
	} {
		t.Run(test.name, func(t *testing.T) {
			name := "update_types_" + test.name
			table := quoteTable(name)
			kind, match := test.sqliteType, test.sqliteMatch
			if postgres {
				kind, match = test.pgType, test.pgMatch
			}
			for _, statement := range []string{"CREATE TABLE " + table + " (id BIGINT PRIMARY KEY,value " + kind + ")", "INSERT INTO " + table + " (id) VALUES (1),(2),(3)"} {
				if _, err := database.ExecContext(t.Context(), statement); err != nil {
					t.Fatal(err)
				}
			}
			field := query.NewFieldRef("value", "value", test.kind, true)
			if test.kind == query.FieldDecimal {
				field = query.NewDecimalFieldRef("value", "value", true, 12, 3)
			}
			source := query.NewPlan(name, []query.FieldRef{ID, field})
			for _, null := range []bool{false, true} {
				value, want := test.value, match
				if null {
					value, want = query.Null(), "value IS NULL"
				}
				count, err := backend.BulkUpdate(t.Context(), Plan(t, source, []query.FieldRef{field}, []int64{2, 1}, [][]query.Value{{value}, {value}}))
				if err != nil || count != 2 {
					t.Fatal(count, err)
				}
				var matched int
				if err := database.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table+" WHERE id IN (1,2) AND "+want).Scan(&matched); err != nil || matched != 2 {
					t.Fatal("native scalar storage", matched, err)
				}
				if err := database.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table+" WHERE id=3 AND value IS NULL").Scan(&matched); err != nil || matched != 1 {
					t.Fatal("nonselected key changed", matched, err)
				}
			}
		})
	}
}
