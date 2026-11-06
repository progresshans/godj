package postgres

import (
	"database/sql"
	"errors"
	"math"
	"math/big"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/progresshans/godj/query"
)

// math/big supplies an independent exact reference. These probes execute the
// actual PostgreSQL expressions; compiling their Go wrapper is not execution.
func TestPostgresDurationFractionNumeric(t *testing.T) {
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
	backend := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)

	t.Run("binary64_factors_preserve_actual_bits", func(t *testing.T) {
		pair, err := postgresNumberFractionSQL(query.FieldFloat, "$1::double precision")
		if err != nil {
			t.Fatal(err)
		}
		statement := "SELECT pg_catalog.trunc(value[1],0)::text,pg_catalog.trunc(value[2],0)::text FROM (SELECT " + pair + " AS value OFFSET 0) AS factor"
		for _, value := range []float64{0, math.Copysign(0, -1), 0.1, -0.1, 0.3, 1, -17.5, math.SmallestNonzeroFloat64, -math.SmallestNonzeroFloat64, math.Ldexp(1, -1022), math.MaxFloat64, -math.MaxFloat64, 9007199254740992} {
			var numerator, denominator string
			if err := backend.database.QueryRowContext(t.Context(), statement, value).Scan(&numerator, &denominator); err != nil {
				t.Fatal("native factor extraction failed", value, err)
			}
			n, validN := new(big.Int).SetString(numerator, 10)
			d, validD := new(big.Int).SetString(denominator, 10)
			if !validN || !validD || d.Sign() <= 0 || new(big.Rat).SetFrac(n, d).Cmp(new(big.Rat).SetFloat64(value)) != 0 {
				t.Fatalf("factor lost binary64 bits %016x: %s/%s", math.Float64bits(value), numerator, denominator)
			}
		}
		for _, value := range []float64{math.Inf(1), math.Inf(-1), math.NaN()} {
			var numerator, denominator string
			err := backend.database.QueryRowContext(t.Context(), statement, value).Scan(&numerator, &denominator)
			var native *pgconn.PgError
			if !errors.As(err, &native) || native.Code != "22P02" {
				t.Fatal("nonfinite factor lost its native invalid-input cause", value, err)
			}
		}
	})

	t.Run("correctly_rounded_ratio_boundaries", func(t *testing.T) {
		pair, err := postgresReducedFractionSQL("ARRAY[$1::numeric,$2::numeric]")
		if err != nil {
			t.Fatal(err)
		}
		statement, err := postgresFractionFloatSQL(pair)
		if err != nil {
			t.Fatal(err)
		}
		pow2 := func(exponent uint) *big.Int { return new(big.Int).Lsh(big.NewInt(1), exponent) }
		maximum := new(big.Rat).SetFloat64(math.MaxFloat64)
		cases := []*big.Rat{
			big.NewRat(0, 1), big.NewRat(1, 3), big.NewRat(-1, 3),
			big.NewRat(9007199254740995, 9007199254740993),
			new(big.Rat).SetFrac(big.NewInt(9007199254740993), pow2(53)),
			new(big.Rat).SetFrac(big.NewInt(9007199254740995), pow2(53)),
			new(big.Rat).SetFrac(big.NewInt(1), pow2(1074)),
			new(big.Rat).SetFrac(big.NewInt(1), pow2(1075)),
			new(big.Rat).SetFrac(big.NewInt(-1), pow2(1075)),
			new(big.Rat).SetFrac(big.NewInt(3), pow2(1075)),
			new(big.Rat).SetFrac(big.NewInt(5), pow2(1075)),
			new(big.Rat).SetFrac(big.NewInt(9007199254740991), pow2(1075)),
			new(big.Rat).SetFrac(big.NewInt(1), new(big.Int).Exp(big.NewInt(10), big.NewInt(1000), nil)),
			new(big.Rat).SetFrac(big.NewInt(-1), new(big.Int).Exp(big.NewInt(10), big.NewInt(1000), nil)),
			maximum,
			new(big.Rat).Add(maximum, new(big.Rat).SetInt(pow2(969))),
			new(big.Rat).Add(maximum, new(big.Rat).SetInt(pow2(970))),
			new(big.Rat).SetInt(pow2(1024)),
		}
		for _, value := range cases {
			want, _ := value.Float64()
			var actual float64
			err := backend.database.QueryRowContext(t.Context(), "SELECT "+statement, value.Num().String(), value.Denom().String()).Scan(&actual)
			if math.IsInf(want, 0) {
				var native *pgconn.PgError
				if !errors.As(err, &native) || native.Code != "22003" {
					t.Fatal("overflowing ratio did not retain native Float range failure", err)
				}
				continue
			}
			if err != nil || math.Float64bits(actual) != math.Float64bits(want) {
				t.Fatalf("ratio rounding changed for %s: got %016x want %016x: %v", value.RatString(), math.Float64bits(actual), math.Float64bits(want), err)
			}
		}
		statement, err = postgresFractionFloatSQL("NULL::numeric[]")
		if err != nil {
			t.Fatal(err)
		}
		var value sql.NullFloat64
		if err := backend.database.QueryRowContext(t.Context(), "SELECT "+statement).Scan(&value); err != nil || value.Valid {
			t.Fatal("NULL ratio acquired a Float", value, err)
		}
	})

	t.Run("half_even_and_full_interval_range", func(t *testing.T) {
		whole, err := postgresRoundFractionSQL("ARRAY[$1::numeric,$2::numeric]")
		if err != nil {
			t.Fatal(err)
		}
		interval, err := postgresMicrosecondsIntervalSQL(whole)
		if err != nil {
			t.Fatal(err)
		}
		for _, test := range []struct{ n, d, want string }{
			{"1", "2", "0"}, {"3", "2", "2"}, {"5", "2", "2"},
			{"-1", "2", "0"}, {"-3", "2", "-2"}, {"-5", "2", "-2"},
			{"18014398509481987", "2", "9007199254740994"},
			{"-18014398509481987", "2", "-9007199254740994"},
			{"86399999999999999999", "1", "86399999999999999999"},
			{"-86399999913600000000", "1", "-86399999913600000000"},
		} {
			var actual string
			statement := "SELECT pg_catalog.trunc(EXTRACT(EPOCH FROM " + interval + ")*1000000,0)::text"
			if err := backend.database.QueryRowContext(t.Context(), statement, test.n, test.d).Scan(&actual); err != nil || actual != test.want {
				t.Fatal("native final rounding or interval range changed", test, actual, err)
			}
		}
	})
}
