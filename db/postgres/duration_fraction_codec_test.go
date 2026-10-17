package postgres

import (
	"bytes"
	"encoding/hex"
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/internal/durationcalc"
)

func TestPostgresDurationFractionCodec(t *testing.T) {
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
	backend := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
	encoded, err := postgresDurationFractionKeySQL("ARRAY[$1::numeric,$2::numeric]")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := postgresDurationFractionPairSQL("$1::bytea")
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := postgresDurationFractionKeySQL(decoded)
	if err != nil {
		t.Fatal(err)
	}
	readKey := func(t *testing.T, numerator, denominator string) []byte {
		t.Helper()
		var key []byte
		if err := backend.database.QueryRowContext(t.Context(), "SELECT "+encoded, numerator, denominator).Scan(&key); err != nil {
			t.Fatal("native fraction encoding failed", numerator, denominator, err)
		}
		return key
	}
	check := func(t *testing.T, numerator, denominator string, expected durationcalc.Value) {
		t.Helper()
		key := readKey(t, numerator, denominator)
		if !bytes.Equal(key, expected.Key()) {
			t.Fatalf("native fraction key differs for %s/%s: %x != %x", numerator, denominator, key, expected.Key())
		}
		value, err := durationcalc.FromKey(key)
		if err != nil || value.Compare(expected) != 0 {
			t.Fatal("native key lost its exact value", err)
		}
		var rebuilt []byte
		if err := backend.database.QueryRowContext(t.Context(), "SELECT "+roundTrip, key).Scan(&rebuilt); err != nil || !bytes.Equal(key, rebuilt) {
			t.Fatal("native decoder changed a canonical key", err)
		}
	}

	t.Run("canonical_values_and_full_range", func(t *testing.T) {
		for _, numerator := range []int64{math.MinInt64, -9007199254740993, -7, -1, 0, 1, 7, 9007199254740993, math.MaxInt64} {
			for _, denominator := range []int64{-7, 1, 2, 7, 4807526976, math.MaxInt64} {
				value, err := durationcalc.FromMicroseconds(numerator).Divide(durationcalc.IntegerFactor(denominator))
				if err != nil {
					t.Fatal(err)
				}
				check(t, strconv.FormatInt(numerator, 10), strconv.FormatInt(denominator, 10), value)
			}
		}
		for _, test := range []struct {
			numerator string
			period    duration.Duration
		}{
			{"86399999999999999999", duration.Duration{Days: duration.MaxDays, Microseconds: duration.MicrosecondsPerDay - 1}},
			{"-86399999913600000000", duration.Duration{Days: duration.MinDays}},
		} {
			value, err := durationcalc.FromDuration(test.period)
			if err != nil {
				t.Fatal(err)
			}
			check(t, test.numerator, "1", value)
		}
		factor, err := durationcalc.DecimalFactor(decimal.Decimal{Coefficient: "1", Exponent: 1000})
		if err != nil {
			t.Fatal(err)
		}
		tiny, err := durationcalc.FromMicroseconds(1).Divide(factor)
		if err != nil {
			t.Fatal(err)
		}
		check(t, "1", "1e1000", tiny)
		// A nonreduced pair must have the exact same canonical key.
		half, err := durationcalc.FromMicroseconds(1).Divide(durationcalc.IntegerFactor(2))
		if err != nil {
			t.Fatal(err)
		}
		check(t, "2", "4", half)
		// These independent byte fixtures fix the representation, including the
		// sign reversal and the even/odd end marker, rather than only roundtrip.
		for _, test := range []struct{ n, d, key string }{
			{"0", "1", "4744520101"},
			{"1", "1", "47445201028000000000000000013100"},
			{"-1", "1", "47445201007ffffffffffffffffeceff"},
			{"1", "2", "47445201028000000000000000007ffffffffffffffffecdff"},
		} {
			want, err := hex.DecodeString(test.key)
			if err != nil {
				t.Fatal(err)
			}
			if got := readKey(t, test.n, test.d); !bytes.Equal(got, want) {
				t.Fatalf("canonical byte fixture changed: %s/%s: %x != %x", test.n, test.d, got, want)
			}
		}
	})

	t.Run("native_numeric_order_and_distinct", func(t *testing.T) {
		keySQL, err := postgresDurationFractionKeySQL("ARRAY[input.n,input.d]")
		if err != nil {
			t.Fatal(err)
		}
		statement := "WITH inputs(id,n,d) AS (VALUES (1,1::numeric,2::numeric),(2,1,3),(3,1,6),(4,-1,2),(5,-1,3),(6,0,1),(7,2,4)) SELECT id FROM inputs AS input ORDER BY " + keySQL + ",id"
		rows, err := backend.database.QueryContext(t.Context(), statement)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var actual []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			actual = append(actual, id)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			t.Fatal(err)
		}
		want := []int64{4, 5, 6, 3, 2, 1, 7}
		if len(actual) != len(want) {
			t.Fatal("native fraction order changed row count", actual)
		}
		for i := range want {
			if actual[i] != want[i] {
				t.Fatal("native byte ordering differs from rational ordering", actual, want)
			}
		}
		statement = "WITH inputs(n,d) AS (VALUES (1::numeric,2::numeric),(2,4),(3,6),(-1,2),(0,1)) SELECT COUNT(DISTINCT " + keySQL + ") FROM inputs AS input"
		var count int64
		if err := backend.database.QueryRowContext(t.Context(), statement).Scan(&count); err != nil || count != 3 {
			t.Fatal("equivalent fractions did not share a native DISTINCT key", count, err)
		}
	})

	t.Run("null_and_invalid_values", func(t *testing.T) {
		for _, statement := range []string{"NULL::numeric[]", "ARRAY[1::numeric,0::numeric]", "ARRAY[1.5::numeric,2::numeric]", "ARRAY[1::numeric,NULL::numeric]", "ARRAY[1::numeric]", "ARRAY[1::numeric,2::numeric,3::numeric]", "ARRAY[ARRAY[1::numeric,2::numeric]]", "ARRAY['Infinity'::numeric,1::numeric]", "ARRAY[1::numeric,'NaN'::numeric]", "ARRAY[86400000000000000000::numeric,1::numeric]", "ARRAY[-86399999913600000001::numeric,1::numeric]"} {
			sql, err := postgresDurationFractionKeySQL(statement)
			if err != nil {
				t.Fatal(err)
			}
			var key []byte
			err = backend.database.QueryRowContext(t.Context(), "SELECT "+sql).Scan(&key)
			if statement == "NULL::numeric[]" {
				if err != nil || key != nil {
					t.Fatal("NULL acquired a fraction key", key, err)
				}
				continue
			}
			var native *pgconn.PgError
			if !errors.As(err, &native) || !strings.HasPrefix(native.Code, "22") {
				t.Fatal("invalid pair lost its native data exception", statement, err)
			}
		}
		valid := readKey(t, "3", "7")
		var invalid [][]byte
		for length := range len(valid) {
			invalid = append(invalid, bytes.Clone(valid[:length]))
		}
		invalid = append(invalid, append(bytes.Clone(valid), 0), []byte("3/7"), []byte{'G', 'D', 'R', 1, 3}, []byte{'G', 'D', 'R', 1, 1, 0})
		for _, key := range invalid {
			var n, d string
			err := backend.database.QueryRowContext(t.Context(), "SELECT value[1]::text,value[2]::text FROM (SELECT "+decoded+" AS value OFFSET 0) AS decoded", key).Scan(&n, &d)
			var native *pgconn.PgError
			if !errors.As(err, &native) || !strings.HasPrefix(native.Code, "22") {
				t.Fatal("malformed key became a numeric pair", hex.EncodeToString(key), err)
			}
		}
		// Failure must not poison a subsequent nontransactional operation.
		check(t, "1", "1", durationcalc.FromMicroseconds(1))
	})
}
